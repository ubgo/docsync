//go:build windows

package cli

import (
	"errors"
	"time"

	"golang.org/x/sys/windows"
)

const (
	// sharingAttempts and sharingFirstWait bound the retry: the wait doubles
	// from one millisecond, so ten attempts give up after about a second. A
	// rename or a read of a ledger takes microseconds; a second is long
	// enough for any number of other ds commands to finish theirs, and short
	// enough that a file a program really holds open is still reported.
	sharingAttempts  = 10
	sharingFirstWait = time.Millisecond
)

// retrySharing runs op, and runs it again while it fails only because
// another process has the file open at that instant.
//
// On Unix a rename replaces a file other processes are reading, and they go
// on reading the old one. On Windows the rename is refused while a reader
// has the target open, and an open is refused while the rename is under
// way, so a `ds check` beside a `ds scan` made one of them fail for a
// reason that was gone a millisecond later. Both errors mean "not now",
// never "not possible", so they are waited out rather than reported; any
// other error returns at once.
func retrySharing(op func() error) error {
	return retrySharingWith(op, time.Sleep)
}

// retrySharingWith is retrySharing with the wait injected, so the test does
// not spend a second proving the budget runs out.
func retrySharingWith(op func() error, sleep func(time.Duration)) error {
	wait := sharingFirstWait
	var err error
	for i := 0; i < sharingAttempts; i++ {
		if err = op(); !sharingError(err) {
			return err
		}
		sleep(wait)
		wait *= 2
	}
	return err
}

// sharingError reports the two errors Windows gives for a file that is
// momentarily in use by another process.
func sharingError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
