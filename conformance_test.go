package docsync

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/match"
)

// The conformance suite is the spec (docs/SPEC.md §33): every rule is a
// fixture under testdata/conformance/<case>/ with
//
//	after/          the tree to check (required)
//	before/         the previously committed tree; scanned to build the
//	                previous ledger and refs, and to answer OldContent
//	merged_before/  optional; another repository's tree as it was, and
//	merged_after/   optional; as it is now. Their defs arrive the way sync
//	                delivers them — through a ledger row, so they carry a
//	                hash and no body — which is what makes a cross-repo
//	                citation different from a local one.
//	config.toml     optional; defaults scan everything under after/
//	seen/           optional; the tree as it was when the citations were
//	                first recorded, named by seen_tree "seen"
//	fixture.json    optional {prev_tree: "before"|"after", bodies: bool,
//	                seen_tree: "before"|"after"|"seen"|""}.
//	                prev_tree "after" models a `ds scan` having run since
//	                the edit, which rewrites the ledger so that prev equals
//	                current; bodies false models an unavailable body store.
//	acks.json       optional list of {doc, line, id, hash: "before"|"after"|
//	                "merged_before"|"merged_after", actor_kind,
//	                delegated_by, days_ago}
//	expected.json   the findings as {doc, line, id, state, severity} plus
//	                exit_code, sorted the way Check sorts them
//
// Run with -update to rewrite expected.json from the current behaviour, then
// read the diff: a changed expectation is a changed rule.
var update = flag.Bool("update", false, "rewrite conformance expectations")

// conformanceNow is fixed so date-based rules (claims, sunset, review_every)
// are reproducible.
var conformanceNow = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

type expectedFinding struct {
	Doc      string `json:"doc"`
	Line     int    `json:"line"`
	ID       string `json:"id,omitempty"`
	State    string `json:"state"`
	Severity string `json:"severity"`
}

type expectation struct {
	ExitCode int               `json:"exit_code"`
	Findings []expectedFinding `json:"findings"`
}

// fixtureSpec is the optional per-case knobs; the zero value is the default
// shape every existing fixture uses.
type fixtureSpec struct {
	// PrevTree names the tree the previous ledger is built from. "before"
	// (the default) is the ordinary "edited since the last scan" case;
	// "after" is the state `ds scan` leaves behind, where the ledger no
	// longer remembers the edit and only the ack does.
	PrevTree string `json:"prev_tree"`
	// Bodies, default true, supplies the body store. False models a store
	// that cannot answer, which must degrade to block.ClassUnknown rather
	// than to silence.
	Bodies *bool `json:"bodies"`
	// SeenTree overrides the first-seen baseline carried in the previous
	// refs, so a fixture can say "this citation was first recorded when the
	// block still said X" independently of which tree built the ledger.
	// Empty leaves whatever Snapshot recorded.
	SeenTree string `json:"seen_tree"`
}

type ackSpec struct {
	Doc         string `json:"doc"`
	Line        int    `json:"line"`
	ID          string `json:"id"`
	Hash        string `json:"hash"`
	ActorKind   string `json:"actor_kind,omitempty"`
	DelegatedBy string `json:"delegated_by,omitempty"`
	DaysAgo     int    `json:"days_ago"`
	// Rule is the extraction rule the ack was made under; zero means the
	// current one, which is what an ack by this tool records.
	Rule int `json:"rule,omitempty"`
	// Sentence "before" records the sentence the citation had in the
	// before tree, so a fixture can show a sentence rewritten since its
	// ack; empty records none.
	Sentence string `json:"sentence,omitempty"`
}

// promise:ack-other-rule -- the fixtures ack-under-another-sentence-rule and
// ack-under-another-rule-same-sentence record acks under rule 3, which this
// build does not use, and pin that such a citation reports "ack was recorded
// under sentence rule 3, not 1" once, never as a rewrite.
func TestConformance(t *testing.T) {
	t.Parallel()
	cases, err := filepath.Glob("testdata/conformance/*")
	if err != nil || len(cases) == 0 {
		t.Fatalf("no conformance cases: %v", err)
	}
	for _, dir := range cases {
		dir := dir
		t.Run(filepath.Base(dir), func(t *testing.T) {
			t.Parallel()
			runConformance(t, dir)
		})
	}
}

