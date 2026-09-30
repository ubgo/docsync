// Package linerange parses the `lines=` fragment a citation uses to show part of
// a block: `a-b` or `a`, 1-based and inclusive.
//
// It lives here because three readers need the same grammar: the checker
// judges a citation, the renderer slices it, and `ds read --lines` returns
// it. They had one copy each and drifted — the reader rejected `lines=3`,
// which the other two accepted, and took `2-3xyz`, which they refused — so
// a citation could render in a doc and fail when an assistant read it.
//
// The ledger's stored `lines` column is a different thing and has its own
// parser: it must accept `1-0`, the range of an empty file.
package linerange

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrBad is returned for any fragment outside the grammar.
var ErrBad = errors.New("bad lines")

// sep separates the first line from the last.
const sep = "-"

// Parse reads `a-b` or `a`. Both are decimal digits only — no sign, space,
// or leading `+` — and 1 <= a <= b. A single `a` is `a-a`.
func Parse(s string) (start, end int, err error) {
	a, b, ranged := strings.Cut(s, sep)
	start, ok := number(a)
	if !ok || start < 1 {
		return 0, 0, fmt.Errorf("%w %q: want a or a-b, counting from 1", ErrBad, s)
	}
	if !ranged {
		return start, start, nil
	}
	end, ok = number(b)
	if !ok || end < start {
		return 0, 0, fmt.Errorf("%w %q: want a-b with a <= b", ErrBad, s)
	}
	return start, end, nil
}

// number reads a non-empty run of ASCII digits that fits in an int.
// strconv.Atoi alone would also take a sign.
func number(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}
