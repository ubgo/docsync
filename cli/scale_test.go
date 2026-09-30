package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/ledger"
)

func TestCollapseCacheAndShards(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	// Six pages cite the same block: findings collapse unless expanded.
	for i := 0; i < 6; i++ {
		write(t, dir, fmt.Sprintf("docs/p%d.md", i), "Cites [save](ds:block?id=sess-save-k7m2p4xq).\n")
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[ledger]\nshard = true\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ds", "ledger", "internal.tsv")); err != nil {
		t.Errorf("sharded ledger not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ds", "ledger.tsv")); err == nil {
		t.Error("single ledger must be removed when sharding")
	}
	if _, err := os.Stat(filepath.Join(dir, ".ds", "cache", "extract.json")); err != nil {
		t.Errorf("extraction cache not written: %v", err)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	v.files["abc1234:internal/store/write.go"] = src
	write(t, dir, "internal/store/write.go", goV2)
	r := run(t, dir, v, "check")
	if r.code != ExitFindings || !strings.Contains(r.out, "8 sentences cite sess-save-k7m2p4xq and are unacked") || strings.Contains(r.out, "docs/p3.md") {
		t.Errorf("collapsed = %+v", r)
	}
	if r := run(t, dir, v, "check", "--expand"); !strings.Contains(r.out, "docs/p3.md") {
		t.Errorf("expanded = %+v", r)
	}
	if r := run(t, dir, v, "check", "--full"); r.code != ExitFindings {
		t.Errorf("full = %+v", r)
	}
	// The sharded ledger reads back for a second run; a corrupt shard fails.
	if r := run(t, dir, v, "refresh"); r.code != 0 || !strings.Contains(r.out, "ledger updated") {
		t.Errorf("refresh with shards = %+v", r)
	}
	write(t, dir, ".ds/ledger/bad.tsv", "garbage\n")
	if r := run(t, dir, v, "check"); r.code != ExitError || !strings.Contains(r.err, "ledger/bad.tsv") {
		t.Errorf("corrupt shard = %+v", r)
	}
	os.Remove(filepath.Join(dir, ".ds", "ledger", "bad.tsv"))
	write(t, dir, ".ds/ledger/notes.txt", "ignored")
	if r := run(t, dir, v, "check"); r.code != ExitFindings {
		t.Errorf("non-tsv in shard dir is ignored = %+v", r)
	}
	// Turning sharding off collapses back to one file.
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ds", "ledger")); err == nil {
		t.Error("shard dir must be removed when not sharding")
	}
	// An empty tree sharded still writes a root shard so the header survives.
	empty := t.TempDir()
	write(t, empty, "docs/a.md", "text\n")
	write(t, empty, ".ds/config.toml", "[scan]\ndocs = [\"docs/**\"]\n[ledger]\nshard = true\n")
	if r := run(t, empty, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(empty, ".ds", "ledger", "_root.tsv")); err != nil {
		t.Error("root shard for an empty ledger")
	}
	// Cache: a corrupt file fails; a foreign format is ignored; a write
	// failure surfaces; a directory where the shard dir goes fails.
	write(t, dir, ".ds/cache/extract.json", "{")
	if r := run(t, dir, v, "check"); r.code != ExitError || !strings.Contains(r.err, "extract.json") {
		t.Errorf("corrupt cache = %+v", r)
	}
	write(t, dir, ".ds/cache/extract.json", `{"format":99,"entries":{"x":{}}}`)
	if r := run(t, dir, v, "check"); r.code != ExitFindings {
		t.Errorf("foreign cache format = %+v", r)
	}
	if err := os.Mkdir(filepath.Join(dir, ".ds", "cache", "extract.json"+writeTempExt), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, ".ds", "cache", "extract.json"))
	if r := run(t, dir, v, "check"); r.code != ExitError {
		t.Errorf("cache write failure = %+v", r)
	}
	if r := run(t, dir, v, "scan"); r.code != ExitError {
		t.Errorf("scan cache write failure = %+v", r)
	}
	if r := run(t, dir, v, "refresh"); r.code != ExitError {
		t.Errorf("refresh cache write failure = %+v", r)
	}
	os.Remove(filepath.Join(dir, ".ds", "cache", "extract.json"+writeTempExt))
	st := NewStore(dir)
	if err := os.RemoveAll(filepath.Join(dir, ".ds", "ledger")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".ds/ledger", "a file, not a dir")
	if _, _, _, err := st.LoadState(); err == nil {
		t.Error("ledger dir that is a file must fail")
	}
	if err := st.SaveLedgerSharded(ledger.Ledger{Rows: []ledger.Row{{ID: "a", File: "internal/a.go"}}}, ledger.Refs{}, true); err == nil {
		t.Error("shard write into a file must fail")
	}
	os.Remove(filepath.Join(dir, ".ds", "ledger"))
	// A non-empty directory where a shard's temp file goes survives the
	// sweep of old shards and blocks the write.
	write(t, dir, ".ds/ledger/internal.tsv"+writeTempExt+"/keep", "x")
	if err := st.SaveLedgerSharded(ledger.Ledger{Rows: []ledger.Row{{ID: "a", File: "internal/a.go"}}}, ledger.Refs{}, true); err == nil {
		t.Error("shard temp collision must fail")
	}
	os.RemoveAll(filepath.Join(dir, ".ds", "ledger"))
	write(t, dir, ".ds/ledger/"+ledger.RootShard+".tsv"+writeTempExt+"/keep", "x")
	if err := st.SaveLedgerSharded(ledger.Ledger{}, ledger.Refs{}, true); err == nil {
		t.Error("root shard temp collision must fail")
	}
	os.RemoveAll(filepath.Join(dir, ".ds", "ledger"))
	write(t, dir, ".ds/ledger/unreadable.tsv", "id\n")
	if err := os.Chmod(filepath.Join(dir, ".ds", "ledger", "unreadable.tsv"), 0); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.LoadState(); err == nil {
		t.Error("unreadable shard must fail")
	}
	// An unlistable directory: everywhere but as root.
	if dirPermsEnforced(t) {
		restore := restrictDir(t, filepath.Join(dir, ".ds", "ledger"), denyList)
		if err := st.SaveLedgerSharded(ledger.Ledger{}, ledger.Refs{}, true); err == nil {
			t.Error("unlistable shard dir must fail")
		}
		restore()
	}
	os.RemoveAll(filepath.Join(dir, ".ds", "ledger"))
	// The cache directory as a file blocks the cache write.
	os.RemoveAll(filepath.Join(dir, ".ds", CacheDir))
	write(t, dir, ".ds/"+CacheDir, "a file")
	if err := st.SaveCache(&extractCache{entries: map[string]cacheEntry{}, dirty: true}); err == nil {
		t.Error("cache dir as a file must fail")
	}
}

