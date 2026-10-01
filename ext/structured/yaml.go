// Package structured is the structured tier (docs/SPEC.md §10): extractors
// that parse a format instead of reading it line by line, so a def binds to
// a node's exact extent. It lives outside the root module because it
// depends on format parsers; the root's config tier keeps working without
// it, in line mode.
//
// YAML is the first format. A directive on a key's line binds that key's
// entry: a scalar (including a multi-line block scalar) as its value, a
// mapping or sequence as its whole extent. A directive on its own line binds
// the next entry. Register it ahead of the built-in tiers:
//
//	docsync.New(docsync.WithExtractor(structured.YAML()))
package structured

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
	"github.com/ubgo/docsync/extract"
	"gopkg.in/yaml.v3"
)

var _ = directive.Separator

// yamlExts are the extensions this tier claims.
var yamlExts = map[string]bool{".yaml": true, ".yml": true}

// yamlMarkers is YAML's only comment syntax.
var yamlMarkers = []string{"#"}

// ErrYAML wraps a parse failure; the file then falls back to the line-mode
// config tier so no directive is lost.
var ErrYAML = errors.New("structured: yaml parse failed; fell back to line mode")

// yamlTier implements extract.Extractor.
type yamlTier struct{}

// YAML returns the YAML extractor.
func YAML() extract.Extractor { return yamlTier{} }

func (yamlTier) Name() string { return "yaml" }

func (yamlTier) Match(p string) bool { return yamlExts[strings.ToLower(path.Ext(p))] }

// entry is one key with the extent of its value.
type entry struct {
	path      string
	start     int // key line
	end       int // last line of the entry
	isScalar  bool
	scalarVal string
	// span and hasSpan carry the def's `span=+N`, which replaces the
	// entry's own extent with the bound line plus N lines.
	span    int
	hasSpan bool
}

// Extract binds directives to YAML entries.
func (yamlTier) Extract(p string, src []byte, prefix string) extract.Found {
	var f extract.Found
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		f = extract.Config{}.Extract(p, src, prefix)
		f.Problems = append(f.Problems, extract.Problem{Pos: block.Position{Start: 1, End: 1}, Err: fmt.Errorf("%w: %v", ErrYAML, err)})
		return f
	}
	bindEntries(lines, collect(&root, "", len(lines), lines), prefix, yamlMarkers, &f)
	return f
}

// def builds the bound block for an entry.
func def(d directive.Directive, id string, e entry, pos block.Position, lines []string, carriers map[int]bool) extract.Def {
	b := block.Block{ID: id, Kind: block.KindKey, Symbol: e.path, Pos: block.Position{Start: e.start, End: e.end}, DirectivePos: pos, Carrier: block.CarrierComment, Args: d.Args}
	if e.isScalar {
		// A block scalar's final newline is part of its value but not of
		// what a reader or a hash should see.
		b.SetContent(strings.TrimRight(e.scalarVal, "\n"))
	} else {
		// The hash leaves out a nested key's carrier, so anchoring a child
		// never rehashes the map that holds it (extract.HashedJoin).
		b.SetContentHashed(strings.Join(lines[e.start-1:e.end], "\n"), extract.HashedJoin(lines, e.start, e.end, carriers))
	}
	return extract.Def{Directive: d, Block: b}
}

// nextEntryLine returns the first entry starting at or after line.
func nextEntryLine(entries []entry, line int) int {
	best := 0
	for _, e := range entries {
		if e.start >= line && (best == 0 || e.start < best) {
			best = e.start
		}
	}
	return best
}

// collect walks the document and records every mapping entry and sequence
// item with its extent. parentEnd bounds the last child.
func collect(n *yaml.Node, prefix string, parentEnd int, lines []string) []entry {
	var out []entry
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			out = append(out, collect(c, prefix, parentEnd, lines)...)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			end := trimBack(lines, parentEnd)
			if i+2 < len(n.Content) {
				end = trimBack(lines, n.Content[i+2].Line-1)
			}
			p := k.Value
			if prefix != "" {
				p = prefix + "." + k.Value
			}
			out = append(out, newEntry(p, k, v, end))
			out = append(out, collect(v, p, end, lines)...)
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			end := trimBack(lines, parentEnd)
			if i+1 < len(n.Content) {
				end = trimBack(lines, n.Content[i+1].Line-1)
			}
			p := fmt.Sprintf("%s[%d]", prefix, i)
			out = append(out, newEntry(p, item, item, end))
			out = append(out, collect(item, p, end, lines)...)
		}
	}
	return out
}

func newEntry(p string, k, v *yaml.Node, end int) entry {
	e := entry{path: p, start: k.Line, end: end}
	if v.Kind == yaml.ScalarNode {
		e.isScalar = true
		e.scalarVal = v.Value
	}
	// Flow collections put several entries on one line; an entry can never
	// end before it starts.
	if e.end < e.start {
		e.end = e.start
	}
	return e
}

// trimBack moves end up past blank and comment-only lines.
func trimBack(lines []string, end int) int {
	for end > 1 {
		t := strings.TrimSpace(lines[end-1])
		if t == "" || markerAt(t, yamlMarkers) != "" {
			end--
			continue
		}
		break
	}
	return end
}
