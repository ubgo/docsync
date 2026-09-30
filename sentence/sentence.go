// Package sentence decides what prose a reference binds to (docs/SPEC.md §18).
//
// A reference in running text binds to its enclosing sentence. `.`, `!`, or
// `?` ends a sentence only when whitespace follows and then an uppercase
// letter, a digit, a quote or backtick, or an opening bracket — or the text
// ends. So "v1.2 and", "Fig. 3" and "e.g. this" do not split, and a short
// fixed list of abbreviations (Abbreviations) and a run of three or more
// dots never end one either. Inside a list item or a table cell it binds to
// the whole item or cell. The markdown extractor hands Bind a whole
// paragraph, joined, so a sentence wrapped across lines is one sentence.
//
// Two references in one sentence share one ack because they share one
// sentence hash. This is the unit a reviewer approves, so the rule has to be
// boring and stable: an ack must survive a paragraph moving, re-wrapping, or
// re-spacing, and must be invalidated when the sentence is rewritten. Runs
// of whitespace in the result are collapsed to one space for that reason.
package sentence

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ubgo/docsync/internal/mdspan"
)

// Terminators end a sentence when followed by whitespace or end of text.
const Terminators = ".!?"

// Bind returns the sentence, list item, or table cell of line that contains
// byte offset col. If col is out of range the whole line is returned so a
// caller with an imprecise column still gets a sensible binding.
func Bind(line string, col int) string {
	if col < 0 || col > len(line) {
		col = 0
	}
	// A column at or past the last non-space character belongs to the last
	// sentence, not to an empty span after its terminator. Trailing spaces are
	// common when a directive comment follows prose on the same line.
	if last := len(strings.TrimRight(line, " \t")); col >= last && last > 0 {
		col = last - 1
	}
	if cell, ok := tableCell(line, col); ok {
		return collapse(cell)
	}
	if item, ok := listItem(line); ok {
		return collapse(item)
	}
	start, end := sentenceBounds(line, col)
	return collapse(line[start:end])
}

// collapse joins s's words with single spaces: re-wrapping or re-spacing a
// sentence is not rewriting it.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// Abbreviations never end a sentence. The list is short, English, and fixed
// on purpose: it covers what technical prose uses, and a term not on it
// splits as before, so the list can only remove false splits. Compared
// lowercased.
var Abbreviations = map[string]bool{
	"e.g.": true, "i.e.": true, "etc.": true, "vs.": true, "cf.": true, "viz.": true,
	"fig.": true, "no.": true, "dr.": true, "mr.": true, "mrs.": true, "ms.": true,
	"st.": true, "u.s.": true, "u.k.": true,
}

// startsSentence reports whether r can begin a sentence after a terminator:
// an uppercase letter, a digit, a quote or backtick, or an opening bracket.
func startsSentence(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsDigit(r) || strings.ContainsRune("\"'`“‘([{", r)
}

// Split returns every sentence in text, in order, trimmed and non-empty.
// Line breaks always split. Used by tests and by tools that count sentences.
func Split(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		i := 0
		for i < len(line) {
			// sentenceBounds always returns end > i: either a terminator at or
			// after i (end = its index + 1) or len(line).
			_, end := sentenceBounds(line, i)
			if s := strings.TrimSpace(line[i:end]); s != "" {
				out = append(out, s)
			}
			i = end
		}
	}
	return out
}

// sentenceBounds returns [start,end) of the sentence containing col within a
// single line. A terminator ends a sentence only when followed by whitespace
// or end of line, so "1.5" and "e.g.x" do not split.
func sentenceBounds(line string, col int) (int, int) {
	inside := protected(line)
	start := 0
	for i := 0; i < col && i < len(line); i++ {
		if end, ok := terminatorEnd(line, i); ok && end <= col && !inside[i] {
			start = end
		}
	}
	end := len(line)
	for i := col; i < len(line); i++ {
		if e, ok := terminatorEnd(line, i); ok && !inside[i] {
			end = e
			break
		}
	}
	// Skip leading whitespace so start points at the first word. start can
	// never exceed end: every candidate start is <= col and end > col.
	for start < end && unicode.IsSpace(rune(line[start])) {
		start++
	}
	return start, end
}

