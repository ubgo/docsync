package check

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/ledger"
)

// claimAbout is a claim on d.md:1 about ids, valid for ten years.
func claimAbout(ids string) block.Reference {
	return ref("claim", "", "d.md", 1, map[string]string{"owner": "@t", "reviewed": "2026-09-01", "expires": "3650d", "about": ids})
}

// claimInput is a check run in which `ds scan` has already recorded the
// current blocks (Prev equals Defs and Merged, so the change table is empty)
// and the claim's refs.tsv row carries seen as its baseline.
func claimInput(claim block.Reference, seen string, local, merged []block.Block, old ...block.Block) Input {
	row := ledger.FromReference("api", claim)
	row.SeenHash = seen
	var prev []ledger.Row
	for _, b := range local {
		prev = append(prev, ledger.FromBlock("api", b))
	}
	return Input{
		Repo: "api", Now: now, Defs: local, Merged: merged,
		Refs:     []block.Reference{claim},
		Prev:     ledger.Ledger{Rows: prev},
		PrevRefs: ledger.Refs{Rows: []ledger.RefRow{row}},
		Opts:     Options{BodyAt: bodies(append(append(append([]block.Block{}, old...), local...), merged...)...), CommentPrefixes: func(string) []string { return []string{"//"} }},
	}
}

// promise:claim-baseline-ack-only
// A claim about a block that changed stays expired after `ds scan` records
// the change: the claim is measured against the baseline in refs.tsv, not
// the scan-to-scan change table, which the scan has just emptied (bug 145).
// Only a renewal of the blocks as they are clears it.
func TestClaimAboutSurvivesAScan(t *testing.T) {
	t.Parallel()
	api := map[string]string{block.KeyStability: "api"}
	old := def("x-k7m2p4xq", "a.go", 3, "func X(a int) error {\n\treturn nil\n}", copyArgs(api))
	sig := def("x-k7m2p4xq", "a.go", 3, "func X(a string) error {\n\treturn nil\n}", copyArgs(api))
	body := def("x-k7m2p4xq", "a.go", 3, "func X(a int) error {\n\treturn errNope\n}", copyArgs(api))
	claim := claimAbout(old.ID)
	seen := AboutBaseline([]string{old.ID}, []block.Block{old})

	f := one(t, Run(claimInput(claim, seen, []block.Block{sig}, nil, old)), StateExpired)
	if f.ID != old.ID || len(f.Classes) != 1 || f.Classes[0] != block.ClassSignature || f.Message != "claim is about x-k7m2p4xq, which changed (signature)" {
		t.Errorf("signature change after a scan = %+v", f)
	}
	// A change the def's stability does not flag leaves the claim alone, as
	// it leaves a citation alone.
	one(t, Run(claimInput(claim, seen, []block.Block{body}, nil, old)), StateOK)
	// No store to classify from: unknown, which flags rather than passes.
	in := claimInput(claim, seen, []block.Block{sig}, nil)
	in.Opts.BodyAt = nil
	if f := one(t, Run(in), StateExpired); len(f.Classes) != 1 || f.Classes[0] != block.ClassUnknown {
		t.Errorf("no body store = %+v", f)
	}
	// Renewed against the blocks as they are now: reviewed.
	in = claimInput(claim, seen, []block.Block{sig}, nil, old)
	in.Acks = ledger.Acks{Rows: []ledger.Ack{{At: now, Actor: "k", ActorKind: ledger.ActorHuman, Repo: "api", Doc: "d.md", Line: 1, BlockHash: AboutBaseline([]string{old.ID}, []block.Block{sig})}}}
	one(t, Run(in), StateOK)
}

// Before any scan has recorded the change, the change table says what
// changed, classes and all, as it did before the baseline existed.
func TestClaimAboutReadsTheChangeTableWithoutABaseline(t *testing.T) {
	t.Parallel()
	old := def("x-k7m2p4xq", "a.go", 3, "func X(a int) error {\n\treturn nil\n}", nil)
	nw := def("x-k7m2p4xq", "a.go", 3, "func X(a string) error {\n\treturn nil\n}", nil)
	claim := claimAbout(old.ID)
	in := claimInput(claim, "", []block.Block{nw}, nil)
	in.Prev = ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", old)}}
	in.Opts.OldContent = func(ledger.Row) (string, bool) { return old.Content, true }
	if f := one(t, Run(in), StateExpired); f.ID != old.ID {
		t.Errorf("no baseline = %+v", f)
	}
	// With a baseline, the change table supplies the classes when it holds
	// exactly the change being measured.
	in.PrevRefs.Rows[0].SeenHash = AboutBaseline([]string{old.ID}, []block.Block{old})
	in.Opts.BodyAt = nil
	if f := one(t, Run(in), StateExpired); len(f.Classes) == 0 || f.Classes[0] == block.ClassUnknown {
		t.Errorf("classes from the change table = %+v", f)
	}
}

