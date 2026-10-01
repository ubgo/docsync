package extract

import (
	"regexp"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
	"github.com/ubgo/docsync/internal/mdspan"
	"github.com/ubgo/docsync/sentence"
)

// Markdown is the document tier (§7.1, §10): markdown, MDX, and html-family
// files. Directives sit in html comments (`<!-- ds:… -->`), JSX comments in
// MDX, or link targets. A `ds:def` comment above a heading binds the section
// until the next heading of the same or higher level; above a paragraph it
// binds that paragraph; above an html element it binds through the matching
// close tag on the same nesting level. An inline def link `[8081](ds:def?…)`
// binds its own link text, which is how a fact lives in prose.
//
// Code is never a carrier: a directive inside a fenced block, an indented
// code block, or an inline code span is an example, not a directive.
// Frontmatter is skipped entirely and is never part of a block.
type Markdown struct{}

var markdownExts = map[string]bool{".md": true, ".mdx": true, ".markdown": true}

var htmlExts = map[string]bool{".html": true, ".htm": true, ".xml": true, ".svg": true}

// Prose implements ProseTier: markdown paragraphs are often one long line.
func (Markdown) Prose() bool { return true }

// Name implements Extractor.
func (Markdown) Name() string { return "markdown" }

// Match implements Extractor.
func (Markdown) Match(p string) bool { return extIn(p, markdownExts) || extIn(p, htmlExts) }

// atxRE matches an ATX heading and captures the hashes and the text.
var atxRE = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)

// inlineLinkRE matches `[text](target)` and captures both. A preceding `!`
// (image) is captured so images are not treated as defs.
var inlineLinkRE = regexp.MustCompile(`(!?)\[([^\]]*)\]\(([^)\s]+)\)`)

// Extract implements Extractor.
func (Markdown) Extract(p string, src []byte, prefix string) Found {
	var f Found
	lines := splitLines(src)
	st, _ := StyleFor(p)
	if st.BlockOpen == "" {
		st = Styles[".md"]
	}
	html := extIn(p, htmlExts)
	if !html {
		f.Page = frontmatterPage(lines)
	}
	masked := proseLines(lines, html)
	occs := scanComments(masked, st, prefix, &f)
	carrier := carrierLines(occs)
	for _, o := range occs {
		switch o.dir.Verb {
		case VerbDef:
			bindMarkdown(o, lines, html, carrier, &f)
		default:
			carrier := block.CarrierBlock
			if o.trailing {
				carrier = block.CarrierComment
			}
			r := block.Reference{Verb: o.dir.Verb, ID: o.dir.Args[block.KeyID], Pos: o.pos, Carrier: carrier, Args: o.dir.Args}
			if o.trailing {
				// The sentence ends where the comment starts; the rest of the
				// line is the directive, not prose.
				withCode := append([]string(nil), lines...)
				withCode[o.pos.Start-1] = o.code
				r.SetSentence(paragraphSentence(withCode, masked, o.pos.Start-1, len(strings.TrimRight(o.code, " \t"))))
			} else if o.dir.Verb == VerbBlock {
				r.Region = region(lines, o.pos.End+1, prefix)
			}
			f.Refs = append(f.Refs, Ref{Directive: o.dir, Reference: r})
		}
	}
	// Link-form directives, including inline defs.
	head := prefix + directive.Separator
	for i, l := range masked {
		// Matched on the masked line, so a link inside code is not one;
		// read from the original, so a value written in code — `8081` —
		// is its text and not the blanks that stood in for it.
		orig := lines[i]
		for _, m := range inlineLinkRE.FindAllStringSubmatchIndex(l, -1) {
			target := orig[m[6]:m[7]]
			if !strings.HasPrefix(target, head) {
				continue
			}
			d, err := directive.ParseLink(prefix, target)
			pos := block.Position{Start: i + 1, End: i + 1}
			if err != nil {
				f.Problems = append(f.Problems, Problem{Pos: pos, Err: err})
				continue
			}
			text := orig[m[4]:m[5]]
			if d.Verb == VerbDef && m[2] != m[3] {
				// `![alt](ds:def?…)`: an image has no text to be the value.
				f.Problems = append(f.Problems, Problem{Pos: pos, Err: ErrDefOnImage})
				continue
			}
			if d.Verb == VerbDef {
				id, ok := requireID(d, pos, &f)
				if !ok {
					continue
				}
				if isRemote(d) {
					f.Defs = append(f.Defs, remoteDef(d, id, pos, block.CarrierLink))
					continue
				}
				f.Defs = append(f.Defs, newDef(d, id, block.KindLinkText, text, pos, pos, block.CarrierLink, text))
				continue
			}
			r := block.Reference{Verb: d.Verb, ID: d.Args[block.KeyID], Pos: pos, Carrier: block.CarrierLink, Args: d.Args}
			r.SetSentence(paragraphSentence(lines, masked, i, m[0]))
			f.Refs = append(f.Refs, Ref{Directive: d, Reference: r})
		}
	}
	return f
}

