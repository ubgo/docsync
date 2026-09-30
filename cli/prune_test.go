package cli

import (
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/workspace"
)

// pruneFixture gives a repo whose block has churned, so the store holds
// several superseded bodies alongside the live ones.
func pruneFixture(t *testing.T) (dir string, v fakeVCS, id string) {
	t.Helper()
	dir = t.TempDir()
	write(t, dir, "docs/spec.md", "# Spec\n\n### Depth\n\nAt most 12 deep.\n")
	v = fakeVCS{head: "abc1234", branch: "main", files: map[string][]byte{}}
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatalf("init = %+v", r)
	}
	r := run(t, dir, v, "def", "docs/spec.md#Depth", "--label", "depth")
	if r.code != 0 {
		t.Fatalf("def = %+v", r)
	}
	id = strings.TrimSpace(r.out)
	write(t, dir, "docs/guide.md", "See [d](ds:block?id="+id+").\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	if r := run(t, dir, v, "ack", id, "--all"); r.code != 0 {
		t.Fatalf("ack = %+v", r)
	}
	// Churn the block so superseded bodies pile up.
	for _, n := range []string{"11", "10", "9", "8"} {
		src, err := os.ReadFile(filepath.Join(dir, "docs/spec.md"))
		if err != nil {
			t.Fatal(err)
		}
		write(t, dir, "docs/spec.md", strings.Replace(string(src), "deep", n+" deep", 1))
		if r := run(t, dir, v, "scan"); r.code != 0 {
			t.Fatalf("scan = %+v", r)
		}
	}
	return dir, v, id
}

func bodyCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, DirName, BlocksDir))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// TestPruneKeepsEveryLiveSource is the safety property, one case per source.
// Each is proven by removing that source's hash from the live set and
// watching the body become prunable: if a source stopped counting, its
// bodies would be deleted while something still reads them.
func TestPruneKeepsEveryLiveSource(t *testing.T) {
	t.Parallel()
	dir, _, _ := pruneFixture(t)
	st := NewStore(dir)
	prev, refs, acks, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	// Each source contributes at least one hash, and every one is live.
	sources := map[string][]string{}
	for _, r := range prev.Rows {
		sources["ledger"] = append(sources["ledger"], r.Hash)
	}
	for _, r := range refs.Rows {
		if r.AckedHash != "" {
			sources["acked_hash"] = append(sources["acked_hash"], r.AckedHash)
		}
		if r.SeenHash != "" {
			sources["seen_hash"] = append(sources["seen_hash"], r.SeenHash)
		}
	}
	for _, a := range acks.Latest() {
		if a.BlockHash != "" {
			sources["acks"] = append(sources["acks"], a.BlockHash)
		}
	}
	// The foreign snapshot: written by hand, since this repo has no
	// workspace, so the source is still exercised.
	foreign := ledger.Foreign{Rows: []ledger.ForeignRow{{Row: ledger.Row{ID: "up-k7m2p4xq", Repo: "docs", Hash: "aaaaaaaabbbbbbbb"}, Commit: "d1"}}}
	if err := st.SaveForeign(foreign); err != nil {
		t.Fatal(err)
	}
	sources["foreign.tsv"] = []string{"aaaaaaaabbbbbbbb"}
	live, err := st.liveHashes()
	if err != nil {
		t.Fatal(err)
	}
	for name, hashes := range sources {
		if len(hashes) == 0 {
			t.Errorf("%s contributed no hash; this test is not exercising it", name)
			continue
		}
		for _, h := range hashes {
			if !live[h] {
				t.Errorf("%s hash %s is not live", name, short(h))
			}
		}
	}
	// And the complement really is removable: with no grace period the dead
	// bodies go and every live one stays.
	before := bodyCount(t, dir)
	dead, total, _, err := deadIn(st.path(BlocksDir), live, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(dead) == 0 || total != before {
		t.Fatalf("expected dead bodies among %d: %d", total, len(dead))
	}
	for _, d := range dead {
		if live[d.Hash] {
			t.Errorf("%s is live and was listed dead", short(d.Hash))
		}
	}
}

// TestPruneRemovesAndRespectsGrace covers the everyday path: a dry run
// writes nothing, the grace period keeps a recent body, and a real run
// removes only the complement of the live set.
func TestPruneRemovesAndRespectsGrace(t *testing.T) {
	t.Parallel()
	dir, v, _ := pruneFixture(t)
	before := bodyCount(t, dir)
	if before < 3 {
		t.Fatalf("fixture should have churned: %d bodies", before)
	}
	// Default grace keeps everything, because every body is new.
	r := run(t, dir, v, "prune", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "0 dead") {
		t.Errorf("default grace = %+v", r)
	}
	// With no grace, the dead are listed but a dry run removes nothing.
	r = run(t, dir, v, "prune", "--keep", "0h", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "dead") || !strings.Contains(r.out, "removed nothing") {
		t.Errorf("dry run = %+v", r)
	}
	if bodyCount(t, dir) != before {
		t.Error("--dry-run removed bodies")
	}
	// The real run.
	r = run(t, dir, v, "prune", "--keep", "0h")
	if r.code != 0 || !strings.Contains(r.out, "removed ") {
		t.Fatalf("prune = %+v", r)
	}
	after := bodyCount(t, dir)
	if after >= before {
		t.Errorf("prune removed nothing: %d -> %d", before, after)
	}
	// What survived is exactly the live set, and a check still classifies
	// precisely rather than degrading.
	st := NewStore(dir)
	live, err := st.liveHashes()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, DirName, BlocksDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !live[e.Name()] {
			t.Errorf("%s survived but is not live", short(e.Name()))
		}
	}
	if r := run(t, dir, v, "check", "--json"); !strings.Contains(r.out, `"body"`) {
		t.Errorf("a pruned store must still classify exactly: %s", r.out)
	}
	// Pruning again is a no-op.
	if r := run(t, dir, v, "prune", "--keep", "0h"); r.code != 0 || !strings.Contains(r.out, "0 dead") {
		t.Errorf("second prune = %+v", r)
	}
}

