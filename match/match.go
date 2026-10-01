// Package match compares the blocks a scan found against the previous
// ledger and decides, per id, what happened (docs/SPEC.md §16 pass 3): ok,
// moved, changed, moved-unmarked, rewritten?, deleted, or new. It also
// classifies a change (§20) so a finding can say "signature changed" instead
// of "hash differs", and so a stability policy can decide whether prose flags.
//
// The classifier here is the generic one that works without a grammar:
// whitespace, comment, value, body, renamed, moved. The syntax tier in
// ext/treesitter registers a Classifier that adds signature and type; the
// interface is in the root package and this package accepts the result.
package match

import (
	"sort"
	"strings"
	"unicode"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/internal/difflib"
	"github.com/ubgo/docsync/internal/textnorm"
	"github.com/ubgo/docsync/ledger"
)

// State is what happened to an id between the previous ledger and now.
type State string

const (
	StateOK            State = "ok"             // same hash, same place
	StateMoved         State = "moved"          // same hash, new file or lines
	StateChanged       State = "changed"        // same id, different hash
	StateMovedUnmarked State = "moved-unmarked" // id gone, a block with the same hash exists elsewhere
	StateRewritten     State = "rewritten?"     // id gone, a similar block exists; a guess, reported as such
	StateDeleted       State = "deleted"        // id gone, nothing similar
	StateNew           State = "new"            // id not in the previous ledger
)

// StateValues is the canonical order.
var StateValues = []State{StateOK, StateMoved, StateChanged, StateMovedUnmarked, StateRewritten, StateDeleted, StateNew}

// DefaultFuzzyThreshold is the difflib ratio at or above which a vanished
// block is reported as `rewritten?` against its best unmarked candidate
// (§35 decisions: diff ratio, 0.8).
const DefaultFuzzyThreshold = 0.8

// Change is the comparison result for one id.
type Change struct {
	ID    string
	State State
	// Old is the previous ledger row; zero for StateNew.
	Old ledger.Row
	// New is the current block; zero for StateDeleted and StateRewritten.
	New block.Block
	// Candidate is the unmarked block a vanished id was matched to, for
	// StateMovedUnmarked and StateRewritten.
	Candidate block.Block
	// Ratio is the similarity for StateRewritten.
	Ratio float64
	// Classes is non-empty only for StateChanged.
	Classes []block.Class
	// Diff is the compact unified diff for StateChanged, empty otherwise. It
	// is computed against Old.Content only when the caller supplied it via
	// Options.OldContent; the ledger stores hashes, not bodies.
	Diff string
	// Flags reports whether the change should flag citing prose under the
	// block's stability policy. False for every state except StateChanged.
	Flags bool
}

// Env is the environment of the def this change is about: one id defined
// per environment is one change per environment.
func (c Change) Env() string {
	if c.New.ID != "" {
		return c.New.Env()
	}
	return c.Old.Env
}

// Key identifies the def this change is about; see BlockKey.
func (c Change) Key() string {
	if c.New.ID != "" {
		return BlockKey(c.New)
	}
	return RowKey(c.Old)
}

// BlockKey is how a def is identified across scans and between readers: its
// id, its environment, and the branch it was published from. An id alone is
// not enough. `port` defined for prod and for dev is two blocks, and so is
// an id published from main and from a feature branch; keyed by id, each
// comparison used whichever came last — listing prod before dev reported a
// change nothing made, and a published branch made main look behind.
func BlockKey(b block.Block) string { return key(b.ID, b.Env(), b.Args[block.KeyBranch]) }

// RowKey is BlockKey for a ledger row.
func RowKey(r ledger.Row) string { return key(r.ID, r.Env, r.Args[block.KeyBranch]) }

// key joins the parts with a byte no id, environment, or branch contains.
func key(id, env, branch string) string { return id + "\x00" + env + "\x00" + branch }

// Classifier decides the classes of a change between two versions of a block.
// The generic one is Classify; a grammar-aware one may replace it.
type Classifier func(old, new block.Block, oldContent string) []block.Class

// Options tunes matching.
type Options struct {
	// FuzzyThreshold for rewritten?; zero means DefaultFuzzyThreshold.
	FuzzyThreshold float64
	// OldContent returns the body the block had in row, a previous ledger
	// row, if the caller has it (from git at the ledger's commit, or the
	// body store). It is handed the row rather than an id because an id
	// alone cannot say which of several defs is meant — one per
	// environment, one per published branch — and a by-id lookup served
	// another environment's body. The row also carries its repo, so the
	// same hook can answer for a foreign block. Nil, or a miss, means no
	// diff and a hash difference classified as unknown (value for fact
	// kinds), never body: `api` does not flag body, so guessing body for a
	// change nobody could read let a signature change pass (bug 28).
	OldContent func(row ledger.Row) (string, bool)
	// Classify overrides the generic classifier.
	Classify Classifier
	// CommentPrefixes lists line-comment prefixes for the block's language so
	// comment-only changes can be detected without a grammar. The caller
	// derives it from extract.Styles; empty disables comment detection.
	CommentPrefixes func(file string) []string
}

