package extract

import (
	"errors"
	"reflect"
	"testing"

	"github.com/ubgo/docsync/block"
)

const mdSrc = `---
title: Sessions
ds:
  covers: [a]
---

# Sessions

<!-- ds:def id=sec-aaaaaaaa -->
## Session policy

Sessions live thirty days. See [dec](ds:block?id=dec-bbbbbbbb&lines=1-4) for why.

` + "```" + `md
<!-- ds:def id=inside-fence -->
[x](ds:block?id=inside-fence)
` + "```" + `

### Sub heading

Deep text.

## Other

<!-- ds:def id=para-cccccccc owner=@x -->
A paragraph that is defined
across two lines.

Port [8081](ds:def?id=port-dddddddd&type=int) and host [https://x.dev](ds:def?id=host-eeeeeeee&type=url). Not a def: ![img](ds:def?id=nope).

<!-- ds:block id=sec-aaaaaaaa lines=1-3 -->

Design choice. <!-- ds:claim owner=@p reviewed=2026-09-06 expires=90d -->

<!-- ds:def id=span-ffffffff span=+1 -->
line one
line two

<!-- ds:def id=el-gggggggg -->
<div class="x">
  <p>inner</p>
</div>

<!-- ds:def id=remote-hhhhhhhh file=a.json pick=json:$.x -->
<!-- ds:def id=badspan-iiiiiiii span=x -->
text
<!-- ds:def -->
text
[y](ds:Bad?id=1)
<!-- ds:def id=tail-jjjjjjjj -->
`

func TestMarkdownExtract(t *testing.T) {
	t.Parallel()
	f := Markdown{}.Extract("docs/sessions.md", []byte(mdSrc), "ds")
	d := defsByID(f)
	cases := map[string]struct {
		kind       block.Kind
		symbol     string
		start, end int
		content    string
	}{
		"sec-aaaaaaaa":    {block.KindSection, "Session policy", 10, 21, ""},
		"para-cccccccc":   {block.KindParagraph, "", 26, 27, "A paragraph that is defined\nacross two lines."},
		"port-dddddddd":   {block.KindLinkText, "8081", 29, 29, "8081"},
		"host-eeeeeeee":   {block.KindLinkText, "https://x.dev", 29, 29, "https://x.dev"},
		"span-ffffffff":   {block.KindSpan, "", 36, 37, "line one\nline two"},
		"el-gggggggg":     {block.KindElement, "div", 40, 42, "<div class=\"x\">\n  <p>inner</p>\n</div>"},
		"remote-hhhhhhhh": {"", "", 0, 0, ""},
	}
	if len(d) != len(cases) {
		ids := []string{}
		for id := range d {
			ids = append(ids, id)
		}
		t.Fatalf("defs = %v", ids)
	}
	for id, w := range cases {
		got := d[id]
		if id == "remote-hhhhhhhh" {
			if !got.Remote {
				t.Error("remote def not flagged")
			}
			continue
		}
		if got.Block.Kind != w.kind || got.Block.Symbol != w.symbol || got.Block.Pos.Start != w.start || got.Block.Pos.End != w.end {
			t.Errorf("%s = %s %q %d-%d; want %s %q %d-%d", id, got.Block.Kind, got.Block.Symbol, got.Block.Pos.Start, got.Block.Pos.End, w.kind, w.symbol, w.start, w.end)
		}
		if w.content != "" && got.Block.Content != w.content {
			t.Errorf("%s content = %q", id, got.Block.Content)
		}
	}
	// The section must include the fenced code and the deeper heading but stop
	// before "## Other" and not include trailing blanks.
	if sec := d["sec-aaaaaaaa"].Block.Content; sec[:17] != "## Session policy" || sec[len(sec)-10:] != "Deep text." {
		t.Errorf("section content = %q", sec)
	}
	// References: the link to dec, the block-position block, the claim, and
	// nothing from inside the fence.
	byVerb := map[string][]block.Reference{}
	for _, r := range f.Refs {
		byVerb[r.Reference.Verb] = append(byVerb[r.Reference.Verb], r.Reference)
	}
	if len(byVerb["block"]) != 2 || len(byVerb["claim"]) != 1 || len(f.Refs) != 3 {
		t.Fatalf("refs = %+v", f.Refs)
	}
	for _, r := range byVerb["block"] {
		switch r.Carrier {
		case block.CarrierLink:
			if r.Sentence != "See dec for why." && r.Sentence != "See [dec](ds:block?id=dec-bbbbbbbb&lines=1-4) for why." {
				t.Errorf("link sentence = %q", r.Sentence)
			}
			if r.Args["lines"] != "1-4" || r.Pos.Start != 12 {
				t.Errorf("link ref = %+v", r)
			}
		case block.CarrierBlock:
			if r.Sentence != "" || r.Pos.Start != 31 {
				t.Errorf("block-position ref = %+v", r)
			}
		default:
			t.Errorf("unexpected carrier %s", r.Carrier)
		}
	}
	if c := byVerb["claim"][0]; c.Carrier != block.CarrierComment || c.Sentence != "Design choice." {
		t.Errorf("claim ref = %+v", c)
	}
	// Problems: def on an image, bad span, missing id, bad link verb, tail
	// with nothing after.
	if len(f.Problems) != 5 {
		t.Fatalf("problems = %+v", f.Problems)
	}
	seen := map[error]bool{}
	for _, p := range f.Problems {
		for _, s := range []error{ErrBadSpan, ErrNoID, ErrNothingToBind, ErrDefOnImage} {
			if errors.Is(p.Err, s) {
				seen[s] = true
			}
		}
	}
	if len(seen) != 4 {
		t.Errorf("problem kinds = %v", seen)
	}
}

