package check

import (
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/scan"
)

// The regressions in this file are the two ways the coverage question used
// to be answered from the wrong table (§16 pass 4). Both produced a silent
// `ok` with exit 0 over a citation whose block had demonstrably changed, so
// each asserts the finding AND the exit code: a test that only counted
// findings would have passed against the broken build.

// ackedAt is the acks log for one sentence acked at a given hash.
func ackedAt(id, doc string, line int, repo, hash string) ledger.Acks {
	return ledger.Acks{Rows: []ledger.Ack{{At: now, Actor: "k", ActorKind: ledger.ActorHuman, ID: id, Repo: repo, Doc: doc, Line: line, BlockHash: hash}}}
}

// TestScanDoesNotLaunderUnacked pins the single-repo half of the defect: a
// citation is acked, the block is edited, and `ds scan` runs before `check`.
// Scan rewrites the ledger, so prev == current and match.Compare produces no
// Change at all — which used to send the reference down the "up to date"
// path. The ack still names a hash the block no longer has, so the sentence
// is uncovered and must say so.
func TestScanDoesNotLaunderUnacked(t *testing.T) {
	t.Parallel()
	old := def("depth-k7m2p4xq", "a.go", 3, "func Depth() int {\n\treturn 12\n}", nil)
	nw := def("depth-k7m2p4xq", "a.go", 3, "func Depth() int {\n\treturn 6\n}", nil)
	r := ref("block", nw.ID, "docs/a.md", 5, nil)
	// prev is the CURRENT scan, which is what `ds scan` leaves behind.
	prev := ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", nw)}}
	in := Input{
		Repo: "api", Now: now, Defs: []block.Block{nw}, Refs: []block.Reference{r},
		Prev: prev, Acks: ackedAt(nw.ID, "docs/a.md", 5, "api", old.Hash),
	}
	rep := Run(in)
	f := one(t, rep, StateUnacked)
	if rep.ExitCode != 1 || f.Hash.Acked != old.Hash || f.Hash.Current != nw.Hash {
		t.Fatalf("scan must not clear an open finding: exit=%d %+v", rep.ExitCode, f)
	}
	// Without the body store the difference is real but undescribed, and
	// ClassUnknown must not be quietly downgraded to ClassBody.
	if len(f.Classes) != 1 || f.Classes[0] != block.ClassUnknown || f.Diff != "" {
		t.Errorf("want an unclassified drift, got classes=%v diff=%q", f.Classes, f.Diff)
	}
	// With the body store it classifies exactly, and carries a diff.
	in.Opts.BodyAt = bodies(old, nw)
	f = one(t, Run(in), StateUnacked)
	if len(f.Classes) != 1 || f.Classes[0] != block.ClassBody || !strings.Contains(f.Diff, "return 6") {
		t.Errorf("stored body must classify: classes=%v diff=%q", f.Classes, f.Diff)
	}
	// Acking at the hash it has now clears it, and a later scan keeps it clear.
	in.Acks = ackedAt(nw.ID, "docs/a.md", 5, "api", nw.Hash)
	if rep := Run(in); rep.ExitCode != 0 || one(t, rep, StateOK).Message != "up to date" {
		t.Errorf("ack at current hash must clear: %+v", rep.Findings)
	}
}