// A claim about a block another repository in the workspace publishes
// expires when that block changes: the local change table never held
// another repository's blocks, so the claim stayed ok (bug 146).
func TestClaimAboutForeignBlock(t *testing.T) {
	t.Parallel()
	foreign := map[string]string{block.KeyRepo: "code"}
	old := def("thing-api-a1b2c3d4", "src/thing.ts", 1, "export function makeThing(a: number): number {\n  return a + 1;\n}", copyArgs(foreign))
	nw := def("thing-api-a1b2c3d4", "src/thing.ts", 1, "export function makeThing(a: string): number {\n  return a + 1;\n}", copyArgs(foreign))
	claim := claimAbout(old.ID)
	seen := AboutBaseline([]string{old.ID}, nil, []block.Block{old})
	f := one(t, Run(claimInput(claim, seen, nil, []block.Block{nw}, old)), StateExpired)
	if f.ID != old.ID || !strings.Contains(f.Message, "which changed") {
		t.Errorf("foreign change = %+v", f)
	}
	one(t, Run(claimInput(claim, seen, nil, []block.Block{old})), StateOK)
	// about= names no branch: a change to a published branch's def of the
	// id is not what the claim was reviewed against.
	branch := nw
	branch.Args = copyArgs(nw.Args)
	branch.Args[block.KeyBranch] = "feature"
	one(t, Run(claimInput(claim, seen, nil, []block.Block{old, branch})), StateOK)
}

// A claim that cannot be told apart from others that moved (its sentence
// rewritten, several claims gone from the doc) takes the baseline that
// still reports a change, as a citation does: pairing it with the
// unchanged one would hide the change.
func TestMovedClaimTakesTheChangedBaseline(t *testing.T) {
	t.Parallel()
	old := def("x-k7m2p4xq", "a.go", 3, "func X(a int) error {\n\treturn nil\n}", nil)
	nw := def("x-k7m2p4xq", "a.go", 3, "func X(a string) error {\n\treturn nil\n}", nil)
	mk := func(line int, sentence string) block.Reference {
		c := claimAbout(old.ID)
		c.Pos.Start = line
		c.SetSentence(sentence)
		return c
	}
	in := claimInput(mk(9, "Rewritten."), "", []block.Block{nw}, nil, old)
	reviewed, stale := ledger.FromReference("api", mk(1, "First.")), ledger.FromReference("api", mk(2, "Second."))
	reviewed.SeenHash = AboutBaseline([]string{old.ID}, []block.Block{nw})
	stale.SeenHash = AboutBaseline([]string{old.ID}, []block.Block{old})
	in.PrevRefs = ledger.Refs{Rows: []ledger.RefRow{reviewed, stale}}
	one(t, Run(in), StateExpired)
}

// promise:claim-undefined-broken
// A claim about an id nothing defines is broken, never ok: it used to pass
// for good once its block was deleted, or from the start when the id was
// mistyped (bug 149). A renewal cannot clear it. An environment the claim
// was reviewed against that is no longer defined, while others are, expires
// it until a renewal records what is there now.
func TestClaimAboutUndefinedIsBroken(t *testing.T) {
	t.Parallel()
	old := def("x-k7m2p4xq", "a.go", 3, "func X() {}", nil)
	claim := claimAbout(old.ID)
	seen := AboutBaseline([]string{old.ID}, []block.Block{old})
	in := claimInput(claim, seen, nil, nil)
	in.Prev = ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", old)}}
	f := one(t, Run(in), StateBroken)
	if f.ID != old.ID || !strings.Contains(f.Message, "x-k7m2p4xq was deleted") || !strings.Contains(f.Remedy.Fix, "fix the id in d.md:1") {
		t.Errorf("deleted = %+v", f)
	}
	in.Acks = ledger.Acks{Rows: []ledger.Ack{{At: now, Actor: "k", ActorKind: ledger.ActorHuman, Repo: "api", Doc: "d.md", Line: 1, BlockHash: AboutHash([]string{old.ID})}}}
	one(t, Run(in), StateBroken)
	// Never defined, with no baseline at all.
	if f := one(t, Run(claimInput(claimAbout("x-k7m2p4xz"), "", []block.Block{old}, nil)), StateBroken); !strings.Contains(f.Message, "did you mean x-k7m2p4xq") {
		t.Errorf("mistyped = %+v", f)
	}

	prod := def(old.ID, "a.go", 3, "func X() {}", map[string]string{block.KeyEnv: "prod"})
	dev := def(old.ID, "b.go", 3, "func X() {}", map[string]string{block.KeyEnv: "dev"})
	both := AboutBaseline([]string{old.ID}, []block.Block{prod, dev})
	f = one(t, Run(claimInput(claim, both, []block.Block{dev}, nil)), StateExpired)
	if f.ID != old.ID || f.Message != "claim is about x-k7m2p4xq, which is no longer defined for env=prod" || !strings.Contains(f.Remedy.Fix, "ds ack --doc d.md --line 1") {
		t.Errorf("an environment removed = %+v", f)
	}
}

