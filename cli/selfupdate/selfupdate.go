// Package selfupdate replaces a running program with a newer release of
// itself: it finds the newest release of one binary stream, downloads that
// platform's archive, verifies it against the release's checksums.txt, and
// swaps the program and its companion executables in place.
//
// It is mechanism only. Which repository, which tag prefix, which files to
// install, when to check and whether to ask first are the caller's
// decisions, so any command-line program released the way docsync is
// (archives named <binary>_<version>_<os>_<arch>.tar.gz, .zip on Windows,
// beside a checksums.txt of sha256 sums) can use it. It imports no command
// framework and keeps no state of its own: the throttle file in State lives
// wherever the caller says.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// ChecksumsFile is the asset every release carries, one "<sha256>  <asset>"
// line per archive.
const ChecksumsFile = "checksums.txt"

// Archive extensions by platform: Windows has no tar in older releases and
// unzips natively, every other platform takes a gzipped tar.
const (
	extTarGz = ".tar.gz"
	extZip   = ".zip"
	// goosWindows is runtime.GOOS on Windows.
	goosWindows = "windows"
)

// oldSuffix marks the previous executable moved aside on Windows, where a
// running program cannot be overwritten but can be renamed.
const oldSuffix = ".old"

// newSuffix marks an extracted file waiting to be moved into place, beside
// its destination so the move is a rename within one directory.
const newSuffix = ".new"

// Errors.
var (
	// ErrNoRelease means the source has no published release in the stream.
	ErrNoRelease = errors.New("no release found")
	// ErrChecksum means the archive does not match the sum the release
	// published for it; nothing is installed.
	ErrChecksum = errors.New("checksum verification failed")
	// ErrNotInArchive means the archive lacks the program being updated.
	ErrNotInArchive = errors.New("not in the release archive")
)

// goos is runtime.GOOS; a variable so tests reach the Windows replacement
// on any host.
var goos = runtime.GOOS

// Release is one published release of a binary stream.
type Release struct {
	// Tag is the git tag, with the stream's prefix: "ds/v0.1.7".
	Tag string `json:"tag"`
	// Version is the tag without the prefix: "v0.1.7".
	Version string `json:"version"`
	// URL is the release's web page, where its notes are.
	URL string `json:"url"`
}

// Source finds and fetches releases. GitHub is the implementation; a
// program released elsewhere supplies its own.
type Source interface {
	// Latest is the newest published release of the stream.
	Latest(ctx context.Context) (Release, error)
	// Release names a version of the stream without contacting anything;
	// a version that was never released fails at Download.
	Release(version string) Release
	// Download opens one asset of a release.
	Download(ctx context.Context, rel Release, asset string) (io.ReadCloser, error)
}