// TestPruneRefusesOnUnreadableState pins that a partial view never prunes.
// Guessing that an unreadable file named no hashes is how a live body gets
// deleted.
// promise:prune-unreadable
func TestPruneRefusesOnUnreadableState(t *testing.T) {
	t.Parallel()
	for _, file := range []string{ledger.RefsFile, ledger.ForeignFile} {
		dir, v, _ := pruneFixture(t)
		write(t, dir, ".ds/"+file, "# docsync "+strings.TrimSuffix(file, ".tsv")+" format=99\n")
		before := bodyCount(t, dir)
		r := run(t, dir, v, "prune", "--keep", "0h")
		if r.code != ExitError {
			t.Errorf("%s unreadable = %+v", file, r)
		}
		if bodyCount(t, dir) != before {
			t.Errorf("%s: a refused prune must remove nothing", file)
		}
	}
	// A bad --keep is a usage error, not a default.
	dir, v, _ := pruneFixture(t)
	if r := run(t, dir, v, "prune", "--keep", "soon"); r.code != ExitError || !strings.Contains(r.err, "keep") {
		t.Errorf("bad keep = %+v", r)
	}
	// Uninitialised.
	if r := run(t, t.TempDir(), v, "prune"); r.code != ExitError {
		t.Errorf("uninitialised = %+v", r)
	}
}

