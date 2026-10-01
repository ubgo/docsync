package treesitter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/extract"
)

func find(t *testing.T, f extract.Found, id string) block.Block {
	t.Helper()
	for _, d := range f.Defs {
		if d.Block.ID == id {
			return d.Block
		}
	}
	t.Fatalf("no def %s in %+v (problems %+v)", id, f.Defs, f.Problems)
	return block.Block{}
}

const goSrc = `package p

import "fmt"

// Save writes the session.
// ds:def id=save-a2b6f8jk
//   owner=@auth
func (s *Store) Save() error {
	// ds:def id=inner-b3c7g9kl
	x := 1
	fmt.Println(x)
	return nil
}

// ds:def id=type-c4d8h2lm
type Store struct {
	A int
}

// ds:def id=const-d5e9j3mn
const Limit = 5

// ds:def id=group-e6f2k4np
const (
	A = 1
	B = "two"
)

// ds:def id=str-f7g3l5pq
var Name = "n"

// ds:def id=expr-m5n8p2uv
var Expr = f()

// ds:def id=span-g8h4m6qr span=+1
func Two() {}
func After() {}

// ds:def id=fn-h2j5k7rs
func (p Plain) M() {}

var y = 2 // ds:def id=line-j3k6m8st

// ds:def
func NoID() {}

// ds:def id=nothing-k4m7n9tu
`

func TestGo(t *testing.T) {
	t.Parallel()
	tier := Go()
	if tier.Name() != "go" || !tier.Match("x/y.GO") || tier.Match("y.ts") {
		t.Error("name/match")
	}
	f := tier.Extract("a.go", []byte(goSrc), "ds")
	save := find(t, f, "save-a2b6f8jk")
	if save.Kind != block.KindFunc || save.Symbol != "Store.Save" || save.Pos != (block.Position{Start: 8, End: 13}) || save.DirectivePos != (block.Position{Start: 6, End: 7}) || save.Owner() != "@auth" || !strings.HasPrefix(save.Content, "func (s *Store) Save() error {") {
		t.Errorf("save = %+v", save)
	}
	inner := find(t, f, "inner-b3c7g9kl")
	if inner.Kind != block.KindStatement || inner.Symbol != "" || inner.Pos != (block.Position{Start: 10, End: 10}) || inner.Content != "\tx := 1" {
		t.Errorf("inner = %+v", inner)
	}
	if typ := find(t, f, "type-c4d8h2lm"); typ.Kind != block.KindType || typ.Symbol != "Store" || typ.Pos.End != 18 {
		t.Errorf("type = %+v", typ)
	}
	// A single literal is a value.
	if c := find(t, f, "const-d5e9j3mn"); c.Kind != block.KindConst || c.Symbol != "Limit" || c.Content != "5" {
		t.Errorf("const = %+v", c)
	}
	if g := find(t, f, "group-e6f2k4np"); g.Symbol != "A" || !strings.Contains(g.Content, `B = "two"`) || g.Pos != (block.Position{Start: 24, End: 27}) {
		t.Errorf("group = %+v", g)
	}
	if s := find(t, f, "str-f7g3l5pq"); s.Content != `"n"` || s.Symbol != "Name" {
		t.Errorf("str = %+v", s)
	}
	// A single non-literal value keeps the whole declaration.
	if e := find(t, f, "expr-m5n8p2uv"); e.Content != "var Expr = f()" {
		t.Errorf("expr = %+v", e)
	}
	if sp := find(t, f, "span-g8h4m6qr"); sp.Pos != (block.Position{Start: 36, End: 37}) || !strings.Contains(sp.Content, "After") {
		t.Errorf("span = %+v", sp)
	}
	if m := find(t, f, "fn-h2j5k7rs"); m.Symbol != "Plain.M" {
		t.Errorf("value receiver = %+v", m)
	}
	if l := find(t, f, "line-j3k6m8st"); l.Kind != block.KindLine || l.Content != "var y = 2" {
		t.Errorf("trailing = %+v", l)
	}
	if len(f.Problems) != 2 || !errors.Is(f.Problems[0].Err, extract.ErrNoID) || !errors.Is(f.Problems[1].Err, extract.ErrNothingToBind) || f.Problems[1].Pos.Start != 47 {
		t.Errorf("problems = %+v", f.Problems)
	}
}

// promise:formatters
func TestTokenHashing(t *testing.T) {
	t.Parallel()
	tier := Go()
	a := tier.Extract("a.go", []byte("package p\n\n// ds:def id=f-a2b6f8jk\nfunc F(a int) int {\n\treturn a + 1 // add\n}\n"), "ds")
	b := tier.Extract("a.go", []byte("package p\n\n// ds:def id=f-a2b6f8jk\nfunc F(a int) int { return a+1 }\n"), "ds")
	c := tier.Extract("a.go", []byte("package p\n\n// ds:def id=f-a2b6f8jk\nfunc F(a int) int { return a+2 }\n"), "ds")
	ha, hb, hc := find(t, a, "f-a2b6f8jk").Hash, find(t, b, "f-a2b6f8jk").Hash, find(t, c, "f-a2b6f8jk").Hash
	if ha != hb || ha == hc {
		t.Errorf("token hashes: reformat %v, edit %v", ha == hb, ha == hc)
	}
	// Frozen blocks hash their comments too.
	fa := tier.Extract("a.go", []byte("package p\n\n// ds:def id=f-a2b6f8jk stability=frozen\nfunc F(a int) int {\n\treturn a + 1 // add\n}\n"), "ds")
	fb := tier.Extract("a.go", []byte("package p\n\n// ds:def id=f-a2b6f8jk stability=frozen\nfunc F(a int) int {\n\treturn a + 1 // edited\n}\n"), "ds")
	if find(t, fa, "f-a2b6f8jk").Hash == find(t, fb, "f-a2b6f8jk").Hash {
		t.Error("frozen must see comment edits")
	}
	// Content stays the source as written.
	if got := find(t, a, "f-a2b6f8jk").Content; !strings.Contains(got, "// add") {
		t.Errorf("content = %q", got)
	}
}

