package check

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/internal/textnorm"
	"github.com/ubgo/docsync/render"
)

// A repo-mode copy in docs/ written by a build whose links were relative to
// the repository root is stale, for refresh to rewrite, not tampered: no
// hand touched it (bug 64). An edited copy is still tampered.
func TestRepoModeRootRelativeCopyIsStale(t *testing.T) {
	t.Parallel()
	d := def("save-k7m2p4xq", "a.go", 3, "func Save() {}", nil)
	d.Symbol = "Save"
	at := func(doc string) block.Reference {
		return block.Reference{ID: d.ID, Args: map[string]string{"id": d.ID}, Pos: block.Position{File: doc, Start: 1}}
	}
	legacy, _, _ := render.Fragment(d, at(""), "ds", 40)
	current, _, _ := render.Fragment(d, at("docs/d.md"), "ds", 40)
	if legacy == current || !strings.Contains(current, "(../a.go#L3-L3)") {
		t.Fatalf("the two forms should differ: %q / %q", legacy, current)
	}
	mk := func(line int, text string) block.Reference {
		r := ref("block", d.ID, "docs/d.md", line, nil)
		r.Carrier = block.CarrierBlock
		r.SetSentence("")
		r.Region = &block.Region{Start: line + 1, End: line + 5, Hash: textnorm.Short(d.Hash), Text: text}
		return r
	}
	rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{d}, Refs: []block.Reference{mk(1, current), mk(2, legacy), mk(3, strings.Replace(legacy, "Save", "Edited", 1))}})
	got := map[int]State{}
	for _, f := range rep.Findings {
		got[f.Line] = f.State
	}
	if got[1] != StateOK || got[2] != StateStale || got[3] != StateTampered {
		t.Errorf("states = %v", got)
	}
}

// AboutHash changes with any hash of any listed id and with nothing else
// (bug 72).
func TestAboutHash(t *testing.T) {
	t.Parallel()
	a := def("a-k7m2p4xq", "a.go", 1, "x", nil)
	b := def("b-h3v8n2wd", "b.go", 1, "y", nil)
	other := def("c-t4k2b9rf", "c.go", 1, "z", nil)
	if AboutHash(nil, []block.Block{a}) != "" {
		t.Error("no ids, no hash")
	}
	h := AboutHash([]string{a.ID}, []block.Block{a, other})
	if h == "" || h != AboutHash([]string{a.ID}, []block.Block{other}, []block.Block{a}) {
		t.Error("the same blocks in another order or set must hash the same")
	}
	changed := a
	changed.Hash = "different"
	if h == AboutHash([]string{a.ID}, []block.Block{changed, other}) {
		t.Error("a changed listed block must change the hash")
	}
	otherChanged := other
	otherChanged.Hash = "different"
	if h != AboutHash([]string{a.ID}, []block.Block{a, otherChanged}) {
		t.Error("an unlisted block must not change the hash")
	}
	if h == AboutHash([]string{a.ID, b.ID}, []block.Block{a, b}) {
		t.Error("another id listed is another claim")
	}
	r := block.Reference{Verb: "claim", Args: map[string]string{"about": "[a-k7m2p4xq],b-h3v8n2wd"}}
	if ids := ClaimAbout(r); len(ids) != 2 || ids[0] != a.ID || ids[1] != b.ID {
		t.Errorf("ClaimAbout = %v", ids)
	}
}
