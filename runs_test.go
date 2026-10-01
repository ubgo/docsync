package docsync

import (
	"testing"

	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/extract"
)

// TestReportApplyRuns pins the library half of bug 29: a run the caller
// executed and saw fail becomes a `run failed` finding, and the report's
// states, severity summary and exit code are recounted with it, so a caller
// printing the summary cannot show only passing findings for a failed run.
func TestReportApplyRuns(t *testing.T) {
	t.Parallel()
	rep := Report{Findings: []check.Finding{{Doc: "r.md", Line: 1, Verb: extract.VerbRun, State: check.StateOK, Severity: check.SeverityNone}}}
	rep.Summary = map[check.Severity]int{check.SeverityNone: 1}
	rep = rep.ApplyRuns([]check.RunResult{{Doc: "r.md", Line: 1, Command: "false", Failed: true}})
	if rep.ExitCode != 1 || rep.States[check.StateRunFailed] != 1 || rep.Summary[check.SeverityError] != 1 || rep.Summary[check.SeverityNone] != 0 {
		t.Errorf("recounted report = %+v", rep)
	}
}
