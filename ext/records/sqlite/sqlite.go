// Package sqlite is the SQLite record source for `ds:table` and `ds:cfg
// query=` (docs/SPEC.md §9.5, §37.1): rows of a table become records with
// one field per column. Filtering, sorting, projection, and limits are
// applied by the root records package, so every source answers the same
// query language; the database only supplies rows.
//
// The driver is modernc.org/sqlite, pure Go, so a `ds` binary with this
// source needs no C toolchain.
package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/ubgo/docsync/records"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// DriverName is the database/sql driver this package registers.
const DriverName = "sqlite"

// ErrBadTable rejects a table name that is not a plain identifier. Table
// names cannot be bound as parameters, so they are validated instead of
// quoted; a `ds:table kind=` in a doc is user input.
var ErrBadTable = errors.New("sqlite records: table must be an identifier")

// ErrNoTable is returned when neither kind= nor a default table names one.
var ErrNoTable = errors.New("sqlite records: no table: set kind= on the directive or table in [records]")

var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Open opens the database file read-only and returns a source over it plus
// the handle to close when done. defaultTable answers queries without a
// kind=; "" means kind= is required.
func Open(path, defaultTable string) (records.Source, io.Closer, error) {
	// sql.Open fails only for an unregistered driver name; this package
	// registers DriverName by importing it, so Ping is the first real
	// failure point.
	db, _ := sql.Open(DriverName, "file:"+path+"?mode=ro")
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("sqlite records: %s: %w", path, err)
	}
	return Source(db, defaultTable), db, nil
}

// Source wraps an open handle. kind= selects the table; the rest of the
// query is applied to the rows in memory by records.Apply.
func Source(db *sql.DB, defaultTable string) records.Source {
	return func(args map[string]string) ([]map[string]string, error) {
		table := args[records.KeyKind]
		if table == "" {
			table = defaultTable
		}
		if table == "" {
			return nil, ErrNoTable
		}
		if !identRE.MatchString(table) {
			return nil, fmt.Errorf("%w: %q", ErrBadTable, table)
		}
		rows, err := db.Query("SELECT * FROM " + table)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		// Columns errors only on a closed Rows; this one was just opened.
		cols, _ := rows.Columns()
		var out []map[string]string
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return nil, err
			}
			rec := map[string]string{}
			for i, c := range cols {
				rec[c] = vals[i].String
			}
			out = append(out, rec)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		// kind= chose the table; it must not also filter a column the
		// table may not have.
		filtered := map[string]string{}
		for k, v := range args {
			if k != records.KeyKind {
				filtered[k] = v
			}
		}
		return records.Apply(out, filtered)
	}
}
