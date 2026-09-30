package docsync

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/check"
)

// bomMark is the UTF-8 byte order mark Windows editors prepend.
const bomMark = "\xef\xbb\xbf"

// withBOM returns a copy of fsys with a byte order mark in front of every
// file, which is what saving each one from Notepad does.
func withBOM(fsys fstest.MapFS) fstest.MapFS {
	out := fstest.MapFS{}
	for name, f := range fsys {
		out[name] = &fstest.MapFile{Data: append([]byte(bomMark), f.Data...)}
	}
	return out
}

// applyAll applies edits to fsys in place.
func applyAll(t *testing.T, fsys fstest.MapFS, edits []Edit) {
	t.Helper()
	for _, e := range edits {
		out, err := e.Apply(fsys[e.File].Data)
		if err != nil {
			t.Fatalf("apply %+v: %v", e, err)
		}
		fsys[e.File] = &fstest.MapFile{Data: out}
	}
}

// TestBOMDoesNotChangeTheLedger pins that a byte order mark is not content:
// every def, cite, hash, and finding matches the same files without one.
// Before, the mark glued itself to the first YAML key's symbol, turned a
// block carrier on line 1 into a comment carrier, and hid front matter, so
// the def it covered was reported uncovered.
func TestBOMDoesNotChangeTheLedger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	plain := repo(false)
	plain["config/first.env"] = &fstest.MapFile{Data: []byte("PORT=8080 # ds:def id=first-line-a2b6f8jk\n")}
	plain["docs/first.md"] = &fstest.MapFile{Data: []byte("<!-- ds:block id=sess-save-k7m2p4xq -->\n\nPort [8080](ds:cfg?id=first-line-a2b6f8jk).\n")}
	plain["docs/front.md"] = &fstest.MapFile{Data: []byte("---\nds:\n  covers: [sess-ttl-p2c4y7mk]\n---\n# F\n")}
	type snapshot struct {
		ledger, refs any
		findings     []string
	}
	snap := func(fsys fstest.MapFS) snapshot {
		s := newSys(t, fsys)
		res, err := s.Scan(ctx)
		if err != nil {
			t.Fatal(err)
		}
		l, r := s.Snapshot(res)
		rep, err := s.Check(ctx, CheckOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var st []string
		for _, f := range rep.Findings {
			st = append(st, f.Doc+" "+f.ID+" "+string(f.State))
		}
		return snapshot{l.Rows, r.Rows, st}
	}
	a, b := snap(plain), snap(withBOM(plain))
	for _, f := range a.findings {
		if strings.Contains(f, string(check.StateUncovered)) {
			t.Fatalf("the plain fixture must cover everything, or front matter is not being tested: %v", a.findings)
		}
	}
	if !reflect.DeepEqual(a.ledger, b.ledger) {
		t.Errorf("ledger differs with a BOM:\n%+v\n%+v", a.ledger, b.ledger)
	}
	if !reflect.DeepEqual(a.refs, b.refs) {
		t.Errorf("refs differ with a BOM:\n%+v\n%+v", a.refs, b.refs)
	}
	if !reflect.DeepEqual(a.findings, b.findings) {
		t.Errorf("findings differ with a BOM:\n%v\n%v", a.findings, b.findings)
	}
}

// TestApplyKeepsTheBOM pins that an edit leaves a leading byte order mark
// where it was: in front of line 1, once per mark the file had.
func TestApplyKeepsTheBOM(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, src string
		e         Edit
		want      string
	}{
		{"insert above line 1", bomMark + "a\nb\n", Edit{Line: 1, New: "new"}, bomMark + "new\na\nb\n"},
		{"replace line 1", bomMark + "a\nb\n", Edit{Line: 1, Old: "a", New: "A"}, bomMark + "A\nb\n"},
		{"replace a later line", bomMark + "a\r\nb\r\n", Edit{Line: 2, Old: "b", New: "B"}, bomMark + "a\r\nB\r\n"},
		{"stacked marks all stay", bomMark + bomMark + "a\n", Edit{Line: 1, Old: "a", New: "A"}, bomMark + bomMark + "A\n"},
		{"no mark, none added", "a\n", Edit{Line: 1, New: "new"}, "new\na\n"},
	} {
		got, err := tc.e.Apply([]byte(tc.src))
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: got %q %v, want %q", tc.name, got, err, tc.want)
		}
	}
}

