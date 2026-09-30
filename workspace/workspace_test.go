package workspace

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/ledger"
)

func row(id, repo, file string) ledger.Row {
	return ledger.Row{ID: id, Repo: repo, Kind: block.KindFunc, File: file, Start: 1, End: 2, Hash: "h-" + id, Args: map[string]string{"id": id}}
}

var when = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func entries() []Entry {
	return []Entry{
		{
			Repo: "web", Ledger: ledger.Ledger{Header: ledger.Header{Commit: "w1", ScannedAt: when}, Rows: []ledger.Row{row("web-a2b6f8jk", "web", "app.ts"), row("dup-b3c7g9kl", "web", "dup.ts"), row("web-a2b6f8jk", "web", "twice.ts"), row("also-e6f2k4np", "web", "also.ts")}},
			Refs: ledger.Refs{Rows: []ledger.RefRow{{ID: "api-c4d8h2lm", Doc: "docs/w.md", Line: 3, Verb: "block", AckedHash: "h-api-c4d8h2lm"}}}, Tests: map[string]check.TestOutcome{"t-web": check.TestPassed},
		},
		{
			Repo: "api", Ledger: ledger.Ledger{Header: ledger.Header{Commit: "a1", ScannedAt: when.Add(-48 * time.Hour)}, Rows: []ledger.Row{row("api-c4d8h2lm", "api", "store.go"), row("dup-b3c7g9kl", "api", "dup.go"), row("also-e6f2k4np", "api", "also.go")}},
			Tests: map[string]check.TestOutcome{"t-api": check.TestFailed},
		},
	}
}

func TestMerge(t *testing.T) {
	t.Parallel()
	m := Merge(entries())
	if len(m.Defs) != 7 || m.Defs[0].Args[RepoKey] != "api" || m.Defs[0].ID != "api-c4d8h2lm" {
		t.Errorf("defs sorted by repo = %+v", m.Defs)
	}
	if m.RepoOf["web-a2b6f8jk"] != "web" || m.RepoOf["dup-b3c7g9kl"] != "api" {
		t.Errorf("repoOf = %v", m.RepoOf)
	}
	if len(m.Refs) != 1 || m.Refs[0].Repo != "web" {
		t.Errorf("refs = %+v", m.Refs)
	}
	if len(m.Tests) != 2 || m.Tests["t-api"] != check.TestFailed {
		t.Errorf("tests = %v", m.Tests)
	}
	// A repeat within one repo is that repo's own problem, not a cross-repo
	// duplicate.
	if len(m.Duplicates) != 2 || m.Duplicates[0].ID != "also-e6f2k4np" || m.Duplicates[1].ID != "dup-b3c7g9kl" || len(m.Duplicates[1].Repos) != 2 {
		t.Errorf("duplicates = %+v", m.Duplicates)
	}
	defs, refs := m.Others("api")
	if len(defs) != 4 || defs[0].Args[RepoKey] != "web" || len(refs) != 1 {
		t.Errorf("others = %+v %+v", defs, refs)
	}
	if err := m.CheckDuplicate("api", ledger.Ledger{Rows: []ledger.Row{row("api-c4d8h2lm", "api", "x")}}); err != nil {
		t.Errorf("own id is not a duplicate: %v", err)
	}
	if err := m.CheckDuplicate("infra", ledger.Ledger{Rows: []ledger.Row{row("api-c4d8h2lm", "infra", "x")}}); !errors.Is(err, ErrDuplicateAcrossRepos) {
		t.Errorf("foreign id = %v", err)
	}
	if age, err := m.StaleAge("api", when); err != nil || age != 48*time.Hour {
		t.Errorf("stale = %v %v", age, err)
	}
	if _, err := m.StaleAge("nope", when); !errors.Is(err, ErrNotPublished) {
		t.Errorf("unpublished = %v", err)
	}
	if got := Merge(nil); len(got.Defs) != 0 || got.Duplicates != nil {
		t.Errorf("empty merge = %+v", got)
	}
}

