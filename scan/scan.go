// Package scan walks a file tree, hands each eligible file to the extractor
// registry, and merges the results into one Result with repo-relative
// positions, resolved remote defs, and duplicate-id detection (docs/SPEC.md
// §16 passes 1 and 2).
//
// It is a pure function over an fs.FS: no environment, no home directory, no
// network, no writes. The CLI decides what tree to hand in (usually
// os.DirFS(repo)) and what to do with the result. Because the input is an
// fs.FS, tests use fstest.MapFS and the conformance suite uses fixture
// directories, both without touching disk paths.
package scan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/id"
	"github.com/ubgo/docsync/internal/glob"
	"github.com/ubgo/docsync/internal/textnorm"
	"github.com/ubgo/docsync/pick"
)

// Defaults for the limits that keep a scan bounded (§10 normalization notes).
const (
	DefaultMaxFileKB    = 512
	DefaultMaxLineChars = 2000
)

// Directories never descended into, regardless of config. Vendored and
// installed code is other people's; VCS metadata is never a document.
// StateDir is the tool's own directory (§6). It is never scanned: the
// ledger, refs, acks, and journal it holds quote directives and would
// otherwise define and cite themselves.
const StateDir = ".ds"

var skipDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, "node_modules": true, "vendor": true, StateDir: true}

// SkipReason is why a file was not scanned; reported, never silent.
type SkipReason string

const (
	SkipExcluded  SkipReason = "excluded"     // matched an exclude glob
	SkipNotIn     SkipReason = "not-included" // matched no include glob
	SkipTooLarge  SkipReason = "too-large"    // larger than MaxFileKB
	SkipLongLine  SkipReason = "long-line"    // a non-prose line past MaxLineChars, taken as minified
	SkipBinary    SkipReason = "binary"       // holds a NUL byte
	SkipReadError SkipReason = "read-error"   // could not be stat'd or read
)

// Unreadable reports whether a file was skipped because of its form — too
// large, a line past the limit, binary, or unreadable — rather than by a
// choice in config. Such a file still exists and may still be cited, so its
// previous state is carried forward and it is reported, never dropped: a
// dropped doc took every citation in it, and any unreviewed change they
// carried, with it, and the check went green.
func (r SkipReason) Unreadable() bool {
	switch r {
	case SkipTooLarge, SkipLongLine, SkipBinary, SkipReadError:
		return true
	}
	return false
}

// SkipReasonValues is the canonical order.
var SkipReasonValues = []SkipReason{SkipExcluded, SkipNotIn, SkipTooLarge, SkipLongLine, SkipBinary, SkipReadError}

