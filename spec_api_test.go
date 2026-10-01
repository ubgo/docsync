package docsync

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// specAPIPackages maps the package names SPEC §37 writes identifiers under
// to the directory that defines them.
var specAPIPackages = map[string]string{
	"docsync":    ".",
	"cli":        "cli",
	"treesitter": "ext/treesitter",
	"structured": "ext/structured",
	"sqlite":     "ext/records/sqlite",
	"records":    "records",
	"check":      "check",
	"extract":    "extract",
	"match":      "match",
	"config":     "config",
	"procplugin": "procplugin",
	"pick":       "pick",
	"render":     "render",
	"ledger":     "ledger",
	"block":      "block",
}

// specAPIRE finds a qualified exported identifier, docsync.WithRecords.
var specAPIRE = regexp.MustCompile(`\b([a-z]+)\.([A-Z][A-Za-z0-9]*)\b`)

// TestSpecArchitectureNamesRealAPI pins bug 123: SPEC §37, which tells
// another Go project how to embed docsync, named options, types, modules
// and grammars that did not exist (WithRecordSource, a cli.WithResolver,
// treesitter.Kotlin, ext/resolve/*, a separate mcp module). Every qualified
// exported identifier in §37 must be declared by the package it names, and
// every module path it names must have a go.mod, so the section cannot
// drift from `go doc` again.
func TestSpecArchitectureNamesRealAPI(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("docs/SPEC.md")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	start, end := strings.Index(spec, "### 37. "), strings.Index(spec, "### 38. ")
	if start < 0 || end < start {
		t.Fatal("SPEC has no section 37 followed by 38")
	}
	section := spec[start:end]
	decls := map[string]map[string]bool{}
	checked := 0
	for _, m := range specAPIRE.FindAllStringSubmatch(section, -1) {
		dir, ok := specAPIPackages[m[1]]
		if !ok {
			continue
		}
		if decls[dir] == nil {
			decls[dir] = exportedDecls(t, dir)
		}
		checked++
		if !decls[dir][m[2]] {
			t.Errorf("SPEC §37 names %s.%s, which %s does not declare", m[1], m[2], dir)
		}
	}
	if checked < 30 {
		t.Errorf("checked only %d identifiers; the pattern no longer matches the section", checked)
	}
	for _, m := range regexp.MustCompile("`github\\.com/ubgo/docsync(/[a-z/]+)?`").FindAllStringSubmatch(section, -1) {
		if _, err := os.Stat(filepath.Join("."+m[1], "go.mod")); err != nil {
			t.Errorf("SPEC §37 names module %s, which has no go.mod", m[0])
		}
	}
}

// exportedDecls is every exported top-level name declared in dir's
// non-test Go files.
func exportedDecls(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files in %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					out[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						out[s.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range s.Names {
							out[n.Name] = true
						}
					}
				}
			}
		}
	}
	return out
}
