// Command doctest runs the examples in docsync's guide pages against the
// built ds binary and fails when what a page shows is not what ds prints.
//
// Why it exists: every unit test here asserts what its author expected, on
// inputs the author chose, through the parts the author wired up. Writing the
// guide -- running every documented behaviour as a user would, with the page
// as the oracle -- found some eighty places where the binary and its own
// documentation disagreed, after 400 e2e cases and 100% statement coverage
// had found none of them. This keeps that check permanent: a page whose
// example no longer matches the binary fails the gate, so the guide cannot
// drift from the tool and the tool cannot regress behind the guide.
//
// A page is run top to bottom in one fresh git repository:
//
//   - A fenced block whose info string carries file=PATH is written to PATH
//     (relative to the current directory) before the next command runs.
//     file=PATH append=true appends instead.
//   - A fenced block whose first line starts with "$ " is a session: each
//     "$ " line is a command, the lines after it until the next command are
//     its expected output. A command ending in a heredoc (<<'EOF') takes the
//     following lines up to the terminator as its input, not as output.
//   - <!-- doctest ... --> holds hidden commands, run but not compared: the
//     setup a reader does not need to see (git commits, a fake plugin).
//   - <!-- doctest:skip REASON --> before a block skips it. The reason is
//     printed and the skip counted, so a page cannot pass by skipping.
//
// Ids are minted at random, so the id a page shows is not the id ds mints
// when the page runs. Once an id is paired (below), every later command and
// file the page writes has the page's id replaced by the real one: a page
// can `$ ds def x.go#F`, then `$ ds ack <the id it showed>` and cite it in a
// doc, and the run uses the id ds actually minted.
//
// `$ cd DIR` and `$ export K=V` change the directory and environment of the
// commands after them. Expected output may contain a line that is only "…"
// (any number of lines) or end a line with "…" (any rest of the line).
// Generated values are normalised on both sides before comparing: commit
// hashes, block hashes, timestamps, dates, durations and the repository's
// temporary path. Id suffixes are paired instead: where the page shows an id
// and ds printed one in the same place, the two are paired for the rest of
// the page, one to one, so the same id must recur where the page says it
// does. Lines an ellipsis skips pair nothing.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	// promptPrefix starts a command line in a session block.
	promptPrefix = "$ "
	// ellipsis in expected output matches any lines, or any rest of a line.
	ellipsis = "…"
	// hiddenOpen and skipOpen are the HTML comments a page uses for hidden
	// setup and for skipping the next block; GitHub renders neither.
	hiddenOpen = "<!-- doctest"
	skipOpen   = "<!-- doctest:skip"
	// commentClose ends either comment.
	commentClose = "-->"
	// fileKey and appendKey are the info-string words that make a block a
	// file to write.
	fileKey   = "file="
	appendKey = "append=true"
	// commandTimeout bounds one command, so a hung ds fails its page rather
	// than the whole run. It is generous because a page may build a Go
	// program against this checkout, and the first build on a machine with a
	// cold cache compiles the tree-sitter grammars.
	commandTimeout = 5 * time.Minute
	// repoDirName is the directory each page starts in, inside its own
	// temporary root, so a page may create sibling repositories with `cd ..`.
	repoDirName = "repo"
)

// stepKind is what a step does.
type stepKind int

const (
	stepFile    stepKind = iota // write a file
	stepCommand                 // run a command and compare its output
	stepHidden                  // run a command, compare nothing
	stepSkip                    // a block the page skipped, with its reason
)

// step is one thing a page asks for, in order.
type step struct {
	kind   stepKind
	line   int      // 1-based line in the page, for messages
	path   string   // stepFile: where to write
	append bool     // stepFile: append rather than replace
	body   string   // stepFile: content; stepCommand/stepHidden: the command
	want   []string // stepCommand: expected output lines
	reason string   // stepSkip
}

// ErrUnclosed is a fence or comment the page never closes.
var ErrUnclosed = errors.New("doctest: unclosed block")

