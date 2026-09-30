package docsync

import (
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/render"
	"github.com/ubgo/docsync/scan"
)

// ReportOptions selects the hygiene views of `ds report` (§22, §26.9).
type ReportOptions struct {
	// Churn is commits touching each file, supplied by the CLI from git;
	// nil disables the most-changed-files-without-defs view.
	Churn map[string]int
	// Limit caps each list; 0 means everything.
	Limit int
	// ServedBytes and SourceBytes are what agents received through
	// `context` and `read` against the size of the files those items came
	// from (§26.11), recorded by the host; zero means not measured.
	ServedBytes, SourceBytes int64
}

// Prefix is an id label with how many ids use it (Part VII "busy
// prefixes").
type Prefix struct {
	Prefix string `json:"prefix"`
	Count  int    `json:"count"`
}

// busyPrefixMin is the count from which a label counts as busy.
const busyPrefixMin = 5

// Unmarked is a file with change history and no definitions, or an exported
// declaration nobody cites.
type Unmarked struct {
	File   string `json:"file"`
	Symbol string `json:"symbol,omitempty"`
	Line   int    `json:"line,omitempty"`
	Churn  int    `json:"churn,omitempty"`
}

// Stale is a page ordered by its oldest live ack.
type Stale struct {
	Doc       string    `json:"doc"`
	OldestAck time.Time `json:"oldest_ack"`
	Cites     int       `json:"cites"`
	NeverAck  bool      `json:"never_acked"`
}

// Literal is a fact value typed by hand in prose instead of cited.
type Literal struct {
	Doc   string `json:"doc"`
	Line  int    `json:"line"`
	ID    string `json:"id"`
	Value string `json:"value"`
}

// OwnerMetrics is freshness per owner or page.
type OwnerMetrics struct {
	Key   string              `json:"key"`
	State map[check.State]int `json:"state"`
}

// ReportResult is every view; the CLI prints the ones asked for.
type ReportResult struct {
	Envelope
	Uncovered      []block.Block  `json:"uncovered"`
	Unmarked       []Unmarked     `json:"unmarked"`
	Stalest        []Stale        `json:"stalest"`
	Literals       []Literal      `json:"literals"`
	OrphanedOwners []string       `json:"orphaned_owners"`
	Gaps           []string       `json:"gaps"`
	PerPage        []OwnerMetrics `json:"per_page"`
	PerOwner       []OwnerMetrics `json:"per_owner"`
	MeanTimeToAck  time.Duration  `json:"mean_time_to_ack_ns"`
	BusyPrefixes   []Prefix       `json:"busy_prefixes"`
	// ServedBytes and SourceBytes echo the options; ContextSavings is the
	// fraction of file bytes an agent did not have to read.
	ServedBytes    int64   `json:"served_bytes"`
	SourceBytes    int64   `json:"source_bytes"`
	ContextSavings float64 `json:"context_savings"`
}

// exportedDeclRE finds exported Go-style declarations for the unmarked view;
// other languages count through their extractor's declaration table when a
// syntax tier is present. Heuristic on purpose: report, not check.
var exportedDeclRE = regexp.MustCompile(`^\s*(?:func\s+(?:\([^)]*\)\s*)?|type\s+|export\s+(?:async\s+)?function\s+|export\s+class\s+|pub\s+fn\s+|def\s+)([A-Z]\w*)`)

