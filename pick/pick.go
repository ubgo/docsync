// Package pick evaluates `pick=` expressions: how a single value or a single
// contiguous range is taken out of a bound block (docs/SPEC.md §10).
//
// The rule that keeps this from becoming a scripting language: a pick returns
// exactly one line (a Value) or one contiguous range (a Range). No transforms,
// no arithmetic, no joins. Display formatting belongs to the cite side.
//
// Tiers implemented here, all stdlib: the text vocabulary (`line`, `regex`,
// `url`, `after`, `between`), the document vocabulary over markdown
// (`heading`, `section`, `paragraph`, `link`), the structured vocabulary for
// formats that need no dependency (`json`, `env`, `ini`, `csv`, and line-mode
// `yaml` and `toml` for the common flat and nested-map cases), and `file`.
// `symbol:` needs a grammar, so this package answers it with ErrUnsupported:
// on a remote def the scanner hands it to the host's resolver
// (scan.Options.Symbol), which asks the target file's own tier, as `ds def
// file#Name` does. A picker plugin (Picker interface in the root package) may
// add schemes.
package pick

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ubgo/docsync/internal/keypath"
)

// Scheme names. Kept as constants so the dispatcher, the docs, and the
// conformance fixtures agree on spelling.
const (
	SchemeLine      = "line"
	SchemeRegex     = "regex"
	SchemeURL       = "url"
	SchemeAfter     = "after"
	SchemeBetween   = "between"
	SchemeHeading   = "heading"
	SchemeSection   = "section"
	SchemeParagraph = "paragraph"
	SchemeLink      = "link"
	SchemeJSON      = "json"
	SchemeEnv       = "env"
	SchemeINI       = "ini"
	SchemeCSV       = "csv"
	SchemeYAML      = "yaml"
	SchemeTOML      = "toml"
	SchemeFile      = "file"
	SchemeSymbol    = "symbol"
)

// Schemes is the canonical list, in the order the spec table presents them.
var Schemes = []string{SchemeLine, SchemeRegex, SchemeURL, SchemeAfter, SchemeBetween, SchemeHeading, SchemeSection, SchemeParagraph, SchemeLink, SchemeJSON, SchemeEnv, SchemeINI, SchemeCSV, SchemeYAML, SchemeTOML, SchemeFile, SchemeSymbol}

// Sentinel errors.
var (
	// ErrBadExpr is a malformed expression: unknown scheme, missing argument,
	// bad number, invalid regex.
	ErrBadExpr = errors.New("pick: bad expression")
	// ErrNotFound means the expression is valid but nothing in the content
	// matched. `check` reports this as `pick failed`.
	ErrNotFound = errors.New("pick: nothing matched")
	// ErrMultiLine means a value-producing pick (line, regex, a scalar key)
	// yielded more than one line, which the one-line rule forbids. The way out
	// is a pick that names a block -- a key or table holding the lines, a
	// section, a symbol -- cited with ds:block. The message used to say only
	// "use ds:block", which read as a fault in the citation even when the
	// citation already was ds:block (bug 134).
	ErrMultiLine = errors.New("pick: the picked value spans more than one line; pick the key, table, section or symbol that holds those lines to bind them as a block")
	// ErrUnsupported means the scheme needs an extractor this package does not
	// have (a grammar). Plugins answer it; the root reports it honestly.
	ErrUnsupported = errors.New("pick: scheme not supported without a plugin")
)

// Kind says whether a Result is a one-line value or a line range.
type Kind string

const (
	// KindValue is a single line, what `ds:cfg` inlines.
	KindValue Kind = "value"
	// KindRange is one or more contiguous lines, what `ds:block` renders.
	KindRange Kind = "range"
)

// Result is what a pick produced. For KindValue, Value is set and Start==End.
// For KindRange, Text is the joined lines. Start and End are 1-based line
// numbers within the content handed to Pick, inclusive, so a caller that knows
// where the content began in its file can add the offset.
type Result struct {
	Kind  Kind
	Value string
	Text  string
	Start int
	End   int
}

// Expr is a parsed expression: scheme plus its raw argument, quotes removed.
type Expr struct {
	Scheme string
	Arg    string
}

