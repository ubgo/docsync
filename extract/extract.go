// Package extract finds directives in a file and binds `ds:def` directives to
// the block they define (docs/SPEC.md §7.1, §10).
//
// An Extractor owns one tier: it knows the comment syntax of its file types,
// how to walk them, and what a def immediately above (or beside) a line binds
// to. The root module ships the tiers that need no dependency: plain text,
// config formats, markdown and html, and a heuristic code binder. A grammar
// tier (tree-sitter) lives in ext/treesitter and registers ahead of the
// heuristic one for the languages it supports.
//
// Every extractor returns the same Found value: bound Defs, References, and
// Problems (a malformed directive, a def with nothing to bind to). Nothing here
// touches the filesystem or knows about the ledger; the scanner feeds bytes in
// and merges results.
package extract

import (
	"errors"
	"path"
	"sort"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
)

// Verb names the core recognises at extraction time. Only `def` changes how
// a file is read (it binds); every other verb is recorded as a Reference and
// interpreted later. The list exists so extractors and the scanner spell them
// identically.
const (
	VerbDef   = "def"
	VerbBlock = "block"
	VerbCfg   = "cfg"
	VerbRun   = "run"
	VerbTable = "table"
	VerbClaim = "claim"
	VerbURL   = "url"
	VerbChain = "chain"
)

// BuiltinVerbs is the canonical list from the spec (§9).
var BuiltinVerbs = []string{VerbDef, VerbBlock, VerbCfg, VerbRun, VerbTable, VerbClaim, VerbURL, VerbChain}

// Def is a bound definition: the directive as written plus the block it
// produced. Remote is set when the def carries `file=` and points elsewhere;
// the extractor cannot bind it (it has no access to other files) and leaves
// Block.Pos empty for the scanner to resolve.
type Def struct {
	Directive directive.Directive
	Block     block.Block
	Remote    bool
}

// Ref is a reference occurrence with its parsed directive.
type Ref struct {
	Directive directive.Directive
	Reference block.Reference
}

// Problem is a directive the extractor could not use: a parse error, a def
// with no id, a def with nothing after it. It becomes a finding with the file
// and line so an author can fix it; extraction never aborts a file for one bad
// line.
type Problem struct {
	Pos block.Position
	Err error
}

// Sentinel problem errors.
var (
	ErrNoID          = errors.New("extract: ds:def without id=")
	ErrNothingToBind = errors.New("extract: ds:def has nothing after it to bind to")
	// ErrSkippedCode is raised when the block a def would bind does not begin
	// on the first code line below the directive, so real code sits between
	// them. It exists because the alternative is silent and worse: a grammar
	// that does not recognise the construct under the directive used to walk
	// on and bind the NEXT declaration it did recognise, and the sentence
	// citing the def was then measured against something its author never
	// looked at (bug 18). An unbindable directive must be a finding the author
	// sees, not a binding to whatever came next.
	ErrSkippedCode = errors.New("extract: ds:def cannot bind the declaration below it")
	ErrBadSpan     = errors.New("extract: span= must be +N with N >= 0")
	ErrDefOnImage  = errors.New("extract: ds:def on an image link defines nothing; use a text link")
	ErrNoExtractor = errors.New("extract: no extractor matches this path")
)

// Found is everything one extractor produced for one file.
type Found struct {
	Defs     []Def
	Refs     []Ref
	Problems []Problem
	// Page carries page-level declarations from markdown frontmatter (§13):
	// the ids this page covers and its review interval. Nil for non-pages.
	Page *Page
}

// Page is the `ds:` block of a markdown page's frontmatter.
type Page struct {
	Covers      []string
	ReviewEvery string
}

// Extractor is the capability interface for a tier (§37.3). Match decides by
// path; Extract never returns an error for content problems (those are
// Problems), only for misuse such as an invalid prefix.
type Extractor interface {
	// Name is a short identifier for reports, e.g. "text", "config", "markdown".
	Name() string
	// Match reports whether this extractor handles the path.
	Match(path string) bool
	// Extract scans src, which is the whole file, with the workspace prefix.
	Extract(path string, src []byte, prefix string) Found
}

// Registry is an ordered list of extractors; the first whose Match returns
// true wins. Order is therefore precedence: a grammar tier is registered
// before the heuristic code tier for the same extensions.
type Registry struct {
	extractors []Extractor
}

// NewRegistry builds a registry from extractors in precedence order.
func NewRegistry(extractors ...Extractor) *Registry {
	return &Registry{extractors: extractors}
}