// closerRE matches the repo-mode closer; group 1 is the hash.
func closerRE(prefix string) *regexp.Regexp {
	return regexp.MustCompile(`^\s*<!--\s*/` + regexp.QuoteMeta(prefix) + `:block(?:\s+hash=([0-9a-f]+))?\s*-->\s*$`)
}

// fence follows CommonMark fenced code blocks line by line: a line opening
// with a run of three or more backticks or tildes starts one, and only a
// line holding a run of the same character at least as long, and nothing
// else, closes it. The looser rule it replaces closed a four-backtick fence
// at the first three-backtick line inside it, which is exactly the nesting
// render.fenceFor produces when a copied block itself contains a fence.
type fence struct {
	marker string // the opening run; empty outside a fence
}

// step reports whether line is part of a fence — its opening line, a line
// inside it, or its closing line — and advances the state.
func (f *fence) step(line string) bool {
	t := strings.TrimSpace(line)
	if f.marker != "" {
		if strings.HasPrefix(t, f.marker) && strings.Trim(t, f.marker[:1]) == "" {
			f.marker = ""
		}
		return true
	}
	if !strings.HasPrefix(t, "```") && !strings.HasPrefix(t, "~~~") {
		return false
	}
	n := 0
	for n < len(t) && t[n] == t[0] {
		n++
	}
	f.marker = t[:n]
	return true
}

// Region is region for other packages: render replaces an existing
// repo-mode copy with its fresh rendering by the same rule the scanner
// finds it by, so the two can never disagree about where a copy ends. from
// is the 1-based line after the directive.
func Region(lines []string, from int, prefix string) *block.Region {
	return region(lines, from, prefix)
}

// region finds a repo-mode fence after a block-position directive: the
// lines from the directive up to a closer comment, with no other directive
// in between. Absent closer means build mode.
func region(lines []string, from int, prefix string) *block.Region {
	re := closerRE(prefix)
	head := prefix + directive.Separator
	var fc fence
	for i := from; i <= len(lines); i++ {
		l := lines[i-1]
		// A copy holds the block's code in a fence, and that code may show a
		// directive or a closer as text. Neither ends the region: before this
		// a copied block that mentioned a citation was not recognised, and
		// every refresh appended another copy after it.
		if fc.step(l) {
			continue
		}
		if m := re.FindStringSubmatch(l); m != nil {
			text := strings.Join(lines[from-1:i-1], "\n")
			return &block.Region{Start: from, End: i, Hash: m[1], Text: strings.Trim(text, "\n")}
		}
		if strings.Contains(l, "<!-- "+head) || strings.Contains(l, "<!--"+head) {
			return nil
		}
	}
	return nil
}

// frontmatterPage reads the `ds:` mapping out of YAML frontmatter with the
// same line-mode reader the config tier uses: `covers: [a, b]` or a block
// list under `covers:`, and `review_every: 180d`. It returns nil when the
// page has no frontmatter or no `ds:` mapping. A full YAML parser is not
// needed for the two keys the spec defines, and using one would make the
// root module depend on it.
func frontmatterPage(lines []string) *Page {
	end := frontmatterEnd(lines)
	if end < 0 {
		return nil
	}
	var page *Page
	inDS := false
	dsIndent := 0
	for i := 1; i < end; i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		ind := indentOf(l)
		if !inDS {
			if t == "ds:" {
				inDS, dsIndent, page = true, ind, &Page{}
			}
			continue
		}
		if ind <= dsIndent {
			if strings.HasPrefix(t, "- ") {
				continue
			}
			break
		}
		key, val, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "covers":
			if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
				for _, item := range strings.Split(strings.Trim(val, "[]"), ",") {
					if s := strings.Trim(strings.TrimSpace(item), "\"'"); s != "" {
						page.Covers = append(page.Covers, s)
					}
				}
			} else if val == "" {
				for j := i + 1; j < end; j++ {
					it := strings.TrimSpace(lines[j])
					if !strings.HasPrefix(it, "- ") {
						break
					}
					if s := strings.Trim(strings.TrimSpace(it[2:]), "\"'"); s != "" {
						page.Covers = append(page.Covers, s)
					}
					i = j
				}
			}
		case "review_every":
			page.ReviewEvery = strings.Trim(val, "\"'")
		}
	}
	return page
}

