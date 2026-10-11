package docsync

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
)

// repoModeSys is newSys with [include] mode = "repo".
func repoModeSys(t *testing.T, fsys fstest.MapFS) *System {
	t.Helper()
	c := cfg()
	c.Include.Mode = config.IncludeRepo
	return newSys(t, fsys, WithConfig(c))
}

// fencesDoc cites the 3-line sess-save block every way refresh cannot fill
// a copy, plus one it can only fill with a link and one it fills whole.
const fencesDoc = "# R\n\n" +
	"<!-- ds:block id=sess-save-k7m2p4xq lines=1-4 -->\n" + // 3: one past the end
	"<!-- ds:block id=sess-save-k7m2p4xq lines=abc -->\n" + // 4: not a range
	"<!-- ds:block id=nope-a2b6f8jk -->\n" + // 5: not defined
	"<!-- ds:block id=op-stripe-key-p9c2v7ld -->\n" + // 6: a secret
	"<!-- ds:block id=sess-save-k7m2p4xq at=deadbeef -->\n" + // 7: no snapshot: link only
	"<!-- ds:block id=sess-save-k7m2p4xq lines=1-3 -->\n" // 8: fits

// Refresh never skips a repo-mode copy silently: FencesReport names every
// copy it could not write and every one it wrote with only a link, with the
// reason, in line order; check reports each skipped one as what it is and
// never as stale; a second run reports the same and changes nothing; and
// once the directive is fixed the copy is written and checks clean (bugs
// 136 and 138, GitHub issue 4).
//
// promise:repo-copy-not-stale
func TestFencesReportNamesEverySkippedCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fsys := repo(false)
	fsys["docs/repo.md"] = &fstest.MapFile{Data: []byte(fencesDoc)}
	s := repoModeSys(t, fsys)
	res, _ := s.Scan(ctx)
	out, n, notes := s.FencesReport(res, "docs/repo.md", fsys["docs/repo.md"].Data)
	if n != 2 {
		t.Fatalf("written = %d\n%s", n, out)
	}
	want := []CopyNote{
		{Doc: "docs/repo.md", Line: 3, ID: "sess-save-k7m2p4xq", Skipped: true, Reason: "lines=1-4 is outside the block's 3 lines"},
		{Doc: "docs/repo.md", Line: 4, ID: "sess-save-k7m2p4xq", Skipped: true, Reason: "lines=abc is not a line range (want a or a-b, counting from 1)"},
		{Doc: "docs/repo.md", Line: 5, ID: "nope-a2b6f8jk", Skipped: true, Reason: "nope-a2b6f8jk is not defined"},
		{Doc: "docs/repo.md", Line: 6, ID: "op-stripe-key-p9c2v7ld", Skipped: true, Reason: "op-stripe-key-p9c2v7ld is a secret; ds:block refuses to render it"},
		{Doc: "docs/repo.md", Line: 7, ID: "sess-save-k7m2p4xq", Skipped: false, Reason: "no snapshot for sess-save-k7m2p4xq at deadbeef; rendering the link only"},
	}
	if len(notes) != len(want) {
		t.Fatalf("notes = %+v", notes)
	}
	for i := range want {
		if notes[i] != want[i] {
			t.Errorf("note %d = %+v, want %+v", i, notes[i], want[i])
		}
	}
	// Fences is FencesReport without the notes.
	if plain, pn := s.Fences(res, "docs/repo.md", fsys["docs/repo.md"].Data); pn != n || string(plain) != string(out) {
		t.Errorf("Fences disagrees with FencesReport: %d", pn)
	}

	// Written back: check names each skipped copy for what it is.
	fsys["docs/repo.md"] = &fstest.MapFile{Data: out}
	s2 := repoModeSys(t, fsys)
	rep, _ := s2.Check(ctx, CheckOptions{})
	got := map[int]check.Finding{}
	for _, f := range rep.Findings {
		if f.Doc == "docs/repo.md" {
			if f.State == check.StateStale || f.State == check.StateTampered {
				t.Errorf("a copy refresh cannot write is reported %s: %+v", f.State, f)
			}
			got[f.Line] = f
		}
	}
	for line, st := range map[int]check.State{3: check.StateRange, 4: check.StateRange, 5: check.StateBroken, 6: check.StateProblem} {
		if got[line].State != st || got[line].Severity != check.SeverityError {
			t.Errorf("line %d = %s %s, want %s error", line, got[line].State, got[line].Severity, st)
		}
	}

	// A second refresh is a fixed point: the same report, the same bytes.
	res2, _ := s2.Scan(ctx)
	again, n2, notes2 := s2.FencesReport(res2, "docs/repo.md", out)
	if n2 != 0 || string(again) != string(out) || len(notes2) != len(want) {
		t.Errorf("second refresh: %d written, %d notes", n2, len(notes2))
	}
	for i := range notes2 {
		if notes2[i] != want[i] {
			t.Errorf("second refresh note %d = %+v", i, notes2[i])
		}
	}

	// Fix the range: the next refresh writes the copy and check is clean.
	fixed := strings.Replace(string(out), "lines=1-4 -->", "lines=1-2 -->", 1)
	fsys["docs/repo.md"] = &fstest.MapFile{Data: []byte(fixed)}
	s3 := repoModeSys(t, fsys)
	res3, _ := s3.Scan(ctx)
	written, n3, _ := s3.FencesReport(res3, "docs/repo.md", []byte(fixed))
	if n3 != 1 || !strings.Contains(string(written), "lines=1-2 -->\n**Store.Save**") {
		t.Fatalf("refresh after fixing lines= wrote %d:\n%s", n3, written)
	}
	fsys["docs/repo.md"] = &fstest.MapFile{Data: written}
	rep4, _ := repoModeSys(t, fsys).Check(ctx, CheckOptions{})
	for _, f := range rep4.Findings {
		if f.Doc == "docs/repo.md" && f.Line == 3 && f.State != check.StateOK {
			t.Errorf("the fixed copy = %s %s", f.State, f.Message)
		}
	}
}

