package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tgz builds a gzipped tar of the files, plus a directory entry that every
// reader must skip.
func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "dir/", Typeflag: tar.TypeDir, Mode: 0o755})
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func zipped(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	_, _ = zw.Create("dir/")
	for name, body := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// fakeSource serves assets from memory.
type fakeSource struct {
	assets map[string][]byte
	err    error
}

func (f fakeSource) Latest(context.Context) (Release, error) { return Release{}, f.err }
func (f fakeSource) Release(v string) Release                { return Release{Tag: "x/" + v, Version: v} }
func (f fakeSource) Download(_ context.Context, _ Release, asset string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	b, ok := f.assets[asset]
	if !ok {
		return nil, fmt.Errorf("404 %s", asset)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func release(asset string, archive []byte) fakeSource {
	return fakeSource{assets: map[string][]byte{
		asset:         archive,
		ChecksumsFile: []byte("0000  other.tar.gz\n" + sum(archive) + "  *" + asset + "\n"),
	}}
}

func plugins(name string) bool { return strings.HasPrefix(name, "app-") }

func TestAssetNameAndNewer(t *testing.T) {
	if got := AssetName("ds", "v1.2.3", "linux", "amd64"); got != "ds_v1.2.3_linux_amd64.tar.gz" {
		t.Fatal(got)
	}
	if got := AssetName("ds", "v1.2.3", "windows", "arm64"); got != "ds_v1.2.3_windows_arm64.zip" {
		t.Fatal(got)
	}
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v0.1.6", "v0.1.7", true},
		{"v0.1.7", "v0.1.7", false},
		{"v0.2.0", "v0.1.9", false},
		{"v0.1.9", "v0.1.10", true}, // numeric, not string, order
		{"(devel)", "v0.1.7", false},
		{"v0.1.7", "garbage", false},
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.cur, c.latest, got)
		}
	}
}

func TestInstallTarGz(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "app"), []byte("old"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "app-plugin"), []byte("old plugin"), 0o755)
	asset := AssetName("app", "v2.0.0", "linux", "amd64")
	src := release(asset, tgz(t, map[string]string{"app": "new", "app-plugin": "new plugin", "app-b": "b", "README.md": "readme"}))
	res, err := Install{Source: src, Release: src.Release("v2.0.0"), Asset: asset, Dir: dir, Main: "app", Want: plugins}.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Installed, ",") != "app,app-b,app-plugin" || res.Bytes == 0 {
		t.Fatalf("%+v", res)
	}
	for name, want := range map[string]string{"app": "new", "app-plugin": "new plugin", "app-b": "b"} {
		got, _ := os.ReadFile(filepath.Join(dir, name))
		if string(got) != want {
			t.Errorf("%s = %q", name, got)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
		t.Error("an unwanted entry was installed")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*"+newSuffix)); len(left) != 0 {
		t.Errorf("staged files left behind: %v", left)
	}
}