// Parse splits `scheme:arg` and strips optional single or double quotes from
// the argument. `heading`, `url`, and `file` take no argument.
func Parse(expr string) (Expr, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return Expr{}, fmt.Errorf("%w: empty", ErrBadExpr)
	}
	scheme, arg, _ := strings.Cut(expr, ":")
	if !known(scheme) {
		return Expr{}, fmt.Errorf("%w: unknown scheme %q", ErrBadExpr, scheme)
	}
	arg = strings.TrimSpace(arg)
	// Strip one enclosing quote pair, but only when it is the only pair: a
	// list argument like `'<','>'` keeps its quotes for splitQuotedList.
	if len(arg) >= 2 && (arg[0] == '\'' || arg[0] == '"') && arg[len(arg)-1] == arg[0] && strings.Count(arg, arg[:1]) == 2 {
		arg = arg[1 : len(arg)-1]
	}
	return Expr{Scheme: scheme, Arg: arg}, nil
}

func known(s string) bool {
	for _, k := range Schemes {
		if k == s {
			return true
		}
	}
	return false
}

// Picker is a plugin scheme (§37.3): given the argument after the colon and
// the block content, it returns a value or a range.
type Picker func(arg, content string) (Result, error)

// SplitScheme separates `scheme:arg`; a bare word is a scheme with no arg.
func SplitScheme(expr string) (scheme, arg string) {
	scheme, arg, _ = strings.Cut(expr, ":")
	return strings.TrimSpace(scheme), arg
}

// PickWith applies a registered Picker when the scheme names one, otherwise
// the built-in schemes. Plugins never shadow built-ins: a registered scheme
// with a built-in name is ignored, so `json:` always means the same thing.
func PickWith(pickers map[string]Picker, expr, content string) (Result, error) {
	scheme, arg := SplitScheme(expr)
	if p, ok := pickers[scheme]; ok && !known(scheme) {
		return p(arg, content)
	}
	return Pick(expr, content)
}

// Pick evaluates expr against content and applies the one-line rule for value
// schemes. Line numbers in the Result are relative to content.
func Pick(expr, content string) (Result, error) {
	e, err := Parse(expr)
	if err != nil {
		return Result{}, err
	}
	return e.Apply(content)
}

// Apply evaluates a parsed expression.
//
// CRLF is read as LF. The scanner hands over a file's raw bytes, and a
// Windows checkout with core.autocrlf has \r on every line: `line:2` then
// failed as spanning two lines and a `$`-anchored regex matched nothing,
// so one repository resolved differently depending on who checked it out.
func (e Expr) Apply(content string) (Result, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := splitLines(content)
	switch e.Scheme {
	case SchemeFile:
		return rangeResult(lines, 1, len(lines)), nil
	case SchemeLine:
		return pickLine(e.Arg, lines)
	case SchemeRegex:
		return pickRegex(e.Arg, lines)
	case SchemeURL:
		return pickURL(lines)
	case SchemeAfter:
		return pickAfter(e.Arg, lines)
	case SchemeBetween:
		return pickBetween(e.Arg, lines)
	case SchemeHeading:
		return pickHeading(lines)
	case SchemeSection:
		return pickSection(e.Arg, lines)
	case SchemeParagraph:
		return pickParagraph(e.Arg, lines)
	case SchemeLink:
		return pickLink(e.Arg, lines)
	case SchemeJSON:
		return pickJSON(e.Arg, content)
	case SchemeEnv:
		return pickEnv(e.Arg, lines)
	case SchemeINI:
		return pickINI(e.Arg, lines)
	case SchemeCSV:
		return pickCSV(e.Arg, content)
	case SchemeYAML:
		return pickYAML(e.Arg, lines)
	case SchemeTOML:
		return pickTOML(e.Arg, lines)
	default: // SchemeSymbol; Parse guarantees the scheme is known
		return Result{}, fmt.Errorf("%w: %s", ErrUnsupported, e.Scheme)
	}
}

// splitLines splits on \n without a phantom trailing element.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	l := strings.Split(s, "\n")
	if l[len(l)-1] == "" {
		l = l[:len(l)-1]
	}
	return l
}

func valueResult(v string, line int) (Result, error) {
	if strings.ContainsAny(v, "\n\r") {
		return Result{}, ErrMultiLine
	}
	return Result{Kind: KindValue, Value: v, Start: line, End: line}, nil
}

