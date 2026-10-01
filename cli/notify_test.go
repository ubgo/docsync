package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// promise:notify-once
func TestNotify(t *testing.T) {
	// Not parallel: the webhook comes from the environment.
	dir, v := initialised(t)
	write(t, dir, "docs/two.md", "Also [save](ds:block?id=sess-save-k7m2p4xq) and [port](ds:cfg?id=auth-port-h3v8n2wd).\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	v.files["abc1234:internal/store/write.go"] = src
	write(t, dir, "internal/store/write.go", goV2)
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[owners]\n\"@auth\" = [\"khanakia\"]\n[notify]\nescalate_after = \"2d\"\n")
	// Dry run prints digests and records nothing.
	r := run(t, dir, v, "notify", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "docsync: @auth (khanakia)") || !strings.Contains(r.out, "docsync: (none)") || !strings.Contains(r.out, "unacked") || !strings.Contains(r.out, "broken") {
		t.Errorf("dry run = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ds", NotifiedFile)); err == nil {
		t.Error("dry run must not record")
	}
	// Without a webhook the digest is printed and recorded; a second run is quiet.
	r = run(t, dir, v, "notify")
	if r.code != 0 || !strings.Contains(r.out, "docsync: @auth") {
		t.Errorf("print notify = %+v", r)
	}
	state, err := NewStore(dir).LoadNotified()
	if err != nil || len(state) != 4 {
		t.Errorf("state = %+v %v", state, err)
	}
	if r := run(t, dir, v, "notify"); !strings.Contains(r.out, "nothing new") {
		t.Errorf("deduped = %+v", r)
	}
	// After escalate_after the still-open findings escalate once.
	later := clock.Add(3 * 24 * time.Hour)
	var out bytes.Buffer
	Run([]string{"notify"}, WithDir(dir), WithIO(nil, &out, &out), WithVCS(v), WithClock(func() time.Time { return later }))
	if !strings.Contains(out.String(), "ESCALATED docs/sessions.md") {
		t.Errorf("escalation = %s", out.String())
	}
	out.Reset()
	Run([]string{"notify"}, WithDir(dir), WithIO(nil, &out, &out), WithVCS(v), WithClock(func() time.Time { return later.Add(24 * time.Hour) }))
	if !strings.Contains(out.String(), "nothing new") {
		t.Errorf("escalate once = %s", out.String())
	}
	// A closed finding leaves the state; reopening notifies again.
	write(t, dir, "docs/two.md", "Port [8081](ds:cfg?id=auth-port-h3v8n2wd).\n")
	run(t, dir, v, "notify")
	state, _ = NewStore(dir).LoadNotified()
	for k := range state {
		if strings.HasPrefix(k, "docs/two.md:1:sess-save") {
			t.Errorf("closed finding still recorded: %s", k)
		}
	}
	// Slack webhook delivery, then a failing webhook.
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, string(b))
		if strings.Contains(string(b), "boom") {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	t.Setenv("DS_TEST_HOOK", srv.URL)
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[notify]\nslack = \"$DS_TEST_HOOK\"\nescalate_after = \"\"\n")
	os.Remove(filepath.Join(dir, ".ds", NotifiedFile))
	out.Reset()
	// The state was just deleted, so this run has no memory. That sends one
	// message naming everything open, not one per owner: a burst of digests
	// from an empty state reads as a sudden pile of new problems, which is
	// how a channel gets muted.
	code := Run([]string{"notify"}, WithDir(dir), WithIO(nil, &out, &out), WithVCS(v), WithHTTPClient(srv.Client()), WithClock(func() time.Time { return clock }))
	if code != 0 || len(got) != 1 || !strings.Contains(out.String(), "via slack") || !strings.Contains(got[0], `"text":"docsync:`) {
		t.Errorf("slack = %d %s got=%v", code, out.String(), got)
	}
	if !strings.Contains(got[0], "no previous notifier state") {
		t.Errorf("the message must say why it is listing everything: %s", got[0])
	}
	write(t, dir, "docs/boom.md", "[x](ds:block?id=boom-a2b6f8jk)\n")
	os.Remove(filepath.Join(dir, ".ds", NotifiedFile))
	if code := Run([]string{"notify"}, WithDir(dir), WithIO(nil, &out, &out), WithVCS(v), WithHTTPClient(srv.Client())); code != ExitError {
		t.Errorf("webhook failure = %d", code)
	}
	// The default client is used when none is injected.
	os.Remove(filepath.Join(dir, "docs/boom.md"))
	os.Remove(filepath.Join(dir, ".ds", NotifiedFile))
	if r := run(t, dir, v, "notify"); r.code != 0 {
		t.Errorf("default client = %+v", r)
	}
	// Corrupt state, unreachable webhook, and the usual failures.
	write(t, dir, ".ds/notified.json", "{")
	if r := run(t, dir, v, "notify"); r.code != ExitError {
		t.Errorf("corrupt state = %+v", r)
	}
	os.Remove(filepath.Join(dir, ".ds", NotifiedFile))
	t.Setenv("DS_TEST_HOOK", "http://127.0.0.1:1/hook")
	if r := run(t, dir, v, "notify"); r.code != ExitError {
		t.Errorf("unreachable webhook = %+v", r)
	}
	if err := os.Mkdir(filepath.Join(dir, ".ds", NotifiedFile+writeTempExt), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DS_TEST_HOOK", "")
	if r := run(t, dir, v, "notify"); r.code != ExitError {
		t.Errorf("state write failure = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"[\"]\n")
	if r := run(t, dir, v, "notify"); r.code != ExitError {
		t.Errorf("bad glob = %+v", r)
	}
	if r := run(t, t.TempDir(), v, "notify"); r.code != ExitError {
		t.Errorf("uninitialised = %+v", r)
	}
}

func TestImpactStagedAckFromCommitAndMCPRegistration(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "config/other.yaml", "other: 1   # ds:def id=other-b3c7g9kl\n")
	write(t, dir, "docs/o.md", "[o](ds:cfg?id=other-b3c7g9kl)\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	v.files["abc1234:internal/store/write.go"] = src
	write(t, dir, "internal/store/write.go", goV2)
	write(t, dir, "config/other.yaml", "other: 2   # ds:def id=other-b3c7g9kl\n")
	v.staged = []string{"config/other.yaml"}
	r := run(t, dir, v, "impact", "--staged")
	if r.code != 0 || !strings.Contains(r.out, "docs/o.md (1)") || strings.Contains(r.out, "docs/sessions.md") {
		t.Errorf("impact staged = %+v", r)
	}
	if r := run(t, dir, fakeVCS{head: "abc1234", err: ErrNoVCS, files: v.files}, "impact", "--staged"); r.code != ExitError {
		t.Errorf("staged without git = %+v", r)
	}
	// ack --from-commit reads directives out of the message.
	v.files["msg:HEAD"] = []byte("Swap storage backend\n\nds:ack id=sess-save-k7m2p4xq\nds:ack id=other-b3c7g9kl\n")
	v.files["msg:empty"] = []byte("no directives here\n")
	r = run(t, dir, v, "ack", "--from-commit", "HEAD")
	if r.code != 0 || strings.Count(r.out, "acked sess-save-k7m2p4xq") != 2 || !strings.Contains(r.out, "acked other-b3c7g9kl at docs/o.md:1") {
		t.Errorf("ack from commit = %+v", r)
	}
	_, _, acks, _ := NewStore(dir).LoadState()
	if len(acks.Rows) != 3 || acks.Rows[0].Note != "Swap storage backend" {
		t.Errorf("acks = %+v", acks.Rows)
	}
	if r := run(t, dir, v, "ack", "--from-commit", "empty"); r.code != ExitError || !strings.Contains(r.err, "no ds:ack") {
		t.Errorf("commit without directives = %+v", r)
	}
	if r := run(t, dir, v, "ack", "--from-commit", "missing"); r.code != ExitError {
		t.Errorf("missing commit = %+v", r)
	}
	if r := run(t, dir, v, "ack"); r.code != ExitError || !strings.Contains(r.err, "at least one id") {
		t.Errorf("ack without ids = %+v", r)
	}
	if ids, _ := ackDirectives("ds", "x ds:ack id=a-b2c3 y nods:ack id=z ds:ack  id=c"); len(ids) != 2 || ids[0].ID != "a-b2c3" || ids[1].ID != "c" {
		t.Errorf("ackDirectives = %v", ids)
	}
	// init --agents registers the MCP server once.
	fresh := t.TempDir()
	write(t, fresh, "docs/a.md", "x\n")
	r = run(t, fresh, v, "init", "--agents")
	if r.code != 0 || !strings.Contains(r.out, "wrote .mcp.json") {
		t.Errorf("init agents = %+v", r)
	}
	raw, _ := os.ReadFile(filepath.Join(fresh, MCPConfigFile))
	if !strings.Contains(string(raw), `"command": "ds"`) || !strings.Contains(string(raw), `"mcp"`) {
		t.Errorf(".mcp.json = %s", raw)
	}
	if r := run(t, fresh, v, "init", "--agents", "--force"); !strings.Contains(r.out, ".mcp.json already exists") {
		t.Errorf("existing mcp config = %+v", r)
	}
	if ok, err := registerMCP(filepath.Join(fresh, "nope"), "ds"); err == nil || ok {
		t.Error("unwritable location must fail")
	}
	// A dangling symlink where .mcp.json goes: absent to Stat, unwritable to
	// WriteFile, so init reports the failure.
	broken := t.TempDir()
	write(t, broken, "docs/a.md", "x\n")
	if err := os.Symlink(filepath.Join(broken, "nodir", "target"), filepath.Join(broken, MCPConfigFile)); err != nil {
		t.Fatal(err)
	}
	if r := run(t, broken, v, "init", "--agents"); r.code != ExitError {
		t.Errorf("init with unwritable mcp config = %+v", r)
	}
}

// TestNotifyRetriesWhatFailedToSend pins at-least-once delivery. A webhook
// that fails leaves the finding unrecorded, so the next run delivers it;
// once delivered it is not sent again. Recording before sending would lose a
// message to a single outage, and every later run would believe it went out.
func TestNotifyRetriesWhatFailedToSend(t *testing.T) {
	// Not parallel: environment variables.
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	down := true
	var delivered []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if down {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		delivered = append(delivered, string(b))
	}))
	defer srv.Close()
	t.Setenv("DS_RETRY_HOOK", srv.URL)
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[notify]\nslack = \"$DS_RETRY_HOOK\"\n")
	notify := func() int {
		var out strings.Builder
		return Run([]string{"notify"}, WithDir(dir), WithIO(nil, &out, &out), WithVCS(v), WithHTTPClient(srv.Client()), WithClock(func() time.Time { return clock }))
	}
	// Establish memory while nothing is open, then open a finding.
	down = false
	if code := notify(); code != 0 {
		t.Fatalf("first run = %d", code)
	}
	delivered = nil
	write(t, dir, "internal/store/write.go", goV2)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	down = true
	before, _ := os.ReadFile(filepath.Join(dir, DirName, NotifiedFile))
	if code := notify(); code != ExitError {
		t.Fatalf("a failed delivery must fail the run, got %d", code)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, DirName, NotifiedFile)); string(after) != string(before) {
		t.Fatal("a failed delivery was recorded as sent")
	}
	down = false
	if code := notify(); code != 0 || len(delivered) == 0 {
		t.Fatalf("the next run must deliver what failed: code %d, %d messages", code, len(delivered))
	}
	n := len(delivered)
	if code := notify(); code != 0 || len(delivered) != n {
		t.Errorf("once delivered it must not be sent again: %d then %d messages", n, len(delivered))
	}
}

