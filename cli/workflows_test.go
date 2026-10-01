package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// automaticTriggers are the GitHub Actions events that start a run without a
// person asking for one. Each spends metered minutes on a private repository.
var automaticTriggers = []string{"push", "pull_request", "pull_request_target", "schedule", "workflow_run", "release", "issues", "issue_comment"}

// triggersOf returns the event names under a workflow's top-level `on:` key.
// It reads the block by indentation rather than parsing YAML so this module
// takes no dependency to check one key.
func triggersOf(workflow string) []string {
	var out []string
	in := false
	for _, l := range strings.Split(workflow, "\n") {
		if strings.HasPrefix(l, "#") {
			continue
		}
		if l == "on:" {
			in = true
			continue
		}
		if !in {
			continue
		}
		if l != "" && !strings.HasPrefix(l, " ") {
			break
		}
		t := strings.TrimSpace(l)
		// An event is a key two spaces in; deeper lines are its settings.
		if strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "   ") && strings.HasSuffix(t, ":") {
			out = append(out, strings.TrimSuffix(t, ":"))
		}
	}
	return out
}

// TestShippedWorkflowsAreManual pins the default every workflow docsync hands
// out: manual triggers only. The one `ds init` wrote ran on every push and pull
// request, which on a private repository bills minutes by default and, once
// they run out, fails for billing rather than for code -- a red check that
// teaches everyone to ignore it. The automatic triggers live in each file's
// header comment, to paste back where the budget is there.
func TestShippedWorkflowsAreManual(t *testing.T) {
	t.Parallel()
	workflows := map[string]string{"ds init " + CISnippet: ciSnippet}
	paths, err := filepath.Glob(filepath.Join("..", "integrations", "github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	// A glob that matched nothing would make this pass vacuously.
	if len(paths) < 3 {
		t.Fatalf("expected the shipped workflow templates, found %v", paths)
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		workflows[p] = string(b)
	}
	for name, body := range workflows {
		trig := triggersOf(body)
		if len(trig) == 0 {
			t.Errorf("%s: no on: block found, so nothing here was checked", name)
			continue
		}
		for _, e := range trig {
			for _, auto := range automaticTriggers {
				if e == auto {
					t.Errorf("%s: triggers on %q automatically; ship workflow_dispatch and put this in the header", name, e)
				}
			}
		}
		// And the way back has to be written down, or manual-only is a trap.
		if !strings.Contains(body, "replace `on:` below") && !strings.Contains(body, "replace on: below") {
			t.Errorf("%s: no header telling the reader how to restore the automatic triggers", name)
		}
	}
}

// The workflow ds init writes keeps its report out of the checkout: it
// wrote docsync.json into the repository, where the default code = ["**"]
// scanned it as a citation of every block it quoted (bug 75).
func TestInitWorkflowWritesItsReportOutsideTheCheckout(t *testing.T) {
	t.Parallel()
	for _, l := range strings.Split(ciSnippet, "\n") {
		if strings.Contains(l, "ds check --json") && (!strings.Contains(l, `> "$RUNNER_TEMP/`) || strings.Contains(l, "> docsync.json")) {
			t.Errorf("report written into the checkout: %s", l)
		}
	}
	if !strings.Contains(ciSnippet, "ds check --json") {
		t.Error("the template no longer runs ds check --json, so nothing here was checked")
	}
}

// TestTriggersOf pins the reader the test above depends on, including the
// case that matters most: it must see an automatic trigger when one is there,
// or the check above passes on nothing.
func TestTriggersOf(t *testing.T) {
	t.Parallel()
	got := triggersOf("name: x\n# on:\n#   push:\non:\n  pull_request:\n    types: [opened]\n  push:\n    branches: [main]\njobs:\n  a:\n")
	if strings.Join(got, ",") != "pull_request,push" {
		t.Errorf("triggersOf = %v, want pull_request,push (the commented header must not count)", got)
	}
	if got := triggersOf("jobs:\n  a:\n"); got != nil {
		t.Errorf("no on: block = %v", got)
	}
}
