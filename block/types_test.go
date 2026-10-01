package block

import "testing"

// TestValueTypes pins what each `type=` accepts (bug 54): type= used to be
// accepted and check nothing. Each type of §9.1 has values it must take and
// values it must refuse, including the spec's own fact examples (§11).
func TestValueTypes(t *testing.T) {
	t.Parallel()
	for typ, c := range map[ValueType]struct{ ok, bad []string }{
		TypeURL:      {[]string{"https://example.com", "http://a.b/c?d=1"}, []string{"example.com", "ftp://x.y", "https://", "://bad"}},
		TypeEmail:    {[]string{"ops@example.com", "a.b+c@x.co.uk"}, []string{"ops", "ops@example", "a b@x.com", "@x.com"}},
		TypeInt:      {[]string{"8081", "-3", "+0", " 42 "}, []string{"", "8081.5", "8k", "abc"}},
		TypeFloat:    {[]string{"1.5", "-2", ".5", "3.", "1e9", "2E-3"}, []string{"", "1.2M", "1,000", "e9"}},
		TypePercent:  {[]string{"99.9%", "5 %", "-1%"}, []string{"99.9", "%", "five%"}},
		TypeSemver:   {[]string{"2.14.0", "v1.0.0", "1.2.3-rc.1+build.5"}, []string{"1.2", "01.2.3", "1.2.3.4"}},
		TypeDate:     {[]string{"2026-09-06"}, []string{"2026-13-01", "06/09/2026", "2026-9-6"}},
		TypeDuration: {[]string{"30 days", "90d", "1.5 hours", "2w", "10 Minutes"}, []string{"", "thirty days", "30 fortnights"}},
		TypeHost:     {[]string{"example.com", "localhost", "db.internal:5432", "10.0.0.1"}, []string{"https://example.com", "a b", "-x.com", "x.com:port"}},
		TypeOpRef:    {[]string{"op://Platform/stripe-prod/credential", "op://v/i/s/f"}, []string{"op://v/i", "op:/v/i/f", "vault/item/field"}},
	} {
		if !typ.Known() {
			t.Errorf("%s is not Known", typ)
		}
		for _, v := range c.ok {
			if !typ.Accepts(v) {
				t.Errorf("%s must accept %q", typ, v)
			}
		}
		for _, v := range c.bad {
			if typ.Accepts(v) {
				t.Errorf("%s must refuse %q", typ, v)
			}
		}
	}
	if ValueType("colour").Known() || ValueType("colour").Accepts("red") {
		t.Error("an unknown type is not Known and accepts nothing")
	}
}

// TestCrosses pins the overlap rule a def must not break (bug 58): nesting
// and disjoint blocks are fine, crossing is not, and positions without an
// extent or in different files never cross.
func TestCrosses(t *testing.T) {
	t.Parallel()
	p := func(f string, s, e int) Position { return Position{File: f, Start: s, End: e} }
	for _, c := range []struct {
		a, b Position
		want bool
	}{
		{p("a", 1, 5), p("a", 3, 8), true},
		{p("a", 3, 8), p("a", 1, 5), true},
		{p("a", 1, 10), p("a", 3, 5), false},
		{p("a", 3, 5), p("a", 1, 10), false},
		{p("a", 1, 5), p("a", 1, 5), false},
		{p("a", 1, 2), p("a", 3, 4), false},
		{p("a", 1, 5), p("b", 3, 8), false},
		{p("a", 0, 0), p("a", 1, 5), false},
		{p("a", 1, 5), p("a", 0, 0), false},
	} {
		if got := Crosses(c.a, c.b); got != c.want {
			t.Errorf("Crosses(%v, %v) = %v", c.a, c.b, got)
		}
	}
}
