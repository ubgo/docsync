package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/docsync/ledger"
)

// claimDoc is a page claiming something about the fixture's Save block.
const claimDoc = "# Claims\n\nSaves are dual-written. <!-- ds:claim owner=@auth reviewed=2026-09-01 expires=3650d about=sess-save-k7m2p4xq -->\n"

// A claim about a block that changed stays expired after `ds scan`, until
// the claim is renewed: scan used to re-baseline it by emptying the change
// table the claim was read from (bug 145). A `ds prune` in between keeps the
// body the claim is measured against, so the change is still classified.
func TestClaimAboutStaysExpiredAcrossScan(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	// Only the claim refers to Save, so nothing else keeps its old body.
	write(t, dir, "docs/sessions.md", "Sessions.\n")
	write(t, dir, "docs/claims.md", claimDoc)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", goV2)
	for _, args := range [][]string{{"scan"}, {"prune", "--keep", "0d"}} {
		if r := run(t, dir, v, args...); r.code != 0 {
			t.Fatalf("%v = %+v", args, r)
		}
	}
	if r := run(t, dir, v, "check"); r.code != ExitFindings || !strings.Contains(r.out, "claim is about sess-save-k7m2p4xq, which changed (body)") {
		t.Fatalf("after scan and prune = %+v", r)
	}
	if r := run(t, dir, v, "ack", "--doc", "docs/claims.md", "--line", "3", "--note", "still dual-written"); r.code != 0 {
		t.Fatal(r)
	}
	for _, args := range [][]string{{"check"}, {"scan"}, {"check"}} {
		if r := run(t, dir, v, args...); strings.Contains(r.out, "docs/claims.md") {
			t.Errorf("%v after the renewal = %+v", args, r)
		}
	}
}

// A claim about a deleted block is broken and says how to fix it; it used
// to stay ok (bug 149).
func TestClaimAboutDeletedBlockIsBroken(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "docs/claims.md", claimDoc)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", "package store\n")
	r := run(t, dir, v, "check")
	if r.code != ExitFindings || !strings.Contains(r.out, "3\terror    broken             sess-save-k7m2p4xq was deleted") || !strings.Contains(r.out, "fix the id in docs/claims.md:3") {
		t.Errorf("deleted = %+v", r)
	}
}

// A claim about a block another workspace repository publishes expires when
// that block changes, live and frozen; `ds sync` records the claim's id in
// foreign.tsv so a frozen check has it, and an index prune keeps the body
// the claim is measured against (bug 146).
func TestClaimAboutForeignBlock(t *testing.T) {
	t.Parallel()
	api, docs, _ := twoRepos(t)
	write(t, docs, "docs/runbook.md", claimDoc)
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
	must := func(dir string, v fakeVCS, args ...string) {
		t.Helper()
		if r := run(t, dir, v, args...); r.code != 0 {
			t.Fatalf("%v = %+v", args, r)
		}
	}
	must(api, apiVCS, "scan")
	must(api, apiVCS, "publish")
	must(docs, docsVCS, "scan")
	must(docs, docsVCS, "sync")
	must(docs, docsVCS, "check")
	raw, err := os.ReadFile(filepath.Join(docs, ".ds", ledger.ForeignFile))
	if err != nil || !strings.Contains(string(raw), "\nsess-save-k7m2p4xq\t") {
		t.Fatalf("foreign.tsv does not record the claim's id: %s %v", raw, err)
	}
	must(docs, docsVCS, "publish")

	src, _ := os.ReadFile(filepath.Join(api, "internal/store.go"))
	write(t, api, "internal/store.go", strings.Replace(string(src), "legacy.Save()", "sessions.Insert()", 1))
	must(api, apiVCS, "scan")
	must(api, apiVCS, "publish")
	must(docs, docsVCS, "scan")
	// After the sync, foreign.tsv no longer names the old body either, so
	// only the claim's baseline keeps it from the prunes.
	must(docs, docsVCS, "sync")
	must(docs, docsVCS, "prune", "--keep", "0d")
	// The index is pruned from the repository that does not hold the
	// claim, so only the claim's published refs keep the body.
	must(api, apiVCS, "prune", "--index", "--keep", "0d")
	want := "claim is about sess-save-k7m2p4xq, which changed (body)"
	if r := run(t, docs, docsVCS, "check"); r.code != ExitFindings || !strings.Contains(r.out, want) {
		t.Errorf("live = %+v", r)
	}
	if r := run(t, docs, docsVCS, "check", "--frozen"); r.code != ExitFindings || !strings.Contains(r.out, want) {
		t.Errorf("frozen = %+v", r)
	}
}

// A definition of the claim's id for another environment, added after the
// claim was reviewed, expires it until it is renewed, across scans; it used
// to pass, and the next scan adopted it (bug 152).
func TestClaimAboutNewDefinition(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "docs/claims.md", claimDoc)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/staging.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq env=staging\nfunc SaveStaging() error {\n\treturn nil\n}\n")
	for _, args := range [][]string{{"check"}, {"scan"}, {"check"}} {
		if r := run(t, dir, v, args...); args[0] == "check" && !strings.Contains(r.out, "claim is about sess-save-k7m2p4xq: a new definition of sess-save-k7m2p4xq appeared (env=staging)") {
			t.Fatalf("%v = %+v", args, r)
		}
	}
	if r := run(t, dir, v, "ack", "--doc", "docs/claims.md", "--line", "3", "--note", "staging too"); r.code != 0 {
		t.Fatal(r)
	}
	if r := run(t, dir, v, "check"); strings.Contains(r.out, "docs/claims.md") {
		t.Errorf("after the renewal = %+v", r)
	}
}

// A renewal records one hash per def, and the bodies behind them are stored
// and kept: renewed after an edit no scan saw, then edited again and pruned,
// the claim's change is still classified against what was renewed.
func TestClaimRenewalBodiesAreKept(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "docs/sessions.md", "Sessions.\n")
	write(t, dir, "docs/claims.md", claimDoc)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", goV2)
	if r := run(t, dir, v, "ack", "--doc", "docs/claims.md", "--line", "3", "--note", "ok"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", strings.Replace(goV2, "sessions.Insert()", "sessions.Upsert()", 1))
	// Pruned first with only the ack log naming the renewed bodies, then
	// again once a scan has carried the renewal into refs.tsv.
	for _, steps := range [][]string{{"prune"}, {"scan", "prune"}} {
		for _, step := range steps {
			args := []string{step}
			if step == "prune" {
				args = append(args, "--keep", "0d")
			}
			if r := run(t, dir, v, args...); r.code != 0 {
				t.Fatalf("%v = %+v", args, r)
			}
		}
		if r := run(t, dir, v, "check"); !strings.Contains(r.out, "claim is about sess-save-k7m2p4xq, which changed (body)") {
			t.Errorf("after renewal, edit and %v = %+v", steps, r)
		}
	}
}
