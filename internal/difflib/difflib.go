// Package difflib computes line-level diffs and a similarity ratio between two
// texts, with no dependencies.
//
// Two consumers: findings attach a unified-style diff of a changed block so a
// reviewer or an agent can judge without opening the file (docs/SPEC.md §17),
// and the matcher uses Ratio to decide `rewritten?` when a def vanished and a
// similar unmarked block appeared (§16, threshold in config, default 0.8).
//
// The algorithm is a longest-common-subsequence over lines. Blocks are small
// (a function, a section), so O(n·m) memory is fine and simplicity wins over
// Myers. Ratio follows difflib's definition, 2·matches/(len(a)+len(b)), so the
// 0.8 default in the spec means what people expect from Python's difflib.
package difflib

import (
	"strings"
)

// Op is one edit operation in a diff.
type Op byte

const (
	// OpEqual is a line present in both texts.
	OpEqual Op = ' '
	// OpDelete is a line only in the old text.
	OpDelete Op = '-'
	// OpInsert is a line only in the new text.
	OpInsert Op = '+'
)

// Edit is one line of a diff with its operation.
type Edit struct {
	Op   Op
	Line string
}

// Lines splits text into lines without a trailing empty element for a final
// newline, so "a\nb\n" and "a\nb" diff as equal.
func Lines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Diff returns the edit script transforming a into b. Equal lines are kept so
// callers can render context; use Compact to drop them.
func Diff(a, b []string) []Edit {
	n, m := len(a), len(b)
	// lcs[i][j] = length of LCS of a[i:] and b[j:]
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var out []Edit
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, Edit{OpEqual, a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, Edit{OpDelete, a[i]})
			i++
		default:
			out = append(out, Edit{OpInsert, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, Edit{OpDelete, a[i]})
	}
	for ; j < m; j++ {
		out = append(out, Edit{OpInsert, b[j]})
	}
	return out
}

// Ratio is 2·M/T where M is the number of matching lines (the LCS length) and
// T the total number of lines in both inputs. Two empty inputs are identical
// (1.0); one empty input is entirely different (0.0).
func Ratio(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	matches := 0
	for _, e := range Diff(a, b) {
		if e.Op == OpEqual {
			matches++
		}
	}
	return 2 * float64(matches) / float64(len(a)+len(b))
}

// Compact drops equal lines except up to context lines around each change,
// which is what a finding shows. context of 0 keeps only changed lines.
func Compact(edits []Edit, context int) []Edit {
	if context < 0 {
		context = 0
	}
	keep := make([]bool, len(edits))
	for i, e := range edits {
		if e.Op == OpEqual {
			continue
		}
		lo, hi := i-context, i+context
		if lo < 0 {
			lo = 0
		}
		if hi >= len(edits) {
			hi = len(edits) - 1
		}
		for k := lo; k <= hi; k++ {
			keep[k] = true
		}
	}
	var out []Edit
	for i, e := range edits {
		if keep[i] {
			out = append(out, e)
		}
	}
	return out
}

// Format renders edits in the familiar `-`/`+`/space prefixed form, one per
// line, with a trailing newline after each line. An empty script renders "".
func Format(edits []Edit) string {
	var b strings.Builder
	for _, e := range edits {
		b.WriteByte(byte(e.Op))
		b.WriteString(e.Line)
		b.WriteByte('\n')
	}
	return b.String()
}

// Unified is the convenience most callers want: diff two texts, keep context
// lines around changes, render. Identical texts render "".
func Unified(oldText, newText string, context int) string {
	return Format(Compact(Diff(Lines(oldText), Lines(newText)), context))
}
