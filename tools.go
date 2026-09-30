package docsync

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/id"
	"github.com/ubgo/docsync/internal/difflib"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/match"
	"github.com/ubgo/docsync/scan"
)

// TriageThreshold is the diff similarity at or above which two unacked
// findings are one decision (§19 `ds triage`). It reuses the fuzzy-match
// ratio so "similar" means the same thing everywhere in the tool.
const TriageThreshold = match.DefaultFuzzyThreshold

// TriageGroup is a set of unacked findings whose diffs look alike.
type TriageGroup struct {
	Group    int             `json:"group"`
	Diff     string          `json:"diff"`
	Classes  []block.Class   `json:"class,omitempty"`
	Findings []check.Finding `json:"findings"`
}

// Triage groups a report's unacked findings by diff similarity so a
// mechanical change across many blocks is one decision with one note.
// Findings without a diff (no old content was available) group by block
// id instead: the same block changed is still the same decision. Group
// numbers are assigned in finding order, so they are stable for identical
// input.
func Triage(rep Report) []TriageGroup {
	var groups []TriageGroup
	for _, f := range rep.Findings {
		if f.State != check.StateUnacked && f.State != check.StateTranslationStale {
			continue
		}
		placed := false
		for i := range groups {
			g := &groups[i]
			same := false
			switch {
			case f.Diff != "" && g.Diff != "":
				same = difflib.Ratio(diffTokens(g.Diff), diffTokens(f.Diff)) >= TriageThreshold
			case f.Diff == "" && g.Diff == "":
				same = g.Findings[0].ID == f.ID
			}
			if same {
				g.Findings = append(g.Findings, f)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, TriageGroup{Group: len(groups) + 1, Diff: f.Diff, Classes: f.Classes, Findings: []check.Finding{f}})
		}
	}
	return groups
}

// diffTokens splits a diff into the tokens triage compares: runs of
// identifier characters, and every other non-space character on its own,
// with the `-` and `+` that mark a line's direction kept.
//
// Why tokens and not lines: the mechanical change triage exists for — one
// parameter renamed across fifty functions (§19) — puts each function's own
// name on every changed line, so no two diffs share a line and a line-level
// ratio of zero gave fifty groups of one. Over tokens those diffs differ only
// in the name, and group.
func diffTokens(diff string) []string {
	var out []string
	word := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	start := -1
	for i, r := range diff {
		switch {
		case word(r):
			if start < 0 {
				start = i
			}
			continue
		case start >= 0:
			out = append(out, diff[start:i])
			start = -1
		}
		if !unicode.IsSpace(r) {
			out = append(out, string(r))
		}
	}
	if start >= 0 {
		out = append(out, diff[start:])
	}
	return out
}

// AuditOptions filters the ack log.
type AuditOptions struct {
	Since     time.Time
	ActorKind ledger.ActorKind
	ID        string
}

// Audit returns ack events matching the filter, oldest first; the log is
// already append-only so no other ordering is needed (§33).
func (s *System) Audit(opts AuditOptions) []ledger.Ack {
	var out []ledger.Ack
	for _, a := range s.acks.Rows {
		if !opts.Since.IsZero() && a.At.Before(opts.Since) {
			continue
		}
		if opts.ActorKind != "" && a.ActorKind != opts.ActorKind {
			continue
		}
		if opts.ID != "" && a.ID != opts.ID {
			continue
		}
		out = append(out, a)
	}
	return out
}

// RenameResult is what Rename proposes.
type RenameResult struct {
	// Mapping is old id to new id for every id whose label changed.
	Mapping map[string]string `json:"mapping"`
	// Edits rewrite every def and reference line; one edit per line even
	// when a line holds several ids.
	Edits []Edit `json:"edits"`
}

