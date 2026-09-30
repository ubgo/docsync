package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// dirPermsEnforced reports whether removing a directory's permission bits
// stops this process reading it. It does not on Windows, where directory
// modes are not access control, nor for root anywhere. A test whose fixture
// is "a directory I cannot read" has nothing to stand on there, and says so
// instead of asserting on a fixture that never came to exist.
func dirPermsEnforced(t *testing.T) bool {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "probe")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, err := os.ReadDir(dir)
	return err != nil
}

// TestListDir pins what each thing at a store path means, identically on
// every platform: absent is "nothing stored yet", a directory is listed, and
// anything else is an error. The last is the one that differed: Windows
// reports reading a file as a directory as "path not found", which reads as
// absent, so a corrupted store looked empty.
func TestListDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	entries, exists, err := listDir(filepath.Join(root, "absent"))
	if err != nil || exists || entries != nil {
		t.Errorf("absent = %v %v %v, want no entries, not existing, no error", entries, exists, err)
	}

	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if entries, exists, err := listDir(empty); err != nil || !exists || len(entries) != 0 {
		t.Errorf("empty dir = %v %v %v, want existing and empty", entries, exists, err)
	}
	write(t, root, "empty/a.tsv", "x")
	if entries, exists, err := listDir(empty); err != nil || !exists || len(entries) != 1 {
		t.Errorf("dir with a file = %v %v %v", entries, exists, err)
	}

	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := listDir(file); !errors.Is(err, ErrNotDir) || !exists {
		t.Errorf("a file where the directory should be = exists %v, err %v; want ErrNotDir", exists, err)
	}

	// A path below a regular file cannot be examined at all on Unix
	// (ENOTDIR); Windows calls it not found, which is absent.
	_, _, err = listDir(filepath.Join(file, "below"))
	if runtime.GOOS != windowsOS && err == nil {
		t.Error("a path below a regular file must be an error")
	}

	if !dirPermsEnforced(t) {
		t.Log("directory permissions are not enforced here; the unreadable-directory case cannot be set up")
		return
	}
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, exists, err := listDir(locked); err == nil || !exists {
		t.Errorf("an unreadable directory = exists %v, err %v; want an error", exists, err)
	}
}

// makeUnremovable makes the files in dir impossible to delete for the rest of
// the test, by whatever the platform honours: removing the directory's write
// permission on Unix, holding each file open on Windows, where directory
// modes are not access control but an open handle blocks deletion.
func makeUnremovable(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS != windowsOS {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
	}
}
