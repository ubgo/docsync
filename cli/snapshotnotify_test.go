package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ledger"
)

// defaultSnapshotCfg is the shipped policy.
func defaultSnapshotCfg() config.SnapshotNotifyConfig {
	return config.Default().Notify.Snapshot
}

// TestSnapshotTier pins the composition, which is the part of the policy
// with two natural readings: rank over the classes that flag INDIVIDUALLY
// for this block's stability, not over the change as a whole. Reading it
// the other way — rank everything, then filter — gives `api` blocks a
// different answer, so the table is the specification.
// Pins bug 9.
// promise:moved-no-drag
func TestSnapshotTier(t *testing.T) {
	t.Parallel()
	cfg := defaultSnapshotCfg()
	for _, tc := range []struct {
		name      string
		stability block.Stability
		classes   []block.Class
		want      string
	}{
		{"api ignores body", block.StabilityAPI, []block.Class{block.ClassBody}, config.TierNever},
		{"api on signature, body does not dilute", block.StabilityAPI, []block.Class{block.ClassSignature, block.ClassBody}, config.TierImmediate},
		{"stable comment and body", block.StabilityStable, []block.Class{block.ClassComment, block.ClassBody}, config.TierDigest},
		{"frozen comment", block.StabilityFrozen, []block.Class{block.ClassComment}, config.TierDigest},
		{"frozen comment and signature", block.StabilityFrozen, []block.Class{block.ClassComment, block.ClassSignature}, config.TierImmediate},
		{"volatile never", block.StabilityVolatile, []block.Class{block.ClassSignature, block.ClassBody}, config.TierNever},
		{"unknown is immediate under stable", block.StabilityStable, []block.Class{block.ClassUnknown}, config.TierImmediate},
		{"unknown is immediate under api", block.StabilityAPI, []block.Class{block.ClassUnknown}, config.TierImmediate},
		{"unknown is immediate under frozen", block.StabilityFrozen, []block.Class{block.ClassUnknown}, config.TierImmediate},
		{"unknown is silent under volatile", block.StabilityVolatile, []block.Class{block.ClassUnknown}, config.TierNever},
		{"moved alone never speaks", block.StabilityFrozen, []block.Class{block.ClassMoved}, config.TierNever},
		// These two are what separate the two readings of the policy. A
		// class that does not flag on its own must not be ranked just
		// because some other class in the same change did: `moved` is in
		// neither list, so ranking it would make it immediate and drag the
		// whole change up with it.
		{"moved does not drag a digest change up", block.StabilityStable, []block.Class{block.ClassBody, block.ClassMoved}, config.TierDigest},
		{"moved does not drag a comment change up", block.StabilityFrozen, []block.Class{block.ClassComment, block.ClassMoved}, config.TierDigest},
		{"no classes", block.StabilityStable, nil, config.TierNever},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := snapshotTier(StaleBlock{Classes: tc.classes}, tc.stability, cfg)
			if got != tc.want {
				t.Errorf("tier = %q, want %q", got, tc.want)
			}
		})
	}

	// A deletion is not a class: it has no second version, and its tier is
	// its own key.
	gone := StaleBlock{Gone: true}
	if got := snapshotTier(gone, block.StabilityStable, cfg); got != config.TierImmediate {
		t.Errorf("deletion defaults to immediate, got %q", got)
	}
	quiet := cfg
	quiet.OnDeleted = config.TierNever
	if got := snapshotTier(gone, block.StabilityStable, quiet); got != config.TierNever {
		t.Errorf("on_deleted must be honoured, got %q", got)
	}
	// A deletion is ranked even for a volatile block, because it is not a
	// change to the block's content: the citation is about to break.
	if got := snapshotTier(gone, block.StabilityVolatile, cfg); got != config.TierImmediate {
		t.Errorf("a deleted volatile block still breaks its citations, got %q", got)
	}

	// A class that flags but is in neither list is immediate. Guessing the
	// other way fails toward silence.
	neither := cfg
	neither.Immediate = []string{"signature"}
	neither.Digest = []string{"comment"}
	if got := snapshotTier(StaleBlock{Classes: []block.Class{block.ClassBody}}, block.StabilityStable, neither); got != config.TierImmediate {
		t.Errorf("an unlisted flagging class must be immediate, got %q", got)
	}
}

