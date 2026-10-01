package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync"
)

func TestTriageAuditBlame(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	v.user = "gituser"
	write(t, dir, "docs/other.md", "Also [save](ds:block?id=sess-save-k7m2p4xq).\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	if r := run(t, dir, v, "triage"); r.code != 0 || !strings.Contains(r.out, "nothing unacked") {
		t.Errorf("empty triage = %+v", r)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	v.files["abc1234:internal/store/write.go"] = src
	write(t, dir, "internal/store/write.go", goV2)
	r := run(t, dir, v, "triage")
	if r.code != 0 || !strings.Contains(r.out, "group 1: 3 sentence(s)") || !strings.Contains(r.out, "  | +\treturn s.sessions.Insert()") {
		t.Errorf("triage = %+v", r)
	}
	if r := run(t, dir, v, "triage", "--json"); !strings.Contains(r.out, `"groups"`) {
		t.Errorf("triage json = %+v", r)
	}
	if r := run(t, dir, v, "triage", "--ack-group", "9"); r.code != ExitError {
		t.Errorf("bad group = %+v", r)
	}
	tmp := filepath.Join(dir, DirName, AcksFile+writeTempExt)
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, v, "triage", "--ack-group", "1"); r.code != ExitError {
		t.Errorf("group ack with blocked acks = %+v", r)
	}
	os.Remove(tmp)
	r = run(t, dir, v, "triage", "--ack-group", "1", "--note", "rename only")
	if r.code != 0 || !strings.Contains(r.out, "acked group 1: 3 sentences") {
		t.Errorf("ack group = %+v", r)
	}
	if r := run(t, dir, v, "check"); r.code != ExitFindings || strings.Contains(r.out, "unacked") {
		t.Errorf("after group ack only the broken ref remains = %+v", r)
	}
	r = run(t, dir, v, "audit")
	if r.code != 0 || strings.Count(r.out, "gituser") != 3 || !strings.Contains(r.out, "rename only") {
		t.Errorf("audit = %+v", r)
	}
	if r := run(t, dir, v, "audit", "--since", "2030-01-01"); r.out != "" {
		t.Errorf("audit since future = %+v", r)
	}
	if r := run(t, dir, v, "audit", "--since", "yesterday"); r.code != ExitError {
		t.Errorf("audit bad date = %+v", r)
	}
	addOwners(t, dir, "k")
	if r := run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--doc", "docs/other.md", "--line", "1", "--agent", "--delegated-by", "k"); r.code != 0 {
		t.Fatal(r)
	}
	if r := run(t, dir, v, "audit", "--actor-kind", "agent", "--id", "sess-save-k7m2p4xq"); strings.Count(r.out, "\n") != 1 || !strings.Contains(r.out, "(agent, delegated by k)") {
		t.Errorf("audit agents = %+v", r)
	}
	if r := run(t, dir, v, "audit", "--json"); !strings.Contains(r.out, `"acks"`) {
		t.Errorf("audit json = %+v", r)
	}
	// Without a git user the actor is recorded as unknown.
	if r := run(t, dir, fakeVCS{head: "abc1234"}, "ack", "sess-save-k7m2p4xq", "--doc", "docs/other.md", "--line", "1"); r.code != 0 {
		t.Fatal(r)
	}
	if r := run(t, dir, v, "audit"); !strings.Contains(r.out, "\tunknown\t") {
		t.Errorf("unknown actor = %+v", r)
	}
	r = run(t, dir, v, "blame", "docs/sessions.md", "1")
	if r.code != 0 || !strings.Contains(r.out, "docs/sessions.md:1  ds:block sess-save-k7m2p4xq") || !strings.Contains(r.out, "block  internal/store/write.go:4-6") || !strings.Contains(r.out, "change changed") || !strings.Contains(r.out, "ack    2026-09-06 gituser rename only") {
		t.Errorf("blame = %+v", r)
	}
	if r := run(t, dir, v, "blame", "docs/sessions.md", "5"); r.code != 0 || !strings.Contains(r.out, "state  broken") {
		t.Errorf("blame broken = %+v", r)
	}
	if r := run(t, dir, v, "blame", "docs/sessions.md", "1", "--json"); !strings.Contains(r.out, `"reference"`) {
		t.Errorf("blame json = %+v", r)
	}
	if r := run(t, dir, v, "blame", "docs/sessions.md", "x"); r.code != ExitError {
		t.Errorf("blame bad line = %+v", r)
	}
	if r := run(t, dir, v, "blame", "docs/sessions.md", "99"); r.code != ExitError {
		t.Errorf("blame no ref = %+v", r)
	}
}

