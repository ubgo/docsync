package extract

import (
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
)

const goSrc = `package store

import "fmt"

// ds:def id=save-aaaaaaaa owner=@auth stability=api
//   desc="dual-write guard"
/* a block comment between the def and the code is skipped */
func (s *Store) SaveSession(ctx context.Context, sess Session) error {
	if err := s.legacy.Save(ctx, sess); err != nil {
		return fmt.Errorf("legacy save: %w", err)
	}
	s.log("brace in string: {")
	return nil
}

// ds:def id=iv-bbbbbbbb
const sweepInterval = time.Hour

// ds:def id=ty-cccccccc
type Store struct {
	db *DB
}

// ds:def id=multi-dddddddd
var (
	a = 1
	b = 2
)

// ds:def id=noc-eeeeeeee
x := 1

// ds:def id=trail-ffffffff span=+2
func A() {}
func B() {}

port := 8081 // ds:def id=port-gggggggg

// implements ds:block?id=spec-hhhhhhhh
func Persist() {}

// ds:claim owner=@auth reviewed=2026-09-06

// ds:def id=tail-iiiiiiii
`

func TestCodeGo(t *testing.T) {
	t.Parallel()
	f := Code{}.Extract("internal/store/session.go", []byte(goSrc), "ds")
	d := defsByID(f)
	cases := map[string]struct {
		kind       block.Kind
		symbol     string
		start, end int
	}{
		"save-aaaaaaaa":  {block.KindFunc, "Store.SaveSession", 8, 14},
		"iv-bbbbbbbb":    {block.KindConst, "sweepInterval", 17, 17},
		"ty-cccccccc":    {block.KindType, "Store", 20, 22},
		"multi-dddddddd": {block.KindConst, "", 25, 28},
		"noc-eeeeeeee":   {block.KindStatement, "", 31, 31},
		"trail-ffffffff": {block.KindFunc, "A", 34, 36},
		"port-gggggggg":  {block.KindLine, "", 37, 37},
	}
	if len(d) != len(cases) {
		t.Fatalf("defs = %d: %v", len(d), d)
	}
	for id, w := range cases {
		got := d[id].Block
		if got.Kind != w.kind || got.Symbol != w.symbol || got.Pos.Start != w.start || got.Pos.End != w.end {
			t.Errorf("%s = %s %q %d-%d; want %s %q %d-%d", id, got.Kind, got.Symbol, got.Pos.Start, got.Pos.End, w.kind, w.symbol, w.start, w.end)
		}
	}
	if s := d["save-aaaaaaaa"].Block; s.Owner() != "@auth" || s.Args["desc"] != "dual-write guard" || s.DirectivePos != (block.Position{Start: 5, End: 6}) {
		t.Errorf("save args/dpos = %+v", s)
	}
	if len(f.Refs) != 2 {
		t.Fatalf("refs = %+v", f.Refs)
	}
	for _, r := range f.Refs {
		switch r.Reference.Verb {
		case "block":
			if r.Reference.ID != "spec-hhhhhhhh" || r.Reference.Carrier != block.CarrierLink {
				t.Errorf("link ref = %+v", r.Reference)
			}
		case "claim":
			if r.Reference.Carrier != block.CarrierComment || r.Reference.Args["owner"] != "@auth" {
				t.Errorf("comment ref = %+v", r.Reference)
			}
		default:
			t.Errorf("unexpected ref %+v", r.Reference)
		}
	}
	if len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, ErrNothingToBind) {
		t.Errorf("problems = %+v", f.Problems)
	}
}

