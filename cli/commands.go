package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/match"
	"github.com/ubgo/docsync/scan"
)

// Flag names, so help text and tests share one spelling.
const (
	flagJSON        = "json"
	flagStrict      = "strict"
	flagEnv         = "env"
	flagRun         = "run"
	flagResolve     = "resolve"
	flagFull        = "full"
	flagAgents      = "agents"
	flagForce       = "force"
	flagOwner       = "owner"
	flagStability   = "stability"
	flagTags        = "tags"
	flagDesc        = "desc"
	flagLabel       = "label"
	flagDryRun      = "dry-run"
	flagApply       = "apply"
	flagDoc         = "doc"
	flagLine        = "line"
	flagNote        = "note"
	flagActor       = "actor"
	flagAgent       = "agent"
	flagDelegatedBy = "delegated-by"
	flagAll         = "all"
	flagBudget      = "budget"
	flagSince       = "since"
	flagMode        = "mode"
	flagCitedBy     = "cited-by"
	flagChain       = "chain"
	flagHistory     = "history"
	flagLines       = "lines"
	flagOut         = "out"
	flagAt          = "at"
	flagStaged      = "staged"
	flagFromCommit  = "from-commit"
	flagFile        = "file"
	flagTag         = "tag"
	flagGroup       = "group"
	flagExport      = "export"
	flagFix         = "fix"
	flagExplain     = "explain"
	flagExpand      = "expand"
)

func (a *App) initCmd() *cobra.Command {
	var agents, force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "write .ds/config.toml, empty ledgers, and a CI snippet",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st := NewStore(a.dir)
			exists := st.Exists()
			if exists && !force && !agents {
				return ErrExists
			}
			if root, ok := st.ancestorRoot(); ok && !exists && !force {
				return fmt.Errorf("%w (%s); run ds there, or pass --force to start a separate docsync root here", ErrNestedInit, root)
			}
			if exists && !force {
				// `init --agents` on an initialised repo only installs the
				// agent files; the config and ledgers stay as they are, and
				// the rules use the prefix that config chose.
				cfg, err := a.loadConfig(st)
				if err != nil {
					return err
				}
				return a.printAgentInstall(cmd, cfg.Prefix)
			}
			cfg := a.defaults()
			if _, err := os.Stat(filepath.Join(a.dir, "docs")); err != nil {
				cfg.Scan.Docs = []string{"**/*.md"}
			}
			commit, _ := a.vcs.Head()
			h := ledger.Header{Format: ledger.Format, Extract: extract.Rule, Repo: repoName(a.dir, cfg), Commit: commit, ScannedAt: a.now()}
			files := []struct {
				name string
				data []byte
			}{
				{ConfigFile, []byte(ConfigTOML(cfg))},
				{LedgerFile, ledger.Ledger{Header: h}.Bytes()},
				{RefsFile, ledger.Refs{Header: h}.Bytes()},
				{AcksFile, ledger.Acks{Header: h}.Bytes()},
				{CISnippet, []byte(ciSnippet)},
				{GitignoreFile, []byte(gitignoreBody)},
				{GitattributesFile, []byte(gitattributesBody)},
			}
			if agents {
				files = append(files, struct {
					name string
					data []byte
				}{AgentsFile, []byte(agentRules(cfg.Prefix))})
			}
			for _, f := range files {
				if err := st.Write(f.name, f.data); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "wrote %s/%s\n", DirName, f.name)
			}
			if agents {
				if err := a.printAgentInstall(cmd, cfg.Prefix); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "next: %s scan, then commit %s/\n", a.name, DirName)
			return nil
		},
	}
	cmd.Flags().BoolVar(&agents, flagAgents, false, "also write the CLAUDE.md and AGENTS.md rules, a Claude Code skill, the MCP registration, and a session-start hook")
	cmd.Flags().BoolVar(&force, flagForce, false, "overwrite an existing config")
	return cmd
}

// printAgentInstall runs the --agents steps and prints one line each.
func (a *App) printAgentInstall(cmd *cobra.Command, prefix string) error {
	lines, err := a.installAgentFiles(prefix)
	if err != nil {
		return err
	}
	for _, l := range lines {
		fmt.Fprintln(cmd.OutOrStdout(), l)
	}
	return nil
}

// MCPConfigFile is the repo-level MCP registration agents read (§26.10).
const MCPConfigFile = ".mcp.json"

// registerMCP writes .mcp.json when absent. An existing file is left alone:
// merging someone's editor config is not this tool's business.
func registerMCP(dir, name string) (bool, error) {
	p := filepath.Join(dir, MCPConfigFile)
	if _, err := os.Stat(p); err == nil {
		return false, nil
	}
	body := map[string]any{"mcpServers": map[string]any{mcpServerName: map[string]any{"command": name, "args": []string{"mcp"}}}}
	raw, _ := json.MarshalIndent(body, "", "  ")
	err := os.WriteFile(p, append(raw, '\n'), filePerm)
	return err == nil, err
}

// The statuses a `ds doctor` row can report. A FAIL row means a command will
// not work as things stand, so doctor exits ExitError when any row has one: a
// setup step or CI job that runs it must not pass over a broken repo. WARN
// is a degraded but working state and does not change the exit code.
const (
	doctorOK   = "ok"
	doctorWarn = "WARN"
	doctorFail = "FAIL"
)

// DoctorStatusValues is the canonical order.
var DoctorStatusValues = []string{doctorOK, doctorWarn, doctorFail}

