package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/config"
)

// offlineRepo is a repository whose `workspace` names a local directory that
// does not exist on this machine, the shape of GitHub issue 8: a public
// repository joined to a workspace whose index lives somewhere private.
func offlineRepo(t *testing.T) (string, fakeVCS) {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "docs/a.md", "# A\n\ntext\n")
	write(t, dir, ".ds/config.toml", "workspace = \"../shared-index\"\n[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	return dir, fakeVCS{head: "c1", branch: "main", files: map[string][]byte{}}
}

// recordSnapshot writes .ds/foreign.tsv the way a user would: create the
// index, sync, and remove the index again.
func recordSnapshot(t *testing.T, dir string, v VCS) {
	t.Helper()
	idx := filepath.Join(dir, "..", "shared-index")
	write(t, idx, "repos/.keep", "")
	if r := run(t, dir, v, "sync"); r.code != 0 {
		t.Fatalf("sync with the index present = %+v", r)
	}
	if err := os.RemoveAll(idx); err != nil {
		t.Fatal(err)
	}
}

// TestMissingIndexCarriesOn pins bug 147 (GitHub issue 8): a workspace index
// that is not on this machine made every non-frozen command exit 2, even
// `ds scan` and `ds def`, which only touch this repository. Now every
// command but `sync` warns and carries on, foreign citations come from the
// committed snapshot when there is one, and when there is none they are
// reported as not recorded -- never as passing -- and `check --strict`
// fails closed.
// promise:missing-index-carries-on
// promise:strict-no-index-fails
func TestMissingIndexCarriesOn(t *testing.T) {
	t.Parallel()
	dir, v := offlineRepo(t)
	for _, args := range [][]string{{"scan"}, {"check"}, {"def", "docs/a.md:3", "--dry-run"}, {"map"}} {
		r := run(t, dir, v, args...)
		if r.code != 0 || !strings.Contains(r.err, "warning: workspace path ../shared-index does not exist") || !strings.Contains(r.err, "cannot be checked") {
			t.Errorf("%v with no index = %+v", args, r)
		}
	}
	if r := run(t, dir, v, "sync"); r.code != ExitError || !strings.Contains(r.err, "does not exist") {
		t.Errorf("sync with no index must stop = %+v", r)
	}
	// --strict fails closed: nothing could check a foreign citation.
	if r := run(t, dir, v, "check", "--strict"); r.code != ExitFindings || !strings.Contains(r.err, "--strict") || !strings.Contains(r.err, "ds sync") {
		t.Errorf("check --strict with no index and no snapshot = %+v", r)
	}
	// A workspace file that does not load is still refused (§21), not
	// carried past: it is the workspace's own rule, not a missing index.
	idx := filepath.Join(dir, "..", "bad-index")
	write(t, idx, config.WorkspaceFile, "[workspace]\nname = 1\n")
	write(t, dir, ".ds/config.toml", "workspace = \"../bad-index\"\n[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	if r := run(t, dir, v, "scan"); r.code != ExitError || !strings.Contains(r.err, config.WorkspaceFile) {
		t.Errorf("scan with a workspace file that does not load = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "workspace = \"../shared-index\"\n[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	// A foreign citation is reported, not passed in silence.
	write(t, dir, "docs/b.md", "<!-- ds:block id=limit-5td2n7pp -->\nLimit is 5.\n")
	if r := run(t, dir, v, "check"); r.code != ExitFindings || !strings.Contains(r.out, "is cited but is not recorded") {
		t.Errorf("foreign citation with no index and no snapshot = %+v", r)
	}
	os.Remove(filepath.Join(dir, "docs/b.md"))

	// With the committed snapshot, the run is what --frozen would give, and
	// --strict passes.
	recordSnapshot(t, dir, v)
	r := run(t, dir, v, "check", "--strict")
	if r.code != 0 || !strings.Contains(r.err, "foreign citations are read from .ds/foreign.tsv") {
		t.Errorf("check --strict with a snapshot = %+v", r)
	}
	// A snapshot that does not parse is a named error, not a pass.
	write(t, dir, ".ds/foreign.tsv", "not a snapshot\n")
	if r := run(t, dir, v, "check"); r.code != ExitError || !strings.Contains(r.err, "foreign.tsv") {
		t.Errorf("unreadable snapshot with no index = %+v", r)
	}
}

// TestDoctorRowWithoutIndex pins bug 147's doctor half: with no index at
// all, check still runs, so the index row is WARN when the committed
// snapshot lets it resolve foreign citations and FAIL when nothing does,
// for a local path and a remote alike.
func TestDoctorRowWithoutIndex(t *testing.T) {
	t.Parallel()
	index := func(t *testing.T, dir string, v VCS) (string, string) {
		t.Helper()
		a := &App{name: DefaultName, dir: dir, vcs: v, now: func() time.Time { return clock }}
		cfg, err := a.loadConfig(NewStore(dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range a.workspaceRows(cfg) {
			if r[0] == rowIndex {
				return r[1], r[2]
			}
		}
		return "", ""
	}
	dir, v := offlineRepo(t)
	if st, detail := index(t, dir, v); st != doctorFail || !strings.Contains(detail, "no .ds/foreign.tsv either") {
		t.Errorf("local path, no snapshot = %s %q", st, detail)
	}
	recordSnapshot(t, dir, v)
	if st, detail := index(t, dir, v); st != doctorWarn || !strings.Contains(detail, "check reads the 0 foreign blocks") {
		t.Errorf("local path, snapshot = %s %q", st, detail)
	}
	write(t, dir, ".ds/foreign.tsv", "not a snapshot\n")
	if st, detail := index(t, dir, v); st != doctorFail || !strings.Contains(detail, "cannot be read either") {
		t.Errorf("local path, corrupt snapshot = %s %q", st, detail)
	}

	remote := t.TempDir()
	write(t, remote, ".ds/config.toml", "workspace = \"https://example.invalid/org/idx.git\"\n[scan]\ndocs = [\"docs/**\"]\n")
	pv := probingVCS{reach: errors.New("could not resolve host")}
	if st, _ := index(t, remote, pv); st != doctorFail {
		t.Errorf("remote, no cache, no snapshot = %s", st)
	}
	write(t, remote, ".ds/foreign.tsv", string(emptySnapshot(t)))
	if st, detail := index(t, remote, pv); st != doctorWarn || !strings.Contains(detail, "unreachable (could not resolve host)") {
		t.Errorf("remote, no cache, snapshot = %s %q", st, detail)
	}
}

// emptySnapshot is a valid foreign.tsv recording no blocks.
func emptySnapshot(t *testing.T) []byte {
	t.Helper()
	dir, v := offlineRepo(t)
	recordSnapshot(t, dir, v)
	raw, err := os.ReadFile(filepath.Join(dir, ".ds", "foreign.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestGitReason pins bug 150: doctor's index row showed the last line of
// git's stderr, which is the generic footer "and the repository exists.",
// instead of the line that says why.
func TestGitReason(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ stderr, want string }{
		{"fatal: '/x/idx.git' does not appear to be a git repository\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n", "'/x/idx.git' does not appear to be a git repository"},
		{"ssh: Could not resolve hostname example.invalid: nodename nor servname provided\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n", "ssh: Could not resolve hostname example.invalid: nodename nor servname provided"},
		{"fatal: unable to access 'https://example.invalid/idx.git/': Could not resolve host: example.invalid\n", "unable to access 'https://example.invalid/idx.git/': Could not resolve host: example.invalid"},
		{"warning: redirecting to https://example.com/idx.git/\nhint: use --quiet\nremote: Repository not found.\nfatal: repository 'https://example.com/idx.git/' not found\n", "remote: Repository not found."},
		{"\n\n", ""},
	} {
		if got := gitReason(tc.stderr); got != tc.want {
			t.Errorf("gitReason(%q) = %q, want %q", tc.stderr, got, tc.want)
		}
	}
	// The real probe and the real clone both say why.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if err := (Git{}).Reachable(missing); err == nil || !strings.Contains(err.Error(), "does not appear to be a git repository") {
		t.Errorf("probe of a missing repository = %v", err)
	}
	dir := t.TempDir()
	write(t, dir, ".ds/config.toml", "workspace = \"file://"+filepath.ToSlash(missing)+"\"\n[scan]\ndocs = [\"docs/**\"]\n")
	r := run(t, dir, Git{Dir: dir}, "scan")
	if r.code != 0 || !strings.Contains(r.err, "does not appear to be a git repository") || strings.Contains(r.err, "repository exists") {
		t.Errorf("scan with an unclonable index = %+v", r)
	}
}

// cloneRecorder is a fakeVCS that records what it was asked to clone.
type cloneRecorder struct {
	fakeVCS
	cloned *[]string
}

func (c cloneRecorder) Clone(url, dir string) error {
	*c.cloned = append(*c.cloned, url)
	return c.fakeVCS.Clone(url, dir)
}

// TestWorkspacePathIsNotCloned pins bug 151: a `workspace` path missing on
// this machine was handed to `git clone` as if it were a URL, so the run
// reported git's "repository '../shared-index' does not exist" about a
// clone nobody asked for. A path is now a path, named in its own error, and
// only a URL-shaped value is cloned -- the bare `host/path` form over https,
// since git would read it as a local path.
// promise:workspace-path-never-cloned
func TestWorkspacePathIsNotCloned(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		loc, src string
		remote   bool
	}{
		{"https://github.com/org/idx", "https://github.com/org/idx", true},
		{"file:///srv/idx.git", "file:///srv/idx.git", true},
		{"git@github.com:org/idx.git", "git@github.com:org/idx.git", true},
		{"host:idx.git", "host:idx.git", true},
		{"github.com/org/ds-index", "https://github.com/org/ds-index", true},
		{"../shared-index", "", false},
		{"..", "", false},
		{"./idx.d/x", "", false},
		{"idx", "", false},
		{"/srv/idx", "", false},
		{`C:\idx`, "", false},
		{"C:/idx", "", false},
		{"idx.d", "", false},
	} {
		if src, ok := cloneSource(tc.loc); src != tc.src || ok != tc.remote {
			t.Errorf("cloneSource(%q) = %q, %v; want %q, %v", tc.loc, src, ok, tc.src, tc.remote)
		}
	}

	var cloned []string
	dir, base := offlineRepo(t)
	v := cloneRecorder{fakeVCS: base, cloned: &cloned}
	if r := run(t, dir, v, "sync"); r.code != ExitError || !strings.Contains(r.err, "workspace path ../shared-index does not exist") || !strings.Contains(r.err, "point `workspace`") {
		t.Errorf("sync with a missing path = %+v", r)
	}
	if len(cloned) != 0 {
		t.Errorf("a missing path was cloned: %v", cloned)
	}
	// An absolute path that does not exist, and one that is a file.
	abs := filepath.Join(t.TempDir(), "idx")
	write(t, dir, ".ds/config.toml", "workspace = "+strconvQuote(abs)+"\n[scan]\ndocs = [\"docs/**\"]\n")
	if r := run(t, dir, v, "sync"); r.code != ExitError || !strings.Contains(r.err, "workspace path "+abs+" does not exist") {
		t.Errorf("sync with a missing absolute path = %+v", r)
	}
	write(t, filepath.Dir(abs), "idx", "a file\n")
	if r := run(t, dir, v, "sync"); r.code != ExitError || !strings.Contains(r.err, "is not a directory") {
		t.Errorf("sync with a file for an index = %+v", r)
	}
	// A symlink whose target is missing names the link and its target, not
	// just "does not exist" (bug 151): a link at .ds/workspace pointing at
	// a checkout this machine does not have.
	if runtime.GOOS != "windows" {
		link := filepath.Join(dir, ".ds", "workspace")
		if err := os.Symlink("../gone/index", link); err != nil {
			t.Fatal(err)
		}
		write(t, dir, ".ds/config.toml", "workspace = \".ds/workspace\"\n[scan]\ndocs = [\"docs/**\"]\n")
		want := "workspace path .ds/workspace (resolved to " + link + ") is a symlink to " + filepath.Join(dir, "gone", "index") + ", which does not exist"
		if r := run(t, dir, v, "sync"); r.code != ExitError || !strings.Contains(r.err, want) {
			t.Errorf("sync with a dangling symlink = %+v\nwant %q", r, want)
		}
		if r := run(t, dir, v, "scan"); r.code != 0 || !strings.Contains(r.err, "warning: "+want) {
			t.Errorf("scan with a dangling symlink = %+v", r)
		}
	}
	// A relative path names where it resolved.
	write(t, dir, ".ds/config.toml", "workspace = \"idx\"\n[scan]\ndocs = [\"docs/**\"]\n")
	if r := run(t, dir, v, "sync"); r.code != ExitError || !strings.Contains(r.err, "(resolved to "+filepath.Join(dir, "idx")+")") {
		t.Errorf("sync with a missing relative path = %+v", r)
	}
	if len(cloned) != 0 {
		t.Errorf("a missing path was cloned: %v", cloned)
	}
	// The spec's own bare form is cloned over https.
	write(t, dir, ".ds/config.toml", "workspace = \"github.com/org/ds-index\"\n[scan]\ndocs = [\"docs/**\"]\n")
	v.cloneOK = true
	if r := run(t, dir, v, "sync"); r.code != 0 || len(cloned) != 1 || cloned[0] != "https://github.com/org/ds-index" {
		t.Errorf("bare host/path = %+v, cloned %v", r, cloned)
	}
	cfg := config.Default()
	cfg.Workspace = "github.com/org/ds-index"
	if _, managed := (&App{dir: dir}).indexPath(cfg); !managed {
		t.Error("a bare host/path must be a managed clone")
	}
}
