package docsync

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/internal/linerange"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/match"
	"github.com/ubgo/docsync/render"
	"github.com/ubgo/docsync/scan"
)

// BytesPerToken is the estimate behind every `tokens` field (§26.2): four
// bytes per token is the usual English-and-code average and errs high for
// prose, so a budget is never exceeded by the estimate being optimistic.
const BytesPerToken = 4

// Tokens estimates the token count of s.
func Tokens(s string) int { return (len(s) + BytesPerToken - 1) / BytesPerToken }

// Content modes for Context items (§26.4).
const (
	ModeAuto  = "auto"
	ModeFull  = "full"
	ModeDiff  = "diff"
	ModeValue = "value"
	ModeLine  = "line"
)

// ModeValues is the canonical list callers may pass.
var ModeValues = []string{ModeAuto, ModeFull, ModeDiff, ModeValue}

// SinceAck asks Context for diffs since the last ack; any other Since value
// is a commit the caller's OldContent hook understands.
const SinceAck = "ack"

// Omission reasons.
const (
	ReasonOverBudget = "over budget"
	ReasonUnchanged  = "unchanged since ack; over budget"
)

// Fact is a one-line def with its current value (§11, `ds facts`).
type Fact struct {
	ID      string           `json:"id"`
	Value   string           `json:"value"`
	Type    string           `json:"type,omitempty"`
	File    string           `json:"file"`
	Line    int              `json:"line"`
	Env     string           `json:"env,omitempty"`
	Owner   string           `json:"owner,omitempty"`
	Secret  bool             `json:"secret,omitempty"`
	CitedBy []block.Position `json:"cited_by"`
}

// Facts lists every one-line def and who cites it. Secrets are addresses
// and render as such; the value is the visible name, never a credential.
func (s *System) Facts(res scan.Result) []Fact {
	citers := citersByID(res.Refs)
	var out []Fact
	for _, b := range res.Defs {
		v, ok := render.Value(b)
		if !ok {
			continue
		}
		out = append(out, Fact{ID: b.ID, Value: v, Type: b.Args[block.KeyType], File: b.Pos.File, Line: b.Pos.Start, Env: b.Env(), Owner: b.Owner(), Secret: b.IsSecret(), CitedBy: citers[b.ID]})
	}
	return out
}

func citersByID(refs []block.Reference) map[string][]block.Position {
	m := map[string][]block.Position{}
	for _, r := range refs {
		if r.ID != "" {
			m[r.ID] = append(m[r.ID], r.Pos)
		}
	}
	return m
}

// Why is everything that depends on an id (§22 `ds why`).
type Why struct {
	ID        string            `json:"id"`
	Defs      []block.Block     `json:"defs"`
	Refs      []block.Reference `json:"refs"`
	CoveredBy []string          `json:"covered_by"`
	// Chain is the from= path from the id to its truth, first element the
	// id itself; empty when the id has no from= and nothing points at it.
	Chain []block.Block `json:"chain"`
	// Copies are defs whose from= is this id.
	Copies  []block.Block `json:"copies"`
	History []ledger.Ack  `json:"history"`
}

