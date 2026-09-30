package directive

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// promise:datastore-not-directive
func TestParse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		in      string
		want    Directive
		wantErr error
	}{
		{"def one key", "ds:def id=sess-save-k7m2p4xq", Directive{Verb: "def", Args: map[string]string{"id": "sess-save-k7m2p4xq"}, Keys: []string{"id"}}, nil},
		{"leading and trailing space", "   ds:def id=x   ", Directive{Verb: "def", Args: map[string]string{"id": "x"}, Keys: []string{"id"}}, nil},
		{"verb only", "ds:chain", Directive{Verb: "chain", Args: map[string]string{}}, nil},
		{"verb only trailing space", "ds:chain  ", Directive{Verb: "chain", Args: map[string]string{}}, nil},
		{"many keys keep order", "ds:def id=a owner=@auth stability=api", Directive{Verb: "def", Args: map[string]string{"id": "a", "owner": "@auth", "stability": "api"}, Keys: []string{"id", "owner", "stability"}}, nil},
		{"double quoted value with spaces", `ds:def id=a desc="dual write guard"`, Directive{Verb: "def", Args: map[string]string{"id": "a", "desc": "dual write guard"}, Keys: []string{"id", "desc"}}, nil},
		{"single quoted value with double quotes", `ds:def id=a desc='he said "hi"'`, Directive{Verb: "def", Args: map[string]string{"id": "a", "desc": `he said "hi"`}, Keys: []string{"id", "desc"}}, nil},
		{"double quoted value with single quote", `ds:def desc="it's fine"`, Directive{Verb: "def", Args: map[string]string{"desc": "it's fine"}, Keys: []string{"desc"}}, nil},
		{"empty value", "ds:def id=a empty=", Directive{Verb: "def", Args: map[string]string{"id": "a", "empty": ""}, Keys: []string{"id", "empty"}}, nil},
		{"empty quoted value", `ds:def desc=""`, Directive{Verb: "def", Args: map[string]string{"desc": ""}, Keys: []string{"desc"}}, nil},
		{"value with equals and commas", "ds:run cmd=a=b,c expect=ok", Directive{Verb: "run", Args: map[string]string{"cmd": "a=b,c", "expect": "ok"}, Keys: []string{"cmd", "expect"}}, nil},
		{"regex value in quotes", `ds:def pick='secrets\.(\w+)'`, Directive{Verb: "def", Args: map[string]string{"pick": `secrets\.(\w+)`}, Keys: []string{"pick"}}, nil},
		{"tabs as separators", "ds:def\tid=a\towner=b", Directive{Verb: "def", Args: map[string]string{"id": "a", "owner": "b"}, Keys: []string{"id", "owner"}}, nil},
		{"underscore and digits in verb and key", "ds:my_verb2 some_key9=v", Directive{Verb: "my_verb2", Args: map[string]string{"some_key9": "v"}, Keys: []string{"some_key9"}}, nil},
		{"not a directive plain prose", "ds is the datastore", Directive{}, ErrNoDirective},
		{"not a directive prefix with space", "ds: def id=x", Directive{}, ErrBadVerb},
		{"not a directive other prefix", "tie:def id=x", Directive{}, ErrNoDirective},
		{"empty text", "", Directive{}, ErrNoDirective},
		{"uppercase verb", "ds:Def id=x", Directive{}, ErrBadVerb},
		{"verb followed by equals", "ds:def=1", Directive{}, ErrBadVerb},
		{"verb followed by punctuation", "ds:def,x", Directive{}, ErrBadVerb},
		{"positional after verb", "ds:def sess-save", Directive{}, ErrPositional},
		{"positional between keys", "ds:def id=a bare owner=b", Directive{}, ErrPositional},
		{"uppercase key", "ds:def Id=a", Directive{}, ErrBadKey},
		{"key starting with digit", "ds:def 1id=a", Directive{}, ErrBadKey},
		{"duplicate key", "ds:def id=a id=b", Directive{}, ErrDuplicateKey},
		{"unterminated double quote", `ds:def desc="open`, Directive{}, ErrUnterminatedQuote},
		{"unterminated single quote", `ds:def desc='open`, Directive{}, ErrUnterminatedQuote},
		{"key with dash rejected", "ds:def my-key=a", Directive{}, ErrPositional},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(DefaultPrefix, tc.in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Parse(%q) err = %v, want %v", tc.in, err, tc.wantErr)
				}
				var pe *ParseError
				if !errors.As(err, &pe) {
					t.Fatalf("Parse(%q) err type = %T, want *ParseError", tc.in, err)
				}
				if pe.Column < 0 || pe.Column > len(strings.TrimSpace(tc.in)) {
					t.Errorf("Parse(%q) column %d out of range", tc.in, pe.Column)
				}
				if pe.Error() == "" {
					t.Errorf("empty error string")
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected err %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseCustomPrefix(t *testing.T) {
	t.Parallel()
	d, err := Parse("tie", "tie:def id=x")
	if err != nil || d.Verb != "def" || d.Args["id"] != "x" {
		t.Fatalf("custom prefix parse = %#v, %v", d, err)
	}
	if _, err := Parse("tie", "ds:def id=x"); !errors.Is(err, ErrNoDirective) {
		t.Fatalf("default prefix under custom prefix should be ErrNoDirective, got %v", err)
	}
}

func TestParseErrorColumns(t *testing.T) {
	t.Parallel()
	// Columns are byte offsets into the trimmed text. Pin a few so editors can
	// rely on them.
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"ds:def id=a bare", 12},
		{"ds:def id=a id=b", 12},
		{`ds:def desc="x`, 12},
		{"ds:Def", 3},
		{"nope", 0},
	} {
		_, err := Parse(DefaultPrefix, tc.in)
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Fatalf("%q: want ParseError, got %v", tc.in, err)
		}
		if pe.Column != tc.want {
			t.Errorf("%q: column = %d, want %d", tc.in, pe.Column, tc.want)
		}
	}
}