// Add appends an extractor at the lowest precedence.
func (r *Registry) Add(e Extractor) { r.extractors = append(r.extractors, e) }

// Prepend inserts an extractor at the highest precedence.
func (r *Registry) Prepend(e Extractor) {
	r.extractors = append([]Extractor{e}, r.extractors...)
}

// For returns the extractor for a path or ErrNoExtractor.
func (r *Registry) For(p string) (Extractor, error) {
	for _, e := range r.extractors {
		if e.Match(p) {
			return e, nil
		}
	}
	return nil, ErrNoExtractor
}

// Names lists registered extractors in precedence order, for `ds doctor`.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.extractors))
	for _, e := range r.extractors {
		out = append(out, e.Name())
	}
	return out
}

// Default returns the root module's tiers in precedence order: markdown and
// html, config formats, the heuristic code binder, then plain text as the
// universal fallback. The fallback matches every path, so For never fails
// with the default registry; a caller that wants "unknown files are skipped"
// filters by scan globs first.
func Default() *Registry {
	return NewRegistry(Markdown{}, Document{}, Config{}, Code{}, Text{})
}

// Ext returns the lowercase extension of p including the dot, or the lowercase
// base name for extensionless files such as `Dockerfile`, so tables can key on
// either.
func Ext(p string) string {
	b := path.Base(p)
	if e := path.Ext(b); e != "" {
		return strings.ToLower(e)
	}
	return strings.ToLower(b)
}

// extIn is a helper for Match implementations: true when Ext(p) is in the set.
func extIn(p string, set map[string]bool) bool {
	return set[Ext(p)]
}

// Extensions returns the sorted extensions and base names a set of tiers
// claims; `ds doctor` prints it so a team can see which files are read by
// which tier without reading source.
func Extensions() map[string][]string {
	return map[string][]string{
		Markdown{}.Name(): sortedKeys(markdownExts, htmlExts),
		Document{}.Name(): sortedKeys(documentExts),
		Config{}.Name():   sortedKeys(configExts),
		Code{}.Name():     sortedKeys(codeExts),
	}
}

