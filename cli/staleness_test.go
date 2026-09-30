package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ledger"
)

// staleSetup publishes api, has docs record and ack what it cites, and
// returns both repos ready for upstream to move on.
func staleSetup(t *testing.T) (api, docs, index string, apiVCS, docsVCS fakeVCS) {
	t.Helper()
	api, docs, index = twoRepos(t)
	apiVCS = fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS = fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("publish = %+v", r)
	}
	if r := run(t, docs, docsVCS, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	if r := run(t, docs, docsVCS, "sync"); r.code != 0 {
		t.Fatalf("sync = %+v", r)
	}
	if r := run(t, docs, docsVCS, "ack", "sess-save-k7m2p4xq", "--all"); r.code != 0 {
		t.Fatalf("ack = %+v", r)
	}
	return api, docs, index, apiVCS, docsVCS
}

// moveUpstream republishes api with a changed block.
func moveUpstream(t *testing.T, api string, v fakeVCS, body string) {
	t.Helper()
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn "+body+"\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if r := run(t, api, v, "publish"); r.code != 0 {
		t.Fatalf("republish = %+v", r)
	}
}

// TestStatusReportsSnapshotStaleness is the other half of --frozen: a pinned
// view is reproducible, and therefore ages silently. A frozen check stays
// green indefinitely while the repo drifts from the spec it claims to
// implement, so something has to say how far behind the pin is.
func TestStatusReportsSnapshotStaleness(t *testing.T) {
	t.Parallel()
	api, docs, _, apiVCS, docsVCS := staleSetup(t)

	// Freshly synced: up to date, and it says so per repo.
	r := run(t, docs, docsVCS, "status")
	if r.code != 0 {
		t.Fatalf("status = %+v", r)
	}
	if !strings.Contains(r.out, "snapshot") || !strings.Contains(r.out, "up to date") {
		t.Errorf("a fresh snapshot must say so: %s", r.out)
	}

	// Upstream moves on.
	moveUpstream(t, api, apiVCS, "s.sessions.Insert()")
	r = run(t, docs, docsVCS, "status")
	if r.code != 0 {
		t.Fatalf("staleness must never change the exit code: %+v", r)
	}
	for _, want := range []string{"1 of 2 cited blocks behind", "sess-save-k7m2p4xq"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("status must report %q: %s", want, r.out)
		}
	}
	// The change class is there, so a reader can judge urgency before
	// syncing — this is why the bodies are published.
	if !strings.Contains(r.out, "body") && !strings.Contains(r.out, "signature") {
		t.Errorf("status should classify the drift: %s", r.out)
	}

	// JSON carries the same, additively.
	r = run(t, docs, docsVCS, "status", "--json")
	for _, want := range []string{`"snapshot"`, `"behind": 1`, `"cited": 2`, `"compared": true`} {
		if !strings.Contains(r.out, want) {
			t.Errorf("json must contain %s: %s", want, r.out)
		}
	}

	// Syncing clears it.
	if r := run(t, docs, docsVCS, "sync"); r.code != 0 {
		t.Fatalf("sync = %+v", r)
	}
	if r := run(t, docs, docsVCS, "status"); !strings.Contains(r.out, "up to date") {
		t.Errorf("after syncing = %s", r.out)
	}
}

