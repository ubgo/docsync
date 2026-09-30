package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/config"
)

func TestRunExecution(t *testing.T) {
	// Not parallel: t.Setenv feeds the run.env expansion.
	dir, v := initialised(t)
	write(t, dir, "db/sweep.sql", "-- ds:def id=sweep-a2b6f8jk runnable=true\necho swept 3 rows\n")
	write(t, dir, "db/noexec.sql", "-- ds:def id=noexec-b3c7g9kl\necho never\n")
	write(t, dir, "scripts/smoke.sh", "echo smoke ok\n")
	write(t, dir, "runbooks/r.md", strings.Join([]string{
		"<!-- ds:run id=sweep-a2b6f8jk env=staging expect=rows -->",
		"<!-- ds:run cmd=\"echo hello $GREETING\" expect=hello -->",
		"<!-- ds:run file=scripts/smoke.sh expect=ok -->",
		"<!-- ds:run id=noexec-b3c7g9kl -->",
		"<!-- ds:run id=missing-c4d8h2lm -->",
		"<!-- ds:run cmd=\"exit 3\" expect=ok -->",
		"<!-- ds:run cmd=\"sleep 5\" -->",
		"<!-- ds:run cmd=\"printf ''\" expect=rows -->",
		"<!-- ds:run expect=ok -->",
		"",
	}, "\n"))
	write(t, dir, "docs/notallowed.md", "<!-- ds:run cmd=\"echo nope\" -->\n")
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\", \"runbooks/**\"]\n[run]\nenabled = false\n")
	// Disabled: nothing runs.
	r := run(t, dir, v, "check", "--run")
	if !strings.Contains(r.out, "run.enabled is false") {
		t.Errorf("disabled = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\", \"runbooks/**\"]\n[run]\nenabled = true\nallow = [\"runbooks/**\"]\ntimeout = \"300ms\"\n[run.env.staging]\nGREETING = \"$DS_TEST_GREETING\"\n")
	t.Setenv("DS_TEST_GREETING", "world")
	r = run(t, dir, v, "check", "--run", "--env", "staging")
	for _, want := range []string{
		"runbooks/r.md:1  run ok: echo swept 3 rows",
		"runbooks/r.md:2  run ok: echo hello $GREETING",
		"runbooks/r.md:3  run ok: sh 'scripts/smoke.sh'",
		"runbooks/r.md:4  run skipped: noexec-b3c7g9kl is not runnable=true",
		"runbooks/r.md:5  run skipped: missing-c4d8h2lm is not defined",
		"runbooks/r.md:6  run FAILED: exit 3",
		"runbooks/r.md:7  run FAILED: sleep 5",
		"runbooks/r.md:8  run FAILED: printf ''",
		"runbooks/r.md:9  run skipped: needs one of",
		"docs/notallowed.md:1  run skipped: cmd= is allowed only",
	} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q in:\n%s", want, r.out)
		}
	}
	if r.code != ExitFindings {
		t.Errorf("failed runs must fail the check: %d", r.code)
	}
	runs, err := NewStore(dir).LoadRuns()
	if err != nil || len(runs) != 6 || !strings.Contains(runs["runbooks/r.md:2"].Output, "hello world") || !strings.Contains(runs["runbooks/r.md:7"].Output, "timed out") {
		t.Errorf("runs = %+v %v", runs, err)
	}
	// render shows the recorded result.
	r = run(t, dir, v, "render", "runbooks/r.md")
	if r.code != 0 || !strings.Contains(r.out, "```sh\necho swept 3 rows\n```\n\n_ok · as of 2026-09-06T12:00:00Z_\n\n```text\nswept 3 rows\n```") || !strings.Contains(r.out, "_failed · as of") {
		t.Errorf("render with runs = %+v", r)
	}
	// Corrupt runs file and a bad allow glob.
	write(t, dir, ".ds/runs.json", "{")
	if r := run(t, dir, v, "render", "runbooks/r.md"); r.code != ExitError || !strings.Contains(r.err, "runs.json") {
		t.Errorf("corrupt runs on render = %+v", r)
	}
	if r := run(t, dir, v, "check", "--run"); r.code != ExitError {
		t.Errorf("corrupt runs on check = %+v", r)
	}
	os.Remove(filepath.Join(dir, ".ds", "runs.json"))
	// No --env, no run.env, an empty timeout (default applies), and an
	// output larger than the cap.
	write(t, dir, "runbooks/big.md", "<!-- ds:run cmd=\"head -c 70000 /dev/zero | tr '\\0' x\" -->\n")
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"runbooks/big.md\"]\n[run]\nenabled = true\nallow = [\"runbooks/**\"]\ntimeout = \"\"\n")
	if r := run(t, dir, v, "check", "--run"); !strings.Contains(r.out, "runbooks/big.md:1  run ok") {
		t.Errorf("big output run = %+v", r)
	}
	if runs, _ := NewStore(dir).LoadRuns(); len(runs["runbooks/big.md:1"].Output) != runOutputCap {
		t.Errorf("output not capped: %d", len(runs["runbooks/big.md:1"].Output))
	}
	os.Remove(filepath.Join(dir, ".ds", "runs.json"))
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"runbooks/**\"]\n[run]\nenabled = true\nallow = [\"[\"]\n")
	if r := run(t, dir, v, "check", "--run"); !strings.Contains(r.out, "run.allow:") {
		t.Errorf("bad allow glob = %+v", r)
	}
	// A write failure for runs.json.
	if err := os.Mkdir(filepath.Join(dir, ".ds", RunsFile+writeTempExt), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, v, "check", "--run"); r.code != ExitError {
		t.Errorf("runs write failure = %+v", r)
	}
	if got := runsFor(map[string]RunRecord{"a.md:x": {}, "nocolon": {}, "a.md:2": {OK: true}}, "a.md"); len(got) != 1 || !got[2].OK {
		t.Errorf("runsFor = %+v", got)
	}
}

