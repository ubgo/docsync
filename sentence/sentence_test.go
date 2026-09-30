package sentence

import (
	"reflect"
	"strings"
	"testing"
)

func TestBind(t *testing.T) {
	t.Parallel()
	line := "Every write goes through SaveSession. It writes the legacy row first, then the table! Really? Yes."
	for _, tc := range []struct {
		name string
		line string
		col  int
		want string
	}{
		{"first sentence", line, 10, "Every write goes through SaveSession."},
		{"second sentence", line, strings.Index(line, "legacy"), "It writes the legacy row first, then the table!"},
		{"third sentence", line, strings.Index(line, "Really"), "Really?"},
		{"last sentence", line, len(line) - 1, "Yes."},
		{"col on the terminator itself", line, strings.Index(line, "."), "Every write goes through SaveSession."},
		{"col at end", line, len(line), "Yes."},
		{"col negative falls back to whole-line start", line, -1, "Every write goes through SaveSession."},
		{"col beyond end falls back", line, 999, "Every write goes through SaveSession."},
		{"decimal does not split", "Version 1.5 ships on port 8081.", 20, "Version 1.5 ships on port 8081."},
		{"a listed abbreviation does not split", "See e.g. the guard here.", 15, "See e.g. the guard here."},
		{"closing quote stays with terminator", `He said "done." Then left.`, 3, `He said "done."`},
		{"closing paren stays", "See the guard (below.) Then more.", 3, "See the guard (below.)"},
		{"no terminator", "a sentence without an end", 5, "a sentence without an end"},
		{"empty line", "", 0, ""},
		{"bullet item whole", "- Refresh already happens daily. Rotation is cheap.", 40, "Refresh already happens daily. Rotation is cheap."},
		{"star item", "  * item text. more", 8, "item text. more"},
		{"plus item", "+ x", 2, "x"},
		{"ordered dot", "3. Delete the old token. Then retry.", 30, "Delete the old token. Then retry."},
		{"ordered paren", "12) Item. Two.", 12, "Item. Two."},
		{"checkbox stripped", "- [x] add flag. done.", 10, "add flag. done."},
		{"checkbox unchecked", "- [ ] todo", 8, "todo"},
		{"not a list: dash mid line", "a - b. C.", 7, "C."},
		{"not a list: number without space", "3.Delete x. Y.", 12, "Y."},
		{"table cell first", "| id | uuid | primary key. yes |", 3, "id"},
		{"table cell third", "| id | uuid | primary key. yes |", 20, "primary key. yes"},
		{"table row without leading pipe", "a | b. c | d", 6, "b. c"},
		{"table col on closing pipe belongs to that cell", "| a | b |", 9, "b"},
		{"separator row is not a cell", "|---|---|", 3, "|---|---|"},
		{"pipe without spaces is prose", "a|b. C.", 6, "C."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Bind(tc.line, tc.col); got != tc.want {
				t.Errorf("Bind(%q,%d) = %q, want %q", tc.line, tc.col, got, tc.want)
			}
		})
	}
}

func TestSplit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"One. Two! Three?", []string{"One.", "Two!", "Three?"}},
		{"No terminator", []string{"No terminator"}},
		{"Line one.\nLine two", []string{"Line one.", "Line two"}},
		{"Ends with 1.5 and more.", []string{"Ends with 1.5 and more."}},
		{"   Spaced.   Out.  ", []string{"Spaced.", "Out."}},
		{"...", []string{"..."}},
		{".", []string{"."}},
	} {
		if got := Split(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Split(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSameSentenceSharesBinding(t *testing.T) {
	t.Parallel()
	line := "Listens on 8081 and stores 30 days of sessions. Next."
	a := Bind(line, strings.Index(line, "8081"))
	b := Bind(line, strings.Index(line, "30 days"))
	if a != b || a != "Listens on 8081 and stores 30 days of sessions." {
		t.Errorf("two refs in one sentence must bind identically: %q vs %q", a, b)
	}
}

func TestSentenceBoundsEdge(t *testing.T) {
	t.Parallel()
	// A column on trailing whitespace after the last terminator binds to an
	// empty span, which Bind's clamp avoids for the end-of-line case.
	s, e := sentenceBounds("x.   ", 4)
	if s != e {
		t.Errorf("bounds on trailing whitespace = %d,%d", s, e)
	}
	if got := Bind("x.   ", 5); got != "x." {
		t.Errorf("Bind past trailing whitespace = %q, want the last sentence", got)
	}
	if got := Bind("Design choice. ", 15); got != "Design choice." {
		t.Errorf("Bind at end after trailing space = %q", got)
	}
	// A terminator followed by a non-space closer-less char is not an end.
	if got := Bind("v1.2rc. Ok", 0); got != "v1.2rc." {
		t.Errorf("Bind = %q", got)
	}
}

// TestNoSentenceEndsInsideALinkOrCode pins that a terminator inside a
// link's text or an inline code span is not a sentence boundary. Before,
// `[e.g. Save](…)` bound "Writes go through [e.g." — the sentence was cut
// inside the citation and no longer contained it, so the ack was keyed to
// a fragment.
func TestNoSentenceEndsInsideALinkOrCode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ line, at, want string }{
		{"Writes go through [e.g. Save](ds:block?id=a) first. Then more.", "Save", "Writes go through [e.g. Save](ds:block?id=a) first."},
		{"Is it [really? yes](ds:block?id=a) so. Next.", "yes", "Is it [really? yes](ds:block?id=a) so."},
		{"Run `make. all` then [x](ds:block?id=a) done. Next.", "[x]", "Run `make. all` then [x](ds:block?id=a) done."},
		{"First. [a](ds:block?id=a). Second [b](ds:block?id=b) here.", "[b]", "Second [b](ds:block?id=b) here."},
		{"First. [a](ds:block?id=a). Second [b](ds:block?id=b) here.", "[a]", "[a](ds:block?id=a)."},
		{"An ` unmatched. Tick [x](ds:block?id=a) here.", "[x]", "Tick [x](ds:block?id=a) here."},
	} {
		if got := Bind(tc.line, strings.Index(tc.line, tc.at)); got != tc.want {
			t.Errorf("Bind(%q at %q) = %q, want %q", tc.line, tc.at, got, tc.want)
		}
	}
}

// TestCheckboxNeedsWhitespace pins the GFM task marker: `[x]` is a checkbox
// only when whitespace follows it or the item ends there.
func TestCheckboxNeedsWhitespace(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]string{
		"- [x](ds:cfg?id=a) is the port": "[x](ds:cfg?id=a) is the port",
		"* [X]() empty":                  "[X]() empty",
		"- [x] done":                     "done",
		"- [ ]\tlater":                   "later",
		"- [x]":                          "",
	} {
		if got := Bind(line, 3); got != want {
			t.Errorf("Bind(%q) = %q, want %q", line, got, want)
		}
	}
}

