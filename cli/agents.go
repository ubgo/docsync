package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ubgo/docsync/config"
)

// `ds init --agents` (§26.10): the rules of section 25 land where agents
// read them, `ds mcp` is registered, and a session-start hook loads the
// map. Every write is idempotent: fragments sit between markers and are
// replaced in place, JSON configs are merged, so running it again after a
// tool upgrade refreshes the rules without touching anything else.
const (
	// ClaudeFile and AgentsRootFile are the repo-root instruction files
	// Claude Code and other agents read.
	ClaudeFile     = "CLAUDE.md"
	AgentsRootFile = "AGENTS.md"
	// SkillFile is the Claude Code skill, loadable on demand.
	SkillFile = ".claude/skills/docsync/SKILL.md"
	// ClaudeSettingsFile holds Claude Code hooks for the repo.
	ClaudeSettingsFile = ".claude/settings.json"
	// fragmentBegin and fragmentEnd bracket the managed section in a
	// shared instruction file.
	fragmentBegin = "<!-- docsync:begin -->"
	fragmentEnd   = "<!-- docsync:end -->"
	// sessionHookEvent is the Claude Code hook that fires when a session
	// starts; the command it runs is agents.session_hook.
	sessionHookEvent = "SessionStart"
	hooksKey         = "hooks"
	hookCommandType  = "command"
)

// ErrSettings is returned when .claude/settings.json is not a JSON object.
var ErrSettings = errors.New("settings.json is not a JSON object; fix it or remove it")

// skillFrontmatter heads SKILL.md; the description is what an agent sees
// when deciding whether to load it.
const skillFrontmatter = "---\nname: docsync\ndescription: Keep docs bound to code with docsync. Load before writing or reviewing documentation, citing code, or changing defined blocks.\n---\n\n"

// installAgentFiles performs every step of --agents for the repository's
// config: the rules in its prefix, the MCP registration unless agents.mcp is
// false, the session hook agents.session_hook names (none when it is
// empty), and the agents.max_defs_per_run line in .ds/config.toml. It
// returns one line per action for the command to print.
func (a *App) installAgentFiles(cfg config.Config) ([]string, error) {
	rules := agentRules(cfg.Prefix)
	var lines []string
	line, err := a.setDefCap(cfg)
	if err != nil {
		return nil, err
	}
	lines = append(lines, line)
	for _, f := range []string{ClaudeFile, AgentsRootFile} {
		action, err := mergeFragment(filepath.Join(a.dir, f), rules)
		if err != nil {
			return nil, err
		}
		lines = append(lines, fmt.Sprintf("%s %s (docsync rules between markers)", action, f))
	}
	skill := filepath.Join(a.dir, filepath.FromSlash(SkillFile))
	if err := os.MkdirAll(filepath.Dir(skill), dirPerm); err != nil {
		return nil, err
	}
	if err := os.WriteFile(skill, []byte(skillFrontmatter+rules), filePerm); err != nil {
		return nil, err
	}
	lines = append(lines, "wrote "+SkillFile)
	if !cfg.Agents.MCP {
		lines = append(lines, "skipped MCP registration (agents.mcp = false)")
	} else {
		for _, t := range MCPTargets {
			line, err := registerMCP(a.dir, t, a.name)
			if err != nil {
				return nil, err
			}
			if line != "" {
				lines = append(lines, line)
			}
		}
	}
	hook := a.sessionHook(cfg)
	if hook == "" {
		lines = append(lines, "skipped the session hook (agents.session_hook is empty)")
		return lines, nil
	}
	action, err := installSessionHook(filepath.Join(a.dir, filepath.FromSlash(ClaudeSettingsFile)), hook)
	if err != nil {
		return nil, err
	}
	lines = append(lines, fmt.Sprintf("%s %s (%s hook: %s)", action, ClaudeSettingsFile, sessionHookEvent, hook))
	return lines, nil
}

// sessionHook is the command the session-start hook runs:
// agents.session_hook, whose default is `ds map --budget 2000` (§26.10),
// with a leading `ds` replaced by this binary's name so a renamed build
// hooks itself. Empty means no hook. The hook used to be hardcoded, and the
// key was parsed and ignored (bug 108).
func (a *App) sessionHook(cfg config.Config) string {
	hook := strings.TrimSpace(cfg.Agents.SessionHook)
	if rest, ok := strings.CutPrefix(hook, DefaultName+" "); ok {
		return a.name + " " + rest
	}
	return hook
}

// agentsTable is the config table `init --agents` adds when absent.
const agentsTable = "[agents]"

// setDefCap writes `[agents] max_defs_per_run` into .ds/config.toml when the
// file has no [agents] table, so the cap an MCP session enforces is written
// down where a reviewer sees it (§26.10 "sets agents.max_defs_per_run").
// An existing table is the repository's decision and is left alone.
func (a *App) setDefCap(cfg config.Config) (string, error) {
	p := filepath.Join(a.dir, DirName, ConfigFile)
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == agentsTable {
			return fmt.Sprintf("kept %s/%s %s (max_defs_per_run = %d)", DirName, ConfigFile, agentsTable, cfg.Agents.MaxDefsPerRun), nil
		}
	}
	text := string(raw)
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += fmt.Sprintf("\n%s\nmax_defs_per_run = %d\n", agentsTable, cfg.Agents.MaxDefsPerRun)
	if err := os.WriteFile(p, []byte(text), filePerm); err != nil {
		return "", err
	}
	return fmt.Sprintf("updated %s/%s (%s max_defs_per_run = %d)", DirName, ConfigFile, agentsTable, cfg.Agents.MaxDefsPerRun), nil
}

