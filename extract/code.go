package extract

import (
	"regexp"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/sentence"
)

// Code is the heuristic syntax tier: it binds a `ds:def` to the declaration
// after it in languages with C-like braces, Python-like indentation, or
// SQL-like statements, without a grammar. It exists so docsync is useful on
// day one in any language; ext/treesitter registers ahead of it and takes
// over exactly for the languages it supports.
//
// Binding rules, in order: skip comment and decorator/attribute lines; the
// first code line starts the block; if that line or the lines up to the first
// `{` open a brace block, the block ends at the matching `}`; if it ends with
// `:` (Python), the block is the following deeper-indented lines; if the
// language is SQL-like, the block ends at the first `;`; otherwise the block
// is the blank-bounded run of lines. The symbol is extracted by a small set of
// declaration patterns and is "" when none match, which is honest rather than
// wrong.
type Code struct{}

// codeExts is what this tier claims: every Style entry that is not config,
// markdown, or html.
var codeExts = func() map[string]bool {
	m := map[string]bool{}
	for e, st := range Styles {
		// A format whose carrier is a bare line is the text tier's by
		// definition: routing it here would stop its directives being read at
		// all, since the code tier looks for comments and a bare line is not
		// one.
		if st.Bare {
			continue
		}
		if configExts[e] || markdownExts[e] || htmlExts[e] || documentExts[e] || e == ".gitignore" || e == ".dockerignore" || e == ".vue" || e == ".svelte" {
			continue
		}
		m[e] = true
	}
	return m
}()

// Name implements Extractor.
func (Code) Name() string { return "code" }

// Match implements Extractor.
func (Code) Match(p string) bool { return extIn(p, codeExts) }

// sqlExts use `;` as the statement terminator.
var sqlExts = map[string]bool{".sql": true}

// indentExts use indentation blocks after a line ending in `:`.
var indentExts = map[string]bool{".py": true}

// Extract implements Extractor.
func (Code) Extract(p string, src []byte, prefix string) Found {
	var f Found
	lines := splitLines(src)
	st, _ := StyleFor(p)
	ext := Ext(p)
	occs := withBareDefs(scanComments(lines, st, prefix, &f), lines, prefix)
	carriers := carrierLines(occs)
	for _, o := range occs {
		switch o.dir.Verb {
		case VerbDef:
			bindCode(o, lines, st, ext, carriers, &f)
		default:
			r := block.Reference{Verb: o.dir.Verb, ID: o.dir.Args[block.KeyID], Pos: o.pos, Carrier: block.CarrierComment, Args: o.dir.Args}
			f.Refs = append(f.Refs, Ref{Directive: o.dir, Reference: r})
		}
	}
	f.Refs = append(f.Refs, scanLinks(lines, prefix, &f, codeSentence)...)
	return f
}

// codeSentence binds a link cite in code to its enclosing sentence.
func codeSentence(line string, col int) string { return sentence.Bind(line, col) }

