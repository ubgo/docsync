package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/ledger"
)

// Flags for the second tier of commands.
const (
	flagAckGroup   = "ack-group"
	flagActorKind  = "actor-kind"
	flagID         = "id"
	flagDot        = "dot"
	flagUncovered  = "uncovered"
	flagUnmarked   = "unmarked"
	flagStalest    = "stalest"
	flagLiterals   = "literals"
	flagOrphaned   = "orphaned-owners"
	flagGaps       = "gaps"
	flagMetrics    = "metrics"
	flagLimit      = "limit"
	flagSinceDate  = "since"
	dateLayout     = "2006-01-02"
	unknownActor   = "unknown"
	undoneTemplate = "undid %s:%d\n"
)

// actor resolves --actor: the flag, else the VCS user, else "unknown". The
// audit log always says who; it never says nobody.
func (a *App) actor(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if u := a.vcs.User(); u != "" {
		return u
	}
	return unknownActor
}

func (a *App) triageCmd() *cobra.Command {
	var asJSON bool
	var group int
	var note, actor string
	cmd := &cobra.Command{
		Use:   "triage",
		Short: "group unacked findings by diff similarity; --ack-group N acks one group",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			rep, err := ld.sys.Check(cmd.Context(), docsync.CheckOptions{})
			if err != nil {
				return err
			}
			groups := docsync.Triage(rep)
			out := cmd.OutOrStdout()
			if group > 0 {
				if group > len(groups) {
					return fmt.Errorf("%w: group %d of %d", docsync.ErrNotFound, group, len(groups))
				}
				var reqs []docsync.AckRequest
				for _, f := range groups[group-1].Findings {
					reqs = append(reqs, docsync.AckRequest{ID: f.ID, Doc: f.Doc, Line: f.Line, Actor: a.actor(actor), Note: note})
				}
				rows, err := a.recordAcks(ld, rep.Scan, reqs, false)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "acked group %d: %d sentences\n", group, len(rows))
				return nil
			}
			if asJSON {
				return printJSON(out, struct {
					docsync.Envelope
					Groups []docsync.TriageGroup `json:"groups"`
				}{rep.Envelope, groups})
			}
			if len(groups) == 0 {
				fmt.Fprintln(out, "nothing unacked")
				return nil
			}
			for _, g := range groups {
				fmt.Fprintf(out, "group %d: %d sentence(s)\n", g.Group, len(g.Findings))
				for _, f := range g.Findings {
					fmt.Fprintf(out, "  %s:%d  %s\n", f.Doc, f.Line, f.ID)
				}
				if g.Diff != "" {
					fmt.Fprintf(out, "%s\n", indent(g.Diff, "  | "))
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	cmd.Flags().IntVar(&group, flagAckGroup, 0, "ack every sentence in this group")
	cmd.Flags().StringVar(&note, flagNote, "", "note for the group ack")
	cmd.Flags().StringVar(&actor, flagActor, "", "who is acking")
	return cmd
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// auditWhat names what an ack approved: an id, a claim, or a whole page.
// Claims and page reviews have no id, and printed an empty column.
func auditWhat(r ledger.Ack) string {
	switch {
	case r.ID != "":
		return r.ID
	case r.Line == 0:
		return auditPageReview
	}
	return auditClaim
}

// auditWhere is the doc line an ack sits on, or the doc alone for a page
// review, which has no line; it printed as line 0, a line that does not exist.
func auditWhere(r ledger.Ack) string {
	if r.Line == 0 {
		return r.Doc
	}
	return fmt.Sprintf("%s:%d", r.Doc, r.Line)
}

// What an id-less ack approved, in audit's text form.
const (
	auditPageReview = "(page review)"
	auditClaim      = "(claim)"
)

func (a *App) auditCmd() *cobra.Command {
	var asJSON bool
	var since, kind, id, export string
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "the append-only ack log; --export writes it as JSON lines",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			opts := docsync.AuditOptions{ActorKind: ledger.ActorKind(kind), ID: id}
			if since != "" {
				t, err := time.Parse(dateLayout, since)
				if err != nil {
					return fmt.Errorf("%w: --since wants YYYY-MM-DD", ErrUsage)
				}
				opts.Since = t
			}
			rows := ld.sys.Audit(opts)
			out := cmd.OutOrStdout()
			if export != "" {
				// Acks are plain structs; encoding them cannot fail, so the
				// only error worth a branch is the write itself.
				var buf bytes.Buffer
				enc := json.NewEncoder(&buf)
				for _, r := range rows {
					_ = enc.Encode(r)
				}
				if err := os.WriteFile(a.userPath(export), buf.Bytes(), filePerm); err != nil {
					return err
				}
				fmt.Fprintf(out, "exported %d event(s) to %s\n", len(rows), export)
				return nil
			}
			if asJSON {
				return printJSON(out, struct {
					docsync.Envelope
					Acks []ledger.Ack `json:"acks"`
				}{rep(a, ld), rows})
			}
			for _, r := range rows {
				who := r.Actor
				if r.ActorKind == ledger.ActorAgent {
					who += " (agent, delegated by " + r.DelegatedBy + ")"
				}
				fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", r.At.Format(time.RFC3339), who, auditWhat(r), auditWhere(r), r.Note)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	cmd.Flags().StringVar(&since, flagSinceDate, "", "only events on or after this date")
	cmd.Flags().StringVar(&kind, flagActorKind, "", "human or agent")
	cmd.Flags().StringVar(&id, flagID, "", "only events for this id")
	cmd.Flags().StringVar(&export, flagExport, "", "write the events as JSON lines to this file")
	return cmd
}

// rep builds an envelope for commands that do not run a check.
func rep(a *App, ld loaded) docsync.Envelope {
	commit, _ := a.vcs.Head()
	return docsync.Envelope{JSONFormat: docsync.JSONFormat, GeneratedAt: a.now(), Repo: repoName(a.dir, ld.sys.Config()), Commit: commit}
}

func (a *App) renameCmd() *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "rename <old-label> <new-label>",
		Short: "relabel ids everywhere; identity (the suffix) is unchanged",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			res, err := ld.sys.Scan(cmd.Context())
			if err != nil {
				return err
			}
			r, err := ld.sys.Rename(res, args[0], args[1])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for from, to := range r.Mapping {
				fmt.Fprintf(out, "%s -> %s\n", from, to)
			}
			if dry {
				fmt.Fprintf(out, "%d line(s) would change (--dry-run)\n", len(r.Edits))
				return nil
			}
			if err := ld.st.applyAll(WriteRename, r.Edits); err != nil {
				return err
			}
			fmt.Fprintf(out, "%d line(s) changed; run %s scan\n", len(r.Edits), a.name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dry, flagDryRun, false, "print without writing")
	return cmd
}

func (a *App) graphCmd() *cobra.Command {
	var dot, asJSON bool
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "defs, docs, cites, chains, covers, and claims as a graph",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			rp, err := ld.sys.Check(cmd.Context(), docsync.CheckOptions{})
			if err != nil {
				return err
			}
			g := ld.sys.Graph(rp)
			out := cmd.OutOrStdout()
			switch {
			case dot:
				_, err := fmt.Fprint(out, g.DOT())
				return err
			case asJSON:
				return printJSON(out, g)
			}
			for _, e := range g.Edges {
				fmt.Fprintf(out, "%s -%s-> %s\n", e.From, e.Kind, e.To)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dot, flagDot, false, "Graphviz output")
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	return cmd
}

func (a *App) blameCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "blame <doc> <line>",
		Short: "the block behind a reference and what happened to it",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			line, err := strconv.Atoi(args[1])
			if err != nil || line < 1 {
				return fmt.Errorf("%w: line must be a positive number", ErrUsage)
			}
			ld, err := a.system()
			if err != nil {
				return err
			}
			rp, err := ld.sys.Check(cmd.Context(), docsync.CheckOptions{})
			if err != nil {
				return err
			}
			doc, err := a.repoPath(args[0])
			if err != nil {
				return err
			}
			b, err := ld.sys.Blame(rp, doc, line)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, b)
			}
			fmt.Fprintf(out, "%s:%d  ds:%s %s\n", doc, line, b.Reference.Verb, b.Reference.ID)
			if b.Block.ID != "" {
				fmt.Fprintf(out, "block  %s:%d-%d  %s\n", b.Block.Pos.File, b.Block.Pos.Start, b.Block.Pos.End, b.Block.Hash[:12])
			}
			fmt.Fprintf(out, "state  %s  %s\n", b.Finding.State, b.Finding.Message)
			if b.Change.State != "" {
				fmt.Fprintf(out, "change %s %v\n", b.Change.State, b.Change.Classes)
			}
			if b.Finding.Diff != "" {
				fmt.Fprintln(out, indent(b.Finding.Diff, "  | "))
			}
			for _, ack := range b.Acks {
				fmt.Fprintf(out, "ack    %s %s %s\n", ack.At.Format(dateLayout), ack.Actor, ack.Note)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	return cmd
}

func (a *App) reportCmd() *cobra.Command {
	var asJSON, uncovered, unmarked, stalest, literals, orphaned, gaps, metrics bool
	var limit int
	cmd := &cobra.Command{
		Use:   "report",
		Short: "hygiene: uncovered, unmarked, stalest, literals, orphaned owners, gaps, metrics",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			rp, err := ld.sys.Check(cmd.Context(), docsync.CheckOptions{})
			if err != nil {
				return err
			}
			churn, _ := a.vcs.Churn()
			served, source, err := ld.st.LoadMetrics()
			if err != nil {
				return err
			}
			r := ld.sys.Report(rp, docsync.ReportOptions{Churn: churn, Limit: limit, ServedBytes: served, SourceBytes: source})
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, r)
			}
			all := !(uncovered || unmarked || stalest || literals || orphaned || gaps || metrics)
			if all || uncovered {
				fmt.Fprintf(out, "uncovered (%d)\n", len(r.Uncovered))
				for _, b := range r.Uncovered {
					fmt.Fprintf(out, "  %s  %s:%d\n", b.ID, b.Pos.File, b.Pos.Start)
				}
			}
			if all || unmarked {
				fmt.Fprintf(out, "unmarked (%d)\n", len(r.Unmarked))
				for _, u := range r.Unmarked {
					if u.Symbol != "" {
						fmt.Fprintf(out, "  %s:%d  %s\n", u.File, u.Line, u.Symbol)
					} else {
						fmt.Fprintf(out, "  %s  changed %d times, no defs\n", u.File, u.Churn)
					}
				}
			}
			if all || stalest {
				fmt.Fprintf(out, "stalest (%d)\n", len(r.Stalest))
				for _, s := range r.Stalest {
					when := "never acked"
					if !s.NeverAck {
						when = s.OldestAck.Format(dateLayout)
					}
					fmt.Fprintf(out, "  %s  %s  %d cites\n", s.Doc, when, s.Cites)
				}
			}
			if all || literals {
				fmt.Fprintf(out, "literals (%d)\n", len(r.Literals))
				for _, l := range r.Literals {
					fmt.Fprintf(out, "  %s:%d  %q is %s; cite it\n", l.Doc, l.Line, l.Value, l.ID)
				}
			}
			if all || orphaned {
				fmt.Fprintf(out, "orphaned owners (%d): %s\n", len(r.OrphanedOwners), strings.Join(r.OrphanedOwners, ", "))
			}
			if all || gaps {
				fmt.Fprintf(out, "gaps (%d)\n", len(r.Gaps))
				for _, g := range r.Gaps {
					fmt.Fprintf(out, "  %s\n", g)
				}
			}
			if all || metrics {
				fmt.Fprintln(out, "freshness per page")
				for _, m := range r.PerPage {
					fmt.Fprintf(out, "  %s  %s\n", m.Key, stateSummary(m.State))
				}
				fmt.Fprintln(out, "freshness per owner")
				for _, m := range r.PerOwner {
					fmt.Fprintf(out, "  %s  %s\n", orNone(m.Key), stateSummary(m.State))
				}
				fmt.Fprintf(out, "mean time to ack %s\n", r.MeanTimeToAck.Round(time.Minute))
				if r.SourceBytes > 0 {
					fmt.Fprintf(out, "context served to agents: %d bytes of %d (%.0f%% saved)\n", r.ServedBytes, r.SourceBytes, r.ContextSavings*100)
				}
				for _, p := range r.BusyPrefixes {
					fmt.Fprintf(out, "busy prefix %s: %d ids\n", p.Prefix, p.Count)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	cmd.Flags().BoolVar(&uncovered, flagUncovered, false, "defs nothing cites or covers")
	cmd.Flags().BoolVar(&unmarked, flagUnmarked, false, "changed files and exported symbols without defs")
	cmd.Flags().BoolVar(&stalest, flagStalest, false, "pages by oldest ack")
	cmd.Flags().BoolVar(&literals, flagLiterals, false, "fact values typed by hand")
	cmd.Flags().BoolVar(&orphaned, flagOrphaned, false, "owners missing from [owners]")
	cmd.Flags().BoolVar(&gaps, flagGaps, false, "ranked worklist")
	cmd.Flags().BoolVar(&metrics, flagMetrics, false, "freshness per page and owner")
	cmd.Flags().IntVar(&limit, flagLimit, 0, "cap each list")
	return cmd
}

// repairCmd finds directives an older build wrote as bare lines into files
// that cannot hold one, and comments them. It prints by default and writes only
// with --apply, because it edits source a person did not ask it to touch by
// name; a run that changes nothing without asking is the careless path, and
// the careless path has to be the safe one.
func (a *App) repairCmd() *cobra.Command {
	var apply, asJSON bool
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "find directives left bare in files that cannot hold one; comment them, or delete them where there is no comment syntax (--apply to write)",
		Example: "  ds repair            # show what would change\n" +
			"  ds repair --apply    # comment them; ds undo reverses it\n" +
			"  ds repair --json     # the same, for a script",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			res, err := ld.sys.Scan(cmd.Context())
			if err != nil {
				return err
			}
			r := ld.sys.Repair(res)
			out := cmd.OutOrStdout()
			if asJSON {
				return printJSON(out, r)
			}
			printRepair(out, r)
			switch {
			case len(r.Edits) == 0 && len(r.Manual) == 0:
				fmt.Fprintln(out, "nothing to repair")
				return nil
			case !apply:
				fmt.Fprintf(out, "%d line(s) would be repaired, %d need a person (run with --apply to write)\n", len(r.Edits), len(r.Manual))
				return nil
			}
			if err := ld.st.applyAll(WriteRepair, r.Edits); err != nil {
				return err
			}
			fmt.Fprintf(out, "%d line(s) repaired, %d need a person; %s undo reverses this, then run %s scan\n", len(r.Edits), len(r.Manual), a.name, a.name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, flagApply, false, "write the edits (default: print them)")
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "print the proposed repair as JSON")
	return cmd
}

// printRepair writes the proposal `ds repair` shows: each edit as the line it
// replaces and what replaces it, a deletion marked as one, and the damage left
// for a person. It is apart from the command so every shape of a proposal can
// be checked -- the "by hand" case only arises when a file cannot be read back
// in the middle of a run, which one process cannot arrange.
func printRepair(out io.Writer, r docsync.RepairResult) {
	for _, e := range r.Edits {
		if e.Delete {
			fmt.Fprintf(out, "%s:%d  delete (no comment syntax here)\n  - %s\n", e.File, e.Line, e.Old)
			continue
		}
		fmt.Fprintf(out, "%s:%d\n  - %s\n  + %s\n", e.File, e.Line, e.Old, e.New)
	}
	for _, m := range r.Manual {
		fmt.Fprintf(out, "by hand: %s\n", m)
	}
}

func (a *App) adoptCmd() *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "adopt",
		Short: "convert path#L10-L20 and path#symbol links into defs and cites",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			res, err := ld.sys.Scan(cmd.Context())
			if err != nil {
				return err
			}
			r, err := ld.sys.Adopt(cmd.Context(), res)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, u := range r.Unresolved {
				fmt.Fprintf(out, "left alone %s:%d %s: %s\n", u.Doc, u.Line, u.Target, u.Reason)
			}
			if dry {
				for _, e := range r.Edits {
					fmt.Fprintf(out, "%s:%d: %s\n", e.File, e.Line, e.New)
				}
				fmt.Fprintf(out, "%d link(s) would be adopted (--dry-run)\n", r.Adopted)
				return nil
			}
			if err := ld.st.applyAll(WriteAdopt, r.Edits); err != nil {
				return err
			}
			fmt.Fprintf(out, "%d link(s) adopted, %d edit(s); run %s scan\n", r.Adopted, len(r.Edits), a.name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&dry, flagDryRun, false, "print without writing")
	return cmd
}
