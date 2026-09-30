package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/workspace"
)

// `ds prune` removes superseded block bodies (§20.1). The stores are
// content-addressed and append-only, so a block that churns leaves one body
// per version in `.ds/blocks/` and another in the index — both committed, so
// every superseded body is permanent history in two git repositories.
//
// The safety property that matters is not "usually removes the right ones"
// but "cannot remove a live one". Liveness is computed as a set and only its
// complement is removed; anything that cannot be read is a refusal, never an
// assumption that the hashes it held were dead.
const (
	flagIndexStore = "index"
	flagKeep       = "keep"
	flagHash       = "hash"
	// DefaultKeep is how long a dead body is kept anyway. A repo that has
	// not synced lately may be about to ack against it, and a body is
	// cheap: when in doubt it stays.
	DefaultKeep = "30d"
)

// ErrPruneState is returned when a file the live set depends on cannot be
// read. Pruning on a partial view is how a live body gets deleted, so it is
// refused outright rather than degraded.
var ErrPruneState = fmt.Errorf("prune: cannot read the state that says which bodies are live")

// ErrPruneIndex is returned when `--index` is asked for from a position that
// cannot see who cites what.
var ErrPruneIndex = fmt.Errorf("prune --index needs the default branch and a fresh sync")

// deadBody is one body prune would remove.
type deadBody struct {
	Hash string
	Path string
	Age  time.Duration
	Size int64
}

// liveHashes is every hash that must survive a prune of the local store.
//
// The five sources are not interchangeable and none can be dropped: the
// ledger holds what blocks are now, refs hold what citations were acked at
// and first saw, the ack log holds the latest ack per sentence, and the
// foreign snapshot holds the pinned upstream versions. A body outside all of
// them is unreachable by any code path that reads bodies.
func (s *Store) liveHashes() (map[string]bool, error) {
	prev, refs, acks, err := s.LoadState()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPruneState, err)
	}
	return s.liveFrom(prev, refs, acks)
}

// liveFrom is liveHashes over state the caller already holds, so a command
// that needs both the live set and the ack log reads the files once.
func (s *Store) liveFrom(prev ledger.Ledger, refs ledger.Refs, acks ledger.Acks) (map[string]bool, error) {
	live := map[string]bool{}
	for _, r := range prev.Rows {
		live[r.Hash] = true
	}
	for _, r := range refs.Rows {
		live[r.AckedHash] = true
		live[r.SeenHash] = true
	}
	for _, a := range acks.Latest() {
		live[a.BlockHash] = true
	}
	foreign, ok, err := s.LoadForeign()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPruneState, err)
	}
	if ok {
		for _, r := range foreign.Rows {
			live[r.Hash] = true
		}
	}
	delete(live, "")
	return live, nil
}

// deadIn lists the bodies in one store directory that no live hash names and
// that are older than keep.
//
// Age is measured against real wall time rather than the injected clock,
// because a body's age is how long the file has actually existed on disk —
// a fact about the filesystem, not about the run. A logical clock behind the
// mtimes would make every body look newer than it is and quietly prune
// nothing, which is the failure that hides itself.
func deadIn(dir string, live map[string]bool, keep time.Duration, now time.Time) (dead []deadBody, total, kept int, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, 0, nil
		}
		return nil, 0, 0, err
	}
	for _, e := range entries {
		if e.IsDir() || !hashNameOK(e.Name()) {
			continue
		}
		total++
		if live[e.Name()] {
			continue
		}
		p := filepath.Join(dir, e.Name())
		// Stat rather than the directory entry, so a body that is a link to
		// something gone is skipped instead of being reported with the
		// link's own age and then failing to delete.
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		age := now.Sub(info.ModTime())
		if age < keep {
			// Unreachable but too young to remove. Counted so the summary
			// can say where they went; otherwise "4 bodies, 2 live, 0 dead"
			// reads as a miscount to anyone who can see four files.
			kept++
			continue
		}
		dead = append(dead, deadBody{Hash: e.Name(), Path: p, Age: age, Size: info.Size()})
	}
	sort.Slice(dead, func(i, j int) bool { return dead[i].Hash < dead[j].Hash })
	return dead, total, kept, nil
}

