package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/config"
)

// TestOrganisationDefaultsReachEveryLoad pins WithConfigDefaults: an
// organisation-wide binary's defaults apply to every repository whose config
// does not say otherwise. They used to reach only the few keys init writes,
// so an agent cap of 7 ran as 20 and the organisation's owners vanished.
func TestOrganisationDefaultsReachEveryLoad(t *testing.T) {
	t.Parallel()
	org := WithConfigDefaults(func(c *config.Config) {
		c.Agents.MaxDefsPerRun = 7
		c.Owners["@core"] = []string{"alice"}
		c.Scan.MaxLineChars = 5000
	})
	dir := t.TempDir()
	v := fakeVCS{head: "abc1234", files: map[string][]byte{}}
	if code := Run([]string{"init"}, WithDir(dir), WithVCS(v), org, WithIO(strings.NewReader(""), &strings.Builder{}, &strings.Builder{})); code != 0 {
		t.Fatalf("init = %d", code)
	}
	a := &App{dir: dir}
	org(a)
	cfg, err := a.loadConfig(NewStore(dir))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents.MaxDefsPerRun != 7 || !reflect.DeepEqual(cfg.Owners["@core"], []string{"alice"}) || cfg.Scan.MaxLineChars != 5000 {
		t.Errorf("loaded max_defs=%d owners=%v max_line_chars=%d, want the organisation's 7, alice, 5000", cfg.Agents.MaxDefsPerRun, cfg.Owners, cfg.Scan.MaxLineChars)
	}
	// What the repository's file sets still wins.
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[agents]\nmax_defs_per_run = 3\n")
	cfg, err = a.loadConfig(NewStore(dir))
	if err != nil || cfg.Agents.MaxDefsPerRun != 3 {
		t.Errorf("a repository's own value must win: %d %v", cfg.Agents.MaxDefsPerRun, err)
	}
	// init's scan globs are what it writes, not a fallback: a hand-written
	// config that leaves out `exclude` keeps the built-in default.
	if !reflect.DeepEqual(cfg.Scan.Exclude, config.Default().Scan.Exclude) {
		t.Errorf("exclude fell back to init's list: %v", cfg.Scan.Exclude)
	}
}

// TestConfigRoundTrip pins that what init writes reads back as the same
// config, including values that need escaping, so a repository never starts
// out running a config different from the one it was given.
func TestConfigRoundTrip(t *testing.T) {
	t.Parallel()
	a := &App{}
	for _, c := range []config.Config{
		a.defaults(),
		func() config.Config {
			c := config.Default()
			c.Scan.Code = []string{`src/**`, `a "quoted" dir/**`, `back\slash/**`, "ü/**"}
			c.Scan.Docs = []string{"docs with space/**"}
			c.Check.Permalink = `https://g/{sha}/{file}#L{start}-L{end}`
			c.Env.Default = "prod"
			return c
		}(),
	} {
		got, err := config.Parse(strings.NewReader(ConfigTOML(c)))
		if err != nil {
			t.Fatalf("%v\n%s", err, ConfigTOML(c))
		}
		for _, f := range []struct {
			name      string
			got, want any
		}{
			{"spec", got.Spec, c.Spec},
			{"prefix", got.Prefix, c.Prefix},
			{"code", got.Scan.Code, c.Scan.Code},
			{"docs", got.Scan.Docs, c.Scan.Docs},
			{"exclude", got.Scan.Exclude, c.Scan.Exclude},
			{"generated", got.Scan.Generated, c.Scan.Generated},
			{"mode", got.Include.Mode, c.Include.Mode},
			{"max_lines", got.Include.MaxLines, c.Include.MaxLines},
			{"fuzzy", got.Check.FuzzyThreshold, c.Check.FuzzyThreshold},
			{"unacked", got.Check.Unacked, c.Check.Unacked},
			{"permalink", got.Check.Permalink, c.Check.Permalink},
			{"env", got.Env.Default, c.Env.Default},
		} {
			if !reflect.DeepEqual(emptyAsNil(f.got), emptyAsNil(f.want)) {
				t.Errorf("%s: wrote %v, read back %v\n%s", f.name, f.want, f.got, ConfigTOML(c))
			}
		}
	}
}

