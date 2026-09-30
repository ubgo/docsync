package glob

import (
	"errors"
	"testing"
)

func TestMatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		// single segment
		{"*.go", "main.go", true},
		{"*.go", "dir/main.go", false},
		{"README.md", "README.md", true},
		{"README.md", "docs/README.md", false},
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},
		{"[abc].go", "b.go", true},
		{"[abc].go", "d.go", false},
		// literal multi-segment
		{"docs/index.md", "docs/index.md", true},
		{"docs/index.md", "docs/other.md", false},
		{"docs/*.md", "docs/a.md", true},
		{"docs/*.md", "docs/sub/a.md", false},
		// double star
		{"**", "anything", true},
		{"**", "a/b/c", true},
		{"**/*.go", "main.go", true},
		{"**/*.go", "a/b/main.go", true},
		{"**/*.go", "a/b/main.ts", false},
		{"internal/**", "internal/x.go", true},
		{"internal/**", "internal/a/b/c.go", true},
		{"internal/**", "internal", false},
		{"internal/**", "external/x.go", false},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/x/y/c", false},
		{"a/**/b", "a/b/c", false},
		{"**/testdata/**", "pkg/testdata/x.txt", true},
		{"**/testdata/**", "testdata/x.txt", true},
		{"**/testdata/**", "pkg/testdata", false},
		{"**/*_test.go", "a/b/c_test.go", true},
		{"**/*_test.go", "a/b/c.go", false},
		// double star inside a segment degrades to star
		{"a**b", "axxb", true},
		{"a**b", "a/b", false},
		// edge cases
		{"*", "", false},
		{"**", "", false},
		{"public/**", "public/", true},
	} {
		t.Run(tc.pattern+"~"+tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Match(tc.pattern, tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("Match(%q,%q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
			}
		})
	}
}

func TestCompileErrors(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "[", "a/[b", "**/[z-a"} {
		if _, err := Compile(bad); !errors.Is(err, ErrBadPattern) {
			t.Errorf("Compile(%q) err = %v, want ErrBadPattern", bad, err)
		}
		if _, err := Match(bad, "x"); !errors.Is(err, ErrBadPattern) {
			t.Errorf("Match(%q) err = %v, want ErrBadPattern", bad, err)
		}
	}
	// A bracket that is valid for path.Match must compile.
	if _, err := Compile("[a-z]/**"); err != nil {
		t.Errorf("valid class rejected: %v", err)
	}
}

func TestPatternZeroAndString(t *testing.T) {
	t.Parallel()
	var zero Pattern
	if zero.Match("anything") {
		t.Error("zero Pattern must match nothing")
	}
	p, _ := Compile("docs/**")
	if p.String() != "docs/**" {
		t.Errorf("String = %q", p.String())
	}
}

func TestSet(t *testing.T) {
	t.Parallel()
	s, err := CompileAll([]string{"docs/**/*.md", "README.md"})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{
		"README.md":       true,
		"docs/a.md":       true,
		"docs/x/y/z.md":   true,
		"docs/a.txt":      false,
		"other/README.md": false,
		"":                false,
	} {
		if got := s.MatchAny(name); got != want {
			t.Errorf("MatchAny(%q) = %v, want %v", name, got, want)
		}
	}
	var empty Set
	if empty.MatchAny("anything") {
		t.Error("empty Set must match nothing")
	}
	if _, err := CompileAll([]string{"ok/**", "["}); !errors.Is(err, ErrBadPattern) {
		t.Errorf("CompileAll err = %v", err)
	} else if err.Error() == "" {
		t.Error("error message empty")
	}
}