// TestAckFromCommitUsesThePrefix pins that a commit-message ack uses the
// configured prefix like every other directive. A repository picks another
// prefix because `ds:` already appears in its prose; before, a commit that
// only mentioned `ds:ack id=x` acked every citation of x there, and the
// repository's own `<prefix>:ack` was ignored.
func TestAckFromCommitUsesThePrefix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v := fakeVCS{head: "abc1234", files: map[string][]byte{}}
	write(t, dir, "config/a.yaml", "a: 1   # mine:def id=a-a2b6f8jk\nb: 2   # mine:def id=b-h3v8n2wd\n")
	write(t, dir, "docs/d.md", "A is [1](mine:cfg?id=a-a2b6f8jk).\n\nB is [2](mine:cfg?id=b-h3v8n2wd).\n")
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, ".ds/config.toml", "prefix = \"mine\"\n[scan]\ncode = [\"config/**\"]\ndocs = [\"docs/**\"]\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	v.files["msg:HEAD"] = []byte("Explain how ds:ack id=a-a2b6f8jk works\n\nmine:ack id=b-h3v8n2wd\n")
	r := run(t, dir, v, "ack", "--from-commit", "HEAD")
	if r.code != 0 || !strings.Contains(r.out, "acked b-h3v8n2wd") || strings.Contains(r.out, "a-a2b6f8jk") {
		t.Errorf("only the configured prefix acks: %+v", r)
	}
	v.files["msg:other"] = []byte("mentions ds:ack id=a-a2b6f8jk only\n")
	if r := run(t, dir, v, "ack", "--from-commit", "other"); r.code != ExitError || !strings.Contains(r.err, "no mine:ack id=") {
		t.Errorf("a commit with only the default prefix carries nothing here: %+v", r)
	}
	if ids, _ := ackDirectives("mine", "mine:ack id=x, notmine:ack id=y, (mine:ack id=z)"); len(ids) != 2 || ids[0].ID != "x" || ids[1].ID != "z" {
		t.Errorf("ackDirectives = %v", ids)
	}
	// Without a config there is no prefix to read, and the command says so.
	if r := run(t, t.TempDir(), v, "ack", "--from-commit", "HEAD"); r.code != ExitError {
		t.Errorf("uninitialised = %+v", r)
	}
}

