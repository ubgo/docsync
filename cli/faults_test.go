package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The fault-injection matrix: every input `ds` reads, broken every way it
// can be broken, under every command that reads it. It exists because bug
// 147 was one cell of it -- a missing index made every command die with
// exit 2 -- and a class of failure is only closed when every cell is
// checked, not the one a user reported.
//
// The invariant in every cell is the same: the command either succeeds
// (with findings or without) having said what it could not read, or stops
// with an error that names what is wrong. Never a panic, never an exit 2
// with no reason, and never a silent green: exit 0 with nothing on stderr
// while an input it reads is broken, unless the table below says why that
// input being in that state is a normal state for that command.

// faultInput is one thing ds reads (axis 1).
type faultInput struct {
	name string
	// path is relative to the fixture root: the index lives beside the
	// repository, everything else under the citing repository's .ds/.
	path string
	dir  bool
	// corrupt writes a broken version of the input under root.
	corrupt func(t *testing.T, root string)
	// mentions are the words a warning or an error about this input must
	// contain, so a message about something else does not count.
	mentions []string
}

// fault is one way an input breaks (axis 2).
type fault string

const (
	faultMissing    fault = "missing"
	faultEmpty      fault = "empty"
	faultCorrupt    fault = "corrupt"
	faultUnreadable fault = "unreadable"
)

// faultValues lists every fault, in table order.
var faultValues = []fault{faultMissing, faultEmpty, faultCorrupt, faultUnreadable}

// faultCommands are the commands every cell runs (axis 3), from the citing
// repository.
var faultCommands = [][]string{
	{"scan"}, {"check"}, {"check", "--strict"}, {"check", "--frozen"}, {"doctor"}, {"refresh"}, {"sync"},
}

// faultFixture builds two repositories and a local index beside them: api
// defines `limit` and publishes it; docs defines `max`, cites both, has
// synced (foreign.tsv), scanned (ledger, refs, block store), drifted `max`
// and acked it (acks). Every input in the matrix therefore exists and
// matters before a fault is injected.
func faultFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	idx := filepath.Join(root, "index")
	write(t, idx, "repos/.keep", "")
	api, docs := filepath.Join(root, "api"), filepath.Join(root, "docs")
	v := fakeVCS{head: "a1", branch: "main", files: map[string][]byte{}}
	cfg := "workspace = \"../index\"\n[scan]\ncode = [\"*.go\"]\ndocs = [\"docs/**\"]\n"
	must := func(dir string, args ...string) string {
		t.Helper()
		r := run(t, dir, v, args...)
		if r.code != 0 {
			t.Fatalf("fixture: %v in %s = %+v", args, filepath.Base(dir), r)
		}
		return r.out
	}
	idOf := func(dir, file string) string {
		raw, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		return regexp.MustCompile(`id=([a-z0-9-]+)`).FindStringSubmatch(string(raw))[1]
	}
	// init first, so doctor's .gitignore and .gitattributes rows are ok and
	// a WARN row that mentions an input is about that input.
	must(api, "init")
	must(docs, "init")
	write(t, api, ".ds/config.toml", cfg)
	write(t, api, "a.go", "package api\n\nconst Limit = 5\n")
	must(api, "def", "a.go#Limit", "--label", "limit")
	must(api, "scan")
	must(api, "publish")
	write(t, docs, ".ds/config.toml", cfg)
	write(t, docs, "b.go", "package docs\n\nconst Max = 9\n")
	must(docs, "def", "b.go#Max", "--label", "max")
	foreign, local := idOf(api, "a.go"), idOf(docs, "b.go")
	write(t, docs, "docs/a.md", "# A\n\n<!-- ds:block id="+foreign+" -->\nLimit is 5.\n\n<!-- ds:block id="+local+" -->\nMax is 9.\n")
	must(docs, "scan")
	must(docs, "sync")
	raw, _ := os.ReadFile(filepath.Join(docs, "b.go"))
	write(t, docs, "b.go", strings.Replace(string(raw), "= 9", "= 10", 1))
	must(docs, "ack", local, "--doc", "docs/a.md", "--line", "6", "--note", "still right")
	must(docs, "check")
	return root
}