// TestCrossRepoDriftFlags pins the cross-repo half: the def lives in another
// repository and arrives through Merged, so it is never matched and no
// Change exists for it. The acked hash is the only baseline, and it is
// enough. A merged row carries no body, so classification needs both sides
// from the store.
func TestCrossRepoDriftFlags(t *testing.T) {
	t.Parallel()
	old := def("p1-co-04-k7m2p4xq", "spec/SPEC.md", 4, "A parent chain may be at most 12 deep.", nil)
	nw := def("p1-co-04-k7m2p4xq", "spec/SPEC.md", 4, "A parent chain may be at most 6 deep.", nil)
	// What sync actually hands over: a ledger row, hash but no body.
	mergedRow := ledger.FromBlock("docs", nw).ToBlock()
	if mergedRow.Content != "" {
		t.Fatal("a merged row is expected to carry no body; this test is built on that")
	}
	r := ref("block", nw.ID, "pkg/depth.go", 3, nil)
	in := Input{
		Repo: "code", Now: now, Merged: []block.Block{mergedRow}, Refs: []block.Reference{r},
		Acks: ackedAt(nw.ID, "pkg/depth.go", 3, "code", old.Hash),
	}
	rep := Run(in)
	f := one(t, rep, StateUnacked)
	if rep.ExitCode != 1 || f.Hash.Acked != old.Hash {
		t.Fatalf("cross-repo drift must flag: exit=%d %+v", rep.ExitCode, f)
	}
	if len(f.Classes) != 1 || f.Classes[0] != block.ClassUnknown {
		t.Errorf("no store means unknown, got %v", f.Classes)
	}
	// Only the OLD body available: the current side is still missing, so it
	// stays unknown rather than diffing against an empty string.
	in.Opts.BodyAt = bodies(old)
	if cs := one(t, Run(in), StateUnacked).Classes; len(cs) != 1 || cs[0] != block.ClassUnknown {
		t.Errorf("half a store is not a classification, got %v", cs)
	}
	// Both sides available: classified, with a diff.
	in.Opts.BodyAt = bodies(old, nw)
	f = one(t, Run(in), StateUnacked)
	if len(f.Classes) != 1 || f.Classes[0] != block.ClassBody || !strings.Contains(f.Diff, "6 deep") {
		t.Errorf("cross-repo classification: classes=%v diff=%q", f.Classes, f.Diff)
	}
	// Acking at the merged block's current hash clears it.
	in.Acks = ackedAt(nw.ID, "pkg/depth.go", 3, "code", nw.Hash)
	if rep := Run(in); rep.ExitCode != 0 {
		t.Errorf("ack must clear cross-repo too: %+v", rep.Findings)
	}
}

// TestDriftRespectsStability checks the policy still decides whether a drift
// speaks. ClassUnknown is deliberately wider than ClassBody — see
// block.Flags — so `api`, which ignores a body change, must still flag an
// unclassified one rather than return a wrong `ok`.
func TestDriftRespectsStability(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		stability string
		store     bool
		want      State
	}{
		{"volatile", false, StateOK}, // opts out of prose flagging entirely
		{"volatile", true, StateOK},
		{"api", false, StateUnacked}, // unclassified: could be a signature
		{"api", true, StateOK},       // classified as body: api ignores it
		{"stable", true, StateUnacked},
		{"frozen", true, StateUnacked},
	} {
		args := map[string]string{block.KeyStability: tc.stability}
		old := def("x-k7m2p4xq", "a.go", 3, "func X() int {\n\treturn 1\n}", args)
		nw := def("x-k7m2p4xq", "a.go", 3, "func X() int {\n\treturn 2\n}", copyArgs(args))
		in := Input{
			Repo: "api", Now: now, Defs: []block.Block{nw},
			Refs: []block.Reference{ref("block", nw.ID, "d.md", 1, nil)},
			Prev: ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", nw)}},
			Acks: ackedAt(nw.ID, "d.md", 1, "api", old.Hash),
			Opts: Options{CommentPrefixes: func(string) []string { return []string{"//"} }},
		}
		if tc.store {
			in.Opts.BodyAt = bodies(old, nw)
		}
		if f := one(t, Run(in), tc.want); f.State != tc.want {
			t.Errorf("stability=%s store=%v: got %s", tc.stability, tc.store, f.State)
		}
	}
}

