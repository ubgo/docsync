package cli

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/directive"
	"github.com/ubgo/docsync/ledger"
)

// `ds undo` reverses source writes from the journal (§33 safety). The stack
// is deep — it reaches back to the first write this checkout ever made — so
// the command's job is as much to refuse as to act:
//
//   - The default boundary is the last commit. A write already in history is
//     no longer a recent mistake; docs cite it and, with a workspace index,
//     other repos may too. Reversing it is a deliberate change that belongs
//     in an ordinary edit and a review, and `--force` says so out loud.
//   - A def that sentences still cite is not removed silently. `check` would
//     report `broken` afterwards, but afterwards is too late: the source has
//     already changed. `--orphan` accepts the breakage knowingly.
//   - Every run says what the next entry is, because the accident this
//     guards against is pressing undo once more than intended.
const (
	flagList   = "list"
	flagOrphan = "orphan"
)

// undoStatus is where an entry sits relative to git history.
type undoStatus string

const (
	// statusUncommitted is a write not yet in history: undo's proper scope.
	statusUncommitted undoStatus = "uncommitted"
	// statusCommitted is a write present in HEAD.
	statusCommitted undoStatus = "committed"
	// statusUnknown is a repo with no git, or no commits yet. Every write
	// counts as uncommitted there, because there is no history to protect.
	statusUnknown undoStatus = "no git"
)

// undoEntry is one journal entry with everything the command needs to decide
// and to explain: where it sits in history, the def it would remove, and who
// cites that def.
type undoEntry struct {
	journalEntry
	Status undoStatus
	Commit string
	// ID is the def the reverted line defines, empty when the write was not
	// a def (an adopted link, a renamed doc).
	ID string
	// Citers are the sentences that would be left pointing at nothing,
	// including ones published by other repositories in the workspace.
	Citers []string
}

func (a *App) undoCmd() *cobra.Command {
	var list, dry, force, orphan bool
	cmd := &cobra.Command{
		Use:   "undo",
		Short: "reverse the last uncommitted source write made by def, adopt, or rename",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st := NewStore(a.dir)
			if !st.Exists() {
				return ErrNotInitialised
			}
			entries, err := st.journal()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if list {
				return a.printUndoList(out, st, entries)
			}
			if len(entries) == 0 {
				return ErrNothingToUndo
			}
			batch, err := a.describeBatch(st, entries, lastBatch(entries))
			if err != nil {
				return err
			}
			if !force && batch[0].Status == statusCommitted {
				return fmt.Errorf("%w.\n    next entry is committed (%s, %s): %s\n    to reverse it anyway: %s",
					ErrUndoCommitted, short(batch[0].Commit), ago(batch[0].At, a.now()), describe(batch), a.retry(true, orphan))
			}
			if !orphan {
				if msg := orphanMessage(batch, a.retry(force, true)); msg != "" {
					return fmt.Errorf("%w:\n%s", ErrUndoOrphans, msg)
				}
			}
			if dry {
				fmt.Fprintf(out, "would undo %s (--dry-run)\n", describe(batch))
				for _, e := range batch {
					fmt.Fprintf(out, "  %s:%d\n    - %s\n    + %s\n", e.Edit.File, e.Edit.Line, e.Edit.New, orEmptyLine(e.Edit.Old))
				}
				return nil
			}
			edits, err := st.Undo()
			for _, e := range edits {
				fmt.Fprintf(out, undoneTemplate, e.File, e.Line)
			}
			if err != nil {
				return err
			}
			return a.printNext(out, st)
		},
	}
	cmd.Flags().BoolVar(&list, flagList, false, "show the undo stack, newest first, and write nothing")
	cmd.Flags().BoolVar(&dry, flagDryRun, false, "print the edit that would be made and write nothing")
	cmd.Flags().BoolVar(&force, flagForce, false, "reverse a write that is already committed")
	cmd.Flags().BoolVar(&orphan, flagOrphan, false, "reverse even when it leaves a cited def undefined")
	return cmd
}

// printUndoList renders the stack newest first, one line per batch.
func (a *App) printUndoList(out io.Writer, st *Store, entries []journalEntry) error {
	if len(entries) == 0 {
		fmt.Fprintln(out, "nothing to undo")
		return nil
	}
	var batches []int
	seen := map[int]bool{}
	for i := len(entries) - 1; i >= 0; i-- {
		if !seen[entries[i].Batch] {
			seen[entries[i].Batch] = true
			batches = append(batches, entries[i].Batch)
		}
	}
	rows := [][]string{{"#", "KIND", "WHERE", "ID", "AGE", "STATE"}}
	for n, b := range batches {
		batch, err := a.describeBatch(st, entries, b)
		if err != nil {
			return err
		}
		state := string(batch[0].Status)
		if batch[0].Status == statusCommitted {
			state += " " + short(batch[0].Commit)
		}
		if cited := allCiters(batch); len(cited) > 0 {
			state += " · cited by " + strings.Join(cited, ", ")
		}
		rows = append(rows, []string{fmt.Sprint(n + 1), orUnknown(batch[0].Kind), where(batch), orUnknown(batch[0].ID), ago(batch[0].At, a.now()), state})
	}
	table(out, rows)
	return nil
}