// faultInputs is axis 1.
func faultInputs() []faultInput {
	garbage := func(rel string) func(*testing.T, string) {
		return func(t *testing.T, root string) { write(t, root, rel, "\x00not\tthe\tformat\x01\n") }
	}
	return []faultInput{
		{name: "index", path: "index", dir: true, mentions: []string{"index"}, corrupt: func(t *testing.T, root string) {
			write(t, root, "index/repos/api/ledger.tsv", "\x00not\tthe\tformat\x01\n")
		}},
		{name: "foreign.tsv", path: "docs/.ds/foreign.tsv", mentions: []string{"foreign.tsv"}, corrupt: garbage("docs/.ds/foreign.tsv")},
		{name: "ledger.tsv", path: "docs/.ds/ledger.tsv", mentions: []string{"ledger.tsv"}, corrupt: garbage("docs/.ds/ledger.tsv")},
		{name: "refs.tsv", path: "docs/.ds/refs.tsv", mentions: []string{"refs.tsv"}, corrupt: garbage("docs/.ds/refs.tsv")},
		{name: "config.toml", path: "docs/.ds/config.toml", mentions: []string{"config"}, corrupt: garbage("docs/.ds/config.toml")},
		{name: "blocks", path: "docs/.ds/blocks", dir: true, mentions: []string{"block"}, corrupt: func(t *testing.T, root string) {
			entries, err := os.ReadDir(filepath.Join(root, "docs/.ds/blocks"))
			if err != nil || len(entries) == 0 {
				t.Fatalf("fixture has no block bodies: %v", err)
			}
			for _, e := range entries {
				write(t, root, "docs/.ds/blocks/"+e.Name(), "not the body this hash names\n")
			}
		}},
		{name: "acks.tsv", path: "docs/.ds/acks.tsv", mentions: []string{"acks"}, corrupt: garbage("docs/.ds/acks.tsv")},
	}
}

// faultInputNamed is one row of axis 1.
func faultInputNamed(t *testing.T, name string) faultInput {
	t.Helper()
	for _, in := range faultInputs() {
		if in.name == name {
			return in
		}
	}
	t.Fatalf("no fault input %s", name)
	return faultInput{}
}

// inject breaks one input of a fixture copy.
func inject(t *testing.T, root string, in faultInput, f fault) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(in.path))
	switch f {
	case faultMissing:
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
	case faultEmpty:
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
		if in.dir {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	case faultCorrupt:
		in.corrupt(t, root)
	case faultUnreadable:
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(p, 0o755) })
	}
}

// copyTree copies a fixture so each cell starts from the same state.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// cellOutcome runs one command and recovers a panic into the result, so
// one broken cell is reported as a cell rather than ending the matrix.
func cellOutcome(t *testing.T, dir string, args []string) (r result, panicked any) {
	t.Helper()
	defer func() { panicked = recover() }()
	return run(t, dir, fakeVCS{head: "a1", branch: "main", files: map[string][]byte{}}, args...), nil
}

// remedyMarkers are the phrases that make an error actionable: a command
// to run, a file to restore, a path to create or point at, a permission to
// fix. An error with none of them names a problem and leaves the user there.
var remedyMarkers = []string{"run `", "restore it", "create ", "point `", "permission denied", "republish"}

// benignFault says why an input in this state is a normal state for this
// command, so finishing green without a word about it is correct. Every
// other faulted cell that exits 0 must have said what it could not read.
func benignFault(in string, f fault, cmd string) (string, bool) {
	switch {
	case in == "index" && cmd == "check --frozen":
		return "--frozen resolves from foreign.tsv and reads the index only for bodies, whose absence classifies a drift as `unknown`", true
	case in == "index" && f == faultEmpty:
		return "an index that publishes nothing is valid; a citation into it is reported broken, never passed", true
	case in == "foreign.tsv" && f == faultMissing && cmd != "check --frozen":
		return "a repository that never synced: commands that are not frozen resolve from the live index", true
	case (in == "ledger.tsv" || in == "refs.tsv" || in == "acks.tsv") && f == faultMissing:
		return "the state before the first scan or ack, which a deletion cannot be told apart from; review shows the deleted committed file", true
	case in == "blocks":
		return "bodies are read only to classify a change, and a missing or wrong one classifies it as `unknown` (§20.1); nothing in the fixture changed", true
	}
	return "", false
}

// configNamesItsFix exempts config errors from remedyMarkers: they name the
// key or the line to change, which is the remedy.
func configNamesItsFix(in string) bool { return in == "config.toml" }

