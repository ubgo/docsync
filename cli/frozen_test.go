package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/docsync/ledger"
)

// TestFrozenCheckIsReproducible is the point of the snapshot: the same
// commit of the citing repo must give the same answer however long ago the
// index was synced. Without it a CI build fails for a reason absent from its
// own diff, cannot be bisected, and an old commit re-run gives a different
// answer than it gave at the time.
// Pins bug 5.
func TestFrozenCheckIsReproducible(t *testing.T) {
	t.Parallel()
	api, docs, _ := twoRepos(t)
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("api publish = %+v", r)
	}
	// docs records what it cites, and acks it, so the baseline is green.
	if r := run(t, docs, docsVCS, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	r := run(t, docs, docsVCS, "sync")
	if r.code != 0 || !strings.Contains(r.out, "now cited at") {
		t.Fatalf("sync must record what is newly cited: %+v", r)
	}
	if r := run(t, docs, docsVCS, "ack", "sess-save-k7m2p4xq", "--all"); r.code != 0 {
		t.Fatalf("ack = %+v", r)
	}
	if r := run(t, docs, docsVCS, "check", "--frozen"); r.code != 0 {
		t.Fatalf("frozen baseline must be green: %+v", r)
	}

	// Upstream changes and republishes. Nothing in docs changes.
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.sessions.Insert()\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("api republish = %+v", r)
	}

	// An ordinary check syncs and sees it; a frozen one does not.
	if r := run(t, docs, docsVCS, "check"); r.code != ExitFindings {
		t.Errorf("an unfrozen check must see the upstream change: %+v", r)
	}
	if r := run(t, docs, docsVCS, "check", "--frozen"); r.code != 0 {
		t.Errorf("a frozen check must give the same answer as before: %+v", r)
	}

	// Syncing is the deliberate act, and it reports what moved.
	r = run(t, docs, docsVCS, "sync")
	if r.code != 0 || !strings.Contains(r.out, "~ sess-save-k7m2p4xq") {
		t.Errorf("sync must report the move: %+v", r)
	}
	// After which the frozen check sees it too — in the pull request that
	// carries the foreign.tsv diff.
	if r := run(t, docs, docsVCS, "check", "--frozen"); r.code != ExitFindings {
		t.Errorf("after syncing, frozen must see it: %+v", r)
	}
	// A sync that changes nothing says so.
	if r := run(t, docs, docsVCS, "sync"); r.code != 0 || !strings.Contains(r.out, snapshotUnchanged) {
		t.Errorf("an idempotent sync must say so: %+v", r)
	}
}

// TestFrozenWithoutSnapshotFails pins that --frozen never passes vacuously:
// with nothing to resolve against it would report green for citations it
// never looked at, which is the worst possible failure for a gate.
// promise:frozen-no-snapshot
func TestFrozenWithoutSnapshotFails(t *testing.T) {
	t.Parallel()
	api, docs, _ := twoRepos(t)
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("publish = %+v", r)
	}
	r := run(t, docs, docsVCS, "check", "--frozen")
	if r.code == 0 {
		t.Fatalf("--frozen with no snapshot must not pass: %+v", r)
	}
	if !strings.Contains(r.err, ledger.ForeignFile) || !strings.Contains(r.err, "sync") {
		t.Errorf("the error must name the file and the remedy: %s", r.err)
	}
	// A corrupt snapshot is an error too, never an empty one.
	write(t, docs, ".ds/"+ledger.ForeignFile, "# docsync foreign format=99\n")
	if r := run(t, docs, docsVCS, "check", "--frozen"); r.code == 0 {
		t.Errorf("a snapshot this tool cannot read must stop the check: %+v", r)
	}
}

