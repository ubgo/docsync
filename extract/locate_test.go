package extract

import (
	"errors"
	"testing"

	"github.com/ubgo/docsync/block"
)

func TestParseTarget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		path string
		tgt  Target
		ok   bool
	}{
		{"internal/store/write.go#Store.Persist", "internal/store/write.go", Target{Symbol: "Store.Persist"}, true},
		{"config/auth.yaml:3", "config/auth.yaml", Target{Line: 3}, true},
		{"C:/x/a.go:7", "C:/x/a.go", Target{Line: 7}, true},
		{"a.go#", "", Target{}, false},
		{"a.go:zero", "", Target{}, false},
		{"a.go", "", Target{}, false},
		{":3", "", Target{}, false},
	} {
		p, tgt, err := ParseTarget(tc.in)
		if (err == nil) != tc.ok || p != tc.path || tgt != tc.tgt {
			t.Errorf("ParseTarget(%q) = %q %+v %v", tc.in, p, tgt, err)
		}
	}
}

const locateGoSrc = `package store

import "context"

// Save writes.
func (s *Store) Save(ctx context.Context) error {
	return s.legacy.Save(ctx)
}

type Store struct {
	legacy Legacy
}

const Limit = 3
`

func TestLocateCode(t *testing.T) {
	t.Parallel()
	b, err := Locate("internal/store/write.go", []byte(locateGoSrc), Target{Symbol: "Save"})
	if err != nil || b.Pos != (block.Position{Start: 6, End: 8}) || b.Symbol != "Store.Save" || b.Kind != block.KindFunc {
		t.Errorf("by method name: %+v %v", b, err)
	}
	if b, _ := Locate("a.go", []byte(locateGoSrc), Target{Symbol: "Store.Save"}); b.Pos.Start != 6 {
		t.Errorf("by qualified name: %+v", b)
	}
	if b, _ := Locate("a.go", []byte(locateGoSrc), Target{Symbol: "Store"}); b.Pos != (block.Position{Start: 10, End: 12}) || b.Kind != block.KindType {
		t.Errorf("type: %+v", b)
	}
	if b, _ := Locate("a.go", []byte(locateGoSrc), Target{Line: 5}); b.Pos.Start != 6 || b.Symbol != "Store.Save" {
		t.Errorf("line on a comment binds the code after it: %+v", b)
	}
	if b, _ := Locate("a.go", []byte(locateGoSrc), Target{Line: 14}); b.Pos != (block.Position{Start: 14, End: 14}) || b.Kind != block.KindConst || b.Content != "const Limit = 3" {
		t.Errorf("const line: %+v", b)
	}
	if b, _ := Locate("a.go", []byte("const A = 1\n\n\n"), Target{Line: 2}); b.Pos.Start != 2 || b.Content != "" {
		t.Errorf("trailing blank line binds itself: %+v", b)
	}
	if _, err := Locate("a.go", []byte(locateGoSrc), Target{Symbol: "Nope"}); !errors.Is(err, ErrSymbolNotFound) {
		t.Errorf("missing symbol: %v", err)
	}
	if _, err := Locate("a.go", []byte(locateGoSrc), Target{Line: 99}); !errors.Is(err, ErrLineOutOfRange) {
		t.Errorf("line out of range: %v", err)
	}
	if _, err := Locate("a.go", []byte(locateGoSrc), Target{}); !errors.Is(err, ErrLineOutOfRange) {
		t.Errorf("zero target: %v", err)
	}
	py := "def a():\n    return 1\n\nx = 2\n"
	if b, _ := Locate("m.py", []byte(py), Target{Symbol: "a"}); b.Pos != (block.Position{Start: 1, End: 2}) {
		t.Errorf("python indent: %+v", b)
	}
	sql := "-- c\nDELETE FROM t\nWHERE x = 1;\nSELECT 1;\n"
	if b, _ := Locate("q.sql", []byte(sql), Target{Line: 1}); b.Pos != (block.Position{Start: 2, End: 3}) || b.Kind != block.KindStatement {
		t.Errorf("sql statement: %+v", b)
	}
}

