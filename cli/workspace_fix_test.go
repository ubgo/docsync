package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitIn runs git in dir with a fixed identity and no global config, failing
// the test on error, and returns its trimmed output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

// runGit is run with the real Git VCS on dir, the way the binary runs.
func runGit(t *testing.T, dir string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, WithDir(dir), WithIO(strings.NewReader(""), &out, &errb), WithClock(func() time.Time { return clock }))
	return result{code, out.String(), errb.String()}
}

// promise:local-index-never-pushed
// TestPublishCommitsALocalGitIndex pins bug 100: publishing into an index
// that is a local directory wrote the files and stopped, leaving them
// untracked in an index that is itself a git repository. The publish now
// commits there (never pushes), and a plain directory -- even one nested
// inside an unrelated repository -- is written and left alone.
func TestPublishCommitsALocalGitIndex(t *testing.T) {
	t.Parallel()
	needGit(t)
	root := t.TempDir()
	index := filepath.Join(root, "index")
	repo := filepath.Join(root, "api")
	write(t, index, "ds-workspace.toml", "[workspace]\nname = \"p\"\nrepos = [\"github.com/org/api\"]\n")
	gitIn(t, index, "init", "-q", "-b", "main")
	gitIn(t, index, "add", "-A")
	gitIn(t, index, "commit", "-qm", "ws")
	write(t, repo, "x.go", "package x\n\n// ds:def id=x-a2b6f8jk\nvar X = 1\n")
	write(t, repo, ".ds/config.toml", "workspace = "+strconvQuote(index)+"\n[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "remote", "add", "origin", "https://github.com/org/api")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "x")
	gitIn(t, index, "config", "user.name", "t")
	gitIn(t, index, "config", "user.email", "t@t")
	// The index has a remote, and publish must not push to it.
	remote := filepath.Join(root, "remote.git")
	gitIn(t, root, "init", "-q", "--bare", remote)
	gitIn(t, index, "remote", "add", "origin", remote)
	r := runGit(t, repo, "publish")
	if r.code != 0 || !strings.Contains(r.out, "committed in "+index) || strings.Contains(r.out, "pushed") {
		t.Fatalf("publish = %+v", r)
	}
	if out, err := exec.Command("git", "-C", remote, "rev-parse", "--verify", "-q", "HEAD").CombinedOutput(); err == nil {
		t.Errorf("publish pushed to the local index's remote: %s", out)
	}
	if st := gitIn(t, index, "status", "--porcelain"); st != "" {
		t.Errorf("index left dirty after publish:\n%s", st)
	}
	if log := gitIn(t, index, "log", "--format=%s", "-1"); !strings.HasPrefix(log, "docsync publish api @ ") {
		t.Errorf("index commit message = %q", log)
	}
	// Publishing the same ledger again commits nothing and says nothing.
	if r := runGit(t, repo, "publish"); r.code != 0 || strings.Contains(r.out, "committed") {
		t.Errorf("republish = %+v", r)
	}

	// A plain directory nested inside another repository is not that
	// repository's to commit into.
	outer := filepath.Join(root, "outer")
	nested := filepath.Join(outer, "index")
	write(t, nested, "ds-workspace.toml", "[workspace]\nname = \"p\"\nrepos = [\"github.com/org/api\"]\n")
	gitIn(t, outer, "init", "-q", "-b", "main")
	write(t, repo, ".ds/config.toml", "workspace = "+strconvQuote(nested)+"\n[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	if r := runGit(t, repo, "publish", "--force"); r.code != 0 || strings.Contains(r.out, "committed") {
		t.Errorf("nested plain index = %+v", r)
	}
	if log, err := exec.Command("git", "-C", outer, "log", "--oneline").CombinedOutput(); err == nil {
		t.Errorf("outer repository got a commit: %s", log)
	}

	// A commit the index refuses (a failing hook) fails the publish.
	write(t, index, ".git/hooks/pre-commit", "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(filepath.Join(index, ".git/hooks/pre-commit"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, repo, ".ds/config.toml", "workspace = "+strconvQuote(index)+"\n[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	write(t, repo, "y.go", "package x\n\n// ds:def id=y-a2b6f8jk\nvar Y = 1\n")
	if r := runGit(t, repo, "publish", "--force"); r.code != ExitError {
		t.Errorf("refused index commit = %+v", r)
	}
	if ok, err := (Git{}).CommitIndex(filepath.Join(root, "missing"), "m"); ok || err != nil {
		t.Errorf("missing dir = %v %v", ok, err)
	}
}

// TestFirstCommandClonesAGitIndex pins bug 104: with a git-URL index, every
// command that loads the workspace -- `ds def`, `ds scan` -- stopped with
// "workspace index unreachable and no cached copy" until someone ran
// `ds sync`. The first command that needs the clone now makes it.
func TestFirstCommandClonesAGitIndex(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	write(t, repo, "docs/a.md", "# A\n\n## Part\n\ntext\n")
	write(t, repo, ".ds/config.toml", "workspace = \"git@github.com:org/ds-index.git\"\n[scan]\ndocs = [\"docs/**\"]\n")
	v := fakeVCS{head: "c1", branch: "main", files: map[string][]byte{}, cloneOK: true}
	if r := run(t, repo, v, "def", "docs/a.md#Part", "--label", "part"); r.code != 0 {
		t.Fatalf("def before any sync = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(repo, DirName, IndexDir)); err != nil {
		t.Errorf("def did not clone the index: %v", err)
	}
	if r := run(t, repo, v, "scan"); r.code != 0 {
		t.Errorf("scan after the clone = %+v", r)
	}
	// A clone that reports success and leaves nothing is still offline.
	empty := t.TempDir()
	write(t, empty, "docs/a.md", "text\n")
	write(t, empty, ".ds/config.toml", "workspace = \"git@github.com:org/ds-index.git\"\n[scan]\ndocs = [\"docs/**\"]\n")
	if r := run(t, empty, hollowClone{v}, "scan"); r.code != ExitError || !strings.Contains(r.err, "unreachable") {
		t.Errorf("hollow clone = %+v", r)
	}
}

// hollowClone is a VCS whose Clone succeeds without creating anything.
type hollowClone struct{ fakeVCS }

func (hollowClone) Clone(string, string) error { return nil }

// TestStaleAfterCommitsWarns pins bug 101: workspace.stale_after_commits
// was parsed and never read, and the "index for api is N commits behind"
// warning did not exist. The publishing repository now hears it once its
// default branch is more than that many commits past what it published.
func TestStaleAfterCommitsWarns(t *testing.T) {
	t.Parallel()
	needGit(t)
	root := t.TempDir()
	index := filepath.Join(root, "index")
	repo := filepath.Join(root, "api")
	write(t, index, "ds-workspace.toml", "[workspace]\nname = \"p\"\nrepos = [\"github.com/org/api\"]\nstale_after_commits = 2\n")
	write(t, repo, "x.go", "package x\n\n// ds:def id=x-a2b6f8jk\nvar X = 1\n")
	write(t, repo, ".ds/config.toml", "workspace = "+strconvQuote(index)+"\n[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "remote", "add", "origin", "https://github.com/org/api")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "x")
	if r := runGit(t, repo, "publish"); r.code != 0 {
		t.Fatalf("publish = %+v", r)
	}
	for i := range 3 {
		if r := runGit(t, repo, "scan"); r.code != 0 || strings.Contains(r.err, "commits behind") {
			t.Errorf("after %d commits = %+v", i, r)
		}
		write(t, repo, "y.txt", strings.Repeat("y", i+1))
		gitIn(t, repo, "add", "-A")
		gitIn(t, repo, "commit", "-qm", "y")
	}
	if r := runGit(t, repo, "scan"); r.code != 0 || !strings.Contains(r.err, "index for api is 3 commits behind main (workspace.stale_after_commits = 2)") {
		t.Errorf("3 commits past a limit of 2 = %+v", r)
	}
	if n, err := (Git{Dir: repo}).CommitsBetween("nosuchcommit", "main"); err == nil {
		t.Errorf("unknown commit counted %d", n)
	}
}

// TestIndexKeyMustMatchTheConfiguredIndex pins the other half of bug 101:
// the workspace file's `index` was parsed and ignored. It names the
// canonical index, so a repository whose `workspace` URL is another
// repository hears that it is reading the wrong one.
func TestIndexKeyMustMatchTheConfiguredIndex(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	write(t, repo, "docs/a.md", "text\n")
	write(t, repo, ".ds/config.toml", "workspace = \"git@github.com:someone/ds-index.git\"\n[scan]\ndocs = [\"docs/**\"]\n")
	write(t, repo, ".ds/index/ds-workspace.toml", "[workspace]\nname = \"p\"\nrepos = [\"github.com/org/api\"]\nindex = \"https://github.com/org/ds-index\"\n")
	v := fakeVCS{head: "c1", branch: "main", files: map[string][]byte{}}
	want := "ds-workspace.toml names the index https://github.com/org/ds-index, but .ds/config.toml points workspace at git@github.com:someone/ds-index.git"
	if r := run(t, repo, v, "scan"); !strings.Contains(r.err, want) {
		t.Errorf("mismatched index = %+v", r)
	}
	write(t, repo, ".ds/config.toml", "workspace = \"git@github.com:org/ds-index.git\"\n[scan]\ndocs = [\"docs/**\"]\n")
	if r := run(t, repo, v, "scan"); strings.Contains(r.err, "names the index") {
		t.Errorf("same index written another way = %+v", r)
	}
}