// printNext says what a further undo would reach, so the reader never has to
// guess whether pressing it again is safe.
func (a *App) printNext(out io.Writer, st *Store) error {
	entries, err := st.journal()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Fprintln(out, "next: nothing left to undo.")
		return nil
	}
	batch, err := a.describeBatch(st, entries, lastBatch(entries))
	if err != nil {
		return err
	}
	if batch[0].Status == statusCommitted {
		fmt.Fprintf(out, "next: nothing uncommitted left to undo; the next entry is committed (%s): %s\n", short(batch[0].Commit), describe(batch))
		return nil
	}
	fmt.Fprintf(out, "next: %s\n", describe(batch))
	return nil
}

// describeBatch annotates every entry of one batch with its history status,
// the def it would remove, and that def's citers.
func (a *App) describeBatch(st *Store, entries []journalEntry, batch int) ([]undoEntry, error) {
	var out []undoEntry
	for _, e := range entries {
		if e.Batch != batch {
			continue
		}
		u := undoEntry{journalEntry: e, ID: defID(e.Edit.New, a.prefix())}
		u.Status, u.Commit = a.historyStatus(e.Edit)
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil, ErrNothingToUndo
	}
	citers, err := a.citations(st)
	if err != nil {
		return nil, err
	}
	for i, u := range out {
		if u.ID != "" {
			out[i].Citers = survivors(citers[u.ID], u.ID, out)
		}
	}
	return out, nil
}

// survivors drops the citations of id that this same batch wrote, and so
// removes when it is undone: `adopt` writes a def and the citation of it in
// one batch, and counting that citation made `adopt; scan; undo` — the
// sequence adopt itself prints — refuse, saying the undo would orphan a def
// whose only citer the undo was about to take away. A citation is dropped
// only when the batch holds an edit at exactly its line whose new text names
// the id; anything less certain still guards.
func survivors(citers []string, id string, batch []undoEntry) []string {
	written := map[string]bool{}
	for _, e := range batch {
		if strings.Contains(e.Edit.New, id) {
			written[fmt.Sprintf("%s:%d", e.Edit.File, e.Edit.Line)] = true
		}
	}
	var out []string
	for _, c := range citers {
		if !written[c] {
			out = append(out, c)
		}
	}
	return out
}

// historyStatus reports whether a write is already in HEAD. The test is the
// content itself — the written line present at its line in HEAD — rather
// than a stored commit id, so it stays correct across amends and rebases. A
// repo with no git, or no commits, has no history to protect and every write
// there counts as uncommitted.
//
// A written line is found anywhere in HEAD's file, not only at the line it
// was written to: a def committed and then pushed down by lines added above
// it is still in history, and was listed as "uncommitted" (bug 71). A
// deletion has no written line to look for and is judged at its line. The
// commit named is the one that introduced the line, where the VCS can say
// (introducer); it used to be HEAD, whatever commit had made the write.
func (a *App) historyStatus(e docsync.Edit) (undoStatus, string) {
	head, err := a.vcs.Head()
	if err != nil || head == "" {
		return statusUnknown, ""
	}
	src, err := a.vcs.Show(head, e.File)
	if err != nil {
		return statusUncommitted, head
	}
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	committed := e.Line >= 1 && e.Line <= len(lines) && lines[e.Line-1] == e.New
	if !e.Delete && e.New != "" && !committed {
		committed = slices.Contains(lines, e.New)
	}
	if !committed {
		return statusUncommitted, head
	}
	if in, ok := a.vcs.(introducer); ok && !e.Delete {
		if sha, err := in.Introduced(e.File, e.New); err == nil && sha != "" {
			return statusCommitted, sha
		}
	}
	return statusCommitted, head
}

// introducer is the optional VCS upgrade that names the commit which first
// added a line of text to a file, for `undo --list`. Git implements it; a
// VCS without it names HEAD.
type introducer interface {
	Introduced(path, text string) (string, error)
}

// Introduced returns the short sha of the oldest commit that changed how
// many times text occurs in path (git's pickaxe), which for a line written
// once is the commit that added it.
func (g Git) Introduced(path, text string) (string, error) {
	out, err := g.run("log", "--format=%h", "--abbrev=7", "--reverse", "-S"+text, "--", path)
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return first, nil
}

