//go:build unix

package cli

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// lockFile takes an exclusive advisory lock on f, waiting for it. The kernel
// releases it when the process exits, so a crash can never leave .ds/ locked.
func lockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX) }

// unlockFile releases lockFile's lock.
func unlockFile(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }

// lockUnsupported reports the errors a filesystem gives when it does not do
// locking at all, as opposed to a lock that failed.
func lockUnsupported(err error) bool {
	return errors.Is(err, unix.ENOLCK) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP)
}
