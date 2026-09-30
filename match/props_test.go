package match

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/ledger"
)

// The classes Classify returns decide whether prose flags (block.Flags), so
// a wrong class is a silent pass, not a cosmetic slip. These tests state
// what must hold for any edit rather than for a few chosen ones.

// TestAttributesAreNotComments pins the case that was a silent pass: PHP 8
// attributes start with `#[`, and PHP's comment prefixes include `#`. A
// route attribute changing was classified comment-only, and neither a
// stable nor an api block flags on a comment change.
func TestAttributesAreNotComments(t *testing.T) {
	t.Parallel()
	php := []string{"//", "#"}
	old := kind(mk("r-k7m2p4xq", "a.php", 1, 3, "index", "#[Route('/users')]\npublic function index() {\n}", nil), block.KindFunc)
	nw := kind(mk("r-k7m2p4xq", "a.php", 1, 3, "index", "#[Route('/admins')]\npublic function index() {\n}", nil), block.KindFunc)
	got := Classify(old, nw, old.Content, php)
	if !block.Flags(block.StabilityStable, got) || !block.Flags(block.StabilityAPI, got) {
		t.Errorf("a changed attribute must flag stable and api blocks, got %v", got)
	}
	// A real comment change is still a comment change.
	c1 := kind(mk("r-k7m2p4xq", "a.php", 1, 3, "index", "# lists users\npublic function index() {\n}", nil), block.KindFunc)
	c2 := kind(mk("r-k7m2p4xq", "a.php", 1, 3, "index", "# lists all users\npublic function index() {\n}", nil), block.KindFunc)
	if got := Classify(c1, c2, c1.Content, php); len(got) != 1 || got[0] != block.ClassComment {
		t.Errorf("a comment edit = %v, want comment", got)
	}
}

// TestClassifyNeverSilencesCode is the property: over random Go functions,
// any edit that touches a line of code — not a comment, not whitespace —
// yields a class that flags a stable block. An edit that touches only
// comment lines yields comment and nothing else. The generator mixes
// comment lines, code lines, blank lines, and edits of each.
func TestClassifyNeverSilencesCode(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(7))
	prefixes := []string{"//"}
	codeLine := func(i int) string { return fmt.Sprintf("\tx%d := %d", i, rng.Intn(100)) }
	for trial := 0; trial < 20000; trial++ {
		var lines []string
		lines = append(lines, "func F() {")
		kinds := []string{"head"}
		for i := 0; i < 1+rng.Intn(6); i++ {
			switch rng.Intn(3) {
			case 0:
				lines, kinds = append(lines, fmt.Sprintf("\t// note %d", i)), append(kinds, "comment")
			case 1:
				lines, kinds = append(lines, codeLine(i)), append(kinds, "code")
			default:
				lines, kinds = append(lines, ""), append(kinds, "blank")
			}
		}
		lines, kinds = append(lines, "}"), append(kinds, "code")
		edited := append([]string(nil), lines...)
		at := 1 + rng.Intn(len(lines)-2)
		var touched string
		switch kinds[at] {
		case "comment":
			edited[at] = fmt.Sprintf("\t// changed %d", trial)
			touched = "comment"
		case "code":
			edited[at] = codeLine(1000 + trial)
			touched = "code"
		default:
			continue
		}
		if edited[at] == lines[at] {
			continue
		}
		old := kind(mk("f-k7m2p4xq", "a.go", 1, len(lines), "F", strings.Join(lines, "\n"), nil), block.KindFunc)
		nw := kind(mk("f-k7m2p4xq", "a.go", 1, len(lines), "F", strings.Join(edited, "\n"), nil), block.KindFunc)
		got := Classify(old, nw, old.Content, prefixes)
		switch touched {
		case "code":
			if !block.Flags(block.StabilityStable, got) {
				t.Fatalf("trial %d: a code edit was classified %v, which a stable block ignores\n--- old\n%s\n--- new\n%s", trial, got, old.Content, nw.Content)
			}
		case "comment":
			if len(got) != 1 || got[0] != block.ClassComment {
				t.Fatalf("trial %d: a comment-only edit was classified %v\n--- old\n%s\n--- new\n%s", trial, got, old.Content, nw.Content)
			}
		}
	}
}

