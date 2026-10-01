package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/match"
	"github.com/ubgo/docsync/workspace"
)

// Workspace index handling (§21). The repo config's `workspace` names the
// index: a directory on disk, or a git URL cloned into .ds/index/.
const (
	IndexDir = "index"
	// staleAfter is how old a published ledger may be before consumers warn
	// when commit distance is not known.
	staleAfter = 7 * 24 * time.Hour
	// defaultBranchFallback is used when neither the workspace file nor the
	// VCS says which branch publishes.
	defaultBranchFallback = "main"
	flagTests             = "tests"
	flagIndex             = "index"
	flagBranch            = "branch"
	publishMessage        = "docsync publish %s @ %s"
)

// Errors.
var (
	ErrNoWorkspace   = errors.New("no workspace configured; set `workspace` in .ds/config.toml")
	ErrNotDefault    = errors.New("publish runs only on the default branch (§21); pass --force to override")
	ErrIndexOffline  = errors.New("workspace index unreachable and no cached copy")
	ErrIndexNoRemote = errors.New("index is a local directory; nothing to push")
)

// indexState is what sync produced.
type indexState struct {
	Dir      string
	Cloned   bool
	Offline  bool
	Merged   workspace.Merged
	Entries  []workspace.Entry
	Warnings []string
	WS       config.Workspace
}

// ErrNotMember refuses a publish from a repository the workspace does not
// list: a fork or mirror of a listed one, or a repository that is not in the
// workspace at all.
var ErrNotMember = errors.New("this repository is not one of the workspace's repos")

// canPublish enforces "only the canonical URL may publish" (§21). The index
// directory is the URL's last segment, so a fork at someone/api wrote over
// org/api's published ledger; and a repository missing from workspace.repos
// published rows the merge then reported as a removed repo's. With a remote
// the URL must be listed; without one there is nothing to verify, so the
// name must at least be a listed repository's. An index with no workspace
// file lists nothing, and the merge filters nothing, so neither does this.
func (a *App) canPublish(ws config.Workspace, repo string) error {
	if len(ws.Repos) == 0 {
		return nil
	}
	if url := a.vcs.RemoteURL(); url != "" {
		if !ws.Member(url) {
			return fmt.Errorf("%w: %s is not listed in %s", ErrNotMember, url, config.WorkspaceFile)
		}
		return nil
	}
	for _, r := range ws.Repos {
		if config.RepoName(r) == repo {
			return nil
		}
	}
	return fmt.Errorf("%w: %s is not the name of any repository in %s", ErrNotMember, repo, config.WorkspaceFile)
}

// defaultBranch is the branch that publishes, and the only one index
// maintenance runs from: the workspace file's default_branch, or main. Every
// command asks here — prune --index compared against main alone, so a
// workspace publishing from master could publish but never prune.
func (st indexState) defaultBranch() string {
	if st.WS.DefaultBranch != "" {
		return st.WS.DefaultBranch
	}
	return defaultBranchFallback
}

// indexPath returns where the index lives and whether it is a checkout of
// a remote that the CLI manages.
func (a *App) indexPath(cfg config.Config) (dir string, managed bool) {
	loc := cfg.Workspace
	if info, err := os.Stat(loc); err == nil && info.IsDir() {
		return loc, false
	}
	if info, err := os.Stat(filepath.Join(a.dir, loc)); err == nil && info.IsDir() && !filepath.IsAbs(loc) {
		return filepath.Join(a.dir, loc), false
	}
	return filepath.Join(a.dir, DirName, IndexDir), true
}

