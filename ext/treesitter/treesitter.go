// Package treesitter is the syntax tier (docs/SPEC.md §10, §37.1): a
// `ds:def` in a comment binds to the next declaration as the grammar sees
// it, so blocks end where the language says they end and hashes are taken
// over the token stream, so formatters never cry wolf. Go, TypeScript,
// JavaScript, Python, and SQL ship here; any other tree-sitter grammar is
// registered through New with a Grammar describing its declaration nodes.
//
// This module is cgo: the grammars compile from C on first build.
package treesitter

import (
	"context"
	"path"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/extract"
)

// Grammar describes how one language's tree maps to docsync blocks. Only
// the fields a language needs are set; the zero value of each means "the
// generic rule applies".
type Grammar struct {
	// Name is the tier name `check --explain` prints and Extractor.Name
	// returns, e.g. "go".
	Name string
	// Exts are the lowercase extensions this grammar claims, with the dot.
	Exts []string
	// Language is the compiled grammar.
	Language *sitter.Language
	// Kinds maps declaration node types to the block kind they define.
	// Node types absent here bind only under the generic rule: a type
	// named "statement" or ending in "_statement" or "_declaration" is a
	// statement.
	Kinds map[string]block.Kind
	// Wrappers are node types that hold a declaration rather than being
	// one (`export_statement`, `decorated_definition`). The value says
	// whether the block keeps the wrapper's range (true: `export function`
	// includes `export`) or the inner declaration's (false: decorators are
	// skipped, per §7.1).
	Wrappers map[string]bool
	// Groups are node types that hold sibling declarations rather than
	// qualifying them: a Go `const ( … )` contains const_specs, and the group
	// is not part of any entry's name. A type listed here may also appear in
	// Kinds -- Kinds says a directive above the group binds the whole group,
	// Groups says the group never contributes a segment to a name inside it.
	// Without it a grouped `type ( Shape … )` was named "Shape.Shape", because
	// the group takes its own name from its first entry.
	Groups map[string]bool
	// Comments are node types hashed only for frozen blocks.
	Comments map[string]bool
	// Literals are node types that make a one-name declaration a value: the
	// block's content is the literal, not the statement (§10 "a value only
	// when the block is a single literal").
	Literals map[string]bool
	// Symbol names a declaration node; nil uses the node's `name` field.
	Symbol func(n *sitter.Node, src []byte) string
	// Qualifier returns an extra leading segment for a node's symbol, for
	// languages whose methods name their type outside the nesting (Go
	// receivers). nil adds nothing.
	Qualifier func(n *sitter.Node, src []byte) string
}

// New returns an extractor for the grammar. It panics on a Grammar without
// a Name, Exts, or Language: that is a programming error at registration,
// not a runtime condition.
func New(g Grammar) extract.Extractor {
	if g.Name == "" || len(g.Exts) == 0 || g.Language == nil {
		panic("treesitter: Grammar needs Name, Exts, and Language")
	}
	if g.Comments == nil {
		g.Comments = map[string]bool{nodeComment: true}
	}
	exts := map[string]bool{}
	for _, e := range g.Exts {
		exts[strings.ToLower(e)] = true
	}
	return tier{g: g, exts: exts}
}

// Node type names shared by the shipped grammars.
const (
	nodeComment           = "comment"
	fieldName             = "name"
	fieldValue            = "value"
	fieldRight            = "right"
	fieldDeclaration      = "declaration"
	fieldDefinition       = "definition"
	nodeImportSpec        = "import_spec"
	nodeImportDeclaration = "import_declaration"
	nodeFieldDeclaration  = "field_declaration"
	nodeTypeElem          = "type_elem"
	fieldPath             = "path"
	fieldKey              = "key"
	nodePair              = "pair"
	nodeComputedProperty  = "computed_property_name"
	fieldType             = "type"
	suffixStatement       = "_statement"
	suffixDeclaration     = "_declaration"
	nodeStatement         = "statement"
	symbolSep             = "."
	tokenSep              = " "
)

type tier struct {
	g    Grammar
	exts map[string]bool
}

func (t tier) Name() string { return t.g.Name }

func (t tier) Match(p string) bool { return t.exts[strings.ToLower(path.Ext(p))] }

