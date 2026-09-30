package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/match"
)

// Snapshot staleness (§21). `--frozen` made a check reproducible by pinning
// the foreign blocks it resolves against; the cost is that a pinned view
// ages silently. A frozen check stays green indefinitely while the repo
// drifts from the spec it claims to implement, and nothing in the tool says
// so. This is the other half: knowing when the pin has gone stale.
//
// It is reported, never enforced. Staleness is information — the whole point
// of the snapshot is that time does not change the answer — so it never
// changes an exit code and never becomes a finding.

// StaleBlock is one cited block whose upstream has moved past the snapshot.
type StaleBlock struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	// Classes describe the difference when both bodies are available, so a
	// reader can tell a signature change from a comment before deciding
	// whether syncing is urgent.
	Classes []block.Class `json:"class,omitempty"`
	// Gone says the publishing repo no longer defines this id. It is the
	// most urgent state, not the quietest: the next `ds sync` turns every
	// citation of it `broken`, so hearing about it before that sync is the
	// whole value. It must never share a branch with "unchanged" — doing so
	// reported a repo whose cited block had been deleted as up to date.
	Gone bool `json:"gone,omitempty"`
}

// StaleRepo is one upstream repository's position relative to the snapshot.
type StaleRepo struct {
	Repo string `json:"repo"`
	// Cited is how many of this repo's blocks the snapshot records; Behind
	// how many of those have moved on.
	Cited  int `json:"cited"`
	Behind int `json:"behind"`
	// SnapshotCommit is what the snapshot recorded; IndexCommit what the
	// index publishes now. Either may be empty when a repo published
	// without a commit.
	SnapshotCommit string       `json:"snapshot_commit,omitempty"`
	IndexCommit    string       `json:"index_commit,omitempty"`
	Blocks         []StaleBlock `json:"blocks,omitempty"`
	// Compared is false when the index publishes nothing for this repo, so
	// its blocks could not be looked up at all. Reporting that as "up to
	// date" would be a claim about blocks nobody read — the same silent
	// pass the whole snapshot section exists to prevent.
	Compared bool `json:"compared"`
}

// Staleness is the snapshot section of `ds status`.
type Staleness struct {
	// Present is false when this repo has no snapshot, which is the normal
	// state for a repo with no workspace.
	Present bool `json:"present"`
	// SyncedAt is when the snapshot was written; Age its age.
	SyncedAt time.Time `json:"synced_at,omitempty"`
	Age      string    `json:"age,omitempty"`
	// Compared is false when no index was reachable. The repos list is then
	// empty and the reader is told the comparison was skipped, rather than
	// being shown a silent "up to date" that was never checked.
	Compared bool        `json:"compared"`
	Repos    []StaleRepo `json:"repos,omitempty"`
}

// staleness compares the committed snapshot against the index. A missing
// snapshot or an unreachable index is a state to report, never an error:
// `status` describes, it does not gate.
func (a *App) staleness(cfg config.Config, bodyAt func(string) (string, bool)) Staleness {
	snap, ok, err := NewStore(a.dir).LoadForeign()
	if err != nil || !ok {
		return Staleness{}
	}
	out := Staleness{Present: true, SyncedAt: snap.Header.ScannedAt, Age: ago(snap.Header.ScannedAt, a.now())}
	if cfg.Workspace == "" {
		return out
	}
	st, err := a.syncIndex(cfg, false)
	if err != nil || st.Offline {
		return out
	}
	out.Compared = true
	// Keyed by id and environment, as match keys a def: a row published
	// for prod is measured against prod, not against whichever
	// environment's def of the id the index listed last.
	current := map[string]block.Block{}
	for _, b := range st.Merged.Defs {
		current[match.BlockKey(b)] = b
	}
	byRepo := map[string]*StaleRepo{}
	for _, row := range snap.Rows {
		r, seen := byRepo[row.Repo]
		if !seen {
			published, inIndex := st.Merged.Published[row.Repo]
			r = &StaleRepo{Repo: row.Repo, SnapshotCommit: row.Commit, IndexCommit: published.Commit, Compared: inIndex}
			byRepo[row.Repo] = r
		}
		r.Cited++
		now, still := current[match.RowKey(row.Row)]
		switch {
		case !still:
			r.Behind++
			r.Blocks = append(r.Blocks, StaleBlock{ID: row.ID, From: row.Hash, Gone: true})
		case now.Hash != row.Hash:
			r.Behind++
			r.Blocks = append(r.Blocks, StaleBlock{ID: row.ID, From: row.Hash, To: now.Hash, Classes: staleClasses(row, now, bodyAt)})
		}
	}
	for _, r := range byRepo {
		sort.Slice(r.Blocks, func(i, j int) bool { return r.Blocks[i].ID < r.Blocks[j].ID })
		out.Repos = append(out.Repos, *r)
	}
	sort.Slice(out.Repos, func(i, j int) bool { return out.Repos[i].Repo < out.Repos[j].Repo })
	return out
}