// TestStatusWithoutIndexSaysSkipped pins the honest degradation: where the
// comparison cannot be made, saying "up to date" would be a claim nobody
// checked.
func TestStatusWithoutIndexSaysSkipped(t *testing.T) {
	t.Parallel()
	v := fakeVCS{head: "d1", branch: "main", files: map[string][]byte{}}

	// A repo with a snapshot but no workspace configured: the age is known,
	// the comparison is not.
	dir := t.TempDir()
	write(t, dir, "docs/a.md", "# A\n")
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatalf("init = %+v", r)
	}
	st := NewStore(dir)
	if err := st.SaveForeign(ledger.Foreign{
		Header: ledger.Header{Repo: "docs", ScannedAt: clock.Add(-48 * time.Hour)},
		Rows:   []ledger.ForeignRow{{Row: ledger.Row{ID: "up-k7m2p4xq", Repo: "api", Hash: "aaaaaaaabbbbbbbb"}, Commit: "a1"}},
	}); err != nil {
		t.Fatal(err)
	}
	r := run(t, dir, v, "status")
	if r.code != 0 {
		t.Fatalf("an unavailable index must not fail status: %+v", r)
	}
	if !strings.Contains(r.out, "comparison skipped") {
		t.Errorf("status must say the comparison was skipped: %s", r.out)
	}
	if strings.Contains(r.out, "up to date") {
		t.Error("status must not claim up to date when it did not look")
	}
	if !strings.Contains(r.out, "synced ") {
		t.Errorf("the age must still be shown: %s", r.out)
	}

	// A repo the index does not publish is reported as uncomparable, not as
	// up to date: its blocks were never read.
	_, docs, index, _, docsVCS := staleSetup(t)
	if err := os.RemoveAll(index + "/repos/api"); err != nil {
		t.Fatal(err)
	}
	r = run(t, docs, docsVCS, "status")
	if r.code != 0 {
		t.Fatalf("status = %+v", r)
	}
	if !strings.Contains(r.out, "could not be compared") || strings.Contains(r.out, "up to date") {
		t.Errorf("an unpublished repo must not read as up to date: %s", r.out)
	}

	// A repo with no snapshot at all prints no snapshot section.
	plain := t.TempDir()
	write(t, plain, "docs/a.md", "# A\n")
	if r := run(t, plain, v, "init"); r.code != 0 {
		t.Fatalf("init = %+v", r)
	}
	if r := run(t, plain, v, "status"); r.code != 0 || strings.Contains(r.out, "snapshot") {
		t.Errorf("no snapshot = %+v", r)
	}
}

// TestSnapshotMaxAge pins the opt-in nudge: a warning, never a failure,
// because making age change the result would undo the reproducibility that
// --frozen exists to provide.
// promise:max-age-warning
func TestSnapshotMaxAge(t *testing.T) {
	t.Parallel()
	_, docs, _, _, docsVCS := staleSetup(t)

	// Off by default: no warning however old the snapshot is.
	ageSnapshot(t, docs, 90*24*time.Hour)
	if r := run(t, docs, docsVCS, "check", "--frozen"); strings.Contains(r.err, "snapshot_max_age") {
		t.Errorf("the nudge must be off by default: %s", r.err)
	}

	// Configured and exceeded: a warning on stderr, and the exit code is
	// whatever the findings say — age alone never fails the run.
	cfg, err := os.ReadFile(docs + "/.ds/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	write(t, docs, ".ds/config.toml", string(cfg)+"\n[check]\nsnapshot_max_age = \"30d\"\n")
	r := run(t, docs, docsVCS, "check", "--frozen")
	if !strings.Contains(r.err, "snapshot_max_age") || !strings.Contains(r.err, "ds sync") {
		t.Errorf("an aged snapshot must warn: %q", r.err)
	}
	if r.code != 0 {
		t.Errorf("a warning must not fail the run: %+v", r)
	}
	// Not even under --strict, which promotes warnings that are findings:
	// this one is not a finding, and §692 says age never changes the answer.
	if r := run(t, docs, docsVCS, "check", "--frozen", "--strict"); r.code != 0 || !strings.Contains(r.err, "snapshot_max_age") {
		t.Errorf("--strict must not turn snapshot age into a failure: %+v", r)
	}

	// Within the limit: quiet again.
	ageSnapshot(t, docs, time.Hour)
	if r := run(t, docs, docsVCS, "check", "--frozen"); strings.Contains(r.err, "snapshot_max_age") {
		t.Errorf("a fresh snapshot must be quiet: %s", r.err)
	}
	// And an unfrozen run never warns, because it just synced.
	ageSnapshot(t, docs, 90*24*time.Hour)
	if r := run(t, docs, docsVCS, "check"); strings.Contains(r.err, "snapshot_max_age") {
		t.Errorf("an unfrozen run must not warn: %s", r.err)
	}
	// A malformed duration is refused when the config is parsed.
	write(t, docs, ".ds/config.toml", string(cfg)+"\n[check]\nsnapshot_max_age = \"soon\"\n")
	if r := run(t, docs, docsVCS, "check", "--frozen"); r.code != ExitError || !strings.Contains(r.err, "snapshot_max_age") {
		t.Errorf("a bad duration must be refused: %+v", r)
	}
}