// emptyAsNil treats an empty list and no list as the same value: `[]` in a
// file and an unset key mean the same thing to every reader.
func emptyAsNil(v any) any {
	if s, ok := v.([]string); ok && len(s) == 0 {
		return nil
	}
	return v
}

// TestAckRenewsAClaim pins the command an expired claim's remedy prints:
// `ds ack --doc D --line N` with no id renews the claim there. It was a
// usage error, so no claim could be renewed. Without --doc and --line, or
// with --all, there is nothing to renew and it is still a usage error.
func TestAckRenewsAClaim(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "docs/claims.md", "We chose Postgres. <!-- ds:claim owner=@p reviewed=2020-01-01 expires=90d -->\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	r := run(t, dir, v, "ack", "--doc", "docs/claims.md", "--line", "1", "--note", "still true")
	if r.code != 0 || !strings.Contains(r.out, "renewed the claim at docs/claims.md:1") {
		t.Fatalf("renew = %+v", r)
	}
	if got := stateAt(t, run(t, dir, v, "check", "--json").out, "docs/claims.md", 1, ""); got != "ok" {
		t.Errorf("a renewed claim is %q, want ok", got)
	}
	for _, args := range [][]string{{"ack"}, {"ack", "--all", "--doc", "docs/claims.md", "--line", "1"}} {
		if r := run(t, dir, v, args...); r.code != ExitError || !strings.Contains(r.err, "renew a claim") {
			t.Errorf("%v = %+v", args, r)
		}
	}
	// --doc alone is a page review, which this page has no schedule for.
	if r := run(t, dir, v, "ack", "--doc", "docs/claims.md"); r.code != ExitError || !strings.Contains(r.err, "no review_every") {
		t.Errorf("page review of a page without review_every = %+v", r)
	}
}

// TestPageReviewCanBeRecorded pins that a page past review_every can be
// cleared by doing what its remedy says. Before, the remedy said "ack what
// is still true", and a page with nothing cited on it had nothing to ack:
// `ds ack --doc D --line 1` failed with no reference at that line, so the
// finding could never be cleared. The review lasts one window.
func TestPageReviewCanBeRecorded(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "docs/runbook.md", "---\nds:\n  review_every: 30d\n---\n# Runbook\n\nRestart the thing.\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	due := clock.Add(60 * 24 * time.Hour)
	out := runAt(t, dir, v, due, "check")
	if !strings.Contains(out, "docs/runbook.md") || !strings.Contains(out, "ds ack --doc docs/runbook.md") {
		t.Fatalf("the finding must name the command that clears it: %s", out)
	}
	if out := runAt(t, dir, v, due, "ack", "--doc", "docs/runbook.md", "--note", "reread, still right"); !strings.Contains(out, "recorded a review of docs/runbook.md") {
		t.Fatalf("page review = %s", out)
	}
	if out := runAt(t, dir, v, due, "check"); strings.Contains(out, "review_every window") {
		t.Errorf("a recorded review clears the finding: %s", out)
	}
	if out := runAt(t, dir, v, due.Add(31*24*time.Hour), "check"); !strings.Contains(out, "review_every window") {
		t.Errorf("the review lasts one window, not forever: %s", out)
	}
	_, _, acks, _ := NewStore(dir).LoadState()
	last := acks.Rows[len(acks.Rows)-1]
	if last.Doc != "docs/runbook.md" || last.ID != "" || last.Line != 0 || last.BlockHash != "" || last.Note != "reread, still right" {
		t.Errorf("the review row = %+v", last)
	}
	// A path the scan does not know records nothing.
	if r := run(t, dir, v, "ack", "--doc", "docs/nope.md"); r.code != ExitError || !strings.Contains(r.err, "no review_every") {
		t.Errorf("unknown page = %+v", r)
	}
}