func TestCodePythonSQLAndOthers(t *testing.T) {
	t.Parallel()
	py := "import x\n\n# ds:def id=fn-aaaaaaaa\n@decorator\ndef sweep(conn):\n    a = 1\n\n    return a\n\nprint(1)\n# ds:def id=cls-bbbbbbbb\n# a plain comment between def and code is skipped\nclass Job:\n    pass\n# ds:def id=one-cccccccc\nvalue = 3\n"
	f := Code{}.Extract("jobs/sweep.py", []byte(py), "ds")
	d := defsByID(f)
	if fn := d["fn-aaaaaaaa"].Block; fn.Kind != block.KindFunc || fn.Symbol != "sweep" || fn.Pos.Start != 5 || fn.Pos.End != 8 {
		t.Errorf("py func = %+v", fn)
	}
	if c := d["cls-bbbbbbbb"].Block; c.Kind != block.KindType || c.Symbol != "Job" || c.Pos.Start != 13 || c.Pos.End != 14 {
		t.Errorf("py class = %+v", c)
	}
	if o := d["one-cccccccc"].Block; o.Pos.Start != 16 || o.Pos.End != 16 || o.Kind != block.KindStatement {
		t.Errorf("py assignment = %+v", o)
	}

	sql := "-- ds:def id=del-aaaaaaaa runnable=true\nDELETE FROM sessions\nWHERE expires_at < now();\n\n-- ds:def id=ct-bbbbbbbb\nCREATE TABLE IF NOT EXISTS sessions (\n  id uuid\n);\n-- ds:def id=nosemi-cccccccc\nselect 1\n\nselect 2;\n"
	f = Code{}.Extract("db/sweep.sql", []byte(sql), "ds")
	d = defsByID(f)
	if del := d["del-aaaaaaaa"].Block; del.Kind != block.KindStatement || del.Symbol != "delete" || del.Pos.End != 3 || !del.IsRunnable() {
		t.Errorf("sql delete = %+v", del)
	}
	if ct := d["ct-bbbbbbbb"].Block; ct.Symbol != "sessions" || ct.Pos.End != 8 {
		t.Errorf("sql create = %+v", ct)
	}
	if ns := d["nosemi-cccccccc"].Block; ns.Pos.Start != 10 || ns.Pos.End != 10 {
		t.Errorf("sql without semicolon is blank-bounded: %+v", ns)
	}

	ts := "// ds:def id=fn-aaaaaaaa\nexport async function rotate(token: string): Promise<Token> {\n  return x;\n}\n// ds:def id=arrow-bbbbbbbb\nexport const handler = async () => {\n  await go();\n};\n// ds:def id=rs-cccccccc\n#[derive(Debug)]\npub struct Cfg { port: u16 }\n// ds:def id=remote-dddddddd file=x.json\n"
	f = Code{}.Extract("api/rotate.ts", []byte(ts), "ds")
	d = defsByID(f)
	if fn := d["fn-aaaaaaaa"].Block; fn.Symbol != "rotate" || fn.Pos.End != 4 {
		t.Errorf("ts function = %+v", fn)
	}
	if a := d["arrow-bbbbbbbb"].Block; a.Kind != block.KindConst || a.Symbol != "handler" || a.Pos.End != 8 {
		t.Errorf("ts arrow = %+v", a)
	}
	if r := d["rs-cccccccc"].Block; r.Kind != block.KindType || r.Symbol != "Cfg" || r.Pos.Start != 11 || r.Pos.End != 11 {
		t.Errorf("attribute skipped, struct bound: %+v", r)
	}
	if !d["remote-dddddddd"].Remote {
		t.Error("remote def")
	}
}

func TestCodeBraceAndSpanEdges(t *testing.T) {
	t.Parallel()
	// Brace opened later in the declaration run, quotes with escapes, and a
	// declaration whose braces never close (falls back to blank-bounded).
	src := "// ds:def id=late-aaaaaaaa\nfunc f(\n\ta int,\n) {\n\ts := \"}\\\"{\"\n\treturn\n}\n\n// ds:def id=open-bbbbbbbb\nfunc g() {\n\tnever closed\n\nafter blank\n// ds:def id=bad-cccccccc span=nope\nx\n// ds:def\ny\n// ds:def id=spanbig-dddddddd span=+50\nz\n"
	f := Code{}.Extract("a.go", []byte(src), "ds")
	d := defsByID(f)
	if l := d["late-aaaaaaaa"].Block; l.Pos.Start != 2 || l.Pos.End != 7 {
		t.Errorf("late brace = %+v", l)
	}
	if o := d["open-bbbbbbbb"].Block; o.Pos.Start != 10 || o.Pos.End != 11 {
		t.Errorf("unclosed brace falls back to blank-bounded: %+v", o)
	}
	if s := d["spanbig-dddddddd"].Block; s.Pos.End != 19 {
		t.Errorf("span clamped: %+v", s)
	}
	if len(f.Problems) != 2 {
		t.Errorf("problems = %+v", f.Problems)
	}
	// Decorator-only file: nothing to bind.
	f = Code{}.Extract("a.py", []byte("# ds:def id=x-aaaaaaaa\n@only\n"), "ds")
	if len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, ErrNothingToBind) {
		t.Errorf("decorator only = %+v", f.Problems)
	}
	// Python def whose header does not end with ':' on that line uses brace/blank rule.
	f = Code{}.Extract("a.py", []byte("# ds:def id=y-aaaaaaaa\ndef f(a,\n      b):\n    return a\n\nnext\n"), "ds")
	if b := f.Defs[0].Block; b.Pos.Start != 2 || b.Pos.End != 4 {
		t.Errorf("py multi-line header = %+v", b)
	}
	if !(Code{}).Match("x.rs") || (Code{}).Match("x.md") || (Code{}).Match("x.yaml") || (Code{}).Name() != "code" {
		t.Error("code Match/Name")
	}
}

