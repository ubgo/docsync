package cli

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
	"strconv"
	"strings"
	"testing"

	"github.com/ubgo/docsync/config"
)

// A command a message tells the reader to run: `ds <sub>` and the --flags
// after it, up to the end of the command (a backtick, a semicolon, a closing
// parenthesis, or the end of the string).
var (
	messageCommandRE = regexp.MustCompile("(?:^|[\\s`(])ds ((?:[a-z][a-z-]*)(?: [^`;)\\n]*)?)")
	messageFlagRE    = regexp.MustCompile(`(?:^|\s)--([a-z][a-z-]*)`)
	// A config key a message names: a dotted key whose first segment is a
	// config table ("check.snapshot_max_age"), or a table header
	// ("[records]", "[scan.limits]"). A word after a header is prose as often
	// as it is a key ("[records] to render"), so only the header is checked.
	messageDottedKeyRE = regexp.MustCompile(`(?:^|[\s` + "`" + `(])([a-z_]+(?:\.[a-z_]+)+)`)
	messageTableKeyRE  = regexp.MustCompile(`\[([a-z_]+(?:\.[a-z_]+)*)\]`)
)

// fileExtensions end a dotted word that is a file name, not a key
// ("ledger.tsv").
var fileExtensions = map[string]bool{"tsv": true, "json": true, "toml": true, "md": true, "go": true, "yml": true, "yaml": true, "txt": true}

// configTables are the top-level tables of .ds/config.toml; a dotted word
// that does not start with one is not a config key ("internal.go").
var configTables = map[string]bool{"scan": true, "include": true, "check": true, "policy": true, "owners": true, "secret": true, "env": true, "resolve": true, "run": true, "url": true, "records": true, "notify": true, "agents": true, "id": true, "review": true, "ledger": true, "plugins": true, "update": true}

// messageExceptions are matches that look like a command or a key and are
// not one, each with the reason.
var messageExceptions = map[string]string{
	"ds there": "\"run ds there\": the binary run in another directory, not a subcommand",
	"ds that":  "\"the build of ds that is running\": the program, in prose",
	"ds wrote": "\"if a pre-release ds wrote it\": the program, in prose",
}

// TestMessagesNameRealCommandsAndKeys pins that every command,
// flag, and config key that a remedy, error, warning or doctor row names
// must exist, or the message sends its reader to something that is not
// there. It reads every string literal in the non-test Go source of the root
// module and this one, so a new message is covered the moment it is written;
// commands and flags are checked against the real cobra tree and keys
// against the real parser (a key exists when setting it is not ErrUnknown).
func TestMessagesNameRealCommandsAndKeys(t *testing.T) {
	t.Parallel()
	root := (&App{name: DefaultName}).root()
	lits := sourceStrings(t)
	if len(lits) < 500 {
		t.Fatalf("found only %d string literals; the source scan is broken", len(lits))
	}
	commands, keys := 0, 0
	for _, lit := range lits {
		for _, m := range messageCommandRE.FindAllStringSubmatch(lit.text, -1) {
			words := strings.Fields(m[1])
			if _, ok := messageExceptions["ds "+words[0]]; ok {
				continue
			}
			commands++
			cmd, rest, err := root.Find(words)
			if err != nil || cmd == root {
				t.Errorf("%s: %q names `ds %s`, which is not a command", lit.pos, lit.text, words[0])
				continue
			}
			for _, f := range messageFlagRE.FindAllStringSubmatch(strings.Join(rest, " "), -1) {
				if cmd.Flags().Lookup(f[1]) == nil && cmd.InheritedFlags().Lookup(f[1]) == nil && root.PersistentFlags().Lookup(f[1]) == nil {
					t.Errorf("%s: %q names `ds %s --%s`, which is not a flag of %s", lit.pos, lit.text, cmd.Name(), f[1], cmd.CommandPath())
				}
			}
		}
		for _, key := range messageKeys(lit.text) {
			keys++
			if !configKeyExists(key) {
				t.Errorf("%s: %q names config key %s, which the parser does not accept", lit.pos, lit.text, key)
			}
		}
	}
	if commands < 20 || keys < 10 {
		t.Errorf("checked %d commands and %d keys; the patterns no longer match the messages", commands, keys)
	}
}

// messageKeys returns the config keys a message names.
func messageKeys(text string) []string {
	var out []string
	for _, m := range messageDottedKeyRE.FindAllStringSubmatch(text, -1) {
		parts := strings.Split(m[1], ".")
		if configTables[parts[0]] && !fileExtensions[parts[len(parts)-1]] {
			out = append(out, m[1])
		}
	}
	for _, m := range messageTableKeyRE.FindAllStringSubmatch(text, -1) {
		if configTables[strings.SplitN(m[1], ".", 2)[0]] {
			out = append(out, m[1])
		}
	}
	return out
}

// configKeyExists asks the parser: a key is accepted when setting it fails
// for any reason but ErrUnknown (a wrong type is fine; the name was known).
// A key with an open-ended segment, such as an owners team or a run.env
// environment, is tried with a placeholder name.
func configKeyExists(key string) bool {
	parts := strings.Split(key, ".")
	var toml string
	switch {
	case len(parts) == 1:
		toml = "[scan]\ncode = [\"x\"]\n[" + key + "]\n"
	case parts[0] == "scan" && len(parts) == 2:
		toml = "[scan]\ncode = [\"x\"]\n" + parts[1] + " = 1\n"
	default:
		toml = "[scan]\ncode = [\"x\"]\n[" + strings.Join(parts[:len(parts)-1], ".") + "]\n" + parts[len(parts)-1] + " = 1\n"
	}
	_, err := config.Parse(strings.NewReader(toml))
	return !errors.Is(err, config.ErrUnknown)
}

type sourceString struct {
	pos  string
	text string
}

// sourceStrings returns every string literal in the non-test Go files of
// the root module and the cli module, with where it is.
func sourceStrings(t *testing.T) []sourceString {
	t.Helper()
	repo, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var out []sourceString
	fset := token.NewFileSet()
	err = filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// ext/ holds parsers, internal/doctest drives the binary and is
			// not a message to a user; neither is in scope.
			if path != repo && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules" || name == "ext" || name == "doctest" || name == "integrations" || name == "editors") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil {
					rel, _ := filepath.Rel(repo, fset.Position(lit.Pos()).Filename)
					out = append(out, sourceString{pos: rel + ":" + strconv.Itoa(fset.Position(lit.Pos()).Line), text: s})
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pos < out[j].pos })
	return out
}