// Sentinel problem errors produced at the scan level (extractor problems keep
// their own sentinels).
var (
	ErrDuplicateID = errors.New("scan: id defined more than once")
	// ErrSharedBlock is two different ids bound to the same lines of the same
	// file. It is a finding because the ids are then indistinguishable to
	// every later pass: both hash the same bytes, so a sentence citing either
	// is measured against the other's block and a change flags both or
	// neither. It is nearly always a directive that failed to bind where its
	// author aimed it and landed on a neighbour (bug 18); the honest remedy is
	// to move or remove one, which is why the message names both.
	ErrSharedBlock = errors.New("scan: two ids bound to the same block")
	// ErrCrossingBlocks is two blocks in one file that overlap without one
	// containing the other: each holds part of the other, so an edit to the
	// shared lines flags both. Nesting is not this and is never reported.
	ErrCrossingBlocks = errors.New("scan: two blocks overlap without one containing the other")
	ErrDefInGenerated = errors.New("scan: ds:def in a generated file will be overwritten by the next generation")
	// ErrBadStability is an unrecognised `stability=`. It is a finding
	// rather than a silent default because the fallback is `stable`, so a
	// typo in `frozen` would quietly relax the policy the author asked for
	// and the block would stop flagging changes they wanted flagged.
	ErrBadStability = errors.New("scan: unknown stability")
	// ErrBadType is an unrecognised `type=`, and ErrTypeMismatch a value that
	// does not have the shape its type declares. Both used to pass silently:
	// type= was read by render (for url) and printed by facts, and nothing
	// checked it.
	ErrBadType = errors.New("scan: unknown type")
	// ErrMalformedID is a def whose id is not in the §8 shape for this
	// workspace, `<label>-<suffix>` (bug 55).
	ErrMalformedID  = errors.New("scan: malformed id")
	ErrTypeMismatch = errors.New("scan: value does not match its type=")
	// ErrBareDirective is a directive written as a line of its own in a file
	// whose type does carry comments. It is almost always damage rather than a
	// choice: `ds def` used to insert an uncommented line into any file type it
	// did not recognise, which is invalid syntax in every language that has
	// any -- a bare line in a go.work stopped every build in that workspace.
	// Reporting it is how a repository already hit finds the places to fix.
	ErrBareDirective = errors.New("scan: directive is not inside a comment")
	// ErrBadID is a def id holding whitespace or a control character. A
	// quoted value allows both, and such an id was accepted without a word:
	// it can never be cited through a link, and a tab or newline in it split
	// rows of the files that store it.
	ErrBadID         = errors.New("scan: id contains whitespace or a control character")
	ErrRemoteMissing = errors.New("scan: remote def target file not found")
	ErrRemotePick    = errors.New("scan: remote def pick failed")
	// ErrRemoteSkipped is a file= target the scanner would not read as a
	// source either: over scan.max_file_kb, or binary.
	ErrRemoteSkipped = errors.New("scan: remote def target is not readable as text")
	ErrPick          = errors.New("scan: pick failed")
	ErrNoInclude     = errors.New("scan: no include patterns; nothing would be scanned")
)

// Options configures a scan. Include and Exclude are compiled glob sets;
// Registry defaults to extract.Default().
type Options struct {
	Prefix       string
	Repo         string
	Include      glob.Set
	Exclude      glob.Set
	Generated    glob.Set
	Secret       glob.Set
	Registry     *extract.Registry
	MaxFileKB    int
	MaxLineChars int
	// Pickers are plugin pick= schemes (§37.3 Picker), by scheme name.
	Pickers map[string]pick.Picker
	// Cache remembers extraction per file and content hash, so a check
	// after a small change re-extracts only what changed (§16 "incremental
	// by default"). nil extracts everything.
	Cache Cache
	// Workers bounds concurrent extraction; 0 means one per CPU.
	Workers int
}

// Cache is the incremental-extraction store. Keys are the path and the
// content hash; a hit returns what extracting the same bytes produced. The
// scanner serialises its calls, so an implementation needs no locking.
type Cache interface {
	Get(path, hash string) (extract.Found, bool)
	Put(path, hash string, found extract.Found)
}

// StatCache is an optional upgrade a Cache may implement so an unchanged
// file is not even read: Unchanged answers from the size and modification
// time recorded for it, and Stamp records them once the scanner holds that
// file's extraction (a Get hit or a Put). On a large tree reading every
// file is most of what a check costs — 100,000 opens and reads took four
// seconds against the two the performance target allows (§33) — and a
// content-hash key can only be consulted after the read.
//
// The scanner calls Unchanged only with a real modification time (a zero
// one, as fstest.MapFS reports, always reads) and after the size limit, so
// a hit skips exactly the read, the binary test and the long-line test that
// produced the entry. A file edited so quickly that its time does not move
// is the implementation's to guard against, as git does for its index:
// refuse to Stamp a time too close to the moment of stamping.
type StatCache interface {
	Cache
	Unchanged(path string, size int64, mod time.Time) (extract.Found, bool)
	Stamp(path string, size int64, mod time.Time)
}

// ErrSymlink is a remote `file=` target that is a symbolic link; links are
// refused so a def cannot point outside the tree by indirection (§ Security).
var ErrSymlink = errors.New("scan: file= target is a symbolic link")

