package docsync

import (
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/id"
	"github.com/ubgo/docsync/internal/glob"
	"github.com/ubgo/docsync/internal/textnorm"
)

// Edit is a proposed change to one source file: the library never writes
// (§37.2). It has three shapes, told apart by its fields:
//
//   - insert: Old empty -- New goes in as a new line before Line.
//   - replace: Old set -- that exact line becomes New.
//   - delete: Delete set -- the line Old at Line is removed, and Next is the
//     line that followed it.
//
// Apply refuses when the file no longer has Old at Line, so an edit computed
// against a stale read cannot land on the wrong line.
type Edit struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Old  string `json:"old,omitempty"`
	New  string `json:"new"`
	// Delete removes the line instead of rewriting it. It exists for the one
	// write that cannot be expressed as a replacement: taking a directive out
	// of a format with no comment syntax, where commenting it is impossible.
	Delete bool `json:"delete,omitempty"`
	// Next is the line that followed the deleted one when the edit was
	// computed, or "" when it was the last. It is what makes a delete
	// reversible: undo finds that line where the deleted one was, and only
	// then puts the deleted line back above it, so an edit made since is never
	// overwritten or misplaced.
	Next string `json:"next,omitempty"`
}

// IsZero reports an edit that changes nothing (Define found an existing id).
func (e Edit) IsZero() bool { return e.New == "" && !e.Delete }

// Apply performs the edit on src. Every line it does not touch comes back
// byte for byte, each keeping its own ending, and a leading byte order mark
// stays in front of line 1. A zero edit returns src unchanged.
func (e Edit) Apply(src []byte) ([]byte, error) {
	if e.IsZero() {
		return src, nil
	}
	// A byte order mark is set aside and put back: every reader strips it,
	// so an edit's Old never carries it, and inserting above line 1 must
	// not push the mark into the middle of the file.
	body := textnorm.TrimBOM(src)
	mark := string(src[:len(src)-len(body)])
	// Split on "\n" only, so each line keeps its own "\r" and every line
	// the edit does not touch is written back byte for byte. Normalising
	// CRLF to LF here rewrote the ending of every line of a Windows file.
	lines := strings.Split(string(body), "\n")
	if e.Line < 1 || e.Line > len(lines) {
		return nil, fmt.Errorf("%w: line %d of %d", ErrNotFound, e.Line, len(lines))
	}
	i := e.Line - 1
	switch {
	case e.Delete:
		if strings.TrimSuffix(lines[i], LineCR(lines[i])) != e.Old {
			return nil, fmt.Errorf("%w: line %d changed since the edit was computed", ErrNotFound, e.Line)
		}
		// The line after it must be the one recorded too; otherwise the file
		// has moved around the deletion and undo could not find its place.
		if i+1 < len(lines) && strings.TrimSuffix(lines[i+1], LineCR(lines[i+1])) != e.Next {
			return nil, fmt.Errorf("%w: the line after %d changed since the edit was computed", ErrNotFound, e.Line)
		}
		lines = append(lines[:i], lines[i+1:]...)
	case e.Old == "":
		lines = append(lines[:i], append([]string{e.New + insertedEnding(lines, i)}, lines[i:]...)...)
	default:
		cr := LineCR(lines[i])
		if strings.TrimSuffix(lines[i], cr) != e.Old {
			return nil, fmt.Errorf("%w: line %d changed since the edit was computed", ErrNotFound, e.Line)
		}
		lines[i] = e.New + cr
	}
	return []byte(mark + strings.Join(lines, "\n")), nil
}

// readSource reads a repository file the way the scanner does: a leading
// byte order mark is dropped, so a symbol, a line, or front matter found
// here is the one scan found. Line numbers are unchanged, since the mark
// sits in front of line 1; Edit.Apply puts it back on write.
func (s *System) readSource(path string) ([]byte, error) {
	b, err := fs.ReadFile(s.fsys, path)
	return textnorm.TrimBOM(b), err
}

// LineCR returns the "\r" a line split on "\n" ends with, or "". It is how
// a source write keeps each line's ending as it found it.
func LineCR(line string) string {
	if strings.HasSuffix(line, "\r") {
		return "\r"
	}
	return ""
}

