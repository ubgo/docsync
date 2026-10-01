package scan

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

// problemsOf scans fsys with the test options and returns its problems by
// sentinel.
func problemsOf(t *testing.T, fsys fstest.MapFS, o Options) map[error][]Problem {
	t.Helper()
	res, err := Scan(context.Background(), fsys, o)
	if err != nil {
		t.Fatal(err)
	}
	out := map[error][]Problem{}
	for _, p := range res.Problems {
		for _, e := range []error{ErrBadType, ErrTypeMismatch, ErrMalformedID, ErrBadID, ErrSharedBlock, ErrCrossingBlocks} {
			if errors.Is(p.Err, e) {
				out[e] = append(out[e], p)
			}
		}
	}
	return out
}

// TestTypesAreChecked is bug 54 (promise:type-secret-unchecked): `type=` was accepted and checked nothing. A
// value of the wrong shape and an unknown type are each a problem; a secret's
// value is never checked into a message.
func TestTypesAreChecked(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"docs/f.md":    {Data: []byte("Port [8081](ds:def?id=port-k7m2p4xq&type=int), bad [abc](ds:def?id=bad-k7m2p4xr&type=int), odd [x](ds:def?id=odd-k7m2p4xs&type=colour).\n")},
		"docs/s.md":    {Data: []byte("Key [hunter2](ds:def?id=key-k7m2p4xt&type=int&secret=true).\n")},
		"config/a.ini": {Data: []byte("[s]\nport = 80a  ; ds:def id=ini-k7m2p4xu type=int\n")},
	}
	got := problemsOf(t, fsys, opts(t))
	if len(got[ErrTypeMismatch]) != 2 || len(got[ErrBadType]) != 1 {
		t.Fatalf("problems = %v", got)
	}
	for _, p := range got[ErrTypeMismatch] {
		if strings.Contains(p.Err.Error(), "hunter2") {
			t.Error("a secret's value reached a message")
		}
	}
	if !strings.Contains(got[ErrBadType][0].Err.Error(), "url, email, int, float, percent, semver, date, duration, host, opref") {
		t.Errorf("unknown type message does not list the types: %v", got[ErrBadType][0].Err)
	}
}

// TestIDsAreChecked is bug 55: an id outside the §8 character rule was
// accepted. An id with whitespace keeps its own, more precise problem and is
// not reported twice; a short hand-written id keeps the rule and passes.
func TestIDsAreChecked(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"docs/a.md": {Data: []byte("<!-- ds:def id=Foo_Bar -->\nP.\n\n<!-- ds:def id=ok-r9k1w5zb -->\nQ.\n\n<!-- ds:def id=\"a b\" -->\nR.\n\n<!-- ds:def id=sess-ttl -->\nS.\n")}}
	got := problemsOf(t, fsys, opts(t))
	if len(got[ErrMalformedID]) != 1 || len(got[ErrBadID]) != 1 || !strings.Contains(got[ErrMalformedID][0].Err.Error(), "Foo_Bar") {
		t.Errorf("problems = %v", got)
	}
}

// TestFactsOnOneLineAreTwoBlocks is bug 59: two inline facts in one sentence
// -- the spec's own §11 example -- were reported as two ids bound to the same
// block, because blocks were keyed by line.
func TestFactsOnOneLineAreTwoBlocks(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"docs/f.md": {Data: []byte("The API runs on port [8081](ds:def?id=api-port-h3v8n2wd&type=int) and ships as version [2.14.0](ds:def?id=api-version-c8t2m6qp&type=semver).\n")}}
	if got := problemsOf(t, fsys, opts(t)); len(got) != 0 {
		t.Errorf("problems = %v", got)
	}
}

// TestRemoteDefsKeyedByPick is bug 53: two remote defs picking different
// keys from one JSON line were reported as one block; the same pick twice
// still is.
func TestRemoteDefsKeyedByPick(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"config/app.json": {Data: []byte("{\"port\": 8081, \"host\": \"a\"}\n")},
		"docs/a.md":       {Data: []byte("<!-- ds:def id=port-m3k9v2pd file=config/app.json pick=json:$.port -->\n<!-- ds:def id=host-m3k9v2pe file=config/app.json pick=json:$.host -->\n")},
	}
	if got := problemsOf(t, fsys, opts(t)); len(got[ErrSharedBlock]) != 0 {
		t.Errorf("different picks = %v", got)
	}
	fsys["docs/b.md"] = &fstest.MapFile{Data: []byte("<!-- ds:def id=again-m3k9v2pf file=config/app.json pick=json:$.port -->\n")}
	if got := problemsOf(t, fsys, opts(t)); len(got[ErrSharedBlock]) != 2 {
		t.Errorf("same pick twice = %v", got)
	}
}

// TestCrossingBlocksReported is bug 58: blocks that overlap without one
// containing the other were never reported. Nested blocks are fine.
func TestCrossingBlocksReported(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"docs/rota.txt": {Data: []byte("ds:def id=rota-k7m2p4xq span=+2\nWeek 37\nds:def id=late-k7m2p4xr\nWeek 38\nWeek 39\n")},
		"docs/n.md":     {Data: []byte("<!-- ds:def id=outer-k7m2p4xs -->\n## A\ntext\n<!-- ds:def id=inner-k7m2p4xt -->\n### B\nmore\n")},
	}
	got := problemsOf(t, fsys, opts(t))
	if len(got[ErrCrossingBlocks]) != 2 {
		t.Fatalf("problems = %v", got)
	}
	for _, p := range got[ErrCrossingBlocks] {
		if p.Pos.File != "docs/rota.txt" || !strings.Contains(p.Err.Error(), "rota-k7m2p4xq") || !strings.Contains(p.Err.Error(), "late-k7m2p4xr") {
			t.Errorf("crossing = %+v", p)
		}
	}
	// One environment's block does not cross another environment's.
	fsys["docs/rota.txt"] = &fstest.MapFile{Data: []byte("ds:def id=rota-k7m2p4xq span=+2 env=prod\nWeek 37\nds:def id=late-k7m2p4xr\nWeek 38\nWeek 39\n")}
	if got := problemsOf(t, fsys, opts(t)); len(got[ErrCrossingBlocks]) != 0 {
		t.Errorf("across environments = %v", got)
	}
}
