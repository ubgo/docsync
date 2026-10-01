package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/render"
	"github.com/ubgo/docsync/scan"
)

// `ds:run` execution (§9.4): only under `check --run`, only where
// run.enabled is true, `cmd=` only in docs matching run.allow, `id=` only
// with runnable=true. Results are kept in .ds/runs.json so `render` can show
// the command with its last outcome. This is code execution from a text
// file and is treated as such: every guard here is deliberate.
const (
	RunsFile = "runs.json"
	// runOutputCap bounds what is kept from a command's output.
	runOutputCap = 64 << 10
	// runWaitDelay bounds how long a timed-out command's output is waited
	// for after the shell is killed.
	runWaitDelay = time.Second
	// Expectations (§9.4 expect=).
	expectOK   = "ok"
	expectRows = "rows"
	// Directive keys a run reads beyond what it executes.
	runKeyExpect  = "expect"
	runKeyTimeout = "timeout"
	// httpStatusLine starts a status line in output such as `curl -sI`
	// prints: `HTTP/1.1 200 OK`, `HTTP/2 404`.
	httpStatusLine = "HTTP/"
)

// RunRecord is one stored outcome.
type RunRecord struct {
	Command string    `json:"command"`
	Output  string    `json:"output"`
	OK      bool      `json:"ok"`
	Expect  string    `json:"expect,omitempty"`
	At      time.Time `json:"at"`
}

// runKey keys records by doc line.
func runKey(doc string, line int) string { return doc + ":" + strconv.Itoa(line) }

// LoadRuns reads .ds/runs.json; missing means empty.
func (s *Store) LoadRuns() (map[string]RunRecord, error) {
	raw, ok, err := s.read(RunsFile)
	if err != nil || !ok {
		return map[string]RunRecord{}, err
	}
	out := map[string]RunRecord{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", RunsFile, err)
	}
	return out, nil
}

// SaveRuns writes the records.
func (s *Store) SaveRuns(runs map[string]RunRecord) error {
	raw, _ := json.MarshalIndent(runs, "", "  ")
	return s.Write(RunsFile, raw)
}

// runsFor converts stored records into what render wants for one doc.
func runsFor(runs map[string]RunRecord, doc string) map[int]render.RunResult {
	out := map[int]render.RunResult{}
	for key, r := range runs {
		d, l, ok := strings.Cut(key, ":")
		if !ok || d != doc {
			continue
		}
		n, err := strconv.Atoi(l)
		if err != nil {
			continue
		}
		out[n] = render.RunResult{Output: r.Output, OK: r.OK, At: r.At}
	}
	return out
}

// executeRuns runs every ds:run reference in the report's scan, records the
// outcomes in .ds/runs.json, and returns one result per directive for the
// report (check.ApplyRuns), so a failed run is a finding and not only a
// line of progress output.
func (a *App) executeRuns(ctx context.Context, ld loaded, rep docsync.Report, env string, out io.Writer) ([]check.RunResult, error) {
	cfg := ld.sys.Config()
	if !cfg.Run.Enabled {
		fmt.Fprintln(out, "run.enabled is false; nothing executed")
		return nil, nil
	}
	runs, err := ld.st.LoadRuns()
	if err != nil {
		return nil, err
	}
	// run.timeout is validated when the config is loaded; empty means the default.
	timeout, _ := time.ParseDuration(orDefault(cfg.Run.Timeout, config.DefaultRunTimeout))
	if env == "" {
		env = cfg.Env.Default
	}
	shell := orDefault(cfg.Run.Shell, config.DefaultRunShell)
	var results []check.RunResult
	for _, ref := range rep.Scan.Refs {
		if ref.Verb != extract.VerbRun {
			continue
		}
		command, argv, why := a.runCommand(ld, rep.Scan, ref, cfg.Run.Allow, shell)
		if command == "" {
			fmt.Fprintf(out, "%s:%d  run skipped: %s\n", ref.Pos.File, ref.Pos.Start, why)
			results = append(results, check.RunResult{Doc: ref.Pos.File, Line: ref.Pos.Start, Skipped: why})
			continue
		}
		// Asked once something is about to run: a repository with nothing
		// runnable does not need a shell.
		if err := requireShell(shell); err != nil {
			return results, err
		}
		refEnv := ref.Args[block.KeyEnv]
		if refEnv == "" {
			refEnv = env
		}
		limit := timeout
		if t := ref.Args[runKeyTimeout]; t != "" {
			// timeout= on the directive wins over run.timeout; it was
			// accepted and never applied (bug 86). check reports a value
			// that does not parse as a problem, and it is not run here.
			d, err := check.ParseRunTimeout(t)
			if err != nil {
				// Not run, so not a failed run: the check reports the
				// value as a problem on its own.
				fmt.Fprintf(out, "%s:%d  run skipped: %v\n", ref.Pos.File, ref.Pos.Start, err)
				results = append(results, check.RunResult{Doc: ref.Pos.File, Line: ref.Pos.Start, Skipped: err.Error()})
				continue
			}
			limit = d
		}
		rec := a.runOne(ctx, command, argv, refEnv, cfg.Run.Env[refEnv], limit, ref.Args[runKeyExpect])
		runs[runKey(ref.Pos.File, ref.Pos.Start)] = rec
		status := "ok"
		if !rec.OK {
			status = "FAILED"
		}
		results = append(results, check.RunResult{Doc: ref.Pos.File, Line: ref.Pos.Start, Command: command, Failed: !rec.OK})
		fmt.Fprintf(out, "%s:%d  run %s: %s\n", ref.Pos.File, ref.Pos.Start, status, command)
	}
	if err := ld.st.SaveRuns(runs); err != nil {
		return results, err
	}
	return results, nil
}

