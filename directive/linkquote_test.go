package directive

import (
	"errors"
	"testing"
)

// TestParseLinkQuotedValues is bug 52: a quoted link value kept its quotes,
// so the spec's `title="TOAST"` was compared as `"TOAST"` with the quotes and
// every such link reported retitled. A quoted value is taken as written,
// quotes removed and nothing decoded, and may hold `&`; an unquoted one is
// percent-decoded as before.
func TestParseLinkQuotedValues(t *testing.T) {
	t.Parallel()
	for target, want := range map[string]map[string]string{
		`ds:url?href=https://x.y/a&title="TOAST"`:               {"href": "https://x.y/a", "title": "TOAST"},
		`ds:url?title='say "hi"'&href=h`:                        {"title": `say "hi"`, "href": "h"},
		`ds:cfg?query="sql:select count(*) from users"&ttl=24h`: {"query": "sql:select count(*) from users", "ttl": "24h"},
		`ds:cfg?query="a&b=c"&format=raw`:                       {"query": "a&b=c", "format": "raw"},
		`ds:cfg?query="50%"`:                                    {"query": "50%"},
		`ds:cfg?q=a%20b&r=x"y"`:                                 {"q": "a b", "r": `x"y"`},
		`ds:cfg?q="a"b&r=1`:                                     {"q": `"a"b`, "r": "1"},
		`ds:block?id=%22quoted%22`:                              {"id": `"quoted"`},
	} {
		d, err := ParseLink(DefaultPrefix, target)
		if err != nil {
			t.Errorf("%s: %v", target, err)
			continue
		}
		for k, v := range want {
			if d.Args[k] != v {
				t.Errorf("%s: %s = %q, want %q", target, k, d.Args[k], v)
			}
		}
	}
	if _, err := ParseLink(DefaultPrefix, `ds:url?title="open&href=x`); !errors.Is(err, ErrUnterminatedQuote) {
		t.Errorf("an unclosed quote = %v, want ErrUnterminatedQuote", err)
	}
	// FormatLink never emits a value that reads back as quoted.
	d := Directive{Verb: "block", Args: map[string]string{"title": `"x"`}, Keys: []string{"title"}}
	back, err := ParseLink(DefaultPrefix, FormatLink(DefaultPrefix, d))
	if err != nil || back.Args["title"] != `"x"` {
		t.Errorf("round trip of a quoted value = %q, %v", back.Args["title"], err)
	}
}
