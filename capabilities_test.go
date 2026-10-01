package docsync

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/pick"
	"github.com/ubgo/docsync/render"
)

// ticketVerb is a plugin verb: `ds:ticket id=T-1 state=open`.
type ticketVerb struct{ name string }

func (t ticketVerb) Name() string { return t.name }
func (ticketVerb) Carriers() []block.Carrier {
	return []block.Carrier{block.CarrierLink, block.CarrierBlock}
}

func (ticketVerb) Keys() KeySpec {
	return KeySpec{Required: []string{"state"}, Known: []string{"title"}}
}

func (ticketVerb) Check(ref block.Reference, st *VerbState) []check.Finding {
	if ref.Args["state"] == "done" {
		return []check.Finding{{State: check.StateExpired, Message: "closed"}}
	}
	if _, ok := st.Defs["sess-save-k7m2p4xq"]; !ok {
		return []check.Finding{{Message: "state view lacks the tree"}}
	}
	return nil
}

func (ticketVerb) Render(ref block.Reference, _ *VerbState, inline bool) (string, bool, error) {
	if inline {
		return "[" + ref.ID + "](https://tracker/" + ref.ID + ")", true, nil
	}
	return "> " + ref.ID + " " + ref.Args["state"], true, nil
}

type hclPicker struct{}

func (hclPicker) Scheme() string { return "hcl" }
func (hclPicker) Pick(arg, content string) (pick.Result, error) {
	return pick.Result{Kind: pick.KindValue, Value: "hcl:" + arg, Start: 1, End: 1}, nil
}

type upperRenderer struct{ fail bool }

func (u upperRenderer) Render(nodes []render.Node) ([]byte, error) {
	if u.fail {
		return nil, errors.New("renderer down")
	}
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(n.Kind + ":" + strings.ToUpper(n.Text) + "\n")
	}
	return []byte(b.String()), nil
}

type memStore struct {
	l     ledger.Ledger
	r     ledger.Refs
	a     ledger.Acks
	err   error
	saves int
}

func (m *memStore) Load(context.Context) (ledger.Ledger, ledger.Refs, ledger.Acks, error) {
	return m.l, m.r, m.a, m.err
}

func (m *memStore) Save(_ context.Context, l ledger.Ledger, r ledger.Refs, a ledger.Acks) error {
	m.l, m.r, m.a = l, r, a
	m.saves++
	return m.err
}

type recorder struct {
	findings []check.Finding
	acks     []ledger.Ack
	notified [][]check.Finding
	fail     bool
}

func (r *recorder) OnFinding(f check.Finding) { r.findings = append(r.findings, f) }
func (r *recorder) OnAck(a ledger.Ack)        { r.acks = append(r.acks, a) }
func (r *recorder) Notify(_ context.Context, fs []check.Finding) error {
	r.notified = append(r.notified, fs)
	if r.fail {
		return errors.New("channel down")
	}
	return nil
}