// Compare matches current blocks against previous rows. Every previous and
// every current def — an id in an environment, see Key — appears exactly
// once in the result, sorted by id then environment, so a caller can rely on
// completeness.
func Compare(prev []ledger.Row, cur []block.Block, opts Options) []Change {
	if opts.FuzzyThreshold == 0 {
		opts.FuzzyThreshold = DefaultFuzzyThreshold
	}
	if opts.Classify == nil {
		opts.Classify = func(old, nw block.Block, oldContent string) []block.Class {
			return Classify(old, nw, oldContent, prefixesFor(opts, nw.Pos.File))
		}
	}
	prevByKey := map[string]ledger.Row{}
	for _, r := range prev {
		prevByKey[RowKey(r)] = r
	}
	oldContent := func(row ledger.Row) (string, bool) {
		if opts.OldContent == nil || withheld(row.ToBlock()) {
			return "", false
		}
		return opts.OldContent(row)
	}
	curByKey := map[string]block.Block{}
	for _, b := range cur {
		curByKey[BlockKey(b)] = b
	}
	var out []Change
	// Defs present now.
	for key, nw := range curByKey {
		id := nw.ID
		old, had := prevByKey[key]
		switch {
		case !had:
			out = append(out, Change{ID: id, State: StateNew, New: nw})
		case old.Hash == nw.Hash && old.File == nw.Pos.File && old.Start == nw.Pos.Start && old.End == nw.Pos.End:
			out = append(out, Change{ID: id, State: StateOK, Old: old, New: nw})
		case old.Hash == nw.Hash:
			out = append(out, Change{ID: id, State: StateMoved, Old: old, New: nw})
		default:
			body, haveOld := oldContent(old)
			oldBlock := old.ToBlock()
			// Without the old body no classifier can say what changed, and a
			// custom one handed "" would describe an empty block; the generic
			// rules then say what the rows alone support — renamed, moved, a
			// fact's value — and unknown for the rest (§20).
			var classes []block.Class
			if haveOld {
				classes = opts.Classify(oldBlock, nw, body)
			} else {
				classes = Classify(oldBlock, nw, "", nil)
			}
			c := Change{ID: id, State: StateChanged, Old: old, New: nw, Classes: classes, Flags: block.Flags(nw.Stability(), classes)}
			// The new side is checked as well as the old: a def that became
			// secret since the last scan has a clean previous row, and its
			// current content is the value.
			if haveOld && !withheld(nw) {
				c.Diff = difflib.Unified(body, nw.Content, 0)
			}
			out = append(out, c)
		}
	}
	// Ids that vanished: try to find them among current blocks that are new
	// (an unmarked block carries no id, but a block whose id is new and whose
	// hash equals the vanished one is the same code re-marked; a block with a
	// similar body is a probable rewrite).
	var unmatchedNew []block.Block
	for _, b := range cur {
		if _, had := prevByKey[BlockKey(b)]; !had {
			unmatchedNew = append(unmatchedNew, b)
		}
	}
	for key, old := range prevByKey {
		if _, still := curByKey[key]; still {
			continue
		}
		c := Change{ID: old.ID, State: StateDeleted, Old: old}
		if cand, ok := byHash(unmatchedNew, old.Hash); ok {
			c.State, c.Candidate = StateMovedUnmarked, cand
		} else if body, have := oldContent(old); have {
			if cand, ratio, ok := bestFuzzy(unmatchedNew, body, opts.FuzzyThreshold); ok {
				c.State, c.Candidate, c.Ratio = StateRewritten, cand, ratio
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Env() < out[j].Env()
	})
	return out
}

// withheld reports a block whose body docsync never compares or shows: a
// secret, whose content is the value itself, or a local one, which exists on
// some machines only (§12).
//
// Why it is checked here and not left to each hook: a diff is the one place
// the match step turns bodies into text a person or a CI log reads, and
// before this a secret changed since its last scan came back as
// "-api_key: sk-…old / +sk-…new" in `ds check --json`, both values in full.
// The body store already withheld secrets, so a scan made the leak vanish,
// which is why it went unnoticed. Refusing at the one place bodies are
// fetched means no hook, present or future, can hand one in.
func withheld(b block.Block) bool { return b.IsSecret() || b.IsLocal() }

func prefixesFor(opts Options, file string) []string {
	if opts.CommentPrefixes == nil {
		return nil
	}
	return opts.CommentPrefixes(file)
}

func byHash(cands []block.Block, hash string) (block.Block, bool) {
	for _, b := range cands {
		if b.Hash == hash {
			return b, true
		}
	}
	return block.Block{}, false
}

func bestFuzzy(cands []block.Block, oldContent string, threshold float64) (block.Block, float64, bool) {
	oldLines := difflib.Lines(textnorm.NormalizeString(oldContent))
	var best block.Block
	bestRatio := 0.0
	for _, b := range cands {
		r := difflib.Ratio(oldLines, difflib.Lines(textnorm.NormalizeString(b.Content)))
		if r > bestRatio {
			best, bestRatio = b, r
		}
	}
	if bestRatio >= threshold && bestRatio > 0 {
		return best, bestRatio, true
	}
	return block.Block{}, 0, false
}

// valueKinds are the kinds whose change is a value change, not a body change.
var valueKinds = map[block.Kind]bool{block.KindKey: true, block.KindLine: true, block.KindLinkText: true}

// Classify is the generic classifier (§20) for two versions of a block. It
// never returns ClassWhitespace alone with other classes: whitespace-only
// differences are invisible because hashes are computed over normalized text,
// so two blocks that differ only in whitespace have equal hashes and never
// reach Compare's changed branch. It returns:
//   - renamed when the symbol changed
//   - moved when the file or line range changed (in addition to other classes)
//   - value for fact-shaped kinds
//   - comment when old content is known and every differing line is a comment
//   - signature or type when a declaration's head changed
//   - value (and type, when what is declared changed) for a one-line
//     constant: `MaxRetries = 5` to `= 3` is a value change exactly as a
//     YAML key's is, and `api` must flag it (bug 27)
//   - unknown when oldContent is empty: the old body was not available, and
//     the difference is real but undescribed (§20); never body
//   - body otherwise
func Classify(old, nw block.Block, oldContent string, commentPrefixes []string) []block.Class {
	var classes []block.Class
	if old.Symbol != "" && nw.Symbol != "" && old.Symbol != nw.Symbol {
		classes = append(classes, block.ClassRenamed)
	}
	if old.Pos.File != nw.Pos.File || old.Pos.Start != nw.Pos.Start || old.Pos.End != nw.Pos.End {
		classes = append(classes, block.ClassMoved)
	}
	if old.Hash == nw.Hash {
		return classes
	}
	renamed := len(classes) > 0 && classes[0] == block.ClassRenamed
	switch {
	case valueKinds[nw.Kind]:
		classes = append(classes, block.ClassValue)
	case oldContent == "":
		classes = append(classes, block.ClassUnknown)
	case len(commentPrefixes) > 0 && commentOnly(oldContent, nw.Content, commentPrefixes):
		classes = append(classes, block.ClassComment)
	case nw.Kind == block.KindConst && constClasses(&classes, old, nw, oldContent, commentPrefixes, renamed):
		// constClasses appended the classes.
	case len(commentPrefixes) > 0 && declKinds[nw.Kind] && !renamed && declLine(oldContent, commentPrefixes) != declLine(nw.Content, commentPrefixes):
		// The declaration line changed: a signature for functions, a shape
		// for types (§20). A rename already says the head changed, so it is
		// not doubled as a signature. The rest of the block is checked
		// separately so a signature-and-body edit carries both classes.
		if nw.Kind == block.KindType {
			classes = append(classes, block.ClassType)
		} else {
			classes = append(classes, block.ClassSignature)
		}
		if restOf(oldContent, commentPrefixes) != restOf(nw.Content, commentPrefixes) {
			classes = append(classes, block.ClassBody)
		}
	default:
		classes = append(classes, block.ClassBody)
	}
	return classes
}

// constClasses classifies a change to a constant or variable declaration
// that is one line of code on both sides, appending to classes, and reports
// whether it applied. The part before the first `=` is what is declared —
// name and type — and the part after it is the value, so `MaxRetries = 5`
// to `MaxRetries = 3` is value and `X int = 3` to `X int64 = 3` is type. A
// rename already says the head changed, so it is not doubled as type.
// Comment lines, under the file's prefixes, are not code; a multi-line
// initializer is not one value and is left to the other rules.
//
// The syntax tier binds a constant whose value is a single literal to the
// literal alone (§10), so a content of `5` is a value with no head, and a
// head is compared only when both sides have one.
//
// Why: §20 says a fact's change is a value change, and a one-value constant
// is a fact written in code. Classified as body, `stability=api` let a
// changed constant through, though its value is the whole of its API.
func constClasses(classes *[]block.Class, old, nw block.Block, oldContent string, prefixes []string, renamed bool) bool {
	oldHead, oldValue, ok := oneAssignment(oldContent, old.Symbol, prefixes)
	if !ok {
		return false
	}
	newHead, newValue, ok := oneAssignment(nw.Content, nw.Symbol, prefixes)
	if !ok {
		return false
	}
	headChanged := oldHead != "" && newHead != "" && oldHead != newHead
	if headChanged && !renamed {
		*classes = append(*classes, block.ClassType)
	}
	if oldValue != newValue {
		*classes = append(*classes, block.ClassValue)
	}
	if !headChanged && oldValue == newValue {
		// Nothing explains the difference: head and value compare equal
		// once whitespace is collapsed (a rename explains a differing
		// head), so what differs is something else on the line, and it is
		// still a change.
		*classes = append(*classes, block.ClassBody)
	}
	return true
}

// assignOp splits a declaration's head from its value.
const assignOp = "="

// quotes open a string literal; a line starting with one is a value even
// when the string spells the symbol's name.
const quotes = "\"'`"

// oneAssignment splits the single code line of content into the declared
// head and the value, whitespace collapsed, or reports that content is not
// one line of code. A line with no `=` is a declaration with no value
// (`var X int`) when it names the symbol, and otherwise the bare literal
// the syntax tier bound (`5`).
func oneAssignment(content, symbol string, prefixes []string) (string, string, bool) {
	code := ""
	for _, l := range strings.Split(textnorm.NormalizeString(content), "\n") {
		t := strings.TrimSpace(l)
		if t == "" || isCommentLine(t, prefixes) {
			continue
		}
		if code != "" {
			return "", "", false
		}
		code = t
	}
	if code == "" {
		return "", "", false
	}
	collapse := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if head, value, found := strings.Cut(code, assignOp); found {
		return collapse(head), collapse(value), true
	}
	if strings.ContainsAny(code[:1], quotes) || !namesSymbol(code, symbol) {
		return "", collapse(code), true
	}
	return collapse(code), "", true
}

// namesSymbol reports whether code contains the last segment of symbol as a
// whole identifier.
func namesSymbol(code, symbol string) bool {
	name := symbol[strings.LastIndex(symbol, ".")+1:]
	if name == "" {
		return false
	}
	isIdent := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	for _, w := range strings.FieldsFunc(code, func(r rune) bool { return !isIdent(r) }) {
		if w == name {
			return true
		}
	}
	return false
}

// declKinds are the kinds with a declaration line worth classifying on its
// own; statements, sections, and paragraphs have no head/body split. The
// split also needs the file's comment grammar (prefixes), because without it
// the declaration line cannot be told from a leading comment: files without
// a grammar produce body or value only (§20).
var declKinds = map[block.Kind]bool{block.KindFunc: true, block.KindType: true}

// declLine returns the first non-blank, non-comment line with whitespace
// collapsed, so a re-indented declaration is not a signature change.
func declLine(content string, prefixes []string) string {
	i := declIndex(content, prefixes)
	lines := strings.Split(content, "\n")
	if i < 0 || i >= len(lines) {
		return ""
	}
	return strings.Join(strings.Fields(lines[i]), " ")
}

// restOf returns everything after the declaration line, normalised.
func restOf(content string, prefixes []string) string {
	i := declIndex(content, prefixes)
	lines := strings.Split(content, "\n")
	if i < 0 || i+1 >= len(lines) {
		return ""
	}
	return textnorm.NormalizeString(strings.Join(lines[i+1:], "\n"))
}

// declIndex finds the declaration line's index, or -1.
func declIndex(content string, prefixes []string) int {
	for i, l := range strings.Split(content, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || isCommentLine(t, prefixes) {
			continue
		}
		return i
	}
	return -1
}

// notComments are line starts that a comment prefix matches but that are
// code. PHP 8 attributes (`#[Route('/users')]`) begin with `#`, one of
// PHP's comment prefixes, and read as comments made a changed route a
// comment-only edit, which neither a stable nor an api block flags. The
// exclusion applies to every `#` language: a real comment starting `#[` in
// Python or Ruby is then classified as code, which over-reports — the safe
// direction for a check whose failure mode is silence.
var notComments = []string{"#["}

// isCommentLine reports whether a trimmed line is a line comment under the
// given prefixes. It is the one comment test in this package, so the
// classifier and the declaration finder cannot disagree.
func isCommentLine(t string, prefixes []string) bool {
	for _, n := range notComments {
		if strings.HasPrefix(t, n) {
			return false
		}
	}
	for _, p := range prefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// commentOnly reports whether every changed line in the diff between old and
// new is a comment line under the given prefixes.
func commentOnly(oldContent, newContent string, prefixes []string) bool {
	edits := difflib.Compact(difflib.Diff(difflib.Lines(textnorm.NormalizeString(oldContent)), difflib.Lines(textnorm.NormalizeString(newContent))), 0)
	if len(edits) == 0 {
		return false
	}
	for _, e := range edits {
		if !isCommentLine(strings.TrimSpace(e.Line), prefixes) {
			return false
		}
	}
	return true
}
