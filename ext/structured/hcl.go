package structured

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/extract"
)

// HCL (Terraform and friends): a directive on an attribute's line binds
// that attribute, a literal as its value and anything else (objects, lists,
// references, calls) as its full extent. A directive above a block binds
// the block through its closing brace. Paths are `type.label.label.attr`,
// the same shape `pick=hcl:` uses (§10).

var hclExts = map[string]bool{".hcl": true, ".tf": true, ".tfvars": true}

// hclMarkers are HCL's line comment openers; block comments carry no
// directives (§7.1 lists `#` for HCL).
var hclMarkers = []string{"#", "//"}

// ErrHCL wraps a parse failure; the file then falls back to the heuristic
// code tier so no directive is lost.
var ErrHCL = errors.New("structured: hcl parse failed; fell back to line mode")

type hclTier struct{}

// HCL returns the HCL extractor.
func HCL() extract.Extractor { return hclTier{} }

func (hclTier) Name() string { return "hcl" }

func (hclTier) Match(p string) bool { return hclExts[strings.ToLower(path.Ext(p))] }

// Extract binds directives to HCL attributes and blocks.
func (hclTier) Extract(p string, src []byte, prefix string) extract.Found {
	var f extract.Found
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	file, diags := hclsyntax.ParseConfig(src, p, hcl.InitialPos)
	if diags.HasErrors() {
		f = extract.Code{}.Extract(p, src, prefix)
		f.Problems = append(f.Problems, extract.Problem{Pos: block.Position{Start: 1, End: 1}, Err: fmt.Errorf("%w: %v", ErrHCL, diags.Error())})
		return f
	}
	body, _ := file.Body.(*hclsyntax.Body)
	entries := hclEntries(body, "", src)
	bindEntries(lines, entries, prefix, hclMarkers, &f)
	return f
}

// hclEntries walks a body: attributes first (sorted by line, the map has
// no order), then blocks, recursing into each block's body.
func hclEntries(body *hclsyntax.Body, prefix string, src []byte) []entry {
	var out []entry
	names := make([]string, 0, len(body.Attributes))
	for name := range body.Attributes {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return body.Attributes[names[i]].SrcRange.Start.Line < body.Attributes[names[j]].SrcRange.Start.Line
	})
	for _, name := range names {
		a := body.Attributes[name]
		en := entry{path: join(prefix, name), start: a.SrcRange.Start.Line, end: a.SrcRange.End.Line}
		if lit, ok := hclLiteral(a.Expr, src); ok {
			en.isScalar, en.scalarVal = true, lit
		}
		out = append(out, en)
	}
	for _, b := range body.Blocks {
		p := join(prefix, strings.Join(append([]string{b.Type}, b.Labels...), "."))
		out = append(out, entry{path: p, start: b.TypeRange.Start.Line, end: b.CloseBraceRange.End.Line})
		out = append(out, hclEntries(b.Body, p, src)...)
	}
	return out
}

// hclLiteral returns the text of a literal expression: numbers and bools
// as written, strings without their quotes when the template has no
// interpolation.
func hclLiteral(e hclsyntax.Expression, src []byte) (string, bool) {
	switch v := e.(type) {
	case *hclsyntax.LiteralValueExpr:
		return string(v.Range().SliceBytes(src)), true
	case *hclsyntax.TemplateExpr:
		if len(v.Parts) == 1 {
			if lit, ok := v.Parts[0].(*hclsyntax.LiteralValueExpr); ok {
				return lit.Val.AsString(), true
			}
		}
	}
	return "", false
}

func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// All returns every structured tier, for docsync.WithExtractor(All()...).
func All() []extract.Extractor { return []extract.Extractor{YAML(), TOML(), HCL()} }
