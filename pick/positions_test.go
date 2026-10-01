package pick

import "testing"

// TestJSONPositions pins the lines a JSON pick reports to where the value is
// written in the source (bug 45): a remote def of a JSON key was recorded at
// line 1 whatever line the key was on. Duplicate keys resolve to the last,
// which is the one encoding/json keeps; escaped quotes inside strings and
// nested containers must not throw the walk off.
func TestJSONPositions(t *testing.T) {
	t.Parallel()
	doc := "{\n  \"name\": \"a \\\" } [ b\",\n  \"skip\": {\"x\": [1, {\"y\": \"]\"}]},\n  \"port\": 8080,\n  \"port\": 9090,\n  \"list\": [\n    \"zero\",\n    {\n      \"k\": true\n    }\n  ]\n}\n"
	for expr, want := range map[string][2]int{
		"json:port":      {5, 5},
		"json:list[1].k": {9, 9},
		"json:list[1]":   {8, 10},
		"json:list":      {6, 11},
		"json:name":      {2, 2},
		"json:skip":      {3, 3},
		"json:":          {1, 12},
	} {
		r, err := Pick(expr, doc)
		if err != nil || r.Start != want[0] || r.End != want[1] {
			t.Errorf("%s = %d-%d, %v; want %d-%d", expr, r.Start, r.End, err, want[0], want[1])
		}
	}
	if r, _ := Pick("json:port", doc); r.Value != "9090" {
		t.Errorf("duplicate key value = %q, want the last", r.Value)
	}
}
