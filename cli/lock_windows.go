//go:build windows

package cli

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockRange is the byte range locked: one byte is enough for an exclusive
// lock that every writer asks for the same way.
const lockRange = 1

// lockFile takes an exclusive lock on f, waiting for it. Windows releases it
// when the process exits, so a crash can never leave .ds/ locked.
func lockFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, lockRange, 0, new(windows.Overlapped))
}

// unlockFile releases lockFile's lock.
func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, lockRange, 0, new(windows.Overlapped))
}

// lockUnsupported reports the errors a filesystem gives when it does not do
// locking at all, as opposed to a lock that failed.
func lockUnsupported(err error) bool {
	return errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION)
}