// AssetName is the archive a release carries for one platform:
// <binary>_<version>_<os>_<arch>.tar.gz, or .zip on Windows.
func AssetName(binary, version, os, arch string) string {
	ext := extTarGz
	if os == goosWindows {
		ext = extZip
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", binary, version, os, arch, ext)
}

// Newer reports whether latest is a later version than current. Either
// being something other than a semantic version (a development build's
// "(devel)") reports false: nothing can be said to be newer than a build
// whose version is unknown.
func Newer(current, latest string) bool {
	return semver.IsValid(current) && semver.IsValid(latest) && semver.Compare(latest, current) > 0
}

// Install is one update: which release, which archive, where to.
type Install struct {
	Source  Source
	Release Release
	// Asset is the archive to fetch, usually AssetName(...).
	Asset string
	// Dir is the directory the files are installed in, normally the one
	// holding the running program.
	Dir string
	// Main is the program's file name inside the archive. It must be there.
	Main string
	// Want reports which other archive entries to install beside Main (the
	// program's plugins). Nil installs Main alone.
	Want func(name string) bool
}

// Result is what an Install did.
type Result struct {
	// Bytes is the archive's size.
	Bytes int64 `json:"bytes"`
	// Installed names the files written into Dir, Main first.
	Installed []string `json:"installed"`
}

// Run downloads the archive and checksums, verifies the archive, and moves
// every wanted file into Dir. Nothing in Dir changes until the archive has
// verified and every file has been extracted beside its destination.
func (in Install) Run(ctx context.Context) (Result, error) {
	want, err := in.checksum(ctx)
	if err != nil {
		return Result{}, err
	}
	data, err := in.fetch(ctx, in.Asset)
	if err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return Result{}, fmt.Errorf("%w for %s: published %s, downloaded %s", ErrChecksum, in.Asset, want, got)
	}
	files, err := extract(in.Asset, data, in.wanted)
	if err != nil {
		return Result{}, err
	}
	if _, ok := files[in.Main]; !ok {
		return Result{}, fmt.Errorf("%s: %w %s", in.Main, ErrNotInArchive, in.Asset)
	}
	names := []string{in.Main}
	for name := range files {
		if name != in.Main {
			names = append(names, name)
		}
	}
	sort.Strings(names[1:])
	staged := make([]string, 0, len(names))
	defer func() {
		for _, s := range staged {
			_ = os.Remove(s)
		}
	}()
	for _, name := range names {
		s := filepath.Join(in.Dir, name+newSuffix)
		if err := os.WriteFile(s, files[name], 0o755); err != nil {
			return Result{}, err
		}
		staged = append(staged, s)
	}
	// Plugins first and the program last, so an interrupted update leaves
	// the old program in place rather than a new one with old plugins.
	for i := len(names) - 1; i >= 0; i-- {
		if err := replace(staged[i], filepath.Join(in.Dir, names[i])); err != nil {
			return Result{}, err
		}
	}
	return Result{Bytes: int64(len(data)), Installed: names}, nil
}

// wanted reports whether an archive entry is installed.
func (in Install) wanted(name string) bool {
	return name == in.Main || (in.Want != nil && in.Want(name))
}

// checksum is the sum the release publishes for the archive.
func (in Install) checksum(ctx context.Context) (string, error) {
	data, err := in.fetch(ctx, ChecksumsFile)
	if err != nil {
		return "", err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == in.Asset {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%w: %s lists no sum for %s", ErrChecksum, ChecksumsFile, in.Asset)
}

// fetch reads one asset whole.
func (in Install) fetch(ctx context.Context, asset string) ([]byte, error) {
	rc, err := in.Source.Download(ctx, in.Release, asset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// replace moves src onto dst. A rename within one directory is atomic on
// every platform docsync ships for; Windows refuses to replace a running
// executable, so the old one is renamed aside first and removed by the
// next update.
func replace(src, dst string) error {
	if goos == goosWindows {
		old := dst + oldSuffix
		_ = os.Remove(old)
		if err := os.Rename(dst, old); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return os.Rename(src, dst)
}

// extract returns the contents of the archive entries keep accepts, by base
// name. Directories inside the archive are ignored; release archives are
// flat.
func extract(asset string, data []byte, keep func(string) bool) (map[string][]byte, error) {
	out := map[string][]byte{}
	if strings.HasSuffix(asset, extZip) {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", asset, err)
		}
		for _, f := range zr.File {
			name := filepath.Base(f.Name)
			if f.FileInfo().IsDir() || !keep(name) {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", asset, err)
			}
			b, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", asset, err)
			}
			out[name] = b
		}
		return out, nil
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", asset, err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", asset, err)
		}
		name := filepath.Base(h.Name)
		if h.Typeflag != tar.TypeReg || !keep(name) {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", asset, err)
		}
		out[name] = b
	}
}

// State is what a caller remembers between runs to check at most once per
// interval: when it last asked, what it was told, and which version it
// already failed to install (so a read-only install directory is not
// re-downloaded on every run).
type State struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest,omitempty"`
	Failed    string    `json:"failed,omitempty"`
}

// LoadState reads a state file. A missing or unreadable file is the zero
// State, which is due at once: the worst a lost file costs is one check.
func LoadState(path string) State {
	var s State
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}
	}
	if json.Unmarshal(data, &s) != nil {
		return State{}
	}
	return s
}

