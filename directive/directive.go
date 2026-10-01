// Package directive parses and formats the single docsync directive shape,
//
//	ds:<verb> key=value key=value …
//
// as it appears inside any host comment (`// ds:def id=x`, `# ds:def id=x`,
// `<!-- ds:block id=x -->`) and inside markdown link targets as a URL scheme
// (`ds:block?id=x&lines=1-6`). See docs/SPEC.md §7.
//
// Why this package exists on its own: every other part of docsync (scanner,
// verbs, ledger, renderer) consumes Directive values and must never re-parse
// text. The grammar is deliberately tiny — no positionals, no escape
// character — so the whole thing fits in one file and can be fuzzed to
// exhaustion. The scanner searches for the prefix only; verbs are resolved by a
// registry elsewhere, so this package accepts any verb name that matches the
// identifier rule.
//
// Invariants callers may rely on:
//   - Parse(Format(d)) == d for every well-formed Directive (pinned by tests).
//   - A repeated key is an error, never a silent overwrite.
//   - Unknown verbs and keys are NOT errors here; policy lives in the caller.
package directive

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DefaultPrefix is the prefix the scanner searches for when a workspace has not
// configured another. It is glued to the verb (`ds:def`) precisely so that the
// English word "ds" in a comment can never start a directive.
const DefaultPrefix = "ds"

// Separator joins prefix and verb. It is a constant rather than a literal at
// use sites because both the comment carrier and the link carrier depend on it.
const Separator = ":"

// Sentinel errors. Callers branch with errors.Is; messages carry positions.
var (
	// ErrNoDirective is returned when the text does not start with the prefix.
	// It is the common "not for us" case and callers must treat it as a skip,
	// not a failure.
	ErrNoDirective = errors.New("directive: text does not start with the prefix")
	// ErrBadVerb is returned when the token after the prefix is not an identifier.
	ErrBadVerb = errors.New("directive: verb must match [a-z][a-z0-9_]*")
	// ErrPositional is returned for a bare word where key=value was expected.
	// The grammar has no positionals on purpose (§7): one shape for every verb.
	ErrPositional = errors.New("directive: bare word where key=value was expected")
	// ErrBadKey is returned when a key is not an identifier.
	ErrBadKey = errors.New("directive: key must match [a-z][a-z0-9_]*")
	// ErrDuplicateKey is returned when the same key appears twice. Never a
	// silent overwrite: a duplicate is almost always a copy-paste mistake.
	ErrDuplicateKey = errors.New("directive: duplicate key")
	// ErrUnterminatedQuote is returned when a quoted value has no closing quote.
	ErrUnterminatedQuote = errors.New("directive: unterminated quoted value")
	// ErrBadLink is returned when a link target has the prefix but is malformed.
	ErrBadLink = errors.New("directive: malformed link target")
)

// Directive is one parsed directive. Args preserves insertion order through
// Keys so Format is deterministic and round-trips.
type Directive struct {
	// Verb is the word after the prefix, e.g. "def", "block". Never empty.
	Verb string
	// Args holds key=value pairs. Values are unquoted; quoting is a carrier
	// detail removed at parse time and re-added by Format only when needed.
	Args map[string]string
	// Keys is the order keys appeared in the source. Format emits in this order
	// so a parse→format→parse cycle is byte-stable. Len(Keys) == len(Args).
	Keys []string
}

// Get returns the value for key and whether it was present. A present key with
// an empty value (`empty=`) reports ok=true, value "".
func (d Directive) Get(key string) (string, bool) {
	v, ok := d.Args[key]
	return v, ok
}

// Has reports whether key is present, regardless of value.
func (d Directive) Has(key string) bool {
	_, ok := d.Args[key]
	return ok
}