const tsSrc = `// header
// ds:def id=fn-a2b6f8jk
export async function f(a: number) {
  return a
}

// ds:def id=cls-b3c7g9kl
export class C {
  // ds:def id=m-c4d8h2lm
  @dec()
  m() {}
  // ds:def id=fld-d5e9j3mn
  static n = 1
}
// ds:def id=const-e6f2k4np
const X = 5
// ds:def id=multi-f7g3l5pq
export const Y = 'a', Z = 2
// ds:def id=iface-g8h4m6qr
interface I { a: number }
// ds:def id=alias-h2j5k7rs
type A = string
// ds:def id=enum-j3k6m8st
enum E { A }
// ds:def id=stmt-k4m7n9tu
console.log(X)
// ds:def id=str-m5n8p2uv
const S = "s"
`

func TestTypeScriptFamily(t *testing.T) {
	t.Parallel()
	for _, tier := range []extract.Extractor{TypeScript(), TSX()} {
		ext := map[string]string{"typescript": ".ts", "tsx": ".tsx"}[tier.Name()]
		f := tier.Extract("a"+ext, []byte(tsSrc), "ds")
		if fn := find(t, f, "fn-a2b6f8jk"); fn.Kind != block.KindFunc || fn.Symbol != "f" || fn.Pos != (block.Position{Start: 3, End: 5}) || !strings.HasPrefix(fn.Content, "export async") {
			t.Errorf("%s fn = %+v", tier.Name(), fn)
		}
		if cls := find(t, f, "cls-b3c7g9kl"); cls.Kind != block.KindType || cls.Symbol != "C" || cls.Pos.End != 14 {
			t.Errorf("class = %+v", cls)
		}
		if m := find(t, f, "m-c4d8h2lm"); m.Symbol != "C.m" || m.Pos != (block.Position{Start: 11, End: 11}) {
			t.Errorf("method = %+v", m)
		}
		if fld := find(t, f, "fld-d5e9j3mn"); fld.Symbol != "C.n" || fld.Content != "1" {
			t.Errorf("field = %+v", fld)
		}
		if c := find(t, f, "const-e6f2k4np"); c.Content != "5" || c.Symbol != "X" {
			t.Errorf("const = %+v", c)
		}
		if m := find(t, f, "multi-f7g3l5pq"); m.Symbol != "Y" || m.Content != "export const Y = 'a', Z = 2" {
			t.Errorf("multi = %+v", m)
		}
		for id, sym := range map[string]string{"iface-g8h4m6qr": "I", "alias-h2j5k7rs": "A", "enum-j3k6m8st": "E"} {
			if b := find(t, f, id); b.Kind != block.KindType || b.Symbol != sym {
				t.Errorf("%s = %+v", id, b)
			}
		}
		if s := find(t, f, "stmt-k4m7n9tu"); s.Kind != block.KindStatement || s.Symbol != "" || s.Content != "console.log(X)" {
			t.Errorf("stmt = %+v", s)
		}
		if s := find(t, f, "str-m5n8p2uv"); s.Content != `"s"` {
			t.Errorf("string literal = %+v", s)
		}
		if len(f.Problems) != 0 {
			t.Errorf("problems = %+v", f.Problems)
		}
	}
	js := JavaScript()
	if !js.Match("a.mjs") || !js.Match("b.jsx") || js.Match("a.ts") || !TSX().Match("c.tsx") || TypeScript().Match("c.tsx") {
		t.Error("ecma matches")
	}
	f := js.Extract("a.js", []byte("// ds:def id=fn-a2b6f8jk\nfunction f() { return 1 }\n// ds:def id=v-b3c7g9kl\nvar v = 1\n// ds:def id=gen-c4d8h2lm\nfunction* g() {}\n"), "ds")
	if fn := find(t, f, "fn-a2b6f8jk"); fn.Symbol != "f" || fn.Kind != block.KindFunc {
		t.Errorf("js fn = %+v", fn)
	}
	if v := find(t, f, "v-b3c7g9kl"); v.Content != "1" {
		t.Errorf("js var = %+v", v)
	}
	if g := find(t, f, "gen-c4d8h2lm"); g.Symbol != "g" {
		t.Errorf("js generator = %+v", g)
	}
}

const pySrc = `# module
# ds:def id=fn-a2b6f8jk
@dec
def f(a):
    return a

# ds:def id=cls-b3c7g9kl
class C:
    """doc"""
    # ds:def id=m-c4d8h2lm
    def m(self):
        pass

    # ds:def id=cv-d5e9j3mn
    LIMIT = 3

# ds:def id=x-e6f2k4np
X = 5
# ds:def id=typed-f7g3l5pq
Y: int = 6
# ds:def id=if-g8h4m6qr
if X:
    pass
# ds:def id=call-h2j5k7rs
print(X)
# ds:def id=str-j3k6m8st
S = "s"
`

