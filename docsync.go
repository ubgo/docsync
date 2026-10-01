// Package docsync is the library behind the `ds` tool: it keeps documentation
// bound to the code, configuration, and facts it describes, and reports when
// the thing behind a sentence changed (docs/SPEC.md).
//
// The package is stdlib-only and pure over its inputs (§37): a System is
// built from an fs.FS, a parsed config, the previous ledger, and optional
// hooks; every operation returns values and never writes a file, reads the
// environment, or touches the network. The CLI owns paths, git, and printing.
package docsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing/fstest"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/id"
	"github.com/ubgo/docsync/internal/glob"
	"github.com/ubgo/docsync/internal/textnorm"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/match"
	"github.com/ubgo/docsync/pick"
	"github.com/ubgo/docsync/render"
	"github.com/ubgo/docsync/scan"
)

// SpecVersion is the specification this library implements (§37.7).
const SpecVersion = "1.0"

// JSONFormat is the version of the JSON contract (§26.2). It changes only
// additively within a major version.
const JSONFormat = 1

// Sentinel errors.
var (
	ErrNoFS               = errors.New("docsync: WithFS is required")
	ErrNotFound           = errors.New("docsync: not found")
	ErrDelegationRequired = errors.New("docsync: an agent ack needs delegated_by (§26.7)")
	ErrNoReference        = errors.New("docsync: no reference at that doc line")
	// ErrNoCarrier is returned instead of writing a directive into a file whose
	// type has no comment syntax in extract.Styles. It exists because the
	// alternative that shipped was writing the directive bare, which is invalid
	// syntax in every such format.
	ErrNoCarrier = errors.New("docsync: no comment carrier for this file type")
	// ErrGeneratedPath refuses to mint a def in a file under [scan] generated
	// (Part VII: "Generated paths refuse minting"). The next generation
	// rewrites the file and the directive with it, so the id would vanish
	// while every sentence citing it stayed; scan reports such a def after
	// the fact (scan.ErrDefInGenerated), and minting one is how it gets there.
	ErrGeneratedPath = errors.New("docsync: generated file; a def written here is overwritten by the next generation")
	// ErrWouldNotBind is returned instead of writing a directive the scan
	// could not bind. Define checks by extracting the edited source in memory
	// with the scan's own extractor, so the refusal comes before the file is
	// touched and `--dry-run` reports it too.
	ErrWouldNotBind = errors.New("docsync: the directive would not bind")
	// ErrBadLabel is a rename target that is not a label: lowercase
	// words joined by single dashes. A label with a space was written into
	// every def and citation, and each came out as a broken directive.
	ErrBadLabel     = errors.New("docsync: not a label")
	ErrHashMismatch = errors.New("docsync: reference is not to that id")
	// ErrBadLines is a `lines` fragment outside the grammar a citation
	// uses: `a` or `a-b`, digits only, 1 <= a <= b. A well-formed range
	// past the block's end is ErrNotFound instead: those lines do not exist.
	ErrBadLines = errors.New("docsync: bad lines")
	// ErrNoPageReview is a page review requested for a doc that declares
	// no review_every, or that the scan does not know.
	ErrNoPageReview = errors.New("docsync: page has no review_every to record a review against")
	// ErrNewerRule refuses this repo's ledger or refs when they record an
	// extraction rule newer than this build implements (§33 Versioning: an
	// older tool refuses a newer format by name, never by misparsing). Those
	// hashes cannot be compared with the ones this build computes: reading them
	// would report unchanged blocks as drift, and a scan would rewrite the
	// files under the older rule. The remedy is a newer ds.
	//
	// Pre-release builds stamped extract=2 to 4 for what is now rule 1 (the
	// counter was reset for the first release). Such a repository sees this
	// refusal once; setting extract=1 on the header line of .ds/ledger.tsv and
	// .ds/refs.tsv, then running `ds scan`, is the whole fix.
	ErrNewerRule = errors.New("docsync: recorded under a newer extraction rule than this build implements")
	// ErrOtherRule reports a ledger or refs recorded under an older extraction
	// rule, which is the upgrade path: unchanged blocks can report drift until
	// the next scan restamps both files, so hosts warn rather than refuse.
	ErrOtherRule = errors.New("docsync: recorded under an older extraction rule; unchanged blocks may report drift until the next scan")
	// ErrUnknownEnv refuses an environment name that [env] known does not
	// list, when it lists any. A misspelled --env used to run the whole
	// command against an environment nothing defines, and every citation
	// then reported a missing definition instead of the one typo.
	ErrUnknownEnv = errors.New("docsync: environment not in env.known")
)