// Windows cannot overwrite a running program, so the old one is moved
// aside; a leftover from an earlier update is replaced.
func TestInstallZipWindows(t *testing.T) {
	defer func(was string) { goos = was }(goos)
	goos = goosWindows
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "app.exe"), []byte("old"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "app.exe"+oldSuffix), []byte("older"), 0o755)
	asset := AssetName("app", "v2.0.0", goosWindows, "amd64")
	src := release(asset, zipped(t, map[string]string{"app.exe": "new", "app-x.exe": "x"}))
	if _, err := (Install{Source: src, Release: src.Release("v2.0.0"), Asset: asset, Dir: dir, Main: "app.exe", Want: plugins}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "app.exe")); string(got) != "new" {
		t.Fatalf("app.exe = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "app.exe"+oldSuffix)); string(got) != "old" {
		t.Fatalf("old = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "app-x.exe")); string(got) != "x" {
		t.Fatalf("a plugin with no earlier copy = %q", got)
	}
}

func TestInstallRefusesBeforeTouchingAnything(t *testing.T) {
	asset := AssetName("app", "v2.0.0", "linux", "amd64")
	good := tgz(t, map[string]string{"app": "new"})
	cases := map[string]struct {
		src  Source
		main string
		want string
	}{
		"tampered archive": {fakeSource{assets: map[string][]byte{asset: good, ChecksumsFile: []byte(strings.Repeat("ab", 32) + "  " + asset + "\n")}}, "app", "checksum verification failed"},
		"no sum listed":    {fakeSource{assets: map[string][]byte{asset: good, ChecksumsFile: []byte("bad line\n")}}, "app", "lists no sum"},
		"no checksums":     {fakeSource{assets: map[string][]byte{asset: good}}, "app", "404 checksums.txt"},
		"no archive":       {fakeSource{assets: map[string][]byte{ChecksumsFile: []byte(sum(good) + "  " + asset)}}, "app", "404 " + asset},
		"main missing":     {release(asset, good), "other", "not in the release archive"},
		"not an archive":   {release(asset, []byte("plain")), "app", asset + ":"},
		"source down":      {fakeSource{err: errors.New("offline")}, "app", "offline"},
	}
	for name, c := range cases {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "app"), []byte("old"), 0o755)
		_, err := Install{Source: c.src, Asset: asset, Dir: dir, Main: c.main}.Run(context.Background())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
		if got, _ := os.ReadFile(filepath.Join(dir, "app")); string(got) != "old" {
			t.Errorf("%s: the old program was replaced", name)
		}
	}
}

func TestInstallFilesystemFailures(t *testing.T) {
	// The Unix replacement first; a real Windows host would move the
	// directory aside instead.
	defer func(was string) { goos = was }(goos)
	goos = "linux"
	asset := AssetName("app", "v2.0.0", "linux", "amd64")
	src := release(asset, tgz(t, map[string]string{"app": "new"}))
	// A directory that does not exist cannot take the staged file.
	if _, err := (Install{Source: src, Asset: asset, Dir: filepath.Join(t.TempDir(), "gone"), Main: "app"}).Run(context.Background()); err == nil {
		t.Fatal("staging into a missing directory succeeded")
	}
	// A directory where the program should be cannot be replaced by a file.
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "app", "sub"), 0o755)
	if _, err := (Install{Source: src, Asset: asset, Dir: dir, Main: "app"}).Run(context.Background()); err == nil {
		t.Fatal("replacing a non-empty directory succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "app"+newSuffix)); err == nil {
		t.Fatal("a failed install left its staged file")
	}
	// On Windows, a destination that cannot be moved aside stops the update.
	goos = goosWindows
	if err := replace(filepath.Join(dir, "nothing"), filepath.Join(dir, "missing-dir", "app")); err == nil {
		t.Fatal("replace into a missing directory succeeded")
	}
	locked := t.TempDir()
	_ = os.MkdirAll(filepath.Join(locked, "app"+oldSuffix, "keep"), 0o755)
	_ = os.WriteFile(filepath.Join(locked, "app"), []byte("old"), 0o755)
	if err := replace(filepath.Join(locked, "nothing"), filepath.Join(locked, "app")); err == nil {
		t.Fatal("moving aside onto a non-empty directory succeeded")
	}
}

