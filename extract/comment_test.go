package extract

import (
	"errors"
	"reflect"
	"testing"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
)

func TestStylesTableSane(t *testing.T) {
	t.Parallel()
	for ext, st := range Styles {
		// Every entry must declare how a directive is carried: a line prefix, a
		// block delimiter, or explicitly a bare line. An entry with none is a
		// type nothing can write to and nothing can read from, which is worse
		// than being absent, because absence is what makes a write refuse.
		if len(st.Line) == 0 && st.BlockOpen == "" && !st.Bare {
			t.Errorf("%s: style has no comment form and is not marked Bare", ext)
		}
		if st.Bare && (len(st.Line) > 0 || st.BlockOpen != "") {
			t.Errorf("%s: a bare carrier cannot also have a comment form", ext)
		}
		if (st.BlockOpen == "") != (st.BlockClose == "") || (st.AltOpen == "") != (st.AltClose == "") {
			t.Errorf("%s: unbalanced block delimiters", ext)
		}
	}
	if _, ok := StyleFor("x.unknown"); ok {
		t.Error("unknown extension has a style")
	}
	if st, ok := StyleFor("dir/Dockerfile"); !ok || st.Line[0] != "#" {
		t.Error("Dockerfile style")
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()
	goSt := Styles[".go"]
	mdSt := Styles[".md"]
	mdxSt := Styles[".mdx"]
	for _, tc := range []struct {
		name string
		line string
		st   Style
		want commentLine
	}{
		{"whole line comment", "// ds:def id=a", goSt, commentLine{n: 1, body: " ds:def id=a", isComment: true}},
		{"indented comment", "\t  // x", goSt, commentLine{n: 1, body: " x", isComment: true}},
		{"trailing comment", "port := 1 // ds:def id=a", goSt, commentLine{n: 1, body: " ds:def id=a", trailing: true, code: "port := 1 "}},
		{"no comment", "port := 1", goSt, commentLine{n: 1}},
		{"slashes inside string not a comment", `u := "http://x" // real`, goSt, commentLine{n: 1, body: " real", trailing: true, code: `u := "http://x" `}},
		{"escaped quote inside string", `s := "a\"b" // c`, goSt, commentLine{n: 1, body: " c", trailing: true, code: `s := "a\"b" `}},
		{"backtick string", "s := `//not` // c", goSt, commentLine{n: 1, body: " c", trailing: true, code: "s := `//not` "}},
		{"unterminated string swallows line", `s := "open // x`, goSt, commentLine{n: 1}},
		{"block comment same line", "/* ds:def id=a */", goSt, commentLine{n: 1, body: " ds:def id=a ", isComment: true}},
		{"block comment trailing code after close ignored", "x /* c */ y", goSt, commentLine{n: 1, body: " c ", trailing: true, code: "x "}},
		{"multi-line block open is not a carrier", "/* ds:def id=a", goSt, commentLine{n: 1}},
		{"html comment", "<!-- ds:block id=a -->", mdSt, commentLine{n: 1, body: " ds:block id=a ", isComment: true}},
		{"html comment after text is trailing", "text <!-- ds:claim owner=x -->", mdSt, commentLine{n: 1, body: " ds:claim owner=x ", trailing: true, code: "text "}},
		{"mdx jsx comment", "{/* ds:def id=a */}", mdxSt, commentLine{n: 1, body: " ds:def id=a ", isComment: true}},
		{"mdx alt html comment", "<!-- ds:def id=a -->", mdxSt, commentLine{n: 1, body: " ds:def id=a ", isComment: true}},
		{"hash style", "  # ds:def id=a", Styles[".py"], commentLine{n: 1, body: " ds:def id=a", isComment: true}},
		{"sql dashes", "-- ds:def id=a", Styles[".sql"], commentLine{n: 1, body: " ds:def id=a", isComment: true}},
		{"php two prefixes", "x = 1 # c", Styles[".php"], commentLine{n: 1, body: " c", trailing: true, code: "x = 1 "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classify(1, tc.line, tc.st)
			if got != tc.want {
				t.Errorf("classify(%q) = %#v, want %#v", tc.line, got, tc.want)
			}
		})
	}
}

func TestScanComments(t *testing.T) {
	t.Parallel()
	st := Styles[".go"]
	lines := []string{
		"package x",
		"// ds:def id=a-aaaaaaaa owner=@auth",
		"//   stability=api desc=\"two words\"",
		"func A() {}",
		"// plain comment mentioning ds: nothing",
		"// ds:def id=b-bbbbbbbb",
		"// not a continuation",
		"x := 1 // ds:block id=a-aaaaaaaa",
		"// ds:def id=c bare",
		"//   still=folded",
		"/* ds:cfg id=d-dddddddd */",
	}
	var f Found
	occs := scanComments(lines, st, "ds", &f)
	if len(occs) != 4 {
		t.Fatalf("occurrences = %d: %+v", len(occs), occs)
	}
	if occs[0].dir.Args["stability"] != "api" || occs[0].dir.Args["desc"] != "two words" || occs[0].pos != (block.Position{Start: 2, End: 3}) {
		t.Errorf("folded occurrence = %+v", occs[0])
	}
	if occs[1].pos != (block.Position{Start: 6, End: 6}) {
		t.Errorf("non-folded occurrence = %+v", occs[1])
	}
	if !occs[2].trailing || occs[2].code != "x := 1 " || occs[2].dir.Verb != "block" {
		t.Errorf("trailing occurrence = %+v", occs[2])
	}
	if occs[3].dir.Verb != "cfg" || occs[3].carrier != block.CarrierComment {
		t.Errorf("block-comment occurrence = %+v", occs[3])
	}
	if len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, directive.ErrPositional) || f.Problems[0].Pos != (block.Position{Start: 9, End: 10}) {
		t.Errorf("problems = %+v", f.Problems)
	}
}

