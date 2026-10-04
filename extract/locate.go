package extract

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ubgo/docsync/internal/keypath"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
)

// Locate errors.
var (
	ErrSymbolNotFound = errors.New("extract: symbol not found")
	ErrLineOutOfRange = errors.New("extract: line out of range")
)

// Target names a block without a directive: a symbol, or a 1-based line.
// Exactly one is set. `ds def file#Symbol` and `ds def file:line` both land
// here (§22).
type Target struct {
	Symbol string
	Line   int
	// End is the last line of a `path:start-end` target, 0 for a single line.
	// Locate binds from Line as it always does; the caller decides whether
	// the tier can bind exactly Line..End (docsync.Define widens with span=
	// and refuses when no span gives that range). It is carried rather than
	// dropped because a range read as its first line, silently, defined a
	// block the author never asked for.
	End int
	// Prefix is the configured directive prefix. Locate needs it to see
	// which lines are directive carriers, so the extent it reports is the
	// one the scanner will compute for the same block; empty means the
	// default prefix.
	Prefix string
}

// ErrBadTarget is a `ds def` target that is not `path#Symbol`, `path:line`
// or `path:start-end`.
var ErrBadTarget = errors.New("extract: target must be path#Symbol, path:line or path:start-end")

// ParseTarget reads `path#Symbol`, `path:line` or `path:start-end`.
//
// The line part must be all digits: it used to be read with Sscanf, which
// takes the leading number and ignores the rest, so `path:3-5` quietly meant
// `path:3` and `path:3x` meant it too (bug 43).
func ParseTarget(s string) (path string, t Target, err error) {
	if p, sym, ok := strings.Cut(s, "#"); ok && sym != "" {
		return p, Target{Symbol: sym}, nil
	}
	if i := strings.LastIndex(s, ":"); i > 0 {
		from, to, isRange := strings.Cut(s[i+1:], "-")
		a, errA := strconv.Atoi(from)
		b, errB := strconv.Atoi(to)
		switch {
		case !isRange && errA == nil && a > 0 && digitsOnly(from):
			return s[:i], Target{Line: a}, nil
		case isRange && errA == nil && errB == nil && a > 0 && b >= a && digitsOnly(from) && digitsOnly(to):
			t := Target{Line: a}
			if b > a {
				t.End = b
			}
			return s[:i], t, nil
		}
	}
	return "", Target{}, fmt.Errorf("%w: %q", ErrBadTarget, s)
}

