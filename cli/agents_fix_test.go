package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/docsync/config"
)

// promise:init-agents-always-mcp
// TestInitAgentsMatchesTheSpec pins bug 108: `ds init --agents` was
// narrower than §26.10. It registered `ds mcp` only in .mcp.json and left
// an existing one alone, never wrote agents.max_defs_per_run, ignored
// agents.mcp and agents.session_hook, and its rules said `ds impact` where
// §25 says `ds impact --staged` and left out `ds triage`.
func TestInitAgentsMatchesTheSpec(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v := fakeVCS{head: "abc1234", branch: "main", files: map[string][]byte{}}
	write(t, dir, ".cursor/rules.txt", "x\n")
	write(t, dir, ".vscode/mcp.json", `{"servers":{"other":{"command":"x"}},"inputs":[]}`)
	write(t, dir, ".gemini/settings.json", "// comments are not JSON\n{}")
	write(t, dir, ".mcp.json", `{"mcpServers":{"other":{"command":"x"}}}`)
	r := run(t, dir, v, "init", "--agents")
	for _, want := range []string{
		"updated .ds/config.toml ([agents] max_defs_per_run = 20)",
		"updated .mcp.json (registers `ds mcp`)",
		"wrote .cursor/mcp.json (registers `ds mcp`)",
		"updated .vscode/mcp.json (registers `ds mcp`)",
		".gemini/settings.json is not a plain JSON object; add a \"docsync\" server running `ds mcp` to it yourself",
		"(SessionStart hook: ds map --budget 2000)",
	} {
		if !strings.Contains(r.out, want) {
			t.Errorf("init --agents lacks %q:\n%s", want, r.out)
		}
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, ".ds/config.toml"))
	if !strings.HasSuffix(string(cfg), "\n[agents]\nmax_defs_per_run = 20\n") {
		t.Errorf("config = %s", cfg)
	}
	var vscode struct {
		Servers map[string]map[string]any `json:"servers"`
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".vscode/mcp.json"))
	if err := json.Unmarshal(raw, &vscode); err != nil || vscode.Servers["other"] == nil || vscode.Servers["docsync"]["type"] != "stdio" || vscode.Servers["docsync"]["command"] != "ds" {
		t.Errorf("vscode = %s %v", raw, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, ".mcp.json")); !strings.Contains(string(raw), `"other"`) || !strings.Contains(string(raw), `"docsync"`) {
		t.Errorf(".mcp.json merge = %s", raw)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, ".gemini/settings.json")); string(raw) != "// comments are not JSON\n{}" {
		t.Errorf("a file that is not plain JSON was rewritten: %s", raw)
	}
	rules, _ := os.ReadFile(filepath.Join(dir, AgentsRootFile))
	for _, want := range []string{"`ds impact --staged`", "run `ds triage` first", "a person listed in `[owners]`"} {
		if !strings.Contains(string(rules), want) {
			t.Errorf("rules lack %q", want)
		}
	}
	// Again: everything already there is kept, the cap included.
	r = run(t, dir, v, "init", "--agents")
	for _, want := range []string{"kept .ds/config.toml [agents] (max_defs_per_run = 20)", "kept .cursor/mcp.json", "kept .vscode/mcp.json"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("second run lacks %q:\n%s", want, r.out)
		}
	}

	// agents.mcp = false registers nothing; an empty session_hook installs
	// no hook; a custom one is the command, renamed for the binary.
	off := t.TempDir()
	write(t, off, "docs/a.md", "x\n")
	if r := run(t, off, v, "init"); r.code != 0 {
		t.Fatal(r)
	}
	raw, _ = os.ReadFile(filepath.Join(off, ".ds/config.toml"))
	write(t, off, ".ds/config.toml", string(raw)+"\n[agents]\nmcp = false\nsession_hook = \"\"\nmax_defs_per_run = 3\n")
	r = run(t, off, v, "init", "--agents")
	for _, want := range []string{"kept .ds/config.toml [agents] (max_defs_per_run = 3)", "skipped MCP registration (agents.mcp = false)", "skipped the session hook (agents.session_hook is empty)"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("disabled lacks %q:\n%s", want, r.out)
		}
	}
	for _, f := range []string{MCPConfigFile, ClaudeSettingsFile} {
		if _, err := os.Stat(filepath.Join(off, f)); err == nil {
			t.Errorf("%s written while disabled", f)
		}
	}
	write(t, off, ".ds/config.toml", string(raw)+"\n[agents]\nsession_hook = \"ds map --budget 500\"\n")
	var out, errb strings.Builder
	if code := Run([]string{"init", "--agents"}, WithDir(off), WithIO(strings.NewReader(""), &out, &errb), WithVCS(v), WithName("docs")); code != 0 {
		t.Fatalf("renamed: %s %s", out.String(), errb.String())
	}
	if hooks, _ := os.ReadFile(filepath.Join(off, ClaudeSettingsFile)); !strings.Contains(string(hooks), `"docs map --budget 500"`) {
		t.Errorf("custom hook = %s", hooks)
	}
	if mcp, _ := os.ReadFile(filepath.Join(off, MCPConfigFile)); !strings.Contains(string(mcp), `"command": "docs"`) {
		t.Errorf("renamed mcp = %s", mcp)
	}

	// A config with no trailing newline still gets a well-formed table; an
	// unreadable registration file or config fails the command.
	bare := t.TempDir()
	write(t, bare, ".ds/config.toml", "[scan]\ndocs = [\"**/*.md\"]")
	app := &App{dir: bare, name: DefaultName}
	if line, err := app.setDefCap(config.Default()); err != nil || !strings.HasPrefix(line, "updated") {
		t.Errorf("no newline = %q %v", line, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(bare, ".ds/config.toml")); !strings.Contains(string(raw), "]\n\n[agents]\n") {
		t.Errorf("appended = %q", raw)
	}
	if _, err := (&App{dir: t.TempDir()}).setDefCap(config.Default()); err == nil {
		t.Error("missing config must fail")
	}
	if _, err := (&App{dir: t.TempDir(), name: DefaultName}).installAgentFiles(config.Default()); err == nil {
		t.Error("installing with no config must fail")
	}
	ro := t.TempDir()
	write(t, ro, ".ds/config.toml", "[scan]\n")
	if err := os.Chmod(filepath.Join(ro, ".ds/config.toml"), 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := (&App{dir: ro}).setDefCap(config.Default()); err == nil && os.Geteuid() != 0 {
		t.Error("a config that cannot be written must fail")
	}
	if err := os.MkdirAll(filepath.Join(bare, ".mcp.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := registerMCP(bare, MCPTargets[0], "ds"); err == nil {
		t.Error("an unreadable registration file must fail")
	}
}