// Report computes the hygiene views from a report (§26.9 "proactive rather
// than reactive").
func (s *System) Report(rep Report, opts ReportOptions) ReportResult {
	res := rep.Scan
	out := ReportResult{Envelope: rep.Envelope}
	cited := map[string]bool{}
	for _, r := range res.Refs {
		if r.ID != "" {
			cited[r.ID] = true
		}
	}
	for _, p := range res.Pages {
		for _, id := range p.Covers {
			cited[id] = true
		}
	}
	// A chain hop is referenced by the def that copies from it, the same
	// rule check uses for `uncovered`.
	for _, b := range res.Defs {
		if f := b.From(); f != "" {
			cited[f] = true
		}
	}
	filesWithDefs := map[string]bool{}
	for _, b := range res.Defs {
		filesWithDefs[b.Pos.File] = true
		if !cited[b.ID] {
			out.Uncovered = append(out.Uncovered, b)
		}
	}
	// Unmarked: churned files in scope without defs, then exported
	// declarations without defs in scanned code files. Churn for a file the
	// scan did not read (excluded, generated, state) says nothing.
	for f, n := range opts.Churn {
		if _, scanned := res.Tier[f]; scanned && !filesWithDefs[f] {
			out.Unmarked = append(out.Unmarked, Unmarked{File: f, Churn: n})
		}
	}
	sort.Slice(out.Unmarked, func(i, j int) bool {
		if out.Unmarked[i].Churn != out.Unmarked[j].Churn {
			return out.Unmarked[i].Churn > out.Unmarked[j].Churn
		}
		return out.Unmarked[i].File < out.Unmarked[j].File
	})
	defLines := map[string]map[int]bool{}
	for _, b := range res.Defs {
		if defLines[b.Pos.File] == nil {
			defLines[b.Pos.File] = map[int]bool{}
		}
		defLines[b.Pos.File][b.Pos.Start] = true
	}
	var codeFiles []string
	for f, tier := range res.Tier {
		if tier == (extract.Code{}).Name() {
			codeFiles = append(codeFiles, f)
		}
	}
	sort.Strings(codeFiles)
	for _, f := range codeFiles {
		raw, err := fs.ReadFile(s.fsys, f)
		if err != nil {
			continue
		}
		for i, l := range strings.Split(string(raw), "\n") {
			m := exportedDeclRE.FindStringSubmatch(l)
			if m == nil || defLines[f][i+1] {
				continue
			}
			out.Unmarked = append(out.Unmarked, Unmarked{File: f, Symbol: m[1], Line: i + 1})
		}
	}
	// Stalest: pages by oldest ack; never-acked pages first.
	latestAck := map[string]time.Time{}
	oldest := map[string]time.Time{}
	for _, a := range s.acks.Rows {
		key := a.Doc + "\x00" + strconv.Itoa(a.Line)
		if a.At.After(latestAck[key]) {
			latestAck[key] = a.At
		}
	}
	cites := map[string]int{}
	for _, r := range res.Refs {
		if r.ID == "" {
			continue
		}
		cites[r.Pos.File]++
		t, ok := latestAck[r.Pos.File+"\x00"+strconv.Itoa(r.Pos.Start)]
		cur, seen := oldest[r.Pos.File]
		switch {
		case !ok:
			oldest[r.Pos.File] = time.Time{}
		case !seen || (!cur.IsZero() && t.Before(cur)):
			oldest[r.Pos.File] = t
		}
	}
	for doc, t := range oldest {
		out.Stalest = append(out.Stalest, Stale{Doc: doc, OldestAck: t, Cites: cites[doc], NeverAck: t.IsZero()})
	}
	sort.Slice(out.Stalest, func(i, j int) bool {
		a, b := out.Stalest[i], out.Stalest[j]
		if a.NeverAck != b.NeverAck {
			return a.NeverAck
		}
		if !a.OldestAck.Equal(b.OldestAck) {
			return a.OldestAck.Before(b.OldestAck)
		}
		return a.Doc < b.Doc
	})
	out.Literals = s.literals(res)
	// Orphaned owners: owner= values that config's [owners] does not know.
	if len(s.cfg.Owners) > 0 {
		seen := map[string]bool{}
		for _, b := range res.Defs {
			o := b.Owner()
			if o == "" || seen[o] {
				continue
			}
			seen[o] = true
			if _, ok := s.cfg.Owners[o]; !ok {
				out.OrphanedOwners = append(out.OrphanedOwners, o)
			}
		}
		sort.Strings(out.OrphanedOwners)
	}
	// Metrics: state counts per page and per owner; mean time from the
	// ledger's scan to each ack as the only latency the log can show.
	page := map[string]map[check.State]int{}
	owner := map[string]map[check.State]int{}
	for _, f := range rep.Findings {
		if f.Verb == "" {
			continue
		}
		if page[f.Doc] == nil {
			page[f.Doc] = map[check.State]int{}
		}
		page[f.Doc][f.State]++
		if owner[f.Owner] == nil {
			owner[f.Owner] = map[check.State]int{}
		}
		owner[f.Owner][f.State]++
	}
	out.PerPage, out.PerOwner = metricsList(page), metricsList(owner)
	if !s.prev.Header.ScannedAt.IsZero() {
		var total time.Duration
		n := 0
		for _, a := range s.acks.Rows {
			if a.At.After(s.prev.Header.ScannedAt) {
				total += a.At.Sub(s.prev.Header.ScannedAt)
				n++
			}
		}
		if n > 0 {
			out.MeanTimeToAck = total / time.Duration(n)
		}
	}
	// Busy prefixes.
	counts := map[string]int{}
	for _, b := range res.Defs {
		if i := strings.LastIndex(b.ID, "-"); i > 0 {
			counts[b.ID[:i]]++
		}
	}
	for p, n := range counts {
		if n >= busyPrefixMin {
			out.BusyPrefixes = append(out.BusyPrefixes, Prefix{Prefix: p, Count: n})
		}
	}
	sort.Slice(out.BusyPrefixes, func(i, j int) bool {
		if out.BusyPrefixes[i].Count != out.BusyPrefixes[j].Count {
			return out.BusyPrefixes[i].Count > out.BusyPrefixes[j].Count
		}
		return out.BusyPrefixes[i].Prefix < out.BusyPrefixes[j].Prefix
	})
	out.ServedBytes, out.SourceBytes = opts.ServedBytes, opts.SourceBytes
	if opts.SourceBytes > 0 {
		out.ContextSavings = 1 - float64(opts.ServedBytes)/float64(opts.SourceBytes)
	}
	// Gaps: a ranked worklist across the views.
	for _, u := range out.Unmarked {
		if u.Churn > 0 {
			out.Gaps = append(out.Gaps, "define blocks in "+u.File+" (changed "+strconv.Itoa(u.Churn)+" times, nothing documented)")
		}
	}
	for _, st := range out.Stalest {
		if st.NeverAck {
			out.Gaps = append(out.Gaps, "review "+st.Doc+" (cites never acked)")
		}
	}
	for _, l := range out.Literals {
		out.Gaps = append(out.Gaps, "cite "+l.ID+" instead of typing "+l.Value+" at "+l.Doc+":"+strconv.Itoa(l.Line))
	}
	for _, b := range out.Uncovered {
		out.Gaps = append(out.Gaps, "document or drop "+b.ID+" ("+b.Pos.File+")")
	}
	if opts.Limit > 0 {
		out.Uncovered = capBlocks(out.Uncovered, opts.Limit)
		if len(out.Unmarked) > opts.Limit {
			out.Unmarked = out.Unmarked[:opts.Limit]
		}
		if len(out.Stalest) > opts.Limit {
			out.Stalest = out.Stalest[:opts.Limit]
		}
		if len(out.Literals) > opts.Limit {
			out.Literals = out.Literals[:opts.Limit]
		}
		if len(out.Gaps) > opts.Limit {
			out.Gaps = out.Gaps[:opts.Limit]
		}
	}
	return out
}

