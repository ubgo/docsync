package docsync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/extract"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/scan"
)

// changedSystem is the fixture after the Save body changed, with the previous
// ledger and old content wired, plus any extra options.
func changedSystem(t *testing.T, fsys fstest.MapFS, extra ...Option) (*System, Report) {
	t.Helper()
	base := repo(false)
	first := newSys(t, base)
	res, err := first.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	prev, prevRefs := first.Snapshot(res)
	old := map[string]string{}
	for _, b := range res.Defs {
		old[b.ID] = b.Content
	}
	opts := append([]Option{WithPrevious(prev, prevRefs), WithOldContent(func(row ledger.Row) (string, bool) { c, ok := old[row.ID]; return c, ok })}, extra...)
	s := newSys(t, fsys, opts...)
	rep, err := s.Check(context.Background(), CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return s, rep
}

func TestTriage(t *testing.T) {
	t.Parallel()
	fsys := repo(true)
	// A second doc cites the same changed block: same diff, one group. A
	// translation ref and a diff-less unacked each stand alone.
	fsys["docs/other.md"] = &fstest.MapFile{Data: []byte("Also [save](ds:block?id=sess-save-k7m2p4xq).\n")}
	_, rep := changedSystem(t, fsys)
	groups := Triage(rep)
	if len(groups) != 1 || len(groups[0].Findings) != 3 || groups[0].Group != 1 || !strings.Contains(groups[0].Diff, "sessions.Insert") {
		t.Fatalf("triage = %+v", groups)
	}
	// Findings without diffs are their own groups.
	rep2 := Report{Findings: []check.Finding{
		{State: check.StateUnacked, ID: "a"},
		{State: check.StateUnacked, ID: "b"},
		{State: check.StateUnacked, ID: "a", Line: 9},
		{State: check.StateOK, ID: "c"},
		{State: check.StateTranslationStale, ID: "d", Diff: "-x\n+y\n"},
		{State: check.StateUnacked, ID: "e", Diff: "-p\n+q\n-r\n+s\n"},
		{State: check.StateUnacked, ID: "a", Diff: "-x\n+y\n"},
	}}
	g := Triage(rep2)
	if len(g) != 4 || len(g[0].Findings) != 2 || g[0].Findings[1].Line != 9 || len(g[2].Findings) != 2 || g[3].Group != 4 {
		t.Errorf("diffless triage groups by id, diffs by similarity = %+v", g)
	}
	if Triage(Report{}) != nil {
		t.Error("empty")
	}
}

func TestAuditAndBlame(t *testing.T) {
	t.Parallel()
	acks := ledger.Acks{Rows: []ledger.Ack{
		{At: clock.Add(-48 * time.Hour), Actor: "k", ActorKind: ledger.ActorHuman, ID: "sess-save-k7m2p4xq", Doc: "docs/sessions.md", Line: 7, Note: "old"},
		{At: clock, Actor: "bot", ActorKind: ledger.ActorAgent, DelegatedBy: "k", ID: "sess-save-k7m2p4xq", Doc: "docs/sessions.md", Line: 7, Note: "new"},
		{At: clock, Actor: "k", ActorKind: ledger.ActorHuman, ID: "auth-port-h3v8n2wd", Doc: "docs/sessions.md", Line: 7},
	}}
	s, rep := changedSystem(t, repo(true), WithAcks(acks))
	if got := s.Audit(AuditOptions{}); len(got) != 3 {
		t.Errorf("all = %d", len(got))
	}
	if got := s.Audit(AuditOptions{Since: clock.Add(-time.Hour)}); len(got) != 2 {
		t.Errorf("since = %d", len(got))
	}
	if got := s.Audit(AuditOptions{ActorKind: ledger.ActorAgent}); len(got) != 1 || got[0].DelegatedBy != "k" {
		t.Errorf("agents = %+v", got)
	}
	if got := s.Audit(AuditOptions{ID: "auth-port-h3v8n2wd"}); len(got) != 1 {
		t.Errorf("by id = %+v", got)
	}
	b, err := s.Blame(rep, "docs/sessions.md", 7)
	if err != nil || b.Reference.ID != "sess-save-k7m2p4xq" || b.Block.Pos.File != "internal/store/write.go" || b.Change.State != "changed" || b.Finding.State != check.StateUnacked || len(b.Acks) != 3 {
		t.Errorf("blame = %+v %v", b, err)
	}
	if _, err := s.Blame(rep, "docs/sessions.md", 99); !errors.Is(err, ErrNoReference) {
		t.Errorf("blame missing = %v", err)
	}
	// A reference with no id (url) blames its own finding.
	if b, err := s.Blame(rep, "docs/sessions.md", 13); err != nil || b.Finding.State == "" {
		t.Errorf("blame url line = %+v %v", b, err)
	}
}

// promise:rename-identity
func TestRename(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	fsys["docs/claim.md"] = &fstest.MapFile{Data: []byte("Decision. <!-- ds:claim reviewed=2026-09-01 expires=90d about=sess-ttl-p2c4y7mk -->\n")}
	s := newSys(t, fsys)
	res, _ := s.Scan(context.Background())
	r, err := s.Rename(res, "sess", "session")
	if err != nil {
		t.Fatal(err)
	}
	if r.Mapping["sess-save-k7m2p4xq"] != "session-save-k7m2p4xq" || r.Mapping["sess-ttl-p2c4y7mk"] != "session-ttl-p2c4y7mk" || len(r.Mapping) != 2 {
		t.Errorf("mapping = %v", r.Mapping)
	}
	// The def lines, the doc's link line, the block-position line, and the
	// frontmatter covers line? Covers are not references; the page keeps the
	// old id and is reported as orphan after the rename, which is why the
	// remedy says rescan. Count the edits we do expect.
	files := map[string]int{}
	for _, e := range r.Edits {
		files[e.File]++
		if strings.Contains(e.New, "sess-") && !strings.Contains(e.New, "session-") {
			t.Errorf("edit left the old id: %+v", e)
		}
	}
	if files["internal/store/write.go"] != 2 || files["docs/sessions.md"] != 2 || files["docs/claim.md"] != 1 {
		t.Errorf("edits per file = %v: %+v", files, r.Edits)
	}
	// Applying every edit and rescanning yields the new ids with the same suffixes.
	for _, e := range r.Edits {
		out, err := e.Apply(fsys[e.File].Data)
		if err != nil {
			t.Fatal(err)
		}
		fsys[e.File] = &fstest.MapFile{Data: out}
	}
	res2, _ := newSys(t, fsys).Scan(context.Background())
	if _, ok := newSys(t, fsys).LocateID(res2, "session-save-k7m2p4xq"); !ok {
		t.Error("renamed def not found after apply")
	}
	for _, bad := range [][2]string{{"", "x"}, {"x", ""}, {"a", "a"}, {"nomatch", "x"}} {
		if _, err := s.Rename(res, bad[0], bad[1]); !errors.Is(err, ErrNotFound) {
			t.Errorf("Rename(%q,%q) = %v", bad[0], bad[1], err)
		}
	}
	// from= on another def is rewritten too; a missing file surfaces.
	chain := repo(false)
	s2 := newSys(t, chain)
	res3, _ := s2.Scan(context.Background())
	r3, err := s2.Rename(res3, "op-stripe-key", "vault-stripe-key")
	if err != nil || len(r3.Edits) != 2 || !strings.Contains(r3.Edits[0].New, "from=vault-stripe-key-p9c2v7ld") {
		t.Errorf("chain rename = %+v %v", r3, err)
	}
	delete(chain, ".github/workflows/deploy.yml")
	if _, err := s2.Rename(res3, "op-stripe-key", "v"); err == nil {
		t.Error("missing file must error")
	}
	if got := replaceID("a-b a-bc xa-b (a-b)", "a-b", "z"); got != "z a-bc xa-b (z)" {
		t.Errorf("replaceID = %q", got)
	}
	// An out-of-range line in the scan (file shrank) is skipped, not an error.
	shrunk := repo(false)
	s4 := newSys(t, shrunk)
	res4, _ := s4.Scan(context.Background())
	shrunk["docs/sessions.md"] = &fstest.MapFile{Data: []byte("short\n")}
	if r4, err := s4.Rename(res4, "sess", "s2"); err != nil || len(r4.Edits) != 2 {
		t.Errorf("shrunk file = %+v %v", r4, err)
	}
}

func TestGraph(t *testing.T) {
	t.Parallel()
	s, rep := changedSystem(t, repo(true))
	g := s.Graph(rep)
	kinds := map[string]int{}
	for _, e := range g.Edges {
		kinds[e.Kind]++
	}
	if kinds[EdgeCite] != 6 || kinds[EdgeFrom] != 1 || kinds[EdgeCov] != 1 {
		t.Errorf("edges = %v", kinds)
	}
	var docNode, defNode bool
	for _, n := range g.Nodes {
		if n.Kind == NodeDoc && n.ID == "docs/sessions.md" {
			docNode = true
		}
		if n.Kind == NodeDef && n.ID == "sess-save-k7m2p4xq" && n.State == check.StateUnacked && n.Label == "Store.Save" {
			defNode = true
		}
	}
	if !docNode || !defNode {
		t.Errorf("nodes = %+v", g.Nodes)
	}
	dot := g.DOT()
	if !strings.HasPrefix(dot, "digraph docsync {") || !strings.Contains(dot, `"docs/sessions.md" -> "sess-save-k7m2p4xq" [label="cites"]`) || !strings.Contains(dot, "shape=note") {
		t.Errorf("dot = %s", dot)
	}
	// A def without a symbol is labelled by its file; a claim about= adds an edge.
	fsys := repo(true)
	fsys["docs/c.md"] = &fstest.MapFile{Data: []byte("Decision. <!-- ds:claim reviewed=2026-09-01 expires=90d about=sess-ttl-p2c4y7mk -->\n")}
	s2, rep2 := changedSystem(t, fsys)
	g2 := s2.Graph(rep2)
	abt := 0
	for _, e := range g2.Edges {
		if e.Kind == EdgeAbt {
			abt++
		}
	}
	if abt != 1 {
		t.Errorf("about edges = %d", abt)
	}
	for _, n := range g2.Nodes {
		if n.ID == "auth-port-h3v8n2wd" && n.Label != "auth.port" {
			t.Errorf("key label = %q", n.Label)
		}
	}
	// A def with no symbol is labelled by its file.
	plain := repo(false)
	plain["notes.txt"] = &fstest.MapFile{Data: []byte("ds:def id=note-a2b6f8jk\nline\n")}
	s3 := newSys(t, plain)
	rep3, _ := s3.Check(context.Background(), CheckOptions{})
	for _, n := range s3.Graph(rep3).Nodes {
		if n.ID == "note-a2b6f8jk" && n.Label != "notes.txt" {
			t.Errorf("file label = %q", n.Label)
		}
	}
}

func TestReport(t *testing.T) {
	t.Parallel()
	fsys := repo(true)
	fsys["docs/typed.md"] = &fstest.MapFile{Data: []byte("The port is 8081 by hand.\n\n```\n8081 in a fence\n```\n<!-- 8081 in a comment -->\nCited [8081](ds:cfg?id=auth-port-h3v8n2wd) properly.\n")}
	fsys["internal/exported.go"] = &fstest.MapFile{Data: []byte("package p\n\nfunc Exported() {}\n\nfunc unexported() {}\n\n// ds:def id=marked-a2b6f8jk\nfunc Marked() {}\n")}
	for _, f := range []string{"internal/busy.go", "internal/also.go", "internal/cold.go"} {
		fsys[f] = &fstest.MapFile{Data: []byte("package p\n\nvar x = 1\n")}
	}
	c := cfg()
	c.Owners = map[string][]string{"@platform": {"someone"}}
	fsys["docs/acked.md"] = &fstest.MapFile{Data: []byte("Port [8081](ds:cfg?id=auth-port-h3v8n2wd) and [again](ds:cfg?id=auth-port-h3v8n2wd).\n")}
	fsys["docs/acked2.md"] = &fstest.MapFile{Data: []byte("Port [8081](ds:cfg?id=auth-port-h3v8n2wd).\n")}
	fsys["docs/home.md"] = &fstest.MapFile{Data: []byte("<!-- ds:def id=motto-a2b6f8jk -->\nShip weekly always\n\nElsewhere: Ship weekly always is repeated.\n")}
	acks := ledger.Acks{Rows: []ledger.Ack{
		{At: clock.Add(-24 * time.Hour), ID: "sess-save-k7m2p4xq", Doc: "docs/sessions.md", Line: 7},
		{At: clock.Add(-72 * time.Hour), ID: "auth-port-h3v8n2wd", Doc: "docs/acked.md", Line: 1},
		{At: clock.Add(-96 * time.Hour), ID: "auth-port-h3v8n2wd", Doc: "docs/acked.md", Line: 1},
		{At: clock.Add(-48 * time.Hour), ID: "auth-port-h3v8n2wd", Doc: "docs/acked2.md", Line: 1},
	}}
	s, rep := changedSystem(t, fsys, WithConfig(c), WithAcks(acks))
	r := s.Report(rep, ReportOptions{Churn: map[string]int{"internal/store/write.go": 9, "internal/busy.go": 20, "internal/also.go": 20, "internal/cold.go": 2, "internal/exported.go": 1, ".ds/ledger.tsv": 50, "internal/unscanned.go": 99}})
	ids := func(bs []block.Block) []string {
		var out []string
		for _, b := range bs {
			out = append(out, b.ID)
		}
		return out
	}
	if got := ids(r.Uncovered); len(got) != 3 || got[0] != "motto-a2b6f8jk" || got[1] != "marked-a2b6f8jk" || got[2] != "sess-ttl-p2c4y7mk" {
		t.Errorf("uncovered = %v", got)
	}
	if len(r.Unmarked) < 3 || r.Unmarked[0].File != "internal/also.go" || r.Unmarked[1].File != "internal/busy.go" || r.Unmarked[0].Churn != 20 {
		t.Errorf("unmarked churn = %+v", r.Unmarked)
	}
	var exported, marked bool
	for _, u := range r.Unmarked {
		if u.Symbol == "Exported" {
			exported = true
		}
		if u.Symbol == "Marked" {
			marked = true
		}
	}
	if !exported || marked {
		t.Errorf("unmarked symbols = %+v", r.Unmarked)
	}
	// sessions.md has cites never acked (only line 7 was), so both pages are
	// never-acked and sort by name; acked.md is fully acked and sorts last.
	if len(r.Stalest) != 4 || !r.Stalest[0].NeverAck || r.Stalest[0].Doc != "docs/sessions.md" || r.Stalest[1].Doc != "docs/typed.md" || r.Stalest[2].Doc != "docs/acked.md" || r.Stalest[3].Doc != "docs/acked2.md" || !r.Stalest[2].OldestAck.Equal(clock.Add(-72*time.Hour)) {
		t.Errorf("stalest = %+v", r.Stalest)
	}
	// typed.md line 1 is a literal; home.md line 2 is the fact's own home
	// (skipped) and line 4 repeats it by hand.
	if len(r.Literals) != 2 || r.Literals[0].Doc != "docs/home.md" || r.Literals[0].Line != 4 || r.Literals[0].ID != "motto-a2b6f8jk" || r.Literals[1].Doc != "docs/typed.md" || r.Literals[1].Line != 1 {
		t.Errorf("literals = %+v", r.Literals)
	}
	if len(r.OrphanedOwners) != 1 || r.OrphanedOwners[0] != "@auth" {
		t.Errorf("orphaned owners = %v", r.OrphanedOwners)
	}
	if len(r.PerPage) != 4 || len(r.PerOwner) < 2 || r.MeanTimeToAck != 0 {
		t.Errorf("metrics = %+v %+v %v", r.PerPage, r.PerOwner, r.MeanTimeToAck)
	}
	if len(r.BusyPrefixes) != 0 {
		t.Errorf("busy prefixes = %+v", r.BusyPrefixes)
	}
	if len(r.Gaps) < 4 || !strings.HasPrefix(r.Gaps[0], "define blocks in internal/also.go") {
		t.Errorf("gaps = %v", r.Gaps)
	}
	limited := s.Report(rep, ReportOptions{Churn: map[string]int{"internal/busy.go": 1, "internal/cold.go": 1}, Limit: 1})
	if len(limited.Uncovered) != 1 || len(limited.Unmarked) != 1 || len(limited.Stalest) != 1 || len(limited.Literals) != 1 || len(limited.Gaps) != 1 {
		t.Errorf("limit = %+v", limited)
	}
	if roomy := s.Report(rep, ReportOptions{Limit: 100}); len(roomy.Uncovered) != 3 || len(roomy.Stalest) != 4 {
		t.Errorf("large limit keeps everything = %+v", roomy)
	}
	// Files that vanished between scan and report are skipped.
	delete(fsys, "docs/typed.md")
	delete(fsys, "internal/exported.go")
	again := s.Report(rep, ReportOptions{})
	if len(again.Literals) != 1 {
		t.Errorf("vanished doc = %+v", again.Literals)
	}
	for _, u := range again.Unmarked {
		if u.Symbol == "Exported" {
			t.Error("vanished code file must be skipped")
		}
	}
	// Mean time to ack counts acks after the ledger scan; no owners config
	// means no orphan view; no facts means no literals.
	later := ledger.Acks{Rows: []ledger.Ack{{At: clock.Add(2 * time.Hour), ID: "sess-save-k7m2p4xq", Doc: "docs/sessions.md", Line: 7}, {At: clock.Add(-time.Hour)}}}
	s2, rep2 := changedSystem(t, repo(true), WithAcks(later))
	r2 := s2.Report(rep2, ReportOptions{})
	if r2.MeanTimeToAck != 2*time.Hour || r2.OrphanedOwners != nil || len(r2.Unmarked) != 1 || r2.Unmarked[0].Symbol != "Persist" {
		t.Errorf("mtta = %v orphans = %v unmarked = %v", r2.MeanTimeToAck, r2.OrphanedOwners, r2.Unmarked)
	}
	nofacts := fstest.MapFS{"docs/a.md": {Data: []byte("<!-- ds:def id=sec-a2b6f8jk -->\n## S\n\nbody\n")}, "internal/a.go": {Data: []byte("package a\n")}}
	s3 := newSys(t, nofacts)
	rep3, _ := s3.Check(context.Background(), CheckOptions{})
	if r3 := s3.Report(rep3, ReportOptions{}); r3.Literals != nil || len(r3.Uncovered) != 1 {
		t.Errorf("no facts = %+v", r3)
	}
	if isDefLine(rep.Scan, "config/auth.yaml", 2, "auth-port-h3v8n2wd") != true {
		t.Error("isDefLine")
	}
}

func TestAdopt(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	fsys["settings.json"] = &fstest.MapFile{Data: []byte("{\n  \"port\": 8080\n}\n")}
	fsys["docs/legacy.md"] = &fstest.MapFile{Data: []byte(strings.Join([]string{
		"See [Persist](internal/store/write.go#Persist) and [the return](internal/store/write.go#L12-L12).",
		"Also [Persist again](internal/store/write.go#L11) and [Save](internal/store/write.go#Save).",
		"Bad [gone](internal/missing.go#X), [weird](internal/store/write.go#a?b), [zero](internal/store/write.go#L0), [nosym](internal/store/write.go#Nope), [space](internal/store/write.go#a b).",
		"External [x](https://example.com/a#b) and [line](notes.txt#L2) stay or adopt.",
		// A link into a type with no comment carrier: adopt must leave it
		// alone and say why, never rewrite it into invalid syntax.
		"Config [port](settings.json#L2) cannot take a directive.",
		"",
	}, "\n"))}
	s := newSys(t, fsys)
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Adopt(context.Background(), res)
	if err != nil {
		t.Fatal(err)
	}
	if r.Adopted != 5 || len(r.Unresolved) != 5 {
		t.Fatalf("adopted = %d unresolved = %+v", r.Adopted, r.Unresolved)
	}
	// The JSON link was left alone with the carrier reason, and the file was
	// never touched: adopt writing a bare directive into it would corrupt it
	// exactly as `def` used to.
	var jsonLeft bool
	for _, u := range r.Unresolved {
		if strings.Contains(u.Target, "settings.json") && strings.Contains(u.Reason, "no comment carrier") {
			jsonLeft = true
		}
	}
	if !jsonLeft {
		t.Errorf("a link into a type with no carrier must be left with that reason: %+v", r.Unresolved)
	}
	for _, e := range r.Edits {
		if strings.HasSuffix(e.File, ".json") {
			t.Errorf("adopt staged an edit into a file with no comment carrier: %+v", e)
		}
	}

	reasons := map[string]string{}
	for _, u := range r.Unresolved {
		reasons[u.Target] = u.Reason
	}
	// The missing file is named as the page means it -- relative to the doc
	// holding the link -- so the reader is sent to the right place.
	if reasons["internal/missing.go#X"] != "file not found: docs/internal/missing.go" || !strings.Contains(reasons["internal/store/write.go#a?b"], "not a symbol") || !strings.Contains(reasons["internal/store/write.go#L0"], "not a line") || !strings.Contains(reasons["internal/store/write.go#Nope"], "symbol not found") {
		t.Errorf("reasons = %v", reasons)
	}
	// Source edits first: one def for Persist (three links share it), one
	// for the text line; Save already has a def and gets no source edit.
	var src, docs []Edit
	for _, e := range r.Edits {
		if e.File == "docs/legacy.md" {
			docs = append(docs, e)
		} else {
			src = append(src, e)
		}
	}
	if len(src) != 2 || src[0].File != "internal/store/write.go" || src[0].Line != 11 || !strings.HasPrefix(src[0].New, "// ds:def id=store-persist-") || src[1].File != "notes.txt" {
		t.Errorf("source edits = %+v", src)
	}
	if len(docs) != 3 || !strings.Contains(docs[0].New, "](ds:block?id=store-persist-") || !strings.Contains(docs[0].New, "&lines=2-2)") || !strings.Contains(docs[1].New, "](ds:block?id=sess-save-k7m2p4xq)") || !strings.Contains(docs[2].New, "example.com/a#b)") || !strings.Contains(docs[2].New, "](ds:block?id=notes-") {
		t.Errorf("doc edits = %+v", docs)
	}
	if strings.Count(docs[1].New, "store-persist-") != 1 || strings.Contains(docs[1].New, "lines=") {
		t.Errorf("L11 alone is the whole block, no lines=: %s", docs[1].New)
	}
	// Applying everything and rescanning leaves no unresolved link behind
	// and no broken cites.
	for _, e := range r.Edits {
		out, err := e.Apply(fsys[e.File].Data)
		if err != nil {
			t.Fatal(err)
		}
		fsys[e.File] = &fstest.MapFile{Data: out}
	}
	s2 := newSys(t, fsys)
	rep, _ := s2.Check(context.Background(), CheckOptions{})
	for _, f := range rep.Findings {
		if f.Doc == "docs/legacy.md" && f.State == check.StateBroken {
			t.Errorf("broken after adopt: %+v", f)
		}
	}
	res2, _ := s2.Scan(context.Background())
	if r2, _ := s2.Adopt(context.Background(), res2); r2.Adopted != 0 {
		t.Errorf("second adopt must find nothing: %+v", r2)
	}
	// Two new defs in one file shift each other's insertion line.
	two := fstest.MapFS{
		"internal/a.go": {Data: []byte("package a\n\nfunc One() {}\n\nfunc Two() {}\n")},
		"docs/d.md":     {Data: []byte("[1](internal/a.go#One) [2](internal/a.go#Two)\n")},
	}
	s3 := newSys(t, two)
	res3, _ := s3.Scan(context.Background())
	r3, _ := s3.Adopt(context.Background(), res3)
	if len(r3.Edits) != 3 || r3.Edits[0].Line != 3 || r3.Edits[1].Line != 6 {
		t.Fatalf("shifted edits = %+v", r3.Edits)
	}
	for _, e := range r3.Edits {
		out, err := e.Apply(two[e.File].Data)
		if err != nil {
			t.Fatal(err)
		}
		two[e.File] = &fstest.MapFile{Data: out}
	}
	if !strings.Contains(string(two["internal/a.go"].Data), "// ds:def id=one-") || !strings.Contains(string(two["internal/a.go"].Data), "// ds:def id=two-") {
		t.Errorf("both defs inserted:\n%s", two["internal/a.go"].Data)
	}
	res4, _ := newSys(t, two).Scan(context.Background())
	if len(res4.Defs) != 2 || res4.Defs[1].Symbol != "Two" {
		t.Errorf("rescan after two inserts = %+v", res4.Defs)
	}
	if _, err := parseFragment("L3-L1"); err != nil {
		t.Error("descending range parses as a line target; Locate decides")
	}
	// A target whose label slugs to nothing cannot be minted.
	dash := fstest.MapFS{"-.txt": {Data: []byte("x\n")}, "docs/d.md": {Data: []byte("[x](-.txt#L1)\n")}}
	s5 := newSys(t, dash)
	res5, _ := s5.Scan(context.Background())
	if _, err := s5.Adopt(context.Background(), res5); err == nil {
		t.Error("unmintable label must error")
	}
	// A doc that vanished between scan and adopt is skipped.
	gone := repo(false)
	s6 := newSys(t, gone)
	res6, _ := s6.Scan(context.Background())
	delete(gone, "docs/sessions.md")
	if r6, err := s6.Adopt(context.Background(), res6); err != nil || r6.Adopted != 0 {
		t.Errorf("vanished doc = %+v %v", r6, err)
	}
}

func TestFindByAndFixDuplicates(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	fsys["internal/dup.go"] = &fstest.MapFile{Data: []byte("package p\n\n// ds:def id=sess-save-k7m2p4xq tags=dup\nvar Dup = 1\n\n// ds:def id=sess-save-k7m2p4xq\nvar Dup2 = 2\n")}
	fsys["notes.txt"] = &fstest.MapFile{Data: []byte("ds:def id=note-a2b6f8jk tags=ops\nline\n")}
	s := newSys(t, fsys)
	res, _ := s.Scan(context.Background())
	if got := s.FindBy(res, FindOptions{File: "internal/"}); len(got) != 4 {
		t.Errorf("by file = %d", len(got))
	}
	if got := s.FindBy(res, FindOptions{Tag: "ops"}); len(got) != 1 || got[0].ID != "note-a2b6f8jk" {
		t.Errorf("by tag = %+v", got)
	}
	if got := s.FindBy(res, FindOptions{Query: "SAVE", File: "internal/store"}); len(got) != 1 {
		t.Errorf("query and file = %+v", got)
	}
	if got := s.FindBy(res, FindOptions{}); len(got) != len(res.Defs)+1 {
		t.Errorf("no filter = %d", len(got))
	}
	fix, err := s.FixDuplicates(res)
	if err != nil || len(fix.Edits) != 2 || len(fix.Mapping) != 2 {
		t.Fatalf("fix = %+v %v", fix, err)
	}
	// File order decides: internal/dup.go sorts before internal/store, so
	// its first def keeps the id and the other two are re-minted.
	for _, e := range fix.Edits {
		if !strings.Contains(e.New, "id=sess-save-") || strings.Contains(e.New, "k7m2p4xq") {
			t.Errorf("edit = %+v", e)
		}
	}
	if _, kept := fix.Mapping["sess-save-k7m2p4xq@internal/dup.go:3"]; kept {
		t.Error("the first occurrence in file order keeps its id")
	}
	if _, minted := fix.Mapping["sess-save-k7m2p4xq@internal/store/write.go:3"]; !minted {
		t.Error("later occurrences are re-minted")
	}
	for _, e := range fix.Edits {
		out, err := e.Apply(fsys[e.File].Data)
		if err != nil {
			t.Fatal(err)
		}
		fsys[e.File] = &fstest.MapFile{Data: out}
	}
	res2, _ := newSys(t, fsys).Scan(context.Background())
	for _, p := range res2.Problems {
		if errors.Is(p.Err, scan.ErrDuplicateID) {
			t.Errorf("duplicates remain: %v", p.Err)
		}
	}
	// No duplicates: nothing to do. An unsplittable id keeps its whole text
	// as the label. A vanished file surfaces.
	if fix, err := newSys(t, repo(false)).FixDuplicates(res2); err != nil || len(fix.Edits) != 0 {
		t.Errorf("clean tree = %+v %v", fix, err)
	}
	odd := repo(false)
	odd["a.txt"] = &fstest.MapFile{Data: []byte("ds:def id=x\none\nds:def id=x\ntwo\n")}
	odd["internal/x.go"] = &fstest.MapFile{Data: []byte("package p\n")}
	c := cfg()
	c.Scan.Code = append(c.Scan.Code, "a.txt")
	so := newSys(t, odd, WithConfig(c))
	reso, _ := so.Scan(context.Background())
	if fix, err := so.FixDuplicates(reso); err != nil || len(fix.Edits) != 1 || !strings.HasPrefix(fix.Edits[0].New, "ds:def id=x-") {
		t.Errorf("unsplittable id = %+v %v", fix, err)
	}
	// A file that shrank or changed after the scan is skipped line by line.
	odd["a.txt"] = &fstest.MapFile{Data: []byte("a\nb\nc\nd\n")}
	if fix, err := so.FixDuplicates(reso); err != nil || len(fix.Edits) != 0 {
		t.Errorf("changed file after scan = %+v %v", fix, err)
	}
	odd["a.txt"] = &fstest.MapFile{Data: []byte("x\n")}
	if fix, err := so.FixDuplicates(reso); err != nil || len(fix.Edits) != 0 {
		t.Errorf("shrunk file after scan = %+v %v", fix, err)
	}
	delete(odd, "a.txt")
	if _, err := so.FixDuplicates(reso); err == nil {
		t.Error("vanished file must error")
	}
	// An id whose label slugs to nothing cannot be re-minted.
	dash := repo(false)
	dash["a.txt"] = &fstest.MapFile{Data: []byte("ds:def id=-\none\nds:def id=-\ntwo\n")}
	sd := newSys(t, dash, WithConfig(c))
	resd, _ := sd.Scan(context.Background())
	if _, err := sd.FixDuplicates(resd); err == nil {
		t.Error("unmintable label must error")
	}
}

func TestBusyPrefixesAndSavings(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	var b strings.Builder
	b.WriteString("package p\n")
	// Three busy prefixes: busy (6) sorts first by count; alpha and beta
	// tie at five and sort by name.
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&b, "\n// ds:def id=busy-%d2b6f8jk\nvar V%d = %d\n", i, i, i)
	}
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, "\n// ds:def id=beta-%d3c7g9kl\nvar B%d = %d\n\n// ds:def id=alpha-%d4d8h2lm\nvar A%d = %d\n", i, i, i, i, i, i)
	}
	fsys["internal/busy.go"] = &fstest.MapFile{Data: []byte(b.String())}
	s := newSys(t, fsys)
	rep, _ := s.Check(context.Background(), CheckOptions{})
	r := s.Report(rep, ReportOptions{ServedBytes: 250, SourceBytes: 1000})
	if len(r.BusyPrefixes) != 3 || r.BusyPrefixes[0].Prefix != "busy" || r.BusyPrefixes[0].Count != 6 || r.BusyPrefixes[1].Prefix != "alpha" || r.BusyPrefixes[2].Prefix != "beta" || r.ContextSavings != 0.75 {
		t.Errorf("busy/savings = %+v %v", r.BusyPrefixes, r.ContextSavings)
	}
}

