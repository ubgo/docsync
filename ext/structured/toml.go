package structured

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/extract"
)

// TOML: a directive on a key's line binds that key's value, scalars as
// their value and arrays or inline tables as their whole extent (multi-line
// arrays included). A directive above a `[table]` or `[[array]]` header
// binds the table through the line before the next header. Paths are
// dotted, with `[[items]]` entries indexed as `items[0]`.

var tomlExts = map[string]bool{".toml": true}

// ErrTOML wraps a parse failure; the file then falls back to the line-mode
// config tier so no directive is lost.
var ErrTOML = errors.New("structured: toml parse failed; fell back to line mode")

type tomlTier struct{}

// TOML returns the TOML extractor.
func TOML() extract.Extractor { return tomlTier{} }

func (tomlTier) Name() string { return "toml" }

func (tomlTier) Match(p string) bool { return tomlExts[strings.ToLower(path.Ext(p))] }

// tomlScalars are the value kinds whose text is the entry's value.
var tomlScalars = map[unstable.Kind]bool{
	unstable.String: true, unstable.Integer: true, unstable.Float: true, unstable.Bool: true,
	unstable.DateTime: true, unstable.LocalDateTime: true, unstable.LocalDate: true, unstable.LocalTime: true,
}

// Extract binds directives to TOML entries.
func (tomlTier) Extract(p string, src []byte, prefix string) extract.Found {
	var f extract.Found
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	entries, err := tomlEntries(src, lines)
	if err != nil {
		f = extract.Config{}.Extract(p, src, prefix)
		f.Problems = append(f.Problems, extract.Problem{Pos: block.Position{Start: 1, End: 1}, Err: fmt.Errorf("%w: %v", ErrTOML, err)})
		return f
	}
	bindEntries(lines, entries, prefix, yamlMarkers, &f)
	return f
}

// tomlEntries walks the top-level expressions. Table extents are closed
// when the next header arrives, or at the end of the file.
func tomlEntries(src []byte, lines []string) ([]entry, error) {
	var parser unstable.Parser
	parser.Reset(src)
	var out []entry
	table := ""
	openTable := -1 // index in out of the table whose end is pending
	arrayIndex := map[string]int{}
	closeTable := func(before int) {
		if openTable >= 0 {
			// trimBack never moves above the header line itself, so an
			// empty table ends where it starts.
			out[openTable].end = trimBack(lines, before)
			openTable = -1
		}
	}
	for parser.NextExpression() {
		e := parser.Expression()
		switch e.Kind {
		case unstable.Table, unstable.ArrayTable:
			key, line := tomlKey(&parser, e)
			closeTable(line - 1)
			if e.Kind == unstable.ArrayTable {
				key = fmt.Sprintf("%s[%d]", key, arrayIndex[key])
				arrayIndex[strings.TrimSuffix(key, fmt.Sprintf("[%d]", arrayIndex[key]))]++
			}
			table = key
			out = append(out, entry{path: key, start: line, end: line})
			openTable = len(out) - 1
		case unstable.KeyValue:
			key, _ := tomlKey(&parser, e)
			if table != "" {
				key = table + "." + key
			}
			shape := parser.Shape(e.Raw)
			en := entry{path: key, start: shape.Start.Line, end: shape.End.Line}
			if v := e.Value(); tomlScalars[v.Kind] {
				en.isScalar, en.scalarVal = true, string(v.Data)
			}
			out = append(out, en)
		}
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	closeTable(len(lines))
	return out, nil
}

// tomlKey joins an expression's dotted key and returns the line of its
// first segment.
func tomlKey(p *unstable.Parser, e *unstable.Node) (string, int) {
	var parts []string
	line := 0
	it := e.Key()
	for it.Next() {
		k := it.Node()
		if line == 0 {
			line = p.Shape(k.Raw).Start.Line
		}
		parts = append(parts, string(k.Data))
	}
	return strings.Join(parts, "."), line
}