// A definition the claim was not reviewed against — another environment of
// an id it covers, or the def of an id just added to about= — changed what
// the claim is about, so it expires the claim until a renewal records it; it
// used to pass, and the next scan adopted it (bug 152).
func TestClaimAboutNewDefinitionExpires(t *testing.T) {
	t.Parallel()
	prod := def("x-k7m2p4xq", "a.go", 3, "func X() {}", map[string]string{block.KeyEnv: "prod"})
	dev := def("x-k7m2p4xq", "b.go", 3, "func X() {}", map[string]string{block.KeyEnv: "dev"})
	other := def("y-h3v8n2wd", "c.go", 3, "func Y() {}", nil)
	both := []string{prod.ID, other.ID}
	claim := claimAbout(prod.ID + "," + other.ID)
	onlyDev := AboutBaseline([]string{prod.ID}, []block.Block{dev})
	f := one(t, Run(claimInput(claim, onlyDev+","+AboutBaseline([]string{other.ID}, []block.Block{other}), []block.Block{prod, dev, other}, nil)), StateExpired)
	if f.ID != prod.ID || f.Message != "claim is about x-k7m2p4xq: a new definition of x-k7m2p4xq appeared (env=prod)" || !strings.Contains(f.Remedy.Fix, "ds ack --doc d.md --line 1") {
		t.Errorf("an environment added = %+v", f)
	}
	if f := one(t, Run(claimInput(claim, onlyDev, []block.Block{dev, other}, nil)), StateExpired); f.ID != other.ID || f.Message != "claim is about y-h3v8n2wd: a new definition of y-h3v8n2wd appeared" {
		t.Errorf("an id added to about= = %+v", f)
	}
	in := claimInput(claim, onlyDev, []block.Block{prod, dev, other}, nil)
	in.Acks = ledger.Acks{Rows: []ledger.Ack{{At: now, Actor: "k", ActorKind: ledger.ActorHuman, Repo: "api", Doc: "d.md", Line: 1, BlockHash: AboutBaseline(both, []block.Block{prod, dev, other})}}}
	one(t, Run(in), StateOK)
	// A scan carries the baseline as it is: nothing new joins it.
	if got := ExtendAboutBaseline(both, onlyDev, ""); got != onlyDev {
		t.Errorf("carried = %q", got)
	}
}

// A renewal records the per-def baseline, and the next change is measured
// against it, def by def: a body-only change to an `api` block after a
// renewal that accepted a signature change is not reported again. Measured
// against the first-seen baseline instead, it was (the over-report after a
// renewal).
func TestClaimRenewalIsThePerDefBaseline(t *testing.T) {
	t.Parallel()
	api := map[string]string{block.KeyStability: "api"}
	v0 := def("x-k7m2p4xq", "a.go", 3, "func X(a int) error {\n\treturn nil\n}", copyArgs(api))
	v1 := def("x-k7m2p4xq", "a.go", 3, "func X(a string) error {\n\treturn nil\n}", copyArgs(api))
	v2 := def("x-k7m2p4xq", "a.go", 3, "func X(a string) error {\n\treturn errNope\n}", copyArgs(api))
	v3 := def("x-k7m2p4xq", "a.go", 3, "func X(a bool) error {\n\treturn errNope\n}", copyArgs(api))
	ids := []string{v0.ID}
	renewedAt := func(b block.Block) ledger.Acks {
		return ledger.Acks{Rows: []ledger.Ack{{At: now, Actor: "k", ActorKind: ledger.ActorHuman, Repo: "api", Doc: "d.md", Line: 1, BlockHash: AboutBaseline(ids, []block.Block{b})}}}
	}
	seen := AboutBaseline(ids, []block.Block{v0})
	in := claimInput(claimAbout(v0.ID), seen, []block.Block{v2}, nil, v0, v1)
	in.Acks = renewedAt(v1)
	one(t, Run(in), StateOK)
	in = claimInput(claimAbout(v0.ID), seen, []block.Block{v3}, nil, v0, v1)
	in.Acks = renewedAt(v1)
	if f := one(t, Run(in), StateExpired); len(f.Classes) == 0 || f.Classes[0] != block.ClassSignature {
		t.Errorf("a signature change after the renewal = %+v", f)
	}
}

