package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync"
)

// undoFixture gives a repo with one def written by `def`, and returns the
// id. The VCS starts with no record of the file, so the write is
// uncommitted; commitWrite() moves it into history.
func undoFixture(t *testing.T) (string, fakeVCS, string) {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "conf/app.yaml", "svc:\n  port: 8081\n")
	write(t, dir, "docs/notes.md", "# Notes\n\nNothing yet.\n")
	v := fakeVCS{head: "abc1234", files: map[string][]byte{}}
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatalf("init = %+v", r)
	}
	r := run(t, dir, v, "def", "conf/app.yaml#svc.port", "--label", "port")
	if r.code != 0 {
		t.Fatalf("def = %+v", r)
	}
	return dir, v, strings.TrimSpace(r.out)
}

// commitWrite makes the VCS report the current file contents as HEAD, which
// is what "this write is committed" means to undo.
func commitWrite(t *testing.T, dir string, v fakeVCS, rel string) fakeVCS {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	v.files[v.head+":"+rel] = raw
	return v
}

// TestUndoStopsAtTheCommitBoundary is the hazard from bug 3: a second
// undo reached past the last commit and removed a def that was already in
// history. A committed write is no longer a recent mistake, so a bare undo
// must refuse and name what it is refusing.
// promise:undo-bounded
func TestUndoStopsAtTheCommitBoundary(t *testing.T) {
	t.Parallel()
	dir, v, id := undoFixture(t)
	v = commitWrite(t, dir, v, "conf/app.yaml")

	before, err := os.ReadFile(filepath.Join(dir, "conf/app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	r := run(t, dir, v, "undo")
	if r.code == 0 {
		t.Fatalf("a committed write must not be undone by default: %+v", r)
	}
	for _, want := range []string{"nothing to undo since the last commit", "abc1234", id, "--force"} {
		if !strings.Contains(r.err, want) {
			t.Errorf("refusal must mention %q: %s", want, r.err)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "conf/app.yaml"))
	if string(before) != string(after) {
		t.Error("a refused undo must not touch the file")
	}
	// --force crosses the boundary. Nothing cites the def, so no orphan
	// guard fires.
	if r := run(t, dir, v, "undo", "--force"); r.code != 0 {
		t.Fatalf("--force must cross the boundary: %+v", r)
	}
	after, _ = os.ReadFile(filepath.Join(dir, "conf/app.yaml"))
	if strings.Contains(string(after), "ds:def") {
		t.Errorf("--force must actually undo:\n%s", after)
	}
}

// TestUndoRefusesToOrphanACitedDef pins the guard that reads refs.tsv: the
// breakage was previously found only afterwards, from a red `check`, by
// which point the source had already changed.
func TestUndoRefusesToOrphanACitedDef(t *testing.T) {
	t.Parallel()
	dir, v, id := undoFixture(t)
	write(t, dir, "docs/notes.md", "# Notes\n\nListens on [the port](ds:cfg?id="+id+").\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	r := run(t, dir, v, "undo")
	if r.code == 0 {
		t.Fatalf("a cited def must not be orphaned silently: %+v", r)
	}
	for _, want := range []string{id, "docs/notes.md:3", "1 sentence", "--orphan"} {
		if !strings.Contains(r.err, want) {
			t.Errorf("refusal must mention %q: %s", want, r.err)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "conf/app.yaml")); !strings.Contains(string(raw), "ds:def") {
		t.Error("a refused undo must not touch the file")
	}
	// --orphan accepts the breakage knowingly.
	if r := run(t, dir, v, "undo", "--orphan"); r.code != 0 {
		t.Fatalf("--orphan must proceed: %+v", r)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "conf/app.yaml")); strings.Contains(string(raw), "ds:def") {
		t.Error("--orphan must actually undo")
	}
}

// TestUndoDryRunWritesNothing closes the SPEC line 687 gap: undo was the
// only source-writing command without --dry-run.
func TestUndoDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	dir, v, id := undoFixture(t)
	before, err := os.ReadFile(filepath.Join(dir, "conf/app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	journalBefore, err := os.ReadFile(filepath.Join(dir, DirName, JournalFile))
	if err != nil {
		t.Fatal(err)
	}
	r := run(t, dir, v, "undo", "--dry-run")
	if r.code != 0 {
		t.Fatalf("--dry-run = %+v", r)
	}
	if !strings.Contains(r.out, "--dry-run") || !strings.Contains(r.out, id) || !strings.Contains(r.out, "conf/app.yaml:2") {
		t.Errorf("--dry-run must describe the edit: %s", r.out)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "conf/app.yaml"))
	journalAfter, _ := os.ReadFile(filepath.Join(dir, DirName, JournalFile))
	if string(before) != string(after) {
		t.Error("--dry-run wrote the source file")
	}
	if string(journalBefore) != string(journalAfter) {
		t.Error("--dry-run consumed the journal entry")
	}
}

// TestUndoList shows the stack without writing, including an entry from a
// journal written before kind and time existed.
func TestUndoList(t *testing.T) {
	t.Parallel()
	dir, v, id := undoFixture(t)
	r := run(t, dir, v, "undo", "--list")
	if r.code != 0 {
		t.Fatalf("--list = %+v", r)
	}
	for _, want := range []string{"KIND", WriteDef, "conf/app.yaml:2", id, string(statusUncommitted)} {
		if !strings.Contains(r.out, want) {
			t.Errorf("--list must show %q: %s", want, r.out)
		}
	}
	// A five-column journal still lists, with the unknown fields named as
	// unknown rather than filled in with defaults.
	p := filepath.Join(dir, DirName, JournalFile)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Split(strings.TrimRight(string(raw), "\n"), "\t")
	if len(f) != journalFields {
		t.Fatalf("journal has %d fields", len(f))
	}
	old := strings.Join([]string{f[0], f[3], f[4], f[5], f[6]}, "\t") + "\n"
	if err := os.WriteFile(p, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	r = run(t, dir, v, "undo", "--list")
	if r.code != 0 || !strings.Contains(r.out, "unknown") || !strings.Contains(r.out, id) {
		t.Errorf("an old journal must still list: %+v", r)
	}
	// And still undo.
	if r := run(t, dir, v, "undo"); r.code != 0 {
		t.Fatalf("an old journal must still undo: %+v", r)
	}
	// An empty stack says so instead of erroring.
	if r := run(t, dir, v, "undo", "--list"); r.code != 0 || !strings.Contains(r.out, "nothing to undo") {
		t.Errorf("empty --list = %+v", r)
	}
}

// TestUndoReportsWhatIsNext removes the "is it safe to press again" guess
// that produced the accident.
func TestUndoReportsWhatIsNext(t *testing.T) {
	t.Parallel()
	dir, v, _ := undoFixture(t)
	// A second, uncommitted write on top.
	write(t, dir, "conf/log.yaml", "log:\n  level: info\n")
	if r := run(t, dir, v, "def", "conf/log.yaml#log.level", "--label", "lvl"); r.code != 0 {
		t.Fatalf("def = %+v", r)
	}
	r := run(t, dir, v, "undo")
	if r.code != 0 || !strings.Contains(r.out, "next: "+WriteDef+" conf/app.yaml:2") {
		t.Errorf("must name the next entry: %+v", r)
	}
	if r := run(t, dir, v, "undo"); r.code != 0 || !strings.Contains(r.out, "next: nothing left to undo.") {
		t.Errorf("an emptied stack must say so: %+v", r)
	}
}

// TestUndoNextIsCommitted covers the other printNext branch: the stack is
// not empty but everything left is history.
func TestUndoNextIsCommitted(t *testing.T) {
	t.Parallel()
	dir, v, _ := undoFixture(t)
	v = commitWrite(t, dir, v, "conf/app.yaml")
	write(t, dir, "conf/log.yaml", "log:\n  level: info\n")
	if r := run(t, dir, v, "def", "conf/log.yaml#log.level", "--label", "lvl"); r.code != 0 {
		t.Fatalf("def = %+v", r)
	}
	r := run(t, dir, v, "undo")
	if r.code != 0 || !strings.Contains(r.out, "nothing uncommitted left to undo") || !strings.Contains(r.out, "abc1234") {
		t.Errorf("next must name the committed entry: %+v", r)
	}
}

// TestUndoWithoutGit pins the fallback: with no git, or no commits, there is
// no history to protect and every write counts as uncommitted.
func TestUndoWithoutGit(t *testing.T) {
	t.Parallel()
	dir, _, _ := undoFixture(t)
	none := fakeVCS{files: map[string][]byte{}} // Head() returns ""
	if r := run(t, dir, none, "undo", "--list"); r.code != 0 || !strings.Contains(r.out, string(statusUnknown)) {
		t.Errorf("--list without git = %+v", r)
	}
	if r := run(t, dir, none, "undo"); r.code != 0 {
		t.Errorf("without git every write is undoable: %+v", r)
	}
}

// TestUndoUninitialised and the empty-journal path keep their old contract.
func TestUndoEdgeCases(t *testing.T) {
	t.Parallel()
	v := fakeVCS{head: "abc1234", files: map[string][]byte{}}
	if r := run(t, t.TempDir(), v, "undo"); r.code != ExitError || !strings.Contains(r.err, "init") {
		t.Errorf("uninitialised = %+v", r)
	}
	dir, v2, _ := undoFixture(t)
	if r := run(t, dir, v2, "undo"); r.code != 0 {
		t.Fatalf("undo = %+v", r)
	}
	if r := run(t, dir, v2, "undo"); r.code != ExitError || !strings.Contains(r.err, "nothing to undo") {
		t.Errorf("empty journal = %+v", r)
	}
}

// TestDefID covers reading the id back out of the line an undo would
// revert, including the block-comment closers that would otherwise make the
// directive unparseable and silently disable the citation guard.
func TestDefID(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ line, want string }{
		{"  port: 8081   # ds:def id=port-k7m2p4xq", "port-k7m2p4xq"},
		{"<!-- ds:def id=sess-h3v8n2wd owner=@auth -->", "sess-h3v8n2wd"},
		{"/* ds:def id=blk-t4k2b9rf */", "blk-t4k2b9rf"},
		{"{#ds:def id=tpl-m4w8k2qn #}", "tpl-m4w8k2qn"},
		{"an ordinary line", ""},
		{"see ds:block?id=x-k7m2p4xq", ""}, // a citation is not a def
		{"# ds:def", ""},                   // a def with no id
	} {
		if got := defID(tc.line, "ds"); got != tc.want {
			t.Errorf("defID(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

// TestUndoFormatting covers the small renderers, including the boundaries
// where a duration changes unit.
func TestUndoFormatting(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Time{}, "unknown"},
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5 min ago"},
		{now.Add(-3 * time.Hour), "3 hr ago"},
		{now.Add(-50 * time.Hour), "2 days ago"},
	} {
		if got := ago(tc.at, now); got != tc.want {
			t.Errorf("ago(%v) = %q, want %q", tc.at, got, tc.want)
		}
	}
	if got := short("abcdef1234567"); got != "abcdef1" {
		t.Errorf("short = %q", got)
	}
	if got := short("abc"); got != "abc" {
		t.Errorf("short of a short sha = %q", got)
	}
	if orUnknown("") != "-" || orUnknown("def") != "def" {
		t.Error("orUnknown")
	}
	if orEmptyLine("") != "(line removed)" || orEmptyLine("x") != "x" {
		t.Error("orEmptyLine")
	}
	if plural(1, "sentence") != "1 sentence" || plural(2, "sentence") != "2 sentences" {
		t.Error("plural")
	}
	if got := appendOnce(appendOnce(nil, "a"), "a"); len(got) != 1 {
		t.Errorf("appendOnce must deduplicate: %v", got)
	}
	// A batch of several edits names the first and counts the rest.
	batch := []undoEntry{
		{journalEntry: journalEntry{Kind: WriteAdopt, Edit: docsync.Edit{File: "a.md", Line: 3}}},
		{journalEntry: journalEntry{Kind: WriteAdopt, Edit: docsync.Edit{File: "b.md", Line: 9}}},
	}
	if got := where(batch); got != "a.md:3 +1 more" {
		t.Errorf("where = %q", got)
	}
	if got := describe(batch); got != "adopt a.md:3 +1 more" {
		t.Errorf("describe = %q", got)
	}
	if got := allCiters(batch); got != nil {
		t.Errorf("allCiters with none = %v", got)
	}
	if got := orphanMessage(batch, "ds"); got != "" {
		t.Errorf("orphanMessage with no citers = %q", got)
	}
}

// TestUndoSeesCrossRepoCitations is the case the author cannot check by
// hand: the def lives here, the only sentence citing it lives in another
// repository, and nothing in this repo's own refs.tsv mentions it. The
// guard has to read the merged workspace index or it would happily orphan a
// def that a published runbook depends on.
func TestUndoSeesCrossRepoCitations(t *testing.T) {
	t.Parallel()
	api, docs, _ := twoRepos(t)
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatalf("api publish = %+v", r)
	}
	if r := run(t, docs, docsVCS, "publish"); r.code != 0 {
		t.Fatalf("docs publish = %+v", r)
	}
	// A new def in api, cited by nothing locally.
	write(t, api, "internal/extra.go", "package store\n\nfunc Extra() {}\n")
	r := run(t, api, apiVCS, "def", "internal/extra.go#Extra", "--label", "extra")
	if r.code != 0 {
		t.Fatalf("def = %+v", r)
	}
	// Uncited: undo proceeds.
	if r := run(t, api, apiVCS, "undo"); r.code != 0 {
		t.Fatalf("an uncited def must undo: %+v", r)
	}
	// Now the same for a def that docs cites. api's own refs.tsv does not
	// mention it; the merged index does.
	_, refs, _, err := NewStore(api).LoadState()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range refs.Rows {
		if row.ID == "sess-save-k7m2p4xq" {
			t.Fatalf("this test assumes api does not cite the id itself, but it does: %+v", row)
		}
	}
	// Re-write the def line so there is a journal entry for it.
	write(t, api, "internal/store.go", "package store\n\nfunc (s *Store) Save() error {\n\treturn s.legacy.Save()\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	r = run(t, api, apiVCS, "def", "internal/store.go#Store.Save", "--label", "sess-save")
	if r.code != 0 {
		t.Fatalf("def = %+v", r)
	}
	id := strings.TrimSpace(r.out)
	// Point the published docs refs at the newly minted id.
	write(t, docs, "docs/runbook.md", "Writes go through [Save](ds:block?id="+id+").\n")
	if r := run(t, docs, docsVCS, "publish"); r.code != 0 {
		t.Fatalf("docs republish = %+v", r)
	}
	r = run(t, api, apiVCS, "undo")
	if r.code == 0 {
		t.Fatalf("a def cited from another repo must not be orphaned: %+v", r)
	}
	if !strings.Contains(r.err, id) || !strings.Contains(r.err, "docs/") {
		t.Errorf("the refusal must name the other repo's sentence: %s", r.err)
	}
	if r := run(t, api, apiVCS, "undo", "--orphan"); r.code != 0 {
		t.Errorf("--orphan must still proceed: %+v", r)
	}
}

// TestUndoDegradesWithoutTheIndex pins that an unreachable or never-synced
// workspace does not fail the undo: the guard falls back to the local
// answer, and the commit boundary still applies.
func TestUndoDegradesWithoutTheIndex(t *testing.T) {
	t.Parallel()
	dir, v, _ := undoFixture(t)
	// A workspace pointing at nothing that exists.
	write(t, dir, ".ds/config.toml", "workspace = "+strconvQuote(filepath.Join(dir, "no-such-index"))+"\n[scan]\ncode = [\"conf/**\"]\ndocs = [\"docs/**\"]\n")
	if r := run(t, dir, v, "undo", "--list"); r.code != 0 {
		t.Errorf("--list must survive a missing index: %+v", r)
	}
	if r := run(t, dir, v, "undo"); r.code != 0 {
		t.Errorf("undo must survive a missing index: %+v", r)
	}
}

// TestUndoBrokenState covers the failure paths: an unreadable journal, and a
// corrupt ledger that makes the citation lookup fail.
func TestUndoBrokenState(t *testing.T) {
	t.Parallel()
	dir, v, _ := undoFixture(t)
	write(t, dir, ".ds/journal.tsv", "1\ta.txt\n")
	for _, args := range [][]string{{"undo"}, {"undo", "--list"}} {
		if r := run(t, dir, v, args...); r.code != ExitError {
			t.Errorf("%v with a corrupt journal = %+v", args, r)
		}
	}
	dir2, v2, _ := undoFixture(t)
	write(t, dir2, ".ds/refs.tsv", "# docsync refs format=99\n")
	if r := run(t, dir2, v2, "undo"); r.code != ExitError {
		t.Errorf("a refs file this tool cannot read must stop the undo: %+v", r)
	}
	dir3, v3, _ := undoFixture(t)
	write(t, dir3, ".ds/config.toml", "[scan\n")
	if r := run(t, dir3, v3, "undo"); r.code != ExitError {
		t.Errorf("a corrupt config must stop the undo: %+v", r)
	}
}

// TestStoreNowOverride pins the injected clock the journal stamps with.
func TestStoreNowOverride(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	s := NewStore(t.TempDir())
	s.Now = func() time.Time { return fixed }
	if got := s.now(); !got.Equal(fixed) {
		t.Errorf("now = %v, want %v", got, fixed)
	}
	if got := NewStore(t.TempDir()).now(); got.IsZero() {
		t.Error("the default clock must return a real time")
	}
}

// testApp is an App wired the way Run wires one, for the few branches that
// only a direct call can reach: failures that happen after the command has
// already validated its inputs.
func testApp(dir string, v VCS) *App {
	return &App{name: DefaultName, dir: dir, vcs: v, now: func() time.Time { return clock }}
}

// TestUndoInternalFailures covers the defensive paths. Each is a real error
// return that the command shape makes unreachable from the outside: by the
// time printNext runs, the journal it re-reads has just been written, and
// describeBatch is only ever asked for a batch that exists.
func TestUndoInternalFailures(t *testing.T) {
	t.Parallel()
	dir, v, _ := undoFixture(t)
	a := testApp(dir, v)
	st := NewStore(dir)

	// describeBatch on a batch number that is not in the journal.
	entries, err := st.journal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.describeBatch(st, entries, lastBatch(entries)+1); err == nil {
		t.Error("an absent batch must report nothing to undo")
	}

	// printNext and printUndoList over a corrupt journal.
	write(t, dir, ".ds/journal.tsv", "1\ta.txt\n")
	var buf strings.Builder
	if err := a.printNext(&buf, st); err == nil {
		t.Error("printNext must surface a corrupt journal")
	}
	broken, err := st.journal()
	if err == nil {
		t.Fatal("this test needs the journal to be unreadable")
	}
	_ = broken

	// printUndoList and printNext when the citation lookup fails: a refs
	// file this tool cannot read.
	dir2, v2, _ := undoFixture(t)
	a2, st2 := testApp(dir2, v2), NewStore(dir2)
	entries2, err := st2.journal()
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir2, ".ds/refs.tsv", "# docsync refs format=99\n")
	buf.Reset()
	if err := a2.printUndoList(&buf, st2, entries2); err == nil {
		t.Error("printUndoList must surface an unreadable refs file")
	}
	buf.Reset()
	if err := a2.printNext(&buf, st2); err == nil {
		t.Error("printNext must surface an unreadable refs file")
	}
}

// TestHistoryStatusLineMoved covers the case where the file is in HEAD but
// the written line is not at that position any more: the write is not the
// committed one, so it is still undoable.
func TestHistoryStatusLineMoved(t *testing.T) {
	t.Parallel()
	dir, v, _ := undoFixture(t)
	// HEAD has the file, but with different content at line 2.
	v.files[v.head+":conf/app.yaml"] = []byte("svc:\n  port: 9999\n")
	a := testApp(dir, v)
	st := NewStore(dir)
	entries, err := st.journal()
	if err != nil {
		t.Fatal(err)
	}
	batch, err := a.describeBatch(st, entries, lastBatch(entries))
	if err != nil {
		t.Fatal(err)
	}
	if batch[0].Status != statusUncommitted {
		t.Errorf("a line that is not in HEAD is uncommitted, got %q", batch[0].Status)
	}
	if r := run(t, dir, v, "undo"); r.code != 0 {
		t.Errorf("and is therefore undoable: %+v", r)
	}
}

// TestUndoListShowsCommittedAndCited pins the two annotations the listing
// exists for: whether an entry is past the commit boundary, and whether
// reversing it would orphan something.
func TestUndoListShowsCommittedAndCited(t *testing.T) {
	t.Parallel()
	dir, v, id := undoFixture(t)
	write(t, dir, "docs/notes.md", "# Notes\n\nListens on [the port](ds:cfg?id="+id+").\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	v = commitWrite(t, dir, v, "conf/app.yaml")
	r := run(t, dir, v, "undo", "--list")
	if r.code != 0 {
		t.Fatalf("--list = %+v", r)
	}
	for _, want := range []string{string(statusCommitted), "abc1234", "cited by", "docs/notes.md:3"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("--list must show %q: %s", want, r.out)
		}
	}
}

// TestUndoHintsAreExecutable pins that a refusal names the whole command,
// not just the flag it is missing. The two guards are independent and an
// entry can trip both, so a hint that dropped the flag already in effect
// sent the reader in a circle: --orphan on a committed def was refused by
// the boundary, whose hint was --force, which was refused by the citation
// guard, whose hint was --orphan again.
func TestUndoHintsAreExecutable(t *testing.T) {
	t.Parallel()
	// Committed and cited: both guards apply, in either order.
	build := func(t *testing.T) (string, fakeVCS) {
		t.Helper()
		dir, v, id := undoFixture(t)
		write(t, dir, "docs/notes.md", "# Notes\n\nListens on [the port](ds:cfg?id="+id+").\n")
		if r := run(t, dir, v, "scan"); r.code != 0 {
			t.Fatalf("scan = %+v", r)
		}
		return dir, commitWrite(t, dir, v, "conf/app.yaml")
	}
	// hintOf extracts the command a refusal tells the reader to run.
	hintOf := func(t *testing.T, err string) string {
		t.Helper()
		for _, marker := range []string{"to reverse it anyway: ", "Re-run with "} {
			if i := strings.Index(err, marker); i >= 0 {
				rest := err[i+len(marker):]
				rest = strings.TrimSuffix(strings.TrimSpace(strings.SplitN(rest, "\n", 2)[0]), " to proceed.")
				return rest
			}
		}
		t.Fatalf("no hint in: %s", err)
		return ""
	}

	// Follow the hints from a bare undo until one succeeds. Two refusals is
	// the most this should take; a loop would never terminate.
	dir, v := build(t)
	args := []string{"undo"}
	for step := 0; ; step++ {
		if step > 2 {
			t.Fatalf("hints did not converge; last was %v", args)
		}
		r := run(t, dir, v, args...)
		if r.code == 0 {
			break
		}
		hint := hintOf(t, r.err)
		if !strings.HasPrefix(hint, DefaultName+" undo") {
			t.Fatalf("hint is not a %s undo command: %q", DefaultName, hint)
		}
		args = strings.Fields(strings.TrimPrefix(hint, DefaultName+" "))
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "conf/app.yaml")); strings.Contains(string(raw), "ds:def") {
		t.Error("following the hints must eventually undo")
	}

	// Starting from --orphan must reach the same place, and the first
	// refusal must already name --orphan back.
	dir2, v2 := build(t)
	r := run(t, dir2, v2, "undo", "--orphan")
	if r.code == 0 {
		t.Fatal("--orphan alone must not cross the commit boundary")
	}
	if hint := hintOf(t, r.err); hint != DefaultName+" undo --force --orphan" {
		t.Errorf("hint must keep the flag already in effect, got %q", hint)
	}
	if r := run(t, dir2, v2, "undo", "--force", "--orphan"); r.code != 0 {
		t.Errorf("the hinted command must work: %+v", r)
	}

	// And the simple case is not made noisier: uncommitted and cited needs
	// only --orphan.
	dir3, v3, id3 := undoFixture(t)
	write(t, dir3, "docs/notes.md", "# Notes\n\nListens on [the port](ds:cfg?id="+id3+").\n")
	if r := run(t, dir3, v3, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	r = run(t, dir3, v3, "undo")
	if r.code == 0 {
		t.Fatal("a cited def must be guarded")
	}
	if hint := hintOf(t, r.err); hint != DefaultName+" undo --orphan" {
		t.Errorf("an uncommitted entry must not be told to use --force, got %q", hint)
	}
}

// TestSurvivorsDropOnlyTheBatchsOwnCitations pins the orphan guard's
// exception exactly: a citation the batch itself wrote, at that line and
// naming that id, goes away with the undo and does not count; any other
// citation of the def — another doc, another line, another id's edit on the
// same line — still guards.
func TestSurvivorsDropOnlyTheBatchsOwnCitations(t *testing.T) {
	t.Parallel()
	batch := []undoEntry{
		{journalEntry: journalEntry{Edit: docsync.Edit{File: "a.go", Line: 3, New: "// ds:def id=save-k7m2p4xq"}}},
		{journalEntry: journalEntry{Edit: docsync.Edit{File: "docs/d.md", Line: 3, New: "Saving is [here](ds:block?id=save-k7m2p4xq)."}}},
		{journalEntry: journalEntry{Edit: docsync.Edit{File: "docs/e.md", Line: 9, New: "[x](ds:block?id=other-h3v8n2wd)"}}},
	}
	got := survivors([]string{"docs/d.md:3", "docs/other.md:3", "docs/d.md:4", "docs/e.md:9"}, "save-k7m2p4xq", batch)
	want := []string{"docs/other.md:3", "docs/d.md:4", "docs/e.md:9"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("survivors = %v, want %v", got, want)
	}
}

// TestAdoptScanUndo is the sequence adopt itself prints: adopt, scan, undo.
// Undo reverses the def and the citation together, so it must not refuse as
// if the def would be orphaned, and it must put both files back exactly.
func TestAdoptScanUndo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v := fakeVCS{head: "abc1234", files: map[string][]byte{}}
	code := "package p\n\nfunc Save() int {\n\treturn 1\n}\n"
	doc := "# D\n\nSaving is [here](a.go#L3-L5).\n"
	write(t, dir, "a.go", code)
	write(t, dir, "docs/d.md", doc)
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatal(r)
	}
	if r := run(t, dir, v, "adopt"); r.code != 0 || !strings.Contains(r.out, "1 link(s) adopted") {
		t.Fatalf("adopt = %+v", r)
	}
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	if r := run(t, dir, v, "undo"); r.code != 0 {
		t.Fatalf("undo after adopt and scan = %+v", r)
	}
	for path, want := range map[string]string{"a.go": code, "docs/d.md": doc} {
		if got, _ := os.ReadFile(filepath.Join(dir, path)); string(got) != want {
			t.Errorf("%s not restored:\n%s", path, got)
		}
	}
}

// TestBranchCitationsAreSeparateButStillGuardUndo pins both halves of how a
// published branch's citations are read. Upstream check does not see them
// as the default branch's: they carried no branch marker, and one doc line
// was reported twice, once from a branch nobody had merged. The undo guard
// does see them: a def an open branch cites is still cited.
// promise:branch-apart
func TestBranchCitationsAreSeparateButStillGuardUndo(t *testing.T) {
	t.Parallel()
	api, docs, _ := twoRepos(t)
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
	feature := docsVCS
	feature.branch = "feature"
	for _, s := range []struct {
		dir  string
		v    fakeVCS
		args []string
	}{{api, apiVCS, []string{"publish"}}, {docs, docsVCS, []string{"scan"}}, {docs, docsVCS, []string{"publish"}}} {
		if r := run(t, s.dir, s.v, s.args...); r.code != 0 {
			t.Fatalf("%v = %+v", s.args, r)
		}
	}
	// The branch rewrites the sentence around the same citation.
	write(t, docs, "docs/runbook.md", "On the branch, writes go through [Save](ds:block?id=sess-save-k7m2p4xq).\n")
	if r := run(t, docs, feature, "publish", "--branch"); r.code != 0 {
		t.Fatalf("publish --branch = %+v", r)
	}
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.changed()\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if n := strings.Count(run(t, api, apiVCS, "check").out, "docs/runbook.md:1"); n != 1 {
		t.Errorf("docs/runbook.md:1 reported %d times upstream, want once", n)
	}
	// A new def cited only by the branch.
	write(t, api, "internal/extra.go", "package store\n\nfunc Extra() {}\n")
	r := run(t, api, apiVCS, "def", "internal/extra.go#Extra", "--label", "extra")
	if r.code != 0 {
		t.Fatalf("def = %+v", r)
	}
	id := strings.TrimSpace(r.out)
	write(t, docs, "docs/extra.md", "Extra is [here](ds:block?id="+id+").\n")
	if r := run(t, docs, feature, "publish", "--branch"); r.code != 0 {
		t.Fatalf("publish --branch = %+v", r)
	}
	if r := run(t, api, apiVCS, "undo"); r.code == 0 || !strings.Contains(r.err, id) {
		t.Errorf("a def cited on an open branch must not be undone: %+v", r)
	}
}