func runConformance(t *testing.T, dir string) {
	t.Helper()
	after := loadTree(t, filepath.Join(dir, "after"))
	cfg := config.Default()
	cfg.Scan.Code = []string{"**"}
	cfg.Scan.Docs = []string{"docs/**"}
	if raw, err := os.ReadFile(filepath.Join(dir, "config.toml")); err == nil {
		cfg, err = config.Parse(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
	}
	clock := func() time.Time { return conformanceNow }
	base := []Option{WithConfig(cfg), WithRepo("repo"), WithCommit("c0ffee"), WithClock(clock)}

	spec := fixtureSpec{}
	if raw, err := os.ReadFile(filepath.Join(dir, "fixture.json")); err == nil {
		if err := json.Unmarshal(raw, &spec); err != nil {
			t.Fatal(err)
		}
	}

	// bodyStore is every body any tree has ever held, keyed by content hash
	// — the fixture's stand-in for .ds/blocks/ and the index's blocks/.
	bodyStore := map[string]string{}
	record := func(defs []block.Block) {
		for _, b := range defs {
			if b.Content != "" && !b.IsSecret() && !b.IsLocal() {
				bodyStore[b.Hash] = b.Content
			}
		}
	}

	var prev ledger.Ledger
	var prevRefs ledger.Refs
	var beforeDefs, afterDefs map[string]block.Block
	// beforeByKey answers OldContent for the exact previous row: one id can
	// have a row per environment or branch, and defsByID keeps only one.
	beforeByKey := map[string]block.Block{}
	// beforeRefs is each citation in the before tree, by doc line and id,
	// for an ack that records the sentence it approved.
	beforeRefs := map[string]block.Reference{}
	var beforeSnap, afterSnap func() (ledger.Ledger, ledger.Refs)
	if before := loadTree(t, filepath.Join(dir, "before")); before != nil {
		bs, err := New(append([]Option{WithFS(before)}, base...)...)
		if err != nil {
			t.Fatal(err)
		}
		res, err := bs.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		beforeSnap = func() (ledger.Ledger, ledger.Refs) { return bs.Snapshot(res) }
		beforeDefs = defsByID(res.Defs)
		for _, b := range res.Defs {
			beforeByKey[match.BlockKey(b)] = b
		}
		for _, r := range res.Refs {
			beforeRefs[fmt.Sprintf("%s:%d %s", r.Pos.File, r.Pos.Start, r.ID)] = r
		}
		record(res.Defs)
	}
	{
		as, err := New(append([]Option{WithFS(after)}, base...)...)
		if err != nil {
			t.Fatal(err)
		}
		res, err := as.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		afterSnap = func() (ledger.Ledger, ledger.Refs) { return as.Snapshot(res) }
		afterDefs = defsByID(res.Defs)
		record(res.Defs)
	}
	switch spec.PrevTree {
	case "", "before":
		if beforeSnap != nil {
			prev, prevRefs = beforeSnap()
		}
	case "after":
		prev, prevRefs = afterSnap()
	default:
		t.Fatalf("fixture.json: unknown prev_tree %q", spec.PrevTree)
	}

	// The other repository, delivered the way sync delivers it: through a
	// ledger row, so a merged def carries a hash and no body.
	var merged []block.Block
	mergedDefs := map[string]map[string]block.Block{}
	for _, name := range []string{"merged_before", "merged_after"} {
		tree := loadTree(t, filepath.Join(dir, name))
		if tree == nil {
			continue
		}
		ms, err := New(WithFS(tree), WithConfig(cfg), WithRepo("docs"), WithCommit("c0ffee"), WithClock(clock))
		if err != nil {
			t.Fatal(err)
		}
		res, err := ms.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		mergedDefs[name] = defsByID(res.Defs)
		record(res.Defs)
		if name == "merged_after" {
			for _, b := range res.Defs {
				merged = append(merged, ledger.FromBlock("docs", b).ToBlock())
			}
		}
	}
	if spec.SeenTree != "" {
		byTree := map[string]map[string]block.Block{"before": beforeDefs, "after": afterDefs}
		for name, defs := range mergedDefs {
			byTree[name] = defs
		}
		// seen/ is a third state of the tree: the block as it was when the
		// citation was first recorded, earlier than before/. It is what lets
		// a fixture say "first cited at v1, last scanned at v2, then the
		// doc moved", which before/ and after/ alone cannot.
		if tree := loadTree(t, filepath.Join(dir, "seen")); tree != nil {
			ss, err := New(append([]Option{WithFS(tree)}, base...)...)
			if err != nil {
				t.Fatal(err)
			}
			res, err := ss.Scan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			byTree["seen"] = defsByID(res.Defs)
			record(res.Defs)
		}
		defs, ok := byTree[spec.SeenTree]
		if !ok {
			t.Fatalf("fixture.json: unknown seen_tree %q", spec.SeenTree)
		}
		for i := range prevRefs.Rows {
			if b, ok := defs[prevRefs.Rows[i].ID]; ok {
				prevRefs.Rows[i].SeenHash = b.Hash
			}
		}
	}

	var acks ledger.Acks
	if raw, err := os.ReadFile(filepath.Join(dir, "acks.json")); err == nil {
		var specs []ackSpec
		if err := json.Unmarshal(raw, &specs); err != nil {
			t.Fatal(err)
		}
		for _, a := range specs {
			hash := ""
			switch a.Hash {
			case "":
				// A claim ack pins a date, not a block hash.
			case "before":
				hash = beforeDefs[a.ID].Hash
			case "after":
				hash = afterDefs[a.ID].Hash
			case "merged_before", "merged_after":
				hash = mergedDefs[a.Hash][a.ID].Hash
			default:
				t.Fatalf("acks.json: unknown hash %q", a.Hash)
			}
			if hash == "" && a.Hash != "" {
				t.Fatalf("acks.json: %s has no %s hash", a.ID, a.Hash)
			}
			kind := ledger.ActorKind(a.ActorKind)
			if kind == "" {
				kind = ledger.ActorHuman
			}
			rule := a.Rule
			if rule == 0 {
				rule = extract.Rule
			}
			row := ledger.Ack{At: conformanceNow.Add(-time.Duration(a.DaysAgo) * 24 * time.Hour), Actor: "fixture", ActorKind: kind, DelegatedBy: a.DelegatedBy, ID: a.ID, Repo: "repo", Doc: a.Doc, Line: a.Line, BlockHash: hash, Rule: rule}
			switch a.Sentence {
			case "":
			case "before":
				ref, ok := beforeRefs[fmt.Sprintf("%s:%d %s", a.Doc, a.Line, a.ID)]
				if !ok {
					t.Fatalf("acks.json: no citation of %s at %s:%d in the before tree", a.ID, a.Doc, a.Line)
				}
				row.Sentence, row.SentenceHash = ref.Sentence, ref.SentenceHash
			default:
				t.Fatalf("acks.json: unknown sentence %q", a.Sentence)
			}
			acks.Rows = append(acks.Rows, row)
		}
	}
	old := func(row ledger.Row) (string, bool) {
		b, ok := beforeByKey[match.RowKey(row)]
		return b.Content, ok
	}
	opts := []Option{WithFS(after), WithPrevious(prev, prevRefs), WithAcks(acks), WithOldContent(old)}
	if len(merged) > 0 {
		opts = append(opts, WithMerged(merged...))
	}
	if spec.Bodies == nil || *spec.Bodies {
		opts = append(opts, WithBodyAt(func(hash string) (string, bool) {
			body, ok := bodyStore[hash]
			return body, ok
		}))
	}
	s, err := New(append(opts, base...)...)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.Check(context.Background(), CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := expectation{ExitCode: rep.ExitCode}
	for _, f := range rep.Findings {
		got.Findings = append(got.Findings, expectedFinding{Doc: f.Doc, Line: f.Line, ID: f.ID, State: string(f.State), Severity: string(f.Severity)})
	}
	assertContentIsData(t, rep)
	expPath := filepath.Join(dir, "expected.json")
	if *update {
		raw, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(expPath, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(expPath)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	var want expectation
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", "  ")
		t.Errorf("findings differ from %s:\n%s", expPath, g)
	}
}

// assertContentIsData pins §26.6: scanned text reaches the JSON only inside
// the fields named as content. A fixture plants the marker string in a
// comment, a desc, and a page; it may appear in sentence, diff, or message
// (which quotes directive text) and nowhere else.
func assertContentIsData(t *testing.T, rep Report) {
	t.Helper()
	const marker = "IGNORE PREVIOUS INSTRUCTIONS"
	for _, f := range rep.Findings {
		remedy := f.Remedy.IfStillTrue + f.Remedy.IfNot + f.Remedy.Fix
		if strings.Contains(remedy, marker) || strings.Contains(f.ID, marker) || strings.Contains(f.Doc, marker) || strings.Contains(f.Owner, marker) {
			t.Errorf("planted instruction escaped into a non-content field: %+v", f)
		}
	}
}

func defsByID(defs []block.Block) map[string]block.Block {
	m := map[string]block.Block{}
	for _, b := range defs {
		m[b.ID] = b
	}
	return m
}

// loadTree reads a directory into a MapFS, or returns nil when it is absent.
// Files named `.keep` are dropped so empty directories can be committed.
func loadTree(t *testing.T, root string) fstest.MapFS {
	t.Helper()
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	m := fstest.MapFS{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == ".keep" {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		m[filepath.ToSlash(rel)] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestConformanceStatesCovered fails when a state in the findings table has
// no fixture producing it: a rule without a fixture is not yet a rule (§33).
// States that need hooks the suite does not wire (url outcomes, run
// execution) are listed as exempt with the reason.
func TestConformanceStatesCovered(t *testing.T) {
	t.Parallel()
	exempt := map[check.State]string{
		check.StateDead:          "needs a url hook; covered by check unit tests",
		check.StateRetitled:      "needs a url hook; covered by check unit tests",
		check.StateURLMoved:      "needs a url hook; covered by check unit tests",
		check.StateAssertFailed:  "needs published test results; covered by check unit tests",
		check.StateResolveFailed: "needs a resolver; covered by check unit tests",
		check.StateOutOfSync:     "needs a resolver; covered by check unit tests",
		check.StateRotated:       "needs a resolver; covered by check unit tests",
	}
	// In -update mode TestConformance is rewriting these files in parallel,
	// so reading them here races and reports a half-written file as a
	// missing state. The check is meaningful only against settled
	// expectations, which the very next ordinary run provides.
	if *update {
		t.Skip("expectations are being rewritten")
	}
	seen := map[check.State]bool{}
	files, _ := filepath.Glob("testdata/conformance/*/expected.json")
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var e expectation
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		for _, x := range e.Findings {
			seen[check.State(x.State)] = true
		}
	}
	var missing []string
	for _, st := range check.StateValues {
		if !seen[st] && exempt[st] == "" {
			missing = append(missing, string(st))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("states with no conformance fixture: %v", missing)
	}
}