// syncIndex fetches (when fetch is set) and reads the index. Network
// failures never fail a run: the cached copy is used and a warning says
// how old it is (§21).
func (a *App) syncIndex(cfg config.Config, fetch bool) (indexState, error) {
	st := indexState{}
	if cfg.Workspace == "" {
		return st, ErrNoWorkspace
	}
	dir, managed := a.indexPath(cfg)
	st.Dir = dir
	_, statErr := os.Stat(dir)
	// A managed index with no clone yet is cloned by whichever command
	// first needs it, fetching or not (bug 104): requiring `ds sync` first
	// made `ds def` and `ds scan` in a fresh checkout stop with "index
	// unreachable", which is about a cache nobody had been told to build.
	if managed && (fetch || statErr != nil) {
		if statErr != nil {
			if err := a.vcs.Clone(cfg.Workspace, dir); err != nil {
				return st, fmt.Errorf("%w: %v", ErrIndexOffline, err)
			}
			st.Cloned = true
		} else if err := a.vcs.Pull(dir); err != nil {
			st.Offline = true
			st.Warnings = append(st.Warnings, "index fetch failed; using the cached copy: "+err.Error())
		}
	}
	if _, err := os.Stat(dir); err != nil {
		return st, ErrIndexOffline
	}
	if raw, err := os.ReadFile(filepath.Join(dir, config.WorkspaceFile)); err == nil {
		ws, err := config.ParseWorkspace(strings.NewReader(string(raw)))
		if err != nil {
			return st, fmt.Errorf("%s: %w", config.WorkspaceFile, err)
		}
		st.WS = ws
	}
	entries, err := workspace.Read(os.DirFS(dir))
	if err != nil {
		return st, err
	}
	st.Entries = entries
	st.Merged = workspace.MergeFor(entries, st.WS.Repos)
	if len(st.Merged.Removed) > 0 {
		a.removed = st.Merged.Removed
	}
	for _, d := range st.Merged.Duplicates {
		st.Warnings = append(st.Warnings, fmt.Sprintf("id %s is published by %s", d.ID, strings.Join(d.Repos, " and ")))
	}
	for repo, p := range st.Merged.Published {
		if age := a.now().Sub(p.ScannedAt); age > staleAfter {
			st.Warnings = append(st.Warnings, fmt.Sprintf("index for %s is %d days old", repo, int(age.Hours()/24)))
		}
	}
	if w := a.indexMismatch(cfg, st.WS, managed); w != "" {
		st.Warnings = append(st.Warnings, w)
	}
	if w := a.commitsBehind(cfg, st); w != "" {
		st.Warnings = append(st.Warnings, w)
	}
	return st, nil
}

// indexMismatch wires the workspace file's `index` key (bug 101): it names
// the canonical index, so a repository whose own `workspace` URL is a
// different repository is reading -- and would publish into -- the wrong
// one. Only a git-URL index is compared: a local directory is whatever
// checkout the person keeps, and its path says nothing about which
// repository it is a copy of.
func (a *App) indexMismatch(cfg config.Config, ws config.Workspace, managed bool) string {
	if ws.Index == "" || !managed || config.CanonicalURL(ws.Index) == config.CanonicalURL(cfg.Workspace) {
		return ""
	}
	return fmt.Sprintf("%s names the index %s, but .ds/config.toml points workspace at %s", config.WorkspaceFile, ws.Index, cfg.Workspace)
}

// foreignLinkFormat is the permalink of another repository's block:
// https://<host/path>/blob/<commit>/<file>#L<start>-L<end>, the form GitHub
// serves and GitLab and Gitea redirect. The commit is the one that
// repository published, so the link shows the lines the block had then.
const foreignLinkFormat = "https://%s/blob/%s/%s#L%d-L%d"

// foreignLink links a rendered block of another repository into that
// repository (bug 105), using the URL the workspace file lists for it and
// the commit it published from the block's branch. A repository the file
// does not list, or one with no published commit, gets no link, and the
// renderer names the repository instead.
func foreignLink(st indexState) func(b block.Block) (string, bool) {
	return func(b block.Block) (string, bool) {
		owner := b.Args[block.KeyRepo]
		pub := st.Merged.Published[owner]
		if branch := b.Args[block.KeyBranch]; branch != "" {
			pub = st.Merged.BranchPublished[workspace.BranchKey(owner, branch)]
		}
		for _, url := range st.WS.Repos {
			if config.RepoName(url) == owner && pub.Commit != "" {
				return fmt.Sprintf(foreignLinkFormat, config.CanonicalURL(url), pub.Commit, b.Pos.File, b.Pos.Start, b.Pos.End), true
			}
		}
		return "", false
	}
}

// CommitCounter is the optional VCS upgrade behind
// `workspace.stale_after_commits` (bug 101): how many commits `to` is ahead
// of `from`. A VCS without it gets no commit-distance warning, never a
// guessed one.
type CommitCounter interface {
	CommitsBetween(from, to string) (int, error)
}