// Problem is a scan-level or extractor-level issue with its location.
type Problem struct {
	Pos block.Position
	Err error
}

// Skip records a file that was not scanned and why.
type Skip struct {
	File   string
	Reason SkipReason
}

// Result is everything a scan found, sorted by file then line so two scans of
// the same tree are byte-identical when serialised.
type Result struct {
	Defs     []block.Block
	Refs     []block.Reference
	Problems []Problem
	Skipped  []Skip
	// Files is the number of files actually extracted.
	Files int
	// Tier records which extractor handled each scanned file, for `doctor`.
	Tier map[string]string
	// Pages holds frontmatter declarations per markdown file that had any.
	Pages map[string]extract.Page
}

// Scan walks fsys and extracts. It stops early only on context cancellation
// or a walk error; per-file read errors become Skips.
func Scan(ctx context.Context, fsys fs.FS, opts Options) (Result, error) {
	if len(opts.Include) == 0 {
		return Result{}, ErrNoInclude
	}
	if opts.Registry == nil {
		opts.Registry = extract.Default()
	}
	if opts.MaxFileKB <= 0 {
		opts.MaxFileKB = DefaultMaxFileKB
	}
	if opts.MaxLineChars <= 0 {
		opts.MaxLineChars = DefaultMaxLineChars
	}
	res := Result{Tier: map[string]string{}, Pages: map[string]extract.Page{}}
	var remotes []extract.Def
	// Phase one walks and filters; phase two extracts in parallel; phase
	// three merges in path order so the result is deterministic whatever
	// the scheduling.
	var paths []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != "." && skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if opts.Exclude.MatchAny(p) {
			res.Skipped = append(res.Skipped, Skip{File: p, Reason: SkipExcluded})
			return nil
		}
		if !opts.Include.MatchAny(p) {
			res.Skipped = append(res.Skipped, Skip{File: p, Reason: SkipNotIn})
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("scan: %w", err)
	}
	type fileResult struct {
		path   string
		skip   SkipReason
		tier   string
		found  extract.Found
		cached bool
	}
	results := make([]fileResult, len(paths))
	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	var wg sync.WaitGroup
	var cacheMu sync.Mutex
	stats, _ := opts.Cache.(StatCache)
	jobs := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				p := paths[i]
				info, reason := statFile(fsys, p, opts)
				if reason != "" {
					results[i] = fileResult{path: p, skip: reason}
					continue
				}
				ex, exErr := opts.Registry.For(p)
				if exErr != nil {
					// Only possible with a custom registry lacking a fallback tier.
					results[i] = fileResult{path: p, skip: SkipNotIn}
					continue
				}
				stamped := stats != nil && !info.ModTime().IsZero()
				if stamped {
					cacheMu.Lock()
					found, ok := stats.Unchanged(p, info.Size(), info.ModTime())
					cacheMu.Unlock()
					if ok {
						results[i] = fileResult{path: p, tier: ex.Name(), found: found, cached: true}
						continue
					}
				}
				src, reason := readContent(fsys, p)
				if reason != "" {
					results[i] = fileResult{path: p, skip: reason}
					continue
				}
				if !extract.IsProse(ex) && hasLongLine(src, opts.MaxLineChars) {
					results[i] = fileResult{path: p, skip: SkipLongLine}
					continue
				}
				hash := textnorm.Hash(src)
				if opts.Cache != nil {
					cacheMu.Lock()
					found, ok := opts.Cache.Get(p, hash)
					if ok && stamped {
						stats.Stamp(p, info.Size(), info.ModTime())
					}
					cacheMu.Unlock()
					if ok {
						results[i] = fileResult{path: p, tier: ex.Name(), found: found, cached: true}
						continue
					}
				}
				found := ex.Extract(p, src, opts.Prefix)
				if opts.Cache != nil && len(found.Problems) == 0 && cacheable(p, found, opts) {
					cacheMu.Lock()
					opts.Cache.Put(p, hash, found)
					if stamped {
						stats.Stamp(p, info.Size(), info.ModTime())
					}
					cacheMu.Unlock()
				}
				results[i] = fileResult{path: p, tier: ex.Name(), found: found}
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	for _, fr := range results {
		p := fr.path
		if fr.skip != "" {
			res.Skipped = append(res.Skipped, Skip{File: p, Reason: fr.skip})
			continue
		}
		res.Files++
		res.Tier[p] = fr.tier
		found := fr.found
		if found.Page != nil {
			res.Pages[p] = *found.Page
		}
		generated := opts.Generated.MatchAny(p)
		secret := opts.Secret.MatchAny(p)
		for _, def := range found.Defs {
			def.Block.DirectivePos.File = p
			// Checked before the remote branch below, so a remote def's id
			// and policy are validated too.
			if strings.IndexFunc(def.Block.ID, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
				res.Problems = append(res.Problems, Problem{Pos: def.Block.DirectivePos, Err: fmt.Errorf("%w: %q", ErrBadID, def.Block.ID)})
			} else if err := id.CheckShape(def.Block.ID); err != nil {
				res.Problems = append(res.Problems, Problem{Pos: def.Block.DirectivePos, Err: fmt.Errorf("%w: %q: %v", ErrMalformedID, def.Block.ID, err)})
			}
			// A bare directive line in a type that has comments is damage; in
			// one whose carrier IS a bare line (plain text) it is correct.
			if def.Block.Carrier == block.CarrierBareLine {
				switch st, known := extract.StyleFor(p); {
				case extract.HasNoComments(p):
					// No comment form exists, so the line cannot be valid and
					// cannot be fixed by commenting it -- only removed.
					res.Problems = append(res.Problems, Problem{Pos: def.Block.DirectivePos, Err: fmt.Errorf("%w: %s has no comment syntax, so this line breaks the file; ds repair --apply removes it (bind the value with a remote def instead)", ErrBareDirective, p)})
				case known && !st.Bare:
					res.Problems = append(res.Problems, Problem{Pos: def.Block.DirectivePos, Err: fmt.Errorf("%w: %s carries comments, so a bare directive line is probably not valid there; ds repair --apply comments it", ErrBareDirective, p)})
				}
			}
			if s, ok := def.Block.Args[block.KeyStability]; ok && s != "" {
				if _, valid := block.ParseStability(s); !valid {
					res.Problems = append(res.Problems, Problem{Pos: def.Block.DirectivePos, Err: fmt.Errorf("%w: %q is not one of %s", ErrBadStability, s, stabilityList())})
				}
			}
			if def.Remote {
				remotes = append(remotes, def)
				continue
			}
			def.Block.Pos.File = p
			if secret {
				def.Block.Args = withKey(def.Block.Args, block.KeySecret, block.TrueValue)
			}
			if generated {
				res.Problems = append(res.Problems, Problem{Pos: def.Block.DirectivePos, Err: ErrDefInGenerated})
			}
			res.Defs = append(res.Defs, redactSecret(applyPick(def.Block, opts, &res), def.Block.Content, secret))
		}
		for _, ref := range found.Refs {
			ref.Reference.Pos.File = p
			res.Refs = append(res.Refs, ref.Reference)
		}
		for _, pr := range found.Problems {
			pr.Pos.File = p
			res.Problems = append(res.Problems, Problem{Pos: pr.Pos, Err: pr.Err})
		}
	}
	for _, def := range remotes {
		resolveRemote(fsys, def, opts, &res)
	}
	checkTypes(&res)
	detectDuplicates(&res)
	detectSharedBlocks(&res)
	sortResult(&res)
	return res, nil
}

