package docsync

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
)

// --since takes a commit and diffs every cited block against its body
// there, read through OldBody; any other value used to be accepted and
// ignored (bug 61). An unknown mode is refused rather than served as a
// bare location.
func TestContextSinceCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newSys(t, repo(false))
	if _, err := s.Context(ctx, "docs/sessions.md", ContextOptions{Mode: "dif"}); !errors.Is(err, ErrContextMode) || !strings.Contains(err.Error(), `"dif"`) {
		t.Errorf("unknown mode = %v", err)
	}
	if _, err := s.Context(ctx, "docs/sessions.md", ContextOptions{Since: "abc1234"}); !errors.Is(err, ErrSinceCommit) {
		t.Errorf("a commit without OldBody = %v", err)
	}
	old := func(b block.Block) (string, bool) {
		switch b.ID {
		case "sess-save-k7m2p4xq":
			return strings.Replace(b.Content, "s.legacy.Save()", "nil", 1), true
		case "auth-port-h3v8n2wd":
			return b.Content, true
		}
		return "", false
	}
	c, err := s.Context(ctx, "docs/sessions.md", ContextOptions{Since: "abc1234", OldBody: old})
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]ContextItem{}
	for _, it := range c.Items {
		modes[it.ID] = it
	}
	if it := modes["sess-save-k7m2p4xq"]; it.Mode != ModeDiff || !strings.Contains(it.Content, "-\treturn nil") || !strings.Contains(it.Content, "+\treturn s.legacy.Save()") {
		t.Errorf("changed since the commit = %+v", it)
	}
	// Unchanged since the commit, and absent at it: no diff, so auto
	// picks the body or the value as for any unchanged block.
	if it := modes["auth-port-h3v8n2wd"]; it.Mode != ModeValue {
		t.Errorf("unchanged since the commit = %+v", it)
	}
	if it := modes["gh-stripe-key-r4t6x2mb"]; it.Mode == ModeDiff {
		t.Errorf("absent at the commit = %+v", it)
	}
	// §26.4: with a baseline, an unchanged block shrinks to its location.
	same := func(b block.Block) (string, bool) { return b.Content, true }
	c, _ = s.Context(ctx, "docs/sessions.md", ContextOptions{Since: "abc1234", OldBody: same})
	for _, it := range c.Items {
		if it.ID == "sess-save-k7m2p4xq" && (it.Mode != ModeLine || it.Content != "internal/store/write.go:4-6") {
			t.Errorf("unchanged since the commit = %+v", it)
		}
	}
}

// --since ack and --mode diff use the diff since the ack, the one check
// reports, even after a scan recorded the new body and the scan-to-scan
// change is empty (bug 62).
func TestContextSinceAckUsesTheFindingsDiff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := newSys(t, repo(false))
	rep, err := s.Check(ctx, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rep.Changes = nil
	const diff = "-\treturn s.legacy.Save()\n+\treturn s.sessions.Insert()\n"
	rep.Findings = append(rep.Findings, check.Finding{ID: "sess-save-k7m2p4xq", State: check.StateUnacked, Diff: diff})
	for _, opts := range []ContextOptions{{Since: SinceAck}, {Mode: ModeDiff}} {
		c := s.ContextFor(rep, "docs/sessions.md", opts)
		for _, it := range c.Items {
			if it.ID == "sess-save-k7m2p4xq" && (it.Mode != ModeDiff || it.Content != diff) {
				t.Errorf("%+v: %+v", opts, it)
			}
		}
	}
}

// A sentence left out for the budget is named by where it is, since it has
// no id of its own (bug 63).
func TestContextOmittedSentenceIsNamed(t *testing.T) {
	t.Parallel()
	s := newSys(t, repo(false))
	c, err := s.Context(context.Background(), "sess-save-k7m2p4xq", ContextOptions{Budget: 16})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range c.Omitted {
		if o.ID == "docs/sessions.md:7" {
			found = true
		}
		if o.ID == "" {
			t.Errorf("an omitted item with no name: %+v", c.Omitted)
		}
	}
	if !found {
		t.Errorf("omitted = %+v", c.Omitted)
	}
}
