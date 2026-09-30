package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// promise:review-never-acks
func TestReview(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	v.files["abc1234:internal/store/write.go"] = src
	write(t, dir, "internal/store/write.go", goV2)
	r := run(t, dir, v, "review")
	if r.code != 0 || !strings.Contains(r.out, "- [ ] docs/sessions.md:1  unacked") || !strings.Contains(r.out, "still true: ds ack") || !strings.Contains(r.out, "| +\treturn s.sessions.Insert()") || !strings.Contains(r.out, "fix: the id nope-a2b6f8jk") {
		t.Errorf("worklist = %+v", r)
	}
	if r := run(t, dir, v, "review", "--ai"); r.code != ExitError || !strings.Contains(r.err, "[review] command") {
		t.Errorf("ai without command = %+v", r)
	}
	// The command runs under [run] shell; one that is absent is said so.
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[run]\nshell = \"ds-no-such-shell\"\n[review]\ncommand = \"true\"\n")
	if r := run(t, dir, v, "review", "--ai"); r.code != ExitError || !strings.Contains(r.err, "the shell is not on PATH: ds-no-such-shell") {
		t.Errorf("ai without a shell = %+v", r)
	}
	// The model command receives the JSON request and returns a patch; the
	// patch goes to stdout or --out. Acks are untouched.
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[review]\ncommand = \"tee request.json >/dev/null; printf -- '--- a/docs/sessions.md\\\\n+++ b/docs/sessions.md\\\\n'\"\n")
	r = run(t, dir, v, "review", "--ai")
	if r.code != 0 || !strings.HasPrefix(r.out, "--- a/docs/sessions.md") {
		t.Errorf("ai review = %+v", r)
	}
	req, _ := os.ReadFile(filepath.Join(dir, "request.json"))
	if !strings.Contains(string(req), `"instructions"`) || !strings.Contains(string(req), `"unacked"`) || !strings.Contains(string(req), `"context"`) || !strings.Contains(string(req), "sessions.Insert") {
		t.Errorf("request = %.400s", req)
	}
	if _, _, acks, _ := NewStore(dir).LoadState(); len(acks.Rows) != 0 {
		t.Error("review must never ack")
	}
	patch := filepath.Join(dir, "review.patch")
	if r := run(t, dir, v, "review", "--ai", "--out", patch); r.code != 0 {
		t.Errorf("review to file = %+v", r)
	}
	if b, err := os.ReadFile(patch); err != nil || !strings.HasPrefix(string(b), "--- a/") {
		t.Errorf("patch file = %q %v", b, err)
	}
	// A relative --out is under the repository, not the process's directory.
	if r := run(t, dir, v, "review", "--ai", "--out", "rel.patch"); r.code != 0 {
		t.Errorf("review to a relative file = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, "rel.patch")); err != nil {
		t.Errorf("relative patch not under the repository: %v", err)
	}
	if r := run(t, dir, v, "review", "--ai", "--out", filepath.Join(dir, "nodir", "x.patch")); r.code != ExitError {
		t.Errorf("unwritable patch = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[review]\ncommand = \"echo boom >&2; exit 3\"\n")
	if r := run(t, dir, v, "review", "--ai"); r.code != ExitError || !strings.Contains(r.err, "boom") {
		t.Errorf("failing command = %+v", r)
	}
	// Nothing to review, a broken tree, and an uninitialised repo.
	clean := t.TempDir()
	write(t, clean, "docs/a.md", "text\n")
	write(t, clean, ".ds/config.toml", "[scan]\ndocs = [\"docs/**\"]\n")
	if r := run(t, clean, v, "review"); r.code != 0 || !strings.Contains(r.out, "nothing to review") {
		t.Errorf("clean review = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"[\"]\n")
	if r := run(t, dir, v, "review"); r.code != ExitError {
		t.Errorf("bad glob = %+v", r)
	}
	if r := run(t, t.TempDir(), v, "review"); r.code != ExitError {
		t.Errorf("uninitialised = %+v", r)
	}
}
