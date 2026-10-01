package cli

import (
	"bytes"
	"path"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/match"
)

// snapshotAt answers the library's SnapshotBlock hook, which renders a
// `ds:block … at=<sha>` as the block was at that commit: the ledger as
// committed at sha says which file held the id, and that file at sha is run
// through the scan pipeline (extractFile), so the snapshot has the same
// shape as a live block and its caption and link name the lines it occupied
// then. Anything missing renders as a link only, which the renderer already
// handles.
//
// Every `render` installs it, not only `render --at`: a page that pins a
// snapshot with at= used to render the link alone, noting "no snapshot",
// unless the whole page was rendered with --at (bug 66). sha is the commit
// asked for when a directive names none, which the renderer never does
// today; it keeps the hook total.
//
// Ledgers are read once per commit: a page with two snapshots at two
// commits reads two ledgers, where one cached index used to answer every
// commit with the first one's rows.
func (a *App) snapshotAt(sha string, extractFile func(path string, src []byte) ([]block.Block, error)) func(id, at string) (block.Block, bool) {
	indexes := map[string]map[string]ledger.Row{}
	return func(id, at string) (block.Block, bool) {
		if at == "" {
			at = sha
		}
		index, seen := indexes[at]
		if !seen {
			index = a.ledgerAt(at)
			indexes[at] = index
		}
		row, ok := index[id]
		if !ok {
			return block.Block{}, false
		}
		src, err := a.vcs.Show(at, row.File)
		if err != nil {
			return block.Block{}, false
		}
		defs, err := extractFile(row.File, src)
		if err != nil {
			return block.Block{}, false
		}
		key := match.RowKey(row)
		for _, b := range defs {
			if match.BlockKey(b) == key {
				return b, true
			}
		}
		return block.Block{}, false
	}
}

// ledgerAt reads the ledger committed at sha, indexed by id: .ds/ledger.tsv,
// or, in a sharded repository, every .ds/ledger/*.tsv, which needs a VCS
// that lists a tree (TreeVCS). nil when there is none.
func (a *App) ledgerAt(sha string) map[string]ledger.Row {
	if raw, err := a.vcs.Show(sha, path.Join(DirName, LedgerFile)); err == nil {
		if l, err := ledger.DecodeLedger(bytes.NewReader(raw)); err == nil {
			return l.Index()
		}
		return nil
	}
	tv, ok := a.vcs.(TreeVCS)
	if !ok {
		return nil
	}
	tree, err := tv.Tree(sha)
	if err != nil {
		return nil
	}
	index := map[string]ledger.Row{}
	shardDir := path.Join(DirName, LedgerDir) + "/"
	for p := range tree {
		if !strings.HasPrefix(p, shardDir) || path.Ext(p) != ".tsv" {
			continue
		}
		raw, err := a.vcs.Show(sha, p)
		if err != nil {
			continue
		}
		if l, err := ledger.DecodeLedger(bytes.NewReader(raw)); err == nil {
			for id, row := range l.Index() {
				index[id] = row
			}
		}
	}
	return index
}