// envFor is the environment a command runs for: the one asked for, held to
// env.known, or env.default when none was asked for. Check, Render and
// Define all go through it, so a name is accepted or refused the same way
// whichever command it is passed to.
func (s *System) envFor(asked string) (string, error) {
	if asked == "" {
		return s.cfg.Env.Default, nil
	}
	if !s.cfg.KnownEnv(asked) {
		return "", fmt.Errorf("%w: %q; env.known is %s", ErrUnknownEnv, asked, strings.Join(s.cfg.Env.Known, ", "))
	}
	return asked, nil
}

// Envelope is the header every JSON result carries (§26.2).
type Envelope struct {
	JSONFormat  int       `json:"json_format"`
	GeneratedAt time.Time `json:"generated_at"`
	Repo        string    `json:"repo,omitempty"`
	Commit      string    `json:"commit,omitempty"`
}

// System is a configured instance. Build one with New; it is safe to share
// between goroutines because every method is read-only over its fields.
type System struct {
	// command is the binary name remedies are written with (WithCommandName).
	command      string
	fsys         fs.FS
	cfg          config.Config
	repo         string
	commit       string
	registry     *extract.Registry
	prev         ledger.Ledger
	prevRefs     ledger.Refs
	acks         ledger.Acks
	merged       []block.Block
	mergedRefs   []ledger.RefRow
	removed      map[string]string
	snapshotOnly bool
	prevForeign  []ledger.Row
	cache        scan.Cache
	verbs        map[string]bool
	now          func() time.Time
	idcfg        id.Config

	verbHandlers map[string]Verb
	pickers      map[string]pick.Picker
	classifier   Classifier
	renderer     Renderer
	store        Store
	notifiers    []Notifier
	observers    []Observer

	oldContent   func(row ledger.Row) (string, bool)
	bodyAt       func(hash string) (string, bool)
	commitExists func(sha string) bool
	urlCheck     func(href string) check.URLResult
	resolver     check.Resolver
	storedHashes map[string]string
	localRead    func(path string) ([]byte, error)
	tests        map[string]check.TestOutcome
	snapshot     func(id, sha string) (string, bool)
	records      func(args map[string]string) ([]map[string]string, error)
}

// Option configures a System. Functional options only (§37.2).
type Option func(*System) error

// WithFS sets the tree to scan. Required.
func WithFS(fsys fs.FS) Option {
	return func(s *System) error { s.fsys = fsys; return nil }
}

// WithConfig sets the parsed config; the library never reads the file itself.
// Omitted, config.Default() with `docs/**` and `**` as scan globs is used so
// a zero-config call still scans something.
func WithConfig(c config.Config) Option {
	return func(s *System) error { s.cfg = c; return nil }
}

// WithRepo names this repository inside a workspace (§21).
func WithRepo(name string) Option {
	return func(s *System) error { s.repo = name; return nil }
}

// WithCommit records the commit the tree is at, for permalinks and headers.
func WithCommit(sha string) Option {
	return func(s *System) error { s.commit = sha; return nil }
}

// WithExtractor adds tiers ahead of the built-in ones; the last given wins
// for a path both match.
func WithExtractor(es ...extract.Extractor) Option {
	return func(s *System) error {
		for _, e := range es {
			s.registry.Prepend(e)
		}
		return nil
	}
}

// WithRegistry replaces tier selection entirely; the escape hatch (§37.3).
func WithRegistry(r *extract.Registry) Option {
	return func(s *System) error { s.registry = r; return nil }
}

// WithPrevious supplies the committed ledger and refs to diff against. Both
// zero means a first run: everything is new and ok. Either one recorded under
// a newer extraction rule is refused with ErrNewerRule.
func WithPrevious(l ledger.Ledger, r ledger.Refs) Option {
	return func(s *System) error {
		if err := CheckRule(l, r); errors.Is(err, ErrNewerRule) {
			return err
		}
		s.prev, s.prevRefs = l, r
		return nil
	}
}

// WithAcks supplies the append-only ack log.
func WithAcks(a ledger.Acks) Option {
	return func(s *System) error { s.acks = a; return nil }
}

// preReleaseFix is the remedy a refusal carries for a repository that ran a
// pre-release build, whose rule numbers ran above today's rule 1.
const preReleaseFix = "upgrade ds; if a pre-release ds wrote it, set extract=1 on line 1 of .ds/ledger.tsv and .ds/refs.tsv, then run `ds scan`"

