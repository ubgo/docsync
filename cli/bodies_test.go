package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/workspace"
)

const (
	hashA = "aaaaaaaabbbbbbbb"
	hashB = "ccccccccdddddddd"
)

// TestHashNameOK guards the one place a scanned value becomes a path. Every
// rejected case here is a path the store must never open or create.
func TestHashNameOK(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{hashA, "0123456789abcdef", "deadbeef"} {
		if !hashNameOK(ok) {
			t.Errorf("%q should be accepted", ok)
		}
	}
	for _, bad := range []string{
		"",                       // no hash at all
		"abc",                    // too short to be a hash
		"../../etc/passwd",       // traversal
		"..",                     // parent
		"a/b",                    // separator
		"DEADBEEF",               // hashes are lowercase
		"deadbeef.tmp",           // the store's own temp suffix
		strings.Repeat("a", 129), // absurdly long
	} {
		if hashNameOK(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

// TestStoreBodiesRoundTrip covers writing, reading back, the skip for a hash
// already present, and the refusal to touch anything that is not a hash.
func TestStoreBodiesRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.WriteBodies(nil); err != nil {
		t.Fatalf("empty write: %v", err)
	}
	if err := s.WriteBodies(map[string]string{hashA: "first", "../escape": "no"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); err == nil {
		t.Fatal("a non-hash name escaped the blocks directory")
	}
	if body, ok := s.BodyAt(hashA); !ok || body != "first" {
		t.Errorf("BodyAt = %q %v", body, ok)
	}
	// Content-addressed: an existing hash is left alone, never rewritten.
	if err := s.WriteBodies(map[string]string{hashA: "second"}); err != nil {
		t.Fatal(err)
	}
	if body, _ := s.BodyAt(hashA); body != "first" {
		t.Errorf("an existing body must not be rewritten, got %q", body)
	}
	// Misses and bad names are reported, not fatal.
	if _, ok := s.BodyAt(hashB); ok {
		t.Error("absent hash must miss")
	}
	if _, ok := s.BodyAt("../../etc/passwd"); ok {
		t.Error("traversal must miss")
	}
}

// TestWriteBodiesReportsFailure pins that a write failure surfaces rather
// than being swallowed: the store is a cache, but a silently empty one turns
// every classified drift into ClassUnknown with no explanation.
func TestWriteBodiesReportsFailure(t *testing.T) {
	t.Parallel()
	// A file where the blocks directory must go: the directory cannot be
	// created.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, DirName), dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DirName, BlocksDir), []byte("x"), filePerm); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(dir).WriteBodies(map[string]string{hashA: "body"}); err == nil {
		t.Fatal("want an error when the blocks directory cannot be created")
	}

	// The directory exists but is not writable: the body write itself fails.
	dir = t.TempDir()
	blocks := filepath.Join(dir, DirName, BlocksDir)
	if err := os.MkdirAll(blocks, dirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocks, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocks, dirPerm) })
	if err := NewStore(dir).WriteBodies(map[string]string{hashA: "body"}); err == nil {
		t.Fatal("want an error when the body cannot be written")
	}
}

// TestBodyLookupOrder checks the local store answers first, the index
// answers what the local store lacks, and an absent body is a clean miss.
// The memo is asserted by counting reads, because one check asks for the
// same hash once per citing sentence.
func TestBodyLookupOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	local := NewStore(dir)
	if err := local.WriteBodies(map[string]string{hashA: "local body"}); err != nil {
		t.Fatal(err)
	}
	index := fstest.MapFS{
		"repos/docs/blocks/" + hashA: {Data: []byte("index body")},
		"repos/docs/blocks/" + hashB: {Data: []byte("only in the index")},
	}
	entries := []workspace.Entry{{Repo: "other"}, {Repo: "docs"}}
	at := bodyLookup(local, index, entries)
	if body, ok := at(hashA); !ok || body != "local body" {
		t.Errorf("local must win: %q %v", body, ok)
	}
	if body, ok := at(hashB); !ok || body != "only in the index" {
		t.Errorf("index must answer what local lacks: %q %v", body, ok)
	}
	if _, ok := at("eeeeeeeeffffffff"); ok {
		t.Error("absent everywhere must miss")
	}
	// Memoised: deleting the file does not change the answer.
	if err := os.Remove(filepath.Join(dir, DirName, BlocksDir, hashA)); err != nil {
		t.Fatal(err)
	}
	if body, ok := at(hashA); !ok || body != "local body" {
		t.Error("result must be memoised")
	}
	// Nil store and nil index degrade to a miss rather than panicking.
	if _, ok := bodyLookup(nil, nil, nil)(hashA); ok {
		t.Error("no sources must miss")
	}
	// A bad name is never looked up in the index either.
	if _, ok := bodyLookup(nil, index, entries)("../../etc/passwd"); ok {
		t.Error("traversal must miss in the index too")
	}
}
