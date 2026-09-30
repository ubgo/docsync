package docsync

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/ledger"
)

// TestInvariantLedgerByteIdentical pins §37.6: scanning an unchanged tree
// twice encodes the same ledger and refs byte for byte.
func TestInvariantLedgerByteIdentical(t *testing.T) {
	t.Parallel()
	s := newSys(t, repo(false))
	ctx := context.Background()
	res1, _ := s.Scan(ctx)
	res2, _ := s.Scan(ctx)
	l1, r1 := s.Snapshot(res1)
	l2, r2 := s.Snapshot(res2)
	if !bytes.Equal(l1.Bytes(), l2.Bytes()) || !bytes.Equal(r1.Bytes(), r2.Bytes()) {
		t.Error("ledger or refs differ between identical scans")
	}
}

// TestInvariantCheckTwice pins §37.6: a first check, then a check with that
// snapshot as previous, reports every reference as ok (references that
// were broken before stay broken; that is not a change).
func TestInvariantCheckTwice(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	delete(fsys, "docs/sessions.md")
	fsys["docs/clean.md"] = &fstest.MapFile{Data: []byte("Write [save](ds:block?id=sess-save-k7m2p4xq). Port [8081](ds:cfg?id=auth-port-h3v8n2wd).\n")}
	first := newSys(t, fsys)
	rep1, err := first.Check(context.Background(), CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prev, prevRefs := first.Snapshot(rep1.Scan)
	rep2, _ := newSys(t, fsys, WithPrevious(prev, prevRefs)).Check(context.Background(), CheckOptions{})
	for _, f := range rep2.Findings {
		if f.Verb != "" && f.State != check.StateOK {
			t.Errorf("second check is not ok: %+v", f)
		}
	}
}

// TestInvariantRootPurity pins §37.1 and §37.6: no non-test file in the
// root module imports the network, reads the environment, or reads the
// home directory. procplugin is the sanctioned exception for os/exec, the
// process-plugin mechanism itself.
func TestInvariantRootPurity(t *testing.T) {
	t.Parallel()
	forbiddenImports := map[string]bool{"net/http": true, "net": true, "net/url": false, "os/user": true}
	forbiddenCalls := []string{"os.Getenv(", "os.LookupEnv(", "os.Environ(", "os.UserHomeDir(", "os.ExpandEnv(", "http.Get(", "net.Dial("}
	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "cli", "ext", "testdata", ".git", "coverage", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, src, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, "\"")
			if forbiddenImports[path] {
				t.Errorf("%s imports %s", p, path)
			}
			// WalkDir yields the platform's separator; on Windows the file is
			// procplugin\procplugin.go and a "/" prefix never matched it.
			if path == "os/exec" && !strings.HasPrefix(filepath.ToSlash(p), "procplugin/") {
				t.Errorf("%s imports os/exec outside procplugin", p)
			}
		}
		for _, call := range forbiddenCalls {
			if strings.Contains(string(src), call) {
				t.Errorf("%s calls %s", p, call)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestInvariantScanFollowsMovedCitations pins the scan half of the move
// rule, with the real current-hash lookup Snapshot feeds it: a citation
// that moves keeps the baseline it was first seen at, and when a move is
// ambiguous every candidate takes the baseline that still reports a
// change. Before this, the scan after adding one line above a citation
// wrote a fresh baseline and silently accepted the change.
func TestInvariantScanFollowsMovedCitations(t *testing.T) {
	t.Parallel()
	const (
		id      = "sess-save-k7m2p4xq"
		oldSeen = "hash-from-before-the-change"
	)
	code := "package store\n\n// ds:def id=" + id + "\nfunc (s *Store) Save() error { return nil }\n"
	cite := "<!-- ds:block id=" + id + " -->\nEvery write goes through Save.\n"
	prevRow := func(line int, seen string) ledger.RefRow {
		return ledger.RefRow{ID: id, Repo: "api", Doc: "docs/d.md", Line: line, Verb: "block", Carrier: block.CarrierBlock, SeenHash: seen}
	}
	scanSeen := func(t *testing.T, doc string, prev ...ledger.RefRow) map[int]string {
		t.Helper()
		fsys := fstest.MapFS{"internal/store.go": {Data: []byte(code)}, "docs/d.md": {Data: []byte(doc)}}
		s := newSys(t, fsys, WithPrevious(ledger.Ledger{}, ledger.Refs{Rows: prev}))
		res, err := s.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, refs := s.Snapshot(res)
		seen := map[int]string{}
		for _, r := range refs.Rows {
			if r.ID == id {
				seen[r.Line] = r.SeenHash
			}
		}
		return seen
	}
	current := func(t *testing.T) string {
		t.Helper()
		s := newSys(t, fstest.MapFS{"internal/store.go": {Data: []byte(code)}})
		res, err := s.Scan(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range res.Defs {
			if d.ID == id {
				return d.Hash
			}
		}
		t.Fatal("no def")
		return ""
	}(t)

	t.Run("one citation shifted keeps its first-seen hash", func(t *testing.T) {
		t.Parallel()
		got := scanSeen(t, "# D\n\nIntro.\n\n"+cite, prevRow(3, oldSeen))
		if got[5] != oldSeen {
			t.Errorf("seen at line 5 = %q, want the carried %q", got[5], oldSeen)
		}
	})
	t.Run("ambiguous: every candidate takes the baseline that reports", func(t *testing.T) {
		t.Parallel()
		// Two became three; one was reviewed at the current hash, one not.
		got := scanSeen(t, "# D\n\npad\n\n"+cite+"\n"+cite+"\n"+cite, prevRow(3, current), prevRow(6, oldSeen))
		for _, line := range []int{5, 8, 11} {
			if got[line] != oldSeen {
				t.Errorf("seen at line %d = %q, want %q: an unreviewed change must not be dropped", line, got[line], oldSeen)
			}
		}
	})
}

// TestInvariantScanKeepsUnreadableFiles pins the scan half of the unreadable
// rule: a file that could not be read keeps the ledger and refs rows the
// last scan wrote for it, so its citations' first-seen hashes survive until
// it is readable again. Dropping them let an unreviewed change pass as new.
func TestInvariantScanKeepsUnreadableFiles(t *testing.T) {
	t.Parallel()
	prevDef := ledger.Row{ID: "gone-k7m2p4xq", Repo: "api", File: "internal/big.go", Start: 1, End: 1, Hash: "h-def"}
	prevRef := ledger.RefRow{ID: "gone-k7m2p4xq", Repo: "api", Doc: "docs/big.md", Line: 3, Verb: "block", SeenHash: "h-seen"}
	huge := strings.Repeat("x", 600*1024)
	fsys := fstest.MapFS{
		"internal/big.go": {Data: []byte(huge)},
		"docs/big.md":     {Data: []byte(huge)},
	}
	s := newSys(t, fsys, WithPrevious(ledger.Ledger{Rows: []ledger.Row{prevDef}}, ledger.Refs{Rows: []ledger.RefRow{prevRef}}))
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	l, r := s.Snapshot(res)
	if len(l.Rows) != 1 || !reflect.DeepEqual(l.Rows[0], prevDef) {
		t.Errorf("ledger = %+v, want the unreadable file's row kept", l.Rows)
	}
	if len(r.Rows) != 1 || r.Rows[0].SeenHash != "h-seen" || r.Rows[0].Doc != "docs/big.md" {
		t.Errorf("refs = %+v, want the unreadable doc's row kept with its first-seen hash", r.Rows)
	}
	rep, err := s.Check(context.Background(), CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(states(rep)[check.StateUnscanned]); got != 2 {
		t.Errorf("unscanned findings = %d, want 2", got)
	}
}

// TestInvariantPerEnvCitationsRecordTheirOwnDef pins that scan records each
// citation against the def its env resolves to, the way check measures it.
// Every citation of an id used to be recorded against whichever def came
// last, so a dev citation carried the prod value's hash and a fresh repo
// with per-env defs reported changes before anything had changed.
func TestInvariantPerEnvCitationsRecordTheirOwnDef(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"config/dev.yaml":  {Data: []byte("port: 8080 # ds:def id=port-k7m2p4xq env=dev\n")},
		"config/prod.yaml": {Data: []byte("port: 443 # ds:def id=port-k7m2p4xq env=prod\n")},
		"docs/d.md":        {Data: []byte("Prod [443](ds:cfg?id=port-k7m2p4xq&env=prod).\n\nDev [8080](ds:cfg?id=port-k7m2p4xq&env=dev).\n")},
	}
	c := cfg()
	c.Scan.Code = []string{"config/**"}
	s := newSys(t, fsys, WithConfig(c))
	ctx := context.Background()
	res, err := s.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byEnv := map[string]string{}
	for _, d := range res.Defs {
		byEnv[d.Env()] = d.Hash
	}
	_, refs := s.Snapshot(res)
	for _, r := range refs.Rows {
		if r.SeenHash != byEnv[r.Env] {
			t.Errorf("citation for env %q recorded %.8s, want its own def's %.8s", r.Env, r.SeenHash, byEnv[r.Env])
		}
	}
	// And a check straight after the scan is clean.
	l, rr := s.Snapshot(res)
	rep, err := newSys(t, fsys, WithConfig(c), WithPrevious(l, rr)).Check(ctx, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ExitCode != 0 {
		t.Errorf("a fresh repo with per-env defs reports %v", rep.States)
	}
}

// TestInvariantAckTakesTheCitationsEnv pins that an ack of a per-env
// citation records that env and the hash of that env's def, which is the key
// and value check reads it by. It used to take the request's empty env and
// the first def's hash, so a prod citation could never be acked.
func TestInvariantAckTakesTheCitationsEnv(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"config/dev.yaml":  {Data: []byte("port: 8080 # ds:def id=port-k7m2p4xq env=dev\n")},
		"config/prod.yaml": {Data: []byte("port: 443 # ds:def id=port-k7m2p4xq env=prod\n")},
		"docs/d.md":        {Data: []byte("Prod [443](ds:cfg?id=port-k7m2p4xq&env=prod).\n")},
	}
	c := cfg()
	c.Scan.Code = []string{"config/**"}
	s := newSys(t, fsys, WithConfig(c))
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var prod string
	for _, d := range res.Defs {
		if d.Env() == "prod" {
			prod = d.Hash
		}
	}
	a, err := s.Ack(res, AckRequest{ID: "port-k7m2p4xq", Doc: "docs/d.md", Line: 1, Actor: "k"})
	if err != nil || a.Env != "prod" || a.BlockHash != prod {
		t.Errorf("ack = env %q hash %.8s (%v), want env prod and %.8s", a.Env, a.BlockHash, err, prod)
	}
}

// TestInvariantNoBaselineWhileAnIDIsDefinedTwice pins the ambiguity rule: a
// citation of an id defined twice records no first-seen hash, because that
// hash is never revised and the accidental copy's would stay the baseline
// after the copy was deleted. The first scan after the id is unique again
// records the real one.
func TestInvariantNoBaselineWhileAnIDIsDefinedTwice(t *testing.T) {
	t.Parallel()
	real := "package p\n\n// ds:def id=save-k7m2p4xq\nfunc Save() int { return 1 }\n"
	fsys := fstest.MapFS{
		"internal/a.go":   {Data: []byte(real)},
		"internal/dup.go": {Data: []byte("package p\n\n// ds:def id=save-k7m2p4xq\nvar dup = 1\n")},
		"docs/d.md":       {Data: []byte("Save [x](ds:block?id=save-k7m2p4xq).\n")},
	}
	ctx := context.Background()
	s := newSys(t, fsys)
	res, err := s.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, refs := s.Snapshot(res)
	if len(refs.Rows) != 1 || refs.Rows[0].SeenHash != "" {
		t.Fatalf("a citation of a duplicated id recorded %+v, want no first-seen hash", refs.Rows)
	}
	delete(fsys, "internal/dup.go")
	s2 := newSys(t, fsys, WithPrevious(ledger.Ledger{}, refs))
	res2, err := s2.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, refs2 := s2.Snapshot(res2)
	var want string
	for _, d := range res2.Defs {
		want = d.Hash
	}
	if refs2.Rows[0].SeenHash != want {
		t.Errorf("once the id is unique the real hash is recorded: got %.8s want %.8s", refs2.Rows[0].SeenHash, want)
	}
}

// fuzzListRE finds the `for:` list of `task fuzz:all` in Taskfile.yml.
var fuzzListRE = regexp.MustCompile(`(?m)^\s*- for: \[([^\]]*)\]\s*\n\s*cmd: go test \./\{\{splitList ":" \.ITEM \| first\}\}/ -run '\^\$' -fuzz`)

// TestInvariantFuzzAllListsEveryTarget pins that `task fuzz:all` searches
// every fuzz target in the root module. The list is written by hand, and a
// target left off it is never searched: its seeds run under go test and
// look like coverage while nothing ever looks for a new failing input.
// Sub-modules are skipped because their targets cannot run from the root
// module's go test; none has a fuzz target today.
func TestInvariantFuzzAllListsEveryTarget(t *testing.T) {
	t.Parallel()
	taskfile, err := os.ReadFile("Taskfile.yml")
	if err != nil {
		t.Fatal(err)
	}
	m := fuzzListRE.FindSubmatch(taskfile)
	if m == nil {
		t.Fatal("Taskfile.yml has no fuzz:all for-list in the expected shape")
	}
	listed := map[string]bool{}
	for _, item := range strings.Split(string(m[1]), ",") {
		listed[strings.TrimSpace(item)] = true
	}
	found := map[string]bool{}
	err = filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || isModule(p)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Fuzz") {
				found[filepath.ToSlash(filepath.Dir(p))+":"+fn.Name.Name] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("found no fuzz targets; the walk is broken")
	}
	for k := range found {
		if !listed[k] {
			t.Errorf("%s is not in the fuzz:all list in Taskfile.yml", k)
		}
	}
	for k := range listed {
		if !found[k] {
			t.Errorf("fuzz:all lists %s, which does not exist", k)
		}
	}
}

// isModule reports whether dir holds its own go.mod, so is not the root
// module's code.
func isModule(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil
}