// TestDriftSignatureUnderAPI is the case ClassUnknown exists to protect: an
// `api` block whose declaration changed must flag, and must be named a
// signature change rather than a body one.
func TestDriftSignatureUnderAPI(t *testing.T) {
	t.Parallel()
	args := map[string]string{block.KeyStability: "api"}
	old := def("x-k7m2p4xq", "a.go", 3, "func X(a int) error {\n\treturn nil\n}", args)
	nw := def("x-k7m2p4xq", "a.go", 3, "func X(a, b int) error {\n\treturn nil\n}", copyArgs(args))
	in := Input{
		Repo: "api", Now: now, Defs: []block.Block{nw},
		Refs: []block.Reference{ref("block", nw.ID, "d.md", 1, nil)},
		Prev: ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", nw)}},
		Acks: ackedAt(nw.ID, "d.md", 1, "api", old.Hash),
		Opts: Options{BodyAt: bodies(old, nw), CommentPrefixes: func(string) []string { return []string{"//"} }},
	}
	f := one(t, Run(in), StateUnacked)
	if len(f.Classes) != 1 || f.Classes[0] != block.ClassSignature {
		t.Errorf("want a signature change, got %v", f.Classes)
	}
}

// TestDriftTranslationStale checks the translates= branch is reached from
// the ack-anchored path too, not only from the change table.
func TestDriftTranslationStale(t *testing.T) {
	t.Parallel()
	old := def("x-k7m2p4xq", "a.md", 3, "The limit is twelve.", nil)
	nw := def("x-k7m2p4xq", "a.md", 3, "The limit is six.", nil)
	in := Input{
		Repo: "api", Now: now, Defs: []block.Block{nw},
		Refs: []block.Reference{ref("block", nw.ID, "de/a.md", 1, map[string]string{"translates": "true"})},
		Prev: ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", nw)}},
		Acks: ackedAt(nw.ID, "de/a.md", 1, "api", old.Hash),
		Opts: Options{BodyAt: bodies(old, nw)},
	}
	if f := one(t, Run(in), StateTranslationStale); !strings.Contains(f.Message, "body") {
		t.Errorf("translation stale = %+v", f)
	}
}

// TestBodyAtEdgeCases covers the guards on the lookup itself: no hook, an
// empty hash, and a hook that simply does not have the body.
func TestBodyAtEdgeCases(t *testing.T) {
	t.Parallel()
	r := &runner{}
	if _, ok := r.bodyAt("abc"); ok {
		t.Error("no hook must report unavailable")
	}
	r = &runner{in: Input{Opts: Options{BodyAt: func(string) (string, bool) { return "x", true }}}}
	if _, ok := r.bodyAt(""); ok {
		t.Error("an empty hash is never a stored body")
	}
	if _, ok := r.bodyAt("abc"); !ok {
		t.Error("hook must be consulted")
	}
	// commentPrefixes degrades to nil without a grammar.
	if got := r.commentPrefixes("a.go"); got != nil {
		t.Errorf("want nil prefixes, got %v", got)
	}
	r.in.Opts.CommentPrefixes = func(string) []string { return []string{"#"} }
	if got := r.commentPrefixes("a.go"); len(got) != 1 || got[0] != "#" {
		t.Errorf("prefixes = %v", got)
	}
}

// bodies builds a BodyAt hook over the given blocks, keyed by hash.
func bodies(bs ...block.Block) func(string) (string, bool) {
	m := map[string]string{}
	for _, b := range bs {
		m[b.Hash] = b.Content
	}
	return func(hash string) (string, bool) {
		body, ok := m[hash]
		return body, ok
	}
}

