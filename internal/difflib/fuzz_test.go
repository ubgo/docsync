package difflib

import (
	"strings"
	"testing"
)

// FuzzDiff holds the properties the classifier and triage rely on: the edit
// script rebuilds both inputs exactly, Compact keeps every change, and Ratio
// is 1 for identical inputs, symmetric, and within [0, 1].
func FuzzDiff(f *testing.F) {
	for _, s := range [][2]string{{"a\nb\nc", "a\nx\nc"}, {"", ""}, {"a", ""}, {"", "a\nb"}, {"a\na\na", "a"}, {"x\ny", "y\nx"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, x, y string) {
		a, b := Lines(x), Lines(y)
		edits := Diff(a, b)
		var left, right []string
		changes := 0
		for _, e := range edits {
			switch e.Op {
			case OpEqual:
				left, right = append(left, e.Line), append(right, e.Line)
			case OpDelete:
				left = append(left, e.Line)
				changes++
			case OpInsert:
				right = append(right, e.Line)
				changes++
			}
		}
		if strings.Join(left, "\n") != strings.Join(a, "\n") || len(left) != len(a) {
			t.Fatalf("the script does not rebuild the old side:\n%q\n%q", left, a)
		}
		if strings.Join(right, "\n") != strings.Join(b, "\n") || len(right) != len(b) {
			t.Fatalf("the script does not rebuild the new side:\n%q\n%q", right, b)
		}
		kept := 0
		for _, e := range Compact(edits, 0) {
			if e.Op != OpEqual {
				kept++
			}
		}
		if kept != changes {
			t.Fatalf("Compact kept %d of %d changes", kept, changes)
		}
		r1, r2 := Ratio(a, b), Ratio(b, a)
		if r1 != r2 || r1 < 0 || r1 > 1 {
			t.Fatalf("Ratio = %v / %v, want symmetric in [0,1]", r1, r2)
		}
		if Ratio(a, a) != 1 {
			t.Fatalf("Ratio(a, a) = %v", Ratio(a, a))
		}
	})
}