func rangeResult(lines []string, start, end int) Result {
	return Result{Kind: KindRange, Text: strings.Join(lines[start-1:end], "\n"), Start: start, End: end}
}

// --- text tier ------------------------------------------------------------

func pickLine(arg string, lines []string) (Result, error) {
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 {
		return Result{}, fmt.Errorf("%w: line wants a positive number, got %q", ErrBadExpr, arg)
	}
	if n > len(lines) {
		return Result{}, fmt.Errorf("%w: line %d of %d", ErrNotFound, n, len(lines))
	}
	return valueResult(lines[n-1], n)
}

func pickRegex(arg string, lines []string) (Result, error) {
	if arg == "" {
		return Result{}, fmt.Errorf("%w: regex wants a pattern", ErrBadExpr)
	}
	re, err := regexp.Compile(arg)
	if err != nil {
		return Result{}, fmt.Errorf("%w: regex: %v", ErrBadExpr, err)
	}
	for i, l := range lines {
		m := re.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		// First capture group if the pattern has one, else the whole match.
		if len(m) > 1 {
			return valueResult(m[1], i+1)
		}
		return valueResult(m[0], i+1)
	}
	return Result{}, fmt.Errorf("%w: regex %q", ErrNotFound, arg)
}

// urlRE is deliberately conservative: a scheme, `://`, then everything up to
// whitespace or a closing bracket, with trailing sentence punctuation dropped.
var urlRE = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s<>()\[\]"']+`)

func pickURL(lines []string) (Result, error) {
	for i, l := range lines {
		if m := urlRE.FindString(l); m != "" {
			return valueResult(strings.TrimRight(m, ".,;:!?"), i+1)
		}
	}
	return Result{}, fmt.Errorf("%w: no url", ErrNotFound)
}

func pickAfter(arg string, lines []string) (Result, error) {
	if arg == "" {
		return Result{}, fmt.Errorf("%w: after wants a marker", ErrBadExpr)
	}
	for i, l := range lines {
		if _, rest, ok := strings.Cut(l, arg); ok {
			return valueResult(strings.TrimSpace(rest), i+1)
		}
	}
	return Result{}, fmt.Errorf("%w: marker %q", ErrNotFound, arg)
}

// pickBetween takes `'a','b'` (each optionally quoted) and returns the text
// between the first a and the following b on the same line.
func pickBetween(arg string, lines []string) (Result, error) {
	a, b, ok := splitTwo(arg)
	if !ok || a == "" || b == "" {
		return Result{}, fmt.Errorf("%w: between wants two quoted markers", ErrBadExpr)
	}
	for i, l := range lines {
		_, rest, found := strings.Cut(l, a)
		if !found {
			continue
		}
		if mid, _, found2 := strings.Cut(rest, b); found2 {
			return valueResult(strings.TrimSpace(mid), i+1)
		}
	}
	return Result{}, fmt.Errorf("%w: markers %q %q", ErrNotFound, a, b)
}

