package render

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
)

// TestForeignBlocksLinkIntoTheirRepository pins bug 105's links: a block
// another repository defines was linked relative to this repository, a file
// that is not here. It now takes ForeignLink's URL, or names the repository
// with no link at all.
func TestForeignBlocksLinkIntoTheirRepository(t *testing.T) {
	t.Parallel()
	b := def("ret-a2b6f8jk", "spec/retries.md", 4, "five", map[string]string{block.KeyRepo: "docs"})
	b.Symbol = "Backoff"
	src := "<!-- ds:block id=ret-a2b6f8jk -->\nSee [it](ds:block?id=ret-a2b6f8jk).\n<!-- ds:chain id=ret-a2b6f8jk -->\n"
	out, _ := Render(Input{Src: []byte(src), Defs: []block.Block{b}}, Options{})
	for _, want := range []string{"**Backoff** · `spec/retries.md:4` (in docs)", "See it (in docs).", "— spec/retries.md:4 (in docs)"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("no foreign link: lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "](spec/retries.md") {
		t.Errorf("linked relative to this repository:\n%s", out)
	}
	link := func(b block.Block) (string, bool) { return "https://x/" + b.Pos.File, b.Args[block.KeyRepo] == "docs" }
	out, _ = Render(Input{Src: []byte(src), Defs: []block.Block{b}}, Options{ForeignLink: link})
	if strings.Count(string(out), "(https://x/spec/retries.md)") != 3 {
		t.Errorf("foreign link:\n%s", out)
	}
	other := def("ret-a2b6f8jk", "spec/retries.md", 4, "five", map[string]string{block.KeyRepo: "web"})
	if out, _ := Render(Input{Src: []byte(src), Defs: []block.Block{other}}, Options{ForeignLink: link}); !strings.Contains(string(out), "(in web)") {
		t.Errorf("a refused foreign link names the repository:\n%s", out)
	}
	// Link, the escape hatch, still decides everything.
	out, _ = Render(Input{Src: []byte(src), Defs: []block.Block{b}}, Options{ForeignLink: link, Link: func(block.Block) string { return "L" }})
	if strings.Count(string(out), "(L)") != 3 {
		t.Errorf("Link must win:\n%s", out)
	}
	// A copy never links into another repository: a frozen check, which
	// does not read the workspace file, must expect the same text.
	frag, ok, _ := Fragment(b, block.Reference{Args: map[string]string{block.KeyID: b.ID}}, "ds", 40)
	if !ok || !strings.HasPrefix(frag, "**Backoff** · `spec/retries.md:4` (in docs)\n\n```markdown\nfive\n```") {
		t.Errorf("foreign fragment = %q", frag)
	}
}

// TestRenderReplacesARepoModeCopy pins bug 105's duplicate: rendering a page
// that already held a repo-mode copy printed the block twice and left the
// closer comment in the page. The rendering replaces the copy; a cite that
// cannot render keeps the copy as written.
func TestRenderReplacesARepoModeCopy(t *testing.T) {
	t.Parallel()
	b := def("ok-a2b6f8jk", "x.go", 1, "body", nil)
	b.Symbol = "X"
	src := "# P\n<!-- ds:block id=ok-a2b6f8jk -->\n**X** · old\n\n```go\nold\n```\n<!-- /ds:block hash=abc123 -->\nafter\n<!-- ds:block id=gone-a2b6f8jk -->\nkept copy\n<!-- /ds:block hash=def456 -->\n"
	out, _ := Render(Input{Src: []byte(src), Defs: []block.Block{b}}, Options{})
	got := string(out)
	if strings.Count(got, "**X**") != 1 || strings.Contains(got, "old") || strings.Contains(got, "hash=abc123") || !strings.Contains(got, "```go\nbody\n```\nafter\n") {
		t.Errorf("copy not replaced:\n%s", got)
	}
	if !strings.Contains(got, "<!-- ds:block id=gone-a2b6f8jk -->\nkept copy\n<!-- /ds:block hash=def456 -->") {
		t.Errorf("an unrenderable cite must keep its copy:\n%s", got)
	}
}
