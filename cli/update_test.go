package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/cli/selfupdate"
	"github.com/ubgo/docsync/config"
)

// fakeReleases serves one release stream from memory: every version in
// archives can be downloaded, latest is what Latest answers.
type fakeReleases struct {
	latest   string
	err      error
	archives map[string][]byte
	asked    *int
}

func (f fakeReleases) Latest(context.Context) (selfupdate.Release, error) {
	if f.asked != nil {
		*f.asked++
	}
	if f.err != nil {
		return selfupdate.Release{}, f.err
	}
	return f.Release(f.latest), nil
}

func (f fakeReleases) Release(v string) selfupdate.Release {
	return selfupdate.Release{Tag: "ds/" + v, Version: v, URL: "https://example.test/releases/tag/ds/" + v}
}

func (f fakeReleases) Download(_ context.Context, rel selfupdate.Release, asset string) (io.ReadCloser, error) {
	archive, ok := f.archives[rel.Version]
	if !ok {
		return nil, fmt.Errorf("404 %s", rel.Tag)
	}
	if asset == selfupdate.ChecksumsFile {
		s := sha256.Sum256(archive)
		return io.NopCloser(strings.NewReader(hex.EncodeToString(s[:]) + "  " + selfupdate.AssetName("ds", rel.Version, "linux", "amd64") + "\n")), nil
	}
	return io.NopCloser(bytes.NewReader(archive)), nil
}

// dsArchive is a release archive holding ds, one plugin and a README.
func dsArchive(t *testing.T, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"ds": "ds " + version, pluginPrefix + "env": "plugin " + version, "README.md": "readme"} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// updateRig is an installed ds release in a temporary bin directory, with
// every outside effect recorded.
type updateRig struct {
	bin, state string
	env        map[string]string
	terminal   bool
	reran      [][]string
	rerunCode  int
	rerunErr   error
	asked      int
	src        fakeReleases
	u          updater
}

func newRig(t *testing.T, running, latest string) *updateRig {
	t.Helper()
	bin, _ := filepath.EvalSymlinks(t.TempDir())
	r := &updateRig{bin: bin, state: filepath.Join(t.TempDir(), "update.json"), env: map[string]string{envNoColor: "1"}, terminal: true}
	write(t, r.bin, "ds", "ds "+running)
	write(t, r.bin, pluginPrefix+"env", "plugin "+running)
	r.src = fakeReleases{latest: latest, archives: map[string][]byte{latest: dsArchive(t, latest), "v0.1.5": dsArchive(t, "v0.1.5")}, asked: &r.asked}
	r.u = updater{
		source:      r.src,
		release:     running,
		build:       running,
		executable:  func() (string, error) { return filepath.Join(r.bin, "ds"), nil },
		statePath:   func() (string, error) { return r.state, nil },
		getenv:      func(k string) string { return r.env[k] },
		interactive: func() bool { return r.terminal },
		reexec: func(_ string, args []string) (int, error) {
			r.reran = append(r.reran, args)
			return r.rerunCode, r.rerunErr
		},
		goos: "linux", goarch: "amd64",
		now: func() time.Time { return clock },
	}
	return r
}

func (r *updateRig) run(t *testing.T, dir string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, WithDir(dir), WithIO(strings.NewReader(""), &out, &errb), WithVCS(fakeVCS{}), WithClock(func() time.Time { return clock }), func(a *App) { a.upd = r.u })
	return result{code, out.String(), errb.String()}
}

func (r *updateRig) installed(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(r.bin, "ds"))
	return string(b)
}

