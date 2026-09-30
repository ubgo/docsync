package structured

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/extract"
)

// TestAnchoringANestedKeyKeepsTheParentHash is bug 4's invariant for the
// structured tiers: `ds def` anchoring a child key writes its carrier inside
// the parent map's extent, and that must not change the parent's hash — or
// every sentence citing the map flags for a change nobody made. The carrier
// stays in Content, which is what renders.
func TestAnchoringANestedKeyKeepsTheParentHash(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		ex            extract.Extractor
		path          string
		before, after string
	}{
		{
			"yaml", YAML(), "a.yaml",
			"# ds:def id=server-k7m2p4xq\nserver:\n  host: x\n  port: 8081\n",
			"# ds:def id=server-k7m2p4xq\nserver:\n  host: x\n  # ds:def id=port-t4k2b9rf\n  port: 8081\n",
		},
		{
			"toml", TOML(), "a.toml",
			"# ds:def id=server-k7m2p4xq\n[server]\nhost = \"x\"\nport = 8081\n",
			"# ds:def id=server-k7m2p4xq\n[server]\nhost = \"x\"\n# ds:def id=port-t4k2b9rf\nport = 8081\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			get := func(src string) (string, string) {
				for _, d := range tc.ex.Extract(tc.path, []byte(src), "ds").Defs {
					if d.Block.ID == "server-k7m2p4xq" {
						return d.Block.Hash, d.Block.Content
					}
				}
				t.Fatalf("no server def in\n%s", src)
				return "", ""
			}
			h1, _ := get(tc.before)
			h2, content := get(tc.after)
			if h1 != h2 {
				t.Error("anchoring a child key changed the parent map's hash")
			}
			if !strings.Contains(content, "port-t4k2b9rf") {
				t.Errorf("content must keep the carrier: %q", content)
			}
		})
	}
}