// MatchAny reports whether path matches any of the glob patterns, for
// callers outside this module that keep an allow-list of paths (`run.allow`).
func MatchAny(patterns []string, path string) (bool, error) {
	set, err := glob.CompileAll(patterns)
	if err != nil {
		return false, err
	}
	return set.MatchAny(path), nil
}

// CountMatches reports how many files under fsys one include pattern would
// select, honouring the same skipped directories as Scan. `doctor` uses it
// to flag globs that match nothing, which is how a misspelled path fails
// silently otherwise.
func CountMatches(fsys fs.FS, pattern string) (int, error) {
	p, err := glob.Compile(pattern)
	if err != nil {
		return 0, err
	}
	n := 0
	err = fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." && skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if p.Match(path) {
			n++
		}
		return nil
	})
	return n, err
}

// readFile applies the size, line, and binary limits.
func readFile(fsys fs.FS, p string, opts Options) ([]byte, SkipReason) {
	if _, reason := statFile(fsys, p, opts); reason != "" {
		return nil, reason
	}
	return readContent(fsys, p)
}

// statFile applies the limits a file's metadata decides: it must exist and
// be no larger than MaxFileKB.
func statFile(fsys fs.FS, p string, opts Options) (fs.FileInfo, SkipReason) {
	info, err := fs.Stat(fsys, p)
	if err != nil {
		return nil, SkipReadError
	}
	if info.Size() > int64(opts.MaxFileKB)*1024 {
		return nil, SkipTooLarge
	}
	return info, ""
}

