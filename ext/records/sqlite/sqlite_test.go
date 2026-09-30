package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/records"
)

func newDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "records.db")
	db, err := sql.Open(DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		"CREATE TABLE tasks (title TEXT, state TEXT, due TEXT, owner TEXT)",
		"INSERT INTO tasks VALUES ('Rotate keys', 'open', '2026-09-10', '@auth')",
		"INSERT INTO tasks VALUES ('Done thing', 'done', '2026-09-01', NULL)",
		"INSERT INTO tasks VALUES ('Soon', 'open', '2026-09-05', '@ops')",
		"CREATE TABLE weird (\"a b\" INTEGER)",
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestSource(t *testing.T) {
	t.Parallel()
	path := newDB(t)
	src, closer, err := Open(path, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	rows, err := src(map[string]string{records.KeyWhere: "state!=done", records.KeySort: "due", records.KeyCols: "title,due"})
	if err != nil || len(rows) != 2 || rows[0]["title"] != "Soon" || rows[1]["title"] != "Rotate keys" || len(rows[0]) != 2 {
		t.Errorf("query = %v %v", rows, err)
	}
	// NULL is the empty string; kind= names the table and is not a filter.
	all, err := src(map[string]string{records.KeyKind: "tasks"})
	if err != nil || len(all) != 3 || all[1]["owner"] != "" {
		t.Errorf("all = %v %v", all, err)
	}
	if _, err := src(map[string]string{records.KeyKind: "missing"}); err == nil {
		t.Error("missing table")
	}
	if _, err := src(map[string]string{records.KeyKind: "tasks; drop table tasks"}); !errors.Is(err, ErrBadTable) {
		t.Errorf("bad table = %v", err)
	}
	if _, err := src(map[string]string{records.KeyLimit: "0"}); !errors.Is(err, records.ErrBadLimit) {
		t.Errorf("apply errors pass through = %v", err)
	}
	none, _, _ := Open(path, "")
	if _, err := none(nil); !errors.Is(err, ErrNoTable) {
		t.Errorf("no table = %v", err)
	}
	// Read-only: a missing file does not get created.
	if _, _, err := Open(filepath.Join(t.TempDir(), "nope.db"), "t"); err == nil {
		t.Error("missing database must fail")
	}
	// A closed handle fails at query time.
	db, _ := sql.Open(DriverName, path)
	db.Close()
	if _, err := Source(db, "tasks")(nil); err == nil {
		t.Error("closed db")
	}
}

func TestThroughRender(t *testing.T) {
	t.Parallel()
	src, closer, err := Open(newDB(t), "")
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	fsys := fstest.MapFS{"docs/board.md": {Data: []byte("<!-- ds:table kind=tasks where=\"state=open\" cols=title,due sort=due -->\n")}}
	s, err := docsync.New(docsync.WithFS(fsys), docsync.WithRecords(src))
	if err != nil {
		t.Fatal(err)
	}
	out, notes, err := s.Render(context.Background(), "docs/board.md", docsync.RenderOptions{})
	if err != nil || len(notes) != 0 || !strings.Contains(string(out), "| Soon | 2026-09-05 |\n| Rotate keys | 2026-09-10 |") {
		t.Errorf("render = %s %v %v", out, notes, err)
	}
}
