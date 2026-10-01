package check

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/internal/textnorm"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/render"
	"github.com/ubgo/docsync/scan"
)

var now = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

func def(id, file string, start int, content string, args map[string]string) block.Block {
	if args == nil {
		args = map[string]string{}
	}
	args[block.KeyID] = id
	b := block.Block{ID: id, Kind: block.KindFunc, Pos: block.Position{File: file, Start: start, End: start + strings.Count(content, "\n")}, DirectivePos: block.Position{File: file, Start: start - 1, End: start - 1}, Args: args}
	b.SetContent(content)
	return b
}

func ref(verb, id, doc string, line int, args map[string]string) block.Reference {
	if args == nil {
		args = map[string]string{}
	}
	if id != "" {
		args[block.KeyID] = id
	}
	r := block.Reference{Verb: verb, ID: id, Pos: block.Position{File: doc, Start: line}, Carrier: block.CarrierLink, Args: args}
	r.SetSentence("A sentence.")
	return r
}

func byState(rep Report) map[State][]Finding {
	m := map[State][]Finding{}
	for _, f := range rep.Findings {
		m[f.State] = append(m[f.State], f)
	}
	return m
}

func one(t *testing.T, rep Report, st State) Finding {
	t.Helper()
	fs := byState(rep)[st]
	if len(fs) != 1 {
		t.Fatalf("want exactly one %s finding, got %d: %+v", st, len(fs), rep.Findings)
	}
	return fs[0]
}

func TestFirstRunAllOK(t *testing.T) {
	t.Parallel()
	d := def("save-k7m2p4xq", "a.go", 3, "func Save() {}", nil)
	rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{d}, Refs: []block.Reference{ref("block", d.ID, "docs/a.md", 5, nil)}})
	if rep.ExitCode != 0 || len(rep.Findings) != 1 || rep.Findings[0].State != StateOK || rep.Summary[StateOK] != 1 || rep.BySeverity[SeverityNone] != 1 {
		t.Fatalf("first run = %+v", rep)
	}
	f := rep.Findings[0]
	if f.File != "a.go" || f.Lines != [2]int{3, 3} || f.Hash.Current != d.Hash || f.Repo != "api" || f.Sentence != "A sentence." {
		t.Errorf("finding fields = %+v", f)
	}
}

func TestUnackedThenAcked(t *testing.T) {
	t.Parallel()
	old := def("save-k7m2p4xq", "a.go", 3, "func Save() {\n\treturn 1\n}", map[string]string{"owner": "@auth"})
	nw := def("save-k7m2p4xq", "a.go", 3, "func Save() {\n\treturn 2\n}", map[string]string{"owner": "@auth"})
	prev := ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", old)}}
	r := ref("block", nw.ID, "docs/a.md", 5, nil)
	in := Input{
		Repo: "api", Now: now, Defs: []block.Block{nw}, Refs: []block.Reference{r}, Prev: prev,
		Opts: Options{OldContent: func(ledger.Row) (string, bool) { return old.Content, true }},
	}
	rep := Run(in)
	f := one(t, rep, StateUnacked)
	if f.Severity != SeverityError || rep.ExitCode != 1 || f.Owner != "@auth" || f.Diff == "" || len(f.Classes) != 1 || f.Classes[0] != block.ClassBody {
		t.Errorf("unacked = %+v", f)
	}
	if !strings.HasPrefix(f.Remedy.IfStillTrue, "ds ack save-k7m2p4xq --doc docs/a.md --line 5") || !strings.Contains(f.Remedy.IfNot, "docs/a.md:5") || f.Remedy.Fix != "" {
		t.Errorf("remedy = %+v", f.Remedy)
	}
	// Downgrade to warning.
	in.Opts.UnackedIsWarning = true
	if rep := Run(in); rep.ExitCode != 0 || one(t, rep, StateUnacked).Severity != SeverityWarning {
		t.Error("unacked downgrade")
	}
	in.Opts.UnackedIsWarning = false
	// An ack at the current hash clears it.
	in.Acks = ledger.Acks{Rows: []ledger.Ack{{At: now, Actor: "k", ActorKind: ledger.ActorHuman, ID: nw.ID, Repo: "api", Doc: "docs/a.md", Line: 5, BlockHash: nw.Hash}}}
	if rep := Run(in); rep.ExitCode != 0 || one(t, rep, StateOK).Hash.Acked != nw.Hash {
		t.Errorf("acked = %+v", rep.Findings)
	}
	// An ack at the OLD hash does not.
	in.Acks.Rows[0].BlockHash = old.Hash
	if rep := Run(in); one(t, rep, StateUnacked).Hash.Acked != old.Hash {
		t.Error("stale ack must still be unacked")
	}
	// The previous refs file's acked_hash also counts.
	in.Acks = ledger.Acks{}
	in.PrevRefs = ledger.Refs{Rows: []ledger.RefRow{{ID: nw.ID, Repo: "api", Doc: "docs/a.md", Line: 5, AckedHash: nw.Hash}}}
	if rep := Run(in); rep.ExitCode != 0 {
		t.Error("prev refs ack must count")
	}
	// A translation reference reports translation-stale instead.
	tr := ref("block", nw.ID, "docs/de/a.md", 5, map[string]string{"translates": "true"})
	in.PrevRefs = ledger.Refs{}
	in.Refs = []block.Reference{tr}
	if f := one(t, Run(in), StateTranslationStale); f.Severity != SeverityError || !strings.Contains(f.Remedy.Fix, "docs/de/a.md:5") {
		t.Errorf("translation = %+v", f)
	}
	// Under api stability a body change is silent.
	nwAPI := nw
	nwAPI.Args = map[string]string{"id": nw.ID, "stability": "api"}
	in.Defs = []block.Block{nwAPI}
	in.Refs = []block.Reference{r}
	if rep := Run(in); rep.ExitCode != 0 || one(t, rep, StateOK).State != StateOK {
		t.Error("api stability must not flag body")
	}
}

// promise:strict-moved
func TestMovedIsSilent(t *testing.T) {
	t.Parallel()
	old := def("x-k7m2p4xq", "a.go", 3, "func X() {}", nil)
	nw := def("x-k7m2p4xq", "b.go", 9, "func X() {}", nil)
	rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{nw}, Refs: []block.Reference{ref("block", nw.ID, "d.md", 1, nil)}, Prev: ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", old)}}, Opts: Options{Strict: true}})
	f := one(t, rep, StateMoved)
	if f.Severity != SeverityNone || rep.ExitCode != 0 || !strings.Contains(f.Message, "a.go:3") {
		t.Errorf("moved = %+v", f)
	}
}

