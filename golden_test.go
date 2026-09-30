package docsync

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/ubgo/docsync/ledger"
)

// TestJSONContract pins the wire shapes of §26.2: each result type is
// marshalled from one deterministic run and compared byte-for-byte with
// testdata/golden/<shape>.json. A diff here means the JSON contract moved;
// that is allowed only additively, and the golden must be regenerated with
// -update on purpose, never by reflex.
// maskID replaces minted suffixes so goldens are stable across runs.
func maskID(s string) string {
	re := regexp.MustCompile(`id=([a-z0-9-]+)-[23456789abcdefghjkmnpqrstuvwxyz]{8}`)
	return re.ReplaceAllString(s, "id=$1-XXXXXXXX")
}

func TestJSONContract(t *testing.T) {
	t.Parallel()
	first := newSys(t, repo(false))
	ctx := context.Background()
	res, err := first.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prev, prevRefs := first.Snapshot(res)
	old := map[string]string{}
	for _, b := range res.Defs {
		old[b.ID] = b.Content
	}
	s := newSys(t, repo(true), WithPrevious(prev, prevRefs),
		WithOldContent(func(row ledger.Row) (string, bool) { c, ok := old[row.ID]; return c, ok }),
		WithAcks(ledger.Acks{Rows: []ledger.Ack{{At: clock, Actor: "khanakia", ActorKind: ledger.ActorHuman, ID: "sess-save-k7m2p4xq", Repo: "api", Doc: "docs/sessions.md", Line: 9, Note: "still true"}}}))
	rep, err := s.Check(ctx, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := s.Map(ctx, MapOptions{Budget: 4000})
	c, _ := s.Context(ctx, "docs/sessions.md", ContextOptions{Budget: 8000, Since: SinceAck})
	imp, _ := s.Impact(ctx)
	w, _ := s.Why(rep.Scan, "gh-stripe-key-r4t6x2mb")
	d, _ := s.Define(ctx, "internal/store/write.go#Persist", DefineOptions{Owner: "@auth"})
	d.ID, d.Block.ID, d.Edit.New = "store-persist-XXXXXXXX", "store-persist-XXXXXXXX", "// ds:def id=store-persist-XXXXXXXX owner=@auth"
	d.Block.Args["id"] = "store-persist-XXXXXXXX"
	adopt, _ := s.Adopt(ctx, rep.Scan)
	for i := range adopt.Edits {
		adopt.Edits[i].New = maskID(adopt.Edits[i].New)
	}
	blame, _ := s.Blame(rep, "docs/sessions.md", 7)
	rename, _ := s.Rename(rep.Scan, "sess", "session")
	shapes := map[string]any{
		"check":   rep,
		"map":     m,
		"context": c,
		"impact":  imp,
		"facts":   s.Facts(rep.Scan),
		"why":     w,
		"define":  d,
		"triage":  Triage(rep),
		"report":  s.Report(rep, ReportOptions{Churn: map[string]int{"internal/other.go": 3}}),
		"graph":   s.Graph(rep),
		"blame":   blame,
		"adopt":   adopt,
		"rename":  rename,
		"audit":   s.Audit(AuditOptions{}),
	}
	for name, v := range shapes {
		got, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		path := filepath.Join("testdata", "golden", name+".json")
		if *update {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run with -update)", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: JSON contract changed; review and regenerate with -update if the change is additive:\n%s", name, got)
		}
	}
}