// frontmatterEnd is the index of the line closing a page's frontmatter, or
// -1 when it has none. Frontmatter opens with `---` on the first line and
// must close with `---` or `...`; an opener with no closer is a thematic
// break, and the page is ordinary markdown. Reading it as frontmatter to
// the end of the file hid every directive on the page.
func frontmatterEnd(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return -1
	}
	for i := 1; i < len(lines); i++ {
		if t := strings.TrimSpace(lines[i]); t == "---" || t == "..." {
			return i
		}
	}
	return -1
}

// CarrierLines returns the lines of a document with everything that cannot
// carry a directive blanked out, keeping line numbers and, within a line,
// byte offsets: frontmatter, fenced and indented code, and inline code
// spans; for html, fenced code only. Any other path comes back unchanged.
// It is the one view of a page every reader of its directives uses, so
// what check counts is what render renders and adopt rewrites: `ds adopt`
// reading raw lines rewrote a backticked example into a live citation, and
// `ds render` rendered one.
func CarrierLines(p string, lines []string) []string {
	switch {
	case extIn(p, markdownExts):
		return proseLines(lines, false)
	case extIn(p, htmlExts):
		return proseLines(lines, true)
	}
	return lines
}

// proseLines is CarrierLines for a file known to be markdown or html.
// Backticks mean nothing in html, and neither does indentation.
func proseLines(lines []string, html bool) []string {
	mask := carrierMask(lines, html)
	out := make([]string, len(lines))
	for i, l := range lines {
		switch {
		case !mask[i]:
		case html:
			out[i] = l
		default:
			out[i] = maskCodeSpans(l)
		}
	}
	return out
}

// carrierMask reports, per line, whether directives on that line count:
// false inside frontmatter, fenced code, and (outside html) indented code.
// Before, only fences were masked, and the comment claiming backticked
// examples never registered was wrong: `[x](ds:block?id=…)` in a code span
// was a live citation, and so was each line of an indented example.
func carrierMask(lines []string, html bool) []bool {
	mask := make([]bool, len(lines))
	front := -1
	if !html {
		front = frontmatterEnd(lines)
	}
	var fc fence
	var ind indented
	for i, l := range lines {
		if i <= front {
			continue
		}
		// An open fence owns every line to its closer. Otherwise indented
		// code comes first: a fence marker inside an indented block is
		// code, while a list item's indented fence is still a fence,
		// because a list is never indented code.
		if fc.marker == "" && !html && ind.step(l) {
			continue
		}
		if fc.step(l) {
			ind.reset()
			continue
		}
		mask[i] = true
	}
	return mask
}

// indented follows CommonMark indented code blocks, conservatively: where
// the rules need container context this reader does not keep, a line is
// left as prose, which is how every line was read before. A block starts at
// a line indented four columns or more after a blank line or a heading, and
// never inside a list, where that indentation is a continuation paragraph
// of the item. It runs through blank lines while the indentation holds.
type indented struct {
	inCode    bool // inside an indented code block
	prevBlank bool // the previous line was blank, or there was none
	prevHead  bool // the previous line was an ATX heading
	inList    bool // a list item was seen and nothing at column 0 has ended the list since
	started   bool // any line has been seen
}

// step reports whether line is indented code and advances the state.
func (s *indented) step(line string) bool {
	blank := strings.TrimSpace(line) == ""
	first := !s.started
	s.started = true
	if blank {
		s.prevBlank, s.prevHead = true, false
		return s.inCode
	}
	col := indentCols(line)
	code := col >= codeIndent && (s.inCode || ((s.prevBlank || s.prevHead || first) && !s.inList))
	switch {
	case code:
	case listItemRE.MatchString(line):
		s.inList = true
	case col == 0 && s.prevBlank:
		// A paragraph, heading, or anything else at the margin after a
		// blank line ends the list; without the blank it is a lazy
		// continuation of the item.
		s.inList = false
	}
	s.inCode = code
	s.prevBlank = false
	s.prevHead = !code && atxRE.MatchString(line)
	return code
}