// promise:resolve-gated
func TestForkPRRenamesMetricsBranchRemoved(t *testing.T) {
	// Not parallel: the fork check reads GITHUB_EVENT_PATH.
	dir, v := initialised(t)
	ev := filepath.Join(dir, "event.json")
	write(t, dir, "event.json", `{"repository":{"full_name":"org/api"},"pull_request":{"head":{"repo":{"full_name":"fork/api"}}}}`)
	t.Setenv(githubEventPath, ev)
	if r := run(t, dir, v, "check", "--run"); r.code != ExitError || !strings.Contains(r.err, "forks") {
		t.Errorf("fork pr = %+v", r)
	}
	if r := run(t, dir, v, "check"); r.code != ExitFindings {
		t.Errorf("plain check on a fork is fine = %+v", r)
	}
	write(t, dir, "event.json", `{"repository":{"full_name":"org/api"},"pull_request":{"head":{"repo":{"full_name":"org/api"}}}}`)
	if r := run(t, dir, v, "check", "--resolve"); r.code == ExitError && strings.Contains(r.err, "forks") {
		t.Errorf("same-repo pr must not be refused = %+v", r)
	}
	// An event that cannot be read is treated as a fork (TestIsForkPR has the
	// full table). This line used to assert the opposite -- that an unreadable
	// event is trusted -- which is the fail-open defect itself.
	write(t, dir, "event.json", "not json")
	if !isForkPR(ev) || !isForkPR(filepath.Join(dir, "missing.json")) || isForkPR("") {
		t.Error("an unreadable named event is a fork; no event at all is not")
	}
	t.Setenv(githubEventPath, "")
	// A renamed doc keeps its acks, and nothing rewrites the append-only log
	// to make that so: the scan follows the citation to its new path. The
	// block changes first, so the ack is what makes the citation pass — an
	// assertion that also held without it would prove nothing.
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", goV2)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	if got := stateAt(t, run(t, dir, v, "check", "--json").out, "docs/sessions.md", 1, "sess-save-k7m2p4xq"); got != "unacked" {
		t.Fatalf("precondition: the change must be unacked before the ack, got %q", got)
	}
	if r := run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--doc", "docs/sessions.md", "--line", "1"); r.code != 0 {
		t.Fatal(r)
	}
	acksBefore, err := os.ReadFile(filepath.Join(dir, ".ds", AcksFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "docs/sessions.md"), filepath.Join(dir, "docs/moved.md")); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, v, "refresh", "--dry-run"); r.code != 0 || !strings.Contains(r.out, "nothing written (--dry-run)") {
		t.Errorf("rename dry = %+v", r)
	}
	if r := run(t, dir, v, "refresh"); r.code != 0 {
		t.Errorf("rename refresh = %+v", r)
	}
	if acksAfter, _ := os.ReadFile(filepath.Join(dir, ".ds", AcksFile)); string(acksAfter) != string(acksBefore) {
		t.Errorf("refresh rewrote the append-only ack log:\nbefore %q\nafter  %q", acksBefore, acksAfter)
	}
	// Line 1 is the acked citation. Line 3 cites the same block through a
	// comment and was never acked, so it stays unacked — which is also what
	// shows the ack went to the right citation and not to both.
	out := run(t, dir, v, "check", "--json").out
	if got := stateAt(t, out, "docs/moved.md", 1, "sess-save-k7m2p4xq"); got != "ok" {
		t.Errorf("the ack must follow the renamed doc: line 1 is %q", got)
	}
	if got := stateAt(t, out, "docs/moved.md", 3, "sess-save-k7m2p4xq"); got != "unacked" {
		t.Errorf("an ack for line 1 must not cover line 3: line 3 is %q", got)
	}
	// Metrics: mcp context/read record served bytes; report shows savings.
	resps, _ := rpc(t, dir, v, call(ToolContext, `{"target":"docs/moved.md"}`), call(ToolRead, `{"id":"sess-save-k7m2p4xq"}`))
	if len(resps) != 2 {
		t.Fatalf("mcp = %+v", resps)
	}
	served, source, err := NewStore(dir).LoadMetrics()
	if err != nil || served == 0 || source == 0 {
		t.Errorf("metrics = %d %d %v", served, source, err)
	}
	if r := run(t, dir, v, "report", "--metrics"); !strings.Contains(r.out, "context served to agents") {
		t.Errorf("report metrics = %+v", r)
	}
	write(t, dir, ".ds/metrics.json", "{")
	if r := run(t, dir, v, "report"); r.code != ExitError {
		t.Errorf("corrupt metrics = %+v", r)
	}
	if err := NewStore(dir).AddMetrics(1, 2); err == nil {
		t.Error("add onto corrupt metrics must fail")
	}
	os.Remove(filepath.Join(dir, ".ds", MetricsFile))
	if err := NewStore(dir).AddMetrics(1, 2); err != nil {
		t.Error(err)
	}
	if s, so, _ := NewStore(dir).LoadMetrics(); s != 1 || so != 2 {
		t.Error("add metrics")
	}
	// A busy prefix shows in the report.
	var b strings.Builder
	b.WriteString("package p\n")
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&b, "\n// ds:def id=busy-%d2b6f8jk\nvar V%d = %d\n", i, i, i)
	}
	write(t, dir, "internal/busy.go", b.String())
	if r := run(t, dir, v, "report", "--metrics"); !strings.Contains(r.out, "busy prefix busy: 6 ids") {
		t.Errorf("busy prefix = %+v", r)
	}
}