func TestPython(t *testing.T) {
	t.Parallel()
	tier := Python()
	if !tier.Match("a.pyi") {
		t.Error("match")
	}
	f := tier.Extract("a.py", []byte(pySrc), "ds")
	// Decorators are skipped: the block is the def itself.
	if fn := find(t, f, "fn-a2b6f8jk"); fn.Symbol != "f" || fn.Pos != (block.Position{Start: 4, End: 5}) || strings.Contains(fn.Content, "@dec") {
		t.Errorf("fn = %+v", fn)
	}
	if cls := find(t, f, "cls-b3c7g9kl"); cls.Kind != block.KindType || cls.Symbol != "C" || cls.Pos.End != 15 {
		t.Errorf("class = %+v", cls)
	}
	if m := find(t, f, "m-c4d8h2lm"); m.Symbol != "C.m" || m.Kind != block.KindFunc || m.Pos != (block.Position{Start: 11, End: 12}) {
		t.Errorf("method = %+v", m)
	}
	if cv := find(t, f, "cv-d5e9j3mn"); cv.Symbol != "C.LIMIT" || cv.Content != "3" {
		t.Errorf("class var = %+v", cv)
	}
	if x := find(t, f, "x-e6f2k4np"); x.Kind != block.KindConst || x.Symbol != "X" || x.Content != "5" {
		t.Errorf("x = %+v", x)
	}
	if y := find(t, f, "typed-f7g3l5pq"); y.Symbol != "Y" || y.Content != "6" {
		t.Errorf("typed = %+v", y)
	}
	if i := find(t, f, "if-g8h4m6qr"); i.Kind != block.KindStatement || i.Symbol != "" || i.Pos != (block.Position{Start: 22, End: 23}) {
		t.Errorf("if = %+v", i)
	}
	if c := find(t, f, "call-h2j5k7rs"); c.Kind != block.KindConst || c.Symbol != "" || c.Content != "print(X)" {
		t.Errorf("call = %+v", c)
	}
	if s := find(t, f, "str-j3k6m8st"); s.Content != `"s"` {
		t.Errorf("str = %+v", s)
	}
}

const sqlSrc = `-- schema
-- ds:def id=users-a2b6f8jk
CREATE TABLE users (
  id int
);
-- ds:def id=q-b3c7g9kl
SELECT * FROM users
WHERE id = 1;
-- ds:def id=view-c4d8h2lm
CREATE OR REPLACE VIEW v AS SELECT 1;
-- ds:def id=idx-d5e9j3mn
CREATE INDEX i ON users (id);
`

func TestSQL(t *testing.T) {
	t.Parallel()
	tier := SQL()
	f := tier.Extract("s.sql", []byte(sqlSrc), "ds")
	if u := find(t, f, "users-a2b6f8jk"); u.Kind != block.KindStatement || u.Symbol != "users" || u.Pos != (block.Position{Start: 3, End: 5}) {
		t.Errorf("table = %+v", u)
	}
	if q := find(t, f, "q-b3c7g9kl"); q.Symbol != "select" || q.Pos != (block.Position{Start: 7, End: 8}) {
		t.Errorf("select = %+v", q)
	}
	if v := find(t, f, "view-c4d8h2lm"); v.Symbol != "v" {
		t.Errorf("view = %+v", v)
	}
	if i := find(t, f, "idx-d5e9j3mn"); i.Symbol != "users" {
		t.Errorf("index = %+v", i)
	}
	a := tier.Extract("s.sql", []byte("-- ds:def id=q-b3c7g9kl\nSELECT *   FROM users; -- c\n"), "ds")
	b := tier.Extract("s.sql", []byte("-- ds:def id=q-b3c7g9kl\nSELECT * FROM users;\n"), "ds")
	if find(t, a, "q-b3c7g9kl").Hash != find(t, b, "q-b3c7g9kl").Hash {
		t.Error("sql token hash ignores layout and comments")
	}
}

func TestNewAndCustomGrammar(t *testing.T) {
	t.Parallel()
	for _, g := range []Grammar{{}, {Name: "x"}, {Name: "x", Exts: []string{".x"}}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("New(%+v) must panic", g)
				}
			}()
			New(g)
		}()
	}
	// A caller-registered grammar: Go's language under another name with
	// only the generic rule, no symbols, and a wrapper that names nothing.
	custom := New(Grammar{Name: "custom", Exts: []string{".zig"}, Language: golang.GetLanguage(), Wrappers: map[string]bool{"function_declaration": true}})
	// function_declaration is a wrapper with no declaration field, so this
	// grammar cannot classify the func the directive sits above. It must say
	// so: binding the first statement inside the body instead would put the
	// def two lines from where its author aimed it, which is the silent
	// mis-binding of bug 18 in miniature.
	f := custom.Extract("a.zig", []byte("package p\n\n// ds:def id=a-a2b6f8jk\nfunc F() {\n\tx := 1\n\t_ = x\n}\n"), "ds")
	if len(f.Defs) != 0 || len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, extract.ErrSkippedCode) {
		t.Errorf("custom = %+v %+v", f.Defs, f.Problems)
	}
	// No defs: the tree is never built. A file whose extension has no
	// comment style yields nothing.
	if f := custom.Extract("a.zig", []byte("package p\n"), "ds"); len(f.Defs)+len(f.Problems) != 0 {
		t.Errorf("no defs = %+v", f)
	}
	// Same for a wrapper whose inner node this grammar does not classify.
	plainPy := New(Grammar{Name: "plainpy", Exts: []string{".rb"}, Language: python.GetLanguage(), Wrappers: map[string]bool{"decorated_definition": false}})
	f = plainPy.Extract("a.rb", []byte("# ds:def id=a-a2b6f8jk\n@dec\ndef f():\n    pass\nx = 1\n"), "ds")
	if len(f.Defs) != 0 || len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, extract.ErrSkippedCode) {
		t.Errorf("wrapper without decl = %+v %+v", f.Defs, f.Problems)
	}
	// A caller-registered grammar with no Symbol function still names a
	// declaration, through the node's own `name` field: the generic rule
	// classifies func F as a statement and fieldText supplies the name.
	noSym := New(Grammar{Name: "nosym", Exts: []string{".zig"}, Language: golang.GetLanguage()})
	f = noSym.Extract("a.zig", []byte("package p\n\n// ds:def id=n-w8n4r6vc\nfunc F() {}\n"), "ds")
	if a := find(t, f, "n-w8n4r6vc"); a.Symbol != "F" || a.Kind != block.KindStatement {
		t.Errorf("no Symbol func = %+v", a)
	}

	// A directive on the last line with no newline after it has no next
	// line to bind.
	if f := Go().Extract("a.go", []byte("package p\n// ds:def id=a-a2b6f8jk"), "ds"); len(f.Problems) != 1 {
		t.Errorf("last line = %+v", f)
	}
	// span= past the end clamps.
	if f := Go().Extract("a.go", []byte("package p\n// ds:def id=a-a2b6f8jk span=+9\nfunc F() {}\n"), "ds"); find(t, f, "a-a2b6f8jk").Pos.End != 3 {
		t.Errorf("span clamp = %+v", f.Defs)
	}
	odd := New(Grammar{Name: "odd", Exts: []string{".odd"}, Language: golang.GetLanguage()})
	if f := odd.Extract("a.odd", []byte("// ds:def id=a-a2b6f8jk\nfunc F() {}\n"), "ds"); len(f.Defs) != 0 {
		t.Errorf("no comment style = %+v", f)
	}
	if len(All()) != 6 {
		t.Error("All")
	}
}