func TestLocateMarkdownConfigText(t *testing.T) {
	t.Parallel()
	md := "# Title\n\n## Session policy\n\nSessions live thirty days.\nAnd rotate.\n\n## Next\n\nother\n"
	b, err := Locate("docs/a.md", []byte(md), Target{Symbol: "session policy"})
	if err != nil || b.Pos != (block.Position{Start: 3, End: 6}) || b.Kind != block.KindSection || b.Symbol != "Session policy" {
		t.Errorf("heading: %+v %v", b, err)
	}
	if b, _ := Locate("docs/a.md", []byte(md), Target{Line: 5}); b.Pos != (block.Position{Start: 5, End: 6}) || b.Kind != block.KindParagraph {
		t.Errorf("paragraph: %+v", b)
	}
	if _, err := Locate("docs/a.md", []byte(md), Target{Symbol: "Missing"}); !errors.Is(err, ErrSymbolNotFound) {
		t.Errorf("missing heading: %v", err)
	}
	yaml := "# comment\nauth:\n  port: 8081\n  ttl: 30\nother: 1\n"
	if b, _ := Locate("c.yaml", []byte(yaml), Target{Symbol: "auth.port"}); b.Pos != (block.Position{Start: 3, End: 3}) || b.Kind != block.KindKey || b.Symbol != "auth.port" || b.Content != "  port: 8081" {
		t.Errorf("yaml dotted key: %+v", b)
	}
	if b, _ := Locate("c.yaml", []byte(yaml), Target{Symbol: "other"}); b.Pos.Start != 5 {
		t.Errorf("yaml bare key: %+v", b)
	}
	if b, _ := Locate("c.yaml", []byte(yaml), Target{Line: 4}); b.Symbol != "auth.ttl" {
		t.Errorf("yaml line: %+v", b)
	}
	if _, err := Locate("c.yaml", []byte(yaml), Target{Symbol: "auth.nope"}); !errors.Is(err, ErrSymbolNotFound) {
		t.Errorf("missing key: %v", err)
	}
	if _, err := Locate("c.yaml", []byte(yaml), Target{Symbol: "zzz.port"}); !errors.Is(err, ErrSymbolNotFound) {
		t.Errorf("wrong path prefix must not match: %v", err)
	}
	txt := "line one\nline two\n"
	if b, _ := Locate("notes.txt", []byte(txt), Target{Line: 2}); b.Pos != (block.Position{Start: 2, End: 2}) || b.Kind != block.KindLine || b.Content != "line two" {
		t.Errorf("text line: %+v", b)
	}
	if _, err := Locate("notes.txt", []byte(txt), Target{Symbol: "x"}); !errors.Is(err, ErrSymbolNotFound) {
		t.Errorf("text symbol: %v", err)
	}
}

func TestEnclosing(t *testing.T) {
	t.Parallel()
	if b, err := Enclosing("a.go", []byte(locateGoSrc), 7); err != nil || b.Symbol != "Store.Save" || b.Pos.Start != 6 {
		t.Errorf("inside func = %+v %v", b, err)
	}
	if b, _ := Enclosing("a.go", []byte(locateGoSrc), 11); b.Symbol != "Store" {
		t.Errorf("inside type = %+v", b)
	}
	if b, _ := Enclosing("a.go", []byte(locateGoSrc), 3); b.Pos.Start != 3 || b.Symbol != "" {
		t.Errorf("import line has no enclosing decl; binds itself = %+v", b)
	}
	if b, _ := Enclosing("a.go", []byte(locateGoSrc), 9); b.Pos.Start != 10 || b.Symbol != "Store" {
		t.Errorf("blank line after a func binds forward = %+v", b)
	}
	md := "# Title\n\n## Session policy\n\nSessions live thirty days.\n\n## Next\n\nother\n"
	if b, _ := Enclosing("d.md", []byte(md), 5); b.Kind != block.KindSection || b.Symbol != "Session policy" {
		t.Errorf("inside section = %+v", b)
	}
	if b, _ := Enclosing("d.md", []byte(md), 1); b.Symbol != "Title" || b.Pos.End != 9 {
		t.Errorf("title section = %+v", b)
	}
	if b, _ := Enclosing("n.txt", []byte("a\nb\n"), 2); b.Pos.Start != 2 || b.Kind != block.KindLine {
		t.Errorf("text = %+v", b)
	}
	if _, err := Enclosing("a.go", []byte(locateGoSrc), 99); !errors.Is(err, ErrLineOutOfRange) {
		t.Errorf("out of range = %v", err)
	}
}

