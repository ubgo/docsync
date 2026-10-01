package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestCustomNameReachesEveryMessage pins bug 126: a binary built with
// WithName("pds") named itself in help but printed `ds` in its version
// line, in every finding's remedy, and in the commands its errors and
// doctor rows tell the reader to run. Each of those now says `pds`, and a
// directive (`ds:block`) in the same output is left alone.
func TestCustomNameReachesEveryMessage(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	pds := func(args ...string) (string, string) {
		t.Helper()
		var out, errb bytes.Buffer
		Run(args, WithDir(dir), WithIO(strings.NewReader(""), &out, &errb), WithVCS(v), WithClock(func() time.Time { return clock }), WithName("pds"))
		return out.String(), errb.String()
	}
	if out, _ := pds("version"); !strings.HasPrefix(out, "pds ") {
		t.Errorf("version = %q, want it to start with pds", out)
	}
	if out, _ := pds("--version"); !strings.HasPrefix(out, "pds ") {
		t.Errorf("--version = %q, want it to start with pds", out)
	}
	pds("scan")
	write(t, dir, "internal/store/write.go", goV2)
	pds("scan")
	out, _ := pds("check")
	if !strings.Contains(out, "pds ack") || strings.Contains(out, " ds ack") {
		t.Errorf("check remedies must say pds ack, not ds ack:\n%s", out)
	}
	// An error that names a command: init inside an existing root says to
	// run the binary there.
	sub := dir + "/internal"
	var errb bytes.Buffer
	Run([]string{"init"}, WithDir(sub), WithIO(strings.NewReader(""), &bytes.Buffer{}, &errb), WithVCS(v), WithName("pds"))
	if e := errb.String(); !strings.Contains(e, "run pds there") {
		t.Errorf("nested init error = %q, want it to say run pds there", e)
	}
	write(t, dir, ".ds/ledger.tsv", "garbage\n")
	if row, _ := pds("doctor"); strings.Contains(row, "`ds ") || strings.Contains(row, " ds scan") {
		t.Errorf("doctor rows must name pds:\n%s", row)
	}
}
