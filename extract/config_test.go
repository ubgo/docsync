package extract

import (
	"errors"
	"slices"
	"testing"

	"github.com/ubgo/docsync/block"
)

func defsByID(f Found) map[string]Def {
	m := map[string]Def{}
	for _, d := range f.Defs {
		m[d.Block.ID] = d
	}
	return m
}

func TestConfigYAML(t *testing.T) {
	t.Parallel()
	src := "" +
		"# top comment\n" +
		"auth:\n" +
		"  port: 8081             # ds:def id=auth-port-h3v8n2wd\n" +
		"  # ds:def id=ttl-aaaaaaaa owner=@x\n" +
		"  session_ttl_days: 30\n" +
		"  nested:\n" +
		"    deep: yes # ds:def id=deep-bbbbbbbb\n" +
		"other: 1 # ds:def id=other-cccccccc\n" +
		"# ds:def id=multi-dddddddd span=+2\n" +
		"script:\n" +
		"  - echo one\n" +
		"  - echo two\n" +
		"# ds:block id=auth-port-h3v8n2wd\n" +
		"# ds:def id=tail-eeeeeeee\n"
	f := Config{}.Extract("config/auth.yaml", []byte(src), "ds")
	d := defsByID(f)
	cases := map[string]struct {
		symbol     string
		start, end int
		content    string
	}{
		"auth-port-h3v8n2wd": {"auth.port", 3, 3, "  port: 8081             "},
		"ttl-aaaaaaaa":       {"auth.session_ttl_days", 5, 5, "  session_ttl_days: 30"},
		"deep-bbbbbbbb":      {"auth.nested.deep", 7, 7, "    deep: yes "},
		"other-cccccccc":     {"other", 8, 8, "other: 1 "},
		"multi-dddddddd":     {"script", 10, 12, "script:\n  - echo one\n  - echo two"},
	}
	if len(d) != len(cases) {
		t.Fatalf("defs = %v", d)
	}
	for id, w := range cases {
		got, ok := d[id]
		if !ok {
			t.Errorf("missing %s", id)
			continue
		}
		if got.Block.Symbol != w.symbol || got.Block.Pos.Start != w.start || got.Block.Pos.End != w.end || got.Block.Content != w.content || got.Block.Kind != block.KindKey {
			t.Errorf("%s = %q %d-%d %q %s", id, got.Block.Symbol, got.Block.Pos.Start, got.Block.Pos.End, got.Block.Content, got.Block.Kind)
		}
	}
	if len(f.Refs) != 1 || f.Refs[0].Reference.Verb != "block" {
		t.Errorf("refs = %+v", f.Refs)
	}
	if len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, ErrNothingToBind) {
		t.Errorf("problems = %+v", f.Problems)
	}
}

func TestConfigTOMLINIEnv(t *testing.T) {
	t.Parallel()
	toml := "title = \"x\" # ds:def id=t-aaaaaaaa\n[server]\nport = 8081 # ds:def id=p-bbbbbbbb\n# ds:def id=h-cccccccc\nhost = \"h\"\n"
	f := Config{}.Extract("app.toml", []byte(toml), "ds")
	d := defsByID(f)
	if d["t-aaaaaaaa"].Block.Symbol != "title" || d["p-bbbbbbbb"].Block.Symbol != "server.port" || d["h-cccccccc"].Block.Symbol != "server.host" {
		t.Errorf("toml symbols: %v %v %v", d["t-aaaaaaaa"].Block.Symbol, d["p-bbbbbbbb"].Block.Symbol, d["h-cccccccc"].Block.Symbol)
	}
	ini := "top=1 ; ds:def id=top-aaaaaaaa\n[auth]\nport = 8081 # ds:def id=port-bbbbbbbb\nname: x ; ds:def id=name-cccccccc\n"
	f = Config{}.Extract("app.ini", []byte(ini), "ds")
	d = defsByID(f)
	if d["top-aaaaaaaa"].Block.Symbol != "top" || d["port-bbbbbbbb"].Block.Symbol != "auth.port" || d["name-cccccccc"].Block.Symbol != "auth.name" {
		t.Errorf("ini symbols: %+v", d)
	}
	env := "export DATABASE_URL=postgres://x # ds:def id=db-aaaaaaaa secret=true\nSTRIPE_KEY=op://v/i/f # ds:def id=sk-bbbbbbbb\n# ds:def id=nokey-cccccccc\n[weird line]\n"
	f = Config{}.Extract(".env.tpl", []byte(env), "ds")
	d = defsByID(f)
	if d["db-aaaaaaaa"].Block.Symbol != "DATABASE_URL" || !d["db-aaaaaaaa"].Block.IsSecret() || d["sk-bbbbbbbb"].Block.Symbol != "STRIPE_KEY" {
		t.Errorf("env symbols: %+v", d)
	}
	if nk := d["nokey-cccccccc"]; nk.Block.Kind != block.KindLine || nk.Block.Symbol != "" {
		t.Errorf("line without key must be KindLine with empty symbol: %+v", nk.Block)
	}
}