// An outdated release build at a terminal installs the newer release first
// and re-runs the command on it, passing its exit code through.
func TestAutoUpdateInstallsAndReruns(t *testing.T) {
	r := newRig(t, "v0.1.6", "v0.1.7")
	r.rerunCode = 3
	dir := t.TempDir()
	res := r.run(t, dir, "status", "--dir", ".")
	if res.code != 3 || len(r.reran) != 1 || strings.Join(r.reran[0], " ") != "status --dir ." {
		t.Fatalf("%+v reran %v", res, r.reran)
	}
	if r.installed(t) != "ds v0.1.7" {
		t.Fatalf("installed %q", r.installed(t))
	}
	for _, want := range []string{"Updating ds v0.1.6 → v0.1.7 before running status", "DS_UPDATE=off", "Updated to v0.1.7", "what changed: https://example.test/releases/tag/ds/v0.1.7"} {
		if !strings.Contains(res.err, want) {
			t.Errorf("stderr lacks %q:\n%s", want, res.err)
		}
	}
	if st := selfupdate.LoadState(r.state); st.Latest != "v0.1.7" || !st.CheckedAt.Equal(clock) {
		t.Errorf("state %+v", st)
	}
	// The rerun's own failure to start is the command's error.
	r = newRig(t, "v0.1.6", "v0.1.7")
	r.rerunErr = errors.New("exec format error")
	if res := r.run(t, dir, "status"); res.code != ExitError || !strings.Contains(res.err, "exec format error") {
		t.Fatalf("%+v", res)
	}
}

// The check is made once a day: a second run within the day asks nothing,
// and an offline check is not retried until the day is out.
func TestAutoUpdateChecksOnceADay(t *testing.T) {
	r := newRig(t, "v0.1.7", "v0.1.7")
	dir := t.TempDir()
	r.run(t, dir, "status")
	r.run(t, dir, "status")
	if r.asked != 1 {
		t.Fatalf("asked %d times", r.asked)
	}
	r = newRig(t, "v0.1.6", "v0.1.7")
	r.src.err = errors.New("offline")
	r.u.source = r.src
	if res := r.run(t, dir, "status"); strings.Contains(res.err, "Updating") || strings.Contains(res.err, "offline") {
		t.Fatalf("an offline check was visible: %+v", res)
	}
	if st := selfupdate.LoadState(r.state); !st.CheckedAt.Equal(clock) || st.Latest != "" {
		t.Fatalf("offline check not recorded: %+v", st)
	}
	r.run(t, dir, "status")
	if r.asked != 1 || r.installed(t) != "ds v0.1.6" {
		t.Fatalf("asked %d, installed %q", r.asked, r.installed(t))
	}
}

// Every way of not updating: the rig would otherwise install.
// promise:update-stays-out
func TestAutoUpdateStaysOut(t *testing.T) {
	cases := map[string]func(r *updateRig) []string{
		"not a release build": func(r *updateRig) []string { r.u.release = ""; return []string{"status"} },
		"update itself":       func(*updateRig) []string { return []string{"update", "--check"} },
		"version":             func(*updateRig) []string { return []string{"version"} },
		"doctor":              func(*updateRig) []string { return []string{"doctor"} },
		"--no-update":         func(*updateRig) []string { return []string{"status", "--no-update"} },
		"--json":              func(*updateRig) []string { return []string{"status", "--json"} },
		"CI":                  func(r *updateRig) []string { r.env[ciEnv] = "true"; return []string{"status"} },
		"not a terminal":      func(r *updateRig) []string { r.terminal = false; return []string{"status"} },
		"DS_UPDATE=off":       func(r *updateRig) []string { r.env[envUpdate] = config.UpdateOff; return []string{"status"} },
		"no cache directory": func(r *updateRig) []string {
			r.u.statePath = func() (string, error) { return "", errors.New("no home") }
			return []string{"status"}
		},
	}
	for name, setup := range cases {
		r := newRig(t, "v0.1.6", "v0.1.7")
		args := setup(r)
		res := r.run(t, t.TempDir(), args...)
		if r.installed(t) != "ds v0.1.6" || len(r.reran) != 0 || strings.Contains(res.err, "Updating") {
			t.Errorf("%s: updated: %+v", name, res)
		}
	}
}

