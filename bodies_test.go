package docsync

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/scan"
)

// bodyBlock builds a def with content and the given args.
func bodyBlock(id, file, content string, args map[string]string) block.Block {
	if args == nil {
		args = map[string]string{}
	}
	args[block.KeyID] = id
	b := block.Block{ID: id, Kind: block.KindSection, Pos: block.Position{File: file, Start: 1, End: 1}, Args: args}
	b.SetContent(content)
	return b
}

// TestBodiesExcludesSecrets is the guard on the one-way door: the workspace
// index can be readable by people who cannot read the source repository, and
// a body written there cannot be recalled. The exclusion lives in Bodies
// rather than in each caller so no future writer can forget it.
// promise:bodies-no-secret
func TestBodiesExcludesSecrets(t *testing.T) {
	t.Parallel()
	const secret = "token: hunter2-SUPERSECRET"
	pub := bodyBlock("pub-k7m2p4xq", "a.md", "A public paragraph.", nil)
	sec := bodyBlock("tok-k7m2p4xq", "creds.yaml", secret, map[string]string{block.KeySecret: block.TrueValue})
	loc := bodyBlock("loc-k7m2p4xq", "b.md", "Only readable on the author's machine.", map[string]string{block.KeyLocal: block.TrueValue})
	empty := block.Block{ID: "nil-k7m2p4xq", Args: map[string]string{block.KeyID: "nil-k7m2p4xq"}}

	s, err := New(WithFS(fstest.MapFS{}), WithRepo("repo"))
	if err != nil {
		t.Fatal(err)
	}
	got := s.Bodies(scan.Result{Defs: []block.Block{pub, sec, loc, empty}})
	if len(got) != 1 || got[pub.Hash] != pub.Content {
		t.Fatalf("only the public body may be stored, got %d entries: %v", len(got), got)
	}
	for hash, body := range got {
		if strings.Contains(body, "SUPERSECRET") {
			t.Errorf("secret body reachable at %s", hash)
		}
	}
	if _, ok := got[sec.Hash]; ok {
		t.Error("a secret block must contribute no body")
	}
	if _, ok := got[loc.Hash]; ok {
		t.Error("a local block must contribute no body")
	}
	// Content-addressed: the same block twice is one entry.
	if again := s.Bodies(scan.Result{Defs: []block.Block{pub, pub}}); len(again) != 1 {
		t.Errorf("content addressing must deduplicate, got %d", len(again))
	}
}

// TestWithBodyAtReachesCheck pins that the option is actually consulted, so
// a wiring regression cannot silently degrade every drift to ClassUnknown.
func TestWithBodyAtReachesCheck(t *testing.T) {
	t.Parallel()
	asked := []string{}
	s, err := New(WithFS(fstest.MapFS{}), WithRepo("repo"), WithBodyAt(func(hash string) (string, bool) {
		asked = append(asked, hash)
		return "old body", true
	}))
	if err != nil {
		t.Fatal(err)
	}
	if s.bodyAt == nil {
		t.Fatal("WithBodyAt did not set the hook")
	}
	if body, ok := s.bodyAt("deadbeef"); !ok || body != "old body" {
		t.Errorf("hook = %q %v", body, ok)
	}
	if len(asked) != 1 || asked[0] != "deadbeef" {
		t.Errorf("asked = %v", asked)
	}
}

// TestWithForeignSnapshotReachesCheck pins the wiring: without it a frozen
// run would report an unrecorded citation as undefined, sending the reader
// to "fix" a citation that is already correct.
func TestWithForeignSnapshotReachesCheck(t *testing.T) {
	t.Parallel()
	s, err := New(WithFS(fstest.MapFS{}), WithRepo("repo"))
	if err != nil {
		t.Fatal(err)
	}
	if s.snapshotOnly {
		t.Error("the default is the live index")
	}
	s, err = New(WithFS(fstest.MapFS{}), WithRepo("repo"), WithForeignSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !s.snapshotOnly {
		t.Error("WithForeignSnapshot did not take effect")
	}
}

// TestWithPreviousForeignReachesCheck pins the wiring that makes a
// cross-repo `moved` possible at all: without a previous position there is
// nothing to have moved from.
func TestWithPreviousForeignReachesCheck(t *testing.T) {
	t.Parallel()
	s, err := New(WithFS(fstest.MapFS{}), WithRepo("repo"))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.prevForeign) != 0 {
		t.Error("the default is no previous foreign state")
	}
	s, err = New(WithFS(fstest.MapFS{}), WithRepo("repo"),
		WithPreviousForeign(ledger.Row{ID: "a-k7m2p4xq", Hash: "aaa"}),
		WithPreviousForeign(ledger.Row{ID: "b-h3v8n2wd", Hash: "bbb"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.prevForeign) != 2 || s.prevForeign[0].ID != "a-k7m2p4xq" {
		t.Errorf("prevForeign = %+v", s.prevForeign)
	}
}
