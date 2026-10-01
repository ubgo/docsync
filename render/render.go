// Package render expands directives in one markdown document into plain
// markdown at build time (docs/SPEC.md §9, §22 `ds render`). Citations become
// permalinks, `ds:cfg` becomes the current value, block-position `ds:block`
// becomes the live code, inline defs become their text, claims and defs
// disappear. The repository never holds a copy of a block; this is where the
// copy is made, and only in the built output.
//
// Render never fails on a bad directive. It leaves the line as written,
// records a Note with the line number, and continues, so one broken cite
// cannot take a site build down. The checker is what fails a build.
package render

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/internal/linerange"
	"github.com/ubgo/docsync/internal/mdspan"
)

// Note is a rendering problem: the line it happened on and what was wrong.
type Note struct {
	Line    int
	Message string
}

// RunResult is the last recorded outcome of a `ds:run`, keyed by doc line by
// the caller. The renderer runs nothing (§9.4).
type RunResult struct {
	Output string
	OK     bool
	At     time.Time
}

// Options tunes a render. Hooks are optional and degrade honestly: without
// Snapshot an `at=` cite renders as a link with an "as of" badge and no code;
// without Records a `ds:table` renders its `empty=` text.
type Options struct {
	Prefix string
	// Permalink is a template with {sha} {file} {rel} {start} {end}; empty
	// renders DefaultPermalink, a link relative to the page.
	Permalink string
	Commit    string
	MaxLines  int
	// Env picks among per-environment defs for cites that do not say.
	Env string
	Now time.Time
	// Link replaces Permalink entirely; the one escape hatch for hosts whose
	// URLs the template cannot express.
	Link func(b block.Block) string
	// ForeignLink is the URL of a block another repository in the workspace
	// defines (block.KeyRepo set): its path is relative to that repository,
	// so the Permalink template, which describes this one, cannot build it.
	// ok=false, or a nil hook, renders the location unlinked with the
	// repository named. Link, when set, still wins.
	ForeignLink func(b block.Block) (url string, ok bool)
	// Snapshot returns the block's content at a commit, for `at=`.
	Snapshot func(id, sha string) (string, bool)
	// SnapshotBlock returns the whole block at a commit, for `at=`: its
	// content, and the position its caption and link name. It takes
	// precedence over Snapshot; with only Snapshot, the link names where
	// the block is today.
	SnapshotBlock func(id, sha string) (block.Block, bool)
	// Records answers `ds:table`: rows as maps keyed by column.
	Records func(args map[string]string) ([]map[string]string, error)
	// Runs maps a doc line to the last `ds:run` result recorded there.
	Runs map[int]RunResult
	// Verbs renders plugin verbs by name.
	Verbs map[string]VerbRenderer
}

// VerbRenderer renders a plugin verb (§9.9, §37.3 Verb.Render). inline is
// true for a link in prose, false for block position; ok=false leaves the
// source untouched and records a note with the error.
type VerbRenderer func(ref block.Reference, inline bool) (text string, ok bool, err error)

// Node kinds for RenderNodes (§37.3 Renderer): a custom renderer receives
// the document as a sequence of nodes instead of joined markdown.
const (
	NodeText  = "text"  // an untouched source line
	NodeProse = "prose" // a prose line with inline directives expanded
	NodeBlock = "block" // the output of a block-position directive
)

