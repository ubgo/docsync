package linerange

import (
	"errors"
	"strconv"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][2]int{
		"1": {1, 1}, "4": {4, 4}, "2-4": {2, 4}, "3-3": {3, 3}, "007-9": {7, 9},
	} {
		a, b, err := Parse(in)
		if err != nil || a != want[0] || b != want[1] {
			t.Errorf("Parse(%q) = %d %d %v, want %v", in, a, b, err, want)
		}
	}
	for _, in := range []string{
		"", "0", "0-2", "3-1", "-", "-3", "3-", "+3", "3-+4", " 3", "3 ", "2-3xyz",
		"1-2-3", "a", "1.5", "99999999999999999999", "1-99999999999999999999", "٣",
	} {
		if _, _, err := Parse(in); !errors.Is(err, ErrBad) {
			t.Errorf("Parse(%q) = %v, want ErrBad", in, err)
		}
	}
}

// FuzzParse states what holds for every input: an accepted fragment is a
// forward range from 1, and writing it back out reads to the same range.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"1", "2-4", "3-1", "0", "+3", "2-3xyz", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		a, b, err := Parse(s)
		if err != nil {
			if !errors.Is(err, ErrBad) || a != 0 || b != 0 {
				t.Fatalf("Parse(%q) failed badly: %d %d %v", s, a, b, err)
			}
			return
		}
		if a < 1 || b < a {
			t.Fatalf("Parse(%q) = %d-%d is not a forward range from 1", s, a, b)
		}
		back := strconv.Itoa(a) + "-" + strconv.Itoa(b)
		a2, b2, err := Parse(back)
		if err != nil || a2 != a || b2 != b {
			t.Fatalf("round trip %q -> %q -> %d-%d %v", s, back, a2, b2, err)
		}
	})
}