func TestHelpers(t *testing.T) {
	t.Parallel()
	if got := lineOffsets([]byte("a\nbc\n")); len(got) != 4 || got[2] != 2 || got[3] != 5 {
		t.Errorf("offsets = %v", got)
	}
	p := sitter.NewParser()
	p.SetLanguage(golang.GetLanguage())
	tree, _ := p.ParseCtx(context.Background(), nil, []byte("package p\nfunc F() {}\n"))
	defer tree.Close()
	if fieldText(tree.RootNode(), "nope", nil) != "" {
		t.Error("missing field")
	}
	if goSymbol(tree.RootNode(), []byte("package p\nfunc F() {}\n")) != "F" {
		t.Error("root symbol walks two levels")
	}
	if valueNodes(tree.RootNode()) != nil {
		t.Error("root has no values")
	}
	pkgOnly, _ := p.ParseCtx(context.Background(), nil, []byte("package p\n"))
	defer pkgOnly.Close()
	if goSymbol(pkgOnly.RootNode(), []byte("package p\n")) != "" || goReceiver(pkgOnly.RootNode(), nil) != "" || sqlSymbol(pkgOnly.RootNode(), nil) != "" || pythonSymbol(pkgOnly.RootNode(), nil) != "" || ecmaSymbol(pkgOnly.RootNode(), nil) != "" {
		t.Error("nodes without names")
	}
	// Grouped var declarations name their first specifier through the
	// spec list; a method with a bare receiver list still resolves.
	grouped := Go().Extract("a.go", []byte("package p\n\n// ds:def id=g-a2b6f8jk\nvar (\n\tA = 1\n)\n\n// ds:def id=m-b3c7g9kl\nfunc (T) M() {}\n\n// ds:def id=n-c4d8h2lm\nfunc () N() {}\n"), "ds")
	if g := find(t, grouped, "g-a2b6f8jk"); g.Symbol != "A" || g.Content != "1" {
		t.Errorf("grouped = %+v", g)
	}
	if m := find(t, grouped, "m-b3c7g9kl"); m.Symbol != "T.M" {
		t.Errorf("bare receiver = %+v", m)
	}
	if n := find(t, grouped, "n-c4d8h2lm"); n.Symbol != "N" {
		t.Errorf("empty receiver = %+v", n)
	}
}

func TestThroughSystem(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"internal/store.go": {Data: []byte("package store\n\n// ds:def id=save-a2b6f8jk\nfunc (s *Store) Save() error {\n\treturn nil\n}\n\n// ds:def id=limit-b3c7g9kl\nconst Limit = 5\n")},
		"api/handler.ts":    {Data: []byte("// ds:def id=h-c4d8h2lm\nexport function handler() {}\n")},
		"docs/a.md":         {Data: []byte("<!-- ds:block id=save-a2b6f8jk -->\n\nLimit is [5](ds:cfg?id=limit-b3c7g9kl). See [h](ds:block?id=h-c4d8h2lm).\n")},
	}
	s, err := docsync.New(docsync.WithFS(fsys), docsync.WithExtractor(All()...))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.Check(context.Background(), docsync.CheckOptions{})
	if err != nil || rep.ExitCode != 0 || rep.Scan.Tier["internal/store.go"] != "go" || rep.Scan.Tier["api/handler.ts"] != "typescript" {
		t.Fatalf("check = %+v %v %v", rep.States, rep.Scan.Tier, err)
	}
	out, _, err := s.Render(context.Background(), "docs/a.md", docsync.RenderOptions{})
	if err != nil || !strings.Contains(string(out), "```go\nfunc (s *Store) Save() error {") || !strings.Contains(string(out), "Limit is 5.") {
		t.Errorf("render = %s %v", out, err)
	}
}