// citations maps an id to the sentences that cite it: this repo's reverse
// index plus every ref published by another repository in the workspace.
// The cross-repo half is the point — those are exactly the citations an
// author cannot see from inside their own checkout.
func (a *App) citations(st *Store) (map[string][]string, error) {
	out := map[string][]string{}
	_, refs, _, err := st.LoadState()
	if err != nil {
		return nil, err
	}
	add := func(repo, doc string, line int, id string) {
		where := fmt.Sprintf("%s:%d", doc, line)
		if repo != "" {
			where = repo + "/" + where
		}
		out[id] = appendOnce(out[id], where)
	}
	for _, r := range refs.Rows {
		add("", r.Doc, r.Line, r.ID)
	}
	cfg, err := a.loadConfig(st)
	if err != nil {
		return nil, err
	}
	for _, r := range a.mergedRefs(cfg) {
		add(r.Repo, r.Doc, r.Line, r.ID)
	}
	for id := range out {
		sort.Strings(out[id])
	}
	return out, nil
}

// mergedRefs are refs other repositories published into the workspace index,
// branches included: a def an unmerged branch still cites is cited, and the
// guard's job is to refuse rather than strand it.
// A workspace that is absent, unreachable, or never synced yields none: the
// guard degrades to the local answer rather than failing the undo, and the
// commit boundary still applies.
func (a *App) mergedRefs(cfg config.Config) []ledger.RefRow {
	if cfg.Workspace == "" {
		return nil
	}
	st, err := a.syncIndex(cfg, false)
	if err != nil {
		return nil
	}
	repo := a.repoName(cfg)
	_, refs := st.Merged.Others(repo)
	for _, r := range st.Merged.BranchRefs {
		if r.Repo != repo {
			refs = append(refs, r)
		}
	}
	return refs
}

// retry renders the command that gets past a refusal: the flags already in
// effect plus the one being asked for.
//
// The guards are independent, so an entry can be both committed and cited
// and a reader can meet them in either order. Naming only the new flag then
// sends them in a circle — the other guard refuses the retry and points
// back at the first — which is what `--orphan` alone did on a committed,
// cited def. Every hint therefore carries the whole command.
func (a *App) retry(force, orphan bool) string {
	cmd := a.name + " undo"
	if force {
		cmd += " --" + flagForce
	}
	if orphan {
		cmd += " --" + flagOrphan
	}
	return cmd
}

// orphanMessage lists the defs a batch would leave undefined, or "" when it
// would leave none. retry is the full command that proceeds anyway.
func orphanMessage(batch []undoEntry, retry string) string {
	var b strings.Builder
	for _, e := range batch {
		if len(e.Citers) == 0 {
			continue
		}
		fmt.Fprintf(&b, "    %s is cited by %s:\n", e.ID, plural(len(e.Citers), "sentence"))
		for _, c := range e.Citers {
			fmt.Fprintf(&b, "      %s\n", c)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	fmt.Fprintf(&b, "    removing the def will make it `broken`. Re-run with %s to proceed.", retry)
	return b.String()
}

// defID returns the id the directive on a line defines, or "" when the line
// carries no def. It is read from the line the undo would revert, which is
// where the directive the write inserted still is.
func defID(line, prefix string) string {
	head := prefix + directive.Separator
	i := strings.Index(line, head)
	if i < 0 {
		return ""
	}
	// The directive sits inside a comment, so the line may end with that
	// comment's closer. Parse would read one as a malformed key, which would
	// silently disable the citation guard for markdown and block-comment
	// defs, so the known closers are trimmed first.
	rest := line[i:]
	for _, closer := range commentClosers {
		if j := strings.Index(rest, closer); j >= 0 {
			rest = rest[:j]
		}
	}
	d, err := directive.Parse(prefix, rest)
	if err != nil || d.Verb != "def" {
		return ""
	}
	return d.Args[block.KeyID]
}

// commentClosers end a block comment that a directive may sit inside.
var commentClosers = []string{"-->", "*/", "#}", "--}}"}

func (a *App) prefix() string {
	cfg, err := a.loadConfig(NewStore(a.dir))
	if err != nil || cfg.Prefix == "" {
		return directive.DefaultPrefix
	}
	return cfg.Prefix
}

func lastBatch(entries []journalEntry) int { return entries[len(entries)-1].Batch }

// describe names a batch in one phrase for a message.
func describe(batch []undoEntry) string {
	s := orUnknown(batch[0].Kind) + " " + where(batch)
	if batch[0].ID != "" {
		s += " " + batch[0].ID
	}
	return s
}

// where names the edit's position, or the count when a batch spans several.
func where(batch []undoEntry) string {
	if len(batch) == 1 {
		return fmt.Sprintf("%s:%d", batch[0].Edit.File, batch[0].Edit.Line)
	}
	return fmt.Sprintf("%s:%d +%d more", batch[0].Edit.File, batch[0].Edit.Line, len(batch)-1)
}

func allCiters(batch []undoEntry) []string {
	var out []string
	for _, e := range batch {
		for _, c := range e.Citers {
			out = appendOnce(out, c)
		}
	}
	return out
}

func appendOnce(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// ago renders a duration the way a person reads a stack: coarse and recent.
func ago(at, now time.Time) string {
	if at.IsZero() {
		return "unknown"
	}
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hr ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func orUnknown(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orEmptyLine(s string) string {
	if s == "" {
		return "(line removed)"
	}
	return s
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
