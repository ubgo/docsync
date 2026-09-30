package records

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync"
)

var store = fstest.MapFS{
	"records/t1.md":     {Data: []byte("---\nkind: task\nstate: open\ndue: 2026-09-10\nowner: \"@auth\"\ntags:\n  - a\n# comment\nnovalue\n---\n# Rotate keys\n\nbody\n")},
	"records/t2.md":     {Data: []byte("---\nkind: task\nstate: done\ndue: 2026-09-01\ntitle: Explicit title\n...\n")},
	"records/t3.md":     {Data: []byte("---\r\nkind: task\r\nstate: open\r\ndue: 2026-09-05\r\n---\r\ntext without heading\r\n")},
	"records/note.md":   {Data: []byte("# Just a note\n")},
	"records/skip.txt":  {Data: []byte("not a record")},
	"records/sub/t4.md": {Data: []byte("---\nkind: incident\nstate: open\n---\n")},
}

func TestParse(t *testing.T) {
	t.Parallel()
	r := Parse("records/t1.md", string(store["records/t1.md"].Data))
	want := map[string]string{"path": "records/t1.md", "kind": "task", "state": "open", "due": "2026-09-10", "owner": "@auth", "title": "Rotate keys"}
	if !reflect.DeepEqual(r, want) {
		t.Errorf("t1 = %v", r)
	}
	if r := Parse("records/t2.md", string(store["records/t2.md"].Data)); r["title"] != "Explicit title" {
		t.Errorf("explicit title = %v", r)
	}
	if r := Parse("records/t3.md", string(store["records/t3.md"].Data)); r["title"] != "t3" || r["due"] != "2026-09-05" {
		t.Errorf("crlf / fallback title = %v", r)
	}
	if r := Parse("x.md", ""); r["title"] != "x" || len(r) != 2 {
		t.Errorf("empty = %v", r)
	}
	if r := Parse("x.md", "---\nunterminated: 1\n"); r["unterminated"] != "1" || r["title"] != "x" {
		t.Errorf("unterminated frontmatter = %v", r)
	}
}

func TestSourceAndApply(t *testing.T) {
	t.Parallel()
	src := Frontmatter(store, "records")
	rows, err := src(map[string]string{KeyKind: "task", KeyWhere: "state!=done", KeySort: "due", KeyCols: "title,due"})
	if err != nil || len(rows) != 2 || rows[0]["title"] != "t3" || rows[1]["title"] != "Rotate keys" || len(rows[0]) != 2 {
		t.Errorf("query = %v %v", rows, err)
	}
	rows, _ = src(map[string]string{KeySort: "-due", KeyLimit: "1"})
	if len(rows) != 1 || rows[0]["path"] != "records/t1.md" {
		t.Errorf("desc limit = %v", rows)
	}
	rows, _ = src(map[string]string{KeyWhere: "state=open and kind=incident"})
	if len(rows) != 1 || rows[0]["path"] != "records/sub/t4.md" {
		t.Errorf("and = %v", rows)
	}
	if all, _ := src(nil); len(all) != 5 {
		t.Errorf("all = %d", len(all))
	}
	if _, err := src(map[string]string{KeyWhere: "nonsense"}); !errors.Is(err, ErrBadWhere) {
		t.Errorf("bad where = %v", err)
	}
	if _, err := src(map[string]string{KeyLimit: "0"}); !errors.Is(err, ErrBadLimit) {
		t.Errorf("bad limit = %v", err)
	}
	if rows, err := src(map[string]string{KeyLimit: "99"}); err != nil || len(rows) != 5 {
		t.Errorf("large limit = %v %v", rows, err)
	}
	if _, err := Frontmatter(store, "missing")(nil); err == nil {
		t.Error("missing dir")
	}
	if _, err := Frontmatter(errFS{}, "records")(nil); err == nil {
		t.Error("walk error")
	}
	if _, err := Frontmatter(readErrFS{store}, "records")(nil); err == nil {
		t.Error("read error")
	}
	if c, err := parseWhere("  "); err != nil || c != nil {
		t.Error("blank where")
	}
	if _, err := parseWhere("=x"); !errors.Is(err, ErrBadWhere) {
		t.Error("empty field")
	}
}

type errFS struct{}

func (errFS) Open(string) (fs.File, error) { return nil, errors.New("boom") }

// readErrFS lets the walk see entries but fails to read files. fs.ReadFile
// prefers ReadFileFS, so that method is the one that must fail.
type readErrFS struct{ fstest.MapFS }

func (r readErrFS) ReadFile(name string) ([]byte, error) {
	if strings.HasSuffix(name, ".md") {
		return nil, errors.New("unreadable")
	}
	return r.MapFS.ReadFile(name)
}

func TestThroughRender(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{}
	for k, v := range store {
		fsys[k] = v
	}
	fsys["docs/board.md"] = &fstest.MapFile{Data: []byte("<!-- ds:table kind=task where=\"state!=done\" cols=title,due sort=due -->\n")}
	s, err := docsync.New(docsync.WithFS(fsys), docsync.WithRecords(Frontmatter(fsys, "records")))
	if err != nil {
		t.Fatal(err)
	}
	out, notes, err := s.Render(context.Background(), "docs/board.md", docsync.RenderOptions{})
	if err != nil || len(notes) != 0 || !strings.Contains(string(out), "| title | due |\n|---|---|\n| t3 | 2026-09-05 |\n| Rotate keys | 2026-09-10 |") {
		t.Errorf("render = %s %v %v", out, notes, err)
	}
	rep, _ := s.Check(context.Background(), docsync.CheckOptions{})
	if rep.ExitCode != 0 {
		t.Errorf("table with a source is ok: %v", rep.States)
	}
}

// TestParseIgnoresBOM pins that a byte order mark in front of the opening
// fence does not hide the front matter: before, every field of a record
// saved by a Windows editor went missing.
func TestParseIgnoresBOM(t *testing.T) {
	t.Parallel()
	src := "---\nowner: \"@auth\"\nstatus: live\n---\n# Title\n"
	want := Parse("r.md", src)
	if want["owner"] == "" {
		t.Fatalf("the plain record must parse, or this compares nothing: %v", want)
	}
	for _, marks := range []string{"\xef\xbb\xbf", "\xef\xbb\xbf\xef\xbb\xbf"} {
		if got := Parse("r.md", marks+src); !reflect.DeepEqual(got, want) {
			t.Errorf("with %d marks: %v, want %v", len(marks)/3, got, want)
		}
	}
}
