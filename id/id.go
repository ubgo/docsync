// Package id mints, validates, and splits docsync block identifiers.
//
// An id is `<prefix>-<suffix>` (docs/SPEC.md §8). The prefix is a human slug
// chosen so grep finds it; the suffix is generated from a 32-symbol alphabet
// and is the actual identity. Renaming the prefix never changes identity, which
// is why Split exists and why equality of two ids is decided by suffix alone
// (see Same).
//
// Why eight characters: 32^8 ≈ 1.1×10^12 values, the same class as a short git
// sha, so tens of thousands of ids across a workspace never collide in
// practice. Four was rejected: 32^4 is about a million and birthday collisions
// appear in the low thousands. Length is a workspace setting and every repo in
// a workspace must agree, so it is a parameter here, never a package default
// that silently differs between callers.
//
// Randomness comes from crypto/rand. Ids are not secrets, but math/rand would
// make collisions across machines seeded at the same instant far more likely
// than the birthday math assumes.
package id

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
)

// DefaultAlphabet is the 32-symbol suffix alphabet: lowercase letters and
// digits with the glyphs that are confused in print removed (0/o, 1/l/i).
// Callers may configure another alphabet; it must be ASCII lowercase letters
// and digits with no repeats, which Config.validate enforces.
const DefaultAlphabet = "23456789abcdefghjkmnpqrstuvwxyz"

// DefaultSuffixLength is the workspace default. See the package comment for
// why eight.
const DefaultSuffixLength = 8

// minSuffixLength guards against a configuration that makes collisions likely
// enough to bite a small team. Anything under six is a mistake.
const minSuffixLength = 6

// maxSuffixLength bounds pathological configs; longer suffixes only hurt
// typing and diffs.
const maxSuffixLength = 32

// Sentinel errors.
var (
	ErrEmpty         = errors.New("id: empty")
	ErrNoSeparator   = errors.New("id: missing '-' between prefix and suffix")
	ErrBadPrefix     = errors.New("id: prefix must be lowercase words separated by dashes")
	ErrBadSuffix     = errors.New("id: suffix must be exactly the configured length from the alphabet")
	ErrBadAlphabet   = errors.New("id: alphabet must be unique lowercase ASCII letters and digits, at least 16 symbols")
	ErrBadLength     = errors.New("id: suffix length out of range")
	ErrEntropySource = errors.New("id: reading random bytes failed")
	// ErrBadShape is an id outside the §8 character rule: lowercase letters
	// and digits in at least two words joined by single dashes.
	ErrBadShape = errors.New("id: must be lowercase letters and digits in words joined by single dashes, label-suffix")
)

// minAlphabet keeps 32^n arithmetic meaningful; a 4-symbol alphabet with
// length 8 would be 65536 ids.
const minAlphabet = 16

// Config carries the two workspace settings that shape every id. Zero value is
// not usable; use Default or fill both fields.
type Config struct {
	Alphabet     string
	SuffixLength int
}

// Default returns the spec defaults.
func Default() Config {
	return Config{Alphabet: DefaultAlphabet, SuffixLength: DefaultSuffixLength}
}

// Validate reports whether the config can mint and check ids.
func (c Config) Validate() error {
	if c.SuffixLength < minSuffixLength || c.SuffixLength > maxSuffixLength {
		return fmt.Errorf("%w: %d not in [%d,%d]", ErrBadLength, c.SuffixLength, minSuffixLength, maxSuffixLength)
	}
	if len(c.Alphabet) < minAlphabet {
		return ErrBadAlphabet
	}
	seen := map[byte]bool{}
	for i := 0; i < len(c.Alphabet); i++ {
		ch := c.Alphabet[i]
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9') || seen[ch] {
			return ErrBadAlphabet
		}
		seen[ch] = true
	}
	return nil
}

// reader is the entropy source; a package-level var only so tests can inject a
// failing reader. It is never reassigned outside tests.
var reader = rand.Reader