// commitsBehind is the "index for api is 14 commits behind" warning (§21).
// Only the repository that published can count it: commit distance needs
// that repository's history, which no consumer has, so a consumer still
// warns by age alone. The publishing repository hears it from every command
// that loads the workspace, which is where the fix -- `ds publish` -- runs.
func (a *App) commitsBehind(cfg config.Config, st indexState) string {
	limit := st.WS.StaleAfterCommits
	counter, ok := a.vcs.(CommitCounter)
	if limit == 0 || !ok {
		return ""
	}
	repo := a.repoName(cfg)
	pub, published := st.Merged.Published[repo]
	if !published {
		return ""
	}
	n, err := counter.CommitsBetween(pub.Commit, st.defaultBranch())
	if err != nil || n <= limit {
		return ""
	}
	return fmt.Sprintf("index for %s is %d commits behind %s (workspace.stale_after_commits = %d); run `%s publish` on %s", repo, n, st.defaultBranch(), limit, a.name, st.defaultBranch())
}

// repoName is this repository's name in the workspace: the remote's last
// path segment, else the directory name.
func (a *App) repoName(cfg config.Config) string {
	if url := a.vcs.RemoteURL(); url != "" {
		return config.RepoName(url)
	}
	return repoName(a.dir, cfg)
}

// workspaceOptions returns the library options that give a check the merged
// view: other repos' defs and the refs that point into this one. Without a
// workspace it returns nothing.
func (a *App) workspaceOptions(cfg config.Config, repo string, fetch bool) ([]docsync.Option, []string, error) {
	if cfg.Workspace == "" {
		return nil, nil, nil
	}
	if a.frozen {
		// Frozen resolves from the committed snapshot and never touches the
		// index, so the same commit gives the same answer on any machine at
		// any time. Merged refs are not read: a published citation from
		// another repo is not part of this repo's committed state.
		opts, err := a.frozenOptions(NewStore(a.dir), cfg)
		return append(opts, docsync.WithForeignSnapshot()), nil, err
	}
	st, err := a.syncIndex(cfg, fetch)
	if err != nil {
		return nil, nil, err
	}
	defs, refs := st.Merged.Others(repo)
	opts := []docsync.Option{docsync.WithMerged(defs...), docsync.WithMergedRefs(refs...), docsync.WithForeignLink(foreignLink(st))}
	// The snapshot records where each cited foreign block was at the last
	// sync, which is the only previous position a citing repo has for it.
	// `check` never rewrites it, so this stays a comparison against
	// committed state. Its absence simply means no move can be reported.
	if snap, ok, err := NewStore(a.dir).LoadForeign(); err == nil && ok {
		opts = append(opts, docsync.WithPreviousForeign(rowsOf(snap)...))
	}
	if len(st.Merged.Tests) > 0 {
		opts = append(opts, docsync.WithTestResults(st.Merged.Tests))
	}
	a.indexFS, a.indexEntries = os.DirFS(st.Dir), st.Entries
	return opts, st.Warnings, nil
}

func (a *App) syncCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "fetch the workspace index and show what is published",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.loadConfig(NewStore(a.dir))
			if err != nil {
				return err
			}
			st, err := a.syncIndex(cfg, true)
			if err != nil {
				return err
			}
			// Recording what this repo cites is the point of sync: it is the
			// deliberate act of taking upstream's changes, and the diff it
			// produces is where the resulting findings get reviewed.
			changes, rowsSame, snapErr := a.writeSnapshot(cfg, st)
			if snapErr != nil {
				return snapErr
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, struct {
					Workspace  string                         `json:"workspace"`
					Index      string                         `json:"index"`
					Published  map[string]workspace.Published `json:"published"`
					Duplicates []workspace.Duplicate          `json:"duplicates"`
					Warnings   []string                       `json:"warnings"`
				}{st.WS.Name, st.Dir, st.Merged.Published, st.Merged.Duplicates, st.Warnings})
			}
			if st.Cloned {
				fmt.Fprintf(out, "cloned %s into %s\n", cfg.Workspace, st.Dir)
			}
			rows := [][]string{{"REPO", "COMMIT", "PUBLISHED", "DEFS", "REFS"}}
			for _, e := range st.Entries {
				rows = append(rows, []string{e.Repo, e.Ledger.Header.Commit, e.Ledger.Header.ScannedAt.Format(time.RFC3339), fmt.Sprint(len(e.Ledger.Rows)), fmt.Sprint(len(e.Refs.Rows))})
			}
			table(out, rows)
			printChanges(out, changes, rowsSame)
			for _, w := range st.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	return cmd
}

