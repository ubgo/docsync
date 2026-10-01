package docsync

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/extract"
)

func defineSys(t *testing.T, fsys fstest.MapFS, extra ...Option) *System {
	t.Helper()
	s, err := New(append([]Option{WithFS(fsys)}, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestDefineTargets covers how Define resolves what it is asked for with the
// root module's own tiers; cli/cmd/ds/deftargets_test.go covers the grammar
// tiers the binary ships.
func TestDefineTargets(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a.go":          {Data: []byte("package a\n\n// Save saves; Saver is not it.\n// ds:def id=save-k7m2p4xq\nfunc Save() {\n}\n\n// Zed one\n// Zed two\nfunc Load() {\n}\n")},
		"notes.txt":     {Data: []byte("ds:def id=rota-k7m2p4xq\nWeek 37 a\nWeek 38 b\n\none\ntwo\nthree\n")},
		"cross.txt":     {Data: []byte("ds:def id=rota-k7m2p4xr span=+2\nWeek 37\nWeek 38\nWeek 39\nWeek 40\n")},
		"d.md":          {Data: []byte("# Title\n\n## Session policy\nText.\n")},
		"p.json":        {Data: []byte("{\"port\": 8080}\n")},
		"app.yaml":      {Data: []byte("server:\n  port: 8080\n")},
		"db.properties": {Data: []byte("db.port=5432\n")},
	}
	s := defineSys(t, fsys)
	ctx := context.Background()

	// bug 58: the directive line of an existing def names that def.
	if r, err := s.Define(ctx, "notes.txt:1", DefineOptions{}); err != nil || !r.Existing || r.ID != "rota-k7m2p4xq" {
		t.Errorf("directive line = %+v, %v", r, err)
	}
	// A def already recorded under the name answers at once.
	if r, err := s.Define(ctx, "a.go#Save", DefineOptions{}); err != nil || !r.Existing || r.ID != "save-k7m2p4xq" {
		t.Errorf("existing symbol = %+v, %v", r, err)
	}
	// A heading matched without regard to case keeps the line matcher's answer.
	if r, err := s.Define(ctx, "d.md#session policy", DefineOptions{}); err != nil || r.Block.Pos.Start != 3 {
		t.Errorf("heading = %+v, %v", r.Block.Pos, err)
	}
	// The line matcher finds headings without regard to case; the tier's exact
	// name wins when a later heading has it.
	cased := defineSys(t, fstest.MapFS{"h.md": {Data: []byte("## session policy\nA.\n\n## Session policy\nB.\n\nSession policy again.\n")}})
	if r, err := cased.Define(ctx, "h.md#Session policy", DefineOptions{}); err != nil || r.Block.Pos.Start != 4 {
		t.Errorf("exact heading = %+v, %v", r.Block.Pos, err)
	}
	// A dotted properties key the config tier spells quoted.
	if r, err := s.Define(ctx, "db.properties#db.port", DefineOptions{}); err != nil || r.Block.Symbol != `"db.port"` {
		t.Errorf("properties key = %q, %v", r.Block.Symbol, err)
	}
	// No tier names it: the refusal says which tier was asked.
	if _, err := s.Define(ctx, "a.go#Nothing", DefineOptions{}); !errors.Is(err, extract.ErrSymbolNotFound) || !strings.Contains(err.Error(), "the code tier names no block") {
		t.Errorf("unknown symbol = %v", err)
	}
	// A format with no carrier: every candidate probe fails, and so does the lookup.
	if _, err := s.Define(ctx, "p.json#port", DefineOptions{}); !errors.Is(err, extract.ErrSymbolNotFound) {
		t.Errorf("json symbol = %v", err)
	}
	// Two comment lines mention it; both locate the same declaration, which is
	// asked about once and is not it.
	if _, err := s.Define(ctx, "a.go#Zed", DefineOptions{}); !errors.Is(err, extract.ErrSymbolNotFound) {
		t.Errorf("a name only comments mention = %v", err)
	}
	if _, err := s.Define(ctx, "a.go#Save.", DefineOptions{}); err == nil {
		t.Error("an empty last segment names nothing")
	}

	// bug 43: a range binds exactly, with the span the tier needs.
	r, err := s.Define(ctx, "notes.txt:5-6", DefineOptions{})
	if err != nil || !strings.Contains(r.Edit.New, "span=+2") || r.Block.Pos.Start != 5 || r.Block.Pos.End != 6 {
		t.Errorf("text range = %+v %q, %v", r.Block.Pos, r.Edit.New, err)
	}
	if r, err := s.Define(ctx, "notes.txt:5-7", DefineOptions{}); err != nil || strings.Contains(r.Edit.New, "span") {
		t.Errorf("a range that is the natural block needs no span: %q, %v", r.Edit.New, err)
	}
	if _, err := s.Define(ctx, "a.go:7-8", DefineOptions{}); !errors.Is(err, ErrRange) {
		t.Errorf("a range that starts on a blank line = %v", err)
	}
	// bug 58 (promise:def-refuses-crossing): a def whose block would cross
	// another's is refused.
	if _, err := s.Define(ctx, "cross.txt:3", DefineOptions{}); !errors.Is(err, ErrWouldNotBind) || !strings.Contains(err.Error(), "rota-k7m2p4xr") {
		t.Errorf("crossing = %v", err)
	}
	// bug 57: a dotfile labels from its whole name.
	if fileLabel(".gitignore") != "gitignore" || fileLabel("a/b.go") != "b" || fileLabel("-.-") != "" {
		t.Error("fileLabel")
	}
}

// TestDefineHelpers pins the small pieces Define is built from.
func TestDefineHelpers(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		got, want string
		rank      symbolMatch
	}{
		{"", "a", matchNone},
		{"A.b", "A.b", matchExact},
		{"A.b", "b", matchSuffix},
		{`"db.port"`, "db.port", matchUnquoted},
		{"A.b", "c", matchNone},
	} {
		if r := matchSymbol(c.got, c.want); r != c.rank {
			t.Errorf("matchSymbol(%q, %q) = %v, want %v", c.got, c.want, r, c.rank)
		}
	}
	for line, want := range map[string]bool{"func Save() {": true, "Saver Save": true, "Saver": false, "x.Save": true, "": false} {
		if mentions(line, "Save") != want {
			t.Errorf("mentions(%q) != %v", line, want)
		}
	}
	if mentions("anything", "") {
		t.Error("an empty name mentions nothing")
	}
	// A trailing directive replaces its line and moves nothing.
	b := block.Block{Pos: block.Position{Start: 3, End: 3}}
	if got := unshift(b, Edit{Line: 3, Old: "x", New: "x # d"}); got.Pos != b.Pos {
		t.Errorf("trailing unshift = %+v", got.Pos)
	}
	// An insert above line 3 moves lines from 3 down, not those above.
	b = block.Block{Pos: block.Position{Start: 4, End: 6}}
	if got := unshift(b, Edit{Line: 3, New: "# d"}); got.Pos.Start != 3 || got.Pos.End != 5 || got.DirectivePos.Start != 3 {
		t.Errorf("insert unshift = %+v", got)
	}
	b = block.Block{Pos: block.Position{Start: 1, End: 2}}
	if got := unshift(b, Edit{Line: 3, New: "# d"}); got.Pos.Start != 1 || got.Pos.End != 2 {
		t.Errorf("lines above the insert moved: %+v", got.Pos)
	}
	// A YAML key takes its directive trailing, and the probe maps it in place.
	s := defineSys(t, fstest.MapFS{"app.yaml": {Data: []byte("server:\n  port: 8080\n")}})
	if r, err := s.Define(context.Background(), "app.yaml#server.port", DefineOptions{}); err != nil || r.Edit.Old == "" || r.Block.Pos.Start != 2 {
		t.Errorf("yaml = %+v, %v", r, err)
	}
}

// TestAdoptSymbolNeedsATier pins adopt's use of the scanning tier: a
// `path#Name` link into a file no registered tier reads is unresolved, with
// the registry's reason.
func TestAdoptSymbolNeedsATier(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a.go":      {Data: []byte("package a\n\nfunc F() {}\n")},
		"docs/d.md": {Data: []byte("See [F](../a.go#F).\n")},
	}
	s := defineSys(t, fsys, WithRegistry(extract.NewRegistry(extract.Markdown{})))
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Adopt(context.Background(), res)
	if err != nil || len(out.Unresolved) != 1 || !strings.Contains(out.Unresolved[0].Reason, "no extractor") {
		t.Errorf("adopt = %+v, %v", out, err)
	}
}
