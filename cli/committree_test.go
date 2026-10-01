package cli

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// render --at takes the blocks, their line ranges and the permalink's
// commit from the commit it names, not only the page's text (bug 60).
func TestRenderAtUsesTheCommitsTree(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[check]\npermalink = \"https://h/{sha}/{file}#L{start}-L{end}\"\n")
	// At "old" the def sits three lines higher and the value is 8080.
	v.files["old:internal/store/write.go"] = []byte(goV1)
	v.files["old:config/app.yaml"] = []byte("auth:\n  port: 8080   # ds:def id=auth-port-h3v8n2wd\n  host: h\n")
	v.files["old:docs/page.md"] = []byte("Old [`Save`](ds:block?id=sess-save-k7m2p4xq) on [8080](ds:cfg?id=auth-port-h3v8n2wd).\n")
	write(t, dir, "internal/store/write.go", "package store\n\n// a\n// b\n// c\n"+strings.TrimPrefix(goV2, "package store\n\n"))
	write(t, dir, "docs/page.md", "New.\n")
	r := run(t, dir, v, "render", "docs/page.md", "--at", "old")
	if r.code != 0 || r.out != "Old [`Save`](https://h/old/internal/store/write.go#L4-L6) on 8080.\n" {
		t.Errorf("render --at = %+v", r)
	}
	// Today's tree, for contrast: the def moved down three lines.
	write(t, dir, "docs/page.md", "Now [`Save`](ds:block?id=sess-save-k7m2p4xq).\n")
	if r := run(t, dir, v, "render", "docs/page.md"); r.code != 0 || !strings.Contains(r.out, "/abc1234/internal/store/write.go#L7-L9") {
		t.Errorf("render = %+v", r)
	}
	// A page the commit does not have says so, without blaming git.
	r = run(t, dir, v, "render", "docs/new.md", "--at", "old")
	if r.code != ExitError || !strings.Contains(r.err, "docs/new.md does not exist at old") || strings.Contains(r.err, "not a repository") {
		t.Errorf("missing at commit = %+v", r)
	}
	if r := run(t, dir, v, "render", "docs/page.md", "--at", "nosuch"); r.code != ExitError || !strings.Contains(r.err, "nosuch is not a commit") {
		t.Errorf("unknown commit = %+v", r)
	}
	// A VCS that cannot list a tree is refused, not half-served.
	if r := run(t, dir, struct{ VCS }{v}, "render", "docs/page.md", "--at", "old"); r.code != ExitError || !strings.Contains(r.err, ErrNoTree.Error()) {
		t.Errorf("no tree = %+v", r)
	}
	broken := v
	broken.err = errors.New("ls-tree failed")
	if err := (&App{vcs: broken}).renderAt("docs/page.md", "old"); err == nil || err.Error() != "ls-tree failed" {
		t.Errorf("tree error = %v", err)
	}
}

func TestCommitFS(t *testing.T) {
	t.Parallel()
	v := fakeVCS{files: map[string][]byte{"c:a/b.txt": []byte("hello"), "c:top.md": []byte("x")}}
	view, err := newCommitFS(v, v, "c")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := fs.ReadFile(view, "a/b.txt"); err != nil || string(b) != "hello" {
		t.Errorf("read = %q %v", b, err)
	}
	f, err := view.Open("a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(f); string(b) != "hello" {
		t.Errorf("open+read = %q", b)
	}
	if info, err := fs.Stat(view, "a/b.txt"); err != nil || info.Size() != 5 || info.Name() != "b.txt" || info.IsDir() || info.Mode() != treeFileMode || !info.ModTime().IsZero() || info.Sys() != nil {
		t.Errorf("stat = %+v %v", info, err)
	}
	if info, err := fs.Stat(view, "a"); err != nil || !info.IsDir() {
		t.Errorf("stat dir = %+v %v", info, err)
	}
	if es, err := fs.ReadDir(view, "."); err != nil || len(es) != 2 {
		t.Errorf("readdir = %v %v", es, err)
	}
	var walked []string
	_ = fs.WalkDir(view, ".", func(p string, d fs.DirEntry, err error) error {
		if !d.IsDir() {
			walked = append(walked, p)
		}
		return err
	})
	if strings.Join(walked, ",") != "a/b.txt,top.md" {
		t.Errorf("walk = %v", walked)
	}
	if _, err := view.ReadFile("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing read = %v", err)
	}
	if _, err := view.Open("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing open = %v", err)
	}
	// A listed file git then fails to show is an error, not empty bytes.
	view.show = func(string) ([]byte, error) { return nil, errors.New("gone") }
	if _, err := view.Open("top.md"); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("show error = %v", err)
	}
}

func TestParseTree(t *testing.T) {
	t.Parallel()
	out := "100644 blob aaa      12\tdocs/a b.md\x00" +
		"160000 commit bbb       -\tsub\x00" +
		"100644 blob ccc       x\tbad-size\x00" +
		"no tab here\x00" +
		"100755 blob ddd 3\trun.sh\x00"
	got := parseTree(out)
	if len(got) != 2 || got["docs/a b.md"] != 12 || got["run.sh"] != 3 {
		t.Errorf("parseTree = %v", got)
	}
}

func TestGitTreeAndErrors(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	sh := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	g := Git{Dir: dir}
	if _, err := g.Tree("HEAD"); !errors.Is(err, ErrNoVCS) {
		t.Errorf("tree outside a repo = %v", err)
	}
	sh("init", "-q")
	write(t, dir, "docs/a.md", "hello\n")
	sh("add", "-A")
	sh("commit", "-q", "-m", "one")
	head, _ := g.Head()
	if sha, err := g.Resolve("HEAD"); err != nil || sha != head {
		t.Errorf("resolve = %q %v", sha, err)
	}
	if _, err := g.Resolve("nosuch"); err == nil || err.Error() != "nosuch is not a commit" {
		t.Errorf("resolve unknown = %v", err)
	}
	if tree, err := g.Tree(head); err != nil || len(tree) != 1 || tree["docs/a.md"] != 6 {
		t.Errorf("tree = %v %v", tree, err)
	}
	// A path the commit lacks is git's own message, not "not a repository"
	// (bug 60).
	_, err := g.Show(head, "docs/missing.md")
	if err == nil || errors.Is(err, ErrNoVCS) || strings.HasPrefix(err.Error(), "fatal:") || !strings.Contains(err.Error(), "docs/missing.md") {
		t.Errorf("show missing = %v", err)
	}
	// A failure git explains nowhere still says which program failed.
	if _, err := g.run("cat-file", "-e", "0000000000000000000000000000000000000000"); err == nil || errors.Is(err, ErrNoVCS) {
		t.Errorf("silent failure = %v", err)
	}
	if err := gitError(&exec.ExitError{}, ""); err == nil || !strings.HasPrefix(err.Error(), "git: ") {
		t.Errorf("empty stderr = %v", err)
	}
	if err := gitError(errors.New("exec: not found"), ""); !errors.Is(err, ErrNoVCS) {
		t.Errorf("no git = %v", err)
	}
}
