package render

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
)

// SnapshotBlock gives an at= snapshot the body and the lines it had at that
// commit, in block position and in a link (bug 66).
func TestSnapshotBlock(t *testing.T) {
	t.Parallel()
	old := save
	old.Content = "func Save() {}"
	old.Pos = block.Position{File: save.Pos.File, Start: 3, End: 3}
	opts := Options{SnapshotBlock: func(id, sha string) (block.Block, bool) { return old, sha == "c1" }}
	src := "<!-- ds:block id=" + save.ID + " at=c1 -->\n[l](ds:block?id=" + save.ID + "&at=c1)\n<!-- ds:block id=" + save.ID + " at=c2 -->\n[l](ds:block?id=" + save.ID + "&at=c2)"
	out, notes := Render(Input{Doc: "x.md", Src: []byte(src), Defs: []block.Block{save}}, opts)
	want := "**sess-save-k7m2p4xq** · [`internal/store/write.go:3`](internal/store/write.go#L3-L3) · as of `c1`\n\n```go\nfunc Save() {}\n```\n" +
		"[l](internal/store/write.go#L3-L3)\n" +
		"**sess-save-k7m2p4xq** · [`internal/store/write.go:12-15`](internal/store/write.go#L12-L15) · as of `c2`\n" +
		"[l](internal/store/write.go#L12-L15)"
	if string(out) != want || len(notes) != 1 || !strings.Contains(notes[0].Message, "no snapshot for "+save.ID+" at c2") {
		t.Errorf("snapshot block =\n%s\nnotes %v", out, notes)
	}
}

// The default link is relative to the page, because that is what a link in
// a page is resolved against; a root-relative path from docs/ pointed one
// directory too deep (bug 64).
func TestRelativeTo(t *testing.T) {
	t.Parallel()
	cases := []struct{ doc, file, want string }{
		{"README.md", "billing/x.go", "billing/x.go"},
		{"docs/billing.md", "billing/x.go", "../billing/x.go"},
		{"docs/a/page.md", "billing/x.go", "../../billing/x.go"},
		{"docs/a/page.md", "docs/a/y.go", "y.go"},
		{"docs/a/page.md", "docs/b/y.go", "../b/y.go"},
		{"docs/page.md", "docs2/y.go", "../docs2/y.go"},
		{"docs/page.md", "docs", "../docs"},
		{"", "x.go", "x.go"},
	}
	for _, c := range cases {
		if got := relativeTo(c.doc, c.file); got != c.want {
			t.Errorf("relativeTo(%q, %q) = %q, want %q", c.doc, c.file, got, c.want)
		}
	}
	src := []byte("[s](ds:block?id=" + save.ID + ")")
	out, _ := Render(Input{Doc: "docs/x.md", Src: src, Defs: []block.Block{save}}, Options{})
	if string(out) != "[s](../internal/store/write.go#L12-L15)" {
		t.Errorf("default link from docs/ = %q", out)
	}
	// A block merged from another repository has no path in this one: with
	// no ForeignLink it is named, not linked (bug 105).
	foreign := save
	foreign.Args = map[string]string{block.KeyRepo: "api"}
	out, _ = Render(Input{Doc: "docs/x.md", Src: src, Defs: []block.Block{foreign}}, Options{})
	if string(out) != "s (in api)" {
		t.Errorf("foreign link from docs/ = %q", out)
	}
	// {file} stays root-relative, for templates that build an absolute URL.
	out, _ = Render(Input{Doc: "docs/x.md", Src: src, Defs: []block.Block{save}}, Options{Permalink: "https://h/{file}|{rel}"})
	if string(out) != "[s](https://h/internal/store/write.go|../internal/store/write.go)" {
		t.Errorf("template = %q", out)
	}
}