// declRE lists declaration shapes and the capture that names the symbol. The
// receiver form for Go methods produces `Type.Method`.
var declRE = []struct {
	re   *regexp.Regexp
	kind block.Kind
	// symbol builds the name from the submatches.
	symbol func(m []string) string
}{
	{regexp.MustCompile(`^\s*func\s+\(\s*\w+\s+\*?(\w+)\s*\)\s+(\w+)\s*\(`), block.KindFunc, func(m []string) string { return m[1] + "." + m[2] }},
	{regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?(?:pub(?:\([^)]*\))?\s+)?(?:func|fn|function|def|fun)\s+(\w+)`), block.KindFunc, func(m []string) string { return m[1] }},
	{regexp.MustCompile(`^\s*(?:export\s+)?(?:pub(?:\([^)]*\))?\s+)?(?:type|class|struct|interface|enum|trait|impl|object|record|data\s+class)\s+(\w+)`), block.KindType, func(m []string) string { return m[1] }},
	{regexp.MustCompile(`^\s*(?:const|var)\s*\(\s*$`), block.KindConst, func([]string) string { return "" }},
	{regexp.MustCompile(`^\s*(?:export\s+)?(?:pub(?:\([^)]*\))?\s+)?(?:const|var|let|static|val)\s+(?:[\w\[\]<>*.]+\s+)?(\w+)\s*(?:[=:]|$)`), block.KindConst, func(m []string) string { return m[1] }},
	{regexp.MustCompile(`(?i)^\s*create\s+(?:or\s+replace\s+)?(?:table|view|index|function|procedure|type)\s+(?:if\s+not\s+exists\s+)?([\w.]+)`), block.KindStatement, func(m []string) string { return m[1] }},
	{regexp.MustCompile(`(?i)^\s*(select|insert|update|delete|alter|drop|with)\b`), block.KindStatement, func(m []string) string { return strings.ToLower(m[1]) }},
}

// bindCode binds a def to the declaration after it.
func bindCode(o occurrence, lines []string, st Style, ext string, carriers map[int]bool, f *Found) {
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
	if o.trailing {
		// A trailing def on a code line binds that line.
		f.Defs = append(f.Defs, newDef(o.dir, id, block.KindLine, "", block.Position{Start: o.pos.Start, End: o.pos.Start}, o.pos, o.carrier, o.code))
		return
	}
	start := firstCodeLine(lines, o.pos.End+1, st)
	if start == 0 {
		f.Problems = append(f.Problems, Problem{Pos: o.pos, Err: ErrNothingToBind})
		return
	}
	kind, symbol := declaration(lines, start-1, ext)
	var end int
	switch {
	case hasSpan:
		end = spanEnd(lines, start, n+1, carriers)
	case enclosingGroup(lines, start-1) != "", inDeclarationBody(lines, start-1):
		// A group entry or a body member binds itself, never the members below
		// it and never the bracket that closes the construct.
		end = specEnd(lines, start)
	case sqlExts[ext]:
		end = statementEnd(lines, start)
	case indentExts[ext] && strings.HasSuffix(strings.TrimRight(lines[start-1], " \t"), ":"):
		end = indentEnd(lines, start)
	default:
		if e, ok := braceEnd(lines, start); ok {
			end = e
		} else {
			end = blankBoundedEnd(lines, start)
		}
	}
	f.Defs = append(f.Defs, spanDef(o.dir, id, kind, symbol, block.Position{Start: start, End: end}, o.pos, o.carrier, lines, carriers))
}

// firstCodeLine returns the first line at or after from (1-based) that is
// neither blank, a comment, nor a decorator/attribute line, or 0.
func firstCodeLine(lines []string, from int, st Style) int {
	for i := from; i <= len(lines); i++ {
		t := strings.TrimSpace(lines[i-1])
		if t == "" || isCommentOnly(t, st) || isDecorator(t) {
			continue
		}
		return i
	}
	return 0
}

func isCommentOnly(t string, st Style) bool {
	for _, p := range st.Line {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return st.BlockOpen != "" && strings.HasPrefix(t, st.BlockOpen)
}

// isDecorator recognises Python decorators, Rust and C# attributes, and
// TypeScript decorators, which sit between a doc comment and the declaration.
func isDecorator(t string) bool {
	return strings.HasPrefix(t, "@") || strings.HasPrefix(t, "#[") || strings.HasPrefix(t, "[")
}

// declLine classifies a line on its own, with no surrounding context.
func declLine(l string) (block.Kind, string) {
	for _, d := range declRE {
		if m := d.re.FindStringSubmatch(l); m != nil {
			return d.kind, d.symbol(m)
		}
	}
	return block.KindStatement, ""
}

// The keywords that open a parenthesised declaration group. type names types
// rather than values; import groups entries that are paths, not symbols, so an
// entry inside one gets a group's extent but no synthesised name -- an aliased
// import (`f "fmt"`) would otherwise look like a declaration called f.
const (
	keywordType   = "type"
	keywordImport = "import"
)

// groupOpenRE matches the opening line of a parenthesised declaration group
// and captures the keyword, which decides the kind of every entry inside it.
var groupOpenRE = regexp.MustCompile(`^\s*(const|var|type|import)\s*\(\s*$`)

// groupClose is the line that ends such a group.
const groupClose = ")"

// specRE matches one entry inside a group: the leading identifier names it.
var specRE = regexp.MustCompile(`^\s*(\w+)`)

// enclosingGroup returns the keyword of the declaration group containing the
// line at i (0-based), or "" when that line is not inside one.
//
// Why it exists: whether `MaxLength = 256` declares anything is not decidable
// from the line alone. Inside `const ( … )` it declares a constant; inside a
// function body the same text assigns to a variable. Go groups related
// constants, defaults and limits far more often than it declares them singly,
// and those are exactly the values documentation restates, so the line-local
// answer was wrong for the common case (bug 18).
//
// The walk upward stops at the first `)`, which closes a group that ended
// above this line, and at any func or type declaration, which no group entry
// can be inside.
func enclosingGroup(lines []string, i int) string {
	for j := i - 1; j >= 0; j-- {
		if m := groupOpenRE.FindStringSubmatch(lines[j]); m != nil {
			return m[1]
		}
		if strings.TrimSpace(lines[j]) == groupClose {
			return ""
		}
		if k, sym := declLine(lines[j]); sym != "" && (k == block.KindFunc || k == block.KindType) {
			return ""
		}
	}
	return ""
}

// goDeclRE reads a Go `const` or `var` declaration, whose name is always the
// first identifier after the keyword: `const Local Kind = "a"`,
// `var Slice []string`, `var A, B int`. The generic rule in declRE was written
// for the C family, where the type comes first (`const int MAX = 5`), so on a
// typed Go declaration it took the TYPE for the name -- `ds def file.go#Local`
// answered "symbol not found" because the line was recorded as declaring
// `Kind` (bug 19). A `(` after the keyword is a group opener and does not match.
var goDeclRE = regexp.MustCompile(`^\s*(?:const|var)\s+([A-Za-z_]\w*)`)