// digitsOnly reports a non-empty run of ASCII digits; Atoi alone accepts a
// sign, which no line number carries.
func digitsOnly(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// Locate binds a target in src with the same rules the def binder uses for
// that file kind, and returns the block (without an id) plus the line where a
// `ds:def` directive should be inserted. The insertion line is the block's
// first line; the caller places the directive above it in the host's comment
// style, or as a trailing comment for one-line config values.
//
// Symbol lookup matches declarations in code, headings in markdown, and keys
// in config files. Line lookup starts at that line and binds forward exactly
// as a def written above it would.
func Locate(p string, src []byte, t Target) (block.Block, error) {
	lines := splitLines(src)
	if t.Symbol == "" && (t.Line < 1 || t.Line > len(lines)) {
		return block.Block{}, fmt.Errorf("%w: %d of %d", ErrLineOutOfRange, t.Line, len(lines))
	}
	if t.Symbol == "" && t.End > len(lines) {
		return block.Block{}, fmt.Errorf("%w: %d-%d of %d", ErrLineOutOfRange, t.Line, t.End, len(lines))
	}
	st, _ := StyleFor(p)
	ext := Ext(p)
	carrier := carriersIn(lines, st, t.Prefix)
	switch {
	case extIn(p, markdownExts):
		start := t.Line
		if t.Symbol != "" {
			start = findHeading(lines, t.Symbol)
			if start == 0 {
				return block.Block{}, fmt.Errorf("%w: heading %q", ErrSymbolNotFound, t.Symbol)
			}
		}
		return bindLocated(lines, start, func() (int, block.Kind, string) {
			if m := atxRE.FindStringSubmatch(lines[start-1]); m != nil {
				return sectionEnd(lines, start, len(m[1]), carrier), block.KindSection, m[2]
			}
			return blankBoundedEnd(lines, start), block.KindParagraph, ""
		})
	case extIn(p, documentExts):
		// AsciiDoc and reStructuredText bind exactly as markdown does, but
		// their headings are found by their own rules: an rst heading is a
		// title plus an adornment line, so the heading map is the authority
		// on where one starts and how many lines it occupies. Without this
		// branch both fell through to the text tier and `ds def
		// "doc.adoc#Symbol"` was refused with "plain text has no symbols",
		// which named the tier it landed in rather than the file's own.
		headings := asciidocHeadings(lines)
		if extIn(p, rstExts) {
			headings = rstHeadings(lines)
		}
		start := t.Line
		if t.Symbol != "" {
			start = findDocumentHeading(headings, t.Symbol)
			if start == 0 {
				return block.Block{}, fmt.Errorf("%w: heading %q", ErrSymbolNotFound, t.Symbol)
			}
		}
		return bindLocated(lines, start, func() (int, block.Kind, string) {
			if h, ok := headings[start]; ok {
				return documentSectionEnd(lines, start+h.length, h.level, headings, carrier), block.KindSection, h.title
			}
			return blankBoundedEnd(lines, start), block.KindParagraph, ""
		})
	case (Config{}).Match(p):
		start := t.Line
		if t.Symbol != "" {
			start = findKey(lines, t.Symbol, isYAMLPath(p))
			if start == 0 {
				return block.Block{}, fmt.Errorf("%w: key %q", ErrSymbolNotFound, t.Symbol)
			}
		}
		return bindLocated(lines, start, func() (int, block.Kind, string) {
			return start, block.KindKey, keyPath(lines, start, lines[start-1], isYAMLPath(p))
		})
	case extIn(p, codeExts):
		start := t.Line
		if t.Symbol != "" {
			start = findDeclaration(lines, t.Symbol, ext)
			if start == 0 {
				return block.Block{}, fmt.Errorf("%w: declaration %q", ErrSymbolNotFound, t.Symbol)
			}
		} else if s := firstCodeLine(lines, start, st); s != 0 {
			start = s
		}
		return bindLocated(lines, start, func() (int, block.Kind, string) {
			kind, symbol := declaration(lines, start-1, ext)
			// A body member has no name this tier will synthesise; when the
			// caller located it by name, that name is the symbol, since it is
			// what they asked for and what was found.
			if symbol == "" && t.Symbol != "" {
				symbol = t.Symbol
			}
			switch {
			case sqlExts[ext]:
				return statementEnd(lines, start), kind, symbol
			case indentExts[ext] && strings.HasSuffix(strings.TrimRight(lines[start-1], " \t"), ":"):
				return indentEnd(lines, start), kind, symbol
			}
			// A group entry or a body member ends at itself, as in bindCode:
			// running on would make `path#Name` cover every entry below it.
			if enclosingGroup(lines, start-1) != "" || inDeclarationBody(lines, start-1) {
				return specEnd(lines, start), kind, symbol
			}
			if e, ok := braceEnd(lines, start); ok {
				return e, kind, symbol
			}
			return blankBoundedEnd(lines, start), kind, symbol
		})
	}
	if t.Symbol != "" {
		return block.Block{}, fmt.Errorf("%w: plain text has no symbols; use path:line", ErrSymbolNotFound)
	}
	return bindLocated(lines, t.Line, func() (int, block.Kind, string) { return t.Line, block.KindLine, "" })
}

// bindLocated assembles the block from a start line and a range function.
func bindLocated(lines []string, start int, rng func() (int, block.Kind, string)) (block.Block, error) {
	end, kind, symbol := rng()
	b := block.Block{Kind: kind, Symbol: symbol, Pos: block.Position{Start: start, End: end}}
	b.SetContent(join(lines, start, end))
	return b, nil
}

// findDeclaration returns the line of the declaration named symbol, matching
// `Type.Method` or the bare method name for receivers.
//
// A member of a declaration body -- a struct field, an interface method -- is
// found in a second pass, by comparing the name the caller asked for against
// the member's leading identifier. It is a second pass and not part of
// `declaration` on purpose: synthesising a name for every member line would
// call `private int x;` a declaration named "private", and a wrong symbol is
// worse than none because `path#Name` would then bind the wrong line. Matching
// a name the caller supplied can only be right or absent, so the lookup gains
// Go and TypeScript members without the tier claiming to name members it cannot.
func findDeclaration(lines []string, symbol, ext string) int {
	for i := range lines {
		if _, s := declaration(lines, i, ext); s != "" && (s == symbol || strings.HasSuffix(s, "."+symbol)) {
			return i + 1
		}
	}
	for i := range lines {
		for _, n := range memberNames(lines, i) {
			if n == symbol {
				return i + 1
			}
		}
	}
	return 0
}

// memberNames returns the names by which the line at i (0-based) could be
// asked for, when it is a member or an entry this tier does not name itself.
// It is only ever compared against a name the caller supplied, never used to
// label a block, so a line it describes loosely costs nothing.
func memberNames(lines []string, i int) []string {
	if enclosingGroup(lines, i) == keywordImport {
		// An import is asked for by its path or, when it has one, its alias.
		var out []string
		if m := specRE.FindStringSubmatch(lines[i]); m != nil {
			out = append(out, m[1])
		}
		if q := strings.TrimSpace(lines[i]); strings.HasSuffix(q, `"`) {
			if j := strings.Index(q, `"`); j >= 0 {
				out = append(out, strings.Trim(q[j:], `"`))
			}
		}
		return out
	}
	if !inDeclarationBody(lines, i) {
		return nil
	}
	if m := specRE.FindStringSubmatch(lines[i]); m != nil {
		return []string{m[1]}
	}
	return nil
}

// findHeading returns the line of the heading whose text equals symbol.
func findHeading(lines []string, symbol string) int {
	for i, l := range lines {
		if m := atxRE.FindStringSubmatch(l); m != nil && strings.EqualFold(strings.TrimSpace(m[2]), symbol) {
			return i + 1
		}
	}
	return 0
}

// findKey returns the line of the config key named symbol, matching the bare
// key or its full path. The path is compared segment by segment, so a key
// holding a dot is reached by quoting it (`tasks."a.b"`) and a key holding a
// colon needs no quoting at all (`tasks.wfsys:up`).
func findKey(lines []string, symbol string, yaml bool) int {
	want := keypath.Split(symbol)
	for i, l := range lines {
		k := keyOf(l, yaml)
		if k == "" {
			continue
		}
		if len(want) == 1 && k == want[0] {
			return i + 1
		}
		if k == want[len(want)-1] && slices.Equal(keyParts(lines, i+1, l, yaml), want) {
			return i + 1
		}
	}
	return 0
}

// Enclosing binds the smallest declared block that contains line: in code,
// the nearest declaration above the line whose extent reaches it; in
// markdown, the section or paragraph containing it. It falls back to
// Locate when nothing encloses the line. `adopt` uses it so a link to a
// line inside a function defines the function, not the statement.
func Enclosing(p string, src []byte, line int) (block.Block, error) {
	lines := splitLines(src)
	if line < 1 || line > len(lines) {
		return block.Block{}, fmt.Errorf("%w: %d of %d", ErrLineOutOfRange, line, len(lines))
	}
	if extIn(p, codeExts) {
		for start := line; start >= 1; start-- {
			if _, sym := declaration(lines, start-1, Ext(p)); sym == "" {
				continue
			}
			b, err := Locate(p, src, Target{Line: start})
			if err == nil && b.Pos.Start <= line && line <= b.Pos.End {
				return b, nil
			}
		}
	}
	if extIn(p, markdownExts) {
		for start := line; start >= 1; start-- {
			if !atxRE.MatchString(lines[start-1]) {
				continue
			}
			b, err := Locate(p, src, Target{Line: start})
			if err == nil && line <= b.Pos.End {
				return b, nil
			}
		}
	}
	return Locate(p, src, Target{Line: line})
}

// carriersIn finds the directive carrier lines in a file so Locate agrees
// with the scanner about where a block ends. Parse problems are discarded:
// Locate answers "where does this block end", and validating the file is
// the scanner's job.
func carriersIn(lines []string, st Style, prefix string) map[int]bool {
	if prefix == "" {
		prefix = directive.DefaultPrefix
	}
	var discard Found
	return carrierLines(scanComments(lines, st, prefix, &discard))
}

// findDocumentHeading returns the line of the first heading with this title,
// or 0. Titles are compared after trimming, the way the heading map stores
// them, so a target written without the adornment still matches.
func findDocumentHeading(headings map[int]heading, title string) int {
	best := 0
	for line, h := range headings {
		if h.title != title {
			continue
		}
		if best == 0 || line < best {
			best = line
		}
	}
	return best
}
