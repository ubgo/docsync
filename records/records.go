// Package records is the first record source for `ds:table` and `ds:cfg
// query=` (docs/SPEC.md §9.5): a directory of markdown files whose YAML
// frontmatter holds the fields. Every file is one record; its top-level
// `key: value` pairs are the columns, plus `path` and `title` (the first
// heading). SQLite and HTTP sources are adapters that return the same rows.
package records

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/ubgo/docsync/internal/textnorm"
)

// Query keys a `ds:table` passes (§9.5).
const (
	KeyKind  = "kind"
	KeyWhere = "where"
	KeyCols  = "cols"
	KeySort  = "sort"
	KeyLimit = "limit"

	// Field names every record carries.
	FieldPath  = "path"
	FieldTitle = "title"
	FieldKind  = "kind"

	frontmatterFence = "---"
	whereAnd         = " and "
)

// Errors.
var (
	ErrBadWhere = errors.New("records: where= wants `field=value` or `field!=value` joined by ` and `")
	ErrBadLimit = errors.New("records: limit= must be a positive integer")
)

// Source answers table queries; it matches docsync.WithRecords.
type Source func(args map[string]string) ([]map[string]string, error)

// Frontmatter builds a source over dir in fsys. Files are read on every
// query so an edit shows on the next render; the directory is small by
// construction (it is records, not prose).
func Frontmatter(fsys fs.FS, dir string) Source {
	return func(args map[string]string) ([]map[string]string, error) {
		rows, err := load(fsys, dir)
		if err != nil {
			return nil, err
		}
		return Apply(rows, args)
	}
}

// load reads every markdown file under dir into a record.
func load(fsys fs.FS, dir string) ([]map[string]string, error) {
	var rows []map[string]string
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(p), ".md") {
			return nil
		}
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rows = append(rows, Parse(p, string(raw)))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][FieldPath] < rows[j][FieldPath] })
	return rows, nil
}

// Parse reads one file's top-level frontmatter fields, its path, and its
// first heading as title. Nested YAML is ignored: a record is flat.
func Parse(p, src string) map[string]string {
	rec := map[string]string{FieldPath: p}
	// A byte order mark in front of the opening fence hid the front matter,
	// and every field of a file saved by a Windows editor went missing.
	src = string(textnorm.TrimBOM([]byte(src)))
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	i := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == frontmatterFence {
		for i = 1; i < len(lines); i++ {
			t := strings.TrimSpace(lines[i])
			if t == frontmatterFence || t == "..." {
				i++
				break
			}
			if t == "" || strings.HasPrefix(t, "#") || lines[i][0] == ' ' || lines[i][0] == '\t' || strings.HasPrefix(t, "- ") {
				continue
			}
			k, v, ok := strings.Cut(t, ":")
			if !ok || strings.TrimSpace(v) == "" {
				// No value means a nested mapping or list follows; a record is
				// flat, so the parent key carries nothing.
				continue
			}
			rec[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	if _, ok := rec[FieldTitle]; !ok {
		for ; i < len(lines); i++ {
			if t := strings.TrimSpace(lines[i]); strings.HasPrefix(t, "#") {
				rec[FieldTitle] = strings.TrimSpace(strings.TrimLeft(t, "#"))
				break
			}
		}
	}
	if _, ok := rec[FieldTitle]; !ok {
		rec[FieldTitle] = strings.TrimSuffix(path.Base(p), path.Ext(p))
	}
	return rec
}

// Apply filters, sorts, projects, and limits rows per the query args.
func Apply(rows []map[string]string, args map[string]string) ([]map[string]string, error) {
	var out []map[string]string
	conds, err := parseWhere(args[KeyWhere])
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if k := args[KeyKind]; k != "" && r[FieldKind] != k {
			continue
		}
		if !conds.match(r) {
			continue
		}
		out = append(out, r)
	}
	if s := args[KeySort]; s != "" {
		desc := strings.HasPrefix(s, "-")
		field := strings.TrimPrefix(s, "-")
		sort.SliceStable(out, func(i, j int) bool {
			if desc {
				return out[i][field] > out[j][field]
			}
			return out[i][field] < out[j][field]
		})
	}
	if l := args[KeyLimit]; l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%w: %q", ErrBadLimit, l)
		}
		if len(out) > n {
			out = out[:n]
		}
	}
	if cols := args[KeyCols]; cols != "" {
		keep := strings.Split(cols, ",")
		for i, r := range out {
			proj := map[string]string{}
			for _, c := range keep {
				c = strings.TrimSpace(c)
				proj[c] = r[c]
			}
			out[i] = proj
		}
	}
	return out, nil
}

type condition struct {
	field, value string
	negate       bool
}

type conditions []condition

func (cs conditions) match(r map[string]string) bool {
	for _, c := range cs {
		if (r[c.field] == c.value) == c.negate {
			return false
		}
	}
	return true
}

// parseWhere reads `a=b and c!=d`.
func parseWhere(s string) (conditions, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out conditions
	for _, part := range strings.Split(s, whereAnd) {
		part = strings.TrimSpace(part)
		if f, v, ok := strings.Cut(part, "!="); ok && f != "" {
			out = append(out, condition{field: strings.TrimSpace(f), value: strings.TrimSpace(v), negate: true})
			continue
		}
		if f, v, ok := strings.Cut(part, "="); ok && f != "" {
			out = append(out, condition{field: strings.TrimSpace(f), value: strings.TrimSpace(v)})
			continue
		}
		return nil, fmt.Errorf("%w: %q", ErrBadWhere, part)
	}
	return out, nil
}