func TestBrokenVariants(t *testing.T) {
	t.Parallel()
	gone := def("gone-k7m2p4xq", "a.go", 3, "func Gone() {}", nil)
	remarked := def("new-h3v8n2wd", "b.go", 3, "func Gone() {}", nil)
	rwOld := def("rw-p2c4y7mk", "a.go", 10, "a\nb\nc\nd\ne", nil)
	rwNew := def("rwnew-t4k2b9rf", "a.go", 10, "a\nb\nc\nd\nX", nil)
	deleted := def("del-q9x1z6ch", "a.go", 20, "unique thing", nil)
	present := def("here-m2q1s8vt", "a.go", 30, "here", nil)
	prev := ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", gone), ledger.FromBlock("api", rwOld), ledger.FromBlock("api", deleted)}}
	bodies := map[string]string{rwOld.ID: rwOld.Content, deleted.ID: deleted.Content}
	refs := []block.Reference{
		ref("block", gone.ID, "d.md", 1, nil),
		ref("block", rwOld.ID, "d.md", 2, nil),
		ref("block", deleted.ID, "d.md", 3, nil),
		ref("cfg", "typo-m2q1s8vt", "d.md", 4, nil),
		ref("cfg", "here-m2q1s8vt-x", "d.md", 5, nil),
		ref("block", "nothing-like-it", "d.md", 6, nil),
	}
	rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{remarked, rwNew, present}, Refs: refs, Prev: prev, Opts: Options{OldContent: func(row ledger.Row) (string, bool) { c, ok := bodies[row.ID]; return c, ok }}})
	bs := byState(rep)[StateBroken]
	if len(bs) != 6 {
		t.Fatalf("broken = %d: %+v", len(bs), rep.Findings)
	}
	msgs := map[int]string{}
	for _, f := range bs {
		msgs[f.Line] = f.Message + " || " + f.Remedy.Fix
	}
	if !strings.Contains(msgs[1], "lost its ds:def") || !strings.Contains(msgs[1], "b.go:3") {
		t.Errorf("moved-unmarked: %s", msgs[1])
	}
	if !strings.Contains(msgs[2], "rwnew-t4k2b9rf") || !strings.Contains(msgs[2], "similar") {
		t.Errorf("rewritten: %s", msgs[2])
	}
	if !strings.Contains(msgs[3], "was deleted") || !strings.Contains(msgs[3], "a.go:20") {
		t.Errorf("deleted: %s", msgs[3])
	}
	if !strings.Contains(msgs[4], "did you mean here-m2q1s8vt") {
		t.Errorf("suffix suggestion: %s", msgs[4])
	}
	if !strings.Contains(msgs[5], "did you mean here-m2q1s8vt") {
		t.Errorf("edit-distance suggestion: %s", msgs[5])
	}
	if strings.Contains(msgs[6], "did you mean") {
		t.Errorf("no suggestion expected: %s", msgs[6])
	}
}

func TestPresentationAndLifecycle(t *testing.T) {
	t.Parallel()
	big := def("big-k7m2p4xq", "a.go", 3, strings.Repeat("l\n", 49)+"l", nil)
	small := def("small-h3v8n2wd", "c.yaml", 3, "port: 8081", nil)
	dep := def("dep-p2c4y7mk", "a.go", 100, "x", map[string]string{"deprecated": "2026-01-01"})
	sun := def("sun-t4k2b9rf", "a.go", 200, "y", map[string]string{"sunset": "2026-09-06", "deprecated": "2026-01-01"})
	future := def("fut-q9x1z6ch", "a.go", 300, "z", map[string]string{"sunset": "2099-01-01", "deprecated": "bad-date"})
	blockPos := func(id, doc string, line int, args map[string]string) block.Reference {
		r := ref("block", id, doc, line, args)
		r.Carrier = block.CarrierBlock
		r.SetSentence("")
		return r
	}
	refs := []block.Reference{
		blockPos(big.ID, "d.md", 1, nil),                                    // too-large
		blockPos(big.ID, "d.md", 2, map[string]string{"lines": "1-6"}),      // ok
		blockPos(big.ID, "d.md", 3, map[string]string{"lines": "45-60"}),    // range
		blockPos(big.ID, "d.md", 4, map[string]string{"lines": "x"}),        // range (bad)
		ref("block", big.ID, "d.md", 5, nil),                                // ok, inline cite of a big block
		ref("cfg", big.ID, "d.md", 6, nil),                                  // range: cfg on multi-line
		ref("cfg", small.ID, "d.md", 7, nil),                                // ok
		ref("block", dep.ID, "d.md", 8, nil),                                // deprecated + ok
		ref("block", sun.ID, "d.md", 9, nil),                                // sunset
		ref("block", future.ID, "d.md", 10, nil),                            // ok (future sunset, bad deprecated date ignored)
		ref("block", big.ID, "d.md", 11, map[string]string{"at": "abc123"}), // unverifiable (no hook)
		ref("block", big.ID, "d.md", 12, map[string]string{"lines": "0-3"}), // range (start < 1)
		ref("block", big.ID, "d.md", 13, map[string]string{"lines": "5-3"}), // range (end < start)
		ref("block", big.ID, "d.md", 14, map[string]string{"lines": "7"}),   // ok single line
	}
	rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{big, small, dep, sun, future}, Refs: refs})
	bs := byState(rep)
	if len(bs[StateTooLarge]) != 1 || bs[StateTooLarge][0].Line != 1 {
		t.Errorf("too-large = %+v", bs[StateTooLarge])
	}
	rangeLines := map[int]bool{}
	for _, f := range bs[StateRange] {
		rangeLines[f.Line] = true
	}
	if len(rangeLines) != 5 || !rangeLines[3] || !rangeLines[4] || !rangeLines[6] || !rangeLines[12] || !rangeLines[13] {
		t.Errorf("range lines = %v", rangeLines)
	}
	if len(bs[StateDeprecated]) != 1 || bs[StateDeprecated][0].Line != 8 || bs[StateDeprecated][0].Severity != SeverityInfo {
		t.Errorf("deprecated = %+v", bs[StateDeprecated])
	}
	if len(bs[StateSunset]) != 1 || bs[StateSunset][0].Line != 9 {
		t.Errorf("sunset = %+v", bs[StateSunset])
	}
	if len(bs[StateUnverifiable]) != 1 || bs[StateUnverifiable][0].Line != 11 {
		t.Errorf("unverifiable = %+v", bs[StateUnverifiable])
	}
	okLines := map[int]bool{}
	for _, f := range bs[StateOK] {
		okLines[f.Line] = true
	}
	if okLines[8] {
		t.Error("a deprecated badge replaces ok")
	}
	for _, l := range []int{2, 5, 7, 10, 14} {
		if !okLines[l] {
			t.Errorf("line %d should be ok: %v", l, okLines)
		}
	}
	// Commit hook: exists and missing.
	in := Input{Repo: "api", Now: now, Defs: []block.Block{big}, Refs: []block.Reference{ref("block", big.ID, "d.md", 1, map[string]string{"at": "abc"})}, Opts: Options{CommitExists: func(sha string) bool { return sha == "abc" }}}
	if one(t, Run(in), StateOK).Message != "snapshot pinned" {
		t.Error("existing commit")
	}
	in.Refs[0].Args["at"] = "nope"
	if f := one(t, Run(in), StateBroken); !strings.Contains(f.Message, "nope") {
		t.Error("missing commit")
	}
}

