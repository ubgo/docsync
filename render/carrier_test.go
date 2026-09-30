package render

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/extract"
)

// TestRenderLeavesCodeExamplesAlone pins that render acts on the directives
// check counts and nothing else. Before, it kept its own fence mask: an
// example in a code span became a permalink inside the backticks, a cfg
// example collapsed to its value, and an indented comment example expanded
// into a whole rendered block — on the very page explaining the syntax.
func TestRenderLeavesCodeExamplesAlone(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"Write `[Save](ds:block?id=sess-save-k7m2p4xq)` for [Save](ds:block?id=sess-save-k7m2p4xq).",
		"",
		"Port `[8081](ds:cfg?id=auth-port-h3v8n2wd)` is [8081](ds:cfg?id=auth-port-h3v8n2wd).",
		"",
		"    <!-- ds:block id=sess-save-k7m2p4xq -->",
		"",
		"Trailing `<!-- ds:claim note=x -->` stays.",
		"",
	}, "\n")
	out, notes := render(t, src, Options{Permalink: "{file}#L{start}-L{end}"})
	want := strings.Join([]string{
		"Write `[Save](ds:block?id=sess-save-k7m2p4xq)` for [Save](internal/store/write.go#L12-L15).",
		"",
		"Port `[8081](ds:cfg?id=auth-port-h3v8n2wd)` is 8081.",
		"",
		"    <!-- ds:block id=sess-save-k7m2p4xq -->",
		"",
		"Trailing `<!-- ds:claim note=x -->` stays.",
		"",
	}, "\n")
	if out != want || len(notes) != 0 {
		t.Errorf("render:\n%s\nwant:\n%s\nnotes %v", out, want, notes)
	}
}

// FuzzRenderActsOnlyOnExtractedLines states the agreement between the two
// readers of a page for every input: render may change a line only where
// the extractor found a directive — a reference, a def, or a problem it
// reports. A line render rewrites that check never saw is a line the reader
// sees changed with nothing in the ledger to say why; that is how every
// example in code came to be rendered.
func FuzzRenderActsOnlyOnExtractedLines(f *testing.F) {
	for _, s := range []string{
		"See [x](ds:block?id=sess-save-k7m2p4xq).",
		"`[x](ds:cfg?id=auth-port-h3v8n2wd)` and [y](ds:cfg?id=auth-port-h3v8n2wd)",
		"x\n\n    <!-- ds:block id=sess-save-k7m2p4xq -->\n",
		"- item\n\n    [p](ds:cfg?id=auth-port-h3v8n2wd) in the item\n",
		"---\n[p](ds:cfg?id=auth-port-h3v8n2wd)\n",
		"---\nds:\n  covers: [x]\n---\n<!-- ds:block id=sess-save-k7m2p4xq -->\n",
		"```\n<!-- ds:block id=sess-save-k7m2p4xq -->\n```\n",
		"Stripe: <!-- ds:chain id=gh-stripe-key-r4t6x2mb -->",
		"[8081](ds:def?id=port-a2b6f8jk) and `ds:` text",
		"<!-- ds:bogus -->\n[x](ds:block?id=)",
	} {
		f.Add(s)
	}
	ex, err := extract.Default().For("docs/x.md")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, src string) {
		src = strings.ReplaceAll(src, "\r", "")
		found := ex.Extract("docs/x.md", []byte(src), "ds")
		seen := map[int]bool{}
		mark := func(start, end int) {
			for n := start; n <= max(start, end); n++ {
				seen[n] = true
			}
		}
		for _, r := range found.Refs {
			mark(r.Reference.Pos.Start, r.Reference.Pos.End)
		}
		for _, d := range found.Defs {
			mark(d.Block.DirectivePos.Start, d.Block.DirectivePos.End)
		}
		for _, p := range found.Problems {
			mark(p.Pos.Start, p.Pos.End)
		}
		lines := strings.Split(src, "\n")
		nodes, _ := RenderNodes(Input{Doc: "docs/x.md", Src: []byte(src), Defs: all}, Options{})
		for _, n := range nodes {
			if seen[n.Line] || n.Line < 1 || n.Line > len(lines) {
				continue
			}
			orig := strings.TrimRight(lines[n.Line-1], " \t")
			if n.Kind != NodeText && strings.TrimRight(n.Text, " \t") != orig {
				t.Fatalf("render changed line %d, where check found no directive:\n%q\n-> %q", n.Line, lines[n.Line-1], n.Text)
			}
		}
	})
}

// TestRenderFoldsContinuationLines pins that a directive continued onto the
// next comment line renders with every key, as the extractor reads it.
// Before, render read one line at a time: the `lines=` on the second line
// was dropped, the whole block rendered, and the continuation comment was
// left in the page. When the directive is kept as written, every line it
// spans is kept.
func TestRenderFoldsContinuationLines(t *testing.T) {
	t.Parallel()
	out, notes := render(t, "<!-- ds:block id=sess-save-k7m2p4xq -->\n<!--   lines=2-2 -->\n\nEnd.\n", Options{})
	if !strings.Contains(out, "\t// legacy first") || strings.Contains(out, "return nil") || strings.Contains(out, "lines=2-2") || len(notes) != 0 {
		t.Errorf("folded render:\n%s\nnotes %v", out, notes)
	}
	kept := "<!-- ds:block id=sess-save-k7m2p4xq -->\n<!--   lines=9-9 -->\n"
	if out, notes := render(t, kept+"\nEnd.\n", Options{}); !strings.HasPrefix(out, kept) || len(notes) != 1 {
		t.Errorf("an out-of-range fragment keeps both lines:\n%s\nnotes %v", out, notes)
	}
	bad := "<!-- ds:block id=sess-save-k7m2p4xq -->\n<!--   lines=\"2 -->\n"
	if out, notes := render(t, bad+"\nEnd.\n", Options{}); !strings.HasPrefix(out, bad) || len(notes) != 1 {
		t.Errorf("an unparsable continued directive keeps both lines:\n%s\nnotes %v", out, notes)
	}
}

// TestRenderShortComments pins that comment-like lines too short to hold
// anything — `<!-->`, `<!--->` — are text, never a slice past their end.
// Continuation scanning reads the line after every block directive, so one
// of these there must not stop the render.
func TestRenderShortComments(t *testing.T) {
	t.Parallel()
	for _, tail := range []string{"<!-->", "<!--->", "<!---->"} {
		src := "<!-- ds:block id=sess-save-k7m2p4xq -->\n" + tail + "\n"
		if out, _ := render(t, src, Options{}); !strings.HasSuffix(out, tail+"\n") {
			t.Errorf("%q after a directive: %q", tail, out)
		}
	}
}
