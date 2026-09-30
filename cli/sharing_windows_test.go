//go:build windows

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestRetrySharing pins the three outcomes: a sharing error is waited out, a
// different error returns at once, and a file that stays in use is reported
// once the budget is spent.
func TestRetrySharing(t *testing.T) {
	t.Parallel()
	var waits []time.Duration
	sleep := func(d time.Duration) { waits = append(waits, d) }

	calls := 0
	err := retrySharingWith(func() error {
		calls++
		if calls < 3 {
			return &os.PathError{Op: "rename", Path: "x", Err: windows.ERROR_SHARING_VIOLATION}
		}
		return nil
	}, sleep)
	if err != nil || calls != 3 || len(waits) != 2 || waits[1] != 2*waits[0] {
		t.Errorf("a passing sharing error: err %v, %d calls, waits %v", err, calls, waits)
	}

	calls = 0
	if err := retrySharingWith(func() error { calls++; return os.ErrNotExist }, sleep); !errors.Is(err, os.ErrNotExist) || calls != 1 {
		t.Errorf("another error must return at once: %v after %d calls", err, calls)
	}

	calls = 0
	err = retrySharingWith(func() error { calls++; return windows.ERROR_ACCESS_DENIED }, sleep)
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || calls != sharingAttempts {
		t.Errorf("a file that stays in use: %v after %d calls", err, calls)
	}
}

// TestWriteReplacesAFileAReaderHasOpen is the case itself: a reader holds
// the ledger open for a moment while a write renames a new one into place.
func TestWriteReplacesAFileAReaderHasOpen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := NewStore(dir)
	if err := os.MkdirAll(filepath.Join(dir, DirName), dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := st.Write("f.tsv", []byte("old")); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, DirName, "f.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(20 * time.Millisecond); _ = f.Close() }()
	if err := st.Write("f.tsv", []byte("new")); err != nil {
		t.Fatalf("a write beside a brief reader: %v", err)
	}
	if raw, _, err := st.read("f.tsv"); err != nil || string(raw) != "new" {
		t.Errorf("read back %q %v", raw, err)
	}
}
