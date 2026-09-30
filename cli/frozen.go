package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/match"
	"github.com/ubgo/docsync/workspace"
)

// The foreign snapshot (§21). `check` in a repo that cites another repo was
// not reproducible: its answer for one commit depended on what upstream had
// last published and on when the index was last synced, so CI could go red
// with no change in the repo under test and an old commit could not be
// re-run. `.ds/foreign.tsv` is the committed record of the foreign blocks
// this repo cites, and `--frozen` resolves them from it alone.
//
// The division of labour is a lockfile's: `sync` is the deliberate act of
// taking upstream's changes, it shows up as a diff in a pull request, and
// that is where the resulting findings get reviewed — rather than landing
// on whoever pushes next.
const (
	flagFrozen = "frozen"
	flagSync   = "sync"
	// ciEnv is set by every major CI system. A run there defaults to
	// --frozen, because a build that fails for a reason absent from its own
	// diff is worse than one that lags a publish.
	ciEnv = "CI"
)

// ErrNoSnapshot is returned by --frozen when no snapshot has been written.
// It never degrades to "no foreign blocks": a frozen check with nothing to
// resolve against would pass vacuously, reporting green for citations it
// never looked at.
var ErrNoSnapshot = fmt.Errorf("%s/%s not found; run `ds sync` to record the foreign blocks this repo cites", DirName, ledger.ForeignFile)

// LoadForeign reads the snapshot; ok is false when the file is absent.
func (s *Store) LoadForeign() (ledger.Foreign, bool, error) {
	raw, ok, err := s.read(ledger.ForeignFile)
	if err != nil || !ok {
		return ledger.Foreign{}, false, err
	}
	f, err := ledger.DecodeForeign(bytes.NewReader(raw))
	if err != nil {
		return ledger.Foreign{}, false, decodeError(ledger.ForeignFile, ledger.ForeignFile, err)
	}
	return f, true, nil
}

// SaveForeign writes the snapshot.
func (s *Store) SaveForeign(f ledger.Foreign) error {
	return s.Write(ledger.ForeignFile, f.Bytes())
}

// frozenOptions resolves foreign blocks from the committed snapshot instead
// of the live index, and fails rather than pass vacuously when there is none.
func (a *App) frozenOptions(st *Store, cfg config.Config) ([]docsync.Option, error) {
	f, ok, err := st.LoadForeign()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoSnapshot
	}
	a.indexEntries = nil
	if dir, _ := a.indexPath(cfg); dir != "" {
		if _, err := os.Stat(dir); err == nil {
			// The bodies a frozen check needs to classify a drift live in
			// the local index copy, which sync keeps in step with the
			// snapshot. Its absence is not fatal: a drift then reports
			// `unknown` rather than nothing.
			a.indexFS = os.DirFS(dir)
			if entries, err := workspace.Read(a.indexFS); err == nil {
				a.indexEntries = entries
			}
		}
	}
	return []docsync.Option{docsync.WithMerged(f.Blocks()...)}, nil
}

// citedIDs are the ids this repo's own references point at, which is what a
// snapshot records. Reading them from the refs file rather than rescanning
// keeps `sync` cheap and makes it depend on committed state.
func citedIDs(refs ledger.Refs) map[workspace.Cite]bool {
	out := map[workspace.Cite]bool{}
	for _, r := range refs.Rows {
		if r.ID != "" {
			out[workspace.Cite{ID: r.ID, Branch: r.Args[block.KeyBranch]}] = true
		}
	}
	return out
}

// snapshotChangeKind is what happened to one cited id between the snapshot
// on disk and the one a sync would write.
type snapshotChangeKind string

const (
	snapshotAdded   snapshotChangeKind = "added"
	snapshotRemoved snapshotChangeKind = "removed"
	snapshotChanged snapshotChangeKind = "changed"
	// snapshotMoved is the same content at a new position. It has to be
	// reported: a pure move rewrites the recorded lines, so the file
	// changes and a sync that said "unchanged" would be describing a git
	// diff as nothing.
	snapshotMoved snapshotChangeKind = "moved"
)

// snapshotChange is one difference a sync is about to record.
type snapshotChange struct {
	ID       string
	Kind     snapshotChangeKind
	From, To string // hashes, for changed
	At       string // the new position, for moved
	Was      string // the old position, for moved
}

// position renders a row's place for a move message.
func position(r ledger.ForeignRow) string {
	return fmt.Sprintf("%s:%d-%d", r.File, r.Start, r.End)
}