// ageSnapshot rewrites the snapshot's header time, which is what the nudge
// measures.
func ageSnapshot(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	st := NewStore(dir)
	f, ok, err := st.LoadForeign()
	if err != nil || !ok {
		t.Fatalf("snapshot = %v %v", ok, err)
	}
	f.Header.ScannedAt = clock.Add(-age)
	if err := st.SaveForeign(f); err != nil {
		t.Fatal(err)
	}
}

// TestPruneIndexGuards pins that the index store is only pruned from a
// position that can see who cites what. Pruning it from a stale view, or
// from a branch that is not the one that publishes, is how a body another
// repo still needs gets deleted.
func TestPruneIndexGuards(t *testing.T) {
	t.Parallel()
	_, docs, index, _, docsVCS := staleSetup(t)

	// Off the default branch.
	off := docsVCS
	off.branch = "feature"
	if r := run(t, docs, off, "prune", "--index"); r.code != ExitError || !strings.Contains(r.err, "default") {
		t.Errorf("off branch = %+v", r)
	}
	// Detached.
	detached := docsVCS
	detached.branch = ""
	if r := run(t, docs, detached, "prune", "--index"); r.code != ExitError {
		t.Errorf("detached = %+v", r)
	}
	// Without a workspace.
	plain := t.TempDir()
	write(t, plain, "docs/a.md", "# A\n")
	if r := run(t, plain, docsVCS, "init"); r.code != 0 {
		t.Fatalf("init = %+v", r)
	}
	if r := run(t, plain, docsVCS, "prune", "--index"); r.code != ExitError || !strings.Contains(r.err, "workspace") {
		t.Errorf("no workspace = %+v", r)
	}

	// On the default branch with a reachable index it runs, and a dry run
	// removes nothing.
	before := countIndexBodies(t, index)
	r := run(t, docs, docsVCS, "prune", "--index", "--keep", "0h", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "index:") {
		t.Fatalf("index dry run = %+v", r)
	}
	if countIndexBodies(t, index) != before {
		t.Error("--dry-run removed index bodies")
	}
	// A real run keeps every body the union of published state still needs.
	if r := run(t, docs, docsVCS, "prune", "--index", "--keep", "0h"); r.code != 0 {
		t.Fatalf("index prune = %+v", r)
	}
	if countIndexBodies(t, index) == 0 {
		t.Error("pruning the index must not empty it: live bodies are still cited")
	}
	// The citing repo can still classify, which is the point of keeping
	// another repo's acked body alive.
	if r := run(t, docs, docsVCS, "check"); r.code == ExitError {
		t.Errorf("check after an index prune = %+v", r)
	}
}

func countIndexBodies(t *testing.T, index string) int {
	t.Helper()
	n := 0
	entries, err := os.ReadDir(index + "/" + "repos")
	if err != nil {
		return 0
	}
	for _, e := range entries {
		blocks, err := os.ReadDir(index + "/repos/" + e.Name() + "/" + BlocksDir)
		if err != nil {
			continue
		}
		n += len(blocks)
	}
	return n
}

