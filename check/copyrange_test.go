package check

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/internal/textnorm"
	"github.com/ubgo/docsync/render"
)

// structOf is a Go struct of n lines in all, the shape GitHub issue 4 hit.
func structOf(n int) string {
	lines := []string{"type Config struct {"}
	for i := 1; i <= n-2; i++ {
		lines = append(lines, fmt.Sprintf("\tField%02d string", i))
	}
	return strings.Join(append(lines, "}"), "\n")
}

// blockCite is a block-position citation of d on line 3 of d.md; region is
// the repo-mode copy under it, nil in build mode.
func blockCite(d block.Block, lines string, region *block.Region) block.Reference {
	args := map[string]string{}
	if lines != "" {
		args[keyLines] = lines
	}
	r := ref("block", d.ID, "d.md", 3, args)
	r.Carrier = block.CarrierBlock
	r.SetSentence("")
	r.Region = region
	return r
}

// freshCopy is the region refresh writes for r, as it writes it.
func freshCopy(t *testing.T, d block.Block, r block.Reference) *block.Region {
	t.Helper()
	text, ok, notes := render.Fragment(d, r, "ds", DefaultMaxLines)
	if !ok {
		t.Fatalf("a fresh copy of %v did not render: %v", r.Args, notes)
	}
	return &block.Region{Start: 4, End: 4 + strings.Count(text, "\n") + 1, Hash: textnorm.Short(d.Hash), Text: text}
}

