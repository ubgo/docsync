package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/docsync"
)

// GitHub issue 4: in repo mode `ds refresh` skipped a copy whose lines= ran
// past the block and said only "0 repo-mode copies rewritten"; check called
// it stale, told the reader to run refresh, and ack did not clear it. Now
// refresh names each skipped copy and why, the same on every run and under
// --dry-run; check reports range as an error that ack does not hide; and a
// fixed directive is written on the next refresh and checks clean
// (bug 136).
func TestRefreshNamesSkippedCopies(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[include]\nmode = \"repo\"\n")
	page := "# Save\n\n<!-- ds:block id=sess-save-k7m2p4xq lines=1-4 -->\n<!-- /ds:block hash=000000 -->\n\n<!-- ds:block id=sess-save-k7m2p4xq lines=abc -->\n"
	write(t, dir, "docs/save.md", page)
	const report = "skipped docs/save.md:3: lines=1-4 is outside the block's 3 lines\n" +
		"skipped docs/save.md:6: lines=abc is not a line range (want a or a-b, counting from 1)\n" +
		"2 repo-mode copies skipped\n"

	dry := run(t, dir, v, "refresh", "--dry-run")
	if dry.code != 0 || !strings.Contains(dry.out, report) {
		t.Errorf("dry run = %+v", dry)
	}
	first := run(t, dir, v, "refresh")
	if first.code != 0 || !strings.Contains(first.out, "repo-mode copies rewritten\n"+report) {
		t.Fatalf("refresh = %+v", first)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "docs/save.md"))
	if !strings.Contains(string(got), "<!-- ds:block id=sess-save-k7m2p4xq lines=1-4 -->\n<!-- /ds:block hash=000000 -->\n") {
		t.Errorf("a skipped copy was touched:\n%s", got)
	}
	// Idempotent: the same report and not a byte changed.
	second := run(t, dir, v, "refresh")
	again, _ := os.ReadFile(filepath.Join(dir, "docs/save.md"))
	if second.code != 0 || !strings.Contains(second.out, "0 repo-mode copies rewritten\n"+report) || string(again) != string(got) {
		t.Errorf("second refresh = %+v\n%s", second, again)
	}

	if r := run(t, dir, v, "ack", "sess-save-k7m2p4xq", "--doc", "docs/save.md", "--line", "3", "--note", "ok"); r.code != 0 {
		t.Fatalf("ack = %+v", r)
	}
	ck := run(t, dir, v, "check")
	if ck.code != ExitFindings || !strings.Contains(ck.out, "error    range              lines=1-4 outside the block's 3 lines") || !strings.Contains(ck.out, "fix: adjust lines= at docs/save.md:3 to fit the block's 3 lines") || strings.Contains(ck.out, "stale") {
		t.Errorf("check after ack = %+v", ck)
	}

	// Fixed, the copy is written and both lines check clean.
	write(t, dir, "docs/save.md", strings.NewReplacer("lines=1-4", "lines=1-3", "lines=abc", "lines=2").Replace(string(got)))
	if r := run(t, dir, v, "refresh"); r.code != 0 || !strings.Contains(r.out, "2 repo-mode copies rewritten\n") || strings.Contains(r.out, "skipped") {
		t.Errorf("refresh after the fix = %+v", r)
	}
	if r := run(t, dir, v, "check", "--json"); strings.Contains(r.out, `"doc": "docs/save.md"`) && (strings.Contains(r.out, `"state": "range"`) || strings.Contains(r.out, `"state": "stale"`)) {
		t.Errorf("check after the fix = %+v", r)
	}
}

// A copy written with only a link (an at= with no snapshot, or a block over
// max_lines) is reported as such; nothing is counted as skipped.
func TestRefreshNamesLinkOnlyCopies(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[include]\nmode = \"repo\"\nmax_lines = 2\n")
	write(t, dir, "docs/save.md", "<!-- ds:block id=sess-save-k7m2p4xq -->\n")
	r := run(t, dir, v, "refresh")
	if r.code != 0 || !strings.Contains(r.out, "2 repo-mode copies rewritten\nlink only docs/save.md:1: sess-save-k7m2p4xq renders 3 lines, over the cap of 2; cite it instead\nlink only docs/sessions.md:3: ") || strings.Contains(r.out, "skipped") {
		t.Errorf("link-only refresh = %+v", r)
	}
}

// printCopyNotes writes one line per note, labelled from copyLabelValues,
// counts only the skipped ones, and writes nothing for no notes (bug 136).
func TestPrintCopyNotes(t *testing.T) {
	t.Parallel()
	var none bytes.Buffer
	printCopyNotes(&none, nil)
	if none.Len() != 0 {
		t.Errorf("no notes printed %q", none.String())
	}
	var out bytes.Buffer
	printCopyNotes(&out, []docsync.CopyNote{
		{Doc: "d.md", Line: 3, ID: "a-k7m2p4xq", Skipped: true, Reason: "lines=1-9 is outside the block's 3 lines"},
		{Doc: "d.md", Line: 7, ID: "a-k7m2p4xq", Reason: "no snapshot for a-k7m2p4xq at deadbeef; rendering the link only"},
	})
	want := copyLabelValues[0] + " d.md:3: lines=1-9 is outside the block's 3 lines\n" +
		copyLabelValues[1] + " d.md:7: no snapshot for a-k7m2p4xq at deadbeef; rendering the link only\n" +
		"1 repo-mode copies skipped\n"
	if out.String() != want {
		t.Errorf("printed:\n%s\nwant:\n%s", out.String(), want)
	}
}