// insertedEnding is the "\r" a line inserted before lines[i] should carry:
// the ending of the line it lands before, unless that is the file's last,
// unterminated line — then the ending of the line above it.
func insertedEnding(lines []string, i int) string {
	if i == len(lines)-1 && i > 0 {
		return LineCR(lines[i-1])
	}
	return LineCR(lines[i])
}

// mintFor mints the id a writer is about to bind to the block at file:line,
// with a suffix derived from the repository, the place, the file's bytes
// and any extra discriminator, never from a random draw. A dry run and the
// real run over the same tree therefore write the same id: `ds adopt
// --dry-run` printed ids the real run then replaced with others, so the
// preview could not be checked against the result (bug 33). Two different
// blocks differ in place or bytes, so they never share a seed. The label is
// left out, so relabelling keeps the suffix, which is the identity (§8).
func (s *System) mintFor(label, file string, line int, src []byte, extra ...string) (string, error) {
	seed := strings.Join(append([]string{s.repo, file, strconv.Itoa(line)}, extra...), "\x00") + "\x00" + string(src)
	return id.NewDerived(s.idcfg, label, []byte(seed))
}

// DefineOptions are the directive keys `ds def` may set (§9.1).
type DefineOptions struct {
	// Label is the id prefix; empty derives one from the symbol or file.
	Label     string
	Owner     string
	Stability string
	Tags      string
	Desc      string
	Env       string
}

// DefineResult is what Define returns.
type DefineResult struct {
	ID       string `json:"id"`
	Existing bool   `json:"existing"`
	// Block is the block as the scanning tier binds it. For a new def its
	// Pos is in the file as it was read, before Edit; DirectivePos is the
	// line Edit writes the directive at.
	Block block.Block `json:"block"`
	Edit  Edit        `json:"edit"`
}

// Define returns the id for `path#Symbol` or `path:line`, minting one and
// proposing the directive insertion when the block has none (§22 `ds def`).
// One def per block: an existing directive on that block is returned as is.
func (s *System) Define(_ context.Context, target string, opts DefineOptions) (DefineResult, error) {
	if _, err := s.envFor(opts.Env); err != nil {
		return DefineResult{}, err
	}
	path, tgt, err := extract.ParseTarget(target)
	if err != nil {
		return DefineResult{}, err
	}
	src, err := s.readSource(path)
	if err != nil {
		return DefineResult{}, err
	}
	tgt.Prefix = s.cfg.Prefix
	ex, err := s.registry.For(path)
	if err != nil {
		return DefineResult{}, err
	}
	existing := ex.Extract(path, src, s.cfg.Prefix).Defs
	// A line that is itself a def's directive names that def. Without this,
	// `ds def notes.txt:1` on a bare `ds:def` line located the directive line
	// as a block of its own and wrote a second directive above the first
	// (bug 58).
	if tgt.Symbol == "" {
		for _, d := range existing {
			if !d.Remote && d.Block.DirectivePos.Start <= tgt.Line && tgt.Line <= d.Block.DirectivePos.End && d.Block.Env() == opts.Env {
				return existingResult(d, path), nil
			}
		}
	}
	located, err := locateTarget(ex, path, src, tgt)
	if err != nil {
		return DefineResult{}, err
	}
	located.Pos.File = path
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	span, err := rangeSpan(ex, path, src, lines, located, tgt, s.cfg.Prefix)
	if err != nil {
		return DefineResult{}, err
	}
	for _, d := range existing {
		if d.Block.Pos.Start == located.Pos.Start && d.Block.Env() == opts.Env && (tgt.End == 0 || d.Block.Pos.End == tgt.End) {
			return existingResult(d, path), nil
		}
	}
	if err := s.refuseGenerated(path); err != nil {
		return DefineResult{}, err
	}
	label := opts.Label
	if label == "" {
		label = id.Slug(located.Symbol)
	}
	if label == "" {
		// The line matcher here names fewer things than the tier that will
		// scan the file: an object-literal property, a member reached by line
		// number. Ask that tier, so the id reads `server-port-…` rather than
		// the file name -- an id is how a reader greps for the block.
		if b, ok := probeAt(ex, path, src, lines, located, s.cfg.Prefix, ""); ok {
			label = id.Slug(b.Symbol)
		}
	}
	if label == "" {
		label = fileLabel(path)
	}
	newID, err := s.mintFor(label, path, located.Pos.Start, src, opts.Env)
	if err != nil {
		return DefineResult{}, err
	}
	d := directive.Directive{Verb: extract.VerbDef, Args: map[string]string{}}
	add := func(k, v string) {
		if v != "" {
			d.Args[k] = v
			d.Keys = append(d.Keys, k)
		}
	}
	add(block.KeyID, newID)
	add(block.KeyOwner, opts.Owner)
	add(block.KeyStability, opts.Stability)
	add(block.KeyTags, opts.Tags)
	add(block.KeyEnv, opts.Env)
	add(block.KeyDesc, opts.Desc)
	add(block.KeySpan, span)
	text, err := directive.Format(s.cfg.Prefix, d)
	if err != nil {
		return DefineResult{}, err
	}
	edit, err := directiveEdit(path, lines, located, text)
	if err != nil {
		return DefineResult{}, err
	}
	bound, err := verifyBinds(ex, path, src, edit, newID, s.cfg.Prefix)
	if err != nil {
		return DefineResult{}, err
	}
	bound.Pos.File, bound.DirectivePos.File = path, path
	return DefineResult{ID: newID, Block: bound, Edit: edit}, nil
}

