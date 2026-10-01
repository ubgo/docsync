package match

import (
	"reflect"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/ledger"
)

func mk(id, file string, start, end int, symbol, content string, args map[string]string) block.Block {
	b := block.Block{ID: id, Kind: block.KindFunc, Symbol: symbol, Pos: block.Position{File: file, Start: start, End: end}, Args: args}
	b.SetContent(content)
	return b
}

func kind(b block.Block, k block.Kind) block.Block { b.Kind = k; return b }

func row(b block.Block) ledger.Row { return ledger.FromBlock("api", b) }

func TestCompareStates(t *testing.T) {
	t.Parallel()
	same := mk("same-aaaaaaaa", "a.go", 1, 3, "F", "func F() {}", nil)
	movedOld := mk("moved-bbbbbbbb", "a.go", 10, 12, "G", "func G() {}", nil)
	movedNew := mk("moved-bbbbbbbb", "b.go", 20, 22, "G", "func G() {}", nil)
	changedOld := mk("chg-cccccccc", "a.go", 30, 32, "H", "func H() {\n\treturn 1\n}", nil)
	changedNew := mk("chg-cccccccc", "a.go", 30, 32, "H", "func H() {\n\treturn 2\n}", nil)
	gone := mk("gone-dddddddd", "a.go", 40, 41, "Old", "func Old() {}", nil)
	remarked := mk("re-eeeeeeee", "c.go", 5, 6, "Old", "func Old() {}", nil) // same body as gone, new id
	rewrittenOld := mk("rw-ffffffff", "a.go", 50, 55, "Rw", "line1\nline2\nline3\nline4\nline5", nil)
	rewrittenNew := mk("rwnew-gggggggg", "a.go", 50, 55, "Rw2", "line1\nline2\nline3\nline4\nchanged", nil)
	deleted := mk("del-hhhhhhhh", "a.go", 60, 61, "Del", "totally unique body", nil)
	brandNew := mk("new-iiiiiiii", "d.go", 1, 1, "N", "func N() {}", nil)

	prev := []ledger.Row{row(same), row(movedOld), row(changedOld), row(gone), row(rewrittenOld), row(deleted)}
	cur := []block.Block{same, movedNew, changedNew, remarked, rewrittenNew, brandNew}
	oldBodies := map[string]string{
		changedOld.ID:   changedOld.Content,
		rewrittenOld.ID: rewrittenOld.Content,
		deleted.ID:      deleted.Content,
	}
	changes := Compare(prev, cur, Options{OldContent: func(row ledger.Row) (string, bool) { c, ok := oldBodies[row.ID]; return c, ok }})

	got := map[string]Change{}
	for _, c := range changes {
		got[c.ID] = c
	}
	want := map[string]State{
		"same-aaaaaaaa": StateOK, "moved-bbbbbbbb": StateMoved, "chg-cccccccc": StateChanged,
		"gone-dddddddd": StateMovedUnmarked, "rw-ffffffff": StateRewritten, "del-hhhhhhhh": StateDeleted,
		"re-eeeeeeee": StateNew, "rwnew-gggggggg": StateNew, "new-iiiiiiii": StateNew,
	}
	if len(changes) != len(want) {
		t.Fatalf("changes = %d, want %d", len(changes), len(want))
	}
	for id, st := range want {
		if got[id].State != st {
			t.Errorf("%s = %s, want %s", id, got[id].State, st)
		}
	}
	if c := got["chg-cccccccc"]; !reflect.DeepEqual(c.Classes, []block.Class{block.ClassBody}) || c.Diff == "" || !c.Flags {
		t.Errorf("changed = %+v", c)
	}
	if c := got["gone-dddddddd"]; c.Candidate.ID != "re-eeeeeeee" {
		t.Errorf("moved-unmarked candidate = %+v", c.Candidate)
	}
	if c := got["rw-ffffffff"]; c.Candidate.ID != "rwnew-gggggggg" || c.Ratio < 0.8 {
		t.Errorf("rewritten = %+v", c)
	}
	if c := got["moved-bbbbbbbb"]; c.Flags || c.Diff != "" || c.Old.File != "a.go" || c.New.Pos.File != "b.go" {
		t.Errorf("moved = %+v", c)
	}
	// Sorted by id.
	for i := 1; i < len(changes); i++ {
		if changes[i-1].ID > changes[i].ID {
			t.Fatal("not sorted")
		}
	}
}

