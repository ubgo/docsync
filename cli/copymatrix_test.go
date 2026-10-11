package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The copy-combination matrix guards one class of bug: a citation that
// `ds refresh` cannot bring up to date while `ds check` keeps telling the
// reader to run `ds refresh`. Issue 4 proved the class exists three times
// over: a lines= range past the block's end (bug 136), a copy citing env=
// that refresh rendered from another environment's def (bug 137), and a
// ds:block of a secret (bug 138) all stayed `stale` forever, refresh said
// nothing, and ack cleared nothing. Each was found one at a time, in a
// different feature, by a different path, so examples written for the
// features someone thought of are exactly what missed them.
//
// So the cells are generated: the product of four axes, each a list below.
// A feature, fragment or lifecycle step added to its list is crossed with
// every other value automatically, with no cell written by hand.
//
//   - copyFeatures: how the cited def is declared and cited. A plain code
//     def; per-environment defs cited with env=prod; the same cited with an
//     env no def names; a remote def (file= pick= into a JSON file, which
//     cannot hold a comment); a secret def.
//   - copyFragments: the lines= the citation carries. None; a range inside
//     the block; one past its end; one that is not a range.
//   - copyLifecycles: what happens to the def after the first refresh.
//     Nothing; its code changes (the same line count, so a fitting range
//     still fits); its directive is deleted.
//   - copyModes: [include] mode, build or repo.
//   - copyStarts: what the page holds under the directive before the first
//     refresh. Nothing, or a closer recording a hash no block has, which is
//     how GitHub issue 4 was reported: that closer is what made check
//     compare a copy refresh had never written, and call it stale.
//
// The invariant, for every cell, after the last refresh:
//
//   - check never reports the citation `stale` or `tampered`: a copy that
//     refresh just processed is current, or refresh could not write it and
//     then check must name why;
//   - where the page owes a copy (repo mode, or any mode once a copy is in
//     the page), refresh prints `skipped <doc>:<line>: <reason>` exactly
//     when the cell cannot render, and writes the copy when it can (a cell
//     that can must not be skipped, or "skip everything with a reason"
//     would pass); a citation that cannot render gets an error-severity
//     finding of a named state;
//   - where it owes none (build mode, no copy in the page), refresh writes
//     and names nothing, and a cell that cannot render still gets a named
//     finding. The matrix found one more of the class this way: in build
//     mode a page holding a copy was compared and called stale, while
//     refresh never touched copies in build mode (bug 141);
//   - a cell that can render is clean once acked, so the finding a reader
//     follows actually clears;
//   - a second refresh reports the same skips and rewrites nothing.
func TestRefreshCopyCombinationMatrix(t *testing.T) {
	t.Parallel()
	n := 0
	for _, mode := range copyModes {
		for _, feat := range copyFeatures {
			for _, frag := range copyFragments {
				for _, life := range copyLifecycles {
					for _, start := range copyStarts {
						n++
						cell := copyCell{mode: mode, feature: feat, fragment: frag, lifecycle: life, start: start}
						t.Run(cell.name(), func(t *testing.T) {
							t.Parallel()
							cell.run(t)
						})
					}
				}
			}
		}
	}
	// The product, so a list emptied by mistake cannot pass on no cells.
	if want := len(copyModes) * len(copyFeatures) * len(copyFragments) * len(copyLifecycles) * len(copyStarts); n != want || n == 0 {
		t.Fatalf("generated %d cells, want %d", n, want)
	}
}

// copyID is the id every cell cites.
const copyID = "conf-k7m2p4xq"

// copyDoc and copyLine are where the citation sits.
const (
	copyDoc  = "docs/page.md"
	copyLine = 3
)

// copyModes is the [include] mode axis.
var copyModes = []string{"build", "repo"}

// copyFeature declares and cites the def one way.
type copyFeature struct {
	name string
	// files are the tree before the first refresh; change and remove give
	// the tree after the lifecycle step, as edits to it.
	files map[string]string
	// cite is the citation's arguments after id=.
	cite string
	// change is one edit (file, old, new) that changes the block's content
	// and keeps its line count; remove deletes its directive.
	change, remove [3]string
	// renders says a copy of it can be written at all.
	renders bool
}

// goDef is a 6-line Go struct with a def; args go on the directive.
func goDef(args string) string {
	return "package cfg\n\n// ds:def id=" + copyID + args + "\ntype Config struct {\n\tA string\n\tB string\n\tC string\n\tD string\n}\n"
}

