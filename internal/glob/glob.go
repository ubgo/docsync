// Package glob matches slash-separated paths against patterns with `*`, `?`,
// `[...]`, and the `**` segment that spans directories.
//
// Why not path.Match: the standard library has no `**`, and every config in
// docsync (`scan.code`, `scan.docs`, `exclude`, `generated`, `secret`,
// `run.allow`, `policy.require_doc`) is written with it. Why not a dependency:
// the root module is stdlib-only by design (docs/SPEC.md §37.1).
//
// Semantics, chosen to match what people expect from gitignore-style tools:
//   - `*` and `?` never match `/`.
//   - `**` as a whole segment matches zero or more segments: `a/**/b` matches
//     `a/b`, `a/x/b`, `a/x/y/b`. A trailing `**` matches everything below the
//     directory (at least one more segment), never the directory path itself.
//   - `**` inside a segment (`a**b`) is treated as two stars, i.e. `*`.
//   - Patterns and paths are compared as-is; callers normalize separators
//     and case before calling. Paths are relative and never start with `/`.
//   - A pattern with no `/` matches only a single-segment path (`*.go` does
//     not match `dir/x.go`); use `**/*.go` for any depth. This is deliberate:
//     it keeps `README.md` in `scan.docs` from matching every README.
package glob

import (
	"errors"
	"path"
	"strings"
)

// ErrBadPattern is returned by Match and Compile for a malformed bracket class.
var ErrBadPattern = errors.New("glob: bad pattern")

// doubleStar is the directory-spanning segment.
const doubleStar = "**"

// Match reports whether name matches pattern. It is equivalent to
// Compile(pattern) followed by Pattern.Match but validates on every call, so
// hot loops should compile once.
func Match(pattern, name string) (bool, error) {
	p, err := Compile(pattern)
	if err != nil {
		return false, err
	}
	return p.Match(name), nil
}

// Pattern is a compiled glob. Zero value matches nothing.
type Pattern struct {
	segments []string
	source   string
}

// String returns the pattern source.
func (p Pattern) String() string { return p.source }

// Compile validates pattern and prepares it for repeated matching. Each
// segment is checked with path.Match against a probe so bracket errors surface
// here rather than silently never matching.
func Compile(pattern string) (Pattern, error) {
	if pattern == "" {
		return Pattern{}, ErrBadPattern
	}
	segs := strings.Split(pattern, "/")
	for _, s := range segs {
		if s == doubleStar {
			continue
		}
		if _, err := path.Match(s, ""); err != nil {
			return Pattern{}, ErrBadPattern
		}
	}
	return Pattern{segments: segs, source: pattern}, nil
}

// Match reports whether name matches. name uses `/` separators and no leading
// slash. An empty name never matches.
func (p Pattern) Match(name string) bool {
	if len(p.segments) == 0 || name == "" {
		return false
	}
	return matchSegments(p.segments, strings.Split(name, "/"))
}

// matchSegments is the recursive matcher. `**` tries to consume zero or more
// name segments; every other segment must match exactly one name segment via
// path.Match, which already guarantees `*` and `?` stop at `/` because a single
// segment contains none.
func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == doubleStar {
			rest := pat[1:]
			if len(rest) == 0 {
				// A trailing ** matches everything below the directory but not
				// the directory path itself: `internal/**` must not match a file
				// literally named `internal`.
				return len(name) > 0
			}
			for i := 0; i <= len(name); i++ {
				if matchSegments(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		// Compile validated every non-** segment, so path.Match cannot fail
		// here; the error is dropped deliberately.
		ok, _ := path.Match(pat[0], name[0])
		if !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// Set is an ordered list of compiled patterns; MatchAny answers "does any
// pattern match", which is how every include and exclude list in the config
// is consulted.
type Set []Pattern

// CompileAll compiles every pattern or returns the first error with the
// offending pattern in the message.
func CompileAll(patterns []string) (Set, error) {
	out := make(Set, 0, len(patterns))
	for _, s := range patterns {
		p, err := Compile(s)
		if err != nil {
			return nil, errors.Join(err, errors.New("pattern: "+s))
		}
		out = append(out, p)
	}
	return out, nil
}

// MatchAny reports whether any pattern in the set matches name. An empty set
// matches nothing, so an empty include list scans nothing and an empty exclude
// list excludes nothing — the safe defaults.
func (s Set) MatchAny(name string) bool {
	for _, p := range s {
		if p.Match(name) {
			return true
		}
	}
	return false
}