// Rename relabels ids whose label starts with oldPrefix so it starts with
// newPrefix instead (§22 `ds rename`). Identity is the suffix, so the ledger
// keeps matching after the edit; the old rows simply carry the old label and
// match by suffix on the next scan. Nothing is written: the caller applies
// the edits and rescans.
func (s *System) Rename(res scan.Result, oldPrefix, newPrefix string) (RenameResult, error) {
	if oldPrefix == "" || newPrefix == "" || oldPrefix == newPrefix {
		return RenameResult{}, fmt.Errorf("%w: rename needs two different non-empty labels", ErrNotFound)
	}
	// The new label is written into directives as it is, so it must already
	// be one: a space splits the directive, and anything else a slug would
	// change reads back as a different id. Refused before any edit exists.
	if slug := id.Slug(newPrefix); slug != newPrefix {
		return RenameResult{}, fmt.Errorf("%w: %q; did you mean %q?", ErrBadLabel, newPrefix, slug)
	}
	out := RenameResult{Mapping: map[string]string{}}
	for _, b := range res.Defs {
		if strings.HasPrefix(b.ID, oldPrefix+"-") || b.ID == oldPrefix {
			out.Mapping[b.ID] = newPrefix + strings.TrimPrefix(b.ID, oldPrefix)
		}
	}
	if len(out.Mapping) == 0 {
		return out, fmt.Errorf("%w: no id starts with %q", ErrNotFound, oldPrefix)
	}
	// Collect every line that mentions a renamed id: directive lines of
	// defs and the lines of references.
	type at struct {
		file string
		line int
	}
	lines := map[at]bool{}
	for _, b := range res.Defs {
		if _, ok := out.Mapping[b.ID]; ok {
			lines[at{b.DirectivePos.File, b.DirectivePos.Start}] = true
		}
	}
	for _, r := range res.Refs {
		if _, ok := out.Mapping[r.ID]; ok {
			lines[at{r.Pos.File, r.Pos.Start}] = true
		}
		for _, id := range r.Directive().List("about") {
			if _, ok := out.Mapping[id]; ok {
				lines[at{r.Pos.File, r.Pos.Start}] = true
			}
		}
	}
	// from= links on other defs also name the id.
	for _, b := range res.Defs {
		if _, ok := out.Mapping[b.From()]; ok {
			lines[at{b.DirectivePos.File, b.DirectivePos.Start}] = true
		}
	}
	keys := make([]at, 0, len(lines))
	for k := range lines {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].file != keys[j].file {
			return keys[i].file < keys[j].file
		}
		return keys[i].line < keys[j].line
	})
	cache := map[string][]string{}
	for _, k := range keys {
		src, ok := cache[k.file]
		if !ok {
			raw, err := s.readSource(k.file)
			if err != nil {
				return RenameResult{}, err
			}
			src = strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
			cache[k.file] = src
		}
		if k.line < 1 || k.line > len(src) {
			continue
		}
		old := src[k.line-1]
		nw := old
		for from, to := range out.Mapping {
			nw = replaceID(nw, from, to)
		}
		if nw != old {
			out.Edits = append(out.Edits, Edit{File: k.file, Line: k.line, Old: old, New: nw})
		}
	}
	return out, nil
}

// replaceID substitutes an id where it stands as a whole token, so a label
// that is a prefix of another id is left alone.
func replaceID(line, from, to string) string {
	var b strings.Builder
	for {
		i := strings.Index(line, from)
		if i < 0 {
			b.WriteString(line)
			return b.String()
		}
		end := i + len(from)
		before := i == 0 || !isIDChar(line[i-1])
		after := end == len(line) || !isIDChar(line[end])
		b.WriteString(line[:i])
		if before && after {
			b.WriteString(to)
		} else {
			b.WriteString(from)
		}
		line = line[end:]
	}
}

func isIDChar(c byte) bool {
	return c == '-' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// Graph node and edge kinds (§22 `ds graph`).
const (
	NodeDef  = "def"
	NodeDoc  = "doc"
	EdgeCite = "cites"
	EdgeFrom = "from"
	EdgeCov  = "covers"
	EdgeAbt  = "about"
)

// GraphNode is a def or a doc.
type GraphNode struct {
	ID    string      `json:"id"`
	Kind  string      `json:"kind"`
	Label string      `json:"label"`
	State check.State `json:"state,omitempty"`
}

// GraphEdge links two nodes.
type GraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	Line int    `json:"line,omitempty"`
}

// Graph is defs, docs, cites, chains, covers, and claims as a graph.
type Graph struct {
	Envelope
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// Graph builds the dependency graph from a report.
func (s *System) Graph(rep Report) Graph {
	res := rep.Scan
	g := Graph{Envelope: rep.Envelope}
	state := map[string]check.State{}
	for _, f := range rep.Findings {
		if f.ID != "" && rank(f.State) > rank(state[f.ID]) {
			state[f.ID] = f.State
		}
	}
	docs := map[string]bool{}
	for _, b := range res.Defs {
		st := state[b.ID]
		if st == "" {
			st = check.StateOK
		}
		label := b.Symbol
		if label == "" {
			label = b.Pos.File
		}
		g.Nodes = append(g.Nodes, GraphNode{ID: b.ID, Kind: NodeDef, Label: label, State: st})
		if f := b.From(); f != "" {
			g.Edges = append(g.Edges, GraphEdge{From: b.ID, To: f, Kind: EdgeFrom})
		}
	}
	for _, r := range res.Refs {
		docs[r.Pos.File] = true
		if r.ID != "" {
			g.Edges = append(g.Edges, GraphEdge{From: r.Pos.File, To: r.ID, Kind: EdgeCite, Line: r.Pos.Start})
		}
		for _, id := range r.Directive().List("about") {
			g.Edges = append(g.Edges, GraphEdge{From: r.Pos.File, To: id, Kind: EdgeAbt, Line: r.Pos.Start})
		}
	}
	for _, doc := range sortedPages(res) {
		docs[doc] = true
		for _, id := range res.Pages[doc].Covers {
			g.Edges = append(g.Edges, GraphEdge{From: doc, To: id, Kind: EdgeCov})
		}
	}
	names := make([]string, 0, len(docs))
	for d := range docs {
		names = append(names, d)
	}
	sort.Strings(names)
	for _, d := range names {
		g.Nodes = append(g.Nodes, GraphNode{ID: d, Kind: NodeDoc, Label: d})
	}
	return g
}

// DOT renders the graph for Graphviz.
func (g Graph) DOT() string {
	var b strings.Builder
	b.WriteString("digraph docsync {\n  rankdir=LR;\n")
	for _, n := range g.Nodes {
		shape := "box"
		if n.Kind == NodeDoc {
			shape = "note"
		}
		label := n.Label
		if n.Kind == NodeDef {
			label = n.ID + "\\n" + n.Label + "\\n" + string(n.State)
		}
		fmt.Fprintf(&b, "  %q [shape=%s label=%q];\n", n.ID, shape, label)
	}
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "  %q -> %q [label=%q];\n", e.From, e.To, e.Kind)
	}
	b.WriteString("}\n")
	return b.String()
}