// A repo-mode copy is rendered from the def check compares it with: the
// one its env= names. Fences took the first def of the id, so a copy citing
// env=prod could never be written and check called it stale forever
// (bug 137).
func TestFencesResolvesEnvLikeCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fsys := repo(false)
	fsys["config/dev.yaml"] = &fstest.MapFile{Data: []byte("port: 8080 # ds:def id=port-k7m2p4xq env=dev\n")}
	fsys["config/prod.yaml"] = &fstest.MapFile{Data: []byte("port: 443 # ds:def id=port-k7m2p4xq env=prod\n")}
	doc := "# R\n\n<!-- ds:block id=port-k7m2p4xq env=prod -->\n\n<!-- ds:block id=port-k7m2p4xq env=staging -->\n"
	fsys["docs/repo.md"] = &fstest.MapFile{Data: []byte(doc)}
	s := repoModeSys(t, fsys)
	res, _ := s.Scan(ctx)
	out, n, notes := s.FencesReport(res, "docs/repo.md", []byte(doc))
	if n != 1 || !strings.Contains(string(out), "```yaml\n443\n```") || strings.Contains(string(out), "8080") {
		t.Fatalf("the prod copy (%d):\n%s", n, out)
	}
	if len(notes) != 1 || notes[0].Line != 5 || !notes[0].Skipped || notes[0].Reason != "port-k7m2p4xq is not defined for env=staging" {
		t.Errorf("notes = %+v", notes)
	}
	fsys["docs/repo.md"] = &fstest.MapFile{Data: out}
	rep, _ := repoModeSys(t, fsys).Check(ctx, CheckOptions{})
	for _, f := range rep.Findings {
		if f.Doc == "docs/repo.md" && f.Line == 3 && f.State != check.StateOK {
			t.Errorf("the prod copy = %s %s", f.State, f.Message)
		}
		if f.Doc == "docs/repo.md" && f.Line == 5 && f.State != check.StateBroken {
			t.Errorf("the staging copy = %s %s", f.State, f.Message)
		}
	}
}

// In build mode FencesReport adds no copy, but brings one already in the
// page up to date, since check compares it in either mode; Fences, the
// older entry point, still writes every copy whatever the mode (bug 141).
func TestFencesReportInBuildModeKeepsExistingCopiesOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fsys := repo(false)
	doc := "# R\n\n<!-- ds:block id=sess-save-k7m2p4xq -->\n<!-- /ds:block hash=000000 -->\n\n<!-- ds:block id=sess-save-k7m2p4xq lines=1-2 -->\n"
	fsys["docs/repo.md"] = &fstest.MapFile{Data: []byte(doc)}
	s := newSys(t, fsys)
	res, _ := s.Scan(ctx)
	out, n, notes := s.FencesReport(res, "docs/repo.md", []byte(doc))
	if n != 1 || len(notes) != 0 || strings.Contains(string(out), "hash=000000") || strings.Count(string(out), "/ds:block hash=") != 1 {
		t.Fatalf("build-mode report (%d, %+v):\n%s", n, notes, out)
	}
	if _, all := s.Fences(res, "docs/repo.md", []byte(doc)); all != 2 {
		t.Errorf("Fences in build mode wrote %d copies, want both", all)
	}
	fsys["docs/repo.md"] = &fstest.MapFile{Data: out}
	rep, _ := newSys(t, fsys).Check(ctx, CheckOptions{})
	for _, f := range rep.Findings {
		if f.Doc == "docs/repo.md" && f.State == check.StateStale {
			t.Errorf("the refreshed copy is stale: %+v", f)
		}
	}
}