// TestStalenessRenderers covers the small formatting decisions: a repo that
// published without a commit still prints a readable row, and a drift whose
// bodies are unavailable is named `unknown` rather than left blank.
func TestStalenessRenderers(t *testing.T) {
	t.Parallel()
	if got := orMissingCommit(""); got != "-" {
		t.Errorf("no commit = %q", got)
	}
	if got := orMissingCommit("abc1234"); got != "abc1234" {
		t.Errorf("commit = %q", got)
	}
	if got := classList(nil); got != "unknown" {
		t.Errorf("no classes must read as unknown, got %q", got)
	}
	if got := classList([]block.Class{block.ClassMoved, block.ClassBody}); got != "moved, body" {
		t.Errorf("classes = %q", got)
	}
	// staleClasses is nil without a body store, and nil when only one side
	// is available: naming a class the bodies cannot support is a guess.
	row := ledger.ForeignRow{Row: ledger.Row{ID: "a-k7m2p4xq", Hash: "old", File: "a.go"}}
	now := block.Block{ID: "a-k7m2p4xq", Hash: "new", Pos: block.Position{File: "a.go"}}
	if got := staleClasses(row, now, nil); got != nil {
		t.Errorf("no store = %v", got)
	}
	half := func(h string) (string, bool) { return "x", h == "old" }
	if got := staleClasses(row, now, half); got != nil {
		t.Errorf("half a store = %v", got)
	}
	both := func(h string) (string, bool) {
		if h == "old" {
			return "func A() int { return 1 }", true
		}
		return "func A() int { return 2 }", true
	}
	if got := staleClasses(row, now, both); len(got) == 0 {
		t.Error("both bodies available must classify")
	}
	// A file with no known comment style still classifies, without prefixes.
	row.File, now.Pos.File = "a.unknownext", "a.unknownext"
	if got := staleClasses(row, now, both); len(got) == 0 {
		t.Error("an unknown extension must still classify")
	}
	// printStaleness prints nothing when there is no snapshot.
	var b strings.Builder
	printStaleness(&b, Staleness{})
	if b.String() != "" {
		t.Errorf("no snapshot = %q", b.String())
	}
}

// TestStalenessInternals covers the paths a command cannot reach: an index
// that errors, the sort order when several repos and blocks are stale, and
// the two guards on the age warning.
func TestStalenessInternals(t *testing.T) {
	t.Parallel()
	// An index that cannot be read leaves the comparison undone, which is
	// reported rather than silently treated as up to date.
	dir := t.TempDir()
	write(t, dir, "docs/a.md", "# A\n")
	v := fakeVCS{head: "d1", branch: "main", files: map[string][]byte{}}
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatalf("init = %+v", r)
	}
	st := NewStore(dir)
	if err := st.SaveForeign(ledger.Foreign{
		Header: ledger.Header{Repo: "docs", ScannedAt: clock},
		Rows:   []ledger.ForeignRow{{Row: ledger.Row{ID: "a-k7m2p4xq", Repo: "api", Hash: "aaa"}}},
	}); err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".ds/config.toml", "workspace = \"http://127.0.0.1:0/nope\"\n[scan]\ndocs = [\"docs/**\"]\n")
	cfg, err := st.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	a := testApp(dir, v)
	if got := a.staleness(cfg, nil); got.Compared {
		t.Errorf("an unreadable index must not count as compared: %+v", got)
	}

	// Ordering: repos and blocks both sort by name, so a resync reads as a
	// diff rather than a reshuffle.
	var b strings.Builder
	printStaleness(&b, Staleness{
		Present: true, Compared: true, Age: "1 day ago",
		Repos: []StaleRepo{
			{Repo: "api", Cited: 2, Behind: 2, Compared: true, Blocks: []StaleBlock{
				{ID: "a-k7m2p4xq", From: "aaa", To: "bbb"},
				{ID: "b-h3v8n2wd", From: "ccc", To: "ddd", Classes: []block.Class{block.ClassSignature}},
			}},
			{Repo: "docs", Cited: 1, Compared: true},
			{Repo: "web", Cited: 3},
		},
	})
	out := b.String()
	for _, want := range []string{"2 of 2 cited blocks behind", "a-k7m2p4xq", "signature", "docs     up to date", "could not be compared"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}

	// warnStaleSnapshot: a malformed duration is ignored here (the config
	// parser already refused it), and a missing snapshot says nothing.
	var w strings.Builder
	warnStaleSnapshot(&w, st, config.Config{Check: config.CheckConfig{SnapshotMaxAge: "soon"}}, clock)
	if w.Len() != 0 {
		t.Errorf("a bad duration must not produce a warning here: %q", w.String())
	}
	empty := NewStore(t.TempDir())
	warnStaleSnapshot(&w, empty, config.Config{Check: config.CheckConfig{SnapshotMaxAge: "1d"}}, clock)
	if w.Len() != 0 {
		t.Errorf("no snapshot = %q", w.String())
	}
}

