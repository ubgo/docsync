package treesitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/sql"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/extract"
)

// Grammar tables for the shipped languages. Node type names are the
// grammars' own; each table was checked against parsed samples in the
// tests, which fail if a grammar upgrade renames a node.

// Go declarations. Methods are named `Type.Method` through the receiver.
var goGrammar = Grammar{
	Name: "go", Exts: []string{".go"}, Language: golang.GetLanguage(),
	Kinds: map[string]block.Kind{
		"function_declaration": block.KindFunc,
		"method_declaration":   block.KindFunc,
		"type_declaration":     block.KindType,
		"const_declaration":    block.KindConst,
		"var_declaration":      block.KindConst,
		// The *_spec nodes are the individual entries inside a parenthesised
		// group: `const ( A = 1; B = 2 )` is one const_declaration holding two
		// const_specs, and Go code groups related constants far more often
		// than it declares them alone. Without these a directive above a
		// grouped entry matched nothing here, and find() walked past the
		// group to bind the NEXT top-level declaration in silence -- the doc
		// sentence was then checked against an unrelated constant (bug 18).
		// A directive above the group's own `const (` line still binds the
		// whole group, because find() classifies the outer node first.
		"const_spec": block.KindConst,
		"var_spec":   block.KindConst,
		"type_spec":  block.KindType,
		// Named members of a declaration's body. Same shape as the grouped
		// specs above and the same reason: a member absent from this table is
		// not merely undefinable, it makes the finder walk out of the body it
		// was pointed into. A struct field or an interface method is exactly
		// what a doc sentence restates, and an import is how a doc names the
		// dependency it is describing.
		//
		// This grammar calls an interface's method method_elem and an embedded
		// interface type_elem; earlier releases used method_spec, so the tests
		// parse a sample and fail if a rename lands.
		"field_declaration":  block.KindConst,
		"method_elem":        block.KindFunc,
		"type_elem":          block.KindType,
		"import_spec":        block.KindConst,
		"import_declaration": block.KindConst,
	},
	// The parenthesised groups and the list node the Go grammar puts inside a
	// `var ( … )`: containers of entries, never part of an entry's name.
	Groups:    map[string]bool{"const_declaration": true, "var_declaration": true, "type_declaration": true, "var_spec_list": true, "import_declaration": true, "import_spec_list": true},
	Literals:  map[string]bool{"int_literal": true, "float_literal": true, "interpreted_string_literal": true, "raw_string_literal": true, "true": true, "false": true},
	Symbol:    goSymbol,
	Qualifier: goReceiver,
}

// goReceiver returns the receiver's type name for a method, "" otherwise;
// a pointer receiver names the pointed-to type.
func goReceiver(n *sitter.Node, src []byte) string {
	if n.Type() != "method_declaration" {
		return ""
	}
	recv := n.ChildByFieldName("receiver")
	if recv == nil || recv.NamedChildCount() == 0 {
		return ""
	}
	first := recv.NamedChild(0)
	if typ := first.ChildByFieldName("type"); typ != nil && typ.Type() == "pointer_type" && typ.NamedChildCount() > 0 {
		return typ.NamedChild(0).Content(src)
	}
	return fieldText(first, "type", src)
}

// goSymbol names a Go declaration: functions and methods by name, type,
// const, and var declarations by their first specifier, and the members of a
// body by the part of them that a reader would grep for.
func goSymbol(n *sitter.Node, src []byte) string {
	if name := fieldText(n, fieldName, src); name != "" {
		return name
	}
	switch n.Type() {
	case nodeImportDeclaration:
		// `import "fmt"` with no group: the declaration holds one spec, and the
		// name a reader greps for is that spec's.
		for i := 0; i < int(n.NamedChildCount()); i++ {
			if c := n.NamedChild(i); c.Type() == nodeImportSpec {
				return goSymbol(c, src)
			}
		}
	case nodeImportSpec:
		// An unaliased import has no name, only a path: `"net/http"` is what
		// someone looking for it would search, so the quotes come off and the
		// path is the symbol. An aliased one was named above.
		return strings.Trim(fieldText(n, fieldPath, src), `"`)
	case nodeFieldDeclaration, nodeTypeElem:
		// An embedded field or interface is named by the type it embeds, which
		// is also how Go itself refers to it.
		if ty := fieldText(n, fieldType, src); ty != "" {
			return ty
		}
		if n.NamedChildCount() > 0 {
			c := n.NamedChild(0)
			if name := fieldText(c, fieldName, src); name != "" {
				return name
			}
			return c.Content(src)
		}
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if name := fieldText(c, fieldName, src); name != "" {
			return name
		}
		for j := 0; j < int(c.NamedChildCount()); j++ {
			if name := fieldText(c.NamedChild(j), fieldName, src); name != "" {
				return name
			}
		}
	}
	return ""
}

// ecmaKinds are shared by TypeScript, TSX, and JavaScript, whose grammars
// use the same node names for declarations.
var ecmaKinds = map[string]block.Kind{
	"function_declaration":           block.KindFunc,
	"generator_function_declaration": block.KindFunc,
	"method_definition":              block.KindFunc,
	"class_declaration":              block.KindType,
	"abstract_class_declaration":     block.KindType,
	"interface_declaration":          block.KindType,
	"type_alias_declaration":         block.KindType,
	"enum_declaration":               block.KindType,
	"lexical_declaration":            block.KindConst,
	"variable_declaration":           block.KindConst,
	"public_field_definition":        block.KindConst,
	// Members of a body, for the same reason as Go's: absent from this table a
	// member is not just undefinable, it sends the finder out of the body the
	// directive pointed into, to bind whatever came next.
	"property_signature": block.KindConst,
	"enum_assignment":    block.KindConst,
	"method_signature":   block.KindFunc,
	// A property of an object literal. Configuration in this ecosystem is an
	// object -- Vite, Vitest, ESLint, Tailwind -- and `server: { port: 5173 }`
	// is exactly the value a doc restates. It was left out once for fear of
	// surprising anchors, but binding only ever happens under a directive a
	// person wrote, and a directive above a pair used to bind nothing at all,
	// so this can add defs and cannot move one.
	nodePair: block.KindConst,
}