func TestDeclaration(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]struct {
		kind block.Kind
		sym  string
	}{
		"func (r *T) M(x int) {":          {block.KindFunc, "T.M"},
		"func (r T) M() {":                {block.KindFunc, "T.M"},
		"pub fn run() {":                  {block.KindFunc, "run"},
		"pub(crate) fn run() {":           {block.KindFunc, "run"},
		"fun kotlin() {":                  {block.KindFunc, "kotlin"},
		"export class Foo {":              {block.KindType, "Foo"},
		"interface I {":                   {block.KindType, "I"},
		"enum Color {":                    {block.KindType, "Color"},
		"  let y = 2":                     {block.KindConst, "y"},
		"static int z = 3;":               {block.KindConst, "z"},
		"CREATE OR REPLACE VIEW v AS":     {block.KindStatement, "v"},
		"create index if not exists i on": {block.KindStatement, "i"},
		"  UPDATE t SET a=1;":             {block.KindStatement, "update"},
		"with cte as (":                   {block.KindStatement, "with"},
		"random line":                     {block.KindStatement, ""},
	} {
		// One line with nothing above it: the line-local answer.
		k, s := declaration([]string{line}, 0, "")
		if k != want.kind || s != want.sym {
			t.Errorf("declaration(%q) = %s %q, want %s %q", line, k, s, want.kind, want.sym)
		}
	}
}

// TestGroupedDeclarations covers the entries inside `const ( … )`, `var ( … )`
// and `type ( … )`. Go groups related constants, limits and defaults far more
// often than it declares them singly, and those are the values documentation
// restates, so a tier that could not bind one was wrong for the common case
// (bug 18). Whether such a line declares anything is not decidable from the line
// alone, which is why declaration takes its surroundings.
func TestGroupedDeclarations(t *testing.T) {
	t.Parallel()
	const src = `package p

const (
	// MinLength is the shortest accepted.
	// ds:def id=min-k7m2p4xq
	MinLength = 8
	// MaxLength bounds the input.
	MaxLength = 256
)

var (
	// ds:def id=table-h3v8n2wd
	Table = map[string]int{
		"a": 1,
	}
	Other = 2
)

type (
	// ds:def id=shape-t4k2b9rf
	Shape struct{ X int }
	Second int
)

func f() {
	// ds:def id=assign-b3c7g9kl
	total = 1
	_ = total
}
`
	f := Code{}.Extract("a.go", []byte(src), "ds")
	if len(f.Problems) != 0 {
		t.Fatalf("problems = %+v", f.Problems)
	}
	byID := map[string]block.Block{}
	for _, d := range f.Defs {
		byID[d.Block.ID] = d.Block
	}
	for _, tc := range []struct {
		id     string
		kind   block.Kind
		symbol string
		start  int
		end    int
	}{
		// The entry binds itself, not the rest of the group and not the
		// closing paren: running to `)` is what made one directive cover
		// every constant below it.
		{"min-k7m2p4xq", block.KindConst, "MinLength", 6, 6},
		// A value spanning lines stays one block, because the bracket it
		// opened is still open.
		{"table-h3v8n2wd", block.KindConst, "Table", 13, 15},
		{"shape-t4k2b9rf", block.KindType, "Shape", 21, 21},
		// Inside a function body the same shape is an assignment, and the
		// group rule must not reach across the func declaration to claim it:
		// no symbol, and the blank-bounded extent a bare statement has always
		// had here rather than the single-line extent of a group entry.
		{"assign-b3c7g9kl", block.KindStatement, "", 27, 29},
	} {
		b, ok := byID[tc.id]
		if !ok {
			t.Errorf("no def %s", tc.id)
			continue
		}
		if b.Kind != tc.kind || b.Symbol != tc.symbol || b.Pos.Start != tc.start || b.Pos.End != tc.end {
			t.Errorf("%s = %s %q %d-%d, want %s %q %d-%d", tc.id, b.Kind, b.Symbol, b.Pos.Start, b.Pos.End, tc.kind, tc.symbol, tc.start, tc.end)
		}
	}
	// Locate finds a grouped entry by name, which is what `ds def
	// file.go#Name` does; before the fix it answered "symbol not found".
	for _, sym := range []string{"MinLength", "MaxLength", "Table", "Other", "Shape", "Second"} {
		b, err := Locate("a.go", []byte(src), Target{Symbol: sym, Prefix: "ds"})
		if err != nil {
			t.Errorf("Locate(%s) = %v", sym, err)
			continue
		}
		if b.Symbol != sym {
			t.Errorf("Locate(%s) bound %q at %d", sym, b.Symbol, b.Pos.Start)
		}
	}
	// A name that is only an assignment inside a body is still not a
	// declaration, so the lookup must not offer it.
	if _, err := Locate("a.go", []byte(src), Target{Symbol: "total", Prefix: "ds"}); !errors.Is(err, ErrSymbolNotFound) {
		t.Errorf("assignment resolved as a declaration: %v", err)
	}
}

