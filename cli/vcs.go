package cli

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/match"

	"github.com/ubgo/docsync/ledger"
)

// VCS is what the CLI needs from version control: the current commit, a
// file at a commit, and whether a commit exists. Git is the only built-in;
// the interface exists so tests and other hosts can supply their own.
type VCS interface {
	Head() (string, error)
	Show(commit, path string) ([]byte, error)
	Exists(commit string) bool
	// User is the configured author name, for the ack log's actor.
	User() string
	// Churn is commits touching each file over recent history, for
	// `report --unmarked`; the count is a ranking signal, not an audit.
	Churn() (map[string]int, error)
	// Branch is the current branch name, for publish's default-branch rule.
	Branch() (string, error)
	// RemoteURL is origin's URL, which names the repo in a workspace.
	RemoteURL() string
	// Clone, Pull, and Push manage the workspace index checkout.
	Clone(url, dir string) error
	Pull(dir string) error
	Push(dir, message string) error
	// Staged lists files in the index, for `impact --staged`.
	Staged() ([]string, error)
	// Message returns a commit's message, for `ack --from-commit`.
	Message(commit string) (string, error)
}

// endOfOptions ends git's option parsing, so the arguments after it are read
// as values even when they start with "-".
const endOfOptions = "--end-of-options"

// churnCommits bounds the history `Churn` reads so a large repository
// answers in a moment; older history rarely changes the ranking.
const churnCommits = "500"

// ErrNoVCS is returned by Git when the directory is not a repository or git
// is not installed; commands degrade to no permalinks and no diffs.
var ErrNoVCS = errors.New("git: not a repository or git not installed")

// gitTimeout bounds each git call so a hung credential helper cannot hang
// `check`.
const gitTimeout = 20 * time.Second

// Git shells out to the `git` program, which must be on PATH, 2.24 or later
// for --end-of-options. Documented at the point of use per the library
// rules; the root module never does this.
//
// Every value that is not a flag — a commit, a path, a URL — goes after
// --end-of-options or "--". They come from committed files a pull request
// can edit (the ledger header's commit, an at= in a doc, the workspace URL),
// and git reads anything starting with "-" as an option: a commit of
// `--output=<path>` made `git show` write that file.
type Git struct {
	Dir string
}

func (g Git) run(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = g.Dir
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.Join(ErrNoVCS, errors.New(strings.TrimSpace(stderr.String())))
	}
	return out.Bytes(), nil
}