// splitTwo splits `'a','b'` or `a,b` into two unquoted parts.
func splitTwo(arg string) (string, string, bool) {
	parts := splitQuotedList(arg)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// splitQuotedList splits on commas outside quotes and strips quotes.
func splitQuotedList(arg string) []string {
	var out []string
	var cur strings.Builder
	var q byte
	for i := 0; i < len(arg); i++ {
		c := arg[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			q = c
		case c == ',':
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, strings.TrimSpace(cur.String()))
	return out
}

// --- document tier (markdown) -----------------------------------------------

// headingLevel returns the ATX heading level of a line, or 0.
func headingLevel(l string) int {
	n := 0
	for n < len(l) && l[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(l) || l[n] != ' ' {
		return 0
	}
	return n
}

func headingText(l string) string {
	return strings.TrimSpace(strings.TrimLeft(l, "#"))
}

// isFence reports a code fence opener or closer (backticks or tildes).
func isFence(l string) bool {
	t := strings.TrimSpace(l)
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

func pickHeading(lines []string) (Result, error) {
	for i, l := range lines {
		if headingLevel(l) > 0 {
			return valueResult(headingText(l), i+1)
		}
	}
	return Result{}, fmt.Errorf("%w: no heading", ErrNotFound)
}

// pickSection returns the heading whose text equals arg together with every
// line until the next heading of the same or higher level (§7.1: skipped
// levels are handled by "level <= defining one").
func pickSection(arg string, lines []string) (Result, error) {
	if arg == "" {
		return Result{}, fmt.Errorf("%w: section wants a title", ErrBadExpr)
	}
	inFence := false
	for i, l := range lines {
		if isFence(l) {
			inFence = !inFence
			continue
		}
		lvl := headingLevel(l)
		if inFence || lvl == 0 || headingText(l) != arg {
			continue
		}
		end := len(lines)
		fenced := false
		for j := i + 1; j < len(lines); j++ {
			if isFence(lines[j]) {
				fenced = !fenced
				continue
			}
			// A `#` inside a code fence is not a heading and must not end the
			// section; the fuzz-style markdown fixture pins this.
			if hl := headingLevel(lines[j]); !fenced && hl > 0 && hl <= lvl {
				end = j
				break
			}
		}
		return rangeResult(lines, i+1, end), nil
	}
	return Result{}, fmt.Errorf("%w: section %q", ErrNotFound, arg)
}

// pickParagraph returns the N-th paragraph: a run of non-blank lines separated
// by blank lines, ignoring headings and fenced code.
func pickParagraph(arg string, lines []string) (Result, error) {
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 {
		return Result{}, fmt.Errorf("%w: paragraph wants a positive number, got %q", ErrBadExpr, arg)
	}
	count := 0
	inFence := false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if isFence(l) {
			inFence = !inFence
			continue
		}
		if inFence || strings.TrimSpace(l) == "" || headingLevel(l) > 0 {
			continue
		}
		start := i
		for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" && headingLevel(lines[i+1]) == 0 && !isFence(lines[i+1]) {
			i++
		}
		count++
		if count == n {
			return rangeResult(lines, start+1, i+1), nil
		}
	}
	return Result{}, fmt.Errorf("%w: paragraph %d of %d", ErrNotFound, n, count)
}

// linkRE matches an inline markdown link and captures its text.
var linkRE = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// pickLink returns the text of the N-th inline link, which is how an inline
// fact def `[8081](ds:def?id=…)` yields its value.
func pickLink(arg string, lines []string) (Result, error) {
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 {
		return Result{}, fmt.Errorf("%w: link wants a positive number, got %q", ErrBadExpr, arg)
	}
	count := 0
	for i, l := range lines {
		for _, m := range linkRE.FindAllStringSubmatch(l, -1) {
			count++
			if count == n {
				return valueResult(m[1], i+1)
			}
		}
	}
	return Result{}, fmt.Errorf("%w: link %d of %d", ErrNotFound, n, count)
}

// --- structured tier ------------------------------------------------------

// pickJSON walks a dotted path with optional [n] indexes over the parsed
// document. A leading `$.` is accepted for JSONPath familiarity. Scalars come
// back as their JSON text; an object or array is a range of its pretty form,
// because a cite of a whole object is a block, not a value.
func pickJSON(arg, content string) (Result, error) {
	var doc any
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		return Result{}, fmt.Errorf("%w: json: %v", ErrNotFound, err)
	}
	path := strings.TrimPrefix(strings.TrimPrefix(arg, "$"), ".")
	cur := doc
	// sc follows the same path through the text, so the result carries the
	// lines the value is written on rather than line 1 (bug 45).
	sc := &jsonScan{s: content}
	sc.ws()
	if path != "" {
		for _, seg := range strings.Split(path, ".") {
			key, idx, hasIdx, err := splitIndex(seg)
			if err != nil {
				return Result{}, err
			}
			if key != "" {
				m, ok := cur.(map[string]any)
				if !ok {
					return Result{}, fmt.Errorf("%w: json path %q: not an object at %q", ErrNotFound, arg, key)
				}
				cur, ok = m[key]
				if !ok {
					return Result{}, fmt.Errorf("%w: json path %q: no key %q", ErrNotFound, arg, key)
				}
			}
			if hasIdx {
				arr, ok := cur.([]any)
				if !ok || idx < 0 || idx >= len(arr) {
					return Result{}, fmt.Errorf("%w: json path %q: bad index %d", ErrNotFound, arg, idx)
				}
				cur = arr[idx]
			}
			sc.step(key, idx, hasIdx)
		}
	}
	start, end := sc.lines()
	switch v := cur.(type) {
	case map[string]any, []any:
		// The text stays the pretty form, as it always was, because it is
		// what the block hashes; only the position is the source's.
		b, _ := json.MarshalIndent(v, "", "  ")
		ls := splitLines(string(b))
		r := rangeResult(ls, 1, len(ls))
		r.Start, r.End = start, end
		return r, nil
	case string:
		return valueResult(v, start)
	default:
		b, _ := json.Marshal(v)
		return valueResult(string(b), start)
	}
}

// jsonScan walks the text of a JSON document already known to be valid, to
// find where a value is written. Unmarshal answers what the value is and
// keeps no positions, so the remote def of a JSON key used to be recorded at
// line 1 whatever line the key was on: `ds locate`, permalinks and the
// ledger all pointed at the top of the file.
type jsonScan struct {
	s string
	i int
}

// ws skips whitespace.
func (p *jsonScan) ws() {
	for p.i < len(p.s) && strings.IndexByte(" \t\r\n", p.s[p.i]) >= 0 {
		p.i++
	}
}

// str skips a string token and returns its decoded text.
func (p *jsonScan) str() string {
	start := p.i
	for p.i++; p.i < len(p.s) && p.s[p.i] != '"'; p.i++ {
		if p.s[p.i] == '\\' {
			p.i++
		}
	}
	p.i++
	var out string
	// The document parsed, so every string token in it decodes.
	_ = json.Unmarshal([]byte(p.s[start:p.i]), &out)
	return out
}

// skip moves past one value of any kind.
func (p *jsonScan) skip() {
	switch c := p.s[p.i]; c {
	case '"':
		p.str()
	case '{', '[':
		depth := 0
		for ; p.i < len(p.s); p.i++ {
			switch p.s[p.i] {
			case '"':
				p.str()
				p.i--
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					p.i++
					return
				}
			}
		}
	default:
		for p.i < len(p.s) && strings.IndexByte(",}] \t\r\n", p.s[p.i]) < 0 {
			p.i++
		}
	}
}