func copyArgs(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// TestNeverAckedDriftFlags pins the last of the four silent-ok paths: a
// citation nobody ever acked. Its baseline is the hash recorded the first
// time it was seen, carried in refs.tsv so a fresh checkout has it without
// the ack log. Before seen_hash existed there was no baseline at all, so
// once `ds scan` had rewritten the ledger an unreviewed change vanished.
// promise:coverage-baseline
func TestNeverAckedDriftFlags(t *testing.T) {
	t.Parallel()
	old := def("depth-k7m2p4xq", "a.go", 3, "func Depth() int {\n\treturn 12\n}", nil)
	nw := def("depth-k7m2p4xq", "a.go", 3, "func Depth() int {\n\treturn 6\n}", nil)
	seen := ledger.Refs{Rows: []ledger.RefRow{{ID: nw.ID, Repo: "api", Doc: "docs/a.md", Line: 5, SeenHash: old.Hash}}}
	in := Input{
		Repo: "api", Now: now, Defs: []block.Block{nw},
		Refs: []block.Reference{ref("block", nw.ID, "docs/a.md", 5, nil)},
		// prev is the current scan: `ds scan` has run since the edit.
		Prev:     ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", nw)}},
		PrevRefs: seen,
		Opts:     Options{BodyAt: bodies(old, nw)},
	}
	rep := Run(in)
	f := one(t, rep, StateUnacked)
	if rep.ExitCode != 1 {
		t.Fatalf("a never-acked drift must fail the run: %+v", f)
	}
	// The message must not claim an ack that never happened.
	if !strings.Contains(f.Message, sinceFirstCited) || strings.Contains(f.Message, sinceAcked) {
		t.Errorf("message = %q", f.Message)
	}
	if f.Hash.Acked != "" {
		t.Errorf("there is no acked hash to report, got %q", f.Hash.Acked)
	}
	if len(f.Classes) != 1 || f.Classes[0] != block.ClassBody {
		t.Errorf("classes = %v", f.Classes)
	}
	// A seen hash equal to the current one is covered.
	in.PrevRefs.Rows[0].SeenHash = nw.Hash
	if rep := Run(in); rep.ExitCode != 0 {
		t.Errorf("seen at the current hash is covered: %+v", rep.Findings)
	}
	// An ack always outranks the first-seen baseline, and restores the
	// wording that says a reviewed statement went stale.
	in.PrevRefs.Rows[0].SeenHash = old.Hash
	in.Acks = ackedAt(nw.ID, "docs/a.md", 5, "api", old.Hash)
	f = one(t, Run(in), StateUnacked)
	if !strings.Contains(f.Message, sinceAcked) || f.Hash.Acked != old.Hash {
		t.Errorf("ack must outrank seen: %+v", f)
	}
}

// TestUnrecordedIDUnderSnapshot pins the difference between "nothing defines
// this" and "nothing here defines this, and the snapshot does not record
// it". A frozen run consults only the committed snapshot, so a new
// cross-repo citation reaches the second state the moment CI runs before
// anyone has synced — and telling that reader to "fix the id" sends them to
// correct a citation that is already right.
func TestUnrecordedIDUnderSnapshot(t *testing.T) {
	t.Parallel()
	r := ref("block", "width-k7m2p4xq", "pkg/width.go", 3, nil)
	in := Input{Repo: "code", Now: now, Refs: []block.Reference{r}}

	// With the live index, an unresolvable id is undefined.
	f := one(t, Run(in), StateBroken)
	if !strings.Contains(f.Message, "is not defined") {
		t.Errorf("live message = %q", f.Message)
	}
	if !strings.Contains(f.Remedy.Fix, "re-add the ds:def") {
		t.Errorf("live remedy = %q", f.Remedy.Fix)
	}

	// Under a frozen run it is unrecorded, and the remedy names both
	// possibilities rather than guessing between them.
	in.SnapshotOnly = true
	f = one(t, Run(in), StateBroken)
	if !strings.Contains(f.Message, ledger.ForeignFile) || strings.Contains(f.Message, "is not defined") {
		t.Errorf("frozen message = %q", f.Message)
	}
	if !strings.Contains(f.Remedy.Fix, "ds sync") || !strings.Contains(f.Remedy.Fix, "fix the id in pkg/width.go:3") {
		t.Errorf("frozen remedy must offer both branches: %q", f.Remedy.Fix)
	}
	// Severity is unchanged: this is still an error, never a pass.
	if f.Severity != SeverityError || Run(in).ExitCode != 1 {
		t.Errorf("an unrecorded citation must still fail the run: %+v", f)
	}

	// A deleted local id keeps its own, more specific message even when
	// frozen: the snapshot has nothing to do with it.
	old := def("gone-h3v8n2wd", "a.go", 3, "func Gone() {}", nil)
	in.Refs = []block.Reference{ref("block", old.ID, "d.md", 1, nil)}
	in.Prev = ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("code", old)}}
	if f := one(t, Run(in), StateBroken); !strings.Contains(f.Message, "was deleted") {
		t.Errorf("a deleted local id = %q", f.Message)
	}
}