// A repository can turn updates down for everyone working in it, never up.
func TestAutoUpdateRepositoryMode(t *testing.T) {
	dir, v := initialised(t)
	cfg := filepath.Join(dir, DirName, ConfigFile)
	base, _ := os.ReadFile(cfg)
	setMode := func(mode string) {
		_ = os.WriteFile(cfg, append(append([]byte{}, base...), []byte("\n[update]\nmode = \""+mode+"\"\n")...), 0o644)
	}
	_ = v
	setMode(config.UpdateOff)
	r := newRig(t, "v0.1.6", "v0.1.7")
	if res := r.run(t, dir, "status"); r.asked != 0 || strings.Contains(res.err, "Updating") {
		t.Fatalf("repo off: %+v", res)
	}
	setMode(config.UpdateNotify)
	r = newRig(t, "v0.1.6", "v0.1.7")
	res := r.run(t, dir, "status")
	if r.installed(t) != "ds v0.1.6" || !strings.Contains(res.err, "ds v0.1.7 is available (you have v0.1.6): run `ds update`") {
		t.Fatalf("repo notify: %+v", res)
	}
	// The notice is once a day, with the check, not on every command.
	if res := r.run(t, dir, "status"); strings.Contains(res.err, "available") {
		t.Fatalf("notice repeated: %+v", res)
	}
	// A repository asking for auto cannot override a user's notify.
	setMode(config.UpdateAuto)
	r = newRig(t, "v0.1.6", "v0.1.7")
	r.env[envUpdate] = config.UpdateNotify
	if r.run(t, dir, "status"); r.installed(t) != "ds v0.1.6" {
		t.Fatal("repo auto overrode DS_UPDATE=notify")
	}
	// A config that does not load is the command's to report, not the
	// updater's: it falls back to the user's mode.
	_ = os.WriteFile(cfg, []byte("[update]\nmode = \"sometimes\"\n"), 0o644)
	r = newRig(t, "v0.1.6", "v0.1.7")
	if res := r.run(t, dir, "status"); r.installed(t) != "ds v0.1.7" || res.code != 0 {
		t.Fatalf("broken config: %+v installed %q", res, r.installed(t))
	}
}

// An unknown DS_UPDATE is said out loud and read as notify, so a typo never
// means "install".
func TestAutoUpdateUnknownMode(t *testing.T) {
	r := newRig(t, "v0.1.6", "v0.1.7")
	r.env[envUpdate] = "sometimes"
	res := r.run(t, t.TempDir(), "status")
	if r.installed(t) != "ds v0.1.6" || !strings.Contains(res.err, "DS_UPDATE=sometimes is not one of auto, notify, off; treating it as notify") || !strings.Contains(res.err, "is available") {
		t.Fatalf("%+v", res)
	}
}

// A failed automatic update is one warning, the command runs on the old
// version, and that version is not downloaded again on every run.
func TestAutoUpdateFailureDoesNotBlock(t *testing.T) {
	r := newRig(t, "v0.1.6", "v0.1.7")
	r.src.archives = map[string][]byte{}
	r.u.source = r.src
	dir := t.TempDir()
	res := r.run(t, dir, "status")
	if len(r.reran) != 0 || !strings.Contains(res.err, "automatic update failed: 404 ds/v0.1.7") || !strings.Contains(res.err, "running status on v0.1.6; `ds update` retries") {
		t.Fatalf("%+v", res)
	}
	if st := selfupdate.LoadState(r.state); st.Failed != "v0.1.7" {
		t.Fatalf("state %+v", st)
	}
	if res := r.run(t, dir, "status"); strings.Contains(res.err, "Updating") || strings.Contains(res.err, "available") {
		t.Fatalf("retried within the day: %+v", res)
	}
	// The next day's check says the release exists rather than retrying.
	st := selfupdate.LoadState(r.state)
	st.CheckedAt = clock.Add(-2 * updateEvery)
	_ = st.Save(r.state)
	if res := r.run(t, dir, "status"); !strings.Contains(res.err, "is available") || strings.Contains(res.err, "Updating") {
		t.Fatalf("next day: %+v", res)
	}
}

