package cli

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/workspace"
)

// twoRepos builds an api repo and a docs repo sharing a local index dir.
func twoRepos(t *testing.T) (api, docs, index string) {
	t.Helper()
	index = t.TempDir()
	write(t, index, "ds-workspace.toml", "[workspace]\nname = \"platform\"\nrepos = [\"github.com/org/api\", \"github.com/org/docs\"]\ndefault_branch = \"main\"\n")
	api = t.TempDir()
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.legacy.Save()\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	write(t, api, "docs/api.md", "Internal note.\n")
	docs = t.TempDir()
	write(t, docs, "docs/runbook.md", "Writes go through [Save](ds:block?id=sess-save-k7m2p4xq). Tested by [it](ds:block?id=t-save-a2b6f8jk&assert=true).\n")
	for _, d := range []string{api, docs} {
		write(t, d, ".ds/config.toml", "workspace = "+strconvQuote(index)+"\n[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	}
	return api, docs, index
}

func strconvQuote(s string) string { return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"` }

// promise:pr-never-publish
func TestWorkspacePublishSyncCheck(t *testing.T) {
	t.Parallel()
	api, docs, index := twoRepos(t)
	var pushes []string
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}, pushes: &pushes}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}

	// Before api publishes, docs' cite is broken.
	if r := run(t, docs, docsVCS, "check"); r.code != ExitFindings || !strings.Contains(r.out, "broken") {
		t.Errorf("docs before publish = %+v", r)
	}
	// Publish refuses off the default branch and without a workspace.
	if r := run(t, api, fakeVCS{head: "a1", branch: "feature"}, "publish"); r.code != ExitError || !strings.Contains(r.err, "default branch") {
		t.Errorf("publish off branch = %+v", r)
	}
	if r := run(t, api, fakeVCS{head: "a1"}, "publish"); r.code != ExitError {
		t.Errorf("publish detached = %+v", r)
	}
	plain := t.TempDir()
	write(t, plain, ".ds/config.toml", "[scan]\ndocs = [\"**/*.md\"]\n")
	if r := run(t, plain, apiVCS, "publish"); r.code != ExitError || !strings.Contains(r.err, "no workspace") {
		t.Errorf("publish without workspace = %+v", r)
	}
	if r := run(t, plain, apiVCS, "sync"); r.code != ExitError {
		t.Errorf("sync without workspace = %+v", r)
	}
	// Publish with test outcomes.
	junit := filepath.Join(api, "junit.xml")
	write(t, api, "junit.xml", `<testsuites><testsuite name="p"><testcase name="TestSave"><failure/></testcase></testsuite></testsuites>`)
	r := run(t, api, apiVCS, "publish", "--tests", junit)
	if r.code != 0 || !strings.Contains(r.out, "published api: 2 defs, 0 refs, 1 test outcomes") || strings.Contains(r.out, "pushed") || len(pushes) != 0 {
		t.Fatalf("publish = %+v pushes=%v", r, pushes)
	}
	for _, f := range []string{"repos/api/ledger.tsv", "repos/api/refs.tsv", "repos/api/tests.tsv"} {
		if _, err := os.Stat(filepath.Join(index, f)); err != nil {
			t.Errorf("%s not written: %v", f, err)
		}
	}
	// A relative --tests is the repository's file, not the process's
	// working directory's: the same report publishes the same outcome.
	if r := run(t, api, apiVCS, "publish", "--tests", "junit.xml"); r.code != 0 || !strings.Contains(r.out, "1 test outcomes") {
		t.Errorf("relative --tests = %+v", r)
	}
	if r := run(t, api, apiVCS, "publish", "--tests", filepath.Join(api, "missing.xml")); r.code != ExitError {
		t.Errorf("missing junit = %+v", r)
	}
	write(t, api, "bad.xml", "not xml")
	if r := run(t, api, apiVCS, "publish", "--tests", filepath.Join(api, "bad.xml")); r.code != ExitError {
		t.Errorf("bad junit = %+v", r)
	}
	// Sync from docs shows api; docs' check now resolves the cite through
	// the merged view and the failed test flags the assert.
	r = run(t, docs, docsVCS, "sync")
	if r.code != 0 || !strings.Contains(r.out, "api") || !strings.Contains(r.out, "a1") {
		t.Errorf("sync = %+v", r)
	}
	if r := run(t, docs, docsVCS, "sync", "--json"); !strings.Contains(r.out, `"platform"`) {
		t.Errorf("sync json = %+v", r)
	}
	r = run(t, docs, docsVCS, "check")
	if r.code != ExitFindings || strings.Contains(r.out, "broken") || !strings.Contains(r.out, "assert failed") {
		t.Errorf("docs after publish = %+v", r)
	}
	// docs publishes its refs; api's check now sees the docs page depending
	// on Save when Save changes.
	if r := run(t, docs, docsVCS, "publish"); r.code != 0 || !strings.Contains(r.out, "published docs: 0 defs, 2 refs") {
		t.Fatalf("docs publish = %+v", r)
	}
	if r := run(t, api, apiVCS, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	src, _ := os.ReadFile(filepath.Join(api, "internal/store.go"))
	apiVCS.files["a1:internal/store.go"] = src
	write(t, api, "internal/store.go", strings.Replace(string(src), "legacy.Save()", "sessions.Insert()", 1))
	r = run(t, api, apiVCS, "check", "--json")
	if r.code != ExitFindings || !strings.Contains(r.out, `"doc_repo": "docs"`) || !strings.Contains(r.out, `"doc": "docs/runbook.md"`) {
		t.Errorf("api sees docs' dependency = %+v", r)
	}
	if r := run(t, api, apiVCS, "impact"); !strings.Contains(r.out, "docs/runbook.md") {
		t.Errorf("impact across repos = %+v", r)
	}
	// A second listed repo publishing the same id is rejected.
	dup := t.TempDir()
	write(t, dup, "x.go", "package x\n\n// ds:def id=sess-save-k7m2p4xq\nvar X = 1\n")
	write(t, dup, ".ds/config.toml", "workspace = "+strconvQuote(index)+"\n[scan]\ncode = [\"**\"]\n")
	if r := run(t, dup, fakeVCS{head: "x1", branch: "main", remote: "https://github.com/org/docs"}, "publish", "--force"); r.code != ExitError || !strings.Contains(r.err, "more than one repository") {
		t.Errorf("duplicate publish = %+v", r)
	}
	// Uninitialised repos cannot sync or publish.
	if r := run(t, t.TempDir(), docsVCS, "sync"); r.code != ExitError {
		t.Errorf("sync uninitialised = %+v", r)
	}
	if r := run(t, t.TempDir(), docsVCS, "publish"); r.code != ExitError {
		t.Errorf("publish uninitialised = %+v", r)
	}
	// A duplicate that slipped into the index (a copied directory) is warned
	// about, once the workspace lists both; an unlisted repo is "removed",
	// not a duplicate.
	write(t, index, "ds-workspace.toml", "[workspace]\nname = \"platform\"\nrepos = [\"github.com/org/api\", \"github.com/org/docs\", \"github.com/org/api-copy\", \"github.com/org/old\"]\ndefault_branch = \"main\"\n")
	copied, _ := os.ReadFile(filepath.Join(index, "repos/api/ledger.tsv"))
	write(t, index, "repos/api-copy/ledger.tsv", string(copied))
	if r := run(t, docs, docsVCS, "sync"); !strings.Contains(r.err, "is published by api and api-copy") {
		t.Errorf("duplicate warning = %+v", r)
	}
	os.RemoveAll(filepath.Join(index, "repos/api-copy"))
	// Stale and duplicate warnings surface on sync and publish.
	write(t, index, "repos/old/ledger.tsv", "# docsync ledger format=1 repo=old commit=o1 scanned_at=2020-01-01T00:00:00Z\nid\trepo\tkind\tfile\tsymbol\tlines\thash\towner\tstability\tenv\targs\n")
	r = run(t, docs, docsVCS, "sync")
	if !strings.Contains(r.err, "index for old is") {
		t.Errorf("stale warning = %+v", r)
	}
	if r := run(t, docs, docsVCS, "publish"); r.code != 0 || !strings.Contains(r.err, "index for old is") {
		t.Errorf("publish warns too = %+v", r)
	}
	// Index write failures: a file where the repo dir goes, then a dir
	// where the ledger goes.
	os.RemoveAll(filepath.Join(index, "repos/docs"))
	write(t, index, "repos/docs", "squatter")
	if r := run(t, docs, docsVCS, "publish"); r.code != ExitError {
		t.Errorf("publish over a file = %+v", r)
	}
	os.Remove(filepath.Join(index, "repos/docs"))
	if err := os.MkdirAll(filepath.Join(index, "repos/docs/ledger.tsv"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run(t, docs, docsVCS, "publish"); r.code != ExitError {
		t.Errorf("publish onto a dir = %+v", r)
	}
	os.RemoveAll(filepath.Join(index, "repos/docs"))
	// Publish surfaces a broken ledger and a bad glob.
	write(t, docs, ".ds/ledger.tsv", "garbage\n")
	if r := run(t, docs, docsVCS, "publish"); r.code != ExitError || !strings.Contains(r.err, "ledger.tsv") {
		t.Errorf("publish with corrupt ledger = %+v", r)
	}
	os.Remove(filepath.Join(docs, ".ds/ledger.tsv"))
	write(t, docs, ".ds/config.toml", "workspace = "+strconvQuote(index)+"\n[scan]\ncode = [\"[\"]\ndocs = [\"docs/**\"]\n")
	if r := run(t, docs, docsVCS, "publish"); r.code != ExitError {
		t.Errorf("publish with bad glob = %+v", r)
	}
	// A relative index directory inside the repo works too.
	rel := t.TempDir()
	write(t, rel, "docs/a.md", "x\n")
	write(t, rel, "idx/.keep", "")
	write(t, rel, ".ds/config.toml", "workspace = \"idx\"\n[scan]\ndocs = [\"docs/**\"]\n")
	if r := run(t, rel, fakeVCS{head: "r1", branch: "main", files: map[string][]byte{}}, "publish"); r.code != 0 || !strings.Contains(r.out, "published") {
		t.Errorf("relative index = %+v", r)
	}
	var out bytes.Buffer
	if code := Run([]string{"sync", "--json"}, WithDir(rel), WithIO(nil, failWriter{}, &out), WithVCS(fakeVCS{head: "r1"})); code != ExitError {
		t.Errorf("sync json write failure = %d", code)
	}
	// A corrupt workspace file or index entry fails clearly.
	write(t, index, "ds-workspace.toml", "[workspace]\n")
	if r := run(t, docs, docsVCS, "sync"); r.code != ExitError || !strings.Contains(r.err, "ds-workspace.toml") {
		t.Errorf("bad workspace file = %+v", r)
	}
	write(t, index, "ds-workspace.toml", "[workspace]\nname = \"platform\"\nrepos = [\"a\"]\n")
	write(t, index, "repos/bad/ledger.tsv", "garbage\n")
	if r := run(t, docs, docsVCS, "sync"); r.code != ExitError {
		t.Errorf("bad index entry = %+v", r)
	}
	os.RemoveAll(filepath.Join(index, "repos/bad"))
	// Without the default_branch key, main is assumed.
	if r := run(t, api, fakeVCS{head: "a1", branch: "master", files: map[string][]byte{}}, "publish"); r.code != ExitError || !strings.Contains(r.err, `default is "main"`) {
		t.Errorf("fallback branch = %+v", r)
	}
}

// promise:offline-cached
func TestWorkspaceRemoteIndex(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	write(t, repo, "docs/a.md", "text\n")
	write(t, repo, ".ds/config.toml", "workspace = \"git@github.com:org/ds-index.git\"\n[scan]\ndocs = [\"docs/**\"]\n")
	var pushes []string
	v := fakeVCS{head: "c1", branch: "main", files: map[string][]byte{}, pushes: &pushes}
	// Offline with no cache: sync fails, check still fails clearly, and a
	// command that only reads the cache has none.
	if r := run(t, repo, v, "sync"); r.code != ExitError || !strings.Contains(r.err, "unreachable") {
		t.Errorf("offline no cache = %+v", r)
	}
	if r := run(t, repo, v, "map"); r.code != ExitError || !strings.Contains(r.err, "unreachable") {
		t.Errorf("map with no cache = %+v", r)
	}
	if r := run(t, repo, v, "check"); r.code != ExitError {
		t.Errorf("check offline no cache = %+v", r)
	}
	// Online: clone, publish pushes.
	v.cloneOK = true
	if r := run(t, repo, v, "sync"); r.code != 0 || !strings.Contains(r.out, "cloned git@github.com:org/ds-index.git") {
		t.Errorf("clone = %+v", r)
	}
	r := run(t, repo, v, "publish")
	if r.code != 0 || !strings.Contains(r.out, "pushed") || len(pushes) != 1 || !strings.Contains(pushes[0], "docsync publish") {
		t.Errorf("publish pushes = %+v pushes=%v", r, pushes)
	}
	v.pushErr = errNoPush
	if r := run(t, repo, v, "publish"); r.code != ExitError {
		t.Errorf("push failure = %+v", r)
	}
	// Later, offline again: the cached copy serves check with a warning.
	v.err = ErrNoVCS
	r = run(t, repo, v, "check")
	if r.code != 0 || !strings.Contains(r.err, "using the cached copy") {
		t.Errorf("cached check = %+v", r)
	}
	// Commands that do not fetch use the cache silently.
	if r := run(t, repo, v, "map"); r.code != 0 || strings.Contains(r.err, "cached") {
		t.Errorf("map uses cache quietly = %+v", r)
	}
	// A repo name falls back to the directory when there is no remote.
	if r := run(t, repo, v, "publish", "--force"); r.code != ExitError && !strings.Contains(r.out, filepath.Base(repo)) {
		t.Errorf("repo name = %+v", r)
	}
}

var errNoPush = os.ErrPermission

// TestOnlyWorkspaceReposPublish pins "only the canonical URL may publish".
// The index directory is the URL's last segment, so a fork at someone/api
// wrote over org/api's published ledger, and a repository the workspace
// does not list published rows the merge reported as a removed repo's.
// promise:publish-canonical
func TestOnlyWorkspaceReposPublish(t *testing.T) {
	t.Parallel()
	api, _, index := twoRepos(t)
	canonical := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	if r := run(t, api, canonical, "publish"); r.code != 0 {
		t.Fatalf("the canonical repo publishes: %+v", r)
	}
	published := filepath.Join(index, "repos", "api", "ledger.tsv")
	before, err := os.ReadFile(published)
	if err != nil {
		t.Fatal(err)
	}
	// A fork with the same last segment, publishing different content.
	fork := t.TempDir()
	write(t, fork, "internal/store.go", "package store\n\n// ds:def id=fork-only-h3v8n2wd\nfunc Fork() {}\n")
	write(t, fork, ".ds/config.toml", "workspace = "+strconvQuote(index)+"\n[scan]\ncode = [\"**\"]\n")
	forkVCS := fakeVCS{head: "f1", branch: "main", remote: "git@github.com:someone/api.git", files: map[string][]byte{}}
	if r := run(t, fork, forkVCS, "publish"); r.code != ExitError || !strings.Contains(r.err, "not listed") {
		t.Errorf("a fork must not publish: %+v", r)
	}
	if after, _ := os.ReadFile(published); string(after) != string(before) {
		t.Error("the fork overwrote the canonical repo's published ledger")
	}
	// A repository the workspace does not list at all.
	if r := run(t, fork, fakeVCS{head: "f1", branch: "main", remote: "https://github.com/org/unlisted", files: map[string][]byte{}}, "publish"); r.code != ExitError || !strings.Contains(r.err, "not listed") {
		t.Errorf("an unlisted repo must not publish: %+v", r)
	}
	// Without a remote there is no URL to check; the directory name must be
	// a listed repository's.
	named := filepath.Join(t.TempDir(), "docs")
	write(t, named, "docs/a.md", "x\n")
	write(t, named, ".ds/config.toml", "workspace = "+strconvQuote(index)+"\n[scan]\ndocs = [\"docs/**\"]\n")
	if r := run(t, named, fakeVCS{head: "d1", branch: "main", files: map[string][]byte{}}, "publish"); r.code != 0 {
		t.Errorf("a listed name without a remote publishes: %+v", r)
	}
	if r := run(t, fork, fakeVCS{head: "f1", branch: "main", files: map[string][]byte{}}, "publish"); r.code != ExitError || !strings.Contains(r.err, "not the name of any repository") {
		t.Errorf("an unlisted name without a remote = %+v", r)
	}
}

// TestPublishDryRun pins `publish --dry-run`: it writes nothing into the
// index, pushes nothing, and says in one line how the index would change.
// A mistake in publish reaches every repo that syncs, so it must be
// possible to look first.
func TestPublishDryRun(t *testing.T) {
	t.Parallel()
	api, _, index := twoRepos(t)
	var pushes []string
	v := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}, pushes: &pushes}
	repoDir := filepath.Join(index, "repos", "api")
	r := run(t, api, v, "publish", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "would publish api: 2 defs") || !strings.Contains(r.out, "index would change: 2 defs added") {
		t.Fatalf("first dry run = %+v", r)
	}
	if _, err := os.Stat(repoDir); !os.IsNotExist(err) {
		t.Fatalf("a dry run wrote into the index: %v", err)
	}
	if r := run(t, api, v, "publish"); r.code != 0 {
		t.Fatal(r)
	}
	mtimes := func() map[string]time.Time {
		m := map[string]time.Time{}
		_ = filepath.WalkDir(repoDir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				info, _ := d.Info()
				m[p] = info.ModTime()
			}
			return nil
		})
		return m
	}
	before := mtimes()
	if r := run(t, api, v, "publish", "--dry-run"); r.code != 0 || !strings.Contains(r.out, "index unchanged") {
		t.Errorf("republishing the same state = %+v", r)
	}
	write(t, api, "internal/store.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.changed()\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if r := run(t, api, v, "publish", "--dry-run"); r.code != 0 || !strings.Contains(r.out, "index would change: 1 defs changed") {
		t.Errorf("one changed block = %+v", r)
	}
	if after := mtimes(); !reflect.DeepEqual(before, after) || len(before) == 0 {
		t.Errorf("a dry run touched index files:\nbefore %v\nafter  %v", before, after)
	}
	if len(pushes) != 0 {
		t.Errorf("a local-directory index is never pushed: %v", pushes)
	}
}

