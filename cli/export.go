package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/match"
)

// `ds export hugo --out DIR` (§24 "docs site", build order 8): everything a
// static site generator needs at build time, as data files. Hugo cannot run
// a program during a build, so the Hugo module in integrations/hugo reads
// `data/docsync/blocks.json` from a shortcode and `status.json` from a
// partial; this command writes both. Docusaurus runs the remark plugin
// instead, which calls `ds render` directly.
const (
	// ExportBlocksFile maps every local id to its rendered block.
	ExportBlocksFile = "blocks.json"
	// ExportStatusFile is `status --json`, so a site can paint freshness
	// dots beside cited sentences.
	ExportStatusFile = "status.json"
	exportHugo       = "hugo"
)

// exportedBlock is one entry of blocks.json. Rendered is a fenced code
// block ready for markdownify; the rest lets a template build its own.
type exportedBlock struct {
	ID       string      `json:"id"`
	Symbol   string      `json:"symbol,omitempty"`
	File     string      `json:"file"`
	Start    int         `json:"start"`
	End      int         `json:"end"`
	Lang     string      `json:"lang,omitempty"`
	Content  string      `json:"content"`
	Rendered string      `json:"rendered"`
	Change   match.State `json:"change"`
}

// statusRow is one reference's state, the `status --json` shape, and what
// a docs site needs to paint it (§24): the severity maps to green, amber,
// or red without the site copying the state table, and the ack behind the
// citation's baseline supplies the note shown on hover. Both were missing,
// so the dots and the note the spec promised could not be drawn.
type statusRow struct {
	Doc      string         `json:"doc"`
	Line     int            `json:"line"`
	ID       string         `json:"id,omitempty"`
	State    check.State    `json:"state"`
	Severity check.Severity `json:"severity"`
	// Note, AckedBy, and AckedAt describe the latest ack recorded against
	// the hash this citation was last acked at; all empty when nobody has.
	Note    string `json:"note,omitempty"`
	AckedBy string `json:"acked_by,omitempty"`
	AckedAt string `json:"acked_at,omitempty"`
}

// statusRows lists every reference the check saw with its state and the
// ack, from acks, that its baseline is.
func statusRows(rep docsync.Report, acks ledger.Acks) []statusRow {
	rows := []statusRow{}
	for _, f := range rep.Findings {
		if f.Verb == "" {
			continue
		}
		row := statusRow{Doc: f.Doc, Line: f.Line, ID: f.ID, State: f.State, Severity: f.Severity}
		if a, ok := baselineAck(f, acks); ok {
			row.Note, row.AckedBy, row.AckedAt = a.Note, a.Actor, a.At.UTC().Format(time.RFC3339)
		}
		rows = append(rows, row)
	}
	return rows
}

// baselineAck is the latest ack of f's citation: the same id and doc, the
// same sentence (or, for a citation with no sentence around it, the same
// line), at the block hash f was last acked against. A claim has no block,
// so any hash matches. The sentence keeps two citations of one id in one
// doc from lending each other notes, and the hash ties the note to the
// statement the reviewer actually saw.
func baselineAck(f check.Finding, acks ledger.Acks) (ledger.Ack, bool) {
	var ref block.Reference
	ref.SetSentence(f.Sentence)
	// An up-to-date finding leaves Acked out because it equals Current.
	baseline := f.Hash.Acked
	if baseline == "" {
		baseline = f.Hash.Current
	}
	var best ledger.Ack
	found := false
	for _, a := range acks.Rows {
		if a.Doc != f.Doc || a.ID != f.ID {
			continue
		}
		if f.Sentence != "" && a.SentenceHash != ref.SentenceHash || f.Sentence == "" && a.Line != f.Line {
			continue
		}
		if f.ID != "" && a.BlockHash != baseline {
			continue
		}
		if !found || !a.At.Before(best.At) {
			best, found = a, true
		}
	}
	return best, found
}

func (a *App) exportCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "export " + exportHugo,
		Short: "write blocks.json and status.json for a static site generator (Hugo module in integrations/hugo)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != exportHugo {
				return fmt.Errorf("%w: export knows %q", ErrUsage, exportHugo)
			}
			if out == "" {
				return fmt.Errorf("%w: --out is required", ErrUsage)
			}
			ld, err := a.system()
			if err != nil {
				return err
			}
			rep, err := ld.sys.Check(cmd.Context(), docsync.CheckOptions{})
			if err != nil {
				return err
			}
			changes := map[string]match.State{}
			for _, c := range rep.Changes {
				changes[c.Key()] = c.State
			}
			// The shortcode names an id and no environment, so each id
			// exports the def an environment-less citation resolves to.
			// Keeping whichever def of the id came last showed one
			// environment's value on one build and another's after a file
			// was renamed.
			defsOf := map[string][]block.Block{}
			for _, b := range rep.Scan.Defs {
				defsOf[b.ID] = append(defsOf[b.ID], b)
			}
			blocks := map[string]exportedBlock{}
			for id, defs := range defsOf {
				b, _ := check.ResolveDef(defs, ld.cfg.Env.Default, "")
				lang := strings.TrimPrefix(path.Ext(b.Pos.File), ".")
				blocks[id] = exportedBlock{ID: b.ID, Symbol: b.Symbol, File: b.Pos.File, Start: b.Pos.Start, End: b.Pos.End, Lang: lang, Content: b.Content, Rendered: fmt.Sprintf("```%s\n%s\n```", lang, b.Content), Change: changes[match.BlockKey(b)]}
			}
			dir := a.userPath(out)
			if err := os.MkdirAll(dir, dirPerm); err != nil {
				return err
			}
			for name, v := range map[string]any{ExportBlocksFile: blocks, ExportStatusFile: struct {
				docsync.Envelope
				Refs []statusRow `json:"refs"`
			}{rep.Envelope, statusRows(rep, ld.acks)}} {
				raw, _ := json.MarshalIndent(v, "", "  ")
				if err := os.WriteFile(filepath.Join(dir, name), append(raw, '\n'), filePerm); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "exported %d blocks and %d references to %s\n", len(blocks), len(statusRows(rep, ld.acks)), dir)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, flagOut, "", "directory to write into (Hugo: data/docsync)")
	return cmd
}

// userPath resolves a file argument that is not a repository path — --out,
// --export, --report, --tests — the one way every command does, for reading
// and writing alike: an absolute path as given, a relative one from where the
// command was started, as git reads it. export used to join every path onto
// the repository, so --out /tmp/site wrote into <repo>/tmp/site while
// reporting /tmp/site. Repository paths go through repoPath instead.
func (a *App) userPath(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(a.startDir(), filepath.FromSlash(p))
}