// Extract implements extract.Extractor. Directives are read by the root's
// comment scanner; only binding is grammar-aware.
func (t tier) Extract(p string, src []byte, prefix string) extract.Found {
	var f extract.Found
	lines, defs := extract.ScanCode(p, src, prefix, &f)
	if len(defs) == 0 {
		return f
	}
	carriers := extract.Carriers(p, lines, prefix)
	parser := sitter.NewParser()
	parser.SetLanguage(t.g.Language)
	// ParseCtx only fails when the context is cancelled; a background
	// context never is, so the error carries nothing.
	tree, _ := parser.ParseCtx(context.Background(), nil, src)
	defer tree.Close()
	root := tree.RootNode()
	offsets := lineOffsets(src)
	for _, o := range defs {
		id, span, hasSpan, ok := extract.Prelude(o, &f)
		if !ok {
			continue
		}
		if o.Trailing {
			// A trailing def binds the code on its own line, as in the
			// heuristic tier; the grammar adds nothing to a one-line block.
			f.Defs = append(f.Defs, extract.NewDef(o, id, block.KindLine, "", block.Position{Start: o.Pos.Start, End: o.Pos.Start}, o.Code))
			continue
		}
		from := uint32(len(src))
		if o.Pos.End < len(offsets) {
			from = offsets[o.Pos.End]
		}
		d := t.find(root, from)
		if d.node == nil {
			extract.NothingToBind(o, &f)
			continue
		}
		start := int(d.node.StartPoint().Row) + 1
		// The block must begin on the first code line below the directive.
		// find() walks forward until the grammar recognises something, so a
		// construct the grammar tables miss would otherwise bind whatever
		// declaration came next, silently and far away (bug 18).
		if want := extract.FirstCodeLine(p, lines, o.Pos.End+1); want != 0 && start > want {
			extract.SkippedCode(o, &f, start)
			continue
		}
		end := int(d.node.EndPoint().Row) + 1
		if hasSpan {
			end = extract.SpanEnd(lines, start, span+1, carriers)
		}
		content := extract.Join(lines, start, end)
		hashed := extract.HashedJoin(lines, start, end, carriers)
		frozen := block.Block{Args: o.Directive.Args}.Stability() == block.StabilityFrozen
		if lit := t.literal(d, src); lit != "" && !hasSpan {
			content, hashed = lit, lit
		} else if !hasSpan {
			hashed = t.tokens(d.node, src, frozen)
		}
		def := extract.NewDef(o, id, d.kind, t.symbol(d, src), block.Position{Start: start, End: end}, content)
		def.Block.SetContentHashed(content, hashed)
		f.Defs = append(f.Defs, def)
	}
	return f
}

// decl is a declaration the finder chose: the node whose range is the
// block, the node that names it (differs under a wrapper), and its kind.
type decl struct {
	node, named *sitter.Node
	kind        block.Kind
}

// find returns the first declaration starting at or after byte offset from,
// in source order, without descending into a chosen declaration: a def
// before a class binds the class; a def inside it binds the next member
// (Part VII "nested declarations bind to the innermost").
func (t tier) find(n *sitter.Node, from uint32) decl {
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c.EndByte() <= from {
			continue
		}
		if c.StartByte() >= from {
			if d, ok := t.classify(c); ok {
				return d
			}
		}
		if d := t.find(c, from); d.node != nil {
			return d
		}
	}
	return decl{}
}

// classify decides whether n is a declaration and, through a wrapper, which
// node names it.
func (t tier) classify(n *sitter.Node) (decl, bool) {
	typ := n.Type()
	if keepOuter, isWrapper := t.g.Wrappers[typ]; isWrapper {
		inner := n.ChildByFieldName(fieldDeclaration)
		if inner == nil {
			inner = n.ChildByFieldName(fieldDefinition)
		}
		if inner == nil {
			return decl{}, false
		}
		d, ok := t.classify(inner)
		if !ok {
			return decl{}, false
		}
		if keepOuter {
			d.node = n
		}
		return d, true
	}
	if k, ok := t.g.Kinds[typ]; ok {
		return decl{node: n, named: n, kind: k}, true
	}
	if typ == nodeStatement || strings.HasSuffix(typ, suffixStatement) || strings.HasSuffix(typ, suffixDeclaration) {
		return decl{node: n, named: n, kind: block.KindStatement}, true
	}
	return decl{}, false
}

