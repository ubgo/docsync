package check

import (
	"testing"

	"github.com/ubgo/docsync/block"
)

// TestStrictPromotesOnlyWarnings pins §17's strict rule over every state, so a
// state added later is held to it without anyone remembering to: `--strict`
// turns a warning into an error, touches nothing else, and never promotes a
// state in neverStrict -- `moved` and `deprecated` above all, which the spec
// says never affect the exit code.
// promise:strict-moved
func TestStrictPromotesOnlyWarnings(t *testing.T) {
	t.Parallel()
	for _, st := range StateValues {
		plain, strict := &runner{}, &runner{in: Input{Opts: Options{Strict: true}}}
		plain.emit(Finding{State: st})
		strict.emit(Finding{State: st})
		before, after := plain.out[0].Severity, strict.out[0].Severity
		want := before
		if before == SeverityWarning && !neverStrict[st] {
			want = SeverityError
		}
		if after != want {
			t.Errorf("%s: strict severity %s, want %s (plain %s)", st, after, want, before)
		}
	}
	for _, st := range []State{StateMoved, StateDeprecated} {
		r := &runner{in: Input{Opts: Options{Strict: true, UnackedIsWarning: true}}}
		r.emit(Finding{State: st})
		if r.out[0].Severity == SeverityError {
			t.Errorf("%s became an error under --strict; §17 says it never affects the exit code", st)
		}
	}
}

// TestStrictDeprecatedExitsZero is the same promise through Run: a cited def
// past its deprecation date, and nothing else wrong, exits 0 with --strict.
// promise:strict-moved
func TestStrictDeprecatedExitsZero(t *testing.T) {
	t.Parallel()
	d := def("old-a2b6f8jk", "x.go", 3, "var Old = 1", map[string]string{"deprecated": "2026-01-01"})
	rep := Run(Input{Repo: "api", Now: now, Defs: []block.Block{d}, Refs: []block.Reference{ref("block", d.ID, "d.md", 1, nil)}, Opts: Options{Strict: true}})
	if rep.Summary[StateDeprecated] == 0 {
		t.Fatalf("no deprecated finding: %+v", rep.Findings)
	}
	if rep.ExitCode != 0 {
		t.Errorf("--strict with only a deprecation exited %d: %+v", rep.ExitCode, rep.Findings)
	}
}
