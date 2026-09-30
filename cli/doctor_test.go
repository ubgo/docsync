package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ubgo/docsync"
)

// TestDoctorStatusesAreTheClosedSet pins that every row doctor prints, in a
// healthy repo and a degraded one, carries a status from DoctorStatusValues.
// The exit code is decided by comparing a row's status with doctorFail, so a
// row that spelled FAIL some other way would print as a failure and still
// exit 0 -- the fault a named constant exists to rule out.
func TestDoctorStatusesAreTheClosedSet(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	check := func(state string) {
		t.Helper()
		out := run(t, dir, v, "doctor").out
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		// tabwriter aligns every row, so the status column starts where the
		// first row's does; row names may hold spaces ("glob docs/**").
		col := strings.Index(lines[0], doctorOK)
		if col < 0 {
			t.Fatalf("%s: no status on the first row: %q", state, lines[0])
		}
		for _, l := range lines {
			if len(l) <= col {
				t.Errorf("%s: short row %q", state, l)
				continue
			}
			status, _, _ := strings.Cut(l[col:], " ")
			if !slices.Contains(DoctorStatusValues, status) {
				t.Errorf("%s: row %q has status %q, not one of %v", state, l, status, DoctorStatusValues)
			}
		}
	}
	check("healthy")
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"[\"]\ndocs = [\"nothing/**\"]\n")
	write(t, dir, ".ds/ledger.tsv", "garbage\n")
	check("degraded")
}

// TestOlderRuleHelpers pins the upgrade-path output, which no file can reach
// while rule 1 is the only released rule: doctor warns rather than fails,
// check and scan print through their format, and a newer rule or a match
// prints nothing here (a newer one was refused before any command ran).
func TestOlderRuleHelpers(t *testing.T) {
	t.Parallel()
	older := fmt.Errorf("%w: ledger.tsv says extract=1, this build implements rule 2", docsync.ErrOtherRule)
	newer := fmt.Errorf("%w: ledger.tsv says extract=3", docsync.ErrNewerRule)
	if row := ruleRow(older); len(row) != 3 || row[1] != doctorWarn || !strings.Contains(row[2], "ds scan") {
		t.Errorf("older row = %v", row)
	}
	if row := ruleRow(newer); len(row) != 3 || row[1] != doctorFail {
		t.Errorf("newer row = %v", row)
	}
	if row := ruleRow(nil); row != nil {
		t.Errorf("matching rule row = %v", row)
	}
	var b strings.Builder
	noteOlderRule(&b, older, "warning: %v; run `ds scan`")
	if !strings.HasPrefix(b.String(), "warning: ") || !strings.HasSuffix(b.String(), "run `ds scan`\n") || !strings.Contains(b.String(), "extract=1") {
		t.Errorf("older note = %q", b.String())
	}
	b.Reset()
	noteOlderRule(&b, newer, "%v")
	noteOlderRule(&b, nil, "%v")
	if b.Len() != 0 {
		t.Errorf("newer or matching rule printed %q", b.String())
	}
}
