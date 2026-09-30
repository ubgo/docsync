package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/block"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/ledger"
)

const (
	goV1 = "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.legacy.Save()\n}\n\nfunc (s *Store) Persist() error {\n\treturn nil\n}\n"
	goV2 = "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.sessions.Insert()\n}\n\nfunc (s *Store) Persist() error {\n\treturn nil\n}\n"
	yml  = "auth:\n  port: 8081   # ds:def id=auth-port-h3v8n2wd\n  host: h\n"
	doc  = "Every write goes through [`Save`](ds:block?id=sess-save-k7m2p4xq). Port [8081](ds:cfg?id=auth-port-h3v8n2wd).\n\n<!-- ds:block id=sess-save-k7m2p4xq -->\n\nBroken [x](ds:block?id=nope-a2b6f8jk).\n"
)

var clock = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

// fakeVCS answers from memory; Files maps "commit:path" to content.
type fakeVCS struct {
	head   string
	err    error
	files  map[string][]byte
	user   string
	churn  map[string]int
	branch string
	remote string
	// pushes records Push calls; pushErr fails them.
	pushes  *[]string
	pushErr error
	cloneOK bool
	staged  []string
}

func (f fakeVCS) User() string                   { return f.user }
func (f fakeVCS) Churn() (map[string]int, error) { return f.churn, f.err }
func (f fakeVCS) Branch() (string, error) {
	if f.branch == "" {
		return "", errors.New("detached")
	}
	return f.branch, nil
}
func (f fakeVCS) RemoteURL() string { return f.remote }
func (f fakeVCS) Staged() ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.staged, nil
}

func (f fakeVCS) Message(commit string) (string, error) {
	if m, ok := f.files["msg:"+commit]; ok {
		return string(m), nil
	}
	return "", errors.New("no such commit")
}

func (f fakeVCS) Clone(url, dir string) error {
	if !f.cloneOK {
		return errors.New("offline")
	}
	return os.MkdirAll(dir, 0o755)
}
func (f fakeVCS) Pull(string) error { return f.err }
func (f fakeVCS) Push(dir, message string) error {
	if f.pushes != nil {
		*f.pushes = append(*f.pushes, dir+": "+message)
	}
	return f.pushErr
}

func (f fakeVCS) Head() (string, error) { return f.head, f.err }
func (f fakeVCS) Show(commit, path string) ([]byte, error) {
	if b, ok := f.files[commit+":"+path]; ok {
		return b, nil
	}
	return nil, errors.New("no such object")
}
func (f fakeVCS) Exists(commit string) bool { return commit == f.head }

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "internal/store/write.go", goV1)
	write(t, dir, "config/auth.yaml", yml)
	write(t, dir, "docs/sessions.md", doc)
	return dir
}

type result struct {
	code     int
	out, err string
}

func run(t *testing.T, dir string, vcs VCS, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, WithDir(dir), WithIO(strings.NewReader(""), &out, &errb), WithVCS(vcs), WithClock(func() time.Time { return clock }))
	return result{code, out.String(), errb.String()}
}

func initialised(t *testing.T) (string, fakeVCS) {
	t.Helper()
	dir := fixture(t)
	v := fakeVCS{head: "abc1234", files: map[string][]byte{}}
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatalf("init: %+v", r)
	}
	return dir, v
}

func TestRunAndMain(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	if r := run(t, dir, fakeVCS{}); r.code != 0 || !strings.Contains(r.out, "keep docs bound") {
		t.Errorf("help = %+v", r)
	}
	if r := run(t, dir, fakeVCS{}, "bogus"); r.code != ExitError || !strings.Contains(r.err, "ds: unknown command") {
		t.Errorf("unknown = %+v", r)
	}
	if r := run(t, dir, fakeVCS{}, "check"); r.code != ExitError || !strings.Contains(r.err, "run `init` first") {
		t.Errorf("uninitialised = %+v", r)
	}
	// Main swaps the exit function.
	code := -1
	Exit = func(c int) { code = c }
	defer func() { Exit = os.Exit }()
	os.Args = []string{"ds", "doctor"}
	var out bytes.Buffer
	Main(WithDir(dir), WithIO(strings.NewReader(""), &out, &out), WithVCS(fakeVCS{}), WithName("pds"))
	if code != ExitError || !strings.Contains(out.String(), "FAIL") {
		t.Errorf("main = %d %q", code, out.String())
	}
	if (exitCode(3)).Error() != "exit 3" {
		t.Error("exit code text")
	}
	if r := Run(nil, WithDefaults(Standard()), WithDir(dir), WithIO(nil, &out, &out), WithVCS(fakeVCS{})); r != 0 {
		t.Errorf("standard defaults = %d", r)
	}
}

