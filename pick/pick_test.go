package pick

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in      string
		want    Expr
		wantErr error
	}{
		{"line:3", Expr{SchemeLine, "3"}, nil},
		{" regex:'v(\\d+)' ", Expr{SchemeRegex, `v(\d+)`}, nil},
		{`after:"Hosted at "`, Expr{SchemeAfter, "Hosted at "}, nil},
		{"url", Expr{SchemeURL, ""}, nil},
		{"file", Expr{SchemeFile, ""}, nil},
		{"json:$.a.b", Expr{SchemeJSON, "$.a.b"}, nil},
		{"csv:col=region", Expr{SchemeCSV, "col=region"}, nil},
		{"section:\"Session policy\"", Expr{SchemeSection, "Session policy"}, nil},
		{"regex:'unbalanced", Expr{SchemeRegex, "'unbalanced"}, nil},
		{"", Expr{}, ErrBadExpr},
		{"nope:1", Expr{}, ErrBadExpr},
		{":1", Expr{}, ErrBadExpr},
	} {
		got, err := Parse(tc.in)
		if tc.wantErr != nil {
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Parse(%q) err = %v, want %v", tc.in, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("Parse(%q) = %#v, %v; want %#v", tc.in, got, err, tc.want)
		}
	}
}

const mdDoc = "---\ntitle: x\n---\n\n# Title\n\nFirst para line one\nline two.\n\n## Session policy\n\nSessions live [30 days](ds:def?id=a) and rotate. See https://example.com/docs, ok.\n\n```go\n# not a heading\ncode\n```\n\n### Sub\n\nDeep text.\n\n## Other\n\nLast para with [two](x) [links](y).\n"

