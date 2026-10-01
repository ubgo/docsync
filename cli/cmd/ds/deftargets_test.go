package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync"
)

// defSources is one file per tier the binary ships, holding the shapes a doc
// names by path: members, module constants, object paths, HCL blocks, TOML
// tables, shell functions.
var defSources = fstest.MapFS{
	"a.py":           {Data: []byte("TIMEOUT = 30\n\n\nclass Config:\n    retries = 3\n\n    def run(self):\n        return 1\n")},
	"b.ts":           {Data: []byte("export const config = {\n  server: {\n    port: 5173,\n  },\n};\n\nexport class Store {\n  save(): number {\n    return 1;\n  }\n}\n")},
	"c.tf":           {Data: []byte("variable \"region\" {\n  default = \"us-east-1\"\n}\n")},
	"d.toml":         {Data: []byte("[server]\nport = 8080\n\n[db]\nhost = \"x\"\n")},
	"e.sh":           {Data: []byte("#!/bin/sh\ndeploy() {\n  echo hi\n}\n")},
	"f.go":           {Data: []byte("package f\n\ntype Limits struct {\n\tMinLength int\n\tMaxLength int\n}\n")},
	"app.properties": {Data: []byte("db.host=localhost\ndb.port=5432\n")},
	"app.ini":        {Data: []byte("[server]\nport = 8080\n")},
	"g.txt":          {Data: []byte("one\ntwo\nthree\nfour\n\nfive\n")},
	"i.mts":          {Data: []byte("export const a = 1;\n")},
	"j.cjs":          {Data: []byte("module.exports = { a: 1 };\n")},
	"k.pyi":          {Data: []byte("X: int\n")},
	"h.tfvars":       {Data: []byte("region = \"eu\"\n")},
}

func defSystem(t *testing.T) *docsync.System {
	t.Helper()
	s, err := docsync.New(docsync.WithFS(defSources), docsync.WithRegistry(standardRegistry()))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestDefineResolvesThroughTheScanningTier is bugs 41, 42 and 48: `ds def
// path#Name` resolved names with a line matcher, so every name the grammar
// tiers record -- a Python class attribute, a TypeScript object path, an HCL
// block, a TOML table, a POSIX shell function, a Go struct field by its
// holder -- was refused although a scan recorded it. Each target here must
// resolve, at the line the scan binds, and the new def must carry the symbol
// the ledger will show.
func TestDefineResolvesThroughTheScanningTier(t *testing.T) {
	t.Parallel()
	s := defSystem(t)
	for target, want := range map[string]struct {
		symbol string
		line   int
	}{
		"a.py#TIMEOUT":           {"TIMEOUT", 1},
		"a.py#Config.retries":    {"Config.retries", 5},
		"a.py#retries":           {"Config.retries", 5},
		"a.py#Config.run":        {"Config.run", 7},
		"b.ts#server.port":       {"server.port", 3},
		"b.ts#Store.save":        {"Store.save", 8},
		"c.tf#variable.region":   {"variable.region", 1},
		"d.toml#server":          {"server", 1},
		"d.toml#db.host":         {"db.host", 5},
		"e.sh#deploy":            {"deploy", 2},
		"f.go#Limits.MinLength":  {"Limits.MinLength", 4},
		"f.go#MinLength":         {"Limits.MinLength", 4},
		"app.properties#db.port": {`"db.port"`, 2},
	} {
		res, err := s.Define(context.Background(), target, docsync.DefineOptions{})
		if err != nil {
			t.Errorf("%s: %v", target, err)
			continue
		}
		if res.Block.Symbol != want.symbol || res.Block.Pos.Start != want.line {
			t.Errorf("%s = %q at %d, want %q at %d", target, res.Block.Symbol, res.Block.Pos.Start, want.symbol, want.line)
		}
	}
	if _, err := s.Define(context.Background(), "a.py#nothere", docsync.DefineOptions{}); err == nil || !strings.Contains(err.Error(), "python tier names no block") {
		t.Errorf("a name no tier records must be refused, naming the tier: %v", err)
	}
}

// TestDefineKeyValueWritesAbove is bug 40: a trailing comment is part of the
// value to Java properties, configparser and EditorConfig, and `ds def` wrote
// `db.port=5432   # ds:def id=…` -- the port became that whole string. The
// directive goes on the line above wherever trailing comments are not
// comments; YAML and TOML keep the trailing form, which their parsers read.
func TestDefineKeyValueWritesAbove(t *testing.T) {
	t.Parallel()
	s := defSystem(t)
	for target, trailing := range map[string]bool{
		"app.properties#db.port": false,
		"app.properties:1":       false,
		"app.ini#server.port":    false,
		"d.toml#db.host":         true,
	} {
		res, err := s.Define(context.Background(), target, docsync.DefineOptions{})
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if got := res.Edit.Old != ""; got != trailing {
			t.Errorf("%s: trailing = %v, want %v (edit %+v)", target, got, trailing, res.Edit)
		}
		if !trailing && !strings.HasPrefix(res.Edit.New, "# ds:def id=") {
			t.Errorf("%s: directive above must be a whole-line comment: %q", target, res.Edit.New)
		}
	}
}

// TestDefineLineRange is bug 43 (promise:def-range-exact): `path:3-5` was read as `path:3`, silently.
// A range is bound exactly, with the span= the tier needs, or refused.
func TestDefineLineRange(t *testing.T) {
	t.Parallel()
	s := defSystem(t)
	res, err := s.Define(context.Background(), "g.txt:1-3", docsync.DefineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Block.Pos.Start != 1 || res.Block.Pos.End != 3 || !strings.Contains(res.Edit.New, "span=+3") {
		t.Errorf("g.txt:1-3 = %+v / %q", res.Block.Pos, res.Edit.New)
	}
	// The natural block needs no span.
	if res, err := s.Define(context.Background(), "g.txt:1-4", docsync.DefineOptions{}); err != nil || strings.Contains(res.Edit.New, "span=") {
		t.Errorf("g.txt:1-4 = %q, %v", res.Edit.New, err)
	}
	// A grammar tier counts from the bound line.
	if res, err := s.Define(context.Background(), "b.ts:7-9", docsync.DefineOptions{}); err != nil || !strings.Contains(res.Edit.New, "span=+2") {
		t.Errorf("b.ts:7-9 = %q, %v", res.Edit.New, err)
	}
	if _, err := s.Define(context.Background(), "g.txt:2-9", docsync.DefineOptions{}); err == nil {
		t.Error("a range past the end of the file must be refused")
	}
	// Line 2 is blank, so a def there binds the class below it: no span starts at 2.
	if _, err := s.Define(context.Background(), "a.py:2-3", docsync.DefineOptions{}); !errors.Is(err, docsync.ErrRange) {
		t.Errorf("a range no span can bind must be ErrRange: %v", err)
	}
}

// TestDefineNewCarrierTypes is bug 46: .mts, .cts, .cjs and .pyi are read by
// a grammar tier but had no comment style, so directives in them were never
// read and `ds def` refused them; .tfvars the same for HCL.
func TestDefineNewCarrierTypes(t *testing.T) {
	t.Parallel()
	s := defSystem(t)
	for target, prefix := range map[string]string{"i.mts:1": "// ds:def", "j.cjs:1": "// ds:def", "k.pyi:1": "# ds:def", "h.tfvars:1": "# ds:def"} {
		res, err := s.Define(context.Background(), target, docsync.DefineOptions{})
		if err != nil || !strings.HasPrefix(res.Edit.New, prefix) {
			t.Errorf("%s = %q, %v", target, res.Edit.New, err)
		}
	}
}
