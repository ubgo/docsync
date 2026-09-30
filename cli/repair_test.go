package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/docsync"
)

// TestRepairCommand covers `ds repair` end to end: it prints by default and
// writes only with --apply; in a format with comments it comments the damaged
// line, and in one without it deletes it; and the whole write is journaled so
// `ds undo` puts every file back byte for byte, deletions included.
func TestRepairCommand(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	// Damage exactly as an older `ds def` left it: a Go workspace, and a JSON
	// file that no longer parses because a directive line sits on top of it.
	damagedWork := "ds:def id=w-t4k2b9rf\ngo 1.26\n\nuse .\n"
	damagedJSON := "ds:def id=j-k7m2p4xq\n{\"a\": 1}\n"
	write(t, dir, "go.work", damagedWork)
	write(t, dir, "p.json", damagedJSON)

	// Default: print, change nothing.
	r := run(t, dir, v, "repair")
	if r.code != 0 || !strings.Contains(r.out, "// ds:def id=w-t4k2b9rf") || !strings.Contains(r.out, "--apply") {
		t.Fatalf("repair = %+v", r)
	}
	if !strings.Contains(r.out, "p.json:1") || !strings.Contains(r.out, "delete") {
		t.Errorf("the JSON line must be proposed for deletion: %s", r.out)
	}
	for f, want := range map[string]string{"go.work": damagedWork, "p.json": damagedJSON} {
		if got, _ := os.ReadFile(filepath.Join(dir, f)); string(got) != want {
			t.Fatalf("repair without --apply changed %s:\n%s", f, got)
		}
	}
	// --json is the same proposal for a script: one comment, one delete.
	r = run(t, dir, v, "repair", "--json")
	var proposed docsync.RepairResult
	if err := json.Unmarshal([]byte(r.out), &proposed); err != nil || len(proposed.Edits) != 2 || len(proposed.Manual) != 0 {
		t.Fatalf("repair --json = %+v (%v)", r, err)
	}
	// --apply writes.
	r = run(t, dir, v, "repair", "--apply")
	if r.code != 0 || !strings.Contains(r.out, "2 line(s)") {
		t.Fatalf("repair --apply = %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "go.work")); string(got) != "// ds:def id=w-t4k2b9rf\ngo 1.26\n\nuse .\n" {
		t.Errorf("go.work after repair:\n%s", got)
	}
	// The JSON is valid again: the line is gone and the rest is untouched.
	got, _ := os.ReadFile(filepath.Join(dir, "p.json"))
	var doc map[string]int
	if string(got) != "{\"a\": 1}\n" || json.Unmarshal(got, &doc) != nil || doc["a"] != 1 {
		t.Errorf("p.json after repair is not the original JSON:\n%s", got)
	}
	// Running it again finds nothing.
	if r := run(t, dir, v, "repair"); !strings.Contains(r.out, "nothing to repair") {
		t.Errorf("a second repair = %s", r.out)
	}
	// undo puts both files back exactly as they were found, the deleted line
	// included.
	if r := run(t, dir, v, "undo"); r.code != 0 {
		t.Fatalf("undo = %+v", r)
	}
	for f, want := range map[string]string{"go.work": damagedWork, "p.json": damagedJSON} {
		if got, _ := os.ReadFile(filepath.Join(dir, f)); string(got) != want {
			t.Errorf("undo did not restore %s:\n%q\nwant\n%q", f, got, want)
		}
	}
	// A file it cannot write fails the command rather than reporting success.
	if os.Getuid() != 0 {
		write(t, dir, "go.work", damagedWork)
		_ = os.Chmod(filepath.Join(dir, "go.work"), 0o444)
		if r := run(t, dir, v, "repair", "--apply"); r.code != ExitError {
			t.Errorf("repair on a read-only file = %+v", r)
		}
		_ = os.Chmod(filepath.Join(dir, "go.work"), 0o644)
	}
	// With nothing damaged, it says so and exits clean.
	clean, cv := initialised(t)
	if r := run(t, clean, cv, "repair"); r.code != 0 || !strings.Contains(r.out, "nothing to repair") {
		t.Errorf("repair on a clean tree = %+v", r)
	}
}

// TestPrintRepair checks every shape of a proposal as it is printed, including
// the damage left for a person, which a single run cannot produce on demand.
func TestPrintRepair(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	printRepair(&b, docsync.RepairResult{
		Edits: []docsync.Edit{
			{File: "go.work", Line: 1, Old: "ds:def id=a", New: "// ds:def id=a"},
			{File: "p.json", Line: 1, Old: "ds:def id=b", Delete: true, Next: "{}"},
		},
		Manual: []docsync.ManualFix{{File: "gone.go", Line: 3, Reason: "could not be read to repair: missing"}},
	})
	for _, want := range []string{"go.work:1\n  - ds:def id=a\n  + // ds:def id=a", "p.json:1  delete", "by hand: gone.go:3  could not be read to repair: missing"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("printRepair must show %q:\n%s", want, b.String())
		}
	}
}