func TestRenameGraphReportAdoptUndo(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	v.churn = map[string]int{"internal/store/write.go": 3, "internal/hot.go": 40, "internal/missing.go": 99}
	write(t, dir, "internal/hot.go", "package store\n\nvar hot = 1\n")
	write(t, dir, "docs/legacy.md", "Old style [Persist](internal/store/write.go#Persist) and [again](internal/store/write.go#L8-L8) and [gone](internal/nope.go#X).\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/lonely.go", "package store\n\n// ds:def id=lonely-m2q1s8vt\nvar Lonely = 1\n")
	if r := run(t, dir, v, "ack", "auth-port-h3v8n2wd", "--doc", "docs/sessions.md", "--line", "1"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "docs/onlyport.md", "Port [8081](ds:cfg?id=auth-port-h3v8n2wd).\n")
	if r := run(t, dir, v, "ack", "auth-port-h3v8n2wd", "--doc", "docs/onlyport.md", "--line", "1"); r.code != 0 {
		t.Fatal(r)
	}
	// rename: dry, real, undo.
	r := run(t, dir, v, "rename", "sess", "session", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "sess-save-k7m2p4xq -> session-save-k7m2p4xq") || !strings.Contains(r.out, "would change") {
		t.Errorf("rename dry = %+v", r)
	}
	if r := run(t, dir, v, "rename", "zzz", "y"); r.code != ExitError {
		t.Errorf("rename nothing = %+v", r)
	}
	r = run(t, dir, v, "rename", "sess", "session")
	if r.code != 0 || !strings.Contains(r.out, "line(s) changed") {
		t.Errorf("rename = %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "docs/sessions.md")); !strings.Contains(string(b), "id=session-save-k7m2p4xq") {
		t.Errorf("rename not applied:\n%s", b)
	}
	if r := run(t, dir, v, "undo"); r.code != 0 || strings.Count(r.out, "undid") != 3 {
		t.Errorf("undo rename = %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "docs/sessions.md")); strings.Contains(string(b), "session-save") {
		t.Errorf("undo did not restore:\n%s", b)
	}
	if r := run(t, dir, v, "undo"); r.code != ExitError || !strings.Contains(r.err, "nothing to undo") {
		t.Errorf("empty undo = %+v", r)
	}
	// graph
	r = run(t, dir, v, "graph")
	if r.code != 0 || !strings.Contains(r.out, "docs/sessions.md -cites-> sess-save-k7m2p4xq") {
		t.Errorf("graph = %+v", r)
	}
	if r := run(t, dir, v, "graph", "--dot"); !strings.HasPrefix(r.out, "digraph docsync") {
		t.Errorf("graph dot = %+v", r)
	}
	if r := run(t, dir, v, "graph", "--json"); !strings.Contains(r.out, `"nodes"`) {
		t.Errorf("graph json = %+v", r)
	}
	// report: all sections, then single sections, then json. The tool's own
	// state directory never appears, even when git reports churn for it.
	v.churn[".ds/ledger.tsv"] = 7
	r = run(t, dir, v, "report")
	if strings.Contains(r.out, ".ds/") {
		t.Errorf("state dir leaked into report = %+v", r)
	}
	if r.code != 0 || !strings.Contains(r.out, "internal/hot.go  changed 40 times") || strings.Contains(r.out, "internal/missing.go") || !strings.Contains(r.out, "Persist") || !strings.Contains(r.out, "never acked") || !strings.Contains(r.out, "docs/onlyport.md  2026-09-06  1 cites") || !strings.Contains(r.out, "lonely-m2q1s8vt  internal/lonely.go:4") || !strings.Contains(r.out, "gaps (") || !strings.Contains(r.out, "mean time to ack") {
		t.Errorf("report = %+v", r)
	}
	for _, flag := range []string{"--uncovered", "--unmarked", "--stalest", "--literals", "--orphaned-owners", "--gaps", "--metrics"} {
		if r := run(t, dir, v, "report", flag, "--limit", "1"); r.code != 0 || r.out == "" {
			t.Errorf("report %s = %+v", flag, r)
		}
	}
	write(t, dir, "docs/typed.md", "The port is 8081 here.\n")
	if r := run(t, dir, v, "report", "--literals"); !strings.Contains(r.out, `"8081" is auth-port-h3v8n2wd`) {
		t.Errorf("report literals = %+v", r)
	}
	if r := run(t, dir, v, "report", "--json"); !strings.Contains(r.out, `"uncovered"`) {
		t.Errorf("report json = %+v", r)
	}
	// adopt: dry, real, undo.
	r = run(t, dir, v, "adopt", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "left alone docs/legacy.md:1 internal/nope.go#X: file not found") || !strings.Contains(r.out, "2 link(s) would be adopted") {
		t.Errorf("adopt dry = %+v", r)
	}
	r = run(t, dir, v, "adopt")
	if r.code != 0 || !strings.Contains(r.out, "2 link(s) adopted, 2 edit(s)") {
		t.Errorf("adopt = %+v", r)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	docb, _ := os.ReadFile(filepath.Join(dir, "docs/legacy.md"))
	if !strings.Contains(string(src), "// ds:def id=store-persist-") || strings.Count(string(docb), "ds:block?id=store-persist-") != 2 || !strings.Contains(string(docb), "internal/nope.go#X") {
		t.Errorf("adopt not applied:\n%s\n%s", src, docb)
	}
	if r := run(t, dir, v, "check"); strings.Contains(r.out, "docs/legacy.md") && strings.Contains(r.out, "broken") {
		t.Errorf("adopted cites must resolve = %+v", r)
	}
	if r := run(t, dir, v, "undo"); r.code != 0 || strings.Count(r.out, "undid") != 2 {
		t.Errorf("undo adopt = %+v", r)
	}
	if src2, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go")); string(src2) != goV1 {
		t.Errorf("undo adopt did not restore source:\n%s", src2)
	}
	// def journals too; a hand edit after the write refuses undo.
	if r := run(t, dir, v, "def", "internal/store/write.go#Persist"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", strings.Replace(goV1, "func (s *Store) Persist()", "// edited\nfunc (s *Store) Persist()", 1))
	if r := run(t, dir, v, "undo"); r.code != ExitError || !strings.Contains(r.err, "changed since the write") {
		t.Errorf("undo after hand edit = %+v", r)
	}
	// Adopt and rename fail cleanly when a source file cannot be written or
	// an id cannot be minted.
	if os.Getuid() != 0 {
		write(t, dir, "docs/ro.md", "[x](internal/store/write.go#Persist)\n")
		_ = os.Chmod(filepath.Join(dir, "internal/store/write.go"), 0o444)
		if r := run(t, dir, v, "adopt"); r.code != ExitError {
			t.Errorf("adopt on read-only source = %+v", r)
		}
		_ = os.Chmod(filepath.Join(dir, "config/auth.yaml"), 0o444)
		if r := run(t, dir, v, "rename", "auth-port", "port"); r.code != ExitError {
			t.Errorf("rename on read-only source = %+v", r)
		}
		_ = os.Chmod(filepath.Join(dir, "internal/store/write.go"), 0o644)
		_ = os.Chmod(filepath.Join(dir, "config/auth.yaml"), 0o644)
		os.Remove(filepath.Join(dir, "docs/ro.md"))
	}
	write(t, dir, "-.txt", "x\n")
	write(t, dir, "docs/dash.md", "[x](-.txt#L1)\n")
	if r := run(t, dir, v, "adopt"); r.code != ExitError {
		t.Errorf("adopt unmintable = %+v", r)
	}
	os.Remove(filepath.Join(dir, "docs/dash.md"))
	// Uninitialised undo.
	if r := run(t, t.TempDir(), v, "undo"); r.code != ExitError {
		t.Errorf("undo uninitialised = %+v", r)
	}
	// A failing stdout surfaces from graph --dot.
	if code := Run([]string{"graph", "--dot"}, WithDir(dir), WithIO(nil, failWriter{}, failWriter{}), WithVCS(v)); code != ExitError {
		t.Errorf("dot write failure = %d", code)
	}
	// Every new command fails cleanly on a bad glob.
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"[\"]\ndocs = [\"docs/**\"]\n")
	for _, args := range [][]string{{"triage"}, {"rename", "a", "b"}, {"graph"}, {"blame", "d", "1"}, {"report"}, {"adopt"}, {"repair"}} {
		if r := run(t, dir, v, args...); r.code != ExitError {
			t.Errorf("%v with a bad glob = %+v", args, r)
		}
	}
	os.Remove(filepath.Join(dir, ".ds", "config.toml"))
	for _, args := range [][]string{{"triage"}, {"audit"}, {"rename", "a", "b"}, {"graph"}, {"blame", "d", "1"}, {"report"}, {"adopt"}, {"repair"}} {
		if r := run(t, dir, v, args...); r.code != ExitError {
			t.Errorf("%v uninitialised = %+v", args, r)
		}
	}
}

func TestJournal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Journal(WriteDef, nil); err != nil {
		t.Errorf("empty journal write: %v", err)
	}
	if _, err := st.Undo(); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("undo empty: %v", err)
	}
	write(t, dir, "a.txt", "one\ntwo\n")
	e1 := docsync.Edit{File: "a.txt", Line: 2, New: "tab\there\\slash"}
	e2 := docsync.Edit{File: "a.txt", Line: 1, Old: "one", New: "uno"}
	if err := st.applyAll(WriteDef, []docsync.Edit{e1, e2}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "uno\ntab\there\\slash\ntwo\n" {
		t.Errorf("applied = %q", b)
	}
	entries, err := st.journal()
	if err != nil || len(entries) != 2 || entries[1].Edit.New != "uno" || entries[0].Edit.New != e1.New {
		t.Errorf("journal = %+v %v", entries, err)
	}
	// A second batch, then undo twice.
	if err := st.applyAll(WriteDef, []docsync.Edit{{File: "a.txt", Line: 3, New: "three"}}); err != nil {
		t.Fatal(err)
	}
	if got, err := st.Undo(); err != nil || len(got) != 1 {
		t.Errorf("undo batch 2 = %+v %v", got, err)
	}
	if got, err := st.Undo(); err != nil || len(got) != 2 {
		t.Errorf("undo batch 1 = %+v %v", got, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one\ntwo\n" {
		t.Errorf("restored = %q", b)
	}
	// A partial batch journals what landed.
	write(t, dir, "b.txt", "x\n")
	err = st.applyAll(WriteDef, []docsync.Edit{{File: "b.txt", Line: 1, New: "y"}, {File: "missing.txt", Line: 1, New: "z"}})
	if err == nil {
		t.Error("partial batch must report the failure")
	}
	if got, err := st.Undo(); err != nil || len(got) != 1 {
		t.Errorf("undo partial = %+v %v", got, err)
	}
	// Corrupt journal lines.
	write(t, dir, ".ds/journal.tsv", "1\ta.txt\n")
	if _, err := st.journal(); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("want %d fields", journalFields)) {
		t.Errorf("bad fields: %v", err)
	}
	// A time that is not RFC3339 is a corrupt journal, not an unknown one:
	// the empty string means unknown, so anything else must parse.
	write(t, dir, ".ds/journal.tsv", "1\tdef\tnope\ta.txt\t1\t\tnew\n")
	if _, err := st.journal(); err == nil || !strings.Contains(err.Error(), "bad time") {
		t.Errorf("bad time: %v", err)
	}
	write(t, dir, ".ds/journal.tsv", "x\ta.txt\t1\t\tnew\n")
	if _, err := st.Undo(); err == nil || !strings.Contains(err.Error(), "bad number") {
		t.Errorf("bad number: %v", err)
	}
	write(t, dir, ".ds/journal.tsv", "1\tmissing.txt\t1\t\tnew\n")
	if _, err := st.Undo(); err == nil {
		t.Error("missing file on undo")
	}
	write(t, dir, ".ds/journal.tsv", "1\ta.txt\t9\t\tnew\n")
	if _, err := st.Undo(); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Errorf("out of range: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".ds/journal.tsv", "1\tadir\t1\t\tnew\n")
	if _, err := st.Undo(); err == nil {
		t.Error("directory on undo")
	}
	if os.Getuid() != 0 {
		write(t, dir, "ro.txt", "keep\nnew\n")
		_ = os.Chmod(filepath.Join(dir, "ro.txt"), 0o444)
		write(t, dir, ".ds/journal.tsv", "1\tro.txt\t2\t\tnew\n")
		if _, err := st.Undo(); err == nil {
			t.Error("read-only file on undo")
		}
	}
	// Blank lines in the journal are skipped.
	write(t, dir, ".ds/journal.tsv", "1\ta.txt\t1\tone\tone\n\n")
	if entries, err := st.journal(); err != nil || len(entries) != 1 {
		t.Errorf("blank line = %+v %v", entries, err)
	}
	// An unreadable journal fails every operation that touches it.
	os.Remove(filepath.Join(dir, ".ds", JournalFile))
	if err := os.Mkdir(filepath.Join(dir, ".ds", JournalFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st.Journal(WriteDef, []docsync.Edit{{File: "a.txt", Line: 1, New: "q"}}); err == nil {
		t.Error("journal on a directory")
	}
	if _, err := st.Undo(); err == nil {
		t.Error("undo with unreadable journal")
	}
	write(t, dir, "c.txt", "c\n")
	if err := st.applyAll(WriteDef, []docsync.Edit{{File: "c.txt", Line: 1, New: "d"}}); err == nil {
		t.Error("applyAll surfaces the journal error")
	}
	if unesc(`a\tb\\c\nd\re\q`) != "a\tb\\c\nd\req" {
		t.Errorf("unesc = %q", unesc(`a\tb\\c\nd\re\q`))
	}
}

func TestRecordsSource(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "records/t1.md", "---\nkind: task\nstate: open\n---\n# Rotate keys\n")
	write(t, dir, "docs/board.md", "<!-- ds:table kind=task cols=title,state -->\n")
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[records]\nsource = \"frontmatter\"\npath = \"records/\"\n")
	r := run(t, dir, v, "render", "docs/board.md")
	if r.code != 0 || !strings.Contains(r.out, "| Rotate keys | open |") {
		t.Errorf("render table = %+v", r)
	}
	if r := run(t, dir, v, "check"); strings.Contains(r.out, "no record source") {
		t.Errorf("table with a source must not be unverifiable = %+v", r)
	}
}

func TestHTTPRecordsAndRepoModeRefresh(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"kind":"task","title":"Remote row","state":"open"}]`))
	}))
	defer srv.Close()
	dir, v := initialised(t)
	write(t, dir, "docs/board.md", "<!-- ds:table kind=task cols=title,state -->\n")
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[records]\nsource = \"http\"\npath = \""+srv.URL+"/records\"\n")
	var out, errb bytes.Buffer
	if code := Run([]string{"render", "docs/board.md"}, WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithHTTPClient(srv.Client())); code != 0 || !strings.Contains(out.String(), "| Remote row | open |") {
		t.Errorf("http records = %d %s %s", code, out.String(), errb.String())
	}
	if r := run(t, dir, v, "render", "docs/board.md"); r.code != 0 || !strings.Contains(r.out, "Remote row") {
		t.Errorf("default client = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[include]\nmode = \"repo\"\n")
	r := run(t, dir, v, "refresh")
	if r.code != 0 || !strings.Contains(r.out, "1 repo-mode copies rewritten") {
		t.Errorf("refresh repo = %+v", r)
	}
	doc, _ := os.ReadFile(filepath.Join(dir, "docs/sessions.md"))
	if !strings.Contains(string(doc), "<!-- /ds:block hash=") || !strings.Contains(string(doc), "return s.legacy.Save()") {
		t.Errorf("fence not written:\n%s", doc)
	}
	if r := run(t, dir, v, "check"); strings.Contains(r.out, "stale") || strings.Contains(r.out, "tampered") {
		t.Errorf("fresh copy = %+v", r)
	}
	if r := run(t, dir, v, "refresh"); !strings.Contains(r.out, "0 repo-mode copies") {
		t.Errorf("idempotent refresh = %+v", r)
	}
	write(t, dir, "internal/store/write.go", goV2)
	if r := run(t, dir, v, "check"); !strings.Contains(r.out, "stale") {
		t.Errorf("stale = %+v", r)
	}
	if r := run(t, dir, v, "refresh"); !strings.Contains(r.out, "1 repo-mode copies rewritten") {
		t.Errorf("refresh after change = %+v", r)
	}
	doc, _ = os.ReadFile(filepath.Join(dir, "docs/sessions.md"))
	if strings.Contains(string(doc), "legacy.Save()") || strings.Count(string(doc), "/ds:block") != 1 {
		t.Errorf("fence not replaced:\n%s", doc)
	}
	write(t, dir, "docs/sessions.md", strings.Replace(string(doc), "sessions.Insert()", "hacked()", 1))
	if r := run(t, dir, v, "check"); !strings.Contains(r.out, "tampered") {
		t.Errorf("tampered = %+v", r)
	}
	if os.Getuid() != 0 {
		_ = os.Chmod(filepath.Join(dir, "docs/sessions.md"), 0o444)
		if r := run(t, dir, v, "refresh"); r.code != ExitError {
			t.Errorf("refresh on read-only doc = %+v", r)
		}
		// A doc that becomes unreadable between the scan and the write is
		// reported, not skipped: scan first, then take the file away.
		app := &App{dir: dir, vcs: v, now: func() time.Time { return clock }, stderr: &bytes.Buffer{}}
		ld, err := app.system()
		if err != nil {
			t.Fatal(err)
		}
		res, err := ld.sys.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(filepath.Join(dir, "docs/sessions.md"), 0o000)
		if _, _, err := app.writeFences(ld.sys, res, ld.st, false); err == nil {
			t.Error("unreadable doc between scan and write must surface")
		}
		_ = os.Chmod(filepath.Join(dir, "docs/sessions.md"), 0o644)
	}
	// A repo-mode refresh honours --dry-run and reports a policy failure.
	if r := run(t, dir, v, "refresh", "--dry-run"); r.code != 0 || strings.Contains(r.out, "copies rewritten") {
		t.Errorf("dry run in repo mode = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[include]\nmode = \"repo\"\n[policy]\nrequire_doc = [\"[\"]\n")
	if r := run(t, dir, v, "refresh"); r.code != ExitError {
		t.Errorf("policy error after fences = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"[\"]\ndocs = [\"docs/**\"]\n[include]\nmode = \"repo\"\n")
	if r := run(t, dir, v, "refresh"); r.code != ExitError {
		t.Errorf("scan error in repo mode = %+v", r)
	}
}

func TestNewFlags(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "internal/zz_dup.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq tags=ops\nvar Dup = 1\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	// find --file / --tag and the usage error.
	if r := run(t, dir, v, "find", "--file", "config/"); r.code != 0 || !strings.Contains(r.out, "auth-port-h3v8n2wd") || strings.Contains(r.out, "sess-save") {
		t.Errorf("find --file = %+v", r)
	}
	if r := run(t, dir, v, "find", "save", "--tag", "ops"); r.code != 0 || strings.Count(r.out, "sess-save-k7m2p4xq") != 1 {
		t.Errorf("find --tag = %+v", r)
	}
	if r := run(t, dir, v, "find"); r.code != ExitError {
		t.Errorf("find without filters = %+v", r)
	}
	// check --explain lists every match with its tier.
	r := run(t, dir, v, "check", "--explain")
	if !strings.Contains(r.out, "internal/zz_dup.go:3") || !strings.Contains(r.out, "def const") || !strings.Contains(r.out, "ds:block") || !strings.Contains(r.out, "markdown") {
		t.Errorf("explain = %+v", r)
	}
	// def --fix re-mints the duplicate; then nothing is left to fix.
	r = run(t, dir, v, "def", "--fix", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "would be re-minted") {
		t.Errorf("fix dry = %+v", r)
	}
	r = run(t, dir, v, "def", "--fix")
	if r.code != 0 || !strings.Contains(r.out, "sess-save-k7m2p4xq@internal/zz_dup.go:3 -> sess-save-") || !strings.Contains(r.out, "1 def(s) re-minted") {
		t.Errorf("fix = %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "internal/zz_dup.go")); strings.Contains(string(b), "k7m2p4xq") {
		t.Errorf("duplicate not re-minted:\n%s", b)
	}
	if r := run(t, dir, v, "def", "--fix"); !strings.Contains(r.out, "no duplicated ids") {
		t.Errorf("fix clean = %+v", r)
	}
	if r := run(t, dir, v, "def"); r.code != ExitError || !strings.Contains(r.err, "target or --fix") {
		t.Errorf("def without target = %+v", r)
	}
	if os.Getuid() != 0 {
		write(t, dir, "internal/zz_dup2.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq\nvar Dup2 = 1\n")
		_ = os.Chmod(filepath.Join(dir, "internal/zz_dup2.go"), 0o444)
		if r := run(t, dir, v, "def", "--fix"); r.code != ExitError {
			t.Errorf("fix on read-only file = %+v", r)
		}
		_ = os.Chmod(filepath.Join(dir, "internal/zz_dup2.go"), 0o644)
		os.Remove(filepath.Join(dir, "internal/zz_dup2.go"))
	}
	// ack --group N acks one triage group.
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	v.files["abc1234:internal/store/write.go"] = src
	write(t, dir, "internal/store/write.go", goV2)
	if r := run(t, dir, v, "ack", "--group", "5"); r.code != ExitError {
		t.Errorf("ack missing group = %+v", r)
	}
	tmp := filepath.Join(dir, DirName, AcksFile+writeTempExt)
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, v, "ack", "--group", "1"); r.code != ExitError {
		t.Errorf("group ack with a blocked log = %+v", r)
	}
	os.Remove(tmp)
	r = run(t, dir, v, "ack", "--group", "1", "--note", "swap")
	if r.code != 0 || !strings.Contains(r.out, "acked group 1: 2 sentences") {
		t.Errorf("ack group = %+v", r)
	}
	if r := run(t, dir, v, "check"); strings.Contains(r.out, "unacked") {
		t.Errorf("after group ack = %+v", r)
	}
	// audit --export writes JSON lines.
	exp := filepath.Join(dir, "acks.jsonl")
	r = run(t, dir, v, "audit", "--export", exp)
	if r.code != 0 || !strings.Contains(r.out, "exported 2 event(s)") {
		t.Errorf("export = %+v", r)
	}
	if b, _ := os.ReadFile(exp); strings.Count(string(b), "\n") != 2 || !strings.Contains(string(b), `"Note":"swap"`) {
		t.Errorf("export file = %s", b)
	}
	if r := run(t, dir, v, "audit", "--export", filepath.Join(dir, "nodir", "x.jsonl")); r.code != ExitError {
		t.Errorf("export to a missing dir = %+v", r)
	}
	// A duplicate whose label cannot be re-minted fails --fix.
	write(t, dir, "notes.txt", "ds:def id=-\none\nds:def id=-\ntwo\n")
	if r := run(t, dir, v, "def", "--fix"); r.code != ExitError {
		t.Errorf("unmintable duplicate = %+v", r)
	}
	os.Remove(filepath.Join(dir, "notes.txt"))
	// A policy error surfaces from the group ack's check.
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[policy]\nrequire_doc = [\"[\"]\n")
	if r := run(t, dir, v, "ack", "--group", "1"); r.code != ExitError {
		t.Errorf("group ack with a policy error = %+v", r)
	}
	// Every new path fails cleanly on a broken tree.
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"[\"]\ndocs = [\"docs/**\"]\n")
	for _, args := range [][]string{{"find", "--tag", "x"}, {"def", "--fix"}, {"ack", "--group", "1"}} {
		if r := run(t, dir, v, args...); r.code != ExitError {
			t.Errorf("%v with a bad glob = %+v", args, r)
		}
	}
	os.Remove(filepath.Join(dir, ".ds/config.toml"))
	if r := run(t, dir, v, "ack", "--group", "1"); r.code != ExitError {
		t.Errorf("group ack uninitialised = %+v", r)
	}
}

// TestFindListsByPlace pins bug 32 at the command: `ds find` sorted its rows
// by id, so two defs sharing a label were listed in the order of their
// random suffixes. They are listed by file and line.
func TestFindListsByPlace(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "internal/limits.go", "package store\n\n// ds:def id=limit-zzzzzzzz\nvar A = 1\n\n// ds:def id=limit-aaaaaaaa\nvar B = 2\n")
	r := run(t, dir, v, "find", "limit")
	if r.code != 0 || strings.Index(r.out, "limit-zzzzzzzz") > strings.Index(r.out, "limit-aaaaaaaa") || !strings.Contains(r.out, "limit-aaaaaaaa") {
		t.Errorf("find = %+v", r)
	}
}
