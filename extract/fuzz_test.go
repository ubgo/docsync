package extract

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// fuzzPaths is one path per tier the root module ships, so the fuzzer drives
// every extractor over the same bytes.
var fuzzPaths = []string{"a.md", "a.adoc", "a.rst", "a.go", "a.py", "a.txt", "a.yaml", "a.toml", "a.json", "a.html"}

// FuzzExtract: no tier panics on any input, and every position a def or a
// reference reports lies inside the file. A position outside it is a slice
// out of range waiting in whatever consumer reads that range next.
func FuzzExtract(f *testing.F) {
	for _, s := range []string{
		"# T\n\n<!-- ds:def id=a-k7m2p4xq -->\n## A\n\ntext\n",
		"= D\n\n// ds:def id=a-k7m2p4xq\n== A\n\nOne.\n",
		"T\n=\n\n.. ds:def id=a-k7m2p4xq\n\nA\n-\n\ntext\n",
		"package p\n\n// ds:def id=a-k7m2p4xq span=+3\nfunc A() {}\n",
		"# ds:def id=a-k7m2p4xq\nkey: 1\n",
		"see [x](ds:block?id=a-k7m2p4xq) and <!-- ds:block id=b-h3v8n2wd -->",
		"<!-- ds:def id=a-k7m2p4xq -->",
		"// ds:def id=a-k7m2p4xq span=+99999\nx\n",
		"",
	} {
		f.Add(s)
	}
	reg := Default()
	f.Fuzz(func(t *testing.T, src string) {
		lines := len(strings.Split(src, "\n"))
		for _, p := range fuzzPaths {
			ex, err := reg.For(p)
			if err != nil {
				t.Fatal(err)
			}
			found := ex.Extract(p, []byte(src), "ds")
			for _, d := range found.Defs {
				if d.Remote {
					continue
				}
				b := d.Block
				// 1-based and inclusive; an empty extent is written n..n-1.
				if b.Pos.Start < 1 || b.Pos.End > lines || b.Pos.End < b.Pos.Start-1 {
					t.Fatalf("%s: def %s at %d-%d, file has %d lines\n%q", p, b.ID, b.Pos.Start, b.Pos.End, lines, src)
				}
				if dp := b.DirectivePos; dp.Start < 1 || dp.End > lines || dp.End < dp.Start {
					t.Fatalf("%s: def %s directive at %d-%d, file has %d lines\n%q", p, b.ID, dp.Start, dp.End, lines, src)
				}
			}
			for _, r := range found.Refs {
				if pos := r.Reference.Pos; pos.Start < 1 || pos.Start > lines {
					t.Fatalf("%s: reference %s at line %d, file has %d lines\n%q", p, r.Reference.ID, pos.Start, lines, src)
				}
			}
		}
	})
}

// anchorStyles are the carriers `ds def` writes above a heading or symbol,
// per tier, and the heading syntax that tier binds a section to.
var anchorStyles = []struct {
	path    string
	carrier func(id string) string
	heading func(level int, title string) []string
}{
	{"a.md", func(id string) string { return "<!-- ds:def id=" + id + " -->" }, func(l int, t string) []string {
		return []string{strings.Repeat("#", l+1) + " " + t}
	}},
	{"a.adoc", func(id string) string { return "// ds:def id=" + id }, func(l int, t string) []string {
		return []string{strings.Repeat("=", l+1) + " " + t}
	}},
	{"a.rst", func(id string) string { return ".. ds:def id=" + id }, func(l int, t string) []string {
		return []string{t, strings.Repeat(string("=-~^"[l]), len(t))}
	}},
}

// TestAnchoringNeverRehashesANeighbour is bug 4 as a property. `ds def`
// anchors a heading by writing a carrier on the line above it; that must
// never change the hash of any other block, or anchoring one section flags
// every sentence citing the section before it although none of its text
// changed. Documents are generated with random heading levels, paragraphs,
// blank runs and existing anchors, in every document tier.
func TestAnchoringNeverRehashesANeighbour(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(4))
	reg := Default()
	for trial := 0; trial < 20000; trial++ {
		style := anchorStyles[trial%len(anchorStyles)]
		var doc []string
		var headingAt []int // index into doc of each heading's first line
		anchored := map[int]bool{}
		doc = append(doc, style.heading(0, "Title")...)
		for s := 0; s < 2+rng.Intn(5); s++ {
			doc = append(doc, "")
			if rng.Intn(2) == 0 {
				id := fmt.Sprintf("s%d-k7m2p4x%c", s, 'a'+rune(s))
				doc = append(doc, style.carrier(id))
				anchored[len(headingAt)] = true
			}
			headingAt = append(headingAt, len(doc))
			doc = append(doc, style.heading(1+rng.Intn(3), fmt.Sprintf("Section %d", s))...)
			for p := 0; p < 1+rng.Intn(3); p++ {
				doc = append(doc, strings.Repeat("", rng.Intn(2)))
				doc = append(doc, fmt.Sprintf("Paragraph %d of %d.", p, s))
			}
			for b := rng.Intn(3); b > 0; b-- {
				doc = append(doc, "")
			}
		}
		// Anchor one heading that has none, the way ds def does.
		var free []int
		for i := range headingAt {
			if !anchored[i] {
				free = append(free, i)
			}
		}
		if len(free) == 0 {
			continue
		}
		at := headingAt[free[rng.Intn(len(free))]]
		after := append(append(append([]string(nil), doc[:at]...), style.carrier("new-t4k2b9rf")), doc[at:]...)

		ex, _ := reg.For(style.path)
		hashes := func(lines []string) map[string]string {
			m := map[string]string{}
			for _, d := range ex.Extract(style.path, []byte(strings.Join(lines, "\n")+"\n"), "ds").Defs {
				m[d.Block.ID] = d.Block.Hash
			}
			return m
		}
		before, now := hashes(doc), hashes(after)
		for id, h := range before {
			if now[id] != h {
				t.Fatalf("%s trial %d: anchoring a heading changed the hash of %s\nbefore:\n%s\n\nafter:\n%s", style.path, trial, id, strings.Join(doc, "\n"), strings.Join(after, "\n"))
			}
		}
	}
}