// readContent reads a file statFile passed and applies the limits its bytes
// decide: readable, and not binary.
func readContent(fsys fs.FS, p string) ([]byte, SkipReason) {
	src, err := fs.ReadFile(fsys, p)
	if err != nil {
		return nil, SkipReadError
	}
	if bytes.IndexByte(src, 0) >= 0 {
		return nil, SkipBinary
	}
	// Line numbers are unchanged: the mark sits in front of line 1.
	return textnorm.TrimBOM(src), ""
}

// hasLongLine reports a line past max, the scanner's sign of minified code.
// It is applied only to tiers that are not prose (extract.ProseTier).
func hasLongLine(src []byte, max int) bool {
	for _, l := range bytes.Split(src, []byte("\n")) {
		if len(l) > max {
			return true
		}
	}
	return false
}

// resolveRemote binds a `file=` def against its target: reads the file,
// applies `pick` (default `file`), and produces a block positioned in the
// target. Missing targets and failed picks are problems at the directive.
func resolveRemote(fsys fs.FS, def extract.Def, opts Options, res *Result) {
	target := strings.TrimPrefix(def.Directive.Args[block.KeyFile], "./")
	if def.Block.IsLocal() {
		// local=true (§9.1, §12): the target exists on some machines only and
		// is never read by a scan. The def is kept, unbound and contentless,
		// so citations resolve as unverifiable rather than broken.
		b := def.Block
		b.Kind = block.KindFile
		b.Pos = block.Position{File: target}
		b.Symbol = target
		res.Defs = append(res.Defs, b)
		return
	}
	if strings.HasPrefix(target, "/") || strings.Contains(target, "..") {
		// Outside the tree, or a local= path (§9.1): never read from a scan.
		res.Problems = append(res.Problems, Problem{Pos: def.Block.DirectivePos, Err: fmt.Errorf("%w: %s is outside the repo", ErrRemoteMissing, target)})
		return
	}
	if info, err := fs.Lstat(fsys, target); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		res.Problems = append(res.Problems, Problem{Pos: def.Block.DirectivePos, Err: fmt.Errorf("%w: %s", ErrSymlink, target)})
		return
	}
	// The same read, with the same limits, as any scanned file. A plain
	// ReadFile loaded a target of any size whole and picked through binary
	// bytes: a file= at a large log or a build artefact held the whole of
	// it in memory on every scan.
	src, skip := readFile(fsys, target, opts)
	switch skip {
	case "":
	case SkipReadError:
		res.Problems = append(res.Problems, Problem{Pos: def.Block.DirectivePos, Err: fmt.Errorf("%w: %s", ErrRemoteMissing, target)})
		return
	default:
		res.Problems = append(res.Problems, Problem{Pos: def.Block.DirectivePos, Err: fmt.Errorf("%w: %s: %s", ErrRemoteSkipped, target, skip)})
		return
	}
	expr := def.Directive.Args[block.KeyPick]
	if expr == "" {
		expr = pick.SchemeFile
	}
	r, err := pick.PickWith(opts.Pickers, expr, string(src))
	if err != nil {
		res.Problems = append(res.Problems, Problem{Pos: def.Block.DirectivePos, Err: fmt.Errorf("%w: %s: %v", ErrRemotePick, target, err)})
		return
	}
	b := def.Block
	b.Pos = block.Position{File: target, Start: r.Start, End: r.End}
	switch r.Kind {
	case pick.KindValue:
		b.Kind = block.KindKey
		b.SetContent(r.Value)
	default:
		if expr == pick.SchemeFile {
			b.Kind = block.KindFile
		} else {
			b.Kind = block.KindSpan
		}
		b.SetContent(r.Text)
	}
	b.Symbol = expr
	globbed := opts.Secret.MatchAny(target)
	if globbed {
		b.Args = withKey(b.Args, block.KeySecret, block.TrueValue)
	}
	// Judged on the picked value only: the text a remote def reads from is a
	// whole file, and one address anywhere in it says nothing about the rest.
	res.Defs = append(res.Defs, redactSecret(b, "", globbed))
}