// diffSnapshot compares two snapshots def by def — id, environment, branch,
// as match.RowKey — so `sync` can say what it is recording instead of
// leaving the reader to read a TSV diff. By id alone, a prod row was
// compared with a dev row and reported as changed.
//
// It compares content *and* position, because either rewrites the file.
// Comparing hashes alone was wrong in a way that undermined the point of
// the snapshot: a block that moved upstream produced a real git diff and a
// summary that said nothing had changed.
func diffSnapshot(old, new ledger.Foreign) []snapshotChange {
	was := map[string]ledger.ForeignRow{}
	for _, r := range old.Rows {
		was[match.RowKey(r.Row)] = r
	}
	now := map[string]ledger.ForeignRow{}
	for _, r := range new.Rows {
		now[match.RowKey(r.Row)] = r
	}
	var out []snapshotChange
	for key, r := range now {
		id := r.ID
		prev, had := was[key]
		switch {
		case !had:
			out = append(out, snapshotChange{ID: id, Kind: snapshotAdded, To: r.Hash})
		case prev.Hash != r.Hash:
			out = append(out, snapshotChange{ID: id, Kind: snapshotChanged, From: prev.Hash, To: r.Hash})
		case position(prev) != position(r):
			out = append(out, snapshotChange{ID: id, Kind: snapshotMoved, Was: position(prev), At: position(r)})
		}
	}
	for key, r := range was {
		if _, still := now[key]; !still {
			out = append(out, snapshotChange{ID: r.ID, Kind: snapshotRemoved, From: r.Hash})
		}
	}
	// One id can have a row per environment or branch, so the id alone does
	// not order them; the rest of the change does, and the summary a sync
	// prints must not depend on map iteration.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return string(a.Kind)+a.From+a.To+a.Was+a.At < string(b.Kind)+b.From+b.To+b.Was+b.At
	})
	return out
}

// rowsEqual reports whether two snapshots record the same rows, ignoring the
// header. The header carries the sync time and this repo's commit, which
// change on every run; the rows are what a reader reviews.
func rowsEqual(a, b ledger.Foreign) bool {
	a.Header, b.Header = ledger.Header{}, ledger.Header{}
	return bytes.Equal(a.Bytes(), b.Bytes())
}

// printChanges renders what a sync recorded.
//
// Invariant: it prints snapshotUnchanged if and only if the rows are
// identical. The file itself is rewritten either way, because the header
// records when the pin was last confirmed and `status` and
// `snapshot_max_age` both read that — so the message says which of the two
// happened rather than implying the file was left alone.
func printChanges(w io.Writer, changes []snapshotChange, rowsSame bool) {
	if len(changes) == 0 {
		if rowsSame {
			fmt.Fprintf(w, "%s: %s\n", ledger.ForeignFile, snapshotUnchanged)
			return
		}
		fmt.Fprintf(w, "%s: upstream commits recorded; no cited block changed\n", ledger.ForeignFile)
		return
	}
	for _, c := range changes {
		switch c.Kind {
		case snapshotAdded:
			fmt.Fprintf(w, "  + %s now cited at %s\n", c.ID, short(c.To))
		case snapshotRemoved:
			fmt.Fprintf(w, "  - %s no longer published\n", c.ID)
		case snapshotChanged:
			fmt.Fprintf(w, "  ~ %s %s -> %s\n", c.ID, short(c.From), short(c.To))
		case snapshotMoved:
			fmt.Fprintf(w, "  > %s moved %s -> %s\n", c.ID, c.Was, c.At)
		}
	}
}

// snapshotUnchanged is the wording for a sync whose rows are identical. It
// is deliberately not a substring of the metadata-only message: two states
// a reader must distinguish cannot be told apart by a phrase common to
// both, and a test that matched one would silently match the other.
const snapshotUnchanged = "no change to record"

// missingBodies counts the hashes in a snapshot with no body in the local
// index copy. `sync` pulls the whole index, bodies included, so a miss means
// the publishing repo predates the body store or pruned it — which degrades
// a later frozen check to `unknown` rather than to silence. Reporting the
// count is honest; inventing the bodies is not possible.
func missingBodies(indexDir string, f ledger.Foreign) int {
	if indexDir == "" {
		return 0
	}
	n := 0
	for _, r := range f.Rows {
		if r.Hash == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(indexDir, workspace.ReposDir, r.Repo, workspace.BlocksDir, r.Hash)); err != nil {
			n++
		}
	}
	return n
}

// rowsOf flattens a snapshot to ledger rows, which is what the library's
// change matcher takes.
func rowsOf(f ledger.Foreign) []ledger.Row {
	out := make([]ledger.Row, 0, len(f.Rows))
	for _, r := range f.Rows {
		out = append(out, r.Row)
	}
	return out
}
