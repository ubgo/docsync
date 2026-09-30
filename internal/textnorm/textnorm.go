// Package textnorm normalizes text before hashing so that changes which carry
// no meaning never register as changes.
//
// docs/SPEC.md §10 and §15 fix the rules: CRLF becomes LF, leading UTF-8 byte
// order marks are stripped, trailing whitespace on every line is removed, and trailing
// blank lines are dropped. The block hash is sha256 over that normalized form,
// so a Windows checkout, an editor that strips trailing spaces, or a file that
// gained a final newline does not flag every citing document as `unacked`.
//
// This package deliberately does NOT collapse interior whitespace or touch
// indentation: those are meaning in Python, YAML, and Markdown. Token-stream
// hashing for languages with a grammar lives in the syntax tier, not here.
package textnorm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// bom is the UTF-8 byte order mark some Windows editors prepend.
var bom = []byte{0xEF, 0xBB, 0xBF}

// Normalize applies the full rule set and returns the canonical bytes. It never
// returns nil for non-nil input; an input of only whitespace normalizes to an
// empty slice.
func Normalize(b []byte) []byte {
	b = TrimBOM(b)
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	// A lone CR (old Mac endings) is also a line break for our purposes.
	b = bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
	lines := bytes.Split(b, []byte("\n"))
	for i, l := range lines {
		lines[i] = bytes.TrimRight(l, " \t\f\v")
	}
	// Drop trailing blank lines.
	for len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return bytes.Join(lines, []byte("\n"))
}

// TrimBOM drops every leading UTF-8 byte order mark. Every one, not the
// first: tools that each prepend a BOM stack them, and stripping only one
// would leave Normalize non-idempotent.
//
// Readers apply it before parsing, not only before hashing: a mark left in
// front of line 1 glued itself to the first YAML key's symbol, hid a
// block carrier on line 1, and hid front matter, so a file saved by a
// Windows editor read as a different file.
func TrimBOM(b []byte) []byte {
	for bytes.HasPrefix(b, bom) {
		b = b[len(bom):]
	}
	return b
}

// NormalizeString is Normalize for strings.
func NormalizeString(s string) string {
	return string(Normalize([]byte(s)))
}

// Hash returns the lowercase hex sha256 of Normalize(b). This is the value
// stored in the ledger `hash` column and compared by `check`; the full digest
// is stored, callers may truncate for display.
func Hash(b []byte) string {
	sum := sha256.Sum256(Normalize(b))
	return hex.EncodeToString(sum[:])
}

// HashLines hashes a slice of lines as if joined by "\n". It exists so a
// caller holding a block as lines does not allocate the joined form twice.
func HashLines(lines []string) string {
	return Hash([]byte(strings.Join(lines, "\n")))
}

// ShortHashLength is how many hex characters findings and ledgers show. Six
// hex digits (24 bits) is enough to tell two versions of one block apart in a
// report; equality checks always use the full digest.
const ShortHashLength = 6

// Short truncates a full hash for display. Shorter inputs are returned as-is.
func Short(h string) string {
	if len(h) <= ShortHashLength {
		return h
	}
	return h[:ShortHashLength]
}