func TestURLResolve(t *testing.T) {
	t.Parallel()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("<html><head><title> TOAST docs </title></head></html>"))
		case "/moved":
			http.Redirect(w, r, "/ok", http.StatusMovedPermanently)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	dir, v := initialised(t)
	write(t, dir, "docs/links.md", strings.Join([]string{
		"See [ok](ds:url?href=" + srv.URL + "/ok&title=TOAST).",
		"See [retitled](ds:url?href=" + srv.URL + "/ok&title=Other).",
		"See [moved](ds:url?href=" + srv.URL + "/moved).",
		"See [dead](ds:url?href=" + srv.URL + "/gone).",
		"See [unreachable](ds:url?href=http://127.0.0.1:1/x).",
		"See [bad](ds:url?href=://nope).",
		"",
	}, "\n"))
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[url]\nttl = \"7d\"\nrate_per_minute = 60000\n")
	client := srv.Client()
	var out, errb bytes.Buffer
	code := Run([]string{"check", "--resolve"}, WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithHTTPClient(client), WithClock(func() time.Time { return clock }))
	text := out.String()
	for _, want := range []string{"retitled", "url moved", "dead"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s%s", want, text, errb.String())
		}
	}
	if code != ExitFindings || strings.Contains(text, "unverifiable") {
		t.Errorf("resolve = %d\n%s", code, text)
	}
	firstHits := hits
	cache, err := NewStore(dir).LoadURLs()
	if err != nil || len(cache) != 5 || cache[srv.URL+"/ok"].Title != "TOAST docs" || cache[srv.URL+"/moved"].Final != srv.URL+"/ok" || cache[srv.URL+"/gone"].Status != 404 {
		t.Errorf("cache = %+v %v", cache, err)
	}
	// Within the ttl the cache answers; nothing is fetched again.
	out.Reset()
	Run([]string{"check", "--resolve"}, WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithHTTPClient(client), WithClock(func() time.Time { return clock }))
	if hits != firstHits {
		t.Errorf("cache miss: hits %d -> %d", firstHits, hits)
	}
	// Past the ttl (an empty ttl means the default) it refetches.
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[url]\nttl = \"\"\nrate_per_minute = 60000\n")
	Run([]string{"check", "--resolve"}, WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithHTTPClient(client), WithClock(func() time.Time { return clock.Add(30 * 24 * time.Hour) }))
	if hits <= firstHits {
		t.Error("expired cache must refetch")
	}
	// Without --resolve nothing is fetched and urls are unverifiable.
	before := hits
	if r := run(t, dir, v, "check"); !strings.Contains(r.out, "unverifiable") || hits != before {
		t.Errorf("no resolve = %+v hits %d", r, hits)
	}
	// A corrupt cache is reported after the run; a missing config fails early.
	write(t, dir, ".ds/urls.json", "{")
	if code := Run([]string{"check", "--resolve"}, WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithHTTPClient(client)); code != ExitError {
		t.Errorf("corrupt url cache = %d", code)
	}
	os.Remove(filepath.Join(dir, ".ds", "urls.json"))
	os.Remove(filepath.Join(dir, ".ds", "config.toml"))
	if code := Run([]string{"check", "--resolve"}, WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithHTTPClient(client)); code != ExitError {
		t.Errorf("resolve uninitialised = %d", code)
	}
	// The default client is used when none is injected; the rate limiter
	// sleeps between two live requests.
	dir2, v2 := initialised(t)
	write(t, dir2, "docs/links.md", "See [a](ds:url?href="+srv.URL+"/ok). See [b](ds:url?href="+srv.URL+"/gone).\n")
	write(t, dir2, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[url]\nrate_per_minute = 6000\n")
	if r := run(t, dir2, v2, "check", "--resolve"); r.code != ExitFindings || !strings.Contains(r.out, "dead") {
		t.Errorf("default client = %+v", r)
	}
}

