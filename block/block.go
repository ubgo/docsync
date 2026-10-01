// Package block holds the data model shared by every docsync stage: a defined
// Block, a Reference to one, and the closed-set constants (kind, carrier,
// stability, change class) that the ledger, findings, and JSON contract use.
//
// Why a separate package with no behaviour: the scanner produces these, the
// matcher compares them, the ledger serialises them, the renderer reads them.
// If each stage defined its own struct there would be four lossy conversions.
// Keeping the model here and dependency-free means every stage, and every
// plugin, speaks the same shape.
package block

import (
	"regexp"

	"github.com/ubgo/docsync/directive"
	"github.com/ubgo/docsync/internal/textnorm"
)

// Position locates a directive or a block in a file. Lines are 1-based and
// inclusive; a single line has Start == End. File is repo-relative with `/`
// separators.
type Position struct {
	File  string `json:"file"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// Crosses reports two extents in one file that overlap without either one
// containing the other. Nesting is legitimate -- a method in a class, a
// subsection in a section -- but crossing blocks each hold part of the other,
// so an edit that concerns one flags both. A position with no extent (Start
// 0: remote, local, unbound) crosses nothing.
func Crosses(a, b Position) bool {
	if a.File != b.File || a.Start == 0 || b.Start == 0 {
		return false
	}
	overlap := a.Start <= b.End && b.Start <= a.End
	nested := a.Start <= b.Start && b.End <= a.End || b.Start <= a.Start && a.End <= b.End
	return overlap && !nested
}

// Block is one defined unit: the target of a `ds:def`. Everything the ledger
// stores about a definition is here; verbs read their own keys from Args.
type Block struct {
	// ID is the full `prefix-suffix` id from the def.
	ID string `json:"id"`
	// Kind is decided by the extractor that bound the def.
	Kind Kind `json:"kind"`
	// Symbol is the human name of what was bound: `Store.SaveSession`,
	// `auth.port`, a heading title, or "" when the host has no symbols.
	Symbol string `json:"symbol,omitempty"`
	// Pos is where the block's content lives, not where the directive sits;
	// the directive line is DirectivePos.
	Pos Position `json:"pos"`
	// DirectivePos is the line(s) the `ds:def` occupied, including any
	// continuation lines. Renderers strip these; hashes exclude them.
	DirectivePos Position `json:"directive_pos"`
	// Carrier is how the def was written.
	Carrier Carrier `json:"carrier"`
	// Content is the bound text with the directive lines removed. For a fact
	// it is the single picked line. It is kept so `read`, `context`, and the
	// classifier have the body without re-reading the file.
	Content string `json:"-"`
	// Hash is textnorm.Hash(Content). Stored in full; displayed short.
	Hash string `json:"hash"`
	// Args holds every key from the def directive, verbatim, so verbs and
	// policies can read `owner`, `stability`, `env`, `from`, and so on. The
	// well-known ones are exposed by methods; unknown keys are preserved for
	// forward compatibility (§7).
	Args map[string]string `json:"args,omitempty"`
}

// Directive keys read by the core. Verbs may define more; these are the ones
// that change how a block is matched, hashed, or reported.
const (
	KeyID        = "id"
	KeyOwner     = "owner"
	KeyTags      = "tags"
	KeyStability = "stability"
	KeySpan      = "span"
	KeyPick      = "pick"
	KeyType      = "type"
	KeyFile      = "file"
	KeyLocal     = "local"
	KeyEnv       = "env"
	KeySecret    = "secret"
	KeySource    = "source"
	KeyFrom      = "from"
	KeyTruth     = "truth"
	KeySync      = "sync"
	KeyRunnable  = "runnable"
	KeyDeprecate = "deprecated"
	KeySunset    = "sunset"
	KeyDesc      = "desc"
	KeyDoc       = "doc"
	// KeyRepo is set by a workspace merge, never written by an author: the
	// repository a merged block was published from.
	KeyRepo = "repo"
	// KeyBranch is set by a workspace merge for blocks published from a
	// non-default branch (§9.2 `branch=`, Part VII "Ids and branches").
	KeyBranch = "branch"
)

// KeyValues lists the core-recognised def keys so a linter can warn on
// unknown ones without hardcoding the list twice.
var KeyValues = []string{KeyID, KeyOwner, KeyTags, KeyStability, KeySpan, KeyPick, KeyType, KeyFile, KeyLocal, KeyEnv, KeySecret, KeySource, KeyFrom, KeyTruth, KeySync, KeyRunnable, KeyDeprecate, KeySunset, KeyDesc, KeyDoc}

// TrueValue is the only spelling of a boolean key (`secret=true`). Anything
// else is false; the spec does not infer booleans from shape (§9.1).
const TrueValue = "true"

// Stability returns the def's policy, defaulting per spec. An invalid value
// is reported by the scanner as a finding; here it falls back to the default
// so downstream code never sees an unknown policy.
func (b Block) Stability() Stability {
	if s, ok := ParseStability(b.Args[KeyStability]); ok {
		return s
	}
	return DefaultStability
}

// Owner returns `owner=` or "".
func (b Block) Owner() string { return b.Args[KeyOwner] }

// Env returns `env=` or "".
func (b Block) Env() string { return b.Args[KeyEnv] }

// From returns `from=` or "".
func (b Block) From() string { return b.Args[KeyFrom] }

// IsSecret reports `secret=true`.
func (b Block) IsSecret() bool { return b.Args[KeySecret] == TrueValue }

// IsTruth reports `truth=true`.
func (b Block) IsTruth() bool { return b.Args[KeyTruth] == TrueValue }

// secretAddressRE matches the reference shapes a secret provider uses (§12):
// a GitHub Actions secret expression, a 1Password reference, an AWS Secrets
// Manager ARN, a GCP Secret Manager resource and a Vault path. Each names
// WHERE a secret lives; none can hold its value.
var secretAddressRE = regexp.MustCompile(`\$\{\{\s*secrets\.[A-Za-z_]\w*\s*\}\}|\bop://\S+|\barn:aws:secretsmanager:\S+|\bprojects/[^/\s]+/secrets/[^/\s]+|\bvault:\S+`)

// SecretAddress reports text that holds a secret's address rather than its
// value, and so is safe to show.
//
// Why it exists: the spec promises the tool "never stores, renders, or logs a
// value" and calls an address "safe to hash and render". Nothing enforced the
// difference, so `secret=true` on a line holding a real credential -- or any
// line in a `[secret] paths` file, which holds values by definition -- was
// printed by render, facts, context, read and export alike. A bare uppercase
// name is deliberately NOT treated as an address: an AWS access key id is
// uppercase too, and the spec already asks for `source=` on a bare env name.
func SecretAddress(s string) bool { return secretAddressRE.MatchString(s) }

// IsLocal reports `local=true`.
func (b Block) IsLocal() bool { return b.Args[KeyLocal] == TrueValue }

// IsRunnable reports `runnable=true`.
func (b Block) IsRunnable() bool { return b.Args[KeyRunnable] == TrueValue }

// Tags returns the comma list from `tags=`.
func (b Block) Tags() []string {
	return directive.Directive{Args: b.Args}.List(KeyTags)
}

// SetContent stores the body and recomputes the hash. It is the only way the
// hash should be set so Content and Hash can never disagree.
func (b *Block) SetContent(content string) {
	b.Content = content
	b.Hash = textnorm.Hash([]byte(content))
}

// SetContentHashed stores content but hashes hashed instead: grammar tiers
// hash the token stream so a formatter's whitespace never counts as a
// change (Part VII "What changed"), while Content stays the source as
// written for rendering. hashed goes through the same normalisation, so a
// tier that passes content itself gets exactly SetContent.
func (b *Block) SetContentHashed(content, hashed string) {
	b.Content = content
	b.Hash = textnorm.Hash([]byte(hashed))
}

// Reference is one use of a defined id: a `ds:block`, `ds:cfg`, `ds:run`, or
// any other referring verb, wherever it sat.
type Reference struct {
	// Verb is the directive verb, e.g. "block".
	Verb string `json:"verb"`
	// ID is the referenced id, or "" for verbs that do not point at a def
	// (`table`, `claim`, `url`, and `run cmd=`).
	ID string `json:"id,omitempty"`
	// Pos is the line the reference sits on. For a link it is the line of the
	// enclosing sentence, which is what findings attach to.
	Pos Position `json:"pos"`
	// Carrier is link, comment, or block position.
	Carrier Carrier `json:"carrier"`
	// Sentence is the enclosing sentence, list item, or table cell (§18), or
	// "" for block-position references, which have no prose around them.
	Sentence string `json:"sentence,omitempty"`
	// SentenceHash is textnorm.Hash(Sentence), what an ack is recorded against.
	SentenceHash string `json:"sentence_hash,omitempty"`
	// Args holds every key from the directive verbatim.
	Args map[string]string `json:"args,omitempty"`
	// Region is the repo-mode fence that follows a block-position
	// reference (§9.2 include.mode = repo), or nil in build mode.
	Region *Region `json:"region,omitempty"`
}

// Region is a rendered copy kept in the document between a block-position
// directive and its closer `<!-- /ds:block hash=… -->`. Hash is the short
// block hash the copy was rendered from; Text is the copy itself.
type Region struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Hash  string `json:"hash"`
	Text  string `json:"-"`
}

// SetSentence stores the enclosing prose and its hash together.
func (r *Reference) SetSentence(s string) {
	r.Sentence = s
	if s == "" {
		r.SentenceHash = ""
		return
	}
	r.SentenceHash = textnorm.Hash([]byte(s))
}

// Directive reconstructs the parsed directive for a reference, for verbs that
// want the typed accessors.
func (r Reference) Directive() directive.Directive {
	return directive.Directive{Verb: r.Verb, Args: r.Args}
}