// existingResult is Define's answer for a block that already has a def.
func existingResult(d extract.Def, path string) DefineResult {
	d.Block.Pos.File, d.Block.DirectivePos.File = path, path
	return DefineResult{ID: d.Block.ID, Existing: true, Block: d.Block}
}

// fileLabel is the id label a block with no symbol takes from its file: the
// base name without its extension, or the whole base name when that leaves
// nothing. A dotfile is all extension to Ext -- `.gitignore` -- and used to
// leave an empty label, so `ds def .gitignore:1` failed to mint at all until
// given --label (bug 57).
func fileLabel(p string) string {
	if l := id.Slug(strings.TrimSuffix(baseName(p), extract.Ext(p))); l != "" {
		return l
	}
	return id.Slug(baseName(p))
}

// applyInMemory applies an edit that directiveEdit built from these same bytes.
// Apply fails only for a line out of range or a replaced line that no longer
// matches, and neither can happen when the edit was computed from src a moment
// ago, so the error is discarded. It is safe in the other direction too: were
// it ever to fail, the result is empty, verifyBinds then finds no directive and
// refuses, and nothing is written. A check that fails closed does not need an
// error branch it could never take.
func applyInMemory(edit Edit, src []byte) []byte {
	after, _ := edit.Apply(src)
	return after
}

// probeID is a well-formed id used only to ask an extractor what it would name
// a block; it never reaches a file. Every symbol is from the suffix alphabet.
const probeID = "probe-23456789"

// probeAt returns the block the scanning tier binds when a def is written for
// located, by writing a directive with probeID (and span, when not "") in
// memory and extracting the result. Positions are mapped back to src, so they
// can be compared with lines the caller read there. false when there is no
// carrier or the probe binds nothing.
//
// It is how Define asks the tier that will scan the file rather than guessing
// for it: what a symbol is called, where a block ends, and whether a span
// gives the range the caller asked for are all the tier's answers, and every
// line-matching guess in this module disagreed with some tier somewhere.
func probeAt(ex extract.Extractor, path string, src []byte, lines []string, located block.Block, prefix, span string) (block.Block, bool) {
	d := directive.Directive{Verb: extract.VerbDef, Args: map[string]string{block.KeyID: probeID}, Keys: []string{block.KeyID}}
	if span != "" {
		d.Args[block.KeySpan] = span
		d.Keys = append(d.Keys, block.KeySpan)
	}
	// Format fails only on a malformed directive; this one is a constant with
	// a well-formed id under the configured prefix, which New validated.
	text, _ := directive.Format(prefix, d)
	edit, err := directiveEdit(path, lines, located, text)
	if err != nil {
		// No carrier: there is nothing to ask. Define's own call returns the
		// refusal; here the caller falls back.
		return block.Block{}, false
	}
	for _, def := range ex.Extract(path, applyInMemory(edit, src), prefix).Defs {
		if def.Block.ID == probeID && def.Block.Pos.Start > 0 {
			return unshift(def.Block, edit), true
		}
	}
	return block.Block{}, false
}

