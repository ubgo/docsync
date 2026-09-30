package cli

import (
	"bytes"
	"path"
	"strings"

	"github.com/ubgo/docsync/ledger"
)

// snapshotAt answers the library's Snapshot hook for `render --at`: the
// ledger as committed at sha says where each block was, and the VCS gives
// the file at that commit. Anything missing renders as a link only, which
// the renderer already handles.
func (a *App) snapshotAt(sha string) func(id, at string) (string, bool) {
	var index map[string]ledger.Row
	return func(id, at string) (string, bool) {
		if at == "" {
			at = sha
		}
		if index == nil {
			raw, err := a.vcs.Show(at, path.Join(DirName, LedgerFile))
			if err != nil {
				return "", false
			}
			l, err := ledger.DecodeLedger(bytes.NewReader(raw))
			if err != nil {
				return "", false
			}
			index = l.Index()
		}
		row, ok := index[id]
		if !ok {
			return "", false
		}
		src, err := a.vcs.Show(at, row.File)
		if err != nil {
			return "", false
		}
		lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
		if row.Start < 1 || row.End > len(lines) || row.End < row.Start {
			return "", false
		}
		return strings.Join(lines[row.Start-1:row.End], "\n"), true
	}
}