func TestUpdateCommand(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, "v0.1.6", "v0.1.7")
	res := r.run(t, dir, "update")
	for _, want := range []string{"Current version: v0.1.6", "Checking for updates to latest version...", "Updating ds v0.1.6 → v0.1.7", "Downloaded and verified ds_v0.1.7_linux_amd64.tar.gz", "Installed ds and 1 plugin in " + r.bin, "ds is now v0.1.7", "what changed: https://example.test/releases/tag/ds/v0.1.7"} {
		if !strings.Contains(res.out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, res.out)
		}
	}
	if res.code != 0 || r.installed(t) != "ds v0.1.7" || strings.Contains(res.out, "\x1b[") {
		t.Fatalf("%+v installed %q", res, r.installed(t))
	}
	if b, _ := os.ReadFile(filepath.Join(r.bin, pluginPrefix+"env")); string(b) != "plugin v0.1.7" {
		t.Fatalf("plugin %q", b)
	}
	if _, err := os.Stat(filepath.Join(r.bin, "README.md")); err == nil {
		t.Fatal("README installed into the bin directory")
	}

	// Up to date, in words and as JSON.
	r = newRig(t, "v0.1.7", "v0.1.7")
	if res := r.run(t, dir, "update"); res.code != 0 || !strings.Contains(res.out, "✓ ds is up to date (v0.1.7)") {
		t.Fatalf("%+v", res)
	}
	res = r.run(t, dir, "update", "--json")
	var rep UpdateReport
	if err := json.Unmarshal([]byte(res.out), &rep); err != nil || rep.Current != "v0.1.7" || rep.Latest != "v0.1.7" || rep.Available || rep.Updated {
		t.Fatalf("%+v %v", res, err)
	}

	// --check reports and changes nothing.
	r = newRig(t, "v0.1.6", "v0.1.7")
	res = r.run(t, dir, "update", "--check")
	if res.code != 0 || !strings.Contains(res.out, "↑ v0.1.7 is available: run `ds update`") || r.installed(t) != "ds v0.1.6" {
		t.Fatalf("%+v", res)
	}
	res = r.run(t, dir, "update", "--check", "--json")
	if err := json.Unmarshal([]byte(res.out), &rep); err != nil || !rep.Available || rep.Updated || strings.Contains(res.out, "Checking") {
		t.Fatalf("%+v", res)
	}
	if res := r.run(t, dir, "update", "--dry-run"); !strings.Contains(res.out, "↑ v0.1.7 is available") || r.installed(t) != "ds v0.1.6" {
		t.Fatalf("--dry-run: %+v", res)
	}
	r = newRig(t, "v0.1.7", "v0.1.7")
	if res := r.run(t, dir, "update", "--check"); !strings.Contains(res.out, "is up to date") {
		t.Fatalf("%+v", res)
	}

	// --json on an update lists the files written.
	r = newRig(t, "v0.1.6", "v0.1.7")
	res = r.run(t, dir, "update", "--json")
	if err := json.Unmarshal([]byte(res.out), &rep); err != nil || !rep.Updated || len(rep.Installed) != 2 || rep.Installed[0] != filepath.Join(r.bin, "ds") {
		t.Fatalf("%+v %v", res, err)
	}
	if st := selfupdate.LoadState(r.state); st.Latest != "v0.1.7" || st.Failed != "" {
		t.Fatalf("state %+v", st)
	}

	// A build newer than the newest release is left alone.
	r = newRig(t, "v0.1.8", "v0.1.7")
	if res := r.run(t, dir, "update"); !strings.Contains(res.out, "ds v0.1.8 is newer than the latest release (v0.1.7); nothing to do") || r.installed(t) != "ds v0.1.8" {
		t.Fatalf("%+v", res)
	}
}

func TestUpdateVersionAndForce(t *testing.T) {
	dir := t.TempDir()
	// Back to an older release by name; the state's daily check is untouched.
	r := newRig(t, "v0.1.6", "v0.1.7")
	res := r.run(t, dir, "update", "--version", "v0.1.5")
	if res.code != 0 || r.installed(t) != "ds v0.1.5" || strings.Contains(res.out, "Checking") || !strings.Contains(res.out, "Updating ds v0.1.6 → v0.1.5") {
		t.Fatalf("%+v", res)
	}
	if st := selfupdate.LoadState(r.state); !st.CheckedAt.IsZero() {
		t.Fatalf("a named version counted as a check: %+v", st)
	}
	if res := r.run(t, dir, "update", "--version", "latest"); res.code != ExitError || !strings.Contains(res.err, "is not a version like v0.1.6") {
		t.Fatalf("%+v", res)
	}
	if res := r.run(t, dir, "update", "--version", "v9.9.9"); res.code != ExitError || !strings.Contains(res.err, "404 ds/v9.9.9") {
		t.Fatalf("%+v", res)
	}
	// The same version is a no-op unless forced.
	r = newRig(t, "v0.1.7", "v0.1.7")
	write(t, r.bin, "ds", "damaged")
	if res := r.run(t, dir, "update", "--version", "v0.1.7"); !strings.Contains(res.out, "up to date") || r.installed(t) != "damaged" {
		t.Fatalf("%+v", res)
	}
	if res := r.run(t, dir, "update", "--force"); res.code != 0 || r.installed(t) != "ds v0.1.7" {
		t.Fatalf("%+v", res)
	}
}