func TestMarkdownInlineDefEdge(t *testing.T) {
	t.Parallel()
	// Missing id on an inline def is a problem; a remote inline def is remote.
	src := "[v](ds:def?type=int) and [w](ds:def?id=r-aaaaaaaa&file=x.json) and [plain](https://example.com).\n\n<!-- ds:def id=big-bbbbbbbb span=+99 -->\nlast line\n"
	f := Markdown{}.Extract("a.md", []byte(src), "ds")
	if len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, ErrNoID) {
		t.Errorf("problems = %+v", f.Problems)
	}
	d := defsByID(f)
	if len(d) != 2 || !d["r-aaaaaaaa"].Remote {
		t.Errorf("defs = %+v", f.Defs)
	}
	if big := d["big-bbbbbbbb"].Block; big.Pos.Start != 4 || big.Pos.End != 4 || big.Kind != block.KindSpan {
		t.Errorf("span clamped to end of file: %+v", big)
	}
	if len(f.Refs) != 0 {
		t.Errorf("a plain https link is not a reference: %+v", f.Refs)
	}
}

func TestMarkdownMDXAndHTML(t *testing.T) {
	t.Parallel()
	mdx := "import X from 'x'\n\n{/* ds:def id=h-aaaaaaaa */}\n## Heading\n\nbody\n\n<!-- ds:def id=p-bbbbbbbb -->\nAlt comment paragraph\n"
	f := Markdown{}.Extract("docs/page.mdx", []byte(mdx), "ds")
	d := defsByID(f)
	if d["h-aaaaaaaa"].Block.Kind != block.KindSection || d["p-bbbbbbbb"].Block.Kind != block.KindParagraph {
		t.Errorf("mdx defs = %+v", d)
	}
	html := "<html>\n<!-- ds:def id=hero-aaaaaaaa -->\n<section class=\"hero\">\n  <h1>Hi</h1>\n  <section><p>nested same tag</p></section>\n</section>\n<!-- ds:def id=br-bbbbbbbb -->\n<br/>\n<!-- ds:def id=text-cccccccc -->\nplain text in html\n\n<!-- ds:def id=open-dddddddd -->\n<p>never closed\nmore\n\nafter blank\n</html>\n"
	f = Markdown{}.Extract("site/index.html", []byte(html), "ds")
	d = defsByID(f)
	if h := d["hero-aaaaaaaa"]; h.Block.Kind != block.KindElement || h.Block.Symbol != "section" || h.Block.Pos.Start != 3 || h.Block.Pos.End != 6 {
		t.Errorf("hero = %+v", h.Block)
	}
	if b := d["br-bbbbbbbb"]; b.Block.Pos.Start != 8 || b.Block.Pos.End != 8 {
		t.Errorf("self-closing element = %+v", b.Block)
	}
	if tx := d["text-cccccccc"]; tx.Block.Kind != block.KindElement || tx.Block.Symbol != "" || tx.Block.Pos.End != 10 {
		t.Errorf("text in html falls back to blank-bounded: %+v", tx.Block)
	}
	if o := d["open-dddddddd"]; o.Block.Pos.Start != 13 || o.Block.Pos.End != 14 {
		t.Errorf("unclosed element falls back to blank-bounded: %+v", o.Block)
	}
	// In html files, a `#` line is not a heading.
	f = Markdown{}.Extract("a.html", []byte("<!-- ds:def id=x-aaaaaaaa -->\n# not heading\n"), "ds")
	if f.Defs[0].Block.Kind != block.KindElement || f.Defs[0].Block.Symbol != "" {
		t.Errorf("html hash line = %+v", f.Defs[0].Block)
	}
}