// reset forgets indented-code state at a fence, which ends a code block and
// is not blank.
func (s *indented) reset() {
	s.inCode, s.prevBlank, s.prevHead, s.started = false, false, false, true
}

// codeIndent is the indentation, in columns, that makes a line code.
const codeIndent = 4

// listItemRE matches a list item marker: up to three spaces, then a bullet
// or an ordered number, then a space or the end of the line.
var listItemRE = regexp.MustCompile(`^ {0,3}(?:[-*+]|\d{1,9}[.)])(?:[ \t]|$)`)

// indentCols is a line's leading indentation in columns, a tab advancing to
// the next multiple of four as CommonMark counts it.
func indentCols(line string) int {
	col := 0
	for _, c := range line {
		switch c {
		case ' ':
			col++
		case '\t':
			col += codeIndent - col%codeIndent
		default:
			return col
		}
	}
	return col
}

// maskCodeSpans blanks every inline code span in line, backticks included,
// with spaces, so offsets into the line stay valid. The span rule is
// mdspan's, shared with the sentence binder.
func maskCodeSpans(line string) string {
	spans := mdspan.CodeSpans(line)
	if len(spans) == 0 {
		return line
	}
	b := []byte(line)
	for _, sp := range spans {
		for k := sp.Start; k < sp.End; k++ {
			b[k] = ' '
		}
	}
	return string(b)
}

// bindMarkdown binds a comment-form def to the section, paragraph, or element
// after it.
func bindMarkdown(o occurrence, lines []string, html bool, carrier map[int]bool, f *Found) {
	id, ok := requireID(o.dir, o.pos, f)
	if !ok {
		return
	}
	if isRemote(o.dir) {
		f.Defs = append(f.Defs, remoteDef(o.dir, id, o.pos, o.carrier))
		return
	}
	n, hasSpan, err := parseSpan(o.dir)
	if err != nil {
		f.Problems = append(f.Problems, Problem{Pos: o.pos, Err: err})
		return
	}
	start := nextNonBlank(lines, o.pos.End+1)
	if start == 0 {
		f.Problems = append(f.Problems, Problem{Pos: o.pos, Err: ErrNothingToBind})
		return
	}
	var end int
	var kind block.Kind
	var symbol string
	switch {
	case hasSpan:
		end = spanEnd(lines, start, n+1, carrier)
		kind = block.KindSpan
		if n == 0 {
			kind = block.KindLine
		}
	case !html && atxRE.MatchString(lines[start-1]):
		m := atxRE.FindStringSubmatch(lines[start-1])
		level := len(m[1])
		symbol = m[2]
		end = sectionEnd(lines, start, level, carrier)
		kind = block.KindSection
	case html || strings.HasPrefix(strings.TrimSpace(lines[start-1]), "<"):
		end = elementEnd(lines, start)
		kind = block.KindElement
		symbol = tagName(lines[start-1])
	default:
		end = blankBoundedEnd(lines, start)
		kind = block.KindParagraph
	}
	f.Defs = append(f.Defs, spanDef(o.dir, id, kind, symbol, block.Position{Start: start, End: end}, o.pos, o.carrier, lines, carrier))
}

// sectionEnd returns the last line of the section headed at start: up to but
// not including the next heading of level <= the given one, skipping fenced
// code, and trimming trailing blank lines.
func sectionEnd(lines []string, start, level int, carrier map[int]bool) int {
	end := len(lines)
	inFence := ""
	for i := start; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if inFence != "" {
			if strings.HasPrefix(t, inFence) {
				inFence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = t[:3]
			continue
		}
		if m := atxRE.FindStringSubmatch(lines[i]); m != nil && len(m[1]) <= level {
			end = i
			break
		}
	}
	return trimCarriers(lines, start, end, carrier)
}

// tagRE captures the tag name of an opening tag.
var tagRE = regexp.MustCompile(`^\s*<([A-Za-z][A-Za-z0-9:-]*)`)

func tagName(l string) string {
	if m := tagRE.FindStringSubmatch(l); m != nil {
		return m[1]
	}
	return ""
}