// step moves from the value at p.i to its member key (the last one written,
// which is the one Unmarshal keeps) and then to element idx. pickJSON has
// already walked the same path through the decoded value, so every step
// exists.
func (p *jsonScan) step(key string, idx int, hasIdx bool) {
	if key != "" {
		at := p.i
		p.i++ // {
		for p.ws(); p.s[p.i] != '}'; p.ws() {
			k := p.str()
			p.ws()
			p.i++ // :
			p.ws()
			if k == key {
				at = p.i
			}
			p.skip()
			p.ws()
			if p.s[p.i] == ',' {
				p.i++
				p.ws()
			}
		}
		p.i = at
	}
	if hasIdx {
		p.i++ // [
		p.ws()
		for n := 0; n < idx; n++ {
			p.skip()
			p.ws()
			p.i++ // ,
			p.ws()
		}
	}
}

// lines returns the first and last line of the value at p.i.
func (p *jsonScan) lines() (int, int) {
	start := 1 + strings.Count(p.s[:p.i], "\n")
	from := p.i
	p.skip()
	return start, start + strings.Count(p.s[from:p.i], "\n")
}

// splitIndex parses `key`, `key[2]`, or `[2]`.
func splitIndex(seg string) (key string, idx int, hasIdx bool, err error) {
	open := strings.IndexByte(seg, '[')
	if open < 0 {
		return seg, 0, false, nil
	}
	if !strings.HasSuffix(seg, "]") {
		return "", 0, false, fmt.Errorf("%w: bad index in %q", ErrBadExpr, seg)
	}
	n, convErr := strconv.Atoi(seg[open+1 : len(seg)-1])
	if convErr != nil {
		return "", 0, false, fmt.Errorf("%w: bad index in %q", ErrBadExpr, seg)
	}
	return seg[:open], n, true, nil
}

// pickEnv finds `NAME=value` in dotenv-style content, ignoring comments and
// an optional `export ` prefix, and strips one layer of matching quotes.
func pickEnv(arg string, lines []string) (Result, error) {
	if arg == "" {
		return Result{}, fmt.Errorf("%w: env wants a name", ErrBadExpr)
	}
	for i, l := range lines {
		t := strings.TrimSpace(l)
		t = strings.TrimPrefix(t, "export ")
		k, v, ok := strings.Cut(t, "=")
		if !ok || strings.TrimSpace(k) != arg || strings.HasPrefix(t, "#") {
			continue
		}
		return valueResult(unquote(stripInlineComment(v)), i+1)
	}
	return Result{}, fmt.Errorf("%w: env %s", ErrNotFound, arg)
}