// TestSentenceEndsRules pins bug 13's boundary rules: what may start the
// next sentence, the fixed abbreviation list, ellipses, and whitespace. A
// sentence split in the middle bound a fragment, so an edit to the other
// half changed nothing an ack could see.
// promise:sentence-split
func TestSentenceEndsRules(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ line, at, want string }{
		// What follows decides.
		{"It is fast. Then [x](d) runs.", "[x]", "Then [x](d) runs."},
		{"Version 3. 42 [x](d) runs.", "[x]", "42 [x](d) runs."},
		{`He said so. "Yes" [x](d) says.`, "[x]", `"Yes" [x](d) says.`},
		{"Done. (Then) [x](d) runs.", "[x]", "(Then) [x](d) runs."},
		{"Run it. `ds check` [x](d) works.", "[x]", "`ds check` [x](d) works."},
		{"It is fast. then [x](d) runs.", "[x]", "It is fast. then [x](d) runs."},
		{"Use v1.2 and [x](d) now.", "[x]", "Use v1.2 and [x](d) now."},
		{"See Fig. 3 for [x](d) here.", "[x]", "See Fig. 3 for [x](d) here."},
		{"Is it? Yes, [x](d) is.", "[x]", "Yes, [x](d) is."},
		{"Émile said. Élan [x](d) wins.", "[x]", "Élan [x](d) wins."},
		// The listed abbreviations never end a sentence, in any case.
		{"Writes go through e.g. [x](d) first.", "[x]", "Writes go through e.g. [x](d) first."},
		{"Read the docs, i.e. [x](d) first.", "[x]", "Read the docs, i.e. [x](d) first."},
		{"Tools (e.g. Linters) run [x](d) here.", "[x]", "Tools (e.g. Linters) run [x](d) here."},
		{"Ask Dr. Smith about [x](d) now.", "[x]", "Ask Dr. Smith about [x](d) now."},
		{"The U.S. Team wrote [x](d) here.", "[x]", "The U.S. Team wrote [x](d) here."},
		{"See No. 4 and [x](d) too.", "[x]", "See No. 4 and [x](d) too."},
		// An unlisted abbreviation before a capital still splits, as before.
		{"Ask Prof. Smith about [x](d) now.", "[x]", "Smith about [x](d) now."},
		// A run of three or more dots is not an end.
		{"It waits... Then [x](d) runs.", "[x]", "It waits... Then [x](d) runs."},
		// Whitespace is collapsed: re-spacing is not rewriting.
		{"Writes   go\tthrough  [x](d)  first.", "[x]", "Writes go through [x](d) first."},
	} {
		if got := Bind(tc.line, strings.Index(tc.line, tc.at)); got != tc.want {
			t.Errorf("Bind(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

// TestListItemsAndCellsCollapse pins that re-spacing a list item or a table
// cell is not rewriting it, as for a sentence: before, the two returned
// their text as written, so one extra space inside an acked item reported
// it as rewritten.
func TestListItemsAndCellsCollapse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ a, b string }{
		{"- Retries  three\ttimes.", "- Retries three times."},
		{"1.  Retries   three times.", "1. Retries three times."},
		{"| Retries  three times | x |", "| Retries three times | x |"},
	} {
		if got, want := Bind(tc.a, 4), Bind(tc.b, 4); got != want {
			t.Errorf("Bind(%q) = %q, Bind(%q) = %q; re-spacing must not change the binding", tc.a, got, tc.b, want)
		}
	}
}
