package extract

import (
	"path"
	"regexp"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
)

// Style describes how a file type writes comments. Every extractor except the
// bare-text tier reads directives through one of these. Only the shapes in
// the spec's carrier table exist (§7.1); there is no per-project comment
// customisation because the scanner must never guess.
type Style struct {
	// Line prefixes that start a comment running to end of line: `//`, `#`, `--`.
	Line []string
	// BlockOpen and BlockClose delimit block comments: `/*` `*/`, `<!--` `-->`,
	// `{/*` `*/}`. Both empty means no block comments.
	BlockOpen, BlockClose string
	// AltOpen and AltClose are a second block form for hosts with two (MDX has
	// JSX comments and, in some renderers, HTML comments).
	AltOpen, AltClose string
	// NoStrings marks a host with no string literals, where a quote is
	// punctuation: prose and markup. Tracking quotes there read the
	// apostrophe in "It's" as opening a string that never closed, and every
	// trailing directive after it on the line was dropped without a word.
	NoStrings bool
	// Bare marks a format that carries a directive as a line of its own with
	// no delimiter at all, which is the text tier's carrier (§10). It is set
	// explicitly rather than assumed for anything unrecognised: writing a bare
	// line into a format that has syntax is not a fallback, it is corruption,
	// and JSON, go.work and Pkl each proved that in the field. A type absent
	// from this table is refused, not written bare.
	Bare bool
	// Trailing marks a format whose own parser reads a comment after a value
	// on the same line as a comment, so `ds def` may write a key's directive
	// there. Everywhere else a key's directive goes on the line above it.
	//
	// Opt-in because the failure is silent data corruption: Java properties,
	// Python's configparser, EditorConfig and `docker run --env-file` read
	// `port=8080   # ds:def id=…` as the value "8080   # ds:def id=…", and
	// `ds def` used to write exactly that into every key-value format.
	// Reading is unaffected: a trailing directive a person wrote is still
	// found, since that is their choice to make about their parser.
	Trailing bool
}