// TestAnchoringNestedCodeAndConfig is the same property for the tiers whose
// blocks nest by structure rather than by heading: a field inside an
// anchored struct, a function inside an anchored span, a child key inside an
// anchored YAML map. Anchoring the inner one must leave the outer hash alone.
func TestAnchoringNestedCodeAndConfig(t *testing.T) {
	t.Parallel()
	reg := Default()
	for _, tc := range []struct {
		path, before, after, outer string
	}{
		{
			path:   "a.go",
			before: "package p\n\n// ds:def id=store-k7m2p4xq\ntype Store struct {\n\tA int\n\tB int\n}\n",
			after:  "package p\n\n// ds:def id=store-k7m2p4xq\ntype Store struct {\n\tA int\n\t// ds:def id=field-t4k2b9rf\n\tB int\n}\n",
			outer:  "store-k7m2p4xq",
		},
		{
			path:   "a.py",
			before: "# ds:def id=cls-k7m2p4xq\nclass C:\n    def a(self):\n        return 1\n    def b(self):\n        return 2\n",
			after:  "# ds:def id=cls-k7m2p4xq\nclass C:\n    def a(self):\n        return 1\n    # ds:def id=meth-t4k2b9rf\n    def b(self):\n        return 2\n",
			outer:  "cls-k7m2p4xq",
		},
		{
			// The root config tier binds a map only through span; anchoring a
			// child key inside it must neither rehash the map nor push its
			// last line out of the span.
			path:   "a.yaml",
			before: "# ds:def id=server-k7m2p4xq span=+2\nserver:\n  host: x\n  port: 8081\n",
			after:  "# ds:def id=server-k7m2p4xq span=+2\nserver:\n  host: x\n  # ds:def id=port-t4k2b9rf\n  port: 8081\n",
			outer:  "server-k7m2p4xq",
		},
		{
			path:   "a.md",
			before: "<!-- ds:def id=rows-k7m2p4xq span=+2 -->\nrow one\nrow two\nrow three\n",
			after:  "<!-- ds:def id=rows-k7m2p4xq span=+2 -->\nrow one\nrow two\n<!-- ds:def id=inner-t4k2b9rf -->\nrow three\n",
			outer:  "rows-k7m2p4xq",
		},
		{
			path:   "a.txt",
			before: "ds:def id=rota-k7m2p4xq span=+2\nWeek 37\nWeek 38\n",
			after:  "ds:def id=rota-k7m2p4xq span=+2\nWeek 37\nds:def id=inner-t4k2b9rf\nWeek 38\n",
			outer:  "rota-k7m2p4xq",
		},
		{
			path:   "a.txt",
			before: "ds:def id=para-k7m2p4xq\nline one\nline two\nline three\n",
			after:  "ds:def id=para-k7m2p4xq\nline one\nds:def id=inner-t4k2b9rf\nline two\nline three\n",
			outer:  "para-k7m2p4xq",
		},
	} {
		ex, err := reg.For(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		hash := func(src string) (string, string) {
			for _, d := range ex.Extract(tc.path, []byte(src), "ds").Defs {
				if d.Block.ID == tc.outer {
					return d.Block.Hash, d.Block.Content
				}
			}
			t.Fatalf("%s: no def %s in\n%s", tc.path, tc.outer, src)
			return "", ""
		}
		h1, _ := hash(tc.before)
		h2, content := hash(tc.after)
		if h1 != h2 {
			t.Errorf("%s: anchoring an inner block changed the hash of %s", tc.path, tc.outer)
		}
		// Content still holds the carrier: it is what renders.
		if !strings.Contains(content, "t4k2b9rf") {
			t.Errorf("%s: content lost the inner carrier; only the hash may leave it out:\n%s", tc.path, content)
		}
	}
}

// TestHashedJoinClamps pins that hashedJoin clamps its range exactly as join
// does, so a hash and the content it stands for are always cut from the same
// lines.
func TestHashedJoinClamps(t *testing.T) {
	t.Parallel()
	lines := []string{"a", "b", "c"}
	none := map[int]bool{}
	for _, r := range [][2]int{{0, 2}, {-5, 9}, {2, 2}, {3, 1}, {1, 3}} {
		if got, want := hashedJoin(lines, r[0], r[1], none), join(lines, r[0], r[1]); got != want {
			t.Errorf("hashedJoin(%d,%d) = %q, join = %q", r[0], r[1], got, want)
		}
	}
	if got := hashedJoin(lines, 1, 3, map[int]bool{2: true}); got != "a\nc" {
		t.Errorf("carrier left in: %q", got)
	}
	if got := HashedJoin(lines, 1, 3, map[int]bool{1: true, 3: true}); got != "b" {
		t.Errorf("exported HashedJoin = %q", got)
	}
}

// TestSpanEndCountsContentLines pins spanEnd directly: carriers inside the
// window do not count toward it, and it never runs past the file.
func TestSpanEndCountsContentLines(t *testing.T) {
	t.Parallel()
	lines := []string{"a", "<!-- c -->", "b", "c", "d"}
	carriers := map[int]bool{2: true}
	for _, tc := range []struct{ start, want, end int }{
		{1, 1, 1},  // just the bound line
		{1, 2, 3},  // skips the carrier on line 2
		{1, 3, 4},  // a, b, c
		{1, 99, 5}, // clamped
		{3, 0, 2},  // want nothing: an empty range before start
	} {
		if got := spanEnd(lines, tc.start, tc.want, carriers); got != tc.end {
			t.Errorf("spanEnd(start %d, want %d) = %d, want %d", tc.start, tc.want, got, tc.end)
		}
	}
	if got := SpanEnd(lines, 1, 2, nil); got != 2 {
		t.Errorf("with no carriers a span is plain lines: %d", got)
	}
}

// TestProseTiers pins which built-in tiers are prose, and so are exempt from
// the scanner's long-line skip: the ones whose files are written sentences.
func TestProseTiers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ex    Extractor
		prose bool
	}{{Markdown{}, true}, {Document{}, true}, {Text{}, true}, {Code{}, false}, {Config{}, false}} {
		if got := IsProse(tc.ex); got != tc.prose {
			t.Errorf("IsProse(%s) = %v, want %v", tc.ex.Name(), got, tc.prose)
		}
	}
}