// Builds that are not release archives say how they are updated instead.
func TestUpdateRefusesOtherBuilds(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, "", "v0.1.7")
	r.u.build = "v0.1.7-0.20261004120000-0264590abcde+dirty"
	res := r.run(t, dir, "update")
	if res.code != ExitError || !strings.Contains(res.err, "this is a development build (v0.1.7-0.20261004120000-0264590abcde+dirty), made from a checkout; rebuild it there, or pass --force") {
		t.Fatalf("%+v", res)
	}
	// --check still answers, and --force replaces it.
	if res := r.run(t, dir, "update", "--check"); res.code != 0 || !strings.Contains(res.out, "Current version: v0.1.7-0.2026") {
		t.Fatalf("%+v", res)
	}
	if res := r.run(t, dir, "update", "--force"); res.code != 0 || r.installed(t) != "ds v0.1.7" {
		t.Fatalf("%+v", res)
	}
	r.u.build = "v0.1.6"
	if res := r.run(t, dir, "update"); !strings.Contains(res.err, "installed with `go install`; update it the same way: go install github.com/ubgo/docsync/cli/cmd/ds@latest") {
		t.Fatalf("%+v", res)
	}
}

func TestUpdateFailures(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, "v0.1.6", "v0.1.7")
	r.u.source = fakeReleases{err: errors.New("offline")}
	if res := r.run(t, dir, "update"); res.code != ExitError || !strings.Contains(res.err, "could not find the latest release: offline") {
		t.Fatalf("%+v", res)
	}
	// No cache directory: the update still works, nothing is remembered.
	r = newRig(t, "v0.1.6", "v0.1.7")
	r.u.statePath = func() (string, error) { return "", errors.New("no home") }
	if res := r.run(t, dir, "update"); res.code != 0 || r.installed(t) != "ds v0.1.7" {
		t.Fatalf("%+v", res)
	}
	// The running program cannot be found, or is not called ds.
	r = newRig(t, "v0.1.6", "v0.1.7")
	r.u.executable = func() (string, error) { return "", errors.New("no /proc") }
	if res := r.run(t, dir, "update"); !strings.Contains(res.err, "no /proc") {
		t.Fatalf("%+v", res)
	}
	r.u.executable = func() (string, error) { return filepath.Join(r.bin, "ds-old"), nil }
	if res := r.run(t, dir, "update"); !strings.Contains(res.err, "ds-old, not ds; reinstall it with install.sh") {
		t.Fatalf("%+v", res)
	}
	// A directory the user cannot write says how to fix it.
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		r = newRig(t, "v0.1.6", "v0.1.7")
		_ = os.Chmod(r.bin, 0o555)
		defer func() { _ = os.Chmod(r.bin, 0o755) }()
		if res := r.run(t, dir, "update"); !strings.Contains(res.err, "is not writable: re-run `ds update` with sudo") {
			t.Fatalf("%+v", res)
		}
	}
}

// The binary is found through a symlink, as `task install` and Homebrew
// both link it, and the link's target is what is replaced.
func TestUpdateFollowsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	r := newRig(t, "v0.1.6", "v0.1.7")
	link := filepath.Join(t.TempDir(), "ds")
	if err := os.Symlink(filepath.Join(r.bin, "ds"), link); err != nil {
		t.Fatal(err)
	}
	r.u.executable = func() (string, error) { return link, nil }
	if res := r.run(t, t.TempDir(), "update"); res.code != 0 || r.installed(t) != "ds v0.1.7" {
		t.Fatalf("%+v", res)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced instead of its target")
	}
}