// protected marks the bytes of line inside a link or an inline code span,
// where a terminator is not the end of a sentence. `[e.g. Save](…)` split
// at "e.g. " bound the sentence "Writes go through [e.g." — cut inside the
// citation, which it no longer even contained.
func protected(line string) []bool {
	in := make([]bool, len(line))
	for _, spans := range [][]mdspan.Span{mdspan.CodeSpans(line), mdspan.Links(line)} {
		for _, sp := range spans {
			for k := sp.Start; k < sp.End; k++ {
				in[k] = true
			}
		}
	}
	return in
}

// terminatorEnd reports whether a sentence terminator sits at i and, if so,
// the index just past it and any closing quotes or brackets that follow, which
// stay with the sentence they close. A terminator counts only when followed by
// whitespace or end of line, so "1.5" and "e.g.x" do not split.
func terminatorEnd(line string, i int) (int, bool) {
	if strings.IndexByte(Terminators, line[i]) < 0 {
		return 0, false
	}
	j := i + 1
	for j < len(line) && strings.ContainsRune(`"')]`, rune(line[j])) {
		j++
	}
	if j < len(line) && !unicode.IsSpace(rune(line[j])) {
		return 0, false
	}
	if line[i] == '.' && (dotRun(line, i) >= 3 || Abbreviations[strings.ToLower(wordEndingAt(line, i))]) {
		return 0, false
	}
	// What follows the whitespace decides: a sentence starts with a capital,
	// a digit, a quote, or a bracket. The end of the text is an end too.
	next := strings.TrimLeftFunc(line[j:], unicode.IsSpace)
	if next == "" {
		return j, true
	}
	r, _ := utf8.DecodeRuneInString(next)
	if !startsSentence(r) {
		return 0, false
	}
	return j, true
}

// dotRun is the length of the run of dots ending at line[i]. It is only
// asked about a dot followed by whitespace, so i always ends its run.
func dotRun(line string, i int) int {
	s := i
	for s > 0 && line[s-1] == '.' {
		s--
	}
	return i - s + 1
}

// wordEndingAt is the word that ends at line[i], without the quotes or
// brackets that may open it: "(e.g." reads as "e.g.".
func wordEndingAt(line string, i int) string {
	s := i
	for s > 0 && !unicode.IsSpace(rune(line[s-1])) {
		s--
	}
	return strings.TrimLeft(line[s:i+1], "\"'`“‘([{")
}

// listMarkers are the prefixes that make a line a list item. Ordered lists
// are detected separately (digits then `.` or `)`).
var listMarkers = []string{"- ", "* ", "+ "}

// listItem reports whether line is a list item and returns its full text with
// the marker and checkbox removed. The whole item is the binding unit.
func listItem(line string) (string, bool) {
	t := strings.TrimLeft(line, " \t")
	for _, m := range listMarkers {
		if strings.HasPrefix(t, m) {
			return strings.TrimSpace(stripCheckbox(t[len(m):])), true
		}
	}
	// Ordered: 1. or 1)
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(t) && (t[i] == '.' || t[i] == ')') && t[i+1] == ' ' {
		return strings.TrimSpace(t[i+2:]), true
	}
	return "", false
}

// stripCheckbox removes a leading task checkbox `[ ]`, `[x]`, `[X]`. As in
// GFM, a checkbox is followed by whitespace or ends the item: `[x](…)` is a
// link whose text is x, and stripping it cut the citation's own text off.
func stripCheckbox(s string) string {
	if len(s) >= 3 && s[0] == '[' && s[2] == ']' && (s[1] == ' ' || s[1] == 'x' || s[1] == 'X') && (len(s) == 3 || s[3] == ' ' || s[3] == '\t') {
		return s[3:]
	}
	return s
}

// tableCell reports whether line is a pipe-table row and, if so, returns the
// cell containing col. A row is a line whose trimmed form starts with `|` or
// contains an unescaped ` | `. Separator rows (`|---|`) are not cells.
func tableCell(line string, col int) (string, bool) {
	t := strings.TrimSpace(line)
	if t == "" || !(strings.HasPrefix(t, "|") || strings.Contains(t, " | ")) {
		return "", false
	}
	if strings.Trim(t, "|-: ") == "" {
		return "", false
	}
	// Walk the raw line so col maps onto the right cell. col is within
	// [0,len(line)] (Bind clamps it), so one of the segments always contains
	// it and the loop always returns.
	start := 0
	for i := 0; ; i++ {
		if i == len(line) || line[i] == '|' {
			if col >= start && col <= i {
				return strings.TrimSpace(line[start:i]), true
			}
			start = i + 1
		}
	}
}