// TestForeignMoved pins the cross-repo `moved` state. It is scan-to-scan and
// needs a previous row; a citing repo's own ledger never had one for a
// foreign block, so until the committed snapshot existed a block that moved
// upstream could not be reported at all — it simply showed as `ok`.
func TestForeignMoved(t *testing.T) {
	t.Parallel()
	was := def("depth-k7m2p4xq", "spec/SPEC.md", 4, "At most 12 deep.", nil)
	now := def("depth-k7m2p4xq", "spec/SPEC.md", 8, "At most 12 deep.", nil)
	if was.Hash != now.Hash {
		t.Fatal("this test needs the content to be identical, only the position moved")
	}
	merged := ledger.FromBlock("docs", now).ToBlock()
	snapshot := ledger.FromBlock("docs", was)
	r := ref("block", now.ID, "pkg/depth.go", 3, nil)
	in := Input{
		Repo: "code", Now: now2, Merged: []block.Block{merged}, Refs: []block.Reference{r},
		PrevForeign: []ledger.Row{snapshot},
		Acks:        ackedAt(now.ID, "pkg/depth.go", 3, "code", now.Hash),
	}
	f := one(t, Run(in), StateMoved)
	if f.Severity != SeverityNone {
		t.Errorf("a move is never a problem: %+v", f)
	}
	for _, want := range []string{"spec/SPEC.md:4", "docs", "since the last sync"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message must contain %q: %q", want, f.Message)
		}
	}
	// Without the snapshot there is no previous position, so nothing moved.
	in.PrevForeign = nil
	if f := one(t, Run(in), StateOK); f.Message != "up to date" {
		t.Errorf("no snapshot = %+v", f)
	}
	// A frozen run compares the snapshot with itself: no move, by design.
	in.PrevForeign = []ledger.Row{ledger.FromBlock("docs", was)}
	in.Merged = []block.Block{ledger.FromBlock("docs", was).ToBlock()}
	in.Acks = ackedAt(now.ID, "pkg/depth.go", 3, "code", was.Hash)
	if f := one(t, Run(in), StateOK); f.State != StateOK {
		t.Errorf("frozen must report no move: %+v", f)
	}
	// A foreign block that moved AND changed reports the change, not the
	// move: coverage outranks position.
	changed := def("depth-k7m2p4xq", "spec/SPEC.md", 8, "At most 6 deep.", nil)
	in.Merged = []block.Block{ledger.FromBlock("docs", changed).ToBlock()}
	in.PrevForeign = []ledger.Row{snapshot}
	in.Acks = ackedAt(changed.ID, "pkg/depth.go", 3, "code", was.Hash)
	if f := one(t, Run(in), StateUnacked); f.State != StateUnacked {
		t.Errorf("a changed foreign block must report the change: %+v", f)
	}
	// A foreign id that is new to the snapshot is not a move.
	in.PrevForeign = nil
	in.Merged = []block.Block{merged}
	in.Acks = ackedAt(now.ID, "pkg/depth.go", 3, "code", now.Hash)
	if f := one(t, Run(in), StateOK); f.State != StateOK {
		t.Errorf("newly cited = %+v", f)
	}
}

// now2 is the fixed clock these cases use; `now` is already the package-level
// one and `now` is shadowed above by a block variable.
var now2 = now

