package extract

import (
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/sentence"
)

// Text is the universal fallback tier (§10, "text"): any file, no comment
// syntax assumed. A directive is a bare line whose trimmed form starts with
// the prefix. A def binds to the following lines until a blank line, or to
// `span=+N` lines. Renderers strip the bare directive line.
//
// It also serves files whose Style has line comments (`#`, `//`): if the file
// is not claimed by a more specific tier, a directive inside such a comment is
// recognised too, so a `.conf` with no dedicated tier still works.
type Text struct{}

// Name implements Extractor.
// Prose implements ProseTier: plain text is written, not minified.
func (Text) Prose() bool { return true }

func (Text) Name() string { return "text" }

// Match implements Extractor: the text tier matches everything, which is why
// it must be last in a registry.
func (Text) Match(string) bool { return true }

// Extract implements Extractor.
func (Text) Extract(p string, src []byte, prefix string) Found {
	var f Found
	lines := splitLines(src)
	occs := scanBareLines(lines, prefix, &f)
	if st, ok := StyleFor(p); ok && len(st.Line) > 0 {
		occs = append(occs, scanComments(lines, st, prefix, &f)...)
	}
	carriers := carrierLines(occs)
	for _, o := range occs {
		switch o.dir.Verb {
		case VerbDef:
			bindText(o, lines, carriers, &f)
		default:
			r := block.Reference{Verb: o.dir.Verb, ID: o.dir.Args[block.KeyID], Pos: o.pos, Carrier: o.carrier, Args: o.dir.Args}
			f.Refs = append(f.Refs, Ref{Directive: o.dir, Reference: r})
		}
	}
	f.Refs = append(f.Refs, scanLinks(lines, prefix, &f, sentence.Bind)...)
	return f
}

// bindText binds a def to the lines after it. With `span=+N` exactly N lines
// after the directive are taken (N may be 0, meaning the directive line
// itself is the block, which is how a trailing comment on a config line is
// handled by the config tier and how a self-describing line works here).
// Without span, the block runs from the next non-blank line to the next blank
// line.
func bindText(o occurrence, lines []string, carriers map[int]bool, f *Found) {
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
	var start, end int
	kind := block.KindSpan
	switch {
	case hasSpan && n == 0:
		// The directive line itself. For a trailing comment that is the code
		// part of the line; for a bare line it is the line minus the directive,
		// which is empty, so the block is the whole line and renderers hide it.
		if o.trailing {
			f.Defs = append(f.Defs, newDef(o.dir, id, block.KindLine, "", block.Position{Start: o.pos.Start, End: o.pos.Start}, o.pos, o.carrier, o.code))
			return
		}
		start, end, kind = o.pos.Start, o.pos.End, block.KindLine
	case hasSpan:
		start = o.pos.End + 1
		end = spanEnd(lines, start, n, carriers)
		if start > len(lines) {
			f.Problems = append(f.Problems, Problem{Pos: o.pos, Err: ErrNothingToBind})
			return
		}
		if n == 1 {
			kind = block.KindLine
		}
	default:
		start = nextNonBlank(lines, o.pos.End+1)
		if start == 0 {
			f.Problems = append(f.Problems, Problem{Pos: o.pos, Err: ErrNothingToBind})
			return
		}
		end = blankBoundedEnd(lines, start)
		if start == end {
			kind = block.KindLine
		}
	}
	f.Defs = append(f.Defs, spanDef(o.dir, id, kind, "", block.Position{Start: start, End: end}, o.pos, o.carrier, lines, carriers))
}
