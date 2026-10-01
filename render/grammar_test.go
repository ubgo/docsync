package render

import (
	"strings"
	"testing"
)

// TestRenderStripsBareDirectivesInText is bug 44: §7.1 says renderers strip a
// plain-text file's bare `ds:def` line, and render left it in the output of
// every .txt page. A continuation line goes with it; a bare `ds:block` line
// renders in block position as a comment form would; the same line in a
// markdown page is text, since markdown carries directives in comments.
func TestRenderStripsBareDirectivesInText(t *testing.T) {
	t.Parallel()
	src := "ds:def id=rota-m3k9v2pq span=+2\n  owner=@ops\nWeek 37 a\nWeek 38 b\n\nds:block id=sess-save-k7m2p4xq lines=1-1\n"
	out, notes := Render(Input{Doc: "docs/rota.txt", Src: []byte(src), Defs: all}, Options{})
	got := string(out)
	if strings.Contains(got, "ds:def") || strings.Contains(got, "owner=@ops") || !strings.HasPrefix(got, "Week 37 a\nWeek 38 b\n") || !strings.Contains(got, "func Save() {") || len(notes) != 0 {
		t.Errorf("rendered:\n%s\nnotes %v", got, notes)
	}
	md, _ := Render(Input{Doc: "docs/x.md", Src: []byte("ds:def id=rota-m3k9v2pq\nWeek 37\n"), Defs: all}, Options{})
	if !strings.HasPrefix(string(md), "ds:def id=rota-m3k9v2pq") {
		t.Errorf("markdown kept the line as text? %q", md)
	}
}

// TestRenderMultiLineComment is bug 50 on the render side: a directive in an
// HTML comment that closes on a later line renders as the extractor reads it
// -- a def disappears, a block-position cite expands with every key -- and
// one that never closes stays as written.
func TestRenderMultiLineComment(t *testing.T) {
	t.Parallel()
	out, notes := render(t, "<!-- ds:def id=policy-h2n8wq4t\n     owner=@auth -->\n## Policy\n<!-- ds:block id=sess-save-k7m2p4xq\n     lines=2-2 -->\nEnd.\n", Options{})
	if strings.Contains(out, "ds:def") || strings.Contains(out, "owner=@auth") || !strings.HasPrefix(out, "## Policy\n") || !strings.Contains(out, "// legacy first") || strings.Contains(out, "return nil") || len(notes) != 0 {
		t.Errorf("rendered:\n%s\nnotes %v", out, notes)
	}
	open := "<!-- ds:block id=sess-save-k7m2p4xq\n## T\n"
	if out, _ := render(t, open, Options{}); out != open {
		t.Errorf("an unclosed comment must stay as written: %q", out)
	}
}

// TestRenderAngleLinks is bug 51 on the render side: a `<…>` destination,
// which is how a value with spaces is written, renders like any other link.
func TestRenderAngleLinks(t *testing.T) {
	t.Parallel()
	out, notes := render(t, "Port [x](<ds:cfg?id=auth-port-h3v8n2wd&format=code>).\n", Options{})
	if out != "Port `8081`.\n" || len(notes) != 0 {
		t.Errorf("rendered %q notes %v", out, notes)
	}
}
