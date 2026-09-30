package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// dirDenial is what restrictDir takes away from a directory.
type dirDenial int

const (
	// denyWrite: nothing can be created in the directory.
	denyWrite dirDenial = iota
	// denyList: the directory's entries cannot be read.
	denyList
)

// everyoneSID is the well-known SID for Everyone. icacls takes it with a
// leading *, which avoids naming an account: account names are localised and
// differ between a desktop and a CI runner.
const everyoneSID = "*S-1-1-0"

// restrictDir takes one kind of access to dir away from this process, by
// what the platform honours, and returns the call that gives it back. On
// Unix that is the mode bits. On Windows a directory's mode is not access
// control, so it is a deny entry in the ACL, set with icacls: the specific
// rights are named (WD,AD to create entries, RD to list) because the broad
// W also denies SYNCHRONIZE, which would block reading as well and make a
// "cannot write" fixture into a "cannot open" one. The entry is not
// inherited, so files already inside stay as they were.
//
// It is also registered as a cleanup, so t.TempDir can remove the tree
// whether or not the test restores it itself.
func restrictDir(t *testing.T, dir string, deny dirDenial) (restore func()) {
	t.Helper()
	if runtime.GOOS != windowsOS {
		mode := os.FileMode(0o555)
		if deny == denyList {
			mode = 0
		}
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		restore = func() { _ = os.Chmod(dir, 0o755) }
		t.Cleanup(restore)
		return restore
	}
	rights := "(WD,AD)"
	if deny == denyList {
		rights = "(RD)"
	}
	if out, err := exec.Command("icacls", dir, "/deny", everyoneSID+":"+rights).CombinedOutput(); err != nil {
		t.Fatalf("icacls /deny on %s: %v\n%s", dir, err, out)
	}
	restore = func() { _ = exec.Command("icacls", dir, "/remove:d", everyoneSID).Run() }
	t.Cleanup(restore)
	return restore
}

// dirPermsEnforced reports whether restrictDir has any effect on this
// process. It has none for root on Unix, which is not subject to mode bits.
// A test whose fixture is "a directory I cannot read" has nothing to stand on
// there, and says so instead of asserting on a fixture that never came to
// exist.
func dirPermsEnforced(t *testing.T) bool {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "probe")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	restrictDir(t, dir, denyList)
	_, err := os.ReadDir(dir)
	return err != nil
}

// TestRestrictDir pins the fixture itself: each denial takes away exactly
// what it names and the restore gives it back. Without this, a test built on
// restrictDir could pass because the directory was never restricted.
func TestRestrictDir(t *testing.T) {
	t.Parallel()
	if !dirPermsEnforced(t) {
		t.Skip("this process is not subject to directory permissions (root)")
	}
	dir := t.TempDir()
	write(t, dir, "d/kept.txt", "x")
	d := filepath.Join(dir, "d")
	create := func() error { return os.WriteFile(filepath.Join(d, "new.txt"), []byte("x"), 0o644) }

	restore := restrictDir(t, d, denyWrite)
	if err := create(); err == nil {
		t.Error("denyWrite: a file was created")
	}
	if _, err := os.ReadDir(d); err != nil {
		t.Errorf("denyWrite must leave the directory listable: %v", err)
	}
	restore()
	if err := create(); err != nil {
		t.Errorf("after restoring from denyWrite: %v", err)
	}

	restore = restrictDir(t, d, denyList)
	if _, err := os.ReadDir(d); err == nil {
		t.Error("denyList: the directory was listed")
	}
	restore()
	if entries, err := os.ReadDir(d); err != nil || len(entries) != 2 {
		t.Errorf("after restoring from denyList: %v %v", entries, err)
	}
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
	restrictDir(t, locked, denyList)
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