func (a *App) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "check config, globs, extractors, ledger, and git",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			st := NewStore(a.dir)
			cfg, err := a.loadConfig(st)
			if err != nil {
				fmt.Fprintf(out, "config\t%s\t%v\n", doctorFail, err)
				return exitCode(ExitError)
			}
			rows := [][]string{{"config", doctorOK, fmt.Sprintf("spec %s, prefix %s", cfg.Spec, cfg.Prefix)}}
			for _, p := range append(append([]string{}, cfg.Scan.Code...), cfg.Scan.Docs...) {
				n, gerr := scan.CountMatches(os.DirFS(a.dir), p)
				switch {
				case gerr != nil:
					rows = append(rows, []string{"glob " + p, doctorFail, gerr.Error()})
				case n == 0:
					rows = append(rows, []string{"glob " + p, doctorWarn, "matches no files"})
				default:
					rows = append(rows, []string{"glob " + p, doctorOK, fmt.Sprintf("%d files", n)})
				}
			}
			rows = append(rows, []string{"extractors", doctorOK, strings.Join(a.tiers(), ", ")})
			if l, r, _, err := st.LoadState(); err != nil {
				rows = append(rows, []string{"ledger", doctorFail, err.Error()})
			} else if row := ruleRow(docsync.CheckRule(l, r)); row != nil {
				rows = append(rows, row)
			} else {
				rows = append(rows, []string{"ledger", doctorOK, fmt.Sprintf("format %d", ledger.Format)})
			}
			if head, err := a.vcs.Head(); err != nil {
				rows = append(rows, []string{"git", doctorWarn, "unavailable: no permalinks, no diffs"})
			} else {
				rows = append(rows, []string{"git", doctorOK, "HEAD " + head})
			}
			// A repo initialised before the ignore list existed keeps
			// committing machine-local state, which is how a secret block's
			// value reached git history (§12). `init` writes it; only doctor
			// can tell a repo that already exists.
			rows = append(rows, st.gitignoreRow(), st.gitattributesRow(), a.blocksRow(st), a.notifyStateRow(st))
			rows = append(append(rows, a.workspaceRows(cfg)...), a.resolverRows(cfg)...)
			for _, row := range rows {
				row[2] = a.cmdText(row[2])
			}
			table(out, rows)
			for _, row := range rows {
				if row[1] == doctorFail {
					return exitCode(ExitError)
				}
			}
			return nil
		},
	}
}

// ruleRow is doctor's row for docsync.CheckRule's answer: nil when the rule
// matches, FAIL for a newer rule (every other command refuses it), and WARN
// for an older one (the upgrade path, healed by a scan).
func ruleRow(err error) []string {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, docsync.ErrNewerRule):
		return []string{"ledger", doctorFail, err.Error()}
	default:
		return []string{"ledger", doctorWarn, err.Error() + "; run `ds scan`"}
	}
}

// noteOlderRule prints an older-rule answer from docsync.CheckRule through
// format, whose one verb is the error. A newer rule never reaches it: the
// system refuses to load before any command runs.
func noteOlderRule(w io.Writer, err error, format string) {
	if errors.Is(err, docsync.ErrOtherRule) {
		fmt.Fprintf(w, format+"\n", err)
	}
}

func (a *App) defCmd() *cobra.Command {
	var opts docsync.DefineOptions
	var dry, fix bool
	cmd := &cobra.Command{
		Use:   "def <file>#<symbol> | <file>:<line> | --fix",
		Short: "return the block's id, minting one and inserting the directive when it has none; --fix re-mints duplicated ids",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys, st := ld.sys, ld.st
			if fix {
				res, err := sys.Scan(cmd.Context())
				if err != nil {
					return err
				}
				r, err := sys.FixDuplicates(res)
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				for from, to := range r.Mapping {
					fmt.Fprintf(out, "%s -> %s\n", from, to)
				}
				if len(r.Edits) == 0 {
					fmt.Fprintln(out, "no duplicated ids")
					return nil
				}
				if dry {
					fmt.Fprintf(out, "%d def(s) would be re-minted (--dry-run)\n", len(r.Edits))
					return nil
				}
				if err := st.applyAll(WriteDef, r.Edits); err != nil {
					return err
				}
				fmt.Fprintf(out, "%d def(s) re-minted; run %s scan\n", len(r.Edits), a.name)
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("%w: def needs a target or --fix", ErrUsage)
			}
			target, err := a.repoTarget(args[0])
			if err != nil {
				return err
			}
			res, err := sys.Define(cmd.Context(), target, opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if res.Existing {
				fmt.Fprintln(out, res.ID)
				return nil
			}
			if dry {
				fmt.Fprintf(out, "%s\nwould insert at %s:%d:\n%s\n", res.ID, res.Edit.File, res.Edit.Line, res.Edit.New)
				return nil
			}
			if err := st.applyAll(WriteDef, []docsync.Edit{res.Edit}); err != nil {
				return err
			}
			fmt.Fprintln(out, res.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Owner, flagOwner, "", "owner=")
	cmd.Flags().StringVar(&opts.Stability, flagStability, "", "stability= (frozen|stable|api|volatile)")
	cmd.Flags().StringVar(&opts.Tags, flagTags, "", "tags= comma list")
	cmd.Flags().StringVar(&opts.Desc, flagDesc, "", "desc= one line")
	cmd.Flags().StringVar(&opts.Label, flagLabel, "", "id label; default derived from the symbol")
	cmd.Flags().StringVar(&opts.Env, flagEnv, "", "env=")
	cmd.Flags().BoolVar(&dry, flagDryRun, false, "print the edit instead of applying it")
	cmd.Flags().BoolVar(&fix, flagFix, false, "re-mint every def after the first for each duplicated id")
	return cmd
}

func (a *App) scanCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "rebuild the ledger and the reverse index",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys, st := ld.sys, ld.st
			res, err := sys.Scan(cmd.Context())
			if err != nil {
				return err
			}
			l, r := sys.Snapshot(res)
			if err := st.SaveLedgerSharded(l, r, ld.cfg.Ledger.Shard); err != nil {
				return err
			}
			// Said once, when it changes: the files are now under this
			// build's rule, and citations that report drift after this scan
			// may be the rule change rather than a text change.
			noteOlderRule(cmd.ErrOrStderr(), docsync.CheckRule(ld.prev, ld.refs), fmt.Sprintf("note: %%v; rewritten under rule %d", extract.Rule))
			// Bodies go in before the ledger is trusted as "the previous
			// scan": rewriting the ledger is exactly what used to lose the
			// evidence an open `unacked` finding rests on (§20.1).
			if err := st.WriteBodies(sys.Bodies(res)); err != nil {
				return err
			}
			if err := ld.flush(); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%d files, %d defs, %d refs, %d problems, %d skipped\n", res.Files, len(res.Defs), len(res.Refs), len(res.Problems), len(res.Skipped))
			printProblems(out, res.Problems)
			printUnreadable(cmd.ErrOrStderr(), res.Skipped, ld.prev, ld.refs)
			return nil
		},
	}
}

// printUnreadable names, on stderr, each file the scan could not read that
// held citations or blocks at the last scan — the same files check reports
// as unscanned. Such a file is not dropped, its state is kept, but a skip
// counted into a number nobody reads is how a doc and every citation in it
// went unchecked without anyone noticing.
//
// A file that never held anything is not named: on a Mac every directory
// has a binary .DS_Store, and a warning that fires on junk teaches people to
// ignore the one that matters. Files skipped by config are deliberate.
func printUnreadable(w io.Writer, skipped []scan.Skip, prev ledger.Ledger, refs ledger.Refs) {
	held := map[string]bool{}
	for _, row := range prev.Rows {
		held[row.File] = true
	}
	for _, row := range refs.Rows {
		held[row.Doc] = true
	}
	for _, sk := range skipped {
		if sk.Reason.Unreadable() && held[sk.File] {
			fmt.Fprintf(w, "warning: %s not scanned (%s); its last recorded state is kept\n", sk.File, sk.Reason)
		}
	}
}