// TestEnclosingGroupStops pins where the upward walk gives up. Each case is a
// line that looks like a group entry but is not inside one; a wrong answer
// here turns ordinary code into phantom declarations.
func TestEnclosingGroupStops(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		lines []string
		i     int
		want  string
	}{
		"inside a const group":       {[]string{"const (", "\tA = 1"}, 1, "const"},
		"inside a var group":         {[]string{"var (", "\tA = 1"}, 1, "var"},
		"inside a type group":        {[]string{"type (", "\tA int"}, 1, keywordType},
		"after the group closed":     {[]string{"const (", "\tA = 1", ")", "", "b = 2"}, 4, ""},
		"across a func":              {[]string{"const (", "\tA = 1", ")", "func f() {", "\tb = 2"}, 4, ""},
		"across a type":              {[]string{"const (", "\tA = 1", ")", "type T struct {", "\tb = 2"}, 4, ""},
		"nothing above it":           {[]string{"a = 1"}, 0, ""},
		"a call's arguments":         {[]string{"foo(", "\tb,"}, 1, ""},
		"the opener is not an entry": {[]string{"const ("}, 0, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got := enclosingGroup(tc.lines, tc.i); got != tc.want {
				t.Errorf("enclosingGroup = %q, want %q", got, tc.want)
			}
		})
	}
	// The closing paren is never an entry, even though it is inside a group.
	if k, s := declaration([]string{"const (", "\tA = 1", ")"}, 2, ".go"); s != "" || k != block.KindStatement {
		t.Errorf("close paren = %s %q", k, s)
	}
	// A bracket left open runs to the end rather than past it.
	if got := specEnd([]string{"\tA = f(", "\t\t1,"}, 1); got != 2 {
		t.Errorf("unclosed spec = %d, want 2", got)
	}
	// A bracket inside a string literal is not structure.
	if got := specEnd([]string{`	A = "("`, "\tB = 2"}, 1); got != 1 {
		t.Errorf("bracket in a literal = %d, want 1", got)
	}
}

