package config

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The parsers this test reads, and the receivers their setters assign
// through: c is a Config, w a Workspace.
var (
	parserFiles     = []string{"config.go", "workspace.go"}
	setterReceivers = map[string]bool{"c": true, "w": true}
)

// pendingElsewhere names fields another change in flight wires up. Each
// entry must still be unread here: an entry whose field is now read fails
// the test as stale, so the list shrinks to nothing as those changes land
// instead of quietly exempting a key forever.
var pendingElsewhere = map[string]string{
	"Agents.MCP":         "agents.mcp, wired by ds init --agents",
	"Agents.SessionHook": "agents.session_hook, wired by ds init --agents",
	"Index":              "workspace.index, wired by the cross-repo change",
	"StaleAfterCommits":  "workspace.stale_after_commits, wired by the cross-repo change",
}

// acceptedAndIgnored are top-level tables the parser accepts on purpose and
// nothing reads. [performance] holds targets for the project's own
// performance suite (SPEC §23); listing it here is the reviewed decision.
var acceptedAndIgnored = map[string]bool{"performance": true}

// TestEveryParsedKeyHasAnEffect pins bug 120 against the whole class: a key
// the config parser accepts must be read by something outside this package,
// or be refused through NotImplementedValues. A key parsed into a field
// nobody reads loads cleanly and changes nothing, which is how
// notify.github_issues, [sources.*], env.known and four more went unnoticed.
//
// It reads the parser's source rather than a hand-kept list, so a new key is
// covered the moment its setter is written. The fields come from every
// assignment through c or w inside the apply functions; the readers are
// every non-test Go file of every module in the repository outside config/.
func TestEveryParsedKeyHasAnEffect(t *testing.T) {
	t.Parallel()
	fields, idle := parsedFields(t)
	if len(fields) < 40 {
		t.Fatalf("found only %d parsed fields; the source scan is broken", len(fields))
	}
	for _, key := range idle {
		t.Errorf("config key %q neither sets a field, nor is refused through NotImplementedValues, nor is a table; it is accepted and does nothing", key)
	}
	src := readersOutsideConfig(t)
	viaMethod := readThroughMethods(t, src)
	for _, f := range fields {
		read := isRead(src, f) || viaMethod[f]
		why, pending := pendingElsewhere[f]
		switch {
		case pending && read:
			t.Errorf("%s is read now; delete its pendingElsewhere entry (%s)", f, why)
		case !pending && !read:
			t.Errorf("config field %s is parsed but nothing outside config/ reads it: wire it, or refuse its key through NotImplementedValues", f)
		}
	}
}

// parsedFields returns the field paths the parsers assign ("Include.Mode",
// "Run.Env") and the keys whose setter does nothing recognisable.
func parsedFields(t *testing.T) (fields, idle []string) {
	t.Helper()
	seen := map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range parserFiles {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !(strings.HasPrefix(fn.Name.Name, "apply") || fn.Name.Name == "ParseWorkspace") {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.AssignStmt:
					for _, lhs := range n.Lhs {
						if p := fieldPath(lhs); p != "" && !seen[p] {
							seen[p] = true
							fields = append(fields, p)
						}
					}
				case *ast.KeyValueExpr:
					if lit, ok := n.Key.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if !hasEffect(n.Value) {
							idle = append(idle, strings.Trim(lit.Value, `"`))
						}
					}
				case *ast.CaseClause:
					for _, e := range n.List {
						key := caseKey(e)
						if key != "" && !acceptedAndIgnored[key] && !hasEffect(&ast.BlockStmt{List: n.Body}) {
							idle = append(idle, key)
						}
					}
				}
				return true
			})
		}
	}
	sort.Strings(fields)
	return fields, idle
}

// caseKey is a switch case's key, written as a string or a Key* constant.
func caseKey(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.BasicLit:
		return strings.Trim(e.Value, `"`)
	case *ast.Ident:
		return e.Name
	}
	return ""
}

// hasEffect reports whether a setter assigns a field, refuses the key, or
// is a table whose own keys are checked separately.
func hasEffect(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		switch m := m.(type) {
		case *ast.AssignStmt:
			for _, lhs := range m.Lhs {
				if fieldPath(lhs) != "" {
					found = true
				}
			}
		case *ast.CallExpr:
			switch fn := m.Fun.(type) {
			case *ast.Ident:
				found = found || fn.Name == "notImplemented" || fn.Name == "applyTable"
			case *ast.SelectorExpr:
				found = found || strings.HasPrefix(fn.Sel.Name, "apply")
			}
		case *ast.SelectorExpr:
			// A method value used as a setter, c.applySnapshotNotify.
			found = found || strings.HasPrefix(m.Sel.Name, "apply")
		}
		return !found
	})
	return found
}

// fieldPath turns c.Include.Mode or c.Run.Env[name] into "Include.Mode" or
// "Run.Env"; anything not rooted at a setter receiver is "".
func fieldPath(e ast.Expr) string {
	if ix, ok := e.(*ast.IndexExpr); ok {
		e = ix.X
	}
	var parts []string
	for {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			break
		}
		parts = append([]string{sel.Sel.Name}, parts...)
		e = sel.X
	}
	id, ok := e.(*ast.Ident)
	if !ok || !setterReceivers[id.Name] || len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ".")
}