func printProblems(w io.Writer, problems []scan.Problem) {
	for _, p := range problems {
		fmt.Fprintf(w, "  %s:%d  %v\n", p.Pos.File, p.Pos.Start, p.Err)
	}
}

func (a *App) checkCmd() *cobra.Command {
	var asJSON, full, explain, expand, frozen, sync bool
	var opts docsync.CheckOptions
	cmd := &cobra.Command{
		Use:   "check",
		Short: "the six passes; exit 1 on any error-severity finding",
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.Full = full
			if (opts.Run || opts.Resolve) && isForkPR(os.Getenv(githubEventPath)) {
				return ErrForkPR
			}
			// CI defaults to the committed snapshot: a build that fails for
			// a reason absent from its own diff cannot be bisected and
			// teaches people to ignore the gate. Interactive use keeps
			// syncing first, where finding out the spec moved is the point.
			if !frozen && !sync && os.Getenv(ciEnv) != "" {
				frozen = true
			}
			if frozen && sync {
				return fmt.Errorf("%w: --%s and --%s ask for opposite things", ErrUsage, flagFrozen, flagSync)
			}
			a.fetchIndex = !frozen
			a.frozen = frozen
			st := NewStore(a.dir)
			var saveURLs func() error
			var resolveCfg config.Config
			if opts.Resolve {
				cfg, err := a.loadConfig(st)
				if err != nil {
					return err
				}
				resolveCfg = cfg
				if !cfg.Resolve.Enabled {
					// Said on stderr so `--json` stays one document; the
					// run then checks as if --resolve were absent, which
					// reports every link and secret hop as not verified.
					fmt.Fprintln(cmd.ErrOrStderr(), resolveDisabled)
					opts.Resolve = false
				}
			}
			if opts.Resolve {
				a.urlCheck, saveURLs = a.urlChecker(resolveCfg, st)
				a.resolveHook = a.resolver(resolveCfg)
				var err error
				if a.storedHashes, err = st.LoadHashes(); err != nil {
					return err
				}
			}
			ld, err := a.system()
			if err != nil {
				return err
			}
			if frozen {
				warnStaleSnapshot(cmd.ErrOrStderr(), ld.st, ld.cfg, a.now(), a.name)
			}
			noteOlderRule(cmd.ErrOrStderr(), docsync.CheckRule(ld.prev, ld.refs), "warning: %v; run `"+a.name+" scan`")
			rep, err := ld.sys.Check(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if saveURLs != nil {
				if err := saveURLs(); err != nil {
					return err
				}
			}
			if opts.Resolve && resolveCfg.Resolve.StoreHash && len(rep.TruthHashes) > 0 {
				for id, h := range rep.TruthHashes {
					a.storedHashes[id] = h
				}
				if err := st.SaveHashes(a.storedHashes); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			if opts.Run {
				// Progress goes to stderr under --json, which must stay one
				// JSON document.
				progress := out
				if asJSON {
					progress = cmd.ErrOrStderr()
				}
				runs, err := a.executeRuns(cmd.Context(), ld, rep, opts.Env, progress)
				if err != nil {
					return err
				}
				rep = rep.ApplyRuns(runs)
			}
			if err := ld.flush(); err != nil {
				return err
			}
			if explain {
				printExplain(out, rep)
			}
			if asJSON {
				if err := printJSON(out, rep); err != nil {
					return err
				}
			} else {
				printFindings(out, rep.Findings, expand)
				fmt.Fprintf(out, "%s\n", summaryLine(rep))
			}
			if rep.ExitCode != 0 {
				return exitCode(ExitFindings)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	cmd.Flags().BoolVar(&opts.Strict, flagStrict, false, "warnings fail the run")
	cmd.Flags().StringVar(&opts.Env, flagEnv, "", "environment for cites without env=")
	cmd.Flags().BoolVar(&opts.Run, flagRun, false, "allow ds:run where run.enabled")
	cmd.Flags().BoolVar(&opts.Resolve, flagResolve, false, "reach providers and the network where resolve.enabled")
	cmd.Flags().BoolVar(&full, flagFull, false, "rescan every file")
	cmd.Flags().BoolVar(&explain, flagExplain, false, "print every directive the scan matched, with its tier and carrier")
	cmd.Flags().BoolVar(&expand, flagExpand, false, "list every finding; by default a heavily cited id collapses to one line with a count")
	cmd.Flags().BoolVar(&frozen, flagFrozen, false, "resolve foreign blocks from the committed .ds/foreign.tsv instead of syncing; the default when CI is set")
	cmd.Flags().BoolVar(&sync, flagSync, false, "sync the workspace index first, even under CI")
	return cmd
}

// Fork pull requests never run or resolve (§ Security): GitHub exposes the
// event as a JSON file; when the head repository differs from the base
// the run is a fork's.
const githubEventPath = "GITHUB_EVENT_PATH"

// ErrForkPR refuses --run and --resolve on a fork pull request.
var ErrForkPR = errors.New("--run and --resolve are disabled on pull requests from forks")

// ErrForkPRReview refuses `review --ai` on a fork pull request: it runs the
// [review] command out of .ds/config.toml, a committed file the pull request
// can change, so on a fork's PR it would run the fork's command with the
// repository's credentials.
var ErrForkPRReview = errors.New("review --ai is disabled on pull requests from forks: it runs the [review] command from committed configuration")

// isForkPR reports whether this run has to be treated as a fork's pull
// request, so it must not execute anything the pull request could have
// written: ds:run commands, resolver plugins, the [review] command.
//
// It fails CLOSED. When an event file is named but cannot be read or parsed,
// or a pull_request event carries no head repository -- which is what GitHub
// sends once the fork has been deleted -- it answers true, because in none of
// those cases can it be shown that the code is the repository's own. It used
// to answer false for all three, so a malformed event, or a PR from a fork
// deleted after it was opened, ran `ds check --run` with the repository's
// credentials. No event file at all means the run is not on GitHub Actions --
// a developer's machine -- and is trusted as before, as is an event with no
// pull request in it: a push, a schedule, a manual dispatch.
func isForkPR(eventPath string) bool {
	if eventPath == "" {
		return false
	}
	raw, err := os.ReadFile(eventPath)
	if err != nil {
		return true
	}
	var ev struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		PullRequest *struct {
			Head struct {
				Repo *struct {
					FullName string `json:"full_name"`
				} `json:"repo"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		return true
	}
	if ev.PullRequest == nil {
		return false
	}
	head := ev.PullRequest.Head.Repo
	return head == nil || head.FullName == "" || head.FullName != ev.Repository.FullName
}

// collapseAt is how many findings of one state for one id fold into a
// single line (§ Scale "findings on a heavily cited id collapse").
const collapseAt = 5

// printExplain lists every matched directive (Part VII "check --explain"),
// so a repository whose comments already used the prefix can see what the
// scanner took as directives before changing the prefix.
func printExplain(w io.Writer, rep docsync.Report) {
	rows := [][]string{{"WHERE", "TIER", "WHAT", "CARRIER", "ID"}}
	for _, b := range rep.Scan.Defs {
		rows = append(rows, []string{fmt.Sprintf("%s:%d", b.DirectivePos.File, b.DirectivePos.Start), rep.Scan.Tier[b.DirectivePos.File], "def " + string(b.Kind), string(b.Carrier), b.ID})
	}
	for _, r := range rep.Scan.Refs {
		rows = append(rows, []string{fmt.Sprintf("%s:%d", r.Pos.File, r.Pos.Start), rep.Scan.Tier[r.Pos.File], "ds:" + r.Verb, string(r.Carrier), r.ID})
	}
	table(w, rows)
}

// printFindings groups findings by doc, one line each, ok rows omitted.
// Without expand, an id with collapseAt or more findings of one state is
// printed once with a count.
func printFindings(w io.Writer, findings []check.Finding, expand bool) {
	counts := map[string]int{}
	for _, f := range findings {
		if f.ID != "" {
			counts[f.ID+"|"+string(f.State)]++
		}
	}
	printed := map[string]bool{}
	cur := ""
	for _, f := range findings {
		if f.State == check.StateOK {
			continue
		}
		key := f.ID + "|" + string(f.State)
		if !expand && f.ID != "" && counts[key] >= collapseAt {
			if !printed[key] {
				printed[key] = true
				fmt.Fprintf(w, "%d sentences cite %s and are %s; pass --expand to list them\n", counts[key], f.ID, f.State)
			}
			continue
		}
		if f.Doc != cur {
			cur = f.Doc
			fmt.Fprintf(w, "%s\n", cur)
		}
		fmt.Fprintf(w, "  %d\t%-8s %-18s %s\n", f.Line, f.Severity, f.State, f.Message)
		printDiff(w, f.Diff)
		if f.Remedy.IfStillTrue != "" {
			fmt.Fprintf(w, "      still true: %s\n      otherwise:  %s\n", f.Remedy.IfStillTrue, f.Remedy.IfNot)
		} else if f.Remedy.Fix != "" {
			fmt.Fprintf(w, "      fix: %s\n", f.Remedy.Fix)
		}
	}
}

// diffPreviewLines bounds the diff printed under one finding, so a rewritten
// function does not push every other finding off the screen; the rest is
// counted, and `check --json` carries it whole.
const diffPreviewLines = 12

// printDiff writes a finding's diff under it, each line behind a bar so it
// cannot be read as another finding. SPEC §28 shows the diff under the
// finding; the text output used to print only the message, so the one thing
// a reviewer needs to decide between "still true" and "edit the sentence"
// was in the JSON alone (bug 26). A secret's diff never reaches here: the
// library withholds it where bodies are compared.
func printDiff(w io.Writer, diff string) {
	if diff == "" {
		return
	}
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	shown := lines
	if len(shown) > diffPreviewLines {
		shown = shown[:diffPreviewLines]
	}
	for _, l := range shown {
		fmt.Fprintf(w, "      | %s\n", l)
	}
	if more := len(lines) - len(shown); more > 0 {
		fmt.Fprintf(w, "      | (%d more diff lines; --json has them all)\n", more)
	}
}

// summaryWord names a severity in the text summary. Every severity names
// itself except none, whose findings passed: "2 none" read as "nothing",
// the opposite of two citations checked and found current (bug 30). The
// JSON keeps the severity name, which is the contract.
func summaryWord(sev check.Severity) string {
	if sev == check.SeverityNone {
		return string(check.StateOK)
	}
	return string(sev)
}

func summaryLine(rep docsync.Report) string {
	parts := []string{}
	for _, sev := range check.SeverityValues {
		if n := rep.Summary[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, summaryWord(sev)))
		}
	}
	if len(parts) == 0 {
		return "no references"
	}
	return strings.Join(parts, ", ")
}

func (a *App) ackCmd() *cobra.Command {
	var doc, note, actor, delegatedBy, fromCommit string
	var dry bool
	var line, group int
	var agent, all bool
	cmd := &cobra.Command{
		Use:   "ack <id>...",
		Short: "record that the sentences citing these ids are still true at the current hash",
		RunE: func(cmd *cobra.Command, ids []string) error {
			// noteFor holds the note= a commit-message ack gave its id; it
			// wins over --note and the commit subject.
			noteFor := map[string]string{}
			if doc != "" {
				d, err := a.repoPath(doc)
				if err != nil {
					return err
				}
				doc = d
			}
			if group > 0 {
				// `ack --group N` acks one triage group (§19): the same
				// mechanical change across many blocks, one note.
				ld, err := a.system()
				if err != nil {
					return err
				}
				rep, err := ld.sys.Check(cmd.Context(), docsync.CheckOptions{})
				if err != nil {
					return err
				}
				groups := docsync.Triage(rep)
				if group > len(groups) {
					return fmt.Errorf("%w: group %d of %d", docsync.ErrNotFound, group, len(groups))
				}
				var reqs []docsync.AckRequest
				for _, f := range groups[group-1].Findings {
					reqs = append(reqs, docsync.AckRequest{ID: f.ID, Doc: f.Doc, Line: f.Line, Actor: a.actor(actor), Note: note})
				}
				rows, err := a.recordAcks(ld, rep.Scan, reqs, dry)
				if err != nil {
					return err
				}
				if dry {
					printAckPreview(cmd.OutOrStdout(), rep.Scan, rows, false)
					return nil
				}
				fmt.Fprintf(cmd.OutOrStdout(), "acked group %d: %d sentences\n", group, len(rows))
				return nil
			}
			if fromCommit != "" {
				// `<prefix>:ack id=…` in a commit message acks every citation
				// of the id with the commit subject as the note (§19).
				cfg, err := a.loadConfig(NewStore(a.dir))
				if err != nil {
					return err
				}
				msg, err := a.vcs.Message(fromCommit)
				if err != nil {
					return err
				}
				found, err := ackDirectives(cfg.Prefix, msg)
				if err != nil {
					return fmt.Errorf("commit %s: %w", fromCommit, err)
				}
				if len(found) == 0 {
					return fmt.Errorf("%w: commit %s carries no %s:ack id=…", ErrUsage, fromCommit, cfg.Prefix)
				}
				for _, ca := range found {
					ids = append(ids, ca.ID)
					if ca.Note != "" {
						noteFor[ca.ID] = ca.Note
					}
				}
				all = true
				if note == "" {
					note, _, _ = strings.Cut(msg, "\n")
				}
			}
			page := false
			if len(ids) == 0 {
				// A claim has no id: `ds ack --doc D --line N` renews the claim
				// on that line, which is exactly what an expired claim's
				// remedy tells the reader to run. It used to be refused here,
				// so no expired claim could be renewed from the CLI at all.
				// `ds ack --doc D` alone records a review of the whole page,
				// what a page past review_every asks for.
				if doc == "" || all {
					return fmt.Errorf("%w: ack needs at least one id, --doc and --line to renew a claim, --doc alone to record a page review, or --from-commit", ErrUsage)
				}
				ids = []string{""}
				page = line == 0
			}
			if !all && !page && (doc == "" || line == 0) {
				return fmt.Errorf("%w: ack needs --doc and --line, or --all to ack every citation of the id", ErrUsage)
			}
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys := ld.sys
			res, err := sys.Scan(cmd.Context())
			if err != nil {
				return err
			}
			kind := ledger.ActorHuman
			if agent {
				kind = ledger.ActorAgent
			}
			var reqs []docsync.AckRequest
			for _, id := range ids {
				targets := []block.Position{{File: doc, Start: line}}
				if all {
					targets = targets[:0]
					for _, r := range res.Refs {
						if r.ID == id && (doc == "" || r.Pos.File == doc) {
							targets = append(targets, r.Pos)
						}
					}
				}
				for _, t := range targets {
					reqs = append(reqs, docsync.AckRequest{ID: id, Doc: t.File, Line: t.Start, Actor: a.actor(actor), ActorKind: kind, DelegatedBy: delegatedBy, Note: orDefault(noteFor[id], note), Claim: id == "" && !page, Page: page})
				}
			}
			if len(reqs) == 0 {
				return fmt.Errorf("%w: nothing cites %s", docsync.ErrNoReference, strings.Join(ids, ", "))
			}
			rows, err := a.recordAcks(ld, res, reqs, dry)
			if err != nil {
				return err
			}
			if dry {
				printAckPreview(cmd.OutOrStdout(), res, rows, page)
				return nil
			}
			for _, r := range rows {
				if page {
					fmt.Fprintf(cmd.OutOrStdout(), "recorded a review of %s (%s)\n", r.Doc, r.ActorKind)
					continue
				}
				if r.ID == "" {
					fmt.Fprintf(cmd.OutOrStdout(), "renewed the claim at %s:%d (%s)\n", r.Doc, r.Line, r.ActorKind)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "acked %s at %s:%d (%s)\n", r.ID, r.Doc, r.Line, r.ActorKind)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&doc, flagDoc, "", "the citing document")
	cmd.Flags().IntVar(&line, flagLine, 0, "the citing line")
	cmd.Flags().StringVar(&note, flagNote, "", "why it is still true")
	cmd.Flags().StringVar(&actor, flagActor, "", "who is acking; default git user.name")
	cmd.Flags().BoolVar(&agent, flagAgent, false, "the actor is an agent; requires --delegated-by")
	cmd.Flags().StringVar(&delegatedBy, flagDelegatedBy, "", "the human who delegated an agent ack")
	cmd.Flags().BoolVar(&all, flagAll, false, "ack every citation of the id (in --doc if given)")
	cmd.Flags().StringVar(&fromCommit, flagFromCommit, "", "read <prefix>:ack id=… directives from the message of this `commit`")
	cmd.Flags().IntVar(&group, flagGroup, 0, "ack every sentence in triage group N")
	cmd.Flags().BoolVar(&dry, flagDryRun, false, "list every sentence that would be acked, and at which hash, without recording anything")
	return cmd
}

// sentencePreviewWidth bounds the sentence `ack --dry-run` quotes; the rest
// is elided so each ack stays one readable line.
const sentencePreviewWidth = 80

// printAckPreview lists what an ack run would record. What a person
// approves is a sentence, so each line quotes it: a location and a hash are
// not reviewable. A selection that cites nothing is refused before this, as
// the real run refuses it, so a dry run fails exactly where the real one does.
func printAckPreview(w io.Writer, res scan.Result, rows []ledger.Ack, page bool) {
	// Every row was built from the citation at its doc line, so each has a
	// sentence here; a block-position citation's is empty.
	sentence := map[string]string{}
	for _, ref := range res.Refs {
		sentence[citationKey(ref.Pos.File, ref.Pos.Start, ref.ID)] = ref.Sentence
	}
	for _, r := range rows {
		switch {
		case page:
			fmt.Fprintf(w, "would record a review of %s (%s)\n", r.Doc, r.ActorKind)
		case r.ID == "":
			fmt.Fprintf(w, "would renew the claim at %s:%d — %q\n", r.Doc, r.Line, preview(sentence[citationKey(r.Doc, r.Line, r.ID)]))
		default:
			// A block-position citation has no sentence; the block is
			// what it approves.
			quoted := ""
			if s := sentence[citationKey(r.Doc, r.Line, r.ID)]; s != "" {
				quoted = fmt.Sprintf(" — %q", preview(s))
			}
			fmt.Fprintf(w, "would ack %s at %s:%d (%s)%s\n", r.ID, r.Doc, r.Line, short(r.BlockHash), quoted)
		}
	}
}

// citationKey identifies a citation by its doc line and id.
func citationKey(doc string, line int, id string) string {
	return fmt.Sprintf("%s:%d %s", doc, line, id)
}

// preview cuts s to sentencePreviewWidth runes, ending in an ellipsis when
// it had to.
func preview(s string) string {
	r := []rune(s)
	if len(r) <= sentencePreviewWidth {
		return s
	}
	return string(r[:sentencePreviewWidth-1]) + "…"
}

// commitAck is one `<prefix>:ack id=… [note=…]` in a commit message.
type commitAck struct {
	ID   string
	Note string // "" when the directive gives none
}

// Keys a commit-message ack takes.
const (
	commitAckID   = "id"
	commitAckNote = "note"
)

// commitAckKeys is the closed set of keys a commit-message ack accepts.
var commitAckKeys = []string{commitAckID, commitAckNote}

// ErrCommitAckKey is a key on a commit-message ack that is not in
// commitAckKeys. note= used to be read past without a word, so the note a
// developer wrote never reached the ack log (bug 73); any key that does
// nothing is refused rather than ignored.
var ErrCommitAckKey = fmt.Errorf("%w: an ack in a commit message takes only %s", ErrUsage, strings.Join(commitAckKeys, "= and ")+"=")

// commitAckRE finds `<prefix>:ack` and the run of key=value pairs after
// it; a value is quoted, or runs to whitespace, a comma or a parenthesis,
// so an ack in running prose ("(ds:ack id=x)") ends where the prose resumes.
func commitAckRE(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|[^A-Za-z0-9_])` + regexp.QuoteMeta(prefix) + `:ack((?:\s+[A-Za-z_]+=(?:"[^"]*"|'[^']*'|[^\s"'(),]+))+)`)
}

// commitAckPairRE splits that run into key and value.
var commitAckPairRE = regexp.MustCompile(`([A-Za-z_]+)=("[^"]*"|'[^']*'|[^\s"'(),]+)`)

// commitAckIDRE is the shape of an id an ack in prose may name.
var commitAckIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ackDirectives extracts every `<prefix>:ack id=<id> [note=…]` in text. The
// prefix is the configured one, as for every other directive: a repository
// changes it precisely because `ds:` already appears in its prose, and a
// commit message that only mentioned `ds:ack id=x` acked x across every
// doc. An ack with no usable id is not an ack and is skipped.
func ackDirectives(prefix, text string) ([]commitAck, error) {
	var out []commitAck
	for _, m := range commitAckRE(prefix).FindAllStringSubmatch(text, -1) {
		var ca commitAck
		for _, kv := range commitAckPairRE.FindAllStringSubmatch(m[1], -1) {
			v := strings.Trim(kv[2], `"'`)
			switch kv[1] {
			case commitAckID:
				ca.ID = v
			case commitAckNote:
				ca.Note = v
			default:
				return nil, fmt.Errorf("%w; %s:ack has %s=", ErrCommitAckKey, prefix, kv[1])
			}
		}
		if commitAckIDRE.MatchString(ca.ID) {
			out = append(out, ca)
		}
	}
	return out, nil
}

// recordAcks builds every ack against the current hashes and appends them
// to the log in one write, so a batch either lands whole or not at all.
//
// With dry set it builds exactly the rows a real run would and writes none,
// so what `ack --dry-run` lists is what the real run then records.
func (a *App) recordAcks(ld loaded, res scan.Result, reqs []docsync.AckRequest, dry bool) ([]ledger.Ack, error) {
	var rows []ledger.Ack
	for _, req := range reqs {
		req.Preview = dry
		ack, err := ld.sys.Ack(res, req)
		if err != nil {
			return nil, err
		}
		rows = append(rows, ack)
	}
	if dry {
		return rows, nil
	}
	// An ack approves a body, so the body goes into the store before the
	// row that names it, as a scan's do (§15.1). Without it an ack made with
	// no scan since the change named a hash nothing held, and the next
	// change to the block reported `changed (unknown)` with no diff (bug 25).
	if err := ld.st.WriteBodies(ackedBodies(ld.sys.Bodies(res), rows)); err != nil {
		return nil, err
	}
	ld.acks.Rows = append(ld.acks.Rows, rows...)
	if err := ld.st.AppendAcks(rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// ackedBodies keeps, of a scan's bodies (already free of secret and local
// blocks), those whose hash an ack row approves. A claim renewal and a page
// review name no hash and contribute nothing.
func ackedBodies(bodies map[string]string, rows []ledger.Ack) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		if body, ok := bodies[r.BlockHash]; ok {
			out[r.BlockHash] = body
		}
	}
	return out
}

func (a *App) refreshCmd() *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "update ledger locations for moved blocks; in repo mode rewrite the copies",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys, st := ld.sys, ld.st
			out := cmd.OutOrStdout()
			if sys.Config().Include.Mode == config.IncludeRepo && !dry {
				res, err := sys.Scan(cmd.Context())
				if err != nil {
					return err
				}
				regions, err := a.writeFences(sys, res, st)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "%d repo-mode copies rewritten\n", regions)
			}
			rep, err := sys.Check(cmd.Context(), docsync.CheckOptions{})
			if err != nil {
				return err
			}
			moved := 0
			for _, c := range rep.Changes {
				if c.State == match.StateMoved {
					moved++
					fmt.Fprintf(out, "moved %s: %s:%d -> %s:%d\n", c.ID, c.Old.File, c.Old.Start, c.New.Pos.File, c.New.Pos.Start)
				}
			}
			// A renamed doc keeps its acks without anything being rewritten
			// here: the next scan follows each citation to its new path
			// (ledger.Baselines). This used to edit old rows of acks.tsv in
			// place, which broke its append-only promise, made it conflict
			// under merge=union, and in practice never ran — it keyed off the
			// commit ds init recorded, which is empty when init precedes the
			// first commit, as it normally does.
			if dry {
				fmt.Fprintf(out, "%d moved; nothing written (--dry-run)\n", moved)
				return nil
			}
			l, r := sys.Snapshot(rep.Scan)
			// Nothing to record: the files stay as they are. Rewriting them
			// anyway changed only the header's scanned_at, so every refresh
			// dirtied two committed files and a second refresh was never a
			// no-op (bug 77).
			if sameBody(l.Bytes(), ld.prev.Bytes()) && sameBody(r.Bytes(), ld.refs.Bytes()) && st.shardedOnDisk() == ld.cfg.Ledger.Shard {
				fmt.Fprintf(out, "%d moved; ledger unchanged\n", moved)
				return ld.flush()
			}
			if err := st.SaveLedgerSharded(l, r, ld.cfg.Ledger.Shard); err != nil {
				return err
			}
			if err := ld.flush(); err != nil {
				return err
			}
			fmt.Fprintf(out, "%d moved; ledger updated\n", moved)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dry, flagDryRun, false, "report without writing")
	return cmd
}

// writeFences rewrites repo-mode copies in every doc with a block-position
// reference (§9.2). These are the one source write besides def and adopt,
// and they are not journaled: a copy is derived, and refresh regenerates it.
func (a *App) writeFences(sys *docsync.System, res scan.Result, st *Store) (int, error) {
	docs := map[string]bool{}
	for _, r := range res.Refs {
		if r.Verb == extract.VerbBlock && r.Carrier == block.CarrierBlock {
			docs[r.Pos.File] = true
		}
	}
	total := 0
	for doc := range docs {
		p := filepath.Join(st.Root, filepath.FromSlash(doc))
		src, err := os.ReadFile(p)
		if err != nil {
			return total, err
		}
		out, n := sys.Fences(res, doc, src)
		if n == 0 {
			continue
		}
		// WriteFile keeps an existing file's mode.
		if err := os.WriteFile(p, out, filePerm); err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

func (a *App) renderCmd() *cobra.Command {
	var env, outPath, at string
	cmd := &cobra.Command{
		Use:   "render <doc>",
		Short: "expand directives to plain markdown; --at renders the page as of a commit",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			doc, err := a.repoPath(args[0])
			if err != nil {
				return err
			}
			ropts := docsync.RenderOptions{Env: env}
			if at != "" {
				if err := a.renderAt(doc, at); err != nil {
					return err
				}
			}
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys := ld.sys
			runs, err := ld.st.LoadRuns()
			if err != nil {
				return err
			}
			ropts.Runs = runsFor(runs, doc)
			out, notes, err := sys.Render(cmd.Context(), doc, ropts)
			if err != nil {
				return err
			}
			for _, n := range notes {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s:%d: %s\n", doc, n.Line, n.Message)
			}
			if outPath != "" {
				return os.WriteFile(a.userPath(outPath), out, filePerm)
			}
			_, err = cmd.OutOrStdout().Write(out)
			return err
		},
	}
	cmd.Flags().StringVar(&env, flagEnv, "", "environment for cites without env=")
	cmd.Flags().StringVar(&outPath, flagOut, "", "write to a file instead of stdout")
	cmd.Flags().StringVar(&at, flagAt, "", "render the page and its blocks as of this commit")
	return cmd
}

func (a *App) mapCmd() *cobra.Command {
	var budget int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "map",
		Short: "token-bounded table of contents",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys := ld.sys
			m, err := sys.Map(cmd.Context(), docsync.MapOptions{Budget: budget})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, m)
			}
			rows := [][]string{{"PAGE", "COVERS", "CITES", "STATE"}}
			for _, p := range m.Pages {
				rows = append(rows, []string{p.Path, strconv.Itoa(p.Covers), strconv.Itoa(p.Cites), stateSummary(p.State)})
			}
			rows = append(rows, []string{}, []string{"DEF", "FILE", "CITED BY", "STATE"})
			for _, d := range m.Defs {
				rows = append(rows, []string{d.ID, fmt.Sprintf("%s:%d-%d", d.File, d.Lines[0], d.Lines[1]), strconv.Itoa(d.CitedBy), string(d.State)})
			}
			table(out, rows)
			fmt.Fprintf(out, "%d tokens used, %d omitted\n", m.UsedTokens, m.Omitted)
			return nil
		},
	}
	cmd.Flags().IntVar(&budget, flagBudget, 0, "token budget; 0 is unbounded")
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	return cmd
}