func TestAssertAndEnv(t *testing.T) {
	t.Parallel()
	test := def("test-k7m2p4xq", "a_test.go", 3, "func TestX(t *testing.T) {}", nil)
	prodDef := def("host-h3v8n2wd", "prod.yaml", 3, "host: p", map[string]string{"env": "prod"})
	stagDef := def("host-h3v8n2wd", "staging.yaml", 3, "host: s", map[string]string{"env": "staging"})
	refs := []block.Reference{
		ref("block", test.ID, "d.md", 1, map[string]string{"assert": "true"}),
		ref("cfg", "host-h3v8n2wd", "d.md", 2, map[string]string{"env": "staging"}),
		ref("cfg", "host-h3v8n2wd", "d.md", 3, nil),                             // default env prod
		ref("cfg", "host-h3v8n2wd", "d.md", 4, map[string]string{"env": "dev"}), // no dev def
	}
	in := Input{Repo: "api", Now: now, Env: "prod", Defs: []block.Block{test, prodDef, stagDef}, Refs: refs, Opts: Options{TestResults: map[string]TestOutcome{test.ID: TestFailed}}}
	rep := Run(in)
	bs := byState(rep)
	if len(bs[StateAssertFailed]) != 1 || !strings.Contains(bs[StateAssertFailed][0].Message, "failed") {
		t.Errorf("assert = %+v", bs[StateAssertFailed])
	}
	if len(bs[StateBroken]) != 1 || bs[StateBroken][0].Line != 4 || !strings.Contains(bs[StateBroken][0].Remedy.Fix, "env=dev") {
		t.Errorf("env missing = %+v", bs[StateBroken])
	}
	files := map[int]string{}
	for _, f := range bs[StateOK] {
		files[f.Line] = f.File
	}
	if files[2] != "staging.yaml" || files[3] != "prod.yaml" {
		t.Errorf("env resolution = %v", files)
	}
	// Missing result and passed result.
	in.Opts.TestResults = map[string]TestOutcome{}
	if f := one(t, Run(Input{Repo: "api", Now: now, Defs: []block.Block{test}, Refs: refs[:1], Opts: in.Opts}), StateAssertFailed); !strings.Contains(f.Message, "no result recorded") {
		t.Errorf("missing result = %+v", f)
	}
	in.Opts.TestResults = map[string]TestOutcome{test.ID: TestPassed}
	if rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{test}, Refs: refs[:1], Opts: in.Opts}); rep.ExitCode != 0 {
		t.Error("passed test is ok")
	}
	// No TestResults at all: assert refs check as ordinary.
	if rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{test}, Refs: refs[:1]}); rep.ExitCode != 0 {
		t.Error("no results published means ordinary check")
	}
	// A def with an env and a ref without env and no default env: first def wins.
	if rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{prodDef}, Refs: []block.Reference{ref("cfg", prodDef.ID, "d.md", 1, nil)}}); rep.ExitCode != 0 {
		t.Error("env-less ref against single env def")
	}
	// A ref with env against an env-less def falls back to it.
	plain := def("plain-p2c4y7mk", "c.yaml", 3, "k: v", nil)
	if rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{plain}, Refs: []block.Reference{ref("cfg", plain.ID, "d.md", 1, map[string]string{"env": "prod"})}}); rep.ExitCode != 0 {
		t.Error("env ref against env-less def")
	}
}

func TestOtherVerbs(t *testing.T) {
	t.Parallel()
	sql := def("sweep-k7m2p4xq", "s.sql", 2, "DELETE 1;", map[string]string{"runnable": "true"})
	refs := []block.Reference{
		ref("run", sql.ID, "r.md", 1, map[string]string{"expect": "ok"}),
		ref("run", "", "r.md", 2, map[string]string{"cmd": "task test"}),
		ref("run", "missing-h3v8n2wd", "r.md", 3, nil),
		ref("table", "", "r.md", 4, map[string]string{"kind": "task"}),
		ref("url", "", "r.md", 5, map[string]string{"href": "https://example.com"}),
		ref("url", "", "r.md", 6, nil),
		ref("cfg", "", "r.md", 7, map[string]string{"query": "sql:select 1"}),
		ref("cfg", "", "r.md", 8, nil),
		ref("frobnicate", "", "r.md", 9, nil),
		ref("myverb", "", "r.md", 10, nil),
		ref("block", sql.ID, "r.md", 11, map[string]string{"bogus": "1", "another": "2"}),
	}
	in := Input{Repo: "api", Now: now, Defs: []block.Block{sql}, Refs: refs, Opts: Options{ExtraVerbs: map[string]bool{"myverb": true}}}
	rep := Run(in)
	bs := byState(rep)
	lines := func(st State) []int {
		var out []int
		for _, f := range bs[st] {
			out = append(out, f.Line)
		}
		return out
	}
	if l := lines(StateSkipped); len(l) != 2 || l[0] != 1 || l[1] != 2 {
		t.Errorf("skipped = %v", l)
	}
	if l := lines(StateBroken); len(l) != 1 || l[0] != 3 {
		t.Errorf("broken run = %v", l)
	}
	if l := lines(StateUnverifiable); len(l) != 3 || l[0] != 4 || l[1] != 5 || l[2] != 7 {
		t.Errorf("unverifiable = %v", l)
	}
	if l := lines(StateProblem); len(l) != 2 || l[0] != 6 || l[1] != 8 {
		t.Errorf("problem = %v", l)
	}
	if l := lines(StateUnknown); len(l) != 2 || l[0] != 9 || l[1] != 11 {
		t.Errorf("unknown = %v %+v", l, bs[StateUnknown])
	}
	if u := bs[StateUnknown][1]; !strings.Contains(u.Message, "another, bogus") {
		t.Errorf("unknown keys sorted = %q", u.Message)
	}
	if l := lines(StateOK); len(l) != 2 || l[0] != 10 || l[1] != 11 {
		t.Errorf("ok = %v", l)
	}
	// With run and records enabled, and a url hook, the verbs report ok.
	in.Opts.RunEnabled, in.Opts.HasRecords = true, true
	in.Opts.URLCheck = func(href string) URLResult {
		return URLResult{Checked: true, Status: 200, Final: href, Title: "Example"}
	}
	rep = Run(in)
	bs = byState(rep)
	if len(bs[StateSkipped]) != 0 || len(lines(StateOK)) != 6 {
		t.Errorf("enabled verbs = %+v", rep.Summary)
	}
	// Strict promotes warnings except the never-strict states.
	in.Opts.Strict = true
	rep = Run(in)
	for _, f := range rep.Findings {
		if f.Severity == SeverityWarning {
			t.Errorf("strict left a warning: %+v", f)
		}
	}
	if rep.ExitCode != 1 {
		t.Error("strict must fail on promoted warnings")
	}
}

// TestApplyRunsMakesFailuresFindings pins bug 29: a `ds:run` that failed
// under `check --run` made the command exit 1 with no finding naming it, and
// the summary counted only passing findings. The outcome of each executed
// run is now its finding, and the report is recounted.
func TestApplyRunsMakesFailuresFindings(t *testing.T) {
	t.Parallel()
	refs := []block.Reference{
		ref("run", "", "r.md", 1, map[string]string{"cmd": "false"}),
		ref("run", "", "r.md", 2, map[string]string{"cmd": "true"}),
		ref("run", "", "r.md", 3, map[string]string{"cmd": "x"}),
		ref("run", "", "r.md", 4, map[string]string{"cmd": "never reported"}),
		ref("run", "missing-h3v8n2wd", "r.md", 5, nil),
	}
	rep := Run(Input{Repo: "api", Now: now, Refs: refs, Opts: Options{RunEnabled: true}})
	if rep.ExitCode != 1 {
		t.Fatalf("the broken id fails the run already: %+v", rep.Summary)
	}
	rep = ApplyRuns(rep, []RunResult{
		{Doc: "r.md", Line: 1, Command: "false", Failed: true},
		{Doc: "r.md", Line: 2, Command: "true"},
		{Doc: "r.md", Line: 3, Skipped: "cmd= is not allowed here"},
		// A result for a broken directive does not hide that it is broken.
		{Doc: "r.md", Line: 5, Command: "x", Failed: true},
	})
	bs := byState(rep)
	if f := bs[StateRunFailed]; len(f) != 1 || f[0].Line != 1 || f[0].Severity != SeverityError || f[0].Message != "run failed: false" || !strings.Contains(f[0].Remedy.Fix, "run `false` by hand") {
		t.Errorf("failed run = %+v", f)
	}
	if f := bs[StateSkipped]; len(f) != 1 || f[0].Line != 3 || f[0].Severity != SeverityInfo || !strings.Contains(f[0].Message, "not allowed") {
		t.Errorf("skipped run = %+v", f)
	}
	if f := bs[StateOK]; len(f) != 2 || f[0].Message != "run passed: true" || f[1].Message != "run directive will execute where enabled" {
		t.Errorf("passing and unreported runs = %+v", f)
	}
	if len(bs[StateBroken]) != 1 {
		t.Errorf("broken = %+v", bs[StateBroken])
	}
	if rep.Summary[StateRunFailed] != 1 || rep.BySeverity[SeverityError] != 2 || rep.ExitCode != 1 {
		t.Errorf("recount = %+v %+v %d", rep.Summary, rep.BySeverity, rep.ExitCode)
	}
	// Recounting is the same counting Run does.
	if s, b, e := Tally(nil); len(s) != 0 || len(b) != 0 || e != 0 {
		t.Error("an empty report counts nothing")
	}
}