func TestScanCommentsCustomPrefixAndTrailingNoFold(t *testing.T) {
	t.Parallel()
	lines := []string{"x := 1 // tie:def id=a-aaaaaaaa", "//   owner=@x"}
	var f Found
	occs := scanComments(lines, Styles[".go"], "tie", &f)
	if len(occs) != 1 || occs[0].dir.Has("owner") {
		t.Fatalf("trailing directive must not fold: %+v", occs)
	}
	if occs := scanComments(lines, Styles[".go"], "ds", &f); len(occs) != 0 {
		t.Fatal("wrong prefix matched")
	}
}

func TestScanBareLines(t *testing.T) {
	t.Parallel()
	lines := []string{"intro", "ds:def id=a-aaaaaaaa span=+2", "  owner=@x", "Week 37", "Week 38", "", "ds:def bare", "  k=v", "  ds:block id=a-aaaaaaaa"}
	var f Found
	occs := scanBareLines(lines, "ds", &f)
	if len(occs) != 2 {
		t.Fatalf("occurrences = %+v", occs)
	}
	if occs[0].pos != (block.Position{Start: 2, End: 3}) || occs[0].dir.Args["owner"] != "@x" || occs[0].carrier != block.CarrierBareLine {
		t.Errorf("occ0 = %+v", occs[0])
	}
	if occs[1].dir.Verb != "block" || occs[1].pos.Start != 9 {
		t.Errorf("occ1 = %+v", occs[1])
	}
	if len(f.Problems) != 1 || f.Problems[0].Pos != (block.Position{Start: 7, End: 8}) {
		t.Errorf("problems = %+v", f.Problems)
	}
}

func TestScanLinks(t *testing.T) {
	t.Parallel()
	lines := []string{
		"See [x](ds:block?id=a-aaaaaaaa) and [y](ds:cfg?id=b-bbbbbbbb&format=code). Done.",
		"// implements ds:block?id=c-cccccccc",
		"not ours: nods:block?id=q and https://x.dev/ds:block",
		"bad: ds:block?id=x&id=y",
		"comment form is not a link: ds:def id=q-qqqqqqqq",
	}
	var f Found
	refs := scanLinks(lines, "ds", &f, func(line string, col int) string { return "S" })
	if len(refs) != 3 {
		t.Fatalf("refs = %+v", refs)
	}
	if refs[0].Reference.ID != "a-aaaaaaaa" || refs[0].Reference.Carrier != block.CarrierLink || refs[0].Reference.Sentence != "S" || refs[0].Reference.Pos.Start != 1 {
		t.Errorf("ref0 = %+v", refs[0].Reference)
	}
	if refs[1].Reference.Args["format"] != "code" {
		t.Errorf("ref1 args = %v", refs[1].Reference.Args)
	}
	if refs[2].Reference.Pos.Start != 2 || refs[2].Reference.ID != "c-cccccccc" {
		t.Errorf("ref2 = %+v", refs[2].Reference)
	}
	if len(f.Problems) != 1 || f.Problems[0].Pos.Start != 4 {
		t.Errorf("problems = %+v", f.Problems)
	}
	// nil sentence func leaves the sentence empty.
	refs = scanLinks([]string{"[a](ds:block?id=z-zzzzzzzz)"}, "ds", &f, nil)
	if refs[0].Reference.Sentence != "" || refs[0].Reference.SentenceHash != "" {
		t.Error("nil sentenceOf must leave sentence empty")
	}
}