// sortedKeys returns the union of map keys, sorted.
func sortedKeys(ms ...map[string]bool) []string {
	var out []string
	for _, m := range ms {
		for k := range m {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// requireID pulls `id=` from a def directive or records a problem. It is the
// one check every tier shares.
func requireID(d directive.Directive, pos block.Position, f *Found) (string, bool) {
	id, ok := d.Get(block.KeyID)
	if !ok || id == "" {
		f.Problems = append(f.Problems, Problem{Pos: pos, Err: ErrNoID})
		return "", false
	}
	return id, true
}

// parseSpan reads `span=+N`. Absent returns (0,false,nil).
func parseSpan(d directive.Directive) (int, bool, error) {
	v, ok := d.Get(block.KeySpan)
	if !ok {
		return 0, false, nil
	}
	if !strings.HasPrefix(v, "+") {
		return 0, true, ErrBadSpan
	}
	n := 0
	for _, c := range v[1:] {
		if c < '0' || c > '9' {
			return 0, true, ErrBadSpan
		}
		n = n*10 + int(c-'0')
	}
	if len(v) == 1 {
		return 0, true, ErrBadSpan
	}
	return n, true, nil
}

// newDef assembles a Def from its parts and computes the hash.
func newDef(d directive.Directive, id string, kind block.Kind, symbol string, pos, dpos block.Position, carrier block.Carrier, content string) Def {
	b := block.Block{ID: id, Kind: kind, Symbol: symbol, Pos: pos, DirectivePos: dpos, Carrier: carrier, Args: d.Args}
	b.SetContent(content)
	return Def{Directive: d, Block: b}
}

// spanDef builds a def over lines start..end. Content is the lines as
// written, because that is what renders; the hash is taken over them with
// the standalone carriers inside the range left out (hashedJoin).
func spanDef(d directive.Directive, id string, kind block.Kind, symbol string, pos, dpos block.Position, carrier block.Carrier, lines []string, carriers map[int]bool) Def {
	def := newDef(d, id, kind, symbol, pos, dpos, carrier, join(lines, pos.Start, pos.End))
	def.Block.SetContentHashed(def.Block.Content, hashedJoin(lines, pos.Start, pos.End, carriers))
	return def
}

// spanEnd returns the last line of a `span=+N` block that starts at start
// and holds want content lines, clamped to the file.
//
// Content lines, not lines: a standalone carrier inside the span is
// metadata, and counting it would let `ds def` anchoring something inside a
// span-bound block push that block's last real line out of the window —
// changing its hash, and flagging every sentence citing it, with no text
// changed. Each tier keeps its documented base: grammarless files count N
// lines after the directive, structured tiers the bound line plus N.
func spanEnd(lines []string, start, want int, carriers map[int]bool) int {
	end := start - 1
	for got := 0; got < want && end < len(lines); {
		end++
		if !carriers[end] {
			got++
		}
	}
	return end
}

// hashedJoin re-joins a 1-based inclusive range the way a block's hash sees
// it: without the standalone directive carriers inside it.
//
// Why: a directive is metadata about text, never text. trimCarriers keeps
// the carrier that anchors the next block out of a block's extent, but a
// carrier can also sit inside one — `ds def` anchoring `### Details` writes
// its carrier inside the `## Overview` section that contains it, a YAML
// child key's carrier sits inside its parent map, and a comment cite can sit
// in the middle of a paragraph. Hashing those made anchoring a subsection
// re-hash every section that contains it, and flag every sentence citing
// them, with no text changed. Range clamping matches join.
func hashedJoin(lines []string, start, end int, carriers map[int]bool) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	var out []string
	for i := start; i <= end; i++ {
		if !carriers[i] {
			out = append(out, lines[i-1])
		}
	}
	return strings.Join(out, "\n")
}

// ProseTier is the optional upgrade an extractor implements when its files
// are prose. The scanner skips a file with a very long line as minified
// code, which is right for code and wrong for prose: a paragraph written on
// one line — the norm where markdown is not hard-wrapped — easily runs past
// the limit, and skipping the doc silently dropped every citation in it,
// along with any unreviewed change they carried.
type ProseTier interface {
	Prose() bool
}

// IsProse reports whether ex declared its files prose (ProseTier).
func IsProse(ex Extractor) bool {
	p, ok := ex.(ProseTier)
	return ok && p.Prose()
}

// isRemote reports whether a def points at another file.
func isRemote(d directive.Directive) bool {
	return d.Has(block.KeyFile)
}

// remoteDef records a def that the scanner must resolve against another file.
func remoteDef(d directive.Directive, id string, dpos block.Position, carrier block.Carrier) Def {
	return Def{Directive: d, Block: block.Block{ID: id, DirectivePos: dpos, Carrier: carrier, Args: d.Args}, Remote: true}
}

// Rule is the version of the extraction rules: what bytes a block's hash
// covers, and what prose a citation binds to. It is stamped into the ledger
// and refs headers and onto every ack, so a hash or a sentence is never read
// as comparable with one taken under different rules.
//
// Rule 1 is the first released set: a section stops before the next block's
// directive carrier, a standalone carrier inside a block is left out of its
// hash, and a citation binds to its whole markdown paragraph, where a
// sentence ends only before a capital, digit, quote or bracket, never after
// a listed abbreviation, with whitespace collapsed and HTML comments left
// out. Any later change to what those bytes cover bumps this by one.
//
// The extraction cache is keyed on it, so a bump can never be served results
// computed under the old rule, and each ack records it, so an ack made under
// another rule is never compared as if its sentence were bound the same way.
// Such an ack reports once (check.msgAckOtherRule) and is cleared by acking
// the sentence again.
// dsself:def id=rule-7y7umdv9 owner=@docsync stability=stable
const Rule = 1

// carrierLines marks every line occupied by a standalone directive carrier —
// a comment whose whole purpose is to anchor or cite a block.
//
// A trailing directive is excluded because it shares its line with real
// content: `port: 8081  # ds:def id=…` is one line that is both. Only a
// carrier that occupies a line by itself can be mistaken for body text.
func carrierLines(occs []occurrence) map[int]bool {
	out := map[int]bool{}
	for _, o := range occs {
		if o.trailing {
			continue
		}
		for i := o.pos.Start; i <= o.pos.End; i++ {
			out[i] = true
		}
	}
	return out
}

// trimCarriers walks a section's end back over any trailing run of blank
// lines and standalone directive carriers, and returns the new end.
//
// A section runs to the next heading, and `ds def` writes its carrier on the
// line before the heading it anchors — so without this the carrier for one
// block lands inside the previous block's extent and is hashed as part of
// its body. Anchoring a heading then changed the hash of the section above
// it and flagged every sentence citing that section, with nothing about it
// having changed. A directive is metadata about text, never text.
func trimCarriers(lines []string, start, end int, carrier map[int]bool) int {
	for end > start {
		switch {
		case carrier[end]:
		case strings.TrimSpace(lines[end-1]) == "":
		default:
			return end
		}
		end--
	}
	return end
}