func TestBundleReadRoundTrip(t *testing.T) {
	t.Parallel()
	e := entries()[0]
	files := e.Files()
	if len(files) != 3 {
		t.Fatalf("files = %v", files)
	}
	fsys := fstest.MapFS{}
	for p, data := range files {
		fsys[p] = &fstest.MapFile{Data: data}
	}
	fsys["repos/empty/README.md"] = &fstest.MapFile{Data: []byte("no ledger here")}
	fsys["repos/notadir"] = &fstest.MapFile{Data: []byte("x")}
	got, err := Read(fsys)
	if err != nil || len(got) != 1 || got[0].Repo != "web" || len(got[0].Ledger.Rows) != 4 || len(got[0].Refs.Rows) != 1 || got[0].Refs.Rows[0].AckedHash != "h-api-c4d8h2lm" || got[0].Tests["t-web"] != check.TestPassed {
		t.Errorf("read = %+v %v", got, err)
	}
	if noTests := (Entry{Repo: "r"}).Files(); len(noTests) != 2 {
		t.Errorf("bundle without tests = %v", noTests)
	}
	if got, err := Read(fstest.MapFS{}); err != nil || got != nil {
		t.Errorf("no repos dir = %+v %v", got, err)
	}
	for name, bad := range map[string]fstest.MapFS{
		"ledger": {"repos/x/ledger.tsv": {Data: []byte("garbage\n")}},
		"refs":   {"repos/x/ledger.tsv": {Data: e.Ledger.Bytes()}, "repos/x/refs.tsv": {Data: []byte("garbage\n")}},
		"tests":  {"repos/x/ledger.tsv": {Data: e.Ledger.Bytes()}, "repos/x/tests.tsv": {Data: []byte("garbage\n")}},
	} {
		if _, err := Read(bad); err == nil {
			t.Errorf("corrupt %s must error", name)
		}
	}
	if _, err := Read(errFS{}); err == nil {
		t.Error("readdir error")
	}
}

type errFS struct{}

func (errFS) Open(string) (fs.File, error) { return nil, errors.New("boom") }

func TestTestsEncoding(t *testing.T) {
	t.Parallel()
	in := map[string]check.TestOutcome{"b": check.TestSkipped, "a": check.TestPassed}
	enc := string(EncodeTests(in))
	if !strings.HasPrefix(enc, "# docsync tests format=1\nid\toutcome\na\tpassed\nb\tskipped\n") {
		t.Errorf("encoded = %q", enc)
	}
	got, err := DecodeTests(strings.NewReader(enc + "\n"))
	if err != nil || len(got) != 2 || got["b"] != check.TestSkipped {
		t.Errorf("decoded = %v %v", got, err)
	}
	for _, bad := range []string{"", "# other\n", "# docsync tests format=1\nno-tab\n", "# docsync tests format=1\nx\tmaybe\n"} {
		if _, err := DecodeTests(strings.NewReader(bad)); !errors.Is(err, ErrTestsFormat) {
			t.Errorf("DecodeTests(%q) = %v", bad, err)
		}
	}
}

