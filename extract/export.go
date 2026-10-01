package extract

import (
	"fmt"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
)

// Exported hooks for grammar tiers outside the root module (§37.1:
// ext/treesitter). They expose the directive-reading half of the syntax
// tier, which is the same for every language, so an external tier only
// decides where a block starts and ends. Nothing here reads a file or keeps
// state.

// Occurrence is one directive found in a comment: what was parsed and where
// it sat. Trailing means it followed code on the same line; Code is that
// code.
type Occurrence struct {
	Directive directive.Directive
	Pos       block.Position
	Carrier   block.Carrier
	Trailing  bool
	Code      string
}

// ScanCode reads every directive in p's comments the way the built-in code
// tier does: references and link-carrier cites are appended to f, parse
// problems are recorded, and the `ds:def` occurrences are returned for the
// caller to bind. lines is the split source, 1-based through Occurrence.Pos.
// A path with no known comment style yields no occurrences and no problems:
// a grammar tier that matched such a path has nothing to read directives
// from.
func ScanCode(p string, src []byte, prefix string, f *Found) (lines []string, defs []Occurrence) {
	lines = splitLines(src)
	st, ok := StyleFor(p)
	if !ok {
		return lines, nil
	}
	for _, o := range withBareDefs(scanComments(lines, st, prefix, f), lines, prefix) {
		switch o.dir.Verb {
		case VerbDef:
			defs = append(defs, Occurrence{Directive: o.dir, Pos: o.pos, Carrier: o.carrier, Trailing: o.trailing, Code: strings.TrimRight(o.code, " \t")})
		default:
			r := block.Reference{Verb: o.dir.Verb, ID: o.dir.Args[block.KeyID], Pos: o.pos, Carrier: block.CarrierComment, Args: o.dir.Args}
			f.Refs = append(f.Refs, Ref{Directive: o.dir, Reference: r})
		}
	}
	f.Refs = append(f.Refs, scanLinks(lines, prefix, f, sentenceOfCode)...)
	return lines, defs
}

// sentenceOfCode is the sentence binder the code tier uses for link cites in
// free text; it lives behind a name so ScanCode and Code cannot drift.
var sentenceOfCode = codeSentence

// Prelude performs the checks every def needs before binding: the id must
// be present, a remote def (file=) is complete as written, and span= must
// parse. It returns the id and span, and ok=false when the caller has
// nothing left to do (the def was recorded as remote, or a problem was
// recorded).
func Prelude(o Occurrence, f *Found) (id string, span int, hasSpan, ok bool) {
	id, ok = requireID(o.Directive, o.Pos, f)
	if !ok {
		return "", 0, false, false
	}
	if isRemote(o.Directive) {
		f.Defs = append(f.Defs, remoteDef(o.Directive, id, o.Pos, o.Carrier))
		return id, 0, false, false
	}
	span, hasSpan, err := parseSpan(o.Directive)
	if err != nil {
		f.Problems = append(f.Problems, Problem{Pos: o.Pos, Err: err})
		return id, 0, false, false
	}
	return id, span, hasSpan, true
}

// NewDef builds a bound def with its hash over content.
func NewDef(o Occurrence, id string, kind block.Kind, symbol string, pos block.Position, content string) Def {
	return newDef(o.Directive, id, kind, symbol, pos, o.Pos, o.Carrier, content)
}

// Join returns lines start..end (1-based, inclusive) joined by "\n", clamped
// to the slice.
func Join(lines []string, start, end int) string { return join(lines, start, end) }

// HashedJoin is lines start..end joined the way a block's hash sees them:
// with the standalone carriers in carriers left out. An external tier that
// hashes source lines must use it rather than Join, or anchoring anything
// nested inside one of its blocks changes that block's hash (see hashedJoin).
func HashedJoin(lines []string, start, end int, carriers map[int]bool) string {
	return hashedJoin(lines, start, end, carriers)
}

// SpanEnd is the last line of a span that starts at start and holds want
// content lines — lines that are not standalone carriers (see spanEnd).
func SpanEnd(lines []string, start, want int, carriers map[int]bool) int {
	return spanEnd(lines, start, want, carriers)
}

// ParseSpan reads a directive's `span=+N`: N, whether the key is present,
// and ErrBadSpan when it is present but not `+N`. Every tier reads the key
// through it, so `span=` cannot mean "accepted and ignored" in one tier
// while it widens the block in another.
func ParseSpan(d directive.Directive) (int, bool, error) { return parseSpan(d) }

// Carriers returns the lines of p holding a standalone directive carrier —
// every def and cite that occupies a line by itself — for HashedJoin. A path
// with no known comment style has none.
func Carriers(p string, lines []string, prefix string) map[int]bool {
	st, ok := StyleFor(p)
	if !ok {
		return map[int]bool{}
	}
	return carriersIn(lines, st, prefix)
}

// FirstCodeLine returns the first line of p at or after from (1-based) that
// is neither blank, a comment, nor a decorator, or 0 when there is none. A
// tier with its own parser uses it to check that the block it chose actually
// begins where the directive points; see ErrSkippedCode.
func FirstCodeLine(p string, lines []string, from int) int {
	st, ok := StyleFor(p)
	if !ok {
		return from
	}
	return firstCodeLine(lines, from, st)
}

// SkippedCode records the problem a def raises when the block it would bind
// starts below the first code line under the directive.
func SkippedCode(o Occurrence, f *Found, start int) {
	f.Problems = append(f.Problems, Problem{Pos: o.Pos, Err: fmt.Errorf("%w: line %d is not part of it", ErrSkippedCode, start)})
}

// NothingToBind records the problem a def raises when no block follows it.
func NothingToBind(o Occurrence, f *Found) {
	f.Problems = append(f.Problems, Problem{Pos: o.Pos, Err: ErrNothingToBind})
}