func TestUpdateHelpers(t *testing.T) {
	// Colour only at a terminal, and not under NO_COLOR or TERM=dumb.
	env := map[string]string{}
	u := updater{getenv: func(k string) string { return env[k] }}
	if p := u.colours(true); !p.on || p.ok("x") != "\x1b[32m✓ \x1b[0mx" || p.dim("d") != "\x1b[2md\x1b[0m" {
		t.Fatalf("%q", p.ok("x"))
	}
	if u.colours(false).on {
		t.Fatal("colour without a terminal")
	}
	env[envNoColor] = "1"
	if u.colours(true).on {
		t.Fatal("colour under NO_COLOR")
	}
	env = map[string]string{envTerm: termDumb}
	if u.colours(true).on {
		t.Fatal("colour on a dumb terminal")
	}
	// Modes combine to the strictest.
	if stricter(config.UpdateAuto, config.UpdateOff) != config.UpdateOff || stricter(config.UpdateOff, config.UpdateNotify) != config.UpdateOff {
		t.Fatal("stricter")
	}
	// Windows names the program ds.exe.
	if main, want := (updater{goos: "windows"}).files(); main != "ds.exe" || !want("ds-resolve-aws.exe") || want("README.md") {
		t.Fatal(main)
	}
	// A buffer and a regular file are not terminals.
	f, _ := os.Create(filepath.Join(t.TempDir(), "f"))
	defer f.Close()
	if isTerminal(&bytes.Buffer{}) || isTerminal(f) {
		t.Fatal("terminal")
	}
}

// The real updater: a custom binary never follows ds's releases, and the
// seams it fills work.
func TestDefaultUpdater(t *testing.T) {
	var out bytes.Buffer
	a := &App{name: "pds", stdin: strings.NewReader(""), stdout: &out, stderr: &out}
	u := a.defaultUpdater()
	if u.release != "" || u.interactive() {
		t.Fatalf("%+v", u)
	}
	if p, err := u.statePath(); err == nil && !strings.HasSuffix(p, filepath.Join(stateDir, stateFile)) {
		t.Fatal(p)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	sh, _ := exec.LookPath("sh")
	if code, err := u.reexec(sh, []string{"-c", "echo $" + envUpdate + "; exit 3"}); code != 3 || err != nil || strings.TrimSpace(out.String()) != config.UpdateOff {
		t.Fatalf("%d %v %q", code, err, out.String())
	}
	if code, err := u.reexec(sh, []string{"-c", "true"}); code != 0 || err != nil {
		t.Fatalf("%d %v", code, err)
	}
	if _, err := u.reexec(filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Fatal("a missing program ran")
	}
	releaseVersion = "v0.1.6"
	defer func() { releaseVersion = "" }()
	a.name = DefaultName
	if u := a.defaultUpdater(); u.release != "v0.1.6" {
		t.Fatal(u.release)
	}
}

// Every update message says how to change the behaviour, at the moment
// someone would want to.
func TestUpdateSettingsAreDiscoverable(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, "v0.1.6", "v0.1.7")
	if res := r.run(t, dir, "status"); !strings.Contains(res.err, "DS_UPDATE=notify to only be told, DS_UPDATE=off to stop · ds update --help") {
		t.Fatalf("auto message: %+v", res)
	}
	r = newRig(t, "v0.1.6", "v0.1.7")
	r.env[envUpdate] = config.UpdateNotify
	if res := r.run(t, dir, "status"); !strings.Contains(res.err, "run `ds update` · DS_UPDATE=off to stop these") {
		t.Fatalf("notice: %+v", res)
	}
	// ds update ends with the mode, what decided it, and how to change it.
	res := r.run(t, dir, "update", "--check")
	if !strings.Contains(res.out, "Automatic updates: notify (DS_UPDATE) · change with DS_UPDATE=notify|off or [update] mode in .ds/config.toml") {
		t.Fatalf("footer: %+v", res)
	}
	var rep UpdateReport
	res = r.run(t, dir, "update", "--check", "--json")
	if err := json.Unmarshal([]byte(res.out), &rep); err != nil || rep.AutoUpdate != config.UpdateNotify || rep.AutoUpdateFrom != envUpdate {
		t.Fatalf("%+v %v", res, err)
	}
	r = newRig(t, "v0.1.7", "v0.1.7")
	if res := r.run(t, dir, "update"); !strings.Contains(res.out, "Automatic updates: auto (default) · change with") {
		t.Fatalf("default footer: %+v", res)
	}
	r.env[envUpdate] = "sometimes"
	if res := r.run(t, dir, "update"); !strings.Contains(res.err, "DS_UPDATE=sometimes is not one of") || !strings.Contains(res.out, "Automatic updates: notify (DS_UPDATE)") {
		t.Fatalf("unknown env: %+v", res)
	}
	// A development build says why it never updates itself.
	r = newRig(t, "", "v0.1.7")
	r.u.build = "v0.1.7-0.2026-abc+dirty"
	if res := r.run(t, dir, "update", "--check"); !strings.Contains(res.out, "Automatic updates: off (not a release build)\n") {
		t.Fatalf("dev footer: %+v", res)
	}
	// The repository's setting is named when it is the one that decided.
	repo, _ := initialised(t)
	cfg := filepath.Join(repo, DirName, ConfigFile)
	base, _ := os.ReadFile(cfg)
	_ = os.WriteFile(cfg, append(base, []byte("\n[update]\nmode = \"off\"\n")...), 0o644)
	r = newRig(t, "v0.1.7", "v0.1.7")
	r.env[envUpdate] = config.UpdateNotify
	if res := r.run(t, repo, "update", "--check"); !strings.Contains(res.out, "Automatic updates: off (.ds/config.toml [update] mode)") {
		t.Fatalf("repo footer: %+v", res)
	}
	// ds --help lists the environment variables.
	if res := r.run(t, dir, "--help"); !strings.Contains(res.out, "Environment:") || !strings.Contains(res.out, "DS_UPDATE  auto | notify | off") || !strings.Contains(res.out, "NO_COLOR") {
		t.Fatalf("help: %+v", res)
	}
}