// unshift maps a block extracted from src-with-edit back to src's lines: an
// inserted directive pushed every line from edit.Line down by one. A trailing
// directive replaced a line in place and moved nothing.
func unshift(b block.Block, edit Edit) block.Block {
	if edit.Old != "" {
		return b
	}
	at := func(n int) int {
		if n >= edit.Line {
			return n - 1
		}
		return n
	}
	b.Pos.Start, b.Pos.End = at(b.Pos.Start), at(b.Pos.End)
	b.DirectivePos = block.Position{File: b.DirectivePos.File, Start: edit.Line, End: edit.Line}
	return b
}

// symbolMatch ranks how well a tier's symbol answers the name a caller asked
// for. Exact wins; then a member asked for by its own name (`MinLength` for
// `Limits.MinLength`), which is how findDeclaration has always matched; then
// the same path with its quoting removed, so `db.port` finds the properties
// key the config tier spells `"db.port"` because the key itself holds a dot.
type symbolMatch int

const (
	matchNone symbolMatch = iota
	matchUnquoted
	matchSuffix
	matchExact
)

func matchSymbol(got, want string) symbolMatch {
	unq := func(s string) string { return strings.ReplaceAll(s, `"`, "") }
	switch {
	case got == "":
		return matchNone
	case got == want:
		return matchExact
	case strings.HasSuffix(got, "."+want):
		return matchSuffix
	case unq(got) == unq(want):
		return matchUnquoted
	}
	return matchNone
}

// locateTarget resolves a target to the block a def written for it binds,
// asking the extractor that scans the file (bugs 41, 42 and 48).
//
// A line target binds from that line as it always has. A symbol is looked up
// in steps. The line matcher's answer is put to the scanning tier first: a
// directive is written above it in memory and the tier says what the block
// there is called. Unless that is the name exactly, each other line that
// mentions the symbol's last segment is tried the same way (a def already
// there is found too: the probe goes below its directive and binds the same
// block), and a line replaces the
// answer only by matching better -- exact, then member-by-name, then
// unquoted -- so the earliest best match wins. Last, the line matcher's
// answer is kept when no tier name matches at all (a markdown heading found
// without regard to case), because verifyBinds still refuses it if the tier
// cannot bind there.
//
// Why: `path#Name` used to be resolved by a line matcher in this module, while
// the scan binds with tree-sitter or a real parser. `Config.retries` in Python,
// `server.port` in a TypeScript object, `variable.region` in HCL, a TOML
// table and a POSIX shell function were all recorded in the ledger under those
// names and none of them could be defined by name. The probe makes the name a
// scan records the name `ds def` accepts, for every tier, present and future.
func locateTarget(ex extract.Extractor, path string, src []byte, tgt extract.Target) (block.Block, error) {
	heuristic, herr := extract.Locate(path, src, tgt)
	if tgt.Symbol == "" {
		return heuristic, herr
	}
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	segs := strings.Split(strings.ReplaceAll(tgt.Symbol, `"`, ""), ".")
	last := segs[len(segs)-1]
	var best block.Block
	rank := matchNone
	seen := map[int]bool{}
	// The line matcher's own answer goes first, and a later line replaces it
	// only by matching better: a member asked for by its bare name (`Check`)
	// keeps resolving to the declaration it always did rather than to
	// whichever same-named member comes first in the file.
	if herr == nil {
		if p, ok := probeAt(ex, path, src, lines, heuristic, tgt.Prefix, ""); ok {
			if rank = matchSymbol(p.Symbol, tgt.Symbol); rank != matchNone {
				// The block keeps the line matcher's name for its label, as
				// it always has, so ids minted for names that already worked
				// read as they did. (Define asks the tier for a label only
				// when a block has no name at all.)
				best = heuristic
			}
		}
		seen[heuristic.Pos.Start] = true
	}
	for i := 0; i < len(lines) && rank != matchExact; i++ {
		l := lines[i]
		if !mentions(l, last) {
			continue
		}
		b, err := extract.Locate(path, src, extract.Target{Line: i + 1, Prefix: tgt.Prefix})
		if err != nil || seen[b.Pos.Start] {
			continue
		}
		seen[b.Pos.Start] = true
		p, ok := probeAt(ex, path, src, lines, b, tgt.Prefix, "")
		if !ok {
			continue
		}
		if r := matchSymbol(p.Symbol, tgt.Symbol); r > rank {
			b.Symbol = p.Symbol
			best, rank = b, r
		}
	}
	if rank != matchNone {
		return best, nil
	}
	if herr != nil {
		return block.Block{}, fmt.Errorf("%w (the %s tier names no block %q in %s)", herr, ex.Name(), tgt.Symbol, path)
	}
	return heuristic, nil
}