// TestInstructionsUseThePrefix pins that everything the tool tells a person
// or an agent to type uses the repository's prefix. Remedies said `ds:def`
// and the agent rules said `ds:block`; in a repository that chose another
// prefix, following either wrote a directive the scanner ignores.
func TestInstructionsUseThePrefix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v := fakeVCS{head: "abc1234", files: map[string][]byte{}}
	write(t, dir, "internal/a.go", "package a\n\n// mine:def id=long-a2b6f8jk\nfunc Long() {\n\tone()\n\ttwo()\n}\n")
	write(t, dir, "docs/d.md", "Gone [x](mine:block?id=gone-h3v8n2wd).\n\nValue [v](mine:cfg?id=long-a2b6f8jk).\n")
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, ".ds/config.toml", "prefix = \"mine\"\n[scan]\ncode = [\"internal/**\"]\ndocs = [\"docs/**\"]\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	var rep struct {
		Findings []struct {
			State  string `json:"state"`
			Remedy struct {
				Fix string `json:"fix"`
			} `json:"remedy"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(run(t, dir, v, "check", "--json").out), &rep); err != nil {
		t.Fatal(err)
	}
	fixes := map[string]string{}
	for _, f := range rep.Findings {
		fixes[f.State] = f.Remedy.Fix
		if strings.Contains(f.Remedy.Fix, "ds:") {
			t.Errorf("%s remedy names the default prefix: %s", f.State, f.Remedy.Fix)
		}
	}
	if !strings.Contains(fixes["broken"], "mine:def") || !strings.Contains(fixes["range"], "mine:cfg") || !strings.Contains(fixes["range"], "mine:block") {
		t.Errorf("remedies = %v", fixes)
	}
	if r := run(t, dir, v, "init", "--agents"); r.code != 0 {
		t.Fatal(r)
	}
	rules, err := os.ReadFile(filepath.Join(dir, AgentsRootFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rules), "`mine:block`") || strings.Contains(string(rules), "`ds:") || !strings.Contains(string(rules), "`ds def ") {
		t.Errorf("agent rules must name the prefix for directives and the binary for commands:\n%s", rules)
	}
}

// TestInitAgentsNeedsAReadableConfig pins that `init --agents` on a repo
// whose config does not parse stops and writes nothing: the rules would
// otherwise fall back to the default prefix, which is the mistake the
// prefix exists to avoid.
func TestInitAgentsNeedsAReadableConfig(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, ".ds/config.toml", "prefix = \n")
	if r := run(t, dir, v, "init", "--agents"); r.code != ExitError {
		t.Errorf("init --agents over a broken config = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, AgentsRootFile)); !os.IsNotExist(err) {
		t.Errorf("no agent rules may be written: %v", err)
	}
}