// TestSourceWritesOnBOMFiles drives every command that writes source over
// files that start with a byte order mark: def finds a first key, adopt
// rewrites a link on line 1, and rename rewrites a directive on line 1.
// Each result keeps the mark at byte 0 and rescans clean.
func TestSourceWritesOnBOMFiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fsys := withBOM(fstest.MapFS{
		"config/auth.yaml":  {Data: []byte("port: 8081\nhost: x\n")},
		"internal/store.go": {Data: []byte("func Save() {}\n")},
		"docs/d.md":         {Data: []byte("[save](internal/store.go#Save)\n")},
	})
	c := cfg()
	c.Scan.Code = []string{"config/**", "internal/**"}
	s := newSys(t, fsys, WithConfig(c))
	d, err := s.Define(ctx, "config/auth.yaml#port", DefineOptions{Label: "auth-port"})
	if err != nil {
		t.Fatalf("def on a BOM file's first key: %v", err)
	}
	applyAll(t, fsys, []Edit{d.Edit})
	res, err := s.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Adopt(ctx, res)
	if err != nil || r.Adopted != 1 || len(r.Unresolved) != 0 {
		t.Fatalf("adopt on BOM files = %+v %v", r, err)
	}
	applyAll(t, fsys, r.Edits)
	res, err = s.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rn, err := s.Rename(res, "auth-port", "api-port")
	if err != nil || len(rn.Edits) == 0 {
		t.Fatalf("rename on a BOM file = %+v %v", rn, err)
	}
	applyAll(t, fsys, rn.Edits)
	for name, f := range fsys {
		if !bytes.HasPrefix(f.Data, []byte(bomMark)) || bytes.Contains(f.Data[len(bomMark):], []byte(bomMark)) {
			t.Errorf("%s must keep exactly one mark, at byte 0: %q", name, f.Data)
		}
	}
	res, err = s.Scan(ctx)
	if err != nil || len(res.Problems) != 0 {
		t.Fatalf("rescan = %+v %v", res.Problems, err)
	}
	bound := map[string]string{}
	for _, b := range res.Defs {
		bound[b.Symbol] = b.ID
	}
	if !strings.HasPrefix(bound["port"], "api-port-") || bound["Save"] == "" {
		t.Errorf("defs after the writes = %+v", res.Defs)
	}
}

// FuzzEditApply states two things of every edit on every file. A leading
// byte order mark changes nothing but itself: applying to mark+src gives
// mark+(the result on src), or fails the same way. And an edit touches only
// its line: every other line comes back byte for byte.
func FuzzEditApply(f *testing.F) {
	f.Add("a\nb\n", 1, "", "new")
	f.Add("a\r\nb\r\n", 2, "b", "B")
	f.Add("a", 1, "a", "A")
	f.Add("", 1, "", "x")
	f.Add("a\nb", 3, "", "x")
	f.Fuzz(func(t *testing.T, src string, line int, old, nw string) {
		if strings.HasPrefix(src, bomMark) || strings.Contains(nw, "\n") {
			return
		}
		e := Edit{File: "f", Line: line, Old: old, New: nw}
		plain, errPlain := e.Apply([]byte(src))
		marked, errMarked := e.Apply([]byte(bomMark + src))
		if (errPlain == nil) != (errMarked == nil) {
			t.Fatalf("a BOM changed whether the edit applies: %v vs %v", errPlain, errMarked)
		}
		if errPlain != nil {
			return
		}
		if e.IsZero() {
			if string(plain) != src {
				t.Fatalf("a zero edit changed the file: %q -> %q", src, plain)
			}
			return
		}
		if string(marked) != bomMark+string(plain) {
			t.Fatalf("a BOM changed the result:\nplain  %q\nmarked %q", plain, marked)
		}
		before, after := strings.Split(src, "\n"), strings.Split(string(plain), "\n")
		shift := 0
		if old == "" {
			shift = 1
		}
		for i := range before {
			j := i
			if i >= line-1 {
				j = i + shift
			}
			if i == line-1 && old != "" {
				continue
			}
			if after[j] != before[i] {
				t.Fatalf("line %d changed though the edit was for line %d: %q -> %q", i+1, line, before[i], after[j])
			}
		}
	})
}
