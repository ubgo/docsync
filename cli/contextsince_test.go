package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/block"
)

// promise:context-since-checked
// `context --since <commit>` diffs each cited block against its body at
// that commit, and an unknown value is refused; it used to accept anything
// and diff against the last scan (bug 61).
func TestContextSinceCommit(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	v.files["abc1234:internal/store/write.go"] = []byte(goV1)
	write(t, dir, "internal/store/write.go", goV2)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	r := run(t, dir, v, "context", "docs/sessions.md", "--since", "abc1234")
	if r.code != 0 || !strings.Contains(r.out, "(diff, ") || !strings.Contains(r.out, "-\treturn s.legacy.Save()\n+\treturn s.sessions.Insert()") {
		t.Errorf("since a commit = %+v", r)
	}
	r = run(t, dir, v, "context", "docs/sessions.md", "--since", "v9")
	if r.code != ExitError || !strings.Contains(r.err, `--since "v9" is neither "ack" nor a commit`) {
		t.Errorf("unknown since = %+v", r)
	}
	if r := run(t, dir, v, "context", "docs/sessions.md", "--mode", "dif"); r.code != ExitError || !strings.Contains(r.err, "unknown mode") {
		t.Errorf("unknown mode = %+v", r)
	}
	// The MCP tool goes through the same check.
	resps, _ := rpc(t, dir, v,
		call(ToolContext, `{"target":"docs/sessions.md","since":"abc1234"}`),
		call(ToolContext, `{"target":"docs/sessions.md","since":"v9"}`),
	)
	if text, isErr := payload(t, resps[0]); isErr || !strings.Contains(text, `"mode": "diff"`) {
		t.Errorf("mcp since a commit = %s", text)
	}
	if text, isErr := payload(t, resps[1]); !isErr || !strings.Contains(text, "neither") {
		t.Errorf("mcp unknown since = %s", text)
	}
}

func TestContextSinceHook(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	ledgerRaw, _ := os.ReadFile(filepath.Join(dir, ".ds/ledger.tsv"))
	a := &App{vcs: v}
	ld, err := (&App{dir: dir, vcs: v, now: func() time.Time { return clock }, stderr: &bytes.Buffer{}}).system()
	if err != nil {
		t.Fatal(err)
	}
	if hook, err := a.contextSince(context.Background(), ld.sys, ""); hook != nil || err != nil {
		t.Errorf("no since = %v %v", hook != nil, err)
	}
	if hook, err := a.contextSince(context.Background(), ld.sys, "ack"); hook != nil || err != nil {
		t.Errorf("since ack = %v %v", hook != nil, err)
	}
	// The commit's ledger says the block lived in old/write.go then.
	v.files["abc1234:.ds/ledger.tsv"] = []byte(strings.ReplaceAll(string(ledgerRaw), "internal/store/write.go", "old/write.go"))
	v.files["abc1234:old/write.go"] = []byte(goV1)
	hook, err := a.contextSince(context.Background(), ld.sys, "abc1234")
	if err != nil {
		t.Fatal(err)
	}
	save := block.Block{ID: "sess-save-k7m2p4xq", Pos: block.Position{File: "internal/store/write.go"}}
	if body, ok := hook(save); !ok || !strings.Contains(body, "s.legacy.Save()") {
		t.Errorf("moved since the commit = %q %v", body, ok)
	}
	// Today's file when the commit's ledger has no row; nothing when that
	// file is not there either; nothing for a block from another repo.
	if _, ok := hook(block.Block{ID: "new-a2b6f8jk", Pos: block.Position{File: "internal/new.go"}}); ok {
		t.Error("a block absent at the commit")
	}
	if _, ok := hook(block.Block{ID: "x-a2b6f8jk", Args: map[string]string{block.KeyRepo: "api"}, Pos: block.Position{File: "old/write.go"}}); ok {
		t.Error("a merged block has no history here")
	}
}

// With no budget the closing line says so, rather than "of 0" (bug 65).
func TestTokensLine(t *testing.T) {
	t.Parallel()
	if got := tokensLine(12, 0); got != "12 tokens used, no budget" {
		t.Errorf("unbudgeted = %q", got)
	}
	if got := tokensLine(12, 40); got != "12 tokens used of 40" {
		t.Errorf("budgeted = %q", got)
	}
	dir, v := initialised(t)
	if r := run(t, dir, v, "context", "docs/sessions.md"); !strings.HasSuffix(r.out, "tokens used, no budget\n") {
		t.Errorf("context = %+v", r)
	}
}
