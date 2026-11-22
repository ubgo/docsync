package extract

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
)

// refSentences is the sentence of each reference in a markdown source, in
// the order they were read, with the verb in front.
func refSentences(src string) []string {
	var out []string
	f := Markdown{}.Extract("docs/p.md", []byte(src), "ds")
	for _, r := range f.Refs {
		out = append(out, r.Reference.Verb+": "+r.Reference.Sentence)
	}
	return out
}

// TestACommentBindsTheSentenceItSharesALineWith pins what a directive
// comment that shares its line with prose binds (bugs 160, 161 and 162),
// and that the commonest layouts bind as they always did.
func TestACommentBindsTheSentenceItSharesALineWith(t *testing.T) {
	t.Parallel()
	const claim = "<!-- ds:claim reviewed=2026-09-01 expires=30d -->"
	for _, tc := range []struct {
		name, src string
		want      []string
	}{
		{
			// Bug 160: the column just past "holds." is where the next
			// sentence starts, so a paragraph that went on bound it.
			name: "after a sentence, the paragraph going on below",
			src:  "S0 holds. " + claim + "\nAnother line.\n",
			want: []string{"claim: S0 holds."},
		},
		{
			name: "after a sentence that ends the paragraph",
			src:  "S0 holds. " + claim + "\n\nNext.\n",
			want: []string{"claim: S0 holds."},
		},
		{
			name: "after the first of two sentences on a line",
			src:  "S0 holds. " + claim + " S1 holds.\n",
			want: []string{"claim: S0 holds."},
		},
		{
			name: "after a sentence wrapped across lines",
			src:  "The cache is\nwarmed nightly. " + claim + "\nMore.\n",
			want: []string{"claim: The cache is warmed nightly."},
		},
		{
			// Bug 161: a comment that starts the line was read as a carrier
			// on a line of its own, and bound no sentence.
			name: "at the start of a line, before a sentence",
			src:  "S0 holds.\n" + claim + " S1 holds. S2 holds.\n",
			want: []string{"claim: S1 holds."},
		},
		{
			name: "a ds:block comment before a sentence cites it",
			src:  "<!-- ds:block id=a-k7m2p4xq --> Saves are atomic.\n",
			want: []string{"block: Saves are atomic."},
		},
		{
			name: "a comment on a line of its own binds no sentence, as before",
			src:  claim + "\nS0 holds.\n",
			want: []string{"claim: "},
		},
		{
			// Bug 162: the column of a link after a comment that starts its
			// line was counted from where the line's text starts once the
			// comment is removed, so it landed the comment's length too far.
			name: "a link after a comment at the start of the line",
			src:  "<!-- a note that is long enough to push the column past the sentence --> Intro. See [x](ds:block?id=a-k7m2p4xq) here. And more.\n",
			want: []string{"block: See [x](ds:block?id=a-k7m2p4xq) here."},
		},
		{
			name: "a link in a quote",
			src:  ">  Quote. See [x](ds:block?id=a-k7m2p4xq) here.\n",
			want: []string{"block: See [x](ds:block?id=a-k7m2p4xq) here."},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := refSentences(tc.src); strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("sentences = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestACommentBeforeTextLeavesTheLineInTheHash pins the hash half of bug
// 161: a line starting with a directive comment and going on with prose was
// a standalone carrier, so the prose was left out of the hash of every block
// spanning it, and editing it flagged nothing.
func TestACommentBeforeTextLeavesTheLineInTheHash(t *testing.T) {
	t.Parallel()
	page := func(text string) string {
		return "<!-- ds:def id=sec-k7m2p4xq -->\n## Policy\n\nIntro.\n<!-- ds:claim reviewed=2026-09-01 expires=30d --> " + text + "\n"
	}
	hash := func(src string) string {
		d := defsByID(Markdown{}.Extract("docs/p.md", []byte(src), "ds"))["sec-k7m2p4xq"]
		return d.Block.Hash
	}
	if hash(page("Sessions last a day.")) == hash(page("Sessions last a week.")) {
		t.Error("editing the prose after a leading comment left the section's hash unchanged")
	}
	// A carrier on a line of its own is still metadata, never text.
	alone := func(args string) string {
		return "<!-- ds:def id=sec-k7m2p4xq -->\n## Policy\n\nIntro.\n<!-- ds:claim " + args + " -->\nMore.\n"
	}
	if hash(alone("reviewed=2026-09-01 expires=30d")) != hash(alone("reviewed=2026-10-01 expires=60d")) {
		t.Error("editing a standalone carrier changed the section's hash")
	}
}

// TestADefBeforeTextBindsTheBlockTheTextBegins pins the def half of bug
// 161: a def comment followed by prose bound the next line, leaving out the
// first line of its own paragraph. It now binds the paragraph from its own
// line, and the directive is not part of the block, so editing the def's
// keys does not change its hash.
func TestADefBeforeTextBindsTheBlockTheTextBegins(t *testing.T) {
	t.Parallel()
	src := func(keys string) string {
		return "# T\n\n<!-- ds:def id=motto-k7m2p4xq" + keys + " --> Ship weekly.\nAlways.\n\nAfter.\n"
	}
	d := defsByID(Markdown{}.Extract("docs/p.md", []byte(src("")), "ds"))["motto-k7m2p4xq"]
	if d.Block.Kind != block.KindParagraph || d.Block.Pos.Start != 3 || d.Block.Pos.End != 4 || d.Block.Content != "Ship weekly.\nAlways." {
		t.Errorf("def = %s %d-%d %q; want the paragraph on lines 3-4 without the directive", d.Block.Kind, d.Block.Pos.Start, d.Block.Pos.End, d.Block.Content)
	}
	other := defsByID(Markdown{}.Extract("docs/p.md", []byte(src(" owner=@x stability=api")), "ds"))["motto-k7m2p4xq"]
	if other.Block.Hash != d.Block.Hash {
		t.Error("editing the def's own keys changed its hash")
	}
	// In HTML a comment before an element on the same line is left as it
	// was: the element parser reads from the start of the line.
	html := Markdown{}.Extract("p.html", []byte("<!-- ds:def id=el-k7m2p4xq --> <p>x</p>\n<div>y</div>\n"), "ds")
	if e := defsByID(html)["el-k7m2p4xq"]; e.Block.Pos.Start != 2 {
		t.Errorf("an HTML def before an element on its line bound line %d, want 2 as before", e.Block.Pos.Start)
	}
}
