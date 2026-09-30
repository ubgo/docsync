package scan

import (
	"context"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/internal/glob"
)

// countingFS counts the files opened for reading, so a test can tell a
// scan that skipped a read from one that read and hit the content cache.
type countingFS struct {
	fstest.MapFS
	mu    sync.Mutex
	reads map[string]int
}

func (c *countingFS) ReadFile(name string) ([]byte, error) {
	c.mu.Lock()
	c.reads[name]++
	c.mu.Unlock()
	return c.MapFS.ReadFile(name)
}

// memStatCache is a StatCache over maps, recording every stamp.
type memStatCache struct {
	byHash map[string]extract.Found
	stamps map[string][2]int64
}

func (m *memStatCache) Get(p, hash string) (extract.Found, bool) {
	f, ok := m.byHash[p+"\x00"+hash]
	return f, ok
}

func (m *memStatCache) Put(p, hash string, f extract.Found) { m.byHash[p+"\x00"+hash] = f }

func (m *memStatCache) Unchanged(p string, size int64, mod time.Time) (extract.Found, bool) {
	s, ok := m.stamps[p]
	if !ok || s != [2]int64{size, mod.UnixNano()} {
		return extract.Found{}, false
	}
	for k, f := range m.byHash {
		if len(k) > len(p) && k[:len(p)+1] == p+"\x00" {
			return f, true
		}
	}
	return extract.Found{}, false
}

func (m *memStatCache) Stamp(p string, size int64, mod time.Time) {
	m.stamps[p] = [2]int64{size, mod.UnixNano()}
}

// TestStatCacheSkipsTheRead pins the scanner's side of StatCache: a file
// whose size and time match its stamp is served without being read, with
// the same result a read would give; a file with no modification time is
// always read; and a stamp is recorded both when the content cache hits and
// when a fresh extraction is stored.
func TestStatCacheSkipsTheRead(t *testing.T) {
	t.Parallel()
	mod := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	src := []byte("package p\n\n// ds:def id=alpha-k7m2p4xq\nfunc A() int { return 1 }\n")
	doc := []byte("See [a](ds:block?id=alpha-k7m2p4xq).\n")
	fsys := &countingFS{MapFS: fstest.MapFS{
		"a.go":       {Data: src, ModTime: mod},
		"d.md":       {Data: doc, ModTime: mod},
		"nomtime.md": {Data: doc},
	}, reads: map[string]int{}}
	inc, err := glob.CompileAll([]string{"**"})
	if err != nil {
		t.Fatal(err)
	}
	cache := &memStatCache{byHash: map[string]extract.Found{}, stamps: map[string][2]int64{}}
	o := Options{Prefix: "ds", Include: inc, Cache: cache}

	first, err := Scan(context.Background(), fsys, o)
	if err != nil {
		t.Fatal(err)
	}
	if fsys.reads["a.go"] != 1 || cache.stamps["a.go"] != [2]int64{int64(len(src)), mod.UnixNano()} {
		t.Fatalf("first scan: reads %v, stamps %v; want one read and a stamp after Put", fsys.reads, cache.stamps)
	}
	if _, ok := cache.stamps["nomtime.md"]; ok {
		t.Error("a file with no modification time was stamped")
	}

	second, err := Scan(context.Background(), fsys, o)
	if err != nil {
		t.Fatal(err)
	}
	if fsys.reads["a.go"] != 1 || fsys.reads["d.md"] != 1 {
		t.Errorf("second scan read stamped files again: %v", fsys.reads)
	}
	if fsys.reads["nomtime.md"] != 2 {
		t.Errorf("a file with no modification time must be read every time, read %d times", fsys.reads["nomtime.md"])
	}
	if len(second.Defs) != 1 || len(second.Refs) != 2 || second.Defs[0].Hash != first.Defs[0].Hash || second.Tier["a.go"] != first.Tier["a.go"] {
		t.Errorf("served by stamp differs from read: %+v vs %+v", second, first)
	}

	// A content-cache hit stamps too: drop the stamp, scan, and it is back.
	delete(cache.stamps, "a.go")
	if _, err := Scan(context.Background(), fsys, o); err != nil {
		t.Fatal(err)
	}
	if fsys.reads["a.go"] != 2 || cache.stamps["a.go"][0] != int64(len(src)) {
		t.Errorf("a content-cache hit did not re-stamp: reads %d, stamp %v", fsys.reads["a.go"], cache.stamps["a.go"])
	}

	// The size limit still applies before the stamp is consulted.
	o.MaxFileKB = 1
	fsys.MapFS["a.go"] = &fstest.MapFile{Data: make([]byte, 2048), ModTime: mod}
	cache.stamps["a.go"] = [2]int64{2048, mod.UnixNano()}
	res, err := Scan(context.Background(), fsys, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Defs) != 0 || !skipped(res, "a.go", SkipTooLarge) {
		t.Errorf("an oversized stamped file was served: %+v", res.Skipped)
	}
}

func skipped(res Result, p string, why SkipReason) bool {
	for _, s := range res.Skipped {
		if s.File == p && s.Reason == why {
			return true
		}
	}
	return false
}
