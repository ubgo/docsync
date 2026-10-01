package docsync

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/scan"
)

const (
	goFile = `package store

// ds:def id=sess-save-k7m2p4xq owner=@auth stability=stable
func (s *Store) Save() error {
	return s.legacy.Save()
}

// ds:def id=sess-ttl-p2c4y7mk
const TTL = 30

func (s *Store) Persist() error {
	return nil
}
`
	goFileChanged = `package store

// ds:def id=sess-save-k7m2p4xq owner=@auth stability=stable
func (s *Store) Save() error {
	return s.sessions.Insert()
}

// ds:def id=sess-ttl-p2c4y7mk
const TTL = 30

func (s *Store) Persist() error {
	return nil
}
`
	yamlFile = "auth:\n  port: 8081   # ds:def id=auth-port-h3v8n2wd\n  host: example.internal\n"
	envFile  = "STRIPE_KEY=op://Platform/stripe-prod/credential   # ds:def id=op-stripe-key-p9c2v7ld secret=true truth=true\n"
	wfFile   = "env:\n  STRIPE_KEY: ${{ secrets.STRIPE_KEY }}   # ds:def id=gh-stripe-key-r4t6x2mb secret=true from=op-stripe-key-p9c2v7ld sync=scripts/sync.sh\n"
	docFile  = `---
ds:
  covers: [sess-save-k7m2p4xq]
---
# Sessions

Every write goes through [` + "`Save`" + `](ds:block?id=sess-save-k7m2p4xq). It listens on [8081](ds:cfg?id=auth-port-h3v8n2wd).

<!-- ds:block id=sess-save-k7m2p4xq -->

Stripe: <!-- ds:chain id=gh-stripe-key-r4t6x2mb -->

Broken [x](ds:block?id=nope-a2b6f8jk). See [pg](ds:url?href=https://example.com/pg).

Other repo: [remote](ds:block?id=remote-c4d8h2lm).
`
	txtFile = "line one\nline two\n"
)

var clock = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

func repo(changed bool) fstest.MapFS {
	g := goFile
	if changed {
		g = goFileChanged
	}
	return fstest.MapFS{
		"internal/store/write.go":      {Data: []byte(g)},
		"config/auth.yaml":             {Data: []byte(yamlFile)},
		"config/secrets.env":           {Data: []byte(envFile)},
		".github/workflows/deploy.yml": {Data: []byte(wfFile)},
		"docs/sessions.md":             {Data: []byte(docFile)},
		"notes.txt":                    {Data: []byte(txtFile)},
	}
}

func cfg() config.Config {
	c := config.Default()
	c.Scan.Code = []string{"internal/**", "config/**", ".github/**", "notes.txt"}
	c.Scan.Docs = []string{"docs/**"}
	c.Check.Permalink = "https://g/{sha}/{file}#L{start}-L{end}"
	return c
}

func remoteDef() block.Block {
	b := block.Block{ID: "remote-c4d8h2lm", Kind: block.KindFunc, Pos: block.Position{File: "x.go", Start: 1, End: 1}, Args: map[string]string{"id": "remote-c4d8h2lm"}}
	b.SetContent("remote")
	return b
}