func TestCompareWithoutOldContent(t *testing.T) {
	t.Parallel()
	old := mk("x-aaaaaaaa", "a.go", 1, 2, "X", "v1", nil)
	nw := mk("x-aaaaaaaa", "a.go", 1, 2, "X", "v2", nil)
	gone := mk("g-bbbbbbbb", "a.go", 5, 6, "G", "unique", nil)
	changes := Compare([]ledger.Row{row(old), row(gone)}, []block.Block{nw}, Options{})
	if changes[0].State != StateDeleted {
		t.Errorf("without old content a vanished id with no hash match is deleted: %+v", changes[0])
	}
	// Unknown, never body: a body nobody could read may hide a signature
	// change, and `api` does not flag body (bug 28).
	if c := changes[1]; c.State != StateChanged || c.Diff != "" || !reflect.DeepEqual(c.Classes, []block.Class{block.ClassUnknown}) || !c.Flags {
		t.Errorf("changed without old content = %+v", c)
	}
	// OldContent present but returns false for the id.
	changes = Compare([]ledger.Row{row(old)}, []block.Block{nw}, Options{OldContent: func(ledger.Row) (string, bool) { return "", false }})
	if changes[0].Diff != "" {
		t.Error("no diff when old content is unknown")
	}
}

func TestCompareCustomClassifierAndThreshold(t *testing.T) {
	t.Parallel()
	old := mk("x-aaaaaaaa", "a.go", 1, 2, "X", "v1", map[string]string{"stability": "api"})
	nw := mk("x-aaaaaaaa", "a.go", 1, 2, "X", "v2", map[string]string{"stability": "api"})
	custom := func(o, n block.Block, oc string) []block.Class { return []block.Class{block.ClassSignature} }
	have := func(ledger.Row) (string, bool) { return old.Content, true }
	c := Compare([]ledger.Row{row(old)}, []block.Block{nw}, Options{Classify: custom, OldContent: have})[0]
	if !reflect.DeepEqual(c.Classes, []block.Class{block.ClassSignature}) || !c.Flags {
		t.Errorf("custom classifier = %+v", c)
	}
	// Under `api`, a body-only change does not flag.
	c = Compare([]ledger.Row{row(old)}, []block.Block{nw}, Options{OldContent: have})[0]
	if c.Flags {
		t.Error("api stability must not flag a body change")
	}
	// Without the old body a custom classifier is not asked to describe an
	// empty block, and the change is unknown, which `api` flags (bug 28).
	c = Compare([]ledger.Row{row(old)}, []block.Block{nw}, Options{Classify: custom})[0]
	if !reflect.DeepEqual(c.Classes, []block.Class{block.ClassUnknown}) || !c.Flags {
		t.Errorf("no old body under api = %+v", c)
	}
	// A high threshold turns a rewrite into a deletion.
	rwOld := mk("rw-ffffffff", "a.go", 1, 5, "Rw", "a\nb\nc\nd\ne", nil)
	rwNew := mk("rwnew-gggggggg", "a.go", 1, 5, "Rw", "a\nb\nc\nd\nX", nil)
	oc := func(ledger.Row) (string, bool) { return rwOld.Content, true }
	if c := Compare([]ledger.Row{row(rwOld)}, []block.Block{rwNew}, Options{OldContent: oc, FuzzyThreshold: 0.99})[0]; c.State != StateDeleted {
		t.Errorf("high threshold = %s", c.State)
	}
	// Zero similarity is never a rewrite even with a tiny threshold.
	rwNone := mk("z-zzzzzzzz", "a.go", 1, 1, "Z", "nothing alike", nil)
	if c := Compare([]ledger.Row{row(rwOld)}, []block.Block{rwNone}, Options{OldContent: oc, FuzzyThreshold: 0.01})[0]; c.State != StateDeleted {
		t.Errorf("zero ratio = %s", c.State)
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()
	prefixes := []string{"//"}
	base := mk("x-aaaaaaaa", "a.go", 1, 3, "F", "// doc\nfunc F() {\n\treturn 1\n}", nil)
	for _, tc := range []struct {
		name string
		old  block.Block
		nw   block.Block
		oc   string
		want []block.Class
	}{
		{"identical", base, base, base.Content, nil},
		{"renamed only, same hash", base, mk("x-aaaaaaaa", "a.go", 1, 3, "G", base.Content, nil), base.Content, []block.Class{block.ClassRenamed}},
		{"moved only, same hash", base, mk("x-aaaaaaaa", "b.go", 9, 11, "F", base.Content, nil), base.Content, []block.Class{block.ClassMoved}},
		{"comment only", base, mk("x-aaaaaaaa", "a.go", 1, 3, "F", "// better doc\nfunc F() {\n\treturn 1\n}", nil), base.Content, []block.Class{block.ClassComment}},
		{"body", base, mk("x-aaaaaaaa", "a.go", 1, 3, "F", "// doc\nfunc F() {\n\treturn 2\n}", nil), base.Content, []block.Class{block.ClassBody}},
		{"without old content it is unknown, never body (bug 28)", base, mk("x-aaaaaaaa", "a.go", 1, 3, "F", "// better doc\nfunc F() {\n\treturn 1\n}", nil), "", []block.Class{block.ClassUnknown}},
		{"const value (bug 27)", kind(mk("c", "a.go", 1, 1, "MaxRetries", "const MaxRetries = 5", nil), block.KindConst), kind(mk("c", "a.go", 1, 1, "MaxRetries", "const MaxRetries = 3", nil), block.KindConst), "const MaxRetries = 5", []block.Class{block.ClassValue}},
		{"grouped const value with a doc comment (bug 27)", kind(mk("c", "a.go", 1, 2, "MaxRetries", "// retries\nMaxRetries = 5", nil), block.KindConst), kind(mk("c", "a.go", 1, 2, "MaxRetries", "// retries\nMaxRetries   =  3", nil), block.KindConst), "// retries\nMaxRetries = 5", []block.Class{block.ClassValue}},
		{"const type and value", kind(mk("c", "a.go", 1, 1, "X", "var X int = 3", nil), block.KindConst), kind(mk("c", "a.go", 1, 1, "X", "var X int64 = 4", nil), block.KindConst), "var X int = 3", []block.Class{block.ClassType, block.ClassValue}},
		{"a literal const is bound to its literal: value (bug 27)", kind(mk("c", "a.go", 4, 4, "MaxRetries", "5", nil), block.KindConst), kind(mk("c", "a.go", 4, 4, "MaxRetries", "3", nil), block.KindConst), "5", []block.Class{block.ClassValue}},
		{"a literal becoming an expression is a value change", kind(mk("c", "a.go", 4, 4, "Every", "60", nil), block.KindConst), kind(mk("c", "a.go", 4, 4, "Every", "const Every = time.Hour", nil), block.KindConst), "60", []block.Class{block.ClassValue}},
		{"a string literal spelling the symbol is a value", kind(mk("c", "a.go", 4, 4, "Name", `"Name"`, nil), block.KindConst), kind(mk("c", "a.go", 4, 4, "Name", `"Other"`, nil), block.KindConst), `"Name"`, []block.Class{block.ClassValue}},
		{"a symbol-less literal is a value", kind(mk("c", "a.go", 4, 4, "", "1", nil), block.KindConst), kind(mk("c", "a.go", 4, 4, "", "2", nil), block.KindConst), "1", []block.Class{block.ClassValue}},
		{"a held member's head is matched by its last segment", kind(mk("c", "a.go", 4, 4, "Limits.Max", "Max int", nil), block.KindConst), kind(mk("c", "a.go", 4, 4, "Limits.Max", "Max int64", nil), block.KindConst), "Max int", []block.Class{block.ClassType}},
		{"const with no value changes type", kind(mk("c", "a.go", 1, 1, "X", "var X int", nil), block.KindConst), kind(mk("c", "a.go", 1, 1, "X", "var X int64", nil), block.KindConst), "var X int", []block.Class{block.ClassType}},
		{"const renamed keeps its value", kind(mk("c", "a.go", 1, 1, "X", "const X = 3", nil), block.KindConst), kind(mk("c", "a.go", 1, 1, "Y", "const Y = 3", nil), block.KindConst), "const X = 3", []block.Class{block.ClassRenamed}},
		{"const renamed and revalued", kind(mk("c", "a.go", 1, 1, "X", "const X = 3", nil), block.KindConst), kind(mk("c", "a.go", 1, 1, "Y", "const Y = 4", nil), block.KindConst), "const X = 3", []block.Class{block.ClassRenamed, block.ClassValue}},
		{"const differing only in collapsed whitespace is body", kind(mk("c", "a.go", 1, 1, "X", "const X = a b", nil), block.KindConst), kind(mk("c", "a.go", 1, 1, "X", "const X = a  b", nil), block.KindConst), "const X = a b", []block.Class{block.ClassBody}},
		{"multi-line const initializer is body", kind(mk("c", "a.go", 1, 3, "X", "var X = []int{\n1,\n}", nil), block.KindConst), kind(mk("c", "a.go", 1, 3, "X", "var X = []int{\n2,\n}", nil), block.KindConst), "var X = []int{\n1,\n}", []block.Class{block.ClassBody}},
		{"multi-line new const initializer is body", kind(mk("c", "a.go", 1, 1, "X", "var X = 1", nil), block.KindConst), kind(mk("c", "a.go", 1, 3, "X", "var X = []int{\n2,\n}", nil), block.KindConst), "var X = 1", []block.Class{block.ClassMoved, block.ClassBody}},
		{"comment-only const old side is body", kind(mk("c", "a.go", 1, 1, "X", "// x", nil), block.KindConst), kind(mk("c", "a.go", 1, 1, "X", "var X = 1", nil), block.KindConst), "// x", []block.Class{block.ClassBody}},
		{"renamed and moved and body", base, mk("x-aaaaaaaa", "b.go", 1, 3, "G", "other", nil), base.Content, []block.Class{block.ClassRenamed, block.ClassMoved, block.ClassBody}},
		{"signature only", base, mk("x-aaaaaaaa", "a.go", 1, 3, "F", "// doc\nfunc F(a int) {\n\treturn 1\n}", nil), base.Content, []block.Class{block.ClassSignature}},
		{"signature re-indented is not a change", base, mk("x-aaaaaaaa", "a.go", 1, 3, "F", "// doc\n  func   F()  {\n\treturn 2\n}", nil), base.Content, []block.Class{block.ClassBody}},
		{"signature and body", base, mk("x-aaaaaaaa", "a.go", 1, 3, "F", "// doc\nfunc F(a int) {\n\treturn 2\n}", nil), base.Content, []block.Class{block.ClassSignature, block.ClassBody}},
		{"type shape", kind(mk("t", "a.go", 1, 1, "T", "type T struct{ A int }", nil), block.KindType), kind(mk("t", "a.go", 1, 1, "T", "type T struct{ A, B int }", nil), block.KindType), "type T struct{ A int }", []block.Class{block.ClassType}},
		{"one-line func signature with no rest", mk("f", "a.go", 1, 1, "F", "func F() {}", nil), mk("f", "a.go", 1, 1, "F", "func F(a int) {}", nil), "func F() {}", []block.Class{block.ClassSignature}},
		{"comment-only old content has no declaration", mk("f", "a.go", 1, 1, "F", "// only", nil), mk("f", "a.go", 1, 1, "F", "// only\nfunc F() {}", nil), "// only", []block.Class{block.ClassSignature}},
		{"statement first line is body", kind(mk("s", "q.sql", 1, 2, "", "DELETE FROM a\nWHERE x;", nil), block.KindStatement), kind(mk("s", "q.sql", 1, 2, "", "DELETE FROM b\nWHERE x;", nil), block.KindStatement), "DELETE FROM a\nWHERE x;", []block.Class{block.ClassBody}},
		{"empty symbols never count as renamed", kind(mk("x", "a.go", 1, 1, "", "a", nil), block.KindStatement), kind(mk("x", "a.go", 1, 1, "F", "b", nil), block.KindStatement), "a", []block.Class{block.ClassBody}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Classify(tc.old, tc.nw, tc.oc, prefixes)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Classify = %v, want %v", got, tc.want)
			}
		})
	}
	// Value kinds classify as value.
	oldKey := block.Block{ID: "k", Kind: block.KindKey, Pos: block.Position{File: "c.yaml", Start: 3, End: 3}}
	oldKey.SetContent("port: 8081")
	newKey := oldKey
	newKey.SetContent("port: 8443")
	if got := Classify(oldKey, newKey, oldKey.Content, prefixes); !reflect.DeepEqual(got, []block.Class{block.ClassValue}) {
		t.Errorf("value kind = %v", got)
	}
	// No comment prefixes means comment detection is off.
	if got := Classify(base, mk("x-aaaaaaaa", "a.go", 1, 3, "F", "// better doc\nfunc F() {\n\treturn 1\n}", nil), base.Content, nil); !reflect.DeepEqual(got, []block.Class{block.ClassBody}) {
		t.Errorf("no prefixes = %v", got)
	}
	if commentOnly("a", "a", prefixes) {
		t.Error("identical content is not a comment-only change")
	}
	if len(StateValues) != 7 {
		t.Error("StateValues")
	}
}