// TestGoGroupedSpecs is the bug in bug 18, reported from a real repository: a
// grouped constant could not be defined at all, and a directive written above
// one bound the next top-level declaration in silence, so the doc sentence was
// checked against an unrelated constant. `const ( … )` is one const_declaration
// whose children are const_specs, and the spec nodes were missing from the
// grammar table.
func TestGoGroupedSpecs(t *testing.T) {
	t.Parallel()
	const src = `package p

const (
	// MinLength is the shortest accepted.
	// ds:def id=min-k7m2p4xq
	MinLength = 8
	// ds:def id=max-h3v8n2wd
	MaxLength = 256
)

var (
	// ds:def id=cache-t4k2b9rf
	Cache = map[string]int{}
)

type (
	// ds:def id=shape-b3c7g9kl
	Shape struct{ X int }
)

// ds:def id=group-w8n4r6vc
const (
	A = 1
	B = 2
)

// ds:def id=after-p2c4y7mk
const Standalone = 42
`
	f := Go().Extract("a.go", []byte(src), "ds")
	if len(f.Problems) != 0 {
		t.Fatalf("problems = %+v", f.Problems)
	}
	for _, tc := range []struct {
		id      string
		kind    block.Kind
		symbol  string
		start   int
		end     int
		content string
	}{
		{"min-k7m2p4xq", block.KindConst, "MinLength", 6, 6, "8"},
		{"max-h3v8n2wd", block.KindConst, "MaxLength", 8, 8, "256"},
		{"cache-t4k2b9rf", block.KindConst, "Cache", 13, 13, "\tCache = map[string]int{}"},
		{"shape-b3c7g9kl", block.KindType, "Shape", 18, 18, "\tShape struct{ X int }"},
		// A directive above the group's own opening line still binds the whole
		// group: find classifies the outer node before descending.
		{"group-w8n4r6vc", block.KindConst, "A", 22, 25, ""},
		{"after-p2c4y7mk", block.KindConst, "Standalone", 28, 28, "42"},
	} {
		b := find(t, f, tc.id)
		if b.Kind != tc.kind || b.Symbol != tc.symbol || b.Pos.Start != tc.start || b.Pos.End != tc.end {
			t.Errorf("%s = %s %q %d-%d, want %s %q %d-%d", tc.id, b.Kind, b.Symbol, b.Pos.Start, b.Pos.End, tc.kind, tc.symbol, tc.start, tc.end)
		}
		if tc.content != "" && b.Content != tc.content {
			t.Errorf("%s content = %q, want %q", tc.id, b.Content, tc.content)
		}
	}
	// The group case must still cover both of its entries.
	if g := find(t, f, "group-w8n4r6vc"); !strings.Contains(g.Content, "A = 1") || !strings.Contains(g.Content, "B = 2") {
		t.Errorf("group lost an entry: %q", g.Content)
	}
}

// TestDirectiveMustBindTheLineBelowIt pins the guard rather than any one
// grammar gap. find() walks forward until the tables recognise something, so
// without this a construct they miss binds whatever declaration came next,
// arbitrarily far away. The author has to be told instead.
func TestDirectiveMustBindTheLineBelowIt(t *testing.T) {
	t.Parallel()
	// A grammar with no Kinds classifies nothing by name, and a const_spec is
	// missed by the generic rule too (its type ends in neither _declaration
	// nor _statement). So the entry under the directive is unbindable, and the
	// next thing this grammar does recognise is five lines further down.
	bare := New(Grammar{Name: "bare", Exts: []string{".zig"}, Language: golang.GetLanguage()})
	f := bare.Extract("a.zig", []byte("package p\n\nconst (\n\t// ds:def id=a-k7m2p4xq\n\tA = 1\n)\n\nfunc F() {}\n"), "ds")
	if len(f.Defs) != 0 || len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, extract.ErrSkippedCode) {
		t.Fatalf("defs = %+v problems = %+v", f.Defs, f.Problems)
	}
	// The message says where the block it refused would have started, which is
	// the fact that makes the cause obvious.
	if !strings.Contains(f.Problems[0].Err.Error(), "line 8") {
		t.Errorf("message must name the line it refused: %v", f.Problems[0].Err)
	}
	if f.Problems[0].Pos.Start != 4 {
		t.Errorf("reported at %d, want the directive line 4", f.Problems[0].Pos.Start)
	}
	// A def deliberately placed inside a body still binds the next statement:
	// the guard rejects a skip, not a nested anchor.
	g := Go().Extract("a.go", []byte("package p\n\nfunc F() {\n\t// ds:def id=b-h3v8n2wd\n\tx := 1\n}\n"), "ds")
	if b := find(t, g, "b-h3v8n2wd"); b.Pos.Start != 5 || len(g.Problems) != 0 {
		t.Errorf("nested anchor = %+v %+v", b, g.Problems)
	}
}