func TestInitAndDoctor(t *testing.T) {
	t.Parallel()
	dir := fixture(t)
	v := fakeVCS{head: "abc1234"}
	r := run(t, dir, v, "init", "--agents")
	if r.code != 0 || !strings.Contains(r.out, "wrote .ds/config.toml") || !strings.Contains(r.out, "wrote .ds/AGENTS.md") {
		t.Fatalf("init = %+v", r)
	}
	for _, f := range []string{ConfigFile, LedgerFile, RefsFile, AcksFile, CISnippet, AgentsFile} {
		if _, err := os.Stat(filepath.Join(dir, DirName, f)); err != nil {
			t.Errorf("%s not written: %v", f, err)
		}
	}
	cfg, err := NewStore(dir).LoadConfig()
	if err != nil || cfg.Scan.Docs[0] != "docs/**" || len(cfg.Scan.Generated) == 0 {
		t.Errorf("config = %+v %v", cfg, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, DirName, LedgerFile))
	if !strings.Contains(string(raw), "commit=abc1234") {
		t.Errorf("ledger header = %s", raw)
	}
	if r := run(t, dir, v, "init"); r.code != ExitError || !strings.Contains(r.err, "--force") {
		t.Errorf("second init = %+v", r)
	}
	if r := run(t, dir, v, "init", "--force"); r.code != 0 {
		t.Errorf("forced init = %+v", r)
	}
	// Without a docs dir, markdown anywhere is scanned; config defaults hook applies.
	bare := t.TempDir()
	var out bytes.Buffer
	code := Run([]string{"init"}, WithDir(bare), WithIO(nil, &out, &out), WithVCS(fakeVCS{err: ErrNoVCS}), WithConfigDefaults(func(c *config.Config) { c.Prefix = "dx" }))
	if code != 0 {
		t.Fatalf("bare init: %s", out.String())
	}
	if c, _ := NewStore(bare).LoadConfig(); c.Scan.Docs[0] != "**/*.md" || c.Prefix != "dx" {
		t.Errorf("bare config = %+v", c)
	}
	// A file where .ds should be makes every write fail.
	blocked := t.TempDir()
	write(t, blocked, DirName, "not a dir")
	if r := run(t, blocked, v, "init"); r.code != ExitError {
		t.Errorf("blocked init = %+v", r)
	}
	// Doctor: healthy, then each degraded row.
	r = run(t, dir, v, "doctor")
	if r.code != 0 || !strings.Contains(r.out, "HEAD abc1234") || !strings.Contains(r.out, "glob docs/**") || !strings.Contains(r.out, "markdown, document, config, code, text") {
		t.Errorf("doctor = %+v", r)
	}
	// A WARN is a degraded but working repo and does not fail the run.
	if r := run(t, dir, fakeVCS{err: ErrNoVCS}, "doctor"); !strings.Contains(r.out, "git") || !strings.Contains(r.out, doctorWarn) || r.code != 0 {
		t.Errorf("doctor without git = %+v", r)
	}
	// A ledger recorded under a newer extraction rule is refused by name
	// everywhere (§33 Versioning): doctor fails, and check and scan refuse
	// without touching the file, since an older scan would rewrite it under
	// the older rule.
	led, err := os.ReadFile(filepath.Join(dir, ".ds", "ledger.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	newer := fmt.Sprintf("extract=%d", extract.Rule+1)
	stamped := strings.Replace(string(led), fmt.Sprintf("extract=%d", extract.Rule), newer, 1)
	write(t, dir, ".ds/ledger.tsv", stamped)
	if r := run(t, dir, v, "doctor"); r.code != ExitError || !strings.Contains(r.out, "ledger          "+doctorFail) || !strings.Contains(r.out, newer) {
		t.Errorf("doctor on a newer-rule ledger = %+v", r)
	}
	for _, c := range []string{"check", "scan"} {
		if r := run(t, dir, v, c); r.code != ExitError || !strings.Contains(r.err, newer) || !strings.Contains(r.err, "set extract=1") {
			t.Errorf("%s on a newer-rule ledger = %+v", c, r)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, ".ds", "ledger.tsv")); string(got) != stamped {
		t.Error("a refused scan rewrote the ledger")
	}
	// The remedy the refusal gives for a pre-release repo is the whole fix.
	write(t, dir, ".ds/ledger.tsv", strings.Replace(stamped, newer, "extract=1", 1))
	if r := run(t, dir, v, "check"); strings.Contains(r.err, "extraction rule") || r.code == ExitError {
		t.Errorf("check after the remedy = %+v", r)
	}
	write(t, dir, ".ds/ledger.tsv", string(led))
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"[\"]\ndocs = [\"nothing/**\"]\n")
	write(t, dir, ".ds/ledger.tsv", "garbage\n")
	r = run(t, dir, v, "doctor")
	if !strings.Contains(r.out, "glob [") || !strings.Contains(r.out, doctorFail) || !strings.Contains(r.out, "matches no files") || !strings.Contains(r.out, "ledger") {
		t.Errorf("degraded doctor = %+v", r)
	}
	// Any FAIL row fails the run, so a setup step that runs doctor stops.
	if r.code != ExitError {
		t.Errorf("doctor with FAIL rows exited %d, want %d", r.code, ExitError)
	}
	var eout bytes.Buffer
	Run([]string{"doctor"}, WithDir(dir), WithIO(nil, &eout, &eout), WithVCS(v), WithExtractor(txtOnly{}))
	if !strings.Contains(eout.String(), "txtonly, markdown") {
		t.Errorf("extra extractor listed: %s", eout.String())
	}
	if r := run(t, dir, v, "check"); r.code != ExitError || !strings.Contains(r.err, "ledger.tsv") {
		t.Errorf("corrupt ledger blocks commands = %+v", r)
	}
}

type txtOnly struct{}

func (txtOnly) Name() string                                 { return "txtonly" }
func (txtOnly) Match(p string) bool                          { return strings.HasSuffix(p, ".txt") }
func (txtOnly) Extract(string, []byte, string) extract.Found { return extract.Found{} }

func TestDefScanCheckAck(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	// def: dry run, apply, existing.
	r := run(t, dir, v, "def", "internal/store/write.go#Persist", "--owner", "@auth", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "would insert at internal/store/write.go:8") {
		t.Fatalf("def dry = %+v", r)
	}
	r = run(t, dir, v, "def", "internal/store/write.go#Persist", "--owner", "@auth", "--stability", "api")
	if r.code != 0 || !strings.HasPrefix(r.out, "store-persist-") {
		t.Fatalf("def = %+v", r)
	}
	id := strings.TrimSpace(r.out)
	src, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	if !strings.Contains(string(src), "// ds:def id="+id+" owner=@auth stability=api\nfunc (s *Store) Persist()") {
		t.Errorf("directive not inserted:\n%s", src)
	}
	if r := run(t, dir, v, "def", "internal/store/write.go#Persist"); r.code != 0 || strings.TrimSpace(r.out) != id {
		t.Errorf("existing def = %+v", r)
	}
	if r := run(t, dir, v, "def", "nope.go#X"); r.code != ExitError {
		t.Errorf("bad target = %+v", r)
	}
	// scan writes the ledger and prints problems.
	write(t, dir, "internal/dup.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq\nvar dup = 1\n")
	r = run(t, dir, v, "scan")
	if r.code != 0 || !strings.Contains(r.out, "4 defs, 4 refs, 2 problems") || !strings.Contains(r.out, "internal/dup.go:3  scan: id defined more than once") {
		t.Fatalf("scan = %+v", r)
	}
	os.Remove(filepath.Join(dir, "internal/dup.go"))
	if r := run(t, dir, v, "scan"); r.code != 0 || !strings.Contains(r.out, "3 defs, 4 refs, 0 problems") {
		t.Fatalf("scan = %+v", r)
	}
	// A directory squatting on the temp path makes the atomic write fail.
	for _, name := range []string{LedgerFile, AcksFile} {
		tmp := filepath.Join(dir, DirName, name+writeTempExt)
		if err := os.Mkdir(tmp, 0o755); err != nil {
			t.Fatal(err)
		}
		if name == LedgerFile {
			if r := run(t, dir, v, "scan"); r.code != ExitError {
				t.Errorf("scan with blocked ledger = %+v", r)
			}
			if r := run(t, dir, v, "refresh"); r.code != ExitError {
				t.Errorf("refresh with blocked ledger = %+v", r)
			}
		} else if r := run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--all"); r.code != ExitError {
			t.Errorf("ack with blocked acks = %+v", r)
		}
		os.Remove(tmp)
	}
	// A file squatting on .ds/blocks makes the body store unwritable, and
	// scan must fail rather than quietly leave the store empty: an empty
	// store downgrades every later drift to an unclassified one (§20.1).
	blocked := filepath.Join(dir, DirName, BlocksDir)
	if err := os.RemoveAll(blocked); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, v, "scan"); r.code != ExitError {
		t.Errorf("scan with a blocked body store = %+v", r)
	}
	os.Remove(blocked)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatalf("scan must recover once the body store is writable = %+v", r)
	}
	if os.Getuid() != 0 {
		_ = os.Chmod(filepath.Join(dir, "config/auth.yaml"), 0o444)
		if r := run(t, dir, v, "def", "config/auth.yaml#auth.host"); r.code != ExitError {
			t.Errorf("def on a read-only file = %+v", r)
		}
		_ = os.Chmod(filepath.Join(dir, "config/auth.yaml"), 0o644)
	}
	// check: broken ref and uncovered def; text then json.
	r = run(t, dir, v, "check")
	if r.code != ExitFindings || !strings.Contains(r.out, "broken") || !strings.Contains(r.out, "fix: the id nope-a2b6f8jk") || !strings.Contains(r.out, "1 error") {
		t.Errorf("check = %+v", r)
	}
	r = run(t, dir, v, "check", "--json", "--strict", "--env", "prod", "--full")
	if r.code != ExitFindings || !strings.Contains(r.out, `"json_format": 1`) {
		t.Errorf("check json = %+v", r)
	}
	// Change the block: unacked with a diff from the fake VCS, then ack it.
	v.files["abc1234:internal/store/write.go"] = src
	write(t, dir, "internal/store/write.go", strings.Replace(string(src), "s.legacy.Save()", "s.sessions.Insert()", 1))
	r = run(t, dir, v, "check")
	if !strings.Contains(r.out, "unacked") || !strings.Contains(r.out, "still true: ds ack sess-save-k7m2p4xq --doc docs/sessions.md --line 1") {
		t.Errorf("unacked check = %+v", r)
	}
	if r := run(t, dir, v, "ack", "sess-save-k7m2p4xq"); r.code != ExitError || !strings.Contains(r.err, "--doc and --line") {
		t.Errorf("ack usage = %+v", r)
	}
	if r := run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--doc", "docs/sessions.md", "--line", "1", "--agent"); r.code != ExitError || !strings.Contains(r.err, "delegated_by") {
		t.Errorf("agent ack = %+v", r)
	}
	if r := run(t, dir, v, "ack", "nope-zz", "--all"); r.code != ExitError || !strings.Contains(r.err, "nothing cites") {
		t.Errorf("ack nothing = %+v", r)
	}
	r = run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--doc", "docs/sessions.md", "--line", "1", "--note", "still true", "--actor", "khanakia")
	if r.code != 0 || !strings.Contains(r.out, "acked sess-save-k7m2p4xq at docs/sessions.md:1 (human)") {
		t.Fatalf("ack = %+v", r)
	}
	r = run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--all", "--agent", "--delegated-by", "khanakia")
	if r.code != 0 || strings.Count(r.out, "(agent)") != 2 {
		t.Errorf("ack all = %+v", r)
	}
	_, _, acks, _ := NewStore(dir).LoadState()
	if len(acks.Rows) != 3 || acks.Rows[0].Note != "still true" || acks.Rows[2].DelegatedBy != "khanakia" {
		t.Errorf("acks = %+v", acks.Rows)
	}
	if r := run(t, dir, v, "check"); strings.Contains(r.out, "unacked") {
		t.Errorf("after ack = %+v", r)
	}
	// status and impact.
	r = run(t, dir, v, "status")
	if r.code != 0 || !strings.Contains(r.out, "docs/sessions.md:5\tbroken\tnope-a2b6f8jk") {
		t.Errorf("status = %+v", r)
	}
	if r := run(t, dir, v, "status", "--json"); !strings.Contains(r.out, `"refs"`) {
		t.Errorf("status json = %+v", r)
	}
	r = run(t, dir, v, "impact")
	if r.code != 0 || !strings.Contains(r.out, "docs/sessions.md (1)") || !strings.Contains(r.out, "owner (none): 1") {
		t.Errorf("impact = %+v", r)
	}
	if r := run(t, dir, v, "impact", "--json"); !strings.Contains(r.out, `"by_doc"`) {
		t.Errorf("impact json = %+v", r)
	}
	write(t, dir, "docs/sessions.md", "nothing cited\n")
	if r := run(t, dir, v, "impact"); !strings.Contains(r.out, "no documented behaviour") {
		t.Errorf("empty impact = %+v", r)
	}
	if r := run(t, dir, v, "check"); r.code != 0 || !strings.Contains(r.out, "info") {
		t.Errorf("check with only info findings passes = %+v", r)
	}
	// A failing stdout surfaces from --json.
	var errOut bytes.Buffer
	if code := Run([]string{"check", "--json"}, WithDir(dir), WithIO(nil, failWriter{}, &errOut), WithVCS(v)); code != ExitError {
		t.Errorf("json write failure = %d %s", code, errOut.String())
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestRefreshRender(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	// Move the block to another file: refresh reports and rewrites.
	write(t, dir, "internal/store/write.go", "package store\n\nfunc (s *Store) Persist() error {\n\treturn nil\n}\n")
	write(t, dir, "internal/store/save.go", "package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn s.legacy.Save()\n}\n")
	r := run(t, dir, v, "refresh", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "moved sess-save-k7m2p4xq: internal/store/write.go:4 -> internal/store/save.go:4") || !strings.Contains(r.out, "nothing written") {
		t.Errorf("refresh dry = %+v", r)
	}
	r = run(t, dir, v, "refresh")
	if r.code != 0 || !strings.Contains(r.out, "1 moved; ledger updated") {
		t.Errorf("refresh = %+v", r)
	}
	if r := run(t, dir, v, "refresh"); !strings.Contains(r.out, "0 moved") {
		t.Errorf("second refresh = %+v", r)
	}
	// render to stdout and to a file; notes on stderr.
	r = run(t, dir, v, "render", "docs/sessions.md")
	if r.code != 0 || !strings.Contains(r.out, "```go\nfunc (s *Store) Save()") || !strings.Contains(r.out, "Port 8081.") || !strings.Contains(r.err, "nope-a2b6f8jk is not defined") {
		t.Errorf("render = %+v", r)
	}
	outFile := filepath.Join(dir, "out.md")
	if r := run(t, dir, v, "render", "docs/sessions.md", "--out", outFile, "--env", "prod"); r.code != 0 {
		t.Errorf("render out = %+v", r)
	}
	if b, err := os.ReadFile(outFile); err != nil || !strings.Contains(string(b), "Port 8081") {
		t.Errorf("rendered file: %v", err)
	}
	if r := run(t, dir, v, "render", "docs/sessions.md", "--out", filepath.Join(dir, "nodir", "x.md")); r.code != ExitError {
		t.Errorf("render bad out = %+v", r)
	}
	if r := run(t, dir, v, "render", "docs/missing.md"); r.code != ExitError {
		t.Errorf("render missing = %+v", r)
	}
	if code := Run([]string{"render", "docs/sessions.md"}, WithDir(dir), WithIO(nil, failWriter{}, failWriter{}), WithVCS(v)); code != ExitError {
		t.Errorf("render write failure = %d", code)
	}
}

func TestAgentCommands(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "config/secrets.env", "STRIPE_KEY=op://Platform/stripe/credential   # ds:def id=op-stripe-p9c2v7ld secret=true truth=true\n")
	write(t, dir, "internal/pay.go", "package pay\n\n// ds:def id=app-stripe-m4w8k2qn secret=true source=env from=op-stripe-p9c2v7ld tags=pay,secret\nvar key = getenv(\"STRIPE_KEY\")\n")
	write(t, dir, "docs/ports.md", "---\nds:\n  covers: [auth-port-h3v8n2wd]\n---\n# Ports\n\nTicket [T-1](ds:ticket?id=T-1).\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	// A registered plugin verb is not unknown; an unregistered one is.
	var vout bytes.Buffer
	Run([]string{"check", "--json"}, WithDir(dir), WithIO(nil, &vout, &vout), WithVCS(v), WithVerb("ticket"))
	if strings.Contains(vout.String(), `"unknown"`) {
		t.Errorf("registered verb reported unknown: %s", vout.String())
	}
	if r := run(t, dir, v, "check", "--json"); !strings.Contains(r.out, `"unknown"`) {
		t.Errorf("unregistered verb must be unknown: %+v", r)
	}
	write(t, dir, "internal/store/write.go", goV2)
	v.files["abc1234:internal/store/write.go"] = []byte(goV1)
	if r := run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--doc", "docs/sessions.md", "--line", "3", "--note", "checked"); r.code != 0 {
		t.Fatal(r)
	}
	r := run(t, dir, v, "map")
	if r.code != 0 || !strings.Contains(r.out, "docs/sessions.md") || !strings.Contains(r.out, "sess-save-k7m2p4xq") || !strings.Contains(r.out, "unacked") || !strings.Contains(r.out, "tokens used") {
		t.Errorf("map = %+v", r)
	}
	if r := run(t, dir, v, "map", "--json", "--budget", "100"); !strings.Contains(r.out, `"budget_tokens": 100`) {
		t.Errorf("map json = %+v", r)
	}
	r = run(t, dir, v, "context", "docs/sessions.md", "--since", "ack")
	if r.code != 0 || !strings.Contains(r.out, "## 1. sess-save-k7m2p4xq unacked (diff") || !strings.Contains(r.out, "+\treturn s.sessions.Insert()") {
		t.Errorf("context = %+v", r)
	}
	r = run(t, dir, v, "context", "docs/sessions.md", "--budget", "5", "--mode", "full")
	if !strings.Contains(r.out, "omitted") || !strings.Contains(r.out, "tokens used of 5") {
		t.Errorf("context budget = %+v", r)
	}
	if r := run(t, dir, v, "context", "sess-save-k7m2p4xq", "--json"); !strings.Contains(r.out, `"target": "sess-save-k7m2p4xq"`) {
		t.Errorf("context json = %+v", r)
	}
	r = run(t, dir, v, "facts")
	if r.code != 0 || !regexp.MustCompile(`auth-port-h3v8n2wd\s+8081`).MatchString(r.out) || !strings.Contains(r.out, "op://Platform/stripe/credential") {
		t.Errorf("facts = %+v", r)
	}
	r = run(t, dir, v, "facts", "--cited-by", "docs/sessions.md", "--json")
	if !strings.Contains(r.out, "auth-port-h3v8n2wd") || strings.Contains(r.out, "op-stripe") {
		t.Errorf("facts cited-by = %+v", r)
	}
	r = run(t, dir, v, "why", "sess-save-k7m2p4xq", "--history", "--chain")
	if r.code != 0 || !strings.Contains(r.out, "docs/sessions.md:1  ds:block") || !strings.Contains(r.out, "checked") || !strings.Contains(r.out, "sess-save-k7m2p4xq  func") {
		t.Errorf("why = %+v", r)
	}
	if r := run(t, dir, v, "why", "auth-port-h3v8n2wd"); !strings.Contains(r.out, "covered by docs/ports.md") {
		t.Errorf("why covered = %+v", r)
	}
	r = run(t, dir, v, "why", "app-stripe-m4w8k2qn", "--chain")
	if !strings.Contains(r.out, "  from op-stripe-p9c2v7ld") || !strings.Contains(r.out, "TRUTH") {
		t.Errorf("why chain = %+v", r)
	}
	if r := run(t, dir, v, "why", "auth-port-h3v8n2wd", "--json"); !strings.Contains(r.out, `"covered_by"`) {
		t.Errorf("why json = %+v", r)
	}
	if r := run(t, dir, v, "why", "missing-a2b6f8jk"); r.code != ExitError {
		t.Errorf("why missing = %+v", r)
	}
	r = run(t, dir, v, "find", "stripe")
	if r.code != 0 || !strings.Contains(r.out, "app-stripe-m4w8k2qn") || !strings.Contains(r.out, "op-stripe-p9c2v7ld") || !strings.Contains(r.out, "cited by 0") {
		t.Errorf("find = %+v", r)
	}
	if r := run(t, dir, v, "find", "pay", "--json"); !strings.Contains(r.out, "app-stripe-m4w8k2qn") {
		t.Errorf("find json = %+v", r)
	}
	if r := run(t, dir, v, "read", "sess-save-k7m2p4xq", "--lines", "2-2"); r.code != 0 || strings.TrimSpace(r.out) != "return s.sessions.Insert()" {
		t.Errorf("read = %+v", r)
	}
	if r := run(t, dir, v, "read", "missing-a2b6f8jk"); r.code != ExitError {
		t.Errorf("read missing = %+v", r)
	}
	if r := run(t, dir, v, "locate", "sess-save-k7m2p4xq"); r.code != 0 || strings.TrimSpace(r.out) != "internal/store/write.go:4-6 @ abc1234" {
		t.Errorf("locate = %+v", r)
	}
	if r := run(t, dir, fakeVCS{}, "locate", "sess-save-k7m2p4xq"); !strings.Contains(r.out, "@ working tree") {
		t.Errorf("locate without git = %+v", r)
	}
	if r := run(t, dir, v, "locate", "missing-a2b6f8jk"); r.code != ExitError {
		t.Errorf("locate missing = %+v", r)
	}
	// Every read command shares the same failure when the tree is broken.
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"[\"]\ndocs = [\"docs/**\"]\n")
	for _, args := range [][]string{{"scan"}, {"check"}, {"ack", "x", "--all"}, {"refresh"}, {"render", "docs/sessions.md"}, {"map"}, {"context", "x"}, {"facts"}, {"why", "x"}, {"find", "x"}, {"read", "x"}, {"locate", "x"}, {"impact"}, {"status"}} {
		if r := run(t, dir, v, args...); r.code != ExitError {
			t.Errorf("%v with a bad glob = %+v", args, r)
		}
	}
	// def does not scan, so a bad glob does not stop it.
	if r := run(t, dir, v, "def", "internal/pay.go:1", "--dry-run"); r.code != 0 {
		t.Errorf("def ignores globs = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ndocs = [\"docs/**\"]\n[id]\nsuffix_alphabet = \"a\"\n")
	if r := run(t, dir, v, "scan"); r.code != ExitError {
		t.Errorf("invalid id config = %+v", r)
	}
	os.Remove(filepath.Join(dir, ".ds", "config.toml"))
	for _, args := range [][]string{{"scan"}, {"def", "x:1"}, {"ack", "x", "--all"}, {"refresh"}, {"render", "x"}, {"map"}, {"context", "x"}, {"facts"}, {"why", "x"}, {"find", "x"}, {"read", "x"}, {"locate", "x"}, {"impact"}, {"status"}} {
		if r := run(t, dir, v, args...); r.code != ExitError || !strings.Contains(r.err, "init") {
			t.Errorf("%v uninitialised = %+v", args, r)
		}
	}
}

