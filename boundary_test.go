package docsync

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// forbiddenImports are the packages through which library code could write a
// file, read the environment or $HOME, or open the network. The repository's
// rule is that the library does none of these: operations that would write
// return an Edit, and git, providers and URLs come in as functional options.
// Forbidding "os" outright covers every file write and every environment read,
// since each goes through it. net/url is absent on purpose: it parses strings
// and does no I/O.
var forbiddenImports = map[string]string{
	"os":        "writes files and reads the environment",
	"syscall":   "reaches the operating system directly",
	"io/ioutil": "writes files",
	"net":       "opens connections",
	"net/http":  "opens the network",
	"net/rpc":   "opens the network",
	"net/smtp":  "opens the network",
	"os/signal": "handles process signals",
	"os/user":   "reads the user's account and home",
	"plugin":    "loads code at run time",
	"unsafe":    "escapes the type system",
}

// modulePath is this module's own import path: the only non-standard-library
// prefix the root may import, because its packages are the root's own.
const modulePath = "github.com/ubgo/docsync"

// execAllowedIn is the one library package that may start a process: it is
// the protocol for the ds-resolve-* plugins, which the CLI runs only behind
// --resolve and never on a fork's pull request.
const execAllowedIn = "procplugin"

// TestLibraryStaysPure pins the library's boundary: it never writes a file,
// reads the environment or $HOME, or opens the network (repo CLAUDE.md, spec
// § Library). A boundary kept by good intentions decays at the first
// convenient import, so this reads every library source file's imports with
// the Go parser -- not a line matcher, which a multi-line import block defeats
// -- and fails on any forbidden one. Sub-modules (cli/, ext/…) are separate Go
// modules with their own rules and are skipped wherever a go.mod appears.
// promise:library-no-writes promise:library-stdlib promise:library-no-network
func TestLibraryStaysPure(t *testing.T) {
	t.Parallel()
	checked := 0
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." {
				if strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		checked++
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if why, bad := forbiddenImports[path]; bad {
				t.Errorf("%s imports %q, which %s; the library must not", p, path, why)
			}
			// The standard library's paths have no dot in their first element;
			// every module path does. So a dotted first element that is not
			// this module is a dependency, which the root never takes.
			if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") && path != modulePath && !strings.HasPrefix(path, modulePath+"/") {
				t.Errorf("%s imports %q; the root module imports the standard library only", p, path)
			}
			if path == "os/exec" && filepath.Base(filepath.Dir(p)) != execAllowedIn {
				t.Errorf("%s imports os/exec; only %s may start a process", p, execAllowedIn)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// And go.mod requires nothing, which is the same rule stated to the
	// toolchain: a require here is a dependency every importer inherits.
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(mod), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "require") {
			t.Errorf("go.mod has %q; the root module imports the standard library only", strings.TrimSpace(line))
		}
	}
	// A walk that found nothing would pass on nothing.
	if checked < 20 {
		t.Fatalf("checked only %d library files; the walk is not reaching the source", checked)
	}
}
