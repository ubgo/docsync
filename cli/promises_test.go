package cli

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// TestMCPExposesNoPrivilegedTool pins the agent boundary (spec, design
// decisions: "MCP for reads and two bounded writes; no run, resolve, undo,
// publish; ack only delegated"). A tool added to the MCP surface that executes
// code, reaches a provider, rewrites history, or pushes to the index would let
// an agent do unreviewed what the CLI gates behind a person; and an ack that
// did not require delegated_by would let an agent approve its own work.
// promise:agent-bounded
func TestMCPExposesNoPrivilegedTool(t *testing.T) {
	t.Parallel()
	var names []string
	for _, tool := range (&mcpServer{}).tools() {
		names = append(names, tool.Name)
	}
	for _, forbidden := range []string{"run", "resolve", "undo", "publish", "prune", "sync", "scan", "rename", "adopt", "repair"} {
		if slices.Contains(names, forbidden) {
			t.Errorf("MCP exposes %q; agents must not reach it (tools: %v)", forbidden, names)
		}
	}
	writes := 0
	for _, tool := range (&mcpServer{}).tools() {
		switch tool.Name {
		case ToolDef, ToolAck:
			writes++
		}
		if tool.Name == ToolAck && !slices.Contains(requiredOf(tool.InputSchema), "delegated_by") {
			t.Errorf("the MCP ack must require delegated_by: %v", tool.InputSchema)
		}
	}
	if writes != 2 {
		t.Errorf("want exactly the two bounded writes (def, ack), got %d in %v", writes, names)
	}
}

// requiredOf reads a JSON schema's "required" list.
func requiredOf(schema map[string]any) []string {
	req, _ := schema["required"].([]string)
	return req
}

// TestSnapshotAgeAloneNeverNotifies pins "Drift triggers it, never age on its
// own: a snapshot ninety days old against an upstream that has not moved costs
// nobody anything." With nothing changed upstream, a notify run a year later
// says nothing about the snapshot.
// promise:drift-not-age
func TestSnapshotAgeAloneNeverNotifies(t *testing.T) {
	// Not parallel: notify reads the webhook from the environment.
	_, docs, _, docsVCS := snapshotNotifyFixture(t)
	out := runAt(t, docs, docsVCS, clock.Add(365*24*time.Hour), "notify")
	if strings.Contains(out, "pinned snapshot") || strings.Contains(out, "sync") {
		t.Errorf("an old snapshot with no upstream change must not notify: %s", out)
	}
}
