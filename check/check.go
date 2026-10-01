// Package check turns a scan, the previous ledger, and the ack log into
// findings: one row per reference or per problem, with a state from the
// spec's table, a severity, the change class and diff where relevant, and a
// remedy an agent can execute verbatim (docs/SPEC.md §16 passes 3 to 5, §17).
//
// It is a pure function of its Input. Nothing here reads files, runs
// commands, or touches the network. Things that need any of those (resolvers,
// url checks, `run`, commit lookups) arrive as optional hooks in Options and
// are reported as `unverifiable` or `skipped` when absent, never silently
// passed.
package check

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/internal/difflib"
	"github.com/ubgo/docsync/internal/duration"
	"github.com/ubgo/docsync/internal/linerange"
	"github.com/ubgo/docsync/internal/textnorm"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/match"
	"github.com/ubgo/docsync/render"
	"github.com/ubgo/docsync/scan"
)

// Finding is one row of `check` output. Fields are the JSON contract (§26.2);
// the JSON tags are the wire names and must not change within json_format 1.
type Finding struct {
	State    State    `json:"state"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	// Where the finding attaches: the doc line of the reference, or the
	// directive line of a def-level finding. DocRepo names the repository
	// the doc lives in when it is not this one (a merged workspace ref).
	Doc      string `json:"doc"`
	DocRepo  string `json:"doc_repo,omitempty"`
	Line     int    `json:"line"`
	Sentence string `json:"sentence,omitempty"`
	// What it is about.
	ID      string        `json:"id,omitempty"`
	Repo    string        `json:"repo,omitempty"`
	File    string        `json:"file,omitempty"`
	Lines   [2]int        `json:"lines,omitempty"`
	Verb    string        `json:"verb,omitempty"`
	Carrier block.Carrier `json:"carrier,omitempty"`
	Owner   string        `json:"owner,omitempty"`
	// Change detail for unacked and translation-stale.
	Classes []block.Class `json:"class,omitempty"`
	// Baseline says what the current hash was compared against: an explicit
	// ack, or the hash recorded when the citation was first seen. It lets a
	// consumer tell "a reviewed statement went stale" from "nobody has
	// reviewed this yet" without parsing the message.
	Baseline Baseline `json:"baseline,omitempty"`
	Hash     HashPair `json:"hash"`
	Diff     string   `json:"diff,omitempty"`
	// Remedy is the concrete command or edit that clears the finding.
	Remedy Remedy `json:"remedy"`
}

// HashPair is the block hash the sentence was last acked against and the
// hash it has now (§26.2 `hash`). Both empty means the finding is not about
// a block.
type HashPair struct {
	Acked   string `json:"acked,omitempty"`
	Current string `json:"current,omitempty"`
}

// Remedy is what clears a finding. `unacked` offers the two branches the
// spec names (§26.2): ack if the sentence is still true, edit if not. Every
// other state has one Fix. An agent executes these verbatim; the texts are
// commands and file positions, never scanned content.
type Remedy struct {
	IfStillTrue string `json:"if_still_true,omitempty"`
	IfNot       string `json:"if_not,omitempty"`
	Fix         string `json:"fix,omitempty"`
}

// TestOutcome is what a published CI run recorded for a test id (§21).
type TestOutcome string

// The outcomes a CI run can publish for a test id: read from its JUnit
// report and stored in the workspace index's tests file, which refuses any
// other value.
const (
	TestPassed  TestOutcome = "passed"
	TestFailed  TestOutcome = "failed"
	TestSkipped TestOutcome = "skipped"
)

// URLResult is what a url checker hook reports. The two error shapes mean
// different things and are reported differently:
//   - Checked false, Err set: no HTTP answer came back (offline, DNS,
//     refused, timed out). Reported `unverifiable`; a hook must not cache it.
//   - Checked true, Err set: the request could not even be formed from href.
//     Reported `problem` at the directive.
type URLResult struct {
	Status  int
	Final   string // final URL after redirects
	Title   string
	Err     error
	Checked bool // false when the hook could not check (offline)
}

// HTTP status codes run from 100 to 599 (RFC 9110 §15); anything else in an
// expect= is a typo, not a status.
const (
	minHTTPStatus = 100
	maxHTTPStatus = 599
)

// ParseHTTPStatus reads an expect= value as an HTTP status code: three
// digits from 100 to 599, optionally quoted. ds:url's expect= takes only a
// status; ds:run's takes one as one of its modes, and the CLI uses this so
// both read the same grammar.
func ParseHTTPStatus(s string) (int, bool) {
	s = strings.Trim(s, `"'`)
	if len(s) != 3 {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < minHTTPStatus || n > maxHTTPStatus {
		return 0, false
	}
	return n, true
}

// ParseRunTimeout reads a ds:run timeout= value: a positive Go duration
// (`300ms`, `30s`, `2m`), the grammar run.timeout uses. The key was accepted
// and never applied (bug 86); a value that does not parse is now a
// `problem` at the directive rather than a silent fall back to run.timeout.
func ParseRunTimeout(s string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.Trim(s, `"'`))
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("timeout=%s must be a positive duration such as 30s", s)
	}
	return d, nil
}

// ResolveResult is what a secret resolver reports for one address (§12).
// A resolver returns existence and, where the provider can be read, a hash
// of the value; never the value. Checked is false when the provider could
// not be reached, which is reported as unverifiable.
type ResolveResult struct {
	Checked bool
	Exists  bool
	Hash    string
	Err     error
}

// LocalResult is what the Local hook reports for one `local=true` def.
// Present says the target file is readable here; Err, with Present, says
// the def's pick= found nothing in it. Neither carries the content: a local
// target is never hashed into the ledger, because the ledger is shared and
// the file is on one machine only.
type LocalResult struct {
	Present bool
	Err     error
}

// Resolver answers for one address at one provider.
type Resolver func(provider, addr string) ResolveResult

// Options tunes a run. Every hook is optional; absence degrades to an
// honest `unverifiable` or `skipped`.
type Options struct {
	MaxLines         int
	FuzzyThreshold   float64
	UnackedIsWarning bool
	Strict           bool
	// Wording holds an ack to the sentence it approved (SPEC §18): a cited
	// sentence rewritten since its ack, or acked under an older sentence
	// rule, is unacked. False is [check] sentence = "position".
	Wording    bool
	OldContent func(row ledger.Row) (string, bool)
	// BodyAt returns a block's normalized body by its content hash, from the
	// body store (§20.1). It is what OldContent cannot be: the acked hash of
	// a citation may be many commits behind the previous ledger, and for a
	// merged def there is no local ledger row at all, so a by-id lookup
	// keyed to the previous scan cannot answer it. Nil, or a miss, degrades
	// a drift to block.ClassUnknown rather than a guess.
	BodyAt          func(hash string) (string, bool)
	CommentPrefixes func(file string) []string
	// TestResults maps a test block id to its last published outcome; nil
	// means no run was published and assert= references check as ordinary.
	TestResults map[string]TestOutcome
	// CommitExists answers `at=` pins; nil means unverifiable.
	CommitExists func(sha string) bool
	// URLCheck answers `ds:url`; nil means unverifiable.
	URLCheck func(href string) URLResult
	// Resolve answers secret addresses under --resolve; nil skips it.
	Resolve Resolver
	// StoredHashes are truth hashes kept from an earlier run when
	// resolve.store_hash is on, keyed by id; a differing hash is `rotated`.
	StoredHashes map[string]string
	// HasRecords says a record source is configured for `ds:table`.
	HasRecords bool
	// RunEnabled says `ds:run` directives may execute; the checker still
	// never executes anything, it only stops reporting them as skipped.
	RunEnabled bool
	// Local answers whether a `local=true` def's target is on this machine
	// and its pick= finds something (§9.1, §12). Nil, or Present false, is
	// `unverifiable`, the state everywhere the file is absent; before this
	// hook it was the state even beside the file (bug 85).
	Local func(b block.Block) LocalResult
	// ExtraVerbs are registered plugin verbs, so they are not `unknown`.
	ExtraVerbs map[string]bool
	// Handlers evaluate plugin verbs (§9.9, §37.3 Verb). A verb with no
	// handler is recorded as ok; one with a handler gets its findings.
	Handlers map[string]VerbHandler
	// KnownKeys extends the per-verb key tables for plugin verbs, so their
	// keys are not `unknown`; RequiredKeys makes a missing key a problem.
	KnownKeys    map[string][]string
	RequiredKeys map[string][]string
	// Classify replaces the change classifier (§37.3 Classifier).
	Classify match.Classifier
	// KnownEnvs is [env] known. When it lists any, an env= on this repo's
	// own defs and citations that it does not list is reported `unknown`:
	// a misspelled environment otherwise reads as a missing definition, or
	// as a def nobody can ever select. Empty disables the test.
	KnownEnvs []string
	// Command is the name of the binary a finding's message and remedy
	// tell the reader to run ("pds ack …"). Empty means DefaultCommand. A
	// custom build named itself in help and still told its users to run a
	// `ds` they did not have.
	Command string
}

// VerbHandler is a plugin verb's check. Findings it returns get the doc,
// line, verb, carrier, and id of the reference filled in when empty, and the
// table severity applied, so a handler only says what it found.
type VerbHandler func(ref block.Reference) []Finding

// RemoteRef is a reference published by another repository in the
// workspace that points at this repository's blocks (§21). Its acked hash
// comes from that repo's published refs, since its ack log is not here.
type RemoteRef struct {
	Repo      string
	Ref       block.Reference
	AckedHash string
	// SeenHash is that repo's recorded first-seen baseline for the citation,
	// used when it has never been acked.
	SeenHash string
}