// TestGroupContextEdges is the other half of the enumeration: lines that look
// like a group entry to the tier with no grammar. Every row here is ordinary
// code that must NOT become a phantom declaration, because the walk upward is
// a heuristic and a false positive invents a symbol nobody wrote.
func TestGroupContextEdges(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		src string
		// want is the symbol the line at line yields, "" for none.
		want string
		kind block.Kind
		line int
	}{
		// A multi-line parameter list closes with `)` at column 0, which ends
		// the upward walk: the body below it is not inside a group.
		"below a multi-line signature": {"package p\n\nfunc g(\n\ta int,\n) {\n\tx = a\n}\n", "", block.KindStatement, 6},
		// A composite literal is not a group: its opener has no `(`.
		"inside a composite literal": {"package p\n\nvar m = map[string]int{\n\t\"k\": 1,\n}\n", "", block.KindStatement, 4},
		// A multi-line call's arguments are not entries.
		"inside a call": {"package p\n\nfunc f() {\n\tg(\n\t\ta,\n\t)\n}\n", "", block.KindStatement, 5},
		// After the group closed, the same text is an assignment again.
		"after the group closes": {"package p\n\nconst (\n\tA = 1\n)\n\nfunc f() {\n\tb = 2\n}\n", "", block.KindStatement, 8},
		// A struct body is a type, not a group; its fields are not entries.
		"inside a struct body": {"package p\n\ntype T struct {\n\tA int\n}\n", "", block.KindStatement, 4},
		// The group's own closing paren is never an entry.
		"the closing paren": {"package p\n\nconst (\n\tA = 1\n)\n", "", block.KindStatement, 5},
		// An import group gets a group's extent but never a synthesised name:
		// an aliased import would otherwise look like a declaration.
		"aliased import": {"package p\n\nimport (\n\tf \"fmt\"\n)\n", "", block.KindStatement, 4},
		// And the positives, so the table shows both sides.
		"a const entry": {"package p\n\nconst (\n\tA = 1\n)\n", "A", block.KindConst, 4},
		"a var entry":   {"package p\n\nvar (\n\tA = 1\n)\n", "A", block.KindConst, 4},
		"a type entry":  {"package p\n\ntype (\n\tA int\n)\n", "A", block.KindType, 4},
	} {
		t.Run(name, func(t *testing.T) {
			lines := splitLines([]byte(tc.src))
			k, s := declaration(lines, tc.line-1, ".go")
			if s != tc.want || k != tc.kind {
				t.Errorf("declaration(line %d) = %s %q, want %s %q", tc.line, k, s, tc.kind, tc.want)
			}
		})
	}
}

// TestGroupedExtentNeverReachesTheParen pins the extent through the tier that
// has no grammar, where the original failure was swallowing every entry below
// the directive along with the group's closing paren.
// promise:member-binds-self
func TestGroupedExtentNeverReachesTheParen(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		src   string
		start int
		end   int
	}{
		"first of three": {"package p\n\nconst (\n\t// ds:def id=x-k7m2p4xq\n\tA = 1\n\tB = 2\n\tC = 3\n)\n", 5, 5},
		"last of three":  {"package p\n\nconst (\n\tA = 1\n\tB = 2\n\t// ds:def id=x-k7m2p4xq\n\tC = 3\n)\n", 7, 7},
		"value on lines": {"package p\n\nvar (\n\t// ds:def id=x-k7m2p4xq\n\tA = f(\n\t\t1,\n\t)\n\tB = 2\n)\n", 5, 7},
		// An import entry still gets the entry's extent, so the closing paren
		// is excluded even where no name is synthesised.
		"import entry": {"package p\n\nimport (\n\t// ds:def id=x-k7m2p4xq\n\t\"fmt\"\n)\n", 5, 5},
	} {
		t.Run(name, func(t *testing.T) {
			f := Code{}.Extract("a.go", []byte(tc.src), "ds")
			if len(f.Defs) != 1 {
				t.Fatalf("defs = %+v problems = %+v", f.Defs, f.Problems)
			}
			if p := f.Defs[0].Block.Pos; p.Start != tc.start || p.End != tc.end {
				t.Errorf("extent = %d-%d, want %d-%d (content %q)", p.Start, p.End, tc.start, tc.end, f.Defs[0].Block.Content)
			}
			if strings.Contains(f.Defs[0].Block.Content, "\n)") {
				t.Errorf("extent reached the group's closing paren: %q", f.Defs[0].Block.Content)
			}
		})
	}
}

