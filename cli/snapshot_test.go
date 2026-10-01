package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	docsync "github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
)

// A page that pins a snapshot with at= renders the block as it was at that
// commit in a plain `ds render`, with the lines it occupied then; it used to
// render the link alone unless the whole page was rendered with --at
// (bug 66).
func TestPlainRenderShowsAtSnapshot(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	ledgerRaw, _ := os.ReadFile(filepath.Join(dir, ".ds/ledger.tsv"))
	v.files["old:.ds/ledger.tsv"] = ledgerRaw
	v.files["old:internal/store/write.go"] = []byte(goV1)
	write(t, dir, "internal/store/write.go", "package store\n\n// a\n// b\n// c\n"+strings.TrimPrefix(goV2, "package store\n\n"))
	write(t, dir, "docs/frozen.md", "Frozen:\n\n<!-- ds:block id=sess-save-k7m2p4xq at=old -->\n")
	r := run(t, dir, v, "render", "docs/frozen.md")
	want := "Frozen:\n\n**Store.Save** · [`internal/store/write.go:4-6`](../internal/store/write.go#L4-L6) · as of `old`\n\n```go\nfunc (s *Store) Save() error {\n\treturn s.legacy.Save()\n}\n```\n"
	if r.code != 0 || r.out != want || strings.Contains(r.err, "no snapshot") {
		t.Errorf("plain render of at= = %+v\nwant %q", r, want)
	}
	// The inline form links to the lines at that commit too.
	write(t, dir, "docs/frozen.md", "[then](ds:block?id=sess-save-k7m2p4xq&at=old) and [now](ds:block?id=sess-save-k7m2p4xq)\n")
	if r := run(t, dir, v, "render", "docs/frozen.md"); r.out != "[then](../internal/store/write.go#L4-L6) and [now](../internal/store/write.go#L7-L9)\n" {
		t.Errorf("inline at= = %+v", r)
	}
}

// shardVCS is a fakeVCS whose tree lists paths Show cannot read, or fails.
type shardVCS struct {
	fakeVCS
	tree    map[string]int64
	treeErr error
}

func (s shardVCS) Tree(string) (map[string]int64, error) { return s.tree, s.treeErr }

func TestSnapshotHook(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	ledgerRaw, _ := os.ReadFile(filepath.Join(dir, ".ds/ledger.tsv"))
	sys, err := docsync.New(docsync.WithFS(fstest.MapFS{}))
	if err != nil {
		t.Fatal(err)
	}
	extract := func(p string, src []byte) ([]block.Block, error) {
		return sys.ExtractFile(context.Background(), p, src)
	}
	failing := func(string, []byte) ([]block.Block, error) { return nil, errors.New("no tier") }
	hook := func(vcs VCS) func(string, string) (block.Block, bool) {
		return (&App{vcs: vcs}).snapshotAt("x", extract)
	}

	files := map[string][]byte{"x:.ds/ledger.tsv": ledgerRaw, "x:internal/store/write.go": []byte(goV1)}
	b, ok := hook(fakeVCS{files: files})("sess-save-k7m2p4xq", "")
	if !ok || b.Pos.Start != 4 || !strings.Contains(b.Content, "s.legacy.Save()") {
		t.Errorf("snapshot = %+v %v", b, ok)
	}
	// Two commits, two ledgers: the second is not answered from the first.
	files["y:.ds/ledger.tsv"] = []byte("garbage\n")
	h := hook(fakeVCS{files: files})
	if _, ok := h("sess-save-k7m2p4xq", "x"); !ok {
		t.Error("first commit")
	}
	if _, ok := h("sess-save-k7m2p4xq", "y"); ok {
		t.Error("a second commit was answered from the first commit's ledger")
	}
	if _, ok := h("sess-save-k7m2p4xq", "x"); !ok {
		t.Error("cached first commit")
	}
	// Degrades to "no snapshot": unknown id, file missing at the commit, a
	// file no tier extracts, a file that no longer holds the id.
	for name, c := range map[string]struct {
		id    string
		files map[string][]byte
		ext   func(string, []byte) ([]block.Block, error)
	}{
		"unknown id":           {"nope-a2b6f8jk", files, extract},
		"file missing":         {"auth-port-h3v8n2wd", files, extract},
		"does not extract":     {"sess-save-k7m2p4xq", files, failing},
		"id gone from file":    {"sess-save-k7m2p4xq", map[string][]byte{"x:.ds/ledger.tsv": ledgerRaw, "x:internal/store/write.go": []byte("package store\n")}, extract},
		"no ledger, no tree":   {"sess-save-k7m2p4xq", map[string][]byte{}, extract},
		"corrupt ledger.tsv":   {"sess-save-k7m2p4xq", map[string][]byte{"x:.ds/ledger.tsv": []byte("garbage\n")}, extract},
		"no ledger, no shards": {"sess-save-k7m2p4xq", map[string][]byte{"x:internal/store/write.go": []byte(goV1)}, extract},
	} {
		vcs := VCS(struct{ VCS }{fakeVCS{files: c.files}})
		if name == "no ledger, no shards" {
			vcs = fakeVCS{files: c.files}
		}
		if _, ok := (&App{vcs: vcs}).snapshotAt("x", c.ext)(c.id, ""); ok {
			t.Errorf("%s: got a snapshot", name)
		}
	}
	// A sharded ledger is read from every .ds/ledger/*.tsv at the commit;
	// an unreadable or corrupt shard is skipped.
	sharded := map[string][]byte{"x:.ds/ledger/root.tsv": ledgerRaw, "x:.ds/ledger/bad.tsv": []byte("garbage\n"), "x:.ds/ledger/notes.txt": nil, "x:internal/store/write.go": []byte(goV1)}
	tree := map[string]int64{".ds/ledger/root.tsv": 1, ".ds/ledger/bad.tsv": 1, ".ds/ledger/gone.tsv": 1, ".ds/ledger/notes.txt": 1, "internal/store/write.go": 1}
	if b, ok := hook(shardVCS{fakeVCS: fakeVCS{files: sharded}, tree: tree})("sess-save-k7m2p4xq", ""); !ok || b.Pos.Start != 4 {
		t.Errorf("sharded = %+v %v", b, ok)
	}
	if _, ok := hook(shardVCS{fakeVCS: fakeVCS{files: sharded}, treeErr: errors.New("x")})("sess-save-k7m2p4xq", ""); ok {
		t.Error("a tree that cannot be listed has no shards")
	}
}