// parse reads a page into its steps.
func parse(page string) ([]step, error) {
	lines := strings.Split(strings.ReplaceAll(page, "\r\n", "\n"), "\n")
	var steps []step
	skipNext := ""
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, skipOpen):
			skipNext = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(t, skipOpen), commentClose))
			if skipNext == "" {
				return nil, fmt.Errorf("line %d: doctest:skip needs a reason", i+1)
			}
		case strings.HasPrefix(t, hiddenOpen):
			end := indexFrom(lines, i+1, func(s string) bool { return strings.TrimSpace(s) == commentClose })
			if end < 0 {
				return nil, fmt.Errorf("%w: hidden commands at line %d", ErrUnclosed, i+1)
			}
			for j, c := range lines[i+1 : end] {
				if strings.TrimSpace(c) != "" {
					steps = append(steps, step{kind: stepHidden, line: i + 2 + j, body: c})
				}
			}
			i = end
		default:
			fence, info, ok := openFence(l)
			if !ok {
				continue
			}
			end := indexFrom(lines, i+1, func(s string) bool { return closesFence(s, fence) })
			if end < 0 {
				return nil, fmt.Errorf("%w: fence at line %d", ErrUnclosed, i+1)
			}
			body := lines[i+1 : end]
			switch {
			case skipNext != "":
				steps = append(steps, step{kind: stepSkip, line: i + 1, reason: skipNext})
				skipNext = ""
			case infoValue(info, fileKey) != "":
				steps = append(steps, step{kind: stepFile, line: i + 1, path: infoValue(info, fileKey), append: hasWord(info, appendKey), body: strings.Join(body, "\n") + "\n"})
			case len(body) > 0 && strings.HasPrefix(body[0], promptPrefix):
				steps = append(steps, session(body, i+2)...)
			}
			i = end
		}
	}
	return steps, nil
}

// session splits a session block into commands and their expected output.
func session(body []string, first int) []step {
	var out []step
	for i := 0; i < len(body); i++ {
		cmd := strings.TrimPrefix(body[i], promptPrefix)
		s := step{kind: stepCommand, line: first + i}
		// Continuation lines, then a heredoc's input, belong to the command.
		for strings.HasSuffix(cmd, "\\") && i+1 < len(body) {
			i++
			cmd += "\n" + body[i]
		}
		if term := heredocTerminator(cmd); term != "" {
			for i+1 < len(body) {
				i++
				cmd += "\n" + body[i]
				if body[i] == term {
					break
				}
			}
		}
		s.body = cmd
		for i+1 < len(body) && !strings.HasPrefix(body[i+1], promptPrefix) {
			i++
			s.want = append(s.want, body[i])
		}
		out = append(out, s)
	}
	return out
}

var heredocRE = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// heredocTerminator returns the word a heredoc in cmd ends at, or "".
func heredocTerminator(cmd string) string {
	if m := heredocRE.FindStringSubmatch(cmd); m != nil {
		return m[1]
	}
	return ""
}

// openFence reports whether l opens a fence, returning the fence itself
// (``` or ~~~ of some length) and the info string after it.
func openFence(l string) (fence, info string, ok bool) {
	t := strings.TrimLeft(l, " ")
	if len(l)-len(t) > 3 {
		return "", "", false
	}
	for _, c := range []byte{'`', '~'} {
		n := 0
		for n < len(t) && t[n] == c {
			n++
		}
		if n >= 3 {
			return t[:n], strings.TrimSpace(t[n:]), true
		}
	}
	return "", "", false
}

// closesFence reports whether l closes a block opened by fence: the same
// character, at least as many, and nothing after.
func closesFence(l, fence string) bool {
	t := strings.TrimSpace(l)
	return strings.HasPrefix(t, fence) && strings.Trim(t, fence[:1]) == ""
}

// infoValue returns the value of key (such as "file=") among the
// space-separated words of an info string.
func infoValue(info, key string) string {
	for w := range strings.FieldsSeq(info) {
		if v, ok := strings.CutPrefix(w, key); ok {
			return v
		}
	}
	return ""
}

func hasWord(info, word string) bool {
	return slices.Contains(strings.Fields(info), word)
}

func indexFrom(lines []string, from int, match func(string) bool) int {
	for i := from; i < len(lines); i++ {
		if match(lines[i]) {
			return i
		}
	}
	return -1
}

// normaliser rewrites generated values to stable placeholders and pulls
// out id suffixes, which are compared by pairing rather than by text.
type normaliser struct {
	repos []string // the temporary root as printed, longest first
}

// idLine is one output line with every id suffix replaced by idMark, and
// the suffixes it held, in order.
type idLine struct {
	text string
	ids  []string
}

