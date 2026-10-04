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

// TestRemoteDefBySymbol pins bug 133: a remote def with pick=symbol:<name>
// binds the block an in-file `ds def file#name` would, with the same hash,
// and the target file is never changed.
func TestRemoteDefBySymbol(t *testing.T) {
	t.Parallel()
	code := "package store\n\nfunc Helper() {}\n\n// Save writes a session.\nfunc Save() error {\n\treturn nil\n}\n"
	remote := newSys(t, fstest.MapFS{
		"internal/store/save.go": {Data: []byte(code)},
		"docs/r.md":              {Data: []byte("<!-- ds:def id=save-k7m2p4xq file=internal/store/save.go pick=symbol:Save -->\n\nSee [save](ds:block?id=save-k7m2p4xq).\n")},
	})
	res, err := remote.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got block.Block
	for _, b := range res.Defs {
		if b.ID == "save-k7m2p4xq" {
			got = b
		}
	}
	if got.Pos != (block.Position{File: "internal/store/save.go", Start: 6, End: 8}) || !strings.Contains(got.Content, "func Save() error") {
		t.Fatalf("remote symbol def = %+v (problems %v)", got, res.Problems)
	}
	inFile := newSys(t, fstest.MapFS{
		"internal/store/save.go": {Data: []byte(strings.Replace(code, "// Save writes a session.\n", "// Save writes a session.\n// ds:def id=save-k7m2p4xq\n", 1))},
	})
	res2, err := inFile.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Defs) != 1 || res2.Defs[0].Hash != got.Hash {
		t.Errorf("a remote symbol def must hash like the in-file def: %+v vs %s", res2.Defs, got.Hash)
	}
}

// TestRemoteSymbolInATrailingCarrierFormat: in YAML the directive trails the
// key on its own line, so the in-memory probe used to find the block landed
// inside it, and with the YAML tier the remote def's content carried `#
// ds:def id=probe-…`. The block is the file's own lines.
func TestRemoteSymbolInATrailingCarrierFormat(t *testing.T) {
	t.Parallel()
	src := []byte("version: '3'\ntasks:\n  deploy:\n    desc: x\n")
	// The default config tier strips the directive itself.
	b, err := newSys(t, fstest.MapFS{}).symbolBlock("Taskfile.yml", src, "tasks.deploy")
	if err != nil || strings.Contains(b.Content, "ds:def") {
		t.Errorf("default tier block = %+v %v", b, err)
	}
	// A tier that keeps the directive in the bound text, as the YAML tier
	// does, gets the file's own lines instead.
	s := newSys(t, fstest.MapFS{}, WithExtractor(keepsDirective{}))
	b, err = s.symbolBlock("Taskfile.yml", src, "tasks.deploy")
	if err != nil || b.Content != "  deploy:\n    desc: x" || b.Pos.Start != 3 || b.Pos.End != 4 {
		t.Errorf("block = %+v %v", b, err)
	}
}

// keepsDirective binds the line holding a trailing `# ds:def` and the one
// below it, keeping the directive in the content as the YAML tier does.
type keepsDirective struct{}

func (keepsDirective) Name() string        { return "keeps" }
func (keepsDirective) Match(p string) bool { return strings.HasSuffix(p, ".yml") }
func (keepsDirective) Extract(p string, src []byte, prefix string) extract.Found {
	lines := strings.Split(string(src), "\n")
	for i, l := range lines {
		if at := strings.Index(l, "# "+prefix+":def id="); at >= 0 && i+1 < len(lines) {
			id := strings.Fields(l[at+len("# "+prefix+":def id="):])[0]
			var b block.Block
			b.ID, b.Kind, b.Symbol = id, block.KindKey, "tasks.deploy"
			b.Pos = block.Position{File: p, Start: i + 1, End: i + 2}
			b.SetContent(l + "\n" + lines[i+1])
			return extract.Found{Defs: []extract.Def{{Block: b}}}
		}
	}
	return extract.Found{}
}

// TestSymbolBlockRefusals: what symbolBlock says when there is nothing to
// bind -- no name, an unknown name, no tier for the file, a tier that cannot
// carry a directive there.
func TestSymbolBlockRefusals(t *testing.T) {
	t.Parallel()
	s := newSys(t, fstest.MapFS{})
	src := []byte("package p\n\nfunc F() {}\n")
	if _, err := s.symbolBlock("p.go", src, ""); !errors.Is(err, extract.ErrBadTarget) {
		t.Errorf("no name = %v", err)
	}
	if _, err := s.symbolBlock("p.go", src, "Missing"); !errors.Is(err, extract.ErrSymbolNotFound) {
		t.Errorf("unknown name = %v", err)
	}
	empty := newSys(t, fstest.MapFS{}, WithRegistry(extract.NewRegistry()))
	if _, err := empty.symbolBlock("p.go", src, "F"); err == nil {
		t.Error("no tier for the file must be an error")
	}
	// A JSON file has no comment to carry a directive, so nothing can be
	// written above the key and the tier binds no block there.
	if _, err := s.symbolBlock("c.json", []byte("{\n  \"port\": 1\n}\n"), "port"); !errors.Is(err, extract.ErrSymbolNotFound) {
		t.Errorf("no carrier = %v", err)
	}
	// A tier that finds the declaration by line but binds no block there (a
	// third-party tier that ignores directives) is refused, not guessed at.
	blind := newSys(t, fstest.MapFS{}, WithRegistry(extract.NewRegistry(bindsNothing{})))
	if _, err := blind.symbolBlock("p.go", src, "F"); !errors.Is(err, extract.ErrSymbolNotFound) || !strings.Contains(err.Error(), "binds no block") {
		t.Errorf("a tier that binds nothing = %v", err)
	}
}

// bindsNothing claims every file and extracts nothing.
type bindsNothing struct{}

func (bindsNothing) Name() string                                 { return "blind" }
func (bindsNothing) Match(string) bool                            { return true }
func (bindsNothing) Extract(string, []byte, string) extract.Found { return extract.Found{} }