// pickINI finds `key` under `[section]`; `section.key` addresses it, and a
// bare `key` addresses the top-level (pre-section) area. Also serves
// `.properties` files, which are INI without sections.
func pickINI(arg string, lines []string) (Result, error) {
	section, key, hasSection := strings.Cut(arg, ".")
	if !hasSection {
		key, section = section, ""
	}
	if key == "" {
		return Result{}, fmt.Errorf("%w: ini wants section.key or key", ErrBadExpr)
	}
	cur := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || t[0] == '#' || t[0] == ';' {
			continue
		}
		if t[0] == '[' && strings.HasSuffix(t, "]") {
			cur = t[1 : len(t)-1]
			continue
		}
		if cur != section {
			continue
		}
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			k, v, ok = strings.Cut(t, ":")
		}
		if ok && strings.TrimSpace(k) == key {
			return valueResult(unquote(stripInlineComment(v)), i+1)
		}
	}
	return Result{}, fmt.Errorf("%w: ini %s", ErrNotFound, arg)
}

// pickCSV supports `col=name` (a Range of that column's values, header
// excluded) and `rNcM` (one cell, 1-based, header is row 1).
func pickCSV(arg, content string) (Result, error) {
	records, err := csv.NewReader(strings.NewReader(content)).ReadAll()
	if err != nil {
		return Result{}, fmt.Errorf("%w: csv: %v", ErrNotFound, err)
	}
	if len(records) == 0 {
		return Result{}, fmt.Errorf("%w: csv empty", ErrNotFound)
	}
	if name, ok := strings.CutPrefix(arg, "col="); ok {
		col := -1
		for i, h := range records[0] {
			if strings.TrimSpace(h) == name {
				col = i
				break
			}
		}
		if col < 0 {
			return Result{}, fmt.Errorf("%w: csv column %q", ErrNotFound, name)
		}
		vals := make([]string, 0, len(records)-1)
		for _, r := range records[1:] {
			if col < len(r) {
				vals = append(vals, r[col])
			}
		}
		if len(vals) == 0 {
			return Result{}, fmt.Errorf("%w: csv column %q has no rows", ErrNotFound, name)
		}
		return Result{Kind: KindRange, Text: strings.Join(vals, "\n"), Start: 2, End: len(records)}, nil
	}
	var r, c int
	if n, _ := fmt.Sscanf(arg, "r%dc%d", &r, &c); n != 2 || r < 1 || c < 1 {
		return Result{}, fmt.Errorf("%w: csv wants col=name or rNcM, got %q", ErrBadExpr, arg)
	}
	if r > len(records) || c > len(records[r-1]) {
		return Result{}, fmt.Errorf("%w: csv cell r%dc%d", ErrNotFound, r, c)
	}
	return valueResult(records[r-1][c-1], r)
}

// pickYAML is a line-mode walker for the common shape of config files: nested
// maps of scalars with two-or-more-space indentation. It resolves `a.b.c` by
// tracking indentation, strips inline comments and one layer of quotes. It is
// not a YAML parser; anchors, flow style, and multi-line scalars belong to the
// structured tier in ext/structured, and content using them yields ErrNotFound
// here rather than a wrong value.
func pickYAML(arg string, lines []string) (Result, error) {
	if arg == "" {
		return Result{}, fmt.Errorf("%w: yaml wants a key path", ErrBadExpr)
	}
	// One grammar with the config tier (internal/keypath): a key ends at a
	// colon followed by whitespace, so `tasks.wfsys:up` names the Taskfile
	// task `wfsys:up`, and a segment holding a dot is quoted (bug 135).
	want := keypath.Split(arg)
	depth := 0
	var stack []int // indentation of each matched ancestor
	for i, l := range lines {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		indent := len(l) - len(strings.TrimLeft(l, " "))
		for len(stack) > 0 && indent <= stack[len(stack)-1] {
			stack = stack[:len(stack)-1]
			depth--
		}
		t := strings.TrimSpace(l)
		end := keypath.End(t, true)
		if end < 0 || keypath.Unquote(strings.TrimSpace(t[:end])) != want[depth] {
			continue
		}
		rest := t[end+1:]
		rest = strings.TrimSpace(stripInlineComment(rest))
		if depth == len(want)-1 {
			if rest == "" || isBlockScalar(rest) {
				// A mapping, a sequence or a block scalar: the key and
				// everything under it, as a range for ds:block. It used to
				// be refused as a multi-line value, so a remote def could not
				// bind a Taskfile task or a compose service without a
				// directive written into that file (bug 132).
				return rangeResult(lines, i+1, yamlSubtreeEnd(lines, i, indent)), nil
			}
			return valueResult(unquote(rest), i+1)
		}
		if rest != "" {
			return Result{}, fmt.Errorf("%w: yaml %s: %q is a scalar, expected a map", ErrNotFound, arg, want[depth])
		}
		stack = append(stack, indent)
		depth++
	}
	return Result{}, fmt.Errorf("%w: yaml %s", ErrNotFound, arg)
}

