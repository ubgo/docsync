package id

import (
	"regexp"
	"strings"
	"testing"
)

// slugShape is what Slug promises: lowercase words of [a-z0-9] joined by
// single dashes, no dash at either end.
var slugShape = regexp.MustCompile(`^([a-z0-9]+(-[a-z0-9]+)*)?$`)

// FuzzIdentity holds the id rules over any label: Slug is idempotent and
// well-formed, a minted id is valid, splits back to its slugged prefix, and
// stays the same block under a relabel. Identity is the suffix; if a label
// could leak into it, renaming would silently orphan every citation.
func FuzzIdentity(f *testing.F) {
	for _, s := range []string{"Store.SaveSession", "Session policy", "", "---", "A_b-C", "ümlaut", "9lives", "x--y"} {
		f.Add(s)
	}
	c := Default()
	f.Fuzz(func(t *testing.T, label string) {
		s := Slug(label)
		if Slug(s) != s {
			t.Fatalf("Slug is not idempotent: %q -> %q -> %q", label, s, Slug(s))
		}
		if !slugShape.MatchString(s) {
			t.Fatalf("Slug(%q) = %q is not a slug", label, s)
		}
		full, err := New(c, label)
		if err != nil {
			// Only a label with nothing to slug may fail, and it must fail
			// rather than mint an id with an empty prefix.
			if s != "" {
				t.Fatalf("New(%q) failed although it slugs to %q: %v", label, s, err)
			}
			return
		}
		if s == "" {
			t.Fatalf("New(%q) minted %q from a label with nothing to slug", label, full)
		}
		if !Valid(c, full) {
			t.Fatalf("New(%q) = %q is not valid", label, full)
		}
		prefix, suffix, err := Split(c, full)
		if err != nil || prefix != s {
			t.Fatalf("Split(%q) = %q %q %v, want prefix %q", full, prefix, suffix, err, s)
		}
		if !Same(c, full, "relabelled-"+suffix) {
			t.Fatalf("a relabel of %q is not the same block", full)
		}
		if Same(c, full, prefix+"-"+strings.Repeat(string(suffix[0]), len(suffix))) && strings.Repeat(string(suffix[0]), len(suffix)) != suffix {
			t.Fatalf("a different suffix is the same block as %q", full)
		}
	})
}
