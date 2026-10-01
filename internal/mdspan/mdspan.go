// Package mdspan finds the inline spans of a markdown line that are not
// running prose: code spans and links.
//
// It lives here because two readers need the same answer and neither may
// import the other: the extractor masks code spans so an example in code is
// not a directive, and the sentence binder must not end a sentence at a
// full stop inside a code span or a link's text. A second copy of the
// backtick rule is how the two would drift.
package mdspan

import (
	"regexp"
	"strings"
)

// Span is a half-open byte range [Start, End) of a line.
type Span struct{ Start, End int }

// CodeSpans returns every inline code span in line, backticks included. A
// span opens at a run of backticks and closes at the next run of exactly
// the same length; a run with no closer is literal text, as CommonMark
// reads it. A span continuing onto the next line is not seen.
func CodeSpans(line string) []Span {
	var out []Span
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		n := run(line, i)
		closer := -1
		for j := i + n; j < len(line); {
			if line[j] != '`' {
				j++
				continue
			}
			m := run(line, j)
			if m == n {
				closer = j
				break
			}
			j += m
		}
		if closer < 0 {
			i += n
			continue
		}
		out = append(out, Span{i, closer + n})
		i = closer + n
	}
	return out
}

// run is the length of the run of backticks starting at line[i].
func run(line string, i int) int {
	n := 0
	for i+n < len(line) && line[i+n] == '`' {
		n++
	}
	return n
}

// Link is one inline link or image, `[text](destination)`, as byte ranges of
// its line. Dest excludes the angle brackets of a `<…>` destination.
type Link struct {
	All, Text, Dest Span
	Image           bool
}

// openRE finds where a link's text could start; the rest is scanned by hand
// because a destination's grammar is not regular.
var openRE = regexp.MustCompile(`!?\[[^\]]*\]\(`)

// InlineLinks returns every inline link or image in line, in order.
//
// The destination follows CommonMark: either `<…>`, which may hold spaces, or
// a run with no whitespace in which parentheses must balance, so
// `count(*)` stays inside the destination rather than ending it. Every reader
// of directive links -- the extractor, render, the sentence binder -- uses
// this one function. They each had a regular expression that stopped at the
// first `)` or space, so a query holding `count(*)` was cut short and one
// holding a space was not a link at all, and the directive vanished without a
// word (bug 51).
func InlineLinks(line string) []Link {
	var out []Link
	for at := 0; at < len(line); {
		m := openRE.FindStringIndex(line[at:])
		if m == nil {
			break
		}
		start, open := at+m[0], at+m[1]
		textEnd := open - 2
		textStart := start + 1
		image := line[start] == '!'
		if image {
			textStart++
		}
		dest, end, ok := destination(line, open)
		if !ok {
			at = open
			continue
		}
		out = append(out, Link{All: Span{start, end}, Text: Span{textStart, textEnd}, Dest: dest, Image: image})
		at = end
	}
	return out
}

// destination reads a link destination starting at i (just after the `(`)
// and the closing `)`, allowing only whitespace between them. It returns the
// destination's span and the offset after the `)`.
func destination(line string, i int) (Span, int, bool) {
	var dest Span
	j := i
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	i = j
	if j < len(line) && line[j] == '<' {
		k := strings.IndexAny(line[j+1:], "<>")
		if k < 0 || line[j+1+k] != '>' {
			return Span{}, 0, false
		}
		dest = Span{j + 1, j + 1 + k}
		j = j + 1 + k + 1
	} else {
		depth := 0
	scan:
		for ; j < len(line); j++ {
			switch c := line[j]; {
			case c == ' ' || c == '\t':
				break scan
			case c == '(':
				depth++
			case c == ')':
				if depth == 0 {
					break scan
				}
				depth--
			}
		}
		if depth != 0 {
			return Span{}, 0, false
		}
		dest = Span{i, j}
	}
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	if j >= len(line) || line[j] != ')' {
		return Span{}, 0, false
	}
	return dest, j + 1, true
}

// Links returns every inline link or image in line, brackets included.
func Links(line string) []Span {
	var out []Span
	for _, l := range InlineLinks(line) {
		out = append(out, l.All)
	}
	return out
}