func stateSummary(m map[check.State]int) string {
	var parts []string
	for _, st := range check.StateValues {
		if n := m[st]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", st, n))
		}
	}
	return strings.Join(parts, ", ")
}

func (a *App) contextCmd() *cobra.Command {
	var opts docsync.ContextOptions
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "context <doc>|<id>",
		Short: "a page with every block it cites, or a block with every sentence about it, budgeted",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys := ld.sys
			// A doc or an id: a path to an existing file is a doc, typed
			// relative to where the user is; anything else is an id.
			target := args[0]
			if a.isFile(target) {
				if target, err = a.repoPath(target); err != nil {
					return err
				}
			}
			if opts.OldBody, err = a.contextSince(cmd.Context(), sys, opts.Since); err != nil {
				return err
			}
			c, err := sys.Context(cmd.Context(), target, opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, c)
			}
			for _, it := range c.Items {
				fmt.Fprintf(out, "## %d. %s %s (%s, %d tokens)\n%s\n\n", it.Rank, it.ID, it.Why, it.Mode, it.Tokens, it.Content)
			}
			for _, o := range c.Omitted {
				fmt.Fprintf(out, "omitted %s: %s\n", o.ID, o.Reason)
			}
			fmt.Fprintln(out, tokensLine(c.UsedTokens, c.BudgetTokens))
			return nil
		},
	}
	cmd.Flags().IntVar(&opts.Budget, flagBudget, 0, "token budget; 0 is unbounded")
	cmd.Flags().StringVar(&opts.Since, flagSince, "", "ack, or a commit: diff each cited block against its body then")
	cmd.Flags().StringVar(&opts.Mode, flagMode, docsync.ModeAuto, "auto|full|diff|value")
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	return cmd
}