// List splits a comma-separated value into trimmed, non-empty items. Lists are
// carried inside one value (`tags=a,b,c`) because the grammar has no list
// syntax of its own (§7). A missing key yields nil.
func (d Directive) List(key string) []string {
	v, ok := d.Args[key]
	if !ok {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseError reports where in the text a parse failed. Column is a zero-based
// byte offset into the text handed to Parse, so a caller that knows the line
// can point an editor at the exact spot.
type ParseError struct {
	Column int
	Err    error
}

func (e *ParseError) Error() string { return fmt.Sprintf("column %d: %v", e.Column, e.Err) }

// Unwrap lets callers use errors.Is against the sentinels above.
func (e *ParseError) Unwrap() error { return e.Err }

// Parse parses the body of a comment: the text with the host's comment
// delimiters already stripped. Leading and trailing whitespace is ignored.
// Continuation folding (§7, "a comment line directly below starting with
// whitespace and key=value") is the caller's job because only the caller knows
// line structure; Fold does that step once the lines are collected.
//
// The prefix must be the very first non-space token and must be glued to the
// verb with Separator. Text that does not start that way returns
// ErrNoDirective so scanners can cheaply skip ordinary comments.
func Parse(prefix, text string) (Directive, error) {
	s := strings.TrimSpace(text)
	head := prefix + Separator
	if !strings.HasPrefix(s, head) {
		return Directive{}, &ParseError{Column: 0, Err: ErrNoDirective}
	}
	rest := s[len(head):]
	verbEnd := identEnd(rest)
	if verbEnd == 0 {
		return Directive{}, &ParseError{Column: len(head), Err: ErrBadVerb}
	}
	verb := rest[:verbEnd]
	tail := rest[verbEnd:]
	// The verb must be followed by whitespace or end of text; "ds:defx" with x
	// not an identifier char is impossible by construction of identEnd, but
	// "ds:def=1" would leave "=1" here and must be rejected as a bad verb.
	if tail != "" && spaceAt(tail, 0) == 0 {
		return Directive{}, &ParseError{Column: len(head) + verbEnd, Err: ErrBadVerb}
	}
	d := Directive{Verb: verb, Args: map[string]string{}}
	offset := len(s) - len(tail)
	if err := parseArgs(&d, tail, offset); err != nil {
		return Directive{}, err
	}
	return d, nil
}

// parseArgs consumes `key=value` pairs from tail, appending to d. offset is the
// byte position of tail within the original text, used for error columns.
func parseArgs(d *Directive, tail string, offset int) error {
	i := 0
	for {
		for i < len(tail) {
			w := spaceAt(tail, i)
			if w == 0 {
				break
			}
			i += w
		}
		if i >= len(tail) {
			return nil
		}
		start := i
		keyEnd := i + identEnd(tail[i:])
		if keyEnd == i {
			return &ParseError{Column: offset + start, Err: ErrBadKey}
		}
		if keyEnd >= len(tail) || tail[keyEnd] != '=' {
			// A bare word, or a word followed by whitespace/other: no '='.
			return &ParseError{Column: offset + start, Err: ErrPositional}
		}
		key := tail[i:keyEnd]
		i = keyEnd + 1
		value, next, err := parseValue(tail, i)
		if err != nil {
			return &ParseError{Column: offset + i, Err: err}
		}
		i = next
		if _, dup := d.Args[key]; dup {
			return &ParseError{Column: offset + start, Err: ErrDuplicateKey}
		}
		d.Args[key] = value
		d.Keys = append(d.Keys, key)
	}
}

// parseValue reads a value starting at i. Three forms (§7): bare up to
// whitespace, "double quoted" (may contain single quotes), 'single quoted' (may
// contain double quotes). There is no escape character by design; a value that
// needs both quote kinds goes on a continuation line as raw text via Fold.
func parseValue(s string, i int) (value string, next int, err error) {
	if i >= len(s) {
		return "", i, nil
	}
	switch q := s[i]; q {
	case '"', '\'':
		end := strings.IndexByte(s[i+1:], q)
		if end < 0 {
			return "", i, ErrUnterminatedQuote
		}
		return s[i+1 : i+1+end], i + 1 + end + 1, nil
	default:
		j := i
		for j < len(s) && spaceAt(s, j) == 0 {
			j++
		}
		return s[i:j], j, nil
	}
}

// spaceAt returns the byte width of the whitespace rune starting at s[i], or
// 0 when there is none there.
//
// It decodes UTF-8 rather than testing one byte, and it is the only
// whitespace test the parser makes, because quote decides what needs
// quoting the same way. Testing bytes read 0xA0 — half of a no-break space,
// which a Mac types on Option+Space — as U+00A0 and ended a bare value
// inside the character; and a lone invalid byte that quote left bare was
// read back as a separator, so a value was lost in the round trip. An
// invalid byte is never whitespace.
func spaceAt(s string, i int) int {
	r, w := utf8.DecodeRuneInString(s[i:])
	if r == utf8.RuneError || !unicode.IsSpace(r) {
		return 0
	}
	return w
}

// identEnd returns the length of the longest prefix of s matching
// [a-z][a-z0-9_]*, or 0 if s does not start with a lowercase letter.
func identEnd(s string) int {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return 0
	}
	j := 1
	for j < len(s) {
		c := s[j]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			j++
			continue
		}
		break
	}
	return j
}

