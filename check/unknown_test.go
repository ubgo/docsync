package check

import (
	"slices"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/ledger"
)

// TestMissingBodyIsUnknownNeverBody pins §19: a drift whose older body the
// store cannot supply -- pruned, never stored, or withheld because the block is
// secret -- is `unknown`, and must never be recorded as `body`. `api`
// stability does not flag a body change, so a missing body read as `body`
// would turn a possible signature change into a silent ok. With the body
// present the same drift is classified for real, so the test can fail.
// promise:unknown-not-body
func TestMissingBodyIsUnknownNeverBody(t *testing.T) {
	t.Parallel()
	api := map[string]string{block.KeyStability: string(block.StabilityAPI)}
	old := def("save-k7m2p4xq", "a.go", 3, "func Save() error {\n\treturn nil\n}", api)
	nw := def(old.ID, "a.go", 3, "func Save(force bool) error {\n\treturn nil\n}", map[string]string{block.KeyStability: string(block.StabilityAPI)})
	run := func(withOldBody bool) Report {
		have := bodies(nw)
		if withOldBody {
			have = bodies(old, nw)
		}
		return Run(Input{
			Repo: "api", Now: now, Defs: []block.Block{nw},
			Refs:     []block.Reference{ref("block", nw.ID, "docs/a.md", 5, nil)},
			Prev:     ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", nw)}},
			PrevRefs: ledger.Refs{Rows: []ledger.RefRow{{ID: nw.ID, Repo: "api", Doc: "docs/a.md", Line: 5, SeenHash: old.Hash}}},
			// The Go grammar the binary supplies: without it no tier can tell
			// a signature from a body, and the control below would prove nothing.
			Opts: Options{BodyAt: have, CommentPrefixes: func(string) []string { return []string{"//"} }},
		})
	}
	missing := run(false)
	for _, f := range missing.Findings {
		if slices.Contains(f.Classes, block.ClassBody) {
			t.Errorf("a drift with no older body was recorded as body: %+v", f)
		}
		if f.State == StateOK {
			t.Errorf("a drift with no older body read as ok under api stability: %+v", f)
		}
	}
	if len(missing.Findings) == 0 || !slices.Contains(missing.Findings[0].Classes, block.ClassUnknown) {
		t.Errorf("want one finding classed unknown, got %+v", missing.Findings)
	}
	// With the body, the signature change is seen for what it is.
	if with := run(true); len(with.Findings) == 0 || !slices.Contains(with.Findings[0].Classes, block.ClassSignature) {
		t.Errorf("with the older body the change must classify as signature: %+v", with.Findings)
	}
}