// redactSecret blanks a secret def's content unless it is an address, keeping
// its hash, so drift on it is still detected and nothing downstream can show
// the value: render, facts, context, read, export and the MCP tools all read
// Content, and before this every one of them printed a secret's value (§12).
//
// prePick is the bound text before any pick, because a pick can narrow a
// reference to its name -- `STRIPE_KEY` out of `${{ secrets.STRIPE_KEY }}` --
// and that name is an address too. `source=` vouches for a bare env name, as
// the spec asks, but only on a def that declares itself secret: a file under
// `[secret] paths` holds values by definition, so there only an address shape
// is trusted.
func redactSecret(b block.Block, prePick string, fromGlob bool) block.Block {
	if !b.IsSecret() || block.SecretAddress(b.Content) || block.SecretAddress(prePick) {
		return b
	}
	if !fromGlob && b.Args[block.KeySource] != "" {
		return b
	}
	b.Content = ""
	return b
}

// applyPick narrows a local def's content: an explicit `pick=` is applied to
// the bound text; a config key with no pick yields its value (§10, §11).
// A failed pick is a problem at the directive and the def keeps the bound
// text, so references to it stay resolvable while the author fixes the
// expression.
func applyPick(b block.Block, opts Options, res *Result) block.Block {
	expr := b.Args[block.KeyPick]
	if expr == "" {
		if b.Kind == block.KindKey && !strings.Contains(b.Content, "\n") {
			if v, ok := pick.KeyValue(b.Content); ok {
				b.SetContent(v)
			}
		}
		return b
	}
	r, err := pick.PickWith(opts.Pickers, expr, b.Content)
	if err != nil {
		res.Problems = append(res.Problems, Problem{Pos: b.DirectivePos, Err: fmt.Errorf("%w: %s: %v", ErrPick, b.ID, err)})
		return b
	}
	if r.Kind == pick.KindValue {
		b.SetContent(r.Value)
		return b
	}
	b.Pos = block.Position{File: b.Pos.File, Start: b.Pos.Start + r.Start - 1, End: b.Pos.Start + r.End - 1}
	b.SetContent(r.Text)
	return b
}