func TestExtractCorrupt(t *testing.T) {
	keep := func(string) bool { return true }
	if _, err := extract("a.zip", []byte("not a zip"), keep); err == nil {
		t.Error("bad zip read")
	}
	// A tar.gz cut short after its first header.
	whole := tgz(t, map[string]string{"app": strings.Repeat("x", 4096)})
	var raw bytes.Buffer
	gz, _ := gzip.NewReader(bytes.NewReader(whole))
	_, _ = io.Copy(&raw, gz)
	var cut bytes.Buffer
	w := gzip.NewWriter(&cut)
	_, _ = w.Write(raw.Bytes()[:1024+512+100])
	_ = w.Close()
	if _, err := extract("a.tar.gz", cut.Bytes(), keep); err == nil {
		t.Error("truncated entry read")
	}
	var head bytes.Buffer
	w = gzip.NewWriter(&head)
	_, _ = w.Write([]byte(strings.Repeat("z", 600)))
	_ = w.Close()
	if _, err := extract("a.tar.gz", head.Bytes(), keep); err == nil {
		t.Error("garbage tar read")
	}
	// A zip whose entry is cut short, and one with an unknown compression.
	var stored bytes.Buffer
	zw := zip.NewWriter(&stored)
	sw, _ := zw.CreateHeader(&zip.FileHeader{Name: "app", Method: zip.Store})
	_, _ = sw.Write([]byte(strings.Repeat("y", 4096)))
	_ = zw.Close()
	bad := bytes.Replace(stored.Bytes(), []byte(strings.Repeat("y", 64)), []byte(strings.Repeat("q", 64)), 1)
	if _, err := extract("a.zip", bad, keep); err == nil {
		t.Error("corrupt zip entry read")
	}
	var unk bytes.Buffer
	zw = zip.NewWriter(&unk)
	zw.RegisterCompressor(99, func(w io.Writer) (io.WriteCloser, error) { return nopWriteCloser{w}, nil })
	fw, _ := zw.CreateHeader(&zip.FileHeader{Name: "app", Method: 99})
	_, _ = fw.Write([]byte("x"))
	_ = zw.Close()
	if _, err := extract("a.zip", unk.Bytes(), keep); err == nil {
		t.Error("unknown method read")
	}
}

func TestState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	if s := LoadState(path); !s.CheckedAt.IsZero() {
		t.Fatal("missing file is not zero")
	}
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	if !(State{}).Due(now, time.Hour) {
		t.Fatal("never checked is not due")
	}
	s := State{CheckedAt: now, Latest: "v1.0.0", Failed: "v0.9.0"}
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := LoadState(path); got != s {
		t.Fatalf("round trip: %+v", got)
	}
	if s.Due(now.Add(30*time.Minute), time.Hour) {
		t.Error("due within the interval")
	}
	if !s.Due(now.Add(time.Hour), time.Hour) || !s.Due(now.Add(-time.Minute), time.Hour) {
		t.Error("not due after the interval, or after the clock went back")
	}
	_ = os.WriteFile(path, []byte("{broken"), 0o644)
	if got := LoadState(path); got != (State{}) {
		t.Errorf("corrupt file: %+v", got)
	}
	// A parent that is a file, and a target that is a directory.
	file := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(file, nil, 0o644)
	if err := s.Save(filepath.Join(file, "state.json")); err == nil {
		t.Error("saved under a file")
	}
	d := t.TempDir()
	_ = os.MkdirAll(filepath.Join(d, "state.json"+newSuffix, "x"), 0o755)
	if err := s.Save(filepath.Join(d, "state.json")); err == nil {
		t.Error("saved over a directory")
	}
}