func TestRenderAt(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	ledgerRaw, _ := os.ReadFile(filepath.Join(dir, ".ds/ledger.tsv"))
	v.files["old:.ds/ledger.tsv"] = ledgerRaw
	v.files["old:internal/store/write.go"] = []byte(goV1)
	v.files["old:docs/sessions.md"] = []byte("Old page: [`Save`](ds:block?id=sess-save-k7m2p4xq).\n\n<!-- ds:block id=sess-save-k7m2p4xq at=old -->\n")
	write(t, dir, "internal/store/write.go", goV2)
	r := run(t, dir, v, "render", "docs/sessions.md", "--at", "old")
	if r.code != 0 || !strings.HasPrefix(r.out, "Old page:") || !strings.Contains(r.out, "as of `old`") || !strings.Contains(r.out, "return s.legacy.Save()") || strings.Contains(r.out, "sessions.Insert") {
		t.Errorf("render at = %+v", r)
	}
	if r := run(t, dir, v, "render", "docs/missing.md", "--at", "old"); r.code != ExitError {
		t.Errorf("missing page at commit = %+v", r)
	}
	// Snapshot hook degrades: no ledger at the commit, corrupt ledger,
	// unknown id, missing file, bad range.
	snap := (&App{vcs: fakeVCS{files: map[string][]byte{}}}).snapshotAt("x")
	if _, ok := snap("id", ""); ok {
		t.Error("no ledger at commit")
	}
	snap = (&App{vcs: fakeVCS{files: map[string][]byte{"x:.ds/ledger.tsv": []byte("garbage\n")}}}).snapshotAt("x")
	if _, ok := snap("id", ""); ok {
		t.Error("corrupt ledger at commit")
	}
	files := map[string][]byte{"x:.ds/ledger.tsv": ledgerRaw, "x:internal/store/write.go": []byte("short\n")}
	snap = (&App{vcs: fakeVCS{files: files}}).snapshotAt("x")
	if _, ok := snap("nope", ""); ok {
		t.Error("unknown id")
	}
	if _, ok := snap("sess-save-k7m2p4xq", ""); ok {
		t.Error("range beyond the file at that commit")
	}
	if _, ok := snap("auth-port-h3v8n2wd", ""); ok {
		t.Error("file missing at commit")
	}
}