func (a *App) pruneCmd() *cobra.Command {
	var dry, index, force bool
	var keep, hash string
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "remove block bodies no ledger, ack, citation, or snapshot still needs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st := NewStore(a.dir)
			if !st.Exists() {
				return ErrNotInitialised
			}
			cfg, err := a.loadConfig(st)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if hash != "" {
				return a.pruneOne(out, st, cfg, hash, dry, force, index)
			}
			d, err := check.ParseDuration(keep)
			if err != nil {
				return fmt.Errorf("%w: --%s %q: a whole number followed by d (days), w (weeks), h (hours), or m (minutes), for example 30d", ErrUsage, flagKeep, keep)
			}
			if index {
				return a.pruneIndex(out, st, cfg, d, dry)
			}
			live, err := st.liveHashes()
			if err != nil {
				return err
			}
			dead, total, kept, err := deadIn(st.path(BlocksDir), live, d, time.Now())
			if err != nil {
				return err
			}
			return report(out, st.path(BlocksDir), dead, total, len(live), kept, keep, dry)
		},
	}
	cmd.Flags().BoolVar(&dry, flagDryRun, false, "list what would be removed and remove nothing")
	cmd.Flags().BoolVar(&index, flagIndexStore, false, "prune the workspace index's stores instead of this repo's")
	cmd.Flags().StringVar(&keep, flagKeep, DefaultKeep, "keep dead bodies younger than this (e.g. 30d)")
	cmd.Flags().StringVar(&hash, flagHash, "", "remove one body by hash, regardless of liveness; needs --force")
	cmd.Flags().BoolVar(&force, flagForce, false, "with --hash, remove a body that is still live")
	return cmd
}

// pruneOne removes a single body by hash. It exists for the case §12 cannot
// undo any other way: a value that was secret before anyone marked it, now
// sitting in a content-addressed store. Removing a live body is destructive,
// so it needs --force and is recorded in the audit log.
func (a *App) pruneOne(out io.Writer, st *Store, cfg config.Config, hash string, dry, force, index bool) error {
	if !hashNameOK(hash) {
		return fmt.Errorf("%w: --%s %q is not a block hash", ErrUsage, flagHash, hash)
	}
	prev, refs, acks, err := st.LoadState()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPruneState, err)
	}
	live, err := st.liveFrom(prev, refs, acks)
	if err != nil {
		return err
	}
	if live[hash] && !force {
		return fmt.Errorf("%w: %s is still live; --%s removes it anyway and a later check will report its changes as `unknown`", ErrUsage, short(hash), flagForce)
	}
	dirs := []string{st.path(BlocksDir)}
	if index {
		indexDir, _ := a.indexPath(cfg)
		entries, err := a.indexEntriesFor(cfg)
		if err != nil {
			return err
		}
		for _, e := range entries {
			dirs = append(dirs, filepath.Join(indexDir, filepath.FromSlash(e.Dir()), BlocksDir))
		}
	}
	removed := 0
	for _, dir := range dirs {
		p := filepath.Join(dir, hash)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if dry {
			fmt.Fprintf(out, "would remove %s (--dry-run)\n", p)
			removed++
			continue
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		fmt.Fprintf(out, "removed %s\n", p)
		removed++
	}
	if removed == 0 {
		fmt.Fprintf(out, "%s is not in any body store\n", short(hash))
		return nil
	}
	if dry {
		return nil
	}
	// The removal is recorded where every other deliberate act is. A body
	// that held a value someone had to erase is exactly the event an audit
	// should be able to find later. The log is the one loaded above, so the
	// record cannot disagree with the liveness decision it followed.
	return st.AppendAcks([]ledger.Ack{{
		At: a.now(), Actor: a.actor(""), ActorKind: ledger.ActorHuman,
		Repo: a.repoName(cfg),
		Note: fmt.Sprintf("prune --%s %s: body removed from %d store(s)", flagHash, short(hash), removed),
	}})
}