// ErrNoShell is returned when a ds:run or the [review] command is about to
// execute and the shell it runs under is not on PATH. It is an error rather
// than a skip: a check that was asked to run commands and ran none must not
// look like one whose commands passed.
var ErrNoShell = errors.New("the shell is not on PATH")

// requireShell reports ErrNoShell, naming the shell and the two ways out.
func requireShell(shell string) error {
	if _, err := exec.LookPath(shell); err != nil {
		return fmt.Errorf("%w: %s (on Windows, Git for Windows provides sh; or name another shell with [run] shell in %s/%s)", ErrNoShell, shell, DirName, ConfigFile)
	}
	return nil
}

// runCommand resolves what a ds:run executes -- the command as it is shown
// and recorded, and the argv that runs it under shell -- or why it must not.
func (a *App) runCommand(ld loaded, res scan.Result, ref block.Reference, allow []string, shell string) (string, []string, string) {
	inline := func(command string) (string, []string, string) {
		return command, []string{shell, "-c", command}, ""
	}
	switch {
	case ref.ID != "":
		b, ok := ld.sys.LocateID(res, ref.ID)
		if !ok {
			return "", nil, ref.ID + " is not defined"
		}
		if !b.IsRunnable() {
			return "", nil, ref.ID + " is not runnable=true"
		}
		return inline(strings.TrimSpace(b.Content))
	case ref.Args["cmd"] != "":
		if why := allowedToRun(allow, ref, "cmd="); why != "" {
			return "", nil, why
		}
		return inline(ref.Args["cmd"])
	case ref.Args["file"] != "":
		// A script is code as much as cmd= is, so it takes the same gate.
		// It had none, and its path went into the shell line unquoted: any
		// doc, run.allow or not, could write file="x; <anything>" and have
		// CI execute it.
		if why := allowedToRun(allow, ref, "file="); why != "" {
			return "", nil, why
		}
		file := ref.Args["file"]
		if why := a.runnableFile(file); why != "" {
			return "", nil, why
		}
		// The script is an argument of its own, never part of a command
		// line: nothing in its name is read by the shell. The quoted form is
		// what is shown and recorded.
		return shell + " " + shellQuote(file), []string{shell, file}, ""
	}
	return "", nil, "needs one of id=, cmd=, file="
}

// allowedToRun reports why a doc may not run what it names, or "" when it
// matches run.allow.
func allowedToRun(allow []string, ref block.Reference, what string) string {
	ok, err := scan.MatchAny(allow, ref.Pos.File)
	if err != nil {
		return "run.allow: " + err.Error()
	}
	if !ok {
		return what + " is allowed only in docs matching run.allow"
	}
	return ""
}

