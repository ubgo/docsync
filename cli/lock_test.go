package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/ledger"
)

// concurrentWriters is enough simultaneous writers that, without the lock,
// losing one is all but certain on every run — the unlocked code recorded
// one ack out of ten.
const concurrentWriters = 50

// TestConcurrentAcksAreAllRecorded pins the lost-update fix: many writers
// appending at once, each with its own row, all land. Before AppendAcks
// re-read the log under a lock, each writer wrote back the copy it had
// loaded plus its own row, and all but one row vanished while every command
// reported success.
func TestConcurrentAcksAreAllRecorded(t *testing.T) {
	t.Parallel()
	st := NewStore(t.TempDir())
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	errs := make(chan error, concurrentWriters)
	for i := 0; i < concurrentWriters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- st.AppendAcks([]ledger.Ack{{At: at, Actor: "k", ActorKind: ledger.ActorHuman, ID: fmt.Sprintf("a%d-k7m2p4xq", i), Doc: "d.md", Line: i + 1}})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	_, _, acks, err := st.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(acks.Rows) != concurrentWriters {
		t.Fatalf("recorded %d of %d concurrent acks", len(acks.Rows), concurrentWriters)
	}
}

// TestAppendAcksSkipsRowsAlreadyThere pins the dedupe that lets a caller pass
// the whole log it holds: rows already on disk are not written twice, and a
// row another writer added meanwhile is kept.
func TestAppendAcksSkipsRowsAlreadyThere(t *testing.T) {
	t.Parallel()
	st := NewStore(t.TempDir())
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	mine := ledger.Ack{At: at, Actor: "k", ActorKind: ledger.ActorHuman, ID: "a-k7m2p4xq", Doc: "d.md", Line: 3}
	theirs := ledger.Ack{At: at, Actor: "j", ActorKind: ledger.ActorHuman, ID: "b-h3v8n2wd", Doc: "d.md", Line: 5}
	if err := st.AppendAcks([]ledger.Ack{mine}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendAcks([]ledger.Ack{theirs}); err != nil {
		t.Fatal(err)
	}
	// A writer holding only its own copy passes all of it again.
	if err := st.AppendAcks([]ledger.Ack{mine}); err != nil {
		t.Fatal(err)
	}
	_, _, acks, _ := st.LoadState()
	if len(acks.Rows) != 2 {
		t.Fatalf("rows = %+v, want mine and theirs once each", acks.Rows)
	}
	if acks.Header.Kind != ledger.KindAcks {
		t.Errorf("a log created by AppendAcks has header %+v", acks.Header)
	}
}

// TestConcurrentJournalBatchesAreAllRecorded: two `ds def` at once each
// journal a batch; both must land, with distinct batch numbers, or undo
// cannot reverse the one that was lost.
func TestConcurrentJournalBatchesAreAllRecorded(t *testing.T) {
	t.Parallel()
	st := NewStore(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < concurrentWriters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := st.Journal(WriteDef, []docsync.Edit{{File: fmt.Sprintf("f%d.go", i), Line: 1, New: "// ds:def id=x"}}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	entries, err := st.journal()
	if err != nil {
		t.Fatal(err)
	}
	batches := map[int]bool{}
	for _, e := range entries {
		batches[e.Batch] = true
	}
	if len(entries) != concurrentWriters || len(batches) != concurrentWriters {
		t.Fatalf("journal holds %d entries in %d batches, want %d of each", len(entries), len(batches), concurrentWriters)
	}
}

// TestConcurrentWritesOfOneFile: many commands writing the same .ds/ file at
// once — two scans rewriting the ledger — all succeed, and the file ends as
// exactly one writer's complete content. With a shared temp path and no
// lock, renames failed and a mixture of writes could land in place.
func TestConcurrentWritesOfOneFile(t *testing.T) {
	t.Parallel()
	st := NewStore(t.TempDir())
	payload := func(i int) []byte {
		return []byte(strings.Repeat(fmt.Sprintf("writer %03d\n", i), 4000))
	}
	var wg sync.WaitGroup
	for i := 0; i < concurrentWriters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := st.Write(LedgerFile, payload(i)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, err := os.ReadFile(st.path(LedgerFile))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < concurrentWriters; i++ {
		if string(got) == string(payload(i)) {
			return
		}
	}
	t.Fatalf("the file is no single writer's content (%d bytes, starts %q)", len(got), got[:22])
}

// TestAppendAcksRefusesALogItCannotRead pins the one refusal that protects
// history: an ack log that does not parse — a merge left unfinished, a bad
// hand edit — is not replaced by one holding only the new rows. It is left
// exactly as it was and the reason is returned.
func TestAppendAcksRefusesALogItCannotRead(t *testing.T) {
	t.Parallel()
	st := NewStore(t.TempDir())
	broken := "# docsync acks format=2 repo=api\ncols\n<<<<<<< HEAD\n"
	if err := st.Write(AcksFile, []byte(broken)); err != nil {
		t.Fatal(err)
	}
	err := st.AppendAcks([]ledger.Ack{{At: time.Now(), ID: "a-k7m2p4xq", Doc: "d.md", Line: 1}})
	if !errors.Is(err, ledger.ErrConflict) {
		t.Fatalf("err = %v, want the conflict reported", err)
	}
	if got, _ := os.ReadFile(st.path(AcksFile)); string(got) != broken {
		t.Errorf("an unreadable log was overwritten:\n%s", got)
	}
}

// TestLockSetupFailures: the lock cannot be taken when .ds/ cannot be
// created or its lock file cannot be opened, and the write it guards does
// not happen.
func TestLockSetupFailures(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// The root is a file, so .ds/ cannot be created under it.
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(file).Write(LedgerFile, []byte("x")); err == nil {
		t.Error("a write under a file must fail")
	}
	// The lock path is a directory, so it cannot be opened for writing.
	st := NewStore(t.TempDir())
	if err := os.MkdirAll(st.path(LockFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st.Write(LedgerFile, []byte("x")); err == nil {
		t.Error("a write whose lock cannot be opened must fail")
	}
	if _, err := os.Stat(st.path(LedgerFile)); !os.IsNotExist(err) {
		t.Error("the guarded write happened without the lock")
	}
}