// TestSnapshotAlertKeys pins the dedupe keys. A deletion has no `to` hash
// and is a different event from any change to the same block, so it keys on
// the tier — a block notified for a signature change and then deleted
// notifies again.
func TestSnapshotAlertKeys(t *testing.T) {
	t.Parallel()
	changed := snapshotAlert{Repo: "api", ID: "a-k7m2p4xq", Tier: config.TierImmediate, To: "bbb"}
	deleted := snapshotAlert{Repo: "api", ID: "a-k7m2p4xq", Tier: config.TierImmediate, Gone: true}
	if changed.key() == deleted.key() {
		t.Error("a deletion must not dedupe against a change to the same block")
	}
	if !strings.HasSuffix(deleted.key(), tierGone) {
		t.Errorf("deletion key = %q", deleted.key())
	}
	// The tier is part of the key, so the same hash at a worse tier is new.
	worse := changed
	worse.Tier = config.TierDigest
	if changed.key() == worse.key() {
		t.Error("the tier must be part of the key")
	}
	// Both are snapshot keys, so the per-finding prune leaves them alone.
	for _, k := range []string{changed.key(), deleted.key(), noComparisonKey("api")} {
		if !isSnapshotKey(k) {
			t.Errorf("%q must be recognised as snapshot state", k)
		}
	}
	if isSnapshotKey("docs/a.md:3:x-k7m2p4xq:unacked:aaa") {
		t.Error("a finding key is not snapshot state")
	}
}

// TestSnapshotAlertsOrderAndCites checks what a reader gets: the most
// urgent first, and the places in this repo that point at each block.
// "3 blocks behind" with no file names is a message people learn to ignore.
func TestSnapshotAlertsOrderAndCites(t *testing.T) {
	t.Parallel()
	st := Staleness{Present: true, Compared: true, Repos: []StaleRepo{{
		Repo: "api", Cited: 3, Behind: 3, Compared: true,
		Blocks: []StaleBlock{
			{ID: "body-k7m2p4xq", From: "a1", To: "a2", Classes: []block.Class{block.ClassBody}},
			{ID: "sig-h3v8n2wd", From: "b1", To: "b2", Classes: []block.Class{block.ClassSignature}},
			{ID: "gone-t4k2b9rf", From: "c1", Gone: true},
		},
	}}}
	refs := ledger.Refs{Rows: []ledger.RefRow{
		{ID: "sig-h3v8n2wd", Doc: "docs/a.md", Line: 3},
		{ID: "sig-h3v8n2wd", Doc: "docs/b.md", Line: 9},
		{ID: "body-k7m2p4xq", Doc: "docs/a.md", Line: 5},
	}}
	stability := map[string]block.Stability{}
	alerts := snapshotAlerts(st, defaultSnapshotCfg(), refs, stability)
	if len(alerts) != 3 {
		t.Fatalf("alerts = %+v", alerts)
	}
	// gone outranks immediate outranks digest.
	if alerts[0].ID != "gone-t4k2b9rf" || alerts[1].ID != "sig-h3v8n2wd" || alerts[2].ID != "body-k7m2p4xq" {
		t.Errorf("order = %s %s %s", alerts[0].ID, alerts[1].ID, alerts[2].ID)
	}
	if len(alerts[1].Cites) != 2 || alerts[1].Cites[0] != "docs/a.md:3" {
		t.Errorf("cites = %v", alerts[1].Cites)
	}
	text := formatSnapshotAlerts(alerts, []string{"khanakia"})
	for _, want := range []string{
		"api moved under the pinned snapshot", "3 cited blocks behind",
		"gone (a sync will make its citations broken)",
		"cited at docs/a.md:3", "cited at docs/b.md:9",
		"owners: khanakia", "run `ds sync`",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message must contain %q:\n%s", want, text)
		}
	}
	// With no owners the message simply carries no owner line rather than
	// inventing one.
	if strings.Contains(formatSnapshotAlerts(alerts, nil), "owners:") {
		t.Error("no owners means no owner line")
	}
	// A volatile block contributes nothing at all.
	stability["sig-h3v8n2wd"] = block.StabilityVolatile
	quiet := snapshotAlerts(st, defaultSnapshotCfg(), refs, stability)
	for _, a := range quiet {
		if a.ID == "sig-h3v8n2wd" {
			t.Error("a volatile block must never notify")
		}
	}
}

