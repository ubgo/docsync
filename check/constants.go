package check

import (
	"regexp"

	"github.com/ubgo/docsync/scan"
)

// Rule: NO BARE STRINGS for any value with a closed set of choices. Every
// state, severity, and remedy text lives here; findings, JSON, and tests all
// pick from these constants.

// State is one row of the findings table (docs/SPEC.md §17).
type State string

// The finding states. Each is one row of the docs/SPEC.md §17 table, which
// says what it means, its severity (severityOf), and how it is cleared; a
// new state needs all three plus a conformance fixture.
const (
	StateOK               State = "ok"
	StateMoved            State = "moved"
	StateUnacked          State = "unacked"
	StateBroken           State = "broken"
	StatePickFailed       State = "pick failed"
	StateTooLarge         State = "too-large"
	StateRange            State = "range"
	StateExpired          State = "expired"
	StateSunset           State = "sunset"
	StateDeprecated       State = "deprecated"
	StateAssertFailed     State = "assert failed"
	StateTranslationStale State = "translation stale"
	StateUnsourced        State = "unsourced"
	StateChainBroken      State = "chain broken"
	StateUnverifiable     State = "unverifiable"
	StateSkipped          State = "skipped"
	StateOrphan           State = "orphan"
	StateUncovered        State = "uncovered"
	StateUnknown          State = "unknown"
	StateProblem          State = "problem"
	StateDead             State = "dead"
	StateRetitled         State = "retitled"
	StateURLMoved         State = "url moved"
	StateUndocumented     State = "undocumented export"
	StateResolveFailed    State = "resolve failed"
	StateOutOfSync        State = "out of sync"
	StateStaleCopy        State = "stale copy"
	StateRotated          State = "rotated"
	StateStale            State = "stale"
	StateTampered         State = "tampered"
	StateUnscanned        State = "unscanned"
	StateRunFailed        State = "run failed"
)

// StateValues is the canonical order, used by the summary.
// dsself:def id=statevalues-vwvk2kq3 owner=@docsync stability=stable
var StateValues = []State{StateOK, StateMoved, StateUnacked, StateBroken, StatePickFailed, StateTooLarge, StateRange, StateExpired, StateSunset, StateDeprecated, StateAssertFailed, StateTranslationStale, StateUnsourced, StateChainBroken, StateUnverifiable, StateSkipped, StateOrphan, StateUncovered, StateUnknown, StateProblem, StateDead, StateRetitled, StateURLMoved, StateUndocumented, StateResolveFailed, StateOutOfSync, StateStaleCopy, StateRotated, StateStale, StateTampered, StateUnscanned, StateRunFailed}

// Baseline names what a drifted finding was measured against (§26.2).
type Baseline string

// The baselines: the hash of an explicit ack, or the hash recorded when the
// citation was first seen and nobody has acked it since.
const (
	BaselineAck  Baseline = "ack"
	BaselineSeen Baseline = "seen"
)

// BaselineValues is the canonical order.
var BaselineValues = []Baseline{BaselineAck, BaselineSeen}

// The two baselines an `unacked` message can be measured against (§16 pass
// 4): an explicit ack, or the hash recorded when the citation was first
// seen. The wording matters because the remedy differs in spirit — the first
// says a reviewed statement went stale, the second says nobody has reviewed
// it yet.
const (
	sinceAcked      = "since this sentence was acked"
	sinceFirstCited = "since this sentence was first cited"
)

// DefaultCommand is the binary name the remedy texts are written with.
const DefaultCommand = "ds"

// commandRE finds DefaultCommand used as a command in a message: at the
// start or after a space, backtick or parenthesis, and followed by a space
// and a lowercase subcommand or flag. A directive (`ds:def`) and a word that
// merely ends in "ds" are not matched.
var commandRE = regexp.MustCompile("(^|[\\s`(])" + DefaultCommand + " ([a-z-])")