// elementEnd returns the last line of the element opening at start by
// counting open and close tags of the same name. A self-closing or single-line
// element ends on its own line. Unknown structure falls back to the blank
// bounded paragraph rule so a def never binds to the rest of the file.
func elementEnd(lines []string, start int) int {
	name := tagName(lines[start-1])
	if name == "" {
		return blankBoundedEnd(lines, start)
	}
	openRE := regexp.MustCompile(`<` + regexp.QuoteMeta(name) + `(?:[\s>]|/>)`)
	closeRE := regexp.MustCompile(`</` + regexp.QuoteMeta(name) + `\s*>`)
	selfClose := regexp.MustCompile(`<` + regexp.QuoteMeta(name) + `[^>]*/>`)
	depth := 0
	for i := start - 1; i < len(lines); i++ {
		l := lines[i]
		depth += len(openRE.FindAllString(l, -1)) - len(selfClose.FindAllString(l, -1)) - len(closeRE.FindAllString(l, -1))
		if depth <= 0 {
			return i + 1
		}
	}
	return blankBoundedEnd(lines, start)
}

// paragraphSentence binds the sentence around byte col of line i, reading
// the whole paragraph the line sits in, so a sentence wrapped across lines
// is one sentence (SPEC §18). Bound line by line, a citation on the second
// line of a wrapped sentence recorded only that line, and rewriting the
// first — "Note that" to "It is false that" — changed nothing its ack saw.
//
// lines is the page; prose is its carrier view (CarrierLines), which only
// decides where paragraphs end. HTML comments are blanked, so editing a
// trailing directive is never rewriting the sentence beside it.
func paragraphSentence(lines, prose []string, i, col int) string {
	start, end := paragraphBounds(lines, prose, i)
	var joined strings.Builder
	offset := 0
	for k := start; k <= end; k++ {
		text := commentSpanRE.ReplaceAllStringFunc(lines[k], func(m string) string { return strings.Repeat(" ", len(m)) })
		text = strings.TrimPrefix(strings.TrimLeft(text, " \t"), ">")
		trimmed := strings.TrimSpace(text)
		if k == i {
			lead := len(lines[k]) - len(strings.TrimLeft(lines[k], " \t"))
			if strings.HasPrefix(strings.TrimLeft(lines[k], " \t"), ">") {
				lead += 1 + len(text[1:]) - len(strings.TrimLeft(text[1:], " \t"))
			}
			offset = joined.Len() + min(max(col-lead, 0), len(trimmed))
		}
		if joined.Len() > 0 {
			joined.WriteByte(' ')
			if k == i {
				offset++
			}
		}
		joined.WriteString(trimmed)
	}
	return sentence.Bind(joined.String(), offset)
}

// commentSpanRE is an HTML comment on one line.
var commentSpanRE = regexp.MustCompile(`<!--.*?-->`)

// thematicBreak reports a markdown thematic break: up to three spaces of
// indentation, then three or more of one of -, *, _, spaces allowed between.
func thematicBreak(line string) bool {
	if indentCols(line) > 3 {
		return false
	}
	var mark rune
	n := 0
	for _, r := range line {
		switch {
		case r == ' ' || r == '\t':
		case mark == 0 && strings.ContainsRune("-*_", r):
			mark, n = r, 1
		case r == mark:
			n++
		default:
			return false
		}
	}
	return n >= 3
}

// paragraphBounds is the first and last line of the paragraph around line
// i: the lines up to a blank or masked line, a heading, a table row, a
// whole-line comment, a thematic break, or a change into or out of a
// blockquote. A list item starts a paragraph of its own, which its
// continuation lines extend, so a wrapped item still binds whole.
func paragraphBounds(lines, prose []string, i int) (int, int) {
	quote := func(k int) bool { return strings.HasPrefix(strings.TrimLeft(lines[k], " \t"), ">") }
	stops := func(k int) bool {
		t := strings.TrimSpace(lines[k])
		return t == "" || prose[k] == "" || atxRE.MatchString(t) || strings.HasPrefix(t, "|") ||
			(strings.HasPrefix(t, "<!--") && strings.HasSuffix(t, "-->")) || thematicBreak(lines[k])
	}
	if stops(i) {
		return i, i
	}
	start := i
	for start > 0 && !listItemRE.MatchString(lines[start]) && !stops(start-1) && quote(start-1) == quote(i) {
		start--
	}
	end := i
	for end+1 < len(lines) && !stops(end+1) && !listItemRE.MatchString(lines[end+1]) && quote(end+1) == quote(i) {
		end++
	}
	return start, end
}