// CheckRule compares the extraction rule the ledger and refs record with
// extract.Rule. A newer rule is ErrNewerRule, which WithPrevious refuses; an
// older one is ErrOtherRule, which hosts print as a warning (`ds check`,
// `ds scan`, `ds doctor`). Both name the file and the two rules. It is exported
// so a host that inspects the state without building a System (`ds doctor`)
// reports the same thing.
//
// The ack log is not checked. Its header is written once and never restamped,
// so its rule says nothing about its rows, and each row carries its own rule,
// which check already reports by name ("ack was recorded under sentence rule
// N", section 18).
func CheckRule(l ledger.Ledger, r ledger.Refs) error { return checkRule(l, r, extract.Rule) }

// checkRule is CheckRule against a given current rule, so the upgrade path --
// a file from an older rule -- is testable while the only released rule is 1.
func checkRule(l ledger.Ledger, r ledger.Refs, current int) error {
	var older error
	for _, h := range []ledger.Header{l.Header, r.Header} {
		// A header that does not record a rule predates the field: rule 1,
		// as the ledger package reads it.
		rule := h.Extract
		if rule == 0 {
			rule = 1
		}
		switch {
		case h.Kind == "":
			// A zero header is a first run: nothing was recorded under any rule.
		case rule > current:
			return fmt.Errorf("%w: %s.tsv says extract=%d, this build implements rule %d; %s", ErrNewerRule, h.Kind, rule, current, preReleaseFix)
		case rule < current && older == nil:
			older = fmt.Errorf("%w: %s.tsv says extract=%d, this build implements rule %d", ErrOtherRule, h.Kind, rule, current)
		}
	}
	return older
}

// WithMerged supplies blocks from other repos in the workspace, as produced
// by sync (§21). They can be cited and rendered but are not this repo's.
func WithMerged(blocks ...block.Block) Option {
	return func(s *System) error { s.merged = append(s.merged, blocks...); return nil }
}

// WithMergedRefs supplies references published by other repositories that
// point into this one (§21), so deleting a block here reports the docs
// elsewhere that depend on it.
func WithMergedRefs(rows ...ledger.RefRow) Option {
	return func(s *System) error { s.mergedRefs = append(s.mergedRefs, rows...); return nil }
}

// WithPreviousForeign supplies the committed snapshot's rows as the previous
// position of every foreign block, so a block that moved in another
// repository since the last sync is reported as `moved` rather than in
// silence (§21).
func WithPreviousForeign(rows ...ledger.Row) Option {
	return func(s *System) error { s.prevForeign = append(s.prevForeign, rows...); return nil }
}

// WithForeignSnapshot says the merged blocks came from a committed snapshot
// rather than the live workspace index, so a citation this run cannot
// resolve is reported as unrecorded rather than as undefined (§21).
func WithForeignSnapshot() Option {
	return func(s *System) error { s.snapshotOnly = true; return nil }
}

// WithRemoved names ids published by repositories that left the workspace,
// so citations report "repo removed" rather than "deleted" (§21).
func WithRemoved(ids map[string]string) Option {
	return func(s *System) error { s.removed = ids; return nil }
}

// WithExtractCache makes scans incremental: files whose bytes did not
// change reuse the cached extraction (§16), and a cache that also
// implements scan.StatCache spares unchanged files the read itself.
// CheckOptions.Full bypasses it.
func WithExtractCache(c scan.Cache) Option {
	return func(s *System) error { s.cache = c; return nil }
}

// WithVerb registers plugin verb names so references to them are not
// reported as unknown. Their check and render behaviour is the plugin's.
func WithVerb(names ...string) Option {
	return func(s *System) error {
		for _, n := range names {
			s.verbs[n] = true
		}
		return nil
	}
}

// WithOldContent supplies the body a block had in a previous ledger row, for
// diffs and change classes; see match.Options.OldContent for why it takes
// the row. Typically backed by `git show` in the CLI.
func WithOldContent(f func(row ledger.Row) (string, bool)) Option {
	return func(s *System) error { s.oldContent = f; return nil }
}

// WithBodyAt supplies block bodies by content hash from the body store
// (§20.1). It is what answers "what did this block say when the sentence was
// acked" across several scans and across repositories, which WithOldContent —
// keyed to the previous ledger — structurally cannot. Without it a drift the
// change table does not span is reported with block.ClassUnknown.
func WithBodyAt(f func(hash string) (string, bool)) Option {
	return func(s *System) error { s.bodyAt = f; return nil }
}