// TestTriageGroupsTheSameEditAcrossBlocks is the spec's own example for
// triage (§19): one mechanical change applied to many functions is one
// group. Each changed line names its own function, so no two diff lines are
// identical, and line-level similarity put every function in a group of its
// own — fifty findings, fifty decisions, the thing triage exists to prevent.
func TestTriageGroupsTheSameEditAcrossBlocks(t *testing.T) {
	t.Parallel()
	names := []string{"Save", "Load", "Delete", "List", "Count"}
	var rep Report
	for i, n := range names {
		diff := fmt.Sprintf("-func (s *Store) %s(ctx context.Context, sess Session) error {\n+func (s *Store) %s(ctx context.Context, session Session) error {\n", n, n)
		rep.Findings = append(rep.Findings, check.Finding{State: check.StateUnacked, ID: fmt.Sprintf("f%d-k7m2p4xq", i), Diff: diff})
	}
	// A different change, which must not join them.
	rep.Findings = append(rep.Findings, check.Finding{State: check.StateUnacked, ID: "other-h3v8n2wd", Diff: "-const Limit = 10\n+const Limit = 20\n"})
	groups := Triage(rep)
	if len(groups) != 2 || len(groups[0].Findings) != len(names) {
		var sizes []int
		for _, g := range groups {
			sizes = append(sizes, len(g.Findings))
		}
		t.Fatalf("groups = %v, want one of %d and one of 1", sizes, len(names))
	}
	if groups[1].Findings[0].ID != "other-h3v8n2wd" {
		t.Errorf("the different change joined the group")
	}
	// Changes that share the shape but not the edit stay apart.
	a := check.Finding{State: check.StateUnacked, ID: "a-k7m2p4xq", Diff: "-func A() int { return 1 }\n+func A() int { return 2 }\n"}
	b := check.Finding{State: check.StateUnacked, ID: "b-h3v8n2wd", Diff: "-func B() error { return legacy() }\n+func B() string { return \"\" }\n"}
	if g := Triage(Report{Findings: []check.Finding{a, b}}); len(g) != 2 {
		t.Errorf("unrelated edits grouped: %d groups", len(g))
	}
}

