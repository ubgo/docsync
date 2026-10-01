package mdspan

import "testing"

// TestInlineLinks is bug 51: every reader of directive links used a pattern
// that stopped at the first `)` or space, so `count(*)` cut a query short and
// a destination with a space was dropped. The destination now follows
// CommonMark: `<…>` may hold spaces, a bare one balances its parentheses.
func TestInlineLinks(t *testing.T) {
	t.Parallel()
	type want struct{ text, dest string }
	for line, links := range map[string][]want{
		"a [x](ds:cfg?q=count(*)) b":             {{"x", "ds:cfg?q=count(*)"}},
		"[x](<ds:cfg?query=\"a b\">) and [y](z)": {{"x", "ds:cfg?query=\"a b\""}, {"y", "z"}},
		"![img](a.png)":                          {{"img", "a.png"}},
		"[x]( ds:block?id=a )":                   {{"x", "ds:block?id=a"}},
		"[x](ds:cfg?query=\"a b\")":              nil,
		"[x](<unclosed":                          nil,
		"[x](<a<b>)":                             nil,
		"[x](a(b)":                               nil,
		"[x](a(b c))":                            nil,
		"[x](a":                                  nil,
		"[x](a b) [y](c)":                        {{"y", "c"}},
		"[](empty)":                              {{"", "empty"}},
		"[x]()":                                  {{"x", ""}},
	} {
		got := InlineLinks(line)
		if len(got) != len(links) {
			t.Errorf("%q: %d links, want %d: %+v", line, len(got), len(links), got)
			continue
		}
		for i, l := range got {
			if line[l.Text.Start:l.Text.End] != links[i].text || line[l.Dest.Start:l.Dest.End] != links[i].dest {
				t.Errorf("%q link %d = %q -> %q", line, i, line[l.Text.Start:l.Text.End], line[l.Dest.Start:l.Dest.End])
			}
		}
	}
	if l := InlineLinks("![i](p)"); len(l) != 1 || !l[0].Image || l[0].All != (Span{0, 7}) {
		t.Errorf("image = %+v", l)
	}
}