// Bodies is what the caller should add to the body store after a scan:
// every def's body keyed by its content hash (§20.1). Content-addressed, so
// re-storing an unchanged block is a no-op and history accumulates without
// duplication; the caller writes, because the library never does.
//
// Secret and local blocks are excluded and this is enforced here rather than
// left to the caller: the workspace index is readable by people who may not
// read the source repository, and a body written there cannot be recalled.
// A block with no content (a merged row, which carries a hash but no body)
// contributes nothing.
func (s *System) Bodies(res scan.Result) map[string]string {
	out := map[string]string{}
	for _, b := range res.Defs {
		if b.Content == "" || b.IsSecret() || b.IsLocal() {
			continue
		}
		out[b.Hash] = b.Content
	}
	return out
}

// WithCommitLookup answers `at=` pins.
func WithCommitLookup(f func(sha string) bool) Option {
	return func(s *System) error { s.commitExists = f; return nil }
}

// WithURLCheck answers `ds:url` under --resolve.
func WithURLCheck(f func(href string) check.URLResult) Option {
	return func(s *System) error { s.urlCheck = f; return nil }
}

// WithResolver supplies the secret resolver used under --resolve (§12).
func WithResolver(r check.Resolver) Option {
	return func(s *System) error { s.resolver = r; return nil }
}

// WithLocalReader lets check look at `local=true` targets (§9.1, §12): f
// reads a def's file= path on the machine running check. A target that f
// reads, and whose pick= finds something, is `ok` there; one f cannot read
// stays `unverifiable`, which is what CI reports. Without it every local def
// is unverifiable everywhere (bug 85). Nothing read is hashed or stored: the
// ledger is shared and the file exists on one machine.
func WithLocalReader(f func(path string) ([]byte, error)) Option {
	return func(s *System) error { s.localRead = f; return nil }
}

// localHook adapts the reader to check's Local hook, reading each target
// once per check however many defs and citations name it.
func (s *System) localHook() func(block.Block) check.LocalResult {
	if s.localRead == nil {
		return nil
	}
	seen := map[string]check.LocalResult{}
	return func(b block.Block) check.LocalResult {
		expr := b.Args[block.KeyPick]
		if expr == "" {
			expr = pick.SchemeFile
		}
		key := b.Pos.File + "\x00" + expr
		if res, ok := seen[key]; ok {
			return res
		}
		var res check.LocalResult
		if src, err := s.localRead(b.Pos.File); err == nil {
			res.Present = true
			if _, err := pick.PickWith(s.pickers, expr, string(src)); err != nil {
				res.Err = fmt.Errorf("pick=%s failed: %w", expr, err)
			}
		}
		seen[key] = res
		return res
	}
}

// WithStoredHashes supplies truth hashes kept from an earlier resolve, so a
// changed truth reports as rotated.
func WithStoredHashes(m map[string]string) Option {
	return func(s *System) error { s.storedHashes = m; return nil }
}

// WithTestResults supplies the last published CI outcomes for `assert=`.
func WithTestResults(m map[string]check.TestOutcome) Option {
	return func(s *System) error { s.tests = m; return nil }
}

// WithSnapshot supplies block bodies at a commit for `at=` rendering.
func WithSnapshot(f func(id, sha string) (string, bool)) Option {
	return func(s *System) error { s.snapshot = f; return nil }
}

// WithRecords supplies a record source for `ds:table`.
func WithRecords(f func(args map[string]string) ([]map[string]string, error)) Option {
	return func(s *System) error { s.records = f; return nil }
}

// WithCommandName names the binary that findings tell the reader to run,
// for a custom build (cli.WithName): its remedies say "pds ack", not a
// "ds ack" its users do not have. Empty keeps check.DefaultCommand.
func WithCommandName(name string) Option {
	return func(s *System) error { s.command = name; return nil }
}

// WithClock replaces time.Now, for reproducible output.
func WithClock(f func() time.Time) Option {
	return func(s *System) error { s.now = f; return nil }
}

// New builds a System.
func New(opts ...Option) (*System, error) {
	s := &System{registry: extract.Default(), verbs: map[string]bool{}, verbHandlers: map[string]Verb{}, pickers: map[string]pick.Picker{}, now: func() time.Time { return time.Now().UTC() }}
	s.cfg = config.Default()
	s.cfg.Scan.Docs = []string{"docs/**"}
	s.cfg.Scan.Code = []string{"**"}
	for _, o := range opts {
		if err := o(s); err != nil {
			return nil, err
		}
	}
	if s.fsys == nil {
		return nil, ErrNoFS
	}
	if err := s.cfg.Validate(); err != nil {
		return nil, err
	}
	// Validate refused an [id] the minting rules would refuse (bug 121).
	s.idcfg = s.cfg.IDConfig()
	return s, nil
}