func TestURLHookOutcomes(t *testing.T) {
	t.Parallel()
	mk := func(res URLResult, title string) Finding {
		args := map[string]string{"href": "https://a.example/x"}
		if title != "" {
			args["title"] = title
		}
		rep := Run(Input{Repo: "api", Now: now, Refs: []block.Reference{ref("url", "", "d.md", 1, args)}, Opts: Options{URLCheck: func(string) URLResult { return res }}})
		return rep.Findings[0]
	}
	if f := mk(URLResult{Checked: false, Err: errors.New("offline")}, ""); f.State != StateUnverifiable || !strings.Contains(f.Message, "offline") {
		t.Errorf("offline = %+v", f)
	}
	if f := mk(URLResult{Checked: true, Status: 404}, ""); f.State != StateDead || f.Severity != SeverityError {
		t.Errorf("dead = %+v", f)
	}
	if f := mk(URLResult{Checked: true, Status: 200, Err: errors.New("tls")}, ""); f.State != StateDead {
		t.Errorf("error = %+v", f)
	}
	if f := mk(URLResult{Checked: true, Status: 200, Final: "https://b.example/y"}, ""); f.State != StateURLMoved {
		t.Errorf("moved = %+v", f)
	}
	if f := mk(URLResult{Checked: true, Status: 200, Final: "https://a.example/x", Title: "Other"}, "TOAST"); f.State != StateRetitled {
		t.Errorf("retitled = %+v", f)
	}
	if f := mk(URLResult{Checked: true, Status: 200, Title: "TOAST docs"}, "TOAST"); f.State != StateOK {
		t.Errorf("alive = %+v", f)
	}
}

func TestClaims(t *testing.T) {
	t.Parallel()
	changedOld := def("about-k7m2p4xq", "a.go", 3, "v1", nil)
	changedNew := def("about-k7m2p4xq", "a.go", 3, "v2", nil)
	prev := ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", changedOld)}}
	claim := func(line int, args map[string]string) block.Reference {
		r := ref("claim", "", "c.md", line, args)
		r.Carrier = block.CarrierComment
		return r
	}
	refs := []block.Reference{
		claim(1, map[string]string{"owner": "@p", "reviewed": "2026-08-01", "expires": "90d"}),             // ok
		claim(2, map[string]string{"owner": "@p", "reviewed": "2026-01-01", "expires": "90d"}),             // expired
		claim(3, map[string]string{"owner": "@p"}),                                                         // unknown (no dates)
		claim(4, map[string]string{"reviewed": "not-a-date", "expires": "90d"}),                            // problem
		claim(5, map[string]string{"reviewed": "2026-08-01", "expires": "3y"}),                             // problem (unit)
		claim(6, map[string]string{"reviewed": "2026-08-01", "expires": "90d", "about": "about-k7m2p4xq"}), // expired via about
		claim(7, map[string]string{"reviewed": "2026-01-01", "expires": "30d"}),                            // renewed by ack
	}
	acks := ledger.Acks{Rows: []ledger.Ack{{At: now.Add(-24 * time.Hour), Actor: "k", ActorKind: ledger.ActorHuman, Repo: "api", Doc: "c.md", Line: 7}}}
	rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{changedNew}, Refs: refs, Prev: prev, Acks: acks})
	bs := byState(rep)
	want := map[State][]int{StateOK: {1, 7}, StateExpired: {2, 6}, StateUnknown: {3}, StateProblem: {4, 5}}
	for st, lines := range want {
		got := []int{}
		for _, f := range bs[st] {
			got = append(got, f.Line)
		}
		if len(got) != len(lines) {
			t.Errorf("%s lines = %v, want %v", st, got, lines)
			continue
		}
		for i := range lines {
			if got[i] != lines[i] {
				t.Errorf("%s lines = %v, want %v", st, got, lines)
			}
		}
	}
	if f := bs[StateExpired][1]; f.ID != "about-k7m2p4xq" || len(f.Classes) == 0 {
		t.Errorf("about expiry = %+v", f)
	}
	if !strings.Contains(bs[StateOK][0].Message, "2026-10-30") {
		t.Errorf("valid until = %q", bs[StateOK][0].Message)
	}
	// An ack on line 6 does not touch the `about` expiry; the block still changed.
	if len(bs[StateUncovered]) != 0 {
		t.Errorf("about ids count as referenced: %+v", bs[StateUncovered])
	}
}

// promise:local-unverifiable
func TestChains(t *testing.T) {
	t.Parallel()
	truth := def("op-k7m2p4xq", ".env.tpl", 2, "op://v/i/f", map[string]string{"secret": "true", "truth": "true"})
	copy1 := def("gh-h3v8n2wd", "wf.yml", 2, "STRIPE_KEY", map[string]string{"secret": "true", "from": truth.ID})
	app := def("app-p2c4y7mk", "a.go", 2, "STRIPE_KEY", map[string]string{"secret": "true", "from": copy1.ID})
	lone := def("lone-t4k2b9rf", ".env.tpl", 9, "X", map[string]string{"secret": "true"})
	dangling := def("dang-q9x1z6ch", "a.go", 9, "Y", map[string]string{"from": "nope-m2q1s8vt"})
	twoTruthA := def("ta-a2b6f8jk", "x", 1, "a", map[string]string{"truth": "true"})
	twoTruthB := def("tb-b3c7g9kl", "x", 2, "b", map[string]string{"truth": "true", "from": twoTruthA.ID})
	noTruthA := def("na-c4d8h2lm", "y", 1, "a", nil)
	noTruthB := def("nb-d5e9j3mn", "y", 2, "b", map[string]string{"from": noTruthA.ID})
	cycA := def("ca-e6f2k4np", "z", 1, "a", map[string]string{"from": "cb-f7g3l5pq"})
	cycB := def("cb-f7g3l5pq", "z", 2, "b", map[string]string{"from": cycA.ID})
	local := def("loc-g8h4m6qr", ".ds/defs.md", 1, "", map[string]string{"local": "true", "file": "~/x"})
	defs := []block.Block{truth, copy1, app, lone, dangling, twoTruthA, twoTruthB, noTruthA, noTruthB, cycA, cycB, local}
	refs := []block.Reference{ref("chain", app.ID, "d.md", 1, nil), ref("block", local.ID, "d.md", 2, nil)}
	rep := Run(Input{Repo: "api", Now: now, Defs: defs, Refs: refs})
	bs := byState(rep)
	ids := func(st State) map[string]bool {
		m := map[string]bool{}
		for _, f := range bs[st] {
			m[f.ID] = true
		}
		return m
	}
	if u := ids(StateUnsourced); len(u) != 1 || !u[lone.ID] {
		t.Errorf("unsourced = %v", u)
	}
	cb := ids(StateChainBroken)
	for _, want := range []string{dangling.ID, twoTruthA.ID, noTruthA.ID, cycA.ID, cycB.ID} {
		if !cb[want] {
			t.Errorf("chain broken missing %s: %v", want, cb)
		}
	}
	if u := bs[StateUnverifiable]; len(u) != 2 || u[0].ID != local.ID || u[1].ID != local.ID {
		t.Errorf("local def and its citation are both unverifiable: %+v", u)
	}
	// The chain reference itself is ok; chain members are referenced through from=.
	if len(bs[StateOK]) != 1 {
		t.Errorf("chain ref = %+v", bs[StateOK])
	}
	unc := ids(StateUncovered)
	if unc[truth.ID] || unc[copy1.ID] || !unc[lone.ID] || unc[local.ID] {
		t.Errorf("uncovered = %v", unc)
	}
}