// TestBlindRepos pins that not comparing is not the same as nothing to
// report: a repo the index stops publishing would otherwise read as no
// drift forever.
func TestBlindRepos(t *testing.T) {
	t.Parallel()
	st := Staleness{Present: true, Compared: true, Repos: []StaleRepo{
		{Repo: "api", Cited: 1, Compared: false},
		{Repo: "web", Cited: 1, Compared: true},
	}}
	state := map[string]Notified{}
	// Silent until the threshold, then once.
	for i := 1; i < missesBeforeWarning; i++ {
		if got := blindRepos(st, state); len(got) != 0 {
			t.Errorf("run %d spoke too early: %v", i, got)
		}
	}
	got := blindRepos(st, state)
	if len(got) != 1 || got[0] != "api" {
		t.Fatalf("threshold run = %v", got)
	}
	if again := blindRepos(st, state); len(again) != 0 {
		t.Errorf("it must say this once, not nightly: %v", again)
	}
	// A repo that compares again is forgotten, so a later outage is news.
	if _, held := state[noComparisonKey("web")]; held {
		t.Error("a comparable repo must hold no miss record")
	}
	st.Repos[0].Compared = true
	blindRepos(st, state)
	if _, held := state[noComparisonKey("api")]; held {
		t.Error("recovery must clear the counter")
	}
	text := formatBlindRepos([]string{"api"})
	for _, want := range []string{"could not compare", "staleness is unknown, not absent", "api"} {
		if !strings.Contains(text, want) {
			t.Errorf("message must contain %q:\n%s", want, text)
		}
	}
}

// TestClearResolved pins that a block the snapshot has caught up with is
// forgotten, so a later drift on it is news again.
func TestClearResolved(t *testing.T) {
	t.Parallel()
	state := map[string]Notified{
		"snapshot:api:a-k7m2p4xq:bbb:immediate": {Tier: config.TierImmediate},
		"snapshot:api:b-h3v8n2wd:ccc:digest":    {Tier: config.TierDigest},
		"snapshot-blind:api":                    {Misses: 2},
		"docs/a.md:3:x:unacked:aaa":             {},
	}
	st := Staleness{Repos: []StaleRepo{{Repo: "api", Blocks: []StaleBlock{{ID: "a-k7m2p4xq"}}}}}
	clearResolved(state, st)
	if _, held := state["snapshot:api:a-k7m2p4xq:bbb:immediate"]; !held {
		t.Error("a block still behind keeps its record")
	}
	if _, held := state["snapshot:api:b-h3v8n2wd:ccc:digest"]; held {
		t.Error("a block that caught up must be forgotten")
	}
	if _, held := state["snapshot-blind:api"]; !held {
		t.Error("the miss counter is not a per-block record")
	}
	if _, held := state["docs/a.md:3:x:unacked:aaa"]; !held {
		t.Error("finding state is not touched here")
	}
	// A malformed key is left alone rather than guessed at.
	state["snapshot:short"] = Notified{}
	clearResolved(state, st)
	if _, held := state["snapshot:short"]; !held {
		t.Error("a key that does not parse must be left alone")
	}
}

// TestCitingOwners pins the routing decision: snapshot messages go to the
// repo that must run `ds sync`, not to the team that made the change.
func TestCitingOwners(t *testing.T) {
	t.Parallel()
	cfg := config.Config{Owners: map[string][]string{"@docs": {"ana"}, "@other": {"bo"}}}
	local := ledger.Ledger{Rows: []ledger.Row{
		{ID: "local-k7m2p4xq", File: "docs/a.md", Owner: "@docs"},
		{ID: "elsewhere-h3v8n2wd", File: "docs/z.md", Owner: "@other"},
	}}
	alerts := []snapshotAlert{{Cites: []string{"docs/a.md:3"}}}
	got := citingOwners(cfg, local, alerts)
	if len(got) != 1 || got[0] != "ana" {
		t.Errorf("owners = %v, want the owner of the citing file", got)
	}
	// A citing file with no defs of its own yields nobody rather than a
	// guess.
	if got := citingOwners(cfg, local, []snapshotAlert{{Cites: []string{"docs/nobody.md:1"}}}); len(got) != 0 {
		t.Errorf("unowned file = %v", got)
	}
	// A cite with no line still resolves its file.
	if got := citingOwners(cfg, local, []snapshotAlert{{Cites: []string{"docs/a.md"}}}); len(got) != 1 {
		t.Errorf("cite without a line = %v", got)
	}
}

