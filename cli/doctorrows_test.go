package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/config"
)

// probingVCS is a fakeVCS that can also answer Prober.
type probingVCS struct {
	fakeVCS
	reach error
}

func (p probingVCS) Reachable(string) error { return p.reach }

// TestDoctorWorkspaceRows pins bug 125: SPEC §22 promises that doctor
// checks index reachability and reports the workspace, and it did neither.
// Every outcome is pinned: no workspace, a local index that loads and one
// that does not, and a remote that answers, does not answer with a cached
// copy (a warning: check runs on the copy), does not answer without one (a
// failure: check cannot run), or cannot be probed at all.
func TestDoctorWorkspaceRows(t *testing.T) {
	t.Parallel()
	rows := func(v VCS, cfg config.Config, dir string) [][]string {
		a := &App{name: DefaultName, dir: dir, vcs: v, now: func() time.Time { return clock }, stderr: &bytes.Buffer{}}
		return a.workspaceRows(cfg)
	}
	status := func(rs [][]string, name string) (string, string) {
		for _, r := range rs {
			if r[0] == name {
				return r[1], r[2]
			}
		}
		return "", ""
	}
	// The rows are on doctor's real output, not only here.
	idir, iv := initialised(t)
	if out := run(t, idir, iv, "doctor").out; !strings.Contains(out, rowWorkspace) {
		t.Errorf("doctor prints no workspace row:\n%s", out)
	}
	dir := t.TempDir()
	cfg := config.Default()
	if st, _ := status(rows(fakeVCS{}, cfg, dir), rowWorkspace); st != doctorOK {
		t.Errorf("no workspace: %v", rows(fakeVCS{}, cfg, dir))
	}

	local := filepath.Join(dir, "idx")
	write(t, local, "repos/.keep", "")
	cfg.Workspace = local
	if st, detail := status(rows(fakeVCS{}, cfg, dir), rowIndex); st != doctorOK || !strings.Contains(detail, "local directory") {
		t.Errorf("readable local index = %s %q", st, detail)
	}
	write(t, local, config.WorkspaceFile, "[workspace]\nname = 1\n")
	if st, _ := status(rows(fakeVCS{}, cfg, dir), rowIndex); st != doctorFail {
		t.Errorf("a local index whose workspace file does not load must FAIL, got %s", st)
	}

	cfg.Workspace = "https://example.invalid/org/idx.git"
	offline := errors.New("could not resolve host")
	for _, tc := range []struct {
		name   string
		v      VCS
		cached bool
		want   string
	}{
		{"reachable", probingVCS{reach: nil}, false, doctorOK},
		{"unreachable with a cached copy", probingVCS{reach: offline}, true, doctorWarn},
		{"unreachable without a cached copy", probingVCS{reach: offline}, false, doctorFail},
		{"cannot probe", fakeVCS{}, false, doctorWarn},
	} {
		d := t.TempDir()
		if tc.cached {
			write(t, d, filepath.Join(DirName, IndexDir, "repos", ".keep"), "")
		}
		if st, detail := status(rows(tc.v, cfg, d), rowIndex); st != tc.want {
			t.Errorf("%s: index row = %s %q, want %s", tc.name, st, detail, tc.want)
		}
	}
}

// TestDoctorResolverRows pins bug 125's resolver half: each provider in
// resolve.providers gets a row saying whether its ds-resolve-<provider>
// plugin is on PATH, and no providers means no rows.
func TestDoctorResolverRows(t *testing.T) {
	t.Parallel()
	a := &App{pluginLookPath: func(name string) (string, error) {
		if name == "ds-resolve-github" {
			return "/bin/ds-resolve-github", nil
		}
		return "", exec.ErrNotFound
	}}
	cfg := config.Default()
	if rs := a.resolverRows(cfg); len(rs) != 0 {
		t.Errorf("no providers must print no rows, got %v", rs)
	}
	cfg.Resolve.Providers = []string{"github", "vault"}
	rs := a.resolverRows(cfg)
	if len(rs) != 2 || rs[0][1] != doctorOK || rs[1][1] != doctorWarn || !strings.Contains(rs[1][2], "ds-resolve-vault") {
		t.Errorf("resolver rows = %v", rs)
	}
	// The default lookup is PATH itself.
	if rs := (&App{}).resolverRows(cfg); len(rs) != 2 {
		t.Errorf("PATH lookup rows = %v", rs)
	}
}

// TestGitReachable runs the real probe against a local repository, a path
// that is not one, and a directory git cannot start in (no stderr at all).
func TestGitReachable(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := (Git{}).Reachable(dir); err != nil {
		t.Errorf("a local repository must be reachable: %v", err)
	}
	if err := (Git{}).Reachable(filepath.Join(dir, "missing")); err == nil || strings.Contains(err.Error(), "\n") {
		t.Errorf("a missing repository must be one line of unreachable, got %v", err)
	}
	if err := (Git{Dir: filepath.Join(dir, "no-such-dir")}).Reachable(dir); err == nil {
		t.Error("a probe that cannot start must report an error")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf("the probe must leave the repository alone: %v", err)
	}
}
