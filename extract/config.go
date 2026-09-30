package extract

import (
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/sentence"
)

// Config is the structured tier for formats that need no parser dependency
// (§10): env, ini, properties, and line-mode yaml and toml. A `ds:def` as a
// trailing comment binds the key on that line; as a whole-line comment it
// binds the next non-blank line. The block is the key's line; the symbol is
// the key path (`auth.port` for nested yaml, `[server]` table prefixes for
// toml, `section.key` for ini); the default value pick is that key's value.
// `span=+N` widens the block for multi-line values.
//
// This is line-mode on purpose: it handles the shape real config files have
// and never returns a wrong value silently, because a key whose value is a
// nested map or a block scalar produces a range, not a value, and `cfg`
// refuses ranges. Full parsers live in ext/structured for the rest.
type Config struct{}

// configExts is the set of extensions this tier claims.
var configExts = map[string]bool{
	".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".cfg": true, ".conf": true,
	".env": true, ".tpl": true, ".properties": true, ".editorconfig": true, "taskfile.yml": true,
}

// Name implements Extractor.
func (Config) Name() string { return "config" }

// Match implements Extractor.
func (Config) Match(p string) bool {
	if extIn(p, configExts) {
		return true
	}
	// `.env.example`, `.env.prod`, `.env.staging.tpl` all have `.env` in the
	// name and no useful extension.
	base := strings.ToLower(p[strings.LastIndex(p, "/")+1:])
	return strings.HasPrefix(base, ".env")
}

// Extract implements Extractor.
func (Config) Extract(p string, src []byte, prefix string) Found {
	var f Found
	lines := splitLines(src)
	st, ok := StyleFor(p)
	if !ok {
		st = Style{Line: []string{"#"}}
	}
	yaml := isYAMLPath(p)
	occs := scanComments(lines, st, prefix, &f)
	carriers := carrierLines(occs)
	for _, o := range occs {
		switch o.dir.Verb {
		case VerbDef:
			bindConfig(o, lines, yaml, carriers, &f)
		default:
			r := block.Reference{Verb: o.dir.Verb, ID: o.dir.Args[block.KeyID], Pos: o.pos, Carrier: o.carrier, Args: o.dir.Args}
			f.Refs = append(f.Refs, Ref{Directive: o.dir, Reference: r})
		}
	}
	f.Refs = append(f.Refs, scanLinks(lines, prefix, &f, sentence.Bind)...)
	return f
}

func isYAMLPath(p string) bool {
	e := Ext(p)
	return e == ".yaml" || e == ".yml" || e == "taskfile.yml"
}

// bindConfig binds a def to a key line.
func bindConfig(o occurrence, lines []string, yaml bool, carriers map[int]bool, f *Found) {
	id, ok := requireID(o.dir, o.pos, f)
	if !ok {
		return
	}
	if isRemote(o.dir) {
		f.Defs = append(f.Defs, remoteDef(o.dir, id, o.pos, o.carrier))
		return
	}
	n, hasSpan, err := parseSpan(o.dir)
	if err != nil {
		f.Problems = append(f.Problems, Problem{Pos: o.pos, Err: err})
		return
	}
	var start int
	if o.trailing {
		start = o.pos.Start
	} else {
		start = nextNonBlank(lines, o.pos.End+1)
		if start == 0 {
			f.Problems = append(f.Problems, Problem{Pos: o.pos, Err: ErrNothingToBind})
			return
		}
	}
	end := start
	if hasSpan {
		end = spanEnd(lines, start, n+1, carriers)
	}
	keyLine := lines[start-1]
	if o.trailing {
		keyLine = o.code
	}
	symbol := keyPath(lines, start, keyLine, yaml)
	// Content is the key line with any trailing directive comment removed,
	// so the hash reflects the value and not the directive.
	content, hashed := keyLine, keyLine
	if end > start {
		// A child key's own carrier sits inside this block; it is left out
		// of the hash like every other (hashedJoin).
		content = keyLine + "\n" + join(lines, start+1, end)
		hashed = keyLine + "\n" + hashedJoin(lines, start+1, end, carriers)
	}
	kind := block.KindKey
	if symbol == "" {
		kind = block.KindLine
	}
	def := newDef(o.dir, id, kind, symbol, block.Position{Start: start, End: end}, o.pos, o.carrier, content)
	def.Block.SetContentHashed(content, hashed)
	f.Defs = append(f.Defs, def)
}

