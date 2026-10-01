package cli

import (
	"strings"
	"testing"
)

// Renewing a claim whose about= block changed clears it at once, as the
// "renewed" message says, until the block changes again; it used to stay
// expired until the next `ds scan` (bug 72).
func TestClaimRenewalAcceptsTheAboutChange(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "docs/claims.md", "# Claims\n\nSaves are dual-written. <!-- ds:claim owner=@auth reviewed=2026-09-01 expires=3650d about=sess-save-k7m2p4xq -->\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", goV2)
	if r := run(t, dir, v, "check"); !strings.Contains(r.out, "claim is about sess-save-k7m2p4xq, which changed") {
		t.Fatalf("before the ack = %+v", r)
	}
	if r := run(t, dir, v, "ack", "--doc", "docs/claims.md", "--line", "3", "--note", "still dual-written"); r.code != 0 || !strings.Contains(r.out, "renewed the claim") {
		t.Fatalf("ack = %+v", r)
	}
	if r := run(t, dir, v, "check"); strings.Contains(r.out, "docs/claims.md") {
		t.Errorf("a renewed claim still reported = %+v", r)
	}
	write(t, dir, "internal/store/write.go", strings.Replace(goV2, "sessions.Insert()", "sessions.Upsert()", 1))
	if r := run(t, dir, v, "check"); !strings.Contains(r.out, "claim is about sess-save-k7m2p4xq, which changed") {
		t.Errorf("a change after the renewal = %+v", r)
	}
}
