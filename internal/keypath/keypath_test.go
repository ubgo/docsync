package keypath

import (
	"slices"
	"testing"
)

func TestEnd(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		line string
		yaml bool
		want int
	}{
		{"port: 1", true, 4},
		{"wfsys:up:", true, 8},
		{"state:bootstrap:   # c", true, 15},
		{"a:\tb", true, 1},
		{"key:value", false, 3},
		{"key:value", true, -1},
		{"k = v", true, 2},
		{"nothing here", true, -1},
	} {
		if got := End(c.line, c.yaml); got != c.want {
			t.Errorf("End(%q, %v) = %d, want %d", c.line, c.yaml, got, c.want)
		}
	}
}

func TestSplitQuoteUnquote(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][]string{
		"tasks.wfsys:up":   {"tasks", "wfsys:up"},
		`tasks."wfsys:up"`: {"tasks", "wfsys:up"},
		`tasks."a.b".desc`: {"tasks", "a.b", "desc"},
		`tasks.'a.b'`:      {"tasks", "a.b"},
		"single":           {"single"},
	} {
		if got := Split(in); !slices.Equal(got, want) {
			t.Errorf("Split(%q) = %q, want %q", in, got, want)
		}
	}
	for k, want := range map[string]string{"port": "port", "wfsys:up": "wfsys:up", "a.b": `"a.b"`} {
		if got := Quote(k); got != want || Split(got)[0] != k {
			t.Errorf("Quote(%q) = %q", k, got)
		}
	}
	for in, want := range map[string]string{`"a.b"`: "a.b", `'x'`: "x", "plain": "plain", `"`: `"`, `"a'`: `"a'`} {
		if got := Unquote(in); got != want {
			t.Errorf("Unquote(%q) = %q, want %q", in, got, want)
		}
	}
}