// detectDuplicates reports every def whose id appears more than once. All
// occurrences are reported, never just the second, because the scanner cannot
// know which one is the original (§ edge cases).
func detectDuplicates(res *Result) {
	// One def per id per environment (§12): the key includes env=.
	seen := map[string][]int{}
	for i, d := range res.Defs {
		seen[d.ID+"\x00"+d.Env()] = append(seen[d.ID+"\x00"+d.Env()], i)
	}
	for id, idx := range seen {
		if len(idx) < 2 {
			continue
		}
		id, _, _ = strings.Cut(id, "\x00")
		for _, i := range idx {
			res.Problems = append(res.Problems, Problem{Pos: res.Defs[i].DirectivePos, Err: fmt.Errorf("%w: %s (%d places)", ErrDuplicateID, id, len(idx))})
		}
	}
}

// detectSharedBlocks reports ids that bound the same lines. Every id on the
// block is reported, not just the later one, because the scanner cannot know
// which directive was the one that went astray.
func detectSharedBlocks(res *Result) {
	at := map[string][]int{}
	for i, d := range res.Defs {
		// A def with no extent (remote, local, unbound) shares nothing: its
		// position is a file, not a range, and several may legitimately name
		// the same target.
		if d.Pos.File == "" || d.Pos.Start == 0 {
			continue
		}
		// An inline fact's block is its link text, a span inside a line, so
		// two facts on one line are two blocks; keyed by line they collided,
		// and the spec's own two-facts-in-a-sentence example could not be
		// scanned cleanly (bug 59).
		if d.Kind == block.KindLinkText {
			continue
		}
		k := fmt.Sprintf("%s:%d-%d\x00%s", d.Pos.File, d.Pos.Start, d.Pos.End, d.Env())
		// A remote def is identified by what it picks as well as where: two
		// keys of one JSON line are two values (bug 53). The same pick twice
		// still shares a block and is still reported.
		if _, remote := d.Args[block.KeyFile]; remote {
			k += "\x00" + d.Args[block.KeyPick]
		}
		at[k] = append(at[k], i)
	}
	detectCrossingBlocks(res)
	for _, idx := range at {
		if len(idx) < 2 {
			continue
		}
		// Distinct ids only. One id appearing twice on one block is already
		// ErrDuplicateID, which says it more precisely; reporting it here as
		// well would print the same id twice in one message and double every
		// problem count.
		seen := map[string]bool{}
		ids := make([]string, 0, len(idx))
		for _, i := range idx {
			if id := res.Defs[i].ID; !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) < 2 {
			continue
		}
		sort.Strings(ids)
		d := res.Defs[idx[0]]
		for _, i := range idx {
			res.Problems = append(res.Problems, Problem{Pos: res.Defs[i].DirectivePos, Err: fmt.Errorf("%w: %s at %s:%d-%d", ErrSharedBlock, strings.Join(ids, ", "), d.Pos.File, d.Pos.Start, d.Pos.End)})
		}
	}
}

// checkTypes reports a def whose `type=` is not a known value type, and one
// whose value does not have the declared shape (bug 54). It runs on the final
// value: after the pick, and after a remote def is resolved. A secret whose
// value was blanked has nothing to check, and is never printed here.
func checkTypes(res *Result) {
	for _, d := range res.Defs {
		raw, ok := d.Args[block.KeyType]
		if !ok {
			continue
		}
		t := block.ValueType(raw)
		switch {
		case !t.Known():
			res.Problems = append(res.Problems, Problem{Pos: d.DirectivePos, Err: fmt.Errorf("%w: %q is not one of %s", ErrBadType, raw, typeList())})
		case d.IsSecret():
			// Never echo a secret's value, even into a finding.
		case !t.Accepts(d.Content):
			res.Problems = append(res.Problems, Problem{Pos: d.DirectivePos, Err: fmt.Errorf("%w: %s's value %q is not of type %s", ErrTypeMismatch, d.ID, d.Content, t)})
		}
	}
}

