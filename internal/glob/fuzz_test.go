package glob

import (
	"strings"
	"testing"
)

// FuzzMatch holds the package's documented semantics against any pattern and
// path: Match and Compile agree, a single-segment pattern never matches a
// deeper path, a literal matches exactly itself, and `**/x` finds x at any
// depth. Configs decide what gets scanned, so a glob that quietly matched
// more or less than it says would scan the wrong files without an error.
func FuzzMatch(f *testing.F) {
	for _, s := range [][2]string{{"*.go", "a.go"}, {"**/*.go", "a/b/c.go"}, {"a/**/b", "a/b"}, {"a/**", "a"}, {"[a-", "a"}, {"docs/**", "docs/x.md"}, {"a**b", "axxb"}, {"?", "/"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, pattern, name string) {
		if strings.HasPrefix(name, "/") {
			return // paths are relative by contract
		}
		got, err := Match(pattern, name)
		p, cerr := Compile(pattern)
		if (err == nil) != (cerr == nil) {
			t.Fatalf("Match and Compile disagree on validity of %q: %v vs %v", pattern, err, cerr)
		}
		if err != nil {
			return
		}
		if p.Match(name) != got {
			t.Fatalf("Match(%q, %q) = %v but the compiled pattern says %v", pattern, name, got, !got)
		}
		// The one exception is a bare `**`, which means everything: it is
		// the default `scan.code`.
		if got && !strings.Contains(pattern, "/") && pattern != doubleStar && strings.Contains(name, "/") {
			t.Fatalf("%q has no slash but matched the deeper path %q", pattern, name)
		}
		if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, `*?[]\`) || strings.Contains(name, "//") || strings.HasSuffix(name, "/") {
			return // below, name is also used as a literal pattern
		}
		if ok, _ := Match(name, name); !ok {
			t.Fatalf("the literal %q does not match itself", name)
		}
		if strings.Contains(name, "/") || name == doubleStar {
			return
		}
		for _, path := range []string{name, "x/" + name, "x/y/" + name} {
			if ok, _ := Match("**/"+name, path); !ok {
				t.Fatalf("**/%s does not match %q", name, path)
			}
		}
	})
}
