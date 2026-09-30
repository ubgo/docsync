package sqlite

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
)

// fakeDriver injects the failures a real database only produces under
// load: an unscannable value and an error mid-iteration.
type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (fakeConn) Close() error                        { return nil }
func (fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }

func (fakeConn) Query(query string, _ []driver.Value) (driver.Rows, error) {
	switch {
	case strings.HasSuffix(query, "unscannable"):
		return &fakeRows{vals: []driver.Value{struct{ X int }{1}}}, nil
	case strings.HasSuffix(query, "midway"):
		return &fakeRows{vals: []driver.Value{"ok"}, failAfter: true}, nil
	}
	return nil, errors.New("no such table")
}

type fakeRows struct {
	vals      []driver.Value
	failAfter bool
	n         int
}

func (r *fakeRows) Columns() []string { return []string{"c"} }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	r.n++
	if r.n == 1 {
		copy(dest, r.vals)
		return nil
	}
	if r.failAfter {
		return errors.New("connection dropped")
	}
	return io.EOF
}

func init() { sql.Register("fakesqlite", fakeDriver{}) }

func TestDriverFailures(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("fakesqlite", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	src := Source(db, "")
	if _, err := src(map[string]string{"kind": "unscannable"}); err == nil {
		t.Error("scan failure must surface")
	}
	if _, err := src(map[string]string{"kind": "midway"}); err == nil || !strings.Contains(err.Error(), "dropped") {
		t.Errorf("iteration failure = %v", err)
	}
}