func (a *App) factsCmd() *cobra.Command {
	var asJSON bool
	var citedBy string
	cmd := &cobra.Command{
		Use:   "facts",
		Short: "every one-line def with its current value and citers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys := ld.sys
			res, err := sys.Scan(cmd.Context())
			if err != nil {
				return err
			}
			facts := sys.Facts(res)
			if citedBy != "" {
				kept := facts[:0]
				for _, f := range facts {
					for _, c := range f.CitedBy {
						if c.File == citedBy {
							kept = append(kept, f)
							break
						}
					}
				}
				facts = kept
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, facts)
			}
			rows := [][]string{{"ID", "VALUE", "WHERE", "CITED BY"}}
			for _, f := range facts {
				rows = append(rows, []string{f.ID, f.Value, fmt.Sprintf("%s:%d", f.File, f.Line), strconv.Itoa(len(f.CitedBy))})
			}
			table(out, rows)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	cmd.Flags().StringVar(&citedBy, flagCitedBy, "", "only facts cited by this doc")
	return cmd
}

func (a *App) whyCmd() *cobra.Command {
	var asJSON, chain, history bool
	cmd := &cobra.Command{
		Use:   "why <id>",
		Short: "every reference to or cover of the id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys := ld.sys
			res, err := sys.Scan(cmd.Context())
			if err != nil {
				return err
			}
			w, err := sys.Why(res, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, w)
			}
			for _, d := range w.Defs {
				fmt.Fprintf(out, "%s  %s  %s:%d-%d\n", d.ID, d.Kind, d.Pos.File, d.Pos.Start, d.Pos.End)
			}
			for _, r := range w.Refs {
				fmt.Fprintf(out, "  %s:%d  ds:%s  %s\n", r.Pos.File, r.Pos.Start, r.Verb, r.Sentence)
			}
			for _, c := range w.CoveredBy {
				fmt.Fprintf(out, "  covered by %s\n", c)
			}
			if chain {
				for i, b := range w.Chain {
					prefix := strings.Repeat("  ", i)
					if i > 0 {
						prefix += "from "
					}
					truth := ""
					if b.IsTruth() {
						truth = "  TRUTH"
					}
					fmt.Fprintf(out, "%s%s  %s  %s:%d%s\n", prefix, b.ID, strings.TrimSpace(b.Content), b.Pos.File, b.Pos.Start, truth)
				}
			}
			if history {
				for _, h := range w.History {
					fmt.Fprintf(out, "  %s  %s (%s)  %s:%d  %s\n", h.At.Format("2006-01-02"), h.Actor, h.ActorKind, h.Doc, h.Line, h.Note)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	cmd.Flags().BoolVar(&chain, flagChain, false, "show the from= path to the truth")
	cmd.Flags().BoolVar(&history, flagHistory, false, "show every ack with its note")
	return cmd
}

func (a *App) findCmd() *cobra.Command {
	var asJSON bool
	var byFile, byTag string
	cmd := &cobra.Command{
		Use:   "find [query] [--file path] [--tag t]",
		Short: "ids by symbol, text, file, or tag",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 && byFile == "" && byTag == "" {
				return fmt.Errorf("%w: find needs a query, --file, or --tag", ErrUsage)
			}
			if byFile != "" {
				// A prefix typed from where the user is; a trailing slash
				// still means "under this directory".
				p, err := a.repoPath(byFile)
				if err != nil {
					return err
				}
				if strings.HasSuffix(byFile, "/") && p != "." {
					p += "/"
				}
				if p == "." {
					p = ""
				}
				byFile = p
			}
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys := ld.sys
			res, err := sys.Scan(cmd.Context())
			if err != nil {
				return err
			}
			opts := docsync.FindOptions{File: byFile, Tag: byTag}
			if len(args) == 1 {
				opts.Query = args[0]
			}
			var found []block.Block
			if byFile == "" && byTag == "" {
				found = sys.Find(res, opts.Query)
			} else {
				found = sys.FindBy(res, opts)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, found)
			}
			citers := map[string]int{}
			for _, r := range res.Refs {
				citers[r.ID]++
			}
			rows := [][]string{}
			for _, b := range found {
				rows = append(rows, []string{b.ID, string(b.Kind), fmt.Sprintf("%s:%d-%d", b.Pos.File, b.Pos.Start, b.Pos.End), b.Args[block.KeyDesc], fmt.Sprintf("cited by %d", citers[b.ID])})
			}
			// In the order Find gives, by place: sorting by id put two defs with
			// one label in the order of their random suffixes (bug 32).
			table(out, rows)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	cmd.Flags().StringVar(&byFile, flagFile, "", "only defs under this path prefix")
	cmd.Flags().StringVar(&byTag, flagTag, "", "only defs carrying this tag")
	return cmd
}

func (a *App) readCmd() *cobra.Command {
	var lines string
	cmd := &cobra.Command{
		Use:   "read <id>",
		Short: "the body of a block",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys := ld.sys
			res, err := sys.Scan(cmd.Context())
			if err != nil {
				return err
			}
			body, err := sys.Read(res, args[0], lines)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), body)
			return nil
		},
	}
	cmd.Flags().StringVar(&lines, flagLines, "", "fragment a-b")
	return cmd
}

