package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// starts; sessionHookBudget is the map size it loads (§26.10).
	sessionHookEvent  = "SessionStart"
	sessionHookBudget = "2000"
	hooksKey          = "hooks"
	hookCommandType   = "command"
)

// ErrSettings is returned when .claude/settings.json is not a JSON object.
var ErrSettings = errors.New("settings.json is not a JSON object; fix it or remove it")

// skillFrontmatter heads SKILL.md; the description is what an agent sees
// when deciding whether to load it.
const skillFrontmatter = "---\nname: docsync\ndescription: Keep docs bound to code with docsync. Load before writing or reviewing documentation, citing code, or changing defined blocks.\n---\n\n"

// installAgentFiles performs every step of --agents. It returns one line
// per action for the command to print.
func (a *App) installAgentFiles(prefix string) ([]string, error) {
	rules := agentRules(prefix)
	var lines []string
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
	written, err := registerMCP(a.dir, a.name)
	if err != nil {
		return nil, err
	}
	if written {
		lines = append(lines, fmt.Sprintf("wrote %s (registers `%s mcp`)", MCPConfigFile, a.name))
	} else {
		lines = append(lines, fmt.Sprintf("%s already exists; add a %q server running `%s mcp` yourself", MCPConfigFile, mcpServerName, a.name))
	}
	action, err := installSessionHook(filepath.Join(a.dir, filepath.FromSlash(ClaudeSettingsFile)), a.name+" map --budget "+sessionHookBudget)
	if err != nil {
		return nil, err
	}
	lines = append(lines, fmt.Sprintf("%s %s (%s hook: %s map --budget %s)", action, ClaudeSettingsFile, sessionHookEvent, a.name, sessionHookBudget))
	return lines, nil
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