func TestTextAndDocumentSchemes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, expr, content string
		want                Result
		wantErr             error
	}{
		{"line", "line:2", "a\nb\nc", Result{KindValue, "b", "", 2, 2}, nil},
		{"line out of range", "line:9", "a\nb", Result{}, ErrNotFound},
		{"line zero", "line:0", "a", Result{}, ErrBadExpr},
		{"line nan", "line:x", "a", Result{}, ErrBadExpr},
		{"regex group", `regex:'go (\d\.\d+)'`, "module m\ngo 1.26\n", Result{KindValue, "1.26", "", 2, 2}, nil},
		{"regex whole match", "regex:8[0-9]+", "port: 8081", Result{KindValue, "8081", "", 1, 1}, nil},
		{"regex no match", "regex:zzz", "a", Result{}, ErrNotFound},
		{"regex empty", "regex:", "a", Result{}, ErrBadExpr},
		{"regex invalid", "regex:(", "a", Result{}, ErrBadExpr},
		{"url first", "url", "see https://a.example.com/x, and http://b.example.com", Result{KindValue, "https://a.example.com/x", "", 1, 1}, nil},
		{"url trailing punctuation dropped", "url", "Hosted at https://example.com.", Result{KindValue, "https://example.com", "", 1, 1}, nil},
		{"url in parens", "url", "(https://example.com/a) yes", Result{KindValue, "https://example.com/a", "", 1, 1}, nil},
		{"url none", "url", "no links here", Result{}, ErrNotFound},
		{"after", "after:'Hosted at '", "The app is Hosted at https://x.dev behind", Result{KindValue, "https://x.dev behind", "", 1, 1}, nil},
		{"after missing", "after:zz", "a", Result{}, ErrNotFound},
		{"after empty", "after:", "a", Result{}, ErrBadExpr},
		{"between", "between:'<','>'", "port <8081> ok", Result{KindValue, "8081", "", 1, 1}, nil},
		{"between unquoted", "between:[,]", "x [y] z", Result{KindValue, "y", "", 1, 1}, nil},
		{"between skips lines without the first marker", "between:'<','>'", "nothing here\nport <8081> ok", Result{KindValue, "8081", "", 2, 2}, nil},
		{"between second marker missing", "between:'<','>'", "port <8081 ok", Result{}, ErrNotFound},
		{"between bad arg", "between:'<'", "a", Result{}, ErrBadExpr},
		{"between empty marker", "between:'',''", "a", Result{}, ErrBadExpr},
		{"heading", "heading", mdDoc, Result{KindValue, "Title", "", 5, 5}, nil},
		{"heading none", "heading", "plain\ntext", Result{}, ErrNotFound},
		{"section until same level", "section:Session policy", mdDoc, Result{KindRange, "", "## Session policy\n\nSessions live [30 days](ds:def?id=a) and rotate. See https://example.com/docs, ok.\n\n```go\n# not a heading\ncode\n```\n\n### Sub\n\nDeep text.\n", 10, 22}, nil},
		{"section to end of file", "section:Other", mdDoc, Result{KindRange, "", "## Other\n\nLast para with [two](x) [links](y).", 23, 25}, nil},
		{"section missing", "section:Nope", mdDoc, Result{}, ErrNotFound},
		{"section empty arg", "section:", mdDoc, Result{}, ErrBadExpr},
		{"paragraph 1 skips frontmatter-ish lines as paragraphs", "paragraph:2", mdDoc, Result{KindRange, "", "First para line one\nline two.", 7, 8}, nil},
		{"paragraph skips fences", "paragraph:4", mdDoc, Result{KindRange, "", "Deep text.", 21, 21}, nil},
		{"paragraph too many", "paragraph:99", mdDoc, Result{}, ErrNotFound},
		{"paragraph bad", "paragraph:0", mdDoc, Result{}, ErrBadExpr},
		{"link 1", "link:1", mdDoc, Result{KindValue, "30 days", "", 12, 12}, nil},
		{"link 3", "link:3", mdDoc, Result{KindValue, "links", "", 25, 25}, nil},
		{"section whose heading is inside a fence is not found", "section:not a heading", mdDoc, Result{}, ErrNotFound},
		{"tilde fence also skipped", "section:A", "## A\n~~~\n# x\n~~~\n## B\n", Result{KindRange, "", "## A\n~~~\n# x\n~~~", 1, 4}, nil},
		{"link none", "link:9", mdDoc, Result{}, ErrNotFound},
		{"link bad", "link:x", mdDoc, Result{}, ErrBadExpr},
		{"file", "file", "a\nb\n", Result{KindRange, "", "a\nb", 1, 2}, nil},
		{"file empty", "file", "", Result{KindRange, "", "", 1, 0}, nil},
		{"symbol unsupported", "symbol:Store.Save", "x", Result{}, ErrUnsupported},
		{"bad expr surfaces from Pick", "nope:1", "x", Result{}, ErrBadExpr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Pick(tc.expr, tc.content)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Pick(%q) err = %v, want %v", tc.expr, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Pick(%q): %v", tc.expr, err)
			}
			if got != tc.want {
				t.Errorf("Pick(%q) = %#v, want %#v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestStructuredSchemes(t *testing.T) {
	t.Parallel()
	jsonDoc := `{"server":{"host":"example.com","port":8081,"tls":true,"tags":["a","b"],"nested":{"x":1}},"n":null}`
	envDoc := "# comment\nexport DATABASE_URL='postgres://x'\nPORT=8081 # inline\nQUOTED=\"a b\"\nEMPTY=\n"
	iniDoc := "top=1\n; c\n[auth]\nport = 8081 ; trailing\nhost: example.com\n[other]\nport=9\n"
	csvDoc := "region,zone\nus-east,a\neu-west,b\n"
	yamlDoc := "# top\nauth:\n  port: 8081   # comment\n  host: \"example.com\"\n  nested:\n    deep: yes\n  block: |\n    text\nother: 1\n"
	tomlDoc := "title = \"x\"\n[server]\nport = 8081 # c\nhost = 'h'\n[server.tls]\nenabled = true\n"
	for _, tc := range []struct {
		name, expr, content string
		want                Result
		wantErr             error
	}{
		{"json string", "json:$.server.host", jsonDoc, Result{KindValue, "example.com", "", 1, 1}, nil},
		{"json number", "json:server.port", jsonDoc, Result{KindValue, "8081", "", 1, 1}, nil},
		{"json bool", "json:server.tls", jsonDoc, Result{KindValue, "true", "", 1, 1}, nil},
		{"json null", "json:n", jsonDoc, Result{KindValue, "null", "", 1, 1}, nil},
		{"json array index", "json:server.tags[1]", jsonDoc, Result{KindValue, "b", "", 1, 1}, nil},
		{"json object is range", "json:server.nested", jsonDoc, Result{KindRange, "", "{\n  \"x\": 1\n}", 1, 1}, nil},
		{"json root is range", "json:", `{"a":1}`, Result{KindRange, "", "{\n  \"a\": 1\n}", 1, 1}, nil},
		{"json missing key", "json:server.nope", jsonDoc, Result{}, ErrNotFound},
		{"json not object", "json:server.host.x", jsonDoc, Result{}, ErrNotFound},
		{"json bad index", "json:server.tags[9]", jsonDoc, Result{}, ErrNotFound},
		{"json index on non array", "json:server[0]", jsonDoc, Result{}, ErrNotFound},
		{"json malformed index", "json:server.tags[x]", jsonDoc, Result{}, ErrBadExpr},
		{"json unterminated index", "json:server.tags[1", jsonDoc, Result{}, ErrBadExpr},
		{"json invalid doc", "json:a", "{", Result{}, ErrNotFound},
		{"env plain with inline comment", "env:PORT", envDoc, Result{KindValue, "8081", "", 3, 3}, nil},
		{"env export and quotes", "env:DATABASE_URL", envDoc, Result{KindValue, "postgres://x", "", 2, 2}, nil},
		{"env double quotes", "env:QUOTED", envDoc, Result{KindValue, "a b", "", 4, 4}, nil},
		{"env empty value", "env:EMPTY", envDoc, Result{KindValue, "", "", 5, 5}, nil},
		{"env missing", "env:NOPE", envDoc, Result{}, ErrNotFound},
		{"env empty arg", "env:", envDoc, Result{}, ErrBadExpr},
		{"ini section key", "ini:auth.port", iniDoc, Result{KindValue, "8081 ; trailing", "", 4, 4}, nil},
		{"ini colon separator", "ini:auth.host", iniDoc, Result{KindValue, "example.com", "", 5, 5}, nil},
		{"ini top level", "ini:top", iniDoc, Result{KindValue, "1", "", 1, 1}, nil},
		{"ini other section same key", "ini:other.port", iniDoc, Result{KindValue, "9", "", 7, 7}, nil},
		{"ini missing", "ini:auth.nope", iniDoc, Result{}, ErrNotFound},
		{"ini empty key", "ini:auth.", iniDoc, Result{}, ErrBadExpr},
		{"csv column range", "csv:col=region", csvDoc, Result{KindRange, "", "us-east\neu-west", 2, 3}, nil},
		{"csv cell", "csv:r2c2", csvDoc, Result{KindValue, "a", "", 2, 2}, nil},
		{"csv missing column", "csv:col=nope", csvDoc, Result{}, ErrNotFound},
		{"csv column no rows", "csv:col=region", "region\n", Result{}, ErrNotFound},
		{"csv cell out of range", "csv:r9c1", csvDoc, Result{}, ErrNotFound},
		{"csv bad arg", "csv:what", csvDoc, Result{}, ErrBadExpr},
		{"csv empty", "csv:r1c1", "", Result{}, ErrNotFound},
		{"csv malformed", "csv:r1c1", "a,\"b\nc", Result{}, ErrNotFound},
		{"yaml nested scalar", "yaml:auth.port", yamlDoc, Result{KindValue, "8081", "", 3, 3}, nil},
		{"yaml quoted", "yaml:auth.host", yamlDoc, Result{KindValue, "example.com", "", 4, 4}, nil},
		{"yaml deeper", "yaml:auth.nested.deep", yamlDoc, Result{KindValue, "yes", "", 6, 6}, nil},
		{"yaml top after dedent", "yaml:other", yamlDoc, Result{KindValue, "1", "", 9, 9}, nil},
		{"yaml map not value", "yaml:auth", yamlDoc, Result{}, ErrMultiLine},
		{"yaml block scalar not value", "yaml:auth.block", yamlDoc, Result{}, ErrMultiLine},
		{"yaml scalar where map expected", "yaml:auth.port.x", yamlDoc, Result{}, ErrNotFound},
		{"yaml missing", "yaml:auth.nope", yamlDoc, Result{}, ErrNotFound},
		{"yaml empty arg", "yaml:", yamlDoc, Result{}, ErrBadExpr},
		{"toml table key", "toml:server.port", tomlDoc, Result{KindValue, "8081", "", 3, 3}, nil},
		{"toml single quoted", "toml:server.host", tomlDoc, Result{KindValue, "h", "", 4, 4}, nil},
		{"toml top level", "toml:title", tomlDoc, Result{KindValue, "x", "", 1, 1}, nil},
		{"toml dotted table", "toml:server.tls.enabled", tomlDoc, Result{KindValue, "true", "", 6, 6}, nil},
		{"toml missing", "toml:server.nope", tomlDoc, Result{}, ErrNotFound},
		{"toml empty arg", "toml:", tomlDoc, Result{}, ErrBadExpr},
		{"toml line without equals ignored", "toml:server.port", "[server]\njunk line\nport = 1\n", Result{KindValue, "1", "", 3, 3}, nil},
		{"toml comments and blanks skipped", "toml:port", "# c\n\nport = 2\n", Result{KindValue, "2", "", 3, 3}, nil},
		{"yaml folded scalar not value", "yaml:a", "a: >-\n  text\n", Result{}, ErrMultiLine},
		{"yaml pipe-like value is fine", "yaml:a", "a: \"|x\"\n", Result{KindValue, "|x", "", 1, 1}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Pick(tc.expr, tc.content)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Pick(%q) err = %v, want %v", tc.expr, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Pick(%q): %v", tc.expr, err)
			}
			if got != tc.want {
				t.Errorf("Pick(%q) = %#v, want %#v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestOneLineRule(t *testing.T) {
	t.Parallel()
	// A regex can capture across nothing but a line, so build a value with a
	// carriage return to prove valueResult refuses it.
	if _, err := Pick(`regex:(a\rb)`, "a\rb"); !errors.Is(err, ErrMultiLine) {
		t.Errorf("multi-line value accepted: %v", err)
	}
}

func TestHelpers(t *testing.T) {
	t.Parallel()
	if got := stripInlineComment(`"a # b" # c`); got != `"a # b" ` {
		t.Errorf("stripInlineComment = %q", got)
	}
	if got := stripInlineComment("#lead"); got != "" {
		t.Errorf("leading hash = %q", got)
	}
	if got := stripInlineComment("a#b"); got != "a#b" {
		t.Errorf("hash without space must stay: %q", got)
	}
	if got := unquote(`  'x'  `); got != "x" {
		t.Errorf("unquote = %q", got)
	}
	if got := unquote(`'x"`); got != `'x"` {
		t.Errorf("mismatched quotes must stay: %q", got)
	}
	if got := splitQuotedList(`'a,b', c ,"d"`); len(got) != 3 || got[0] != "a,b" || got[1] != "c" || got[2] != "d" {
		t.Errorf("splitQuotedList = %q", got)
	}
	if headingLevel("####### seven") != 0 || headingLevel("#nospace") != 0 || headingLevel("#") != 0 || headingLevel("## ok") != 2 {
		t.Error("headingLevel")
	}
	for _, s := range Schemes {
		if !known(s) {
			t.Errorf("scheme %q not known", s)
		}
	}
}

func TestKeyValue(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"port: 8081":                 "8081",
		"  host = \"a.example\" # c": "a.example",
		"KEY='x # y'":                "x # y",
		"k:":                         "",
	} {
		if got, ok := KeyValue(in); !ok || got != want {
			t.Errorf("KeyValue(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "no separator", ": leading", "# comment"} {
		if _, ok := KeyValue(bad); ok {
			t.Errorf("KeyValue(%q) accepted", bad)
		}
	}
}

func TestPickWith(t *testing.T) {
	t.Parallel()
	hcl := func(arg, content string) (Result, error) {
		if arg == "" {
			return Result{}, errors.New("hcl wants a path")
		}
		return Result{Kind: KindValue, Value: "from-plugin:" + arg, Start: 1, End: 1}, nil
	}
	pickers := map[string]Picker{"hcl": hcl, "json": func(string, string) (Result, error) { return Result{Value: "shadow"}, nil }}
	if r, err := PickWith(pickers, "hcl:resource.name", "x"); err != nil || r.Value != "from-plugin:resource.name" {
		t.Errorf("plugin pick = %+v %v", r, err)
	}
	if _, err := PickWith(pickers, "hcl", "x"); err == nil {
		t.Error("plugin error propagates")
	}
	if r, err := PickWith(pickers, "json:a", `{"a": 1}`); err != nil || r.Value != "1" {
		t.Errorf("built-in must not be shadowed = %+v %v", r, err)
	}
	if _, err := PickWith(nil, "nope:x", "x"); err == nil {
		t.Error("unknown scheme without plugins")
	}
	if sc, arg := SplitScheme(" line : 3"); sc != "line" || arg != " 3" {
		t.Errorf("SplitScheme = %q %q", sc, arg)
	}
}