func TestPublishBranchAndRemovedRepo(t *testing.T) {
	t.Parallel()
	api, docs, index := twoRepos(t)
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "git@github.com:org/docs.git", files: map[string][]byte{}}
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatal(r)
	}
	// --branch on the default branch publishes the default entry.
	if r := run(t, api, apiVCS, "publish", "--branch"); r.code != 0 {
		t.Errorf("--branch on main = %+v", r)
	}
	// A release branch publishes under its own directory; docs cite it
	// with branch=.
	rel := fakeVCS{head: "r1", branch: "release-1", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	// The release branch's Save sits one line lower, so which def a cite
	// resolved to shows in the rendered line range (the index carries
	// positions and hashes, never source).
	write(t, api, "internal/store.go", "package store\n\n// release build\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.release()\n}\n\n// ds:def id=t-save-a2b6f8jk\nfunc TestSave(t *testing.T) {}\n")
	if r := run(t, api, rel, "publish"); r.code != ExitError {
		t.Errorf("non-default branch without --branch = %+v", r)
	}
	if r := run(t, api, fakeVCS{head: "r1", remote: "git@github.com:org/api.git"}, "publish", "--branch"); r.code != ExitError {
		t.Errorf("--branch detached = %+v", r)
	}
	r := run(t, api, rel, "publish", "--branch")
	if r.code != 0 || !strings.Contains(r.out, "published api") {
		t.Errorf("publish branch = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(index, "repos/api/@release-1/ledger.tsv")); err != nil {
		t.Errorf("branch dir not written: %v", err)
	}
	write(t, docs, "docs/runbook.md", "Main [s](ds:block?id=sess-save-k7m2p4xq). Release <!-- ds:block id=sess-save-k7m2p4xq branch=release-1 -->\n")
	var out, errb bytes.Buffer
	Run([]string{"render", "docs/runbook.md"}, WithDir(docs), WithIO(nil, &out, &errb), WithVCS(docsVCS), WithClock(func() time.Time { return clock }))
	if got := out.String(); !strings.Contains(got, "Main [s](internal/store.go#L4-L6)") || !strings.Contains(got, "`internal/store.go:5-7`") {
		t.Errorf("branch render = %s %s", got, errb.String())
	}
	// A repo removed from the workspace: its ids report "repo removed".
	write(t, index, "repos/legacy/ledger.tsv", "# docsync ledger format=1 repo=legacy commit=l1 scanned_at=2026-09-01T00:00:00Z\nid\trepo\tkind\tfile\tsymbol\tlines\thash\towner\tstability\tenv\targs\nold-h3v8n2wd\tlegacy\tfunc\tx.go\tX\t1-2\thh\t\tstable\t\tid=old-h3v8n2wd\n")
	write(t, docs, "docs/old.md", "[old](ds:block?id=old-h3v8n2wd)\n")
	r = run(t, docs, docsVCS, "check")
	if !strings.Contains(r.out, "removed from the workspace") {
		t.Errorf("repo removed = %+v", r)
	}
}

// stateAt returns the state `check --json` reported for one citation, or ""
// when there is none. Tests assert on a single finding with it rather than
// on substrings of the whole report, which pass whenever any finding matches.
func stateAt(t *testing.T, out, doc string, line int, id string) string {
	t.Helper()
	var rep struct {
		Findings []struct {
			State string `json:"state"`
			Doc   string `json:"doc"`
			Line  int    `json:"line"`
			ID    string `json:"id"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("check --json: %v\n%s", err, out)
	}
	for _, f := range rep.Findings {
		if f.Doc == doc && f.Line == line && f.ID == id {
			return f.State
		}
	}
	return ""
}

// TestIsForkPR is the full table for the guard that stops a pull request's own
// code from being executed with the repository's credentials: ds:run commands,
// resolver plugins, and the [review] command. It must fail CLOSED -- where it
// cannot show the code is the repository's own it refuses. It failed open for a
// malformed event and for a fork deleted after its PR was opened, and each ran
// `ds check --run`. Pins spec § Security, "never on pull requests from forks".
// promise:run-gated promise:fork-never-runs
func TestIsForkPR(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		event string // "" means no event file named at all
		named bool   // an event path is set but may not exist
		want  bool
	}{
		// Trusted: not on Actions, or the repository's own code.
		"no event: a developer's machine":   {"", false, false},
		"a push":                            {`{"repository":{"full_name":"o/r"},"ref":"refs/heads/main"}`, true, false},
		"a nightly schedule":                {`{"repository":{"full_name":"o/r"},"schedule":"17 3 * * *"}`, true, false},
		"a manual dispatch":                 {`{"repository":{"full_name":"o/r"},"inputs":{}}`, true, false},
		"a pull request from the same repo": {`{"repository":{"full_name":"o/r"},"pull_request":{"head":{"repo":{"full_name":"o/r"}}}}`, true, false},
		// Refused: a fork, or anything that cannot be shown not to be one.
		"a fork's pull request":          {`{"repository":{"full_name":"o/r"},"pull_request":{"head":{"repo":{"full_name":"evil/r"}}}}`, true, true},
		"a deleted fork: head.repo null": {`{"repository":{"full_name":"o/r"},"pull_request":{"head":{"repo":null}}}`, true, true},
		"a head repo with no name":       {`{"repository":{"full_name":"o/r"},"pull_request":{"head":{"repo":{"full_name":""}}}}`, true, true},
		"a head with no repo key":        {`{"repository":{"full_name":"o/r"},"pull_request":{"head":{}}}`, true, true},
		"a malformed event":              {`{not json`, true, true},
		"an empty event file":            {``, true, true},
	} {
		t.Run(name, func(t *testing.T) {
			path := ""
			if tc.named || tc.event != "" {
				path = filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
				if err := os.WriteFile(path, []byte(tc.event), filePerm); err != nil {
					t.Fatal(err)
				}
			}
			if got := isForkPR(path); got != tc.want {
				t.Errorf("isForkPR = %v, want %v", got, tc.want)
			}
		})
	}
	// A named event file that does not exist is unverifiable, so refused.
	if !isForkPR(filepath.Join(dir, "does-not-exist.json")) {
		t.Error("a named but missing event file must be treated as a fork")
	}
}

// TestReviewAIRefusedOnForkPR pins that `review --ai` never runs the [review]
// command on a fork's pull request: the command lives in .ds/config.toml, which
// the pull request can rewrite. Before this it had no fork check at all.
// promise:fork-never-runs
func TestReviewAIRefusedOnForkPR(t *testing.T) {
	// Not parallel: the fork check reads GITHUB_EVENT_PATH.
	dir, v := initialised(t)
	marker := filepath.Join(dir, "review-ran")
	write(t, dir, ".ds/config.toml", mustRead(t, filepath.Join(dir, ".ds/config.toml"))+"\n[review]\ncommand = \"touch "+marker+"\"\n")
	write(t, dir, "event.json", `{"repository":{"full_name":"o/r"},"pull_request":{"head":{"repo":{"full_name":"evil/r"}}}}`)
	t.Setenv(githubEventPath, filepath.Join(dir, "event.json"))
	r := run(t, dir, v, "review", "--ai")
	if r.code != ExitError || !strings.Contains(r.err, "review --ai is disabled on pull requests from forks") {
		t.Errorf("review --ai on a fork PR = %+v", r)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the [review] command ran on a fork's pull request")
	}
	// Without --ai it runs nothing, so it is allowed even there.
	if r := run(t, dir, v, "review"); r.code == ExitError && strings.Contains(r.err, "forks") {
		t.Errorf("plain review on a fork PR must not be refused = %+v", r)
	}
}

// mustRead returns a file's contents or fails the test.
func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
