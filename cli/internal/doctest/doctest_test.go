package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()
	page := strings.Join([]string{
		"# Page",
		"<!-- doctest",
		"git init -q .",
		"",
		"-->",
		"```go file=a/b.go",
		"package b",
		"```",
		"~~~text file=notes.txt append=true",
		"more",
		"~~~",
		"<!-- doctest:skip needs the network -->",
		"```",
		"$ curl example.invalid",
		"```",
		"````markdown",
		"```",
		"$ not a session, a fence inside a fence",
		"```",
		"````",
		"```",
		"$ cat > x <<'EOF'",
		"line",
		"EOF",
		"$ echo a \\",
		"  b",
		"a b",
		"",
		"$ true",
		"```",
		"    ```",
		"indented four spaces is not a fence",
		"",
	}, "\n")
	steps, err := parse(page)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []stepKind{stepHidden, stepFile, stepFile, stepSkip, stepCommand, stepCommand, stepCommand}
	if len(steps) != len(kinds) {
		t.Fatalf("steps = %+v", steps)
	}
	for i, k := range kinds {
		if steps[i].kind != k {
			t.Errorf("step %d kind = %v, want %v", i, steps[i].kind, k)
		}
	}
	if steps[1].path != "a/b.go" || steps[1].body != "package b\n" || steps[1].append || !steps[2].append {
		t.Errorf("files = %+v %+v", steps[1], steps[2])
	}
	if steps[3].reason != "needs the network" {
		t.Errorf("skip = %+v", steps[3])
	}
	if steps[4].body != "cat > x <<'EOF'\nline\nEOF" || steps[4].want != nil {
		t.Errorf("heredoc = %+v", steps[4])
	}
	if steps[5].body != "echo a \\\n  b" || strings.Join(steps[5].want, "|") != "a b|" {
		t.Errorf("continuation = %+v", steps[5])
	}
	for _, bad := range []string{"<!-- doctest:skip -->\n", "<!-- doctest\nls\n", "```\n$ ls\n"} {
		if _, err := parse(bad); err == nil {
			t.Errorf("parse(%q) must fail", bad)
		}
	}
	if _, err := parse("<!-- doctest\n"); !errors.Is(err, ErrUnclosed) {
		t.Errorf("unclosed hidden = %v", err)
	}
}

func TestNormaliseAndMatch(t *testing.T) {
	t.Parallel()
	n := newNormaliser("/tmp/doctest-1", "/private/tmp/doctest-1")
	got := n.line("total-a2b6f8jk and total-a2b6f8jk, other-b3c7g9kl at /private/tmp/doctest-1/repo  ")
	if got.text != "total-<id> and total-<id>, other-<id> at <repo>/repo" || strings.Join(got.ids, ",") != "a2b6f8jk,a2b6f8jk,b3c7g9kl" {
		t.Errorf("ids = %+v", got)
	}
	if got := n.line("commit 1bef39d4 on 2026-10-01T04:09:00Z, 2026-10-01, took 12.5ms, _as of 2026-10-01T04:09:00Z_"); got.text != "commit <hex> on <time>, <date>, took <dur>, _as of <time>_" || got.ids != nil {
		t.Errorf("generated = %+v", got)
	}
	if got := newNormaliser().line("plain"); got.text != "plain" {
		t.Errorf("no repo = %+v", got)
	}
	lines := func(ls ...string) []idLine {
		var out []idLine
		for _, l := range ls {
			out = append(out, newNormaliser().line(l))
		}
		return out
	}
	for _, c := range []struct {
		want, got []idLine
		ok        bool
	}{
		{nil, nil, true},
		{nil, lines("x"), false},
		{lines("a"), nil, false},
		{lines("a", "…", "z"), lines("a", "b", "c", "z"), true},
		{lines("…"), nil, true},
		{lines("a", "…", "z"), lines("a", "b"), false},
		{lines("ds v…"), lines("ds v0.1.4"), true},
		{lines("ds v…"), lines("dx"), false},
		{lines("a"), lines("b"), false},
		// Ids pair one to one across the lines.
		{lines("x-a2b6f8jk", "x-a2b6f8jk"), lines("x-k7m2p4xq", "x-k7m2p4xq"), true},
		{lines("x-a2b6f8jk", "x-a2b6f8jk"), lines("x-k7m2p4xq", "x-m3n8p2uv"), false},
		{lines("x-a2b6f8jk", "x-b3c7g9kl"), lines("x-k7m2p4xq", "x-k7m2p4xq"), false},
		// Lines an ellipsis skips pair nothing, so later ids still line up.
		{lines("…", "x-a2b6f8jk"), lines("y-m3n8p2uv", "x-k7m2p4xq"), true},
		{lines("x-a2b6f8jk…"), lines("x-k7m2p4xq and more"), true},
		{lines("x-a2b6f8jk…"), lines("y-k7m2p4xq"), false},
	} {
		if _, ok := match(c.want, c.got, newIDPairs()); ok != c.ok {
			t.Errorf("match(%v, %v) = %v", c.want, c.got, ok)
		}
	}
	p, _ := match(lines("x-a2b6f8jk"), lines("x-k7m2p4xq"), newIDPairs())
	if got := p.translate("ack x-a2b6f8jk, not y-b3c7g9kl"); got != "ack x-k7m2p4xq, not y-b3c7g9kl" {
		t.Errorf("translate = %q", got)
	}
	if got := trimBlank([]string{"a", "", " "}); len(got) != 1 {
		t.Errorf("trimBlank = %q", got)
	}
}

