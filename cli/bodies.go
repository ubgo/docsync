package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ubgo/docsync/workspace"
)

// The body store (§20.1). `check` has to answer "what did this block say
// when the sentence was acked", and the acked hash can be arbitrarily far
// behind: many scans back in this repo, or in another repository entirely.
// Neither the previous ledger nor `git show` can answer that — the ledger
// holds one row per id at one commit, and a merged row holds no body at all
// — so bodies are kept content-addressed, one file per hash, written by
// `scan` under .ds/blocks/ and by `publish` under the index entry's blocks/.
//
// Content-addressing makes the store append-only and self-deduplicating: a
// block that did not change writes nothing, and a hash that is present is
// correct by construction. A missing body is not an error; it degrades the
// finding to block.ClassUnknown, which still flags.
const BlocksDir = "blocks"

// The bounds a body's file name must satisfy. A full sha256 is 64 hex
// characters; the lower bound admits the shortened forms tests and tools
// use, and the upper one is a sanity limit so an absurd name never becomes a
// path. They are named because a bare 8 and 128 in a path guard read as
// arbitrary, and a future hash of a different width has to change them both
// deliberately.
const (
	minHashChars = 8
	maxHashChars = 128
	// hexDigits are the only characters a hash may contain.
	hexDigits = "0123456789abcdef"
)

// hashNameOK guards the one place a scanned value becomes a path. Hashes are
// lowercase hex from the hasher, so anything else — an empty string, a
// separator, a parent reference — is rejected rather than cleaned, because a
// "cleaned" traversal is still a read or write the caller did not intend.
func hashNameOK(hash string) bool {
	if len(hash) < minHashChars || len(hash) > maxHashChars {
		return false
	}
	return strings.TrimLeft(hash, hexDigits) == ""
}

// WriteBodies adds bodies to the local store, skipping any already present:
// the file name is the content hash, so an existing file cannot disagree
// with what would be written. Writing goes through Store.Write so there is
// exactly one atomic-write path in the CLI, and a body is never observed
// half-written by a concurrent check.
func (s *Store) WriteBodies(bodies map[string]string) error {
	if len(bodies) == 0 {
		return nil
	}
	if err := os.MkdirAll(s.path(BlocksDir), dirPerm); err != nil {
		return err
	}
	for hash, body := range bodies {
		if !hashNameOK(hash) {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.path(BlocksDir), hash)); err == nil {
			continue
		}
		if err := s.Write(filepath.Join(BlocksDir, hash), []byte(body)); err != nil {
			return err
		}
	}
	return nil
}

// BodyAt reads one body from the local store.
func (s *Store) BodyAt(hash string) (string, bool) {
	if !hashNameOK(hash) {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(s.path(BlocksDir), hash))
	if err != nil {
		return "", false
	}
	return string(raw), true
}

// bodyLookup answers by-hash body reads from the local store first and then
// from every repository in the workspace index, so a citation can be
// classified against a block defined in another repo. Results are memoised
// because one check asks for the same hash once per citing sentence.
func bodyLookup(local *Store, index fs.FS, entries []workspace.Entry) func(string) (string, bool) {
	type result struct {
		body string
		ok   bool
	}
	cache := map[string]result{}
	return func(hash string) (string, bool) {
		if r, seen := cache[hash]; seen {
			return r.body, r.ok
		}
		body, ok := "", false
		if local != nil {
			body, ok = local.BodyAt(hash)
		}
		if !ok && index != nil && hashNameOK(hash) {
			for _, e := range entries {
				if raw, err := fs.ReadFile(index, e.BodyPath(hash)); err == nil {
					body, ok = string(raw), true
					break
				}
			}
		}
		cache[hash] = result{body, ok}
		return body, ok
	}
}
