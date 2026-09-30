package extract

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
)

// hashOf extracts one file and returns the named def's hash and extent.
func hashOf(t *testing.T, ex Extractor, path, src, id string) (string, int, int) {
	t.Helper()
	for _, d := range ex.Extract(path, []byte(src), "ds").Defs {
		if d.Block.ID == id {
			return d.Block.Hash, d.Block.Pos.Start, d.Block.Pos.End
		}
	}
	t.Fatalf("%s: no def %s", path, id)
	return "", 0, 0
}

// TestCarrierIsNotBody pins the rule that a directive is metadata about text
// and never text: anchoring or unanchoring one block must leave every other
// block's extent and hash exactly as they were.
//
// A section runs to the next heading and `ds def` writes its carrier on the
// line above the heading it anchors, so without this the new carrier landed
// inside the previous section and was hashed as part of its body. Every
// sentence citing that section then reported `unacked … changed (body)`
// over text that had not changed — the worst kind of finding, because a tool
// whose findings are sometimes false teaches people to ack without reading.
func TestCarrierIsNotBody(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		ex       Extractor
		path     string
		one, two string
		id       string
	}{
		{
			name: "markdown",
			ex:   Markdown{},
			path: "docs/spec.md",
			one:  "# Spec\n\n<!-- ds:def id=depth-k7m2p4xq -->\n### Depth\n\nAt most 12 deep.\n\n### Width\n\nAt most 4 wide.\n",
			two:  "# Spec\n\n<!-- ds:def id=depth-k7m2p4xq -->\n### Depth\n\nAt most 12 deep.\n\n<!-- ds:def id=width-h3v8n2wd -->\n### Width\n\nAt most 4 wide.\n",
			id:   "depth-k7m2p4xq",
		},
		{
			name: "asciidoc",
			ex:   Document{},
			path: "docs/spec.adoc",
			one:  "= Spec\n\n// ds:def id=depth-k7m2p4xq\n== Depth\n\nAt most 12 deep.\n\n== Width\n\nAt most 4 wide.\n",
			two:  "= Spec\n\n// ds:def id=depth-k7m2p4xq\n== Depth\n\nAt most 12 deep.\n\n// ds:def id=width-h3v8n2wd\n== Width\n\nAt most 4 wide.\n",
			id:   "depth-k7m2p4xq",
		},
		{
			name: "rst",
			ex:   Document{},
			path: "docs/spec.rst",
			one:  ".. ds:def id=depth-k7m2p4xq\nDepth\n=====\n\nAt most 12 deep.\n\nWidth\n=====\n\nAt most 4 wide.\n",
			two:  ".. ds:def id=depth-k7m2p4xq\nDepth\n=====\n\nAt most 12 deep.\n\n.. ds:def id=width-h3v8n2wd\nWidth\n=====\n\nAt most 4 wide.\n",
			id:   "depth-k7m2p4xq",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h1, s1, e1 := hashOf(t, tc.ex, tc.path, tc.one, tc.id)
			h2, s2, e2 := hashOf(t, tc.ex, tc.path, tc.two, tc.id)
			if s1 != s2 || e1 != e2 {
				t.Errorf("anchoring the next block moved the extent: %d-%d -> %d-%d", s1, e1, s2, e2)
			}
			if h1 != h2 {
				t.Errorf("anchoring the next block changed the hash: %s -> %s", h1, h2)
			}
			// The body must not contain the neighbour's directive.
			for _, d := range tc.ex.Extract(tc.path, []byte(tc.two), "ds").Defs {
				if d.Block.ID == tc.id && contains(d.Block.Content, "width-h3v8n2wd") {
					t.Errorf("the neighbour's carrier is in the body:\n%s", d.Block.Content)
				}
			}
		})
	}
}