// CommandText rewrites every command in text from DefaultCommand to name, so
// a binary built under another name (cli.WithName) prints remedies its users
// can run. An empty name, or DefaultCommand, returns text unchanged. It is
// exported because the CLI's own messages follow the same rule.
func CommandText(text, name string) string {
	if name == "" || name == DefaultCommand {
		return text
	}
	return commandRE.ReplaceAllString(text, "${1}"+name+" ${2}")
}

// Severity decides the exit code.
type Severity string

// The severities, most to least severe. Any error makes `check` exit 1;
// the rest are reported and do not fail it.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
	SeverityNone    Severity = "none"
)

// SeverityValues is the canonical order.
var SeverityValues = []Severity{SeverityError, SeverityWarning, SeverityInfo, SeverityNone}

// severityOf is the §17 table. `unacked` may be downgraded by config, which
// Run applies after this lookup.
var severityOf = map[State]Severity{
	StateOK:               SeverityNone,
	StateMoved:            SeverityNone,
	StateUnacked:          SeverityError,
	StateBroken:           SeverityError,
	StatePickFailed:       SeverityError,
	StateTooLarge:         SeverityError,
	StateRange:            SeverityWarning,
	StateExpired:          SeverityError,
	StateSunset:           SeverityError,
	StateDeprecated:       SeverityInfo,
	StateAssertFailed:     SeverityError,
	StateTranslationStale: SeverityError,
	StateUnsourced:        SeverityWarning,
	StateChainBroken:      SeverityError,
	StateUnverifiable:     SeverityWarning,
	StateSkipped:          SeverityInfo,
	StateOrphan:           SeverityWarning,
	StateUncovered:        SeverityInfo,
	StateUnknown:          SeverityWarning,
	StateProblem:          SeverityError,
	StateDead:             SeverityError,
	StateRetitled:         SeverityWarning,
	StateURLMoved:         SeverityWarning,
	StateUndocumented:     SeverityError,
	StateResolveFailed:    SeverityError,
	StateOutOfSync:        SeverityError,
	StateStaleCopy:        SeverityError,
	StateRotated:          SeverityWarning,
	StateStale:            SeverityError,
	StateTampered:         SeverityError,
	StateUnscanned:        SeverityError,
	StateRunFailed:        SeverityError,
}

// SeverityOf returns the table severity for a state; unknown states are
// errors so a typo in a plugin can never pass silently.
func SeverityOf(st State) Severity {
	if sev, ok := severityOf[st]; ok {
		return sev
	}
	return SeverityError
}

// neverStrict lists states that `--strict` must not promote: a move is
// absorbed and a deprecation badge is information (§17).
var neverStrict = map[State]bool{StateOK: true, StateMoved: true, StateDeprecated: true, StateSkipped: true, StateUncovered: true}

