package difflib

import (
	"math"
	"reflect"
	"testing"
)

func TestLines(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\nb", []string{"a", "b"}},
		{"a\n\n", []string{"a", ""}},
		{"\n", []string{""}},
	} {
		if got := Lines(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Lines(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		a, b []string
		want []Edit
	}{
		{"both empty", nil, nil, nil},
		{"equal", []string{"a", "b"}, []string{"a", "b"}, []Edit{{OpEqual, "a"}, {OpEqual, "b"}}},
		{"insert at end", []string{"a"}, []string{"a", "b"}, []Edit{{OpEqual, "a"}, {OpInsert, "b"}}},
		{"delete at end", []string{"a", "b"}, []string{"a"}, []Edit{{OpEqual, "a"}, {OpDelete, "b"}}},
		{"insert at start", []string{"b"}, []string{"a", "b"}, []Edit{{OpInsert, "a"}, {OpEqual, "b"}}},
		{"delete at start", []string{"a", "b"}, []string{"b"}, []Edit{{OpDelete, "a"}, {OpEqual, "b"}}},
		{"replace middle", []string{"a", "x", "c"}, []string{"a", "y", "c"}, []Edit{{OpEqual, "a"}, {OpDelete, "x"}, {OpInsert, "y"}, {OpEqual, "c"}}},
		{"all new", nil, []string{"a", "b"}, []Edit{{OpInsert, "a"}, {OpInsert, "b"}}},
		{"all gone", []string{"a", "b"}, nil, []Edit{{OpDelete, "a"}, {OpDelete, "b"}}},
		{"completely different", []string{"a", "b"}, []string{"c", "d"}, []Edit{{OpDelete, "a"}, {OpDelete, "b"}, {OpInsert, "c"}, {OpInsert, "d"}}},
		{"prefers delete when tie", []string{"a", "b", "c"}, []string{"b", "c", "a"}, []Edit{{OpDelete, "a"}, {OpEqual, "b"}, {OpEqual, "c"}, {OpInsert, "a"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Diff(tc.a, tc.b)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Diff = %v, want %v", got, tc.want)
			}
			// Applying the script to a must yield b.
			var rebuilt []string
			for _, e := range got {
				if e.Op != OpDelete {
					rebuilt = append(rebuilt, e.Line)
				}
			}
			if !reflect.DeepEqual(rebuilt, tc.b) && !(len(rebuilt) == 0 && len(tc.b) == 0) {
				t.Errorf("script does not rebuild b: %v vs %v", rebuilt, tc.b)
			}
		})
	}
}

func TestRatio(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b []string
		want float64
	}{
		{nil, nil, 1},
		{[]string{"a"}, nil, 0},
		{nil, []string{"a"}, 0},
		{[]string{"a", "b"}, []string{"a", "b"}, 1},
		{[]string{"a", "b", "c", "d"}, []string{"a", "b", "c", "x"}, 0.75},
		{[]string{"a"}, []string{"b"}, 0},
		{[]string{"a", "b"}, []string{"b"}, 2.0 / 3.0},
	} {
		if got := Ratio(tc.a, tc.b); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("Ratio(%v,%v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestCompactAndFormat(t *testing.T) {
	t.Parallel()
	edits := []Edit{{OpEqual, "1"}, {OpEqual, "2"}, {OpEqual, "3"}, {OpDelete, "x"}, {OpInsert, "y"}, {OpEqual, "4"}, {OpEqual, "5"}, {OpEqual, "6"}}
	if got := Compact(edits, 0); !reflect.DeepEqual(got, []Edit{{OpDelete, "x"}, {OpInsert, "y"}}) {
		t.Errorf("Compact(0) = %v", got)
	}
	if got := Compact(edits, 1); !reflect.DeepEqual(got, []Edit{{OpEqual, "3"}, {OpDelete, "x"}, {OpInsert, "y"}, {OpEqual, "4"}}) {
		t.Errorf("Compact(1) = %v", got)
	}
	if got := Compact(edits, -5); !reflect.DeepEqual(got, []Edit{{OpDelete, "x"}, {OpInsert, "y"}}) {
		t.Errorf("negative context must behave as 0: %v", got)
	}
	if got := Compact(edits, 100); !reflect.DeepEqual(got, edits) {
		t.Errorf("large context keeps everything: %v", got)
	}
	if got := Compact([]Edit{{OpEqual, "a"}}, 3); got != nil {
		t.Errorf("no changes compacts to nothing: %v", got)
	}
	if got := Format([]Edit{{OpEqual, "a"}, {OpDelete, "b"}, {OpInsert, "c"}}); got != " a\n-b\n+c\n" {
		t.Errorf("Format = %q", got)
	}
	if got := Format(nil); got != "" {
		t.Errorf("Format(nil) = %q", got)
	}
}

func TestUnified(t *testing.T) {
	t.Parallel()
	old := "func f() {\n\tif err := legacy.Save(); err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n"
	neu := "func f() {\n\tif err := sessions.Insert(); err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}\n"
	got := Unified(old, neu, 0)
	want := "-\tif err := legacy.Save(); err != nil {\n+\tif err := sessions.Insert(); err != nil {\n"
	if got != want {
		t.Errorf("Unified = %q, want %q", got, want)
	}
	if Unified("same\n", "same", 3) != "" {
		t.Error("identical texts must render empty")
	}
}
