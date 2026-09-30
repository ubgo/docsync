package textnorm

import (
	"bytes"
	"strings"
	"testing"
)

// FuzzNormalize states the package's promises as properties over any input:
// normalizing is idempotent, and none of the changes the package exists to
// ignore — a Windows checkout, an editor stripping trailing spaces, a final
// newline gained or lost — changes the hash. A hash that moved under one of
// these would flag every citing document for a change nobody made.
func FuzzNormalize(f *testing.F) {
	for _, s := range []string{"", "a", "a\nb", "a  \nb\t\n\n", "\xef\xbb\xbfa", "\xef\xbb\xbf\xef\xbb\xbfa", "a\r\nb", "a\rb", "\n\na", "  ", "x \n"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n := Normalize([]byte(s))
		if again := Normalize(n); !bytes.Equal(again, n) {
			t.Fatalf("not idempotent: %q -> %q -> %q", s, n, again)
		}
		h := Hash([]byte(s))
		if strings.ContainsRune(s, '\r') {
			return // the conversions below are defined on LF text only
		}
		if got := Hash([]byte(strings.ReplaceAll(s, "\n", "\r\n"))); got != h {
			t.Fatalf("CRLF changed the hash of %q", s)
		}
		if got := Hash([]byte(s + "\n\n")); got != h {
			t.Fatalf("a trailing newline changed the hash of %q", s)
		}
		lines := strings.Split(s, "\n")
		for i := range lines {
			lines[i] += " \t"
		}
		if got := Hash([]byte(strings.Join(lines, "\n"))); got != h {
			t.Fatalf("trailing whitespace changed the hash of %q", s)
		}
		if got := Hash(append([]byte("\xef\xbb\xbf"), s...)); got != h {
			t.Fatalf("a byte order mark changed the hash of %q", s)
		}
	})
}