// readThroughMethods returns the fields read by exported methods on Config
// that code outside this package calls: c.IDConfig() reads [id] on behalf
// of its caller, so a call to it is a read of those fields.
func readThroughMethods(t *testing.T, src string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range parserFiles {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			// Validate checks a value; checking it is not acting on it.
			if !ok || fn.Recv == nil || !fn.Name.IsExported() || fn.Name.Name == "Validate" || !strings.Contains(src, "."+fn.Name.Name+"(") {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if e, ok := n.(ast.Expr); ok {
					if p := fieldPath(e); p != "" {
						out[p] = true
					}
				}
				return true
			})
		}
	}
	return out
}

// readersOutsideConfig is the text of every non-test Go file in the
// repository outside this package. Generated and fixture trees are skipped.
func readersOutsideConfig(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules" || path == filepath.Join(root, "config")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		b.Write(raw)
		b.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// isRead looks for a field path used as a selector. A one-segment path is
// too common a word on its own (Name, Index), so it must hang off a value
// that is plainly a config or workspace.
//
// A table handed on whole (s := cfg.Notify.Snapshot, then s.Enabled) counts
// when the table itself is taken as a value and its last segment is read
// somewhere; a table only ever reached through its other fields does not, so
// cfg.Resolve.Providers being read says nothing about Resolve.Enabled.
func isRead(src, path string) bool {
	if regexp.MustCompile(selector(path) + `\b`).MatchString(src) {
		return true
	}
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return false
	}
	parent, last := path[:i], path[i+1:]
	wholeParent := regexp.MustCompile(selector(parent) + `([^.\w]|$)`).MatchString(src)
	return wholeParent && regexp.MustCompile(`\.`+last+`\b`).MatchString(src)
}

// selector is the pattern for path used after a dot.
func selector(path string) string {
	if !strings.Contains(path, ".") {
		return `\b(cfg|Cfg|ws|WS|Config\(\))\.` + regexp.QuoteMeta(path)
	}
	return `\.` + regexp.QuoteMeta(path)
}

// TestNotImplementedKeysAreRefused pins the other half of bug 120: every key
// in NotImplementedValues is refused when a file sets it, with an error
// naming the key and what to use instead -- never accepted and ignored.
// promise:config-unbuilt-refused
func TestNotImplementedKeysAreRefused(t *testing.T) {
	t.Parallel()
	const scan = "[scan]\ndocs = [\"d\"]\n"
	const ws = "[workspace]\nname = \"n\"\nrepos = [\"a\"]\n"
	files := map[string]func() error{
		KeyNotifyGitHubIssues: func() error {
			_, err := Parse(strings.NewReader(scan + "[notify]\ngithub_issues = true\n"))
			return err
		},
		KeySources: func() error { _, err := Parse(strings.NewReader(scan + "[sources.sql]\ndsn = \"x\"\n")); return err },
		KeyWorkspaceID: func() error {
			_, err := ParseWorkspace(strings.NewReader(ws + "[workspace.id]\nsuffix_length = 8\n"))
			return err
		},
		KeyWorkspaceEnv: func() error {
			_, err := ParseWorkspace(strings.NewReader(ws + "[workspace.env]\ndefault = \"prod\"\n"))
			return err
		},
	}
	for key, instead := range NotImplementedValues {
		load, ok := files[key]
		if !ok {
			t.Errorf("%s is in NotImplementedValues but this test sets no file for it", key)
			continue
		}
		err := load()
		if !errors.Is(err, ErrNotImplemented) || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), instead) {
			t.Errorf("%s: err = %v, want ErrNotImplemented naming the key and %q", key, err, instead)
		}
	}
	if len(files) != len(NotImplementedValues) {
		t.Errorf("the test sets %d keys, NotImplementedValues has %d", len(files), len(NotImplementedValues))
	}
}

// TestKnownEnv pins the one rule every environment name is held to (bug
// 120): an empty env.known allows any name, a set one only its members.
func TestKnownEnv(t *testing.T) {
	t.Parallel()
	c := Default()
	if !c.KnownEnv("anything") {
		t.Error("an empty env.known must allow any name")
	}
	c.Env.Known = []string{"prod"}
	if !c.KnownEnv("prod") || c.KnownEnv("prdo") {
		t.Error("a set env.known must allow exactly its members")
	}
}

// TestIDConfigDefaults pins that an [id] left empty, as a config built in
// code leaves it, means the defaults and is valid (bug 121).
func TestIDConfigDefaults(t *testing.T) {
	t.Parallel()
	var c Config
	if ic := c.IDConfig(); ic.SuffixLength != DefaultSuffixLength || ic.Alphabet != DefaultSuffixAlphabet {
		t.Errorf("empty [id] = %+v, want the defaults", ic)
	}
	if err := c.validateID(); err != nil {
		t.Errorf("empty [id] = %v, want valid", err)
	}
}
