package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initFrom is an initialize request naming the client.
func initFrom(name string) string {
	return `{"jsonrpc":"2.0","id":"i","method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"` + name + `","version":"1"}}}`
}

// TestMCPAckNeedsAnOwnerAndRecordsTheClient pins bug 106: the MCP ack
// accepted any delegated_by, where §26.7 allows it only when a human named
// in [owners] delegated, and recorded every ack under the actor "mcp" where
// §26.7 asks for the model or tool name. The client's initialize name is
// now the actor.
func TestMCPAckNeedsAnOwnerAndRecordsTheClient(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	v.files["abc1234:internal/store/write.go"] = src
	write(t, dir, "internal/store/write.go", goV2)
	addOwners(t, dir, "alice")
	ack := func(by string) string {
		return call(ToolAck, `{"id":"sess-save-k7m2p4xq","doc":"docs/sessions.md","line":1,"note":"ok","delegated_by":"`+by+`"}`)
	}
	resps, _ := rpc(t, dir, v, initFrom("review-bot"), ack("mallory"), ack("alice"))
	if text, isErr := payload(t, resps[1]); !isErr || !strings.Contains(text, `listed in [owners] (§26.7): "mallory"`) {
		t.Errorf("delegate nobody lists = %v %.300s", isErr, text)
	}
	if text, isErr := payload(t, resps[2]); isErr || !strings.Contains(text, `"Actor": "review-bot"`) || !strings.Contains(text, `"DelegatedBy": "alice"`) {
		t.Errorf("owner's delegation = %v %.300s", isErr, text)
	}
	_, _, acks, _ := NewStore(dir).LoadState()
	if len(acks.Rows) != 1 || acks.Rows[0].Actor != "review-bot" {
		t.Errorf("acks = %+v", acks.Rows)
	}
	if r := run(t, dir, v, "audit", "--actor-kind", "agent"); !strings.Contains(r.out, "review-bot (agent, delegated by alice)") {
		t.Errorf("audit = %+v", r)
	}
	// A client that names nobody, or sends params that do not parse, is
	// recorded as "mcp", the transport.
	m := &mcpServer{app: &App{}}
	m.handle(rpcRequest{JSONRPC: jsonrpcVersion, ID: []byte(`1`), Method: "initialize", Params: []byte(`[1]`)})
	if m.actor() != mcpActor {
		t.Errorf("unnamed client = %q", m.actor())
	}
}

// TestMCPImpactStaged pins bug 107: the MCP impact tool took no arguments,
// so an agent about to commit saw every unstaged change's findings too.
// staged=true is `ds impact --staged`.
func TestMCPImpactStaged(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	src, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	v.files["abc1234:internal/store/write.go"] = src
	write(t, dir, "internal/store/write.go", goV2)
	v.staged = []string{"config/auth.yaml"}
	resps, _ := rpc(t, dir, v, call(ToolImpact, `{}`), call(ToolImpact, `{"staged":true}`))
	if text, _ := payload(t, resps[0]); !strings.Contains(text, `"total": 3`) {
		t.Errorf("unstaged impact = %.300s", text)
	}
	if text, isErr := payload(t, resps[1]); isErr || !strings.Contains(text, `"total": 0`) {
		t.Errorf("staged impact sees an unstaged change = %v %.300s", isErr, text)
	}
	v.staged = []string{"internal/store/write.go"}
	resps, _ = rpc(t, dir, v, call(ToolImpact, `{"staged":true}`))
	if text, _ := payload(t, resps[0]); !strings.Contains(text, `"total": 2`) {
		t.Errorf("staged change = %.300s", text)
	}
	v.err = ErrNoVCS
	resps, _ = rpc(t, dir, v, call(ToolImpact, `{"staged":true}`))
	if _, isErr := payload(t, resps[0]); !isErr {
		t.Error("staged without a VCS must fail")
	}
}