// idMark stands in for an id suffix in a normalised line.
const idMark = "<id>"

var (
	// idSuffixRE matches an id's suffix: a dash and eight lowercase letters
	// or digits at the end of a label. Minted suffixes use a narrower
	// alphabet, but ids written by hand (and every fixture here) use any, and
	// an ordinary hyphenated word that happens to match pairs with itself.
	idSuffixRE = regexp.MustCompile(`\b([a-z][a-z0-9]*(?:[-.][a-z0-9]+)*)-([a-z0-9]{8})\b`)
	hexRE      = regexp.MustCompile(`\b[0-9a-f]{7,64}\b`)
	// No word boundary after a timestamp: markdown output puts `_` (a word
	// character) right after the Z in `_as of …Z_`.
	timeRE     = regexp.MustCompile(`\b\d{4}-\d\d-\d\d[T ]\d\d:\d\d(?::\d\d(?:\.\d+)?)?(?:Z|[+-]\d\d:?\d\d)?`)
	dateRE     = regexp.MustCompile(`\b\d{4}-\d\d-\d\d\b`)
	durationRE = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ns|µs|us|ms|s)\b`)
)

// newNormaliser takes the forms the temporary root may be printed in: on
// macOS the temporary directory is under a symlink (/var is /private/var),
// and a program may print either path.
func newNormaliser(repos ...string) *normaliser {
	sort.Slice(repos, func(i, j int) bool { return len(repos[i]) > len(repos[j]) })
	return &normaliser{repos: repos}
}

func (n *normaliser) line(l string) idLine {
	for _, r := range n.repos {
		l = strings.ReplaceAll(l, r, "<repo>")
	}
	var ids []string
	l = idSuffixRE.ReplaceAllStringFunc(l, func(m string) string {
		sub := idSuffixRE.FindStringSubmatch(m)
		ids = append(ids, sub[2])
		return sub[1] + "-" + idMark
	})
	l = timeRE.ReplaceAllString(l, "<time>")
	l = dateRE.ReplaceAllString(l, "<date>")
	l = hexRE.ReplaceAllString(l, "<hex>")
	l = durationRE.ReplaceAllString(l, "<dur>")
	return idLine{text: strings.TrimRight(l, " \t"), ids: ids}
}

// idPairs is the page's id suffixes paired with the ones ds really printed
// in the same places. It must stay one-to-one: a page that shows one id in
// two places where ds printed two different ids fails, and so does the
// reverse.
type idPairs struct {
	toGot, toWant map[string]string
}

func newIDPairs() idPairs {
	return idPairs{toGot: map[string]string{}, toWant: map[string]string{}}
}

// fits reports whether pairing w with g keeps the pairs one-to-one.
func (p idPairs) fits(w, g string) bool {
	if have, ok := p.toGot[w]; ok && have != g {
		return false
	}
	if have, ok := p.toWant[g]; ok && have != w {
		return false
	}
	return true
}

// with returns a copy of p with w paired to g; the copy is what lets a
// failed alignment be abandoned without undoing anything.
func (p idPairs) with(w, g string) idPairs {
	q := newIDPairs()
	for k, v := range p.toGot {
		q.toGot[k], q.toWant[v] = v, k
	}
	q.toGot[w], q.toWant[g] = g, w
	return q
}

// translate rewrites every id in text whose suffix the page has already
// shown into the suffix ds printed in its place. Suffixes the page has not
// shown yet are left alone.
func (p idPairs) translate(text string) string {
	return idSuffixRE.ReplaceAllStringFunc(text, func(m string) string {
		sub := idSuffixRE.FindStringSubmatch(m)
		if got, ok := p.toGot[sub[2]]; ok {
			return sub[1] + "-" + got
		}
		return m
	})
}

// lineFits compares one want line with one got line: equal text (or, for a
// line ending in the ellipsis, the got line starts with the rest), and ids
// that pair consistently. It returns the pairs extended by this line.
func lineFits(w, g idLine, p idPairs) (idPairs, bool) {
	if pre, ok := strings.CutSuffix(w.text, ellipsis); ok {
		if !strings.HasPrefix(g.text, pre) {
			return p, false
		}
	} else if w.text != g.text {
		return p, false
	}
	for i, id := range w.ids {
		if !p.fits(id, g.ids[i]) {
			return p, false
		}
		p = p.with(id, g.ids[i])
	}
	return p, true
}

// match reports whether got satisfies want, returning the id pairs that
// alignment implies. A want line that is only the ellipsis matches any run
// of lines (including none), and the lines it skips pair no ids; a want
// line ending in it matches any line with that prefix.
func match(want, got []idLine, p idPairs) (idPairs, bool) {
	if len(want) == 0 {
		return p, len(got) == 0
	}
	if strings.TrimSpace(want[0].text) == ellipsis {
		for i := 0; i <= len(got); i++ {
			if q, ok := match(want[1:], got[i:], p); ok {
				return q, true
			}
		}
		return p, false
	}
	if len(got) == 0 {
		return p, false
	}
	q, ok := lineFits(want[0], got[0], p)
	if !ok {
		return p, false
	}
	return match(want[1:], got[1:], q)
}

// trimBlank drops trailing empty lines, which a page's layout adds freely.
func trimBlank(ls []string) []string {
	for len(ls) > 0 && strings.TrimSpace(ls[len(ls)-1]) == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}

// runner executes one page's steps.
type runner struct {
	root, cwd string
	env       []string
	shell     string
	out       io.Writer
	wantN     *normaliser
	gotN      *normaliser
	ids       idPairs
}

// result is one page's outcome.
type result struct {
	passed, failed, skipped int
}

// runPage runs every step of a page and reports each failure to out.
func runPage(name, page, tmp string, env []string, shell string, out io.Writer) (result, error) {
	steps, err := parse(page)
	if err != nil {
		return result{}, fmt.Errorf("%s: %w", name, err)
	}
	root, err := os.MkdirTemp(tmp, "doctest-")
	repo := filepath.Join(root, repoDirName)
	if err == nil {
		defer os.RemoveAll(root)
		err = os.Mkdir(repo, 0o755)
	}
	if err != nil {
		return result{}, err
	}
	resolved, _ := filepath.EvalSymlinks(root)
	r := &runner{root: root, cwd: repo, env: env, shell: shell, out: out, wantN: newNormaliser(), gotN: newNormaliser(root, resolved), ids: newIDPairs()}
	var res result
	for _, s := range steps {
		switch s.kind {
		case stepSkip:
			res.skipped++
			fmt.Fprintf(out, "  SKIP  %s:%d  %s\n", name, s.line, s.reason)
		case stepFile:
			s.body = r.ids.translate(s.body)
			if err := r.write(s); err != nil {
				res.failed++
				fmt.Fprintf(out, "  FAIL  %s:%d  writing %s: %v\n", name, s.line, s.path, err)
			}
		case stepHidden:
			if _, err := r.run(r.ids.translate(s.body)); err != nil {
				res.failed++
				fmt.Fprintf(out, "  FAIL  %s:%d  hidden setup %q: %v\n", name, s.line, s.body, err)
			}
		case stepCommand:
			got, err := r.run(r.ids.translate(s.body))
			if err != nil {
				res.failed++
				fmt.Fprintf(out, "  FAIL  %s:%d  $ %s: %v\n", name, s.line, firstLine(s.body), err)
				continue
			}
			want := trimBlank(s.want)
			gotLines := trimBlank(strings.Split(strings.TrimRight(got, "\n"), "\n"))
			nw := make([]idLine, len(want))
			for i, l := range want {
				nw[i] = r.wantN.line(l)
			}
			ng := make([]idLine, len(gotLines))
			for i, l := range gotLines {
				ng[i] = r.gotN.line(l)
			}
			if p, ok := match(nw, ng, r.ids); ok {
				r.ids = p
				res.passed++
				continue
			}
			res.failed++
			fmt.Fprintf(out, "  FAIL  %s:%d  $ %s\n        want:\n%s        got:\n%s", name, s.line, firstLine(s.body), indent(want), indent(gotLines))
		}
	}
	return res, nil
}

// write performs a file step.
func (r *runner) write(s step) error {
	p := filepath.Join(r.cwd, filepath.FromSlash(s.path))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if s.append {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(p, flags, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(s.body)
	return errors.Join(err, f.Close())
}

// run performs a command, handling cd and export itself so they persist.
// It returns the combined output; the error is non-nil only when the shell
// could not run it at all or (for cd) the directory does not exist. A
// command's own non-zero exit is not an error here: its output is compared.
func (r *runner) run(cmd string) (string, error) {
	t := strings.TrimSpace(cmd)
	if dir, ok := strings.CutPrefix(t, "cd "); ok && !strings.ContainsAny(dir, ";&|") {
		next := filepath.Join(r.cwd, filepath.FromSlash(strings.TrimSpace(dir)))
		if fi, err := os.Stat(next); err != nil || !fi.IsDir() {
			return "", fmt.Errorf("cd %s: not a directory", dir)
		}
		r.cwd = next
		return "", nil
	}
	if kv, ok := strings.CutPrefix(t, "export "); ok && strings.Contains(kv, "=") {
		k, v, _ := strings.Cut(kv, "=")
		r.env = append(r.env, k+"="+os.ExpandEnv(strings.Trim(v, `"'`)))
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, r.shell, "-c", cmd)
	c.Dir = r.cwd
	c.Env = r.env
	b, err := c.CombinedOutput()
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		err = nil
	}
	return string(b), err
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

