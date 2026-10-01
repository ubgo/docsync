package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/ext/structured"
	"github.com/ubgo/docsync/ext/treesitter"
	"github.com/ubgo/docsync/extract"
)

// standardRegistry is the tier order the ds binary uses: the extractors
// given to cli.WithExtractor are prepended, so the last given wins.
func standardRegistry() *extract.Registry {
	r := extract.Default()
	for _, e := range structured.All() {
		r.Prepend(e)
	}
	for _, e := range treesitter.All() {
		r.Prepend(e)
	}
	return r
}

// hashSources holds one file per tier the binary ships, each with defs over
// the shapes docs cite: functions, constants, strings in every quoting form,
// config keys and values. Comments sit on lines of their own, so every other
// non-blank character in a block is code.
var hashSources = map[string]string{
	"a.go":   "package p\n\n// ds:def id=fn-a2b6f8jk\nfunc Lock(n int) error {\n\tif n > 3 {\n\t\treturn errors.New(\"account locked: \" + fmt.Sprint(n))\n\t}\n\treturn nil\n}\n\n// ds:def id=raw-b3c7g9kl\nconst Query = `select id from users where ok = true`\n\n// ds:def id=grp-c4d8h2lm\nconst (\n\tTTL   = 30 * time.Minute\n\tLabel = \"ttl\"\n)\n\n// ds:def id=typ-d5e9j3mn\ntype Opts struct {\n\tPort int `json:\"port\"`\n\tHost string\n}\n",
	"a.ts":   "// ds:def id=fn-e6f2k4np\nexport function greet(name: string): string {\n  return `hello ${name}, welcome` + '!'\n}\n\n// ds:def id=obj-f7g3l5pq\nexport const limits = { max: 10, label: \"max\" }\n",
	"a.tsx":  "// ds:def id=cmp-g8h4m6qr\nexport function Badge() {\n  return <span className=\"badge\">new</span>\n}\n",
	"a.js":   "// ds:def id=fn-h2j5k7rs\nfunction url() {\n  return 'https://example.com/v1'\n}\n",
	"a.py":   "# ds:def id=fn-j3k6m8st\ndef lock(n):\n    if n > 3:\n        return f\"locked {n} times\"\n    return 'ok'\n\n# ds:def id=cls-k4m7n9tu\nclass Limits:\n    MAX = 10\n    NAME = \"limits\"\n",
	"a.sql":  "-- ds:def id=q-m5n8p2uv\nSELECT id, name FROM users WHERE status = 'active' AND age > 21;\n",
	"a.yaml": "server:\n  # ds:def id=port-n6p9q3vw\n  port: 8080\n  # ds:def id=host-p7q2r4wx\n  host: \"api.example.com\"\n",
	"a.toml": "[server]\n# ds:def id=port-q8r3s5xy\nport = 8080\n# ds:def id=name-r2s4t6yz\nname = \"api\"\n",
	"a.tf":   "# ds:def id=var-s3t5u7za\nvariable \"region\" {\n  default = \"us-east-1\"\n}\n",
	"a.rs":   "// ds:def id=fn-t4u6v8ab\npub fn limit() -> u32 {\n    let s = \"cap\";\n    42\n}\n",
	"a.env":  "# ds:def id=env-u5v7w9bc\nAPI_URL=https://example.com/api\n",
}

// TestEveryCodeCharacterIsInTheHash is the property bug 22 broke: change any
// one character of code inside a block -- not whitespace, not a comment --
// and the block must no longer look the same, in every tier the binary
// ships. "Looks the same" is the same hash under the same symbol at the same
// lines; a block that no longer binds at all is noticed too, so it counts as
// seen. Every earlier hashing test changed a number, never the text inside a
// string, which is how a whole class of change went unflagged in Go.
func TestEveryCodeCharacterIsInTheHash(t *testing.T) {
	t.Parallel()
	reg := standardRegistry()
	for file, src := range hashSources {
		ex, err := reg.For(file)
		if err != nil {
			t.Fatal(err)
		}
		base := ex.Extract(file, []byte(src), "ds")
		if len(base.Defs) == 0 {
			t.Errorf("%s (%s): no defs extracted", file, ex.Name())
			continue
		}
		lines := strings.SplitAfter(src, "\n")
		for _, d := range base.Defs {
			b := d.Block
			missed := 0
			var first string
			for ln := b.Pos.Start; ln <= b.Pos.End; ln++ {
				text := lines[ln-1]
				if isCommentLine(text) {
					continue
				}
				for col := 0; col < len(text); col++ {
					mutated, ok := mutate(lines, ln-1, col)
					if !ok {
						continue
					}
					if sameBlock(b, ex.Extract(file, []byte(mutated), "ds")) {
						missed++
						if first == "" {
							first = fmt.Sprintf("line %d col %d (%q)", ln, col+1, text[col])
						}
					}
				}
			}
			if missed > 0 {
				t.Errorf("%s (%s) %s: %d one-character code changes left the block looking unchanged; first at %s", file, ex.Name(), b.ID, missed, first)
			}
		}
	}
}

// isCommentLine reports a line that is only a comment in any of the
// fixtures' languages.
func isCommentLine(l string) bool {
	t := strings.TrimSpace(l)
	return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "--")
}

// mutate replaces the character at col of line i with another of its kind,
// returning false for characters that are not letters or digits.
func mutate(lines []string, i, col int) (string, bool) {
	c := lines[i][col]
	var r byte
	switch {
	case c >= '0' && c <= '9':
		r = '7'
		if c == '7' {
			r = '8'
		}
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		r = 'q'
		if c == 'q' {
			r = 'x'
		}
	default:
		return "", false
	}
	cp := append([]string(nil), lines...)
	cp[i] = cp[i][:col] + string(r) + cp[i][col+1:]
	return strings.Join(cp, ""), true
}

// sameBlock reports whether the mutated extraction still has b with the same
// hash, symbol and lines -- a change nothing downstream could see.
func sameBlock(b block.Block, f extract.Found) bool {
	for _, d := range f.Defs {
		if d.Block.ID == b.ID {
			return d.Block.Hash == b.Hash && d.Block.Symbol == b.Symbol && d.Block.Pos == b.Pos
		}
	}
	return false
}
