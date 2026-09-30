package docsync

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/config"
)

// TestGeneratedPathsRefuseMinting pins Part VII's "Generated paths refuse
// minting". A def written into a generated file is erased by the next
// generation while every sentence citing it stays, so Define and Adopt refuse
// to mint one there -- naming the file and the way round it -- and write
// nothing. An id already in such a file is still returned: reading writes
// nothing, and scan reports it (scan.ErrDefInGenerated).
// promise:generated-no-mint
func TestGeneratedPathsRefuseMinting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	gen := "package g\n\nfunc Gen() int { return 1 }\n"
	fsys := fstest.MapFS{
		"gen/a.go":    &fstest.MapFile{Data: []byte(gen)},
		"gen/b.go":    &fstest.MapFile{Data: []byte("package g\n\n// ds:def id=old-k7m2p4xq\nfunc Old() int { return 1 }\n")},
		"src/c.go":    &fstest.MapFile{Data: []byte(gen)},
		"docs/old.md": &fstest.MapFile{Data: []byte("See [Gen](../gen/a.go#Gen) and [src](../src/c.go#Gen).\n")},
	}
	c := config.Default()
	c.Scan.Code = []string{"**"}
	c.Scan.Docs = []string{"docs/**"}
	c.Scan.Generated = []string{"gen/**"}
	s, err := New(WithFS(fsys), WithConfig(c), WithRepo("r"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.Define(ctx, "gen/a.go#Gen", DefineOptions{})
	if !errors.Is(err, ErrGeneratedPath) || !strings.Contains(err.Error(), "gen/a.go") {
		t.Errorf("minting in a generated file: %v, want ErrGeneratedPath naming it", err)
	}
	// Outside the generated glob the same block mints.
	if res, err := s.Define(ctx, "src/c.go#Gen", DefineOptions{}); err != nil || res.Existing || res.Edit.File != "src/c.go" {
		t.Errorf("minting outside it: %+v %v", res, err)
	}
	// An id already there is read, not minted.
	if res, err := s.Define(ctx, "gen/b.go#Old", DefineOptions{}); err != nil || !res.Existing || res.ID != "old-k7m2p4xq" {
		t.Errorf("existing id in a generated file: %+v %v", res, err)
	}

	res, err := s.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ad, err := s.Adopt(ctx, res)
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, u := range ad.Unresolved {
		reasons = append(reasons, u.Reason)
	}
	if ad.Adopted != 1 || len(ad.Unresolved) != 1 || !strings.Contains(reasons[0], "generated") {
		t.Errorf("adopt = %d adopted, unresolved %v; want the src link adopted and the gen link refused", ad.Adopted, reasons)
	}
	for _, e := range ad.Edits {
		if strings.HasPrefix(e.File, "gen/") {
			t.Errorf("adopt wrote into a generated file: %+v", e)
		}
	}

	// A malformed generated glob is an error, not a silent "not generated".
	c.Scan.Generated = []string{"["}
	bad, err := New(WithFS(fsys), WithConfig(c), WithRepo("r"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Define(ctx, "src/c.go#Gen", DefineOptions{}); err == nil {
		t.Error("a malformed generated glob must be an error")
	}
}
