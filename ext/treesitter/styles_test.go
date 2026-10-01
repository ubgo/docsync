package treesitter

import (
	"testing"

	"github.com/ubgo/docsync/extract"
)

// TestEveryGrammarTypeHasAStyle is bug 46: the TypeScript, JavaScript and
// Python grammars claimed .mts, .cts, .cjs and .pyi, but directives are read
// through the root's comment-style table, which had no entry for them -- so
// a scan of those files found no directive at all, silently, and `ds def`
// refused to write one.
func TestEveryGrammarTypeHasAStyle(t *testing.T) {
	t.Parallel()
	for _, g := range []Grammar{goGrammar, typescriptGrammar, tsxGrammar, javascriptGrammar, pythonGrammar, sqlGrammar} {
		for _, e := range g.Exts {
			if _, ok := extract.StyleFor("f" + e); !ok {
				t.Errorf("%s claims %s, which has no comment style", g.Name, e)
			}
		}
	}
	for path, src := range map[string]string{
		"a.mts": "// ds:def id=a-k7m2p4xq\nexport const a = 1;\n",
		"a.cts": "// ds:def id=a-k7m2p4xq\nexport const a = 1;\n",
		"a.cjs": "// ds:def id=a-k7m2p4xq\nconst a = 1;\n",
		"a.pyi": "# ds:def id=a-k7m2p4xq\nX: int\n",
	} {
		var tier extract.Extractor
		for _, e := range All() {
			if e.Match(path) {
				tier = e
				break
			}
		}
		if f := tier.Extract(path, []byte(src), "ds"); len(f.Defs) != 1 || f.Defs[0].Block.Pos.Start != 2 {
			t.Errorf("%s: defs %+v problems %v", path, f.Defs, f.Problems)
		}
	}
}