// typeList spells ValueTypeValues for a message.
func typeList() string {
	out := make([]string, len(block.ValueTypeValues))
	for i, t := range block.ValueTypeValues {
		out[i] = string(t)
	}
	return strings.Join(out, ", ")
}

// detectCrossingBlocks reports pairs of defs in one file whose blocks overlap
// without either containing the other (bug 58). Nesting is how declarations
// and sections are built and is never reported; crossing is a directive whose
// extent was cut short or run on -- usually a span= written by hand, or a def
// inserted inside another's span -- and it makes an edit to the shared lines
// flag both blocks while each still claims lines the other does not.
// Remote defs are left out (their extent is in another file, chosen by a
// pick), and so are inline facts, whose block is part of one line.
func detectCrossingBlocks(res *Result) {
	byFile := map[string][]int{}
	for i, d := range res.Defs {
		if _, remote := d.Args[block.KeyFile]; remote || d.Kind == block.KindLinkText || d.Pos.Start == 0 {
			continue
		}
		byFile[d.Pos.File] = append(byFile[d.Pos.File], i)
	}
	for _, idx := range byFile {
		for x, i := range idx {
			for _, j := range idx[x+1:] {
				a, b := res.Defs[i], res.Defs[j]
				if a.Env() != b.Env() || !block.Crosses(a.Pos, b.Pos) {
					continue
				}
				for _, d := range []block.Block{a, b} {
					res.Problems = append(res.Problems, Problem{Pos: d.DirectivePos, Err: fmt.Errorf("%w: %s at %d-%d and %s at %d-%d in %s", ErrCrossingBlocks, a.ID, a.Pos.Start, a.Pos.End, b.ID, b.Pos.Start, b.Pos.End, a.Pos.File)})
				}
			}
		}
	}
}

func sortResult(res *Result) {
	sort.SliceStable(res.Defs, func(i, j int) bool { return lessPos(res.Defs[i].DirectivePos, res.Defs[j].DirectivePos) })
	sort.SliceStable(res.Refs, func(i, j int) bool { return lessPos(res.Refs[i].Pos, res.Refs[j].Pos) })
	sort.SliceStable(res.Problems, func(i, j int) bool { return lessPos(res.Problems[i].Pos, res.Problems[j].Pos) })
	sort.SliceStable(res.Skipped, func(i, j int) bool { return res.Skipped[i].File < res.Skipped[j].File })
}

func lessPos(a, b block.Position) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	return a.Start < b.Start
}

// withKey copies a map and sets one key, so a scan never mutates a map that
// an extractor might share between a directive and its block.
// cacheable reports whether this file's extraction may be remembered.
//
// A cache entry stores block bodies verbatim so a later scan can skip
// re-reading the file, and §12 promises that a secret's value is never
// stored. A cache is also a derived artifact that travels — into CI caches,
// backups, and git — so the guard is here, at the single point where
// anything is handed to a Cache, rather than inside one implementation:
// enforcing it in the caller means a custom Cache cannot fail to honour it.
//
// Three things disqualify a file: the configured secret globs match its
// path, a def in it declares secret=, or a def declares local= (whose body
// is by definition not meant to travel). Path-based secrecy is applied to
// blocks further down, after every file is extracted, so it has to be read
// from the globs here rather than from the block.
func cacheable(p string, found extract.Found, opts Options) bool {
	if opts.Secret.MatchAny(p) {
		return false
	}
	for _, d := range found.Defs {
		if d.Block.IsSecret() || d.Block.IsLocal() {
			return false
		}
	}
	return true
}

func withKey(m map[string]string, k, v string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for kk, vv := range m {
		out[kk] = vv
	}
	out[k] = v
	return out
}

// stabilityList renders the accepted policies for an error message, read
// from the canonical list so a new one cannot be missing from the hint.
func stabilityList() string {
	out := make([]string, 0, len(block.StabilityValues))
	for _, s := range block.StabilityValues {
		out = append(out, string(s))
	}
	return strings.Join(out, ", ")
}