// Config returns the effective configuration.
func (s *System) Config() config.Config { return s.cfg }

// Registry returns the extractor registry in use, so a host that has a
// document in memory (an editor buffer) can extract it the same way a scan
// would.
func (s *System) Registry() *extract.Registry { return s.registry }

// Scan extracts every def and reference under the config's globs, using
// the extraction cache when one is configured.
func (s *System) Scan(ctx context.Context) (scan.Result, error) {
	return s.scanWith(ctx, false)
}

// ScanFull ignores the extraction cache.
func (s *System) ScanFull(ctx context.Context) (scan.Result, error) {
	return s.scanWith(ctx, true)
}

func (s *System) scanWith(ctx context.Context, full bool) (scan.Result, error) {
	opts, err := s.scanOptions()
	if err != nil {
		return scan.Result{}, err
	}
	if full {
		opts.Cache = nil
	}
	return scan.Scan(ctx, s.fsys, opts)
}

func (s *System) scanOptions() (scan.Options, error) {
	include, err := glob.CompileAll(append(append([]string{}, s.cfg.Scan.Code...), s.cfg.Scan.Docs...))
	if err != nil {
		return scan.Options{}, fmt.Errorf("scan.code/docs: %w", err)
	}
	exclude, err := glob.CompileAll(s.cfg.Scan.Exclude)
	if err != nil {
		return scan.Options{}, fmt.Errorf("scan.exclude: %w", err)
	}
	generated, err := glob.CompileAll(s.cfg.Scan.Generated)
	if err != nil {
		return scan.Options{}, fmt.Errorf("scan.generated: %w", err)
	}
	secret, err := glob.CompileAll(s.cfg.Secret.Paths)
	if err != nil {
		return scan.Options{}, fmt.Errorf("secret.paths: %w", err)
	}
	return scan.Options{Prefix: s.cfg.Prefix, Repo: s.repo, Include: include, Exclude: exclude, Generated: generated, Secret: secret, Registry: s.registry, MaxFileKB: s.cfg.Scan.MaxFileKB, MaxLineChars: s.cfg.Scan.MaxLineChars, Pickers: s.pickers, Cache: s.cache}, nil
}

// ExtractFile runs the scan pipeline over one file's bytes -- the tier that
// owns the path, `pick`, the key-to-value step and secret redaction -- and
// returns the defs it finds, exactly as a scan of that content would.
//
// Why it exists: an OldContent hook has to hand back a block's previous body
// in the same form as its current one, or the diff compares two different
// things. The CLI's hook cut the raw lines out of the old file, so a config
// value changed from 443 to 8443 was diffed as the whole line
// "port: 443 # ds:def id=…" against "8443", directive comment included, and a
// secret's old value came back unredacted. Going through the same pipeline
// makes old and new comparable by construction.
//
// The extraction cache is not used: old bytes must never be stored or served
// as though they were the current file's.
func (s *System) ExtractFile(ctx context.Context, path string, src []byte) ([]block.Block, error) {
	opts, err := s.scanOptions()
	if err != nil {
		return nil, err
	}
	opts.Cache = nil
	res, err := scan.Scan(ctx, fstest.MapFS{path: &fstest.MapFile{Data: src}}, opts)
	if err != nil {
		return nil, err
	}
	return res.Defs, nil
}