// TestSignatureChangeFlagsAPI: over random parameter edits, a changed
// declaration line of a function always flags an api block — the one change
// api stability exists to report — and a body-only edit never does.
func TestSignatureChangeFlagsAPI(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(11))
	prefixes := []string{"//"}
	params := []string{"", "a int", "a, b int", "ctx context.Context", "s string"}
	for trial := 0; trial < 5000; trial++ {
		p1, p2 := params[rng.Intn(len(params))], params[rng.Intn(len(params))]
		body1, body2 := fmt.Sprintf("\treturn %d", rng.Intn(3)), fmt.Sprintf("\treturn %d", rng.Intn(3))
		comment := ""
		if rng.Intn(2) == 0 {
			comment = "// F does a thing.\n"
		}
		old := kind(mk("f-k7m2p4xq", "a.go", 1, 3, "F", comment+"func F("+p1+") int {\n"+body1+"\n}", nil), block.KindFunc)
		nw := kind(mk("f-k7m2p4xq", "a.go", 1, 3, "F", comment+"func F("+p2+") int {\n"+body2+"\n}", nil), block.KindFunc)
		if old.Hash == nw.Hash {
			continue
		}
		got := Classify(old, nw, old.Content, prefixes)
		if p1 != p2 && !block.Flags(block.StabilityAPI, got) {
			t.Fatalf("signature %q -> %q classified %v, which an api block ignores", p1, p2, got)
		}
		if p1 == p2 && block.Flags(block.StabilityAPI, got) {
			t.Fatalf("a body-only edit classified %v flags an api block", got)
		}
	}
}

// TestCompareAccountsForEveryID is the bookkeeping property of Compare over
// random ledgers and edits: every id on either side gets exactly one change,
// an untouched block is ok, a changed body is changed (never ok), a block
// only on the old side is never reported present, and only a changed block
// may carry classes. A dropped or doubled id is a citation nobody checks.
func TestCompareAccountsForEveryID(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(3))
	for trial := 0; trial < 5000; trial++ {
		var prev []block.Block
		n := 1 + rng.Intn(6)
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("b%d-k7m2p4xq", i)
			prev = append(prev, mk(id, fmt.Sprintf("f%d.go", rng.Intn(2)), 10*i+1, 10*i+3, fmt.Sprintf("F%d", i), fmt.Sprintf("func F%d() {\n\treturn %d\n}", i, i), nil))
		}
		want := map[string]State{}
		var cur []block.Block
		for _, b := range prev {
			switch rng.Intn(5) {
			case 0: // deleted, nothing similar left
				want[b.ID] = ""
			case 1: // body changed in place
				c := mk(b.ID, b.Pos.File, b.Pos.Start, b.Pos.End, b.Symbol, b.Content+"\n// edited", nil)
				cur, want[b.ID] = append(cur, c), StateChanged
			case 2: // moved, same text
				c := mk(b.ID, "moved.go", b.Pos.Start+100, b.Pos.End+100, b.Symbol, b.Content, nil)
				cur, want[b.ID] = append(cur, c), StateMoved
			default: // untouched
				cur, want[b.ID] = append(cur, b), StateOK
			}
		}
		if rng.Intn(2) == 0 {
			nb := mk("fresh-h3v8n2wd", "new.go", 1, 1, "N", "func N() {}", nil)
			cur, want[nb.ID] = append(cur, nb), StateNew
		}
		var rows []ledger.Row
		for _, b := range prev {
			rows = append(rows, row(b))
		}
		seen := map[string]int{}
		for _, c := range Compare(rows, cur, Options{}) {
			seen[c.ID]++
			w := want[c.ID]
			switch {
			case w == "" && (c.State == StateOK || c.State == StateChanged || c.State == StateMoved):
				t.Fatalf("trial %d: deleted %s reported %s", trial, c.ID, c.State)
			case w != "" && c.State != w:
				t.Fatalf("trial %d: %s is %s, want %s", trial, c.ID, c.State, w)
			case c.State != StateChanged && len(c.Classes) > 0:
				t.Fatalf("trial %d: %s is %s but carries classes %v", trial, c.ID, c.State, c.Classes)
			}
		}
		for id := range want {
			if seen[id] != 1 {
				t.Fatalf("trial %d: %s reported %d times, want exactly once", trial, id, seen[id])
			}
		}
	}
}

