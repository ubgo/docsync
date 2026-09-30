package check

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/scan"
)

// TestDuplicateIDIsAHardError pins Part VII (edge-case rulebook): a duplicated def is an error, never
// a warning, and the remedy names `ds def --fix` rather than the generic
// "fix the directive", because that command is the one way out the spec
// gives and it never guesses which copy is the original.
// promise:duplicate-def
func TestDuplicateIDIsAHardError(t *testing.T) {
	t.Parallel()
	p := scan.Problem{Pos: block.Position{File: "src/w.go", Start: 3}, Err: fmt.Errorf("%w: x-k7m2p4xq (2 places)", scan.ErrDuplicateID)}
	rep := Run(Input{Repo: "api", Now: now, Problems: []scan.Problem{p}})
	f := one(t, rep, StateProblem)
	if f.Severity != SeverityError || rep.ExitCode != 1 {
		t.Errorf("duplicate id = %s, exit %d; want error, exit 1", f.Severity, rep.ExitCode)
	}
	if !strings.Contains(f.Remedy.Fix, "ds def --fix") || !strings.Contains(f.Remedy.Fix, "src/w.go:3") {
		t.Errorf("remedy %q must name ds def --fix and the location", f.Remedy.Fix)
	}
}