// TestStalenessSortsStably pins the order the report is built in, so a
// resync reads as a diff rather than a reshuffle: repos by name, blocks by
// id within a repo.
func TestStalenessSortsStably(t *testing.T) {
	t.Parallel()
	api, docs, index, apiVCS, docsVCS := staleSetup(t)
	// A second upstream repo, so the repo sort has something to order.
	// The workspace file decides which repos merge, so a third publisher
	// has to be listed there too.
	write(t, index, "ds-workspace.toml", wsThreeRepos)
	web := t.TempDir()
	write(t, web, "src/a.ts", "// ds:def id=web-h3v8n2wd\nexport function A() { return 1 }\n")
	write(t, web, ".ds/config.toml", "workspace = "+strconvQuote(index)+"\n[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	webVCS := fakeVCS{head: "w1", branch: "main", remote: "https://github.com/org/web", files: map[string][]byte{}}
	if r := run(t, web, webVCS, "publish"); r.code != 0 {
		t.Fatalf("web publish = %+v", r)
	}
	// docs cites both upstream blocks plus the second api one.
	write(t, docs, "docs/runbook.md", "Writes go through [Save](ds:block?id=sess-save-k7m2p4xq). Tested by [it](ds:block?id=t-save-a2b6f8jk&assert=true). Web does [A](ds:block?id=web-h3v8n2wd).\n")
	if r := run(t, docs, docsVCS, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	if r := run(t, docs, docsVCS, "sync"); r.code != 0 {
		t.Fatalf("sync = %+v", r)
	}
	// Both upstreams move, api in two blocks.
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.sessions.Insert()\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) { _ = 1 }\n")
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("api republish = %+v", r)
	}
	write(t, web, "src/a.ts", "// ds:def id=web-h3v8n2wd\nexport function A() { return 2 }\n")
	if r := run(t, web, webVCS, "publish"); r.code != 0 {
		t.Fatalf("web republish = %+v", r)
	}
	r := run(t, docs, docsVCS, "status")
	if r.code != 0 {
		t.Fatalf("status = %+v", r)
	}
	// api before web, and within api the ids in order.
	iAPI, iWeb := strings.Index(r.out, "api "), strings.Index(r.out, "web ")
	if iAPI < 0 || iWeb < 0 || iAPI > iWeb {
		t.Errorf("repos must be ordered by name:\n%s", r.out)
	}
	iSess, iTest := strings.Index(r.out, "sess-save-k7m2p4xq "), strings.Index(r.out, "t-save-a2b6f8jk ")
	if iSess < 0 || iTest < 0 || iSess > iTest {
		t.Errorf("blocks must be ordered by id within a repo:\n%s", r.out)
	}
	if !strings.Contains(r.out, "2 of 2 cited blocks behind") {
		t.Errorf("both api blocks are behind:\n%s", r.out)
	}
}

// wsThreeRepos lists the publishers this file's fixtures merge.
const wsThreeRepos = `[workspace]
name = "platform"
repos = ["github.com/org/api", "github.com/org/docs", "github.com/org/web"]
default_branch = "main"
`

// TestStalenessReportsUpstreamDeletion pins the state that must never be
// quiet: the publishing repo no longer defines a cited id. It shared a
// branch with "unchanged" and so read as `up to date` — about a repo whose
// cited block had been deleted. The next `ds sync` turns those citations
// `broken`, which is exactly why hearing it beforehand is the point.
// promise:staleness-gone
func TestStalenessReportsUpstreamDeletion(t *testing.T) {
	t.Parallel()
	api, docs, _, apiVCS, docsVCS := staleSetup(t)
	// Upstream drops one of the two cited defs.
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.legacy.Save()\n}\n")
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("republish = %+v", r)
	}
	r := run(t, docs, docsVCS, "status")
	if r.code != 0 {
		t.Fatalf("status = %+v", r)
	}
	if strings.Contains(r.out, "up to date") {
		t.Errorf("a deleted cited block is not up to date:\n%s", r.out)
	}
	for _, want := range []string{"1 of 2 cited blocks behind", "t-save-a2b6f8jk", "gone", "broken"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("status must report %q:\n%s", want, r.out)
		}
	}
	// It is still only information: the exit code does not move.
	if r.code != 0 {
		t.Error("staleness must never change the exit code")
	}
	// JSON carries the state so a notifier can act on it.
	r = run(t, docs, docsVCS, "status", "--json")
	if !strings.Contains(r.out, `"gone": true`) {
		t.Errorf("json must carry the gone state: %s", r.out)
	}
	// A block that is merely behind is not marked gone.
	if strings.Count(r.out, `"gone": true`) != 1 {
		t.Errorf("only the deleted block is gone: %s", r.out)
	}
}