func TestPagesAndUncovered(t *testing.T) {
	t.Parallel()
	d := def("x-k7m2p4xq", "a.go", 3, "x", nil)
	un := def("u-h3v8n2wd", "a.go", 9, "u", map[string]string{"owner": "@o"})
	pages := map[string]extract.Page{
		"docs/a.md": {Covers: []string{d.ID, "ghost-p2c4y7mk"}, ReviewEvery: "30d"},
		"docs/b.md": {ReviewEvery: "bad"},
		"docs/c.md": {ReviewEvery: "365d"},
		"docs/d.md": {ReviewEvery: "1d"},
		"docs/e.md": {Covers: []string{d.ID}},
	}
	acks := ledger.Acks{Rows: []ledger.Ack{
		{At: now.Add(-40 * 24 * time.Hour), Repo: "api", Doc: "docs/a.md", Line: 3},
		{At: now.Add(-2 * 24 * time.Hour), Repo: "api", Doc: "docs/d.md", Line: 1},
	}}
	prev := ledger.Ledger{Header: ledger.Header{ScannedAt: now.Add(-100 * 24 * time.Hour)}}
	rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{d, un}, Pages: pages, Acks: acks, Prev: prev})
	bs := byState(rep)
	if len(bs[StateOrphan]) != 1 || bs[StateOrphan][0].ID != "ghost-p2c4y7mk" || bs[StateOrphan][0].Doc != "docs/a.md" {
		t.Errorf("orphan = %+v", bs[StateOrphan])
	}
	exp := map[string]bool{}
	for _, f := range bs[StateExpired] {
		exp[f.Doc] = true
	}
	// a: ack 40d ago with 30d window → due; c: no acks, ledger 100d ago, 365d → not due; d: ack 2d ago, 1d → due.
	if !exp["docs/a.md"] || exp["docs/c.md"] || !exp["docs/d.md"] || len(exp) != 2 {
		t.Errorf("review due = %v", exp)
	}
	if len(bs[StateProblem]) != 1 || bs[StateProblem][0].Doc != "docs/b.md" {
		t.Errorf("bad review_every = %+v", bs[StateProblem])
	}
	if len(bs[StateUncovered]) != 1 || bs[StateUncovered][0].ID != un.ID || bs[StateUncovered][0].Owner != "@o" || bs[StateUncovered][0].Severity != SeverityInfo {
		t.Errorf("uncovered = %+v", bs[StateUncovered])
	}
	// A page with review_every and no acks and no previous ledger: due.
	rep = Run(Input{Repo: "api", Now: now, Pages: map[string]extract.Page{"n.md": {ReviewEvery: "10d"}}})
	if len(byState(rep)[StateExpired]) != 1 {
		t.Error("never-reviewed page is due")
	}
}

func TestProblemsAndMerged(t *testing.T) {
	t.Parallel()
	probs := []scan.Problem{
		{Pos: block.Position{File: "a.md", Start: 1}, Err: scan.ErrRemotePick},
		{Pos: block.Position{File: "a.md", Start: 2}, Err: scan.ErrRemoteMissing},
		{Pos: block.Position{File: "a.md", Start: 5}, Err: scan.ErrPick},
		{Pos: block.Position{File: "g.pb.go", Start: 3}, Err: scan.ErrDefInGenerated},
		{Pos: block.Position{File: "a.md", Start: 4}, Err: extract.ErrNoID},
	}
	other := def("remote-k7m2p4xq", "x.go", 1, "x", nil)
	rep := Run(Input{Repo: "docs", Now: now, Problems: probs, Merged: []block.Block{other}, Refs: []block.Reference{ref("block", other.ID, "d.md", 1, nil)}})
	bs := byState(rep)
	if len(bs[StatePickFailed]) != 3 || len(bs[StateUnknown]) != 1 || len(bs[StateProblem]) != 1 {
		t.Errorf("problem mapping = %+v", rep.Summary)
	}
	if len(bs[StateOK]) != 1 || bs[StateOK][0].File != "x.go" {
		t.Errorf("merged def cite = %+v", bs[StateOK])
	}
	if len(bs[StateUncovered]) != 0 {
		t.Error("merged defs are not this repo's uncovered candidates")
	}
	// Findings are sorted by doc, line, id.
	for i := 1; i < len(rep.Findings); i++ {
		a, b := rep.Findings[i-1], rep.Findings[i]
		if a.Doc > b.Doc || (a.Doc == b.Doc && a.Line > b.Line) {
			t.Fatalf("unsorted at %d", i)
		}
	}
	// Zero Now defaults to the clock without panicking.
	if r := Run(Input{Repo: "x"}); r.ExitCode != 0 || len(r.Findings) != 0 {
		t.Error("empty input")
	}
}

func TestHelpers(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]time.Duration{"90d": 90 * 24 * time.Hour, "2w": 14 * 24 * time.Hour, "6h": 6 * time.Hour, "30m": 30 * time.Minute, "0d": 0} {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "d", "x1d", "-1d", "3y", "10"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("ParseDuration(%q) accepted", bad)
		}
	}
	if editDistance("abc", "abd") != 1 || editDistance("abc", "abc") != 0 || editDistance("abc", "xyz") != 3 || editDistance("a", "abcdef") != 3 || editDistance("abcd", "ab") != 2 {
		t.Error("editDistance")
	}
	if len(StateValues) != len(severityOf) {
		t.Errorf("StateValues (%d) and severityOf (%d) out of sync", len(StateValues), len(severityOf))
	}
	for _, st := range StateValues {
		if _, ok := severityOf[st]; !ok {
			t.Errorf("no severity for %s", st)
		}
	}
	if SeverityOf(StateOK) != SeverityNone || SeverityOf(StateBroken) != SeverityError || SeverityOf(State("made-up")) != SeverityError {
		t.Error("SeverityOf")
	}
	if len(SeverityValues) != 4 || classList(nil) != "body" {
		t.Error("misc")
	}
	if len(knownKeys) != len(extract.BuiltinVerbs) {
		t.Error("knownKeys must cover every built-in verb")
	}
}

