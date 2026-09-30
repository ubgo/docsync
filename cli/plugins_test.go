package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/ledger"
)

func TestProcessPluginCapabilities(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "docs/tickets.md", "Open [T-1](ds:ticket?id=T-1&state=open) and [T-2](ds:ticket?id=T-2&state=done).\n\n<!-- ds:ticket id=T-1 state=open -->\n\n<!-- ds:table kind=task cols=title -->\n")
	write(t, dir, "infra/main.tf", "# ds:def id=bucket-a2b6f8jk pick=hcl:resource.name\nresource \"aws_s3_bucket\" \"logs\" {}\n")
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[plugins]\nverbs = [\"ticket\"]\npicks = [\"hcl\"]\n[records]\nsource = \"tracker\"\n")
	lookup := plugins(t, map[string]string{
		// The verb plugin: a closed ticket is a finding; render returns a link.
		"ds-ticket":          `{"findings":[{"state":"expired","message":"closed in tracker"}]}`,
		"ds-pick-hcl":        `{"value":"logs"}`,
		"ds-records-tracker": `[{"kind":"task","title":"from plugin"}]`,
	})
	// The ticket plugin answers every request the same way; the check for
	// T-1 and T-2 both get the finding, which is the plugin's decision.
	r := runP(t, dir, v, lookup, "check")
	if r.code != ExitFindings || strings.Count(r.out, "closed in tracker") != 3 {
		t.Errorf("plugin verb check = %+v", r)
	}
	if r := runP(t, dir, v, lookup, "find", "bucket"); !strings.Contains(r.out, "bucket-a2b6f8jk") {
		t.Errorf("plugin pick def = %+v", r)
	}
	if r := runP(t, dir, v, lookup, "read", "bucket-a2b6f8jk"); strings.TrimSpace(r.out) != "logs" {
		t.Errorf("plugin pick value = %+v", r)
	}
	render := runP(t, dir, v, plugins(t, map[string]string{
		"ds-ticket":          `{"node":"[T · open](https://tracker/T)"}`,
		"ds-pick-hcl":        `{"value":"logs"}`,
		"ds-records-tracker": `{"records":[{"kind":"task","title":"from plugin"}]}`,
	}), "render", "docs/tickets.md")
	if render.code != 0 || strings.Count(render.out, "[T · open](https://tracker/T)") != 3 || !strings.Contains(render.out, "| from plugin |") {
		t.Errorf("plugin render = %+v", render)
	}
	// Plugin failures: missing executable, malformed findings, non-string
	// node, malformed rows, an empty render.
	missing := plugins(t, nil)
	if r := runP(t, dir, v, missing, "check"); !strings.Contains(r.out, "plugin ds-ticket") || !strings.Contains(r.out, "unverifiable") {
		t.Errorf("missing verb plugin = %+v", r)
	}
	bad := plugins(t, map[string]string{"ds-ticket": `{"findings":"nope"}`, "ds-pick-hcl": `{"error":"no such path"}`, "ds-records-tracker": `{"records":"nope"}`})
	r = runP(t, dir, v, bad, "check")
	if !strings.Contains(r.out, "malformed findings") || !strings.Contains(r.out, "pick failed") {
		t.Errorf("malformed plugin replies = %+v", r)
	}
	r = runP(t, dir, v, plugins(t, map[string]string{"ds-ticket": `{"node":5}`, "ds-pick-hcl": `{"value":"x"}`, "ds-records-tracker": `{"records":"nope"}`}), "render", "docs/tickets.md")
	if !strings.Contains(r.err, "non-string node") || !strings.Contains(r.err, "malformed rows") {
		t.Errorf("render failures = %+v", r)
	}
	r = runP(t, dir, v, plugins(t, map[string]string{"ds-ticket": `{}`, "ds-pick-hcl": `{"range":{"start":1,"end":1,"text":"r"}}`, "ds-records-tracker": `{"records":[]}`}), "render", "docs/tickets.md")
	if r.code != 0 || !strings.Contains(r.out, "[T-1](ds:ticket?id=T-1&state=open)") {
		t.Errorf("empty render keeps the source = %+v", r)
	}
	if r := runP(t, dir, v, plugins(t, map[string]string{"ds-ticket": `{}`, "ds-pick-hcl": `{"range":{"start":1,"end":1,"text":"ranged"}}`, "ds-records-tracker": `{"records":[]}`}), "read", "bucket-a2b6f8jk"); strings.TrimSpace(r.out) != "ranged" {
		t.Errorf("range pick = %+v", r)
	}
	if r := runP(t, dir, v, missing, "render", "docs/tickets.md"); !strings.Contains(r.err, "not found") {
		t.Errorf("missing render plugin = %+v", r)
	}
	// The file store and the slack notifier satisfy the library interfaces.
	st := NewStore(dir)
	l, _, _, err := st.Load(context.Background())
	if err != nil || l.Header.Repo == "" {
		t.Errorf("store load = %+v %v", l.Header, err)
	}
	if err := st.Save(context.Background(), l, ledger.Refs{}, ledger.Acks{Rows: []ledger.Ack{{ID: "x"}}}); err != nil {
		t.Errorf("store save = %v", err)
	}
	if _, _, acks, _ := st.LoadState(); len(acks.Rows) != 1 {
		t.Error("save must write acks")
	}
	if err := os.Mkdir(filepath.Join(dir, DirName, LedgerFile+writeTempExt), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st.Save(context.Background(), l, ledger.Refs{}, ledger.Acks{}); err == nil {
		t.Error("save failure surfaces")
	}
	n := slackNotifier{app: &App{}, hook: "http://127.0.0.1:1/hook"}
	if err := n.Notify(context.Background(), nil); err != nil {
		t.Error("nothing to send is not an error")
	}
	if err := n.Notify(context.Background(), []check.Finding{{State: check.StateBroken}}); err == nil {
		t.Error("unreachable hook must fail")
	}
	if len((procVerb{}).Carriers()) != 3 || (procVerb{}).Keys().Required != nil {
		t.Error("proc verb shape")
	}
}

func runP(t *testing.T, dir string, v VCS, lookup func(string) (string, error), args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(args, WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithPluginLookup(lookup))
	return result{code, out.String(), errb.String()}
}
