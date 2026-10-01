package cli

import (
	"context"
	"fmt"

	docsync "github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/match"
)

// tokensLine is the closing line of `ds context`: the tokens used, and the
// budget when there is one. With no budget it printed "N tokens used of 0",
// which reads as a budget of zero already overrun (bug 65).
func tokensLine(used, budget int) string {
	if budget <= 0 {
		return fmt.Sprintf("%d tokens used, no budget", used)
	}
	return fmt.Sprintf("%d tokens used of %d", used, budget)
}

// contextSince checks a `context --since` value and returns the hook that
// reads a block's body at a commit when it names one. "" and "ack" need no
// hook; anything else must be a commit this repository has. It used to
// accept any value and diff against the last scan whatever it said
// (bug 61).
//
// The body at the commit comes from the file the commit's ledger says held
// the id (today's file when that ledger has no row for it), run through the
// scan pipeline so it has the same shape as the live body, and is paired by
// id, environment and branch. A block merged from another repository has no
// history here and reports none.
func (a *App) contextSince(ctx context.Context, sys *docsync.System, since string) (func(block.Block) (string, bool), error) {
	if since == "" || since == docsync.SinceAck {
		return nil, nil
	}
	if !a.vcs.Exists(since) {
		return nil, fmt.Errorf("--since %q is neither %q nor a commit in this repository", since, docsync.SinceAck)
	}
	var index map[string]string
	bodies := map[string]map[string]string{}
	return func(b block.Block) (string, bool) {
		if b.Args[block.KeyRepo] != "" {
			return "", false
		}
		if index == nil {
			index = map[string]string{}
			for id, row := range a.ledgerAt(since) {
				index[id] = row.File
			}
		}
		file, ok := index[b.ID]
		if !ok {
			file = b.Pos.File
		}
		inFile, read := bodies[file]
		if !read {
			inFile = map[string]string{}
			if src, err := a.vcs.Show(since, file); err == nil {
				if defs, err := sys.ExtractFile(ctx, file, src); err == nil {
					for _, d := range defs {
						inFile[match.BlockKey(d)] = d.Content
					}
				}
			}
			bodies[file] = inFile
		}
		body, ok := inFile[match.BlockKey(b)]
		return body, ok
	}, nil
}