func TestDeprecatedBadgeWithAckedChange(t *testing.T) {
	t.Parallel()
	old := def("dep-p2c4y7mk", "a.go", 3, "v1", map[string]string{"deprecated": "2026-01-01"})
	nw := def("dep-p2c4y7mk", "a.go", 3, "v2", map[string]string{"deprecated": "2026-01-01"})
	r := ref("block", nw.ID, "d.md", 1, nil)
	in := Input{
		Repo: "api", Now: now, Defs: []block.Block{nw}, Refs: []block.Reference{r},
		Prev: ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", old)}},
		Acks: ledger.Acks{Rows: []ledger.Ack{{At: now, Actor: "k", ActorKind: ledger.ActorHuman, ID: nw.ID, Repo: "api", Doc: "d.md", Line: 1, BlockHash: nw.Hash}}},
	}
	rep := Run(in)
	if len(rep.Findings) != 1 || rep.Findings[0].State != StateDeprecated || rep.ExitCode != 0 {
		t.Errorf("acked deprecated change = %+v", rep.Findings)
	}
}

func TestMergedRefs(t *testing.T) {
	t.Parallel()
	old := def("save-k7m2p4xq", "a.go", 3, "v1", nil)
	nw := def("save-k7m2p4xq", "a.go", 3, "v2", nil)
	remote := ref("block", nw.ID, "docs/runbook.md", 4, nil)
	other := def("web-h3v8n2wd", "web.ts", 1, "x", map[string]string{block.KeyRepo: "web"})
	in := Input{
		Repo: "api", Now: now, Defs: []block.Block{nw}, Merged: []block.Block{other},
		Prev:       ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", old)}},
		MergedRefs: []RemoteRef{{Repo: "docs", Ref: remote}, {Repo: "docs", Ref: ref("block", other.ID, "docs/web.md", 1, nil)}},
	}
	rep := Run(in)
	un := one(t, rep, StateUnacked)
	if un.DocRepo != "docs" || un.Doc != "docs/runbook.md" || un.Repo != "api" {
		t.Errorf("merged ref finding = %+v", un)
	}
	okf := one(t, rep, StateOK)
	if okf.Repo != "web" || okf.DocRepo != "docs" {
		t.Errorf("merged def repo = %+v", okf)
	}
	if len(byState(rep)[StateUncovered]) != 0 {
		t.Error("a block cited only from another repo is covered")
	}
	// The publishing repo's acked hash clears it.
	in.MergedRefs[0].AckedHash = nw.Hash
	if rep := Run(in); len(byState(rep)[StateUnacked]) != 0 {
		t.Errorf("published ack must count: %+v", rep.Findings)
	}
	// A published first-seen baseline is honoured the same way, so a
	// citation another repo has never acked is not silently adopted: seen at
	// the old hash flags, seen at the current one does not.
	in.MergedRefs[0].AckedHash = ""
	in.MergedRefs[0].SeenHash = old.Hash
	if rep := Run(in); len(byState(rep)[StateUnacked]) != 1 {
		t.Errorf("published seen hash must give a baseline: %+v", rep.Findings)
	}
	in.MergedRefs[0].SeenHash = nw.Hash
	if rep := Run(in); len(byState(rep)[StateUnacked]) != 0 {
		t.Errorf("seen at the current hash is covered: %+v", rep.Findings)
	}
}

func TestUndocumented(t *testing.T) {
	t.Parallel()
	rep := Run(Input{Repo: "api", Now: now, Undocumented: []Undocumented{{File: "pkg/api/h.go", Line: 12, Symbol: "Handle"}}})
	f := one(t, rep, StateUndocumented)
	if f.Severity != SeverityError || rep.ExitCode != 1 || !strings.Contains(f.Remedy.Fix, "ds def pkg/api/h.go#Handle") || f.Doc != "pkg/api/h.go" {
		t.Errorf("undocumented = %+v", f)
	}
}

func TestResolveChains(t *testing.T) {
	t.Parallel()
	truth := def("op-k7m2p4xq", ".env.tpl", 2, "op://v/i/f", map[string]string{"secret": "true", "truth": "true"})
	ghCopy := def("gh-h3v8n2wd", "wf.yml", 2, "${{ secrets.STRIPE_KEY }}", map[string]string{"secret": "true", "from": truth.ID, "sync": "scripts/sync.sh"})
	vault := def("vault-p2c4y7mk", "v.txt", 2, "vault:kv/x", map[string]string{"secret": "true", "from": truth.ID})
	aws := def("aws-t4k2b9rf", "a.txt", 2, "arn:aws:secretsmanager:x", map[string]string{"secret": "true", "from": truth.ID})
	gone := def("gone-q9x1z6ch", "g.txt", 2, "op://v/missing", map[string]string{"secret": "true", "from": truth.ID})
	down := def("down-m2q1s8vt", "d.txt", 2, "projects/p/secrets/s", map[string]string{"secret": "true", "from": truth.ID})
	noProvider := def("bare-a2b6f8jk", "b.txt", 2, "STRIPE_KEY", map[string]string{"secret": "true", "from": truth.ID})
	multi := def("multi-b3c7g9kl", "m.txt", 2, "a\nb", map[string]string{"secret": "true", "from": truth.ID})
	plain := def("plain-c4d8h2lm", "p.txt", 2, "op://plain", map[string]string{"from": truth.ID})
	resolver := func(provider, addr string) ResolveResult {
		switch {
		case addr == "op://v/i/f":
			return ResolveResult{Checked: true, Exists: true, Hash: "T1"}
		case provider == "github":
			return ResolveResult{Checked: true, Exists: true}
		case provider == "vault":
			return ResolveResult{Checked: true, Exists: true, Hash: "T1"}
		case provider == "aws":
			return ResolveResult{Checked: true, Exists: true, Hash: "OLD"}
		case addr == "op://v/missing":
			return ResolveResult{Checked: true, Exists: false}
		case provider == "gcp":
			return ResolveResult{Checked: false, Err: errors.New("no credentials")}
		}
		t.Errorf("unexpected resolve %s %s", provider, addr)
		return ResolveResult{}
	}
	defs := []block.Block{truth, ghCopy, vault, aws, gone, down, noProvider, multi, plain}
	refs := []block.Reference{ref("chain", ghCopy.ID, "d.md", 1, nil)}
	for _, b := range defs[1:] {
		refs = append(refs, ref("block", b.ID, "d.md", 2, nil))
	}
	in := Input{Repo: "api", Now: now, Defs: defs, Refs: refs, Opts: Options{Resolve: resolver, StoredHashes: map[string]string{truth.ID: "T0"}}}
	rep := Run(in)
	bs := byState(rep)
	if len(bs[StateOutOfSync]) != 1 || bs[StateOutOfSync][0].ID != aws.ID || bs[StateOutOfSync][0].Hash != (HashPair{Acked: "T1", Current: "OLD"}) || !strings.Contains(bs[StateOutOfSync][0].Remedy.Fix, "no sync= declared") {
		t.Errorf("out of sync = %+v", bs[StateOutOfSync])
	}
	if len(bs[StateResolveFailed]) != 1 || bs[StateResolveFailed][0].ID != gone.ID {
		t.Errorf("resolve failed = %+v", bs[StateResolveFailed])
	}
	if len(bs[StateRotated]) != 1 || bs[StateRotated][0].ID != truth.ID || bs[StateRotated][0].Severity != SeverityWarning {
		t.Errorf("rotated = %+v", bs[StateRotated])
	}
	if len(bs[StateUnverifiable]) != 1 || bs[StateUnverifiable][0].ID != down.ID || !strings.Contains(bs[StateUnverifiable][0].Message, "no credentials") {
		t.Errorf("unreachable provider = %+v", bs[StateUnverifiable])
	}
	if rep.TruthHashes[truth.ID] != "T1" {
		t.Errorf("truth hashes = %v", rep.TruthHashes)
	}
	// Without a resolver none of this runs; with matching stored hash no
	// rotation; a chain with no single truth compares nothing.
	if rep := Run(Input{Repo: "api", Now: now, Defs: defs, Refs: refs}); len(byState(rep)[StateOutOfSync]) != 0 || rep.TruthHashes != nil {
		t.Error("no resolver must not resolve")
	}
	in.Opts.StoredHashes = map[string]string{truth.ID: "T1"}
	if rep := Run(in); len(byState(rep)[StateRotated]) != 0 {
		t.Error("matching stored hash is not a rotation")
	}
	two := def("t2-d5e9j3mn", "t.txt", 1, "op://v/other", map[string]string{"secret": "true", "truth": "true", "from": truth.ID})
	inTwo := Input{Repo: "api", Now: now, Defs: append(defs, two), Refs: append(refs, ref("block", two.ID, "d.md", 3, nil)), Opts: Options{Resolve: func(string, string) ResolveResult { return ResolveResult{Checked: true, Exists: true, Hash: "X"} }}}
	if rep := Run(inTwo); len(byState(rep)[StateOutOfSync]) != 0 || len(byState(rep)[StateChainBroken]) == 0 {
		t.Errorf("two truths compare nothing = %v", rep.Summary)
	}
	// A truth that is existence-only (no hash) compares nothing either.
	nameOnly := func(string, string) ResolveResult { return ResolveResult{Checked: true, Exists: true} }
	if rep := Run(Input{Repo: "api", Now: now, Defs: defs, Refs: refs, Opts: Options{Resolve: nameOnly}}); len(byState(rep)[StateOutOfSync]) != 0 {
		t.Error("existence-only truth compares nothing")
	}
	// Unreachable without an error message.
	silent := func(string, string) ResolveResult { return ResolveResult{} }
	if rep := Run(Input{Repo: "api", Now: now, Defs: defs, Refs: refs, Opts: Options{Resolve: silent}}); len(byState(rep)[StateUnverifiable]) != 6 {
		t.Errorf("silent resolver = %v", rep.Summary)
	}
}