// Head returns the short sha of HEAD.
func (g Git) Head() (string, error) {
	out, err := g.run("rev-parse", "--short=7", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Show returns path as of commit.
func (g Git) Show(commit, path string) ([]byte, error) {
	return g.run("show", endOfOptions, commit+":"+path)
}

// Exists reports whether commit resolves.
func (g Git) Exists(commit string) bool {
	_, err := g.run("cat-file", "-e", endOfOptions, commit+"^{commit}")
	return err == nil
}

// User returns git's user.name, or "" when unset.
func (g Git) User() string {
	out, err := g.run("config", "user.name")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Churn counts commits per file over the last churnCommits commits.
func (g Git) Churn() (map[string]int, error) {
	out, err := g.run("log", "--format=", "--name-only", "-n", churnCommits)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			counts[l]++
		}
	}
	return counts, nil
}

// Branch returns the current branch, or an error on a detached HEAD.
func (g Git) Branch() (string, error) {
	out, err := g.run("symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// RemoteURL returns origin's URL or "".
func (g Git) RemoteURL() string {
	out, err := g.run("remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Clone clones url into dir.
func (g Git) Clone(url, dir string) error {
	_, err := g.run("clone", "--quiet", "--", url, dir)
	return err
}

// Pull fast-forwards the checkout at dir.
func (g Git) Pull(dir string) error {
	_, err := Git{Dir: dir}.run("pull", "--quiet", "--ff-only")
	return err
}

// Push commits everything under dir and pushes. A clean tree is not an
// error: publishing the same ledger twice is a no-op.
func (g Git) Push(dir, message string) error {
	committed, err := commitAll(Git{Dir: dir}, message)
	if err != nil || !committed {
		return err
	}
	_, err = Git{Dir: dir}.run("push", "--quiet")
	return err
}

// IndexCommitter is the optional VCS upgrade `publish` uses for an index
// that is a local directory rather than a managed clone (bug 100). Writing
// the files and stopping left them untracked in an index that is itself a
// git repository, so every later publish piled onto one uncommitted change
// and nothing recorded which repository wrote what, at which commit. A VCS
// that does not implement it is left alone: the files are written and
// nothing else happens.
type IndexCommitter interface {
	// CommitIndex commits everything under dir when dir is the top of its
	// own git work tree, and reports whether it made a commit. A directory
	// that is not a work tree's top -- a plain directory, or one nested
	// inside an unrelated repository -- is not committed and is not an
	// error: committing there would sweep up the enclosing repository's
	// unrelated changes.
	CommitIndex(dir, message string) (bool, error)
}

// CommitIndex implements IndexCommitter.
func (g Git) CommitIndex(dir, message string) (bool, error) {
	in := Git{Dir: dir}
	out, err := in.run("rev-parse", "--show-toplevel")
	if err != nil {
		return false, nil
	}
	top, err1 := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	here, err2 := filepath.Abs(dir)
	if err2 == nil {
		here, err2 = filepath.EvalSymlinks(here)
	}
	if err1 != nil || err2 != nil || top != here {
		return false, nil
	}
	return commitAll(in, message)
}

// CommitsBetween implements CommitCounter: the number of commits reachable
// from to and not from from. An unknown commit -- one this clone never
// fetched -- is an error, so the caller says nothing rather than guess.
func (g Git) CommitsBetween(from, to string) (int, error) {
	out, err := g.run("rev-list", "--count", endOfOptions, from+".."+to)
	if err != nil {
		return 0, err
	}
	// rev-list --count prints one integer; anything else reads as 0, which
	// is "not behind" and so never a false warning.
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n, nil
}

// commitAll stages and commits everything in g's tree; a clean tree commits
// nothing and is not an error.
func commitAll(g Git, message string) (bool, error) {
	if _, err := g.run("add", "-A"); err != nil {
		return false, err
	}
	if out, err := g.run("status", "--porcelain"); err != nil || strings.TrimSpace(string(out)) == "" {
		return false, err
	}
	if _, err := g.run("commit", "--quiet", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

// Staged lists paths staged for the next commit.
func (g Git) Staged() ([]string, error) {
	out, err := g.run("diff", "--cached", "--name-only")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// Message returns the full commit message.
func (g Git) Message(commit string) (string, error) {
	out, err := g.run("log", "-1", "--format=%B", endOfOptions, commit)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// oldContent answers the library's OldContent hook at the previous ledger's
// commit: it reads the row's file as it was then and runs it through the scan
// pipeline (extractFile), so the previous body comes back in exactly the form
// the current one has. It pairs by id, environment and branch -- the key the
// match step pairs by -- so one environment is never answered with another's
// body. Failures mean "unknown", which the library reports as a body change
// without a diff.
//
// It used to cut the row's raw lines out of the old file. That compared two
// different things: a config value changed from 443 to 8443 was diffed as the
// whole line, directive comment included, against the bare value, and a
// secret's old value was returned unredacted and printed in the diff.
func oldContent(v VCS, prev ledger.Ledger, extractFile func(path string, src []byte) ([]block.Block, error)) func(row ledger.Row) (string, bool) {
	cache := map[string]map[string]string{}
	return func(row ledger.Row) (string, bool) {
		if prev.Header.Commit == "" {
			return "", false
		}
		bodies, cached := cache[row.File]
		if !cached {
			bodies = map[string]string{}
			if src, err := v.Show(prev.Header.Commit, row.File); err == nil {
				if defs, err := extractFile(row.File, src); err == nil {
					for _, b := range defs {
						bodies[match.BlockKey(b)] = b.Content
					}
				}
			}
			cache[row.File] = bodies
		}
		body, ok := bodies[match.RowKey(row)]
		return body, ok
	}
}