// TestRunFileIsGatedAndQuoted pins ds:run file= as the code execution it
// is. It skipped run.allow, which gates cmd=, and put its path into the
// shell line unquoted, so any doc could write file="x; <anything>" and have
// `check --run` execute it. Now it is gated like cmd=, the path must be a
// regular file inside the repository, and it reaches sh as one word.
// promise:run-gated promise:file-confined
func TestRunFileIsGatedAndQuoted(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	proof := filepath.Join(dir, "proof.txt")
	inject := `x.sh; echo INJECTED > ` + proof
	write(t, dir, "docs/notes.md", "<!-- ds:run file=\""+inject+"\" -->\n")
	write(t, dir, "runbooks/r.md", strings.Join([]string{
		`<!-- ds:run file="../outside.sh" -->`,
		`<!-- ds:run file="/etc/hosts" -->`,
		`<!-- ds:run file="-c" -->`,
		`<!-- ds:run file="scripts/missing.sh" -->`,
		`<!-- ds:run file="scripts/link.sh" -->`,
		`<!-- ds:run file="scripts/odd; $(echo name).sh" -->`,
		`<!-- ds:run file="` + inject + `" -->`,
	}, "\n")+"\n")
	write(t, dir, "scripts/real.sh", "echo real\n")
	write(t, dir, "scripts/odd; $(echo name).sh", "echo odd-ran\n")
	if err := os.Symlink(filepath.Join(dir, "scripts/real.sh"), filepath.Join(dir, "scripts/link.sh")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"internal/**\"]\ndocs = [\"docs/**\", \"runbooks/**\"]\n[run]\nenabled = true\nallow = [\"runbooks/**\"]\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	r := run(t, dir, v, "check", "--run")
	if _, err := os.Stat(proof); !os.IsNotExist(err) {
		t.Fatalf("a doc executed an injected command: %s", r.out)
	}
	for _, want := range []string{
		"docs/notes.md:1  run skipped: file= is allowed only in docs matching run.allow",
		"runbooks/r.md:1  run skipped: file=\"../outside.sh\" must be a clean path",
		"runbooks/r.md:2  run skipped: file=\"/etc/hosts\" must be a clean path",
		"runbooks/r.md:3  run skipped: file=\"-c\" must be a clean path",
		"runbooks/r.md:4  run skipped: file=\"scripts/missing.sh\" is not a regular file",
		"runbooks/r.md:5  run skipped: file=\"scripts/link.sh\" is not a regular file",
		"runbooks/r.md:6  run ok: sh 'scripts/odd; $(echo name).sh'",
		"runbooks/r.md:7  run skipped:",
	} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q in:\n%s", want, r.out)
		}
	}
	runs, _ := NewStore(dir).LoadRuns()
	if got := runs["runbooks/r.md:6"].Output; got != "odd-ran\n" {
		t.Errorf("the oddly named script ran as one file: %q", got)
	}
}

// TestShellQuote states that a quoted string reaches sh as exactly itself,
// whatever it holds.
func TestShellQuote(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"plain", "with space", "it's", `a"b`, "$(id)", "`id`", "a; b", "\\n", "'", "''", ""} {
		out, err := exec.Command(config.DefaultRunShell, "-c", "printf %s "+shellQuote(s)).Output()
		if err != nil || string(out) != s {
			t.Errorf("shellQuote(%q) came back as %q (%v)", s, out, err)
		}
	}
}