// ParseLink parses a markdown link target in URL-scheme form:
//
//	ds:block?id=sess-save-k7m2p4xq&lines=1-6
//
// The scheme is the prefix, the path is the verb, and the query carries the
// same key=value pairs as the comment form (§7.1). Values are percent-decoded.
// A repeated key is an error exactly as in Parse. A target that does not begin
// with the prefix scheme returns ErrNoDirective.
func ParseLink(prefix, target string) (Directive, error) {
	head := prefix + Separator
	if !strings.HasPrefix(target, head) {
		return Directive{}, &ParseError{Column: 0, Err: ErrNoDirective}
	}
	rest := target[len(head):]
	verb, query, _ := strings.Cut(rest, "?")
	if verb == "" || identEnd(verb) != len(verb) {
		return Directive{}, &ParseError{Column: len(head), Err: ErrBadVerb}
	}
	d := Directive{Verb: verb, Args: map[string]string{}}
	if query == "" {
		return d, nil
	}
	// Parse pairs by hand rather than url.ParseQuery so that order is kept and
	// a bare token (no '=') is rejected as positional, matching the comment form.
	col := len(head) + len(verb) + 1
	pairs, err := splitPairs(query)
	if err != nil {
		return Directive{}, &ParseError{Column: col, Err: err}
	}
	for _, pair := range pairs {
		if pair == "" {
			return Directive{}, &ParseError{Column: col, Err: ErrBadLink}
		}
		key, rawVal, hasEq := strings.Cut(pair, "=")
		if identEnd(key) != len(key) || key == "" {
			return Directive{}, &ParseError{Column: col, Err: ErrBadKey}
		}
		if !hasEq {
			return Directive{}, &ParseError{Column: col, Err: ErrPositional}
		}
		val, err := linkValue(rawVal)
		if err != nil {
			return Directive{}, &ParseError{Column: col, Err: fmt.Errorf("%w: %v", ErrBadLink, err)}
		}
		if _, dup := d.Args[key]; dup {
			return Directive{}, &ParseError{Column: col, Err: ErrDuplicateKey}
		}
		d.Args[key] = val
		d.Keys = append(d.Keys, key)
		col += len(pair) + 1
	}
	return d, nil
}

// splitPairs splits a link query on `&`, except inside a quoted value: a
// value whose first character is `"` or `'` runs to the matching quote, which
// must end the pair. An unclosed quote is ErrUnterminatedQuote, as in Parse.
func splitPairs(query string) ([]string, error) {
	var out []string
	start := 0
	for i := 0; i <= len(query); i++ {
		if i == len(query) || query[i] == '&' {
			out = append(out, query[start:i])
			start = i + 1
			continue
		}
		if q := query[i]; (q == '"' || q == '\'') && i > start && query[i-1] == '=' && !strings.Contains(query[start:i-1], "=") {
			end := strings.IndexByte(query[i+1:], q)
			if end < 0 {
				return nil, ErrUnterminatedQuote
			}
			i += end + 1
		}
	}
	return out, nil
}

// linkValue decodes one link value. A value in quotes is taken as written,
// quotes removed and nothing decoded, which is how the comment form reads
// one: `title="TOAST"` is TOAST, where it used to keep its quotes and never
// equal a page title (bug 52), and a quoted value may hold `&`. Any other
// value is percent-decoded, as a query string is; FormatLink encodes a
// leading quote as %22, so its output never reads as quoted.
func linkValue(raw string) (string, error) {
	if len(raw) >= 2 && (raw[0] == '"' || raw[0] == '\'') && raw[len(raw)-1] == raw[0] {
		return raw[1 : len(raw)-1], nil
	}
	return url.QueryUnescape(raw)
}

