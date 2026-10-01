package structured

import (
	"errors"
	"testing"

	"github.com/ubgo/docsync/extract"
)

// TestSpanWidensEveryStructuredTier is bug 49: `span=+N` was accepted and
// ignored by the YAML, TOML and HCL tiers, so the same def bound different
// lines depending on whether the build carried this module. It now means what
// it means in the line-mode config tier: the bound line plus N. A span that
// leaves the extent as it was keeps a scalar its value; a malformed one is a
// problem.
func TestSpanWidensEveryStructuredTier(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		tier      extract.Extractor
		path, src string
		end       int
		content   string
	}{
		{YAML(), "a.yaml", "# ds:def id=a-k7m2p4xq span=+1\nport: 8080\nhost: x\nother: y\n", 3, "port: 8080\nhost: x"},
		{TOML(), "a.toml", "# ds:def id=a-k7m2p4xq span=+2\nport = 8080\nhost = \"x\"\nother = 1\nlast = 2\n", 4, "port = 8080\nhost = \"x\"\nother = 1"},
		{HCL(), "a.tf", "# ds:def id=a-k7m2p4xq span=+1\nregion = \"eu\"\nzone = \"a\"\n", 3, "region = \"eu\"\nzone = \"a\""},
		{YAML(), "a.yaml", "# ds:def id=a-k7m2p4xq span=+0\nport: 8080\n", 2, "8080"},
		// A child's own carrier inside the span is not counted or hashed.
		{YAML(), "a.yaml", "# ds:def id=a-k7m2p4xq span=+1\nport: 8080\n# ds:def id=b-k7m2p4xr\nhost: x\nother: y\n", 4, "port: 8080\n# ds:def id=b-k7m2p4xr\nhost: x"},
	} {
		f := c.tier.Extract(c.path, []byte(c.src), "ds")
		if len(f.Problems) != 0 || len(f.Defs) == 0 {
			t.Errorf("%s: %+v %v", c.path, f.Defs, f.Problems)
			continue
		}
		b := f.Defs[0].Block
		if b.Pos.Start != 2 || b.Pos.End != c.end || b.Content != c.content {
			t.Errorf("%s %q: %d-%d %q", c.path, c.src, b.Pos.Start, b.Pos.End, b.Content)
		}
	}
	f := YAML().Extract("a.yaml", []byte("# ds:def id=a-k7m2p4xq span=3\nport: 8080\n"), "ds")
	if len(f.Defs) != 0 || len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, extract.ErrBadSpan) {
		t.Errorf("bad span = %+v %v", f.Defs, f.Problems)
	}
}

// TestEveryStructuredTypeHasAStyle is bug 46's guard for this module: a type
// a tier here reads must have a comment style, or `ds def` refuses to write
// into it while scans read it -- `.tfvars` was exactly that.
func TestEveryStructuredTypeHasAStyle(t *testing.T) {
	t.Parallel()
	for _, set := range []map[string]bool{yamlExts, tomlExts, hclExts} {
		for e := range set {
			if _, ok := extract.StyleFor("f" + e); !ok {
				t.Errorf("%s has no comment style", e)
			}
		}
	}
}