var copyFeatures = []copyFeature{
	{
		name:    "plain",
		files:   map[string]string{"internal/cfg/cfg.go": goDef("")},
		change:  [3]string{"internal/cfg/cfg.go", "A string", "A int64"},
		remove:  [3]string{"internal/cfg/cfg.go", "// ds:def id=" + copyID + "\n", ""},
		renders: true,
	},
	{
		// The case of bug 137: refresh must render the def check compares.
		name:    "env",
		files:   map[string]string{"internal/cfg/dev.go": goDef(" env=dev"), "internal/cfg/prod.go": strings.Replace(goDef(" env=prod"), "Config", "Prod", 1)},
		cite:    " env=prod",
		change:  [3]string{"internal/cfg/prod.go", "A string", "A int64"},
		remove:  [3]string{"internal/cfg/prod.go", "// ds:def id=" + copyID + " env=prod\n", ""},
		renders: true,
	},
	{
		// No def names env=staging and none is env-less: broken, not stale.
		name:   "env-undefined",
		files:  map[string]string{"internal/cfg/dev.go": goDef(" env=dev"), "internal/cfg/prod.go": strings.Replace(goDef(" env=prod"), "Config", "Prod", 1)},
		cite:   " env=staging",
		change: [3]string{"internal/cfg/prod.go", "A string", "A int64"},
		remove: [3]string{"internal/cfg/prod.go", "// ds:def id=" + copyID + " env=prod\n", ""},
	},
	{
		// JSON cannot hold a comment, so the def lives in a page and picks.
		name: "remote",
		files: map[string]string{
			"config/app.json": "{\n  \"server\": {\n    \"a\": 1,\n    \"b\": 2,\n    \"c\": 3,\n    \"d\": 4\n  }\n}\n",
			"docs/defs.md":    "# Defs\n\n<!-- ds:def id=" + copyID + " file=config/app.json pick=json:$.server -->\n",
		},
		change:  [3]string{"config/app.json", "\"a\": 1", "\"a\": 5"},
		remove:  [3]string{"docs/defs.md", "<!-- ds:def id=" + copyID + " file=config/app.json pick=json:$.server -->\n", ""},
		renders: true,
	},
	{
		// The case of bug 138: a secret never renders, in any mode.
		name:   "secret",
		files:  map[string]string{"internal/cfg/cfg.go": goDef(" secret=true truth=true")},
		change: [3]string{"internal/cfg/cfg.go", "A string", "A int64"},
		remove: [3]string{"internal/cfg/cfg.go", "// ds:def id=" + copyID + " secret=true truth=true\n", ""},
	},
}

// copyFragment is one lines= value and whether it fits every block above.
type copyFragment struct {
	name, lines string
	fits        bool
}

var copyFragments = []copyFragment{
	{"whole", "", true},
	{"inside", "1-2", true},
	// The case of bug 136 (GitHub issue 4).
	{"past-end", "1-99", false},
	{"not-a-range", "abc", false},
}

// copyLifecycles is what happens to the def between the two refreshes.
var copyLifecycles = []string{"fresh", "changed", "deleted"}

// copyStart is what the page holds under the directive at first.
type copyStart struct {
	name, below string
}

var copyStarts = []copyStart{
	{"no-copy", ""},
	{"placeholder-closer", "<!-- /ds:block hash=000000 -->\n"},
}

type copyCell struct {
	mode      string
	feature   copyFeature
	fragment  copyFragment
	lifecycle string
	start     copyStart
}

func (c copyCell) name() string {
	return strings.Join([]string{c.mode, c.feature.name, c.fragment.name, c.lifecycle, c.start.name}, "/")
}

// owesCopy says the page must hold an up-to-date copy: every citation in
// repo mode, and in build mode one that already has a copy under it.
func (c copyCell) owesCopy() bool {
	return c.mode == "repo" || c.start.below != ""
}

// renders says a copy of this cell can be written, whatever the mode.
func (c copyCell) renders() bool {
	return c.feature.renders && c.fragment.fits && c.lifecycle != "deleted"
}