// MCPTarget is one MCP client's project-level registration file: where it
// is, the top-level key holding its servers, and the directory whose
// presence says the client is in use. The first target has no directory
// and is always written; the others are written only where their client
// has already left its directory, which is how "the configs it can find"
// (§26.10) is decided without guessing at what someone has installed.
type MCPTarget struct {
	File string
	Key  string
	Dir  string
	// Stdio adds "type": "stdio", which VS Code's mcp.json requires.
	Stdio bool
}

// MCP registration keys.
const (
	mcpServersKey = "mcpServers"
	vscodeKey     = "servers"
	mcpTypeStdio  = "stdio"
)

// MCPTargets are the clients `init --agents` registers `ds mcp` with. Only
// .mcp.json was written before, which most clients do not read (bug 108).
var MCPTargets = []MCPTarget{
	{File: MCPConfigFile, Key: mcpServersKey},
	{File: ".cursor/mcp.json", Key: mcpServersKey, Dir: ".cursor"},
	{File: ".vscode/mcp.json", Key: vscodeKey, Dir: ".vscode", Stdio: true},
	{File: ".gemini/settings.json", Key: mcpServersKey, Dir: ".gemini"},
}

// registerMCP adds the docsync server to one client's file, creating the
// file or merging into it, and returns the line to print; "" when the
// client is not in use here. An existing docsync entry is kept as it is,
// since someone may have pointed it at an absolute path on purpose. A file
// that is not a plain JSON object (VS Code accepts comments) is reported
// and left alone rather than rewritten.
func registerMCP(dir string, t MCPTarget, name string) (string, error) {
	if t.Dir != "" {
		if info, err := os.Stat(filepath.Join(dir, t.Dir)); err != nil || !info.IsDir() {
			return "", nil
		}
	}
	p := filepath.Join(dir, filepath.FromSlash(t.File))
	entry := map[string]any{"command": name, "args": []string{"mcp"}}
	if t.Stdio {
		entry["type"] = mcpTypeStdio
	}
	doc := map[string]any{}
	existing, err := os.ReadFile(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return "", err
	default:
		if json.Unmarshal(existing, &doc) != nil || doc == nil {
			return fmt.Sprintf("%s is not a plain JSON object; add a %q server running `%s mcp` to it yourself", t.File, mcpServerName, name), nil
		}
	}
	servers, _ := doc[t.Key].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if _, ok := servers[mcpServerName]; ok {
		return fmt.Sprintf("kept %s (a %q server is already registered)", t.File, mcpServerName), nil
	}
	servers[mcpServerName] = entry
	doc[t.Key] = servers
	raw, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(p, append(raw, '\n'), filePerm); err != nil {
		return "", err
	}
	action := "wrote"
	if existing != nil {
		action = "updated"
	}
	return fmt.Sprintf("%s %s (registers `%s mcp`)", action, t.File, name), nil
}

// mergeFragment writes fragment between the markers in path: creating the
// file, replacing the managed section, or appending one. It reports which.
func mergeFragment(path, fragment string) (string, error) {
	managed := fragmentBegin + "\n" + fragment + fragmentEnd + "\n"
	existing, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "wrote", os.WriteFile(path, []byte(managed), filePerm)
	}
	if err != nil {
		return "", err
	}
	s := string(existing)
	if i := strings.Index(s, fragmentBegin); i >= 0 {
		if j := strings.Index(s[i:], fragmentEnd); j >= 0 {
			s = s[:i] + strings.TrimSuffix(managed, "\n") + s[i+j+len(fragmentEnd):]
			return "updated", os.WriteFile(path, []byte(s), filePerm)
		}
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return "appended to", os.WriteFile(path, []byte(s+"\n"+managed), filePerm)
}

// installSessionHook adds a SessionStart command hook to a Claude Code
// settings file, creating it when absent and leaving an existing hook for
// the same command alone.
func installSessionHook(path, command string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return "", err
	}
	settings := map[string]any{}
	existing, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return "", err
	default:
		if err := json.Unmarshal(existing, &settings); err != nil || settings == nil {
			return "", fmt.Errorf("%w: %s", ErrSettings, path)
		}
	}
	hooks, _ := settings[hooksKey].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	entries, _ := hooks[sessionHookEvent].([]any)
	if bytes.Contains(existing, []byte(command)) {
		return "kept", nil
	}
	entries = append(entries, map[string]any{hooksKey: []any{map[string]any{"type": hookCommandType, "command": command}}})
	hooks[sessionHookEvent] = entries
	settings[hooksKey] = hooks
	raw, _ := json.MarshalIndent(settings, "", "  ")
	action := "wrote"
	if existing != nil {
		action = "updated"
	}
	return action, os.WriteFile(path, append(raw, '\n'), filePerm)
}