// TestSubdirectoryFindsTheRoot pins git-style discovery (bug 15): from inside
// an initialised repository every command finds its root, a path the user
// types is read from where they are, and every path printed stays
// repository-relative. It used to answer "run init first", and init there
// created a second, disjoint .ds.
// promise:init-nested
func TestSubdirectoryFindsTheRoot(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	sub := filepath.Join(dir, "docs")
	abs, _ := filepath.Abs(dir)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"check"}, "docs/sessions.md:5"},
		{[]string{"render", "sessions.md"}, "Every write goes through"},
		{[]string{"render", filepath.Join(abs, "docs", "sessions.md")}, "Every write goes through"},
		{[]string{"blame", "sessions.md", "1"}, "docs/sessions.md:1"},
		{[]string{"ack", "sess-save-k7m2p4xq", "--doc", "sessions.md", "--line", "1", "--dry-run"}, "would ack sess-save-k7m2p4xq at docs/sessions.md:1"},
		{[]string{"context", "sessions.md"}, "sess-save-k7m2p4xq"},
		{[]string{"context", "sess-save-k7m2p4xq"}, "cites sess-save-k7m2p4xq"},
		{[]string{"find", "--file", "../internal/"}, "sess-save-k7m2p4xq"},
		{[]string{"def", "../internal/store/write.go#Store.Persist", "--dry-run"}, "store-persist-"},
	}
	for _, c := range cases {
		r := run(t, sub, v, c.args...)
		if r.code == ExitError || !strings.Contains(r.out+r.err, c.want) {
			t.Errorf("%v from docs/ = %+v, want %q", c.args, r, c.want)
		}
	}
	// A path that leaves the repository is refused, not resolved under it.
	for _, args := range [][]string{{"render", "../../elsewhere.md"}, {"blame", "/etc/hosts", "1"}, {"ack", "x", "--doc", "../../a.md", "--line", "1"}} {
		if r := run(t, sub, v, args...); r.code != ExitError || !strings.Contains(r.err, "outside the repository") {
			t.Errorf("%v = %+v", args, r)
		}
	}
	// An output path is relative to where the user is, as git's are.
	if r := run(t, sub, v, "render", "sessions.md", "--out", "page.md"); r.code != 0 {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(sub, "page.md")); err != nil {
		t.Errorf("--out from docs/ belongs in docs/: %v", err)
	}
	// init still makes a root where it is started, and will not nest one
	// silently.
	if r := run(t, sub, v, "init"); r.code != ExitError || !strings.Contains(r.err, "already has .ds/config.toml") {
		t.Errorf("nested init = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(sub, DirName)); !os.IsNotExist(err) {
		t.Errorf("a refused init must write nothing: %v", err)
	}
	if r := run(t, t.TempDir(), v, "check"); r.code != ExitError || !strings.Contains(r.err, "run `init` first") {
		t.Errorf("no repository = %+v", r)
	}
	// init --force makes a separate root, and from then on discovery stops
	// at the nearer one.
	if r := run(t, sub, v, "init", "--force"); r.code != 0 {
		t.Fatalf("--force starts a separate root on purpose: %+v", r)
	}
	if r := run(t, sub, v, "check"); strings.Contains(r.out, "docs/sessions.md") {
		t.Errorf("the nested root is found first: %+v", r)
	}
}

