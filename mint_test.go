package docsync

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/block"
)

// TestWritersMintTheSameIDsTwice pins bug 33: `ds adopt --dry-run` printed
// one set of ids and `ds adopt` wrote another, because every writer drew its
// suffix at random, so a preview could not be checked against its run. Each
// writer -- adopt, def, def --fix -- now derives the suffix from what it
// binds, so two runs over the same tree propose the same edits, byte for
// byte, while two different blocks still get different ids.
func TestWritersMintTheSameIDsTwice(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	fsys["docs/legacy.md"] = &fstest.MapFile{Data: []byte("See [Persist](internal/store/write.go#Persist) and [notes](notes.txt#L2).\n")}
	fsys["internal/dup.go"] = &fstest.MapFile{Data: []byte("package x\n\n// ds:def id=twice-k7m2p4xq\nfunc A() {}\n\n// ds:def id=twice-k7m2p4xq\nfunc B() {}\n\n// ds:def id=twice-k7m2p4xq\nfunc C() {}\n")}
	ctx := context.Background()
	run := func() (AdoptResult, DefineResult, DefineResult, RenameResult) {
		s := newSys(t, fsys)
		res, err := s.Scan(ctx)
		if err != nil {
			t.Fatal(err)
		}
		a, err := s.Adopt(ctx, res)
		if err != nil {
			t.Fatal(err)
		}
		d, err := s.Define(ctx, "internal/store/write.go#Persist", DefineOptions{})
		if err != nil {
			t.Fatal(err)
		}
		relabelled, err := s.Define(ctx, "internal/store/write.go#Persist", DefineOptions{Label: "other"})
		if err != nil {
			t.Fatal(err)
		}
		f, err := s.FixDuplicates(res)
		if err != nil {
			t.Fatal(err)
		}
		return a, d, relabelled, f
	}
	a1, d1, r1, f1 := run()
	a2, d2, r2, f2 := run()
	if len(a1.Edits) == 0 || !reflect.DeepEqual(a1.Edits, a2.Edits) {
		t.Errorf("adopt proposed different edits:\n%+v\n%+v", a1.Edits, a2.Edits)
	}
	if d1.ID == "" || d1.ID != d2.ID || d1.Edit != d2.Edit || r1.ID != r2.ID {
		t.Errorf("def minted %q then %q", d1.ID, d2.ID)
	}
	// The label is not part of the identity, so relabelling keeps the suffix.
	if d1.ID[len(d1.ID)-8:] != r1.ID[len(r1.ID)-8:] || r1.ID[:6] != "other-" {
		t.Errorf("relabelled %q against %q", r1.ID, d1.ID)
	}
	if len(f1.Mapping) != 2 || !reflect.DeepEqual(f1.Mapping, f2.Mapping) {
		t.Errorf("def --fix re-minted differently:\n%v\n%v", f1.Mapping, f2.Mapping)
	}
	seen := map[string]bool{}
	for _, v := range f1.Mapping {
		if seen[v] {
			t.Errorf("two blocks share a re-minted id: %v", f1.Mapping)
		}
		seen[v] = true
	}
}

// TestFindOrdersByPlace pins bug 32: find listed defs by id, so two defs with
// one label came out in the order of their random suffixes, which differs in
// every checkout that minted its own. They are listed by place.
func TestFindOrdersByPlace(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	// The suffixes sort opposite to the lines.
	fsys["internal/limit.go"] = &fstest.MapFile{Data: []byte("package x\n\n// ds:def id=limit-zzzzzzzz\nfunc A() {}\n\n// ds:def id=limit-aaaaaaaa\nfunc B() {}\n")}
	s := newSys(t, fsys)
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][]block.Block{"Find": s.Find(res, "limit"), "FindBy": s.FindBy(res, FindOptions{Query: "limit"})} {
		if len(got) != 2 || got[0].ID != "limit-zzzzzzzz" || got[1].ID != "limit-aaaaaaaa" {
			t.Errorf("%s = %+v", name, got)
		}
	}
	// Two defs at one place, as two published repos can have, fall back to id.
	same := []block.Block{{ID: "b", Pos: block.Position{File: "f", Start: 1}}, {ID: "a", Pos: block.Position{File: "f", Start: 1}}}
	if got := byPlace(same); got[0].ID != "a" {
		t.Errorf("tie = %+v", got)
	}
}

// TestMapOrdersTiesByPlace pins the map half of bug 32: defs tied on state
// and citers were listed by id, so in the order of their random suffixes.
// They are listed by file, then line.
func TestMapOrdersTiesByPlace(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"internal/a/x.go": {Data: []byte("package a\n\n// ds:def id=limit-zzzzzzzz\nfunc A() {}\n\n// ds:def id=limit-yyyyyyyy\nfunc B() {}\n")},
		"internal/b/x.go": {Data: []byte("package b\n\n// ds:def id=limit-aaaaaaaa\nfunc C() {}\n")},
	}
	s := newSys(t, fsys)
	m, err := s.Map(context.Background(), MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range m.Defs {
		got = append(got, d.ID)
	}
	if strings.Join(got, " ") != "limit-zzzzzzzz limit-yyyyyyyy limit-aaaaaaaa" {
		t.Errorf("map defs = %v", got)
	}
}