// TestFrozenIsTheCIDefault covers the flag resolution, including the
// conflict between asking for both.
func TestFrozenIsTheCIDefault(t *testing.T) {
	api, docs, _ := twoRepos(t)
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
	for _, args := range [][]string{{"publish"}} {
		if r := run(t, api, apiVCS, args...); r.code != 0 {
			t.Fatalf("%v = %+v", args, r)
		}
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
	// Upstream moves; the snapshot does not.
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.sessions.Insert()\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("republish = %+v", r)
	}
	// This subtest cannot be parallel: it sets a process-wide variable.
	t.Setenv(ciEnv, "1")
	if r := run(t, docs, docsVCS, "check"); r.code != 0 {
		t.Errorf("CI must default to frozen: %+v", r)
	}
	if r := run(t, docs, docsVCS, "check", "--sync"); r.code != ExitFindings {
		t.Errorf("--sync must opt back in under CI: %+v", r)
	}
	if r := run(t, docs, docsVCS, "check", "--frozen", "--sync"); r.code != ExitError || !strings.Contains(r.err, "opposite") {
		t.Errorf("asking for both must be a usage error: %+v", r)
	}
	// Without CI the default is to sync.
	t.Setenv(ciEnv, "")
	if r := run(t, docs, docsVCS, "check"); r.code != ExitFindings {
		t.Errorf("interactive use must still sync first: %+v", r)
	}
}

// TestFrozenNeedsNoIndex pins the offline story: a frozen check resolves
// from committed state alone, so a CI runner with no network still works.
func TestFrozenNeedsNoIndex(t *testing.T) {
	t.Parallel()
	api, docs, index := twoRepos(t)
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
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
	// The index disappears entirely.
	if err := os.RemoveAll(index); err != nil {
		t.Fatal(err)
	}
	if r := run(t, docs, docsVCS, "check", "--frozen"); r.code != 0 {
		t.Errorf("frozen must not need the index: %+v", r)
	}
}

// TestSnapshotIsCommitted guards the classification: foreign.tsv is a shared
// fact, so it must not join the machine-local ignore list.
func TestSnapshotIsCommitted(t *testing.T) {
	t.Parallel()
	if strings.Contains(gitignoreBody, ledger.ForeignFile) {
		t.Error("the foreign snapshot is committed state and must not be ignored")
	}
}

// TestSyncSummaryMatchesTheFile is the invariant, not the instance: the
// summary reports "no cited block changed" if and only if the rows are
// identical. A summary computed from hashes alone satisfied the instance
// (content changes) while silently missing a pure move, which rewrote the
// file and produced a git diff the sync described as nothing.
func TestSyncSummaryMatchesTheFile(t *testing.T) {
	t.Parallel()
	row := func(id, hash, file string, start, end int) ledger.ForeignRow {
		return ledger.ForeignRow{Row: ledger.Row{ID: id, Repo: "api", Hash: hash, File: file, Start: start, End: end}}
	}
	base := ledger.Foreign{Rows: []ledger.ForeignRow{
		row("keep-k7m2p4xq", "aaa", "a.go", 1, 3),
		row("move-h3v8n2wd", "bbb", "b.go", 9, 11),
		row("gone-t4k2b9rf", "ccc", "c.go", 1, 2),
	}}
	for _, tc := range []struct {
		name string
		next ledger.Foreign
		want string // the marker the summary must carry, "" for unchanged
	}{
		{
			name: "identical",
			next: base,
			want: "",
		},
		{
			name: "content changed",
			next: ledger.Foreign{Rows: []ledger.ForeignRow{row("keep-k7m2p4xq", "zzz", "a.go", 1, 3), base.Rows[1], base.Rows[2]}},
			want: "~ keep-k7m2p4xq",
		},
		{
			name: "moved with the same content",
			next: ledger.Foreign{Rows: []ledger.ForeignRow{base.Rows[0], row("move-h3v8n2wd", "bbb", "b.go", 4, 6), base.Rows[2]}},
			want: "> move-h3v8n2wd moved b.go:9-11 -> b.go:4-6",
		},
		{
			name: "moved to another file",
			next: ledger.Foreign{Rows: []ledger.ForeignRow{base.Rows[0], row("move-h3v8n2wd", "bbb", "moved.go", 9, 11), base.Rows[2]}},
			want: "> move-h3v8n2wd",
		},
		{
			name: "newly cited",
			next: ledger.Foreign{Rows: append(append([]ledger.ForeignRow{}, base.Rows...), row("new-m4w8k2qn", "ddd", "d.go", 1, 1))},
			want: "+ new-m4w8k2qn",
		},
		{
			name: "no longer published",
			next: ledger.Foreign{Rows: base.Rows[:2]},
			want: "- gone-t4k2b9rf",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			same := rowsEqual(base, tc.next)
			changes := diffSnapshot(base, tc.next)
			var b strings.Builder
			printChanges(&b, changes, same)
			out := b.String()

			// The invariant, both directions.
			saysUnchanged := strings.Contains(out, snapshotUnchanged)
			if saysUnchanged != same {
				t.Errorf("summary says unchanged=%v but rows identical=%v:\n%s", saysUnchanged, same, out)
			}
			if tc.want == "" {
				if len(changes) != 0 {
					t.Errorf("identical rows must yield no changes: %+v", changes)
				}
				return
			}
			if len(changes) == 0 {
				t.Fatalf("a changed file must produce a summary:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, out)
			}
		})
	}
}