// literals finds fact values typed into prose lines of docs that do not
// cite the fact on that line. Values shorter than three characters or that
// are common words are skipped, so `30` and `on` do not flood the report;
// the check is a hint for `report`, never a finding.
func (s *System) literals(res scan.Result) []Literal {
	type fact struct{ id, value string }
	var facts []fact
	for _, b := range res.Defs {
		if v, ok := render.Value(b); ok && len(v) >= 3 && !b.IsSecret() {
			facts = append(facts, fact{b.ID, v})
		}
	}
	if len(facts) == 0 {
		return nil
	}
	// Every scanned markdown file, not only those with directives: a page
	// with no cites at all is exactly where a hand-typed value hides.
	var names []string
	for f, tier := range res.Tier {
		if tier == (extract.Markdown{}).Name() {
			names = append(names, f)
		}
	}
	sort.Strings(names)
	var out []Literal
	for _, d := range names {
		raw, err := fs.ReadFile(s.fsys, d)
		if err != nil {
			continue
		}
		inFence := false
		for i, l := range strings.Split(string(raw), "\n") {
			t := strings.TrimSpace(l)
			if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
				inFence = !inFence
				continue
			}
			if inFence || strings.HasPrefix(t, "<!--") {
				continue
			}
			for _, f := range facts {
				if !strings.Contains(l, f.value) || strings.Contains(l, "id="+f.id) {
					continue
				}
				// A def line carries its own value; skip the home of the fact.
				if isDefLine(res, d, i+1, f.id) {
					continue
				}
				out = append(out, Literal{Doc: d, Line: i + 1, ID: f.id, Value: f.value})
			}
		}
	}
	return out
}

func isDefLine(res scan.Result, doc string, line int, id string) bool {
	for _, b := range res.Defs {
		if b.ID == id && b.Pos.File == doc && line >= b.Pos.Start && line <= b.Pos.End {
			return true
		}
	}
	return false
}

func metricsList(m map[string]map[check.State]int) []OwnerMetrics {
	out := make([]OwnerMetrics, 0, len(m))
	for k, v := range m {
		out = append(out, OwnerMetrics{Key: k, State: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func capBlocks(bs []block.Block, n int) []block.Block {
	if len(bs) > n {
		return bs[:n]
	}
	return bs
}