func TestJUnit(t *testing.T) {
	t.Parallel()
	suites := `<?xml version="1.0"?>
<testsuites>
  <testsuite name="pkg">
    <testcase classname="pkg" name="TestSave"/>
    <testcase classname="pkg" name="TestLoad"><failure message="boom"/></testcase>
    <testcase classname="pkg" name="TestSkip"><skipped/></testcase>
    <testcase classname="pkg" name="TestErr"><error/></testcase>
  </testsuite>
  <testcase name="TestTop"/>
</testsuites>`
	got, err := ParseJUnit(strings.NewReader(suites))
	if err != nil || got["TestSave"] != check.TestPassed || got["TestLoad"] != check.TestFailed || got["TestSkip"] != check.TestSkipped || got["TestErr"] != check.TestFailed || got["TestTop"] != check.TestPassed {
		t.Errorf("junit = %v %v", got, err)
	}
	single := `<testsuite name="pkg"><testcase name="TestOnly"/></testsuite>`
	if got, err := ParseJUnit(strings.NewReader(single)); err != nil || got["TestOnly"] != check.TestPassed {
		t.Errorf("single suite = %v %v", got, err)
	}
	if _, err := ParseJUnit(strings.NewReader("not xml")); err == nil {
		t.Error("bad xml")
	}
	if _, err := ParseJUnit(errReader{}); err == nil {
		t.Error("read error")
	}
	defs := []block.Block{
		{ID: "t1", Kind: block.KindFunc, Symbol: "TestSave"},
		{ID: "t2", Kind: block.KindFunc, Symbol: "Suite.TestLoad"},
		{ID: "t3", Kind: block.KindFunc, Symbol: "TestGone"},
		{ID: "f", Kind: block.KindFunc, Symbol: "Save"},
		{ID: "k", Kind: block.KindKey, Symbol: "TestNotAFunc"},
	}
	m := MatchTests(defs, got)
	if len(m) != 3 || m["t1"] != check.TestPassed || m["t2"] != check.TestFailed || m["t3"] != check.TestSkipped {
		t.Errorf("match = %v", m)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("io") }

func TestBranchesAndRemovedRepos(t *testing.T) {
	t.Parallel()
	es := entries()
	es = append(es, Entry{Repo: "api", Branch: "release-1", Ledger: ledger.Ledger{Header: ledger.Header{Commit: "r1"}, Rows: []ledger.Row{row("api-c4d8h2lm", "api", "store.go")}}})
	es = append(es, Entry{Repo: "legacy", Ledger: ledger.Ledger{Rows: []ledger.Row{row("old-h3v8n2wd", "legacy", "x")}}})
	m := MergeFor(es, []string{"github.com/org/api", "github.com/org/web.git"})
	if m.Removed["old-h3v8n2wd"] != "legacy" || len(m.Removed) != 1 {
		t.Errorf("removed = %v", m.Removed)
	}
	branch := 0
	for _, b := range m.Defs {
		if b.Args[block.KeyBranch] == "release-1" {
			branch++
		}
		if b.ID == "old-h3v8n2wd" {
			t.Error("removed repo's defs must not merge")
		}
	}
	if branch != 1 || m.Published["api"].Commit != "a1" {
		t.Errorf("branch defs = %d published = %v", branch, m.Published)
	}
	if len(Merge(es).Removed) != 0 {
		t.Error("Merge keeps everything")
	}
	// Branch entries round-trip through the index layout.
	e := es[len(es)-2]
	files := e.Files()
	if _, ok := files["repos/api/@release-1/ledger.tsv"]; !ok || e.Dir() != "repos/api/@release-1" {
		t.Errorf("branch files = %v", files)
	}
	fsys := fstest.MapFS{}
	for p, data := range files {
		fsys[p] = &fstest.MapFile{Data: data}
	}
	for p, data := range es[1].Files() {
		fsys[p] = &fstest.MapFile{Data: data}
	}
	fsys["repos/api/@bad/ledger.tsv"] = &fstest.MapFile{Data: []byte("garbage\n")}
	if _, err := Read(fsys); err == nil {
		t.Error("corrupt branch entry must error")
	}
	delete(fsys, "repos/api/@bad/ledger.tsv")
	fsys["repos/api/@empty/README.md"] = &fstest.MapFile{Data: []byte("no ledger")}
	got, err := Read(fsys)
	if err != nil || len(got) != 2 || got[1].Branch != "release-1" {
		t.Errorf("read branches = %+v %v", got, err)
	}
}

// TestEntryBodies pins the index layout for block bodies (§20.1): they are
// content-addressed under the entry's blocks/ directory, so republishing an
// unchanged block writes the same path with the same bytes, and a consumer
// can classify a citation whose acked hash predates the published ledger.
func TestEntryBodies(t *testing.T) {
	t.Parallel()
	e := Entry{Repo: "docs", Bodies: map[string]string{"cafe1234": "A parent chain may be at most 12 deep."}}
	if got := e.BodyPath("cafe1234"); got != "repos/docs/blocks/cafe1234" {
		t.Errorf("BodyPath = %q", got)
	}
	files := e.Files()
	body, ok := files["repos/docs/blocks/cafe1234"]
	if !ok || string(body) != "A parent chain may be at most 12 deep." {
		t.Errorf("Files did not carry the body: %v", files)
	}
	// A branch entry keeps its bodies beside its own ledger, so two branches
	// of one repo cannot overwrite each other's history.
	be := Entry{Repo: "docs", Branch: "next", Bodies: map[string]string{"cafe1234": "x"}}
	if got := be.BodyPath("cafe1234"); got != "repos/docs/@next/blocks/cafe1234" {
		t.Errorf("branch BodyPath = %q", got)
	}
	// No bodies means no extra files.
	if files := (Entry{Repo: "docs"}).Files(); len(files) != 2 {
		t.Errorf("want just ledger and refs, got %v", files)
	}
}

// TestMergedSnapshot pins the rule that keeps the snapshot reviewable: it
// records the ids this repo cites and nothing else. Recording the whole
// merged ledger would churn every downstream repo's `.ds/` on an unrelated
// upstream edit, which teaches people to ignore the diff that is the point
// of the file.
func TestMergedSnapshot(t *testing.T) {
	t.Parallel()
	cited := block.Block{ID: "sess-save-k7m2p4xq", Kind: block.KindFunc, Hash: "aaa", Args: map[string]string{block.KeyID: "sess-save-k7m2p4xq", RepoKey: "api"}}
	uncited := block.Block{ID: "other-h3v8n2wd", Kind: block.KindFunc, Hash: "bbb", Args: map[string]string{block.KeyID: "other-h3v8n2wd", RepoKey: "api"}}
	mine := block.Block{ID: "mine-t4k2b9rf", Kind: block.KindFunc, Hash: "ccc", Args: map[string]string{block.KeyID: "mine-t4k2b9rf", RepoKey: "docs"}}
	m := Merged{
		Defs:      []block.Block{cited, uncited, mine},
		Published: map[string]Published{"api": {Commit: "a1"}},
	}
	got := m.Snapshot("docs", map[Cite]bool{{ID: "sess-save-k7m2p4xq"}: true, {ID: "mine-t4k2b9rf"}: true}, ledger.Header{Repo: "docs"})
	if len(got.Rows) != 1 {
		t.Fatalf("want only the cited foreign id, got %d: %+v", len(got.Rows), got.Rows)
	}
	r := got.Rows[0]
	if r.ID != "sess-save-k7m2p4xq" || r.Repo != "api" || r.Hash != "aaa" {
		t.Errorf("row = %+v", r)
	}
	if r.Commit != "a1" {
		t.Errorf("the publishing repo's commit must be recorded, got %q", r.Commit)
	}
	if got.Header.Kind != ledger.KindForeign || got.Header.Format != ledger.Format {
		t.Errorf("header = %+v", got.Header)
	}
	// A repo's own blocks are never foreign, even when cited.
	for _, row := range got.Rows {
		if row.Repo == "docs" {
			t.Error("a local block must not appear in the snapshot")
		}
	}
	// Nothing cited yields an empty snapshot, not every published block.
	if empty := m.Snapshot("docs", nil, ledger.Header{}); len(empty.Rows) != 0 {
		t.Errorf("uncited = %+v", empty.Rows)
	}
}

// TestMergeTestsComeFromTheDefaultBranch pins which run assert= reads: the
// default branch's. A branch merged after main overwrote its outcomes, so a
// failing test on a feature branch failed every assert citation on main.
// promise:branch-apart
func TestMergeTestsComeFromTheDefaultBranch(t *testing.T) {
	t.Parallel()
	for _, order := range [][]Entry{
		{{Repo: "api", Tests: map[string]check.TestOutcome{"t-save-a2b6f8jk": check.TestPassed}}, {Repo: "api", Branch: "feature", Tests: map[string]check.TestOutcome{"t-save-a2b6f8jk": check.TestFailed, "t-new-h3v8n2wd": check.TestPassed}}},
		{{Repo: "api", Branch: "feature", Tests: map[string]check.TestOutcome{"t-save-a2b6f8jk": check.TestFailed, "t-new-h3v8n2wd": check.TestPassed}}, {Repo: "api", Tests: map[string]check.TestOutcome{"t-save-a2b6f8jk": check.TestPassed}}},
	} {
		m := Merge(order)
		if m.Tests["t-save-a2b6f8jk"] != check.TestPassed {
			t.Errorf("main's outcome = %q, want passed", m.Tests["t-save-a2b6f8jk"])
		}
		if _, ok := m.Tests["t-new-h3v8n2wd"]; ok {
			t.Error("a test only a branch ran is not main's outcome")
		}
	}
}

// TestMergeKeepsBranchRefsApart pins where a branch's citations go: not
// into Refs, which consumers read as the default branch's, but BranchRefs.
// promise:branch-apart
func TestMergeKeepsBranchRefsApart(t *testing.T) {
	t.Parallel()
	ref := ledger.RefRow{ID: "sess-save-k7m2p4xq", Doc: "docs/r.md", Line: 1}
	m := Merge([]Entry{
		{Repo: "docs", Refs: ledger.Refs{Rows: []ledger.RefRow{ref}}},
		{Repo: "docs", Branch: "feature", Refs: ledger.Refs{Rows: []ledger.RefRow{ref, ref}}},
	})
	if len(m.Refs) != 1 || m.Refs[0].Repo != "docs" || len(m.BranchRefs) != 2 {
		t.Errorf("refs %d, branch refs %d", len(m.Refs), len(m.BranchRefs))
	}
	if _, refs := m.Others("api"); len(refs) != 1 {
		t.Errorf("Others must hand out the default branch's refs only: %d", len(refs))
	}
}

// TestSnapshotRecordsOnlyCitedBranches pins which published branches a
// snapshot records: those a citation selects with branch=. Taking every
// branch of a cited id rewrote each downstream foreign.tsv whenever anyone
// published one, stamped with the default branch's commit.
func TestSnapshotRecordsOnlyCitedBranches(t *testing.T) {
	t.Parallel()
	def := func(branch, hash string) block.Block {
		args := map[string]string{block.KeyID: "sess-save-k7m2p4xq", RepoKey: "api"}
		if branch != "" {
			args[block.KeyBranch] = branch
		}
		return block.Block{ID: "sess-save-k7m2p4xq", Kind: block.KindFunc, Hash: hash, Args: args}
	}
	m := Merge([]Entry{
		{Repo: "api", Ledger: ledger.Ledger{Header: ledger.Header{Commit: "main1"}, Rows: []ledger.Row{ledger.FromBlock("api", def("", "aaa"))}}},
		{Repo: "api", Branch: "feature", Ledger: ledger.Ledger{Header: ledger.Header{Commit: "feat1"}, Rows: []ledger.Row{ledger.FromBlock("api", def("", "bbb"))}}},
	})
	main := m.Snapshot("docs", map[Cite]bool{{ID: "sess-save-k7m2p4xq"}: true}, ledger.Header{})
	if len(main.Rows) != 1 || main.Rows[0].Hash != "aaa" || main.Rows[0].Commit != "main1" {
		t.Errorf("a default-branch citation records main only: %+v", main.Rows)
	}
	both := m.Snapshot("docs", map[Cite]bool{{ID: "sess-save-k7m2p4xq"}: true, {ID: "sess-save-k7m2p4xq", Branch: "feature"}: true}, ledger.Header{})
	if len(both.Rows) != 2 {
		t.Fatalf("a branch citation adds its branch: %+v", both.Rows)
	}
	for _, r := range both.Rows {
		if r.Args[block.KeyBranch] == "feature" && (r.Hash != "bbb" || r.Commit != "feat1") {
			t.Errorf("the branch row carries the branch's hash and commit: %+v", r)
		}
	}
}

// TestMergeForNamesReposLikePublish pins that the merge keeps an entry under
// the same name publish files it under, whatever form the workspace lists
// the URL in, and records an unlisted repository's ids as removed.
func TestMergeForNamesReposLikePublish(t *testing.T) {
	t.Parallel()
	entry := func(repo, id string) Entry {
		return Entry{Repo: repo, Ledger: ledger.Ledger{Rows: []ledger.Row{{ID: id, Repo: repo}}}}
	}
	m := MergeFor([]Entry{entry("api", "a-a2b6f8jk"), entry("docs", "d-h3v8n2wd"), entry("web", "w-k7m2p4xq"), entry("gone", "g-t4k2b9rf")},
		[]string{"git@github.com:org/api.git", "https://github.com/org/docs", "web"})
	kept := map[string]bool{}
	for _, b := range m.Defs {
		kept[b.Args[RepoKey]] = true
	}
	if !kept["api"] || !kept["docs"] || !kept["web"] || kept["gone"] {
		t.Errorf("kept = %v", kept)
	}
	if m.Removed["g-t4k2b9rf"] != "gone" || len(m.Removed) != 1 {
		t.Errorf("removed = %v", m.Removed)
	}
}