// Remedy texts. `%s` placeholders are filled by Run with the concrete ids,
// paths, and lines so an agent can execute the remedy verbatim (§26.2).
const (
	remedyAck = "ds ack %s --doc %s --line %d --note '…'"
	// msgSentenceRewritten and msgAckOtherRule are the two ways an
	// unchanged block's citation is unacked by its wording (SPEC §18).
	msgSentenceRewritten = "sentence rewritten since the ack"
	msgAckOtherRule      = "ack was recorded under sentence rule %s, not %d; re-read the sentence and ack"
	remedyEdit           = "edit the sentence at %s:%d, then ack"
	remedyBroken         = "the id %s is not defined; fix the id in %s:%d or re-add the %s:def on the block it meant"
	// remedyUnrecorded is the frozen-run version. The run resolved foreign
	// blocks from the committed snapshot, so an id absent from it may be
	// perfectly well defined in another repo and simply not recorded yet —
	// a state a new cross-repo citation reaches the moment CI runs before
	// anyone has synced. Telling that reader to "fix the id" would send
	// them to correct a citation that is already right, so the remedy names
	// the two possibilities and does not guess between them.
	// remedyUnscanned names the file, why it was not read, and what to do
	// about that reason; the citations in it are unchecked until it is read.
	remedyUnscanned      = "%s could not be scanned (%s): %s; until it is, the citations in it are not checked and its blocks are kept as they were last seen"
	remedyUnrecorded     = "run `ds sync` and commit %s to record it; if no repo in the workspace publishes %s, fix the id in %s:%d"
	remedyBrokenMoved    = "the block was moved without its %[1]s:def; re-add `%[1]s:def id=%[2]s` above %[3]s:%[4]d (same content found there)"
	remedyBrokenRewrite  = "the block may have been rewritten as %[1]s (%.0[2]f%% similar) at %[3]s:%[4]d; re-add `%[6]s:def id=%[5]s` there or remove the reference"
	remedyPick           = "fix the def's file= or pick= at %s:%d"
	remedyTooLarge       = "add lines=a-b to show a fragment, or cite it with a link instead of rendering it"
	remedyRange          = "adjust lines= at %s:%d to fit the block's %d lines"
	remedyCfgRange       = "the def yields %[1]d lines; %[2]s:cfg needs one line, use %[2]s:block or a narrower pick="
	remedyExpired        = "review the claim at %s:%d and run ds ack --doc %s --line %d to renew it"
	remedyReviewDue      = "the page %s is past its review_every window; reread it, then run ds ack --doc %s to record the review"
	remedySunset         = "%s passed its sunset date %s; remove the reference at %s:%d"
	remedyDeprecated     = "%s is deprecated since %s; plan to move the reference at %s:%d"
	remedyAssert         = "the cited test %s did not pass in the last published run; fix the test or rewrite the sentence at %s:%d"
	remedyTranslation    = "the source paragraph %s changed; update the translation at %s:%d and ack"
	remedyUnsourced      = "%s is a secret with no from= and no truth=true; declare where it is copied from or mark it the truth"
	remedyChainTruth     = "the chain rooted at %s has %d truth=true defs; exactly one is required"
	remedyChainMissing   = "%s says from=%s but that id is not defined"
	remedyChainCycle     = "%s is part of a from= cycle; chains must end at a truth"
	remedyUnverifiableAt = "at=%s cannot be verified here; run where the repository history is available"
	remedyLocal          = "%s is local=true and cannot be read on this machine; this is expected in CI"
	remedyURL            = "external link checks need --resolve with network access"
	remedyRun            = "pass --run to execute %s:run directives where they are enabled"
	// remedyRunFailed names the command, so it can be run by hand to see
	// why, and the sentence whose claim it was checking.
	remedyRunFailed   = "run `%s` by hand to see why; fix the command, or the sentence at %s:%d if it no longer holds"
	remedyTable       = "register a record source in [records] to render %s:table"
	remedyQuery       = "%s:cfg query= is not built yet (SPEC section 38); cite a def with id= instead"
	remedyOrphan      = "the page %s covers %s, which is not defined; remove it from covers or restore the def"
	remedyUncovered   = "%s is defined but nothing cites or covers it; cite it from a page or remove the def"
	remedyUnknownVerb = "%s is not a registered verb; register a handler or fix the directive at %s:%d"
	remedyUnknownKey  = "unknown key(s) %s on %s at %s:%d; check the spelling against the verb's key table"
	remedyProblem     = "fix the directive at %s:%d: %v"
	// remedyDuplicate names the command Part VII gives for a duplicated id. The
	// tool never guesses which copy is the original, so the remedy says how
	// to keep the right one rather than choosing it.
	remedyDuplicate     = "%s:%d: %v; run `ds def --fix` to re-mint every copy after the first (it prints old -> new), or delete the directive from the copy sentences do not mean"
	remedyGenerated     = "move the %s:def out of the generated file %s or exclude it from scan.generated"
	remedyEnvMissing    = "%s has no definition for env=%s; add one or cite a defined environment"
	remedyUnknownEnv    = "env=%s at %s:%d is not in [env] known (%s); fix the name or add it to the list"
	remedyClaimNoDate   = "the claim at %s:%d needs reviewed=YYYY-MM-DD and expires=Nd to age out"
	remedyURLDead       = "the link %s returned %d; update or remove it at %s:%d"
	remedyURLMoved      = "the link %s now redirects to %s; update it at %s:%d"
	remedyURLRetitled   = "the page at %s no longer has title %q; confirm it is still the right page"
	remedyUndocumented  = "policy.require_doc covers %s; run `ds def %s#%s` and cite it from a page"
	remedyResolveFailed = "%s does not exist at %s; fix the address in %s:%d"
	remedyOutOfSync     = "%s differs from its truth %s; run the sync (%s) and ack the runbooks that cite the chain"
	remedyRotated       = "the truth %s changed since its stored hash; run the syncs of its copies and ack the runbooks"
	// remedyStaleCopy: the copy holds exactly the truth's value from before
	// the rotation, so the cause is known — the sync has not run since.
	remedyStaleCopy = "%s still holds the value %s had before it was rotated; run the sync (%s)"
	// remedyResolveUnverifiable is the fix for a secret hop the resolver
	// could not answer. Secret hops used to carry remedyURL, which talks
	// about external links (bug 83).
	remedyResolveUnverifiable = "install the ds-resolve plugin for %[1]s on PATH and log in to the CLI it wraps, or leave %[1]s out of resolve.providers; the message says what failed"
	// remedyURLUnreachable is the fix for a link whose request failed
	// before any HTTP status came back: the network, not the link, is what
	// is known to be wrong, so nothing is reported dead (bug 87).
	remedyURLUnreachable = "the request for %s failed before the server answered; check again with network access"
	remedyURLExpect      = "the link %s returned %d, not the expected %d; update the link or expect= at %s:%d"
	remedyNoSync         = "no sync= declared"
	remedyRepoRemoved    = "repo removed: add %s back to the workspace or drop the reference at %s:%d"
	remedyStale          = "the copy at %s:%d was rendered from an older %s; run `ds refresh`"
	remedyTampered       = "the copy at %s:%d was edited by hand; edit the source block instead, then `ds refresh`"
)

