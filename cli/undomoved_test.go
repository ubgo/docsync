package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// introVCS is a fakeVCS that can name the commit which added a line.
type introVCS struct {
	fakeVCS
	intro string
	err   error
}

func (v introVCS) Introduced(string, string) (string, error) { return v.intro, v.err }

// A committed def pushed down by lines added above it is still committed,
// and the listing names the commit that made the write rather than HEAD
// (bug 71).
func TestUndoListFindsAMovedCommittedWrite(t *testing.T) {
	t.Parallel()
	dir, v, _ := undoFixture(t)
	raw, _ := os.ReadFile(filepath.Join(dir, "conf/app.yaml"))
	moved := append([]byte("# a\n# b\n"), raw...)
	v.files[v.head+":conf/app.yaml"] = moved
	write(t, dir, "conf/app.yaml", string(moved))
	r := run(t, dir, v, "undo", "--list")
	if r.code != 0 || !strings.Contains(r.out, "committed abc1234") || strings.Contains(r.out, string(statusUncommitted)) {
		t.Errorf("moved committed write, no introducer = %+v", r)
	}
	if r := run(t, dir, introVCS{fakeVCS: v, intro: "1a2b3c4"}, "undo", "--list"); !strings.Contains(r.out, "committed 1a2b3c4") {
		t.Errorf("introducing commit = %+v", r)
	}
	// An introducer that fails or knows nothing falls back to HEAD.
	if r := run(t, dir, introVCS{fakeVCS: v, err: errors.New("x")}, "undo", "--list"); !strings.Contains(r.out, "committed abc1234") {
		t.Errorf("introducer error = %+v", r)
	}
	// Still refused without --force: it is in history.
	if r := run(t, dir, v, "undo"); r.code == 0 {
		t.Errorf("a moved committed write was undone without --force: %+v", r)
	}
}

func TestGitIntroduced(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	sh := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	g := Git{Dir: dir}
	if _, err := g.Introduced("a.txt", "x"); err == nil {
		t.Error("outside a repository")
	}
	sh("init", "-q")
	write(t, dir, "a.txt", "one\n# ds:def id=x-a2b6f8jk\n")
	sh("add", "-A")
	sh("commit", "-q", "-m", "one")
	first, _ := g.Head()
	write(t, dir, "a.txt", "zero\none\n# ds:def id=x-a2b6f8jk\n")
	sh("commit", "-q", "-am", "two")
	if sha, err := g.Introduced("a.txt", "# ds:def id=x-a2b6f8jk"); err != nil || sha != first {
		t.Errorf("introduced = %q %v, want %q", sha, err, first)
	}
	if sha, err := g.Introduced("a.txt", "never written"); err != nil || sha != "" {
		t.Errorf("never written = %q %v", sha, err)
	}
}