// TestSyncReportsAMoveEndToEnd is the case that was missed: upstream moves a
// block without touching it, which rewrites the recorded lines.
func TestSyncReportsAMoveEndToEnd(t *testing.T) {
	t.Parallel()
	api, docs, _, apiVCS, docsVCS := staleSetup(t)
	// Same body, new position: two lines of padding above the def.
	write(t, api, "internal/store.go", storeWithPadding)
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("republish = %+v", r)
	}
	r := run(t, docs, docsVCS, "sync")
	if r.code != 0 {
		t.Fatalf("sync = %+v", r)
	}
	if !strings.Contains(r.out, "moved") {
		t.Errorf("a pure move must be reported, not called unchanged:\n%s", r.out)
	}
	if strings.Contains(r.out, snapshotUnchanged) {
		t.Errorf("the file changed; the summary must not say otherwise:\n%s", r.out)
	}
	// Syncing again is genuinely unchanged.
	if r := run(t, docs, docsVCS, "sync"); !strings.Contains(r.out, snapshotUnchanged) {
		t.Errorf("an idempotent sync = %+v", r)
	}
}

// TestSyncSummaryWhenOnlyMetadataMoves covers the third state: the rows
// differ, but in neither content nor position. Saying "no cited block
// changed" there would be true of the blocks and false of the file, so the
// summary distinguishes the two.
func TestSyncSummaryWhenOnlyMetadataMoves(t *testing.T) {
	t.Parallel()
	a := ledger.Foreign{Rows: []ledger.ForeignRow{{Row: ledger.Row{ID: "x-k7m2p4xq", Repo: "api", Hash: "aaa", File: "a.go", Start: 1, End: 2}, Commit: "c1"}}}
	b := a
	b.Rows = []ledger.ForeignRow{{Row: a.Rows[0].Row, Commit: "c2"}}
	if rowsEqual(a, b) {
		t.Fatal("a different upstream commit is a different row")
	}
	if changes := diffSnapshot(a, b); len(changes) != 0 {
		t.Errorf("a commit-only change is not a block change: %+v", changes)
	}
	var out strings.Builder
	printChanges(&out, nil, false)
	if strings.Contains(out.String(), snapshotUnchanged) {
		t.Errorf("must not claim nothing changed when the rows differ: %s", out.String())
	}
	if !strings.Contains(out.String(), "upstream commits recorded") {
		t.Errorf("must say what did change: %s", out.String())
	}
}

// TestMissingBodies covers the warning: a hash with no published body
// degrades a later frozen finding to `unknown`, which is worth saying.
func TestMissingBodies(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := ledger.Foreign{Rows: []ledger.ForeignRow{
		{Row: ledger.Row{ID: "a-k7m2p4xq", Repo: "api", Hash: "aaaaaaaabbbbbbbb"}},
		{Row: ledger.Row{ID: "b-h3v8n2wd", Repo: "api", Hash: ""}}, // no hash: not counted
	}}
	if got := missingBodies(dir, f); got != 1 {
		t.Errorf("missingBodies = %d, want 1", got)
	}
	p := filepath.Join(dir, "repos", "api", "blocks", "aaaaaaaabbbbbbbb")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := missingBodies(dir, f); got != 0 {
		t.Errorf("missingBodies with the body present = %d", got)
	}
	if got := missingBodies("", f); got != 0 {
		t.Errorf("no index dir = %d", got)
	}
}

