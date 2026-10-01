package extract

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
)

// TestEveryClaimedTypeHasAStyle is the class behind bug 46: a tier claimed
// .mts, .cts, .cjs and .pyi, .tfvars and .markdown, but the comment-style
// table had no entry for them, so directives there were never read (the code
// tiers read comments through the table) and `ds def` refused to write one.
// Every type a tier claims must have a style, which is what makes reading and
// writing agree. ext/treesitter and ext/structured hold the same test for
// their own extensions.
func TestEveryClaimedTypeHasAStyle(t *testing.T) {
	t.Parallel()
	for tier, exts := range Extensions() {
		for _, e := range exts {
			p := e
			if strings.HasPrefix(e, ".") {
				p = "f" + e
			}
			if _, ok := StyleFor(p); !ok {
				t.Errorf("%s claims %s but it has no comment style", tier, e)
			}
		}
	}
	for _, p := range []string{".env.prod", "x/.env.staging.tpl", "a.markdown", "a.mts", "a.cts", "a.cjs", "a.pyi", "a.tfvars"} {
		if _, ok := StyleFor(p); !ok {
			t.Errorf("%s has no comment style", p)
		}
	}
	if st, _ := StyleFor(".env.prod"); len(st.Line) == 0 || st.Line[0] != "#" {
		t.Errorf(".env.prod style = %+v", st)
	}
}

// TestTrailingOnlyWhereTheParserReadsIt is bug 40's table half: only formats
// whose own parser treats `value   # comment` as a comment may take a key's
// directive on the key's line.
func TestTrailingOnlyWhereTheParserReadsIt(t *testing.T) {
	t.Parallel()
	for e, st := range Styles {
		want := e == ".yaml" || e == ".yml" || e == ".toml" || e == "taskfile.yml"
		if st.Trailing != want {
			t.Errorf("%s: Trailing = %v, want %v", e, st.Trailing, want)
		}
	}
}