// TestCompareKeysByEnvironment pins that one id defined per environment is
// one def per environment. Compare keyed by id alone compared whichever of
// each came last: listing prod before dev — what renaming a file does —
// reported a change nothing made, and a real change to one environment
// could be read against another's row.
func TestCompareKeysByEnvironment(t *testing.T) {
	t.Parallel()
	mk := func(env, file, content string) block.Block {
		b := block.Block{ID: "port-k7m2p4xq", Kind: block.KindKey, Pos: block.Position{File: file, Start: 1, End: 1}, Args: map[string]string{"id": "port-k7m2p4xq", "env": env}}
		b.SetContent(content)
		return b
	}
	dev, prod := mk("dev", "config/dev.yaml", "8080"), mk("prod", "config/prod.yaml", "443")
	prev := []ledger.Row{ledger.FromBlock("r", dev), ledger.FromBlock("r", prod)}
	states := func(cs []Change) string {
		var s []string
		for _, c := range cs {
			s = append(s, c.Env()+"="+string(c.State))
		}
		return strings.Join(s, " ")
	}
	// The body hook is handed the exact previous row, so each environment
	// is answered with its own body.
	bodies := map[string]string{"dev": "8080", "prod": "443"}
	var asked []string
	opts := Options{OldContent: func(r ledger.Row) (string, bool) { asked = append(asked, r.Env); return bodies[r.Env], true }}
	for name, tc := range map[string]struct {
		cur  []block.Block
		want string
	}{
		"reordered":       {[]block.Block{prod, dev}, "dev=ok prod=ok"},
		"prod changed":    {[]block.Block{dev, mk("prod", "config/prod.yaml", "8443")}, "dev=ok prod=changed"},
		"prod removed":    {[]block.Block{dev}, "dev=ok prod=deleted"},
		"staging added":   {[]block.Block{dev, prod, mk("staging", "config/s.yaml", "9000")}, "dev=ok prod=ok staging=new"},
		"prod file moved": {[]block.Block{dev, mk("prod", "config/a-prod.yaml", "443")}, "dev=ok prod=moved"},
	} {
		if got := states(Compare(prev, tc.cur, opts)); got != tc.want {
			t.Errorf("%s: %s, want %s", name, got, tc.want)
		}
	}
	// A change to prod asks for prod's body, never dev's, and gets its own
	// diff. With an id-keyed hook it had to go without one.
	asked = nil
	cs := Compare(prev, []block.Block{dev, mk("prod", "config/prod.yaml", "8443")}, opts)
	var prodChange Change
	for _, c := range cs {
		if c.Env() == "prod" {
			prodChange = c
		}
	}
	if strings.Join(asked, ",") != "prod" || !strings.Contains(prodChange.Diff, "-443") || !strings.Contains(prodChange.Diff, "+8443") {
		t.Errorf("asked %v; prod diff %q", asked, prodChange.Diff)
	}
	single := []ledger.Row{ledger.FromBlock("r", mk("", "config/p.yaml", "1"))}
	cs = Compare(single, []block.Block{mk("", "config/p.yaml", "2")}, Options{OldContent: func(ledger.Row) (string, bool) { return "1", true }})
	if len(cs) != 1 || cs[0].State != StateChanged || cs[0].Diff == "" {
		t.Errorf("a single-row id still gets its diff: %+v", cs)
	}
}