// goExt is the extension whose declarations put the name before the type.
const goExt = ".go"

// declaration classifies the line at i (0-based) and extracts its symbol,
// reading the lines above it so an entry inside a declaration group is
// recognised as the declaration it is. See enclosingGroup for why the
// context is required. ext decides the declaration order: Go names a
// constant before its type and the C family after, which no line-local rule
// can tell apart.
func declaration(lines []string, i int, ext string) (block.Kind, string) {
	if ext == goExt {
		if m := goDeclRE.FindStringSubmatch(lines[i]); m != nil {
			return block.KindConst, m[1]
		}
	}
	if k, sym := declLine(lines[i]); sym != "" {
		return k, sym
	}
	if kw := enclosingGroup(lines, i); kw != "" && kw != keywordImport && strings.TrimSpace(lines[i]) != groupClose {
		if m := specRE.FindStringSubmatch(lines[i]); m != nil {
			if kw == keywordType {
				return block.KindType, m[1]
			}
			return block.KindConst, m[1]
		}
	}
	return declLine(lines[i])
}

// bodyOpenRE matches a line that opens a brace-delimited declaration body
// whose contents are named members: a struct, an interface, a class, an enum.
// The keyword may appear anywhere before the brace, so an anonymous inner
// `A struct {` is recognised as well as a named `type T struct {`. A line with
// no such keyword -- `func f() {`, or a TypeScript method's `m() {` -- opens a
// body of statements, not of members.
var bodyOpenRE = regexp.MustCompile(`(?:^|\s)(?:type|class|struct|interface|enum|trait|impl|object|record)\b[^{]*\{\s*$`)