// Undocumented is an exported declaration under a policy.require_doc path
// that has no def (§23). The root computes these from the scan; check only
// turns them into findings.
type Undocumented struct {
	File   string
	Line   int
	Symbol string
}

// Input is everything a run needs.
type Input struct {
	Repo string
	Now  time.Time
	// Prefix is the directive prefix, needed to render repo-mode copies for
	// comparison; empty means the default.
	Prefix string
	// Env is the default environment for references that do not say.
	Env string
	// Defs and Refs are the current scan; Merged are blocks from other repos
	// in the workspace (resolved through sync), referable but not matched.
	Defs         []block.Block
	Merged       []block.Block
	Refs         []block.Reference
	MergedRefs   []RemoteRef
	Problems     []scan.Problem
	Undocumented []Undocumented
	// Removed maps ids that were published by repositories since removed
	// from the workspace to that repo (§21): a reference to one is broken
	// with a message that says so rather than "deleted".
	Removed map[string]string
	// PrevForeign is the committed snapshot's rows, which is what a merged
	// block's previous position was at the last `ds sync`. `moved` is a
	// scan-to-scan state and needs a previous row; for a foreign block the
	// citing repo's own ledger never had one, so until the snapshot existed
	// a block that moved upstream could not be reported at all.
	PrevForeign []ledger.Row
	// SnapshotOnly says Merged came from the committed foreign snapshot
	// rather than the live workspace index (§21 `check --frozen`). It
	// changes what an unresolvable id means: not "nothing defines this"
	// but "nothing here defines this, and the snapshot does not record
	// it", which are different problems with different remedies.
	SnapshotOnly bool
	// Unreadable are files the scan skipped because of their form (see
	// scan.SkipReason.Unreadable). One that held citations or defs at the
	// last scan is reported: its citations are not being checked, and a
	// check that passed without saying so would be a wrong "up to date".
	Unreadable []scan.Skip
	Pages      map[string]extract.Page
	// Prev is the previous ledger for this repo; PrevRefs its reverse index;
	// Acks the append-only log.
	Prev     ledger.Ledger
	PrevRefs ledger.Refs
	Acks     ledger.Acks
	Opts     Options
}

// Report is the result of Run.
type Report struct {
	Findings   []Finding
	Changes    []match.Change
	Summary    map[State]int
	BySeverity map[Severity]int
	ExitCode   int
	// TruthHashes are the hashes resolved for truth=true defs this run, for
	// the caller to store when resolve.store_hash is on.
	TruthHashes map[string]string
}

// RunResult is what executing one `ds:run` directive came to. The checker
// never executes anything, so the caller that did reports back through
// ApplyRuns, and the outcome becomes the directive's finding.
type RunResult struct {
	// Doc and Line locate the directive.
	Doc  string
	Line int
	// Command is what ran, as the directive gave it.
	Command string
	// Failed says the command exited non-zero, timed out, or did not
	// print what expect= asked for.
	Failed bool
	// Skipped, when not empty, says why the command was not run: an id
	// that is not runnable=true, a cmd= outside run.allow.
	Skipped string
}

// ApplyRuns turns the outcomes of executed `ds:run` directives into their
// findings and recounts the report. Before it, check said "run directive
// will execute where enabled" for every run and the caller only printed what
// happened, so a failed command made `check --run` exit 1 with no finding
// naming it and a summary of only passing findings (bug 29). A run that was
// not executed becomes `skipped` with the reason. Findings with no result
// are left alone.
func ApplyRuns(rep Report, runs []RunResult) Report {
	byPlace := map[string]RunResult{}
	for _, r := range runs {
		byPlace[fmt.Sprintf("%s:%d", r.Doc, r.Line)] = r
	}
	out := make([]Finding, len(rep.Findings))
	for i, f := range rep.Findings {
		r, ok := byPlace[fmt.Sprintf("%s:%d", f.Doc, f.Line)]
		if ok && f.Verb == extract.VerbRun && f.State == StateOK {
			switch {
			case r.Skipped != "":
				f.State, f.Message = StateSkipped, "run not executed: "+r.Skipped
			case r.Failed:
				f.State, f.Message = StateRunFailed, "run failed: "+r.Command
				f.Remedy.Fix = fmt.Sprintf(remedyRunFailed, r.Command, f.Doc, f.Line)
			default:
				f.Message = "run passed: " + r.Command
			}
			f.Severity = severityOf[f.State]
		}
		out[i] = f
	}
	rep.Findings = out
	rep.Summary, rep.BySeverity, rep.ExitCode = Tally(out)
	return rep
}

// Tally counts findings by state and by severity, and gives the exit code:
// 1 when any finding is an error. It is the one place a report is counted,
// so a report changed after Run counts exactly as Run would have.
func Tally(findings []Finding) (map[State]int, map[Severity]int, int) {
	states, sevs, exit := map[State]int{}, map[Severity]int{}, 0
	for _, f := range findings {
		states[f.State]++
		sevs[f.Severity]++
		if f.Severity == SeverityError {
			exit = 1
		}
	}
	return states, sevs, exit
}

// defsByID groups current and merged blocks by id; several per id are legal
// when they differ by env.
type defIndex map[string][]block.Block

func (d defIndex) forEnv(id, env string) (block.Block, bool) {
	return d.forEnvBranch(id, env, "")
}

// forEnvBranch also honours `branch=`: a merged def published from that
// branch wins; without a match the default-branch def is used (Part VII
// "Ids and branches").
func (d defIndex) forEnvBranch(id, env, branch string) (block.Block, bool) {
	return ResolveDef(d[id], env, branch)
}

// ResolveDef picks, from the defs sharing one id, the one a citation with the
// given env and branch resolves to: a def on that branch (or on none, for a
// citation naming no branch), then an exact env match; a citation with no
// env takes the first; one whose env no def names takes the def with no env.
//
// Exported so scan's snapshot resolves a citation exactly as check does.
// Recording every citation of an id against whichever def came last gave a
// dev citation the prod value's hash, and a fresh repo with per-env defs
// reported its citations changed before anything had changed.
func ResolveDef(cands []block.Block, env, branch string) (block.Block, bool) {
	if len(cands) == 0 {
		return block.Block{}, false
	}
	if branch != "" {
		var onBranch []block.Block
		for _, b := range cands {
			if b.Args[block.KeyBranch] == branch {
				onBranch = append(onBranch, b)
			}
		}
		if len(onBranch) > 0 {
			cands = onBranch
		}
	}
	if branch == "" {
		var defaults []block.Block
		for _, b := range cands {
			if b.Args[block.KeyBranch] == "" {
				defaults = append(defaults, b)
			}
		}
		if len(defaults) > 0 {
			cands = defaults
		}
	}
	for _, b := range cands {
		if b.Env() == env {
			return b, true
		}
	}
	if env == "" {
		return cands[0], true
	}
	for _, b := range cands {
		if b.Env() == "" {
			return b, true
		}
	}
	return block.Block{}, false
}

