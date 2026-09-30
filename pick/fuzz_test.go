package pick

import (
	"strings"
	"testing"
)

// FuzzPick: no expression or content panics a pick, and a range it returns
// lies inside the content — a range outside it is a slice out of bounds in
// whatever renders the block next.
func FuzzPick(f *testing.F) {
	for _, s := range [][2]string{{"line:2", "a\nb\nc"}, {"lines:2-9", "a\nb"}, {"regex:(\\d+)", "port 8081"}, {"heading:Setup", "# T\n## Setup\nx\n"}, {"key:a.b", "a:\n  b: 1\n"}, {"file", ""}, {"", "x"}, {"lines:0-0", "x"}, {"json:$.a", "{\"a\":1}"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, expr, content string) {
		r, err := Pick(expr, content)
		if err != nil || r.Kind != KindRange {
			return
		}
		n := len(strings.Split(content, "\n"))
		if r.Start < 1 || r.End > n || r.End < r.Start-1 {
			t.Fatalf("Pick(%q) range %d-%d, content has %d lines", expr, r.Start, r.End, n)
		}
	})
}

// FuzzPickLineEndings states that a pick does not depend on how the file's
// lines end: for content with LF endings, the same content with CRLF endings
// picks the same thing or fails the same way. A Windows checkout with
// core.autocrlf is the same repository, not a different one.
func FuzzPickLineEndings(f *testing.F) {
	for _, s := range [][2]string{
		{"line:2", "a\nb\n"},
		{"regex:^port: (\\d+)$", "x\nport: 80\n"},
		{"env:KEY", "A=1\nKEY=2\n"},
		{"yaml:a.b", "a:\n  b: 5\n"},
		{"ini:s.k", "[s]\nk = v\n"},
		{"csv:1,1", "h,i\nj,k\n"},
		{"json:a", "{\n\"a\": 1\n}\n"},
		{"after:start", "start\nx\ny\n"},
		{"between:<a>,</a>", "<a>\nx\n</a>\n"},
		{"heading", "# T\n\nbody\n"},
		{"section:Setup", "# T\n## Setup\nx\n## Next\n"},
		{"paragraph:1", "one\ntwo\n\nthree\n"},
		{"link:docs", "see [docs](https://x.y/docs)\n"},
		{"url", "go to https://x.y/a\n"},
		{"toml:a", "a = 3\n"},
		{"file", "a\nb\n"},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, expr, lf string) {
		if strings.Contains(lf, "\r") {
			return
		}
		a, errA := Pick(expr, lf)
		b, errB := Pick(expr, strings.ReplaceAll(lf, "\n", "\r\n"))
		if (errA == nil) != (errB == nil) || a != b {
			t.Fatalf("Pick(%q) differs by line ending:\nlf   %+v %v\ncrlf %+v %v", expr, a, errA, b, errB)
		}
	})
}
