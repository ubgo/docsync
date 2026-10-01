package scan

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/internal/glob"
)

// ds:cfg on a line of its own is a problem at scan, where check sees it,
// because render refuses it and would publish the page without its value
// (bug 67). The link form in a sentence is not.
func TestCfgBlockFormIsAProblem(t *testing.T) {
	t.Parallel()
	doc := "Grace is [14](ds:cfg?id=g-k7m2p4xq) days.\n\n<!-- ds:cfg id=g-k7m2p4xq -->\n"
	fsys := fstest.MapFS{
		"a.go":    &fstest.MapFile{Data: []byte("package p\n\n// ds:def id=g-k7m2p4xq\nconst G = 14\n")},
		"docs.md": &fstest.MapFile{Data: []byte(doc)},
	}
	inc, _ := glob.CompileAll([]string{"**"})
	res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 1 || !errors.Is(res.Problems[0].Err, ErrCfgBlockForm) || res.Problems[0].Pos.File != "docs.md" || res.Problems[0].Pos.Start != 3 {
		t.Fatalf("problems = %+v", res.Problems)
	}
	if len(res.Refs) != 2 {
		t.Errorf("both citations are still recorded: %+v", res.Refs)
	}
}
