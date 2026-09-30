package extract

import (
	"regexp"
	"strings"
	"testing"
)

// fuzzCite is the citation FuzzRewrapKeepsSentence places among the words.
const fuzzCite = "[x](ds:block?id=a-k7m2p4xq)"

// proseWord is a word that cannot start markdown structure at the head of a
// line, so wrapping never turns the paragraph into a list, heading, quote,
// table or setext underline: the property is about re-wrapping prose, and
// anything else changing the binding is correct.
var proseWord = regexp.MustCompile(`^[A-Za-z0-9.,!?;:'"]+$`)

// orderedMarker is a word that starts an ordered list item at a line head.
var orderedMarker = regexp.MustCompile(`^[0-9]+[.)]$`)

// FuzzRewrapKeepsSentence states the promise an ack rests on (SPEC §18): a
// paragraph re-wrapped or re-spaced — the same words, broken across lines
// differently, with spaces doubled — binds every citation in it to the same
// sentence text and hash. Were it otherwise, running a formatter over a doc
// would report every acked sentence in it as rewritten.
// promise:rewrap-not-rewrite
func FuzzRewrapKeepsSentence(f *testing.F) {
	for _, s := range []string{
		"Writes go through Save. It retries three times, e.g. on a timeout.",
		"The limit is 10. See Fig. 3 for why... It holds.",
		"One. Two! Three? Four.",
	} {
		f.Add(s, uint64(0x5a5a), uint8(3))
	}
	ex, err := Default().For("a.md")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, text string, breaks uint64, at uint8) {
		var words []string
		for _, w := range strings.Fields(text) {
			if !proseWord.MatchString(w) || orderedMarker.MatchString(w) || strings.Contains(w, "ds:") {
				return
			}
			words = append(words, w)
		}
		if len(words) == 0 || len(words) > 60 {
			return
		}
		k := int(at) % (len(words) + 1)
		words = append(words[:k], append([]string{fuzzCite}, words[k:]...)...)
		var wrapped strings.Builder
		for i, w := range words {
			if i > 0 {
				switch (breaks >> (2 * (i % 32))) & 3 {
				case 0:
					wrapped.WriteString(" ")
				case 1:
					wrapped.WriteString("\n")
				case 2:
					wrapped.WriteString("  ")
				default:
					wrapped.WriteString(" \n  ")
				}
			}
			wrapped.WriteString(w)
		}
		one := sentenceOf(t, ex, "# T\n\n"+strings.Join(words, " ")+"\n")
		two := sentenceOf(t, ex, "# T\n\n"+wrapped.String()+"\n")
		if one != two {
			t.Fatalf("re-wrapping changed the binding:\none line: %q\nwrapped:  %q\ntext: %q", one, two, wrapped.String())
		}
	})
}

// sentenceOf returns the sentence and hash of the one citation in src.
func sentenceOf(t *testing.T, ex Extractor, src string) [2]string {
	t.Helper()
	found := ex.Extract("a.md", []byte(src), "ds")
	if len(found.Refs) != 1 {
		t.Fatalf("want one citation, got %d in %q", len(found.Refs), src)
	}
	r := found.Refs[0].Reference
	if !strings.Contains(r.Sentence, fuzzCite) {
		t.Fatalf("sentence %q does not hold the citation, in %q", r.Sentence, src)
	}
	return [2]string{r.Sentence, r.SentenceHash}
}