// snapshotNotifyFixture gives a citing repo with a fresh snapshot, an ack,
// and the notifier's memory already established, so the next run is an
// ordinary one rather than a first run.
func snapshotNotifyFixture(t *testing.T) (api, docs string, apiVCS, docsVCS fakeVCS) {
	t.Helper()
	api, docs, _, apiVCS, docsVCS = staleSetup(t)
	if r := run(t, docs, docsVCS, "notify"); r.code != 0 {
		t.Fatalf("notify = %+v", r)
	}
	return api, docs, apiVCS, docsVCS
}

// TestSnapshotNotifyEndToEnd is the policy working: a drift that matters
// speaks once, a drift that does not is silent, and syncing resolves it.
// promise:notify-once promise:notify-exit
func TestSnapshotNotifyEndToEnd(t *testing.T) {
	// Not parallel: notify reads the webhook from the environment.
	api, docs, apiVCS, docsVCS := snapshotNotifyFixture(t)

	// A body change on a `stable` block is digest tier, so it is recorded
	// and not said yet.
	moveUpstream(t, api, apiVCS, "s.sessions.Insert()")
	r := run(t, docs, docsVCS, "notify")
	if strings.Contains(r.out, "moved under the pinned snapshot") {
		t.Errorf("a digest-tier drift must wait: %s", r.out)
	}
	if r.code != 0 {
		t.Errorf("staleness must never change the exit code: %+v", r)
	}

	// Past digest_after it is worth mentioning.
	later := clock.Add(20 * 24 * time.Hour)
	out := runAt(t, docs, docsVCS, later, "notify")
	if !strings.Contains(out, "moved under the pinned snapshot") || !strings.Contains(out, "digest") {
		t.Errorf("after digest_after it must be said: %s", out)
	}
	// And only once.
	if again := runAt(t, docs, docsVCS, later, "notify"); strings.Contains(again, "moved under the pinned snapshot") {
		t.Errorf("said twice: %s", again)
	}

	// The same block changing again at the same tier is not news.
	moveUpstream(t, api, apiVCS, "s.sessions.Upsert()")
	if again := runAt(t, docs, docsVCS, later, "notify"); strings.Contains(again, "moved under the pinned snapshot") {
		t.Errorf("a second body edit is not news: %s", again)
	}

	// Worsening to a signature change is.
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save(force bool) error {\n\treturn nil\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("publish = %+v", r)
	}
	worse := runAt(t, docs, docsVCS, later, "notify")
	if !strings.Contains(worse, "immediate") || !strings.Contains(worse, "sess-save-k7m2p4xq") {
		t.Errorf("a worse tier is news: %s", worse)
	}
	// The message names where it is cited and what to do.
	if !strings.Contains(worse, "cited at docs/runbook.md:1") || !strings.Contains(worse, "ds sync") {
		t.Errorf("the message must be actionable: %s", worse)
	}

	// Syncing resolves it, and a later drift on the same block is news
	// afresh.
	if r := run(t, docs, docsVCS, "sync"); r.code != 0 {
		t.Fatalf("sync = %+v", r)
	}
	if r := run(t, docs, docsVCS, "ack", "sess-save-k7m2p4xq", "--all"); r.code != 0 {
		t.Fatalf("ack = %+v", r)
	}
	runAt(t, docs, docsVCS, later, "notify")
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save(force bool, why string) error {\n\treturn nil\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("publish = %+v", r)
	}
	if afresh := runAt(t, docs, docsVCS, later, "notify"); !strings.Contains(afresh, "moved under the pinned snapshot") {
		t.Errorf("a drift after a sync is news again: %s", afresh)
	}
}

// TestSnapshotNotifyDeletion pins the highest-priority case: it speaks
// before any sync has turned the citations broken, which is the whole value
// of hearing it.
func TestSnapshotNotifyDeletion(t *testing.T) {
	api, docs, apiVCS, docsVCS := snapshotNotifyFixture(t)
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.legacy.Save()\n}\n")
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("publish = %+v", r)
	}
	r := run(t, docs, docsVCS, "notify")
	if !strings.Contains(r.out, "gone") || !strings.Contains(r.out, "t-save-a2b6f8jk") {
		t.Errorf("a deleted cited block must speak immediately: %s", r.out)
	}
	if r.code != 0 {
		t.Errorf("it is still only a notification: %+v", r)
	}
	// Once.
	if again := run(t, docs, docsVCS, "notify"); strings.Contains(again.out, "gone") {
		t.Errorf("said twice: %s", again.out)
	}
}

