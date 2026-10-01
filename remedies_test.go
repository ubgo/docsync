package docsync

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/check"
)

// TestRelabelledCiteIsBroken is bug 47: §8 said the suffix alone is the
// identity, so a citation carrying another label "is the same block", while
// the binary -- and the typo-suggests fixture -- report it broken and name
// the id it meant. The spec now says what the binary does; this pins the
// binary's half: broken, with the current id suggested.
func TestRelabelledCiteIsBroken(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"a.go":      {Data: []byte("package a\n\n// ds:def id=sess-save-k7m2p4xq\nfunc Save() {}\n")},
		"docs/d.md": {Data: []byte("See [x](ds:block?id=session-persist-k7m2p4xq).\n")},
	}
	rep, err := defineSys(t, fsys).Check(context.Background(), CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := states(rep)[check.StateBroken]
	if len(got) != 1 || !strings.Contains(got[0].Message, "did you mean sess-save-k7m2p4xq") {
		t.Errorf("broken = %+v", got)
	}
}

// TestRemediesNameWhatExists is bug 56: two remedies sent the reader to
// something that does not exist or does not apply. The no-carrier refusal
// named a [scan] key that adds comment syntaxes (there is none), and a cfg
// query= with no source borrowed ds:table's remedy to register a record
// source, which no query reads.
func TestRemediesNameWhatExists(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"p.json":    {Data: []byte("{\"port\": 1}\n")},
		"docs/d.md": {Data: []byte("We serve [1.2M](<ds:cfg?query=\"sql:select count(*) from users\">) users.\n")},
	}
	s := defineSys(t, fsys)
	_, err := s.Define(context.Background(), "p.json:1", DefineOptions{})
	if !errors.Is(err, ErrNoCarrier) || strings.Contains(err.Error(), "[scan]") || !strings.Contains(err.Error(), "built in") {
		t.Errorf("no-carrier refusal = %v", err)
	}
	rep, err := s.Check(context.Background(), CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := states(rep)[check.StateUnverifiable]
	if len(got) != 1 || strings.Contains(got[0].Remedy.Fix, "[records]") || !strings.Contains(got[0].Remedy.Fix, "query sources are not built yet") {
		t.Errorf("cfg query = %+v", got)
	}
}
