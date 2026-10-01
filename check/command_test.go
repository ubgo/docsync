package check

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/ledger"
)

// TestCommandText pins the rule behind bug 126: a command is renamed, a
// directive, a flag-less word ending in "ds", and an empty or default name
// are not.
func TestCommandText(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"ds ack x --doc d":                   "pds ack x --doc d",
		"run `ds sync` and commit":           "run `pds sync` and commit",
		"re-add the ds:def (ds def --fix)":   "re-add the ds:def (pds def --fix)",
		"it needs review; builds ds-resolve": "it needs review; builds ds-resolve",
		"run ds --help":                      "run pds --help",
	} {
		if got := CommandText(in, "pds"); got != want {
			t.Errorf("CommandText(%q) = %q, want %q", in, got, want)
		}
	}
	for _, name := range []string{"", DefaultCommand} {
		if got := CommandText("ds ack", name); got != "ds ack" {
			t.Errorf("name %q changed the text: %q", name, got)
		}
	}
}

// TestFindingsNameTheCommand pins that Options.Command reaches every text
// field of a finding, including the two-way remedy of an unacked one.
func TestFindingsNameTheCommand(t *testing.T) {
	t.Parallel()
	old := def("save-k7m2p4xq", "a.go", 3, "func Save() {\n\treturn 1\n}", nil)
	nw := def(old.ID, "a.go", 3, "func Save() {\n\treturn 2\n}", nil)
	in := Input{
		Repo: "api", Now: now, Defs: []block.Block{nw}, Refs: []block.Reference{ref("block", nw.ID, "docs/a.md", 5, nil)},
		PrevRefs: ledger.Refs{Rows: []ledger.RefRow{{ID: nw.ID, Repo: "api", Doc: "docs/a.md", Line: 5, SeenHash: old.Hash}}},
		Opts:     Options{Command: "pds"},
	}
	f := one(t, Run(in), StateUnacked)
	if !strings.Contains(f.Remedy.IfStillTrue, "pds ack") {
		t.Errorf("remedy = %+v, want pds ack", f.Remedy)
	}
}