func TestCompareUsesCommentPrefixes(t *testing.T) {
	t.Parallel()
	old := mk("x-aaaaaaaa", "a.py", 1, 2, "f", "# old\ndef f(): pass", nil)
	nw := mk("x-aaaaaaaa", "a.py", 1, 2, "f", "# new\ndef f(): pass", nil)
	opts := Options{
		OldContent:      func(ledger.Row) (string, bool) { return old.Content, true },
		CommentPrefixes: func(file string) []string { return []string{"#"} },
	}
	c := Compare([]ledger.Row{row(old)}, []block.Block{nw}, opts)[0]
	if !reflect.DeepEqual(c.Classes, []block.Class{block.ClassComment}) || c.Flags {
		t.Errorf("comment-only under stable must not flag: %+v", c)
	}
}

// TestSecretChangeHasNoDiff is where the leak was first seen: a secret changed
// since its last scan came back from `ds check --json` as
// "-api_key: sk-…old / +sk-…new". A diff is the one place the match step turns
// bodies into text a person or a CI log reads, so no secret or local block may
// have one, whatever an OldContent hook returns.
// promise:secret-never-shown
func TestSecretChangeHasNoDiff(t *testing.T) {
	t.Parallel()
	mk := func(content string, args map[string]string) block.Block {
		a := map[string]string{"id": "key-k7m2p4xq"}
		for k, v := range args {
			a[k] = v
		}
		b := block.Block{ID: "key-k7m2p4xq", Kind: block.KindKey, Pos: block.Position{File: "a.yaml", Start: 1, End: 1}, Args: a}
		b.SetContent(content)
		return b
	}
	asked := 0
	leaky := func(ledger.Row) (string, bool) { asked++; return "sk-live-OLD", true }
	for name, tc := range map[string]struct {
		old, nw block.Block
	}{
		"a secret":    {mk("sk-live-OLD", map[string]string{"secret": "true"}), mk("sk-live-NEW", map[string]string{"secret": "true"})},
		"a local def": {mk("x", map[string]string{"local": "true"}), mk("y", map[string]string{"local": "true"})},
		// A def that became secret since its last scan has a clean previous
		// row; its current content is the value, so the new side counts too.
		"became secret": {mk("sk-live-OLD", nil), mk("sk-live-NEW", map[string]string{"secret": "true"})},
	} {
		t.Run(name, func(t *testing.T) {
			cs := Compare([]ledger.Row{ledger.FromBlock("r", tc.old)}, []block.Block{tc.nw}, Options{OldContent: leaky})
			if len(cs) != 1 || cs[0].State != StateChanged {
				t.Fatalf("changes = %+v", cs)
			}
			if cs[0].Diff != "" {
				t.Errorf("a withheld block got a diff: %q", cs[0].Diff)
			}
		})
	}
	// A secret's old body is not even fetched: a hook may log, cache or send
	// what it reads, so the safest body is the one never asked for.
	asked = 0
	Compare([]ledger.Row{ledger.FromBlock("r", mk("a", map[string]string{"secret": "true"}))}, []block.Block{mk("b", map[string]string{"secret": "true"})}, Options{OldContent: leaky})
	if asked != 0 {
		t.Errorf("the hook was asked for a secret's body %d times", asked)
	}
	// An ordinary block still gets its diff.
	cs := Compare([]ledger.Row{ledger.FromBlock("r", mk("443", nil))}, []block.Block{mk("8443", nil)}, Options{OldContent: func(ledger.Row) (string, bool) { return "443", true }})
	if cs[0].Diff == "" {
		t.Error("an ordinary change lost its diff")
	}
}