// Why answers `ds why <id>` from a scan and the ack log.
func (s *System) Why(res scan.Result, target string) (Why, error) {
	w := Why{ID: target}
	byID := map[string][]block.Block{}
	for _, b := range append(append([]block.Block{}, res.Defs...), s.merged...) {
		byID[b.ID] = append(byID[b.ID], b)
	}
	w.Defs = byID[target]
	if len(w.Defs) == 0 {
		return w, fmt.Errorf("%w: %s", ErrNotFound, target)
	}
	for _, r := range res.Refs {
		if r.ID == target || contains(r.Directive().List("about"), target) {
			w.Refs = append(w.Refs, r)
		}
	}
	for _, doc := range sortedPages(res) {
		if contains(res.Pages[doc].Covers, target) {
			w.CoveredBy = append(w.CoveredBy, doc)
		}
	}
	seen := map[string]bool{}
	for cur := target; cur != "" && !seen[cur]; {
		seen[cur] = true
		bs := byID[cur]
		if len(bs) == 0 {
			break
		}
		w.Chain = append(w.Chain, bs[0])
		cur = bs[0].From()
	}
	for _, b := range res.Defs {
		if b.From() == target {
			w.Copies = append(w.Copies, b)
		}
	}
	for _, a := range s.acks.Rows {
		if a.ID == target {
			w.History = append(w.History, a)
		}
	}
	return w, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func sortedPages(res scan.Result) []string {
	out := make([]string, 0, len(res.Pages))
	for k := range res.Pages {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// MapOptions bounds `ds map` (§26.3).
type MapOptions struct {
	Budget int
}

// MapPage is one page row.
type MapPage struct {
	Path   string              `json:"path"`
	Covers int                 `json:"covers"`
	Cites  int                 `json:"cites"`
	State  map[check.State]int `json:"state"`
	Owner  string              `json:"owner,omitempty"`
	Tokens int                 `json:"tokens"`
}

// MapDef is one def row.
type MapDef struct {
	ID        string          `json:"id"`
	Desc      string          `json:"desc,omitempty"`
	File      string          `json:"file"`
	Lines     [2]int          `json:"lines"`
	CitedBy   int             `json:"cited_by"`
	Stability block.Stability `json:"stability"`
	State     check.State     `json:"state"`
	Tokens    int             `json:"tokens"`
}

// MapChain is one chain row.
type MapChain struct {
	Root  string      `json:"root"`
	Hops  int         `json:"hops"`
	State check.State `json:"state"`
}

// MapResult is the table of contents an agent reads instead of the tree.
type MapResult struct {
	Envelope
	Pages        []MapPage           `json:"pages"`
	Defs         []MapDef            `json:"defs"`
	Chains       []MapChain          `json:"chains"`
	Freshness    map[check.State]int `json:"freshness"`
	BudgetTokens int                 `json:"budget_tokens"`
	UsedTokens   int                 `json:"used_tokens"`
	Omitted      int                 `json:"omitted"`
}

// Map runs a check and ranks pages and defs under the budget: anything not
// ok first, then most cited, then by path so output is deterministic.
func (s *System) Map(ctx context.Context, opts MapOptions) (MapResult, error) {
	rep, err := s.Check(ctx, CheckOptions{})
	if err != nil {
		return MapResult{}, err
	}
	return s.mapReport(rep, opts), nil
}

func (s *System) mapReport(rep Report, opts MapOptions) MapResult {
	res := rep.Scan
	out := MapResult{Envelope: rep.Envelope, Freshness: rep.States, BudgetTokens: opts.Budget}
	// Per-page and per-def state from findings.
	pageState := map[string]map[check.State]int{}
	defState := map[string]check.State{}
	worst := func(cur, next check.State) check.State {
		if rank(next) > rank(cur) {
			return next
		}
		return cur
	}
	for _, f := range rep.Findings {
		if pageState[f.Doc] == nil {
			pageState[f.Doc] = map[check.State]int{}
		}
		pageState[f.Doc][f.State]++
		if f.ID != "" {
			defState[f.ID] = worst(defState[f.ID], f.State)
		}
	}
	citers := citersByID(res.Refs)
	cites := map[string]int{}
	for _, r := range res.Refs {
		cites[r.Pos.File]++
	}
	docs := map[string]bool{}
	for _, r := range res.Refs {
		docs[r.Pos.File] = true
	}
	for p := range res.Pages {
		docs[p] = true
	}
	var pages []MapPage
	for p := range docs {
		pg := MapPage{Path: p, Covers: len(res.Pages[p].Covers), Cites: cites[p], State: pageState[p]}
		if pg.State == nil {
			pg.State = map[check.State]int{}
		}
		pg.Tokens = Tokens(fmt.Sprintf("%s %d %d %v", p, pg.Covers, pg.Cites, pg.State))
		pages = append(pages, pg)
	}
	sort.Slice(pages, func(i, j int) bool {
		a, b := pages[i], pages[j]
		if na, nb := notOK(a.State), notOK(b.State); na != nb {
			return na > nb
		}
		if a.Cites != b.Cites {
			return a.Cites > b.Cites
		}
		return a.Path < b.Path
	})
	var defs []MapDef
	for _, b := range res.Defs {
		st := defState[b.ID]
		if st == "" {
			st = check.StateOK
		}
		d := MapDef{ID: b.ID, Desc: b.Args[block.KeyDesc], File: b.Pos.File, Lines: [2]int{b.Pos.Start, b.Pos.End}, CitedBy: len(citers[b.ID]), Stability: b.Stability(), State: st}
		d.Tokens = Tokens(fmt.Sprintf("%s %s %s %d %d %s", d.ID, d.Desc, d.File, d.CitedBy, d.Lines, d.State))
		defs = append(defs, d)
	}
	sort.Slice(defs, func(i, j int) bool {
		a, b := defs[i], defs[j]
		if ra, rb := rank(a.State), rank(b.State); ra != rb {
			return ra > rb
		}
		if a.CitedBy != b.CitedBy {
			return a.CitedBy > b.CitedBy
		}
		return a.ID < b.ID
	})
	// Chains: roots are defs with no from= that something points at.
	pointed := map[string]bool{}
	byID := map[string]block.Block{}
	for _, b := range res.Defs {
		byID[b.ID] = b
		if f := b.From(); f != "" {
			pointed[f] = true
		}
	}
	for _, b := range res.Defs {
		if b.From() == "" && (b.IsTruth() || pointed[b.ID]) {
			hops := 0
			for _, c := range res.Defs {
				if chainRoot(byID, c.ID) == b.ID {
					hops++
				}
			}
			st := check.StateOK
			for _, f := range rep.Findings {
				if f.State == check.StateChainBroken && chainRoot(byID, f.ID) == b.ID {
					st = check.StateChainBroken
				}
			}
			out.Chains = append(out.Chains, MapChain{Root: b.ID, Hops: hops, State: st})
		}
	}
	sort.Slice(out.Chains, func(i, j int) bool { return out.Chains[i].Root < out.Chains[j].Root })
	// Fit under budget: pages first, then defs.
	budget := opts.Budget
	for _, p := range pages {
		if budget > 0 && out.UsedTokens+p.Tokens > budget {
			out.Omitted++
			continue
		}
		out.UsedTokens += p.Tokens
		out.Pages = append(out.Pages, p)
	}
	for _, d := range defs {
		if budget > 0 && out.UsedTokens+d.Tokens > budget {
			out.Omitted++
			continue
		}
		out.UsedTokens += d.Tokens
		out.Defs = append(out.Defs, d)
	}
	return out
}

// chainRoot follows from= to the root, stopping on cycles or gaps.
func chainRoot(byID map[string]block.Block, id string) string {
	seen := map[string]bool{}
	for {
		b, ok := byID[id]
		if !ok || seen[id] {
			return id
		}
		seen[id] = true
		if b.From() == "" {
			return id
		}
		id = b.From()
	}
}

// rank orders states by urgency for ranking; higher is more urgent. The
// empty state (nothing found) ranks lowest.
func rank(st check.State) int {
	if st == "" {
		return 0
	}
	switch check.SeverityOf(st) {
	case check.SeverityError:
		return 3
	case check.SeverityWarning:
		return 2
	case check.SeverityInfo:
		return 1
	}
	return 0
}

func notOK(m map[check.State]int) int {
	n := 0
	for st, c := range m {
		if st != check.StateOK && st != check.StateMoved {
			n += c
		}
	}
	return n
}

// ContextOptions bounds `ds context` (§26.4).
type ContextOptions struct {
	Budget int
	Since  string
	Mode   string
}

// ContextItem is one ranked piece of context.
type ContextItem struct {
	Rank    int    `json:"rank"`
	Why     string `json:"why"`
	ID      string `json:"id"`
	File    string `json:"file"`
	Lines   [2]int `json:"lines"`
	Tokens  int    `json:"tokens"`
	Mode    string `json:"mode"`
	Content string `json:"content"`
}

// Omitted names an item that did not fit.
type Omitted struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// ContextResult is the bundle.
type ContextResult struct {
	Envelope
	Target       string        `json:"target"`
	BudgetTokens int           `json:"budget_tokens"`
	UsedTokens   int           `json:"used_tokens"`
	Items        []ContextItem `json:"items"`
	Omitted      []Omitted     `json:"omitted"`
}

// Context returns a page with every block it cites, or a block with every
// sentence about it, ranked and budgeted. Order is deterministic for
// identical inputs so prompt caches hit.
func (s *System) Context(ctx context.Context, target string, opts ContextOptions) (ContextResult, error) {
	rep, err := s.Check(ctx, CheckOptions{})
	if err != nil {
		return ContextResult{}, err
	}
	return s.contextReport(rep, target, opts), nil
}

// ContextFor is Context over an existing report, for callers that already
// checked and want several bundles without rescanning.
func (s *System) ContextFor(rep Report, target string, opts ContextOptions) ContextResult {
	return s.contextReport(rep, target, opts)
}

func (s *System) contextReport(rep Report, target string, opts ContextOptions) ContextResult {
	if opts.Mode == "" {
		opts.Mode = ModeAuto
	}
	res := rep.Scan
	out := ContextResult{Envelope: rep.Envelope, Target: target, BudgetTokens: opts.Budget}
	byID := map[string][]block.Block{}
	for _, b := range append(append([]block.Block{}, res.Defs...), s.merged...) {
		byID[b.ID] = append(byID[b.ID], b)
	}
	changes := map[string]match.Change{}
	for _, c := range rep.Changes {
		changes[c.Key()] = c
	}
	stateOf := map[string]check.State{}
	for _, f := range rep.Findings {
		if f.ID != "" && rank(f.State) > rank(stateOf[f.ID]) {
			stateOf[f.ID] = f.State
		}
	}
	type cand struct {
		item ContextItem
		urg  int
		ord  int
	}
	var cands []cand
	if bs, isID := byID[target]; isID {
		// A block and every sentence about it.
		b := bs[0]
		item := s.blockItem(b, changes[match.BlockKey(b)], stateOf[b.ID], opts)
		cands = append(cands, cand{item: item, urg: rank(stateOf[b.ID]) + 1, ord: 0})
		for i, r := range res.Refs {
			if r.ID != target || r.Sentence == "" {
				continue
			}
			cands = append(cands, cand{item: ContextItem{Why: "cites " + target, File: r.Pos.File, Lines: [2]int{r.Pos.Start, r.Pos.Start}, Mode: ModeFull, Content: r.Sentence, Tokens: Tokens(r.Sentence)}, ord: i + 1})
		}
	} else {
		seen := map[string]bool{}
		for i, r := range res.Refs {
			if r.Pos.File != target || r.ID == "" || seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			bs, ok := byID[r.ID]
			if !ok {
				cands = append(cands, cand{item: ContextItem{Why: string(check.StateBroken), ID: r.ID, Mode: ModeLine, Content: "not defined", Tokens: Tokens("not defined")}, urg: 3, ord: i})
				continue
			}
			b := bs[0]
			cands = append(cands, cand{item: s.blockItem(b, changes[match.BlockKey(b)], stateOf[b.ID], opts), urg: rank(stateOf[b.ID]), ord: i})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].urg != cands[j].urg {
			return cands[i].urg > cands[j].urg
		}
		return cands[i].ord < cands[j].ord
	})
	for _, c := range cands {
		if opts.Budget > 0 && out.UsedTokens+c.item.Tokens > opts.Budget {
			reason := ReasonOverBudget
			if c.urg == 0 {
				reason = ReasonUnchanged
			}
			out.Omitted = append(out.Omitted, Omitted{ID: c.item.ID, Reason: reason})
			continue
		}
		c.item.Rank = len(out.Items) + 1
		out.UsedTokens += c.item.Tokens
		out.Items = append(out.Items, c.item)
	}
	return out
}

// blockItem picks the content mode for a cited block (§26.4 --mode auto).
func (s *System) blockItem(b block.Block, c match.Change, st check.State, opts ContextOptions) ContextItem {
	item := ContextItem{ID: b.ID, File: b.Pos.File, Lines: [2]int{b.Pos.Start, b.Pos.End}}
	why := "cited, unchanged"
	if st != "" && st != check.StateOK {
		why = string(st)
	}
	item.Why = why
	changed := c.State == match.StateChanged
	mode := opts.Mode
	if mode == ModeAuto {
		switch {
		case changed && c.Diff != "" && opts.Since == SinceAck:
			mode = ModeDiff
		case isValue(b):
			mode = ModeValue
		case strings.Count(b.Content, "\n") < s.cfg.Include.MaxLines || changed:
			mode = ModeFull
		default:
			mode = ModeLine
		}
	}
	switch mode {
	case ModeDiff:
		if c.Diff == "" {
			mode = ModeLine
			item.Content = location(b) + " (no diff available)"
			break
		}
		item.Content = c.Diff
	case ModeValue:
		if v, ok := render.Value(b); ok {
			item.Content = v
			break
		}
		mode = ModeFull
		item.Content = b.Content
	case ModeFull:
		item.Content = b.Content
	default:
		mode = ModeLine
		item.Content = location(b)
	}
	item.Mode = mode
	item.Tokens = Tokens(item.Content)
	return item
}

func isValue(b block.Block) bool {
	_, ok := render.Value(b)
	return ok
}

func location(b block.Block) string {
	return fmt.Sprintf("%s:%d-%d", b.Pos.File, b.Pos.Start, b.Pos.End)
}

// ImpactGroup is findings grouped by one key.
type ImpactGroup struct {
	Key      string          `json:"key"`
	Findings []check.Finding `json:"findings"`
}

// ImpactResult is what a change will flag (§22 `ds impact`).
type ImpactResult struct {
	Envelope
	Total   int           `json:"total"`
	ByDoc   []ImpactGroup `json:"by_doc"`
	ByOwner []ImpactGroup `json:"by_owner"`
	ByRepo  []ImpactGroup `json:"by_repo"`
}

// Impact checks the working tree against the previous ledger and returns
// only the findings a change causes: unacked, translation-stale, broken,
// expired-by-about. Everything else is hygiene, not impact.
func (s *System) Impact(ctx context.Context) (ImpactResult, error) {
	rep, err := s.Check(ctx, CheckOptions{})
	if err != nil {
		return ImpactResult{}, err
	}
	return impactReport(rep), nil
}

func impactReport(rep Report) ImpactResult {
	out := ImpactResult{Envelope: rep.Envelope}
	byDoc, byOwner, byRepo := map[string][]check.Finding{}, map[string][]check.Finding{}, map[string][]check.Finding{}
	for _, f := range rep.Findings {
		switch f.State {
		case check.StateUnacked, check.StateTranslationStale, check.StateBroken, check.StateExpired:
		default:
			continue
		}
		out.Total++
		byDoc[f.Doc] = append(byDoc[f.Doc], f)
		byOwner[f.Owner] = append(byOwner[f.Owner], f)
		byRepo[f.Repo] = append(byRepo[f.Repo], f)
	}
	out.ByDoc, out.ByOwner, out.ByRepo = groups(byDoc), groups(byOwner), groups(byRepo)
	return out
}

func groups(m map[string][]check.Finding) []ImpactGroup {
	out := make([]ImpactGroup, 0, len(m))
	for k, fs := range m {
		out = append(out, ImpactGroup{Key: k, Findings: fs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// FindOptions narrows Find (§26.5 `ds find <query> | --file path | --tag t`).
// Query matches id and symbol by case-insensitive substring; File is a path
// prefix; Tag is exact. Set fields combine with and; an empty set matches
// everything.
type FindOptions struct {
	Query string
	File  string
	Tag   string
}

// Find returns defs matching a free query: substring on id or symbol, file
// prefix, or exact tag, whichever hits.
func (s *System) Find(res scan.Result, query string) []block.Block {
	q := strings.ToLower(query)
	var out []block.Block
	for _, b := range append(append([]block.Block{}, res.Defs...), s.merged...) {
		switch {
		case strings.Contains(strings.ToLower(b.ID), q), strings.Contains(strings.ToLower(b.Symbol), q), strings.HasPrefix(b.Pos.File, query), contains(b.Tags(), query):
			out = append(out, b)
		}
	}
	return out
}

// FindBy applies each set option as a filter.
func (s *System) FindBy(res scan.Result, opts FindOptions) []block.Block {
	q := strings.ToLower(opts.Query)
	var out []block.Block
	for _, b := range append(append([]block.Block{}, res.Defs...), s.merged...) {
		if q != "" && !strings.Contains(strings.ToLower(b.ID), q) && !strings.Contains(strings.ToLower(b.Symbol), q) {
			continue
		}
		if opts.File != "" && !strings.HasPrefix(b.Pos.File, opts.File) {
			continue
		}
		if opts.Tag != "" && !contains(b.Tags(), opts.Tag) {
			continue
		}
		out = append(out, b)
	}
	return out
}

// Read returns a block's body, or a `lines=a-b` fragment of it.
func (s *System) Read(res scan.Result, id, lines string) (string, error) {
	b, ok := s.LocateID(res, id)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if lines == "" {
		return b.Content, nil
	}
	// The grammar check and render use, so a citation that renders in a
	// doc also reads here.
	a, c, err := linerange.Parse(lines)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadLines, err)
	}
	all := strings.Split(b.Content, "\n")
	if c > len(all) {
		return "", fmt.Errorf("%w: lines %q beyond %d", ErrNotFound, lines, len(all))
	}
	return strings.Join(all[a-1:c], "\n"), nil
}

// LocateID returns the block for an id, this repo's defs first.
func (s *System) LocateID(res scan.Result, id string) (block.Block, bool) {
	for _, b := range append(append([]block.Block{}, res.Defs...), s.merged...) {
		if b.ID == id {
			return b, true
		}
	}
	return block.Block{}, false
}

// AckRequest describes one approval (§19, §26.7).
type AckRequest struct {
	ID          string
	Doc         string
	Line        int
	Env         string
	Actor       string
	ActorKind   ledger.ActorKind
	DelegatedBy string
	Note        string
	// Claim renews the claim on Doc:Line rather than acking a citation:
	// ID is then ignored, and a line with no claim is ErrNoReference.
	Claim bool
	// Preview builds the row a real ack would record and notifies no
	// observer, for `ack --dry-run`: an embedder watching acks must not be
	// told of one that was never written.
	Preview bool
	// Page records that the whole of Doc was reread, for its review_every
	// schedule: ID and Line are ignored, and the row carries no hashes.
	// Before, a page review could only be recorded by acking a citation on
	// the page, so a page with review_every and nothing cited had a finding
	// nothing could clear.
	Page bool
}

// matches reports whether a reference on the requested line is the one
// this request acks. Claim selects the claim on the line and nothing else —
// a claim has no id, and without it an id-less ack on a line that also held
// a citation acked the citation instead of renewing the claim. Otherwise an
// empty ID takes the first reference on the line, as it always has.
func (req AckRequest) matches(r *block.Reference) bool {
	if req.Claim {
		return r.Verb == extract.VerbClaim
	}
	return req.ID == "" || r.ID == req.ID
}

// Ack builds the ack row for a reference against the block's current hash
// and the sentence's current hash. The caller appends it to the log. An
// agent actor must name who delegated; the library refuses otherwise so no
// tool path can approve its own work silently.
func (s *System) Ack(res scan.Result, req AckRequest) (ledger.Ack, error) {
	if req.ActorKind == "" {
		req.ActorKind = ledger.ActorHuman
	}
	if req.ActorKind == ledger.ActorAgent && req.DelegatedBy == "" {
		return ledger.Ack{}, ErrDelegationRequired
	}
	if req.Page {
		if res.Pages[req.Doc].ReviewEvery == "" {
			return ledger.Ack{}, fmt.Errorf("%w: %s", ErrNoPageReview, req.Doc)
		}
		a := ledger.Ack{At: s.now(), Actor: req.Actor, ActorKind: req.ActorKind, DelegatedBy: req.DelegatedBy, Repo: s.repo, Doc: req.Doc, Note: req.Note, Rule: extract.Rule}
		if !req.Preview {
			s.observeAck(a)
		}
		return a, nil
	}
	var ref *block.Reference
	for i := range res.Refs {
		r := &res.Refs[i]
		if r.Pos.File == req.Doc && r.Pos.Start == req.Line && req.matches(r) {
			ref = r
			break
		}
	}
	if ref == nil {
		return ledger.Ack{}, fmt.Errorf("%w: %s:%d", ErrNoReference, req.Doc, req.Line)
	}
	// The ack is for this citation, so it carries the citation's env and
	// the hash of the def that env resolves to — the key and the value check
	// looks it up by. Using the request's env (empty from the CLI) and the
	// first def with the id recorded a prod citation's ack with no env and
	// the dev value's hash: it matched nothing, and the citation could never
	// be acked.
	env := req.Env
	if env == "" {
		env = ref.Args[block.KeyEnv]
	}
	// The ack records the rule it was made under and the sentence it
	// approved, so check can tell a rewritten sentence from one bound by an
	// older rule, and show the old wording beside the new.
	a := ledger.Ack{At: s.now(), Actor: req.Actor, ActorKind: req.ActorKind, DelegatedBy: req.DelegatedBy, ID: ref.ID, Repo: s.repo, Doc: req.Doc, Line: req.Line, Env: env, SentenceHash: ref.SentenceHash, Note: req.Note, Rule: extract.Rule, Sentence: ref.Sentence}
	if ref.ID != "" {
		resolveEnv := env
		if resolveEnv == "" {
			resolveEnv = s.cfg.Env.Default
		}
		b, ok := check.ResolveDef(s.defsByID(res)[ref.ID], resolveEnv, ref.Args[block.KeyBranch])
		if !ok {
			return ledger.Ack{}, fmt.Errorf("%w: %s", ErrNotFound, ref.ID)
		}
		a.BlockHash = b.Hash
	}
	if !req.Preview {
		s.observeAck(a)
	}
	return a, nil
}

// defsByID lists every def of each id, local first then merged: the same
// candidates, in the same order, that check resolves a citation among.
func (s *System) defsByID(res scan.Result) map[string][]block.Block {
	m := map[string][]block.Block{}
	for _, b := range res.Defs {
		m[b.ID] = append(m[b.ID], b)
	}
	for _, b := range s.merged {
		m[b.ID] = append(m[b.ID], b)
	}
	return m
}
