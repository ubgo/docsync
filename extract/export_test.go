package extract

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
)

func TestExportedHooks(t *testing.T) {
	t.Parallel()
	src := "package p\n\n// ds:def id=a-b3c7g9kl\nfunc A() {}\n\n// ds:block id=a-b3c7g9kl\n// see ds:cfg?id=x-c4d8h2lm\n// ds:def\n// ds:def id=r-d5e9j3mn file=x.json pick=json:v\n// ds:def id=s-e6f2k4np span=bad\nvar x = 1 // ds:def id=t-f7g3l5pq\n"
	var f Found
	lines, defs := ScanCode("a.go", []byte(src), "ds", &f)
	if len(lines) != 11 || len(defs) != 5 || len(f.Refs) != 2 || len(f.Problems) != 0 {
		t.Fatalf("scan = %d lines, %d defs, %d refs, %d problems", len(lines), len(defs), len(f.Refs), len(f.Problems))
	}
	id, span, hasSpan, ok := Prelude(defs[0], &f)
	if !ok || id != "a-b3c7g9kl" || span != 0 || hasSpan {
		t.Errorf("prelude plain = %q %d %v %v", id, span, hasSpan, ok)
	}
	if _, _, _, ok := Prelude(defs[1], &f); ok || len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, ErrNoID) {
		t.Errorf("prelude no id = %v %+v", ok, f.Problems)
	}
	if _, _, _, ok := Prelude(defs[2], &f); ok || len(f.Defs) != 1 || !f.Defs[0].Remote {
		t.Errorf("prelude remote = %v %+v", ok, f.Defs)
	}
	if _, _, _, ok := Prelude(defs[3], &f); ok || len(f.Problems) != 2 || !errors.Is(f.Problems[1].Err, ErrBadSpan) {
		t.Errorf("prelude bad span = %v %+v", ok, f.Problems)
	}
	if !defs[4].Trailing || defs[4].Code != "var x = 1" {
		t.Errorf("trailing = %+v", defs[4])
	}
	d := NewDef(defs[0], "a-b3c7g9kl", block.KindFunc, "A", block.Position{Start: 4, End: 4}, Join(lines, 4, 4))
	if d.Block.Content != "func A() {}" || d.Block.Symbol != "A" || d.Block.DirectivePos.Start != 3 || d.Block.Hash == "" {
		t.Errorf("newdef = %+v", d.Block)
	}
	NothingToBind(defs[0], &f)
	if !errors.Is(f.Problems[2].Err, ErrNothingToBind) {
		t.Errorf("nothing to bind = %+v", f.Problems)
	}
	// A path without a comment style has nothing to scan.
	if lines, defs := ScanCode("x.unknownext", []byte("ds:def id=a\n"), "ds", &f); len(lines) != 1 || defs != nil {
		t.Errorf("unknown style = %v %v", lines, defs)
	}
}

// TestFirstCodeLineAndSkippedCode covers the seam a tier with its own parser
// uses to check that the block it chose begins where the directive points. It
// lives here because only the treesitter module calls it, and the invariant it
// guards belongs to this package: a directive binds the declaration below it or
// reports, never something further down (bug 18).
func TestFirstCodeLineAndSkippedCode(t *testing.T) {
	t.Parallel()
	lines := []string{"package p", "", "// a comment", "@decorator", "func F() {}", ""}
	// Blanks, comments and decorators are skipped; the first real code line is
	// what a directive on line 2 must bind.
	if got := FirstCodeLine("a.go", lines, 2); got != 5 {
		t.Errorf("FirstCodeLine = %d, want 5", got)
	}
	// Nothing below is 0, which callers read as "no opinion" rather than as
	// line zero.
	if got := FirstCodeLine("a.go", lines, 6); got != 0 {
		t.Errorf("past the end = %d, want 0", got)
	}
	// A path with no comment style cannot tell code from prose, so it returns
	// the line it was given rather than guessing and rejecting a valid bind.
	if got := FirstCodeLine("x.unknownext", lines, 3); got != 3 {
		t.Errorf("unknown style = %d, want 3", got)
	}
	var f Found
	_, defs := ScanCode("a.go", []byte("package p\n\n// ds:def id=a-b3c7g9kl\nfunc A() {}\n"), "ds", &f)
	SkippedCode(defs[0], &f, 9)
	if len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, ErrSkippedCode) {
		t.Fatalf("problems = %+v", f.Problems)
	}
	// The line it refused is in the message and the directive is the position,
	// so the reader is sent to the directive and told what it hit.
	if !strings.Contains(f.Problems[0].Err.Error(), "line 9") || f.Problems[0].Pos.Start != 3 {
		t.Errorf("skipped = %v at %d", f.Problems[0].Err, f.Problems[0].Pos.Start)
	}
}