func TestOpenAndCloseFence(t *testing.T) {
	t.Parallel()
	if _, _, ok := openFence("    ```"); ok {
		t.Error("four spaces of indent is code, not a fence")
	}
	if f, info, ok := openFence("  ~~~~ console"); !ok || f != "~~~~" || info != "console" {
		t.Errorf("tilde fence = %q %q %v", f, info, ok)
	}
	if _, _, ok := openFence("``"); ok {
		t.Error("two backticks are not a fence")
	}
	if !closesFence("`````", "````") || closesFence("```", "````") || closesFence("```` x", "````") {
		t.Error("closesFence")
	}
	if infoValue("go file=a.go", "path=") != "" {
		t.Error("absent key")
	}
}

// page writes a markdown page into dir and returns its path.
func page(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunPage(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		"```text file=sub/a.txt",
		"one",
		"```",
		"```text file=sub/a.txt append=true",
		"two",
		"```",
		"<!-- doctest",
		"mkdir -p other",
		"-->",
		"```",
		"$ export GREETING=hello",
		"$ cd sub",
		"$ cat a.txt",
		"one",
		"two",
		"$ echo $GREETING",
		"hello",
		"$ cd ..",
		"$ pwd",
		"<repo>/repo",
		"$ printf 'got\\n'; exit 3",
		"want",
		"$ true",
		"$ cd nowhere",
		"```",
		"<!-- doctest",
		"cd nowhere-hidden",
		"-->",
		"```text file=sub/a.txt/x",
		"cannot write below a file",
		"```",
		"<!-- doctest:skip shown only -->",
		"```",
		"$ false",
		"```",
		"",
	}, "\n")
	var out bytes.Buffer
	res, err := runPage("p.md", body, "", pageEnv("/nonexistent", ".", os.Environ()), "sh", &out)
	if err != nil {
		t.Fatal(err)
	}
	if res.passed != 7 || res.failed != 4 || res.skipped != 1 {
		t.Errorf("result = %+v\n%s", res, out.String())
	}
	for _, want := range []string{"FAIL  p.md:21  $ printf", "want:", "SKIP  p.md:", "hidden setup", "writing sub/a.txt/x", "cd nowhere"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
	if _, err := runPage("bad.md", "```\n$ ls\n", "", nil, "sh", &out); err == nil {
		t.Error("a page that does not parse is an error")
	}
	// A shell that does not exist fails every command step.
	res, _ = runPage("p.md", "<!-- doctest\ntrue\n-->\n", "", nil, filepath.Join(t.TempDir(), "no-shell"), &out)
	if res.failed != 1 {
		t.Errorf("missing shell = %+v", res)
	}
	// A temporary directory that cannot be made is an error, not a pass.
	notDir := page(t, t.TempDir(), "f", "")
	if _, err := runPage("p.md", "", notDir, nil, "sh", &out); err == nil {
		t.Error("an unusable temporary directory must fail the page")
	}
	// A file step onto an existing directory fails that step.
	res, _ = runPage("p.md", "<!-- doctest\nmkdir d\n-->\n```text file=d\nx\n```\n", "", nil, "sh", &out)
	if res.failed != 1 {
		t.Errorf("file onto a directory = %+v", res)
	}
}