// TestSyncSnapshotFailures covers the error paths of writing the snapshot:
// unreadable committed state, an unreadable previous snapshot, and a store
// that cannot be written. Each must stop the sync rather than leave a
// snapshot that disagrees with what was actually published.
// promise:missing-body-unknown
func TestSyncSnapshotFailures(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (string, fakeVCS) {
		t.Helper()
		api, docs, _ := twoRepos(t)
		apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
		if r := run(t, api, apiVCS, "publish"); r.code != 0 {
			t.Fatalf("publish = %+v", r)
		}
		docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
		if r := run(t, docs, docsVCS, "scan"); r.code != 0 {
			t.Fatalf("scan = %+v", r)
		}
		return docs, docsVCS
	}

	// An unreadable refs file: the cited set cannot be determined.
	docs, v := setup(t)
	write(t, docs, ".ds/"+ledger.RefsFile, "# docsync refs format=99\n")
	if r := run(t, docs, v, "sync"); r.code != ExitError {
		t.Errorf("unreadable refs = %+v", r)
	}

	// An unreadable previous snapshot: the diff cannot be computed, and
	// overwriting it silently would hide what moved.
	docs2, v2 := setup(t)
	write(t, docs2, ".ds/"+ledger.ForeignFile, "# docsync foreign format=99\n")
	if r := run(t, docs2, v2, "sync"); r.code != ExitError {
		t.Errorf("unreadable snapshot = %+v", r)
	}

	// A directory squatting on the temp path makes the atomic write fail.
	docs3, v3 := setup(t)
	tmp := filepath.Join(docs3, DirName, ledger.ForeignFile+writeTempExt)
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run(t, docs3, v3, "sync"); r.code != ExitError {
		t.Errorf("unwritable snapshot = %+v", r)
	}
	os.Remove(tmp)

	// A cited block whose body was never published warns, without failing.
	docs4, v4 := setup(t)
	if r := run(t, docs4, v4, "sync"); r.code != 0 {
		t.Fatalf("sync = %+v", r)
	}
	f, ok, err := NewStore(docs4).LoadForeign()
	if err != nil || !ok {
		t.Fatalf("snapshot = %v %v", ok, err)
	}
	if len(f.Rows) == 0 {
		t.Fatal("this test needs a cited foreign row")
	}
}

// TestSyncWarnsAboutMissingBodies covers the honest degradation: a cited
// block whose body the publishing repo never wrote (or pruned) means a later
// frozen check can only say `unknown`. That is worth a warning, and must not
// fail the sync — the snapshot is still correct, only less descriptive.
func TestSyncWarnsAboutMissingBodies(t *testing.T) {
	t.Parallel()
	api, docs, index := twoRepos(t)
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("publish = %+v", r)
	}
	if r := run(t, docs, docsVCS, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	// Remove the published bodies, as a repo predating the body store — or
	// one that pruned — would leave them.
	blocks := filepath.Join(index, "repos", "api", "blocks")
	if _, err := os.Stat(blocks); err != nil {
		t.Fatalf("this test needs published bodies: %v", err)
	}
	if err := os.RemoveAll(blocks); err != nil {
		t.Fatal(err)
	}
	r := run(t, docs, docsVCS, "sync")
	if r.code != 0 {
		t.Fatalf("a missing body must not fail the sync: %+v", r)
	}
	if !strings.Contains(r.err, "no published body") || !strings.Contains(r.err, "unknown") {
		t.Errorf("sync must warn about bodies it cannot find: %q", r.err)
	}
	// And the snapshot is still written and usable.
	if r := run(t, docs, docsVCS, "check", "--frozen"); r.code == ExitError {
		t.Errorf("a frozen check must still run: %+v", r)
	}
}