// TestGoGroupedSpecEdges is the enumeration of group shapes rather than the
// one shape bug 18 reported. Each row was checked against the real grammar; the
// table is the list, so a shape nobody thought of is a missing row and not a
// silent wrong answer.
func TestGoGroupedSpecEdges(t *testing.T) {
	t.Parallel()
	const id = "x-k7m2p4xq"
	for name, tc := range map[string]struct {
		src string
		// want is the symbol, or "" when the case must report instead of bind.
		want    string
		start   int
		end     int
		content string
	}{
		// An iota block: the first entry carries the expression, and the ones
		// after it have no `=` at all, so a rule keyed on assignment misses
		// them. Go's enums are written this way.
		"iota first entry": {"package p\n\nconst (\n\t// ds:def id=" + id + "\n\tA = iota\n\tB\n\tC\n)\n", "A", 5, 5, "\tA = iota"},
		"iota bare entry":  {"package p\n\nconst (\n\tA = iota\n\t// ds:def id=" + id + "\n\tB\n\tC\n)\n", "B", 6, 6, "\tB"},
		// A var group with a type and no value, and the var_spec_list wrapper
		// the Go grammar only puts inside `var ( … )`.
		"var with no value": {"package p\n\nvar (\n\t// ds:def id=" + id + "\n\tA int\n\tB string\n)\n", "A", 5, 5, "\tA int"},
		// Several names on one entry: the first names the block, as a
		// declaration's first specifier does everywhere else.
		"several names": {"package p\n\nconst (\n\t// ds:def id=" + id + "\n\tA, B = 1, 2\n)\n", "A", 5, 5, "\tA, B = 1, 2"},
		// The last entry: the extent must stop before the group's `)`, which
		// is the whole of the reported bug in its smallest form.
		"last entry before the paren": {"package p\n\nconst (\n\tA = 1\n\t// ds:def id=" + id + "\n\tB = 2\n)\n", "B", 6, 6, "2"},
		// A blank line inside the group does not end it.
		"blank line inside the group": {"package p\n\nconst (\n\tA = 1\n\n\t// ds:def id=" + id + "\n\tB = 2\n)\n", "B", 7, 7, "2"},
		// A value spanning lines keeps its own lines and no more.
		"value spanning lines": {"package p\n\nvar (\n\t// ds:def id=" + id + "\n\tA = map[string]int{\n\t\t\"k\": 1,\n\t}\n\tB = 2\n)\n", "A", 5, 7, ""},
		// A group declared inside a function body: the entry is scoped to the
		// function, and the symbol says so.
		"group inside a function": {"package p\n\nfunc f() {\n\tconst (\n\t\t// ds:def id=" + id + "\n\t\tA = 1\n\t)\n}\n", "f.A", 6, 6, "1"},
		// A trailing comment is not part of the value.
		"entry with a trailing comment": {"package p\n\nconst (\n\t// ds:def id=" + id + "\n\tA = 1 // why\n\tB = 2\n)\n", "A", 5, 5, "1"},
		// span= still wins inside a group, so an author can cover two entries
		// on purpose.
		"span covers two entries": {"package p\n\nconst (\n\t// ds:def id=" + id + " span=+1\n\tA = 1\n\tB = 2\n)\n", "A", 5, 6, ""},
		// An import entry binds, named by its path with the quotes off, which
		// is what a reader greps for. An aliased one is named by the alias.
		"import entry":         {"package p\n\nimport (\n\t// ds:def id=" + id + "\n\t\"net/http\"\n)\n", "net/http", 5, 5, "\t\"net/http\""},
		"aliased import entry": {"package p\n\nimport (\n\t// ds:def id=" + id + "\n\tf \"fmt\"\n)\n", "f", 5, 5, "\tf \"fmt\""},
		// An empty group has no entry to bind.
		"empty group reports": {"package p\n\nconst (\n\t// ds:def id=" + id + "\n)\n\nfunc F() {}\n", "", 0, 0, ""},
	} {
		t.Run(name, func(t *testing.T) {
			f := Go().Extract("a.go", []byte(tc.src), "ds")
			if tc.want == "" {
				if len(f.Defs) != 0 || len(f.Problems) != 1 {
					t.Fatalf("must report, got defs %+v problems %+v", f.Defs, f.Problems)
				}
				return
			}
			if len(f.Problems) != 0 {
				t.Fatalf("problems = %+v", f.Problems)
			}
			b := find(t, f, id)
			if b.Symbol != tc.want || b.Pos.Start != tc.start || b.Pos.End != tc.end {
				t.Errorf("= %q %d-%d, want %q %d-%d", b.Symbol, b.Pos.Start, b.Pos.End, tc.want, tc.start, tc.end)
			}
			if tc.content != "" && b.Content != tc.content {
				t.Errorf("content = %q, want %q", b.Content, tc.content)
			}
		})
	}
}