// TestSnapshotNotifyDisabled covers the two ways it stays quiet.
func TestSnapshotNotifyDisabled(t *testing.T) {
	api, docs, apiVCS, docsVCS := snapshotNotifyFixture(t)
	moveUpstream(t, api, apiVCS, "s.sessions.Insert()")
	cfg, err := os.ReadFile(filepath.Join(docs, ".ds", ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	write(t, docs, ".ds/"+ConfigFile, string(cfg)+"\n[notify.snapshot]\nenabled = false\n")
	later := clock.Add(40 * 24 * time.Hour)
	if out := runAt(t, docs, docsVCS, later, "notify"); strings.Contains(out, "moved under the pinned snapshot") {
		t.Errorf("enabled = false must silence it: %s", out)
	}
	// A repo with no workspace has no snapshot to go stale.
	plain := t.TempDir()
	write(t, plain, "docs/a.md", "# A\n")
	v := fakeVCS{head: "d1", branch: "main", files: map[string][]byte{}}
	if r := run(t, plain, v, "init"); r.code != 0 {
		t.Fatalf("init = %+v", r)
	}
	if r := run(t, plain, v, "notify"); r.code != 0 || strings.Contains(r.out, "pinned snapshot") {
		t.Errorf("no workspace = %+v", r)
	}
}

// runAt runs a command at a given time and returns its combined output.
func runAt(t *testing.T, dir string, vcs VCS, at time.Time, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	Run(args, WithDir(dir), WithIO(strings.NewReader(""), &out, &out), WithVCS(vcs), WithClock(func() time.Time { return at }))
	return out.String()
}

// TestSnapshotAlertsSortAcrossRepos covers the ordering when two upstreams
// drift at once, and the guard on a reference with no id.
func TestSnapshotAlertsSortAcrossRepos(t *testing.T) {
	t.Parallel()
	st := Staleness{Present: true, Compared: true, Repos: []StaleRepo{
		{Repo: "web", Compared: true, Blocks: []StaleBlock{{ID: "w-k7m2p4xq", From: "a", To: "b", Classes: []block.Class{block.ClassSignature}}}},
		{Repo: "api", Compared: true, Blocks: []StaleBlock{{ID: "a-h3v8n2wd", From: "c", To: "d", Classes: []block.Class{block.ClassSignature}}}},
	}}
	refs := ledger.Refs{Rows: []ledger.RefRow{
		{ID: "", Doc: "docs/a.md", Line: 1}, // a page-level ref has no id
		{ID: "a-h3v8n2wd", Doc: "docs/a.md", Line: 2},
	}}
	got := snapshotAlerts(st, defaultSnapshotCfg(), refs, nil)
	if len(got) != 2 {
		t.Fatalf("alerts = %+v", got)
	}
	// Same tier, so repo name orders them.
	if got[0].Repo != "api" || got[1].Repo != "web" {
		t.Errorf("order = %s %s", got[0].Repo, got[1].Repo)
	}
	if len(citesByID(refs)[""]) != 0 {
		t.Error("a reference with no id cites nothing")
	}
}

// TestOrDefault covers the small fallback.
func TestOrDefault(t *testing.T) {
	t.Parallel()
	if got := orDefault("", "30d"); got != "30d" {
		t.Errorf("empty = %q", got)
	}
	if got := orDefault("7d", "30d"); got != "7d" {
		t.Errorf("set = %q", got)
	}
}

// TestSnapshotNotifyTimers covers the two waits: a digest-tier drift stays
// quiet until digest_after, and an immediate one that nobody resolved is
// raised again after escalate_after.
func TestSnapshotNotifyTimers(t *testing.T) {
	api, docs, apiVCS, docsVCS := snapshotNotifyFixture(t)

	// Digest tier: recorded, then still waiting on the next run.
	moveUpstream(t, api, apiVCS, "s.sessions.Insert()")
	if out := runAt(t, docs, docsVCS, clock, "notify"); strings.Contains(out, "pinned snapshot") {
		t.Errorf("recorded, not said: %s", out)
	}
	soon := clock.Add(24 * time.Hour)
	if out := runAt(t, docs, docsVCS, soon, "notify"); strings.Contains(out, "pinned snapshot") {
		t.Errorf("still inside digest_after: %s", out)
	}

	// Immediate tier, then escalation.
	api2, docs2, apiVCS2, docsVCS2 := snapshotNotifyFixture(t)
	write(t, api2, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save(force bool) error {\n\treturn nil\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if r := run(t, api2, apiVCS2, "publish"); r.code != 0 {
		t.Fatalf("publish = %+v", r)
	}
	if out := runAt(t, docs2, docsVCS2, clock, "notify"); !strings.Contains(out, "immediate") {
		t.Fatalf("an immediate drift speaks at once: %s", out)
	}
	if out := runAt(t, docs2, docsVCS2, clock, "notify"); strings.Contains(out, "pinned snapshot") {
		t.Errorf("and only once: %s", out)
	}
	// Still unresolved a fortnight later: raised again, once.
	late := clock.Add(14 * 24 * time.Hour)
	if out := runAt(t, docs2, docsVCS2, late, "notify"); !strings.Contains(out, "pinned snapshot") {
		t.Errorf("escalate_after must raise it again: %s", out)
	}
	if out := runAt(t, docs2, docsVCS2, late.Add(24*time.Hour), "notify"); strings.Contains(out, "pinned snapshot") {
		t.Errorf("escalation happens once: %s", out)
	}
}

// TestSnapshotNotifyFailures covers the paths where it stops or says
// nothing: no snapshot recorded yet, and state it cannot read.
func TestSnapshotNotifyFailures(t *testing.T) {
	// A workspace but no snapshot: nothing to compare, and no error.
	_, docs, _, docsVCS := snapshotNotifyFixture(t)
	if err := os.Remove(filepath.Join(docs, ".ds", ledger.ForeignFile)); err != nil {
		t.Fatal(err)
	}
	if r := run(t, docs, docsVCS, "notify"); r.code != 0 || strings.Contains(r.out, "pinned snapshot") {
		t.Errorf("no snapshot = %+v", r)
	}
	// Unreadable refs stop the command before this point, because loading
	// the committed state is what every command does first.
	_, docs2, _, docsVCS2 := snapshotNotifyFixture(t)
	write(t, docs2, ".ds/"+ledger.RefsFile, "# docsync refs format=99\n")
	if r := run(t, docs2, docsVCS2, "notify"); r.code != ExitError {
		t.Errorf("unreadable refs = %+v", r)
	}
	// An unparsable escalate_after stops the run and names the key. It
	// used to fall back to the default, so a typo ran with a setting
	// nobody chose.
	_, docs3, _, docsVCS3 := snapshotNotifyFixture(t)
	cfg, err := os.ReadFile(filepath.Join(docs3, ".ds", ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	write(t, docs3, ".ds/"+ConfigFile, string(cfg)+"\n[notify]\nescalate_after = \"weird\"\n")
	if r := run(t, docs3, docsVCS3, "notify"); r.code != ExitError || !strings.Contains(r.err, `notify.escalate_after "weird"`) {
		t.Errorf("a bad escalate_after must stop the run: %+v", r)
	}
}

// TestSnapshotNotifyChannels covers how a snapshot message leaves: printed
// on a dry run, posted to Slack when one is configured, and carried inside
// the single listing when the notifier has no memory.
func TestSnapshotNotifyChannels(t *testing.T) {
	// Not parallel: the webhook comes from the environment.
	api, docs, apiVCS, docsVCS := snapshotNotifyFixture(t)
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save(force bool) error {\n\treturn nil\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("publish = %+v", r)
	}
	// Dry: printed, and nothing recorded, so a real run still says it.
	r := run(t, docs, docsVCS, "notify", "--dry-run")
	if !strings.Contains(r.out, "pinned snapshot") {
		t.Fatalf("dry run = %+v", r)
	}
	if r := run(t, docs, docsVCS, "notify"); !strings.Contains(r.out, "pinned snapshot") {
		t.Errorf("a dry run must not consume the alert: %+v", r)
	}

	// Slack: posted as its own message, with state cleared first so this
	// run has something to say.
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		posts = append(posts, string(b))
	}))
	defer srv.Close()
	t.Setenv("DS_TEST_HOOK", srv.URL)
	cfg, err := os.ReadFile(filepath.Join(docs, ".ds", ConfigFile))
	if err != nil {
		t.Fatal(err)
	}
	write(t, docs, ".ds/"+ConfigFile, string(cfg)+"\n[notify]\nslack = \"$DS_TEST_HOOK\"\n")
	st := NewStore(docs)
	state, err := st.LoadNotified()
	if err != nil {
		t.Fatal(err)
	}
	for k := range state {
		if isSnapshotKey(k) {
			delete(state, k)
		}
	}
	if err := st.SaveNotified(state); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if code := Run([]string{"notify"}, WithDir(docs), WithIO(nil, &out, &out), WithVCS(docsVCS),
		WithHTTPClient(srv.Client()), WithClock(func() time.Time { return clock })); code != 0 {
		t.Fatalf("slack run = %d %s", code, out.String())
	}
	if !strings.Contains(out.String(), "notified snapshot staleness via slack") {
		t.Errorf("slack delivery = %s", out.String())
	}
	found := false
	for _, p := range posts {
		if strings.Contains(p, "pinned snapshot") {
			found = true
		}
	}
	if !found {
		t.Errorf("the snapshot message must reach the webhook: %v", posts)
	}
	// A webhook that fails surfaces.
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	state, _ = st.LoadNotified()
	for k := range state {
		if isSnapshotKey(k) {
			delete(state, k)
		}
	}
	if err := st.SaveNotified(state); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Run([]string{"notify"}, WithDir(docs), WithIO(nil, &out, &out), WithVCS(docsVCS),
		WithHTTPClient(srv.Client()), WithClock(func() time.Time { return clock })); code != ExitError {
		t.Errorf("a failing webhook must surface: %d %s", code, out.String())
	}
}

// TestSnapshotNotifyInsideReset pins that a run with no memory carries the
// snapshot alert inside its single listing rather than sending a second
// message beside it.
func TestSnapshotNotifyInsideReset(t *testing.T) {
	api, docs, apiVCS, docsVCS := snapshotNotifyFixture(t)
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save(force bool) error {\n\treturn nil\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("publish = %+v", r)
	}
	if err := os.Remove(filepath.Join(docs, ".ds", NotifiedFile)); err != nil {
		t.Fatal(err)
	}
	r := run(t, docs, docsVCS, "notify")
	if r.code != 0 {
		t.Fatalf("notify = %+v", r)
	}
	if !strings.Contains(r.out, "no previous notifier state") || !strings.Contains(r.out, "pinned snapshot") {
		t.Errorf("the listing must include the snapshot alert: %s", r.out)
	}
	if strings.Count(r.out, "no previous notifier state") != 1 {
		t.Errorf("still one message: %s", r.out)
	}
}

// TestSnapshotNotifyBlindEndToEnd pins the rule that not comparing is not
// the same as nothing to report. A repo the index stops publishing would
// otherwise read as no drift forever, which is the vacuous pass one layer
// up from the one `--frozen` already had.
func TestSnapshotNotifyBlindEndToEnd(t *testing.T) {
	_, docs, index, _, docsVCS := staleSetup(t)
	if r := run(t, docs, docsVCS, "notify"); r.code != 0 {
		t.Fatalf("notify = %+v", r)
	}
	// api stops publishing; the index itself is still reachable.
	if err := os.RemoveAll(filepath.Join(index, "repos", "api")); err != nil {
		t.Fatal(err)
	}
	said := 0
	for i := 1; i <= missesBeforeWarning+1; i++ {
		r := run(t, docs, docsVCS, "notify")
		if r.code != 0 {
			t.Fatalf("run %d = %+v", i, r)
		}
		if strings.Contains(r.out, "could not compare") {
			said++
			if i != missesBeforeWarning {
				t.Errorf("spoke on run %d, expected run %d", i, missesBeforeWarning)
			}
			if !strings.Contains(r.out, "staleness is unknown, not absent") {
				t.Errorf("the message must say what it means: %s", r.out)
			}
		}
	}
	if said != 1 {
		t.Errorf("it must say this once, not nightly: %d times", said)
	}
}
