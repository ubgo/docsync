package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/check"
)

// ghServer is a fake GitHub REST API: comments per issue, PATCH by id.
type ghServer struct {
	mu       sync.Mutex
	comments map[int64]ghComment
	next     int64
	calls    []string
	fail     string // method+path prefix that returns 500
	// forbid are comment ids another account wrote, which GitHub refuses
	// to let this token edit.
	forbid map[int64]bool
}

func (g *ghServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if g.fail != "" && strings.HasPrefix(r.Method+" "+r.URL.Path, g.fail) {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var in map[string]string
	_ = json.Unmarshal(body, &in)
	switch {
	case r.Method == http.MethodGet:
		// Paginated the way GitHub is: oldest first, per_page at a time,
		// page from 1. A fake that returned every comment at once hid a
		// client that only ever read the first page.
		var list []ghComment
		for _, c := range g.comments {
			list = append(list, c)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if per <= 0 {
			per = 30
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page <= 0 {
			page = 1
		}
		start, end := (page-1)*per, page*per
		if start > len(list) {
			start = len(list)
		}
		if end > len(list) {
			end = len(list)
		}
		out := list[start:end]
		if out == nil {
			out = []ghComment{}
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodPost:
		g.next++
		g.comments[g.next] = ghComment{ID: g.next, Body: in["body"]}
		_ = json.NewEncoder(w).Encode(g.comments[g.next])
	case r.Method == http.MethodPatch:
		var id int64
		_, _ = sscanf(filepath.Base(r.URL.Path), &id)
		if g.forbid[id] {
			http.Error(w, "must have admin rights", http.StatusForbidden)
			return
		}
		c := g.comments[id]
		c.Body = in["body"]
		g.comments[id] = c
		_ = json.NewEncoder(w).Encode(c)
	}
}

func sscanf(s string, id *int64) (int, error) {
	n, err := jsonNumber(s)
	*id = n
	return 1, err
}

func jsonNumber(s string) (int64, error) {
	var n int64
	err := json.Unmarshal([]byte(s), &n)
	return n, err
}

func TestGitHubComment(t *testing.T) {
	// Not parallel: environment variables.
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	v.files["abc1234:internal/store/write.go"] = src
	write(t, dir, "internal/store/write.go", goV2)
	gh := &ghServer{comments: map[int64]ghComment{}}
	srv := httptest.NewServer(gh)
	defer srv.Close()
	write(t, dir, "event.json", `{"pull_request":{"number":7,"head":{"sha":"feedface"},"labels":[]},"sender":{"login":"reviewer"}}`)
	t.Setenv(envGitHubToken, "tok")
	t.Setenv(envGitHubRepository, "org/api")
	t.Setenv(envGitHubAPIURL, srv.URL)
	t.Setenv(githubEventPath, filepath.Join(dir, "event.json"))
	runGH := func(args ...string) result {
		t.Helper()
		var out, errb strings.Builder
		code := Run(append([]string{"github", "comment"}, args...), WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithHTTPClient(srv.Client()), WithClock(func() time.Time { return clock }))
		return result{code, out.String(), errb.String()}
	}
	// Dry run prints bodies and still exits with the findings.
	r := runGH("--dry-run")
	if r.code != ExitFindings || !strings.Contains(r.out, "<!-- docsync:doc=docs/sessions.md -->") || !strings.Contains(r.out, "[line 1](https://github.com/org/api/blob/feedface/docs/sessions.md#L1) **unacked** `sess-save-k7m2p4xq`") || !strings.Contains(r.out, "```diff") || !strings.Contains(r.out, "still true: `ds ack") || !strings.Contains(r.out, "fix: the id nope") {
		t.Errorf("dry = %+v", r)
	}
	// Every fence is a line of its own (bug 21): the indented diff carries no trailing
	// newline, and a closing fence glued to its last line left the code block
	// open, so the rest of the comment rendered inside it.
	for _, l := range strings.Split(r.out, "\n") {
		if f := strings.TrimSpace(l); strings.Contains(f, "```") && f != "```" && f != "```diff" {
			t.Errorf("a fence shares its line with content: %q", l)
		}
	}
	if len(gh.calls) != 0 {
		t.Errorf("dry run must not call GitHub: %v", gh.calls)
	}
	// First run posts one comment per doc.
	r = runGH()
	if r.code != ExitFindings || !strings.Contains(r.out, "docs/sessions.md: comment posted") || len(gh.comments) != 1 {
		t.Errorf("post = %+v %v", r, gh.comments)
	}
	// Second run finds it unchanged.
	if r := runGH(); !strings.Contains(r.out, "comment unchanged") || len(gh.comments) != 1 {
		t.Errorf("unchanged = %+v", r)
	}
	// A different report edits the comment; a doc that cleared is resolved.
	write(t, dir, "docs/other.md", "Also [save](ds:block?id=sess-save-k7m2p4xq).\n")
	if r := runGH(); !strings.Contains(r.out, "docs/other.md: comment posted") || !strings.Contains(r.out, "docs/sessions.md: comment unchanged") {
		t.Errorf("second doc = %+v", r)
	}
	os.Remove(filepath.Join(dir, "docs/other.md"))
	if r := runGH(); !strings.Contains(r.out, "docs/other.md: comment resolved") {
		t.Errorf("resolved = %+v", r)
	}
	if r := runGH(); strings.Contains(r.out, "docs/other.md") {
		t.Errorf("resolved stays quiet = %+v", r)
	}
	write(t, dir, "docs/sessions.md", "Save [s](ds:block?id=sess-save-k7m2p4xq).\n")
	if r := runGH(); !strings.Contains(r.out, "docs/sessions.md: comment updated") {
		t.Errorf("updated = %+v", r)
	}
	// A saved report is used as is; --pr overrides the event.
	// Reports live outside the tree so the scan never reads them as docs.
	outside := t.TempDir()
	report := filepath.Join(outside, "report.json")
	if r := run(t, dir, v, "check", "--json"); r.code != ExitFindings {
		t.Fatal(r)
	} else {
		write(t, outside, "report.json", r.out)
	}
	if r := runGH("--report", report, "--pr", "9", "--dry-run"); r.code != ExitFindings || !strings.Contains(r.out, "docsync:doc=docs/sessions.md") {
		t.Errorf("report file = %+v", r)
	}
	if r := runGH("--report", filepath.Join(dir, "missing.json")); r.code != ExitError {
		t.Errorf("missing report = %+v", r)
	}
	write(t, outside, "bad.json", "{")
	if r := runGH("--report", filepath.Join(outside, "bad.json")); r.code != ExitError {
		t.Errorf("bad report = %+v", r)
	}
	// The ack label acks every finding for the reviewer.
	// A push to a pull request that already carries the label acks nothing:
	// the sender of a synchronize event is whoever pushed, and acking there
	// let every later change through unreviewed, under the author's name.
	write(t, dir, "event.json", `{"action":"synchronize","pull_request":{"number":7,"head":{"sha":"feedface"},"labels":[{"name":"docs-acked"}]},"sender":{"login":"author"}}`)
	if r := runGH("--report", report); r.code == 0 || strings.Contains(r.out, "acked under label") || !strings.Contains(r.out, "re-apply") {
		t.Errorf("a push to a labelled pull request must not ack = %+v", r)
	}
	if _, _, acks, _ := NewStore(dir).LoadState(); len(acks.Rows) != 0 {
		t.Fatalf("a push acked %d findings", len(acks.Rows))
	}
	// Applying the label is the reviewer's act, and acks under their name.
	write(t, dir, "event.json", `{"action":"labeled","label":{"name":"docs-acked"},"pull_request":{"number":7,"head":{"sha":"feedface"},"labels":[{"name":"docs-acked"}]},"sender":{"login":"reviewer"}}`)
	r = runGH("--report", report)
	if r.code != 0 || !strings.Contains(r.out, "findings acked under label docs-acked; commit .ds/acks.tsv") {
		t.Errorf("label = %+v", r)
	}
	_, _, acks, _ := NewStore(dir).LoadState()
	if len(acks.Rows) == 0 || acks.Rows[0].Actor != "github:reviewer" || !strings.Contains(acks.Rows[0].Note, "pull request #7") {
		t.Errorf("acks = %+v", acks.Rows)
	}
	if r := runGH(); r.code != 0 || !strings.Contains(r.out, "0 findings acked") {
		t.Errorf("nothing left to ack = %+v", r)
	}
	if r := runGH("--ack-label", ""); r.code != 0 {
		t.Errorf("label disabled with nothing open = %+v", r)
	}
	// API failures surface: list, post, patch.
	write(t, dir, "event.json", `{"pull_request":{"number":7,"head":{"sha":"feedface"}},"sender":{"login":"reviewer"}}`)
	write(t, dir, "docs/sessions.md", "Broken [x](ds:block?id=nope-a2b6f8jk).\n")
	steps := []struct {
		prefix string
		before func()
	}{
		{"GET ", func() {}},
		{"POST ", func() { write(t, dir, "docs/new.md", "New [x](ds:block?id=nope-a2b6f8jk).\n") }},
		{"PATCH ", func() { write(t, dir, "docs/sessions.md", "Moved down.\n\nChanged [x](ds:block?id=nope-a2b6f8jk).\n") }},
		// Resolving a cleared doc is a PATCH too.
		{"PATCH ", func() { os.Remove(filepath.Join(dir, "docs/new.md")) }},
	}
	for _, st := range steps {
		st.before()
		gh.fail = st.prefix
		if r := runGH(); r.code != ExitError || !strings.Contains(r.err, "boom") {
			t.Errorf("fail %s = %+v", st.prefix, r)
		}
		gh.fail = ""
		runGH()
	}
	// An API URL that cannot form a request.
	t.Setenv(envGitHubAPIURL, "://bad")
	if r := runGH(); r.code != ExitError || !strings.Contains(r.err, "missing protocol scheme") {
		t.Errorf("bad api url = %+v", r)
	}
	t.Setenv(envGitHubAPIURL, srv.URL)
	// A wrong token is a clear error; a non-JSON body too.
	t.Setenv(envGitHubToken, "nope")
	if r := runGH(); r.code != ExitError || !strings.Contains(r.err, "401") {
		t.Errorf("unauthorized = %+v", r)
	}
	t.Setenv(envGitHubToken, "tok")
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) }))
	defer plain.Close()
	t.Setenv(envGitHubAPIURL, plain.URL)
	if r := runGH(); r.code != ExitError || !strings.Contains(r.err, "invalid character") {
		t.Errorf("bad json = %+v", r)
	}
	// Missing environment.
	t.Setenv(envGitHubRepository, "")
	if r := runGH(); r.code != ExitError || !strings.Contains(r.err, "GITHUB_TOKEN") {
		t.Errorf("env = %+v", r)
	}
	t.Setenv(envGitHubRepository, "org/api")
	t.Setenv(githubEventPath, "")
	if r := runGH(); r.code != ExitError {
		t.Errorf("no pr = %+v", r)
	}
	// An unreachable API without an injected client.
	t.Setenv(envGitHubAPIURL, "http://127.0.0.1:9/")
	if code := Run([]string{"github", "comment", "--pr", "1"}, WithDir(dir), WithIO(nil, io.Discard, io.Discard), WithVCS(v)); code != ExitError {
		t.Errorf("unreachable = %d", code)
	}
	if r := run(t, t.TempDir(), v, "github", "comment"); r.code != ExitError {
		t.Errorf("uninitialised = %+v", r)
	}
	// A report with no scan under the label runs a live check for acks; a
	// broken tree then fails.
	write(t, dir, "event.json", `{"action":"labeled","label":{"name":"docs-acked"},"pull_request":{"number":7,"labels":[{"name":"docs-acked"}]},"sender":{"login":"r"}}`)
	t.Setenv(githubEventPath, filepath.Join(dir, "event.json"))
	t.Setenv(envGitHubAPIURL, srv.URL)
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"[\"]\ndocs = [\"docs/**\"]\n")
	if r := runGH("--report", report, "--dry-run"); r.code != ExitError {
		t.Errorf("label with broken tree = %+v", r)
	}
}

