package duration

import (
	"strconv"
	"testing"
	"time"
)

// FuzzParse: any string either fails or parses to exactly n units — never
// to a value that overflowed. `review_every=100000000w` read as a wrapped,
// possibly negative, duration would make a page permanently due or never
// due, with no error to say why.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"90d", "2w", "12h", "30m", "0d", "", "d", "3M", "-1d", "+5d", "999999999999999999w", "106751d", "106752d"} {
		f.Add(s)
	}
	unit := map[byte]time.Duration{'d': 24 * time.Hour, 'w': 7 * 24 * time.Hour, 'h': time.Hour, 'm': time.Minute}
	f.Fuzz(func(t *testing.T, s string) {
		got, err := Parse(s)
		if err != nil {
			return
		}
		if got < 0 {
			t.Fatalf("Parse(%q) = %v, negative", s, got)
		}
		n, _ := strconv.Atoi(s[:len(s)-1])
		if got/unit[s[len(s)-1]] != time.Duration(n) || got%unit[s[len(s)-1]] != 0 {
			t.Fatalf("Parse(%q) = %v, which is not %d units: it overflowed", s, got, n)
		}
	})
}
