package cli

import (
	"errors"
	"strings"
	"testing"
)

// promise:commit-ack-keys
// An ack in a commit message takes note=, which reaches the ack log; any
// other key is refused. note= used to be read past without a word (bug 73).
func TestAckFromCommitNote(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", goV2)
	v.files["msg:HEAD"] = []byte("Swap storage backend\n\nds:ack id=sess-save-k7m2p4xq note=\"insert keeps the guard\"\n")
	if r := run(t, dir, v, "ack", "--from-commit", "HEAD", "--note", "flag note"); r.code != 0 {
		t.Fatalf("ack = %+v", r)
	}
	_, _, acks, _ := NewStore(dir).LoadState()
	if len(acks.Rows) != 2 || acks.Rows[0].Note != "insert keeps the guard" {
		t.Errorf("the directive's note wins over --note and the subject: %+v", acks.Rows)
	}
	v.files["msg:typo"] = []byte("x\n\nds:ack id=sess-save-k7m2p4xq nte=oops\n")
	if r := run(t, dir, v, "ack", "--from-commit", "typo"); r.code != ExitError || !strings.Contains(r.err, "has nte=") || !strings.Contains(r.err, "id= and note=") {
		t.Errorf("unknown key = %+v", r)
	}
}

// promise:ack-scope-named
// `ds ack <id> --note` with no --doc/--line and no --all is refused and
// records nothing: SPEC §28 showed that form, and the fix was the example,
// not acking every sentence the reader never named (bug 76).
func TestAckWithoutScopeIsRefused(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", goV2)
	r := run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--note", "prose updated")
	if r.code != ExitError || !strings.Contains(r.err, "--doc and --line, or --all") {
		t.Errorf("ack without scope = %+v", r)
	}
	if _, _, acks, _ := NewStore(dir).LoadState(); len(acks.Rows) != 0 {
		t.Errorf("a refused ack recorded %+v", acks.Rows)
	}
	// The form the spec now shows: the page named, every citation in it.
	if r := run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--doc", "docs/sessions.md", "--all", "--note", "prose updated"); r.code != 0 || strings.Count(r.out, "acked sess-save-k7m2p4xq") != 2 {
		t.Errorf("ack --doc --all = %+v", r)
	}
}

func TestAckDirectivesParse(t *testing.T) {
	t.Parallel()
	got, err := ackDirectives("ds", "a ds:ack id=x-1 note='single quoted' b\n(ds:ack note=bare id=y-2) ds:ack id=\"bad id\" ds:ack note=only")
	if err != nil || len(got) != 2 || got[0] != (commitAck{ID: "x-1", Note: "single quoted"}) || got[1] != (commitAck{ID: "y-2", Note: "bare"}) {
		t.Errorf("ackDirectives = %+v %v", got, err)
	}
	if _, err := ackDirectives("ds", "ds:ack id=x env=prod"); !errors.Is(err, ErrUsage) {
		t.Errorf("unknown key = %v", err)
	}
}