func TestLineHelpers(t *testing.T) {
	t.Parallel()
	if got := splitLines([]byte("a\r\nb\n")); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("splitLines = %q", got)
	}
	if got := splitLines(nil); got != nil {
		t.Errorf("splitLines(nil) = %q", got)
	}
	lines := []string{"a", "", "  ", "d", "e", ""}
	if join(lines, 4, 5) != "d\ne" || join(lines, 0, 99) != "a\n\n  \nd\ne\n" || join(lines, 5, 4) != "" {
		t.Error("join")
	}
	if nextNonBlank(lines, 2) != 4 || nextNonBlank(lines, 6) != 0 || nextNonBlank(lines, 7) != 0 {
		t.Error("nextNonBlank")
	}
	if blankBoundedEnd(lines, 4) != 5 || blankBoundedEnd(lines, 1) != 1 {
		t.Error("blankBoundedEnd")
	}
}

// TestHasNoComments pins the formats a directive can never be valid in, and
// the ones that look similar but do take comments -- listing one of those here
// would make a repair delete a line that could have been commented.
func TestHasNoComments(t *testing.T) {
	t.Parallel()
	for p, want := range map[string]bool{
		"a.json": true, "dir/x.jsonl": true, "b.ndjson": true, "m.webmanifest": true, "c.csv": true, "d.tsv": true,
		// Matched by name, and by name regardless of case.
		"go.sum": true, "sub/GO.SUM": true,
		// These take comments: yarn.lock opens with a # line, Cargo.lock is
		// TOML, pnpm-lock.yaml is YAML.
		"yarn.lock": false, "Cargo.lock": false, "pnpm-lock.yaml": false,
		"a.go": false, "go.mod": false, "notes.txt": false, "README": false,
	} {
		if got := HasNoComments(p); got != want {
			t.Errorf("HasNoComments(%q) = %v, want %v", p, got, want)
		}
	}
	// Nothing may be in both tables: a format cannot both carry a directive
	// and be unable to hold one.
	for k := range NoComments {
		if _, ok := Styles[k]; ok {
			t.Errorf("%s is in Styles and NoComments", k)
		}
	}
}

// TestCodeTiersReadBareDefs is the gap that hid Pkl damage: the code tiers
// only read comments, so a bare `ds:def` line in a code file was neither a def
// nor a problem. It is read now, so it keeps working and can be reported.
func TestCodeTiersReadBareDefs(t *testing.T) {
	t.Parallel()
	src := "ds:def id=p-h3v8n2wd\nname = \"x\"\n"
	f := Code{}.Extract("a.pkl", []byte(src), "ds")
	if len(f.Defs) != 1 || f.Defs[0].Block.Carrier != block.CarrierBareLine || f.Defs[0].Block.ID != "p-h3v8n2wd" {
		t.Fatalf("defs = %+v problems = %+v", f.Defs, f.Problems)
	}
	// A bare line that is not a def -- a cite written bare -- is not a def,
	// and is not reported here either; the code tiers bind defs.
	g := Code{}.Extract("a.pkl", []byte("ds:block?id=p-h3v8n2wd\nname = \"x\"\n"), "ds")
	if len(g.Defs) != 0 {
		t.Errorf("a bare cite became a def: %+v", g.Defs)
	}
	// A Go label named `ds` is a line reading `ds:`. It must not be read as a
	// malformed directive and reported, which would be a problem in correct
	// code; the parse errors of this pass are dropped for exactly that.
	h := Code{}.Extract("a.go", []byte("package p\n\nfunc F() {\nds:\n\tfor {\n\t\tbreak ds\n\t}\n}\n"), "ds")
	if len(h.Problems) != 0 || len(h.Defs) != 0 {
		t.Errorf("a Go label was read as a directive: defs %+v problems %+v", h.Defs, h.Problems)
	}
	// The same line reaches grammar tiers through ScanCode.
	var out Found
	_, defs := ScanCode("a.go", []byte("package p\n\nds:def id=g-k7m2p4xq\nfunc F() {}\n"), "ds", &out)
	if len(defs) != 1 || defs[0].Carrier != block.CarrierBareLine {
		t.Errorf("ScanCode missed a bare def: %+v", defs)
	}
}