// TestMovedCitationsAreFollowed runs the move rule through Run itself, with
// the real lookup of each citation's current hash. The ledger tests prove
// the rule over a stubbed lookup; this proves check feeds it the right one.
func TestMovedCitationsAreFollowed(t *testing.T) {
	t.Parallel()
	old := def("depth-k7m2p4xq", "a.md", 3, "At most 12 deep.", nil)
	nw := def("depth-k7m2p4xq", "a.md", 3, "At most 6 deep.", nil)
	sentenceless := func(id string, line int) block.Reference {
		r := ref("block", id, "d.md", line, nil)
		r.Carrier = block.CarrierBlock
		r.SetSentence("")
		return r
	}
	prevRow := func(id string, line int, seen string) ledger.RefRow {
		row := ledger.FromReference("api", sentenceless(id, line))
		row.SeenHash = seen
		return row
	}
	t.Run("a moved citation keeps its unreviewed change", func(t *testing.T) {
		t.Parallel()
		rep := Run(Input{
			Repo: "api", Now: now, Defs: []block.Block{nw},
			Refs:     []block.Reference{sentenceless(nw.ID, 5)},
			Prev:     ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", nw)}},
			PrevRefs: ledger.Refs{Rows: []ledger.RefRow{prevRow(nw.ID, 3, old.Hash)}},
		})
		if f := one(t, rep, StateUnacked); f.Line != 5 {
			t.Errorf("finding at line %d, want 5", f.Line)
		}
	})
	t.Run("ambiguous: every candidate reports when one had a change", func(t *testing.T) {
		t.Parallel()
		// Two sentence-less citations, one reviewed at the current hash and
		// one not, became three. Nothing says which is which.
		rep := Run(Input{
			Repo: "api", Now: now, Defs: []block.Block{nw},
			Refs: []block.Reference{sentenceless(nw.ID, 4), sentenceless(nw.ID, 8), sentenceless(nw.ID, 12)},
			Prev: ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", nw)}},
			PrevRefs: ledger.Refs{Rows: []ledger.RefRow{
				prevRow(nw.ID, 3, nw.Hash),
				prevRow(nw.ID, 7, old.Hash),
			}},
		})
		if got := len(byState(rep)[StateUnacked]); got != 3 {
			t.Errorf("unacked = %d, want 3: %+v", got, rep.Findings)
		}
	})
	t.Run("ambiguous with the block gone: still resolved, reported broken", func(t *testing.T) {
		t.Parallel()
		// The cited id no longer resolves, so there is no current hash to
		// measure candidates by; the move is settled without one and the
		// citations report what they are: broken.
		rep := Run(Input{
			Repo: "api", Now: now,
			Refs: []block.Reference{sentenceless("gone-h3v8n2wd", 4), sentenceless("gone-h3v8n2wd", 8), sentenceless("gone-h3v8n2wd", 12)},
			PrevRefs: ledger.Refs{Rows: []ledger.RefRow{
				prevRow("gone-h3v8n2wd", 3, nw.Hash),
				prevRow("gone-h3v8n2wd", 7, old.Hash),
			}},
		})
		if got := len(byState(rep)[StateBroken]); got != 3 {
			t.Errorf("broken = %d, want 3: %+v", got, rep.Findings)
		}
	})
}

// TestUnscannedFiles pins the report for a file the scan could not read:
// one that held citations or blocks is an error naming what goes unchecked
// and what to do about the reason; one that never held anything is not
// reported, since nothing in it was being checked.
func TestUnscannedFiles(t *testing.T) {
	t.Parallel()
	b := def("depth-k7m2p4xq", "internal/big.go", 3, "At most 12 deep.", nil)
	cite := ledger.FromReference("api", ref("block", b.ID, "docs/long.md", 3, nil))
	rep := Run(Input{
		Repo: "api", Now: now,
		Prev:     ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", b)}},
		PrevRefs: ledger.Refs{Rows: []ledger.RefRow{cite, cite}},
		Unreadable: []scan.Skip{
			{File: "docs/long.md", Reason: scan.SkipTooLarge},
			{File: "internal/big.go", Reason: scan.SkipLongLine},
			{File: "docs/never-cited.md", Reason: scan.SkipBinary},
		},
	})
	got := byState(rep)[StateUnscanned]
	if len(got) != 2 {
		t.Fatalf("unscanned = %+v, want the two files that held state", got)
	}
	for _, f := range got {
		if f.Severity != SeverityError {
			t.Errorf("%s: an unchecked citation must not pass: %s", f.Doc, f.Severity)
		}
		switch f.Doc {
		case "docs/long.md":
			if !strings.Contains(f.Message, "2 citations and 0 blocks") || !strings.Contains(f.Remedy.Fix, "max_file_kb") {
				t.Errorf("doc = %q / %q", f.Message, f.Remedy.Fix)
			}
		case "internal/big.go":
			if !strings.Contains(f.Message, "0 citations and 1 block ") || !strings.Contains(f.Remedy.Fix, "max_line_chars") {
				t.Errorf("code = %q / %q", f.Message, f.Remedy.Fix)
			}
		default:
			t.Errorf("unexpected %s", f.Doc)
		}
	}
	if rep.ExitCode == 0 {
		t.Error("an unscanned file that held citations must fail the check")
	}
	// Every reason has a hint, so no remedy reads "…: ; until it is…".
	for _, r := range scan.SkipReasonValues {
		if r.Unreadable() && unscannedHint[r] == "" {
			t.Errorf("%s has no remedy hint", r)
		}
	}
}

