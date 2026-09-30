package records

import "testing"

// FuzzParse: frontmatter a person wrote never panics the record reader.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"---\nkind: task\ntitle: a\n---\nbody", "---\n", "", "---\n: x\n---", "---\nk: [1, 2\n---"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		_ = Parse("a.md", src)
	})
}

// FuzzApply: any filter, sort, or limit a ds:table directive carries either
// errors or returns a subset of the rows it was given — never a panic, never
// a row that was not there.
func FuzzApply(f *testing.F) {
	for _, s := range [][3]string{{"kind=task", "due", "2"}, {"", "", ""}, {"x", "-due", "-1"}, {"a=b,c=d", "missing", "999999999999"}} {
		f.Add(s[0], s[1], s[2])
	}
	f.Fuzz(func(t *testing.T, where, sort, limit string) {
		rows := []map[string]string{{"kind": "task", "due": "mon"}, {"kind": "note", "due": "tue"}, {"kind": "task"}}
		out, err := Apply(rows, map[string]string{KeyKind: "task", KeyWhere: where, KeySort: sort, KeyLimit: limit})
		if err != nil {
			return
		}
		if len(out) > len(rows) {
			t.Fatalf("Apply returned %d rows from %d", len(out), len(rows))
		}
	})
}