// TestBodyMemberExtent pins the heuristic tier's half of the member fix. A def
// above a struct field used to run to the body's closing brace, so anchoring
// one field covered every field below it and a change to any of them flagged
// the sentence about one. Symbols are deliberately not synthesised here: the
// leading identifier names a member in Go and TypeScript but would name
// `private int x;` as "private", and a wrong symbol is worse than none because
// `path#Name` would then bind the wrong thing. The grammar tiers name them.
func TestBodyMemberExtent(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		src   string
		start int
		end   int
	}{
		"struct field":                       {"package p\n\ntype T struct {\n\t// ds:def id=x-k7m2p4xq\n\tName string\n\tAge int\n}\n", 5, 5},
		"last struct field":                  {"package p\n\ntype T struct {\n\tName string\n\t// ds:def id=x-k7m2p4xq\n\tAge int\n}\n", 6, 6},
		"interface method":                   {"package p\n\ntype I interface {\n\t// ds:def id=x-k7m2p4xq\n\tSave() error\n\tLoad() error\n}\n", 5, 5},
		"class field":                        {"class C {\n\t// ds:def id=x-k7m2p4xq\n\tname = 1;\n\tage = 2;\n}\n", 3, 3},
		"enum member":                        {"enum E {\n\t// ds:def id=x-k7m2p4xq\n\tA = 1,\n\tB = 2,\n}\n", 3, 3},
		"member with a value spanning lines": {"package p\n\ntype T struct {\n\t// ds:def id=x-k7m2p4xq\n\tName map[string]int\n\tAge int\n}\n", 5, 5},
		// A statement inside a func body is not a member: its extent is the
		// blank-bounded run it has always been, so the walk must not treat a
		// func's brace as a declaration body.
		"statement in a func body": {"package p\n\nfunc f() {\n\t// ds:def id=x-k7m2p4xq\n\tx := 1\n\t_ = x\n}\n", 5, 7},
		// A method's body belongs to the method, so a def above the method
		// keeps the brace-matched extent rather than one line.
		"method keeps its body": {"package p\n\ntype T struct{}\n\n// ds:def id=x-k7m2p4xq\nfunc (T) M() error {\n\treturn nil\n}\n", 6, 8},
	} {
		t.Run(name, func(t *testing.T) {
			path := "a.go"
			if strings.HasPrefix(tc.src, "class") || strings.HasPrefix(tc.src, "enum") {
				path = "a.ts"
			}
			f := Code{}.Extract(path, []byte(tc.src), "ds")
			if len(f.Defs) != 1 {
				t.Fatalf("defs = %+v problems = %+v", f.Defs, f.Problems)
			}
			if p := f.Defs[0].Block.Pos; p.Start != tc.start || p.End != tc.end {
				t.Errorf("extent = %d-%d, want %d-%d (content %q)", p.Start, p.End, tc.start, tc.end, f.Defs[0].Block.Content)
			}
		})
	}
}

// TestInDeclarationBodyStops is the negative half: lines that are not members,
// where treating them as one would shorten an extent that must stay whole.
func TestInDeclarationBodyStops(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		lines []string
		i     int
		want  bool
	}{
		"inside a struct":               {[]string{"type T struct {", "\tA int"}, 1, true},
		"inside an interface":           {[]string{"type I interface {", "\tA() int"}, 1, true},
		"inside a class":                {[]string{"class C {", "\ta = 1;"}, 1, true},
		"inside an enum":                {[]string{"enum E {", "\tA = 1,"}, 1, true},
		"inside a func body":            {[]string{"func f() {", "\tx := 1"}, 1, false},
		"after the body closed":         {[]string{"type T struct {", "\tA int", "}", "", "b = 2"}, 4, false},
		"nothing above it":              {[]string{"a = 1"}, 0, false},
		"a body opener is not a member": {[]string{"type T struct {"}, 0, false},
		// A statement inside a method inside a class: the innermost opener is
		// the method, so this is not a member, although a class body does
		// enclose it further out.
		"statement in a method in a class": {[]string{"class C {", "\tm() {", "\t\tx = 1"}, 2, false},
		// But the method itself is a member of the class.
		"the method itself": {[]string{"class C {", "\tm() {", "\t\tx = 1", "\t}"}, 1, true},
		// An anonymous inner struct's field is a member of it, and the keyword
		// is not at the start of that line.
		"inside an anonymous inner struct": {[]string{"type T struct {", "\tA struct {", "\t\tB int"}, 2, true},
		// A closed inner body does not make the line after it a statement.
		"after an inner body closed": {[]string{"type T struct {", "\tA struct {", "\t\tB int", "\t}", "\tC int"}, 4, true},
	} {
		t.Run(name, func(t *testing.T) {
			if got := inDeclarationBody(tc.lines, tc.i); got != tc.want {
				t.Errorf("inDeclarationBody = %v, want %v", got, tc.want)
			}
		})
	}
}