// staleClasses describes a difference when the body store holds both sides;
// nil when it does not, because naming a class the bodies cannot support
// would be a guess.
func staleClasses(row ledger.ForeignRow, now block.Block, bodyAt func(string) (string, bool)) []block.Class {
	if bodyAt == nil {
		return nil
	}
	oldBody, haveOld := bodyAt(row.Hash)
	newBody, haveNew := bodyAt(now.Hash)
	if !haveOld || !haveNew {
		return nil
	}
	old, nw := row.ToBlock(), now
	old.Content, nw.Content = oldBody, newBody
	st, ok := extract.StyleFor(now.Pos.File)
	if !ok {
		return match.Classify(old, nw, oldBody, nil)
	}
	return match.Classify(old, nw, oldBody, st.Line)
}

// printStaleness renders the snapshot section above the per-reference rows.
func printStaleness(w io.Writer, s Staleness) {
	if !s.Present {
		return
	}
	fmt.Fprintf(w, "snapshot  %s/%s   synced %s\n", DirName, ledger.ForeignFile, s.Age)
	if !s.Compared {
		fmt.Fprintln(w, "  comparison skipped: no workspace index available")
		return
	}
	for _, r := range s.Repos {
		if !r.Compared {
			fmt.Fprintf(w, "  %-8s not published in the index; %d cited block(s) could not be compared\n", r.Repo, r.Cited)
			continue
		}
		if r.Behind == 0 {
			fmt.Fprintf(w, "  %-8s up to date\n", r.Repo)
			continue
		}
		fmt.Fprintf(w, "  %-8s %d of %d cited blocks behind upstream   (snapshot %s -> index %s)\n",
			r.Repo, r.Behind, r.Cited, short(orMissingCommit(r.SnapshotCommit)), short(orMissingCommit(r.IndexCommit)))
		for _, b := range r.Blocks {
			if b.Gone {
				fmt.Fprintf(w, "            %s   %s -> gone   no longer published; a sync will make its citations broken\n", b.ID, short(b.From))
				continue
			}
			fmt.Fprintf(w, "            %s   %s -> %s   %s\n", b.ID, short(b.From), short(b.To), classList(b.Classes))
		}
	}
}

// orMissingCommit keeps a column readable when a repo published without one.
func orMissingCommit(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// classList renders classes for the staleness table; unknown when the
// bodies were not available to compare.
func classList(cs []block.Class) string {
	if len(cs) == 0 {
		return string(block.ClassUnknown)
	}
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = string(c)
	}
	return strings.Join(parts, ", ")
}

// warnStaleSnapshot is the opt-in nudge for a CI that never syncs. It is a
// warning on stderr and nothing else: making age fail a frozen check would
// make its result depend on the clock, which is the one property --frozen
// exists to remove. Off unless [check] snapshot_max_age is set.
func warnStaleSnapshot(w io.Writer, st *Store, cfg config.Config, now time.Time) {
	if cfg.Check.SnapshotMaxAge == "" {
		return
	}
	// Validated when the config is loaded.
	max, _ := check.ParseDuration(cfg.Check.SnapshotMaxAge)
	snap, ok, err := st.LoadForeign()
	if err != nil || !ok || snap.Header.ScannedAt.IsZero() {
		return
	}
	if age := now.Sub(snap.Header.ScannedAt); age > max {
		fmt.Fprintf(w, "warning: %s/%s was synced %s, older than check.snapshot_max_age (%s); run `ds sync`\n",
			DirName, ledger.ForeignFile, ago(snap.Header.ScannedAt, now), cfg.Check.SnapshotMaxAge)
	}
}