// Snapshot turns a scan into the ledger and refs to commit (§15). Refs carry
// the latest acked hash per line so a fresh checkout can check without the
// ack log.
func (s *System) Snapshot(res scan.Result) (ledger.Ledger, ledger.Refs) {
	h := ledger.Header{Format: ledger.Format, Extract: extract.Rule, Repo: s.repo, Commit: s.commit, ScannedAt: s.now()}
	l := ledger.Ledger{Header: h}
	l.Header.Kind = ledger.KindLedger
	for _, b := range res.Defs {
		l.Rows = append(l.Rows, ledger.FromBlock(s.repo, b))
	}
	// A file that could not be read this time keeps what the last scan
	// recorded for it. Dropping it would forget its citations' first-seen
	// hashes, and when the file came back its citations would count as new
	// and be baselined at today's hash — accepting whatever changed in the
	// meantime. check reports the file as unscanned until it is read again.
	gone := map[string]bool{}
	for _, sk := range unreadable(res.Skipped) {
		gone[sk.File] = true
	}
	for _, row := range s.prev.Rows {
		if gone[row.File] {
			l.Rows = append(l.Rows, row)
		}
	}
	r := ledger.Refs{Header: h}
	r.Header.Kind = ledger.KindRefs
	latest := s.acks.Latest()
	// Hashes of everything a citation in this repo can point at, including
	// blocks defined elsewhere in the workspace, so a cross-repo citation
	// gets a baseline on the same terms as a local one.
	defsByID := s.defsByID(res)
	hashOf := func(x ledger.RefRow) string {
		env := x.Env
		if env == "" {
			env = s.cfg.Env.Default
		}
		b, ok := check.ResolveDef(defsByID[x.ID], env, x.Args[block.KeyBranch])
		if !ok || ambiguous(defsByID[x.ID], b) {
			return ""
		}
		return b.Hash
	}
	cur := make([]ledger.RefRow, len(res.Refs))
	for i, ref := range res.Refs {
		cur[i] = ledger.FromReference(s.repo, ref)
	}
	// A citation keeps its baselines when it moves: a line inserted above
	// it, or its doc renamed. Looking them up by exact position instead
	// treated every moved citation as new and silently accepted whatever the
	// block says now; ledger.Baselines is where that is decided, for scan
	// and check alike.
	base := ledger.Baselines(s.prevRefs.Rows, cur, latest, s.prevRefs.Header.ScannedAt, hashOf)
	for i, row := range cur {
		row.AckedHash = base[i].Acked
		// Written once, at the first scan that records this citation, and
		// carried forward unchanged afterwards. Re-deriving it every scan is
		// precisely the bug this closes: it would silently adopt whatever
		// the block says now, so a change nobody reviewed would never be
		// reported.
		row.SeenHash = base[i].Seen
		if row.SeenHash == "" {
			row.SeenHash = hashOf(row)
		}
		r.Rows = append(r.Rows, row)
	}
	for _, row := range s.prevRefs.Rows {
		if gone[row.Doc] {
			r.Rows = append(r.Rows, row)
		}
	}
	return l, r
}

// CheckOptions selects the passes of `ds check` (§16, §22).
type CheckOptions struct {
	Strict bool
	// Env overrides env.default for this run.
	Env string
	// Run and Resolve say the caller will execute directives and reach
	// providers; the library itself still runs nothing.
	Run     bool
	Resolve bool
	// Full ignores the extraction cache.
	Full bool
}

// Report is the result of Check: the JSON contract plus the scan it came
// from, so callers can build further views without rescanning.
type Report struct {
	Envelope
	Summary  map[check.Severity]int `json:"summary"`
	States   map[check.State]int    `json:"states"`
	Findings []check.Finding        `json:"findings"`
	Changes  []match.Change         `json:"-"`
	Scan     scan.Result            `json:"-"`
	ExitCode int                    `json:"exit_code"`
	// TruthHashes are resolved truth hashes for the caller to store.
	TruthHashes map[string]string `json:"-"`
}

// ApplyRuns records the outcomes of `ds:run` directives the caller executed
// as their findings, and recounts the summary and exit code: a failed run is
// a `run failed` error, a run not executed is `skipped` with the reason. The
// library runs nothing itself; see check.ApplyRuns.
func (r Report) ApplyRuns(runs []check.RunResult) Report {
	cr := check.ApplyRuns(check.Report{Findings: r.Findings}, runs)
	r.Findings, r.States, r.Summary, r.ExitCode = cr.Findings, cr.Summary, cr.BySeverity, cr.ExitCode
	return r
}

// Check scans and evaluates.
func (s *System) Check(ctx context.Context, opts CheckOptions) (Report, error) {
	res, err := s.scanWith(ctx, opts.Full)
	if err != nil {
		return Report{}, err
	}
	return s.checkScan(res, opts)
}