func TestRepoModeRegions(t *testing.T) {
	t.Parallel()
	d := def("save-k7m2p4xq", "a.go", 3, "func Save() {}", nil)
	d.Symbol = "Save"
	good, _, _ := render.Fragment(d, block.Reference{ID: d.ID, Args: map[string]string{"id": d.ID}, Pos: block.Position{File: "d.md", Start: 1}}, "ds", 40)
	mk := func(line int, hash, text string) block.Reference {
		r := ref("block", d.ID, "d.md", line, nil)
		r.Carrier = block.CarrierBlock
		r.SetSentence("")
		r.Region = &block.Region{Start: line + 1, End: line + 5, Hash: hash, Text: text}
		return r
	}
	short := textnorm.Short(d.Hash)
	refs := []block.Reference{
		mk(1, short, good),                      // ok
		mk(2, "000000", good),                   // stale
		mk(3, short, "**Save** edited by hand"), // tampered
		mk(4, "", ""),                           // stale: no hash recorded
		mk(5, short, good+"   \n\n"),            // ok: whitespace only
	}
	rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{d}, Refs: refs})
	bs := byState(rep)
	lines := func(st State) []int {
		var out []int
		for _, f := range bs[st] {
			out = append(out, f.Line)
		}
		return out
	}
	if l := lines(StateOK); len(l) != 2 || l[0] != 1 || l[1] != 5 {
		t.Errorf("ok = %v", l)
	}
	if l := lines(StateStale); len(l) != 2 || l[0] != 2 || l[1] != 4 {
		t.Errorf("stale = %v", l)
	}
	if l := lines(StateTampered); len(l) != 1 || l[0] != 3 || !strings.Contains(bs[StateTampered][0].Remedy.Fix, "edit the source block") {
		t.Errorf("tampered = %v %+v", l, bs[StateTampered])
	}
	// A region on a secret def cannot be rendered, so only the hash is judged.
	sec := def("sec-h3v8n2wd", "e.env", 1, "op://x", map[string]string{"secret": "true", "truth": "true"})
	r := mk(9, textnorm.Short(sec.Hash), "anything")
	r.ID, r.Args = sec.ID, map[string]string{"id": sec.ID}
	if rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{sec}, Refs: []block.Reference{r}}); len(byState(rep)[StateTampered]) != 0 {
		t.Errorf("secret region = %+v", rep.Findings)
	}
}

func TestVerbHandlersAndKeys(t *testing.T) {
	t.Parallel()
	tk := def("tk-a2b6f8jk", "t.txt", 1, "T-1", nil)
	refs := []block.Reference{
		ref("ticket", tk.ID, "d.md", 1, map[string]string{"state": "open"}),
		ref("ticket", tk.ID, "d.md", 2, map[string]string{"state": "done", "bogus": "1"}),
		ref("ticket", tk.ID, "d.md", 3, nil),
		ref("badge", "", "d.md", 4, nil),
		ref("silent", "", "d.md", 5, nil),
	}
	handlers := map[string]VerbHandler{
		"ticket": func(r block.Reference) []Finding {
			if r.Args["state"] == "done" {
				return []Finding{{State: StateExpired, Message: "ticket closed"}}
			}
			return nil
		},
		"badge": func(block.Reference) []Finding { return []Finding{{Message: "no state given"}} },
	}
	in := Input{Repo: "api", Now: now, Defs: []block.Block{tk}, Refs: refs, Opts: Options{
		ExtraVerbs:   map[string]bool{"ticket": true, "badge": true, "silent": true},
		Handlers:     handlers,
		KnownKeys:    map[string][]string{"ticket": {"id"}},
		RequiredKeys: map[string][]string{"ticket": {"state"}},
	}}
	rep := Run(in)
	bs := byState(rep)
	if len(bs[StateOK]) != 2 || bs[StateOK][0].Line != 1 || bs[StateOK][1].Line != 5 {
		t.Errorf("ok = %+v", bs[StateOK])
	}
	if len(bs[StateExpired]) != 1 || bs[StateExpired][0].Line != 2 || bs[StateExpired][0].Verb != "ticket" || bs[StateExpired][0].ID != tk.ID {
		t.Errorf("handler finding = %+v", bs[StateExpired])
	}
	if len(bs[StateUnknown]) != 1 || !strings.Contains(bs[StateUnknown][0].Message, "bogus") {
		t.Errorf("unknown key on plugin verb = %+v", bs[StateUnknown])
	}
	probs := bs[StateProblem]
	if len(probs) != 2 || probs[0].Line != 3 || !strings.Contains(probs[0].Message, "needs state") || probs[1].Line != 4 || probs[1].Message != "no state given" {
		t.Errorf("required key and default state = %+v", probs)
	}
	if len(bs[StateUncovered]) != 0 {
		t.Error("a def cited only by a plugin verb is covered")
	}
	// A classifier replacement is passed through.
	old := def("c-b3c7g9kl", "a.go", 1, "v1", nil)
	nw := def("c-b3c7g9kl", "a.go", 1, "v2", nil)
	called := false
	rep = Run(Input{
		Repo: "api", Now: now, Defs: []block.Block{nw}, Refs: []block.Reference{ref("block", nw.ID, "d.md", 1, nil)},
		Prev: ledger.Ledger{Rows: []ledger.Row{ledger.FromBlock("api", old)}},
		// A classifier is asked only when the old body is known (bug 28).
		Opts: Options{OldContent: func(ledger.Row) (string, bool) { return old.Content, true }, Classify: func(o, n block.Block, oc string) []block.Class {
			called = true
			return []block.Class{block.ClassWhitespace}
		}},
	})
	if !called || len(byState(rep)[StateUnacked]) != 0 {
		t.Errorf("custom classifier must decide: called=%v %v", called, rep.Summary)
	}
}