// lone runs check over one citation and returns its only finding.
func lone(t *testing.T, defs []block.Block, r block.Reference, opts Options) Finding {
	t.Helper()
	rep := Run(Input{Repo: "api", Now: now, Defs: defs, Refs: []block.Reference{r}, Opts: opts})
	var got []Finding
	for _, f := range rep.Findings {
		if f.Doc == "d.md" {
			got = append(got, f)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want one finding for d.md:3, got %+v", rep.Findings)
	}
	return got[0]
}

// A lines= fragment that does not fit the block is `range` in repo mode as
// in build mode, never `stale`: refresh cannot write such a copy, so a
// stale finding's "run ds refresh" changed nothing, forever, and ack did
// not clear it (bug 136, GitHub issue 4). In repo mode it is an error, the
// owner's call, because the page keeps a copy nobody can write; in build
// mode it stays a warning.
func TestCopyRangeBoundaries(t *testing.T) {
	t.Parallel()
	d := def("config-k7m2p4xq", "config.go", 5, structOf(45), nil)
	d.Symbol = "Config"
	// What each lines= value is, independent of mode: inside (fits),
	// whole-but-too-large (fits, over the 40-line cap), or bad.
	type kind int
	const (
		fits kind = iota
		tooLarge
		bad
	)
	cases := []struct {
		lines string
		k     kind
	}{
		{"1-44", tooLarge},
		{"1-45", tooLarge},
		{"1-46", bad},
		{"46-46", bad},
		{"46", bad},
		{"45-45", fits},
		{"1-1", fits},
		{"3-30", fits},
		{"0-3", bad},
		{"5-3", bad},
		{"abc", bad},
		{"", tooLarge},
	}
	never := &block.Region{Start: 4, End: 4, Hash: "000000"} // the issue's placeholder closer
	for _, c := range cases {
		build := lone(t, []block.Block{d}, blockCite(d, c.lines, nil), Options{})
		repoNever := lone(t, []block.Block{d}, blockCite(d, c.lines, never), Options{})
		want := map[kind][3]State{
			fits:     {StateOK, StateStale, StateOK},
			tooLarge: {StateTooLarge, StateStale, StateTooLarge},
			bad:      {StateRange, StateRange, StateRange},
		}[c.k]
		if build.State != want[0] {
			t.Errorf("build lines=%q: %s, want %s", c.lines, build.State, want[0])
		}
		if repoNever.State != want[1] {
			t.Errorf("repo, copy never written, lines=%q: %s (%s), want %s", c.lines, repoNever.State, repoNever.Message, want[1])
		}
		if c.k == bad {
			if build.Severity != SeverityWarning || repoNever.Severity != SeverityError {
				t.Errorf("lines=%q: severity build %s repo %s, want warning and error", c.lines, build.Severity, repoNever.Severity)
			}
			if !strings.HasPrefix(repoNever.Remedy.Fix, "adjust lines= at d.md:3 to fit the block's 45 lines") {
				t.Errorf("lines=%q: remedy %q", c.lines, repoNever.Remedy.Fix)
			}
			continue
		}
		// A copy refresh just wrote checks as build mode does.
		r := blockCite(d, c.lines, nil)
		r.Region = freshCopy(t, d, r)
		if got := lone(t, []block.Block{d}, r, Options{}); got.State != want[2] {
			t.Errorf("repo, fresh copy, lines=%q: %s (%s), want %s", c.lines, got.State, got.Message, want[2])
		}
	}
	// A malformed range says so rather than calling it outside the block.
	if f := lone(t, []block.Block{d}, blockCite(d, "abc", never), Options{}); !strings.Contains(f.Message, "lines=abc is not a line range") {
		t.Errorf("malformed message = %q", f.Message)
	}
	if f := lone(t, []block.Block{d}, blockCite(d, "1-46", never), Options{}); f.Message != "lines=1-46 outside the block's 45 lines" {
		t.Errorf("outside message = %q", f.Message)
	}
	// --strict promotes the build-mode warning as it always did.
	if f := lone(t, []block.Block{d}, blockCite(d, "1-46", nil), Options{Strict: true}); f.Severity != SeverityError {
		t.Errorf("strict build range = %s", f.Severity)
	}
	// A link-form citation has no copy, so lines= stays a warning even in a
	// repository whose block-position cites are copies.
	link := blockCite(d, "1-46", nil)
	link.Carrier = block.CarrierLink
	if f := lone(t, []block.Block{d}, link, Options{}); f.State != StateRange || f.Severity != SeverityWarning {
		t.Errorf("link-form range = %s %s", f.State, f.Severity)
	}
}

// A block that shrinks under a citation that fitted it reports range, not
// stale; growing back makes the copy refresh wrote current again (bug 136).
func TestCopyRangeAsTheBlockShrinksAndGrows(t *testing.T) {
	t.Parallel()
	opts := Options{MaxLines: 100}
	was := def("config-k7m2p4xq", "config.go", 5, structOf(45), nil)
	was.Symbol = "Config"
	r := blockCite(was, "1-45", nil)
	text, ok, _ := render.Fragment(was, r, "ds", 100)
	if !ok {
		t.Fatal("the 45-line copy did not render")
	}
	r.Region = &block.Region{Start: 4, End: 4 + strings.Count(text, "\n") + 1, Hash: textnorm.Short(was.Hash), Text: text}
	if f := lone(t, []block.Block{was}, r, opts); f.State != StateOK {
		t.Fatalf("the copy as rendered = %s %s", f.State, f.Message)
	}
	shrunk := def("config-k7m2p4xq", "config.go", 5, structOf(43), nil)
	shrunk.Symbol = "Config"
	f := lone(t, []block.Block{shrunk}, r, opts)
	if f.State != StateRange || f.Severity != SeverityError || f.Message != "lines=1-45 outside the block's 43 lines" {
		t.Errorf("after shrinking = %s %s %q", f.State, f.Severity, f.Message)
	}
	if f := lone(t, []block.Block{was}, r, opts); f.State != StateOK {
		t.Errorf("after growing back = %s %s", f.State, f.Message)
	}
}

// A citation whose range is bad and whose block also changed is range, not
// stale: until lines= fits, refresh cannot write the copy, so "run ds
// refresh" would change nothing; once it fits, the next refresh clears the
// staleness too (bug 136).
func TestCopyBadRangeWinsOverChangedBlock(t *testing.T) {
	t.Parallel()
	old := def("config-k7m2p4xq", "config.go", 5, structOf(45), nil)
	d := def("config-k7m2p4xq", "config.go", 5, strings.Replace(structOf(45), "Field01 string", "Field01 int", 1), nil)
	r := blockCite(d, "1-46", &block.Region{Start: 4, End: 9, Hash: textnorm.Short(old.Hash), Text: "an old copy"})
	if f := lone(t, []block.Block{d}, r, Options{}); f.State != StateRange || f.Severity != SeverityError {
		t.Errorf("bad range and changed block = %s %s", f.State, f.Severity)
	}
}

// A block-position cite of a secret never renders, so in repo mode refresh
// skips it and its copy is never written. It is a problem whose remedy is
// another carrier, never `stale` with "run ds refresh" (bug 138); build mode
// renders nothing for it either, and says the same.
func TestSecretBlockCopyIsAProblemNotStale(t *testing.T) {
	t.Parallel()
	sec := def("key-h3v8n2wd", "e.env", 1, "sk-live-x", map[string]string{"secret": "true", "truth": "true"})
	never := &block.Region{Start: 4, End: 4, Hash: "000000"}
	for _, region := range []*block.Region{nil, never} {
		f := lone(t, []block.Block{sec}, blockCite(sec, "", region), Options{})
		if f.State != StateProblem || f.Severity != SeverityError || !strings.Contains(f.Message, "ds:block refuses to render it") || !strings.Contains(f.Remedy.Fix, "ds:cfg?id=key-h3v8n2wd") || strings.Contains(f.Remedy.Fix, "refresh") {
			t.Errorf("secret block, region %v = %+v", region, f)
		}
	}
	// The link form of a secret's block renders a link, not the value.
	link := blockCite(sec, "", nil)
	link.Carrier = block.CarrierLink
	if f := lone(t, []block.Block{sec}, link, Options{}); f.State == StateProblem {
		t.Errorf("link-form secret = %+v", f)
	}
}

// In repo mode a copy refresh has not written yet has no region, and the
// page still owes it: a bad range there is an error too, while a range that
// fits is judged as in build mode (bug 136).
func TestCopyRangeInRepoModeBeforeAnyCopy(t *testing.T) {
	t.Parallel()
	d := def("config-k7m2p4xq", "config.go", 5, structOf(45), nil)
	repo := Options{RepoMode: true}
	if f := lone(t, []block.Block{d}, blockCite(d, "1-46", nil), repo); f.State != StateRange || f.Severity != SeverityError {
		t.Errorf("repo mode, no copy yet, lines=1-46 = %s %s", f.State, f.Severity)
	}
	if f := lone(t, []block.Block{d}, blockCite(d, "1-3", nil), repo); f.State != StateOK {
		t.Errorf("repo mode, no copy yet, lines=1-3 = %s %s", f.State, f.Message)
	}
	link := blockCite(d, "1-46", nil)
	link.Carrier = block.CarrierLink
	if f := lone(t, []block.Block{d}, link, repo); f.Severity != SeverityWarning {
		t.Errorf("repo mode, link form = %s", f.Severity)
	}
}