// doctor's update row: the mode, its source and the last check, without
// asking the network.
func TestDoctorUpdateRow(t *testing.T) {
	repo, _ := initialised(t)
	doctorRow := func(r *updateRig) string {
		t.Helper()
		res := r.run(t, repo, "doctor")
		for _, line := range strings.Split(res.out, "\n") {
			if strings.HasPrefix(line, "update ") {
				return strings.Join(strings.Fields(line), " ")
			}
		}
		t.Fatalf("no update row: %+v", res)
		return ""
	}
	r := newRig(t, "v0.1.6", "v0.1.7")
	if got := doctorRow(r); got != "update ok auto (from default); not checked yet" {
		t.Fatal(got)
	}
	_ = selfupdate.State{CheckedAt: clock.Add(-3 * time.Hour), Latest: "v0.1.7"}.Save(r.state)
	if got := doctorRow(r); got != "update WARN auto (from default); v0.1.7 is available (checked 3 hr ago): run `ds update`" {
		t.Fatal(got)
	}
	r.env[envUpdate] = config.UpdateOff
	_ = selfupdate.State{CheckedAt: clock.Add(-time.Hour), Latest: "v0.1.6"}.Save(r.state)
	if got := doctorRow(r); got != "update ok off (from DS_UPDATE); v0.1.6 is the latest (checked 1 hr ago)" {
		t.Fatal(got)
	}
	r.u.statePath = func() (string, error) { return "", errors.New("no home") }
	if got := doctorRow(r); !strings.HasSuffix(got, "no cache directory, so nothing is remembered between runs") {
		t.Fatal(got)
	}
	if r.asked != 0 {
		t.Fatal("doctor asked the network")
	}
	r.u.release = ""
	if got := doctorRow(r); got != "update ok off (not a release build): rebuild it, or `go install` a newer version" {
		t.Fatal(got)
	}
}