// keyPath derives the symbol for a config line. For yaml it walks upward by
// indentation to build `a.b.key`. For toml it prefixes the enclosing table.
// For ini it prefixes the enclosing `[section]`. For env and properties it is
// the bare key. Lines with no recognisable key return "".
func keyPath(lines []string, lineNo int, keyLine string, yaml bool) string {
	parts := keyParts(lines, lineNo, keyLine, yaml)
	for i, s := range parts {
		parts[i] = quoteSegment(s)
	}
	return strings.Join(parts, ".")
}

// keyParts is keyPath before it is spelled: the segments leading to the key
// on lineNo, unquoted. Comparing segments rather than joined strings is what
// lets a key that contains a dot be found at all.
func keyParts(lines []string, lineNo int, keyLine string, yaml bool) []string {
	key := keyOf(keyLine, yaml)
	if key == "" {
		return nil
	}
	if yaml {
		parts := []string{key}
		indent := indentOf(keyLine)
		for i := lineNo - 2; i >= 0 && indent > 0; i-- {
			l := lines[i]
			if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
				continue
			}
			if ind := indentOf(l); ind < indent {
				if k := keyOf(l, true); k != "" {
					parts = append([]string{k}, parts...)
				}
				indent = ind
			}
		}
		return parts
	}
	// toml / ini: find the nearest enclosing [table] above.
	for i := lineNo - 2; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			return append(splitKeyPath(strings.Trim(t, "[]")), key)
		}
	}
	return []string{key}
}

func keyOf(line string, yaml bool) string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "export ")
	if t == "" || t[0] == '#' || t[0] == ';' || t[0] == '[' || t[0] == '-' {
		return ""
	}
	// A quoted key is everything inside its quotes, colons and dots included.
	if q := t[0]; q == '"' || q == '\'' {
		j := strings.IndexByte(t[1:], q)
		if j < 0 {
			return ""
		}
		if rest := strings.TrimSpace(t[j+2:]); strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, "=") {
			return t[1 : j+1]
		}
		return ""
	}
	i := keyEnd(t, yaml)
	if i <= 0 {
		return ""
	}
	k := strings.TrimSpace(t[:i])
	if strings.ContainsAny(k, " \t") {
		return ""
	}
	return k
}

// keyEnd returns where a key ends: the first `=`, or the first `:` that ends
// it. In YAML that is a colon followed by whitespace or the end of the line,
// because a colon anywhere else is part of the key -- `wfsys:up:` declares the
// key `wfsys:up`, which is how every Taskfile names a namespaced task. Reading
// the first colon made all of them unaddressable. Other formats keep the first
// colon, since a Java properties file writes `key:value` with no space.
func keyEnd(t string, yaml bool) int {
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

// quoteSegment spells one key-path segment so the path round-trips: a key that
// contains the path separator, or begins with a quote, is written quoted.
func quoteSegment(k string) string {
	if strings.ContainsAny(k, ".\"'") {
		return `"` + k + `"`
	}
	return k
}

// splitKeyPath is the inverse of the spelling quoteSegment produces: it splits
// on dots outside quotes and removes the quotes, so `tasks."a.b".desc` is
// three segments and `tasks.wfsys:up` is two.
func splitKeyPath(s string) []string {
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

func indentOf(l string) int {
	return len(l) - len(strings.TrimLeft(l, " \t"))
}