func newSys(t *testing.T, fsys fstest.MapFS, extra ...Option) *System {
	t.Helper()
	opts := append([]Option{WithFS(fsys), WithConfig(cfg()), WithRepo("api"), WithCommit("7c1e2a"), WithClock(func() time.Time { return clock }), WithMerged(remoteDef())}, extra...)
	s, err := New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func states(rep Report) map[check.State][]check.Finding {
	m := map[check.State][]check.Finding{}
	for _, f := range rep.Findings {
		m[f.State] = append(m[f.State], f)
	}
	return m
}

func TestNewErrors(t *testing.T) {
	t.Parallel()
	if _, err := New(); !errors.Is(err, ErrNoFS) {
		t.Errorf("no fs: %v", err)
	}
	bad := config.Default()
	if _, err := New(WithFS(repo(false)), WithConfig(bad)); !errors.Is(err, config.ErrNoScan) {
		t.Errorf("invalid config: %v", err)
	}
	c := cfg()
	c.ID.SuffixLength = 1
	if _, err := New(WithFS(repo(false)), WithConfig(c)); err == nil {
		t.Error("bad id config must fail")
	}
	failing := func(*System) error { return errors.New("opt") }
	if _, err := New(WithFS(repo(false)), failing); err == nil || err.Error() != "opt" {
		t.Errorf("option error: %v", err)
	}
	// Zero-config New scans docs/** and **.
	s, err := New(WithFS(repo(false)))
	if err != nil || len(s.Config().Scan.Code) == 0 {
		t.Fatalf("default config: %v", err)
	}
	if res, err := s.Scan(context.Background()); err != nil || len(res.Defs) != 5 {
		t.Errorf("default scan = %d defs, %v", len(res.Defs), err)
	}
	if rep, err := s.Check(context.Background(), CheckOptions{}); err != nil || rep.GeneratedAt.IsZero() {
		t.Error("default clock")
	}
	if ex, err := s.Registry().For("a.md"); err != nil || ex.Name() != "markdown" {
		t.Errorf("registry = %v %v", ex, err)
	}
	if SpecVersion != "1.0" || JSONFormat != 1 {
		t.Error("versions")
	}
}

// txtOnly is a custom tier claiming .txt so WithExtractor's precedence is
// observable: it defines nothing, so the notes file yields no defs.
type txtOnly struct{}

func (txtOnly) Name() string                                 { return "txtonly" }
func (txtOnly) Match(p string) bool                          { return strings.HasSuffix(p, ".txt") }
func (txtOnly) Extract(string, []byte, string) extract.Found { return extract.Found{} }

func TestFirstRunCheckAndSnapshot(t *testing.T) {
	t.Parallel()
	s := newSys(t, repo(false), WithRegistry(extract.Default()), WithExtractor(txtOnly{}))
	rep, err := s.Check(context.Background(), CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.JSONFormat != 1 || rep.Repo != "api" || rep.Commit != "7c1e2a" || !rep.GeneratedAt.Equal(clock) {
		t.Errorf("envelope = %+v", rep.Envelope)
	}
	st := states(rep)
	if len(st[check.StateBroken]) != 1 || st[check.StateBroken][0].ID != "nope-a2b6f8jk" {
		t.Errorf("broken = %+v", st[check.StateBroken])
	}
	if rep.Scan.Tier["notes.txt"] != "txtonly" {
		t.Errorf("custom tier not used: %v", rep.Scan.Tier)
	}
	if len(st[check.StateUnverifiable]) != 1 || st[check.StateUnverifiable][0].Verb != "url" {
		t.Errorf("url without resolve = %+v", st[check.StateUnverifiable])
	}
	if len(st[check.StateUnacked]) != 0 || len(st[check.StateChainBroken]) != 0 || len(st[check.StateUnsourced]) != 0 {
		t.Errorf("first run must not flag change or chain problems: %v", rep.States)
	}
	if rep.ExitCode != 1 || rep.Summary[check.SeverityError] != 1 {
		t.Errorf("exit = %d summary = %v", rep.ExitCode, rep.Summary)
	}
	okIDs := map[string]bool{}
	for _, f := range st[check.StateOK] {
		okIDs[f.ID] = true
	}
	if !okIDs["remote-c4d8h2lm"] || !okIDs["sess-save-k7m2p4xq"] || !okIDs["auth-port-h3v8n2wd"] {
		t.Errorf("ok ids = %v", okIDs)
	}
	// Uncovered: the ttl const is defined and cited by nothing.
	if len(st[check.StateUncovered]) != 1 || st[check.StateUncovered][0].ID != "sess-ttl-p2c4y7mk" {
		t.Errorf("uncovered = %+v", st[check.StateUncovered])
	}
	l, r := s.Snapshot(rep.Scan)
	if l.Header.Kind != ledger.KindLedger || l.Header.Repo != "api" || l.Header.Commit != "7c1e2a" || len(l.Rows) != 5 || r.Header.Kind != ledger.KindRefs || len(r.Rows) != len(rep.Scan.Refs) {
		t.Errorf("snapshot = %+v / %d refs", l.Header, len(r.Rows))
	}
	var sb strings.Builder
	if err := l.Encode(&sb); err != nil || !strings.Contains(sb.String(), "sess-save-k7m2p4xq") {
		t.Errorf("ledger encodes: %v", err)
	}
}

// promise:audit-actor promise:agent-bounded
func TestSecondRunUnackedThenAcked(t *testing.T) {
	t.Parallel()
	first := newSys(t, repo(false))
	res, err := first.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	prev, prevRefs := first.Snapshot(res)
	old := map[string]string{}
	for _, b := range res.Defs {
		old[b.ID] = b.Content
	}
	oldContent := func(row ledger.Row) (string, bool) { c, ok := old[row.ID]; return c, ok }

	second := newSys(t, repo(true), WithPrevious(prev, prevRefs), WithOldContent(oldContent))
	rep, err := second.Check(context.Background(), CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	un := states(rep)[check.StateUnacked]
	if len(un) != 2 {
		t.Fatalf("unacked = %+v", un)
	}
	for _, f := range un {
		if f.ID != "sess-save-k7m2p4xq" || f.Owner != "@auth" || !strings.Contains(f.Diff, "sessions.Insert") || len(f.Classes) == 0 {
			t.Errorf("unacked detail = %+v", f)
		}
	}
	// Ack the link-form reference as a human; the block-position one stays.
	ack, err := second.Ack(rep.Scan, AckRequest{ID: "sess-save-k7m2p4xq", Doc: "docs/sessions.md", Line: 7, Actor: "khanakia", Note: "still true"})
	if err != nil || ack.BlockHash == "" || ack.SentenceHash == "" || ack.ActorKind != ledger.ActorHuman || !ack.At.Equal(clock) {
		t.Fatalf("ack = %+v %v", ack, err)
	}
	third := newSys(t, repo(true), WithPrevious(prev, prevRefs), WithOldContent(oldContent), WithAcks(ledger.Acks{Rows: []ledger.Ack{ack}}))
	rep3, _ := third.Check(context.Background(), CheckOptions{})
	un3 := states(rep3)[check.StateUnacked]
	if len(un3) != 1 || un3[0].Line != 9 {
		t.Errorf("after ack = %+v", un3)
	}
	// The snapshot after the ack carries the acked hash on that ref line.
	_, refs := third.Snapshot(rep3.Scan)
	found := false
	for _, r := range refs.Rows {
		if r.Doc == "docs/sessions.md" && r.Line == 7 && r.AckedHash == ack.BlockHash {
			found = true
		}
	}
	if !found {
		t.Errorf("acked hash not in refs: %+v", refs.Rows)
	}
	// A fourth run with those refs as previous and no ack log still sees it.
	fourth := newSys(t, repo(true), WithPrevious(prev, refs), WithOldContent(oldContent))
	if rep4, _ := fourth.Check(context.Background(), CheckOptions{}); len(states(rep4)[check.StateUnacked]) != 1 {
		t.Error("prev refs must carry the ack")
	}
	// Impact groups the same findings.
	imp, err := second.Impact(context.Background())
	if err != nil || imp.Total != 3 || len(imp.ByDoc) != 1 || imp.ByDoc[0].Key != "docs/sessions.md" || len(imp.ByOwner) != 2 || imp.ByRepo[0].Key != "api" {
		t.Errorf("impact = %+v %v", imp, err)
	}
	// Agent acks need delegation.
	if _, err := second.Ack(rep.Scan, AckRequest{Doc: "docs/sessions.md", Line: 7, Actor: "bot", ActorKind: ledger.ActorAgent}); !errors.Is(err, ErrDelegationRequired) {
		t.Errorf("agent without delegation: %v", err)
	}
	if a, err := second.Ack(rep.Scan, AckRequest{Doc: "docs/sessions.md", Line: 7, Actor: "bot", ActorKind: ledger.ActorAgent, DelegatedBy: "khanakia"}); err != nil || a.DelegatedBy != "khanakia" || a.ID != "sess-save-k7m2p4xq" {
		t.Errorf("delegated agent ack: %+v %v", a, err)
	}
	if _, err := second.Ack(rep.Scan, AckRequest{Doc: "docs/sessions.md", Line: 99}); !errors.Is(err, ErrNoReference) {
		t.Errorf("no ref: %v", err)
	}
	if _, err := second.Ack(rep.Scan, AckRequest{ID: "other", Doc: "docs/sessions.md", Line: 7}); !errors.Is(err, ErrNoReference) {
		t.Errorf("id mismatch: %v", err)
	}
	if _, err := second.Ack(rep.Scan, AckRequest{ID: "nope-a2b6f8jk", Doc: "docs/sessions.md", Line: 13}); !errors.Is(err, ErrNotFound) {
		t.Errorf("ack on broken ref: %v", err)
	}
	// Config downgrade to warning and strict mode.
	c := cfg()
	c.Check.Unacked = config.UnackedWarn
	warn, _ := newSys(t, repo(true), WithConfig(c), WithPrevious(prev, prevRefs)).Check(context.Background(), CheckOptions{})
	if warn.Summary[check.SeverityError] != 1 || states(warn)[check.StateUnacked][0].Severity != check.SeverityWarning {
		t.Errorf("warn config = %v", warn.Summary)
	}
	strict, _ := newSys(t, repo(true), WithConfig(c), WithPrevious(prev, prevRefs)).Check(context.Background(), CheckOptions{Strict: true})
	if strict.Summary[check.SeverityWarning] != 0 {
		t.Errorf("strict = %v", strict.Summary)
	}
}

func TestHooksAndToggles(t *testing.T) {
	t.Parallel()
	url := func(href string) check.URLResult { return check.URLResult{Checked: true, Status: 404} }
	s := newSys(t, repo(false), WithURLCheck(url), WithCommitLookup(func(string) bool { return true }), WithTestResults(map[string]check.TestOutcome{}), WithVerb("ticket"), WithRecords(func(map[string]string) ([]map[string]string, error) { return nil, nil }))
	rep, _ := s.Check(context.Background(), CheckOptions{Resolve: true, Env: "prod", Run: true})
	if len(states(rep)[check.StateDead]) != 1 {
		t.Errorf("resolve must call the url hook: %v", rep.States)
	}
	rep, _ = s.Check(context.Background(), CheckOptions{})
	if len(states(rep)[check.StateUnverifiable]) != 1 {
		t.Errorf("without resolve the url is unverifiable: %v", rep.States)
	}
	if commentPrefixes("a.go")[0] != "//" || commentPrefixes("a.unknownext") != nil {
		t.Error("commentPrefixes")
	}
	// Bad globs surface from Scan and every caller of it.
	c := cfg()
	c.Scan.Exclude = []string{"["}
	bad := newSys(t, repo(false), WithConfig(c))
	ctx := context.Background()
	if _, err := bad.Scan(ctx); err == nil {
		t.Error("bad exclude")
	}
	if _, err := bad.Check(ctx, CheckOptions{}); err == nil {
		t.Error("check propagates")
	}
	if _, err := bad.Map(ctx, MapOptions{}); err == nil {
		t.Error("map propagates")
	}
	if _, err := bad.Context(ctx, "docs/sessions.md", ContextOptions{}); err == nil {
		t.Error("context propagates")
	}
	if _, err := bad.Impact(ctx); err == nil {
		t.Error("impact propagates")
	}
	if _, _, err := bad.Render(ctx, "docs/sessions.md", RenderOptions{}); err == nil {
		t.Error("render propagates")
	}
	for _, field := range []func(*config.Config){
		func(c *config.Config) { c.Scan.Code = []string{"["} },
		func(c *config.Config) { c.Scan.Generated = []string{"["} },
		func(c *config.Config) { c.Secret.Paths = []string{"["} },
	} {
		c := cfg()
		field(&c)
		if _, err := newSys(t, repo(false), WithConfig(c)).Scan(ctx); err == nil {
			t.Error("bad glob must fail")
		}
	}
}

func TestRender(t *testing.T) {
	t.Parallel()
	s := newSys(t, repo(false), WithSnapshot(func(string, string) (string, bool) { return "", false }))
	out, notes, err := s.Render(context.Background(), "docs/sessions.md", RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	o := string(out)
	for _, want := range []string{
		"[`Save`](https://g/7c1e2a/internal/store/write.go#L4-L6). It listens on 8081.",
		"Stripe:\n\n- `gh-stripe-key-r4t6x2mb` github",
		"**Store.Save** · [`internal/store/write.go:4-6`](https://g/7c1e2a/internal/store/write.go#L4-L6)\n\n```go\nfunc (s *Store) Save() error {",
		"- `gh-stripe-key-r4t6x2mb` github `${{ secrets.STRIPE_KEY }}`",
		"  - from `op-stripe-key-p9c2v7ld` 1password `op://Platform/stripe-prod/credential`",
		"Broken x. See [pg](https://example.com/pg).",
		"[remote](https://g/7c1e2a/x.go#L1-L1)",
	} {
		if !strings.Contains(o, want) {
			t.Errorf("missing %q in:\n%s", want, o)
		}
	}
	if strings.Contains(o, "Stripe: <!--") || len(notes) != 1 || !strings.Contains(notes[0].Message, "nope-a2b6f8jk") {
		t.Errorf("chain comment must be replaced; notes = %+v", notes)
	}
	if _, _, err := s.Render(context.Background(), "docs/missing.md", RenderOptions{}); err == nil {
		t.Error("missing doc")
	}
	if out, _, err := s.Render(context.Background(), "docs/missing.md", RenderOptions{Source: []byte("Port [x](ds:cfg?id=auth-port-h3v8n2wd).\n")}); err != nil || string(out) != "Port 8081.\n" {
		t.Errorf("render from source = %q %v", out, err)
	}
}

func TestDefine(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	s := newSys(t, fsys)
	ctx := context.Background()
	r, err := s.Define(ctx, "internal/store/write.go#Persist", DefineOptions{Owner: "@auth", Stability: "api", Tags: "a,b", Desc: "persists"})
	if err != nil || r.Existing || !strings.HasPrefix(r.ID, "store-persist-") || r.Block.Pos != (block.Position{File: "internal/store/write.go", Start: 11, End: 13}) {
		t.Fatalf("define = %+v %v", r, err)
	}
	if r.Edit.Line != 11 || r.Edit.Old != "" || r.Edit.New != "// ds:def id="+r.ID+" owner=@auth stability=api tags=a,b desc=persists" {
		t.Errorf("edit = %+v", r.Edit)
	}
	applied, err := r.Edit.Apply(fsys["internal/store/write.go"].Data)
	if err != nil {
		t.Fatal(err)
	}
	fsys["internal/store/write.go"] = &fstest.MapFile{Data: applied}
	res, _ := s.Scan(ctx)
	if b, ok := s.LocateID(res, r.ID); !ok || b.Symbol != "Store.Persist" || b.Pos.Start != 12 {
		t.Errorf("applied def not found: %+v", b)
	}
	again, err := s.Define(ctx, "internal/store/write.go#Store.Persist", DefineOptions{})
	if err != nil || !again.Existing || again.ID != r.ID || !again.Edit.IsZero() || again.Block.Pos.File != "internal/store/write.go" {
		t.Errorf("existing = %+v %v", again, err)
	}
	if ex, _ := s.Define(ctx, "internal/store/write.go#Save", DefineOptions{}); !ex.Existing || ex.ID != "sess-save-k7m2p4xq" {
		t.Errorf("existing save = %+v", ex)
	}
	// A yaml key gets a trailing comment.
	y, err := s.Define(ctx, "config/auth.yaml#auth.host", DefineOptions{Label: "auth-host"})
	if err != nil || y.Edit.Old != "  host: example.internal" || y.Edit.New != "  host: example.internal   # ds:def id="+y.ID || !strings.HasPrefix(y.ID, "auth-host-") {
		t.Errorf("yaml define = %+v %v", y, err)
	}
	if applied, err := y.Edit.Apply([]byte(yamlFile)); err != nil || !strings.Contains(string(applied), y.Edit.New) {
		t.Errorf("yaml apply: %v", err)
	}
	// Markdown gets an HTML comment above; text gets a bare line; env gets the env key.
	m, err := s.Define(ctx, "docs/sessions.md#Sessions", DefineOptions{Env: "prod"})
	if err != nil || m.Edit.New != "<!-- ds:def id="+m.ID+" env=prod -->" || m.Edit.Line != 5 || !strings.HasPrefix(m.ID, "sessions-") {
		t.Errorf("md define = %+v %v", m, err)
	}
	if pk, err := s.Define(ctx, "internal/store/write.go:1", DefineOptions{}); err != nil || !strings.HasPrefix(pk.ID, "write-") {
		t.Errorf("label from file name = %+v %v", pk, err)
	}
	tx, err := s.Define(ctx, "notes.txt:2", DefineOptions{})
	if err != nil || tx.Edit.New != "ds:def id="+tx.ID || !strings.HasPrefix(tx.ID, "notes-") {
		t.Errorf("txt define = %+v %v", tx, err)
	}
	// A yaml line target with an existing trailing def returns it.
	if ex, err := s.Define(ctx, "config/auth.yaml:2", DefineOptions{}); err != nil || !ex.Existing || ex.ID != "auth-port-h3v8n2wd" {
		t.Errorf("yaml existing = %+v %v", ex, err)
	}
	for _, bad := range []string{"nofile.go#X", "internal/store/write.go#Missing", "internal/store/write.go", "internal/store/write.go:999"} {
		if _, err := s.Define(ctx, bad, DefineOptions{}); err == nil {
			t.Errorf("Define(%q) must fail", bad)
		}
	}
	if _, err := s.Define(ctx, "notes.txt:1", DefineOptions{Desc: `mixed "double" and 'single'`}); err == nil {
		t.Error("unquotable desc must fail")
	}
	if _, err := s.Define(ctx, "notes.txt:1", DefineOptions{Label: "!!!"}); err == nil {
		t.Error("invalid label must fail")
	}
	// Edit.Apply guards.
	if _, err := (Edit{Line: 99, New: "x"}).Apply([]byte("a\nb")); !errors.Is(err, ErrNotFound) {
		t.Error("line out of range")
	}
	if _, err := (Edit{Line: 1, Old: "zzz", New: "x"}).Apply([]byte("a\nb")); !errors.Is(err, ErrNotFound) {
		t.Error("changed line")
	}
	if out, err := (Edit{}).Apply([]byte("a\r\nb")); err != nil || string(out) != "a\r\nb" {
		t.Error("zero edit is identity")
	}
	// The CRLF file stays CRLF. This used to expect "a\nmid\nb" — pinning
	// the rewrite of every line ending that TestEditApplyKeepsLineEndings
	// now forbids.
	if out, _ := (Edit{Line: 2, New: "mid"}).Apply([]byte("a\r\nb")); string(out) != "a\r\nmid\r\nb" {
		t.Errorf("insert = %q", out)
	}
	if baseName("x") != "x" {
		t.Error("baseName")
	}
	// A registry without a matching extractor.
	empty := newSys(t, repo(false), WithRegistry(extract.NewRegistry()))
	if _, err := empty.Define(ctx, "notes.txt:1", DefineOptions{}); !errors.Is(err, extract.ErrNoExtractor) {
		t.Errorf("no extractor: %v", err)
	}
}

// promise:mcp-content-data promise:truth-declared
func TestAgentSurface(t *testing.T) {
	t.Parallel()
	first := newSys(t, repo(false))
	ctx := context.Background()
	res, _ := first.Scan(ctx)
	prev, prevRefs := first.Snapshot(res)
	old := map[string]string{}
	for _, b := range res.Defs {
		old[b.ID] = b.Content
	}
	s := newSys(t, repo(true), WithPrevious(prev, prevRefs), WithOldContent(func(row ledger.Row) (string, bool) { c, ok := old[row.ID]; return c, ok }), WithAcks(ledger.Acks{Rows: []ledger.Ack{{At: clock, ID: "sess-save-k7m2p4xq", Note: "n"}}}))
	res, _ = s.Scan(ctx)

	facts := s.Facts(res)
	byID := map[string]Fact{}
	for _, f := range facts {
		byID[f.ID] = f
	}
	if len(facts) != 4 || byID["auth-port-h3v8n2wd"].Value != "8081" || len(byID["auth-port-h3v8n2wd"].CitedBy) != 1 || !byID["op-stripe-key-p9c2v7ld"].Secret || byID["sess-ttl-p2c4y7mk"].Value != "const TTL = 30" {
		t.Errorf("facts = %+v", facts)
	}

	w, err := s.Why(res, "sess-save-k7m2p4xq")
	if err != nil || len(w.Refs) != 2 || len(w.CoveredBy) != 1 || len(w.Chain) != 1 || len(w.History) != 1 || len(w.Copies) != 0 {
		t.Errorf("why save = %+v %v", w, err)
	}
	if w, _ := s.Why(res, "gh-stripe-key-r4t6x2mb"); len(w.Chain) != 2 || w.Chain[1].ID != "op-stripe-key-p9c2v7ld" {
		t.Errorf("why chain = %+v", w.Chain)
	}
	if w, _ := s.Why(res, "op-stripe-key-p9c2v7ld"); len(w.Copies) != 1 {
		t.Errorf("why copies = %+v", w.Copies)
	}
	if _, err := s.Why(res, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("why missing: %v", err)
	}

	m, err := s.Map(ctx, MapOptions{})
	if err != nil || len(m.Pages) != 1 || m.Pages[0].Path != "docs/sessions.md" || m.Pages[0].Covers != 1 || m.Pages[0].Cites != 7 || m.Pages[0].State[check.StateUnacked] != 2 {
		t.Fatalf("map pages = %+v %v", m.Pages, err)
	}
	if len(m.Defs) != 5 || m.Defs[0].ID != "sess-save-k7m2p4xq" || m.Defs[0].State != check.StateUnacked || m.Defs[0].CitedBy != 2 || m.Omitted != 0 || m.UsedTokens == 0 {
		t.Errorf("map defs = %+v", m.Defs)
	}
	if len(m.Chains) != 1 || m.Chains[0].Root != "op-stripe-key-p9c2v7ld" || m.Chains[0].Hops != 2 || m.Chains[0].State != check.StateOK {
		t.Errorf("map chains = %+v", m.Chains)
	}
	if m.Freshness[check.StateUnacked] != 2 {
		t.Errorf("freshness = %v", m.Freshness)
	}
	small, _ := s.Map(ctx, MapOptions{Budget: m.Pages[0].Tokens + m.Defs[0].Tokens})
	if len(small.Pages) != 1 || len(small.Defs) != 1 || small.Omitted != 4 || small.UsedTokens > small.BudgetTokens {
		t.Errorf("budgeted map = pages %d defs %d omitted %d", len(small.Pages), len(small.Defs), small.Omitted)
	}
	// A broken chain shows in the map.
	brokenFS := repo(true)
	brokenFS[".github/workflows/deploy.yml"] = &fstest.MapFile{Data: []byte(strings.ReplaceAll(wfFile, "from=op-stripe-key-p9c2v7ld", "from=zz-a2b6f8jk"))}
	brokenSys := newSys(t, brokenFS)
	if bm, _ := brokenSys.Map(ctx, MapOptions{}); len(bm.Chains) != 1 || bm.Chains[0].Hops != 1 || bm.Chains[0].State != check.StateOK {
		t.Errorf("a lone truth is a chain; the dangling copy is not its member: %+v", bm.Chains)
	}
	bres, _ := brokenSys.Scan(ctx)
	if w, err := brokenSys.Why(bres, "gh-stripe-key-r4t6x2mb"); err != nil || len(w.Chain) != 1 {
		t.Errorf("why with dangling from stops at the gap: %+v %v", w.Chain, err)
	}
	// Two pages: the one with problems ranks first; a tiny budget omits both.
	twoDocs := repo(false)
	twoDocs["docs/other.md"] = &fstest.MapFile{Data: []byte("Port [8081](ds:cfg?id=auth-port-h3v8n2wd).\n")}
	twoDocs["docs/cover-b.md"] = &fstest.MapFile{Data: []byte("---\nds:\n  covers: [sess-ttl-p2c4y7mk]\n---\n# B\n")}
	twoDocs["docs/cover-a.md"] = &fstest.MapFile{Data: []byte("---\nds:\n  covers: [sess-ttl-p2c4y7mk]\n---\n# A\n")}
	twoDocs["config/secrets.env"] = &fstest.MapFile{Data: []byte(envFile + "OTHER=op://x/y   # ds:def id=oth-b3c7g9kl secret=true truth=true\n")}
	tm, _ := newSys(t, twoDocs).Map(ctx, MapOptions{})
	paths := []string{}
	for _, p := range tm.Pages {
		paths = append(paths, p.Path)
	}
	if strings.Join(paths, ",") != "docs/sessions.md,docs/other.md,docs/cover-a.md,docs/cover-b.md" {
		t.Errorf("page ranking = %v", paths)
	}
	if len(tm.Chains) != 2 || tm.Chains[0].Root != "op-stripe-key-p9c2v7ld" || tm.Chains[1].Root != "oth-b3c7g9kl" {
		t.Errorf("chains sorted = %+v", tm.Chains)
	}
	if tiny, _ := newSys(t, twoDocs).Map(ctx, MapOptions{Budget: 1}); len(tiny.Pages) != 0 || len(tiny.Defs) != 0 || tiny.Omitted != 10 {
		t.Errorf("tiny budget = %+v", tiny)
	}
	twoTruth := repo(true)
	twoTruth["config/secrets.env"] = &fstest.MapFile{Data: []byte(envFile + "OTHER=op://x   # ds:def id=other-b3c7g9kl truth=true from=op-stripe-key-p9c2v7ld\n")}
	if bm, _ := newSys(t, twoTruth).Map(ctx, MapOptions{}); len(bm.Chains) != 1 || bm.Chains[0].State != check.StateChainBroken || bm.Chains[0].Hops != 3 {
		t.Errorf("broken chain state = %+v", bm.Chains)
	}

	c, err := s.Context(ctx, "docs/sessions.md", ContextOptions{Since: SinceAck})
	if err != nil || len(c.Items) != 5 || c.Items[0].ID != "sess-save-k7m2p4xq" || c.Items[0].Mode != ModeDiff || !strings.Contains(c.Items[0].Content, "sessions.Insert") {
		t.Fatalf("context = %+v %v", c.Items, err)
	}
	modes := map[string]string{}
	whys := map[string]string{}
	for _, it := range c.Items {
		modes[it.ID], whys[it.ID] = it.Mode, it.Why
	}
	if modes["nope-a2b6f8jk"] != ModeLine || whys["nope-a2b6f8jk"] != string(check.StateBroken) || modes["auth-port-h3v8n2wd"] != ModeValue || modes["gh-stripe-key-r4t6x2mb"] != ModeValue || modes["remote-c4d8h2lm"] != ModeValue || whys["remote-c4d8h2lm"] != "cited, unchanged" {
		t.Errorf("modes = %v whys = %v", modes, whys)
	}
	if c.Items[1].ID != "nope-a2b6f8jk" {
		t.Errorf("broken ranks second: %+v", c.Items[1])
	}
	tight, _ := s.Context(ctx, "docs/sessions.md", ContextOptions{Since: SinceAck, Budget: c.Items[0].Tokens})
	if len(tight.Items) != 1 || len(tight.Omitted) != 4 || tight.Omitted[0].Reason != ReasonOverBudget || tight.Omitted[len(tight.Omitted)-1].Reason != ReasonUnchanged {
		t.Errorf("tight context = %+v", tight.Omitted)
	}
	full, _ := s.Context(ctx, "docs/sessions.md", ContextOptions{Mode: ModeFull})
	if full.Items[0].Mode != ModeFull || !strings.Contains(full.Items[0].Content, "func (s *Store) Save") {
		t.Errorf("full mode = %+v", full.Items[0])
	}
	valueMode, _ := s.Context(ctx, "docs/sessions.md", ContextOptions{Mode: ModeValue})
	if valueMode.Items[0].Mode != ModeFull {
		t.Errorf("value mode on a multi-line block falls back to full: %+v", valueMode.Items[0])
	}
	diffMode, _ := s.Context(ctx, "docs/sessions.md", ContextOptions{Mode: ModeDiff})
	for _, it := range diffMode.Items {
		if it.ID == "auth-port-h3v8n2wd" && (it.Mode != ModeLine || !strings.Contains(it.Content, "no diff")) {
			t.Errorf("diff mode without a diff = %+v", it)
		}
	}
	if rep0, _ := s.Check(ctx, CheckOptions{}); len(s.ContextFor(rep0, "docs/sessions.md", ContextOptions{}).Items) == 0 {
		t.Error("ContextFor reuses a report")
	}
	byIDCtx, _ := s.Context(ctx, "sess-save-k7m2p4xq", ContextOptions{})
	if len(byIDCtx.Items) != 2 || byIDCtx.Items[0].ID != "sess-save-k7m2p4xq" || byIDCtx.Items[0].Mode != ModeFull || !strings.HasPrefix(byIDCtx.Items[1].Why, "cites ") || !strings.Contains(byIDCtx.Items[1].Content, "Every write") {
		t.Errorf("context by id = %+v", byIDCtx.Items)
	}
	// A big unchanged block shrinks to a line in auto mode.
	c2 := cfg()
	c2.Include.MaxLines = 1
	lineMode, _ := newSys(t, repo(false), WithConfig(c2)).Context(ctx, "docs/sessions.md", ContextOptions{})
	for _, it := range lineMode.Items {
		if it.ID == "sess-save-k7m2p4xq" && (it.Mode != ModeLine || it.Content != "internal/store/write.go:4-6") {
			t.Errorf("large unchanged = %+v", it)
		}
	}

	if got := s.Find(res, "save"); len(got) != 1 || got[0].ID != "sess-save-k7m2p4xq" {
		t.Errorf("find symbol = %+v", got)
	}
	if got := s.Find(res, "config/"); len(got) != 2 {
		t.Errorf("find file = %d", len(got))
	}
	if got := s.Find(res, "remote"); len(got) != 1 {
		t.Errorf("find merged = %d", len(got))
	}
	tagged := repo(false)
	tagged["notes.txt"] = &fstest.MapFile{Data: []byte("ds:def id=note-a2b6f8jk tags=ops,db\nline\n")}
	tres, _ := newSys(t, tagged).Scan(ctx)
	if got := s.Find(tres, "db"); len(got) != 1 {
		t.Errorf("find tag = %+v", got)
	}
	if body, err := s.Read(res, "sess-save-k7m2p4xq", ""); err != nil || !strings.HasPrefix(body, "func") {
		t.Errorf("read = %q %v", body, err)
	}
	if body, err := s.Read(res, "sess-save-k7m2p4xq", "2-2"); err != nil || strings.TrimSpace(body) != "return s.sessions.Insert()" {
		t.Errorf("read lines = %q %v", body, err)
	}
	if body, err := s.Read(res, "sess-save-k7m2p4xq", "2"); err != nil || strings.TrimSpace(body) != "return s.sessions.Insert()" {
		t.Errorf("a single line reads as it renders = %q %v", body, err)
	}
	for _, bad := range []struct{ id, lines string }{{"missing", ""}, {"sess-save-k7m2p4xq", "2-9"}} {
		if _, err := s.Read(res, bad.id, bad.lines); !errors.Is(err, ErrNotFound) {
			t.Errorf("Read(%q,%q) = %v", bad.id, bad.lines, err)
		}
	}
	for _, bad := range []string{"x", "2-2xyz", "0", "2-1", " 2", "+2"} {
		if _, err := s.Read(res, "sess-save-k7m2p4xq", bad); !errors.Is(err, ErrBadLines) {
			t.Errorf("Read lines %q = %v, want ErrBadLines", bad, err)
		}
	}
	if Tokens("") != 0 || Tokens("abcd") != 1 || Tokens("abcde") != 2 || len(ModeValues) != 4 {
		t.Error("tokens")
	}
	if chainRoot(map[string]block.Block{}, "x") != "x" || rank("") != 0 || rank(check.StateOK) != 0 || rank(check.StateDeprecated) != 1 || rank(check.StateUnknown) != 2 {
		t.Error("rank/chainRoot")
	}
}

func TestMergedRefsFromWorkspace(t *testing.T) {
	t.Parallel()
	first := newSys(t, repo(false))
	res, _ := first.Scan(context.Background())
	prev, prevRefs := first.Snapshot(res)
	row := ledger.RefRow{ID: "sess-save-k7m2p4xq", Repo: "docs", Doc: "runbooks/sessions.md", Line: 3, Verb: "block", Carrier: block.CarrierLink}
	s := newSys(t, repo(true), WithPrevious(prev, prevRefs), WithMergedRefs(row))
	rep, err := s.Check(context.Background(), CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range rep.Findings {
		if f.DocRepo == "docs" && f.Doc == "runbooks/sessions.md" && f.State == check.StateUnacked {
			found = true
		}
	}
	if !found {
		t.Errorf("merged ref must be checked: %+v", rep.Findings)
	}
}

func TestResolverOption(t *testing.T) {
	t.Parallel()
	calls := 0
	resolver := func(provider, addr string) check.ResolveResult {
		calls++
		return check.ResolveResult{Checked: true, Exists: true, Hash: "H-" + provider}
	}
	s := newSys(t, repo(false), WithResolver(resolver), WithStoredHashes(map[string]string{"op-stripe-key-p9c2v7ld": "old"}))
	rep, _ := s.Check(context.Background(), CheckOptions{})
	if calls != 0 || rep.TruthHashes != nil {
		t.Error("resolver must not run without --resolve")
	}
	rep, _ = s.Check(context.Background(), CheckOptions{Resolve: true})
	if calls != 2 || rep.TruthHashes["op-stripe-key-p9c2v7ld"] != "H-1password" || rep.States[check.StateRotated] != 1 || rep.States[check.StateOutOfSync] != 1 {
		t.Errorf("resolve = calls %d states %v hashes %v", calls, rep.States, rep.TruthHashes)
	}
}

func TestRequireDocPolicy(t *testing.T) {
	t.Parallel()
	c := cfg()
	c.Policy.RequireDoc = []string{"internal/**"}
	fsys := repo(false)
	fsys["internal/gone.go"] = &fstest.MapFile{Data: []byte("package p\n\nfunc Exported() {}\n")}
	s := newSys(t, fsys, WithConfig(c))
	rep, err := s.Check(context.Background(), CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var syms []string
	for _, f := range rep.Findings {
		if f.State == check.StateUndocumented {
			syms = append(syms, f.Doc+"#"+strings.TrimPrefix(f.Message, ""))
		}
	}
	// Persist in write.go and Exported in gone.go lack defs; Save and TTL have them.
	if len(syms) != 2 || !strings.HasPrefix(syms[0], "internal/gone.go") || !strings.HasPrefix(syms[1], "internal/store/write.go") {
		t.Errorf("undocumented = %v", syms)
	}
	// A file that vanished between scan and check is skipped.
	res, _ := s.Scan(context.Background())
	delete(fsys, "internal/gone.go")
	if u, err := s.undocumented(res); err != nil || len(u) != 1 {
		t.Errorf("vanished = %+v %v", u, err)
	}
	// A syntax tier ahead of the code tier takes the .go files (bug 20); the policy
	// still covers them, because a file counts by what it is.
	fsys["internal/gone.go"] = &fstest.MapFile{Data: []byte("package p\n\nfunc Exported() {}\n")}
	rep, err = newSys(t, fsys, WithConfig(c), WithExtractor(syntaxTier{})).Check(context.Background(), CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.States[check.StateUndocumented] != 2 {
		t.Errorf("require_doc under a syntax tier = %v", rep.States)
	}
	c.Policy.RequireDoc = []string{"["}
	if _, err := newSys(t, repo(false), WithConfig(c)).Check(context.Background(), CheckOptions{}); err == nil {
		t.Error("bad policy glob must fail")
	}
	if _, err := newSys(t, repo(false), WithConfig(c)).Map(context.Background(), MapOptions{}); err == nil {
		t.Error("map propagates the policy error")
	}
}

// syntaxTier stands in for a grammar tier: it claims .go files ahead of the
// heuristic code tier, under a name of its own, and extracts as that tier does.
type syntaxTier struct{}

func (syntaxTier) Name() string        { return "go-syntax" }
func (syntaxTier) Match(p string) bool { return strings.HasSuffix(p, ".go") }
func (syntaxTier) Extract(p string, src []byte, prefix string) extract.Found {
	return extract.Code{}.Extract(p, src, prefix)
}

func TestFences(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	fsys["docs/repo.md"] = &fstest.MapFile{Data: []byte("# R\n\n<!-- ds:block id=sess-save-k7m2p4xq -->\n\nAfter.\n\n<!-- ds:block id=auth-port-h3v8n2wd lines=1-1 -->\n<!-- ds:block id=nope-a2b6f8jk -->\n<!-- ds:block id=op-stripe-key-p9c2v7ld -->\n")}
	s := newSys(t, fsys)
	ctx := context.Background()
	res, _ := s.Scan(ctx)
	out, n := s.Fences(res, "docs/repo.md", fsys["docs/repo.md"].Data)
	if n != 2 {
		t.Fatalf("fences = %d\n%s", n, out)
	}
	text := string(out)
	if !strings.Contains(text, "<!-- ds:block id=sess-save-k7m2p4xq -->\n**Store.Save**") || strings.Count(text, "<!-- /ds:block hash=") != 2 || !strings.Contains(text, "\nAfter.\n") || !strings.Contains(text, "<!-- ds:block id=nope-a2b6f8jk -->\n") {
		t.Errorf("fenced doc:\n%s", text)
	}
	// Written back, a rescan reads the regions and check reports ok; a
	// second Fences changes nothing.
	fsys["docs/repo.md"] = &fstest.MapFile{Data: out}
	s2 := newSys(t, fsys)
	res2, _ := s2.Scan(ctx)
	if again, n := s2.Fences(res2, "docs/repo.md", out); n != 0 || string(again) != text {
		t.Errorf("second fences changed %d regions", n)
	}
	rep, _ := s2.Check(ctx, CheckOptions{})
	for _, f := range rep.Findings {
		if f.Doc == "docs/repo.md" && (f.State == check.StateStale || f.State == check.StateTampered) {
			t.Errorf("fresh copy flagged: %+v", f)
		}
	}
	// The block changes: stale; refresh rewrites just that region.
	fsys["internal/store/write.go"] = &fstest.MapFile{Data: []byte(goFileChanged)}
	s3 := newSys(t, fsys)
	rep3, _ := s3.Check(ctx, CheckOptions{})
	if rep3.States[check.StateStale] != 1 {
		t.Errorf("stale after change = %v", rep3.States)
	}
	res3, _ := s3.Scan(ctx)
	if fixed, n := s3.Fences(res3, "docs/repo.md", out); n != 1 || !strings.Contains(string(fixed), "sessions.Insert") {
		t.Errorf("refresh after change = %d", n)
	}
	// A hand edit inside the copy: tampered.
	fsys["docs/repo.md"] = &fstest.MapFile{Data: []byte(strings.Replace(text, "return s.legacy.Save()", "return hacked()", 1))}
	fsys["internal/store/write.go"] = &fstest.MapFile{Data: []byte(goFile)}
	rep4, _ := newSys(t, fsys).Check(ctx, CheckOptions{})
	if rep4.States[check.StateTampered] != 1 {
		t.Errorf("tampered = %v", rep4.States)
	}
	if out, n := s.Fences(res, "docs/sessions.md", fsys["docs/sessions.md"].Data); n != 1 || !strings.Contains(string(out), "/ds:block hash=") {
		t.Errorf("build-mode doc gets a region for its block-position ref = %d", n)
	}
}

func TestRemovedAndCacheOptions(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	fsys["docs/legacy.md"] = &fstest.MapFile{Data: []byte("[old](ds:block?id=old-h3v8n2wd)\n")}
	s := newSys(t, fsys, WithRemoved(map[string]string{"old-h3v8n2wd": "legacy"}))
	rep, _ := s.Check(context.Background(), CheckOptions{})
	found := false
	for _, f := range rep.Findings {
		if f.ID == "old-h3v8n2wd" && strings.Contains(f.Message, "removed from the workspace") {
			found = true
		}
	}
	if !found {
		t.Error("repo removed message missing")
	}
	c := &countingCache{m: map[string]scan.Result{}}
	sc := newSys(t, fsys, WithExtractCache(c))
	ctx := context.Background()
	if _, err := sc.Check(ctx, CheckOptions{}); err != nil || c.puts == 0 {
		t.Fatalf("cache not used: %v puts=%d", err, c.puts)
	}
	puts := c.puts
	if _, err := sc.Check(ctx, CheckOptions{}); err != nil || c.puts != puts || c.hits == 0 {
		t.Errorf("second check must hit the cache: puts %d->%d hits %d", puts, c.puts, c.hits)
	}
	hits := c.hits
	if _, err := sc.Check(ctx, CheckOptions{Full: true}); err != nil || c.hits != hits {
		t.Error("--full must bypass the cache")
	}
	if _, err := sc.ScanFull(ctx); err != nil || c.hits != hits {
		t.Error("ScanFull must bypass the cache")
	}
}

type countingCache struct {
	m          map[string]scan.Result
	found      map[string]extract.Found
	hits, puts int
}

func (c *countingCache) Get(path, hash string) (extract.Found, bool) {
	f, ok := c.found[path+hash]
	if ok {
		c.hits++
	}
	return f, ok
}

func (c *countingCache) Put(path, hash string, f extract.Found) {
	if c.found == nil {
		c.found = map[string]extract.Found{}
	}
	c.found[path+hash] = f
	c.puts++
}

// TestEditApplyKeepsLineEndings pins that a source write touches one line
// and nothing else. Apply read a CRLF file as LF and wrote it back as LF,
// so `ds def` on a Windows checkout rewrote the ending of every line in the
// file — a whole-file diff for a one-line change. An inserted line takes
// the ending of the line it lands beside; every other byte is kept, even in
// a file whose endings are mixed.
func TestEditApplyKeepsLineEndings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, src string
		edit      Edit
		want      string
	}{
		{"crlf insert", "a\r\nb\r\nc\r\n", Edit{Line: 2, New: "// def"}, "a\r\n// def\r\nb\r\nc\r\n"},
		{"crlf replace", "a\r\nb\r\nc\r\n", Edit{Line: 2, Old: "b", New: "B"}, "a\r\nB\r\nc\r\n"},
		{"lf insert", "a\nb\n", Edit{Line: 2, New: "x"}, "a\nx\nb\n"},
		{"mixed keeps each line", "a\r\nb\nc\r\n", Edit{Line: 3, New: "x"}, "a\r\nb\nx\r\nc\r\n"},
		{"no final newline", "a\r\nb", Edit{Line: 2, New: "x"}, "a\r\nx\r\nb"},
	} {
		got, err := tc.edit.Apply([]byte(tc.src))
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: Apply = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	// A replacement is still refused when the line changed, CR or not.
	if _, err := (Edit{Line: 2, Old: "x", New: "y"}).Apply([]byte("a\r\nb\r\n")); err == nil {
		t.Error("a stale edit must be refused")
	}
}

// TestAckClaimRenewsTheClaim pins AckRequest.Claim: the claim on that line
// and only a claim, even when a citation shares the line, and an error when
// there is no claim — never a silent ack of something else. An empty ID
// without Claim keeps its existing meaning for library callers, which
// TestSecondRunUnackedThenAcked pins.
func TestAckClaimRenewsTheClaim(t *testing.T) {
	t.Parallel()
	fsys := repo(false)
	fsys["docs/claims.md"] = &fstest.MapFile{Data: []byte("Save [writes](ds:block?id=sess-save-k7m2p4xq). <!-- ds:claim reviewed=2026-01-01 expires=30d -->\nJust a cite [x](ds:block?id=sess-save-k7m2p4xq).\n")}
	s := newSys(t, fsys)
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Ack(res, AckRequest{Doc: "docs/claims.md", Line: 1, Actor: "k", Claim: true})
	if err != nil || a.ID != "" || a.BlockHash != "" {
		t.Errorf("a claim ack on a line with a cite and a claim acked %+v, %v; want the claim", a, err)
	}
	if _, err := s.Ack(res, AckRequest{Doc: "docs/claims.md", Line: 2, Actor: "k", Claim: true}); !errors.Is(err, ErrNoReference) {
		t.Errorf("no claim on the line must be an error, got %v", err)
	}
}

// TestLinesAgreeAcrossReaders pins that check, render, and Read accept the
// same `lines` fragments. Each had its own parser and they drifted: Read
// refused `lines=2`, which renders, and took `2-3xyz`, which check refuses —
// so a citation an assistant was told to read could fail on the doc it was
// rendered from. The block below is three lines long.
func TestLinesAgreeAcrossReaders(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, frag := range []string{"1", "2", "3", "1-3", "2-3", "3-3", "3-2", "0", "4", "2-4", "+2", "2-3xyz", "1-2-3", "x", "007"} {
		fsys := repo(false)
		fsys["docs/f.md"] = &fstest.MapFile{Data: []byte("<!-- ds:block id=sess-save-k7m2p4xq lines=" + frag + " -->\n")}
		s := newSys(t, fsys)
		res, err := s.Scan(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := s.Read(res, "sess-save-k7m2p4xq", frag)
		rep, err := s.Check(ctx, CheckOptions{})
		if err != nil {
			t.Fatal(err)
		}
		checkOK := true
		for _, f := range rep.Findings {
			if f.Doc == "docs/f.md" && f.State == check.StateRange {
				checkOK = false
			}
		}
		_, notes, err := s.Render(ctx, "docs/f.md", RenderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		renderOK := true
		for _, n := range notes {
			if strings.Contains(n.Message, "lines=") {
				renderOK = false
			}
		}
		if readOK := readErr == nil; readOK != checkOK || readOK != renderOK {
			t.Errorf("lines=%s: read ok %v (%v), check ok %v, render ok %v", frag, readOK, readErr, checkOK, renderOK)
		}
	}
}

// TestLineEndingsDoNotChangeTheLedger pins that a CRLF checkout of a
// repository is the same repository: every def, cite, and hash matches the
// LF checkout, and check reports the same findings. A Windows clone with
// core.autocrlf rewrites every text file; if that changed a hash, every
// block would read as drifted for whoever cloned it there. Fixtures cover
// each extractor the fixture repo reaches plus a pick= def.
func TestLineEndingsDoNotChangeTheLedger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	lf := repo(false)
	lf["config/ports.env"] = &fstest.MapFile{Data: []byte("# ds:def id=port-line-a2b6f8jk pick=line:2\nA=1\nPORT=8080\n")}
	// A file= def reads its target's raw bytes, so this is where a CRLF
	// checkout reached pick untranslated.
	lf["config/plain.txt"] = &fstest.MapFile{Data: []byte("A=1\nport: 9090\n")}
	lf["config/remote.txt"] = &fstest.MapFile{Data: []byte("ds:def id=port-remote-k7m2p4xq file=config/plain.txt pick=line:2\nds:def id=port-regex-h3v8n2wd file=config/plain.txt pick=\"regex:^port: (\\d+)$\"\n")}
	lf["docs/extra.md"] = &fstest.MapFile{Data: []byte("# Extra\n\nPort [8080](ds:cfg?id=port-line-a2b6f8jk), [9090](ds:cfg?id=port-remote-k7m2p4xq), [9090](ds:cfg?id=port-regex-h3v8n2wd).\n\n<!-- ds:block id=sess-save-k7m2p4xq lines=2 -->\n")}
	crlf := fstest.MapFS{}
	for name, f := range lf {
		crlf[name] = &fstest.MapFile{Data: []byte(strings.ReplaceAll(string(f.Data), "\n", "\r\n"))}
	}
	snap := func(fsys fstest.MapFS) (ledger.Ledger, ledger.Refs, []string) {
		s := newSys(t, fsys)
		res, err := s.Scan(ctx)
		if err != nil {
			t.Fatal(err)
		}
		l, r := s.Snapshot(res)
		rep, err := s.Check(ctx, CheckOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var states []string
		for _, f := range rep.Findings {
			states = append(states, fmt.Sprintf("%s:%d %s %s", f.Doc, f.Line, f.ID, f.State))
		}
		return l, r, states
	}
	l1, r1, f1 := snap(lf)
	l2, r2, f2 := snap(crlf)
	bound := map[string]bool{}
	for _, r := range l1.Rows {
		bound[r.ID] = true
	}
	for _, id := range []string{"port-line-a2b6f8jk", "port-remote-k7m2p4xq", "port-regex-h3v8n2wd", "sess-save-k7m2p4xq"} {
		if !bound[id] {
			t.Fatalf("%s must bind in the LF checkout, or the comparison is vacuous: %+v", id, l1.Rows)
		}
	}
	for _, f := range f1 {
		if strings.HasPrefix(f, "docs/extra.md") && !strings.HasSuffix(f, " ok") && !strings.Contains(f, "unacked") {
			t.Errorf("the LF fixture itself must be clean: %s", f)
		}
	}
	if !reflect.DeepEqual(l1.Rows, l2.Rows) {
		t.Errorf("ledger differs by line ending:\nlf   %+v\ncrlf %+v", l1.Rows, l2.Rows)
	}
	if !reflect.DeepEqual(r1.Rows, r2.Rows) {
		t.Errorf("refs differ by line ending:\nlf   %+v\ncrlf %+v", r1.Rows, r2.Rows)
	}
	if !reflect.DeepEqual(f1, f2) {
		t.Errorf("findings differ by line ending:\nlf   %v\ncrlf %v", f1, f2)
	}
}

// TestDefineRefusesWithoutACarrier is the corruption bug, reported from five
// repositories at once. `ds def` on a type with no comment syntax inserted a
// line holding only the directive: invalid syntax wherever syntax exists. A
// bare line in a go.work stopped every build in that workspace, and the same
// call on a .json would have left the file unparseable, with nothing to fall
// back to because JSON has no comment form at all.
// promise:no-carrier
func TestDefineRefusesWithoutACarrier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fsys := fstest.MapFS{
		"p.json":    &fstest.MapFile{Data: []byte("{\n  \"port\": 8080\n}\n")},
		"d.csv":     &fstest.MapFile{Data: []byte("a,b\n1,2\n")},
		"notes.txt": &fstest.MapFile{Data: []byte("one\ntwo\n")},
		"go.work":   &fstest.MapFile{Data: []byte("go 1.26\n\nuse .\n")},
		"go.mod":    &fstest.MapFile{Data: []byte("module example.com/m\n\ngo 1.26\n")},
		"app.pkl":   &fstest.MapFile{Data: []byte("dbName = \"demo\"\n")},
		"LICENSE":   &fstest.MapFile{Data: []byte("All rights reserved.\n")},
	}
	c := config.Default()
	c.Scan.Code = []string{"**"}
	c.Scan.Docs = []string{"docs/**"}
	s, err := New(WithFS(fsys), WithConfig(c), WithRepo("r"))
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		target string
		// want is the prefix of the line that would be written, or "" when the
		// call must be refused instead.
		want string
		// names is what a refusal must call the file type, so the reader can
		// look it up: the extension where there is one, the name where not.
		names string
	}{
		// No comment syntax exists for these, so there is nothing to write.
		"json is refused": {"p.json:2", "", ".json"},
		"csv is refused":  {"d.csv:2", "", ".csv"},
		// Plain text carries a directive as a line of its own; that is the
		// text tier's documented carrier, not a fallback for the unknown.
		"text takes a bare line": {"notes.txt:1", "ds:def id=", ""},
		// Go's module files take // like Go source. These are the ones that
		// broke a real workspace.
		"go.work is commented": {"go.work:1", "// ds:def id=", ""},
		"go.mod is commented":  {"go.mod:1", "// ds:def id=", ""},
		"pkl is commented":     {"app.pkl:1", "// ds:def id=", ""},
		// A name with no extension is named by the name in the refusal, since
		// that is what a reader would look up.
		"extensionless is refused by name": {"LICENSE:1", "", "LICENSE"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := s.Define(ctx, tc.target, DefineOptions{})
			if tc.want == "" {
				if !errors.Is(err, ErrNoCarrier) {
					t.Fatalf("must refuse, got %+v %v", got.Edit, err)
				}
				// A refusal that does not say what to do instead is a wall.
				if !strings.Contains(err.Error(), "remote def") {
					t.Errorf("the refusal must name the way round it: %v", err)
				}
				// The type is named the way a reader would look it up.
				if !strings.Contains(err.Error(), tc.names) {
					t.Errorf("the refusal must name %q: %v", tc.names, err)
				}
				// And nothing may be staged for writing.
				if got.Edit.New != "" {
					t.Errorf("a refused def still produced an edit: %+v", got.Edit)
				}
				return
			}
			if err != nil {
				t.Fatalf("Define = %v", err)
			}
			if !strings.HasPrefix(got.Edit.New, tc.want) {
				t.Errorf("edit = %q, want prefix %q", got.Edit.New, tc.want)
			}
		})
	}
	// A style that declares no form at all cannot carry one either. There is
	// no such entry in the table, and this is what keeps that true.
	lines := []string{"x"}
	if _, err := directiveEdit("a.formless", lines, block.Block{Pos: block.Position{Start: 1}}, "ds:def id=a-k7m2p4xq"); !errors.Is(err, ErrNoCarrier) {
		t.Errorf("an unknown type must refuse: %v", err)
	}
}

// TestEditDelete covers the delete shape of an Edit, which exists so a
// directive can be taken out of a format with no comment syntax. It must be as
// careful as the other shapes: refuse a stale file, and leave every line it does
// not remove byte for byte, line endings and all.
func TestEditDelete(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		src  string
		edit Edit
		want string
		err  bool
	}{
		"a middle line":  {"a\nX\nb\n", Edit{Line: 2, Old: "X", Delete: true, Next: "b"}, "a\nb\n", false},
		"the first line": {"X\nb\n", Edit{Line: 1, Old: "X", Delete: true, Next: "b"}, "b\n", false},
		"the last line":  {"a\nX\n", Edit{Line: 2, Old: "X", Delete: true, Next: ""}, "a\n", false},
		// With no final newline the separator before the line goes with it, so
		// the file still has none -- and undo can give back "a\nX" exactly.
		"no final newline":        {"a\nX", Edit{Line: 2, Old: "X", Delete: true, Next: ""}, "a", false},
		"CRLF endings are kept":   {"a\r\nX\r\nb\r\n", Edit{Line: 2, Old: "X", Delete: true, Next: "b"}, "a\r\nb\r\n", false},
		"a byte order mark stays": {"\xef\xbb\xbfX\nb\n", Edit{Line: 1, Old: "X", Delete: true, Next: "b"}, "\xef\xbb\xbfb\n", false},
		// Stale: the line to delete is no longer there.
		"the line changed": {"a\nY\nb\n", Edit{Line: 2, Old: "X", Delete: true, Next: "b"}, "", true},
		// Stale: the file moved around it, so undo could not find its place.
		"the next line changed": {"a\nX\nc\n", Edit{Line: 2, Old: "X", Delete: true, Next: "b"}, "", true},
		"out of range":          {"a\n", Edit{Line: 9, Old: "X", Delete: true}, "", true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := tc.edit.Apply([]byte(tc.src))
			if tc.err {
				if !errors.Is(err, ErrNotFound) {
					t.Errorf("want a refusal, got %q %v", got, err)
				}
				return
			}
			if err != nil || string(got) != tc.want {
				t.Errorf("= %q %v, want %q", got, err, tc.want)
			}
		})
	}
	// A delete is not a zero edit even though it writes no new text.
	if (Edit{Line: 1, Old: "X", Delete: true}).IsZero() {
		t.Error("a delete must not read as an edit that changes nothing")
	}
}

// TestExtractFile pins the one property the old-content hook relies on: old
// bytes come back in exactly the form a scan gives current ones -- the picked
// value, not the raw line, and a secret withheld (bug 16). Comparing a raw old
// line with an extracted new value is what diffed "port: 443 # ds:def …"
// against "8443".
func TestExtractFile(t *testing.T) {
	t.Parallel()
	c := config.Default()
	c.Scan.Code = []string{"**"}
	c.Scan.Docs = []string{"docs/**"}
	s, err := New(WithFS(fstest.MapFS{}), WithConfig(c), WithRepo("r"))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := s.ExtractFile(context.Background(), "config/app.yaml", []byte("port: 443 # ds:def id=port-k7m2p4xq env=prod\napi: sk-live-X # ds:def id=key-h3v8n2wd secret=true\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, b := range defs {
		got[b.ID] = b.Content
	}
	if got["port-k7m2p4xq"] != "443" {
		t.Errorf("a config value comes back as its value, got %q", got["port-k7m2p4xq"])
	}
	if got["key-h3v8n2wd"] != "" {
		t.Errorf("a secret's old value must be withheld like its current one, got %q", got["key-h3v8n2wd"])
	}
	// A configuration the scanner cannot compile fails here as it would in a
	// scan, rather than answering from nothing.
	bad := config.Default()
	bad.Scan.Code = []string{"["}
	bad.Scan.Docs = []string{"docs/**"}
	bs, err := New(WithFS(fstest.MapFS{}), WithConfig(bad), WithRepo("r"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bs.ExtractFile(context.Background(), "a.yaml", []byte("k: v\n")); err == nil {
		t.Error("a bad glob must fail ExtractFile")
	}
	// A cancelled scan fails too.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ExtractFile(ctx, "a.yaml", []byte("k: v # ds:def id=k-w8n4r6vc\n")); err == nil {
		t.Error("a cancelled context must fail ExtractFile")
	}
}
