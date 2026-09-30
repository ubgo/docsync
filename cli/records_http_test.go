package cli

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/docsync/ext/records/sqlite"
	"github.com/ubgo/docsync/pick"

	"github.com/ubgo/docsync/records"
)

func TestHTTPRecordsSource(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rows":
			if r.URL.Query().Get("kind") != "task" {
				t.Errorf("query not forwarded: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"kind":"task","title":"a","n":1},{"kind":"note","title":"b"}]`))
		case "/bad":
			_, _ = w.Write([]byte("not json"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	rows, err := httpRecords(srv.Client(), srv.URL+"/rows?token=x")(map[string]string{records.KeyKind: "task"})
	if err != nil || len(rows) != 1 || rows[0]["n"] != "1" {
		t.Errorf("rows = %v %v", rows, err)
	}
	if _, err := httpRecords(srv.Client(), srv.URL+"/bad")(nil); !errors.Is(err, ErrHTTPRecords) {
		t.Errorf("bad json = %v", err)
	}
	if _, err := httpRecords(srv.Client(), srv.URL+"/missing")(nil); !errors.Is(err, ErrHTTPRecords) {
		t.Errorf("404 = %v", err)
	}
	if _, err := httpRecords(srv.Client(), "://bad")(nil); err == nil {
		t.Error("bad endpoint")
	}
	if _, err := httpRecords(srv.Client(), "http://127.0.0.1:1/x")(nil); err == nil {
		t.Error("unreachable")
	}
}

func TestSQLiteRecords(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	db, err := sql.Open(sqlite.DriverName, filepath.Join(dir, "records.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"CREATE TABLE tasks (title TEXT, state TEXT)", "INSERT INTO tasks VALUES ('Rotate keys', 'open')", "INSERT INTO tasks VALUES ('Old', 'done')"} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[records]\nsource = \"sqlite\"\npath = \"records.db\"\ntable = \"tasks\"\n")
	write(t, dir, "docs/board.md", "<!-- ds:table where=\"state=open\" cols=title -->\n")
	r := run(t, dir, v, "render", "docs/board.md")
	if r.code != 0 || !strings.Contains(r.out, "| Rotate keys |") {
		t.Errorf("sqlite table = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[records]\nsource = \"sqlite\"\npath = \"missing.db\"\n")
	if r := run(t, dir, v, "render", "docs/board.md"); r.code != ExitError || !strings.Contains(r.err, "missing.db") {
		t.Errorf("missing db = %+v", r)
	}
}

// upperPicker is a Go pick scheme registered through cli.WithPicker.
type upperPicker struct{}

func (upperPicker) Scheme() string { return "upper" }
func (upperPicker) Pick(arg, content string) (pick.Result, error) {
	return pick.Result{Kind: pick.KindValue, Value: strings.ToUpper(strings.TrimSpace(content)), Start: 1, End: 1}, nil
}

func TestWithPicker(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "VERSION", "1.2.3\n")
	write(t, dir, "docs/v.md", "<!-- ds:def id=ver-a2b6f8jk file=VERSION pick=upper: -->\n\nVersion [x](ds:cfg?id=ver-a2b6f8jk).\n")
	var out, errb strings.Builder
	code := Run([]string{"render", "docs/v.md"}, WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithPicker(upperPicker{}))
	if code != 0 || !strings.Contains(out.String(), "Version 1.2.3.") {
		t.Errorf("picker = %d %s %s", code, out.String(), errb.String())
	}
}