// TestAncestorRootWithoutAWorkingDirectory covers a working directory that
// was deleted: a relative root cannot be resolved, and there are no
// ancestors to report rather than a guess.
func TestAncestorRootWithoutAWorkingDirectory(t *testing.T) {
	// Windows does not let a process delete its own working directory, so
	// the state this test is about cannot arise there.
	if runtime.GOOS == windowsOS {
		t.Skip("a working directory cannot be deleted on Windows")
	}
	gone := t.TempDir()
	t.Chdir(gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if root, ok := NewStore(".").ancestorRoot(); ok {
		t.Errorf("a deleted working directory has no ancestors: %q", root)
	}
}

// TestDirFlag pins the global --dir the editor and docs-site integrations
// document: every command runs as if started there. Before it existed,
// `ds --dir /repo lsp` exited on an unknown flag, so an editor configured
// the documented way had no server.
func TestDirFlag(t *testing.T) {
	t.Parallel()
	repo, v := initialised(t)
	elsewhere := t.TempDir()
	abs, _ := filepath.Abs(repo)
	if r := run(t, elsewhere, v, "--dir", abs, "check"); !strings.Contains(r.out, "docs/sessions.md") {
		t.Errorf("--dir absolute = %+v", r)
	}
	parent, name := filepath.Split(abs)
	if r := run(t, parent, v, "--dir", name, "scan"); r.code != 0 {
		t.Errorf("--dir relative to the starting directory = %+v", r)
	}
	for _, bad := range []string{filepath.Join(elsewhere, "missing"), filepath.Join(abs, "docs", "sessions.md")} {
		if r := run(t, elsewhere, v, "--dir", bad, "check"); r.code != ExitError || !strings.Contains(r.err, "is not a directory") {
			t.Errorf("--dir %s = %+v", bad, r)
		}
	}
	// --dir may name a subdirectory; the root is discovered from there.
	if r := run(t, elsewhere, v, "--dir", filepath.Join(abs, "docs"), "check"); !strings.Contains(r.out, "docs/sessions.md") {
		t.Errorf("--dir at a subdirectory = %+v", r)
	}
	// The default git client follows the root; an injected VCS is the
	// embedder's and stays.
	a := &App{dir: elsewhere, vcs: Git{Dir: elsewhere}, gitVCS: true}
	if err := a.locate(filepath.Join(abs, "docs"), true); err != nil || a.dir != abs || a.vcs != (Git{Dir: abs}) {
		t.Errorf("git client after --dir = %+v %v (root %s)", a.vcs, err, a.dir)
	}
	b := &App{dir: elsewhere, vcs: v}
	if err := b.locate(abs, true); err != nil {
		t.Fatal(err)
	}
	if kept, ok := b.vcs.(fakeVCS); !ok || kept.head != v.head {
		t.Errorf("an injected VCS must be kept: %+v", b.vcs)
	}
}

// TestAckDryRun pins what `ack --dry-run` promises: nothing is written, and
// what it lists is exactly what the real run then records. The pilot acked
// ids with --all without seeing what each call approved, and one ack's note
// described a sentence that had in fact changed.
func TestAckDryRun(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "docs/claims.md", "---\nds:\n  review_every: 30d\n---\n# C\n\nWe chose Postgres. <!-- ds:claim owner=@p reviewed=2020-01-01 expires=90d -->\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", goV2)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	acksFile := filepath.Join(dir, ".ds", AcksFile)
	before, _ := os.ReadFile(acksFile)
	unchanged := func(what string) {
		t.Helper()
		if now, _ := os.ReadFile(acksFile); string(now) != string(before) {
			t.Errorf("%s wrote to the ack log", what)
		}
	}
	dry := run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--all", "--note", "n", "--dry-run")
	unchanged("ack --all --dry-run")
	if dry.code != 0 || !strings.Contains(dry.out, `would ack sess-save-k7m2p4xq at docs/sessions.md:1 (`) || !strings.Contains(dry.out, `"Every write goes through`) {
		t.Fatalf("dry run = %+v", dry)
	}
	// The block-position citation on line 3 has no sentence to quote.
	if !strings.Contains(dry.out, "at docs/sessions.md:3 (") || strings.Contains(dry.out, `— ""`) {
		t.Errorf("block-position preview = %s", dry.out)
	}
	for _, args := range [][]string{
		{"ack", "--group", "1", "--dry-run"},
		{"ack", "--doc", "docs/claims.md", "--line", "7", "--dry-run"},
		{"ack", "--doc", "docs/claims.md", "--dry-run"},
	} {
		r := run(t, dir, v, args...)
		unchanged(strings.Join(args, " "))
		if r.code != 0 || !strings.Contains(r.out, "would ") {
			t.Errorf("%v = %+v", args, r)
		}
	}
	if r := run(t, dir, v, "ack", "--doc", "docs/claims.md", "--line", "7", "--dry-run"); !strings.Contains(r.out, `would renew the claim at docs/claims.md:7 — "We chose Postgres.`) {
		t.Errorf("claim preview = %s", r.out)
	}
	if r := run(t, dir, v, "ack", "--doc", "docs/claims.md", "--dry-run"); !strings.Contains(r.out, "would record a review of docs/claims.md") {
		t.Errorf("page preview = %s", r.out)
	}
	// The real run records exactly the citations the dry run listed.
	real := run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--all", "--note", "n")
	var listed, recorded []string
	for _, l := range strings.Split(dry.out, "\n") {
		if strings.HasPrefix(l, "would ack ") {
			listed = append(listed, strings.Fields(l)[4])
		}
	}
	for _, l := range strings.Split(real.out, "\n") {
		if strings.HasPrefix(l, "acked ") {
			recorded = append(recorded, strings.Fields(l)[3])
		}
	}
	if len(listed) == 0 || strings.Join(listed, ",") != strings.Join(recorded, ",") {
		t.Errorf("dry run listed %v, real run recorded %v", listed, recorded)
	}
}