// writeSnapshot rewrites .ds/foreign.tsv for the ids this repo cites and
// returns what moved since the last sync. Without a workspace there is
// nothing foreign to record.
func (a *App) writeSnapshot(cfg config.Config, st indexState) ([]snapshotChange, bool, error) {
	store := NewStore(a.dir)
	_, refs, _, err := store.LoadState()
	if err != nil {
		return nil, false, err
	}
	repo := a.repoName(cfg)
	commit, _ := a.vcs.Head()
	next := st.Merged.Snapshot(repo, citedIDs(refs), ledger.Header{Repo: repo, Commit: commit, ScannedAt: a.now()})
	prev, _, err := store.LoadForeign()
	if err != nil {
		return nil, false, err
	}
	if err := store.SaveForeign(next); err != nil {
		return nil, false, err
	}
	if n := missingBodies(st.Dir, next); n > 0 {
		fmt.Fprintf(a.stderr, "warning: %d cited foreign block(s) have no published body; a frozen check will report their changes as `unknown`\n", n)
	}
	return diffSnapshot(prev, next), rowsEqual(prev, next), nil
}

func (a *App) publishCmd() *cobra.Command {
	var tests string
	var force, branchFlag, dry bool
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "write this repo's ledger, refs, and test outcomes into the workspace index",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.loadConfig(NewStore(a.dir))
			if err != nil {
				return err
			}
			st, err := a.syncIndex(cfg, true)
			if err != nil {
				return err
			}
			want := st.defaultBranch()
			if branch, err := a.vcs.Branch(); !force && !branchFlag && (err != nil || branch != want) {
				return fmt.Errorf("%w: on %q, default is %q", ErrNotDefault, branch, want)
			}
			if branchFlag {
				if _, err := a.vcs.Branch(); err != nil {
					return fmt.Errorf("%w: --branch needs a checked-out branch", ErrNotDefault)
				}
			}
			ld, err := a.system()
			if err != nil {
				return err
			}
			res, err := ld.sys.Scan(cmd.Context())
			if err != nil {
				return err
			}
			l, r := ld.sys.Snapshot(res)
			repo := a.repoName(cfg)
			if err := a.canPublish(st.WS, repo); err != nil {
				return err
			}
			if err := st.Merged.CheckDuplicate(repo, l); err != nil {
				return err
			}
			entry := workspace.Entry{Repo: repo, Ledger: l, Refs: r, Bodies: ld.sys.Bodies(res)}
			if branchFlag {
				if branch, err := a.vcs.Branch(); err == nil && branch != want {
					entry.Branch = branch
				}
			}
			if tests != "" {
				f, err := os.Open(a.userPath(tests))
				if err != nil {
					return err
				}
				byName, err := workspace.ParseJUnit(f)
				_ = f.Close()
				if err != nil {
					return fmt.Errorf("%s: %w", tests, err)
				}
				entry.Tests = workspace.MatchTests(res.Defs, byName)
			}
			if dry {
				// Nothing is written or pushed; the one line that matters is
				// what the index would gain and lose.
				fmt.Fprintf(cmd.OutOrStdout(), "would publish %s: %d defs, %d refs, %d test outcomes into %s\n", repo, len(l.Rows), len(r.Rows), len(entry.Tests), st.Dir)
				fmt.Fprintln(cmd.OutOrStdout(), indexDiff(publishedEntry(st.Entries, entry), entry))
				return nil
			}
			for rel, data := range entry.Files() {
				p := filepath.Join(st.Dir, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(p), dirPerm); err != nil {
					return err
				}
				if err := os.WriteFile(p, data, filePerm); err != nil {
					return err
				}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "published %s: %d defs, %d refs, %d test outcomes into %s\n", repo, len(l.Rows), len(r.Rows), len(entry.Tests), st.Dir)
			if _, managed := a.indexPath(cfg); managed {
				if err := a.vcs.Push(st.Dir, fmt.Sprintf(publishMessage, repo, l.Header.Commit)); err != nil {
					return err
				}
				fmt.Fprintln(out, "pushed")
			} else if c, ok := a.vcs.(IndexCommitter); ok {
				// A local index that is its own git repository gets a
				// commit, never a push: the directory is the user's, and
				// where it is shared from is theirs to decide (bug 100).
				committed, err := c.CommitIndex(st.Dir, fmt.Sprintf(publishMessage, repo, l.Header.Commit))
				if err != nil {
					return err
				}
				if committed {
					fmt.Fprintf(out, "committed in %s\n", st.Dir)
				}
			}
			for _, w := range st.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tests, flagTests, "", "JUnit XML report to publish outcomes for assert= cites")
	cmd.Flags().BoolVar(&force, flagForce, false, "publish from a non-default branch")
	cmd.Flags().BoolVar(&branchFlag, flagBranch, false, "publish the current non-default branch under its own index directory (branch= selects it)")
	cmd.Flags().BoolVar(&dry, flagDryRun, false, "show what would be published and how the index would change, without writing or pushing")
	return cmd
}