// Node is one rendered unit; Line is the source line it came from.
type Node struct {
	Kind string `json:"kind"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Input is the document plus every definition it may cite.
type Input struct {
	Doc  string
	Src  []byte
	Defs []block.Block
}

// Format values for `ds:cfg` (§9.3).
const (
	FormatRaw     = "raw"
	FormatCode    = "code"
	FormatQuote   = "quote"
	FormatHost    = "host"
	FormatLink    = "link"
	FormatCompact = "compact"
)

// FormatValues is the canonical list.
var FormatValues = []string{FormatRaw, FormatCode, FormatQuote, FormatHost, FormatLink, FormatCompact}

// Providers a secret address can name (§12).
const (
	ProviderGitHub      = "github"
	ProviderOnePassword = "1password"
	ProviderAWS         = "aws"
	ProviderGCP         = "gcp"
	ProviderVault       = "vault"
	ProviderEnv         = "env"
	ProviderFile        = "file"
)

// ProviderValues is the canonical list of the providers above.
var ProviderValues = []string{ProviderGitHub, ProviderOnePassword, ProviderAWS, ProviderGCP, ProviderVault, ProviderEnv, ProviderFile}

// AddressProviderValues are the providers Provider infers from an address's
// shape, as opposed to those only a `source=` can name. Each is a provider
// `ds check --resolve` asks a plugin about without the author saying which,
// so each must have a plugin shipped beside ds; a test holds the release to
// that (bug 80).
var AddressProviderValues = []string{ProviderGitHub, ProviderOnePassword, ProviderAWS, ProviderGCP, ProviderVault}

// Reference keys the renderer reads.
const (
	keyLines    = "lines"
	keyAt       = "at"
	keyTitle    = "title"
	keyStrip    = "strip"
	keyCollapse = "collapse"
	keyLang     = "lang"
	keyFormat   = "format"
	keyHref     = "href"
	keyCmd      = "cmd"
	keyFile     = "file"
	keyShow     = "show"
	keyEmpty    = "empty"
	keyCols     = "cols"
	keyKind     = "kind"

	stripComments = "comments"
	showNone      = "none"
	showCommand   = "command"
	showOutput    = "output"
)

// DefaultMaxLines mirrors include.max_lines (§9.2).
const DefaultMaxLines = 40

// DefaultPermalink is used when Options.Permalink is empty. It names the
// file relative to the rendered page ({rel}), not to the repository root
// ({file}): a link resolves against the page that holds it, so a
// root-relative billing/x.go written into docs/billing.md pointed at
// docs/billing/x.go, which does not exist, on GitHub and on any static host
// that serves the tree as it is (bug 64).
const DefaultPermalink = "{rel}#L{start}-L{end}"

// langByExt maps file extensions to fence info strings.
var langByExt = map[string]string{
	".go": "go", ".ts": "typescript", ".tsx": "tsx", ".js": "javascript", ".jsx": "jsx", ".mjs": "javascript",
	".py": "python", ".rb": "ruby", ".rs": "rust", ".java": "java", ".kt": "kotlin", ".cs": "csharp", ".php": "php",
	".swift": "swift", ".c": "c", ".h": "c", ".cpp": "cpp", ".sql": "sql", ".yaml": "yaml", ".yml": "yaml",
	".toml": "toml", ".json": "json", ".sh": "bash", ".bash": "bash", ".zsh": "bash", ".env": "bash", ".ini": "ini",
	".css": "css", ".scss": "scss", ".html": "html", ".md": "markdown", ".tf": "hcl", ".hcl": "hcl", ".lua": "lua",
	".dockerfile": "dockerfile", ".xml": "xml", ".proto": "protobuf", ".graphql": "graphql", ".dart": "dart",
}

// commentRE matches one HTML comment; group 1 is its body.
var commentRE = regexp.MustCompile(`<!--\s*(.*?)\s*-->`)

// Render expands in.Src and returns the result with notes, sorted by line.
func Render(in Input, opts Options) ([]byte, []Note) {
	nodes, notes := RenderNodes(in, opts)
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = n.Text
	}
	return []byte(strings.Join(parts, "\n")), notes
}

// RenderNodes is Render as a node sequence, for a custom Renderer.
func RenderNodes(in Input, opts Options) ([]Node, []Note) {
	if opts.Prefix == "" {
		opts.Prefix = directive.DefaultPrefix
	}
	if opts.MaxLines <= 0 {
		opts.MaxLines = DefaultMaxLines
	}
	if opts.Permalink == "" {
		opts.Permalink = DefaultPermalink
	}
	r := &renderer{opts: opts, doc: in.Doc, defs: map[string][]block.Block{}}
	for _, b := range in.Defs {
		r.defs[b.ID] = append(r.defs[b.ID], b)
	}
	lines := strings.Split(strings.ReplaceAll(string(in.Src), "\r\n", "\n"), "\n")
	// The extractor's view of the page, so render acts on exactly the
	// directives check counts: nothing in frontmatter, code blocks, or code
	// spans. Render kept its own fence mask and rewrote every example a page
	// showed in inline or indented code.
	carrier := extract.CarrierLines(in.Doc, lines)
	head := opts.Prefix + directive.Separator
	st, _ := extract.StyleFor(in.Doc)
	bare := st.Bare
	var out []Node
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		n := i + 1
		if !strings.Contains(carrier[i], head) {
			out = append(out, Node{Kind: NodeText, Line: n, Text: l})
			continue
		}
		// Whole-line comment directive: block position.
		joined, consumed, isComment := "", 0, false
		if body, ok := wholeComment(carrier[i]); ok && strings.HasPrefix(body, head) {
			// Continuation lines fold into it, as the extractor folds them;
			// rendering line by line dropped their keys — a `lines=` on the
			// second line rendered the whole block — and left the
			// continuation comment in the page.
			bodies := []string{body}
			for j := i + 1; j < len(lines); j++ {
				next, ok := rawComment(carrier[j])
				if !ok {
					break
				}
				bodies = append(bodies, next)
			}
			joined, consumed = directive.Fold(bodies)
			isComment = true
		} else if text, last, ok := extract.MultiLineDirective(carrier, i, htmlComments, head); ok && text != "" {
			// One comment over several lines, read as the extractor reads it
			// (bug 50), so render acts on the directive check counted.
			joined, consumed, isComment = text, last-i+1, true
		} else if bare && strings.HasPrefix(strings.TrimSpace(l), head) {
			// Plain text carries a directive as a line of its own (§7.1), and
			// renderers strip it; render left `ds:def id=…` in the output of
			// every .txt page (bug 44). Continuations fold as the text tier
			// folds them.
			joined, consumed = directive.Fold(lines[i:])
			isComment = true
		}
		if isComment {
			// asWritten keeps every line the directive spans, for when it
			// is left in the page rather than rendered.
			asWritten := func() {
				for k := i; k < i+consumed; k++ {
					out = append(out, Node{Kind: NodeText, Line: k + 1, Text: lines[k]})
				}
			}
			d, err := directive.Parse(opts.Prefix, joined)
			if err != nil {
				r.note(n, "cannot parse directive: %v", err)
				asWritten()
				i += consumed - 1
				continue
			}
			rendered, keep := r.blockPosition(n, d)
			if keep {
				asWritten()
			} else if rendered != "" {
				out = append(out, Node{Kind: NodeBlock, Line: n, Text: rendered})
			}
			i += consumed - 1
			// A repo-mode copy under a rendered block is replaced by the
			// rendering, closer included. Leaving it printed every copy
			// twice and left the closer comment in the page (bug 105).
			if !keep && d.Verb == extract.VerbBlock {
				if reg := extract.Region(lines, i+2, opts.Prefix); reg != nil {
					i = reg.End - 1
				}
			}
			continue
		}
		out = append(out, Node{Kind: NodeProse, Line: n, Text: r.inline(n, l, carrier[i], head)})
	}
	sort.SliceStable(r.notes, func(i, j int) bool { return r.notes[i].Line < r.notes[j].Line })
	return out, r.notes
}

type renderer struct {
	opts  Options
	doc   string // the page being rendered, repository-relative; {rel} is relative to its directory
	defs  map[string][]block.Block
	notes []Note
}

func (r *renderer) note(line int, format string, args ...any) {
	r.notes = append(r.notes, Note{Line: line, Message: fmt.Sprintf(format, args...)})
}

// def resolves an id for an environment: exact env, then the env-less def,
// then, when no env was asked for, the first.
func (r *renderer) def(id, env string) (block.Block, bool) {
	return r.defBranch(id, env, "")
}

// defBranch prefers a def published from branch when one exists, and the
// default-branch def otherwise.
func (r *renderer) defBranch(id, env, branch string) (block.Block, bool) {
	cands := r.defs[id]
	if branch != "" {
		var on []block.Block
		for _, b := range cands {
			if b.Args[block.KeyBranch] == branch {
				on = append(on, b)
			}
		}
		if len(on) > 0 {
			cands = on
		}
	} else {
		var def []block.Block
		for _, b := range cands {
			if b.Args[block.KeyBranch] == "" {
				def = append(def, b)
			}
		}
		if len(def) > 0 {
			cands = def
		}
	}
	if env == "" {
		env = r.opts.Env
	}
	for _, b := range cands {
		if b.Env() == env {
			return b, true
		}
	}
	for _, b := range cands {
		if b.Env() == "" {
			return b, true
		}
	}
	if env == r.opts.Env && len(cands) > 0 {
		return cands[0], true
	}
	return block.Block{}, false
}

// inline rewrites every directive link on a prose line and strips trailing
// comment directives (claims, refs in comment form) which are invisible.
// Directives are found in carrier, the line with code spans blanked, and
// rewritten in l by position, so an example in code stays as written even
// when the same text appears live beside it.
func (r *renderer) inline(n int, l, carrier, head string) string {
	var edits []splice
	for _, lk := range mdspan.InlineLinks(carrier) {
		m := l[lk.All.Start:lk.All.End]
		image, text, target := lk.Image, l[lk.Text.Start:lk.Text.End], l[lk.Dest.Start:lk.Dest.End]
		if !strings.HasPrefix(target, head) {
			continue
		}
		d, err := directive.ParseLink(r.opts.Prefix, target)
		if err != nil {
			r.note(n, "cannot parse link %q: %v", target, err)
			continue
		}
		if image {
			r.note(n, "directive on an image link renders nothing special")
			continue
		}
		edits = append(edits, splice{lk.All.Start, lk.All.End, r.inlineVerb(n, d, text, m)})
	}
	// Trailing comment directives disappear from the prose line. Verbs with a
	// block rendering (`Stripe: <!-- ds:chain … -->`, §9.8) render it under
	// the line; claims and defs render nothing.
	var after []string
	for _, ix := range commentRE.FindAllStringSubmatchIndex(carrier, -1) {
		body := l[ix[2]:ix[3]]
		if !strings.HasPrefix(body, head) {
			continue
		}
		d, err := directive.Parse(r.opts.Prefix, body)
		if err != nil {
			r.note(n, "cannot parse directive: %v", err)
			continue
		}
		rendered, keep := r.blockPosition(n, d)
		if keep {
			continue
		}
		if rendered != "" {
			after = append(after, rendered)
		}
		edits = append(edits, splice{ix[0], ix[1], ""})
	}
	// Last first, so each splice's offsets still hold. A link and a comment
	// never overlap: the link pattern cannot span "<!--".
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, e := range edits {
		l = l[:e.start] + e.text + l[e.end:]
	}
	l = strings.TrimRight(l, " \t")
	if len(after) == 0 {
		return l
	}
	return l + "\n\n" + strings.Join(after, "\n\n")
}

// splice replaces line[start:end] with text.
type splice struct {
	start, end int
	text       string
}

// inlineVerb renders one link-form directive.
func (r *renderer) inlineVerb(n int, d directive.Directive, text, orig string) string {
	id := d.Args[block.KeyID]
	switch d.Verb {
	case extract.VerbDef:
		if block.ValueType(d.Args[block.KeyType]) == block.TypeURL {
			return "[" + text + "](" + text + ")"
		}
		return text
	case extract.VerbBlock, extract.VerbChain:
		b, ok := r.defBranch(id, d.Args[block.KeyEnv], d.Args[block.KeyBranch])
		if !ok {
			r.note(n, "%s is not defined; link left as text", id)
			return text
		}
		if at := d.Args[keyAt]; at != "" && r.opts.SnapshotBlock != nil {
			if old, ok := r.opts.SnapshotBlock(id, at); ok {
				b.Pos = old.Pos
			}
		}
		return r.anchor(text, b, d.Args[keyAt])
	case extract.VerbCfg:
		if id == "" {
			r.note(n, "cfg query= is not built yet; link text kept")
			return text
		}
		b, ok := r.def(id, d.Args[block.KeyEnv])
		if !ok {
			r.note(n, "%s is not defined; link text kept", id)
			return text
		}
		// A secret whose content is a value, not an address, is blanked
		// before it gets here, and Value then reported it as "more than one
		// line", which sent the reader to ds:block, a verb that refuses
		// secrets too (bug 126). A secret that is an address renders as one.
		if b.IsSecret() && b.Content == "" {
			r.note(n, "%s is a secret; its value is not rendered, link text kept", id)
			return text
		}
		v, ok := Value(b)
		if !ok {
			r.note(n, "%s yields more than one line; use ds:block", id)
			return text
		}
		return formatValue(v, d.Args[keyFormat])
	case extract.VerbURL:
		href := d.Args[keyHref]
		if href == "" {
			r.note(n, "ds:url without href=")
			return text
		}
		return "[" + text + "](" + href + ")"
	}
	if vr, ok := r.opts.Verbs[d.Verb]; ok {
		out, ok, err := vr(block.Reference{Verb: d.Verb, ID: id, Pos: block.Position{Start: n}, Carrier: block.CarrierLink, Args: d.Args}, true)
		if err != nil {
			r.note(n, "verb %s: %v", d.Verb, err)
			return orig
		}
		if ok {
			return out
		}
		return orig
	}
	r.note(n, "verb %q has no inline rendering", d.Verb)
	return orig
}

// blockPosition renders a whole-line comment directive. keep=true leaves
// the source line untouched.
func (r *renderer) blockPosition(n int, d directive.Directive) (string, bool) {
	id := d.Args[block.KeyID]
	switch d.Verb {
	case extract.VerbDef, extract.VerbClaim:
		return "", false
	case extract.VerbBlock:
		return r.blockCode(n, d, id)
	case extract.VerbChain:
		return r.chain(n, id, d.Args[block.KeyEnv])
	case extract.VerbRun:
		return r.run(n, d, id)
	case extract.VerbTable:
		return r.table(n, d)
	case extract.VerbURL:
		if href := d.Args[keyHref]; href != "" {
			return "<" + href + ">", false
		}
		r.note(n, "ds:url without href=")
		return "", true
	case extract.VerbCfg:
		r.note(n, "ds:cfg is link form only; use [value](ds:cfg?id=…)")
		return "", true
	}
	if vr, ok := r.opts.Verbs[d.Verb]; ok {
		out, ok, err := vr(block.Reference{Verb: d.Verb, ID: id, Pos: block.Position{Start: n}, Carrier: block.CarrierBlock, Args: d.Args}, false)
		if err != nil {
			r.note(n, "verb %s: %v", d.Verb, err)
			return "", true
		}
		return out, !ok
	}
	r.note(n, "verb %q has no block rendering", d.Verb)
	return "", true
}

// blockCode renders a cited block as a fenced snippet under a permalink.
func (r *renderer) blockCode(n int, d directive.Directive, id string) (string, bool) {
	b, ok := r.defBranch(id, d.Args[block.KeyEnv], d.Args[block.KeyBranch])
	if !ok {
		r.note(n, "%s is not defined", id)
		return "", true
	}
	if b.IsSecret() {
		r.note(n, "%s is a secret; ds:block refuses to render it", id)
		return "", true
	}
	sha := d.Args[keyAt]
	content := b.Content
	badge := ""
	if sha != "" {
		badge = " · as of `" + sha + "`"
		ok := false
		switch {
		case r.opts.SnapshotBlock != nil:
			var old block.Block
			if old, ok = r.opts.SnapshotBlock(id, sha); ok {
				content, b.Pos = old.Content, old.Pos
			}
		case r.opts.Snapshot != nil:
			content, ok = r.opts.Snapshot(id, sha)
		}
		if !ok {
			r.note(n, "no snapshot for %s at %s; rendering the link only", id, sha)
			content = ""
		}
	}
	lines := strings.Split(content, "\n")
	if ls := d.Args[keyLines]; ls != "" && content != "" {
		a, c, err := linerange.Parse(ls)
		if err != nil || c > len(lines) {
			r.note(n, "lines=%s is outside the block's %d lines", ls, len(lines))
			return "", true
		}
		lines = lines[a-1 : c]
	}
	if d.Args[keyStrip] == stripComments {
		lines = stripCommentLines(lines, b.Pos.File)
	}
	if sha == "" && len(lines) > r.opts.MaxLines {
		r.note(n, "%s renders %d lines, over the cap of %d; cite it instead", id, len(lines), r.opts.MaxLines)
		content = ""
	}
	title := d.Args[keyTitle]
	if title == "" {
		title = b.Symbol
	}
	if title == "" {
		title = id
	}
	caption := "**" + title + "** · " + r.anchor("`"+location(b)+"`", b, sha) + badge
	if content == "" {
		return caption, false
	}
	lang := d.Args[keyLang]
	if lang == "" {
		lang = langByExt[extract.Ext(b.Pos.File)]
	}
	fence := fenceFor(lines)
	body := fence + lang + "\n" + strings.Join(lines, "\n") + "\n" + fence
	if d.Args[keyCollapse] == block.TrueValue {
		return "<details>\n<summary>" + caption + "</summary>\n\n" + body + "\n\n</details>", false
	}
	return caption + "\n\n" + body, false
}

// chain renders the from= path of an id up to its truth (§9.8, §12).
func (r *renderer) chain(n int, id, env string) (string, bool) {
	var out []string
	seen := map[string]bool{}
	depth := 0
	for id != "" {
		if seen[id] {
			r.note(n, "chain has a from= cycle at %s", id)
			break
		}
		seen[id] = true
		b, ok := r.def(id, env)
		if !ok {
			r.note(n, "%s in chain is not defined", id)
			if depth == 0 {
				return "", true
			}
			out = append(out, strings.Repeat("  ", depth)+"- from `"+id+"` — **not defined**")
			break
		}
		v, _ := Value(b)
		item := "- "
		if depth > 0 {
			item = "- from "
		}
		item += "`" + b.ID + "`"
		if p := Provider(v, b.Args[block.KeySource]); p != "" {
			item += " " + p
		}
		if v != "" {
			item += " `" + v + "`"
		}
		item += " — " + r.anchor(location(b), b, "")
		if s := b.Args[block.KeySync]; s != "" {
			item += " · synced by `" + s + "`"
		}
		if b.IsTruth() {
			item += " · **truth**"
		}
		if b.IsLocal() {
			item += " · lives on a machine, not in git"
		}
		out = append(out, strings.Repeat("  ", depth)+item)
		id = b.From()
		depth++
	}
	return strings.Join(out, "\n"), false
}

// run renders the command and, when the caller recorded one, its result.
func (r *renderer) run(n int, d directive.Directive, id string) (string, bool) {
	cmd := d.Args[keyCmd]
	switch {
	case id != "":
		b, ok := r.def(id, d.Args[block.KeyEnv])
		if !ok {
			r.note(n, "%s is not defined", id)
			return "", true
		}
		if !b.IsRunnable() {
			r.note(n, "%s is not runnable=true", id)
		}
		cmd = strings.TrimSpace(b.Content)
	case d.Args[keyFile] != "":
		cmd = d.Args[keyFile]
	case cmd == "":
		r.note(n, "ds:run needs one of id=, cmd=, file=")
		return "", true
	}
	show := d.Args[keyShow]
	if show == showNone {
		return "", false
	}
	var parts []string
	if show != showOutput {
		parts = append(parts, fenced("sh", cmd))
	}
	res, ok := r.opts.Runs[n]
	if !ok {
		if show != showOutput {
			parts = append(parts, "_not run yet_")
		}
		return strings.Join(parts, "\n\n"), false
	}
	if show != showCommand {
		status := "ok"
		if !res.OK {
			status = "failed"
		}
		parts = append(parts, "_"+status+" · as of "+res.At.UTC().Format(time.RFC3339)+"_")
		if res.Output != "" {
			parts = append(parts, fenced("text", strings.TrimRight(res.Output, "\n")))
		}
	}
	return strings.Join(parts, "\n\n"), false
}

// table renders records from the hook as a markdown table.
func (r *renderer) table(n int, d directive.Directive) (string, bool) {
	empty := d.Args[keyEmpty]
	if r.opts.Records == nil {
		r.note(n, "no record source configured for ds:table kind=%s", d.Args[keyKind])
		return empty, false
	}
	rows, err := r.opts.Records(d.Args)
	if err != nil {
		r.note(n, "record source: %v", err)
		return empty, false
	}
	if len(rows) == 0 {
		return empty, false
	}
	cols := d.List(keyCols)
	if len(cols) == 0 {
		set := map[string]bool{}
		for _, row := range rows {
			for k := range row {
				set[k] = true
			}
		}
		for k := range set {
			cols = append(cols, k)
		}
		sort.Strings(cols)
	}
	var sb strings.Builder
	sb.WriteString("| " + strings.Join(cols, " | ") + " |\n|")
	for range cols {
		sb.WriteString("---|")
	}
	for _, row := range rows {
		sb.WriteString("\n|")
		for _, c := range cols {
			sb.WriteString(" " + strings.ReplaceAll(row[c], "|", "\\|") + " |")
		}
	}
	return sb.String(), false
}

// anchor is text linked to b's permalink, or text with the defining
// repository named when b has no link (see link).
func (r *renderer) anchor(text string, b block.Block, sha string) string {
	url := r.link(b, sha)
	if url == "" {
		return text + " (in " + b.Args[block.KeyRepo] + ")"
	}
	return "[" + text + "](" + url + ")"
}

// link builds the permalink for a block, at sha when given. A block another
// repository defines takes ForeignLink's URL; without one it has no link
// at all, because the path is the other repository's, and a link relative
// to this one named a file that is not here (bug 105).
func (r *renderer) link(b block.Block, sha string) string {
	if r.opts.Link != nil {
		return r.opts.Link(b)
	}
	if b.Args[block.KeyRepo] != "" {
		if r.opts.ForeignLink != nil {
			if url, ok := r.opts.ForeignLink(b); ok {
				return url
			}
		}
		return ""
	}
	if sha == "" {
		sha = r.opts.Commit
	}
	rel := relativeTo(r.doc, b.Pos.File)
	rep := strings.NewReplacer("{sha}", sha, "{file}", b.Pos.File, "{rel}", rel, "{start}", strconv.Itoa(b.Pos.Start), "{end}", strconv.Itoa(b.Pos.End))
	return rep.Replace(r.opts.Permalink)
}

// relativeTo returns file, a repository-relative slash path, relative to
// the directory holding doc: from docs/a/page.md, billing/x.go is
// ../../billing/x.go and docs/a/y.go is y.go. Paths are compared element by
// element, so docs2/ is never mistaken for a child of docs/.
func relativeTo(doc, file string) string {
	dir := path.Dir(doc)
	if dir == "." {
		return file
	}
	from := strings.Split(dir, "/")
	to := strings.Split(file, "/")
	common := 0
	for common < len(from) && common < len(to)-1 && from[common] == to[common] {
		common++
	}
	return strings.Repeat("../", len(from)-common) + strings.Join(to[common:], "/")
}

// Value returns a block's single-line value: its content when it is exactly
// one line. Multi-line blocks have no value (§9.3).
func Value(b block.Block) (string, bool) {
	v := strings.TrimSpace(b.Content)
	if v == "" || strings.Contains(v, "\n") {
		return "", false
	}
	return v, true
}

// Provider names the secret provider from an address shape, falling back to
// the declared source (§12). Empty when neither says.
func Provider(addr, source string) string {
	switch {
	case strings.Contains(addr, "secrets."):
		return ProviderGitHub
	case strings.HasPrefix(addr, "op://"):
		return ProviderOnePassword
	case strings.HasPrefix(addr, "arn:aws:secretsmanager:"):
		return ProviderAWS
	case strings.HasPrefix(addr, "projects/") && strings.Contains(addr, "/secrets/"):
		return ProviderGCP
	case strings.HasPrefix(addr, "vault:"):
		return ProviderVault
	}
	return source
}

// formatValue applies a cfg format= (§9.3).
func formatValue(v, format string) string {
	switch format {
	case FormatCode:
		return "`" + v + "`"
	case FormatQuote:
		return "\"" + v + "\""
	case FormatLink:
		return "[" + v + "](" + v + ")"
	case FormatHost:
		if u, err := url.Parse(v); err == nil && u.Host != "" {
			return u.Host
		}
		return v
	case FormatCompact:
		return compact(v)
	}
	return v
}

// compact turns 1200000 into 1.2M; non-numbers pass through.
func compact(v string) string {
	f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64)
	if err != nil {
		return v
	}
	units := []struct {
		div float64
		s   string
	}{{1e9, "B"}, {1e6, "M"}, {1e3, "K"}}
	for _, u := range units {
		if f >= u.div {
			s := strconv.FormatFloat(f/u.div, 'f', 1, 64)
			return strings.TrimSuffix(s, ".0") + u.s
		}
	}
	return v
}

// location formats file:start-end.
func location(b block.Block) string {
	if b.Pos.End > b.Pos.Start {
		return fmt.Sprintf("%s:%d-%d", b.Pos.File, b.Pos.Start, b.Pos.End)
	}
	return fmt.Sprintf("%s:%d", b.Pos.File, b.Pos.Start)
}

// stripCommentLines drops lines that are only a line comment in the file's
// style, keeping code and blank lines.
func stripCommentLines(lines []string, file string) []string {
	st, ok := extract.StyleFor(file)
	if !ok || len(st.Line) == 0 {
		return lines
	}
	out := lines[:0:0]
	for _, l := range lines {
		t := strings.TrimSpace(l)
		drop := false
		for _, p := range st.Line {
			if strings.HasPrefix(t, p) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, l)
		}
	}
	return out
}

// fenced wraps text in a code fence with the given info string, using a run
// of backticks longer than any inside the text. A command's output is
// whatever the command printed: a fixed three-backtick fence was closed early
// by output that held one, and the rest of the page rendered as code.
func fenced(info, text string) string {
	f := fenceFor(strings.Split(text, "\n"))
	return f + info + "\n" + text + "\n" + f
}

// fenceFor picks a backtick fence longer than any run inside the content.
func fenceFor(lines []string) string {
	longest := 0
	for _, l := range lines {
		run := 0
		for _, c := range l {
			if c == '`' {
				run++
				if run > longest {
					longest = run
				}
			} else {
				run = 0
			}
		}
	}
	if longest < 3 {
		return "```"
	}
	return strings.Repeat("`", longest+1)
}

