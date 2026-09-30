package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/docsync/ledger"
)

// TestStatusCarriesSeverityAndAckNote pins what a docs site needs from
// `status --json` and `export hugo` to paint a citation (§24): its severity,
// so green, amber, and red need no copy of the state table, and the ack
// behind its baseline, for the note on hover. Neither was in the output.
// The note belongs to one citation only — the fixture cites one id twice
// in one doc — and survives the block changing, since it says what the
// reviewer saw.
func TestStatusCarriesSeverityAndAckNote(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	if r := run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--doc", "docs/sessions.md", "--line", "1", "--note", "checked the write path"); r.code != 0 {
		t.Fatal(r)
	}
	rows := func() map[int]statusRow {
		t.Helper()
		r := run(t, dir, v, "status", "--json")
		var out struct {
			Refs []statusRow `json:"refs"`
		}
		if err := json.Unmarshal([]byte(r.out), &out); err != nil {
			t.Fatalf("status --json: %v\n%s", err, r.out)
		}
		m := map[int]statusRow{}
		for _, row := range out.Refs {
			if row.ID == "sess-save-k7m2p4xq" || row.ID == "nope-a2b6f8jk" {
				m[row.Line] = row
			}
		}
		return m
	}
	m := rows()
	if a := m[1]; a.Note != "checked the write path" || a.AckedBy == "" || a.AckedAt != "2026-09-06T12:00:00Z" || a.Severity != "none" {
		t.Errorf("the acked citation = %+v", a)
	}
	if b := m[3]; b.Note != "" || b.AckedBy != "" {
		t.Errorf("the other citation of the same id must not borrow the note: %+v", b)
	}
	if c := m[5]; c.Severity != "error" {
		t.Errorf("a broken citation paints red: %+v", c)
	}
	// The block changes: the citation goes stale and keeps the note that
	// says what was reviewed.
	write(t, dir, "internal/store/write.go", goV2)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	if a := rows()[1]; a.State == "ok" || a.Severity == "none" || a.Note != "checked the write path" {
		t.Errorf("the stale citation = %+v", a)
	}
	// export hugo writes the same rows.
	if r := run(t, dir, v, "export", "hugo", "--out", "site"); r.code != 0 {
		t.Fatal(r)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "site", ExportStatusFile))
	if err != nil {
		t.Fatal(err)
	}
	var exported struct {
		Refs []statusRow `json:"refs"`
	}
	if err := json.Unmarshal(raw, &exported); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range exported.Refs {
		found = found || row.Note == "checked the write path" && row.Severity != ""
	}
	if !found {
		t.Errorf("export hugo status.json lacks the note: %s", raw)
	}
}

// TestStatusCarriesClaimAckNote pins the claim path: a claim has no id and
// no block, so its note is found by the sentence it renews, and a renewal
// on another line of the same doc is not it.
func TestStatusCarriesClaimAckNote(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "docs/claims.md", "# C\n\nWe chose Postgres. <!-- ds:claim owner=@p reviewed=2020-01-01 expires=90d -->\n\nWe chose Redis. <!-- ds:claim owner=@p reviewed=2020-01-01 expires=90d -->\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	if r := run(t, dir, v, "ack", "--doc", "docs/claims.md", "--line", "3", "--note", "still Postgres"); r.code != 0 {
		t.Fatal(r)
	}
	r := run(t, dir, v, "status", "--json")
	var out struct {
		Refs []statusRow `json:"refs"`
	}
	if err := json.Unmarshal([]byte(r.out), &out); err != nil {
		t.Fatal(err)
	}
	notes := map[int]string{}
	for _, row := range out.Refs {
		if row.Doc == "docs/claims.md" {
			notes[row.Line] = row.Note
		}
	}
	if notes[3] != "still Postgres" || notes[5] != "" {
		t.Errorf("claim notes = %v\n%s", notes, r.out)
	}
}