// A renewal recorded before per-def baselines holds one combined hash. While
// the blocks still match it the claim is reviewed; once they do not, the
// claim expires with class unknown, even when a baseline was written after
// the change, because that baseline would accept it.
func TestClaimLegacyRenewal(t *testing.T) {
	t.Parallel()
	v0 := def("x-k7m2p4xq", "a.go", 3, "func X(a int) error {\n\treturn nil\n}", nil)
	v1 := def("x-k7m2p4xq", "a.go", 3, "func X(a string) error {\n\treturn nil\n}", nil)
	ids := []string{v0.ID}
	legacy := ledger.Acks{Rows: []ledger.Ack{{At: now, Actor: "k", ActorKind: ledger.ActorHuman, Repo: "api", Doc: "d.md", Line: 1, BlockHash: AboutHash(ids, []block.Block{v0})}}}
	in := claimInput(claimAbout(v0.ID), "", []block.Block{v0}, nil)
	in.Acks = legacy
	one(t, Run(in), StateOK)
	in = claimInput(claimAbout(v0.ID), AboutBaseline(ids, []block.Block{v1}), []block.Block{v1}, nil)
	in.Acks = legacy
	if f := one(t, Run(in), StateExpired); f.ID != v0.ID || len(f.Classes) != 1 || f.Classes[0] != block.ClassUnknown || f.Message != "claim is about x-k7m2p4xq, which changed (unknown)" {
		t.Errorf("legacy renewal, blocks changed since = %+v", f)
	}
	if IsAboutBaseline(AboutHash(ids, []block.Block{v0})) || !IsAboutBaseline(AboutBaseline(ids, []block.Block{v0})) {
		t.Error("a combined hash and a per-def baseline must be told apart")
	}
}

// The baseline's encoding: one entry per default-branch def, kept as first
// recorded by ExtendAboutBaseline, extended with defs it has not seen, and
// trimmed to the ids the claim still names.
func TestAboutBaselineEncoding(t *testing.T) {
	t.Parallel()
	a := def("a-k7m2p4xq", "a.go", 1, "x", nil)
	b := def("b-h3v8n2wd", "b.go", 1, "y", nil)
	branch := def("a-k7m2p4xq", "a.go", 1, "z", map[string]string{block.KeyBranch: "feature"})
	if AboutBaseline(nil, []block.Block{a}) != "" || AboutBaseline([]string{"c-t4k2b9rf"}, []block.Block{a}) != "" {
		t.Error("no ids, or ids nothing defines, give no baseline")
	}
	base := AboutBaseline([]string{a.ID}, []block.Block{a, branch, b})
	if base != a.ID+"||"+a.Hash {
		t.Errorf("baseline = %q", base)
	}
	if got := AboutBaselineHashes(AboutBaseline([]string{a.ID, b.ID}, []block.Block{b, a})); len(got) != 2 {
		t.Errorf("hashes = %v", got)
	}
	changed := a
	changed.SetContent("changed")
	cur := AboutBaseline([]string{a.ID, b.ID}, []block.Block{changed, b})
	if got := ExtendAboutBaseline([]string{a.ID, b.ID}, base, cur); got != AboutBaseline([]string{a.ID, b.ID}, []block.Block{a, b}) {
		t.Errorf("extend kept the old entry and added the new def: %q", got)
	}
	if got := ExtendAboutBaseline([]string{b.ID}, base, AboutBaseline([]string{b.ID}, []block.Block{b})); got != AboutBaseline([]string{b.ID}, []block.Block{b}) {
		t.Errorf("an id dropped from about= drops its entries: %q", got)
	}
	if got := parseAboutBaseline("no-separator,x||,|h," + base); len(got) != 1 {
		t.Errorf("malformed entries are dropped: %v", got)
	}
}
