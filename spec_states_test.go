package docsync

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/ubgo/docsync/check"
)

// specFindingsHeading opens the findings table's section in docs/SPEC.md.
const specFindingsHeading = "### 17. Findings"

// stateCellRE takes one backticked state out of a table's first column.
var stateCellRE = regexp.MustCompile("`([^`]+)`")

// TestSpecFindingsTableIsStateValues pins bug 124: the findings table in
// SPEC §17 lists exactly check.StateValues, each state once, spelled as
// check prints it, with the severity check.SeverityOf gives it. The table
// had drifted three ways at once: `skipped` and `problem` were missing,
// `url moved` was listed as `moved`, and `undocumented export` appeared a
// second time as `undocumented`, a state the tool never prints.
//
// A row may name several states as "`a` / `b`", with one severity for all
// or one per state in the same order.
func TestSpecFindingsTableIsStateValues(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("docs/SPEC.md")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[check.State]int{}
	for _, row := range findingsRows(t, string(raw)) {
		states := strings.Split(row[0], " / ")
		sevs := strings.Split(row[2], " / ")
		for i, cell := range states {
			m := stateCellRE.FindStringSubmatch(cell)
			if m == nil {
				t.Errorf("findings row %q: %q is not a backticked state", row[0], cell)
				continue
			}
			st := check.State(m[1])
			listed[st]++
			sev := sevs[0]
			if len(sevs) == len(states) {
				sev = sevs[i]
			}
			if want := string(check.SeverityOf(st)); strings.Trim(strings.Fields(sev)[0], ",") != want {
				t.Errorf("SPEC §17 gives %q severity %q; check gives %q", st, sev, want)
			}
		}
	}
	known := map[check.State]bool{}
	for _, st := range check.StateValues {
		known[st] = true
		if listed[st] != 1 {
			t.Errorf("SPEC §17 lists state %q %d times, want once", st, listed[st])
		}
	}
	for st := range listed {
		if !known[st] {
			t.Errorf("SPEC §17 lists %q, which is not in check.StateValues", st)
		}
	}
}

// findingsRows returns the cells of each data row of the first table under
// the §17 heading.
func findingsRows(t *testing.T, spec string) [][]string {
	t.Helper()
	i := strings.Index(spec, specFindingsHeading)
	if i < 0 {
		t.Fatalf("SPEC has no %q", specFindingsHeading)
	}
	var rows [][]string
	inTable := false
	for _, line := range strings.Split(spec[i:], "\n")[1:] {
		if !strings.HasPrefix(line, "|") {
			if inTable {
				break
			}
			continue
		}
		inTable = true
		cells := strings.Split(strings.Trim(line, "|"), " | ")
		if len(cells) < 3 || strings.HasPrefix(cells[0], "---") || strings.TrimSpace(cells[0]) == "Finding" {
			continue
		}
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		rows = append(rows, cells)
	}
	if len(rows) < 10 {
		t.Fatalf("found %d rows in the §17 table; the parser is broken", len(rows))
	}
	return rows
}
