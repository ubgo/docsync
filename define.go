package docsync

import (
	"context"
	"fmt"
	"io/fs"
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
	ID       string      `json:"id"`
	Existing bool        `json:"existing"`
	Block    block.Block `json:"block"`
	Edit     Edit        `json:"edit"`
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
	located, err := extract.Locate(path, src, tgt)
	if err != nil {
		return DefineResult{}, err
	}
	located.Pos.File = path
	ex, err := s.registry.For(path)
	if err != nil {
		return DefineResult{}, err
	}
	for _, d := range ex.Extract(path, src, s.cfg.Prefix).Defs {
		if d.Block.Pos.Start == located.Pos.Start && d.Block.Env() == opts.Env {
			d.Block.Pos.File, d.Block.DirectivePos.File = path, path
			return DefineResult{ID: d.Block.ID, Existing: true, Block: d.Block}, nil
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
		label = id.Slug(scanSymbol(ex, path, src, located, s.cfg.Prefix))
	}
	if label == "" {
		label = id.Slug(strings.TrimSuffix(baseName(path), extract.Ext(path)))
	}
	newID, err := id.New(s.idcfg, label)
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
	text, err := directive.Format(s.cfg.Prefix, d)
	if err != nil {
		return DefineResult{}, err
	}
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	edit, err := directiveEdit(path, lines, located, text)
	if err != nil {
		return DefineResult{}, err
	}
	if err := verifyBinds(ex, path, src, edit, newID, s.cfg.Prefix); err != nil {
		return DefineResult{}, err
	}
	located.ID = newID
	located.Args = d.Args
	return DefineResult{ID: newID, Block: located, Edit: edit}, nil
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

// scanSymbol returns the symbol the scanning tier gives the block at located,
// by writing a directive with probeID in memory and extracting the result. ""
// when it names nothing or cannot bind there, in which case Define falls back
// to the file name and verifyBinds decides whether to refuse.
func scanSymbol(ex extract.Extractor, path string, src []byte, located block.Block, prefix string) string {
	d := directive.Directive{Verb: extract.VerbDef, Args: map[string]string{block.KeyID: probeID}, Keys: []string{block.KeyID}}
	// Format fails only on a malformed directive; this one is a constant with
	// a well-formed id under the configured prefix, which New validated.
	text, _ := directive.Format(prefix, d)
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	edit, err := directiveEdit(path, lines, located, text)
	if err != nil {
		// No carrier: there is nothing to ask. Define's own call returns the
		// refusal; here the label simply falls back to the file name.
		return ""
	}
	for _, def := range ex.Extract(path, applyInMemory(edit, src), prefix).Defs {
		if def.Block.ID == probeID {
			return def.Block.Symbol
		}
	}
	return ""
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
func verifyBinds(ex extract.Extractor, path string, src []byte, edit Edit, newID, prefix string) error {
	found := ex.Extract(path, applyInMemory(edit, src), prefix)
	// A problem on the directive's own line is the extractor explaining why it
	// could not bind; that reason is more useful than any this function could
	// write, so it is passed through.
	for _, pr := range found.Problems {
		if pr.Pos.Start <= edit.Line && edit.Line <= pr.Pos.End {
			return fmt.Errorf("%w: %v", ErrWouldNotBind, pr.Err)
		}
	}
	for _, d := range found.Defs {
		if d.Block.ID == newID {
			return nil
		}
	}
	return fmt.Errorf("%w: %s reads no directive at %s:%d", ErrWouldNotBind, ex.Name(), path, edit.Line)
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
		return Edit{}, fmt.Errorf("%w: %s has no comment syntax docsync knows, so a directive cannot be written into it; bind it from a file that does with a remote def (`file=%s pick=…`), or add the type to [scan] if it does have comments", ErrNoCarrier, carrierName(p), p)
	}
	switch {
	case b.Kind == block.KindKey && len(st.Line) > 0:
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
