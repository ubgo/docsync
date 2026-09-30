package extract

import (
	"errors"
	"reflect"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
)

// fake is a minimal extractor for registry tests.
type fake struct{ name, ext string }

func (f fake) Name() string                         { return f.name }
func (f fake) Match(p string) bool                  { return Ext(p) == f.ext }
func (f fake) Extract(string, []byte, string) Found { return Found{} }

func TestRegistry(t *testing.T) {
	t.Parallel()
	r := NewRegistry(fake{"a", ".go"}, fake{"b", ".md"})
	if e, err := r.For("x/y.go"); err != nil || e.Name() != "a" {
		t.Fatalf("For(.go) = %v,%v", e, err)
	}
	if _, err := r.For("x.txt"); !errors.Is(err, ErrNoExtractor) {
		t.Fatalf("For(.txt) err = %v", err)
	}
	r.Add(fake{"c", ".txt"})
	if e, _ := r.For("x.txt"); e.Name() != "c" {
		t.Fatal("Add did not register")
	}
	r.Prepend(fake{"first", ".go"})
	if e, _ := r.For("x.go"); e.Name() != "first" {
		t.Fatal("Prepend must take precedence")
	}
	if got := r.Names(); !reflect.DeepEqual(got, []string{"first", "a", "b", "c"}) {
		t.Errorf("Names = %v", got)
	}
}

func TestDefaultRegistryOrder(t *testing.T) {
	t.Parallel()
	r := Default()
	for p, want := range map[string]string{
		"docs/a.md": "markdown", "site/a.mdx": "markdown", "a.html": "markdown", "a.svg": "markdown",
		"config/auth.yaml": "config", ".env": "config", ".env.prod": "config", "x/.env.staging.tpl": "config", "Taskfile.yml": "config", "a.toml": "config", "a.ini": "config",
		"main.go": "code", "a.ts": "code", "a.py": "code", "a.sql": "code", "a.css": "code", "Dockerfile": "code",
		"notes.txt": "text", "rota": "text", "a.unknownext": "text", "a.csv": "text",
	} {
		e, err := r.For(p)
		if err != nil {
			t.Fatalf("For(%q): %v", p, err)
		}
		if e.Name() != want {
			t.Errorf("For(%q) = %s, want %s", p, e.Name(), want)
		}
	}
}

func TestExt(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"a/b/c.GO": ".go", "Dockerfile": "dockerfile", "dir/Makefile": "makefile", ".env": ".env", "x/.env.prod": ".prod", "Taskfile.yml": ".yml", "noext": "noext",
	} {
		if got := Ext(in); got != want {
			t.Errorf("Ext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtensions(t *testing.T) {
	t.Parallel()
	m := Extensions()
	if len(m["markdown"]) == 0 || len(m["config"]) == 0 || len(m["code"]) == 0 {
		t.Fatalf("Extensions = %v", m)
	}
	// Sorted and no overlap between code and config.
	code := map[string]bool{}
	for _, e := range m["code"] {
		code[e] = true
	}
	for _, e := range m["config"] {
		if code[e] {
			t.Errorf("%q claimed by both code and config", e)
		}
	}
	for i := 1; i < len(m["code"]); i++ {
		if m["code"][i-1] > m["code"][i] {
			t.Fatal("code extensions not sorted")
		}
	}
}

func TestParseSpan(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		val     string
		present bool
		n       int
		has     bool
		wantErr error
	}{
		{"", false, 0, false, nil},
		{"+0", true, 0, true, nil},
		{"+12", true, 12, true, nil},
		{"12", true, 0, true, ErrBadSpan},
		{"+", true, 0, true, ErrBadSpan},
		{"+x", true, 0, true, ErrBadSpan},
		{"-1", true, 0, true, ErrBadSpan},
	} {
		d := directive.Directive{Verb: "def", Args: map[string]string{}}
		if tc.present {
			d.Args[block.KeySpan] = tc.val
		}
		n, has, err := parseSpan(d)
		if n != tc.n || has != tc.has || !errors.Is(err, tc.wantErr) {
			t.Errorf("parseSpan(%q) = %d,%v,%v; want %d,%v,%v", tc.val, n, has, err, tc.n, tc.has, tc.wantErr)
		}
	}
}

func TestRequireID(t *testing.T) {
	t.Parallel()
	var f Found
	pos := block.Position{File: "x", Start: 3, End: 3}
	if id, ok := requireID(directive.Directive{Args: map[string]string{"id": "a-b"}}, pos, &f); !ok || id != "a-b" || len(f.Problems) != 0 {
		t.Error("valid id rejected")
	}
	if _, ok := requireID(directive.Directive{Args: map[string]string{}}, pos, &f); ok || len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, ErrNoID) {
		t.Error("missing id accepted")
	}
	if _, ok := requireID(directive.Directive{Args: map[string]string{"id": ""}}, pos, &f); ok || len(f.Problems) != 2 {
		t.Error("empty id accepted")
	}
	if f.Problems[0].Pos != pos {
		t.Error("problem position lost")
	}
}

func TestNewDefAndRemote(t *testing.T) {
	t.Parallel()
	d := directive.Directive{Verb: "def", Args: map[string]string{"id": "x-y", "owner": "@a"}}
	def := newDef(d, "x-y", block.KindFunc, "F", block.Position{Start: 2, End: 4}, block.Position{Start: 1, End: 1}, block.CarrierComment, "body\n")
	if def.Block.Hash == "" || def.Block.Content != "body\n" || def.Block.Owner() != "@a" || def.Remote {
		t.Errorf("newDef = %#v", def)
	}
	rd := directive.Directive{Verb: "def", Args: map[string]string{"id": "x-y", "file": "a.json"}}
	if !isRemote(rd) || isRemote(d) {
		t.Error("isRemote")
	}
	r := remoteDef(rd, "x-y", block.Position{Start: 1, End: 1}, block.CarrierLink)
	if !r.Remote || r.Block.ID != "x-y" || r.Block.Pos != (block.Position{}) || r.Block.Carrier != block.CarrierLink {
		t.Errorf("remoteDef = %#v", r)
	}
}
