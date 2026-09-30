package extract

import (
	"errors"
	"testing"

	"github.com/ubgo/docsync/block"
)

func TestTextExtract(t *testing.T) {
	t.Parallel()
	src := "" +
		"Intro line\n" +
		"\n" +
		"ds:def id=rota-m3k9v2pd span=+2\n" +
		"Week 37  khanakia\n" +
		"Week 38  someone\n" +
		"Week 39  nobody\n" +
		"\n" +
		"ds:def id=para-aaaaaaaa\n" +
		"\n" +
		"First para line\n" +
		"second line\n" +
		"\n" +
		"ds:def id=one-bbbbbbbb\n" +
		"single\n" +
		"\n" +
		"ds:def id=self-cccccccc span=+0\n" +
		"ds:def id=big-dddddddd span=+99\n" +
		"x\n" +
		"y\n" +
		"See ds:block?id=rota-m3k9v2pd for the rota. Also [x](ds:cfg?id=one-bbbbbbbb).\n" +
		"ds:claim owner=@a reviewed=2026-09-06\n" +
		"ds:def owner=noid\n" +
		"ds:def id=bad-eeeeeeee span=3\n" +
		"ds:def id=remote-ffffffff file=other.json pick=json:$.a\n" +
		"ds:def id=tail-gggggggg\n"
	f := Text{}.Extract("notes.txt", []byte(src), "ds")

	want := map[string]struct {
		kind       block.Kind
		start, end int
		content    string
		remote     bool
	}{
		"rota-m3k9v2pd":   {block.KindSpan, 4, 5, "Week 37  khanakia\nWeek 38  someone", false},
		"para-aaaaaaaa":   {block.KindSpan, 10, 11, "First para line\nsecond line", false},
		"one-bbbbbbbb":    {block.KindLine, 14, 14, "single", false},
		"self-cccccccc":   {block.KindLine, 16, 16, "ds:def id=self-cccccccc span=+0", false},
		"big-dddddddd":    {block.KindSpan, 18, 25, "", false},
		"remote-ffffffff": {"", 0, 0, "", true},
	}
	if len(f.Defs) != len(want) {
		t.Fatalf("defs = %d, want %d: %+v", len(f.Defs), len(want), f.Defs)
	}
	for _, d := range f.Defs {
		w, ok := want[d.Block.ID]
		if !ok {
			t.Errorf("unexpected def %s", d.Block.ID)
			continue
		}
		if d.Remote != w.remote {
			t.Errorf("%s remote = %v", d.Block.ID, d.Remote)
		}
		if w.remote {
			continue
		}
		if d.Block.Kind != w.kind || d.Block.Pos.Start != w.start || d.Block.Pos.End != w.end {
			t.Errorf("%s = kind %s pos %d-%d; want %s %d-%d", d.Block.ID, d.Block.Kind, d.Block.Pos.Start, d.Block.Pos.End, w.kind, w.start, w.end)
		}
		if w.content != "" && d.Block.Content != w.content {
			t.Errorf("%s content = %q, want %q", d.Block.ID, d.Block.Content, w.content)
		}
		if d.Block.Carrier != block.CarrierBareLine || d.Block.Hash == "" {
			t.Errorf("%s carrier/hash: %+v", d.Block.ID, d.Block)
		}
	}
	// References: two links in prose, one bare-line claim.
	verbs := map[string]int{}
	for _, r := range f.Refs {
		verbs[r.Reference.Verb]++
	}
	if verbs["block"] != 1 || verbs["cfg"] != 1 || verbs["claim"] != 1 {
		t.Errorf("refs by verb = %v", verbs)
	}
	for _, r := range f.Refs {
		if r.Reference.Carrier == block.CarrierLink && r.Reference.Sentence == "" {
			t.Errorf("link ref without sentence: %+v", r.Reference)
		}
	}
	// Problems: noid, bad span, tail with nothing after it.
	if len(f.Problems) != 3 {
		t.Fatalf("problems = %+v", f.Problems)
	}
	errs := map[error]bool{}
	for _, p := range f.Problems {
		for _, sentinel := range []error{ErrNoID, ErrBadSpan, ErrNothingToBind} {
			if errors.Is(p.Err, sentinel) {
				errs[sentinel] = true
			}
		}
	}
	if len(errs) != 3 {
		t.Errorf("problem kinds = %v", errs)
	}
}

func TestTextExtractCommentStyleFile(t *testing.T) {
	t.Parallel()
	// A .conf has a `#` style but no dedicated tier; the text tier reads its
	// comments too. A trailing def with span=+0 binds the code part.
	src := "server_name example.com; # ds:def id=host-aaaaaaaa span=+0\n# ds:def id=nx-bbbbbbbb\nworker_processes 4;\n"
	f := Text{}.Extract("nginx.conf", []byte(src), "ds")
	if len(f.Defs) != 2 {
		t.Fatalf("defs = %+v", f.Defs)
	}
	byID := map[string]Def{}
	for _, d := range f.Defs {
		byID[d.Block.ID] = d
	}
	if h := byID["host-aaaaaaaa"]; h.Block.Content != "server_name example.com; " || h.Block.Kind != block.KindLine || h.Block.Carrier != block.CarrierComment {
		t.Errorf("trailing def = %+v", h.Block)
	}
	if n := byID["nx-bbbbbbbb"]; n.Block.Content != "worker_processes 4;" {
		t.Errorf("comment def = %+v", n.Block)
	}
}

func TestTextSpanPastEnd(t *testing.T) {
	t.Parallel()
	f := Text{}.Extract("a.txt", []byte("ds:def id=x-aaaaaaaa span=+3\n"), "ds")
	if len(f.Defs) != 0 || len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, ErrNothingToBind) {
		t.Errorf("span past end: %+v %+v", f.Defs, f.Problems)
	}
	f = Text{}.Extract("a.txt", []byte("ds:def id=x-aaaaaaaa span=+1\nonly\n"), "ds")
	if len(f.Defs) != 1 || f.Defs[0].Block.Kind != block.KindLine {
		t.Errorf("span +1 is a line: %+v", f.Defs)
	}
	if !(Text{}).Match("anything.at.all") || (Text{}).Name() != "text" {
		t.Error("text tier identity")
	}
}