// runnableFile reports why file may not be run as a script, or "": it must
// be a clean path inside the repository — relative, no "..", not starting
// with "-" where sh would read it as an option — naming a regular file that
// is not a symlink, which could point outside.
func (a *App) runnableFile(file string) string {
	clean := filepath.ToSlash(filepath.Clean(file))
	// file= is text committed in a doc, the same on every platform, so what
	// counts as rooted is decided by the text and not by the host: a leading
	// slash of either kind, or a drive (`C:`). filepath.IsAbs alone let
	// file="/etc/hosts" through on Windows, where that is not "absolute".
	if file == "" || filepath.IsAbs(file) || strings.HasPrefix(file, "/") || strings.HasPrefix(file, `\`) || hasDrive(file) || strings.HasPrefix(file, "-") || clean != filepath.ToSlash(file) || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Sprintf("file=%q must be a clean path inside the repository", file)
	}
	info, err := os.Lstat(filepath.Join(a.dir, filepath.FromSlash(clean)))
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Sprintf("file=%q is not a regular file in the repository", file)
	}
	return ""
}

// hasDrive reports whether p begins with a drive, `C:`, whatever the host.
func hasDrive(p string) bool {
	return len(p) >= 2 && p[1] == ':' && (p[0]|0x20 >= 'a' && p[0]|0x20 <= 'z')
}

// shellQuote makes s one word for sh: wrapped in single quotes, with each
// single quote closed, escaped, and reopened. Nothing inside is expanded.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runOne executes a command under the shell with a timeout and the
// configured environment, and judges it against expect=.
func (a *App) runOne(ctx context.Context, command string, argv []string, envName string, extra map[string]string, timeout time.Duration, expect string) RunRecord {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = a.dir
	// Killing the shell at the deadline does not close the output pipe a
	// child it started still holds; without a wait limit the run would
	// still last as long as that child.
	cmd.WaitDelay = runWaitDelay
	cmd.Env = os.Environ()
	for k, v := range extra {
		cmd.Env = append(cmd.Env, k+"="+os.ExpandEnv(v))
	}
	if envName != "" {
		cmd.Env = append(cmd.Env, "DS_ENV="+envName)
	}
	raw, err := cmd.CombinedOutput()
	if len(raw) > runOutputCap {
		raw = raw[:runOutputCap]
	}
	output := string(raw)
	rec := RunRecord{Command: command, Output: output, Expect: expect, At: a.now()}
	exitOK := err == nil
	if ctx.Err() != nil {
		rec.Output += "\n(timed out after " + timeout.String() + ")"
		exitOK = false
	}
	rec.OK = exitOK && meetsExpect(output, expect)
	return rec
}

// meetsExpect judges a run's output against expect= (§9.4), for a command
// that already exited zero: every mode requires that. The substring mode
// used to ignore the exit status, so a command that printed the expected
// text and then failed passed (bug 86).
//   - "" or ok: nothing more.
//   - rows: some output.
//   - an HTTP status (three digits, 100 to 599): the last status the output
//     reports is that code. The last `HTTP/<version> <code>` line counts
//     when there is one, as `curl -sIL` prints after redirects; otherwise
//     the last non-blank line, trimmed, as `curl -s -o /dev/null -w
//     '%{http_code}'` prints. This mode was documented and did not exist.
//   - anything else: a substring the output must contain.
func meetsExpect(output, expect string) bool {
	want := strings.Trim(expect, `"'`)
	switch want {
	case "", expectOK:
		return true
	case expectRows:
		return strings.TrimSpace(output) != ""
	}
	if code, ok := check.ParseHTTPStatus(want); ok {
		return reportedStatus(output) == strconv.Itoa(code)
	}
	return strings.Contains(output, want)
}

// reportedStatus is the last HTTP status a command's output reports, as
// meetsExpect describes, or "" when it reports none.
func reportedStatus(output string) string {
	last, status := "", ""
	for _, l := range strings.Split(output, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		last = l
		if fields := strings.Fields(l); len(fields) >= 2 && strings.HasPrefix(fields[0], httpStatusLine) {
			status = fields[1]
		}
	}
	if status != "" {
		return status
	}
	return last
}