// undocumented lists exported declarations without a def in files that
// policy.require_doc covers (§23).
func (s *System) undocumented(res scan.Result) ([]check.Undocumented, error) {
	if len(s.cfg.Policy.RequireDoc) == 0 {
		return nil, nil
	}
	required, err := glob.CompileAll(s.cfg.Policy.RequireDoc)
	if err != nil {
		return nil, fmt.Errorf("policy.require_doc: %w", err)
	}
	defLines := map[string]map[int]bool{}
	for _, b := range res.Defs {
		if defLines[b.Pos.File] == nil {
			defLines[b.Pos.File] = map[int]bool{}
		}
		defLines[b.Pos.File][b.Pos.Start] = true
	}
	// A file counts by what it is, not by which tier read it: a syntax tier
	// (Go, TypeScript, Python, ...) registered ahead of the heuristic code
	// tier takes those files, and keying on the tier's name left the policy
	// silently empty for every language that has one.
	var files []string
	for f := range res.Tier {
		if (extract.Code{}).Match(f) && required.MatchAny(f) {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	var out []check.Undocumented
	for _, f := range files {
		raw, err := fs.ReadFile(s.fsys, f)
		if err != nil {
			continue
		}
		for i, l := range strings.Split(string(raw), "\n") {
			if m := exportedDeclRE.FindStringSubmatch(l); m != nil && !defLines[f][i+1] {
				out = append(out, check.Undocumented{File: f, Line: i + 1, Symbol: m[1]})
			}
		}
	}
	return out, nil
}

func (s *System) checkScan(res scan.Result, opts CheckOptions) (Report, error) {
	env, err := s.envFor(opts.Env)
	if err != nil {
		return Report{}, err
	}
	urlCheck := s.urlCheck
	resolver := s.resolver
	if !opts.Resolve {
		urlCheck, resolver = nil, nil
	}
	undocumented, policyErr := s.undocumented(res)
	if policyErr != nil {
		return Report{}, policyErr
	}
	var remote []check.RemoteRef
	for _, row := range s.mergedRefs {
		remote = append(remote, check.RemoteRef{Repo: row.Repo, Ref: row.ToReference(), AckedHash: row.AckedHash, SeenHash: row.SeenHash})
	}
	handlers, knownKeys, requiredKeys := s.checkHandlers(res.Defs)
	rep := check.Run(check.Input{
		Repo: s.repo, Now: s.now(), Env: env, Prefix: s.cfg.Prefix,
		Defs: res.Defs, Merged: s.merged, Refs: res.Refs, MergedRefs: remote, Problems: res.Problems, Undocumented: undocumented, Pages: res.Pages,
		Prev: s.prev, PrevRefs: s.prevRefs, Acks: s.acks, Removed: s.removed, SnapshotOnly: s.snapshotOnly, PrevForeign: s.prevForeign,
		Unreadable: unreadable(res.Skipped),
		Opts: check.Options{
			MaxLines: s.cfg.Include.MaxLines, FuzzyThreshold: s.cfg.Check.FuzzyThreshold,
			UnackedIsWarning: s.cfg.Check.Unacked == config.UnackedWarn, Strict: opts.Strict,
			Wording:    s.cfg.Check.Sentence != config.SentencePosition,
			OldContent: s.oldContent, BodyAt: s.bodyAt, CommentPrefixes: commentPrefixes,
			TestResults: s.tests, CommitExists: s.commitExists, URLCheck: urlCheck, Resolve: resolver, StoredHashes: s.storedHashes, Local: s.localHook(),
			HasRecords: s.records != nil, RunEnabled: opts.Run && s.cfg.Run.Enabled, ExtraVerbs: s.verbs,
			Handlers: handlers, KnownKeys: knownKeys, RequiredKeys: requiredKeys, Classify: s.classifier,
			KnownEnvs: s.cfg.Env.Known, Command: s.command,
		},
	})
	s.observeFindings(rep.Findings)
	return Report{Envelope: s.envelope(), Summary: rep.BySeverity, States: rep.Summary, Findings: rep.Findings, Changes: rep.Changes, Scan: res, ExitCode: rep.ExitCode, TruthHashes: rep.TruthHashes}, nil
}

func (s *System) envelope() Envelope {
	return Envelope{JSONFormat: JSONFormat, GeneratedAt: s.now(), Repo: s.repo, Commit: s.commit}
}

// commentPrefixes gives the comment-only classifier a file's line prefixes.
func commentPrefixes(file string) []string {
	st, ok := extract.StyleFor(file)
	if !ok {
		return nil
	}
	return st.Line
}

// Fences rewrites the repo-mode copies in one document (§9.2 include.mode =
// repo): every block-position `ds:block` gets, or has refreshed, the
// fragment `refresh` maintains, closed by a line carrying the block's short
// hash. src is the document's current bytes (the caller read them, so the
// caller decides what a read failure means); it returns the new bytes and
// how many regions changed. Nothing is written.
func (s *System) Fences(res scan.Result, doc string, src []byte) ([]byte, int) {
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	type edit struct {
		start, end int // 1-based lines to replace; end < start means insert after start-1
		text       []string
	}
	var edits []edit
	for _, ref := range res.Refs {
		if ref.Pos.File != doc || ref.Verb != extract.VerbBlock || ref.Carrier != block.CarrierBlock {
			continue
		}
		b, ok := s.LocateID(res, ref.ID)
		if !ok {
			continue
		}
		frag, ok, _ := render.Fragment(b, ref, s.cfg.Prefix, s.cfg.Include.MaxLines)
		if !ok {
			continue
		}
		body := append(strings.Split(frag, "\n"), render.Closer(s.cfg.Prefix, textnorm.Short(b.Hash)))
		if ref.Region != nil {
			if ref.Region.Hash == textnorm.Short(b.Hash) && textnorm.NormalizeString(ref.Region.Text) == textnorm.NormalizeString(frag) {
				continue
			}
			edits = append(edits, edit{start: ref.Region.Start, end: ref.Region.End, text: body})
		} else {
			edits = append(edits, edit{start: ref.Pos.End + 1, end: ref.Pos.End, text: body})
		}
	}
	if len(edits) == 0 {
		return src, 0
	}
	// Apply bottom-up so earlier line numbers stay valid.
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, e := range edits {
		var tail []string
		if e.end >= e.start {
			tail = append([]string{}, lines[e.end:]...)
		} else {
			tail = append([]string{}, lines[e.start-1:]...)
		}
		lines = append(append(lines[:e.start-1], e.text...), tail...)
	}
	// Keep the document's line endings. Rewriting a CRLF doc with LF turned
	// a one-region refresh into a diff of every line in the file.
	eol := "\n"
	if bytes.Contains(src, []byte("\r\n")) {
		eol = "\r\n"
	}
	return []byte(strings.Join(lines, eol)), len(edits)
}

// RenderOptions tunes Render.
type RenderOptions struct {
	Env string
	// Runs supplies recorded `ds:run` results by doc line.
	Runs map[int]render.RunResult
	// Source replaces the document's bytes, for `render --at` where the
	// caller has the page as it was at a commit; nil reads the tree.
	Source []byte
}

// Render expands one document to plain markdown (§22 `ds render`).
func (s *System) Render(ctx context.Context, doc string, opts RenderOptions) ([]byte, []render.Note, error) {
	src := opts.Source
	if src == nil {
		var err error
		if src, err = fs.ReadFile(s.fsys, doc); err != nil {
			return nil, nil, err
		}
	}
	res, err := s.Scan(ctx)
	if err != nil {
		return nil, nil, err
	}
	env, err := s.envFor(opts.Env)
	if err != nil {
		return nil, nil, err
	}
	in := render.Input{Doc: doc, Src: src, Defs: append(append([]block.Block{}, res.Defs...), s.merged...)}
	ropts := render.Options{
		Prefix: s.cfg.Prefix, Permalink: s.cfg.Check.Permalink, Commit: s.commit, MaxLines: s.cfg.Include.MaxLines,
		Env: env, Now: s.now(), Snapshot: s.snapshot, Records: s.records, Runs: opts.Runs, Verbs: s.renderVerbs(res.Defs),
	}
	if s.renderer != nil {
		nodes, notes := render.RenderNodes(in, ropts)
		out, err := s.renderer.Render(nodes)
		return out, notes, err
	}
	out, notes := render.Render(in, ropts)
	return out, notes, nil
}

// unreadable filters a scan's skips to the files it could not read (see
// scan.SkipReason.Unreadable), which are the ones whose state is carried
// forward and reported rather than dropped.
func unreadable(skipped []scan.Skip) []scan.Skip {
	var out []scan.Skip
	for _, sk := range skipped {
		if sk.Reason.Unreadable() {
			out = append(out, sk)
		}
	}
	return out
}

// ambiguous reports that more than one def shares b's id, env and branch —
// the id defined twice, which scan reports as a problem. No first-seen hash
// is recorded against such an id: it is written once and never revised, so
// recording the accidental copy's hash left every citation "changed" for
// good after the copy was deleted. The baseline is taken on the first scan
// after the id is unique again.
func ambiguous(cands []block.Block, b block.Block) bool {
	n := 0
	for _, c := range cands {
		if c.Env() == b.Env() && c.Args[block.KeyBranch] == b.Args[block.KeyBranch] {
			n++
		}
	}
	return n > 1
}
