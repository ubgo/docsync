// Command ds is the standard docsync binary: the built-in verbs and tiers
// plus the shipped ext modules: the structured tier (YAML, TOML, HCL, with
// the `hcl:` pick scheme) and the tree-sitter syntax tier (Go, TypeScript,
// TSX, JavaScript, Python, SQL). Organisations wanting more, or less, build
// their own main with cli.Main and their own options (docs/SPEC.md §37.5).
// The tree-sitter grammars are cgo; a build without a C compiler can drop
// that import and keep everything else.
package main

import (
	"github.com/ubgo/docsync/cli"
	"github.com/ubgo/docsync/ext/structured"
	"github.com/ubgo/docsync/ext/treesitter"
)

func main() {
	cli.Main(cli.WithDefaults(cli.Standard()), cli.WithExtractor(structured.All()...), cli.WithExtractor(treesitter.All()...), cli.WithPicker(structured.HCLPicker()))
}
