package check

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/internal/textnorm"
	"github.com/ubgo/docsync/render"
)

// TestForeignCopyIsJudgedWithItsPublishedBody pins bug 105 in check: a
// merged def arrives without content, so the copy refresh wrote of another
// repository's block was its title and a link only, and check expected the
// same. Both now fill the body from the body store; when this run cannot
// read the body, the copy is not called tampered on a guess.
func TestForeignCopyIsJudgedWithItsPublishedBody(t *testing.T) {
	t.Parallel()
	d := def("ret-a2b6f8jk", "spec/retries.md", 4, "five times", map[string]string{block.KeyRepo: "docs"})
	body := d.Content
	d.Content = ""
	bodyAt := func(hash string) (string, bool) { return body, hash == d.Hash }
	good, ok, _ := render.Fragment(WithPublishedBody(d, bodyAt), block.Reference{Args: map[string]string{block.KeyID: d.ID}}, "ds", 40)
	if !ok || !strings.Contains(good, "five times") {
		t.Fatalf("fragment with the published body = %q", good)
	}
	mk := func(line int, text string) block.Reference {
		r := ref("block", d.ID, "d.md", line, nil)
		r.Carrier = block.CarrierBlock
		r.SetSentence("")
		r.Region = &block.Region{Start: line + 1, End: line + 5, Hash: textnorm.Short(d.Hash), Text: text}
		return r
	}
	refs := []block.Reference{mk(1, good), mk(2, "edited by hand")}
	bs := byState(Run(Input{Repo: "api", Now: now, Merged: []block.Block{d}, Refs: refs, Opts: Options{BodyAt: bodyAt}}))
	if len(bs[StateOK]) != 1 || bs[StateOK][0].Line != 1 || len(bs[StateTampered]) != 1 || bs[StateTampered][0].Line != 2 {
		t.Errorf("with bodies: ok=%+v tampered=%+v", bs[StateOK], bs[StateTampered])
	}
	bs = byState(Run(Input{Repo: "api", Now: now, Merged: []block.Block{d}, Refs: refs}))
	if len(bs[StateTampered]) != 0 || len(bs[StateOK]) != 2 {
		t.Errorf("without a body nothing is tampered: %+v", bs)
	}
	// Only a foreign block with no content is filled.
	local := def("loc-a2b6f8jk", "x.go", 1, "x", nil)
	if got := WithPublishedBody(local, bodyAt); got.Content != "x" {
		t.Errorf("local block changed: %q", got.Content)
	}
	if got := WithPublishedBody(d, nil); got.Content != "" {
		t.Errorf("no body store: %q", got.Content)
	}
	if got := WithPublishedBody(d, func(string) (string, bool) { return "", false }); got.Content != "" {
		t.Errorf("a miss: %q", got.Content)
	}
}