func TestFrontmatterPage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		src  string
		want *Page
	}{
		{"no frontmatter", "# T\n", nil},
		{"unterminated frontmatter", "---\nds:\n  covers: [a]\n", nil},
		{"frontmatter without ds", "---\ntitle: x\n---\n", nil},
		{"flow list", "---\ntitle: x\nds:\n  covers: [a-aaaaaaaa, \"b-bbbbbbbb\", 'c-cccccccc']\n  review_every: 180d\nother: 1\n---\n", &Page{Covers: []string{"a-aaaaaaaa", "b-bbbbbbbb", "c-cccccccc"}, ReviewEvery: "180d"}},
		{"block list", "---\nds:\n  # comment\n  covers:\n    - a-aaaaaaaa\n    - \"b-bbbbbbbb\"\n  review_every: \"90d\"\n---\n", &Page{Covers: []string{"a-aaaaaaaa", "b-bbbbbbbb"}, ReviewEvery: "90d"}},
		{"ds with unknown key and dots terminator", "---\nds:\n  extra: 1\n  covers: []\n  noval\n...\n", &Page{}},
		{"list item at ds indent is skipped", "---\nds:\n  extra:\n- x\n  review_every: 7d\n---\n", &Page{ReviewEvery: "7d"}},
		{"ds mapping ends at dedent", "---\nds:\n  covers: [a-aaaaaaaa]\nlater:\n  covers: [z]\n---\n", &Page{Covers: []string{"a-aaaaaaaa"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := frontmatterPage(splitLines([]byte(tc.src)))
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("frontmatterPage = %+v, want %+v", got, tc.want)
			}
		})
	}
	f := Markdown{}.Extract("docs/sessions.md", []byte(mdSrc), "ds")
	if f.Page == nil || !reflect.DeepEqual(f.Page.Covers, []string{"a"}) {
		t.Errorf("Extract page = %+v", f.Page)
	}
	if h := (Markdown{}).Extract("a.html", []byte("---\nds:\n  covers: [x]\n---\n"), "ds"); h.Page != nil {
		t.Error("html files never have frontmatter pages")
	}
}

func TestSectionEndAndMask(t *testing.T) {
	t.Parallel()
	lines := []string{"## A", "text", "```", "# in fence", "```", "~~~", "## also fenced", "~~~", "", "", "## B"}
	if got := sectionEnd(lines, 1, 2, nil); got != 8 {
		t.Errorf("sectionEnd = %d, want 8 (trailing blanks trimmed, fences skipped)", got)
	}
	if got := sectionEnd([]string{"## A", "text"}, 1, 2, nil); got != 2 {
		t.Errorf("sectionEnd to EOF = %d", got)
	}
	// Frontmatter ends with `...` too; a second `---` later is not frontmatter.
	m := carrierMask([]string{"---", "a: 1", "...", "x", "---", "y"}, false)
	want := []bool{false, false, false, true, true, true}
	for i := range want {
		if m[i] != want[i] {
			t.Errorf("mask[%d] = %v", i, m[i])
		}
	}
	if m := carrierMask([]string{"---", "x"}, true); !m[0] || !m[1] {
		t.Error("html never has frontmatter")
	}
	if tagName("  <Foo.Bar>") != "Foo" || tagName("<my-tag x>") != "my-tag" || tagName("no") != "" {
		t.Error("tagName")
	}
	if !(Markdown{}).Match("a.markdown") || (Markdown{}).Match("a.txt") || (Markdown{}).Name() != "markdown" {
		t.Error("markdown Match/Name")
	}
	// `.markdown` has no Style entry and falls back to html comments; span=+0
	// binds the single next line.
	f := Markdown{}.Extract("a.markdown", []byte("<!-- ds:def id=z-aaaaaaaa span=+0 -->\nonly this\nnot this\n"), "ds")
	if len(f.Defs) != 1 || f.Defs[0].Block.Kind != block.KindLine || f.Defs[0].Block.Content != "only this" {
		t.Errorf("markdown fallback style / span=+0 = %+v", f.Defs)
	}
}

func TestRegion(t *testing.T) {
	t.Parallel()
	src := "<!-- ds:block id=a-a2b6f8jk -->\n\n**x**\n\n```go\nbody\n```\n<!-- /ds:block hash=abc123 -->\n\n<!-- ds:block id=b-b3c7g9kl -->\ntext\n<!-- ds:block id=c-c4d8h2lm -->\n<!-- /ds:block -->\n"
	f := Markdown{}.Extract("d.md", []byte(src), "ds")
	if len(f.Refs) != 3 {
		t.Fatalf("refs = %d", len(f.Refs))
	}
	a := f.Refs[0].Reference.Region
	if a == nil || a.Hash != "abc123" || a.Start != 2 || a.End != 8 || a.Text != "**x**\n\n```go\nbody\n```" {
		t.Errorf("region a = %+v", a)
	}
	if f.Refs[1].Reference.Region != nil {
		t.Errorf("b has another directive before any closer: %+v", f.Refs[1].Reference.Region)
	}
	if c := f.Refs[2].Reference.Region; c == nil || c.Hash != "" || c.Text != "" {
		t.Errorf("empty region without hash = %+v", c)
	}
	if r := region([]string{"<!-- ds:block id=x -->", "no closer"}, 2, "ds"); r != nil {
		t.Error("no closer means no region")
	}
}