func TestExportHugo(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	if r := run(t, dir, v, "export", "hugo"); r.code != ExitError {
		t.Errorf("no --out = %+v", r)
	}
	if r := run(t, dir, v, "export", "jekyll", "--out", "x"); r.code != ExitError {
		t.Errorf("unknown target = %+v", r)
	}
	r := run(t, dir, v, "export", "hugo", "--out", "site/data/docsync")
	if r.code != 0 || !strings.Contains(r.out, "exported 2 blocks") {
		t.Errorf("export = %+v", r)
	}
	var blocks map[string]exportedBlock
	raw, _ := os.ReadFile(filepath.Join(dir, "site/data/docsync", ExportBlocksFile))
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatal(err)
	}
	b := blocks["sess-save-k7m2p4xq"]
	if b.Lang != "go" || !strings.HasPrefix(b.Rendered, "```go\nfunc (s *Store) Save()") || b.Symbol != "Store.Save" || b.Change != "ok" {
		t.Errorf("block = %+v", b)
	}
	status, _ := os.ReadFile(filepath.Join(dir, "site/data/docsync", ExportStatusFile))
	if !strings.Contains(string(status), `"refs": [`) || !strings.Contains(string(status), `"state": "broken"`) {
		t.Errorf("status = %s", status)
	}
	write(t, dir, "blocker", "")
	if r := run(t, dir, v, "export", "hugo", "--out", "blocker/x"); r.code != ExitError {
		t.Errorf("unwritable = %+v", r)
	}
	if err := os.Mkdir(filepath.Join(dir, "site/data/docsync", ExportBlocksFile+".d"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(dir, "site/data/docsync", ExportBlocksFile))
	if err := os.Mkdir(filepath.Join(dir, "site/data/docsync", ExportBlocksFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, v, "export", "hugo", "--out", "site/data/docsync"); r.code != ExitError {
		t.Errorf("file blocked by a directory = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"[\"]\n")
	if r := run(t, dir, v, "export", "hugo", "--out", "x"); r.code != ExitError {
		t.Errorf("broken tree = %+v", r)
	}
	if r := run(t, t.TempDir(), v, "export", "hugo", "--out", "x"); r.code != ExitError {
		t.Errorf("uninitialised = %+v", r)
	}
	// The Hugo module renders the exported block through its shortcode.
	hugo, err := exec.LookPath("hugo")
	if err != nil {
		t.Skip("hugo not installed")
	}
	site := t.TempDir()
	write(t, site, "hugo.toml", "baseURL = 'https://example.org/'\ntitle = 'docs'\n[markup.goldmark.renderer]\nunsafe = true\n")
	write(t, site, "content/_index.md", "---\ntitle: Home\n---\n\nSave:\n\n{{< ds id=\"sess-save-k7m2p4xq\" >}}\n\nMissing: {{< ds id=\"nope-a2b6f8jk\" >}}\n")
	write(t, site, "layouts/index.html", "{{ .Content }}\n{{ partial \"docsync/status.html\" . }}\n")
	write(t, site, "layouts/_default/single.html", "{{ .Content }}\n{{ partial \"docsync/status.html\" . }}\n")
	// The page built from content/docs/sessions.md is the repository's
	// docs/sessions.md, so its status lists that page's references.
	write(t, site, "content/docs/sessions.md", "---\ntitle: Sessions\n---\n\nSessions.\n")
	write(t, site, "layouts/_default/list.html", "{{ .Content }}")
	shortcode, _ := os.ReadFile(filepath.Join("..", "integrations", "hugo", "layouts", "shortcodes", "ds.html"))
	partial, _ := os.ReadFile(filepath.Join("..", "integrations", "hugo", "layouts", "partials", "docsync", "status.html"))
	write(t, site, "layouts/shortcodes/ds.html", string(shortcode))
	write(t, site, "layouts/partials/docsync/status.html", string(partial))
	write(t, site, "data/docsync/blocks.json", string(raw))
	write(t, site, "data/docsync/status.json", string(status))
	cmd := exec.Command(hugo, "--quiet", "--destination", "public")
	cmd.Dir = site
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hugo: %v\n%s", err, out)
	}
	html, _ := os.ReadFile(filepath.Join(site, "public", "index.html"))
	if !strings.Contains(string(html), `class="language-go"`) || !strings.Contains(string(html), `data-ds-id="sess-save-k7m2p4xq" data-ds-change="ok"`) || !strings.Contains(string(html), "nope-a2b6f8jk not exported") {
		t.Errorf("hugo output = %s", html)
	}
	// The status partial lists only the page's own references (bug 112):
	// the home page cites nothing, the sessions page carries its own.
	if !strings.Contains(string(html), `class="docsync-status"`) || strings.Contains(string(html), "data-ds-doc=") {
		t.Errorf("home page status = %s", html)
	}
	sessions, _ := os.ReadFile(filepath.Join(site, "public", "docs", "sessions", "index.html"))
	if !strings.Contains(string(sessions), `data-ds-doc="docs/sessions.md"`) || !strings.Contains(string(sessions), `data-ds-state="broken"`) {
		t.Errorf("sessions page status = %s", sessions)
	}
}

// TestGitHubCommentFindsItselfPastTheFirstPage: on a busy pull request
// docsync's comment is not in the first page of comments. Reading one page
// missed it, and every push posted another copy.
func TestGitHubCommentFindsItselfPastTheFirstPage(t *testing.T) {
	// Not parallel: environment variables.
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", goV2)
	gh := &ghServer{comments: map[int64]ghComment{}}
	for i := 0; i < 2*commentPageSize+5; i++ {
		gh.next++
		gh.comments[gh.next] = ghComment{ID: gh.next, Body: "a human comment"}
	}
	srv := httptest.NewServer(gh)
	defer srv.Close()
	write(t, dir, "event.json", `{"action":"synchronize","pull_request":{"number":7,"head":{"sha":"feedface"}},"sender":{"login":"a"}}`)
	t.Setenv(envGitHubToken, "tok")
	t.Setenv(envGitHubRepository, "org/api")
	t.Setenv(envGitHubAPIURL, srv.URL)
	t.Setenv(githubEventPath, filepath.Join(dir, "event.json"))
	for i := 0; i < 3; i++ {
		var out, errb strings.Builder
		Run([]string{"github", "comment"}, WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithHTTPClient(srv.Client()), WithClock(func() time.Time { return clock }))
	}
	mine := 0
	for _, c := range gh.comments {
		if strings.HasPrefix(c.Body, "<!-- docsync:doc=") {
			mine++
		}
	}
	if mine != 1 {
		t.Errorf("three runs left %d docsync comments, want one kept up to date", mine)
	}
}

// TestCommentBodiesFitGitHubsLimit: a doc with more findings than fit in one
// comment still gets a comment GitHub accepts, and it says what it left out.
func TestCommentBodiesFitGitHubsLimit(t *testing.T) {
	t.Parallel()
	const githubCommentLimit = 65536
	var rep docsync.Report
	for i := 0; i < 400; i++ {
		rep.Findings = append(rep.Findings, check.Finding{State: check.StateUnacked, Severity: check.SeverityError, Doc: "docs/big.md", Line: i + 1, ID: fmt.Sprintf("b%d-k7m2p4xq", i), Message: "changed", Diff: strings.Repeat("-old line\n+new line\n", 20)})
	}
	body := commentBodies(rep, "https://github.com/o/r", "sha")["docs/big.md"]
	if len(body) > githubCommentLimit {
		t.Fatalf("body is %d characters, over GitHub's %d", len(body), githubCommentLimit)
	}
	if !strings.Contains(body, "more; run `ds check`") {
		t.Error("a truncated comment must say findings were left out")
	}
}

// TestGitHubCommentIgnoresASpoofedMarker: anyone can post a comment that
// starts with docsync's marker. docsync cannot edit it, and that used to
// fail the whole job; it now posts its own comment for a doc with findings
// and leaves a spoof for a clean doc alone.
func TestGitHubCommentIgnoresASpoofedMarker(t *testing.T) {
	// Not parallel: environment variables.
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", goV2)
	gh := &ghServer{comments: map[int64]ghComment{
		1: {ID: 1, Body: "<!-- docsync:doc=docs/sessions.md -->\nnot docsync"},
		2: {ID: 2, Body: "<!-- docsync:doc=docs/clean.md -->\nnot docsync either"},
	}, next: 2, forbid: map[int64]bool{1: true, 2: true}}
	srv := httptest.NewServer(gh)
	defer srv.Close()
	write(t, dir, "event.json", `{"action":"synchronize","pull_request":{"number":7,"head":{"sha":"feedface"}},"sender":{"login":"a"}}`)
	t.Setenv(envGitHubToken, "tok")
	t.Setenv(envGitHubRepository, "org/api")
	t.Setenv(envGitHubAPIURL, srv.URL)
	t.Setenv(githubEventPath, filepath.Join(dir, "event.json"))
	var out, errb strings.Builder
	code := Run([]string{"github", "comment"}, WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithHTTPClient(srv.Client()), WithClock(func() time.Time { return clock }))
	if code == ExitError || !strings.Contains(out.String(), "docs/sessions.md: comment posted") {
		t.Fatalf("code %d\nout %s\nerr %s", code, out.String(), errb.String())
	}
	if gh.comments[1].Body != "<!-- docsync:doc=docs/sessions.md -->\nnot docsync" || gh.comments[2].Body != "<!-- docsync:doc=docs/clean.md -->\nnot docsync either" {
		t.Error("someone else's comment was changed")
	}
}