// TestRunnableFileRefusesRootedPathsOnEveryHost pins that a ds:run file= is
// judged by its text, not by the host's idea of "absolute". file= is committed
// in a doc and read on every platform; filepath.IsAbs alone let "/etc/hosts"
// through on Windows, where a path needs a drive letter to be absolute.
func TestRunnableFileRefusesRootedPathsOnEveryHost(t *testing.T) {
	t.Parallel()
	a := &App{dir: t.TempDir()}
	for _, file := range []string{"/etc/hosts", `\etc\hosts`, `C:\Windows\x.bat`, "c:/x.sh", `\\server\share\x.sh`} {
		if got := a.runnableFile(file); !strings.Contains(got, "must be a clean path inside the repository") {
			t.Errorf("runnableFile(%q) = %q, want it refused as not a clean path", file, got)
		}
	}
	// A name that merely contains a colon later on is not a drive.
	if hasDrive("scripts/a:b.sh") || hasDrive("1:x") || hasDrive("") || !hasDrive("Z:") {
		t.Error("hasDrive must match a leading letter and colon only")
	}
	if !rooted("/x") || rooted("x/y") || rooted("") {
		t.Error("rooted must match a leading separator only")
	}
}

// TestRunShellIsConfigurableAndRequired pins [run] shell (promise:run-shell):
// commands run under the shell the config names, a file= script reaches it as
// one argument, and a shell that is not on PATH is an error rather than a run
// that quietly did nothing -- but only once something is about to run.
// With sh present and another shell named, sh is not used instead
// (promise:run-shell-no-fallback).
func TestRunShellIsConfigurableAndRequired(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "scripts/smoke.sh", "echo smoke ok\n")
	write(t, dir, "runbooks/r.md", "<!-- ds:run expect=ok -->\n")
	setShell := func(shell string) {
		write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"internal/**\"]\ndocs = [\"runbooks/**\"]\n[run]\nenabled = true\nallow = [\"runbooks/**\"]\nshell = \""+shell+"\"\n")
	}
	const absent = "ds-no-such-shell"
	setShell(absent)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	// Nothing runnable: the missing shell is not asked for.
	if r := run(t, dir, v, "check", "--run"); r.code == ExitError || !strings.Contains(r.out, "run skipped: needs one of") {
		t.Errorf("nothing to run, yet the shell was required: %+v", r)
	}
	write(t, dir, "runbooks/r.md", "<!-- ds:run cmd=\"echo hi\" expect=hi -->\n<!-- ds:run file=scripts/smoke.sh expect=ok -->\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	r := run(t, dir, v, "check", "--run")
	if r.code != ExitError || !strings.Contains(r.err, "the shell is not on PATH: "+absent) || !strings.Contains(r.err, "[run] shell") || strings.Contains(r.out, "run ok") {
		t.Errorf("a missing shell must stop the run: %+v", r)
	}
	if err := requireShell(absent); !errors.Is(err, ErrNoShell) {
		t.Errorf("requireShell = %v", err)
	}
	if runtime.GOOS == "windows" {
		return // the wrapper below is a #! script, which Windows cannot start
	}
	// A shell of the user's own: it logs its arguments, then hands them to sh.
	wrapper := filepath.Join(t.TempDir(), "myshell")
	log := wrapper + ".log"
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nfor a in \"$@\"; do printf '[%s]' \"$a\" >> "+shellQuote(log)+"; done\necho >> "+shellQuote(log)+"\nexec sh \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	setShell(wrapper)
	r = run(t, dir, v, "check", "--run")
	if !strings.Contains(r.out, "runbooks/r.md:1  run ok: echo hi") || !strings.Contains(r.out, "runbooks/r.md:2  run ok: "+wrapper+" 'scripts/smoke.sh'") {
		t.Errorf("the configured shell did not run the commands: %+v", r)
	}
	if got, _ := os.ReadFile(log); string(got) != "[-c][echo hi]\n[scripts/smoke.sh]\n" {
		t.Errorf("the shell was started with %q", got)
	}
}