// TestLocateFindsMembers covers looking a member up by name, which is what
// `ds def path#Name` does. It is a separate pass from naming: this tier will
// not label a member line, because the leading identifier is the name in Go and
// TypeScript but is a modifier in the C family, and a wrong symbol sends the
// def to the wrong line. Comparing against a name the caller gave is safe, so
// the lookup works where the labelling would not.
// promise:member-binds-self promise:member-name
func TestLocateFindsMembers(t *testing.T) {
	t.Parallel()
	const src = `package p

import (
	"net/http"
	f "fmt"
)

type Limits struct {
	MinLength int
	MaxLength int
}

type Store interface {
	Save() error
}

func run() {
	total = 1
	_ = total
}
`
	for name, tc := range map[string]struct {
		symbol string
		start  int
		end    int
	}{
		// A member binds its own line, never the members below it.
		"struct field":        {"MinLength", 9, 9},
		"second struct field": {"MaxLength", 10, 10},
		"interface method":    {"Save", 14, 14},
		// An import is asked for by path or by alias; the path keeps its slash
		// and loses its quotes, which is how a reader would grep for it.
		"import path":  {"net/http", 4, 4},
		"import alias": {"f", 5, 5},
		// The holder itself still resolves, so the member pass has not shadowed
		// the ordinary declaration lookup.
		"the struct":    {"Limits", 8, 11},
		"the interface": {"Store", 13, 15},
	} {
		t.Run(name, func(t *testing.T) {
			b, err := Locate("a.go", []byte(src), Target{Symbol: tc.symbol, Prefix: "ds"})
			if err != nil {
				t.Fatalf("Locate(%s) = %v", tc.symbol, err)
			}
			if b.Pos.Start != tc.start || b.Pos.End != tc.end {
				t.Errorf("%s at %d-%d, want %d-%d", tc.symbol, b.Pos.Start, b.Pos.End, tc.start, tc.end)
			}
			// The caller asked by name, so the block carries that name even
			// where this tier would not have synthesised one.
			if b.Symbol != tc.symbol {
				t.Errorf("symbol = %q, want %q", b.Symbol, tc.symbol)
			}
		})
	}
	// A statement inside a function body is not a member, so an assignment is
	// not reachable by name. A tier that answered here would be binding a line
	// that declares nothing.
	if _, err := Locate("a.go", []byte(src), Target{Symbol: "total", Prefix: "ds"}); !errors.Is(err, ErrSymbolNotFound) {
		t.Errorf("an assignment must not resolve as a declaration: %v", err)
	}
	// Nor is a name that appears only as a type.
	if _, err := Locate("a.go", []byte(src), Target{Symbol: "int", Prefix: "ds"}); !errors.Is(err, ErrSymbolNotFound) {
		t.Errorf("a type name must not resolve as a member: %v", err)
	}
}

// TestLocateTypedGoDeclarations is bug 19, reported from ubgo/auth: a standalone
// Go constant with an explicit type could not be found by name. The generic
// declaration rule was written for the C family, where the type comes first,
// so on `const Local Kind = "a"` it recorded the TYPE as the name. Typed vars
// failed the same way, which the report did not mention. Typed standalone
// constants are Go's idiom for enum-like values, so this is the common case.
// Pins bug 19.
func TestLocateTypedGoDeclarations(t *testing.T) {
	t.Parallel()
	const src = `package pkg

import "time"

const Plain = 5

const Local Kind = "a"

const Qualified time.Duration = 5

var Typed int = 3

var Slice []string

var A, B int

var Handler func() error

type Kind string
`
	for sym, line := range map[string]int{
		"Plain": 5, "Local": 7, "Qualified": 9, "Typed": 11, "Slice": 13, "A": 15, "Handler": 17,
	} {
		b, err := Locate("pkg/k.go", []byte(src), Target{Symbol: sym, Prefix: "ds"})
		if err != nil {
			t.Errorf("Locate(%s) = %v", sym, err)
			continue
		}
		if b.Pos.Start != line || b.Symbol != sym {
			t.Errorf("Locate(%s) = %q at %d, want line %d", sym, b.Symbol, b.Pos.Start, line)
		}
	}
	// The type is not a declaration, however it is spelled.
	for _, typ := range []string{"Kind", "time", "Duration", "int"} {
		if typ == "Kind" {
			// Kind IS declared, by `type Kind string`, and must resolve there.
			if b, err := Locate("pkg/k.go", []byte(src), Target{Symbol: typ, Prefix: "ds"}); err != nil || b.Pos.Start != 19 {
				t.Errorf("Kind must resolve to its type declaration: %+v %v", b, err)
			}
			continue
		}
		if _, err := Locate("pkg/k.go", []byte(src), Target{Symbol: typ, Prefix: "ds"}); !errors.Is(err, ErrSymbolNotFound) {
			t.Errorf("a type used in a declaration resolved as one: %s (%v)", typ, err)
		}
	}
	// The C family keeps type-first: in a .c file the same shape names the
	// second identifier, and the type is not a declaration.
	c := "const int MAX = 5;\nstatic int count = 0;\n"
	for sym, line := range map[string]int{"MAX": 1, "count": 2} {
		if b, err := Locate("a.c", []byte(c), Target{Symbol: sym, Prefix: "ds"}); err != nil || b.Pos.Start != line {
			t.Errorf("C Locate(%s) = %+v %v", sym, b, err)
		}
	}
	if _, err := Locate("a.c", []byte(c), Target{Symbol: "int", Prefix: "ds"}); !errors.Is(err, ErrSymbolNotFound) {
		t.Errorf("a C type resolved as a declaration: %v", err)
	}
	// A group opener is not a declaration named after its parenthesis.
	if k, s := declaration([]string{"var ("}, 0, goExt); s != "" {
		t.Errorf("var ( = %s %q", k, s)
	}
}
