package extract

import (
	"regexp"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/sentence"
)

// Document is the document tier for AsciiDoc and reStructuredText (§10:
// "markdown mdx html asciidoc rst"). Markdown and HTML keep their own
// extractor because their carriers differ; these two write directives in
// line comments (`// ds:def` in AsciiDoc, `.. ds:def` in rst) and bind the
// way markdown does: a heading binds its section until the next heading of
// the same or higher level, anything else binds the blank-bounded
// paragraph, `span=+N` overrides. Cites are the free-text `ds:verb?…` form
// (AsciiDoc `link:ds:block?id=…[text]` contains it), bound to their
// sentence.
type Document struct{}

// documentExts is what this tier claims; the code tier excludes them.
var documentExts = map[string]bool{".adoc": true, ".asciidoc": true, ".rst": true}

// rstExts pick the rst heading rule; everything else here is AsciiDoc.
var rstExts = map[string]bool{".rst": true}

// Prose implements ProseTier: AsciiDoc and rst paragraphs are prose.
func (Document) Prose() bool { return true }

// Name implements Extractor.
func (Document) Name() string { return "document" }

// Match implements Extractor.
func (Document) Match(p string) bool { return extIn(p, documentExts) }

// asciidocHeadingRE matches `= Title` through `====== Level 5`; the count
// of `=` is the level.
var asciidocHeadingRE = regexp.MustCompile(`^(={1,6})\s+(\S.*?)\s*$`)

// asciidocFences open and close delimited blocks whose content is not
// structure: listings, literals, examples, sidebars, passthroughs, quotes.
var asciidocFences = []string{"----", "....", "====", "****", "++++", "____"}

// rstUnderlineMin is the shortest underline rst accepts as adornment.
const rstUnderlineMin = 3

// isRSTUnderline reports a heading adornment: one ASCII punctuation
// character repeated at least rstUnderlineMin times. The character's order
// of first appearance is the level, as rst defines it.
func isRSTUnderline(t string) bool {
	if len(t) < rstUnderlineMin {
		return false
	}
	c := t[0]
	if !strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", rune(c)) {
		return false
	}
	for i := 1; i < len(t); i++ {
		if t[i] != c {
			return false
		}
	}
	return true
}

// heading describes a heading line for one syntax.
type heading struct {
	level  int
	title  string
	start  int // first line of the heading (an rst overline when present)
	length int // lines the heading occupies
}

// Extract implements Extractor.
func (Document) Extract(p string, src []byte, prefix string) Found {
	var f Found
	lines := splitLines(src)
	st, _ := StyleFor(p)
	rst := extIn(p, rstExts)
	var headings map[int]heading
	if rst {
		headings = rstHeadings(lines)
	} else {
		headings = asciidocHeadings(lines)
	}
	occs := scanComments(lines, st, prefix, &f)
	carrier := carrierLines(occs)
	for _, o := range occs {
		if o.dir.Verb != VerbDef {
			r := block.Reference{Verb: o.dir.Verb, ID: o.dir.Args[block.KeyID], Pos: o.pos, Carrier: block.CarrierComment, Args: o.dir.Args}
			f.Refs = append(f.Refs, Ref{Directive: o.dir, Reference: r})
			continue
		}
		bindDocument(o, lines, headings, carrier, &f)
	}
	f.Refs = append(f.Refs, scanLinks(lines, prefix, &f, sentence.Bind)...)
	return f
}

// bindDocument binds one def to the section or paragraph after it.
func bindDocument(o occurrence, lines []string, headings map[int]heading, carrier map[int]bool, f *Found) {
	id, ok := requireID(o.dir, o.pos, f)
	if !ok {
		return
	}
	if isRemote(o.dir) {
		f.Defs = append(f.Defs, remoteDef(o.dir, id, o.pos, o.carrier))
		return
	}
	n, hasSpan, err := parseSpan(o.dir)
	if err != nil {
		f.Problems = append(f.Problems, Problem{Pos: o.pos, Err: err})
		return
	}
	start := nextNonBlank(lines, o.pos.End+1)
	if start == 0 {
		f.Problems = append(f.Problems, Problem{Pos: o.pos, Err: ErrNothingToBind})
		return
	}
	var end int
	var kind block.Kind
	var symbol string
	h, isHeading := headings[start]
	switch {
	case hasSpan:
		end = spanEnd(lines, start, n+1, carrier)
		kind = block.KindSpan
		if n == 0 {
			kind = block.KindLine
		}
	case isHeading:
		symbol = h.title
		end = documentSectionEnd(lines, start+h.length, h.level, headings, carrier)
		kind = block.KindSection
	default:
		end = blankBoundedEnd(lines, start)
		kind = block.KindParagraph
	}
	f.Defs = append(f.Defs, spanDef(o.dir, id, kind, symbol, block.Position{Start: start, End: end}, o.pos, o.carrier, lines, carrier))
}

// documentSectionEnd returns the last line before the next heading of level
// <= level, trailing blank lines trimmed; from is the first body line.
func documentSectionEnd(lines []string, from, level int, headings map[int]heading, carrier map[int]bool) int {
	end := len(lines)
	for i := from; i <= len(lines); i++ {
		if h, ok := headings[i]; ok && h.level <= level {
			end = i - 1
			break
		}
	}
	return trimCarriers(lines, from-1, end, carrier)
}

// asciidocHeadings maps the 1-based line of every heading, skipping the
// inside of delimited blocks.
func asciidocHeadings(lines []string) map[int]heading {
	out := map[int]heading{}
	fence := ""
	for i, l := range lines {
		t := strings.TrimRight(l, " \t")
		if fence != "" {
			if t == fence {
				fence = ""
			}
			continue
		}
		if isAsciidocFence(t) {
			fence = t
			continue
		}
		if m := asciidocHeadingRE.FindStringSubmatch(l); m != nil {
			out[i+1] = heading{level: len(m[1]), title: m[2], start: i + 1, length: 1}
		}
	}
	return out
}

func isAsciidocFence(t string) bool {
	for _, f := range asciidocFences {
		if t == f {
			return true
		}
	}
	return false
}

// rstHeadings finds title lines followed by an underline at least as long
// as the title, with an optional matching overline. Levels follow the
// order in which underline characters first appear.
func rstHeadings(lines []string) map[int]heading {
	out := map[int]heading{}
	order := map[byte]int{}
	for i := 0; i+1 < len(lines); i++ {
		title := strings.TrimSpace(lines[i])
		under := strings.TrimRight(lines[i+1], " \t")
		if title == "" || !isRSTUnderline(under) || len(under) < len(title) {
			continue
		}
		if isRSTUnderline(strings.TrimRight(lines[i], " \t")) {
			// This line is itself an underline (or an overline); the title
			// is the one it decorates.
			continue
		}
		ch := under[0]
		start, length := i+1, 2
		if i > 0 && strings.TrimRight(lines[i-1], " \t") == under {
			start, length = i, 3
		}
		if _, seen := order[ch]; !seen {
			order[ch] = len(order) + 1
		}
		out[start] = heading{level: order[ch], title: title, start: start, length: length}
	}
	return out
}
