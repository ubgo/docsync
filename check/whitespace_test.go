package check

import (
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/ledger"
)

// TestWhitespaceIsNeverReported pins §19's `whitespace` class: formatting
// only, never reported. Trailing whitespace is normalised out of the hash in
// every tier, so a citation measured against a baseline taken before it stays
// ok. Re-indentation is not, in a block with no grammar: indentation is
// content where it can be meaning (YAML, Python), and only a syntax tier that
// hashes the token stream can tell it is not -- scripts/e2e/promises.sh pins
// that half against the real Go tier. A real edit is run through the same
// setup, so the test is seen to be able to fail.
// promise:whitespace-silent
func TestWhitespaceIsNeverReported(t *testing.T) {
	t.Parallel()
	old := def("depth-k7m2p4xq", "a.go", 3, "func Depth() int {\n\treturn 12\n}", nil)
	for _, c := range []struct {
		name, body string
		flags      bool
	}{
		{"reindented, no grammar", "func Depth() int {\n    return 12\n}", true},
		{"trailing spaces", "func Depth() int {   \n\treturn 12  \n}", false},
		{"a real edit", "func Depth() int {\n\treturn 6\n}", true},
	} {
		nw := def(old.ID, "a.go", 3, c.body, nil)
		rep := Run(Input{
			Repo: "api", Now: now, Defs: []block.Block{nw},
			Refs:     []block.Reference{ref("block", nw.ID, "docs/a.md", 5, nil)},
			Prev:     ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", nw)}},
			PrevRefs: ledger.Refs{Rows: []ledger.RefRow{{ID: nw.ID, Repo: "api", Doc: "docs/a.md", Line: 5, SeenHash: old.Hash}}},
			Opts:     Options{BodyAt: bodies(old, nw)},
		})
		if got := rep.ExitCode == 1; got != c.flags {
			t.Errorf("%s: reported = %v, want %v: %+v", c.name, got, c.flags, rep.Findings)
		}
	}
}
