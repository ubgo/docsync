//go:build !windows

package cli

// retrySharing runs op once. Only Windows refuses to rename over, or open,
// a file another process is using at that instant (sharing_windows.go);
// everywhere else there is nothing to wait out.
func retrySharing(op func() error) error { return op() }