// TestBodyMembersBind covers the named members of a declaration's body, which
// are the same shape as the grouped specs of bug 18 and failed the same way: a
// member absent from a grammar's table did not merely fail to bind, it sent the
// finder out of the body the directive pointed into. Go interface methods and
// TypeScript interface properties, enum members and method signatures all
// reported "nothing to bind" before this.
func TestBodyMembersBind(t *testing.T) {
	t.Parallel()
	const id = "x-k7m2p4xq"
	for name, tc := range map[string]struct {
		tier   extract.Extractor
		path   string
		src    string
		kind   block.Kind
		symbol string
		start  int
		// end defaults to start: a member binds its own line unless it has a
		// body of its own, which a Python method does.
		end int
	}{
		// A struct field is what a doc restates most often after a constant,
		// and the symbol carries its type, as Go itself refers to it.
		"struct field": {tier: Go(), path: "a.go", src: "package p\n\ntype T struct {\n\t// ds:def id=" + id + "\n\tName string\n\tAge int\n}\n", kind: block.KindConst, symbol: "T.Name", start: 5},
		// A struct tag is part of the field, not a separate thing.
		"struct field with a tag": {tier: Go(), path: "a.go", src: "package p\n\ntype T struct {\n\t// ds:def id=" + id + "\n\tName string `json:\"n\"`\n}\n", kind: block.KindConst, symbol: "T.Name", start: 5},
		// An embedded field has no name of its own; the type it embeds is the
		// name, which is also what a reader would grep for.
		"embedded field": {tier: Go(), path: "a.go", src: "package p\n\ntype T struct {\n\t// ds:def id=" + id + "\n\tBase\n\tAge int\n}\n", kind: block.KindConst, symbol: "T.Base", start: 5},
		// An interface method is a signature, so it is a func.
		"interface method":   {tier: Go(), path: "a.go", src: "package p\n\ntype I interface {\n\t// ds:def id=" + id + "\n\tSave() error\n\tLoad() error\n}\n", kind: block.KindFunc, symbol: "I.Save", start: 5},
		"embedded interface": {tier: Go(), path: "a.go", src: "package p\n\ntype I interface {\n\t// ds:def id=" + id + "\n\tfmt.Stringer\n\tLoad() error\n}\n", kind: block.KindType, symbol: "I.Stringer", start: 5},
		// A field of an anonymous inner struct still names its outer type.
		"nested struct field": {tier: Go(), path: "a.go", src: "package p\n\ntype T struct {\n\tA struct {\n\t\t// ds:def id=" + id + "\n\t\tB int\n\t}\n}\n", kind: block.KindConst, symbol: "T.B", start: 6},
		// An import is how a doc names the dependency it describes. The path
		// loses its quotes because that is what someone greps for; an alias
		// wins over the path, because that is the name in scope.
		"import entry":         {tier: Go(), path: "a.go", src: "package p\n\nimport (\n\t// ds:def id=" + id + "\n\t\"net/http\"\n)\n", kind: block.KindConst, symbol: "net/http", start: 5},
		"aliased import entry": {tier: Go(), path: "a.go", src: "package p\n\nimport (\n\t// ds:def id=" + id + "\n\tf \"fmt\"\n)\n", kind: block.KindConst, symbol: "f", start: 5},
		"single import":        {tier: Go(), path: "a.go", src: "package p\n\n// ds:def id=" + id + "\nimport \"net/http\"\n", kind: block.KindConst, symbol: "net/http", start: 4},
		// A generic type set is an interface member too. Its terms have no name
		// of their own, so the first term names it -- not ideal to cite, but
		// honest, and the alternative was walking out of the interface.
		"type constraint":          {tier: Go(), path: "a.go", src: "package p\n\ntype N interface {\n\t// ds:def id=" + id + "\n\t~int | ~string\n}\n", kind: block.KindType, symbol: "N.~int", start: 5},
		"type constraint no tilde": {tier: Go(), path: "a.go", src: "package p\n\ntype N interface {\n\t// ds:def id=" + id + "\n\tint | string\n}\n", kind: block.KindType, symbol: "N.int", start: 5},
		// TypeScript has the same shapes under different node names.
		"ts interface property": {tier: TypeScript(), path: "a.ts", src: "interface I {\n\t// ds:def id=" + id + "\n\tname: string;\n\tage: number;\n}\n", kind: block.KindConst, symbol: "I.name", start: 3},
		"ts interface method":   {tier: TypeScript(), path: "a.ts", src: "interface I {\n\t// ds:def id=" + id + "\n\tsave(): void;\n\tload(): void;\n}\n", kind: block.KindFunc, symbol: "I.save", start: 3},
		"ts enum member":        {tier: TypeScript(), path: "a.ts", src: "enum E {\n\t// ds:def id=" + id + "\n\tA = 1,\n\tB = 2,\n}\n", kind: block.KindConst, symbol: "E.A", start: 3},
		"ts class field":        {tier: TypeScript(), path: "a.ts", src: "class C {\n\t// ds:def id=" + id + "\n\tname = \"a\";\n\tage = 1;\n}\n", kind: block.KindConst, symbol: "C.name", start: 3},
		// And Python, whose members already worked; the row is here so a
		// regression in the shared walk shows up for every grammar at once.
		"py class attribute": {tier: Python(), path: "a.py", src: "class C:\n    # ds:def id=" + id + "\n    NAME = \"a\"\n    AGE = 1\n", kind: block.KindConst, symbol: "C.NAME", start: 3},
		"py method":          {tier: Python(), path: "a.py", src: "class C:\n    # ds:def id=" + id + "\n    def m(self):\n        pass\n", kind: block.KindFunc, symbol: "C.m", start: 3, end: 4},
	} {
		t.Run(name, func(t *testing.T) {
			f := tc.tier.Extract(tc.path, []byte(tc.src), "ds")
			if len(f.Problems) != 0 {
				t.Fatalf("problems = %+v", f.Problems)
			}
			b := find(t, f, id)
			if b.Kind != tc.kind || b.Symbol != tc.symbol || b.Pos.Start != tc.start {
				t.Errorf("= %s %q at %d, want %s %q at %d", b.Kind, b.Symbol, b.Pos.Start, tc.kind, tc.symbol, tc.start)
			}
			// A member covers itself and, where it has one, its own body --
			// never the members below it and never the brace that closes the
			// holder, which is the extent bug in its other form.
			wantEnd := tc.end
			if wantEnd == 0 {
				wantEnd = tc.start
			}
			if b.Pos.End != wantEnd {
				t.Errorf("extent = %d-%d, want %d-%d", b.Pos.Start, b.Pos.End, tc.start, wantEnd)
			}
		})
	}
}

// TestBodyContainersDoNotQualify pins that a body is a container, not a name
// segment: without it a member came out with its holder's name repeated, the
// way a grouped type once read "Shape.Shape".
func TestBodyContainersDoNotQualify(t *testing.T) {
	t.Parallel()
	const id = "x-k7m2p4xq"
	for name, tc := range map[string]struct {
		tier extract.Extractor
		path string
		src  string
		want string
	}{
		"go struct":    {Go(), "a.go", "package p\n\ntype Shape struct {\n\t// ds:def id=" + id + "\n\tX int\n}\n", "Shape.X"},
		"go interface": {Go(), "a.go", "package p\n\ntype Shape interface {\n\t// ds:def id=" + id + "\n\tX() int\n}\n", "Shape.X"},
		"ts interface": {TypeScript(), "a.ts", "interface Shape {\n\t// ds:def id=" + id + "\n\tx: number;\n}\n", "Shape.x"},
		"ts enum":      {TypeScript(), "a.ts", "enum Shape {\n\t// ds:def id=" + id + "\n\tX = 1,\n}\n", "Shape.X"},
		"ts class":     {TypeScript(), "a.ts", "class Shape {\n\t// ds:def id=" + id + "\n\tx = 1;\n}\n", "Shape.x"},
	} {
		t.Run(name, func(t *testing.T) {
			b := find(t, tc.tier.Extract(tc.path, []byte(tc.src), "ds"), id)
			if b.Symbol != tc.want {
				t.Errorf("symbol = %q, want %q", b.Symbol, tc.want)
			}
		})
	}
}

