package extract

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
)

// TestCodeIsNeverACarrier pins every form markdown code takes, and each
// prose look-alike beside it. Before, only fenced blocks were masked: a
// citation in an inline code span was live, and so was each line of an
// indented example, so a page that showed how to cite reported a broken
// citation per example.
func TestCodeIsNeverACarrier(t *testing.T) {
	t.Parallel()
	const id = "sess-save-k7m2p4xq"
	link := "[x](ds:block?id=" + id + ")"
	cmt := "<!-- ds:block id=" + id + " -->"
	def := "[8081](ds:def?id=port-a2b6f8jk)"
	for _, tc := range []struct {
		name, file, src string
		refs, defs      int
	}{
		{"inline code", "d.md", "Use `" + link + "` to cite.\n", 0, 0},
		{"double backticks around a backtick", "d.md", "Use `` a ` " + link + " `` here.\n", 0, 0},
		{"code span at the end of the line", "d.md", "Write `" + cmt + "`\n", 0, 0},
		{"inline def in a code span", "d.md", "Like `" + def + "`.\n", 0, 0},
		{"real link after a code span", "d.md", "Not `" + link + "` but " + link + ".\n", 1, 0},
		{"unmatched backtick is literal", "d.md", "A ` then " + link + ".\n", 1, 0},
		{"runs of different lengths do not pair", "d.md", "A `` then " + link + " then `.\n", 1, 0},
		{"indented code after a blank line", "d.md", "Example:\n\n    " + cmt + "\n    " + link + "\n", 0, 0},
		{"indented code after a heading", "d.md", "# H\n    " + link + "\n", 0, 0},
		{"indented code at the top of the page", "d.md", "    " + link + "\n", 0, 0},
		{"tab-indented code", "d.md", "x\n\n\t" + link + "\n", 0, 0},
		{"blank lines inside indented code", "d.md", "x\n\n    a\n\n    " + link + "\n", 0, 0},
		{"indentation cannot interrupt a paragraph", "d.md", "A paragraph\n    " + link + " continues it.\n", 1, 0},
		{"three spaces is not code", "d.md", "x\n\n   " + link + "\n", 1, 0},
		{"continuation paragraph of a list item", "d.md", "- item\n\n    " + link + " is in the item.\n", 1, 0},
		{"ordered list continuation", "d.md", "1. step\n\n    " + link + " too.\n", 1, 0},
		{"lazy continuation keeps the list", "d.md", "- item\nlazy\n\n    " + link + " still the item.\n", 1, 0},
		{"a margin paragraph ends the list", "d.md", "- item\n\nAfter.\n\n    " + link + "\n", 0, 0},
		{"a list item's indented fence is a fence", "d.md", "1. run\n\n    ```\n    " + link + "\n    ```\n\nThen " + link + ".\n", 1, 0},
		{"a fence inside indented code is code", "d.md", "x\n\n    ```\n\n" + link + " is prose again.\n", 1, 0},
		{"code ends where the indentation does", "d.md", "x\n\n    " + link + "\nBack " + link + ".\n", 1, 0},
		{"html keeps backticks", "d.html", "<p>`" + link + "`</p>\n", 1, 0},
		{"html keeps indentation", "d.html", "<div>\n\n    " + link + "\n</div>\n", 1, 0},
	} {
		ex, err := Default().For(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		f := ex.Extract(tc.file, []byte(tc.src), "ds")
		if len(f.Refs) != tc.refs || len(f.Defs) != tc.defs {
			t.Errorf("%s: refs %d defs %d, want %d %d\n%s", tc.name, len(f.Refs), len(f.Defs), tc.refs, tc.defs, tc.src)
		}
	}
}

// TestRealLinkAfterCodeSpanKeepsItsSentence pins that masking keeps offsets:
// the live link after a code span binds the same sentence as the line with
// no code span at all would give it, measured on the original text.
func TestRealLinkAfterCodeSpanKeepsItsSentence(t *testing.T) {
	t.Parallel()
	ex, _ := Default().For("d.md")
	src := "Not `x` but [y](ds:block?id=sess-save-k7m2p4xq) holds.\n"
	f := ex.Extract("d.md", []byte(src), "ds")
	if len(f.Refs) != 1 || !strings.Contains(f.Refs[0].Reference.Sentence, "`x`") {
		t.Fatalf("the sentence must come from the original line: %+v", f.Refs)
	}
}

// TestCarrierLines pins the exported view: markdown is masked, anything else
// comes back as it was, and line count and each line's length never change.
func TestCarrierLines(t *testing.T) {
	t.Parallel()
	lines := []string{"a `b` c", "", "    code", "```", "x", "```"}
	got := CarrierLines("d.md", lines)
	want := []string{"a     c", "", "", "", "", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if other := CarrierLines("notes.txt", lines); &other[0] != &lines[0] {
		t.Error("a non-markdown page comes back unchanged")
	}
	// html is read by the markdown extractor too: fences are masked, while
	// backticks and indentation mean nothing there.
	html := CarrierLines("p.html", lines)
	for i, w := range []string{"a `b` c", "", "    code", "", "", ""} {
		if html[i] != w {
			t.Errorf("html line %d = %q, want %q", i, html[i], w)
		}
	}
}

// FuzzMaskCodeSpans states what masking must keep for every line: its
// length, every byte outside a span, and nothing but spaces where it
// changed anything; and masking twice changes nothing more.
func FuzzMaskCodeSpans(f *testing.F) {
	for _, s := range []string{"a `b` c", "`` x ` y ``", "` open", "```", "a``b`c`d``", "é `ü` ☕"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		m := maskCodeSpans(s)
		if len(m) != len(s) {
			t.Fatalf("length %d -> %d", len(s), len(m))
		}
		for i := range s {
			if m[i] != s[i] && m[i] != ' ' {
				t.Fatalf("byte %d became %q, not a space", i, m[i])
			}
		}
		if again := maskCodeSpans(m); again != m {
			t.Fatalf("not idempotent:\n%q\n%q", m, again)
		}
	})
}

// TestInlineDefValueInCode pins that a def whose link text is written in
// code keeps that text as its value: matching happens on the masked line,
// and reading the value from it recorded blanks.
func TestInlineDefValueInCode(t *testing.T) {
	t.Parallel()
	ex, _ := Default().For("d.md")
	f := ex.Extract("d.md", []byte("Port [`8081`](ds:def?id=port-a2b6f8jk).\n"), "ds")
	if len(f.Defs) != 1 || f.Defs[0].Block.Content != "`8081`" {
		t.Fatalf("defs = %+v", f.Defs)
	}
}

// TestIndentCols pins how indentation is measured: spaces count one column,
// a tab advances to the next multiple of four, and a line of nothing but
// whitespace is as wide as its whitespace.
func TestIndentCols(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int{"x": 0, "   x": 3, "\tx": 4, "  \tx": 4, "    \tx": 8, "\t\tx": 8, " \t x": 5, "     ": 5, "": 0} {
		if got := indentCols(in); got != want {
			t.Errorf("indentCols(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestQuotesInProseDoNotHideDirectives pins that a quote in prose is
// punctuation. The comment finder tracked string literals for every style,
// so the apostrophe in "It's" opened a string that never closed and the
// trailing directive after it was dropped with no problem reported.
func TestQuotesInProseDoNotHideDirectives(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ file, line string }{
		{"d.md", "It's fast. <!-- ds:claim note=x -->"},
		{"d.md", `Say "hi. <!-- ds:claim note=x -->`},
		{"d.md", "Use a ` here. <!-- ds:claim note=x -->"},
		{"d.mdx", "It's fast. {/* ds:claim note=x */}"},
		{"d.html", "<p>It's fast.</p> <!-- ds:claim note=x -->"},
		{"d.xml", "<p>It's</p> <!-- ds:claim note=x -->"},
	} {
		ex, err := Default().For(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		f := ex.Extract(tc.file, []byte(tc.line+"\n"), "ds")
		if len(f.Refs) != 1 {
			t.Errorf("%s %q: refs %d problems %v", tc.file, tc.line, len(f.Refs), f.Problems)
		}
	}
	// Code still has string literals: a comment marker inside one is text.
	cl := classify(1, `url := "http://x // ds:claim note=x"`, Styles[".go"])
	if cl.body != "" {
		t.Errorf("a // inside a Go string is not a comment: %+v", cl)
	}
}

// TestProseStylesHaveNoStrings guards the table: every prose or markup host
// sets NoStrings, so a style added for one later cannot bring the bug back.
func TestProseStylesHaveNoStrings(t *testing.T) {
	t.Parallel()
	for _, ext := range []string{".md", ".mdx", ".html", ".htm", ".xml", ".svg", ".adoc", ".asciidoc", ".rst"} {
		if !Styles[ext].NoStrings {
			t.Errorf("%s must set NoStrings", ext)
		}
	}
	for _, ext := range []string{".go", ".py", ".ts", ".vue", ".svelte", ".yaml"} {
		if Styles[ext].NoStrings {
			t.Errorf("%s has string literals and must track them", ext)
		}
	}
}

// TestParagraphSentence pins how a markdown citation's sentence is found
// (bug 13): across the lines of its paragraph, stopping where markdown stops a
// paragraph, with directive comments left out.
func TestParagraphSentence(t *testing.T) {
	t.Parallel()
	link := "[x](ds:block?id=a-a2b6f8jk)"
	sentenceOf := func(src string) string {
		t.Helper()
		ex, _ := Default().For("d.md")
		f := ex.Extract("d.md", []byte(src), "ds")
		for _, r := range f.Refs {
			if r.Reference.Carrier == block.CarrierLink {
				return r.Reference.Sentence
			}
		}
		t.Fatalf("no link citation in %q", src)
		return ""
	}
	for _, tc := range []struct{ name, src, want string }{
		{"wrapped", "Note that every write\ngoes through " + link + " first.\n", "Note that every write goes through " + link + " first."},
		{"wrapped, the citation first", "Every write goes through " + link + "\nfirst, always.\n", "Every write goes through " + link + " first, always."},
		{"re-spaced", "Every   write goes  through " + link + " first.\n", "Every write goes through " + link + " first."},
		{"previous sentence on the line above", "It is fast.\nThen " + link + " runs.\n", "Then " + link + " runs."},
		{"blank line ends it", "Unrelated words\n\nThen " + link + " runs.\n", "Then " + link + " runs."},
		{"heading ends it", "# Heading\nThen " + link + " runs.\n", "Then " + link + " runs."},
		{"fence ends it", "```\ncode\n```\nThen " + link + " runs.\n", "Then " + link + " runs."},
		{"thematic break ends it", "Words\n---\nThen " + link + " runs.\n", "Then " + link + " runs."},
		{"thematic break with spaces", "Words\n* * *\nThen " + link + " runs.\n", "Then " + link + " runs."},
		{"a wrapped list item binds whole", "- The item says\n  " + link + " matters.\n- next item\n", "The item says " + link + " matters."},
		{"a list item does not join the one above", "- first item\n- Then " + link + " runs.\n", "Then " + link + " runs."},
		{"a table row stands alone", "| a | b |\n|---|---|\n| cell | " + link + " here |\n", link + " here"},
		{"a blockquote joins its own lines", "> Quoted words\n> then " + link + " end.\nNot quoted.\n", "Quoted words then " + link + " end."},
		{"a blockquote does not join plain lines", "Plain words\n> Quoted " + link + " end.\n", "Quoted " + link + " end."},
		{"a trailing directive is not prose", "Every write goes through " + link + " first. <!-- ds:claim note=x -->\n", "Every write goes through " + link + " first."},
		{"a comment line ends it", "<!-- ds:block id=a-a2b6f8jk -->\nThen " + link + " runs.\n", "Then " + link + " runs."},
	} {
		if got := sentenceOf(tc.src); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
	// A claim binds the sentence its comment ends, across the paragraph.
	ex, _ := Default().For("d.md")
	f := ex.Extract("d.md", []byte("We chose\nPostgres. <!-- ds:claim note=x -->\n"), "ds")
	if len(f.Refs) != 1 || f.Refs[0].Reference.Sentence != "We chose Postgres." {
		t.Errorf("claim sentence = %+v", f.Refs)
	}
	if !thematicBreak("___") || thematicBreak("--") || thematicBreak("    ---") || thematicBreak("-x-") {
		t.Error("thematicBreak")
	}
}