// TestCarriersExported pins the hook external tiers use to leave carriers
// out of a hash: it finds standalone defs and cites, not trailing ones, and
// a path with no comment style has none.
func TestCarriersExported(t *testing.T) {
	t.Parallel()
	lines := []string{"package p", "// ds:def id=a-k7m2p4xq", "var a = 1 // ds:def id=b-h3v8n2wd", "// ds:block id=a-k7m2p4xq"}
	got := Carriers("a.go", lines, "ds")
	if !got[2] || got[3] || !got[4] || len(got) != 2 {
		t.Errorf("Carriers = %v, want lines 2 and 4", got)
	}
	if got := Carriers("a.unknownext", lines, "ds"); len(got) != 0 {
		t.Errorf("no comment style, no carriers: %v", got)
	}
}

// TestFenceFollowsCommonMark pins the fence rules both the directive mask and
// the repo-mode region finder rely on.
func TestFenceFollowsCommonMark(t *testing.T) {
	t.Parallel()
	inside := func(lines ...string) []bool {
		var f fence
		out := make([]bool, len(lines))
		for i, l := range lines {
			out[i] = f.step(l)
		}
		return out
	}
	for _, tc := range []struct {
		name  string
		lines []string
		want  []bool
	}{
		{"a plain fence", []string{"a", "```go", "x", "```", "b"}, []bool{false, true, true, true, false}},
		// The nesting render.fenceFor writes: a shorter run does not close.
		{"a longer fence holds a shorter one", []string{"````", "```", "x", "```", "````", "b"}, []bool{true, true, true, true, true, false}},
		{"tildes are not closed by backticks", []string{"~~~", "```", "~~~", "b"}, []bool{true, true, true, false}},
		// A closing line carries nothing but the run.
		{"an info string does not close", []string{"```", "```go", "```", "b"}, []bool{true, true, true, false}},
		{"a longer run closes", []string{"```", "`````", "b"}, []bool{true, true, false}},
		{"an unclosed fence runs to the end", []string{"```", "x", "y"}, []bool{true, true, true}},
		{"two backticks are not a fence", []string{"``x``", "b"}, []bool{false, false}},
	} {
		if got := inside(tc.lines...); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}