// TestFrozenUnrecordedCitation is the end-to-end version: a new cross-repo
// citation lands, CI runs the frozen check before anyone has synced, and the
// remedy it prints must be one that helps rather than one that sends the
// reader to "fix" a citation that is already correct.
func TestFrozenUnrecordedCitation(t *testing.T) {
	t.Parallel()
	api, docs, _ := twoRepos(t)
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("publish = %+v", r)
	}
	if r := run(t, docs, docsVCS, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	if r := run(t, docs, docsVCS, "sync"); r.code != 0 {
		t.Fatalf("sync = %+v", r)
	}
	// A new upstream block, newly cited here, and nobody has synced since.
	write(t, api, "internal/extra.go", "package store\n\n// ds:def id=extra-h3v8n2wd\nfunc Extra() {}\n")
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("republish = %+v", r)
	}
	write(t, docs, "docs/extra.md", "See [extra](ds:block?id=extra-h3v8n2wd).\n")
	if r := run(t, docs, docsVCS, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	r := run(t, docs, docsVCS, "check", "--frozen")
	if r.code != ExitFindings {
		t.Fatalf("an unrecorded citation must fail the run: %+v", r)
	}
	if strings.Contains(r.out, "is not defined") {
		t.Errorf("a frozen run must not claim an id is undefined when it only failed to look: %s", r.out)
	}
	for _, want := range []string{"not recorded in " + ledger.ForeignFile, "ds sync"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("output must contain %q: %s", want, r.out)
		}
	}
	// And the remedy works: syncing records it and the check goes green.
	if r := run(t, docs, docsVCS, "sync"); r.code != 0 {
		t.Fatalf("sync = %+v", r)
	}
	if r := run(t, docs, docsVCS, "check", "--frozen"); r.code == ExitError {
		t.Errorf("after syncing the id must resolve: %+v", r)
	}
	// An unfrozen check still calls a genuinely undefined id undefined.
	write(t, docs, "docs/bogus.md", "See [bogus](ds:block?id=nope-a2b6f8jk).\n")
	if r := run(t, docs, docsVCS, "check"); !strings.Contains(r.out, "nope-a2b6f8jk is not defined") {
		t.Errorf("unfrozen wording must be unchanged: %s", r.out)
	}
}

// storeWithPadding is the api fixture's file with two padding lines above
// the def, so the block keeps its content and changes only its position.
const storeWithPadding = `package store

// padding
// padding

// ds:def id=sess-save-k7m2p4xq owner=@auth
func (s *Store) Save() error {
	return s.legacy.Save()
}

// ds:def id=t-save-a2b6f8jk
func TestSave(t *testing.T) {}
`

// TestDiffSnapshotPerEnvironment pins that a sync summary compares each
// environment's row with the same environment's row, and prints in a fixed
// order. Keyed by id, a snapshot listing prod and dev in a new order
// reported both as changed, and the order came from map iteration.
func TestDiffSnapshotPerEnvironment(t *testing.T) {
	t.Parallel()
	row := func(env, hash string) ledger.ForeignRow {
		return ledger.ForeignRow{Row: ledger.Row{ID: "port-k7m2p4xq", Env: env, Hash: hash, File: env + ".yaml", Start: 1, End: 1}}
	}
	old := ledger.Foreign{Rows: []ledger.ForeignRow{row("dev", "d1"), row("prod", "p1")}}
	if got := diffSnapshot(old, ledger.Foreign{Rows: []ledger.ForeignRow{row("prod", "p1"), row("dev", "d1")}}); len(got) != 0 {
		t.Errorf("reordered environments are not a change: %+v", got)
	}
	got := diffSnapshot(old, ledger.Foreign{Rows: []ledger.ForeignRow{row("dev", "d2"), row("prod", "p2")}})
	if len(got) != 2 || got[0].From != "d1" || got[1].From != "p1" {
		t.Errorf("each environment against itself, in a fixed order: %+v", got)
	}
}