// TestPruneByHash covers the targeted removal §12 needs: a value that was
// secret before anyone marked it has no other removal path. It is
// destructive, so it needs --force and is recorded.
// promise:prune-by-hash
func TestPruneByHash(t *testing.T) {
	t.Parallel()
	dir, v, id := pruneFixture(t)
	st := NewStore(dir)
	_, refs, _, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	var acked string
	for _, r := range refs.Rows {
		if r.ID == id && r.AckedHash != "" {
			acked = r.AckedHash
		}
	}
	if acked == "" {
		t.Fatal("this test needs an acked hash")
	}
	// A live body is refused without --force, and the refusal says what
	// removing it would cost.
	r := run(t, dir, v, "prune", "--hash", acked)
	if r.code != ExitError || !strings.Contains(r.err, "still live") || !strings.Contains(r.err, "unknown") {
		t.Errorf("live without force = %+v", r)
	}
	// --dry-run writes nothing.
	before := bodyCount(t, dir)
	if r := run(t, dir, v, "prune", "--hash", acked, "--force", "--dry-run"); r.code != 0 || !strings.Contains(r.out, "would remove") {
		t.Errorf("dry run = %+v", r)
	}
	if bodyCount(t, dir) != before {
		t.Error("--dry-run removed a body")
	}
	// The real removal, recorded in the audit log.
	if r := run(t, dir, v, "prune", "--hash", acked, "--force"); r.code != 0 || !strings.Contains(r.out, "removed") {
		t.Fatalf("force = %+v", r)
	}
	if bodyCount(t, dir) != before-1 {
		t.Error("the body was not removed")
	}
	_, _, acks, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	last := acks.Rows[len(acks.Rows)-1]
	if !strings.Contains(last.Note, "prune") || !strings.Contains(last.Note, short(acked)) {
		t.Errorf("the removal must be auditable: %q", last.Note)
	}
	// The worst case of an over-eager prune: a finding with no diff, never
	// a wrong ok.
	r = run(t, dir, v, "check", "--json")
	if r.code != ExitFindings {
		t.Fatalf("the citation is still uncovered: %+v", r)
	}
	if !strings.Contains(r.out, `"unknown"`) {
		t.Errorf("a missing body must degrade to unknown: %s", r.out)
	}
	// A hash that is not a hash, and one that is absent.
	if r := run(t, dir, v, "prune", "--hash", "../etc/passwd", "--force"); r.code != ExitError {
		t.Errorf("non-hash = %+v", r)
	}
	if r := run(t, dir, v, "prune", "--hash", "eeeeeeeeffffffff", "--force"); r.code != 0 || !strings.Contains(r.out, "not in any body store") {
		t.Errorf("absent hash = %+v", r)
	}
}

// TestDoctorBlocksRow surfaces a store that has quietly grown.
func TestDoctorBlocksRow(t *testing.T) {
	t.Parallel()
	dir, v, _ := pruneFixture(t)
	r := run(t, dir, v, "doctor")
	if !strings.Contains(r.out, "blocks") || !strings.Contains(r.out, "live") {
		t.Fatalf("doctor = %+v", r)
	}
	// The fixture churned five versions against two live, so most of the
	// store is unreachable and doctor should be suggesting a prune.
	if !strings.Contains(r.out, "ds prune") {
		t.Errorf("a store that is mostly dead should suggest a prune: %s", r.out)
	}
	if r := run(t, dir, v, "prune", "--keep", "0h"); r.code != 0 {
		t.Fatalf("prune = %+v", r)
	}
	if r := run(t, dir, v, "doctor"); strings.Contains(r.out, "ds prune") {
		t.Errorf("after pruning it should be quiet: %s", r.out)
	}
	// An unreadable state file makes the row a warning, not a crash.
	write(t, dir, ".ds/"+ledger.RefsFile, "# docsync refs format=99\n")
	if r := run(t, dir, v, "doctor"); !strings.Contains(r.out, "blocks") {
		t.Errorf("doctor must still print the row: %s", r.out)
	}
}

// TestBlocksRowThreshold pins the boundary rather than leaving it to a
// fixture's size: doctor speaks when more than half the store is
// unreachable, and stays quiet at exactly half.
func TestBlocksRowThreshold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		total, live int
		warn        bool
	}{
		{total: 2, live: 2, warn: false},
		{total: 4, live: 2, warn: false}, // exactly the ratio
		{total: 5, live: 2, warn: true},  // past it
		{total: 0, live: 0, warn: false}, // an empty store is not a problem
	} {
		got := tc.live > 0 && tc.total > tc.live*pruneRatio
		if got != tc.warn {
			t.Errorf("%d/%d live: warn = %v, want %v", tc.live, tc.total, got, tc.warn)
		}
	}
}