// inDeclarationBody reports whether the line at i (0-based) is a member of a
// brace-delimited declaration body.
//
// Why it exists: it is the group problem in its other form. A def above a
// struct field or an interface method used to run to the body's closing brace,
// swallowing every member below it, so anchoring the first field of a struct
// covered the whole struct and a change to any field flagged the sentence about
// one of them.
//
// It finds the INNERMOST brace still open above i and asks what opened that,
// rather than stopping at the first keyword it recognises walking up. The
// difference is a statement inside a method inside a class: the nearest opener
// is the method, so the statement is not a member, even though a class body
// encloses it further out. Getting this wrong shortens an extent that has to
// stay whole.
func inDeclarationBody(lines []string, i int) bool {
	depth := 0
	for j := i - 1; j >= 0; j-- {
		opens, closes := 0, 0
		scanCode(lines[j], func(c byte) {
			switch c {
			case '{':
				opens++
			case '}':
				closes++
			}
		})
		// Walking upward, a `}` met first belongs to a construct that already
		// closed above this line and below i, so it cancels an opener further up.
		depth += closes
		if opens > depth {
			return bodyOpenRE.MatchString(lines[j])
		}
		depth -= opens
	}
	return false
}

// specEnd returns the last line of the group entry that begins at start
// (1-based): its own line, extended while a bracket it opened is still open,
// so an entry whose value spans lines stays one block. It never runs to the
// group's closing `)`, which is what made a directive above one entry bind
// every entry below it as well.
func specEnd(lines []string, start int) int {
	depth := 0
	for i := start; i <= len(lines); i++ {
		scanCode(lines[i-1], func(c byte) {
			switch c {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				depth--
			}
		})
		if depth <= 0 {
			return i
		}
	}
	return len(lines)
}

// scanCode calls f for every byte of l that lies outside a string or rune
// literal, so a bracket written inside one is not counted as structure.
// Backslash escapes are skipped. The tracker is deliberately crude: it is
// enough for the bracket counting its callers do, and a file it misreads is
// one where a heuristic tier was always going to guess.
func scanCode(l string, f func(c byte)) {
	var q byte
	for j := 0; j < len(l); j++ {
		c := l[j]
		if q != 0 {
			if c == '\\' {
				j++
			} else if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			q = c
		default:
			f(c)
		}
	}
}

// braceEnd finds the line of the `}` matching the first `{` at or after
// start, scanning at most the blank-bounded run before the first brace so a
// declaration with no brace does not swallow the file. Braces inside string
// literals are ignored by a crude quote tracker.
func braceEnd(lines []string, start int) (int, bool) {
	depth := 0
	opened := false
	for i := start - 1; i < len(lines); i++ {
		l := lines[i]
		if !opened && strings.TrimSpace(l) == "" {
			return 0, false
		}
		closed := 0
		scanCode(l, func(c byte) {
			switch c {
			case '{':
				depth++
				opened = true
			case '}':
				depth--
				if opened && depth == 0 && closed == 0 {
					closed = i + 1
				}
			}
		})
		if closed != 0 {
			return closed, true
		}
	}
	return 0, false
}

// indentEnd returns the last line of a Python-style block: all following
// lines indented deeper than the header, blank lines included when followed
// by more block lines.
func indentEnd(lines []string, start int) int {
	base := indentOf(lines[start-1])
	end := start
	for i := start; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			continue
		}
		if indentOf(l) <= base {
			break
		}
		end = i + 1
	}
	return end
}

// statementEnd returns the line containing the first `;` within the
// blank-bounded run that starts at start, or the end of that run when the
// statement has no terminator. It never crosses a blank line, so a missing
// semicolon cannot make one statement swallow the next.
func statementEnd(lines []string, start int) int {
	end := blankBoundedEnd(lines, start)
	for i := start - 1; i < end; i++ {
		if strings.Contains(lines[i], ";") {
			return i + 1
		}
	}
	return end
}
