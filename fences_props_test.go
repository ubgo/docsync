package docsync

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
)

// hostileBodies are Go function bodies holding the text that can confuse a
// repo-mode region: a run of backticks longer than the fence, a line that
// looks exactly like a region closer, and a citation directive. A copy of
// such a block must still be read back as one region, or the next refresh
// is not idempotent and check calls a fresh copy tampered.
var hostileBodies = []string{
	"\treturn 1",
	"\ts := \"```go\"\n\t_ = s",
	"\ts := `x`\n\t_ = s",
	"\t// <!-- /ds:block hash=abcdef -->\n\treturn 2",
	"\t// <!-- ds:block id=fn0-k7m2p4xq -->\n\treturn 3",
	"\t// ~~~\n\treturn 4",
}

// TestRepoModeRefreshIsAFixedPoint is the refresh contract as a property,
// over generated documents: after one refresh, scanning the written doc and
// refreshing again changes nothing; check reports no copy stale or
// tampered; every line outside a region is exactly as written; and the
// doc's line endings are kept. A refresh that is not a fixed point rewrites
// docs on every run and teaches everyone to ignore its diff.
func TestRepoModeRefreshIsAFixedPoint(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(9))
	ctx := context.Background()
	for trial := 0; trial < 300; trial++ {
		var code strings.Builder
		code.WriteString("package p\n")
		n := 1 + rng.Intn(3)
		ids := make([]string, n)
		for i := 0; i < n; i++ {
			ids[i] = fmt.Sprintf("fn%d-k7m2p4xq", i)
			fmt.Fprintf(&code, "\n// ds:def id=%s\nfunc F%d() int {\n%s\n\treturn 0\n}\n", ids[i], i, hostileBodies[rng.Intn(len(hostileBodies))])
		}
		var doc []string
		doc = append(doc, "# Doc", "")
		for i := 0; i < n; i++ {
			doc = append(doc, fmt.Sprintf("Paragraph %d.", i), "", "<!-- ds:block id="+ids[rng.Intn(n)]+" -->", "")
		}
		doc = append(doc, "The end.")
		eol := "\n"
		if rng.Intn(2) == 0 {
			eol = "\r\n"
		}
		src := []byte(strings.Join(doc, eol) + eol)
		fsys := fstest.MapFS{
			"internal/f.go": {Data: []byte(code.String())},
			"docs/repo.md":  {Data: src},
		}
		cfgOpt := WithConfig(repoModeConfig())
		s := newSys(t, fsys, cfgOpt)
		res, err := s.Scan(ctx)
		if err != nil {
			t.Fatal(err)
		}
		once, _ := s.Fences(res, "docs/repo.md", src)

		fsys["docs/repo.md"] = &fstest.MapFile{Data: once}
		s2 := newSys(t, fsys, cfgOpt)
		res2, err := s2.Scan(ctx)
		if err != nil {
			t.Fatal(err)
		}
		twice, n2 := s2.Fences(res2, "docs/repo.md", once)
		if n2 != 0 || string(twice) != string(once) {
			t.Fatalf("trial %d: a second refresh changed %d regions\n--- once\n%s\n--- twice\n%s", trial, n2, once, twice)
		}
		rep, err := s2.Check(ctx, CheckOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range rep.Findings {
			if f.State == check.StateStale || f.State == check.StateTampered {
				t.Fatalf("trial %d: a fresh copy is %s: %+v\n%s", trial, f.State, f, once)
			}
		}
		if eol == "\r\n" && strings.Count(string(once), "\n") != strings.Count(string(once), "\r\n") {
			t.Fatalf("trial %d: refresh changed a CRLF doc's line endings", trial)
		}
		// Outside the regions the doc is untouched: every original line is
		// still there, in order.
		got := strings.Split(strings.ReplaceAll(string(once), "\r\n", "\n"), "\n")
		j := 0
		for _, want := range doc {
			for j < len(got) && got[j] != want {
				j++
			}
			if j == len(got) {
				t.Fatalf("trial %d: refresh lost the line %q", trial, want)
			}
			j++
		}
	}
}

// repoModeConfig is the shared test config with include.mode = repo and the
// generated trees' paths in scope.
func repoModeConfig() config.Config {
	c := cfg()
	c.Include.Mode = config.IncludeRepo
	c.Scan.Code = []string{"**"}
	c.Scan.Docs = []string{"docs/**"}
	return c
}