func (a *App) locateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "locate <id>",
		Short: "file and line range at the current commit",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys := ld.sys
			res, err := sys.Scan(cmd.Context())
			if err != nil {
				return err
			}
			b, ok := sys.LocateID(res, args[0])
			if !ok {
				return fmt.Errorf("%w: %s", docsync.ErrNotFound, args[0])
			}
			commit, _ := a.vcs.Head()
			if commit == "" {
				commit = "working tree"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s:%d-%d @ %s\n", b.Pos.File, b.Pos.Start, b.Pos.End, commit)
			return nil
		},
	}
}

func (a *App) impactCmd() *cobra.Command {
	var asJSON, staged bool
	cmd := &cobra.Command{
		Use:   "impact",
		Short: "which sentences, pages, and owners the working tree's changes will flag; --staged limits to staged files",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys := ld.sys
			imp, err := sys.Impact(cmd.Context())
			if err != nil {
				return err
			}
			if staged {
				files, err := a.vcs.Staged()
				if err != nil {
					return err
				}
				imp = filterImpact(imp, files)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, imp)
			}
			if imp.Total == 0 {
				fmt.Fprintln(out, "no documented behaviour is affected")
				return nil
			}
			for _, g := range imp.ByDoc {
				fmt.Fprintf(out, "%s (%d)\n", g.Key, len(g.Findings))
				for _, f := range g.Findings {
					fmt.Fprintf(out, "  %d  %s  %s\n", f.Line, f.State, f.ID)
				}
			}
			for _, g := range imp.ByOwner {
				fmt.Fprintf(out, "owner %s: %d\n", orNone(g.Key), len(g.Findings))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	cmd.Flags().BoolVar(&staged, flagStaged, false, "only findings caused by staged files (pre-commit)")
	return cmd
}

// filterImpact keeps findings whose block lives in one of files.
func filterImpact(imp docsync.ImpactResult, files []string) docsync.ImpactResult {
	keep := map[string]bool{}
	for _, f := range files {
		keep[filepath.ToSlash(f)] = true
	}
	filter := func(groups []docsync.ImpactGroup) []docsync.ImpactGroup {
		var out []docsync.ImpactGroup
		for _, g := range groups {
			var fs []check.Finding
			for _, f := range g.Findings {
				if keep[f.File] {
					fs = append(fs, f)
				}
			}
			if len(fs) > 0 {
				out = append(out, docsync.ImpactGroup{Key: g.Key, Findings: fs})
			}
		}
		return out
	}
	imp.ByDoc, imp.ByOwner, imp.ByRepo = filter(imp.ByDoc), filter(imp.ByOwner), filter(imp.ByRepo)
	imp.Total = 0
	for _, g := range imp.ByDoc {
		imp.Total += len(g.Findings)
	}
	return imp
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func (a *App) statusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "per-reference state for renderers to paint freshness",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			sys := ld.sys
			rep, err := sys.Check(cmd.Context(), docsync.CheckOptions{})
			if err != nil {
				return err
			}
			rows := statusRows(rep, ld.acks)
			out := cmd.OutOrStdout()
			stale := a.staleness(ld.cfg, bodyLookup(ld.st, a.indexFS, a.indexEntries))
			if asJSON {
				return printJSON(out, struct {
					docsync.Envelope
					Refs     []statusRow `json:"refs"`
					Snapshot Staleness   `json:"snapshot"`
				}{rep.Envelope, rows, stale})
			}
			printStaleness(out, stale)
			for _, r := range rows {
				fmt.Fprintf(out, "%s:%d\t%s\t%s\n", r.Doc, r.Line, r.State, r.ID)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	return cmd
}
