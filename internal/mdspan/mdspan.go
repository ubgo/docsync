// Package mdspan finds the inline spans of a markdown line that are not
// running prose: code spans and links.
//
// It lives here because two readers need the same answer and neither may
// import the other: the extractor masks code spans so an example in code is
// not a directive, and the sentence binder must not end a sentence at a
// full stop inside a code span or a link's text. A second copy of the
// backtick rule is how the two would drift.
package mdspan

import "regexp"

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

// linkRE matches an inline link or image, `[text](target)`, with no
// whitespace in the target. It is the shape every directive link has.
var linkRE = regexp.MustCompile(`!?\[[^\]]*\]\([^)\s]*\)`)

// Links returns every inline link or image in line, brackets included.
func Links(line string) []Span {
	var out []Span
	for _, m := range linkRE.FindAllStringIndex(line, -1) {
		out = append(out, Span{m[0], m[1]})
	}
	return out
}