// TestAuditLabelsAndRelativePaths pins two things about paths and rows.
// audit prints what an id-less ack approved — a claim, or a whole page —
// instead of an empty column and a line 0 that does not exist. And the
// path flags that read or write a file (audit --export, github comment
// --report) resolve a relative path against the repository, as --out does,
// not against the working directory of whoever embeds the command.
func TestAuditLabelsAndRelativePaths(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "docs/claims.md", "---\nds:\n  review_every: 30d\n---\n# C\n\nWe chose Postgres. <!-- ds:claim owner=@p reviewed=2020-01-01 expires=90d -->\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	for _, args := range [][]string{
		{"ack", "sess-save-k7m2p4xq", "--doc", "docs/sessions.md", "--line", "1", "--note", "one"},
		{"ack", "--doc", "docs/claims.md", "--line", "7", "--note", "two"},
		{"ack", "--doc", "docs/claims.md", "--note", "three"},
	} {
		if r := run(t, dir, v, args...); r.code != 0 {
			t.Fatalf("%v = %+v", args, r)
		}
	}
	out := run(t, dir, v, "audit").out
	for _, want := range []string{"\tsess-save-k7m2p4xq\tdocs/sessions.md:1\tone", "\t(claim)\tdocs/claims.md:7\ttwo", "\t(page review)\tdocs/claims.md\tthree"} {
		if !strings.Contains(out, want) {
			t.Errorf("audit lacks %q:\n%s", want, out)
		}
	}
	if r := run(t, dir, v, "audit", "--export", "audit.jsonl"); r.code != 0 {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(dir, "audit.jsonl")); err != nil {
		t.Errorf("a relative --export belongs in the repository: %v", err)
	}
	report := run(t, dir, v, "check", "--json").out
	write(t, dir, "report.json", report)
	rep, err := (&App{dir: dir}).reportFor(context.Background(), loaded{}, "report.json")
	if err != nil || len(rep.Findings) == 0 {
		t.Errorf("a relative --report is read from the repository: %d findings, %v", len(rep.Findings), err)
	}
}

// TestExportPicksTheDefaultEnvironment pins which def of a per-environment
// id export hugo writes: the one an environment-less citation resolves to.
// Keeping the last def listed exported whichever environment's file sorted
// last, and a rename changed the published value.
func TestExportPicksTheDefaultEnvironment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v := fakeVCS{head: "abc1234", files: map[string][]byte{}}
	write(t, dir, "config/a-prod.yaml", "port: 443 # ds:def id=port-k7m2p4xq env=prod\n")
	write(t, dir, "config/b-dev.yaml", "port: 8080 # ds:def id=port-k7m2p4xq env=dev\n")
	write(t, dir, "docs/d.md", "Port [x](ds:cfg?id=port-k7m2p4xq).\n")
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatal(r)
	}
	for _, def := range []string{"prod", "dev"} {
		write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"config/**\"]\ndocs = [\"docs/**\"]\n[env]\ndefault = \""+def+"\"\nknown = [\"prod\", \"dev\"]\n")
		if r := run(t, dir, v, "export", "hugo", "--out", "site"); r.code != 0 {
			t.Fatal(r)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "site", ExportBlocksFile))
		if err != nil {
			t.Fatal(err)
		}
		var blocks map[string]struct {
			File    string `json:"file"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(raw, &blocks); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"prod": "443", "dev": "8080"}[def]
		if got := blocks["port-k7m2p4xq"].Content; got != want {
			t.Errorf("env.default %s exports %q, want %q", def, got, want)
		}
	}
}

// TestSyncIgnoresPublishedBranches pins that publishing a branch upstream
// leaves a downstream snapshot alone when nothing cites that branch, and
// that the sync summary compares one def with the same def.
func TestSyncIgnoresPublishedBranches(t *testing.T) {
	t.Parallel()
	api, docs, _, apiVCS, docsVCS := staleSetup(t)
	before, err := os.ReadFile(filepath.Join(docs, ".ds", "foreign.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	feature := apiVCS
	feature.branch = "feature"
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.branchOnly()\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if r := run(t, api, feature, "publish", "--branch"); r.code != 0 {
		t.Fatal(r)
	}
	r := run(t, docs, docsVCS, "sync")
	after, _ := os.ReadFile(filepath.Join(docs, ".ds", "foreign.tsv"))
	if r.code != 0 || !rowsEqual(mustForeign(t, before), mustForeign(t, after)) {
		t.Errorf("an uncited branch rewrote foreign.tsv: %+v\nbefore:\n%s\nafter:\n%s", r, before, after)
	}
}

func mustForeign(t *testing.T, raw []byte) ledger.Foreign {
	t.Helper()
	f, err := ledger.DecodeForeign(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return f
}