// Known keys per verb, for `unknown` warnings. Every key from the spec's
// tables (§9). Unknown keys never fail a run unless --strict.
var knownKeys = map[string]map[string]bool{
	"def":   set("id", "owner", "tags", "stability", "span", "pick", "type", "file", "local", "env", "secret", "source", "from", "truth", "sync", "runnable", "deprecated", "sunset", "desc", "doc"),
	"block": set("id", "lines", "at", "branch", "translates", "assert", "title", "strip", "collapse", "lang", "label", "env"),
	"cfg":   set("id", "query", "ttl", "env", "format"),
	"run":   set("id", "cmd", "file", "expect", "env", "timeout", "show"),
	"table": set("kind", "where", "cols", "sort", "limit", "empty"),
	"claim": set("owner", "reviewed", "expires", "about"),
	"url":   set("href", "title", "expect"),
	"chain": set("id", "env"),
}

func set(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// Reference keys read by the checker (mirrors block.Key* for the def side).
const (
	keyLines      = "lines"
	keyAt         = "at"
	keyTranslates = "translates"
	keyAssert     = "assert"
	keyQuery      = "query"
	keyReviewed   = "reviewed"
	keyExpires    = "expires"
	keyAbout      = "about"
	keyHref       = "href"
	keyTitle      = "title"
	keyCmd        = "cmd"
	keyExpect     = "expect"
	keyTimeout    = "timeout"
	keyFileArg    = "file"
)

// DefaultMaxLines is the rendered-block cap (§9.2).
const DefaultMaxLines = 40

// DateLayout is the only date format accepted in directives. One format,
// no locale guessing.
const DateLayout = "2006-01-02"

// unscannedHint says what to do about each reason a file was not read.
var unscannedHint = map[scan.SkipReason]string{
	scan.SkipTooLarge:  "raise [scan.limits] max_file_kb, or split the file",
	scan.SkipLongLine:  "raise [scan.limits] max_line_chars; an extractor whose files are prose can declare extract.ProseTier instead",
	scan.SkipBinary:    "it contains a NUL byte; if it is text, remove the byte",
	scan.SkipReadError: "check that the file can be read",
}