// locateSymbol is locateTarget for a caller that has not looked up the tier:
// adopt resolving a `path#Name` link.
func (s *System) locateSymbol(path string, src []byte, tgt extract.Target) (block.Block, error) {
	ex, err := s.registry.For(path)
	if err != nil {
		return block.Block{}, err
	}
	return locateTarget(ex, path, src, tgt)
}

// mentions reports whether line holds name as a whole word: not inside a
// longer identifier. It only picks the lines worth asking the tier about, so
// it errs towards yes; a phrase (a markdown heading) is matched as text.
func mentions(line, name string) bool {
	if name == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(line[i:], name)
		if j < 0 {
			return false
		}
		at := i + j
		end := at + len(name)
		if (at == 0 || !identByte(line[at-1])) && (end == len(line) || !identByte(line[end])) {
			return true
		}
		i = at + 1
	}
}

func identByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// rangeSpan returns the `span=` a def needs to bind exactly the lines of a
// `path:start-end` target, "" for any other target or when the block already
// ends there, and an error when no span makes the tier bind that range.
//
// A span means different things in different tiers -- lines after the
// directive in plain text, lines after the bound line elsewhere -- so the
// value is found by asking the tier, smallest first, never computed here. The
// range used to be read as its first line, silently (bug 43).
func rangeSpan(ex extract.Extractor, path string, src []byte, lines []string, located block.Block, tgt extract.Target, prefix string) (string, error) {
	if tgt.End == 0 {
		return "", nil
	}
	want := block.Position{Start: tgt.Line, End: tgt.End}
	if b, ok := probeAt(ex, path, src, lines, located, prefix, ""); ok && b.Pos.Start == want.Start && b.Pos.End == want.End {
		return "", nil
	}
	for n := 0; n <= want.End-want.Start+1; n++ {
		span := "+" + strconv.Itoa(n)
		if b, ok := probeAt(ex, path, src, lines, located, prefix, span); ok && b.Pos.Start == want.Start && b.Pos.End == want.End {
			return span, nil
		}
	}
	return "", fmt.Errorf("%w: the %s tier cannot bind exactly lines %d-%d of %s (a def there starts at line %d); use %s:%d, or a range that starts where a block does", ErrRange, ex.Name(), want.Start, want.End, path, located.Pos.Start, path, located.Pos.Start)
}

// verifyBinds applies the edit in memory and extracts the result with the same
// extractor a scan will use, and refuses unless the new id binds cleanly.
//
// Why it exists: Define locates its target with the line matcher in this
// module, and a scan binds with whatever tier owns the file -- tree-sitter for
// Go and TypeScript. When the two disagree the directive was written and only
// the NEXT scan said "nothing to bind", after the user's file had already been
// edited. A TypeScript object-literal property did exactly that. Checking here
// makes every writer's promise the same as the scan's, and because Define runs
// before anything reaches disk, `--dry-run` predicts the refusal instead of the
// scan discovering it.
//
// It is tier-independent on purpose: whatever construct the next grammar gap
// is, the answer is a refusal naming the extractor's own reason, never a
// directive that cannot bind.
//
// It also refuses a block that would cross another def's block without one
// containing the other (bug 58): nesting is how a method sits in a class and a
// subsection in a section, but two blocks that each hold part of the other
// change together for edits that concern only one of them.
//
// It returns the block as the tier bound it, positioned in src.
func verifyBinds(ex extract.Extractor, path string, src []byte, edit Edit, newID, prefix string) (block.Block, error) {
	found := ex.Extract(path, applyInMemory(edit, src), prefix)
	// A problem on the directive's own line is the extractor explaining why it
	// could not bind; that reason is more useful than any this function could
	// write, so it is passed through.
	for _, pr := range found.Problems {
		if pr.Pos.Start <= edit.Line && edit.Line <= pr.Pos.End {
			return block.Block{}, fmt.Errorf("%w: %v", ErrWouldNotBind, pr.Err)
		}
	}
	for _, d := range found.Defs {
		if d.Block.ID != newID {
			continue
		}
		for _, o := range found.Defs {
			if o.Block.ID != newID && block.Crosses(d.Block.Pos, o.Block.Pos) {
				n, b := unshift(d.Block, edit).Pos, unshift(o.Block, edit).Pos
				return block.Block{}, fmt.Errorf("%w: lines %d-%d would overlap %s at %d-%d without either containing the other", ErrWouldNotBind, n.Start, n.End, o.Block.ID, b.Start, b.End)
			}
		}
		return unshift(d.Block, edit), nil
	}
	return block.Block{}, fmt.Errorf("%w: %s reads no directive at %s:%d", ErrWouldNotBind, ex.Name(), path, edit.Line)
}

