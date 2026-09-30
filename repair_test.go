package docsync

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/config"
)

// TestRepair covers the damage an older `ds def` left behind: a directive
// written as a bare line into a file that cannot hold one. Where the format
// has comments the line is commented in place, keeping the def and so every
// citation of it; where it has none there is nothing to comment into, and the
// line is deleted -- reversibly, since the edit carries the line after it.
func TestRepair(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		// Damage, fixable by commenting: the case that broke a real workspace.
		"go.work": &fstest.MapFile{Data: []byte("ds:def id=w-t4k2b9rf\ngo 1.26\n\nuse .\n")},
		// A directive continued onto a second line: both lines need commenting,
		// or the continuation is left bare and the file still does not parse.
		"a.pkl": &fstest.MapFile{Data: []byte("ds:def id=p-h3v8n2wd\n  owner=@team\nname = \"x\"\n")},
		// A block-comment-only format.
		"s.css": &fstest.MapFile{Data: []byte("  ds:def id=c-b3c7g9kl\nbody { color: red; }\n")},
		// Damage no edit can fix: JSON has no comment syntax.
		"p.json": &fstest.MapFile{Data: []byte("ds:def id=j-k7m2p4xq\n{\"a\": 1}\n")},
		// Not damage: plain text carries a directive as a bare line.
		"notes.txt": &fstest.MapFile{Data: []byte("ds:def id=n-w8n4r6vc\nprose\n")},
		// Already fine: a commented directive needs nothing.
		"go.mod": &fstest.MapFile{Data: []byte("// ds:def id=m-p2c4y7mk\nmodule example.com/m\n")},
	}
	c := config.Default()
	c.Scan.Code = []string{"**"}
	c.Scan.Docs = []string{"docs/**"}
	s, err := New(WithFS(fsys), WithConfig(c), WithRepo("r"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := s.Repair(res)
	got := map[string]string{}
	for _, e := range r.Edits {
		got[e.File+":"+string(rune('0'+e.Line))] = e.New
		// The edit replaces exactly what it read, so applying it to a file
		// edited in the meantime is refused rather than guessed at.
		if e.Old == "" {
			t.Errorf("%s:%d: a repair must replace, never insert", e.File, e.Line)
		}
	}
	for key, want := range map[string]string{
		"go.work:1": "// ds:def id=w-t4k2b9rf",
		"a.pkl:1":   "// ds:def id=p-h3v8n2wd",
		"a.pkl:2":   "  // owner=@team",
		"s.css:1":   "  /* ds:def id=c-b3c7g9kl */",
	} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
	// Four commented lines and one deletion.
	if len(r.Edits) != 5 {
		t.Errorf("edits = %+v, want the four commented lines and the JSON deletion", r.Edits)
	}
	// Plain text and an already-commented directive are left entirely alone.
	for _, e := range r.Edits {
		if e.File == "notes.txt" || e.File == "go.mod" {
			t.Errorf("repaired something that was not damage: %+v", e)
		}
	}
	// JSON has no comment syntax, so its line is deleted, and the deletion
	// records the line after it so undo can put it back.
	var del *Edit
	for i := range r.Edits {
		if r.Edits[i].File == "p.json" {
			del = &r.Edits[i]
		}
	}
	if del == nil || !del.Delete || del.Line != 1 || del.Old != "ds:def id=j-k7m2p4xq" || del.Next != `{"a": 1}` || del.New != "" {
		t.Errorf("json deletion = %+v", del)
	}
	if len(r.Manual) != 0 {
		t.Errorf("manual = %+v, want nothing left for a person", r.Manual)
	}
	// Applying the edits makes the scan clean of the damage it can fix, and
	// keeps every def: a repair that dropped a def would break its citations.
	after := fstest.MapFS{}
	for k, v := range fsys {
		after[k] = v
	}
	for _, e := range r.Edits {
		src := after[e.File].Data
		out, err := e.Apply(src)
		if err != nil {
			t.Fatal(err)
		}
		after[e.File] = &fstest.MapFile{Data: out}
	}
	s2, _ := New(WithFS(after), WithConfig(c), WithRepo("r"))
	res2, _ := s2.Scan(context.Background())
	// Every commented def survives; only the JSON one, which was never valid
	// there, is gone.
	if len(res2.Defs) != len(res.Defs)-1 {
		t.Errorf("defs %d -> %d: a repair may drop only the def it deleted", len(res.Defs), len(res2.Defs))
	}
	for _, p := range res2.Problems {
		t.Errorf("damage left after repair: %s:%d %v", p.Pos.File, p.Pos.Start, p.Err)
	}
	// Run twice: nothing left to do the second time.
	r2 := s2.Repair(res2)
	if len(r2.Edits) != 0 {
		t.Errorf("a second repair proposed %d edits; it must be idempotent", len(r2.Edits))
	}
}

// TestRepairEdges covers the paths the main case does not: several damaged
// directives in one file are proposed top to bottom, so applying them in order
// never shifts a later one; a file the scan saw but can no longer read is an
// error rather than a silently empty repair; and a format the scan cannot judge
// is left alone.
func TestRepairEdges(t *testing.T) {
	t.Parallel()
	c := config.Default()
	c.Scan.Code = []string{"**"}
	c.Scan.Docs = []string{"docs/**"}
	two := fstest.MapFS{"go.work": &fstest.MapFile{Data: []byte("ds:def id=a-t4k2b9rf\ngo 1.26\n\nds:def id=b-h3v8n2wd\nuse .\n")}}
	s, _ := New(WithFS(two), WithConfig(c), WithRepo("r"))
	res, _ := s.Scan(context.Background())
	r := s.Repair(res)
	// Bottom-up, so applying them in order never shifts a line a later edit
	// points at.
	if len(r.Edits) != 2 || r.Edits[0].Line != 4 || r.Edits[1].Line != 1 {
		t.Errorf("two damaged lines in one file = %+v, want lines 4 then 1", r.Edits)
	}
	// A directive continued onto the next line, in a format that has to lose
	// both: the first line's Next must be the first line that SURVIVES, not the
	// continuation that is being deleted too, or undo could not find its place.
	cont := fstest.MapFS{"c.csv": &fstest.MapFile{Data: []byte("ds:def id=c-k7m2p4xq\n  owner=@team\na,b\n1,2\n")}}
	sc, _ := New(WithFS(cont), WithConfig(c), WithRepo("r"))
	cres, _ := sc.Scan(context.Background())
	cr := sc.Repair(cres)
	if len(cr.Edits) != 2 || cr.Edits[0].Line != 2 || cr.Edits[1].Line != 1 {
		t.Fatalf("continued directive = %+v", cr.Edits)
	}
	if cr.Edits[0].Next != "a,b" || cr.Edits[1].Next != "a,b" {
		t.Errorf("both deletions must name the first surviving line: %q, %q", cr.Edits[0].Next, cr.Edits[1].Next)
	}
	// Applying both, in order, leaves exactly the CSV.
	src := cont["c.csv"].Data
	for _, e := range cr.Edits {
		out, err := e.Apply(src)
		if err != nil {
			t.Fatalf("apply %+v: %v", e, err)
		}
		src = out
	}
	if string(src) != "a,b\n1,2\n" {
		t.Errorf("csv after repair = %q", src)
	}
	// The scan saw the file; by the time Repair reads it, it is gone. That is
	// named for a person -- neither a failure of the whole repair nor a silent
	// skip, since unreported damage is what this command exists to end.
	gone, _ := New(WithFS(fstest.MapFS{}), WithConfig(c), WithRepo("r"))
	g := gone.Repair(res)
	if len(g.Edits) != 0 || len(g.Manual) != 2 || !strings.Contains(g.Manual[0].Reason, "could not be read") {
		t.Errorf("an unreadable file = %+v, want both directives named for a person", g)
	}
	// Its one-line form locates the damage for the person it is left to.
	if s := g.Manual[0].String(); !strings.HasPrefix(s, "go.work:1  could not be read") {
		t.Errorf("manual line = %q", s)
	}
}