// Mint returns a fresh suffix for the config. It does not check for
// collisions: the caller owns the merged workspace index and re-mints on a
// hit (§8). Mint is uniform over the alphabet by rejection sampling, so a
// 32-symbol alphabet gets exactly 5 bits per character with no modulo bias.
func Mint(c Config) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	n := len(c.Alphabet)
	// Largest multiple of n that fits in a byte; bytes at or above it are
	// rejected to keep the distribution uniform.
	limit := 256 - 256%n
	out := make([]byte, 0, c.SuffixLength)
	buf := make([]byte, c.SuffixLength*2)
	for len(out) < c.SuffixLength {
		if _, err := reader.Read(buf); err != nil {
			return "", fmt.Errorf("%w: %v", ErrEntropySource, err)
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, c.Alphabet[int(b)%n])
			if len(out) == c.SuffixLength {
				break
			}
		}
	}
	return string(out), nil
}

// New mints a full id from a prefix. The prefix is normalized with Slug first,
// so callers may pass a symbol name or a title.
func New(c Config, prefix string) (string, error) {
	p := Slug(prefix)
	if p == "" {
		return "", ErrBadPrefix
	}
	s, err := Mint(c)
	if err != nil {
		return "", err
	}
	return p + "-" + s, nil
}

// Split separates an id into prefix and suffix and validates both against the
// config. The suffix is the last dash-separated segment; everything before it
// is the prefix, which may itself contain dashes.
func Split(c Config, id string) (prefix, suffix string, err error) {
	if id == "" {
		return "", "", ErrEmpty
	}
	i := strings.LastIndexByte(id, '-')
	if i <= 0 || i == len(id)-1 {
		return "", "", ErrNoSeparator
	}
	prefix, suffix = id[:i], id[i+1:]
	if !validPrefix(prefix) {
		return "", "", fmt.Errorf("%w: %q", ErrBadPrefix, prefix)
	}
	if err := c.Validate(); err != nil {
		return "", "", err
	}
	if len(suffix) != c.SuffixLength || !allIn(suffix, c.Alphabet) {
		return "", "", fmt.Errorf("%w: %q", ErrBadSuffix, suffix)
	}
	return prefix, suffix, nil
}

// CheckShape reports whether id keeps the §8 character rule every reader of
// ids relies on: lowercase ASCII words of letters and digits joined by single
// dashes, at least two of them -- `<label>-<suffix>`.
//
// It is looser than Split on purpose. Split holds an id to the minting
// config, the suffix length and alphabet, and ids are also written by hand:
// the spec's own `oncall-lead-r9k1w5zb` uses a digit the alphabet leaves
// out, and short hand-made ids like `sess-ttl` cite and render correctly.
// Reporting those would bury the ids that actually break things -- `Foo_Bar`
// (bug 55), which a slug never matches, `ds rename` cannot relabel and a
// suffix lookup cannot split -- under ones that work.
func CheckShape(id string) error {
	if id == "" {
		return ErrEmpty
	}
	if !strings.Contains(id, "-") || !validPrefix(id) {
		return ErrBadShape
	}
	return nil
}

// Valid reports whether id parses under the config.
func Valid(c Config, id string) bool {
	_, _, err := Split(c, id)
	return err == nil
}

// Same reports whether two ids denote the same block: identical suffixes.
// This is the rule that makes `ds rename` safe — a relabelled prefix is still
// the same identity. Both ids must be valid; an invalid id is never Same as
// anything, including itself.
func Same(c Config, a, b string) bool {
	_, sa, errA := Split(c, a)
	_, sb, errB := Split(c, b)
	return errA == nil && errB == nil && sa == sb
}

// Slug lowercases s and collapses every run of characters outside [a-z0-9]
// into a single dash, trimming dashes at both ends. "Store.SaveSession" becomes
// "store-savesession"; "Session policy" becomes "session-policy". It is used
// both for id prefixes and for the default id of a markdown block that has no
// explicit id.
func Slug(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// validPrefix accepts one or more lowercase alphanumeric words joined by
// single dashes: the output shape of Slug. Split guarantees p is non-empty, so
// the empty case is handled by the per-word check ("" splits to [""]).
func validPrefix(p string) bool {
	for _, w := range strings.Split(p, "-") {
		if w == "" || !allIn(w, "abcdefghijklmnopqrstuvwxyz0123456789") {
			return false
		}
	}
	return true
}

func allIn(s, set string) bool {
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(set, s[i]) < 0 {
			return false
		}
	}
	return true
}