// TestFaultMatrix is the class test for bug 147. It found
// bugs 153, 154, 155 and 156: an unreadable index entry that stopped every
// consumer and did not say it was in the index; an unreadable foreign.tsv
// ignored in silence by every command but the CI default; sync refusing to
// rewrite the snapshot it exists to write; and .ds/ files that do not
// parse, named with no way out. See the top of this file.
func TestFaultMatrix(t *testing.T) {
	t.Parallel()
	base := faultFixture(t)
	// Permission bits are not portable: Windows ignores them, and root reads
	// through them.
	unreadable := runtime.GOOS != "windows" && os.Geteuid() != 0
	cells := 0
	for _, in := range faultInputs() {
		for _, f := range faultValues {
			if f == faultUnreadable && !unreadable {
				continue
			}
			for _, args := range faultCommands {
				cells++
				cmd := strings.Join(args, " ")
				root := t.TempDir()
				copyTree(t, base, root)
				inject(t, root, in, f)
				r, p := cellOutcome(t, filepath.Join(root, "docs"), args)
				name := fmt.Sprintf("%s %s / %s", in.name, f, cmd)
				if p != nil {
					t.Errorf("%s: panic: %v", name, p)
					continue
				}
				// What the run said about its inputs: stderr, and for doctor
				// the rows that are not ok.
				said := r.err
				if args[0] == "doctor" {
					for _, l := range strings.Split(r.out, "\n") {
						if strings.Contains(l, doctorWarn) || strings.Contains(l, doctorFail) {
							said += l + "\n"
						}
					}
				}
				named := false
				for _, m := range in.mentions {
					named = named || strings.Contains(said, m)
				}
				switch {
				case r.code == ExitError:
					if !named {
						t.Errorf("%s: exit 2 without naming %s: %q", name, in.name, said)
						continue
					}
					actionable := configNamesItsFix(in.name)
					for _, m := range remedyMarkers {
						actionable = actionable || strings.Contains(said, m)
					}
					if !actionable {
						t.Errorf("%s: exit 2 with no remedy: %q", name, said)
					}
				case r.code == ExitFindings || named:
					// Findings are not green; a named warning is not silent.
				default:
					if _, ok := benignFault(in.name, f, cmd); !ok {
						t.Errorf("%s: silent green: exit %d, nothing said about %s\nstdout: %s", name, r.code, in.name, r.out)
					}
				}
			}
		}
	}
	if min := len(faultInputs()) * 3 * len(faultCommands); cells < min {
		t.Errorf("the matrix ran %d cells, fewer than the %d it has without permissions", cells, min)
	}
}

// TestSyncReplacesBrokenSnapshot pins bug 155 beyond what the matrix can
// see: once every .ds/ error carries a remedy, a sync that still refused a
// corrupt foreign.tsv would pass the matrix with "run `ds sync`" -- the
// command that just failed. Sync must replace the file and succeed.
func TestSyncReplacesBrokenSnapshot(t *testing.T) {
	t.Parallel()
	root := faultFixture(t)
	docs := filepath.Join(root, "docs")
	v := fakeVCS{head: "a1", branch: "main", files: map[string][]byte{}}
	for _, f := range []fault{faultEmpty, faultCorrupt} {
		inject(t, root, faultInputNamed(t, "foreign.tsv"), f)
		r := run(t, docs, v, "sync")
		if r.code != 0 || !strings.Contains(r.err, "replacing it") || strings.Contains(r.err, "run `ds sync`") {
			t.Errorf("sync over a %s foreign.tsv = %+v", f, r)
		}
		if r := run(t, docs, v, "check", "--frozen"); r.code != 0 {
			t.Errorf("frozen check after sync replaced a %s snapshot = %+v", f, r)
		}
	}
}

// TestDoctorFailsOnBrokenSnapshot pins bug 154's doctor half: foreign.tsv is
// committed state like the ledger, and `check --frozen`, the default in CI,
// stops on it, so doctor's ledger row fails rather than leaving the only
// mention to the blocks row's note about pruning.
func TestDoctorFailsOnBrokenSnapshot(t *testing.T) {
	t.Parallel()
	root := faultFixture(t)
	inject(t, root, faultInputNamed(t, "foreign.tsv"), faultCorrupt)
	r := run(t, filepath.Join(root, "docs"), fakeVCS{head: "a1", branch: "main", files: map[string][]byte{}}, "doctor")
	if !regexp.MustCompile(`(?m)^ledger\s+FAIL\s+foreign\.tsv: .*run ` + "`ds sync`").MatchString(r.out) {
		t.Errorf("doctor with a corrupt foreign.tsv:\n%s", r.out)
	}
}
