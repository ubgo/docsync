package sentence

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/internal/mdspan"
)

// FuzzBind: no line or column panics the sentence binder, the sentence it
// returns comes from the line it was given, and Split never loses text.
func FuzzBind(f *testing.F) {
	for _, s := range []string{"One. Two [x](ds:block?id=a). Three.", "", "e.g. this.", "…", "a.b.c"} {
		f.Add(s, 5)
	}
	f.Fuzz(func(t *testing.T, line string, col int) {
		// Bind collapses whitespace, so the line is compared collapsed too;
		// and a result is already collapsed, so collapsing it again is a
		// no-op.
		got := Bind(line, col)
		if got != "" && !strings.Contains(collapse(line), got) {
			t.Fatalf("Bind(%q, %d) = %q, which is not in the line", line, col, got)
		}
		if collapse(got) != got {
			t.Fatalf("Bind(%q, %d) = %q, which is not collapsed", line, col, got)
		}
		for _, s := range Split(line) {
			if !strings.Contains(line, strings.TrimSpace(s)) {
				t.Fatalf("Split(%q) produced %q, which is not in the text", line, s)
			}
		}
	})
}

// FuzzBindKeepsLinksWhole states that the sentence bound at any column
// inside a link contains that whole link: a citation is never split from
// itself, whatever punctuation its text holds.
func FuzzBindKeepsLinksWhole(f *testing.F) {
	for _, s := range []string{"Writes go through [e.g. Save](ds:block?id=a) first.", "A. [b! c? d.](x) e.", "`a. b` [c. d](e) f."} {
		f.Add(s, 0)
	}
	f.Fuzz(func(t *testing.T, line string, pick int) {
		if strings.ContainsAny(line, "\n|") || strings.HasPrefix(strings.TrimLeft(line, " \t"), "-") {
			return // table cells and list items bind whole, by another rule
		}
		links := mdspan.Links(line)
		if len(links) == 0 {
			return
		}
		sp := links[((pick%len(links))+len(links))%len(links)]
		link := line[sp.Start:sp.End]
		// Bind collapses whitespace, inside a link as everywhere else.
		if got := Bind(line, sp.Start); !strings.Contains(got, collapse(link)) {
			t.Fatalf("Bind(%q, %d) = %q, which does not hold the link %q", line, sp.Start, got, link)
		}
	})
}