// TestDiffTokens pins the tokenizer triage compares by: identifiers whole
// (underscores and non-ASCII letters included), every other non-space
// character alone, the direction markers kept, whitespace dropped, and a
// diff that ends on an identifier keeps its last token.
func TestDiffTokens(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][]string{
		"-x := a_b\n+x := ÿb": {"-", "x", ":", "=", "a_b", "+", "x", ":", "=", "ÿb"},
		"-f(1)":               {"-", "f", "(", "1", ")"},
		"+tail":               {"+", "tail"},
		"":                    nil,
	} {
		if got := diffTokens(in); !reflect.DeepEqual(got, want) {
			t.Errorf("diffTokens(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRenameRefusesANonLabel pins that rename validates the label it writes
// into every def and citation. `ds rename sess-save "Bad Label"` rewrote
// each of them to `id=Bad Label-…`, which reads back as `id=Bad` and a stray
// word: every directive broken, reported as "2 lines changed".
func TestRenameRefusesANonLabel(t *testing.T) {
	t.Parallel()
	s := newSys(t, repo(false))
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"Bad Label", "Sess", "a--b", "-lead", "tail-", "under_score", "a b"} {
		r, err := s.Rename(res, "sess-save", bad)
		if !errors.Is(err, ErrBadLabel) || len(r.Edits) != 0 {
			t.Errorf("rename to %q = %v with %d edits, want ErrBadLabel and none", bad, err, len(r.Edits))
		}
	}
	if !strings.Contains(fmt.Sprint(func() error { _, err := s.Rename(res, "sess-save", "Bad Label"); return err }()), `"bad-label"`) {
		t.Error("the refusal should suggest the slug")
	}
	if r, err := s.Rename(res, "sess-save", "session-store"); err != nil || len(r.Edits) == 0 {
		t.Errorf("a valid label must still rename: %v", err)
	}
}

// TestAdoptLineRangesStayValid pins that every range a GitHub-style link can
// carry becomes a citation check accepts. GitHub highlights #L6-L4 the same
// as #L4-L6, so a reversed range is a real link, and copying it through as
// lines=4-2 wrote a citation check then reported as out of range.
func TestAdoptLineRangesStayValid(t *testing.T) {
	t.Parallel()
	src := "package a\n\nfunc Long() {\n\tone()\n\ttwo()\n\tthree()\n}\n"
	for _, tc := range []struct{ frag, lines string }{
		{"L4-L6", "&lines=2-4)"},
		{"L6-L4", "&lines=2-4)"},
		{"L5-L5", "&lines=3-3)"},
		{"L3-L7", ")"},  // the whole block needs no lines=
		{"L7-L3", ")"},  // reversed whole block, likewise
		{"L4-L99", ")"}, // past the block: cite the block, never a range outside it
		{"L99-L4", ")"}, // the same link reversed finds the same block
	} {
		fsys := fstest.MapFS{
			"internal/a.go": {Data: []byte(src)},
			"docs/d.md":     {Data: []byte("[x](internal/a.go#" + tc.frag + ")\n")},
		}
		s := newSys(t, fsys)
		res, err := s.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		r, err := s.Adopt(context.Background(), res)
		if err != nil || r.Adopted != 1 {
			t.Fatalf("%s: adopt = %+v %v", tc.frag, r, err)
		}
		for _, e := range r.Edits {
			out, err := e.Apply(fsys[e.File].Data)
			if err != nil {
				t.Fatal(err)
			}
			fsys[e.File] = &fstest.MapFile{Data: out}
		}
		doc := string(fsys["docs/d.md"].Data)
		if !strings.Contains(doc, tc.lines) || (tc.lines == ")" && strings.Contains(doc, "lines=")) {
			t.Errorf("%s: doc = %q, want %q", tc.frag, doc, tc.lines)
		}
		rep, err := newSys(t, fsys).Check(context.Background(), CheckOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range rep.Findings {
			if f.Doc == "docs/d.md" && (f.Severity == check.SeverityError || f.State == check.StateRange) {
				t.Errorf("%s: adopted citation is an error: %+v", tc.frag, f)
			}
		}
	}
}

// TestAdoptLeavesCodeExamplesAlone pins that adopt converts only live links.
// A page explaining the syntax shows it in code, and adopt read raw lines:
// it rewrote the example into a citation, and with the example first on the
// line it rewrote the example instead of the real link beside it.
func TestAdoptLeavesCodeExamplesAlone(t *testing.T) {
	t.Parallel()
	example := "[Long](internal/a.go#Long)"
	doc := "# Linking\n\nWrite `" + example + "` to link; here: " + example + ".\n\nOr indented:\n\n    " + example + "\n"
	fsys := fstest.MapFS{
		"internal/a.go": {Data: []byte("package a\n\nfunc Long() {}\n")},
		"docs/d.md":     {Data: []byte(doc)},
	}
	s := newSys(t, fsys)
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Adopt(context.Background(), res)
	if err != nil || r.Adopted != 1 {
		t.Fatalf("only the live link adopts: %+v %v", r, err)
	}
	applyAll(t, fsys, r.Edits)
	got := string(fsys["docs/d.md"].Data)
	if !strings.Contains(got, "`"+example+"`") || !strings.Contains(got, "\n    "+example+"\n") {
		t.Errorf("an example was rewritten:\n%s", got)
	}
	if !strings.Contains(got, "here: [Long](ds:block?id=long-") {
		t.Errorf("the live link was not adopted:\n%s", got)
	}
}

// TestAckPage pins the library side of a page review: it needs a page that
// declares review_every, and the row it builds names only the page.
// promise:page-review
func TestAckPage(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	fsys["docs/runbook.md"] = &fstest.MapFile{Data: []byte("---\nds:\n  review_every: 30d\n---\n# Runbook\n")}
	s := newSys(t, fsys)
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Ack(res, AckRequest{Doc: "docs/runbook.md", Page: true, ID: "ignored-a2b6f8jk", Line: 9, Actor: "khanakia", Note: "reread"})
	if err != nil || a.Doc != "docs/runbook.md" || a.ID != "" || a.Line != 0 || a.BlockHash != "" || a.SentenceHash != "" || a.Note != "reread" || !a.At.Equal(clock) {
		t.Errorf("page ack = %+v %v", a, err)
	}
	for _, doc := range []string{"docs/sessions.md", "docs/nope.md"} {
		if _, err := s.Ack(res, AckRequest{Doc: doc, Page: true}); !errors.Is(err, ErrNoPageReview) {
			t.Errorf("%s: %v, want ErrNoPageReview", doc, err)
		}
	}
	if _, err := s.Ack(res, AckRequest{Doc: "docs/runbook.md", Page: true, ActorKind: ledger.ActorAgent}); !errors.Is(err, ErrDelegationRequired) {
		t.Errorf("an agent page review still needs delegated_by: %v", err)
	}
}

// ackCounter counts the acks an observer is told of.
type ackCounter struct{ acks int }

func (c *ackCounter) OnFinding(check.Finding) {}
func (c *ackCounter) OnAck(ledger.Ack)        { c.acks++ }

// TestAckPreviewTellsNoObserver pins that a preview is not an ack: an
// embedder watching acks must not hear of one `ack --dry-run` only listed.
func TestAckPreviewTellsNoObserver(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	fsys["docs/runbook.md"] = &fstest.MapFile{Data: []byte("---\nds:\n  review_every: 30d\n---\n# Runbook\n")}
	obs := &ackCounter{}
	s := newSys(t, fsys, WithObserver(obs))
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []AckRequest{{ID: "sess-save-k7m2p4xq", Doc: "docs/sessions.md", Line: 7}, {Doc: "docs/runbook.md", Page: true}} {
		req.Preview = true
		if _, err := s.Ack(res, req); err != nil {
			t.Fatal(err)
		}
		req.Preview = false
		if _, err := s.Ack(res, req); err != nil {
			t.Fatal(err)
		}
	}
	if obs.acks != 2 {
		t.Errorf("observer heard %d acks, want the 2 real ones", obs.acks)
	}
}

// TestAdoptResolvesLinksLikeTheRenderer is finding 1 of the field report. A
// relative link means what it means when the page is rendered: relative to the
// file holding it. adopt used to pass the path through unresolved, so every
// link written from a subdirectory reported "file not found" against a file
// that existed -- links into code as well as into other pages.
func TestAdoptResolvesLinksLikeTheRenderer(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"README.md":         &fstest.MapFile{Data: []byte("# P\n\n## Target\n\nText.\n")},
		"internal/store.go": &fstest.MapFile{Data: []byte("package internal\n\nfunc Save() error { return nil }\n\nfunc Load() error { return nil }\n")},
		"CONTRIBUTING.md":   &fstest.MapFile{Data: []byte("See [t](./README.md#target).\n")},
		"docs/a.md": &fstest.MapFile{Data: []byte(strings.Join([]string{
			// A heading link into another page, from a subdirectory.
			"See [t](../README.md#target).",
			// A link into code from a subdirectory, which is how most docs/ pages
			// link to code and which failed the same way.
			"And [save](../internal/store.go#Save).",
			// Root-relative by its own spelling.
			"And [load](/internal/store.go#Load).",
			// Written from the repository root, as adopt always read links. It is
			// broken when rendered, but adopting it repairs the page.
			"And [root](internal/store.go#Save).",
			// Climbing out of the repository is never found.
			"And [out](../../outside.go#X).",
			"",
		}, "\n"))},
	}
	c := config.Default()
	c.Scan.Code = []string{"internal/**"}
	c.Scan.Docs = []string{"**/*.md"}
	s, err := New(WithFS(fsys), WithConfig(c), WithRepo("r"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Adopt(context.Background(), res)
	if err != nil {
		t.Fatal(err)
	}
	// Save (two links, one def), Load: three links adopted.
	if r.Adopted != 3 {
		t.Errorf("adopted = %d, want 3; unresolved %+v", r.Adopted, r.Unresolved)
	}
	// Heading links are navigation between pages and are left alone SILENTLY:
	// reporting each one buried the real findings under nine lines on one repo.
	for _, u := range r.Unresolved {
		if strings.Contains(u.Target, "README.md") {
			t.Errorf("a heading link was reported: %+v", u)
		}
	}
	// The only thing reported is the link that leaves the repository, named as
	// the page means it.
	if len(r.Unresolved) != 1 || !strings.Contains(r.Unresolved[0].Reason, "file not found") {
		t.Errorf("unresolved = %+v, want only the link outside the repository", r.Unresolved)
	}
	// Every edit is inside the repository and into the resolved file.
	for _, e := range r.Edits {
		if strings.HasPrefix(e.File, "..") || e.File == "README.md" {
			t.Errorf("edit into %s", e.File)
		}
	}
}

// TestDefineChecksTheDirectiveBinds is the general half of finding 8: Define
// locates with the line matcher here while a scan binds with whatever tier owns
// the file, and when the two disagreed the directive was written and only the
// next scan said "nothing to bind". Define now extracts the edited source in
// memory with the scan's own extractor and refuses first, so the file is never
// touched and --dry-run predicts it.
// promise:def-must-bind
func TestDefineChecksTheDirectiveBinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fsys := fstest.MapFS{
		"a.go":  &fstest.MapFile{Data: []byte("package p\n\ntype Limits struct {\n\tWindow int\n}\n")},
		"x.cfg": &fstest.MapFile{Data: []byte("key = 1\n")},
		// The line matcher reads `name = "x"` as a statement with no name, so
		// only the tier that scans the file can say what it is.
		"a.pkl": &fstest.MapFile{Data: []byte("name = \"x\"\n")},
	}
	c := config.Default()
	c.Scan.Code = []string{"**"}
	c.Scan.Docs = []string{"docs/**"}

	// A tier that reads the directive but always reports it cannot bind.
	refuse := refusingTier{}
	s, err := New(WithFS(fsys), WithConfig(c), WithRepo("r"), WithExtractor(refuse))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Define(ctx, "x.cfg:1", DefineOptions{})
	if !errors.Is(err, ErrWouldNotBind) || !strings.Contains(err.Error(), "refused on purpose") {
		t.Fatalf("a tier that cannot bind must refuse with its own reason: %v", err)
	}
	// A tier that reads no directive at all: refused, naming the tier. The
	// target is one the line matcher cannot name, so Define asks this tier for
	// a name too, gets none, and falls back before refusing.
	blind := New2(t, fsys, c, blindTier{})
	if _, err := blind.Define(ctx, "a.pkl:1", DefineOptions{}); !errors.Is(err, ErrWouldNotBind) || !strings.Contains(err.Error(), "blind") {
		t.Fatalf("a tier that reads nothing must refuse: %v", err)
	}
	// When the line matcher here cannot name the block but the tier that scans
	// the file can, the id takes the tier's name: server-port, not the file
	// name. An id is how a reader greps for the block.
	named, err := New2(t, fsys, c, namingTier{}).Define(ctx, "a.pkl:1", DefineOptions{})
	if err != nil || !strings.HasPrefix(named.ID, "server-port-") {
		t.Errorf("label from the scanning tier = %q %v", named.ID, err)
	}
	// With no tier able to name it, the file name is the label, as before.
	plain, err := New2(t, fsys, c).Define(ctx, "a.go:4", DefineOptions{})
	if err != nil || !strings.HasPrefix(plain.ID, "a-") {
		t.Errorf("fallback label = %q %v", plain.ID, err)
	}
}

// namingTier binds a def to the line below it and names it server.port, so the
// label Define mints can be seen coming from the scanning tier.
type namingTier struct{}

func (namingTier) Name() string        { return "naming" }
func (namingTier) Match(p string) bool { return strings.HasSuffix(p, ".pkl") }
func (namingTier) Extract(p string, src []byte, prefix string) extract.Found {
	var f extract.Found
	lines := strings.Split(string(src), "\n")
	for i, l := range lines {
		if !strings.Contains(l, prefix+":def id=") {
			continue
		}
		id := strings.Fields(l[strings.Index(l, "id=")+3:])[0]
		f.Defs = append(f.Defs, extract.Def{Block: block.Block{ID: id, Kind: block.KindKey, Symbol: "server.port", Pos: block.Position{Start: i + 2, End: i + 2}}})
	}
	return f
}

// New2 builds a System over fsys with optional extra tiers ahead of the
// defaults, failing the test on error.
func New2(t *testing.T, fsys fstest.MapFS, c config.Config, tiers ...extract.Extractor) *System {
	t.Helper()
	s, err := New(WithFS(fsys), WithConfig(c), WithRepo("r"), WithExtractor(tiers...))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// refusingTier claims .cfg files, reads their directives, and reports each one
// as unbindable, so Define's check can be seen passing the tier's reason on.
type refusingTier struct{}

func (refusingTier) Name() string        { return "refusing" }
func (refusingTier) Match(p string) bool { return strings.HasSuffix(p, ".cfg") }
func (refusingTier) Extract(p string, src []byte, prefix string) extract.Found {
	var f extract.Found
	for i, l := range strings.Split(string(src), "\n") {
		if strings.Contains(l, prefix+":def") {
			f.Problems = append(f.Problems, extract.Problem{Pos: block.Position{Start: i + 1, End: i + 1}, Err: errors.New("refused on purpose")})
		}
	}
	return f
}

// blindTier claims .pkl files and reads nothing from them.
type blindTier struct{}

func (blindTier) Name() string                                 { return "blind" }
func (blindTier) Match(p string) bool                          { return strings.HasSuffix(p, ".pkl") }
func (blindTier) Extract(string, []byte, string) extract.Found { return extract.Found{} }

// TestAdoptLinkEdges covers the two ways a link can resolve and still not be
// adoptable: the file exists but cannot be read, and no tier claims it at all.
func TestAdoptLinkEdges(t *testing.T) {
	t.Parallel()
	c := config.Default()
	c.Scan.Code = []string{"internal/**"}
	c.Scan.Docs = []string{"**/*.md"}
	base := fstest.MapFS{
		"internal/store.go": &fstest.MapFile{Data: []byte("package internal\n\nfunc Save() error { return nil }\n")},
		"docs/a.md":         &fstest.MapFile{Data: []byte("See [save](../internal/store.go#Save).\n")},
	}
	s, _ := New(WithFS(base), WithConfig(c), WithRepo("r"))
	res, _ := s.Scan(context.Background())
	// Stat finds the file; reading it fails, as a permission error would.
	unreadable, _ := New(WithFS(readFails{base, "internal/store.go"}), WithConfig(c), WithRepo("r"))
	r, err := unreadable.Adopt(context.Background(), res)
	if err != nil {
		t.Fatal(err)
	}
	if r.Adopted != 0 || len(r.Unresolved) != 1 || !strings.Contains(r.Unresolved[0].Reason, "file not found") {
		t.Errorf("an unreadable target = %+v", r)
	}
	// A registry with no tier for the target: it is not a document, so a named
	// anchor into it is treated as a symbol and reported, not skipped.
	reg := &extract.Registry{}
	bare, _ := New(WithFS(base), WithConfig(c), WithRepo("r"), WithRegistry(reg))
	if bare.isDocument("internal/store.go") {
		t.Error("a file no tier claims is not a document")
	}
}

// readFails is an FS whose Stat works everywhere but whose reads of one path
// fail, so a file can be found and then not read.
type readFails struct {
	fstest.MapFS
	path string
}

func (r readFails) ReadFile(name string) ([]byte, error) {
	if name == r.path {
		return nil, errors.New("permission denied")
	}
	return r.MapFS.ReadFile(name)
}

func (r readFails) Open(name string) (fs.File, error) {
	if name == r.path {
		return nil, errors.New("permission denied")
	}
	return r.MapFS.Open(name)
}

func (r readFails) Stat(name string) (fs.FileInfo, error) { return r.MapFS.Stat(name) }