// Run performs the evaluation.
func Run(in Input) Report {
	if in.Opts.MaxLines <= 0 {
		in.Opts.MaxLines = DefaultMaxLines
	}
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	if in.Prefix == "" {
		in.Prefix = directive.DefaultPrefix
	}
	r := &runner{in: in, defs: defIndex{}, referenced: map[string]bool{}}
	for _, b := range in.Defs {
		r.defs[b.ID] = append(r.defs[b.ID], b)
	}
	for _, b := range in.Merged {
		r.defs[b.ID] = append(r.defs[b.ID], b)
	}
	r.changes = match.Compare(in.Prev.Rows, in.Defs, match.Options{FuzzyThreshold: in.Opts.FuzzyThreshold, OldContent: in.Opts.OldContent, CommentPrefixes: in.Opts.CommentPrefixes, Classify: in.Opts.Classify})
	// Changes are per def — an id in an environment — so a citation reads
	// the change of the def it resolved to, and a question about an id as a
	// whole reads all of them (changesOf).
	r.changeByKey = map[string]match.Change{}
	r.changesOfID = map[string][]match.Change{}
	for _, c := range r.changes {
		r.changeByKey[c.Key()] = c
		r.changesOfID[c.ID] = append(r.changesOfID[c.ID], c)
	}
	// Foreign blocks are matched separately and kept in their own table.
	// Merging them into r.changes would put another repository's history
	// into this one's "what changed since the last scan", which is what
	// `why`, `impact` and the JSON contract report. Only the `moved` state
	// is read from here: a hash difference is decided by the ack, which
	// needs no change table at all.
	r.foreignByKey = map[string]match.Change{}
	if len(in.PrevForeign) > 0 {
		for _, c := range match.Compare(in.PrevForeign, in.Merged, match.Options{FuzzyThreshold: in.Opts.FuzzyThreshold, CommentPrefixes: in.Opts.CommentPrefixes, Classify: in.Opts.Classify}) {
			r.foreignByKey[c.Key()] = c
		}
	}
	r.acks = in.Acks.Latest()
	r.prevAcked = map[ledger.AckKey]string{}
	r.prevSeen = map[ledger.AckKey]string{}
	r.prevAck = map[ledger.AckKey]ledger.Ack{}
	// This repo's citations are followed across moves before anything is
	// measured, by the same rule scan uses to write refs.tsv, so a line
	// inserted above a citation or a renamed doc cannot turn an unreviewed
	// change into "up to date" (ledger.Baselines).
	cur := make([]ledger.RefRow, len(in.Refs))
	for i, ref := range in.Refs {
		cur[i] = ledger.FromReference(in.Repo, ref)
	}
	current := func(x ledger.RefRow) string {
		def, ok := r.defs.forEnvBranch(x.ID, r.env(x.ToReference()), x.Args[block.KeyBranch])
		if !ok {
			return ""
		}
		return def.Hash
	}
	for i, b := range ledger.Baselines(in.PrevRefs.Rows, cur, r.acks, in.PrevRefs.Header.ScannedAt, current) {
		key := cur[i].Key()
		if b.Acked != "" {
			r.prevAcked[key] = b.Acked
		}
		if b.Seen != "" {
			r.prevSeen[key] = b.Seen
		}
		if b.Ack != nil {
			r.prevAck[key] = *b.Ack
		}
	}

	for _, p := range in.Problems {
		r.problem(p)
	}
	for _, ref := range in.Refs {
		r.reference(ref, "")
	}
	for _, m := range in.MergedRefs {
		key := ledger.AckKey{Repo: m.Repo, Doc: m.Ref.Pos.File, Line: m.Ref.Pos.Start, ID: m.Ref.ID, Env: m.Ref.Args[block.KeyEnv]}
		if m.AckedHash != "" {
			r.prevAcked[key] = m.AckedHash
		}
		if m.SeenHash != "" {
			r.prevSeen[key] = m.SeenHash
		}
		r.reference(m.Ref, m.Repo)
	}
	r.unscanned()
	r.chains()
	r.locals()
	r.defEnvs()
	r.pages()
	r.uncovered()
	for _, u := range in.Undocumented {
		r.emit(Finding{State: StateUndocumented, Doc: u.File, Line: u.Line, File: u.File, Lines: [2]int{u.Line, u.Line}, Message: fmt.Sprintf("%s is exported under a require_doc path and has no def", u.Symbol), Remedy: Remedy{Fix: fmt.Sprintf(remedyUndocumented, u.File, u.File, u.Symbol)}})
	}

	sort.SliceStable(r.out, func(i, j int) bool {
		a, b := r.out[i], r.out[j]
		if a.Doc != b.Doc {
			return a.Doc < b.Doc
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		// Two citations on one line are ordered by where their blocks are,
		// and only then by id: by id, the order followed the random
		// suffixes, so the same tree reported the same line differently in
		// every checkout that minted its own ids (bug 32).
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Lines[0] != b.Lines[0] {
			return a.Lines[0] < b.Lines[0]
		}
		return a.ID < b.ID
	})
	rep := Report{Findings: r.out, Changes: r.changes, TruthHashes: r.truthHashes}
	rep.Summary, rep.BySeverity, rep.ExitCode = Tally(rep.Findings)
	return rep
}

type runner struct {
	in           Input
	defs         defIndex
	changes      []match.Change
	changeByKey  map[string]match.Change
	changesOfID  map[string][]match.Change
	foreignByKey map[string]match.Change
	acks         map[ledger.AckKey]ledger.Ack
	prevAcked    map[ledger.AckKey]string
	prevSeen     map[ledger.AckKey]string
	// prevAck is the ack row behind prevAcked, where there is one, for the
	// wording check.
	prevAck     map[ledger.AckKey]ledger.Ack
	referenced  map[string]bool
	truthHashes map[string]string
	out         []Finding
}

// emit applies the severity table, the unacked downgrade, and strict mode.
func (r *runner) emit(f Finding) {
	if name := r.in.Opts.Command; name != "" {
		f.Message = CommandText(f.Message, name)
		f.Remedy.Fix = CommandText(f.Remedy.Fix, name)
		f.Remedy.IfStillTrue = CommandText(f.Remedy.IfStillTrue, name)
		f.Remedy.IfNot = CommandText(f.Remedy.IfNot, name)
	}
	f.Severity = severityOf[f.State]
	if f.State == StateUnacked && r.in.Opts.UnackedIsWarning {
		f.Severity = SeverityWarning
	}
	if r.in.Opts.Strict && f.Severity == SeverityWarning && !neverStrict[f.State] {
		f.Severity = SeverityError
	}
	if f.Repo == "" {
		f.Repo = r.in.Repo
	}
	r.out = append(r.out, f)
}

// problem maps scan and extractor problems to findings.
func (r *runner) problem(p scan.Problem) {
	f := Finding{Doc: p.Pos.File, Line: p.Pos.Start, Message: p.Err.Error()}
	switch {
	case errors.Is(p.Err, scan.ErrRemotePick), errors.Is(p.Err, scan.ErrRemoteMissing), errors.Is(p.Err, scan.ErrPick):
		f.State = StatePickFailed
		f.Remedy.Fix = fmt.Sprintf(remedyPick, p.Pos.File, p.Pos.Start)
	case errors.Is(p.Err, scan.ErrDuplicateID):
		f.State = StateProblem
		f.Remedy.Fix = fmt.Sprintf(remedyDuplicate, p.Pos.File, p.Pos.Start, p.Err)
	case errors.Is(p.Err, scan.ErrDefInGenerated):
		f.State = StateUnknown
		f.Remedy.Fix = fmt.Sprintf(remedyGenerated, r.in.Prefix, p.Pos.File)
	default:
		f.State = StateProblem
		f.Remedy.Fix = fmt.Sprintf(remedyProblem, p.Pos.File, p.Pos.Start, p.Err)
	}
	r.emit(f)
}

// reference evaluates one reference; docRepo is empty for this repo's own
// docs and the publishing repo for a merged one.
func (r *runner) reference(ref block.Reference, docRepo string) {
	base := Finding{Doc: ref.Pos.File, DocRepo: docRepo, Line: ref.Pos.Start, Sentence: ref.Sentence, ID: ref.ID, Verb: ref.Verb, Carrier: ref.Carrier}
	if !r.knownVerb(ref.Verb) {
		f := base
		f.State = StateUnknown
		f.Message = fmt.Sprintf("unknown verb %q", ref.Verb)
		f.Remedy.Fix = fmt.Sprintf(remedyUnknownVerb, ref.Verb, ref.Pos.File, ref.Pos.Start)
		r.emit(f)
		return
	}
	if unknown := r.unknownKeys(ref); len(unknown) > 0 {
		f := base
		f.State = StateUnknown
		f.Message = fmt.Sprintf("unknown key(s) %s on ds:%s", strings.Join(unknown, ", "), ref.Verb)
		f.Remedy.Fix = fmt.Sprintf(remedyUnknownKey, strings.Join(unknown, ", "), "ds:"+ref.Verb, ref.Pos.File, ref.Pos.Start)
		r.emit(f)
	}
	if e := ref.Args[block.KeyEnv]; docRepo == "" && !r.knownEnv(e) {
		f := base
		f.State = StateUnknown
		f.Message = fmt.Sprintf("env=%s is not in env.known", e)
		f.Remedy.Fix = fmt.Sprintf(remedyUnknownEnv, e, ref.Pos.File, ref.Pos.Start, strings.Join(r.in.Opts.KnownEnvs, ", "))
		r.emit(f)
	}
	if missing := r.missingKeys(ref); len(missing) > 0 {
		f := base
		f.State = StateProblem
		f.Message = fmt.Sprintf("ds:%s needs %s", ref.Verb, strings.Join(missing, ", "))
		f.Remedy.Fix = fmt.Sprintf(remedyProblem, ref.Pos.File, ref.Pos.Start, "missing "+strings.Join(missing, ", "))
		r.emit(f)
		return
	}
	switch ref.Verb {
	case extract.VerbBlock, extract.VerbCfg, extract.VerbChain:
		r.idReference(ref, base)
	case extract.VerbRun:
		if id := ref.ID; id != "" {
			r.referenced[id] = true
			if _, ok := r.defs.forEnv(id, r.env(ref)); !ok {
				r.broken(ref, base, id)
				return
			}
		}
		f := base
		if t := ref.Args[keyTimeout]; t != "" {
			if _, err := ParseRunTimeout(t); err != nil {
				f.State = StateProblem
				f.Message = err.Error()
				f.Remedy.Fix = fmt.Sprintf(remedyProblem, ref.Pos.File, ref.Pos.Start, err)
				r.emit(f)
				return
			}
		}
		if r.in.Opts.RunEnabled {
			f.State = StateOK
			f.Message = "run directive will execute where enabled"
		} else {
			f.State = StateSkipped
			f.Message = "run not executed"
			f.Remedy.Fix = fmt.Sprintf(remedyRun, r.in.Prefix)
		}
		r.emit(f)
	case extract.VerbClaim:
		r.claim(ref, base)
	case extract.VerbURL:
		r.url(ref, base)
	case extract.VerbTable:
		f := base
		if r.in.Opts.HasRecords {
			f.State = StateOK
			f.Message = "table renders from the configured record source"
		} else {
			f.State = StateUnverifiable
			f.Message = "no record source configured"
			f.Remedy.Fix = fmt.Sprintf(remedyTable, r.in.Prefix)
		}
		r.emit(f)
	default:
		// A plugin verb: its handler decides; without one it is recorded as
		// ok because the core has nothing to check.
		h, ok := r.in.Opts.Handlers[ref.Verb]
		if !ok {
			f := base
			f.State = StateOK
			f.Message = "handled by a registered verb"
			r.emit(f)
			return
		}
		if ref.ID != "" {
			r.referenced[ref.ID] = true
		}
		found := h(ref)
		if len(found) == 0 {
			f := base
			f.State = StateOK
			f.Message = "ok (" + ref.Verb + ")"
			r.emit(f)
			return
		}
		for _, f := range found {
			if f.Doc == "" {
				f.Doc, f.Line = ref.Pos.File, ref.Pos.Start
			}
			if f.Verb == "" {
				f.Verb, f.Carrier, f.ID, f.Sentence = ref.Verb, ref.Carrier, ref.ID, ref.Sentence
			}
			if f.State == "" {
				f.State = StateProblem
			}
			r.emit(f)
		}
	}
}

func (r *runner) knownVerb(v string) bool {
	for _, b := range extract.BuiltinVerbs {
		if b == v {
			return true
		}
	}
	return r.in.Opts.ExtraVerbs[v]
}

func (r *runner) unknownKeys(ref block.Reference) []string {
	known, ok := knownKeys[ref.Verb]
	extra := r.in.Opts.KnownKeys[ref.Verb]
	if !ok && extra == nil {
		return nil
	}
	var out []string
	for k := range ref.Args {
		if known[k] || contains(extra, k) || contains(r.in.Opts.RequiredKeys[ref.Verb], k) {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (r *runner) missingKeys(ref block.Reference) []string {
	var out []string
	for _, k := range r.in.Opts.RequiredKeys[ref.Verb] {
		if _, ok := ref.Args[k]; !ok {
			out = append(out, k)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (r *runner) env(ref block.Reference) string {
	if e := ref.Args[block.KeyEnv]; e != "" {
		return e
	}
	return r.in.Env
}

// idReference handles block, cfg, and chain.
func (r *runner) idReference(ref block.Reference, base Finding) {
	if ref.ID == "" {
		if ref.Verb == extract.VerbCfg && ref.Args[keyQuery] != "" {
			f := base
			f.State = StateUnverifiable
			// Its own remedy: it used to borrow ds:table's, which told the
			// reader to register a record source -- something no cfg query
			// reads (bug 56).
			f.Message = "cfg query= is not built yet"
			f.Remedy.Fix = fmt.Sprintf(remedyCfgQuery, r.in.Prefix)
			r.emit(f)
			return
		}
		f := base
		f.State = StateProblem
		f.Message = "ds:" + ref.Verb + " without id="
		f.Remedy.Fix = fmt.Sprintf(remedyProblem, ref.Pos.File, ref.Pos.Start, "missing id=")
		r.emit(f)
		return
	}
	r.referenced[ref.ID] = true
	env := r.env(ref)
	def, ok := r.defs.forEnvBranch(ref.ID, env, ref.Args[block.KeyBranch])
	if !ok {
		if len(r.defs[ref.ID]) > 0 {
			f := base
			f.State = StateBroken
			f.Message = fmt.Sprintf("%s is not defined for env=%s", ref.ID, env)
			f.Remedy.Fix = fmt.Sprintf(remedyEnvMissing, ref.ID, env)
			r.emit(f)
			return
		}
		r.broken(ref, base, ref.ID)
		return
	}
	base.File, base.Lines, base.Owner, base.Hash.Current = def.Pos.File, [2]int{def.Pos.Start, def.Pos.End}, def.Owner(), def.Hash
	if repo := def.Args[block.KeyRepo]; repo != "" {
		base.Repo = repo
	}

	// Lifecycle keys on the def.
	if d := def.Args[block.KeySunset]; d != "" {
		if t, err := time.Parse(DateLayout, d); err == nil && !r.in.Now.Before(t) {
			f := base
			f.State = StateSunset
			f.Message = fmt.Sprintf("%s reached sunset %s", def.ID, d)
			f.Remedy.Fix = fmt.Sprintf(remedySunset, def.ID, d, ref.Pos.File, ref.Pos.Start)
			r.emit(f)
			return
		}
	}
	// A deprecation badge stands in for `ok`; a real problem below still
	// reports on its own, so one reference never yields two rows unless
	// both say something different.
	badged := false
	if d := def.Args[block.KeyDeprecate]; d != "" {
		if t, err := time.Parse(DateLayout, d); err == nil && !r.in.Now.Before(t) {
			f := base
			f.State = StateDeprecated
			f.Message = fmt.Sprintf("%s deprecated since %s", def.ID, d)
			f.Remedy.Fix = fmt.Sprintf(remedyDeprecated, def.ID, d, ref.Pos.File, ref.Pos.Start)
			r.emit(f)
			badged = true
		}
	}
	if def.IsLocal() {
		f := base
		if res := r.local(def); res.Present && res.Err == nil {
			// Present and picked: the citation resolves on this machine.
			// The def's own row reports a failed pick, so a citation of it
			// stays unverifiable rather than repeating that finding.
			f.State = StateOK
			f.Message = fmt.Sprintf("%s is local=true and present on this machine", def.ID)
		} else {
			f.State = StateUnverifiable
			f.Message = fmt.Sprintf("%s is local=true; its content is not readable here", def.ID)
			f.Remedy.Fix = fmt.Sprintf(remedyLocal, def.ID)
		}
		r.emit(f)
		return
	}
	// Repo-mode copies (§9.2): the closer's hash says which block the copy
	// was rendered from; the text says whether a hand touched it.
	if ref.Region != nil && ref.Verb == extract.VerbBlock && ref.Carrier == block.CarrierBlock {
		if ref.Region.Hash != textnorm.Short(def.Hash) {
			f := base
			f.State = StateStale
			f.Message = fmt.Sprintf("copy rendered from %s, block is now %s", orMissing(ref.Region.Hash), textnorm.Short(def.Hash))
			f.Remedy = Remedy{Fix: fmt.Sprintf(remedyStale, ref.Pos.File, ref.Pos.Start, def.ID)}
			r.emit(f)
			return
		}
		filled := WithPublishedBody(def, r.in.Opts.BodyAt)
		expected, ok, _ := render.Fragment(filled, ref, r.in.Prefix, r.in.Opts.MaxLines)
		// Another repository's block whose body this run cannot read (not
		// published, or pruned) cannot say what refresh wrote, and calling
		// the copy tampered would be a guess.
		if filled.Content == "" && filled.Args[block.KeyRepo] != "" {
			ok = false
		}
		if ok && textnorm.NormalizeString(expected) != textnorm.NormalizeString(ref.Region.Text) && legacyCopy(filled, ref, r.in.Prefix, r.in.Opts.MaxLines) {
			// Written by a build whose links were relative to the
			// repository root (bug 64). Nobody edited it and its content
			// is current, so it is neither tampered nor an error: every
			// repo-mode page in a repository would otherwise fail check
			// at once on upgrade, over links that were always written
			// that way. The next `ds refresh` rewrites them relative to
			// the page.
			ok = false
		}
		if ok && textnorm.NormalizeString(expected) != textnorm.NormalizeString(ref.Region.Text) && codeStillInBlock(ref.Region.Text, filled.Content, r.in.Prefix) {
			// Only the caption's line range or a directive inside the
			// block differs: a def added to a member moves the block's
			// lines without changing its code or its hash. That is a copy
			// refresh would rewrite, not one a hand edited.
			f := base
			f.State = StateStale
			f.Message = "copy shows the block's old lines or directives; its code is current"
			f.Remedy = Remedy{Fix: fmt.Sprintf(remedyStale, ref.Pos.File, ref.Pos.Start, def.ID)}
			r.emit(f)
			return
		}
		if ok && textnorm.NormalizeString(expected) != textnorm.NormalizeString(ref.Region.Text) {
			f := base
			f.State = StateTampered
			f.Message = "copy differs from what refresh would write"
			f.Remedy = Remedy{Fix: fmt.Sprintf(remedyTampered, ref.Pos.File, ref.Pos.Start)}
			r.emit(f)
			return
		}
	}
	// Presentation checks.
	blockLen := def.Pos.End - def.Pos.Start + 1
	if def.Content != "" {
		blockLen = strings.Count(def.Content, "\n") + 1
	}
	if ref.Verb == extract.VerbCfg && blockLen > 1 {
		f := base
		f.State = StateRange
		f.Message = fmt.Sprintf("ds:cfg on a %d-line block", blockLen)
		f.Remedy.Fix = fmt.Sprintf(remedyCfgRange, blockLen, r.in.Prefix)
		r.emit(f)
		return
	}
	if ref.Verb == extract.VerbBlock {
		shown := blockLen
		if ls := ref.Args[keyLines]; ls != "" {
			a, b, err := linerange.Parse(ls)
			if err != nil || b > blockLen {
				f := base
				f.State = StateRange
				f.Message = fmt.Sprintf("lines=%s outside the block's %d lines", ls, blockLen)
				f.Remedy.Fix = fmt.Sprintf(remedyRange, ref.Pos.File, ref.Pos.Start, blockLen)
				r.emit(f)
				return
			}
			shown = b - a + 1
		}
		if ref.Carrier == block.CarrierBlock && shown > r.in.Opts.MaxLines {
			f := base
			f.State = StateTooLarge
			f.Message = fmt.Sprintf("rendering %d lines exceeds the cap of %d", shown, r.in.Opts.MaxLines)
			f.Remedy.Fix = remedyTooLarge
			r.emit(f)
			return
		}
		if sha := ref.Args[keyAt]; sha != "" {
			f := base
			switch {
			case r.in.Opts.CommitExists == nil:
				f.State = StateUnverifiable
				f.Message = fmt.Sprintf("at=%s not checked", sha)
				f.Remedy.Fix = fmt.Sprintf(remedyUnverifiableAt, sha)
			case !r.in.Opts.CommitExists(sha):
				f.State = StateBroken
				f.Message = fmt.Sprintf("commit %s does not exist", sha)
				f.Remedy.Fix = fmt.Sprintf(remedyBroken, sha, ref.Pos.File, ref.Pos.Start, r.in.Prefix)
			default:
				f.State = StateOK
				f.Message = "snapshot pinned"
			}
			r.emit(f)
			return
		}
		if ref.Args[keyAssert] == block.TrueValue && r.in.Opts.TestResults != nil {
			if outcome := r.in.Opts.TestResults[def.ID]; outcome != TestPassed {
				f := base
				f.State = StateAssertFailed
				f.Message = fmt.Sprintf("test %s: %s", def.ID, orMissing(string(outcome)))
				f.Remedy.Fix = fmt.Sprintf(remedyAssert, def.ID, ref.Pos.File, ref.Pos.Start)
				r.emit(f)
				return
			}
		}
	}
	// Coverage state (§16 pass 4).
	//
	// The question a citation asks is "has this block changed since this
	// sentence was acked". Only the ack can answer it. The scan-to-scan
	// change table answers a different question — "what changed since the
	// last scan" — and the two diverge whenever the ack is not from the
	// previous scan. Gating on the change table therefore lost findings in
	// two ways: `ds scan` rewrites the ledger, so prev == current, no Change
	// exists, and an open `unacked` silently cleared; and merged defs from
	// other repositories are never matched at all, so no cross-repo citation
	// was ever flagged. The ack is authoritative here and the change table
	// only enriches, supplying `moved` and an exact class list and diff when
	// it happens to span the same two hashes.
	c, hasChange := r.changeByKey[match.BlockKey(def)]
	changedLocally := hasChange && c.State == match.StateChanged
	key := r.ackKey(ref, def, base.DocRepo)
	acked := r.ackedHash(key)
	// baseline is the hash this sentence is measured against: the ack when
	// there is one, and otherwise the hash recorded the first time the
	// citation was seen. A citation nobody has acked still has a baseline,
	// which is why an unreviewed change to it is not silence.
	baseline, fromAck := acked, acked != ""
	if !fromAck {
		baseline = r.prevSeen[key]
	}
	covered := baseline != "" && baseline == def.Hash

	var classes []block.Class
	var diff string
	drifted := false
	switch {
	case covered:
		// The block still has the hash this sentence was measured against.
	case baseline != "":
		// Measured against some other hash, so the sentence is uncovered
		// however the block got here. Describe it from the change table when
		// that table spans exactly this pair, and from the body store
		// otherwise.
		drifted = true
		if changedLocally && c.Old.Hash == baseline {
			classes, diff = c.Classes, c.Diff
		} else {
			classes, diff = r.classifyDrift(baseline, def)
		}
	case changedLocally:
		// No baseline at all — a citation first recorded by a tool too old
		// to write one. The previous scan is then the only thing to compare
		// against, which is what has always been reported here.
		drifted = true
		classes, diff = c.Classes, c.Diff
	}
	if !block.Flags(def.Stability(), classes) {
		drifted = false
	}
	if !drifted {
		if badged {
			return
		}
		if why, diff := r.wording(key, ref); why != "" {
			f := base
			f.State, f.Message, f.Diff, f.Hash.Acked, f.Baseline = StateUnacked, why, diff, acked, BaselineAck
			f.Remedy = Remedy{IfStillTrue: fmt.Sprintf(remedyAck, def.ID, ref.Pos.File, ref.Pos.Start), IfNot: fmt.Sprintf(remedyEdit, ref.Pos.File, ref.Pos.Start)}
			r.emit(f)
			return
		}
		f := base
		switch {
		case covered && changedLocally && c.Flags:
			f.Classes, f.Diff, f.Hash.Acked = c.Classes, c.Diff, acked
			f.State = StateOK
			f.Message = "acked at current hash"
		case hasChange && c.State == match.StateMoved:
			f.State = StateMoved
			f.Message = fmt.Sprintf("moved from %s:%d-%d", c.Old.File, c.Old.Start, c.Old.End)
		case !hasChange && r.movedForeign(def):
			m := r.foreignByKey[match.BlockKey(def)]
			// "since" is the last sync, not the last commit: the snapshot
			// is what moved against, and only sync rewrites it.
			f.State = StateMoved
			f.Message = fmt.Sprintf("moved from %s:%d-%d in %s since the last sync", m.Old.File, m.Old.Start, m.Old.End, orMissing(m.Old.Repo))
		default:
			f.State = StateOK
			f.Message = "up to date"
		}
		r.emit(f)
		return
	}
	f := base
	f.Classes, f.Diff, f.Hash.Acked = classes, diff, acked
	f.Baseline = BaselineSeen
	if fromAck {
		f.Baseline = BaselineAck
	}
	if ref.Args[keyTranslates] == block.TrueValue {
		f.State = StateTranslationStale
		f.Message = fmt.Sprintf("source %s changed (%s)", def.ID, classList(classes))
		f.Remedy.Fix = fmt.Sprintf(remedyTranslation, def.ID, ref.Pos.File, ref.Pos.Start)
		r.emit(f)
		return
	}
	f.State = StateUnacked
	since := sinceAcked
	if !fromAck {
		since = sinceFirstCited
	}
	f.Message = fmt.Sprintf("%s changed (%s) %s", def.ID, classList(classes), since)
	f.Remedy = Remedy{IfStillTrue: fmt.Sprintf(remedyAck, def.ID, ref.Pos.File, ref.Pos.Start), IfNot: fmt.Sprintf(remedyEdit, ref.Pos.File, ref.Pos.Start)}
	r.emit(f)
}

// wording says why a citation whose block is unchanged is still unacked
// because of its sentence (SPEC §18), with the old and new wording as a
// diff, or "" when its ack still holds. Only a citation that has a sentence
// and an ack row carrying one is measured: a block-position citation has no
// wording, and a baseline that is only a hash in refs.tsv has no sentence
// to compare.
//
// The sentence comparison comes BEFORE the rule. A rule decides which text a
// citation is bound to; when the ack's recorded sentence hashes the same as
// the current one, both rules chose the very same text, so the ack approved
// exactly what is there and holds whatever number it was stamped with. Testing
// the rule first asked a person to re-read sentences that had not changed by a
// byte: renumbering the rule for the first release turned 100 of them in the
// pilot into findings after one routine scan (bug 17). Only when the text differs
// AND the rule differs is it undecidable whether the wording changed or only
// the boundary moved, and that is reported once as recorded under another
// rule, never as a rewrite.
func (r *runner) wording(key ledger.AckKey, ref block.Reference) (string, string) {
	a, ok := r.prevAck[key]
	if !r.in.Opts.Wording || !ok || ref.SentenceHash == "" {
		return "", ""
	}
	if a.SentenceHash != "" && a.SentenceHash == ref.SentenceHash {
		return "", ""
	}
	if a.Rule != extract.Rule {
		from := strconv.Itoa(a.Rule)
		if a.Rule == 0 {
			from = "unrecorded"
		}
		return fmt.Sprintf(msgAckOtherRule, from, extract.Rule), ""
	}
	if a.SentenceHash == "" || a.SentenceHash == ref.SentenceHash {
		return "", ""
	}
	return msgSentenceRewritten, "-" + a.Sentence + "\n+" + ref.Sentence + "\n"
}

// ackKey identifies one sentence's ack. docRepo is non-empty only for a
// reference published by another repository, whose acks live in that repo.
//
// ackedHash is the hash a sentence was last acked against: the append-only
// log first, then the acked hash carried in the previous refs so a fresh
// checkout can check without the log (§15).
func (r *runner) ackKey(ref block.Reference, def block.Block, docRepo string) ledger.AckKey {
	repo := r.in.Repo
	if docRepo != "" {
		repo = docRepo
	}
	return ledger.AckKey{Repo: repo, Doc: ref.Pos.File, Line: ref.Pos.Start, ID: def.ID, Env: ref.Args[block.KeyEnv]}
}

func (r *runner) ackedHash(key ledger.AckKey) string {
	// Every citation's acked hash is settled before any finding is made: a
	// local one by ledger.Baselines, ack log included, and a foreign one by
	// what its own repository published. Consulting this repository's ack
	// log again here would let a moved citation pick up an ack that belonged
	// to whatever used to sit on its line — the case Baselines exists to
	// refuse.
	return r.prevAcked[key]
}

// classifyDrift describes the difference between the acked version of a
// block and the current one when the change table cannot: across several
// scans, or across repositories. It needs the acked body, which only the
// body store can supply, because the acked hash may be many commits behind
// the previous ledger that OldContent answers for. Without that body it
// reports ClassUnknown rather than guessing ClassBody, so an `api` block
// whose signature moved is never silently passed (see block.Flags).
func (r *runner) classifyDrift(acked string, def block.Block) ([]block.Class, string) {
	oldBody, ok := r.bodyAt(acked)
	if !ok {
		return []block.Class{block.ClassUnknown}, ""
	}
	// A merged def carries a hash but no body: the ledger a workspace
	// publishes stores hashes, not text. Its current body comes from the
	// same store, written by the defining repo's publish.
	nw := def
	if nw.Content == "" {
		if body, ok := r.bodyAt(def.Hash); ok {
			nw.Content = body
		} else {
			return []block.Class{block.ClassUnknown}, ""
		}
	}
	old := nw
	old.Hash, old.Content = acked, oldBody
	classes := match.Classify(old, nw, oldBody, r.commentPrefixes(nw.Pos.File))
	return classes, difflib.Unified(oldBody, nw.Content, 0)
}

// bodyAt reads one body from the store, or reports that it is unavailable.
func (r *runner) bodyAt(hash string) (string, bool) {
	if r.in.Opts.BodyAt == nil || hash == "" {
		return "", false
	}
	return r.in.Opts.BodyAt(hash)
}

// commentPrefixes is the classifier's line-comment grammar for a file, or
// nil when the caller supplied none.
func (r *runner) commentPrefixes(file string) []string {
	if r.in.Opts.CommentPrefixes == nil {
		return nil
	}
	return r.in.Opts.CommentPrefixes(file)
}

// movedForeign reports whether a block defined in another repository sits at
// a new position with the same content since the last sync.
func (r *runner) movedForeign(def block.Block) bool {
	c, ok := r.foreignByKey[match.BlockKey(def)]
	return ok && c.State == match.StateMoved
}

// vanished is what became of an id no current def carries: the change of
// the first of its environments, in Compare's order, that says it moved
// without its def or was rewritten, and otherwise its first change. The
// order is fixed, so the message does not depend on map iteration.
func (r *runner) vanished(id string) match.Change {
	all := r.changesOfID[id]
	for _, c := range all {
		if c.State == match.StateMovedUnmarked || c.State == match.StateRewritten {
			return c
		}
	}
	if len(all) > 0 {
		return all[0]
	}
	return match.Change{}
}

// legacyCopy reports that a repo-mode copy is exactly what a build before
// page-relative links (bug 64) wrote: the fragment rendered as if the page
// sat at the repository root, where the two forms agree.
func legacyCopy(def block.Block, ref block.Reference, prefix string, maxLines int) bool {
	ref.Pos.File = ""
	legacy, ok, _ := render.Fragment(def, ref, prefix, maxLines)
	return ok && textnorm.NormalizeString(legacy) == textnorm.NormalizeString(ref.Region.Text)
}

// WithPublishedBody returns b with its content filled from the body store
// when b is another repository's block (block.KeyRepo set) and arrived
// without one, as every merged def does: the index carries ledger rows, and
// bodies separately by hash. A copy or a render of such a block was its
// title and a link only (bug 105). A secret or local block publishes no
// body, so it stays empty, and so does any miss.
func WithPublishedBody(b block.Block, bodyAt func(hash string) (string, bool)) block.Block {
	if b.Content != "" || b.Args[block.KeyRepo] == "" || bodyAt == nil {
		return b
	}
	if body, ok := bodyAt(b.Hash); ok {
		b.Content = body
	}
	return b
}

func orMissing(s string) string {
	if s == "" {
		return "no result recorded"
	}
	return s
}

func classList(cs []block.Class) string {
	if len(cs) == 0 {
		return "body"
	}
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = string(c)
	}
	return strings.Join(parts, ", ")
}

// broken reports a reference to an undefined id, using the change table to
// say whether the block moved without its def or was probably rewritten.
func (r *runner) broken(ref block.Reference, base Finding, id string) {
	f := base
	f.State = StateBroken
	f.ID = id
	if repo, removed := r.in.Removed[id]; removed {
		f.Message = fmt.Sprintf("%s was published by %s, which was removed from the workspace", id, repo)
		f.Remedy = Remedy{Fix: fmt.Sprintf(remedyRepoRemoved, repo, ref.Pos.File, ref.Pos.Start)}
		r.emit(f)
		return
	}
	switch c := r.vanished(id); c.State {
	case match.StateMovedUnmarked:
		f.Message = fmt.Sprintf("%s lost its ds:def; identical content is at %s:%d", id, c.Candidate.Pos.File, c.Candidate.Pos.Start)
		f.Remedy.Fix = fmt.Sprintf(remedyBrokenMoved, r.in.Prefix, id, c.Candidate.Pos.File, c.Candidate.Pos.Start)
	case match.StateRewritten:
		f.Message = fmt.Sprintf("%s was deleted; %s is %.0f%% similar", id, c.Candidate.ID, c.Ratio*100)
		f.Remedy.Fix = fmt.Sprintf(remedyBrokenRewrite, c.Candidate.ID, c.Ratio*100, c.Candidate.Pos.File, c.Candidate.Pos.Start, id, r.in.Prefix)
	case match.StateDeleted:
		f.Message = fmt.Sprintf("%s was deleted (last seen %s:%d)", id, c.Old.File, c.Old.Start)
		f.Remedy.Fix = fmt.Sprintf(remedyBroken, id, ref.Pos.File, ref.Pos.Start, r.in.Prefix)
	default:
		// Nothing here has ever defined this id. What that means depends on
		// where foreign blocks came from: with the live index it is
		// genuinely undefined, but a frozen run only consulted the
		// committed snapshot, so the id may be published by another repo
		// and simply not recorded yet.
		f.Message = fmt.Sprintf("%s is not defined", id)
		f.Remedy.Fix = fmt.Sprintf(remedyBroken, id, ref.Pos.File, ref.Pos.Start, r.in.Prefix)
		if r.in.SnapshotOnly {
			f.Message = fmt.Sprintf("%s is cited but is not recorded in %s", id, ledger.ForeignFile)
			f.Remedy.Fix = fmt.Sprintf(remedyUnrecorded, ledger.ForeignFile, id, ref.Pos.File, ref.Pos.Start)
		}
		if sug := r.suggest(id); sug != "" {
			f.Message += "; did you mean " + sug
		}
	}
	r.emit(f)
}

// suggest finds a defined id that shares the suffix (a relabelled prefix) or
// is within two edits of the typo.
func (r *runner) suggest(id string) string {
	suffix := id[strings.LastIndex(id, "-")+1:]
	best, bestDist := "", 3
	for cand := range r.defs {
		if strings.HasSuffix(cand, "-"+suffix) && suffix != "" {
			return cand
		}
		if d := editDistance(id, cand); d < bestDist {
			best, bestDist = cand, d
		}
	}
	return best
}

// editDistance is Levenshtein with early exit past 2, enough for typos.
func editDistance(a, b string) int {
	if abs(len(a)-len(b)) > 2 {
		return 3
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			rowMin = min(rowMin, cur[j])
		}
		if rowMin > 2 {
			return 3
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// claim evaluates reviewed/expires and about.
func (r *runner) claim(ref block.Reference, base Finding) {
	reviewed, expires := ref.Args[keyReviewed], ref.Args[keyExpires]
	if reviewed == "" || expires == "" {
		f := base
		f.State = StateUnknown
		f.Message = "claim without reviewed= and expires="
		f.Remedy.Fix = fmt.Sprintf(remedyClaimNoDate, ref.Pos.File, ref.Pos.Start)
		r.emit(f)
		return
	}
	t, err := time.Parse(DateLayout, reviewed)
	d, derr := ParseDuration(expires)
	if err != nil || derr != nil {
		f := base
		f.State = StateProblem
		f.Message = fmt.Sprintf("claim has bad reviewed=%q or expires=%q", reviewed, expires)
		f.Remedy.Fix = fmt.Sprintf(remedyClaimNoDate, ref.Pos.File, ref.Pos.Start)
		r.emit(f)
		return
	}
	// The latest ack of this claim renews the review date, and, while what
	// the claim is about still hashes as it did then, accepts its changes.
	renewal, renewed := r.claimRenewal(ref)
	if renewed && renewal.At.After(t) {
		t = renewal.At
	}
	about := ClaimAbout(ref)
	reviewedAbout := renewed && renewal.BlockHash != "" && renewal.BlockHash == AboutHash(about, r.in.Defs, r.in.Merged)
	f := base
	if r.in.Now.After(t.Add(d)) {
		f.State = StateExpired
		f.Message = fmt.Sprintf("claim reviewed %s expired after %s", t.Format(DateLayout), expires)
		f.Remedy.Fix = fmt.Sprintf(remedyExpired, ref.Pos.File, ref.Pos.Start, ref.Pos.File, ref.Pos.Start)
		r.emit(f)
		return
	}
	for _, id := range about {
		r.referenced[id] = true
		// A claim about an id is about every environment of it.
		for _, c := range r.changesOfID[id] {
			if c.State == match.StateChanged && c.Flags && !reviewedAbout {
				f.State = StateExpired
				f.ID = id
				f.Classes = c.Classes
				f.Message = fmt.Sprintf("claim is about %s, which changed (%s)", id, classList(c.Classes))
				f.Remedy.Fix = fmt.Sprintf(remedyExpired, ref.Pos.File, ref.Pos.Start, ref.Pos.File, ref.Pos.Start)
				r.emit(f)
				return
			}
		}
	}
	f.State = StateOK
	f.Message = fmt.Sprintf("claim valid until %s", t.Add(d).Format(DateLayout))
	r.emit(f)
}

// url evaluates a ds:url through the hook, or reports unverifiable.
func (r *runner) url(ref block.Reference, base Finding) {
	href := ref.Args[keyHref]
	f := base
	if href == "" {
		f.State = StateProblem
		f.Message = "ds:url without href="
		f.Remedy.Fix = fmt.Sprintf(remedyProblem, ref.Pos.File, ref.Pos.Start, "missing href=")
		r.emit(f)
		return
	}
	want := 0
	if e := ref.Args[keyExpect]; e != "" {
		// expect= was accepted and never read, so a link meant to answer 410
		// or 204 could not say so (bug 87). A value that is not a status is
		// a problem now, not a silent fallback.
		status, ok := ParseHTTPStatus(e)
		if !ok {
			f.State = StateProblem
			f.Message = fmt.Sprintf("ds:url expect=%s is not an HTTP status", e)
			f.Remedy.Fix = fmt.Sprintf(remedyProblem, ref.Pos.File, ref.Pos.Start, "expect= must be a status code from 100 to 599")
			r.emit(f)
			return
		}
		want = status
	}
	if r.in.Opts.URLCheck == nil {
		f.State = StateUnverifiable
		f.Message = "external link not checked"
		f.Remedy.Fix = remedyURL
		r.emit(f)
		return
	}
	res := r.in.Opts.URLCheck(href)
	switch {
	case !res.Checked:
		// The request never got an answer: offline, DNS, refused, timed
		// out. That says nothing about the link, so it is not `dead`; a
		// network failure used to be reported as "returned 0" (bug 87).
		f.State = StateUnverifiable
		f.Message = "external link not checked"
		f.Remedy.Fix = remedyURL
		if res.Err != nil {
			f.Message += ": " + res.Err.Error()
			f.Remedy.Fix = fmt.Sprintf(remedyURLUnreachable, href)
		}
	case res.Err != nil:
		// Checked with an error is a request that could not be formed:
		// the href itself is wrong, which no network will fix.
		f.State = StateProblem
		f.Message = fmt.Sprintf("%s cannot be requested: %v", href, res.Err)
		f.Remedy.Fix = fmt.Sprintf(remedyProblem, ref.Pos.File, ref.Pos.Start, res.Err)
	case want != 0 && res.Status != want:
		f.State = StateDead
		f.Message = fmt.Sprintf("%s returned %d, expected %d", href, res.Status, want)
		f.Remedy.Fix = fmt.Sprintf(remedyURLExpect, href, res.Status, want, ref.Pos.File, ref.Pos.Start)
	case want == 0 && res.Status >= 400:
		f.State = StateDead
		f.Message = fmt.Sprintf("%s returned %d", href, res.Status)
		f.Remedy.Fix = fmt.Sprintf(remedyURLDead, href, res.Status, ref.Pos.File, ref.Pos.Start)
	case res.Final != "" && res.Final != href:
		f.State = StateURLMoved
		f.Message = fmt.Sprintf("%s redirects to %s", href, res.Final)
		f.Remedy.Fix = fmt.Sprintf(remedyURLMoved, href, res.Final, ref.Pos.File, ref.Pos.Start)
	case ref.Args[keyTitle] != "" && !strings.Contains(res.Title, ref.Args[keyTitle]):
		f.State = StateRetitled
		f.Message = fmt.Sprintf("title is now %q", res.Title)
		f.Remedy.Fix = fmt.Sprintf(remedyURLRetitled, href, ref.Args[keyTitle])
	default:
		f.State = StateOK
		f.Message = "link alive"
	}
	r.emit(f)
}

// chains validates from=/truth= structure over all defs (§12).
func (r *runner) chains() {
	byID := map[string]block.Block{}
	for id, bs := range r.defs {
		byID[id] = bs[0]
	}
	// Union chains by following from= to the root.
	rootOf := map[string]string{}
	var visit func(id string, seen map[string]bool) (string, bool)
	visit = func(id string, seen map[string]bool) (string, bool) {
		if root, ok := rootOf[id]; ok {
			return root, true
		}
		if seen[id] {
			return "", false
		}
		seen[id] = true
		b := byID[id]
		from := b.From()
		if from == "" {
			rootOf[id] = id
			return id, true
		}
		if _, ok := byID[from]; !ok {
			r.emit(Finding{State: StateChainBroken, Doc: b.DirectivePos.File, Line: b.DirectivePos.Start, ID: id, File: b.Pos.File, Message: fmt.Sprintf("from=%s is not defined", from), Remedy: Remedy{Fix: fmt.Sprintf(remedyChainMissing, id, from)}})
			rootOf[id] = id
			return id, true
		}
		root, ok := visit(from, seen)
		if !ok {
			return "", false
		}
		r.referenced[from] = true
		rootOf[id] = root
		return root, true
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	members := map[string][]string{}
	for _, id := range ids {
		root, ok := visit(id, map[string]bool{})
		if !ok {
			b := byID[id]
			r.emit(Finding{State: StateChainBroken, Doc: b.DirectivePos.File, Line: b.DirectivePos.Start, ID: id, File: b.Pos.File, Message: "from= cycle", Remedy: Remedy{Fix: fmt.Sprintf(remedyChainCycle, id)}})
			continue
		}
		members[root] = append(members[root], id)
	}
	for _, root := range sortedKeys(members) {
		ms := members[root]
		truths := 0
		for _, id := range ms {
			if byID[id].IsTruth() {
				truths++
			}
		}
		rb := byID[root]
		r.resolveChain(byID, ms, root, truths == 1)
		switch {
		case len(ms) == 1 && truths == 0 && rb.IsSecret():
			r.emit(Finding{State: StateUnsourced, Doc: rb.DirectivePos.File, Line: rb.DirectivePos.Start, ID: root, File: rb.Pos.File, Owner: rb.Owner(), Message: "secret with no declared source", Remedy: Remedy{Fix: fmt.Sprintf(remedyUnsourced, root)}})
		case len(ms) > 1 && truths != 1:
			r.emit(Finding{State: StateChainBroken, Doc: rb.DirectivePos.File, Line: rb.DirectivePos.Start, ID: root, File: rb.Pos.File, Message: fmt.Sprintf("chain of %d has %d truth defs", len(ms), truths), Remedy: Remedy{Fix: fmt.Sprintf(remedyChainTruth, root, truths)}})
		}
	}
}

// resolveChain asks the resolver about every secret hop of one chain (§12):
// a missing address is `resolve failed`; a copy whose hash differs from its
// truth is `out of sync`; a truth whose hash differs from the stored one is
// `rotated`. Providers that only list names (GitHub) return no hash and are
// existence-only.
func (r *runner) resolveChain(byID map[string]block.Block, members []string, root string, oneTruth bool) {
	if r.in.Opts.Resolve == nil {
		return
	}
	hashes := map[string]string{}
	for _, id := range members {
		b := byID[id]
		if !b.IsSecret() {
			continue
		}
		addr := strings.TrimSpace(b.Content)
		if addr == "" || strings.Contains(addr, "\n") {
			continue
		}
		provider := render.Provider(addr, b.Args[block.KeySource])
		if provider == "" {
			continue
		}
		res := r.in.Opts.Resolve(provider, addr)
		switch {
		case !res.Checked || res.Err != nil:
			msg := "provider " + provider + " not reachable"
			if res.Err != nil {
				msg += ": " + res.Err.Error()
			}
			r.emit(Finding{State: StateUnverifiable, Doc: b.DirectivePos.File, Line: b.DirectivePos.Start, ID: id, File: b.Pos.File, Message: msg, Remedy: Remedy{Fix: fmt.Sprintf(remedyResolveUnverifiable, provider)}})
		case !res.Exists:
			r.emit(Finding{State: StateResolveFailed, Doc: b.DirectivePos.File, Line: b.DirectivePos.Start, ID: id, File: b.Pos.File, Owner: b.Owner(), Message: fmt.Sprintf("%s does not exist at %s", addr, provider), Remedy: Remedy{Fix: fmt.Sprintf(remedyResolveFailed, addr, provider, b.DirectivePos.File, b.DirectivePos.Start)}})
		case res.Hash != "":
			hashes[id] = res.Hash
			if b.IsTruth() {
				if r.truthHashes == nil {
					r.truthHashes = map[string]string{}
				}
				r.truthHashes[id] = res.Hash
				if old, ok := r.in.Opts.StoredHashes[id]; ok && old != res.Hash {
					r.emit(Finding{State: StateRotated, Doc: b.DirectivePos.File, Line: b.DirectivePos.Start, ID: id, File: b.Pos.File, Owner: b.Owner(), Message: id + " was rotated since its hash was stored", Hash: HashPair{Acked: old, Current: res.Hash}, Remedy: Remedy{Fix: fmt.Sprintf(remedyRotated, id)}})
				}
			}
		}
	}
	if !oneTruth {
		return
	}
	truthHash, truthID := "", ""
	for _, id := range members {
		if byID[id].IsTruth() {
			truthHash, truthID = hashes[id], id
		}
	}
	if truthHash == "" {
		return
	}
	// The truth's hash from before a rotation, when this run saw one.
	previous := ""
	if old, ok := r.in.Opts.StoredHashes[truthID]; ok && old != truthHash {
		previous = old
	}
	for _, id := range members {
		b := byID[id]
		h, ok := hashes[id]
		if b.IsTruth() || !ok || h == truthHash {
			continue
		}
		sync := b.Args[block.KeySync]
		if sync == "" {
			sync = remedyNoSync
		}
		if previous != "" && h == previous {
			// A copy still holding the pre-rotation value: the sync has
			// not run (§12). The stored hash is left as it was, so the
			// truth keeps reading `rotated` and this copy `stale copy`
			// until the sync catches up; advancing it here would turn the
			// copy into a plain `out of sync` on the next run and lose the
			// cause (bug 84).
			delete(r.truthHashes, truthID)
			r.emit(Finding{State: StateStaleCopy, Doc: b.DirectivePos.File, Line: b.DirectivePos.Start, ID: id, File: b.Pos.File, Owner: b.Owner(), Message: fmt.Sprintf("%s still holds the value from before %s was rotated", id, truthID), Hash: HashPair{Acked: truthHash, Current: h}, Remedy: Remedy{Fix: fmt.Sprintf(remedyStaleCopy, id, truthID, sync)}})
			continue
		}
		r.emit(Finding{State: StateOutOfSync, Doc: b.DirectivePos.File, Line: b.DirectivePos.Start, ID: id, File: b.Pos.File, Owner: b.Owner(), Message: fmt.Sprintf("%s differs from truth %s", id, root), Hash: HashPair{Acked: truthHash, Current: h}, Remedy: Remedy{Fix: fmt.Sprintf(remedyOutOfSync, id, root, sync)}})
	}
}

// knownEnv applies Options.KnownEnvs to one env= value; no value, or no
// list, is always known.
func (r *runner) knownEnv(e string) bool {
	return e == "" || len(r.in.Opts.KnownEnvs) == 0 || contains(r.in.Opts.KnownEnvs, e)
}

// defEnvs reports this repo's defs whose env= is not in env.known; such a
// def can never be selected by a citation that names a listed environment.
func (r *runner) defEnvs() {
	for _, b := range r.in.Defs {
		if e := b.Args[block.KeyEnv]; !r.knownEnv(e) {
			r.emit(Finding{State: StateUnknown, Doc: b.DirectivePos.File, Line: b.DirectivePos.Start, ID: b.ID, File: b.Pos.File, Message: fmt.Sprintf("env=%s is not in env.known", e), Remedy: Remedy{Fix: fmt.Sprintf(remedyUnknownEnv, e, b.DirectivePos.File, b.DirectivePos.Start, strings.Join(r.in.Opts.KnownEnvs, ", "))}})
		}
	}
}

// locals reports local=true defs, which a scan can never verify.
func (r *runner) locals() {
	for _, b := range r.in.Defs {
		if !b.IsLocal() {
			continue
		}
		f := Finding{Doc: b.DirectivePos.File, Line: b.DirectivePos.Start, ID: b.ID, File: b.Pos.File}
		switch res := r.local(b); {
		case !res.Present:
			f.State, f.Message, f.Remedy.Fix = StateUnverifiable, "local=true def is only readable on its own machine", fmt.Sprintf(remedyLocal, b.ID)
		case res.Err != nil:
			f.State, f.Message, f.Remedy.Fix = StatePickFailed, fmt.Sprintf("%s is present here but %v", b.Pos.File, res.Err), fmt.Sprintf(remedyPick, b.DirectivePos.File, b.DirectivePos.Start)
		default:
			f.State, f.Message = StateOK, fmt.Sprintf("local=true target %s is present on this machine", b.Pos.File)
		}
		r.emit(f)
	}
}

// local asks the Local hook about a local=true def; no hook is absent.
func (r *runner) local(b block.Block) LocalResult {
	if r.in.Opts.Local == nil {
		return LocalResult{}
	}
	return r.in.Opts.Local(b)
}

// pages evaluates covers and review_every per page.
func (r *runner) pages() {
	for _, doc := range sortedPageKeys(r.in.Pages) {
		pg := r.in.Pages[doc]
		for _, id := range pg.Covers {
			r.referenced[id] = true
			if _, ok := r.defs[id]; !ok {
				r.emit(Finding{State: StateOrphan, Doc: doc, Line: 1, ID: id, Message: fmt.Sprintf("covers %s, which is not defined", id), Remedy: Remedy{Fix: fmt.Sprintf(remedyOrphan, doc, id)}})
			}
		}
		if pg.ReviewEvery == "" {
			continue
		}
		d, err := ParseDuration(pg.ReviewEvery)
		if err != nil {
			r.emit(Finding{State: StateProblem, Doc: doc, Line: 1, Message: fmt.Sprintf("review_every %q is not a duration", pg.ReviewEvery), Remedy: Remedy{Fix: fmt.Sprintf(remedyProblem, doc, 1, err)}})
			continue
		}
		var newest time.Time
		for k, a := range r.acks {
			if k.Doc == doc && a.At.After(newest) {
				newest = a.At
			}
		}
		if newest.IsZero() {
			newest = r.in.Prev.Header.ScannedAt
		}
		if newest.IsZero() || r.in.Now.After(newest.Add(d)) {
			r.emit(Finding{State: StateExpired, Doc: doc, Line: 1, Message: fmt.Sprintf("page review due (every %s)", pg.ReviewEvery), Remedy: Remedy{Fix: fmt.Sprintf(remedyReviewDue, doc, doc)}})
		}
	}
}

// uncovered reports defs nothing points at.
func (r *runner) uncovered() {
	for _, b := range r.in.Defs {
		if !r.referenced[b.ID] {
			r.emit(Finding{State: StateUncovered, Doc: b.DirectivePos.File, Line: b.DirectivePos.Start, ID: b.ID, File: b.Pos.File, Owner: b.Owner(), Message: "defined but never cited or covered", Remedy: Remedy{Fix: fmt.Sprintf(remedyUncovered, b.ID)}})
		}
	}
}

// ParseDuration reads the duration shape docsync writes. The grammar lives
// in internal/duration so configuration can validate the same shape without
// importing the checker.
func ParseDuration(s string) (time.Duration, error) { return duration.Parse(s) }

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedPageKeys(m map[string]extract.Page) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// unscanned reports each unreadable file that held citations or defs at the
// last scan. A file that never did has nothing here to go unchecked.
func (r *runner) unscanned() {
	for _, sk := range r.in.Unreadable {
		defs, cites := 0, 0
		for _, row := range r.in.Prev.Rows {
			if row.File == sk.File {
				defs++
			}
		}
		for _, row := range r.in.PrevRefs.Rows {
			if row.Doc == sk.File {
				cites++
			}
		}
		if defs+cites == 0 {
			continue
		}
		f := Finding{State: StateUnscanned, Doc: sk.File, Line: 1, File: sk.File, Lines: [2]int{1, 1}}
		f.Message = fmt.Sprintf("could not be scanned (%s); %s and %s are not being checked", sk.Reason, plural(cites, "citation"), plural(defs, "block"))
		f.Remedy.Fix = fmt.Sprintf(remedyUnscanned, sk.File, sk.Reason, unscannedHint[sk.Reason])
		r.emit(f)
	}
}

// plural renders a count with its noun, singular for exactly one.
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// ClaimAbout lists the ids a claim's about= names, brackets trimmed.
func ClaimAbout(ref block.Reference) []string {
	var ids []string
	for _, id := range ref.Directive().List(keyAbout) {
		ids = append(ids, strings.Trim(id, "[]"))
	}
	return ids
}

// AboutHash fingerprints what a claim is about: every current hash of every
// id in ids, across environments and repositories. The ack that renews a
// claim records it, and check then holds a changed about= block as reviewed
// while the fingerprint still matches. Without it, renewing a claim whose
// block had changed printed "renewed" and left the claim expired until the
// next `ds scan` recorded the new hash (bug 72). Empty when ids is empty.
func AboutHash(ids []string, defs ...[]block.Block) string {
	if len(ids) == 0 {
		return ""
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var parts []string
	for _, set := range defs {
		for _, b := range set {
			if want[b.ID] {
				parts = append(parts, b.ID+"\x00"+b.Env()+"\x00"+b.Hash)
			}
		}
	}
	sort.Strings(parts)
	return textnorm.HashLines(append(append([]string{}, ids...), parts...))
}

// claimRenewal is the newest ack that renewed this claim: the one recorded
// on its line, or — when the claim has moved, as it does whenever a line is
// added above it — the newest claim ack in the same doc for the same
// sentence. Looking only at the line lost the renewal on any edit above the
// claim, and a claim renewed yesterday reported expired again.
func (r *runner) claimRenewal(ref block.Reference) (ledger.Ack, bool) {
	if a, ok := r.acks[ledger.AckKey{Repo: r.in.Repo, Doc: ref.Pos.File, Line: ref.Pos.Start}]; ok {
		return a, true
	}
	if ref.SentenceHash == "" {
		return ledger.Ack{}, false
	}
	var best ledger.Ack
	found := false
	for k, a := range r.acks {
		if k.ID == "" && k.Repo == r.in.Repo && k.Doc == ref.Pos.File && a.SentenceHash == ref.SentenceHash && (!found || a.At.After(best.At)) {
			best, found = a, true
		}
	}
	return best, found
}

// codeStillInBlock reports whether every code line a repo-mode copy shows
// is still in the block, in the same order: what moved is only the lines
// around them -- the caption's range, a directive added inside the block, a
// `lines=` window that now starts or ends elsewhere -- and a refresh would
// rewrite it. A copy with a line the block does not have was edited by hand.
// Directive lines are left out on both sides, as the hash leaves them out.
func codeStillInBlock(copyText, content, prefix string) bool {
	shown := fencedLines(copyText, prefix)
	if len(shown) == 0 || content == "" {
		return false
	}
	body := codeLines(strings.Split(content, "\n"), prefix)
	j := 0
	for _, l := range body {
		if j < len(shown) && l == shown[j] {
			j++
		}
	}
	return j == len(shown)
}

// fencedLines returns the code lines inside a copy's first fence.
func fencedLines(text, prefix string) []string {
	var in []string
	open := false
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			if open {
				break
			}
			open = true
			continue
		}
		if open {
			in = append(in, l)
		}
	}
	return codeLines(in, prefix)
}

// codeLines drops directive lines and trailing blanks, keeping the rest as
// written (leading indentation is code).
func codeLines(ls []string, prefix string) []string {
	var out []string
	for _, l := range ls {
		t := strings.TrimSpace(l)
		if strings.Contains(t, prefix+":def") || strings.Contains(t, prefix+":block") {
			continue
		}
		out = append(out, strings.TrimRight(l, " \t\r"))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