// TestKeysSeparateEnvironmentsAndBranches pins what identifies a def: a
// block and its ledger row agree, and id, environment, and branch each tell
// defs apart. A published branch compared as main made main look changed.
func TestKeysSeparateEnvironmentsAndBranches(t *testing.T) {
	t.Parallel()
	mk := func(id string, args map[string]string) block.Block {
		a := map[string]string{"id": id}
		for k, v := range args {
			a[k] = v
		}
		b := block.Block{ID: id, Kind: block.KindKey, Pos: block.Position{File: "f", Start: 1, End: 1}, Args: a}
		b.SetContent("x")
		return b
	}
	defs := []block.Block{
		mk("port-k7m2p4xq", nil),
		mk("port-k7m2p4xq", map[string]string{"env": "prod"}),
		mk("port-k7m2p4xq", map[string]string{"branch": "feature"}),
		mk("port-k7m2p4xq", map[string]string{"env": "prod", "branch": "feature"}),
		mk("host-h3v8n2wd", nil),
	}
	seen := map[string]bool{}
	for _, b := range defs {
		k := BlockKey(b)
		if seen[k] {
			t.Errorf("%v collides", b.Args)
		}
		seen[k] = true
		if RowKey(ledger.FromBlock("r", b)) != k {
			t.Errorf("block and row disagree for %v", b.Args)
		}
		if (Change{New: b}).Key() != k || (Change{Old: ledger.FromBlock("r", b)}).Key() != k {
			t.Errorf("a change's key must be its def's for %v", b.Args)
		}
	}
	// main and a branch of one id are two defs, so reordering them is not a change.
	prev := []ledger.Row{ledger.FromBlock("r", defs[0]), ledger.FromBlock("r", defs[2])}
	for _, c := range Compare(prev, []block.Block{defs[2], defs[0]}, Options{}) {
		if c.State != StateOK {
			t.Errorf("reordered main and branch: %s %s", c.Key(), c.State)
		}
	}
}

// TestVanishedPerEnvironmentDefMatchesAsRewritten pins the other thing the
// row-based body hook gives back: a per-environment def whose directive was
// dropped while its block was rewritten can be matched by content, because
// the hook can now say what that environment's block used to be.
func TestVanishedPerEnvironmentDefMatchesAsRewritten(t *testing.T) {
	t.Parallel()
	mk := func(id, env, file, content string) block.Block {
		args := map[string]string{"id": id}
		if env != "" {
			args["env"] = env
		}
		b := block.Block{ID: id, Kind: block.KindFunc, Pos: block.Position{File: file, Start: 1, End: 3}, Args: args}
		b.SetContent(content)
		return b
	}
	body := "func Serve() {\n\tlisten(443)\n\tlog()\n\treturn\n}"
	dev := mk("serve-k7m2p4xq", "dev", "dev.go", "func Serve() {\n\tlisten(8080)\n}")
	prod := mk("serve-k7m2p4xq", "prod", "prod.go", body)
	prev := []ledger.Row{ledger.FromBlock("r", dev), ledger.FromBlock("r", prod)}
	// prod's directive is gone; an unmarked-looking new block carries a
	// slightly edited body under a new id.
	rewritten := mk("serve-new-h3v8n2wd", "", "prod.go", strings.Replace(body, "log()", "log(1)", 1))
	bodies := map[string]string{"dev": dev.Content, "prod": prod.Content}
	cs := Compare(prev, []block.Block{dev, rewritten}, Options{FuzzyThreshold: 0.5, OldContent: func(r ledger.Row) (string, bool) { return bodies[r.Env], true }})
	found := false
	for _, c := range cs {
		if c.ID == prod.ID && c.Env() == "prod" {
			found = true
			if c.State != StateRewritten || c.Candidate.ID != rewritten.ID {
				t.Errorf("prod vanished and should match as rewritten: %+v", c)
			}
		}
	}
	if !found {
		t.Errorf("no change reported for prod: %+v", cs)
	}
}