// Styles keyed by the lowercase base name first, then by Ext(path), so a file
// whose name decides its syntax (`go.mod`, `taskfile.yml`, `dockerfile`) is
// matched before its extension is considered. Kept as one table so `ds doctor`
// can print it and tests can prove every entry parses.
//
// A file type absent from this table has no carrier as far as docsync is
// concerned, and nothing will write a directive into it: see ErrNoCarrier.
// Adding an entry is therefore how a format becomes writable, and every entry
// is proved by inserting a directive and running that language's own parser
// over the result.
var Styles = map[string]Style{
	".go":    {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".ts":    {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".tsx":   {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".js":    {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".jsx":   {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".mjs":   {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".cjs":   {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".mts":   {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".cts":   {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".rs":    {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".c":     {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".h":     {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".cpp":   {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".java":  {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".kt":    {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".swift": {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".cs":    {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".php":   {Line: []string{"//", "#"}, BlockOpen: "/*", BlockClose: "*/"},
	".scala": {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".dart":  {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".zig":   {Line: []string{"//"}},
	// Plain text: a directive is a line of its own, per the text tier (10).
	".txt":  {Bare: true, NoStrings: true},
	".text": {Bare: true, NoStrings: true},
	".pkl":  {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	// Go's module and workspace files take `//` like Go source. They are not
	// merely unrecognised: a bare directive written into either one stops the
	// whole workspace building with "unknown directive", which is how this
	// table came to be audited.
	"go.mod":        {Line: []string{"//"}},
	"go.work":       {Line: []string{"//"}},
	".css":          {BlockOpen: "/*", BlockClose: "*/"},
	".scss":         {Line: []string{"//"}, BlockOpen: "/*", BlockClose: "*/"},
	".sql":          {Line: []string{"--"}, BlockOpen: "/*", BlockClose: "*/"},
	".lua":          {Line: []string{"--"}},
	".hs":           {Line: []string{"--"}},
	".py":           {Line: []string{"#"}},
	".pyi":          {Line: []string{"#"}},
	".rb":           {Line: []string{"#"}},
	".sh":           {Line: []string{"#"}},
	".bash":         {Line: []string{"#"}},
	".zsh":          {Line: []string{"#"}},
	".pl":           {Line: []string{"#"}},
	".r":            {Line: []string{"#"}},
	".ex":           {Line: []string{"#"}},
	".exs":          {Line: []string{"#"}},
	".nix":          {Line: []string{"#"}},
	".tf":           {Line: []string{"#", "//"}, BlockOpen: "/*", BlockClose: "*/"},
	".hcl":          {Line: []string{"#", "//"}, BlockOpen: "/*", BlockClose: "*/"},
	".tfvars":       {Line: []string{"#", "//"}, BlockOpen: "/*", BlockClose: "*/"},
	".yaml":         {Line: []string{"#"}, Trailing: true},
	".yml":          {Line: []string{"#"}, Trailing: true},
	".toml":         {Line: []string{"#"}, Trailing: true},
	".ini":          {Line: []string{"#", ";"}},
	".cfg":          {Line: []string{"#", ";"}},
	".conf":         {Line: []string{"#"}},
	".env":          {Line: []string{"#"}},
	".tpl":          {Line: []string{"#"}},
	".properties":   {Line: []string{"#", "!"}},
	".editorconfig": {Line: []string{"#", ";"}},
	".gitignore":    {Line: []string{"#"}},
	".dockerignore": {Line: []string{"#"}},
	"dockerfile":    {Line: []string{"#"}},
	"makefile":      {Line: []string{"#"}},
	"taskfile.yml":  {Line: []string{"#"}, Trailing: true},
	".adoc":         {Line: []string{"//"}, NoStrings: true},
	".asciidoc":     {Line: []string{"//"}, NoStrings: true},
	".rst":          {Line: []string{".."}, NoStrings: true},
	".md":           {BlockOpen: "<!--", BlockClose: "-->", NoStrings: true},
	".markdown":     {BlockOpen: "<!--", BlockClose: "-->", NoStrings: true},
	".mdx":          {BlockOpen: "{/*", BlockClose: "*/}", AltOpen: "<!--", AltClose: "-->", NoStrings: true},
	".html":         {BlockOpen: "<!--", BlockClose: "-->", NoStrings: true},
	".htm":          {BlockOpen: "<!--", BlockClose: "-->", NoStrings: true},
	".xml":          {BlockOpen: "<!--", BlockClose: "-->", NoStrings: true},
	".svg":          {BlockOpen: "<!--", BlockClose: "-->", NoStrings: true},
	".vue":          {Line: []string{"//"}, BlockOpen: "<!--", BlockClose: "-->", AltOpen: "/*", AltClose: "*/"},
	".svelte":       {Line: []string{"//"}, BlockOpen: "<!--", BlockClose: "-->", AltOpen: "/*", AltClose: "*/"},
}

// NoComments are formats with no comment syntax at all, keyed like Styles. A
// directive can never be valid in one, so a directive line found there is
// damage without exception, and the only repair is to remove it.
//
// It is a separate, explicit list rather than "anything not in Styles"
// because the unknown includes plain prose -- an extensionless NOTES file
// carries a bare directive line correctly -- and flagging those would bury the
// real damage. These are the formats the old fallback actually broke, plus the
// ones shaped like them.
//
// Deliberately absent, because they DO take comments: yarn.lock opens with a
// `#` line, Cargo.lock is TOML and pnpm-lock.yaml is YAML. package-lock.json is
// covered by its extension.
var NoComments = map[string]bool{
	".json": true, ".jsonl": true, ".ndjson": true, ".geojson": true, ".webmanifest": true,
	".csv": true, ".tsv": true,
	"go.sum": true,
}

// HasNoComments reports a path whose format has no comment syntax, so no
// directive can be written into it or be valid in it.
func HasNoComments(p string) bool {
	return NoComments[strings.ToLower(path.Base(p))] || NoComments[Ext(p)]
}

// StyleFor returns the style for a path and whether one is known. The base
// name wins over the extension, so `go.mod` is not read as some `.mod` format
// and `taskfile.yml` can differ from `.yml` if it ever needs to. Before this
// the base-name keys with a dot in them were unreachable, because Ext returns
// an extension whenever the name has one.
func StyleFor(p string) (Style, bool) {
	base := strings.ToLower(path.Base(p))
	if s, ok := Styles[base]; ok {
		return s, true
	}
	s, ok := Styles[Ext(p)]
	if !ok && strings.HasPrefix(base, ".env") {
		// `.env.example`, `.env.prod`: the config tier reads them by this
		// prefix, so they write the way they are read. Without it they were
		// scanned with `#` comments while `ds def` refused them (bug 46).
		return Styles[".env"], true
	}
	return s, ok
}

// commentLine is one physical line classified for directive purposes.
type commentLine struct {
	// n is the 1-based line number.
	n int
	// body is the comment text with delimiters stripped and untrimmed, or "" if
	// the line is not a comment.
	body string
	// isComment says the whole line is a comment (only whitespace before it).
	isComment bool
	// trailing says the comment follows code on the same line: `port: 8081 # ds:def…`.
	trailing bool
	// code is the non-comment part of the line, for trailing comments.
	code string
}

// classify splits a line into code and comment parts for a style. Block
// comments are only recognised when they open and close on the same line: a
// multi-line block comment is never a directive carrier because the spec's
// continuation rule is line-based, and a directive should be greppable on one
// line. Inside string literals a `//` is not a comment; a crude quote tracker
// keeps `"http://x"` from being cut. It is crude on purpose: the grammar tier
// does this exactly, and a false positive here only costs a spurious Problem
// on a line that merely mentions `ds:` inside a string, which the fixtures
// pin as acceptable.
func classify(n int, line string, st Style) commentLine {
	cl := commentLine{n: n}
	idx, delimLen, closeLen := findComment(line, st)
	if idx < 0 {
		return cl
	}
	before := line[:idx]
	rest := line[idx+delimLen:]
	if closeLen > 0 {
		rest = rest[:len(rest)-closeLen]
	}
	cl.body = rest
	if strings.TrimSpace(before) == "" {
		cl.isComment = true
	} else {
		cl.trailing = true
		cl.code = before
	}
	return cl
}

// findComment returns the index of the first comment delimiter outside a
// string literal, the delimiter length, and the length of a closing delimiter
// (0 for line comments). Returns -1 when the line has no usable comment.
func findComment(line string, st Style) (idx, delimLen, closeLen int) {
	var q byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if q != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == q {
				q = 0
			}
			continue
		}
		if !st.NoStrings && (c == '"' || c == '\'' || c == '`') {
			q = c
			continue
		}
		for _, open := range []struct{ o, cl string }{{st.BlockOpen, st.BlockClose}, {st.AltOpen, st.AltClose}} {
			if open.o != "" && strings.HasPrefix(line[i:], open.o) {
				end := strings.Index(line[i+len(open.o):], open.cl)
				if end < 0 {
					// Multi-line block comment: not a carrier.
					return -1, 0, 0
				}
				// Trim everything after the close so trailing code is ignored.
				return i, len(open.o), len(line) - (i + len(open.o) + end)
			}
		}
		for _, p := range st.Line {
			if strings.HasPrefix(line[i:], p) {
				return i, len(p), 0
			}
		}
	}
	return -1, 0, 0
}

// occurrence is a directive found in a file, before binding.
type occurrence struct {
	dir      directive.Directive
	pos      block.Position // first line .. last continuation line
	carrier  block.Carrier
	trailing bool   // sat after code on the same line
	code     string // that code, when trailing
}

// linkPattern finds `ds:verb?key=value` link targets inside free text: code
// comments that say `implements ds:block?id=…`, prose in files with no
// markdown link syntax. In free text the `?` is required, otherwise the
// comment-form directive `ds:def id=…` would itself look like a bare link
// target; explicit markdown links `[x](ds:chain)` are parsed by the markdown
// tier and may omit it. A non-identifier character (or line start) must
// precede the prefix so `nods:block` and `https://x/ds:block` do not match.
func linkPattern(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^A-Za-z0-9_/])(` + regexp.QuoteMeta(prefix) + `:[a-z][a-z0-9_]*\?[^\s)\]"'<>]*)`)
}

// scanComments walks lines with a style and returns every directive
// occurrence, folding continuation lines, plus problems for malformed ones.
// Non-directive comments are ignored silently. Trailing comments are never
// folded (a continuation must be a whole-line comment).
func scanComments(lines []string, st Style, prefix string, f *Found) []occurrence {
	var out []occurrence
	head := prefix + directive.Separator
	for i := 0; i < len(lines); i++ {
		cl := classify(i+1, lines[i], st)
		if cl.body == "" {
			if text, last, ok := MultiLineDirective(lines, i, st, head); ok {
				pos := block.Position{Start: i + 1, End: last + 1}
				d, err := directive.Parse(prefix, text)
				switch {
				case text == "":
					f.Problems = append(f.Problems, Problem{Pos: pos, Err: ErrUnclosedComment})
				case err != nil:
					f.Problems = append(f.Problems, Problem{Pos: pos, Err: err})
				default:
					out = append(out, occurrence{dir: d, pos: pos, carrier: block.CarrierComment})
				}
				i = last
			}
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(cl.body), head) {
			continue
		}
		bodies := []string{cl.body}
		last := i
		if cl.isComment {
			for j := i + 1; j < len(lines); j++ {
				next := classify(j+1, lines[j], st)
				if !next.isComment {
					break
				}
				probe := []string{cl.body, next.body}
				if _, n := directive.Fold(probe); n < 2 {
					break
				}
				bodies = append(bodies, next.body)
				last = j
			}
		}
		joined, _ := directive.Fold(bodies)
		pos := block.Position{Start: i + 1, End: last + 1}
		d, err := directive.Parse(prefix, joined)
		if err != nil {
			f.Problems = append(f.Problems, Problem{Pos: pos, Err: err})
			i = last
			continue
		}
		out = append(out, occurrence{dir: d, pos: pos, carrier: block.CarrierComment, trailing: cl.trailing, code: cl.code})
		i = last
	}
	return out
}

// MultiLineDirective reads a directive in a block comment that opens at the
// start of line i and closes on a later line:
//
//	<!-- ds:def id=sess-policy-h2n8wq4t
//	     owner=@auth -->
//
// It returns the comment's text with the delimiters removed and its lines
// joined by spaces, and the 0-based line the comment closes on. ok is false
// when line i opens no such comment. A comment that never closes comes back
// ok with empty text and last at the end of the file, for the caller to
// report.
//
// Why: a markdown author breaks a long directive over lines the way any HTML
// comment is broken, and that comment was skipped without a word -- the def
// did not exist and nothing said so (bug 50). Everything inside the comment is
// the directive, so no continuation rule is needed to decide which lines
// belong to it.
func MultiLineDirective(lines []string, i int, st Style, head string) (text string, last int, ok bool) {
	t := strings.TrimLeft(lines[i], " \t")
	for _, d := range [][2]string{{st.BlockOpen, st.BlockClose}, {st.AltOpen, st.AltClose}} {
		if d[0] == "" || !strings.HasPrefix(t, d[0]) {
			continue
		}
		first := strings.TrimSpace(t[len(d[0]):])
		if !strings.HasPrefix(first, head) || strings.Contains(first, d[1]) {
			return "", 0, false
		}
		parts := []string{first}
		for j := i + 1; j < len(lines); j++ {
			if k := strings.Index(lines[j], d[1]); k >= 0 {
				return strings.Join(append(parts, strings.TrimSpace(lines[j][:k])), " "), j, true
			}
			parts = append(parts, strings.TrimSpace(lines[j]))
		}
		return "", len(lines) - 1, true
	}
	return "", 0, false
}

// withBareDefs adds any bare `ds:def` line in a code file to occs.
//
// Why it exists: a code file's directives live in comments, and the code tiers
// only ever read comments -- so a bare `ds:def` line in one was not a def and
// not a problem, just invisible. An older `ds def` wrote exactly such lines into
// types it had no carrier for, and .pkl had none until the carrier table was
// audited; every Pkl file it damaged would otherwise stay damaged with nothing
// able to find it. Reading the line keeps the def working and lets the scan
// report it for `ds repair`.
//
// Only a line that parses as a def is taken, and any parse error from this
// pass is dropped: a Go label named `ds` is a line reading `ds:`, and treating
// it as a malformed directive would report a problem in correct code.
func withBareDefs(occs []occurrence, lines []string, prefix string) []occurrence {
	var discard Found
	for _, o := range scanBareLines(lines, prefix, &discard) {
		if o.dir.Verb == VerbDef {
			occs = append(occs, o)
		}
	}
	return occs
}

// scanBareLines is the text tier's equivalent: a line whose trimmed form
// starts with the prefix is a directive, with the same continuation rule
// (following lines that start with whitespace and key=value).
func scanBareLines(lines []string, prefix string, f *Found) []occurrence {
	var out []occurrence
	head := prefix + directive.Separator
	for i := 0; i < len(lines); i++ {
		t := lines[i]
		if !strings.HasPrefix(strings.TrimSpace(t), head) {
			continue
		}
		bodies := []string{t}
		last := i
		for j := i + 1; j < len(lines); j++ {
			if _, n := directive.Fold([]string{t, lines[j]}); n < 2 {
				break
			}
			bodies = append(bodies, lines[j])
			last = j
		}
		joined, _ := directive.Fold(bodies)
		pos := block.Position{Start: i + 1, End: last + 1}
		d, err := directive.Parse(prefix, joined)
		if err != nil {
			f.Problems = append(f.Problems, Problem{Pos: pos, Err: err})
			i = last
			continue
		}
		out = append(out, occurrence{dir: d, pos: pos, carrier: block.CarrierBareLine})
		i = last
	}
	return out
}

// scanLinks finds link-form directives anywhere in the lines (comments or
// prose) and returns them as references. Defs in link form are handled by
// the markdown tier because only it knows the link text; any other tier
// records a link-form `def` as a Problem via the caller.
func scanLinks(lines []string, prefix string, f *Found, sentenceOf func(line string, col int) string) []Ref {
	re := linkPattern(prefix)
	var out []Ref
	for i, line := range lines {
		for _, m := range re.FindAllStringSubmatchIndex(line, -1) {
			target := line[m[2]:m[3]]
			d, err := directive.ParseLink(prefix, target)
			if err != nil {
				f.Problems = append(f.Problems, Problem{Pos: block.Position{Start: i + 1, End: i + 1}, Err: err})
				continue
			}
			r := block.Reference{Verb: d.Verb, ID: d.Args[block.KeyID], Pos: block.Position{Start: i + 1, End: i + 1}, Carrier: block.CarrierLink, Args: d.Args}
			if sentenceOf != nil {
				r.SetSentence(sentenceOf(line, m[2]))
			}
			out = append(out, Ref{Directive: d, Reference: r})
		}
	}
	return out
}

// splitLines splits file bytes into lines with CRLF tolerated. A trailing
// newline does not produce an empty last line.
func splitLines(src []byte) []string {
	s := strings.ReplaceAll(string(src), "\r\n", "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// join re-joins a 1-based inclusive line range.
func join(lines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n")
}

// nextNonBlank returns the 1-based number of the first non-blank line at or
// after from, or 0.
func nextNonBlank(lines []string, from int) int {
	for i := from; i <= len(lines); i++ {
		if strings.TrimSpace(lines[i-1]) != "" {
			return i
		}
	}
	return 0
}

// blankBoundedEnd returns the last line of the run of non-blank lines that
// starts at start (1-based).
func blankBoundedEnd(lines []string, start int) int {
	end := start
	for end+1 <= len(lines) && strings.TrimSpace(lines[end]) != "" {
		end++
	}
	return end
}