// TestStalenessPerEnvironment pins snapshot staleness for an id published
// once per environment: each snapshot row is measured against the same
// environment's def in the index. Keyed by id, the prod row was compared
// with whichever environment the index listed last and read as behind.
func TestStalenessPerEnvironment(t *testing.T) {
	t.Parallel()
	api, docs, _ := twoRepos(t)
	write(t, api, "config/a-prod.yaml", "port: 443 # ds:def id=port-k7m2p4xq env=prod\n")
	write(t, api, "config/b-dev.yaml", "port: 8080 # ds:def id=port-k7m2p4xq env=dev\n")
	write(t, docs, "docs/ports.md", "Prod [443](ds:cfg?id=port-k7m2p4xq&env=prod).\n\nDev [8080](ds:cfg?id=port-k7m2p4xq&env=dev).\n")
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
	for _, step := range []struct {
		dir  string
		v    fakeVCS
		args []string
	}{{api, apiVCS, []string{"publish"}}, {docs, docsVCS, []string{"scan"}}, {docs, docsVCS, []string{"sync"}}} {
		if r := run(t, step.dir, step.v, step.args...); r.code != 0 {
			t.Fatalf("%v = %+v", step.args, r)
		}
	}
	if r := run(t, docs, docsVCS, "status"); !strings.Contains(r.out, "up to date") {
		t.Errorf("a fresh per-environment snapshot must be up to date: %s", r.out)
	}
	// A real change to prod upstream is one block behind, not two.
	write(t, api, "config/a-prod.yaml", "port: 8443 # ds:def id=port-k7m2p4xq env=prod\n")
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatal(r)
	}
	r := run(t, docs, docsVCS, "status")
	if !strings.Contains(r.out, "1 of 4 cited blocks behind upstream") || strings.Count(r.out, "port-k7m2p4xq   ") != 1 {
		t.Errorf("only prod moved: %s", r.out)
	}
	if !strings.Contains(r.out, "docs/ports.md:1\tunacked") || !strings.Contains(r.out, "docs/ports.md:3\tok") {
		t.Errorf("the prod citation is unacked and the dev one is not: %s", r.out)
	}
}

// TestStalenessIgnoresPublishedBranches pins that a feature branch published
// to the index does not make a downstream snapshot of main look behind. The
// merged defs carry both, keyed by id they collided, and the branch — merged
// after main — was what main's snapshot row was measured against.
// promise:branch-apart
func TestStalenessIgnoresPublishedBranches(t *testing.T) {
	t.Parallel()
	api, docs, _, apiVCS, docsVCS := staleSetup(t)
	if r := run(t, docs, docsVCS, "status"); !strings.Contains(r.out, "up to date") {
		t.Fatalf("baseline = %s", r.out)
	}
	feature := apiVCS
	feature.branch = "feature"
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.branchOnly()\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if r := run(t, api, feature, "publish", "--branch"); r.code != 0 {
		t.Fatalf("publish --branch = %+v", r)
	}
	if r := run(t, docs, docsVCS, "status"); !strings.Contains(r.out, "up to date") || strings.Contains(r.out, "behind") {
		t.Errorf("a published feature branch must not make main look behind: %s", r.out)
	}
	if r := run(t, docs, docsVCS, "check"); r.code != 0 {
		t.Errorf("nor fail main's check: %+v", r)
	}
}
