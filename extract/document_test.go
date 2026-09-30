package extract

import (
	"errors"
	"testing"

	"github.com/ubgo/docsync/block"
)

const adocDoc = `= Sessions
:toc:

// ds:def id=intro-a2b6f8jk
Sessions are saved by the store.
Two lines.

// ds:def id=saving-b3c7g9kl
//   owner=@auth
== Saving

Text with a cite link:ds:block?id=sess-save-k7m2p4xq[the save path].

----
== not a heading inside a listing
----

=== Deeper

still saving

== Loading

// ds:block id=sess-save-k7m2p4xq
Loading text.

// ds:def id=span-c4d8h2lm span=+1
one
two
three

// ds:def
para

// ds:def id=remote-d5e9j3mn file=x.json pick=json:v
// ds:def id=tail-e6f2k4np
`

func TestDocumentAsciiDoc(t *testing.T) {
	t.Parallel()
	d := Document{}
	if d.Name() != "document" || !d.Match("a.adoc") || !d.Match("b.ASCIIDOC") || !d.Match("c.rst") || d.Match("d.md") {
		t.Error("name/match")
	}
	if (Code{}).Match("a.adoc") || (Code{}).Match("a.rst") {
		t.Error("the code tier must not claim documents")
	}
	if _, ok := Extensions()["document"]; !ok {
		t.Error("extensions list")
	}
	f := d.Extract("s.adoc", []byte(adocDoc), "ds")
	byID := map[string]block.Block{}
	for _, def := range f.Defs {
		byID[def.Block.ID] = def.Block
	}
	if b := byID["intro-a2b6f8jk"]; b.Kind != block.KindParagraph || b.Pos != (block.Position{Start: 5, End: 6}) || b.Content != "Sessions are saved by the store.\nTwo lines." {
		t.Errorf("paragraph = %+v", b)
	}
	// The section runs to the next heading of the same level, through the
	// listing whose fake heading is ignored, and over the deeper heading.
	if b := byID["saving-b3c7g9kl"]; b.Kind != block.KindSection || b.Symbol != "Saving" || b.Pos != (block.Position{Start: 10, End: 20}) || b.Owner() != "@auth" || b.DirectivePos != (block.Position{Start: 8, End: 9}) {
		t.Errorf("section = %+v", b)
	}
	if b := byID["span-c4d8h2lm"]; b.Kind != block.KindSpan || b.Pos != (block.Position{Start: 28, End: 29}) {
		t.Errorf("span = %+v", b)
	}
	if b, ok := byID["remote-d5e9j3mn"]; !ok || b.Args[block.KeyFile] != "x.json" {
		t.Errorf("remote = %+v", b)
	}
	if len(f.Defs) != 4 {
		t.Errorf("defs = %d", len(f.Defs))
	}
	// The comment cite and the free-text link cite, with its sentence.
	if len(f.Refs) != 2 || f.Refs[0].Reference.Carrier != block.CarrierComment || f.Refs[0].Reference.Pos.Start != 24 || f.Refs[1].Reference.Pos.Start != 12 || f.Refs[1].Reference.Sentence == "" {
		t.Errorf("refs = %+v", f.Refs)
	}
	var noID, nothing bool
	for _, p := range f.Problems {
		noID = noID || errors.Is(p.Err, ErrNoID)
		nothing = nothing || errors.Is(p.Err, ErrNothingToBind)
	}
	if len(f.Problems) != 2 || !noID || !nothing {
		t.Errorf("problems = %+v", f.Problems)
	}
	// span=+0 is a line; a bad span is a problem; a heading as the last line
	// is a section of one line.
	f = d.Extract("t.adoc", []byte("// ds:def id=l-a2b6f8jk span=+0\nline\n\n// ds:def id=b-b3c7g9kl span=x\nx\n\n// ds:def id=h-c4d8h2lm\n== End"), "ds")
	if len(f.Defs) != 2 || f.Defs[0].Block.Kind != block.KindLine || f.Defs[1].Block.Kind != block.KindSection || f.Defs[1].Block.Pos != (block.Position{Start: 8, End: 8}) || len(f.Problems) != 1 {
		t.Errorf("edges = %+v %+v", f.Defs, f.Problems)
	}
	// A span past the end clamps.
	if f := d.Extract("u.adoc", []byte("// ds:def id=s-a2b6f8jk span=+9\na\nb\n"), "ds"); f.Defs[0].Block.Pos.End != 3 {
		t.Errorf("clamp = %+v", f.Defs)
	}
}

const rstDoc = `=========
Sessions
=========

.. ds:def id=intro-a2b6f8jk
Sessions are saved by the store.

.. ds:def id=saving-b3c7g9kl
..    owner=@auth
Saving
======

Text citing ds:block?id=sess-save-k7m2p4xq here.

Deeper
------

more

Loading
=======

.. ds:block id=sess-save-k7m2p4xq

short
--
not a heading: underline too short

.. ds:def id=tail-c4d8h2lm
`

func TestDocumentRST(t *testing.T) {
	t.Parallel()
	f := Document{}.Extract("s.rst", []byte(rstDoc), "ds")
	byID := map[string]block.Block{}
	for _, def := range f.Defs {
		byID[def.Block.ID] = def.Block
	}
	if b := byID["intro-a2b6f8jk"]; b.Kind != block.KindParagraph || b.Pos != (block.Position{Start: 6, End: 6}) {
		t.Errorf("paragraph = %+v", b)
	}
	// `=` under a title is level 1 (its first use was the overlined title),
	// `-` is level 2; the section ends before Loading.
	if b := byID["saving-b3c7g9kl"]; b.Kind != block.KindSection || b.Symbol != "Saving" || b.Pos != (block.Position{Start: 10, End: 18}) || b.Owner() != "@auth" {
		t.Errorf("section = %+v", b)
	}
	if len(f.Defs) != 2 || len(f.Refs) != 2 || len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, ErrNothingToBind) {
		t.Errorf("counts = %d defs %d refs %+v", len(f.Defs), len(f.Refs), f.Problems)
	}
	hs := rstHeadings(splitLines([]byte(rstDoc)))
	if h := hs[1]; h.title != "Sessions" || h.level != 1 || h.length != 3 {
		t.Errorf("overlined = %+v", h)
	}
	if h := hs[15]; h.title != "Deeper" || h.level != 2 {
		t.Errorf("deeper = %+v", h)
	}
	if _, ok := hs[25]; ok {
		t.Error("short underline is not a heading")
	}
	if isRSTUnderline("aaaa") || isRSTUnderline("==-=") || isRSTUnderline("--") || !isRSTUnderline("~~~") {
		t.Error("underline rule")
	}
	// A def before an overlined title binds from the overline; two
	// adornment lines in a row are not a heading.
	f = Document{}.Extract("o.rst", []byte(".. ds:def id=t-a2b6f8jk\n-----\nTitle\n-----\n\nbody\n\n====\n====\n"), "ds")
	if len(f.Defs) != 1 || f.Defs[0].Block.Symbol != "Title" || f.Defs[0].Block.Pos != (block.Position{Start: 2, End: 9}) {
		t.Errorf("overline bind = %+v", f.Defs)
	}
}