func TestStoreAndHelpers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := NewStore(dir)
	if st.Exists() {
		t.Error("fresh store must not exist")
	}
	if _, err := st.LoadConfig(); !errors.Is(err, ErrNotInitialised) {
		t.Errorf("missing config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, DirName, ConfigFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadConfig(); err == nil || errors.Is(err, ErrNotInitialised) {
		t.Errorf("unreadable config: %v", err)
	}
	if _, _, _, err := st.LoadState(); err != nil {
		t.Errorf("no state files is fine: %v", err)
	}
	os.RemoveAll(filepath.Join(dir, DirName))
	write(t, dir, ".ds/refs.tsv", "bad\n")
	if _, _, _, err := st.LoadState(); err == nil || !strings.Contains(err.Error(), "refs.tsv") {
		t.Errorf("corrupt refs: %v", err)
	}
	os.Remove(filepath.Join(dir, DirName, RefsFile))
	write(t, dir, ".ds/acks.tsv", "bad\n")
	if _, _, _, err := st.LoadState(); err == nil || !strings.Contains(err.Error(), "acks.tsv") {
		t.Errorf("corrupt acks: %v", err)
	}
	os.Remove(filepath.Join(dir, DirName, AcksFile))
	for _, name := range []string{AcksFile, RefsFile, LedgerFile} {
		if err := os.MkdirAll(filepath.Join(dir, DirName, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := st.LoadState(); err == nil {
			t.Errorf("unreadable %s", name)
		}
	}
	os.RemoveAll(filepath.Join(dir, DirName, AcksFile))
	os.RemoveAll(filepath.Join(dir, DirName, RefsFile))
	if err := st.SaveLedger(ledger.Ledger{}, ledger.Refs{}); err == nil {
		t.Error("rename onto a directory must fail")
	}
	os.RemoveAll(filepath.Join(dir, DirName, LedgerFile))
	if err := st.SaveLedger(ledger.Ledger{Header: ledger.Header{Repo: "r"}}, ledger.Refs{}); err != nil {
		t.Errorf("save ledger: %v", err)
	}
	if err := st.AppendAcks(nil); err != nil {
		t.Errorf("save acks: %v", err)
	}
	if err := os.Chmod(filepath.Join(dir, DirName), 0o500); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("x", []byte("y")); err == nil {
		t.Error("unwritable dir")
	}
	_ = os.Chmod(filepath.Join(dir, DirName), 0o755)
	// ApplyEdit paths.
	write(t, dir, "a.txt", "one\ntwo\n")
	if err := st.ApplyEdit(docsync.Edit{File: "a.txt", Line: 2, New: "mid"}); err != nil {
		t.Errorf("apply: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "one\nmid\ntwo\n" {
		t.Errorf("applied = %q", b)
	}
	if err := st.ApplyEdit(docsync.Edit{File: "missing.txt", Line: 1, New: "x"}); err == nil {
		t.Error("missing file")
	}
	if err := st.ApplyEdit(docsync.Edit{File: "a.txt", Line: 1, Old: "zzz", New: "x"}); !errors.Is(err, docsync.ErrNotFound) {
		t.Errorf("stale edit: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st.ApplyEdit(docsync.Edit{File: "adir", Line: 1, New: "x"}); err == nil {
		t.Error("directory is not readable as a file")
	}
	if os.Getuid() != 0 {
		_ = os.Chmod(filepath.Join(dir, "a.txt"), 0o444)
		if err := st.ApplyEdit(docsync.Edit{File: "a.txt", Line: 1, New: "x"}); err == nil {
			t.Error("read-only file")
		}
	}
	// ConfigTOML round-trips through the parser, with and without permalink.
	c := config.Default()
	c.Scan.Docs = []string{"docs/**"}
	if parsed, err := config.Parse(strings.NewReader(ConfigTOML(c))); err != nil || parsed.Scan.Docs[0] != "docs/**" || parsed.Check.Permalink != "" {
		t.Errorf("toml round trip: %+v %v", parsed, err)
	}
	c.Check.Permalink = "https://g/{sha}/{file}#L{start}-L{end}"
	if parsed, err := config.Parse(strings.NewReader(ConfigTOML(c))); err != nil || parsed.Check.Permalink != c.Check.Permalink {
		t.Errorf("toml permalink: %+v %v", parsed, err)
	}
	if repoName(".", c) == "" || repoName("/tmp/x/repo", c) != "repo" {
		t.Error("repoName")
	}
	if orNone("") != "(none)" || orNone("x") != "x" {
		t.Error("orNone")
	}
	var b bytes.Buffer
	table(&b, [][]string{{"a", "b"}, {"ccc", "d"}})
	if !strings.Contains(b.String(), "ccc  d") {
		t.Errorf("table = %q", b.String())
	}
	if summaryLine(docsync.Report{}) != "no references" {
		t.Error("empty summary")
	}
}

// TestOldContentHook pins what the check's old-content hook hands back: the
// previous body as the scan pipeline produced it from the old file, paired by
// id, environment and branch. It used to cut raw lines out of the file, so a
// config value was diffed as its whole line against the bare new value, and a
// secret came back unredacted (bug 16).
func TestOldContentHook(t *testing.T) {
	t.Parallel()
	v := fakeVCS{head: "c1", files: map[string][]byte{"c1:config/app.yaml": []byte("ignored: the fake pipeline decides\n"), "c1:broken.yaml": []byte("x\n")}}
	mk := func(id, env, content string) block.Block {
		b := block.Block{ID: id, Args: map[string]string{"id": id}}
		if env != "" {
			b.Args["env"] = env
		}
		b.Content = content
		return b
	}
	calls := map[string]int{}
	extractFile := func(path string, _ []byte) ([]block.Block, error) {
		calls[path]++
		if path == "broken.yaml" {
			return nil, errors.New("does not parse")
		}
		return []block.Block{mk("port-k7m2p4xq", "dev", "8080"), mk("port-k7m2p4xq", "prod", "443"), mk("plain-h3v8n2wd", "", "value")}, nil
	}
	prev := ledger.Ledger{Header: ledger.Header{Commit: "c1"}}
	f := oldContent(v, prev, extractFile)
	row := func(id, env, file string) ledger.Row { return ledger.Row{ID: id, Env: env, File: file} }
	// Each environment gets its own body, never another's.
	if got, ok := f(row("port-k7m2p4xq", "prod", "config/app.yaml")); !ok || got != "443" {
		t.Errorf("prod = %q %v", got, ok)
	}
	if got, ok := f(row("port-k7m2p4xq", "dev", "config/app.yaml")); !ok || got != "8080" {
		t.Errorf("dev = %q %v", got, ok)
	}
	if got, ok := f(row("plain-h3v8n2wd", "", "config/app.yaml")); !ok || got != "value" {
		t.Errorf("plain = %q %v", got, ok)
	}
	// A file is read and extracted once however many rows ask about it.
	if calls["config/app.yaml"] != 1 {
		t.Errorf("extracted %d times, want once", calls["config/app.yaml"])
	}
	// Unknown, never a guess: an environment the old file did not have, a def
	// it did not hold, a file that is gone, one that does not extract, and a
	// ledger with no commit to read from.
	for name, r := range map[string]ledger.Row{
		"another environment":          row("port-k7m2p4xq", "staging", "config/app.yaml"),
		"a def not in the file":        row("nope-w8n4r6vc", "", "config/app.yaml"),
		"a missing file":               row("port-k7m2p4xq", "prod", "missing.yaml"),
		"a file that does not extract": row("port-k7m2p4xq", "prod", "broken.yaml"),
	} {
		if got, ok := f(r); ok {
			t.Errorf("%s = %q, want unknown", name, got)
		}
	}
	if _, ok := oldContent(v, ledger.Ledger{}, extractFile)(row("port-k7m2p4xq", "prod", "config/app.yaml")); ok {
		t.Error("no commit recorded means no old content")
	}
}

func TestGit(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	sh := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	g := Git{Dir: dir}
	if _, err := g.Head(); !errors.Is(err, ErrNoVCS) {
		t.Errorf("not a repo: %v", err)
	}
	sh("init", "-q")
	write(t, dir, "a.txt", "hello\n")
	sh("add", "a.txt")
	sh("commit", "-q", "-m", "one")
	head, err := g.Head()
	if err != nil || len(head) != 7 {
		t.Fatalf("head = %q %v", head, err)
	}
	if b, err := g.Show(head, "a.txt"); err != nil || string(b) != "hello\n" {
		t.Errorf("show = %q %v", b, err)
	}
	if _, err := g.Show(head, "missing.txt"); err == nil {
		t.Error("show missing")
	}
	if !g.Exists(head) || g.Exists("deadbeef") {
		t.Error("exists")
	}
	sh("config", "user.name", "Repo User")
	if g.User() != "Repo User" {
		t.Errorf("user = %q", g.User())
	}
	if churn, err := g.Churn(); err != nil || churn["a.txt"] != 1 {
		t.Errorf("churn = %v %v", churn, err)
	}
	// Outside a repo git still answers from the global config; a missing
	// directory is the only way the command itself fails.
	if u := (Git{Dir: filepath.Join(t.TempDir(), "nope")}).User(); u != "" {
		t.Errorf("user in a missing dir = %q", u)
	}
	if _, err := (Git{Dir: t.TempDir()}).Churn(); err == nil {
		t.Error("churn outside a repo")
	}
	write(t, dir, "staged.txt", "s\n")
	sh("add", "staged.txt")
	if staged, err := g.Staged(); err != nil || len(staged) != 1 || staged[0] != "staged.txt" {
		t.Errorf("staged = %v %v", staged, err)
	}
	if _, err := (Git{Dir: t.TempDir()}).Staged(); err == nil {
		t.Error("staged outside a repo")
	}
	if msg, err := g.Message("HEAD"); err != nil || msg != "one" {
		t.Errorf("message = %q %v", msg, err)
	}
	if _, err := g.Message("deadbeef"); err == nil {
		t.Error("message of a missing commit")
	}
	// Branch, remote, clone, pull, push against a bare remote.
	branch, err := g.Branch()
	if err != nil || branch == "" {
		t.Errorf("branch = %q %v", branch, err)
	}
	if g.RemoteURL() != "" {
		t.Error("no remote yet")
	}
	bare := t.TempDir()
	shIn := func(where string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = where
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	shIn(bare, "init", "-q", "--bare")
	sh("remote", "add", "origin", bare)
	sh("push", "-q", "-u", "origin", branch)
	if g.RemoteURL() != bare {
		t.Errorf("remote = %q", g.RemoteURL())
	}
	clone := filepath.Join(t.TempDir(), "clone")
	if err := g.Clone(bare, clone); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if err := g.Pull(clone); err != nil {
		t.Errorf("pull: %v", err)
	}
	write(t, clone, "b.txt", "new\n")
	shIn(clone, "config", "user.email", "t@t")
	shIn(clone, "config", "user.name", "t")
	if err := g.Push(clone, "publish"); err != nil {
		t.Errorf("push: %v", err)
	}
	if err := g.Push(clone, "nothing"); err != nil {
		t.Errorf("clean push is a no-op: %v", err)
	}
	if err := g.Pull(dir); err != nil {
		t.Errorf("pull after push: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err != nil {
		t.Error("pushed file not pulled")
	}
	// A commit that cannot be made (signing demanded, no key) fails Push.
	write(t, clone, "c.txt", "more\n")
	shIn(clone, "config", "commit.gpgsign", "true")
	shIn(clone, "config", "user.signingkey", "0000000000000000")
	if err := g.Push(clone, "signed"); err == nil {
		t.Error("commit failure must surface")
	}
	shIn(clone, "config", "commit.gpgsign", "false")
	if err := g.Clone("/nonexistent/remote", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("clone bad url")
	}
	if err := g.Pull(t.TempDir()); err == nil {
		t.Error("pull outside a repo")
	}
	if err := g.Push(t.TempDir(), "m"); err == nil {
		t.Error("push outside a repo")
	}
	sh("checkout", "-q", "--detach")
	if _, err := g.Branch(); err == nil {
		t.Error("detached head has no branch")
	}
	sh("checkout", "-q", branch)
	// The default VCS in Run is Git on the dir; HEAD moved with the pull.
	head, _ = g.Head()
	var out bytes.Buffer
	write(t, dir, "docs/a.md", "text\n")
	if code := Run([]string{"init"}, WithDir(dir), WithIO(nil, &out, &out)); code != 0 {
		t.Fatalf("init with real git: %s", out.String())
	}
	raw, _ := os.ReadFile(filepath.Join(dir, DirName, LedgerFile))
	if !strings.Contains(string(raw), "commit="+head) {
		t.Errorf("ledger header from git = %s", raw)
	}
}

// dryRunCommands are the commands the spec promises a --dry-run (§22): each
// rewrites something a person wrote or cannot rebuild, or sends something
// that cannot be unsent. The spec used to say "every command that writes",
// which ack, scan, init, export, and publish never honoured.
var dryRunCommands = [][]string{{"def"}, {"adopt"}, {"rename"}, {"refresh"}, {"prune"}, {"undo"}, {"notify"}, {"github", "comment"}, {"ack"}, {"publish"}}

// TestDryRunCommandsHaveTheFlag pins that promise to the command tree, so a
// flag dropped in a refactor fails here rather than in someone's repo.
// Pins bug 14.
func TestDryRunCommandsHaveTheFlag(t *testing.T) {
	t.Parallel()
	root := (&App{}).root()
	for _, path := range dryRunCommands {
		cmd, _, err := root.Find(path)
		if err != nil || cmd.Name() != path[len(path)-1] {
			t.Errorf("%v: no such command (%v)", path, err)
			continue
		}
		if cmd.Flags().Lookup(flagDryRun) == nil {
			t.Errorf("%v has no --%s", path, flagDryRun)
		}
	}
}

// TestGitReadsValuesAsValues pins that a commit, path, or URL starting with
// "-" is never an option to git. They come from committed files a pull
// request can edit — the ledger header's commit, an at= in a doc, the
// workspace URL — and `git show` given a commit of --output=<path> wrote
// that file.
// promise:git-options
func TestGitReadsValuesAsValues(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	env := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "one"}} {
		if args[0] == "add" {
			write(t, dir, "a.md", "hello\n")
		}
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	g := Git{Dir: dir}
	head, err := g.Head()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "written")
	if _, err := g.Show("--output="+out, "a.md"); err == nil {
		t.Error("an option-shaped commit must not resolve")
	}
	if _, err := g.Message("--output=" + out); err == nil {
		t.Error("an option-shaped commit has no message")
	}
	if g.Exists("--batch") {
		t.Error("an option-shaped commit does not exist")
	}
	marker := filepath.Join(dir, "clone-ran")
	if err := g.Clone("--upload-pack=touch "+marker, filepath.Join(dir, "idx")); err == nil {
		t.Error("an option-shaped URL must not clone")
	}
	matches, _ := filepath.Glob(out + "*")
	if len(matches) != 0 {
		t.Errorf("git wrote files from an option-shaped value: %v", matches)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("git ran a command from an option-shaped URL")
	}
	// Ordinary values still work.
	if body, err := g.Show(head, "a.md"); err != nil || string(body) != "hello\n" {
		t.Errorf("Show(head) = %q %v", body, err)
	}
	if msg, err := g.Message(head); err != nil || msg != "one" || !g.Exists(head) {
		t.Errorf("Message/Exists(head) = %q %v", msg, err)
	}
}
