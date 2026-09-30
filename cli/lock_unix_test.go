//go:build unix

package cli

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// TestLockFailures pins what a failed lock means. A filesystem that does not
// do locking — NFS without a lock daemon — must not make every write fail,
// so the write goes ahead unlocked; any other lock failure fails the write
// and nothing is written.
func TestLockFailures(t *testing.T) {
	t.Parallel()
	unsupported := NewStore(t.TempDir())
	unsupported.lock = func(*os.File) error { return unix.ENOLCK }
	if err := unsupported.Write(LedgerFile, []byte("x")); err != nil {
		t.Fatalf("a filesystem without locks must still be writable: %v", err)
	}
	if got, _ := os.ReadFile(unsupported.path(LedgerFile)); string(got) != "x" {
		t.Errorf("unlocked write = %q", got)
	}
	failed := NewStore(t.TempDir())
	failed.lock = func(*os.File) error { return unix.EBADF }
	if err := failed.Write(LedgerFile, []byte("x")); !errors.Is(err, unix.EBADF) {
		t.Fatalf("a failed lock must fail the write, got %v", err)
	}
	if _, err := os.Stat(failed.path(LedgerFile)); !os.IsNotExist(err) {
		t.Error("the write happened although the lock failed")
	}
}
