package structured

import (
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
	"github.com/ubgo/docsync/extract"
)

// bindEntries is the format-independent half of every structured tier:
// given the file's lines and the entries a parser found, it reads
// directives from comments and binds each def to the entry on its line
// (trailing) or the next entry below it (standalone). markers are the
// comment openers the format allows.
func bindEntries(lines []string, entries []entry, prefix string, markers []string, f *extract.Found) {
	byLine := map[int]entry{}
	for _, e := range entries {
		if _, dup := byLine[e.start]; !dup {
			byLine[e.start] = e
		}
	}
	head := prefix + directive.Separator
	// Defs are built once the whole file has been read, because a block's
	// hash leaves out every standalone carrier inside it, including ones
	// that come after its own directive (extract.HashedJoin). Remote defs
	// wait in the same list so the defs keep their file order.
	type bound struct {
		d      directive.Directive
		id     string
		e      entry
		pos    block.Position
		remote bool
	}
	var defs []bound
	carriers := map[int]bool{}
	for i := 0; i < len(lines); i++ {
		text, trailing, ok := commentAt(lines[i], head, markers)
		if !ok {
			continue
		}
		// Standalone directives may continue on following comment lines.
		consumed := 1
		if !trailing {
			var body []string
			for j := i; j < len(lines); j++ {
				t := strings.TrimSpace(lines[j])
				m := markerAt(t, markers)
				if m == "" {
					break
				}
				// Keep the indentation after the marker: Fold reads it as a
				// continuation marker.
				body = append(body, strings.TrimPrefix(t, m))
			}
			text, consumed = directive.Fold(body)
		}
		pos := block.Position{Start: i + 1, End: i + consumed}
		d, err := directive.Parse(prefix, text)
		if err != nil {
			f.Problems = append(f.Problems, extract.Problem{Pos: pos, Err: err})
			i += consumed - 1
			continue
		}
		if !trailing {
			for l := pos.Start; l <= pos.End; l++ {
				carriers[l] = true
			}
		}
		if d.Verb != extract.VerbDef {
			f.Refs = append(f.Refs, extract.Ref{Directive: d, Reference: block.Reference{Verb: d.Verb, ID: d.Args[block.KeyID], Pos: pos, Carrier: block.CarrierComment, Args: d.Args}})
			i += consumed - 1
			continue
		}
		id := d.Args[block.KeyID]
		if id == "" {
			f.Problems = append(f.Problems, extract.Problem{Pos: pos, Err: extract.ErrNoID})
			i += consumed - 1
			continue
		}
		if _, remote := d.Args[block.KeyFile]; remote {
			defs = append(defs, bound{d: d, id: id, pos: pos, remote: true})
			i += consumed - 1
			continue
		}
		target := i + 1
		if !trailing {
			target = nextEntryLine(entries, i+consumed+1)
		}
		e, ok := byLine[target]
		if !ok && trailing {
			// A trailing directive on a later line of a multi-line value
			// (a block scalar, a multi-line string) binds the entry that
			// spans the line: the innermost one, which starts last.
			e, ok = covering(entries, target)
		}
		if !ok {
			f.Problems = append(f.Problems, extract.Problem{Pos: pos, Err: extract.ErrNothingToBind})
			i += consumed - 1
			continue
		}
		n, hasSpan, err := extract.ParseSpan(d)
		if err != nil {
			f.Problems = append(f.Problems, extract.Problem{Pos: pos, Err: err})
			i += consumed - 1
			continue
		}
		if hasSpan {
			// span=+N is the bound line plus N, as in the line-mode config
			// tier these formats fall back to without this module. It was
			// accepted and ignored here, so the same def bound different
			// lines depending on which build scanned it (bug 49). The end
			// is set once every carrier is known, below.
			e.span, e.hasSpan = n, true
		}
		defs = append(defs, bound{d: d, id: id, e: e, pos: pos})
		i += consumed - 1
	}
	for _, b := range defs {
		if b.remote {
			f.Defs = append(f.Defs, extract.Def{Directive: b.d, Remote: true, Block: block.Block{ID: b.id, DirectivePos: b.pos, Carrier: block.CarrierComment, Args: b.d.Args}})
			continue
		}
		if end := extract.SpanEnd(lines, b.e.start, b.e.span+1, carriers); b.e.hasSpan && end != b.e.end {
			// A span that changes the extent makes the block those lines; one
			// that does not leaves a scalar its value.
			b.e.end = end
			b.e.isScalar = false
		}
		f.Defs = append(f.Defs, def(b.d, b.id, b.e, b.pos, lines, carriers))
	}
}

// covering returns the entry spanning line that starts last.
func covering(entries []entry, line int) (entry, bool) {
	var best entry
	found := false
	for _, e := range entries {
		if e.start <= line && line <= e.end && (!found || e.start > best.start) {
			best, found = e, true
		}
	}
	return best, found
}

// markerAt returns the comment marker t starts with, or "".
func markerAt(t string, markers []string) string {
	for _, m := range markers {
		if strings.HasPrefix(t, m) {
			return m
		}
	}
	return ""
}

// commentAt finds a directive comment on a line. trailing is true when code
// precedes it. A marker inside a quoted value is not a comment, and a
// marker glued to a word (`a#b`, `http://`) is not one either. A line
// that closes a multi-line string leaves the quote count odd; the scan
// then runs again without quote tracking, because a directive after the
// closing delimiter is a real comment.
func commentAt(line, head string, markers []string) (text string, trailing, ok bool) {
	text, trailing, ok, open := scanComment(line, head, markers, true)
	if !ok && open {
		text, trailing, ok, _ = scanComment(line, head, markers, false)
	}
	return text, trailing, ok
}

// scanComment is one pass of commentAt; open reports an unclosed quote at
// the end of the line.
func scanComment(line, head string, markers []string, quotes bool) (text string, trailing, ok, open bool) {
	var q byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			}
		case quotes && (c == '"' || c == '\''):
			q = c
		case (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			m := markerAt(line[i:], markers)
			if m == "" {
				continue
			}
			body := strings.TrimSpace(line[i+len(m):])
			if !strings.HasPrefix(body, head) {
				return "", false, false, false
			}
			return body, strings.TrimSpace(line[:i]) != "", true, false
		}
	}
	return "", false, false, q != 0
}