// yamlSubtreeEnd returns the 1-based last line of the subtree whose key is on
// line i at the given indentation: every following line indented deeper, plus
// sequence items written at the key's own indentation (`key:` then `- x`,
// which YAML allows), with trailing blank and comment lines left out.
func yamlSubtreeEnd(lines []string, i, indent int) int {
	end := i + 1
	for j := i + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		in := len(lines[j]) - len(strings.TrimLeft(lines[j], " "))
		if in < indent || (in == indent && t != "-" && !strings.HasPrefix(t, "- ")) {
			break
		}
		end = j + 1
	}
	return end
}

// isBlockScalar reports a YAML literal or folded block indicator (`|`, `>`,
// with optional chomping and indentation modifiers): the value continues on
// following lines, so it is never a one-line value.
func isBlockScalar(rest string) bool {
	return rest != "" && (rest[0] == '|' || rest[0] == '>') && strings.Trim(rest[1:], "+-0123456789") == ""
}

// pickTOML resolves `table.key` or `key` in line-mode TOML: `[table]` headers,
// `key = value` pairs, comments stripped, one layer of quotes removed. Dotted
// keys inside a table (`a.b = 1`) are matched by their full path.
func pickTOML(arg string, lines []string) (Result, error) {
	if arg == "" {
		return Result{}, fmt.Errorf("%w: toml wants a key path", ErrBadExpr)
	}
	table := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || t[0] == '#' {
			continue
		}
		if t[0] == '[' && strings.HasSuffix(t, "]") {
			table = strings.Trim(t, "[]")
			if strings.TrimSpace(table) == arg {
				// A whole table: its header and every line up to the next
				// header, as a range for ds:block (bug 132).
				return rangeResult(lines, i+1, tomlTableEnd(lines, i)), nil
			}
			continue
		}
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		full := strings.TrimSpace(k)
		if table != "" {
			full = table + "." + full
		}
		if full == arg {
			return valueResult(unquote(stripInlineComment(v)), i+1)
		}
	}
	return Result{}, fmt.Errorf("%w: toml %s", ErrNotFound, arg)
}

// tomlTableEnd returns the 1-based last line of the table whose header is on
// line i: the line before the next header, with trailing blank and comment
// lines left out.
func tomlTableEnd(lines []string, i int) int {
	end := i + 1
	for j := i + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if strings.HasPrefix(t, "[") {
			break
		}
		if t != "" && !strings.HasPrefix(t, "#") {
			end = j + 1
		}
	}
	return end
}

// KeyValue returns the value part of a `key: value` or `key = value` line
// with any inline comment and one layer of quotes removed. It is the default
// pick for config hosts (§10): the visible value, not the key line, is what
// a fact means. ok is false when the line has no separator.
func KeyValue(line string) (string, bool) {
	t := strings.TrimSpace(line)
	i := strings.IndexAny(t, ":=")
	if i <= 0 {
		return "", false
	}
	return unquote(stripInlineComment(t[i+1:])), true
}

// stripInlineComment removes a trailing ` # …` outside quotes.
func stripInlineComment(v string) string {
	var q byte
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '#' && (i == 0 || v[i-1] == ' ' || v[i-1] == '\t'):
			return v[:i]
		}
	}
	return v
}

// unquote trims space and one layer of matching quotes.
func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}