func TestCapabilities(t *testing.T) {
	t.Parallel()
	fsys := repo(true)
	fsys["docs/tickets.md"] = &fstest.MapFile{Data: []byte("Open [T-1](ds:ticket?id=T-1&state=open). Closed [T-2](ds:ticket?id=T-2&state=done&extra=1). Bare [T-3](ds:ticket?id=T-3).\n\n<!-- ds:ticket id=T-4 state=open -->\n")}
	fsys["infra/main.tf"] = &fstest.MapFile{Data: []byte("# ds:def id=bucket-a2b6f8jk pick=hcl:resource.name\nresource \"aws_s3_bucket\" \"logs\" {}\n")}
	c := cfg()
	c.Scan.Code = append(c.Scan.Code, "infra/**")
	first := newSys(t, repo(false), WithConfig(c))
	res0, _ := first.Scan(context.Background())
	prev, prevRefs := first.Snapshot(res0)
	store := &memStore{l: prev, r: prevRefs}
	rec := &recorder{}
	classified := 0
	s := newSys(t, fsys, WithConfig(c),
		WithVerbHandler(ticketVerb{name: "ticket"}), WithPicker(hclPicker{}),
		WithClassifier(func(o, n block.Block, oc string) []block.Class { classified++; return []block.Class{block.ClassBody} }),
		// A classifier is asked only when the old body is known (bug 28).
		WithOldContent(func(ledger.Row) (string, bool) { return "old", true }),
		WithStore(store), WithObserver(rec), WithNotifier(rec))
	ctx := context.Background()
	rep, err := s.Check(ctx, CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st := states(rep)
	if len(st[check.StateExpired]) != 1 || st[check.StateExpired][0].ID != "T-2" || st[check.StateExpired][0].Verb != "ticket" {
		t.Errorf("verb check = %+v", st[check.StateExpired])
	}
	if len(st[check.StateUnknown]) != 1 || !strings.Contains(st[check.StateUnknown][0].Message, "extra") {
		t.Errorf("unknown key on plugin verb = %+v", st[check.StateUnknown])
	}
	problems := 0
	for _, f := range st[check.StateProblem] {
		if strings.Contains(f.Message, "needs state") {
			problems++
		}
	}
	if problems != 1 {
		t.Errorf("required key = %+v", st[check.StateProblem])
	}
	if classified == 0 || len(st[check.StateUnacked]) != 2 {
		t.Errorf("classifier = %d unacked = %d", classified, len(st[check.StateUnacked]))
	}
	if b, ok := s.LocateID(rep.Scan, "bucket-a2b6f8jk"); !ok || b.Content != "hcl:resource.name" {
		t.Errorf("picker = %+v %v", b, ok)
	}
	if len(rec.findings) != len(rep.Findings) {
		t.Errorf("observer saw %d of %d findings", len(rec.findings), len(rep.Findings))
	}
	// Render through the verb and through a custom renderer.
	out, notes, err := s.Render(ctx, "docs/tickets.md", RenderOptions{})
	if err != nil || len(notes) != 0 || !strings.Contains(string(out), "[T-1](https://tracker/T-1)") || !strings.Contains(string(out), "> T-4 open") {
		t.Errorf("verb render = %s %v %v", out, notes, err)
	}
	up := newSys(t, fsys, WithConfig(c), WithVerbHandler(ticketVerb{name: "ticket"}), WithRenderer(upperRenderer{}))
	out, _, err = up.Render(ctx, "docs/tickets.md", RenderOptions{})
	if err != nil || !strings.HasPrefix(string(out), "prose:OPEN [T-1]") {
		t.Errorf("custom renderer = %s %v", out, err)
	}
	if _, _, err := newSys(t, fsys, WithConfig(c), WithRenderer(upperRenderer{fail: true})).Render(ctx, "docs/tickets.md", RenderOptions{}); err == nil {
		t.Error("renderer failure surfaces")
	}
	// Notify sends the open findings; a failing channel is reported after
	// the others ran.
	if err := s.Notify(ctx, rep); err != nil || len(rec.notified) != 1 || len(rec.notified[0]) == 0 {
		t.Errorf("notify = %v %d", err, len(rec.notified))
	}
	for _, f := range rec.notified[0] {
		if f.Severity != check.SeverityError && f.Severity != check.SeverityWarning {
			t.Errorf("notified an ok finding: %+v", f)
		}
	}
	failing := &recorder{fail: true}
	s2 := newSys(t, fsys, WithConfig(c), WithNotifier(failing), WithNotifier(rec))
	if err := s2.Notify(ctx, rep); err == nil || len(rec.notified) != 2 {
		t.Errorf("failing notifier = %v %d", err, len(rec.notified))
	}
	// Store: loaded state came from it; SaveState writes back; acks are
	// observed.
	ack, err := s.Ack(rep.Scan, AckRequest{ID: "sess-save-k7m2p4xq", Doc: "docs/sessions.md", Line: 7, Actor: "k"})
	if err != nil || len(rec.acks) != 1 {
		t.Fatalf("ack observed = %v %d", err, len(rec.acks))
	}
	store.a.Rows = append(store.a.Rows, ack)
	if err := s.SaveState(ctx, rep.Scan, store.a); err != nil || store.saves != 1 || len(store.l.Rows) != len(rep.Scan.Defs) || len(store.a.Rows) != 1 {
		t.Errorf("save state = %v %+v", err, store)
	}
	if err := newSys(t, fsys).SaveState(ctx, rep.Scan, ledger.Acks{}); !errors.Is(err, ErrNoStore) {
		t.Errorf("no store = %v", err)
	}
	if _, err := New(WithFS(fsys), WithStore(&memStore{err: errors.New("db down")})); err == nil {
		t.Error("store load failure fails New")
	}
	// Bad registrations.
	if _, err := New(WithFS(fsys), WithVerbHandler(ticketVerb{})); err == nil {
		t.Error("nameless verb")
	}
	if _, err := New(WithFS(fsys), WithVerbHandler(nil)); err == nil {
		t.Error("nil verb")
	}
	if _, err := New(WithFS(fsys), WithPicker(nil)); err == nil {
		t.Error("nil picker")
	}
	// Without handlers the hooks are nil, so check and render stay as before.
	plain := newSys(t, fsys, WithConfig(c))
	if h, k, r := plain.checkHandlers(nil); h != nil || k != nil || r != nil {
		t.Error("no handlers means nil hooks")
	}
	if plain.renderVerbs(nil) != nil {
		t.Error("no verbs means nil renderers")
	}
}