// publishedEntry is what the index already holds for next's repository and
// branch, or nil when it holds nothing yet.
func publishedEntry(entries []workspace.Entry, next workspace.Entry) *workspace.Entry {
	for i := range entries {
		if entries[i].Repo == next.Repo && entries[i].Branch == next.Branch {
			return &entries[i]
		}
	}
	return nil
}

// indexDiff says in one line how publishing next would change the index:
// defs by id, environment, and branch (added, removed, or changed in content
// or place), refs by citation (added, removed, or with new hashes), and test
// outcomes. It compares rows, not bytes: every ledger header carries a fresh
// scanned_at, so a byte comparison would never say "unchanged".
func indexDiff(prev *workspace.Entry, next workspace.Entry) string {
	var old workspace.Entry
	if prev != nil {
		old = *prev
	}
	defs := diffCounts(keyed(old.Ledger.Rows, match.RowKey, defFacts), keyed(next.Ledger.Rows, match.RowKey, defFacts))
	refs := diffCounts(keyed(old.Refs.Rows, refKey, refFacts), keyed(next.Refs.Rows, refKey, refFacts))
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{
		{defs.added, "defs added"},
		{defs.changed, "defs changed"},
		{defs.removed, "defs removed"},
		{refs.added, "refs added"},
		{refs.changed, "refs changed"},
		{refs.removed, "refs removed"},
	} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	if !reflect.DeepEqual(noOutcomes(old.Tests), noOutcomes(next.Tests)) {
		parts = append(parts, "test outcomes changed")
	}
	if len(parts) == 0 {
		return "index unchanged"
	}
	return "index would change: " + strings.Join(parts, ", ")
}

// counts is how many keys a diff added, changed, and removed.
type counts struct{ added, changed, removed int }

// diffCounts compares two key → facts maps.
func diffCounts(old, next map[string]string) counts {
	var c counts
	for k, v := range next {
		switch was, ok := old[k]; {
		case !ok:
			c.added++
		case was != v:
			c.changed++
		}
	}
	for k := range old {
		if _, ok := next[k]; !ok {
			c.removed++
		}
	}
	return c
}

// keyed indexes rows by identity, keeping the facts whose change matters.
func keyed[T any](rows []T, key, facts func(T) string) map[string]string {
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[key(r)] = facts(r)
	}
	return m
}

// defFacts are what a changed def differs in: its content and its place.
func defFacts(r ledger.Row) string {
	return fmt.Sprintf("%s %s:%d-%d", r.Hash, r.File, r.Start, r.End)
}

// refKey identifies a citation; refFacts are its recorded hashes.
func refKey(r ledger.RefRow) string {
	return fmt.Sprintf("%s:%d %s %s %s", r.Doc, r.Line, r.ID, r.Verb, r.Env)
}

func refFacts(r ledger.RefRow) string {
	return r.AckedHash + " " + r.SeenHash + " " + r.SentenceHash
}

// noOutcomes treats an empty outcome map as none, so a repo that never
// published tests does not read as changed against one that has no rows.
func noOutcomes(m map[string]check.TestOutcome) map[string]check.TestOutcome {
	if len(m) == 0 {
		return nil
	}
	return m
}
