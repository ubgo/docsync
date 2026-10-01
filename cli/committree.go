package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"testing/fstest"
	"time"
)

// TreeVCS is the optional upgrade to VCS that `render --at` needs to render
// a page as of a commit rather than the page's old text over today's code:
// the commit a ref names, and every file in that commit's tree with its size.
// Git implements it; a VCS that does not makes `render --at` refuse rather
// than mix two commits in one page. It is an optional interface, not a VCS
// method, so a host's existing VCS keeps compiling.
type TreeVCS interface {
	// Resolve returns the short sha that commit (a sha, tag or branch) names.
	Resolve(commit string) (string, error)
	// Tree maps every file path in commit's tree to its size in bytes.
	Tree(commit string) (map[string]int64, error)
}

// ErrNoTree is returned by `render --at` when the VCS cannot list a
// commit's tree (it does not implement TreeVCS).
var ErrNoTree = errors.New("render --at needs a VCS that lists a commit's files (cli.TreeVCS)")

// treeFileMode is the mode every file in a commit view reports; the view is
// read-only and the scanner reads no permission bits.
const treeFileMode fs.FileMode = 0o444

// commitFS is a read-only fs.FS over one commit's tree. The structure (which
// paths exist, the directories they imply) is listed once, up front, by
// TreeVCS.Tree; a file's bytes are fetched only when it is read, so a render
// reads the files the scan's globs select and no others. fstest.MapFS
// supplies directory handling and is never given file bytes.
type commitFS struct {
	tree  fstest.MapFS
	sizes map[string]int64
	show  func(path string) ([]byte, error)
}

// newCommitFS lists sha's tree through v and returns the view of it.
func newCommitFS(v VCS, tv TreeVCS, sha string) (commitFS, error) {
	sizes, err := tv.Tree(sha)
	if err != nil {
		return commitFS{}, err
	}
	tree := fstest.MapFS{}
	for p := range sizes {
		tree[p] = &fstest.MapFile{Mode: treeFileMode}
	}
	return commitFS{tree: tree, sizes: sizes, show: func(p string) ([]byte, error) { return v.Show(sha, p) }}, nil
}

// Open opens a file by fetching it at the commit; a directory, or a path
// the commit does not have, is answered by the listing alone.
func (c commitFS) Open(name string) (fs.File, error) {
	if _, ok := c.sizes[name]; !ok {
		return c.tree.Open(name)
	}
	data, err := c.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return fstest.MapFS{name: {Data: data, Mode: treeFileMode}}.Open(name)
}

// ReadFile returns a file's bytes at the commit.
func (c commitFS) ReadFile(name string) ([]byte, error) {
	if _, ok := c.sizes[name]; !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	data, err := c.show(name)
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: name, Err: err}
	}
	return data, nil
}

// Stat answers from the listing, so the scanner's size limit costs no read.
func (c commitFS) Stat(name string) (fs.FileInfo, error) {
	if size, ok := c.sizes[name]; ok {
		return treeFileInfo{name: path.Base(name), size: size}, nil
	}
	return fs.Stat(c.tree, name)
}

// ReadDir lists a directory of the commit's tree.
func (c commitFS) ReadDir(name string) ([]fs.DirEntry, error) { return c.tree.ReadDir(name) }

// treeFileInfo is a file's metadata in a commit view: a name and a size.
type treeFileInfo struct {
	name string
	size int64
}

func (i treeFileInfo) Name() string       { return i.name }
func (i treeFileInfo) Size() int64        { return i.size }
func (i treeFileInfo) Mode() fs.FileMode  { return treeFileMode }
func (i treeFileInfo) ModTime() time.Time { return time.Time{} }
func (i treeFileInfo) IsDir() bool        { return false }
func (i treeFileInfo) Sys() any           { return nil }

// renderAt points the next system() at the tree of commit at, for
// `render --at`. The page, the blocks it cites, their values, their line
// ranges and the commit in a permalink then all come from that commit; it
// used to take only the page's text from it, so the links pointed at
// today's line numbers under today's commit (bug 60).
func (a *App) renderAt(doc, at string) error {
	tv, ok := a.vcs.(TreeVCS)
	if !ok {
		return ErrNoTree
	}
	sha, err := tv.Resolve(at)
	if err != nil {
		return err
	}
	view, err := newCommitFS(a.vcs, tv, sha)
	if err != nil {
		return err
	}
	if _, ok := view.sizes[doc]; !ok {
		return fmt.Errorf("%s does not exist at %s", doc, at)
	}
	a.atFS, a.atCommit = view, sha
	return nil
}

// Resolve returns the short sha commit names.
func (g Git) Resolve(commit string) (string, error) {
	out, err := g.run("rev-parse", "--short=7", "--verify", "--quiet", endOfOptions, commit+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%s is not a commit", commit)
	}
	return strings.TrimSpace(string(out)), nil
}

// Tree lists every blob in commit's tree with its size. Submodules (commit
// entries) carry no size and are left out, as a scan of the work tree does
// not descend into them either.
func (g Git) Tree(commit string) (map[string]int64, error) {
	out, err := g.run("ls-tree", "-r", "-l", "-z", "--full-tree", endOfOptions, commit)
	if err != nil {
		return nil, err
	}
	return parseTree(string(out)), nil
}

// parseTree reads `git ls-tree -r -l -z` output: NUL-terminated entries of
// "<mode> <type> <object> <size>\t<path>", the size padded with spaces.
func parseTree(out string) map[string]int64 {
	files := map[string]int64{}
	for _, e := range strings.Split(out, "\x00") {
		meta, p, ok := strings.Cut(e, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 4 || f[1] != "blob" {
			continue
		}
		size, err := strconv.ParseInt(f[3], 10, 64)
		if err != nil {
			continue
		}
		files[p] = size
	}
	return files
}