// symbol builds `Outer.inner` from the naming node and its declaring
// ancestors, plus the grammar's qualifier.
func (t tier) symbol(d decl, src []byte) string {
	own := t.nameOf(d.named, src)
	if own == "" {
		return ""
	}
	parts := []string{own}
	if t.g.Qualifier != nil {
		if q := t.g.Qualifier(d.named, src); q != "" {
			parts = append([]string{q}, parts...)
		}
	}
	for p := d.named.Parent(); p != nil; p = p.Parent() {
		if t.g.Groups[p.Type()] {
			continue
		}
		if k, ok := t.g.Kinds[p.Type()]; ok && (k == block.KindFunc || k == block.KindType) {
			if name := t.nameOf(p, src); name != "" {
				parts = append([]string{name}, parts...)
			}
		}
	}
	return strings.Join(parts, symbolSep)
}

func (t tier) nameOf(n *sitter.Node, src []byte) string {
	if t.g.Symbol != nil {
		return t.g.Symbol(n, src)
	}
	return fieldText(n, fieldName, src)
}

// literal returns the value text when the declaration declares one name
// with a literal value, else "".
func (t tier) literal(d decl, src []byte) string {
	if d.kind != block.KindConst {
		return ""
	}
	specs := valueNodes(d.named)
	if len(specs) != 1 {
		return ""
	}
	v := specs[0]
	// Grammars wrap a value in a list or expression node with one child
	// (Go's expression_list); the literal is the node inside.
	for !t.g.Literals[v.Type()] && v.NamedChildCount() == 1 {
		v = v.NamedChild(0)
	}
	if !t.g.Literals[v.Type()] {
		return ""
	}
	return v.Content(src)
}

// valueNodes collects the value fields under a declaration, two levels of
// specifier deep, which covers `const X = 1`, `const (X = 1)`, `var (X =
// 1)` through a spec list, and `const a = 1, b = 2`. Assignments name the
// value `right`.
func valueNodes(n *sitter.Node) []*sitter.Node {
	if v := valueOf(n); v != nil {
		return []*sitter.Node{v}
	}
	var out []*sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if v := valueOf(c); v != nil {
			out = append(out, v)
			continue
		}
		for j := 0; j < int(c.NamedChildCount()); j++ {
			if v := valueOf(c.NamedChild(j)); v != nil {
				out = append(out, v)
			}
		}
	}
	return out
}

// valueOf returns a node's value or right-hand field.
func valueOf(n *sitter.Node) *sitter.Node {
	if v := n.ChildByFieldName(fieldValue); v != nil {
		return v
	}
	return n.ChildByFieldName(fieldRight)
}

// tokens flattens the node to its leaves joined by single spaces, comments
// included only for frozen blocks, so the hash ignores layout.
func (t tier) tokens(n *sitter.Node, src []byte, withComments bool) string {
	var b strings.Builder
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if t.g.Comments[n.Type()] && !withComments {
			return
		}
		if n.ChildCount() == 0 || hasUncoveredText(n, src) {
			// Some grammars surface newlines and other layout as anonymous
			// tokens (Go's statement terminators); they are not code.
			tok := strings.TrimSpace(n.Content(src))
			if tok == "" {
				return
			}
			if b.Len() > 0 {
				b.WriteString(tokenSep)
			}
			b.WriteString(tok)
			return
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(n)
	return b.String()
}

// hasUncoveredText reports whether n holds source text that none of its
// children covers. Walking only the children would drop that text from the
// hash, so such a node is taken whole, as one token.
//
// Go's grammar is the case that made this necessary: an interpreted string
// literal's children are its two quote marks (and any escape sequences),
// and the characters between them belong to no child. Walking the children
// hashed `"one"` and `"two"` identically, so a changed message, URL or
// query inside a function never flagged the sentences citing it. Where a
// grammar does give the text a node (Python's string_content, TypeScript's
// string_fragment) nothing is uncovered and the node is walked as before,
// which keeps every hash that was already right unchanged.
func hasUncoveredText(n *sitter.Node, src []byte) bool {
	at := n.StartByte()
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if strings.TrimSpace(string(src[at:c.StartByte()])) != "" {
			return true
		}
		at = c.EndByte()
	}
	return strings.TrimSpace(string(src[at:n.EndByte()])) != ""
}

// fieldText returns the source of a field child, "" when absent.
func fieldText(n *sitter.Node, field string, src []byte) string {
	c := n.ChildByFieldName(field)
	if c == nil {
		return ""
	}
	return c.Content(src)
}

// lineOffsets returns the byte offset where each 1-based line starts, with
// index 0 unused and a final entry at len(src) so "the line after the last"
// has an offset.
func lineOffsets(src []byte) []uint32 {
	out := []uint32{0, 0}
	for i, c := range src {
		if c == '\n' {
			out = append(out, uint32(i+1))
		}
	}
	return out
}
