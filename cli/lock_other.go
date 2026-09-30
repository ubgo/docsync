//go:build !unix && !windows

package cli

import "os"

// lockFile is a no-op where the platform has no file lock (plan9, wasm).
// Concurrent ds commands there can lose an ack or a journal entry; docsync
// is not built or tested for those platforms (see the cross-build gate).
func lockFile(*os.File) error { return nil }

// unlockFile is lockFile's no-op counterpart.
func unlockFile(*os.File) error { return nil }

// lockUnsupported is always true where there is no lock to take.
func lockUnsupported(error) bool { return true }