// fakeGitHub serves the three endpoints a GitHub source uses.
func fakeGitHub(t *testing.T, latestTag string, list string, assets map[string][]byte) (*httptest.Server, *[]string) {
	t.Helper()
	var auth []string
	mux := http.NewServeMux()
	mux.HandleFunc("/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if latestTag == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.Redirect(w, r, "/o/r/releases/tag/"+latestTag, http.StatusFound)
	})
	mux.HandleFunc("/api/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		if list == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(list))
	})
	mux.HandleFunc("/o/r/releases/download/", func(w http.ResponseWriter, r *http.Request) {
		b, ok := assets[strings.TrimPrefix(r.URL.Path, "/o/r/releases/download/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(b)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &auth
}

func gh(srv *httptest.Server) GitHub {
	return GitHub{Repo: "o/r", TagPrefix: "ds/", Web: srv.URL, API: srv.URL + "/api", Client: srv.Client()}
}

const releaseList = `[
 {"tag_name":"v9.0.0"},
 {"tag_name":"ds/v0.1.10"},
 {"tag_name":"ds/v0.2.0","draft":true},
 {"tag_name":"ds/v0.3.0","prerelease":true},
 {"tag_name":"ds/vbad"},
 {"tag_name":"ds/v0.1.9"}
]`

func TestGitHubLatest(t *testing.T) {
	ctx := context.Background()
	// The redirect names this stream: no API call is made.
	srv, auth := fakeGitHub(t, "ds/v0.1.7", releaseList, nil)
	rel, err := gh(srv).Latest(ctx)
	if err != nil || rel.Version != "v0.1.7" || rel.Tag != "ds/v0.1.7" || rel.URL != srv.URL+"/o/r/releases/tag/ds/v0.1.7" {
		t.Fatalf("%+v %v", rel, err)
	}
	if len(*auth) != 0 {
		t.Fatal("the rate-limited API was asked although the redirect answered")
	}
	// The redirect names another stream: the list is read, highest wins,
	// drafts, prereleases and other streams are skipped, the token is sent.
	srv, auth = fakeGitHub(t, "v9.0.0", releaseList, nil)
	g := gh(srv)
	g.Token = "tok"
	rel, err = g.Latest(ctx)
	if err != nil || rel.Version != "v0.1.10" {
		t.Fatalf("%+v %v", rel, err)
	}
	if strings.Join(*auth, ",") != "Bearer tok" {
		t.Fatalf("auth %v", *auth)
	}
	// No redirect, and a list with nothing in the stream.
	srv, _ = fakeGitHub(t, "", `[{"tag_name":"v1.0.0"}]`, nil)
	if _, err := gh(srv).Latest(ctx); !errors.Is(err, ErrNoRelease) {
		t.Fatal(err)
	}
	// Rate limited, and an unreadable answer.
	srv, _ = fakeGitHub(t, "", "", nil)
	if _, err := gh(srv).Latest(ctx); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatal(err)
	}
	srv, _ = fakeGitHub(t, "", "{not json", nil)
	if _, err := gh(srv).Latest(ctx); err == nil || !strings.Contains(err.Error(), "reading") {
		t.Fatal(err)
	}
	// A server that is not there.
	dead := GitHub{Repo: "o/r", Web: "http://127.0.0.1:1", API: "http://127.0.0.1:1"}
	if _, err := dead.Latest(ctx); err == nil {
		t.Fatal("unreachable server answered")
	}
	// An unusable URL fails before any request.
	bad := GitHub{Repo: "o/r", Web: "http://\x7f", API: "http://\x7f"}
	if _, err := bad.Latest(ctx); err == nil {
		t.Fatal("bad URL answered")
	}
	if _, err := bad.Download(ctx, bad.Release("v1.0.0"), "a"); err == nil {
		t.Fatal("bad URL downloaded")
	}
}

func TestGitHubDefaultsAndDownload(t *testing.T) {
	g := GitHub{Repo: "ubgo/docsync", TagPrefix: "ds/"}
	if g.client() != http.DefaultClient || g.web() != defaultWeb || g.api() != defaultAPI {
		t.Fatal("defaults")
	}
	if r := g.Release("v1.2.3"); r.URL != "https://github.com/ubgo/docsync/releases/tag/ds/v1.2.3" {
		t.Fatal(r.URL)
	}
	asset := AssetName("ds", "v0.1.7", "linux", "amd64")
	archive := tgz(t, map[string]string{"ds": "new"})
	srv, _ := fakeGitHub(t, "ds/v0.1.7", releaseList, map[string][]byte{
		"ds/v0.1.7/" + asset:         archive,
		"ds/v0.1.7/" + ChecksumsFile: []byte(sum(archive) + "  " + asset + "\n"),
	})
	src := gh(srv)
	dir := t.TempDir()
	if _, err := (Install{Source: src, Release: src.Release("v0.1.7"), Asset: asset, Dir: dir, Main: "ds"}).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "ds")); string(got) != "new" {
		t.Fatalf("%q", got)
	}
	if _, err := src.Download(context.Background(), src.Release("v9.9.9"), asset); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatal(err)
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
