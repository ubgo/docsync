// Package keypath is the one grammar for config key paths, shared by the
// config tier that binds keys in a file (extract) and the pick that reads a
// key out of a remote def's target (pick). Two parsers of the same thing
// disagreed: the tier read `wfsys:up:` as the key `wfsys:up`, the pick split
// it at the first colon, so `pick=yaml:tasks.wfsys:up` found nothing while
// `ds def Taskfile.yml#tasks.wfsys:up` worked (bug 135).
package keypath

import "strings"

// End returns where a key ends in the trimmed line t: the first `=`, or the
// first `:` that ends it, or -1. In YAML that is a colon followed by
// whitespace or the end of the line, because a colon anywhere else is part of
// the key -- `wfsys:up:` declares the key `wfsys:up`, which is how every
// Taskfile names a namespaced task. Other formats keep the first colon, since
// a Java properties file writes `key:value` with no space.
func End(t string, yaml bool) int {
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '=':
			return i
		case ':':
			if !yaml || i+1 == len(t) || t[i+1] == ' ' || t[i+1] == '\t' {
				return i
			}
		}
	}
	return -1
}

// Quote spells one path segment so the path round-trips: a key that contains
// the path separator, or a quote, is written quoted.
func Quote(k string) string {
	if strings.ContainsAny(k, ".\"'") {
		return `"` + k + `"`
	}
	return k
}

// Split is the inverse of the spelling Quote produces: it splits on dots
// outside quotes and removes the quotes, so `tasks."a.b".desc` is three
// segments and `tasks.wfsys:up` is two.
func Split(s string) []string {
	var out []string
	var cur strings.Builder
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0 && c == q:
			q = 0
		case q != 0:
			cur.WriteByte(c)
		case c == '"' || c == '\'':
			q = c
		case c == '.':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	return append(out, cur.String())
}

// Unquote removes one pair of matching quotes around a key as written in the
// file (`"a.b":`), so it compares with a path segment.
func Unquote(k string) string {
	if len(k) >= 2 && (k[0] == '"' || k[0] == '\'') && k[len(k)-1] == k[0] {
		return k[1 : len(k)-1]
	}
	return k
}