// TestMintedIdsCarryForward: the page shows an id that ds will not mint
// again; once the outputs have lined the two up, later commands and files
// use the real one.
func TestMintedIdsCarryForward(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		"```",
		"$ echo minted-k7m2p4xq",
		"minted-a2b6f8jk",
		"$ echo minted-a2b6f8jk unknown-b3c7g9kl",
		"minted-a2b6f8jk unknown-b3c7g9kl",
		"```",
		"<!-- doctest",
		"test -f never-a2b6f8jk || echo minted-a2b6f8jk > hidden.txt",
		"-->",
		"```text file=cite.txt",
		"see minted-a2b6f8jk",
		"```",
		"```",
		"$ cat cite.txt hidden.txt",
		"see minted-a2b6f8jk",
		"minted-a2b6f8jk",
		"```",
		"",
	}, "\n")
	var out bytes.Buffer
	res, err := runPage("ids.md", body, "", pageEnv("/nonexistent", ".", os.Environ()), "sh", &out)
	if err != nil || res.failed != 0 || res.passed != 3 {
		t.Errorf("result = %+v %v\n%s", res, err, out.String())
	}
}

func TestPageEnv(t *testing.T) {
	t.Parallel()
	env := strings.Join(pageEnv("/b", "/r", []string{"CI=true", "HOME=/h", "PATH=/x", "GITHUB_TOKEN=t"}), "\n")
	for _, want := range []string{"HOME=/h", "PATH=/b", "DOCSYNC_ROOT=/r", "TZ=UTC", "GIT_CONFIG_NOSYSTEM=1"} {
		if !strings.Contains(env, want) {
			t.Errorf("missing %q in %s", want, env)
		}
	}
	for _, gone := range []string{"CI=true", "GITHUB_TOKEN", "PATH=/x\n"} {
		if strings.Contains(env+"\n", gone) {
			t.Errorf("%q must be dropped: %s", gone, env)
		}
	}
}

func TestRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := page(t, dir, "good.md", "```\n$ echo hi\nhi\n```\n<!-- doctest:skip later -->\n```\n$ x\n```\n")
	var out bytes.Buffer
	if code := run([]string{"-dir", dir, good}, &out); code != 0 || !strings.Contains(out.String(), "1 passed, 0 failed") || !strings.Contains(out.String(), "1 blocks skipped") {
		t.Errorf("good = %d\n%s", code, out.String())
	}
	page(t, dir, "bad.md", "```\n$ echo hi\nbye\n```\n")
	out.Reset()
	if code := run([]string{"-dir", dir}, &out); code != 1 || !strings.Contains(out.String(), "FAIL   bad.md") {
		t.Errorf("all pages = %d\n%s", code, out.String())
	}
	for _, args := range [][]string{
		{"-nope"},
		{"-dir", filepath.Join(dir, "empty")},
		{filepath.Join(dir, "missing.md")},
		{page(t, dir, "broken.md", "```\n$ ls\n")},
	} {
		out.Reset()
		if code := run(args, &out); code != 2 {
			t.Errorf("run(%q) = %d, want 2\n%s", args, code, out.String())
		}
	}
}

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func TestMainRuns(t *testing.T) {
	dir := t.TempDir()
	p := page(t, dir, "m.md", "```\n$ echo ok\nok\n```\n")
	code := -1
	exit = func(c int) { code = c }
	defer func() { exit = os.Exit }()
	os.Args = []string{"doctest", "-dir", dir, p}
	main()
	if code != 0 {
		t.Errorf("exit = %d", code)
	}
}