func indent(ls []string) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString("          ")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}

// pageEnv is the environment every page runs in: the built binaries first
// on PATH, git with no user or system config but a fixed identity, no CI
// variables (ds behaves differently under CI on purpose), UTC, and
// DOCSYNC_ROOT so a page can point a Go program at this checkout.
func pageEnv(bin, root string, base []string) []string {
	drop := map[string]bool{"CI": true, "GITHUB_EVENT_PATH": true, "GITHUB_TOKEN": true, "GITHUB_REPOSITORY": true, "GITHUB_API_URL": true, "GITHUB_SERVER_URL": true, "GIT_DIR": true, "GIT_WORK_TREE": true}
	var env []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if drop[k] || k == "PATH" {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=docs", "GIT_AUTHOR_EMAIL=docs@example.com",
		"GIT_COMMITTER_NAME=docs", "GIT_COMMITTER_EMAIL=docs@example.com",
		"TZ=UTC", "DOCSYNC_ROOT="+root,
	)
}

// pages lists the markdown files to run: the arguments, or every .md in dir.
func pages(dir string, args []string) ([]string, error) {
	if len(args) > 0 {
		return args, nil
	}
	m, err := filepath.Glob(filepath.Join(dir, "*.md"))
	sort.Strings(m)
	return m, err
}