// TestIndexDiffCountsEachKind covers every part of the one-line summary.
func TestIndexDiffCountsEachKind(t *testing.T) {
	t.Parallel()
	row := func(id, hash string) ledger.Row {
		return ledger.Row{ID: id, Hash: hash, File: "a.go", Start: 1, End: 1}
	}
	ref := func(id string, line int, acked string) ledger.RefRow {
		return ledger.RefRow{ID: id, Doc: "d.md", Line: line, Verb: "block", AckedHash: acked}
	}
	old := workspace.Entry{
		Repo:   "api",
		Ledger: ledger.Ledger{Rows: []ledger.Row{row("a", "1"), row("b", "1"), row("gone", "1")}},
		Refs:   ledger.Refs{Rows: []ledger.RefRow{ref("a", 1, "h"), ref("b", 2, "h"), ref("gone", 3, "h")}},
		Tests:  map[string]check.TestOutcome{"t": check.TestPassed},
	}
	next := workspace.Entry{
		Repo:   "api",
		Ledger: ledger.Ledger{Rows: []ledger.Row{row("a", "1"), row("b", "2"), row("new", "1")}},
		Refs:   ledger.Refs{Rows: []ledger.RefRow{ref("a", 1, "h"), ref("b", 2, "h2"), ref("new", 4, "h")}},
		Tests:  map[string]check.TestOutcome{"t": check.TestFailed},
	}
	want := "index would change: 1 defs added, 1 defs changed, 1 defs removed, 1 refs added, 1 refs changed, 1 refs removed, test outcomes changed"
	if got := indexDiff(&old, next); got != want {
		t.Errorf("indexDiff =\n%s\nwant\n%s", got, want)
	}
	if got := indexDiff(&old, old); got != "index unchanged" {
		t.Errorf("same entry = %s", got)
	}
	if got := indexDiff(nil, workspace.Entry{}); got != "index unchanged" {
		t.Errorf("nothing to nothing = %s", got)
	}
}