func (c copyCell) run(t *testing.T) {
	dir := t.TempDir()
	v := fakeVCS{head: "abc1234", files: map[string][]byte{}}
	for f, src := range c.feature.files {
		write(t, dir, f, src)
	}
	args := "id=" + copyID + c.feature.cite
	if c.fragment.lines != "" {
		args += " lines=" + c.fragment.lines
	}
	write(t, dir, copyDoc, "# Page\n\n<!-- ds:block "+args+" -->\n"+c.start.below)
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatalf("init: %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[include]\nmode = \""+c.mode+"\"\n")
	if r := run(t, dir, v, "refresh"); r.code != 0 {
		t.Fatalf("first refresh: %+v", r)
	}
	switch c.lifecycle {
	case "changed":
		c.edit(t, dir, c.feature.change)
	case "deleted":
		c.edit(t, dir, c.feature.remove)
	}
	first := run(t, dir, v, "refresh")
	if first.code != 0 {
		t.Fatalf("refresh: %+v", first)
	}
	skip := fmt.Sprintf("skipped %s:%d: ", copyDoc, copyLine)
	skipped := strings.Contains(first.out, skip)
	f := copyFinding(t, dir, v)
	if f.State == "stale" || f.State == "tampered" {
		t.Fatalf("after refresh the citation is %s (%s); refresh said:\n%s", f.State, f.Message, first.out)
	}
	if want := c.owesCopy() && !c.renders(); skipped != want {
		t.Errorf("refresh skipped=%v, want %v:\n%s", skipped, want, first.out)
	}
	if !c.owesCopy() && strings.Contains(first.out, "copies rewritten") {
		t.Errorf("refresh speaks of copies in a page that owes none:\n%s", first.out)
	}
	if c.owesCopy() && c.renders() {
		// Written, not merely unreported: the page holds a closed copy.
		if page, _ := os.ReadFile(filepath.Join(dir, copyDoc)); !strings.Contains(string(page), "<!-- /ds:block hash=") {
			t.Errorf("a copy that can render was not written:\n%s", page)
		}
	}
	if !c.renders() {
		if f.State == "" || f.State == "ok" {
			t.Errorf("a citation that cannot render has no finding of its own (state %q)", f.State)
		}
		if c.owesCopy() && f.Severity != "error" {
			t.Errorf("repo-mode %s is %s, want an error", f.State, f.Severity)
		}
	} else if f.State != "ok" {
		// The finding a reader is given must be one that clears.
		if r := run(t, dir, v, "ack", copyID, "--doc", copyDoc, "--line", fmt.Sprint(copyLine), "--note", "matrix"); r.code != 0 {
			t.Fatalf("%s (%s), and ack failed: %+v", f.State, f.Message, r)
		}
		if after := copyFinding(t, dir, v); after.State != "ok" {
			t.Errorf("%s (%s) did not clear on ack: now %s (%s)", f.State, f.Message, after.State, after.Message)
		}
	}
	// A fixed point: the same skips, nothing rewritten, the same page.
	page, _ := os.ReadFile(filepath.Join(dir, copyDoc))
	second := run(t, dir, v, "refresh")
	again, _ := os.ReadFile(filepath.Join(dir, copyDoc))
	if skipLines(second.out) != skipLines(first.out) || string(again) != string(page) || rewroteSome.MatchString(second.out) {
		t.Errorf("second refresh is not a fixed point:\n%s\nfirst:\n%s", second.out, first.out)
	}
}

// edit applies one (file, old, new) edit, failing if old is absent so a
// lifecycle step cannot silently do nothing.
func (c copyCell) edit(t *testing.T, dir string, e [3]string) {
	t.Helper()
	p := filepath.Join(dir, e[0])
	src, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(src), e[1]) {
		t.Fatalf("lifecycle edit %q not found in %s", e[1], e[0])
	}
	write(t, dir, e[0], strings.Replace(string(src), e[1], e[2], 1))
}

// rewroteSome matches a refresh that says it rewrote one copy or more.
var rewroteSome = regexp.MustCompile(`(?m)^[1-9][0-9]* repo-mode copies rewritten`)

// skipLines is the skipped and link-only lines of a refresh, in order.
func skipLines(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		for _, label := range copyLabelValues {
			if strings.HasPrefix(l, label+" ") {
				keep = append(keep, l)
			}
		}
	}
	return strings.Join(keep, "\n")
}

// cellFinding is the part of a finding the matrix judges.
type cellFinding struct {
	Doc      string `json:"doc"`
	Line     int    `json:"line"`
	State    string `json:"state"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// copyFinding is check's finding for the citation; a zero value when there
// is none, which the invariant treats as no finding.
func copyFinding(t *testing.T, dir string, v fakeVCS) cellFinding {
	t.Helper()
	r := run(t, dir, v, "check", "--json")
	var rep struct {
		Findings []cellFinding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(r.out), &rep); err != nil {
		t.Fatalf("check --json: %v\n%s\n%s", err, r.out, r.err)
	}
	var got []cellFinding
	for _, f := range rep.Findings {
		if f.Doc == copyDoc && f.Line == copyLine {
			got = append(got, f)
		}
	}
	if len(got) > 1 {
		t.Fatalf("several findings for one citation: %+v", got)
	}
	if len(got) == 0 {
		return cellFinding{}
	}
	return got[0]
}