func TestAccessors(t *testing.T) {
	t.Parallel()
	d, err := Parse(DefaultPrefix, `ds:def id=a tags="go,  backend,," empty= single=x`)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := d.Get("id"); !ok || v != "a" {
		t.Errorf("Get(id) = %q,%v", v, ok)
	}
	if v, ok := d.Get("missing"); ok || v != "" {
		t.Errorf("Get(missing) = %q,%v", v, ok)
	}
	if v, ok := d.Get("empty"); !ok || v != "" {
		t.Errorf("Get(empty) = %q,%v; a present empty key must report ok", v, ok)
	}
	if !d.Has("empty") || d.Has("nope") {
		t.Errorf("Has misreported")
	}
	if got := d.List("tags"); !reflect.DeepEqual(got, []string{"go", "backend"}) {
		t.Errorf("List(tags) = %v", got)
	}
	if got := d.List("single"); !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("List(single) = %v", got)
	}
	if got := d.List("empty"); got != nil {
		t.Errorf("List(empty) = %v, want nil", got)
	}
	if got := d.List("missing"); got != nil {
		t.Errorf("List(missing) = %v, want nil", got)
	}
}

func TestParseLink(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		in      string
		want    Directive
		wantErr error
	}{
		{"id only", "ds:block?id=sess-save-k7m2p4xq", Directive{Verb: "block", Args: map[string]string{"id": "sess-save-k7m2p4xq"}, Keys: []string{"id"}}, nil},
		{"two keys ordered", "ds:block?id=a&lines=1-6", Directive{Verb: "block", Args: map[string]string{"id": "a", "lines": "1-6"}, Keys: []string{"id", "lines"}}, nil},
		{"no query", "ds:chain", Directive{Verb: "chain", Args: map[string]string{}}, nil},
		{"empty query", "ds:chain?", Directive{Verb: "chain", Args: map[string]string{}}, nil},
		{"percent decoded", "ds:cfg?query=sql%3Aselect%20count(*)%20from%20users&ttl=24h", Directive{Verb: "cfg", Args: map[string]string{"query": "sql:select count(*) from users", "ttl": "24h"}, Keys: []string{"query", "ttl"}}, nil},
		{"plus is space", "ds:def?desc=a+b", Directive{Verb: "def", Args: map[string]string{"desc": "a b"}, Keys: []string{"desc"}}, nil},
		{"empty value", "ds:def?empty=", Directive{Verb: "def", Args: map[string]string{"empty": ""}, Keys: []string{"empty"}}, nil},
		{"not ours", "https://example.com", Directive{}, ErrNoDirective},
		{"not ours mailto", "mailto:ds:def", Directive{}, ErrNoDirective},
		{"bad verb", "ds:Block?id=a", Directive{}, ErrBadVerb},
		{"empty verb", "ds:?id=a", Directive{}, ErrBadVerb},
		{"bad key", "ds:block?Id=a", Directive{}, ErrBadKey},
		{"empty key", "ds:block?=a", Directive{}, ErrBadKey},
		{"positional", "ds:block?a", Directive{}, ErrPositional},
		{"duplicate", "ds:block?id=a&id=b", Directive{}, ErrDuplicateKey},
		{"empty pair", "ds:block?id=a&&x=1", Directive{}, ErrBadLink},
		{"bad escape", "ds:block?id=%zz", Directive{}, ErrBadLink},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseLink(DefaultPrefix, tc.in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("ParseLink(%q) err = %v, want %v", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLink(%q) unexpected err %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseLink(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestFold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		in           []string
		wantJoined   string
		wantConsumed int
	}{
		{"empty", nil, "", 0},
		{"single", []string{"ds:def id=a"}, "ds:def id=a", 1},
		{"two continuations", []string{"ds:def id=a", "  owner=@auth tags=x", "   desc=\"d e\""}, "ds:def id=a owner=@auth tags=x desc=\"d e\"", 3},
		{"stops at prose", []string{"ds:def id=a", "  owner=@auth", " this is prose", "  more=1"}, "ds:def id=a owner=@auth", 2},
		{"stops at unindented key", []string{"ds:def id=a", "owner=@auth"}, "ds:def id=a", 1},
		{"stops at empty line", []string{"ds:def id=a", "", "  owner=b"}, "ds:def id=a", 1},
		{"indented bare word is not continuation", []string{"ds:def id=a", "  bare"}, "ds:def id=a", 1},
		{"indented key only no equals", []string{"ds:def id=a", "  owner"}, "ds:def id=a", 1},
		{"tab indent counts", []string{"ds:def id=a", "\towner=b"}, "ds:def id=a owner=b", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			joined, consumed := Fold(tc.in)
			if joined != tc.wantJoined || consumed != tc.wantConsumed {
				t.Errorf("Fold(%q) = %q,%d; want %q,%d", tc.in, joined, consumed, tc.wantJoined, tc.wantConsumed)
			}
		})
	}
}

func TestFoldThenParse(t *testing.T) {
	t.Parallel()
	joined, n := Fold([]string{"ds:def id=sess-save-k7m2p4xq owner=@auth", "  stability=frozen desc=\"dual-write guard, remove after task-120\""})
	if n != 2 {
		t.Fatalf("consumed %d", n)
	}
	d, err := Parse(DefaultPrefix, joined)
	if err != nil {
		t.Fatal(err)
	}
	if d.Args["desc"] != "dual-write guard, remove after task-120" || d.Args["stability"] != "frozen" {
		t.Errorf("folded parse = %#v", d)
	}
}

func TestFormat(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		in      Directive
		want    string
		wantErr error
	}{
		{"bare values", Directive{Verb: "def", Args: map[string]string{"id": "a", "owner": "@auth"}, Keys: []string{"id", "owner"}}, "ds:def id=a owner=@auth", nil},
		{"verb only", Directive{Verb: "chain"}, "ds:chain", nil},
		{"space needs double quotes", Directive{Verb: "def", Args: map[string]string{"desc": "a b"}, Keys: []string{"desc"}}, `ds:def desc="a b"`, nil},
		{"double quote inside uses single quotes", Directive{Verb: "def", Args: map[string]string{"desc": `say "hi"`}, Keys: []string{"desc"}}, `ds:def desc='say "hi"'`, nil},
		{"single quote inside no whitespace stays bare", Directive{Verb: "def", Args: map[string]string{"desc": "it's"}, Keys: []string{"desc"}}, `ds:def desc=it's`, nil},
		{"both quote kinds no whitespace stays bare", Directive{Verb: "def", Args: map[string]string{"v": `0"'`}, Keys: []string{"v"}}, `ds:def v=0"'`, nil},
		{"single quote with whitespace uses double quotes", Directive{Verb: "def", Args: map[string]string{"desc": "it s"}, Keys: []string{"desc"}}, `ds:def desc="it s"`, nil},
		{"single quote inside with space uses double quotes", Directive{Verb: "def", Args: map[string]string{"desc": "it's ok"}, Keys: []string{"desc"}}, `ds:def desc="it's ok"`, nil},
		{"empty value quoted", Directive{Verb: "def", Args: map[string]string{"empty": ""}, Keys: []string{"empty"}}, `ds:def empty=""`, nil},
		{"value starting with quote is quoted", Directive{Verb: "def", Args: map[string]string{"v": "'x"}, Keys: []string{"v"}}, `ds:def v="'x"`, nil},
		{"keys missing from Keys are appended sorted", Directive{Verb: "def", Args: map[string]string{"z": "1", "a": "2", "m": "3"}, Keys: []string{"m"}}, "ds:def m=3 a=2 z=1", nil},
		{"keys in Keys but absent from Args are skipped", Directive{Verb: "def", Args: map[string]string{"a": "1"}, Keys: []string{"ghost", "a", "a"}}, "ds:def a=1", nil},
		{"both quote kinds fails", Directive{Verb: "def", Args: map[string]string{"v": `it's "x"`}, Keys: []string{"v"}}, "", ErrUnquotable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Format(DefaultPrefix, tc.in)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Format err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("Format = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatLink(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   Directive
		want string
	}{
		{"no args", Directive{Verb: "chain"}, "ds:chain"},
		{"ordered args", Directive{Verb: "block", Args: map[string]string{"id": "a", "lines": "1-6"}, Keys: []string{"id", "lines"}}, "ds:block?id=a&lines=1-6"},
		{"escapes", Directive{Verb: "cfg", Args: map[string]string{"query": "sql:select count(*) from users"}, Keys: []string{"query"}}, "ds:cfg?query=sql%3Aselect+count%28%2A%29+from+users"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := FormatLink(DefaultPrefix, tc.in); got != tc.want {
				t.Errorf("FormatLink = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRoundTrip pins the invariant Parse(Format(d)) == d and
// ParseLink(FormatLink(d)) == d for a spread of values including every quoting
// branch and awkward characters.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	values := []string{"a", "", "a b", `say "hi"`, "it's", `0"'`, `a"b'c`, "x=y,z", "@auth", "op://Platform/stripe-prod/credential", "1-6", "sql:select count(*) from users", "'lead", "\"lead", "tab\tinside", "ünïcödé"}
	for i, v := range values {
		d := Directive{Verb: "def", Args: map[string]string{"k": v, "id": "x"}, Keys: []string{"k", "id"}}
		text, err := Format(DefaultPrefix, d)
		if err != nil {
			t.Fatalf("[%d] Format(%q): %v", i, v, err)
		}
		back, err := Parse(DefaultPrefix, text)
		if err != nil {
			t.Fatalf("[%d] Parse(%q): %v", i, text, err)
		}
		if !reflect.DeepEqual(back, d) {
			t.Errorf("[%d] comment round trip: %#v -> %q -> %#v", i, d, text, back)
		}
		link := FormatLink(DefaultPrefix, d)
		back2, err := ParseLink(DefaultPrefix, link)
		if err != nil {
			t.Fatalf("[%d] ParseLink(%q): %v", i, link, err)
		}
		if !reflect.DeepEqual(back2, d) {
			t.Errorf("[%d] link round trip: %#v -> %q -> %#v", i, d, link, back2)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"ds:def id=a", `ds:def desc="a b" x='c"d'`, "ds:def id=a bare", "ds:", "", "ds:def id=a id=b", "ds:block?id=a"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := Parse(DefaultPrefix, s)
		if err != nil {
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("non-ParseError error: %v", err)
			}
			return
		}
		// Parsed output must format and parse back identically; that is the
		// invariant the rest of docsync depends on.
		text, ferr := Format(DefaultPrefix, d)
		if ferr != nil {
			// Only values with both quote kinds are unformattable, and Parse
			// can never produce such a value from one line, so this is a bug.
			t.Fatalf("Format failed on parsed value: %v (%#v)", ferr, d)
		}
		back, perr := Parse(DefaultPrefix, text)
		if perr != nil || !reflect.DeepEqual(back, d) {
			t.Fatalf("round trip mismatch: %q -> %#v -> %q -> %#v (%v)", s, d, text, back, perr)
		}
	})
}

func FuzzParseLink(f *testing.F) {
	for _, s := range []string{"ds:block?id=a", "ds:cfg?q=%20", "ds:x?a=b&c=d", "ds:?", "x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := ParseLink(DefaultPrefix, s)
		if err != nil {
			return
		}
		back, perr := ParseLink(DefaultPrefix, FormatLink(DefaultPrefix, d))
		if perr != nil || !reflect.DeepEqual(back, d) {
			t.Fatalf("link round trip mismatch: %q -> %#v -> %#v (%v)", s, d, back, perr)
		}
	})
}

// TestWhitespaceIsDecoded pins the parser and the printer to one definition
// of whitespace, over decoded runes. A no-break space — Option+Space on a
// Mac — is whitespace in both, so a value holding one is quoted and reads
// back whole; a lone invalid byte is whitespace in neither, so it stays in a
// bare value. The parser used to test single bytes and disagreed on both.
func TestWhitespaceIsDecoded(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"a b", "x\xa0y", "\xa0", "tab\there", "nel\u0085x"} {
		d := Directive{Verb: "def", Args: map[string]string{"id": "a-k7m2p4xq", "note": v}, Keys: []string{"id", "note"}}
		s, err := Format("ds", d)
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		back, err := Parse("ds", s)
		if err != nil || back.Args["note"] != v {
			t.Errorf("%q: formatted %q, read back %q (%v)", v, s, back.Args["note"], err)
		}
	}
	// A bare no-break space separates, and never splits inside the rune.
	d, err := Parse("ds", "ds:def id=a-k7m2p4xq owner=@o")
	if err != nil || d.Args["id"] != "a-k7m2p4xq" || d.Args["owner"] != "@o" {
		t.Errorf("no-break space between pairs = %+v %v", d.Args, err)
	}
}
