package mdspan

import (
	"reflect"
	"strings"
	"testing"
)

func TestCodeSpans(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][]Span{
		"a `b` c":        {{2, 5}},
		"`` x ` y ``":    {{0, 11}},
		"` open":         nil,
		"a``b`c`d``":     {{1, 10}},
		"`a` and `b`":    {{0, 3}, {8, 11}},
		"no ticks":       nil,
		"```":            nil,
		"x `` y ` z `` ": {{2, 13}},
	} {
		if got := CodeSpans(in); !reflect.DeepEqual(got, want) {
			t.Errorf("CodeSpans(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLinks(t *testing.T) {
	t.Parallel()
	in := "See [a. b](x) and ![img](y.png) but not [c] (d) or [e](f g)."
	want := []Span{{4, 13}, {18, 31}}
	if got := Links(in); !reflect.DeepEqual(got, want) {
		t.Errorf("Links = %v, want %v (%q %q)", got, want, in[4:13], in[18:31])
	}
}

// FuzzCodeSpans states what holds for every line: spans are in order, do
// not overlap, stay inside the line, and each starts and ends on a backtick
// run of one length.
func FuzzCodeSpans(f *testing.F) {
	for _, s := range []string{"a `b` c", "`` x ` y ``", "` open", "a``b`c`d``", "é `ü` ☕"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		prev := 0
		for _, sp := range CodeSpans(s) {
			if sp.Start < prev || sp.End <= sp.Start || sp.End > len(s) {
				t.Fatalf("bad span %v after %d in %q", sp, prev, s)
			}
			n := run(s, sp.Start)
			if !strings.HasSuffix(s[:sp.End], strings.Repeat("`", n)) || 2*n > sp.End-sp.Start {
				t.Fatalf("span %v of %q does not close its run of %d", sp, s, n)
			}
			prev = sp.End
		}
	})
}