// Fold merges continuation lines into a directive's text before parsing. A
// continuation is a comment line directly below the directive whose body starts
// with whitespace followed by key=value (§7). Callers pass the comment bodies
// (delimiters stripped) of the directive line and every following comment
// line; Fold returns the joined text and the number of lines consumed
// (including the first). It stops at the first line that is not a
// continuation. Fold never parses; it only decides which lines belong together,
// so a malformed continuation is reported by Parse with a column into the
// joined text.
func Fold(lines []string) (joined string, consumed int) {
	if len(lines) == 0 {
		return "", 0
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(lines[0]))
	consumed = 1
	for _, line := range lines[1:] {
		if !isContinuation(line) {
			break
		}
		b.WriteByte(' ')
		b.WriteString(strings.TrimSpace(line))
		consumed++
	}
	return b.String(), consumed
}

// isContinuation reports whether a comment body is a continuation line: it
// must begin with whitespace and its first token must be key=value.
func isContinuation(line string) bool {
	if line == "" || spaceAt(line, 0) == 0 {
		return false
	}
	t := strings.TrimSpace(line)
	n := identEnd(t)
	return n > 0 && n < len(t) && t[n] == '='
}

// Format renders a Directive in comment form using prefix, emitting keys in
// d.Keys order (falling back to sorted order for any key missing from Keys, so
// a hand-built Directive still formats deterministically). Values containing
// whitespace are double-quoted; values containing a double quote are
// single-quoted. A value containing both cannot be expressed on one line —
// that is the documented limit of the grammar — and Format returns an error
// rather than emitting something Parse would reject.
func Format(prefix string, d Directive) (string, error) {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(Separator)
	b.WriteString(d.Verb)
	for _, k := range orderedKeys(d) {
		v := d.Args[k]
		q, err := quote(v)
		if err != nil {
			return "", fmt.Errorf("directive: key %q: %w", k, err)
		}
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(q)
	}
	return b.String(), nil
}

// FormatLink renders a Directive as a markdown link target. Values are
// percent-encoded, so every value is expressible (unlike Format).
func FormatLink(prefix string, d Directive) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(Separator)
	b.WriteString(d.Verb)
	keys := orderedKeys(d)
	if len(keys) == 0 {
		return b.String()
	}
	b.WriteByte('?')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(d.Args[k]))
	}
	return b.String()
}

// ErrUnquotable is returned by Format for a value containing both quote kinds.
var ErrUnquotable = errors.New("directive: value contains both quote kinds; use a continuation line")

// quote picks the smallest quoting that Parse reads back to v.
//
// A bare value runs to the next whitespace and quotes inside it are literal
// (Parse only treats a quote specially in the first byte), so a value with no
// whitespace that does not start with a quote is emitted bare even if it
// contains both quote kinds — the fuzzer found `0"'` and it must round-trip.
// Only a value that must be quoted (empty, contains whitespace, or starts
// with a quote) is subject to the one-quote-kind limit.
func quote(v string) (string, error) {
	needsQuote := v == "" || strings.IndexFunc(v, unicode.IsSpace) >= 0 || v[0] == '"' || v[0] == '\''
	if !needsQuote {
		return v, nil
	}
	hasDQ := strings.ContainsRune(v, '"')
	hasSQ := strings.ContainsRune(v, '\'')
	switch {
	case hasDQ && hasSQ:
		return "", ErrUnquotable
	case hasDQ:
		return "'" + v + "'", nil
	default:
		return "\"" + v + "\"", nil
	}
}

// orderedKeys returns d.Keys plus any Args keys absent from Keys, sorted, so a
// Directive constructed by hand without Keys still formats deterministically.
func orderedKeys(d Directive) []string {
	seen := make(map[string]bool, len(d.Keys))
	out := make([]string, 0, len(d.Args))
	for _, k := range d.Keys {
		if _, ok := d.Args[k]; ok && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	var extra []string
	for k := range d.Args {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}