// run is main without the process: it returns the exit code.
func run(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("doctest", flag.ContinueOnError)
	fs.SetOutput(out)
	bin := fs.String("bin", "bin", "directory holding the built ds")
	dir := fs.String("dir", "docs/guide", "directory of pages to run when none are named")
	root := fs.String("root", ".", "the docsync checkout, exported to pages as DOCSYNC_ROOT")
	shell := fs.String("shell", "sh", "shell that runs each command")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	absBin, _ := filepath.Abs(*bin)
	absRoot, _ := filepath.Abs(*root)
	files, err := pages(*dir, fs.Args())
	if err != nil || len(files) == 0 {
		fmt.Fprintf(out, "doctest: no pages to run in %s (%v)\n", *dir, err)
		return 2
	}
	var total result
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(out, "doctest: %v\n", err)
			return 2
		}
		res, err := runPage(filepath.Base(f), string(raw), "", pageEnv(absBin, absRoot, os.Environ()), *shell, out)
		if err != nil {
			fmt.Fprintf(out, "doctest: %v\n", err)
			return 2
		}
		fmt.Fprintf(out, "%-6s %s: %d passed, %d failed, %d skipped\n", status(res), filepath.Base(f), res.passed, res.failed, res.skipped)
		total.passed += res.passed
		total.failed += res.failed
		total.skipped += res.skipped
	}
	fmt.Fprintf(out, "\n  ---- %d passed, %d failed ----\n", total.passed, total.failed)
	if total.skipped > 0 {
		fmt.Fprintf(out, "  (%d blocks skipped, each with its reason above)\n", total.skipped)
	}
	if total.failed > 0 {
		return 1
	}
	return 0
}

func status(r result) string {
	if r.failed > 0 {
		return "FAIL"
	}
	return "ok"
}

// exit is os.Exit, replaceable so a test can run main to completion; the
// same seam cli.Exit gives the ds binary.
var exit = os.Exit

func main() {
	w := bufio.NewWriter(os.Stdout)
	code := run(os.Args[1:], w)
	_ = w.Flush()
	exit(code)
}