// Save writes the state file, creating its directory, through a rename so
// two programs saving at once leave one whole file.
func (s State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, _ := json.Marshal(s)
	tmp := path + newSuffix
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Due reports whether a check is owed: never checked, checked longer than
// every ago, or a clock that has gone backwards past the last check.
func (s State) Due(now time.Time, every time.Duration) bool {
	return s.CheckedAt.IsZero() || now.Sub(s.CheckedAt) >= every || now.Before(s.CheckedAt)
}

// GitHub is a Source for one tag-prefixed stream of a GitHub repository's
// releases.
type GitHub struct {
	// Repo is "owner/name".
	Repo string
	// TagPrefix selects the stream in a repository that releases several
	// things: "ds/" for tags "ds/v0.1.7". Empty for plain "v0.1.7" tags.
	TagPrefix string
	// Token, when set, authenticates API calls, which otherwise share a
	// limit of 60 an hour per address.
	Token string
	// Client is the HTTP client; nil is http.DefaultClient.
	Client *http.Client
	// Web and API are the server roots; empty means github.com.
	Web, API string
}

// GitHub's public roots.
const (
	defaultWeb = "https://github.com"
	defaultAPI = "https://api.github.com"
)

// apiPageSize is the most releases one API page returns.
const apiPageSize = 100

func (g GitHub) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return http.DefaultClient
}

func (g GitHub) web() string { return or(g.Web, defaultWeb) }
func (g GitHub) api() string { return or(g.API, defaultAPI) }

func or(s, def string) string {
	if s != "" {
		return s
	}
	return def
}

// Release names a version of the stream.
func (g GitHub) Release(version string) Release {
	tag := g.TagPrefix + version
	return Release{Tag: tag, Version: version, URL: g.web() + "/" + g.Repo + "/releases/tag/" + tag}
}

// Latest asks the /releases/latest redirect first: it has no rate limit and
// names only a published, non-prerelease release. In a repository with
// several streams it names whichever was marked latest, so when that is not
// this stream the release list is read instead, which is rate limited.
func (g GitHub) Latest(ctx context.Context) (Release, error) {
	if tag, err := g.latestRedirect(ctx); err == nil && strings.HasPrefix(tag, g.TagPrefix) && semver.IsValid(strings.TrimPrefix(tag, g.TagPrefix)) {
		return g.Release(strings.TrimPrefix(tag, g.TagPrefix)), nil
	}
	return g.latestListed(ctx)
}

// latestRedirect is the tag /releases/latest redirects to.
func (g GitHub) latestRedirect(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, g.web()+"/"+g.Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	c := *g.client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	_ = resp.Body.Close()
	const marker = "/releases/tag/"
	loc := resp.Header.Get("Location")
	i := strings.Index(loc, marker)
	if i < 0 {
		return "", fmt.Errorf("%w: /releases/latest answered %s", ErrNoRelease, resp.Status)
	}
	return loc[i+len(marker):], nil
}

// latestListed is the highest version among the stream's published,
// non-prerelease releases. Highest, not newest: a patch to an older line
// can be published after a newer line.
func (g GitHub) latestListed(ctx context.Context) (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", g.api(), g.Repo, apiPageSize)
	resp, err := g.get(ctx, url)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var list []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return Release{}, fmt.Errorf("reading %s: %w", url, err)
	}
	best := ""
	for _, r := range list {
		v, ok := strings.CutPrefix(r.Tag, g.TagPrefix)
		if !ok || r.Draft || r.Prerelease || !semver.IsValid(v) {
			continue
		}
		if best == "" || semver.Compare(v, best) > 0 {
			best = v
		}
	}
	if best == "" {
		return Release{}, fmt.Errorf("%w in %s with tag prefix %q", ErrNoRelease, g.Repo, g.TagPrefix)
	}
	return g.Release(best), nil
}

// Download opens an asset of a release.
func (g GitHub) Download(ctx context.Context, rel Release, asset string) (io.ReadCloser, error) {
	resp, err := g.get(ctx, g.web()+"/"+g.Repo+"/releases/download/"+rel.Tag+"/"+asset)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// get is a GET that fails on any status but 200.
func (g GitHub) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if g.Token != "" && strings.HasPrefix(url, g.api()) {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}