// TestDeadInEdgeCases covers the directory walk's guards: an absent store is
// empty rather than an error, an unreadable one is an error rather than an
// empty live set, and anything that is not a body is ignored so a stray file
// is never deleted.
func TestDeadInEdgeCases(t *testing.T) {
	t.Parallel()
	// Absent: nothing to prune, no error.
	dead, total, _, err := deadIn(filepath.Join(t.TempDir(), "nope"), nil, 0, time.Now())
	if err != nil || dead != nil || total != 0 {
		t.Errorf("absent store = %v %d %v", dead, total, err)
	}
	// A file where the directory should be: an error, never an empty set,
	// because an empty set would mean "everything is dead".
	dir := t.TempDir()
	blocks := filepath.Join(dir, BlocksDir)
	if err := os.WriteFile(blocks, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := deadIn(blocks, nil, 0, time.Now()); err == nil {
		t.Error("an unreadable store must be an error")
	}
	// Stray entries are not bodies and are never counted or removed.
	store := t.TempDir()
	if err := os.Mkdir(filepath.Join(store, "aaaaaaaacccccccc"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README", "notahash", "deadbeef.tmp"} {
		if err := os.WriteFile(filepath.Join(store, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(store, "aaaaaaaabbbbbbbb"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	dead, total, _, err = deadIn(store, nil, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(dead) != 1 || dead[0].Hash != "aaaaaaaabbbbbbbb" {
		t.Errorf("only bodies count: total=%d dead=%+v", total, dead)
	}
	// The grace period is measured from the file's own mtime.
	old := filepath.Join(store, "ccccccccdddddddd")
	if err := os.WriteFile(old, []byte("older"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(old, time.Now().Add(-90*24*time.Hour), time.Now().Add(-90*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	dead, _, _, err = deadIn(store, nil, 30*24*time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(dead) != 1 || dead[0].Hash != "ccccccccdddddddd" {
		t.Errorf("only the aged body is prunable: %+v", dead)
	}
}

// TestPruneCommandFailures covers the paths where prune must stop rather
// than guess: an unreadable config, and a store it cannot walk.
func TestPruneCommandFailures(t *testing.T) {
	t.Parallel()
	dir, v, _ := pruneFixture(t)
	// A file squatting on the blocks directory.
	blocks := filepath.Join(dir, DirName, BlocksDir)
	if err := os.RemoveAll(blocks); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocks, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, v, "prune", "--keep", "0h"); r.code != ExitError {
		t.Errorf("unwalkable store = %+v", r)
	}
	os.Remove(blocks)
	// An unreadable config.
	dir2, v2, _ := pruneFixture(t)
	write(t, dir2, ".ds/config.toml", "[scan\n")
	if r := run(t, dir2, v2, "prune"); r.code != ExitError {
		t.Errorf("bad config = %+v", r)
	}
}

// TestPruneByHashAcrossStores covers --hash --index: the same body can sit
// in this repo's store and in the index, and a targeted removal has to clear
// both or the value it was removing is still published.
func TestPruneByHashAcrossStores(t *testing.T) {
	t.Parallel()
	api, _, index, apiVCS, _ := staleSetup(t)
	// publish writes bodies into the index; the local store is scan's job.
	if r := run(t, api, apiVCS, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	st := NewStore(api)
	entries, err := os.ReadDir(filepath.Join(index, "repos", "api", BlocksDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("this test needs a published body")
	}
	hash := entries[0].Name()
	if _, err := os.Stat(filepath.Join(api, DirName, BlocksDir, hash)); err != nil {
		t.Fatalf("the body should also be in the local store: %v", err)
	}
	// A dry run names both stores and removes nothing.
	r := run(t, api, apiVCS, "prune", "--hash", hash, "--force", "--index", "--dry-run")
	if r.code != 0 || strings.Count(r.out, "would remove") != 2 {
		t.Fatalf("dry run across stores = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(index, "repos", "api", BlocksDir, hash)); err != nil {
		t.Error("--dry-run removed the published body")
	}
	// The real removal clears both.
	r = run(t, api, apiVCS, "prune", "--hash", hash, "--force", "--index")
	if r.code != 0 || strings.Count(r.out, "removed ") != 2 {
		t.Fatalf("removal across stores = %+v", r)
	}
	for _, p := range []string{
		filepath.Join(api, DirName, BlocksDir, hash),
		filepath.Join(index, "repos", "api", BlocksDir, hash),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s survived", p)
		}
	}
	// And it is recorded once, naming both stores.
	_, _, acks, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	last := acks.Rows[len(acks.Rows)-1]
	if !strings.Contains(last.Note, "2 store(s)") {
		t.Errorf("the audit note must say how many stores: %q", last.Note)
	}
}

// TestPruneIndexRemoves covers the index walk actually removing something,
// and that the union of published state is what protects a body: one repo's
// ack keeps alive a body only the defining repo's directory holds.
func TestPruneIndexRemoves(t *testing.T) {
	t.Parallel()
	api, docs, index, apiVCS, docsVCS := staleSetup(t)
	// Upstream churns, so the index accumulates superseded bodies.
	for _, body := range []string{"s.sessions.Insert()", "s.sessions.Upsert()"} {
		moveUpstream(t, api, apiVCS, body)
	}
	before := countIndexBodies(t, index)
	if before < 3 {
		t.Fatalf("expected churn in the index: %d", before)
	}
	// docs still cites and acked the original, which only api's directory
	// holds: the union of published state has to keep it.
	st := NewStore(docs)
	_, _, acks, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	var acked string
	for _, a := range acks.Latest() {
		if a.ID == "sess-save-k7m2p4xq" && a.BlockHash != "" {
			acked = a.BlockHash
		}
	}
	if acked == "" {
		t.Fatal("this test needs docs to have acked an api block")
	}
	if r := run(t, docs, docsVCS, "publish"); r.code != 0 {
		t.Fatalf("docs publish = %+v", r)
	}
	r := run(t, api, apiVCS, "prune", "--index", "--keep", "0h")
	if r.code != 0 || !strings.Contains(r.out, "index:") {
		t.Fatalf("index prune = %+v", r)
	}
	if countIndexBodies(t, index) >= before {
		t.Errorf("nothing was pruned from the index: %d -> %d", before, countIndexBodies(t, index))
	}
	if _, err := os.Stat(filepath.Join(index, "repos", "api", BlocksDir, acked)); err != nil {
		t.Error("a body another repo acked must survive a prune of the defining repo's directory")
	}
}

// TestPruneResidualPaths covers the remaining branches: a body that cannot
// be stat'd, a store that cannot be written to, the offline index, and the
// two small renderers.
func TestPruneResidualPaths(t *testing.T) {
	t.Parallel()

	// A dangling link named like a body is skipped, not reported and then
	// failed on.
	store := t.TempDir()
	if err := os.Symlink(filepath.Join(store, "gone"), filepath.Join(store, "aaaaaaaabbbbbbbb")); err != nil {
		t.Fatal(err)
	}
	dead, total, _, err := deadIn(store, nil, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(dead) != 0 {
		t.Errorf("a dangling body must be counted but not prunable: total=%d dead=%+v", total, dead)
	}

	// report: a body that cannot be removed surfaces.
	ro := t.TempDir()
	body := filepath.Join(ro, "ccccccccdddddddd")
	if err := os.WriteFile(body, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeUnremovable(t, ro)
	err = report(io.Discard, ro, []deadBody{{Hash: "ccccccccdddddddd", Path: body}}, 1, 0, 0, DefaultKeep, false)
	if err == nil {
		t.Error("a body that cannot be removed must surface")
	}

	// roundDays reads as a person would.
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{3 * time.Hour, "3h"},
		{25 * time.Hour, "1d"},
		{90 * 24 * time.Hour, "90d"},
	} {
		if got := roundDays(tc.d); got != tc.want {
			t.Errorf("roundDays(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}

	// indexEntriesFor: no workspace means no stores beyond the local one.
	dir, _, _ := pruneFixture(t)
	a := testApp(dir, fakeVCS{head: "abc1234", branch: "main", files: map[string][]byte{}})
	cfg, err := NewStore(dir).LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := a.indexEntriesFor(cfg)
	if err != nil || entries != nil {
		t.Errorf("no workspace = %+v %v", entries, err)
	}

	// blocksRow degrades to a warning when the state cannot be read.
	write(t, dir, ".ds/"+ledger.RefsFile, "# docsync refs format=99\n")
	if row := a.blocksRow(NewStore(dir)); row[1] != "WARN" {
		t.Errorf("unreadable state = %v", row)
	}
	// And when the store itself cannot be walked.
	dir2, _, _ := pruneFixture(t)
	a2 := testApp(dir2, fakeVCS{head: "abc1234", branch: "main", files: map[string][]byte{}})
	blocks := filepath.Join(dir2, DirName, BlocksDir)
	if err := os.RemoveAll(blocks); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocks, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if row := a2.blocksRow(NewStore(dir2)); row[1] != "WARN" {
		t.Errorf("unwalkable store = %v", row)
	}
}

// TestPruneIndexOffline pins that a stale view never prunes the index:
// deleting from a picture of who cites what that is known to be out of date
// is precisely how a live body is lost.
func TestPruneIndexOffline(t *testing.T) {
	t.Parallel()
	_, docs, index, _, docsVCS := staleSetup(t)
	if err := os.RemoveAll(index); err != nil {
		t.Fatal(err)
	}
	r := run(t, docs, docsVCS, "prune", "--index", "--keep", "0h")
	if r.code != ExitError {
		t.Errorf("an unreachable index must refuse the prune: %+v", r)
	}
}

// TestPruneInternalFailures covers the error returns that only a direct call
// can reach: each is a refusal, and a refusal is the correct outcome, since
// pruning on a partial view is how a live body is lost.
func TestPruneInternalFailures(t *testing.T) {
	t.Parallel()
	newApp := func(dir string) (*App, *Store, config.Config) {
		t.Helper()
		a := testApp(dir, fakeVCS{head: "abc1234", branch: "main", files: map[string][]byte{}})
		st := NewStore(dir)
		cfg, err := st.LoadConfig()
		if err != nil {
			t.Fatal(err)
		}
		return a, st, cfg
	}

	// pruneOne: the live set cannot be computed.
	dir, _, _ := pruneFixture(t)
	a, st, cfg := newApp(dir)
	write(t, dir, ".ds/"+ledger.RefsFile, "# docsync refs format=99\n")
	if err := a.pruneOne(io.Discard, st, cfg, "aaaaaaaabbbbbbbb", false, false, false); err == nil {
		t.Error("pruneOne must refuse when the live set is unknown")
	}

	// pruneOne: the snapshot is unreadable. The other state parses, so this
	// is the branch where liveness specifically cannot be completed.
	dirF, _, _ := pruneFixture(t)
	aF, stF, cfgF := newApp(dirF)
	write(t, dirF, ".ds/"+ledger.ForeignFile, "# docsync foreign format=99\n")
	if err := aF.pruneOne(io.Discard, stF, cfgF, "aaaaaaaabbbbbbbb", false, false, false); err == nil {
		t.Error("an unreadable snapshot must refuse the prune")
	}

	// pruneOne: the index cannot be read.
	dir2, _, _ := pruneFixture(t)
	a2, st2, _ := newApp(dir2)
	write(t, dir2, ".ds/config.toml", "workspace = \"http://127.0.0.1:0/nope\"\n[scan]\ndocs = [\"docs/**\"]\n")
	cfg2, err := st2.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := a2.pruneOne(io.Discard, st2, cfg2, "aaaaaaaabbbbbbbb", false, true, true); err == nil {
		t.Error("pruneOne --index must refuse when the index cannot be read")
	}
	if _, err := a2.indexEntriesFor(cfg2); err == nil {
		t.Error("indexEntriesFor must surface an unreadable index")
	}

	// pruneOne: the body cannot be removed.
	dir3, _, _ := pruneFixture(t)
	a3, st3, cfg3 := newApp(dir3)
	entries, err := os.ReadDir(filepath.Join(dir3, DirName, BlocksDir))
	if err != nil || len(entries) == 0 {
		t.Fatalf("fixture = %v %v", entries, err)
	}
	hash := entries[0].Name()
	blocks := filepath.Join(dir3, DirName, BlocksDir)
	makeUnremovable(t, blocks)
	if err := a3.pruneOne(io.Discard, st3, cfg3, hash, false, true, false); err == nil {
		t.Error("an unremovable body must surface")
	}
	_ = os.Chmod(blocks, 0o755)

	// pruneOne: the audit entry cannot be written because the log is
	// unreadable. The body is gone by then, so the error is the honest
	// outcome rather than a silent success.
	dir4, _, _ := pruneFixture(t)
	a4, st4, cfg4 := newApp(dir4)
	entries, err = os.ReadDir(filepath.Join(dir4, DirName, BlocksDir))
	if err != nil || len(entries) == 0 {
		t.Fatal("fixture")
	}
	dead := ""
	live4, err := st4.liveHashes()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !live4[e.Name()] {
			dead = e.Name()
		}
	}
	if dead == "" {
		t.Fatal("this test needs a dead body")
	}
	write(t, dir4, ".ds/"+ledger.AcksFile, "# docsync acks format=99\n")
	if err := a4.pruneOne(io.Discard, st4, cfg4, dead, false, false, false); err == nil {
		t.Error("an unwritable audit log must surface")
	}

	// indexLive unions every published repo, and an empty index is empty
	// rather than an error.
	if got := indexLive(nil); len(got) != 0 {
		t.Errorf("empty index = %v", got)
	}
	got := indexLive([]workspace.Entry{{
		Repo:   "api",
		Ledger: ledger.Ledger{Rows: []ledger.Row{{ID: "a-k7m2p4xq", Hash: "aaa"}}},
		Refs:   ledger.Refs{Rows: []ledger.RefRow{{ID: "a-k7m2p4xq", AckedHash: "bbb", SeenHash: "ccc"}, {ID: "b", AckedHash: ""}}},
	}})
	for _, h := range []string{"aaa", "bbb", "ccc"} {
		if !got[h] {
			t.Errorf("%s must be live", h)
		}
	}
	if got[""] {
		t.Error("the empty hash is not a body")
	}
}

// TestPruneIndexRemovalFailure pins that a body the index will not give up
// stops the prune rather than being reported as removed.
func TestPruneIndexRemovalFailure(t *testing.T) {
	t.Parallel()
	api, _, index, apiVCS, _ := staleSetup(t)
	moveUpstream(t, api, apiVCS, "s.sessions.Insert()")
	blocks := filepath.Join(index, "repos", "api", BlocksDir)
	makeUnremovable(t, blocks)
	if r := run(t, api, apiVCS, "prune", "--index", "--keep", "0h"); r.code != ExitError {
		t.Errorf("an unremovable index body must stop the prune: %+v", r)
	}
	// A dry run over the same store still reports, because it removes
	// nothing.
	if r := run(t, api, apiVCS, "prune", "--index", "--keep", "0h", "--dry-run"); r.code != 0 || !strings.Contains(r.out, "would remove") {
		t.Errorf("index dry run = %+v", r)
	}
}

// TestPruneIndexRefusals covers the remaining ways the index prune stops:
// a fetch that failed and left only a cached copy, a local state it cannot
// read, and a store it cannot walk. All three are the same judgement —
// never delete from a view that is known to be incomplete.
func TestPruneIndexRefusals(t *testing.T) {
	t.Parallel()

	// Offline: the index is a git URL, a cached copy exists, and the pull
	// failed. Pruning against a copy known to be behind could delete a body
	// a repo has started citing since.
	dir := t.TempDir()
	write(t, dir, "docs/a.md", "# A\n")
	v := fakeVCS{head: "d1", branch: "main", files: map[string][]byte{}}
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatalf("init = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "workspace = \"https://example.invalid/index.git\"\n[scan]\ndocs = [\"docs/**\"]\n")
	if err := os.MkdirAll(filepath.Join(dir, DirName, IndexDir, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	offline := v
	offline.err = errPull
	if r := run(t, dir, offline, "prune", "--index", "--keep", "0h"); r.code != ExitError {
		t.Errorf("a stale index must refuse the prune: %+v", r)
	}

	// Local state that cannot be read: the union is incomplete.
	api, _, _, apiVCS, _ := staleSetup(t)
	write(t, api, ".ds/"+ledger.RefsFile, "# docsync refs format=99\n")
	if r := run(t, api, apiVCS, "prune", "--index", "--keep", "0h"); r.code != ExitError {
		t.Errorf("unreadable local state = %+v", r)
	}

	// A store that cannot be walked.
	api2, _, index2, apiVCS2, _ := staleSetup(t)
	blocks := filepath.Join(index2, "repos", "api", BlocksDir)
	if err := os.RemoveAll(blocks); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocks, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, api2, apiVCS2, "prune", "--index", "--keep", "0h"); r.code != ExitError {
		t.Errorf("unwalkable index store = %+v", r)
	}
}

// errPull is the failure a fake VCS reports for a pull that could not reach
// the remote.
var errPull = errors.New("offline")

// TestPruneNeverDeletesALiveBody is prune's safety property over random
// state. Pruning is irreversible, so whatever the ledger, refs, ack log and
// foreign snapshot hold, every hash any of them names keeps its body, and
// only bodies nothing names are removed. It guards the next change that adds
// a baseline source: if prune cannot see it, this fails.
// promise:prune-live-set
func TestPruneNeverDeletesALiveBody(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(12))
	old := time.Now().Add(-365 * 24 * time.Hour)
	for trial := 0; trial < 40; trial++ {
		dir, v := initialised(t)
		st := NewStore(dir)
		hash := func(i int) string { return fmt.Sprintf("%064x", i) }
		live := map[string]bool{}
		var l ledger.Ledger
		var refs ledger.Refs
		var acks []ledger.Ack
		var foreign ledger.Foreign
		n := 0
		pick := func() string { n++; h := hash(n); live[h] = true; return h }
		for i := rng.Intn(4); i >= 0; i-- {
			l.Rows = append(l.Rows, ledger.Row{ID: fmt.Sprintf("l%d-k7m2p4xq", i), Repo: "api", File: "a.go", Start: 1, End: 1, Hash: pick()})
		}
		for i := rng.Intn(4); i >= 0; i-- {
			refs.Rows = append(refs.Rows, ledger.RefRow{ID: fmt.Sprintf("r%d-k7m2p4xq", i), Repo: "api", Doc: "d.md", Line: i + 1, AckedHash: pick(), SeenHash: pick()})
		}
		for i := rng.Intn(4); i >= 0; i-- {
			acks = append(acks, ledger.Ack{At: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC), Actor: "k", ActorKind: ledger.ActorHuman, ID: fmt.Sprintf("a%d-k7m2p4xq", i), Repo: "api", Doc: "d.md", Line: 100 + i, BlockHash: pick()})
		}
		for i := rng.Intn(3); i >= 0; i-- {
			foreign.Rows = append(foreign.Rows, ledger.ForeignRow{Row: ledger.Row{ID: fmt.Sprintf("f%d-k7m2p4xq", i), Repo: "up", Hash: pick()}, Commit: "c"})
		}
		if err := st.SaveLedger(l, refs); err != nil {
			t.Fatal(err)
		}
		if err := st.AppendAcks(acks); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveForeign(foreign); err != nil {
			t.Fatal(err)
		}
		bodies := map[string]string{}
		for h := range live {
			bodies[h] = "live"
		}
		deadCount := 1 + rng.Intn(5)
		for i := 0; i < deadCount; i++ {
			bodies[hash(10000+i)] = "dead"
		}
		if err := st.WriteBodies(bodies); err != nil {
			t.Fatal(err)
		}
		for h := range bodies {
			p := filepath.Join(dir, DirName, BlocksDir, h)
			if err := os.Chtimes(p, old, old); err != nil {
				t.Fatal(err)
			}
		}
		if r := run(t, dir, v, "prune"); r.code != 0 {
			t.Fatalf("trial %d: prune = %+v", trial, r)
		}
		for h := range bodies {
			_, err := os.Stat(filepath.Join(dir, DirName, BlocksDir, h))
			switch {
			case live[h] && err != nil:
				t.Fatalf("trial %d: prune deleted the live body %s", trial, h[:12])
			case !live[h] && err == nil:
				t.Fatalf("trial %d: prune kept the dead body %s", trial, h[:12])
			}
		}
	}
}

// TestPruneIndexUsesTheWorkspaceDefaultBranch pins that index maintenance
// runs from the branch that publishes. prune --index compared against main
// alone, so a workspace whose default_branch is master could publish but
// was refused every prune on master and told to use main.
func TestPruneIndexUsesTheWorkspaceDefaultBranch(t *testing.T) {
	t.Parallel()
	api, _, index := twoRepos(t)
	write(t, index, "ds-workspace.toml", "[workspace]\nname = \"platform\"\nrepos = [\"github.com/org/api\", \"github.com/org/docs\"]\ndefault_branch = \"master\"\n")
	master := fakeVCS{head: "a1", branch: "master", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	if r := run(t, api, master, "publish"); r.code != 0 {
		t.Fatalf("publish from master = %+v", r)
	}
	if r := run(t, api, master, "prune", "--index", "--dry-run"); r.code != 0 {
		t.Errorf("prune --index from the workspace's default branch = %+v", r)
	}
	onMain := master
	onMain.branch = "main"
	if r := run(t, api, onMain, "prune", "--index", "--dry-run"); r.code != ExitError || !strings.Contains(r.err, `default is "master"`) {
		t.Errorf("prune --index off the default branch = %+v", r)
	}
}