var (
	ecmaLiterals = map[string]bool{"number": true, "string": true, "template_string": true, "true": true, "false": true, "null": true}
	ecmaWrappers = map[string]bool{"export_statement": true}
	// The bodies that hold the members above: containers, not name segments.
	ecmaGroups = map[string]bool{"interface_body": true, "enum_body": true, "class_body": true, "statement_block": true}
)

// ecmaSymbol names a declaration; `const a = 1, b = 2` takes the first.
func ecmaSymbol(n *sitter.Node, src []byte) string {
	if n.Type() == nodePair {
		return pairPath(n, src)
	}
	if name := fieldText(n, fieldName, src); name != "" {
		return name
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if name := fieldText(n.NamedChild(i), fieldName, src); name != "" {
			return name
		}
	}
	return ""
}

// pairPath names an object-literal property by the keys that lead to it,
// `server.port`, because a bare `port` is ambiguous in any config with more
// than one server. A quoted key loses its quotes; a computed key (`[k]: 1`) has
// no name a reader could search for, so it names nothing and the lookup by
// name cannot reach it, though a directive above it still binds.
func pairPath(n *sitter.Node, src []byte) string {
	var keys []string
	for p := n; p != nil; p = p.Parent() {
		if p.Type() != nodePair {
			continue
		}
		k := p.ChildByFieldName(fieldKey)
		if k == nil || k.Type() == nodeComputedProperty {
			return ""
		}
		keys = append([]string{strings.Trim(k.Content(src), `"'`)}, keys...)
	}
	return strings.Join(keys, ".")
}

var (
	typescriptGrammar = Grammar{Name: "typescript", Exts: []string{".ts", ".mts", ".cts"}, Language: typescript.GetLanguage(), Kinds: ecmaKinds, Wrappers: ecmaWrappers, Groups: ecmaGroups, Literals: ecmaLiterals, Symbol: ecmaSymbol}
	tsxGrammar        = Grammar{Name: "tsx", Exts: []string{".tsx"}, Language: tsx.GetLanguage(), Kinds: ecmaKinds, Wrappers: ecmaWrappers, Groups: ecmaGroups, Literals: ecmaLiterals, Symbol: ecmaSymbol}
	javascriptGrammar = Grammar{Name: "javascript", Exts: []string{".js", ".jsx", ".mjs", ".cjs"}, Language: javascript.GetLanguage(), Kinds: ecmaKinds, Wrappers: ecmaWrappers, Groups: ecmaGroups, Literals: ecmaLiterals, Symbol: ecmaSymbol}
)

// Python: decorators are skipped by binding the inner definition; a bare
// assignment at any level is a const named by its target.
var pythonGrammar = Grammar{
	Name: "python", Exts: []string{".py", ".pyi"}, Language: python.GetLanguage(),
	Kinds: map[string]block.Kind{
		"function_definition":  block.KindFunc,
		"class_definition":     block.KindType,
		"expression_statement": block.KindConst,
	},
	Wrappers: map[string]bool{"decorated_definition": false},
	Literals: map[string]bool{"integer": true, "float": true, "string": true, "true": true, "false": true, "none": true},
	Symbol:   pythonSymbol,
}

// pythonSymbol names definitions by name and assignments by their target.
func pythonSymbol(n *sitter.Node, src []byte) string {
	if name := fieldText(n, fieldName, src); name != "" {
		return name
	}
	if n.Type() == "expression_statement" && n.NamedChildCount() > 0 {
		if a := n.NamedChild(0); a.Type() == "assignment" {
			return fieldText(a, "left", src)
		}
	}
	return ""
}

// SQL: every statement is a block; `create` statements are named by the
// object they create, others by their verb.
var sqlGrammar = Grammar{
	Name: "sql", Exts: []string{".sql"}, Language: sql.GetLanguage(),
	Comments: map[string]bool{"comment": true, "marginalia": true},
	Symbol:   sqlSymbol,
}

// sqlSymbol names a statement by the object a `create` makes, else by its
// verb. Only `statement` nodes have names; the grammar has no nesting.
func sqlSymbol(n *sitter.Node, src []byte) string {
	if n.Type() != nodeStatement || n.NamedChildCount() == 0 {
		return ""
	}
	first := n.NamedChild(0)
	for i := 0; i < int(first.NamedChildCount()); i++ {
		if c := first.NamedChild(i); c.Type() == "object_reference" {
			return c.Content(src)
		}
	}
	return first.Type()
}

// Go returns the Go syntax tier.
func Go() extract.Extractor { return New(goGrammar) }

// TypeScript returns the TypeScript tier for .ts files.
func TypeScript() extract.Extractor { return New(typescriptGrammar) }

// TSX returns the TypeScript tier for .tsx files.
func TSX() extract.Extractor { return New(tsxGrammar) }

// JavaScript returns the JavaScript tier.
func JavaScript() extract.Extractor { return New(javascriptGrammar) }

// Python returns the Python tier.
func Python() extract.Extractor { return New(pythonGrammar) }

// SQL returns the SQL tier.
func SQL() extract.Extractor { return New(sqlGrammar) }

// All returns every shipped tier, for docsync.WithExtractor(All()...).
func All() []extract.Extractor {
	return []extract.Extractor{Go(), TypeScript(), TSX(), JavaScript(), Python(), SQL()}
}