// TestClaimRenewalFollowsTheSentence pins the renewal of a claim that moved:
// a line added above it keeps the renewal, found by the sentence the ack
// recorded; a sentence that was rewritten does not inherit it.
func TestClaimRenewalFollowsTheSentence(t *testing.T) {
	t.Parallel()
	claim := func(line int, sentence string) block.Reference {
		r := block.Reference{Verb: "claim", Pos: block.Position{File: "d.md", Start: line, End: line}, Carrier: block.CarrierComment, Args: map[string]string{"reviewed": "2026-01-01", "expires": "30d"}}
		r.SetSentence(sentence)
		return r
	}
	renewed := claim(3, "We chose Postgres.")
	acks := ledger.Acks{Rows: []ledger.Ack{{At: now.Add(-24 * time.Hour), Repo: "api", Doc: "d.md", Line: 3, SentenceHash: renewed.SentenceHash}}}
	for _, tc := range []struct {
		name string
		ref  block.Reference
		want State
	}{
		{"unmoved", claim(3, "We chose Postgres."), StateOK},
		{"moved by a line above", claim(5, "We chose Postgres."), StateOK},
		{"rewritten", claim(5, "We moved to MySQL."), StateExpired},
		// A claim on a line of its own has no sentence to follow; if it
		// moves, its renewal cannot be told from another's, so it reports
		// expired — an extra review, never a hidden one.
		{"moved, no sentence", claim(5, ""), StateExpired},
	} {
		rep := Run(Input{Repo: "api", Now: now, Refs: []block.Reference{tc.ref}, Acks: acks})
		if got := one(t, rep, tc.want); got.State != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got.State, tc.want)
		}
	}
}

// TestChainCyclesOfEveryShape pins cycle handling beyond the two-block loop
// the conformance fixture has: a block that names itself, and a tail that
// runs into a loop — its root is unresolvable too — each report a broken
// chain and never recurse forever.
func TestChainCyclesOfEveryShape(t *testing.T) {
	t.Parallel()
	from := func(id, src string) block.Block {
		return def(id, "c.env", 1, id+"=x", map[string]string{"from": src})
	}
	for _, tc := range []struct {
		name string
		defs []block.Block
		want int
	}{
		{"self", []block.Block{from("self-k7m2p4xq", "self-k7m2p4xq")}, 1},
		{"tail into a loop", []block.Block{from("t-k7m2p4xq", "a-h3v8n2wd"), from("a-h3v8n2wd", "b-t4k2b9rf"), from("b-t4k2b9rf", "a-h3v8n2wd")}, 3},
	} {
		done := make(chan Report, 1)
		go func() { done <- Run(Input{Repo: "api", Now: now, Defs: tc.defs}) }()
		select {
		case rep := <-done:
			cycles := 0
			for _, f := range rep.Findings {
				if f.State == StateChainBroken && strings.Contains(f.Message, "cycle") {
					cycles++
				}
			}
			if cycles != tc.want {
				t.Errorf("%s: %d cycle findings, want %d: %+v", tc.name, cycles, tc.want, rep.Findings)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: chain resolution did not finish", tc.name)
		}
	}
}
