package id

import (
	"errors"
	"testing"
)

// TestCheckShape pins the §8 character rule a scan holds every def id to
// (bug 55): `Foo_Bar` was accepted and bound. Hand-written ids -- a suffix
// outside the minting alphabet, a short `sess-ttl` -- keep the rule and pass.
func TestCheckShape(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"sess-save-k7m2p4xq", "oncall-lead-r9k1w5zb", "sess-ttl", "v2-api-00000000"} {
		if err := CheckShape(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	if err := CheckShape(""); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty = %v", err)
	}
	for _, bad := range []string{"Foo_Bar", "foobar", "-k7m2p4xq", "sess-", "Sess-k7m2p4xq", "sess--x-k7m2p4xq", "sess_save-k7m2p4xq", "sess-K7M2P4XQ", "sess.save-k7m2p4xq"} {
		if err := CheckShape(bad); !errors.Is(err, ErrBadShape) {
			t.Errorf("%q = %v, want ErrBadShape", bad, err)
		}
	}
}