func TestRemovedRepoAndBranches(t *testing.T) {
	t.Parallel()
	main := def("api-a2b6f8jk", "x.go", 1, "main body", map[string]string{block.KeyRepo: "api"})
	rel := def("api-a2b6f8jk", "x.go", 1, "release body", map[string]string{block.KeyRepo: "api", block.KeyBranch: "release-1"})
	refs := []block.Reference{
		ref("block", "api-a2b6f8jk", "d.md", 1, nil),
		ref("block", "api-a2b6f8jk", "d.md", 2, map[string]string{"branch": "release-1"}),
		ref("block", "api-a2b6f8jk", "d.md", 3, map[string]string{"branch": "nope"}),
		ref("block", "gone-b3c7g9kl", "d.md", 4, nil),
	}
	rep := Run(Input{Repo: "docs", Now: now, Merged: []block.Block{main, rel}, Refs: refs, Removed: map[string]string{"gone-b3c7g9kl": "legacy"}})
	hashes := map[int]string{}
	for _, f := range byState(rep)[StateOK] {
		hashes[f.Line] = f.Hash.Current
	}
	if hashes[1] != main.Hash || hashes[2] != rel.Hash || hashes[3] != main.Hash {
		t.Errorf("branch selection = %v", hashes)
	}
	b := one(t, rep, StateBroken)
	if !strings.Contains(b.Message, "removed from the workspace") || !strings.Contains(b.Remedy.Fix, "repo removed") {
		t.Errorf("removed repo = %+v", b)
	}
	// Only branch defs exist: a plain cite still resolves.
	rep = Run(Input{Repo: "docs", Now: now, Merged: []block.Block{rel}, Refs: refs[:1]})
	if one(t, rep, StateOK).Hash.Current != rel.Hash {
		t.Error("branch-only def serves plain cites")
	}
}

// TestWordingHoldsAnAckToItsSentence pins SPEC §18 as enforced (bug 13): with
// the block unchanged, a cited sentence rewritten since its ack is unacked
// and shows the old and new wording, and an ack made under an older
// sentence rule is unacked once as predating it — never as a rewrite,
// because nobody knows whether the wording changed. Before, an ack held by
// position alone, so a sentence could be reversed in meaning and stay ok.
func TestWordingHoldsAnAckToItsSentence(t *testing.T) {
	t.Parallel()
	b := def("sess-save-k7m2p4xq", "a.go", 3, "func Save() {}", nil)
	cite := func(sentence string) block.Reference {
		r := ref("block", b.ID, "docs/a.md", 5, nil)
		r.SetSentence(sentence)
		return r
	}
	ack := func(rule int, sentence string) ledger.Acks {
		var r block.Reference
		r.SetSentence(sentence)
		return ledger.Acks{Rows: []ledger.Ack{{At: now, Actor: "k", ActorKind: ledger.ActorHuman, ID: b.ID, Repo: "api", Doc: "docs/a.md", Line: 5, BlockHash: b.Hash, Rule: rule, Sentence: sentence, SentenceHash: r.SentenceHash}}}
	}
	run := func(c block.Reference, acks ledger.Acks, wording bool) Report {
		return Run(Input{Repo: "api", Now: now, Defs: []block.Block{b}, Refs: []block.Reference{c}, Acks: acks, Opts: Options{Wording: wording}})
	}
	old, rewritten := "Every write goes through Save.", "No write ever goes through Save."

	if f := one(t, run(cite(old), ack(extract.Rule, old), true), StateOK); f.Message != "up to date" {
		t.Errorf("same sentence, same rule = %+v", f)
	}
	f := one(t, run(cite(rewritten), ack(extract.Rule, old), true), StateUnacked)
	if f.Message != msgSentenceRewritten || f.Diff != "-"+old+"\n+"+rewritten+"\n" || f.Baseline != BaselineAck || f.Remedy.IfStillTrue == "" {
		t.Errorf("rewritten = %+v", f)
	}
	// The same sentence under another rule holds (bug 17). Both rules chose the
	// very same text, so the ack approved exactly what is there; reporting it
	// asked a person to re-read a sentence that had not changed by a byte, 100
	// of them after the rule was renumbered for the first release.
	for _, other := range []int{extract.Rule + 1, extract.Rule + 3, 0} {
		if f := one(t, run(cite(old), ack(other, old), true), StateOK); f.Message != "up to date" {
			t.Errorf("same sentence under rule %d = %+v", other, f)
		}
	}
	f = one(t, run(cite(rewritten), ack(extract.Rule+1, old), true), StateUnacked)
	if !strings.Contains(f.Message, fmt.Sprintf("recorded under sentence rule %d, not %d", extract.Rule+1, extract.Rule)) || f.Diff != "" {
		t.Errorf("another rule is never read as a rewrite: %+v", f)
	}
	f = one(t, run(cite(old), ack(0, ""), true), StateUnacked)
	if !strings.Contains(f.Message, "under sentence rule unrecorded,") {
		t.Errorf("an ack from before the rule was recorded = %+v", f)
	}
	// [check] sentence = "position" holds an ack to its place only.
	one(t, run(cite(rewritten), ack(extract.Rule, old), false), StateOK)
	// An ack that recorded no sentence cannot be compared.
	noSentence := ack(extract.Rule, old)
	noSentence.Rows[0].SentenceHash = ""
	one(t, run(cite(rewritten), noSentence, true), StateOK)
	// A block-position citation has no wording to rewrite.
	blockPos := cite(old)
	blockPos.Carrier = block.CarrierBlock
	blockPos.SetSentence("")
	one(t, run(blockPos, ack(extract.Rule, old), true), StateOK)
}

// TestFindingsOnOneLineOrderByBlock pins the check half of bug 32: two
// citations on one doc line were ordered by id, so the same tree listed them
// in the order of its random suffixes. They follow their blocks' places.
func TestFindingsOnOneLineOrderByBlock(t *testing.T) {
	t.Parallel()
	low := def("limit-zzzzzzzz", "a.go", 4, "1", nil)
	high := def("limit-aaaaaaaa", "a.go", 7, "10", nil)
	other := def("limit-bbbbbbbb", "0.go", 9, "5", nil)
	rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{high, low, other}, Refs: []block.Reference{
		ref("block", high.ID, "d.md", 3, nil), ref("block", low.ID, "d.md", 3, nil), ref("block", other.ID, "d.md", 3, nil),
	}})
	var got []string
	for _, f := range rep.Findings {
		if f.Doc == "d.md" {
			got = append(got, f.ID)
		}
	}
	if strings.Join(got, " ") != "limit-bbbbbbbb limit-zzzzzzzz limit-aaaaaaaa" {
		t.Errorf("order = %v", got)
	}
}