// BlameResult is the block behind a reference and what happened to it.
type BlameResult struct {
	Envelope
	Reference block.Reference `json:"reference"`
	Block     block.Block     `json:"block"`
	Change    match.Change    `json:"change"`
	Finding   check.Finding   `json:"finding"`
	Acks      []ledger.Ack    `json:"acks"`
}

// Blame answers `ds blame <doc> <line>`: the reference on that line, the
// block it points at, the change since the ledger, the current finding, and
// every ack recorded for that line (§22). History beyond the previous ledger
// needs the VCS and belongs to the CLI.
func (s *System) Blame(rep Report, doc string, line int) (BlameResult, error) {
	out := BlameResult{Envelope: rep.Envelope}
	found := false
	for _, r := range rep.Scan.Refs {
		if r.Pos.File == doc && r.Pos.Start == line {
			out.Reference = r
			found = true
			break
		}
	}
	if !found {
		return out, fmt.Errorf("%w: %s:%d", ErrNoReference, doc, line)
	}
	if b, ok := s.LocateID(rep.Scan, out.Reference.ID); ok {
		out.Block = b
	}
	// The change of the block shown, not the last environment's.
	for _, c := range rep.Changes {
		if c.ID == out.Reference.ID && c.Env() == out.Block.Env() {
			out.Change = c
		}
	}
	for _, f := range rep.Findings {
		if f.Doc == doc && f.Line == line && (f.ID == out.Reference.ID || out.Reference.ID == "") {
			out.Finding = f
			break
		}
	}
	for _, a := range s.acks.Rows {
		if a.Doc == doc && a.Line == line {
			out.Acks = append(out.Acks, a)
		}
	}
	return out, nil
}

// FixDuplicates re-mints every def after the first for each id defined more
// than once (Part VII "Ids and branches"): the first occurrence in file
// order keeps the id, the tool never guesses which one is the original, and
// the caller prints old→new. Edits rewrite only the directive lines; the
// caller applies them and rescans.
func (s *System) FixDuplicates(res scan.Result) (RenameResult, error) {
	byID := map[string][]block.Block{}
	for _, b := range res.Defs {
		byID[b.ID] = append(byID[b.ID], b)
	}
	out := RenameResult{Mapping: map[string]string{}}
	var ids []string
	for id, bs := range byID {
		if len(bs) > 1 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, oldID := range ids {
		bs := byID[oldID]
		sort.Slice(bs, func(i, j int) bool {
			if bs[i].DirectivePos.File != bs[j].DirectivePos.File {
				return bs[i].DirectivePos.File < bs[j].DirectivePos.File
			}
			return bs[i].DirectivePos.Start < bs[j].DirectivePos.Start
		})
		prefix, _, err := id.Split(s.idcfg, oldID)
		if err != nil {
			prefix = oldID
		}
		for _, b := range bs[1:] {
			newID, err := id.New(s.idcfg, prefix)
			if err != nil {
				return RenameResult{}, err
			}
			raw, err := s.readSource(b.DirectivePos.File)
			if err != nil {
				return RenameResult{}, err
			}
			lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
			if b.DirectivePos.Start < 1 || b.DirectivePos.Start > len(lines) {
				continue
			}
			old := lines[b.DirectivePos.Start-1]
			nw := replaceID(old, oldID, newID)
			if nw == old {
				continue
			}
			key := fmt.Sprintf("%s@%s:%d", oldID, b.DirectivePos.File, b.DirectivePos.Start)
			out.Mapping[key] = newID
			out.Edits = append(out.Edits, Edit{File: b.DirectivePos.File, Line: b.DirectivePos.Start, Old: old, New: nw})
		}
	}
	return out, nil
}
