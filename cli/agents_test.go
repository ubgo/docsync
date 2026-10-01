package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitAgents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v := fakeVCS{head: "abc1234", branch: "main", files: map[string][]byte{}}
	write(t, dir, "CLAUDE.md", "# Project rules\n\nKeep it short.")
	write(t, dir, ".claude/settings.json", `{"permissions":{"allow":["Bash(go test:*)"]}}`)
	r := run(t, dir, v, "init", "--agents")
	for _, want := range []string{"appended to CLAUDE.md", "wrote AGENTS.md", "wrote " + SkillFile, "wrote .mcp.json", "updated .claude/settings.json (SessionStart hook: ds map --budget 2000)"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q in %+v", want, r)
		}
	}
	claude, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.HasPrefix(string(claude), "# Project rules\n\nKeep it short.\n\n"+fragmentBegin) || strings.Count(string(claude), fragmentBegin) != 1 {
		t.Errorf("claude = %q", claude)
	}
	var settings map[string]any
	raw, _ := os.ReadFile(filepath.Join(dir, ".claude/settings.json"))
	if err := json.Unmarshal(raw, &settings); err != nil || settings["permissions"] == nil || !strings.Contains(string(raw), `"ds map --budget 2000"`) {
		t.Errorf("settings = %s %v", raw, err)
	}
	skill, _ := os.ReadFile(filepath.Join(dir, SkillFile))
	if !strings.HasPrefix(string(skill), "---\nname: docsync\n") || !strings.Contains(string(skill), "Never cite a path and line") {
		t.Errorf("skill = %.80q", skill)
	}
	// Running again on an initialised repo is allowed without --force and
	// changes nothing but the managed sections.
	r = run(t, dir, v, "init", "--agents")
	if r.code != 0 || !strings.Contains(r.out, "updated CLAUDE.md") || !strings.Contains(r.out, "updated AGENTS.md") || !strings.Contains(r.out, "kept .claude/settings.json") || !strings.Contains(r.out, "kept .mcp.json") || strings.Contains(r.out, "next:") {
		t.Errorf("second run = %+v", r)
	}
	again, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if string(again) != string(claude) {
		t.Errorf("second run changed CLAUDE.md:\n%s", again)
	}
	// A managed section with edits inside is replaced; a file without a
	// trailing newline gets one; an opening marker with no close appends a
	// fresh section.
	write(t, dir, "AGENTS.md", "intro\n"+fragmentBegin+"\nstale\n"+fragmentEnd+"\ntail\n")
	if action, err := mergeFragment(filepath.Join(dir, "AGENTS.md"), "fresh\n"); err != nil || action != "updated" {
		t.Errorf("merge = %s %v", action, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md")); string(b) != "intro\n"+fragmentBegin+"\nfresh\n"+fragmentEnd+"\ntail\n" {
		t.Errorf("merged = %q", b)
	}
	write(t, dir, "AGENTS.md", "x "+fragmentBegin)
	if action, _ := mergeFragment(filepath.Join(dir, "AGENTS.md"), "f\n"); action != "appended to" {
		t.Errorf("unclosed marker = %s", action)
	}
	if _, err := mergeFragment(filepath.Join(dir, ".claude"), "f\n"); err == nil {
		t.Error("directory as file")
	}
	// Settings that are not an object, and a settings path that cannot be
	// created.
	write(t, dir, ".claude/settings.json", "[]")
	if _, err := installSessionHook(filepath.Join(dir, ".claude/settings.json"), "x"); err == nil {
		t.Error("settings array")
	}
	write(t, dir, ".claude/settings.json", "null")
	if _, err := installSessionHook(filepath.Join(dir, ".claude/settings.json"), "x"); err == nil {
		t.Error("settings null")
	}
	if _, err := installSessionHook(filepath.Join(dir, ".claude"), "x"); err == nil {
		t.Error("settings directory")
	}
	write(t, dir, "blocker", "")
	if _, err := installSessionHook(filepath.Join(dir, "blocker", "settings.json"), "x"); err == nil {
		t.Error("settings under a file")
	}
	// A fresh repo without CLAUDE.md gets both files created.
	fresh := t.TempDir()
	r = run(t, fresh, v, "init", "--agents")
	if r.code != 0 || !strings.Contains(r.out, "wrote CLAUDE.md") || !strings.Contains(r.out, "wrote .claude/settings.json") || !strings.Contains(r.out, "next:") {
		t.Errorf("fresh = %+v", r)
	}
	// Failures inside the install surface: a skill path blocked by a file,
	// a CLAUDE.md that is a directory.
	blocked := t.TempDir()
	write(t, blocked, ".claude/skills", "file")
	if r := run(t, blocked, v, "init", "--agents"); r.code != ExitError {
		t.Errorf("blocked skill dir = %+v", r)
	}
	skillDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(skillDir, filepath.FromSlash(SkillFile)), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run(t, skillDir, v, "init", "--agents"); r.code != ExitError {
		t.Errorf("skill file as dir = %+v", r)
	}
	dirClaude := t.TempDir()
	if err := os.Mkdir(filepath.Join(dirClaude, "CLAUDE.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dirClaude, v, "init", "--agents"); r.code != ExitError {
		t.Errorf("CLAUDE.md dir = %+v", r)
	}
	hookBlocked := t.TempDir()
	write(t, hookBlocked, ".claude/settings.json", "[]")
	if r := run(t, hookBlocked, v, "init", "--agents"); r.code != ExitError {
		t.Errorf("bad settings = %+v", r)
	}
}
