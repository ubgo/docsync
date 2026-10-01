package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/procplugin"
)

// Prober is an optional upgrade of VCS: it answers whether a remote can be
// reached without fetching anything, which is what `ds doctor` asks of the
// workspace index (SPEC §22). A VCS without it is not forced to pretend; the
// doctor row says reachability was not checked instead.
type Prober interface {
	Reachable(url string) error
}

// The environment that stops git from prompting during a probe: a doctor
// run in CI or a hook has no terminal, and a credential or host-key prompt
// would hang it until the timeout instead of reporting the remote as
// unreachable.
const (
	envNoTerminalPrompt = "GIT_TERMINAL_PROMPT=0"
	envSSHCommand       = "GIT_SSH_COMMAND"
	sshBatchMode        = "ssh -o BatchMode=yes"
)

// Reachable asks the remote for its HEAD with `git ls-remote`, never
// prompting. It reads nothing into the working tree and writes nothing.
func (g Git) Reachable(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--quiet", endOfOptions, url, "HEAD")
	cmd.Dir = g.Dir
	cmd.Env = append(os.Environ(), envNoTerminalPrompt)
	if os.Getenv(envSSHCommand) == "" {
		cmd.Env = append(cmd.Env, envSSHCommand+"="+sshBatchMode)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// git's last line is the one that says why ("Could not resolve
		// host"); a doctor row is one line, so the rest is dropped.
		lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
		why := strings.TrimPrefix(lines[len(lines)-1], "fatal: ")
		if why == "" {
			why = err.Error()
		}
		return errors.New(why)
	}
	return nil
}

// Doctor row names for the workspace checks.
const (
	rowWorkspace = "workspace"
	rowIndex     = "index"
	rowResolve   = "resolve "
)

// workspaceRows are doctor's answer to "will a cross-repo check work here":
// whether a workspace is configured, and when one is, whether its index can
// be read -- a local directory that parses, or a remote that answers. A
// remote that does not answer is a warning while a cached copy exists,
// because check then runs against that copy, and a failure without one,
// because check cannot run at all.
func (a *App) workspaceRows(cfg config.Config) [][]string {
	if cfg.Workspace == "" {
		return [][]string{{rowWorkspace, doctorOK, "none; this repository is its own workspace"}}
	}
	rows := [][]string{{rowWorkspace, doctorOK, cfg.Workspace}}
	dir, managed := a.indexPath(cfg)
	if !managed {
		st, err := a.syncIndex(cfg, false)
		if err != nil {
			return append(rows, []string{rowIndex, doctorFail, err.Error()})
		}
		return append(rows, []string{rowIndex, doctorOK, fmt.Sprintf("local directory %s, %d repos published", dir, len(st.Entries))})
	}
	cached := ""
	if _, err := os.Stat(dir); err == nil {
		cached = "; cached copy at " + filepath.ToSlash(filepath.Join(DirName, IndexDir))
	}
	prober, ok := a.vcs.(Prober)
	if !ok {
		return append(rows, []string{rowIndex, doctorWarn, "reachability not checked: the version control in use cannot probe a remote" + cached})
	}
	err := prober.Reachable(cfg.Workspace)
	switch {
	case err == nil:
		return append(rows, []string{rowIndex, doctorOK, "reachable" + cached})
	case cached != "":
		return append(rows, []string{rowIndex, doctorWarn, fmt.Sprintf("unreachable (%v); check uses the cached copy", err)})
	default:
		return append(rows, []string{rowIndex, doctorFail, fmt.Sprintf("unreachable (%v) and no cached copy; check cannot run until `%s sync` succeeds", err, a.name)})
	}
}

// resolverRows check each provider in resolve.providers for its plugin on
// PATH. A login cannot be tested without asking the provider about a real
// address, so this is what doctor can promise: a missing plugin is found
// before `check --resolve` reports every address at that provider as
// unverifiable. With no providers listed there is nothing to check and no
// row.
func (a *App) resolverRows(cfg config.Config) [][]string {
	lookPath := a.pluginLookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	var rows [][]string
	for _, p := range cfg.Resolve.Providers {
		name := procplugin.Name(procplugin.KindResolve, resolverPlugin(p))
		if _, err := lookPath(name); err == nil {
			rows = append(rows, []string{rowResolve + p, doctorOK, name + " is on PATH"})
		} else {
			rows = append(rows, []string{rowResolve + p, doctorWarn, name + " is not on PATH; check --resolve reports its addresses unverifiable"})
		}
	}
	return rows
}