// pruneIndex prunes the workspace index. The live set there is the union of
// every published repo's, because one repo's ack can name a body that only
// the defining repo's directory holds — so a per-repo view would delete a
// body another repo still needs. It runs only from the default branch and
// only against a freshly fetched index, since pruning from a stale view of
// who cites what is exactly how that happens.
func (a *App) pruneIndex(out io.Writer, st *Store, cfg config.Config, keep time.Duration, dry bool) error {
	if cfg.Workspace == "" {
		return ErrNoWorkspace
	}
	state, err := a.syncIndex(cfg, true)
	if err != nil {
		return err
	}
	if state.Offline {
		return fmt.Errorf("%w: the index could not be fetched", ErrPruneIndex)
	}
	want := state.defaultBranch()
	if branch, err := a.vcs.Branch(); err != nil || branch != want {
		return fmt.Errorf("%w: on %q, default is %q", ErrPruneIndex, branch, want)
	}
	live := indexLive(state.Entries)
	// The local repo's own state counts too: it may hold acks that have not
	// been published yet.
	mine, err := st.liveHashes()
	if err != nil {
		return err
	}
	for h := range mine {
		live[h] = true
	}
	total, removed := 0, 0
	for _, e := range state.Entries {
		dir := filepath.Join(state.Dir, filepath.FromSlash(e.Dir()), BlocksDir)
		dead, n, _, err := deadIn(dir, live, keep, time.Now())
		if err != nil {
			return err
		}
		total += n
		for _, d := range dead {
			if dry {
				fmt.Fprintf(out, "  would remove  %s  %s\n", short(d.Hash), d.Path)
				removed++
				continue
			}
			if err := os.Remove(d.Path); err != nil {
				return err
			}
			removed++
		}
	}
	verb := "removed"
	if dry {
		verb = "would remove"
	}
	fmt.Fprintf(out, "index: %d bodies, %d live, %s %d\n", total, len(live), verb, removed)
	return nil
}

// indexLive unions the live hashes of every repo published into the index.
func indexLive(entries []workspace.Entry) map[string]bool {
	live := map[string]bool{}
	for _, e := range entries {
		for _, r := range e.Ledger.Rows {
			live[r.Hash] = true
		}
		for _, r := range e.Refs.Rows {
			live[r.AckedHash] = true
			live[r.SeenHash] = true
		}
	}
	delete(live, "")
	return live
}

// indexEntriesFor reads the index without fetching, for commands that only
// need to know where the stores are.
func (a *App) indexEntriesFor(cfg config.Config) ([]workspace.Entry, error) {
	if cfg.Workspace == "" {
		return nil, nil
	}
	state, err := a.syncIndex(cfg, false)
	if err != nil {
		return nil, err
	}
	return state.Entries, nil
}

// report prints what prune found and removes it unless this is a dry run.
func report(out io.Writer, dir string, dead []deadBody, total, live, kept int, keep string, dry bool) error {
	var bytes int64
	for _, d := range dead {
		bytes += d.Size
	}
	held := ""
	if kept > 0 {
		held = fmt.Sprintf(", %d within the %s grace period", kept, keep)
	}
	fmt.Fprintf(out, "%s: %d bodies, %d live, %d dead (%d bytes)%s\n", dir, total, live, len(dead), bytes, held)
	for _, d := range dead {
		fmt.Fprintf(out, "  dead  %s  %s old\n", short(d.Hash), roundDays(d.Age))
	}
	if dry {
		fmt.Fprintf(out, "removed nothing (--dry-run)\n")
		return nil
	}
	for _, d := range dead {
		if err := os.Remove(d.Path); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "removed %d bodies\n", len(dead))
	return nil
}

func roundDays(d time.Duration) string {
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// pruneRatio is the share of dead bodies above which doctor suggests a
// prune. Below it the store is doing its job; above it the history is
// mostly superseded and is being committed and published for nothing.
const pruneRatio = 2

// blocksRow reports the body store's size and how much of it is still
// reachable, so a store that has quietly grown is visible without anyone
// having to go looking.
func (a *App) blocksRow(st *Store) []string {
	live, err := st.liveHashes()
	if err != nil {
		return []string{"blocks", doctorWarn, err.Error()}
	}
	dead, total, _, err := deadIn(st.path(BlocksDir), live, 0, time.Now())
	if err != nil {
		return []string{"blocks", doctorWarn, err.Error()}
	}
	reachable := total - len(dead)
	detail := fmt.Sprintf("%d bodies, %d live", total, reachable)
	if reachable > 0 && total > reachable*pruneRatio {
		return []string{"blocks", doctorWarn, detail + "; run `ds prune`"}
	}
	return []string{"blocks", doctorOK, detail}
}
