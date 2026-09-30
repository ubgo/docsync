package textnorm

import (
	"testing"
)

func TestNormalize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"plain", "a\nb", "a\nb"},
		{"crlf", "a\r\nb\r\n", "a\nb"},
		{"lone cr", "a\rb", "a\nb"},
		{"bom", "\xEF\xBB\xBFa", "a"},
		{"bom only", "\xEF\xBB\xBF", ""},
		{"trailing spaces and tabs", "a  \t\nb\t \n", "a\nb"},
		{"form feed and vtab trimmed", "a\f\v\n", "a"},
		{"interior whitespace kept", "a  b\n\tc", "a  b\n\tc"},
		{"leading indentation kept", "  a\n    b", "  a\n    b"},
		{"trailing blank lines dropped", "a\n\n\n", "a"},
		{"interior blank lines kept", "a\n\nb", "a\n\nb"},
		{"whitespace only", " \n\t\n", ""},
		{"final newline added or not is same", "a\n", "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeString(tc.in); got != tc.want {
				t.Errorf("NormalizeString(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if got := string(Normalize([]byte(tc.in))); got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "a\r\n b \r\n\r\n", "\xEF\xBB\xBFx\ty  \n"} {
		once := Normalize([]byte(in))
		twice := Normalize(once)
		if string(once) != string(twice) {
			t.Errorf("not idempotent for %q: %q vs %q", in, once, twice)
		}
	}
}

func TestHash(t *testing.T) {
	t.Parallel()
	// Equivalent inputs hash equal; meaningfully different inputs differ.
	a := Hash([]byte("func f() {\n\treturn 1\n}\n"))
	b := Hash([]byte("\xEF\xBB\xBFfunc f() {\r\n\treturn 1  \r\n}\r\n\r\n"))
	c := Hash([]byte("func f() {\n\treturn 2\n}\n"))
	if a != b {
		t.Errorf("normalization-equivalent inputs hashed differently")
	}
	if a == c {
		t.Errorf("different bodies hashed equal")
	}
	if len(a) != 64 {
		t.Errorf("hash length = %d, want 64 hex chars", len(a))
	}
	if HashLines([]string{"func f() {", "\treturn 1", "}"}) != a {
		t.Errorf("HashLines disagrees with Hash")
	}
	if got := Hash(nil); len(got) != 64 {
		t.Errorf("Hash(nil) length = %d", len(got))
	}
}

func TestShort(t *testing.T) {
	t.Parallel()
	if got := Short("9f3a1c77aa"); got != "9f3a1c" {
		t.Errorf("Short = %q", got)
	}
	if got := Short("9f3a"); got != "9f3a" {
		t.Errorf("Short of short input = %q", got)
	}
	if got := Short(""); got != "" {
		t.Errorf("Short of empty = %q", got)
	}
}