// TestObjectLiteralPropertiesBind is finding 8 of the field report. Config in
// this ecosystem is an object -- Vite, Vitest, ESLint, Tailwind -- and a
// directive above one of its properties bound nothing, reported only after the
// file had been edited. A property is named by the keys leading to it, since a
// bare `port` is ambiguous in any config with more than one server.
func TestObjectLiteralPropertiesBind(t *testing.T) {
	t.Parallel()
	const id = "x-k7m2p4xq"
	for name, tc := range map[string]struct {
		src     string
		symbol  string
		start   int
		end     int
		content string
	}{
		"top-level property": {"export default {\n\t// ds:def id=" + id + "\n\tport: 5173,\n};\n", "port", 3, 3, "5173"},
		// Nested: the path through the keys, which is what a reader greps for.
		"nested property": {"export default {\n\tserver: {\n\t\t// ds:def id=" + id + "\n\t\tport: 5173,\n\t},\n};\n", "server.port", 4, 4, "5173"},
		// A property whose value is an object covers the whole object.
		"object-valued property": {"export default {\n\t// ds:def id=" + id + "\n\tserver: {\n\t\tport: 1,\n\t},\n};\n", "server", 3, 5, ""},
		// A quoted key loses its quotes.
		"quoted key": {"export default {\n\t// ds:def id=" + id + "\n\t'x-frame': \"DENY\",\n};\n", "x-frame", 3, 3, `"DENY"`},
		// Inside a named const the path starts at the object, not the const:
		// the const is its own declaration with its own name.
		"inside a const": {"const cfg = {\n\t// ds:def id=" + id + "\n\tretries: 3,\n};\n", "retries", 3, 3, "3"},
		// A computed key has no name a reader could search for, so it names
		// nothing -- but the directive above it still binds.
		"computed key": {"export default {\n\t// ds:def id=" + id + "\n\t[key]: 1,\n};\n", "", 3, 3, "1"},
		// A computed key further up leaves the whole path unnameable, rather
		// than naming a path that skips a segment.
		"under a computed key": {"export default {\n\t[key]: {\n\t\t// ds:def id=" + id + "\n\t\tport: 1,\n\t},\n};\n", "", 4, 4, "1"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, tier := range []extract.Extractor{TypeScript(), JavaScript()} {
				f := tier.Extract("a."+map[string]string{"typescript": "ts", "javascript": "js"}[tier.Name()], []byte(tc.src), "ds")
				if len(f.Problems) != 0 {
					t.Fatalf("%s: problems = %+v", tier.Name(), f.Problems)
				}
				b := find(t, f, id)
				if b.Symbol != tc.symbol || b.Pos.Start != tc.start || b.Pos.End != tc.end {
					t.Errorf("%s: = %q %d-%d, want %q %d-%d", tier.Name(), b.Symbol, b.Pos.Start, b.Pos.End, tc.symbol, tc.start, tc.end)
				}
				if tc.content != "" && b.Content != tc.content {
					t.Errorf("%s: content = %q, want %q", tier.Name(), b.Content, tc.content)
				}
			}
		})
	}
}

// TestStringContentIsHashed pins that changing only the text inside a string
// changes a block's hash, in every grammar and every string form (bug 22).
// Go's grammar gives a string literal no node for its text, only the quote
// marks, so the hash used to see `"one"` and `"two"` as the same block and a
// changed message, URL or query never flagged the sentences citing it.
func TestStringContentIsHashed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, file, src string
		tier            extract.Extractor
	}{
		{"go interpreted", "a.go", "package p\n\n// ds:def id=s-a2b6f8jk\nfunc E() string { return \"%s\" }\n", Go()},
		{"go escapes", "a.go", "package p\n\n// ds:def id=s-a2b6f8jk\nfunc E() string { return \"a\\n%s\\t\" }\n", Go()},
		{"go raw", "a.go", "package p\n\n// ds:def id=s-a2b6f8jk\nfunc E() string { return `%s` }\n", Go()},
		{"go call argument", "a.go", "package p\n\n// ds:def id=s-a2b6f8jk\nvar A = errors.New(\"%s\")\n", Go()},
		{"go rune", "a.go", "package p\n\n// ds:def id=s-a2b6f8jk\nfunc R() rune { return '%s' }\n", Go()},
		{"typescript", "a.ts", "// ds:def id=s-a2b6f8jk\nexport function e() { return \"%s\" }\n", TypeScript()},
		{"tsx template", "a.tsx", "// ds:def id=s-a2b6f8jk\nexport function e() { return `x ${1} %s` }\n", TSX()},
		{"javascript", "a.js", "// ds:def id=s-a2b6f8jk\nfunction e() { return '%s' }\n", JavaScript()},
		{"python", "a.py", "# ds:def id=s-a2b6f8jk\ndef e():\n    return \"%s\"\n", Python()},
		{"python f-string", "a.py", "# ds:def id=s-a2b6f8jk\ndef e(x):\n    return f\"{x} %s\"\n", Python()},
		{"sql", "a.sql", "-- ds:def id=s-a2b6f8jk\nSELECT * FROM users WHERE name = '%s';\n", SQL()},
	}
	for _, c := range cases {
		hash := func(v string) string {
			return find(t, c.tier.Extract(c.file, []byte(fmt.Sprintf(c.src, v)), "ds"), "s-a2b6f8jk").Hash
		}
		if v1, v2 := "o", "t"; hash(v1) == hash(v2) {
			t.Errorf("%s: changing the string's text left the hash unchanged", c.name)
		}
	}
	// Layout outside the string still does not count.
	a := Go().Extract("a.go", []byte("package p\n\n// ds:def id=s-a2b6f8jk\nfunc E() string { return \"one\" }\n"), "ds")
	b := Go().Extract("a.go", []byte("package p\n\n// ds:def id=s-a2b6f8jk\nfunc E() string {\n\treturn   \"one\"\n}\n"), "ds")
	if find(t, a, "s-a2b6f8jk").Hash != find(t, b, "s-a2b6f8jk").Hash {
		t.Error("reformatting around a string changed the hash")
	}
}