// directiveEdit places the directive in the host's comment style: trailing on
// the line for one-line config values, above the block otherwise, with the
// block's indentation.
//
// It is the ONE place a directive is turned into a line of source, so it is
// the one place that can refuse. Every writer -- def, adopt, anything added
// later -- goes through it, and a file type with no carrier in
// extract.Styles gets ErrNoCarrier rather than a line.
//
// Why it refuses instead of falling back: it used to write the directive
// bare, with no comment prefix, whenever the type was unknown. That is not a
// degraded result, it is invalid syntax written into the user's file. A bare
// `ds:def` line put into a go.work stopped every build in that workspace with
// "unknown directive", and the same call on a .json would have left the file
// unparseable -- JSON has no comment syntax at all, so there is nothing to
// fall back TO. A tool that keeps docs honest must not corrupt the source it
// describes.
// refuseGenerated returns ErrGeneratedPath when p matches [scan] generated.
// An id that already exists there is still returned by Define: reading one
// writes nothing, and scan reports it.
func (s *System) refuseGenerated(p string) error {
	generated, err := glob.CompileAll(s.cfg.Scan.Generated)
	if err != nil {
		return err
	}
	if generated.MatchAny(p) {
		return fmt.Errorf("%w: %s matches [scan] generated; define the block in the file it is generated from, or cite it with a remote def", ErrGeneratedPath, p)
	}
	return nil
}

func directiveEdit(p string, lines []string, b block.Block, text string) (Edit, error) {
	first := lines[b.Pos.Start-1]
	indent := first[:len(first)-len(strings.TrimLeft(first, " \t"))]
	st, ok := extract.StyleFor(p)
	// One refusal, covering both "no entry" and an entry that declares no way
	// to carry anything. Written as a single condition rather than a branch at
	// the end of the switch so there is no path that falls through to writing
	// a bare line -- that fall-through is the bug this exists to remove.
	if !ok || (len(st.Line) == 0 && st.BlockOpen == "" && !st.Bare) {
		// No setting adds a comment syntax: the carrier table is built in, so
		// the remedy must not point at one. It used to name a [scan] key that
		// does not exist (bug 56).
		return Edit{}, fmt.Errorf("%w: %s has no comment syntax docsync knows, so a directive cannot be written into it; bind it from a file that does with a remote def (`file=%s pick=…`). The comment syntaxes are built in, not configured: if this type does take comments, it needs an entry in docsync's carrier table", ErrNoCarrier, carrierName(p), p)
	}
	switch {
	case b.Kind == block.KindKey && len(st.Line) > 0 && st.Trailing:
		// Only where the format's own parser reads a trailing comment as one
		// (bug 40); everywhere else the key's directive goes above it.
		return Edit{File: p, Line: b.Pos.Start, Old: first, New: strings.TrimRight(first, " \t") + "   " + st.Line[0] + " " + text}, nil
	case len(st.Line) > 0:
		return Edit{File: p, Line: b.Pos.Start, New: indent + st.Line[0] + " " + text}, nil
	case st.BlockOpen != "":
		return Edit{File: p, Line: b.Pos.Start, New: indent + st.BlockOpen + " " + text + " " + st.BlockClose}, nil
	}
	// Bare: plain text, where the directive is the line, which is what the
	// text tier reads back. Declared per type, never assumed.
	return Edit{File: p, Line: b.Pos.Start, New: indent + text}, nil
}

// carrierName is how the refusal names a file type: its base name when that is
// what decides the syntax, else its extension, so the message points at the
// thing the reader would look up.
func carrierName(p string) string {
	if e := extract.Ext(p); strings.HasPrefix(e, ".") {
		return e
	}
	return baseName(p)
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