// TestPreviewCutsLongSentences pins the one-line width of a quoted sentence.
func TestPreviewCutsLongSentences(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", sentencePreviewWidth+5)
	if got := []rune(preview(long)); len(got) != sentencePreviewWidth || got[len(got)-1] != '…' {
		t.Errorf("preview length %d, ends %q", len(got), got[len(got)-1])
	}
	if preview("short") != "short" {
		t.Error("a short sentence is quoted whole")
	}
}

// TestPathArgumentsFromASubdirectory covers the remaining path shapes: a
// def target by line, every path-taking command refusing a path outside
// the repository, and --file .. naming the whole repository.
func TestPathArgumentsFromASubdirectory(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	sub := filepath.Join(dir, "docs")
	outside := filepath.Join(t.TempDir(), "outside.md")
	write(t, filepath.Dir(outside), "outside.md", "x\n")
	if r := run(t, sub, v, "def", "../internal/store/write.go:8", "--dry-run"); r.code != 0 || !strings.Contains(r.out+r.err, "persist") {
		t.Errorf("def by line from docs/ = %+v", r)
	}
	for _, args := range [][]string{
		{"def", "../../x.go#X", "--dry-run"},
		{"def", "../../x.go:3", "--dry-run"},
		{"context", outside},
		{"find", "--file", "../../"},
	} {
		if r := run(t, sub, v, args...); r.code != ExitError || !strings.Contains(r.err, "outside the repository") {
			t.Errorf("%v = %+v", args, r)
		}
	}
	// A target of neither shape reaches Define, which says what it wants.
	if r := run(t, sub, v, "def", "nonsense", "--dry-run"); r.code != ExitError || !strings.Contains(r.err, "path#Symbol or path:line") {
		t.Errorf("malformed target = %+v", r)
	}
	whole := run(t, sub, v, "find", "--file", "..")
	if whole.code != 0 || !strings.Contains(whole.out, "sess-save-k7m2p4xq") || !strings.Contains(whole.out, "auth-port-h3v8n2wd") {
		t.Errorf("find --file .. from docs/ is the whole repository: %+v", whole)
	}
}