func TestConfigEdgeCases(t *testing.T) {
	t.Parallel()
	// Remote def, bad span, missing id, and a file with unknown extension that
	// still matches via the .env prefix and falls back to `#` style.
	src := "# ds:def id=r-aaaaaaaa file=app.json pick=json:$.port\nA=1 # ds:def id=b-bbbbbbbb span=3\nB=2 # ds:def\nC=3 # ds:def id=c-cccccccc span=+5\n"
	f := Config{}.Extract("deploy/.env.production", []byte(src), "ds")
	d := defsByID(f)
	if !d["r-aaaaaaaa"].Remote {
		t.Error("remote def not flagged")
	}
	if c := d["c-cccccccc"]; c.Block.Pos.End != 4 || c.Block.Content != "C=3 " {
		t.Errorf("span clamped to file end: %+v", c.Block)
	}
	if len(f.Problems) != 2 {
		t.Errorf("problems = %+v", f.Problems)
	}
	if !(Config{}).Match("x/.env") || (Config{}).Match("x/env.txt") || (Config{}).Name() != "config" {
		t.Error("config Match/Name")
	}
	if !isYAMLPath("Taskfile.yml") || isYAMLPath("a.toml") {
		t.Error("isYAMLPath")
	}
}

func TestKeyOf(t *testing.T) {
	t.Parallel()
	// The same behaviour in every format, and so checked with yaml both ways.
	for in, want := range map[string]string{
		"port: 8081": "port", "  key = v": "key", "KEY=v": "KEY", "export K=v": "K", `"quoted": 1`: "quoted",
		"# comment": "", "; c": "", "[section]": "", "- item": "", "": "", "no separator": "", "two words: x": "", ":lead": "",
		// A quoted key keeps its colons and dots; an unterminated quote, or a
		// quote with nothing after it, is not a key.
		`"a:b.c": 1`: "a:b.c", `'x': 2`: "x", `"open: 1`: "", `"k" trailing`: "",
	} {
		for _, yaml := range []bool{false, true} {
			if got := keyOf(in, yaml); got != want {
				t.Errorf("keyOf(%q, yaml=%v) = %q, want %q", in, yaml, got, want)
			}
		}
	}
	// Where the formats differ. A YAML key ends at a colon followed by space
	// or the end of the line, so `wfsys:up:` names `wfsys:up` -- the shape of
	// every namespaced Taskfile task, all of which were unaddressable. A Java
	// properties file writes `key:value` with no space, so outside YAML the
	// first colon still ends the key.
	for _, tc := range []struct {
		in   string
		yaml bool
		want string
	}{
		{"wfsys:up:", true, "wfsys:up"},
		{"  docs:check:", true, "docs:check"},
		{"dev:portless: x", true, "dev:portless"},
		{"url: http://x:8080", true, "url"},
		{"time: 12:30", true, "time"},
		{"plain:value", true, ""},
		{"key:value", false, "key"},
		{"wfsys:up:", false, "wfsys"},
	} {
		if got := keyOf(tc.in, tc.yaml); got != tc.want {
			t.Errorf("keyOf(%q, yaml=%v) = %q, want %q", tc.in, tc.yaml, got, tc.want)
		}
	}
}

// TestKeyPathSegments pins the path spelling and its inverse, which must agree
// or a key could be written in one form and never found again.
// promise:key-path-segments
func TestKeyPathSegments(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][]string{
		"tasks.wfsys:up":   {"tasks", "wfsys:up"},
		`tasks."wfsys:up"`: {"tasks", "wfsys:up"},
		`tasks."a.b".desc`: {"tasks", "a.b", "desc"},
		`tasks.'a.b'`:      {"tasks", "a.b"},
		"single":           {"single"},
		`"only.one"`:       {"only.one"},
	} {
		if got := splitKeyPath(in); !slices.Equal(got, want) {
			t.Errorf("splitKeyPath(%q) = %q, want %q", in, got, want)
		}
	}
	// A segment is quoted only when it has to be, so an ordinary path reads
	// as it always did and one holding a dot still round-trips.
	for k, want := range map[string]string{"port": "port", "wfsys:up": "wfsys:up", "a.b": `"a.b"`, `x"y`: `"x"y"`} {
		if got := quoteSegment(k); got != want {
			t.Errorf("quoteSegment(%q) = %q, want %q", k, got, want)
		}
	}
	for _, k := range []string{"port", "wfsys:up", "a.b"} {
		if got := splitKeyPath(quoteSegment(k)); len(got) != 1 || got[0] != k {
			t.Errorf("round trip of %q = %q", k, got)
		}
	}
}