// TestTrailingCarrierIsBody is the exemption: a directive that shares its
// line with content cannot be removed without removing the content, so it
// stays. Config values are written this way.
func TestTrailingCarrierIsBody(t *testing.T) {
	t.Parallel()
	src := "first: 1   # ds:def id=one-k7m2p4xq\nsecond: 2\n"
	h, start, end := hashOf(t, Config{}, "conf/a.yaml", src, "one-k7m2p4xq")
	if start != 1 || end != 1 || h == "" {
		t.Errorf("trailing carrier def = %d-%d %q", start, end, h)
	}
	// Anchoring the next key leaves it alone.
	src2 := "first: 1   # ds:def id=one-k7m2p4xq\nsecond: 2   # ds:def id=two-h3v8n2wd\n"
	h2, s2, e2 := hashOf(t, Config{}, "conf/a.yaml", src2, "one-k7m2p4xq")
	if h2 != h || s2 != start || e2 != end {
		t.Errorf("config def moved: %d-%d %q", s2, e2, h2)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestLocateDocumentHeadings covers the document tier's symbol lookup, which
// was missing: `.adoc` and `.rst` fell through to the text tier and `ds def
// "doc.adoc#First"` was refused with "plain text has no symbols" — a message
// naming the tier it landed in rather than the file's own, for files the
// scanner binds correctly.
func TestLocateDocumentHeadings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, path, src, symbol string
		start, end              int
	}{
		{
			name:   "asciidoc",
			path:   "doc.adoc",
			src:    "= Doc\n\n== First\n\nText one.\n\n== Second\n\nText two.\n",
			symbol: "First", start: 3, end: 5,
		},
		{
			name:   "asciidoc second heading",
			path:   "doc.adoc",
			src:    "= Doc\n\n== First\n\nText one.\n\n== Second\n\nText two.\n",
			symbol: "Second", start: 7, end: 9,
		},
		{
			name:   "rst",
			path:   "doc.rst",
			src:    "Title\n=====\n\nIntro.\n\nSecond\n======\n\nMore.\n",
			symbol: "Second", start: 6, end: 9,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b, err := Locate(tc.path, []byte(tc.src), Target{Symbol: tc.symbol, Prefix: "ds"})
			if err != nil {
				t.Fatalf("Locate = %v", err)
			}
			if b.Kind != block.KindSection || b.Symbol != tc.symbol {
				t.Errorf("kind/symbol = %s %q", b.Kind, b.Symbol)
			}
			if b.Pos.Start != tc.start || b.Pos.End != tc.end {
				t.Errorf("position = %d-%d, want %d-%d", b.Pos.Start, b.Pos.End, tc.start, tc.end)
			}
		})
	}
	// An unknown heading names the heading, not the tier.
	_, err := Locate("doc.adoc", []byte("= Doc\n\n== First\n\nText.\n"), Target{Symbol: "Nope", Prefix: "ds"})
	if err == nil || !errors.Is(err, ErrSymbolNotFound) || !strings.Contains(err.Error(), "heading") {
		t.Errorf("unknown heading = %v", err)
	}
	// Line targets still work, and a paragraph that is not a heading binds
	// as a paragraph.
	b, err := Locate("doc.adoc", []byte("= Doc\n\nJust a paragraph.\n\nAnother.\n"), Target{Line: 3, Prefix: "ds"})
	if err != nil || b.Kind != block.KindParagraph {
		t.Errorf("paragraph = %+v %v", b, err)
	}
	// A carrier for the next heading is not absorbed here either.
	b, err = Locate("doc.adoc", []byte("= Doc\n\n== First\n\nText.\n\n// ds:def id=x-k7m2p4xq\n== Second\n\nMore.\n"), Target{Symbol: "First", Prefix: "ds"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Pos.End != 5 {
		t.Errorf("Locate must trim the neighbour's carrier too, got end %d", b.Pos.End)
	}
}

// TestTrimCarriers covers the trimming a bound extent ends with: the next
// block's own carrier comment is not part of this block, and neither are
// the blank lines before it.
func TestTrimCarriers(t *testing.T) {
	t.Parallel()
	lines := []string{"### A", "", "text", "", "<!-- ds:def id=b-k7m2p4xq -->"}
	if got := trimCarriers(lines, 1, 5, map[int]bool{5: true}); got != 3 {
		t.Errorf("carrier end = %d, want 3", got)
	}
	// With no carrier to walk back over, trailing blanks still go.
	blanks := []string{"### A", "", "text", "", ""}
	if got := trimCarriers(blanks, 1, 5, nil); got != 3 {
		t.Errorf("trailing blanks = %d, want 3", got)
	}
}
