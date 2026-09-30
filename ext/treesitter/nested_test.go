package treesitter

import (
	"testing"
)

// TestAnchoringANestedMemberKeepsTheOuterHash is bug 4's invariant for
// the grammar tier: anchoring a field inside an anchored type, or a line
// inside a span, must not change the outer block's hash.
func TestAnchoringANestedMemberKeepsTheOuterHash(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, before, after string
	}{
		{
			"a field inside an anchored struct",
			"package p\n\n// ds:def id=store-k7m2p4xq\ntype Store struct {\n\tA int\n\tB int\n}\n",
			"package p\n\n// ds:def id=store-k7m2p4xq\ntype Store struct {\n\tA int\n\t// ds:def id=field-t4k2b9rf\n\tB int\n}\n",
		},
		{
			"a line inside a span",
			"package p\n\n// ds:def id=store-k7m2p4xq span=+2\nvar a = 1\nvar b = 2\nvar c = 3\n",
			"package p\n\n// ds:def id=store-k7m2p4xq span=+2\nvar a = 1\n// ds:def id=inner-t4k2b9rf\nvar b = 2\nvar c = 3\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ex := Go()
			get := func(src string) string {
				for _, d := range ex.Extract("a.go", []byte(src), "ds").Defs {
					if d.Block.ID == "store-k7m2p4xq" {
						return d.Block.Hash
					}
				}
				t.Fatalf("no outer def in\n%s", src)
				return ""
			}
			if get(tc.before) != get(tc.after) {
				t.Error("anchoring a nested member changed the outer hash")
			}
		})
	}
}