// wholeComment reports whether the line is exactly one HTML comment and
// returns its body.
func wholeComment(l string) (string, bool) {
	inner, ok := rawComment(l)
	return strings.TrimSpace(inner), ok
}

// HTML comment delimiters.
const (
	commentOpen  = "<!--"
	commentClose = "-->"
)

// htmlComments is the comment style render reads block-position directives
// in: the HTML comment, as wholeComment does.
var htmlComments = extract.Style{BlockOpen: commentOpen, BlockClose: commentClose}

// rawComment is wholeComment without trimming the inside, which a
// continuation line needs: it is recognised by the whitespace its body
// starts with (directive.Fold).
func rawComment(l string) (string, bool) {
	t := strings.TrimSpace(l)
	// The opener and closer must not overlap: "<!-->" has both as prefix
	// and suffix, and slicing between them ran backwards.
	if len(t) < len(commentOpen)+len(commentClose) || !strings.HasPrefix(t, commentOpen) || !strings.HasSuffix(t, commentClose) {
		return "", false
	}
	inner := t[len(commentOpen) : len(t)-len(commentClose)]
	if strings.Contains(inner, "-->") {
		return "", false
	}
	return inner, true
}

// Fragment renders the repo-mode copy of a block-position reference: the
// caption and fence exactly as `refresh` writes them and `check` expects
// them (§9.2). Permalinks inside a committed copy are always repo-relative,
// never commit-pinned, so a new commit does not make every copy tampered.
// ok is false when the reference cannot render (missing def, secret, out of
// range); notes carry why.
//
// A copy of another repository's block names that repository and carries
// no link (bug 105): its URL comes from the workspace file, which a frozen
// check does not read, so a linked copy written by refresh would read as
// tampered in CI.
func Fragment(b block.Block, ref block.Reference, prefix string, maxLines int) (text string, ok bool, notes []Note) {
	r := &renderer{opts: Options{Prefix: prefix, MaxLines: maxLines, Permalink: DefaultPermalink}, doc: ref.Pos.File, defs: map[string][]block.Block{b.ID: {b}}}
	if r.opts.MaxLines <= 0 {
		r.opts.MaxLines = DefaultMaxLines
	}
	d := directive.Directive{Verb: extract.VerbBlock, Args: ref.Args}
	if d.Args == nil {
		d.Args = map[string]string{block.KeyID: b.ID}
	}
	out, keep := r.blockCode(ref.Pos.Start, d, b.ID)
	return out, !keep, r.notes
}

// Closer is the line that ends a repo-mode region and records the block
// hash it was rendered from.
func Closer(prefix, shortHash string) string {
	return "<!-- /" + prefix + ":block hash=" + shortHash + " -->"
}
