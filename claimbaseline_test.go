package docsync

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/ledger"
)

// claimRepo is a repo whose doc claims something about a Go def, at one of
// two versions of the def.
func claimRepo(arg string) fstest.MapFS {
	return fstest.MapFS{
		"internal/store/write.go": {Data: []byte("package store\n\n// ds:def id=sess-save-k7m2p4xq\nfunc Save(a " + arg + ") error {\n\treturn nil\n}\n")},
		"docs/claims.md":          {Data: []byte("# Claims\n\nSaves are dual-written. <!-- ds:claim owner=@auth reviewed=2026-09-01 expires=3650d about=sess-save-k7m2p4xq -->\n")},
	}
}

// claimRow is the claim's row in a snapshot's refs.
func claimRow(t *testing.T, refs ledger.Refs) ledger.RefRow {
	t.Helper()
	for _, r := range refs.Rows {
		if r.Verb == "claim" {
			return r
		}
	}
	t.Fatalf("no claim row in %+v", refs.Rows)
	return ledger.RefRow{}
}

// snapshot scans fsys with the given options and returns what scan writes.
func snapshot(t *testing.T, fsys fstest.MapFS, opts ...Option) (ledger.Ledger, ledger.Refs) {
	t.Helper()
	s := newSys(t, fsys, opts...)
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s.Snapshot(res)
}

// A claim's baseline in refs.tsv is written at its first scan and carried
// unchanged by every later scan (bug 145). A renewal records its own per-def
// baseline, which refs.tsv carries as the claim's acked hash, exactly as a
// citation's ack is carried.
func TestClaimBaselineMovesOnlyOnRenewal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l1, r1 := snapshot(t, claimRepo("int"))
	first := claimRow(t, r1).SeenHash
	if first == "" || !check.IsAboutBaseline(first) {
		t.Fatalf("the first scan must baseline the claim: %q", first)
	}
	// The block changes and a scan records it: the baseline stays.
	l2, r2 := snapshot(t, claimRepo("string"), WithPrevious(l1, r1))
	if got := claimRow(t, r2).SeenHash; got != first {
		t.Fatalf("a scan moved the baseline: %q -> %q", first, got)
	}
	rep, err := newSys(t, claimRepo("string"), WithPrevious(l2, r2)).Check(ctx, CheckOptions{})
	if err != nil || len(states(rep)[check.StateExpired]) != 1 {
		t.Fatalf("after the scan = %+v %v", rep.Findings, err)
	}
	// The renewal records the blocks as they are, def by def; the scan
	// after it carries that as the claim's acked hash and leaves the seen
	// one alone, and the claim is ok from then on.
	s := newSys(t, claimRepo("string"), WithPrevious(l2, r2))
	res, _ := s.Scan(ctx)
	ack, err := s.Ack(res, AckRequest{Doc: "docs/claims.md", Line: 3, Actor: "k", Note: "still true"})
	if err != nil || !check.IsAboutBaseline(ack.BlockHash) {
		t.Fatalf("ack = %+v %v", ack, err)
	}
	acks := WithAcks(ledger.Acks{Rows: []ledger.Ack{ack}})
	l3, r3 := snapshot(t, claimRepo("string"), WithPrevious(l2, r2), acks)
	if row := claimRow(t, r3); row.SeenHash != first || row.AckedHash != ack.BlockHash {
		t.Fatalf("after the renewal = %+v", row)
	}
	if rep, _ := newSys(t, claimRepo("string"), WithPrevious(l3, r3), acks).Check(ctx, CheckOptions{}); len(states(rep)[check.StateExpired]) != 0 {
		t.Errorf("after the renewal = %+v", rep.Findings)
	}
}

// A claim recorded by a version that kept no baseline (its seen_hash empty)
// is baselined at the blocks as that version's last scan recorded them, not
// as they are now, so a change made since that scan is still reported. A
// claim no scan recorded before is baselined at the blocks as they are.
func TestUpgradedClaimIsBaselinedAtTheLastScan(t *testing.T) {
	t.Parallel()
	l1, r1 := snapshot(t, claimRepo("int"))
	first := claimRow(t, r1).SeenHash
	for i := range r1.Rows {
		r1.Rows[i].SeenHash = "" // as an older version wrote it
	}
	l2, r2 := snapshot(t, claimRepo("string"), WithPrevious(l1, r1))
	if got := claimRow(t, r2).SeenHash; got != first {
		t.Fatalf("upgraded claim baselined at %q, want the last scan's %q", got, first)
	}
	rep, _ := newSys(t, claimRepo("string"), WithPrevious(l2, r2)).Check(context.Background(), CheckOptions{})
	if len(states(rep)[check.StateExpired]) != 1 {
		t.Errorf("a change made across the upgrade = %+v", rep.Findings)
	}
	// Foreign blocks come from the previous foreign snapshot the same way.
	prevForeign := remoteDef()
	prevForeign.Hash = "old"
	fs := claimRepo("int")
	fs["docs/claims.md"] = &fstest.MapFile{Data: []byte("Remote. <!-- ds:claim owner=@a reviewed=2026-09-01 expires=3650d about=remote-c4d8h2lm -->\n")}
	l1, r1 = snapshot(t, fs)
	for i := range r1.Rows {
		r1.Rows[i].SeenHash = ""
	}
	_, r2 = snapshot(t, fs, WithPrevious(l1, r1), WithPreviousForeign(ledger.FromBlock("other", prevForeign)))
	if got, want := claimRow(t, r2).SeenHash, check.AboutBaseline([]string{prevForeign.ID}, []block.Block{prevForeign}); got != want {
		t.Errorf("upgraded foreign claim = %q, want %q", got, want)
	}
}

// An id added to a claim's about= joins its baseline only through a renewal:
// the scan after the edit carries the baseline as it was, and the claim
// reports the new definition until it is renewed (bug 152).
func TestClaimAboutAddedIDNeedsARenewal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fs := claimRepo("int")
	fs["internal/store/load.go"] = &fstest.MapFile{Data: []byte("package store\n\n// ds:def id=load-h3v8n2wd\nfunc Load() error {\n\treturn nil\n}\n")}
	l1, r1 := snapshot(t, fs)
	fs["docs/claims.md"] = &fstest.MapFile{Data: []byte("# Claims\n\nSaves are dual-written. <!-- ds:claim owner=@auth reviewed=2026-09-01 expires=3650d about=sess-save-k7m2p4xq,load-h3v8n2wd -->\n")}
	l2, r2 := snapshot(t, fs, WithPrevious(l1, r1))
	if got := claimRow(t, r2).SeenHash; got != claimRow(t, r1).SeenHash {
		t.Fatalf("the scan adopted the added id: %q", got)
	}
	rep, _ := newSys(t, fs, WithPrevious(l2, r2)).Check(ctx, CheckOptions{})
	if f := states(rep)[check.StateExpired]; len(f) != 1 || f[0].Message != "claim is about load-h3v8n2wd: a new definition of load-h3v8n2wd appeared" {
		t.Errorf("after adding the id = %+v", rep.Findings)
	}
}
