package check

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/internal/textnorm"
	"github.com/ubgo/docsync/render"
)

// A repo-mode copy in docs/ written by a build whose links were relative to
// the repository root is accepted as it is: no hand touched it and its
// content is current, and an upgrade must not turn every such copy into an
// error at once; the next refresh rewrites its links (bug 64). An edited
// copy is still tampered.
func TestRepoModeRootRelativeCopyIsAccepted(t *testing.T) {
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
	rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{d}, Refs: []block.Reference{mk(1, current), mk(2, legacy), mk(3, strings.Replace(legacy, "func Save() {}", "func Save() { edited() }", 1))}})
	got := map[int]State{}
	for _, f := range rep.Findings {
		got[f.Line] = f.State
	}
	if got[1] != StateOK || got[2] != StateOK || got[3] != StateTampered {
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

// A def added to a member inside a cited block moves the block's lines and
// puts a directive in its code without changing its hash. The copy that
// shows the old lines is stale, for refresh, not tampered: no hand touched
// it. A copy whose code was edited is still tampered.
func TestCopyWithMovedLinesIsStaleNotTampered(t *testing.T) {
	t.Parallel()
	before := def("scan-k7m2p4xq", "a.go", 3, "type S interface {\n\tB() int\n}", nil)
	before.Symbol = "S"
	before.Pos.End = 5
	after := before
	after.Content = "type S interface {\n\t// ds:def id=s-b-h3v8n2wd\n\tB() int\n}"
	after.Pos.End = 6
	at := block.Reference{ID: before.ID, Args: map[string]string{"id": before.ID}, Pos: block.Position{File: "", Start: 1}}
	old, _, _ := render.Fragment(before, at, "ds", 40)
	mk := func(line int, text string) block.Reference {
		r := ref("block", after.ID, "a.md", line, nil)
		r.Carrier = block.CarrierBlock
		r.SetSentence("")
		r.Region = &block.Region{Start: line + 1, End: line + 6, Hash: textnorm.Short(after.Hash), Text: text}
		return r
	}
	rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{after}, Refs: []block.Reference{mk(1, old), mk(2, strings.Replace(old, "B() int", "B() string", 1))}})
	got := map[int]State{}
	for _, f := range rep.Findings {
		got[f.Line] = f.State
	}
	if got[1] != StateStale || got[2] != StateTampered {
		t.Errorf("states = %v", got)
	}
}

// TestCodeStillInBlock pins the rule that tells a moved copy from an edited
// one: every code line the copy shows is still in the block, in order,
// directive lines aside on both sides.
func TestCodeStillInBlock(t *testing.T) {
	t.Parallel()
	block := "func F() {\n\t// ds:def id=x-h3v8n2wd\n\ta := 1\n\tb := 2\n\treturn a + b\n}\n\n"
	fence := func(lines ...string) string {
		return "**F** · [`a.go:1-6`](a.go#L1-L6)\n\n```go\n" + strings.Join(lines, "\n") + "\n```\n"
	}
	for _, c := range []struct {
		name, copyText, content string
		want                    bool
	}{
		{"the whole block, before the directive was added", fence("func F() {", "\ta := 1", "\tb := 2", "\treturn a + b", "}"), block, true},
		{"a lines= window that now ends earlier", fence("func F() {", "\ta := 1", "\tb := 2"), block, true},
		{"a line the block does not have", fence("func F() {", "\ta := 9"), block, false},
		{"lines out of order", fence("\tb := 2", "\ta := 1"), block, false},
		{"no fence", "**F** only", block, false},
		{"no content to compare with", fence("func F() {"), "", false},
	} {
		if got := codeStillInBlock(c.copyText, c.content, "ds"); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