// TestParseTargetRanges is bug 43: `path:3-5` was read as `path:3`, and any
// trailing junk after the number was ignored.
func TestParseTargetRanges(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]Target{
		"a.txt:3":     {Line: 3},
		"a.txt:3-5":   {Line: 3, End: 5},
		"a.txt:3-3":   {Line: 3},
		"C:x/a.txt:2": {Line: 2},
		"a.go#Name":   {Symbol: "Name"},
	} {
		_, got, err := ParseTarget(in)
		if err != nil || got != want {
			t.Errorf("%s = %+v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"a.txt:5-3", "a.txt:3x", "a.txt:+3", "a.txt:3-", "a.txt:-3", "a.txt:0", "a.txt:3-x", "a.txt", "a.txt:"} {
		if _, _, err := ParseTarget(bad); !errors.Is(err, ErrBadTarget) {
			t.Errorf("%s = %v, want ErrBadTarget", bad, err)
		}
	}
	if _, err := Locate("a.txt", []byte("a\nb\n"), Target{Line: 1, End: 3}); !errors.Is(err, ErrLineOutOfRange) {
		t.Errorf("a range past the end = %v", err)
	}
	if !digitsOnly("12") || digitsOnly("") || digitsOnly("1a") {
		t.Error("digitsOnly")
	}
}

// TestShellFunctionsAreNamed is bug 42: a POSIX `name() {` function was bound
// with no symbol, so the ledger recorded nothing to find it by.
func TestShellFunctionsAreNamed(t *testing.T) {
	t.Parallel()
	src := "#!/bin/sh\n# ds:def id=deploy-k7m2p4xq\ndeploy() {\n  echo hi\n}\n\n# ds:def id=build-k7m2p4xr\nbuild ()\n{\n  echo b\n}\n"
	f := Code{}.Extract("x.sh", []byte(src), "ds")
	got := map[string]string{}
	for _, d := range f.Defs {
		got[d.Block.ID] = d.Block.Symbol
	}
	if got["deploy-k7m2p4xq"] != "deploy" || got["build-k7m2p4xr"] != "build" {
		t.Errorf("shell symbols = %v", got)
	}
	// The same shape is not a function in another language.
	if k, sym := declaration([]string{"deploy() {"}, 0, ".go"); sym != "" {
		t.Errorf("go line read as %v %q", k, sym)
	}
	if _, sym := declaration([]string{"local function boot()"}, 0, ".lua"); sym != "boot" {
		t.Errorf("lua local function = %q", sym)
	}
}

// TestMultiLineCommentDirective is bug 50: a directive in a block comment
// that closes on a later line was dropped without a word, so the def did not
// exist. It is read whole, its lines are carriers (left out of the hash of the
// block that contains them), and one that never closes is a problem.
func TestMultiLineCommentDirective(t *testing.T) {
	t.Parallel()
	md := "<!-- ds:def id=policy-h2n8wq4t\n     owner=@auth -->\n## Policy\nSessions live thirty days.\n"
	f := Markdown{}.Extract("a.md", []byte(md), "ds")
	if len(f.Defs) != 1 || f.Defs[0].Block.Args[block.KeyOwner] != "@auth" || f.Defs[0].Block.Pos.Start != 3 || f.Defs[0].Block.DirectivePos.End != 2 {
		t.Fatalf("defs = %+v problems = %v", f.Defs, f.Problems)
	}
	// Anchoring a subsection with a multi-line directive leaves the outer
	// section's hash alone.
	outer := "<!-- ds:def id=outer-h2n8wq4u -->\n## A\ntext\n### B\nmore\n"
	inner := "<!-- ds:def id=outer-h2n8wq4u -->\n## A\ntext\n<!-- ds:def id=inner-h2n8wq4v\n  owner=@x -->\n### B\nmore\n"
	a := Markdown{}.Extract("a.md", []byte(outer), "ds").Defs[0].Block.Hash
	b := Markdown{}.Extract("a.md", []byte(inner), "ds").Defs[0].Block.Hash
	if a != b {
		t.Error("a multi-line carrier inside a section changed its hash")
	}
	// MDX's JSX comment, and C's block comment in code.
	mdx := Markdown{}.Extract("a.mdx", []byte("{/* ds:def id=m-h2n8wq4w\n  owner=@a */}\nPara.\n"), "ds")
	if len(mdx.Defs) != 1 {
		t.Errorf("mdx defs = %+v %v", mdx.Defs, mdx.Problems)
	}
	css := Code{}.Extract("a.css", []byte("/* ds:def id=c-h2n8wq4x\n   owner=@a */\n.a { color: red; }\n"), "ds")
	if len(css.Defs) != 1 || css.Defs[0].Block.Args[block.KeyOwner] != "@a" {
		t.Errorf("css defs = %+v %v", css.Defs, css.Problems)
	}
	// Never closed: a problem at the opening line, not silence.
	open := Markdown{}.Extract("a.md", []byte("<!-- ds:def id=x-h2n8wq4y\n## T\n"), "ds")
	if len(open.Defs) != 0 || len(open.Problems) != 1 || !errors.Is(open.Problems[0].Err, ErrUnclosedComment) || open.Problems[0].Pos.Start != 1 {
		t.Errorf("unclosed = %+v %v", open.Defs, open.Problems)
	}
	// A malformed directive inside one is reported like any other.
	bad := Markdown{}.Extract("a.md", []byte("<!-- ds:def id=x-h2n8wq4z\n  owner -->\nPara.\n"), "ds")
	if len(bad.Problems) != 1 || len(bad.Defs) != 0 {
		t.Errorf("malformed = %+v %v", bad.Defs, bad.Problems)
	}
	// A plain multi-line comment, or one that is not a directive, is not one.
	for _, src := range []string{"<!--\nds:def id=x-h2n8wq4z -->\nP\n", "<!-- note\nmore -->\nP\n", "<!-- ds:def id=x-h2n8wq4z --> <!-- more\n-->\nP\n"} {
		if got := (Markdown{}).Extract("a.md", []byte(src), "ds"); len(got.Problems) != 0 {
			t.Errorf("%q: problems %v", src, got.Problems)
		}
	}
	if _, _, ok := MultiLineDirective([]string{"<!-- ds:def id=a -->"}, 0, Styles[".md"], "ds:"); ok {
		t.Error("a comment closed on its own line is classify's, not a multi-line one")
	}
	if _, _, ok := MultiLineDirective([]string{"x"}, 0, Style{}, "ds:"); ok {
		t.Error("a style with no block comments has none")
	}
}

// TestUnreadDirectiveLinks is bug 51: `[x](ds:cfg?query="a b")` is not a
// markdown link -- the space ends it -- and the directive vanished. It is now
// a problem naming the two ways to write it; the `<…>` form is read.
func TestUnreadDirectiveLinks(t *testing.T) {
	t.Parallel()
	src := "Users [1.2M](ds:cfg?query=\"select count(*) from users\").\nUsers [1.2M](<ds:cfg?query=\"select count(*) from users\">) and [n](ds:cfg?id=a-k7m2p4xq).\n"
	f := Markdown{}.Extract("a.md", []byte(src), "ds")
	if len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, ErrLinkDestination) || f.Problems[0].Pos.Start != 1 {
		t.Errorf("problems = %v", f.Problems)
	}
	if len(f.Refs) != 2 || f.Refs[0].Directive.Args["query"] != "select count(*) from users" {
		t.Errorf("refs = %+v", f.Refs)
	}
}

// TestParseSpanExport pins the exported span reader the structured tiers use
// (bug 49), so every tier reads span= the same way.
func TestParseSpanExport(t *testing.T) {
	t.Parallel()
	f := Config{}.Extract("a.env", []byte("# ds:def id=a-k7m2p4xq span=+1\nA=1\nB=2\n"), "ds")
	if len(f.Defs) != 1 || f.Defs[0].Block.Pos.End != 3 {
		t.Fatalf("config span = %+v", f.Defs)
	}
	d := f.Defs[0].Directive
	if n, ok, err := ParseSpan(d); n != 1 || !ok || err != nil {
		t.Errorf("ParseSpan = %d %v %v", n, ok, err)
	}
}
