package ledger

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/block"
)

var when = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

func sampleLedger() Ledger {
	return Ledger{
		Header: Header{Repo: "api", Commit: "7c1e2a", ScannedAt: when},
		Rows: []Row{
			{ID: "sess-ttl-p2c4y7mk", Repo: "api", Kind: block.KindKey, File: "config/auth.yaml", Symbol: "auth.session_ttl_days", Start: 4, End: 4, Hash: "c8a2e5", Stability: block.StabilityStable, Args: map[string]string{"id": "sess-ttl-p2c4y7mk"}},
			{ID: "sess-save-k7m2p4xq", Repo: "api", Kind: block.KindFunc, File: "internal/store/session.go", Symbol: "Store.SaveSession", Start: 5, End: 13, Hash: "9f3a1c", Owner: "@auth", Stability: block.StabilityAPI, Env: "prod", Args: map[string]string{"id": "sess-save-k7m2p4xq", "owner": "@auth", "desc": "dual write guard, with = and % and\ttab"}},
		},
	}
}

func TestLedgerRoundTrip(t *testing.T) {
	t.Parallel()
	l := sampleLedger()
	var buf bytes.Buffer
	if err := l.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	text := buf.String()
	// Header, columns, then rows sorted by id: sess-save before sess-ttl.
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if lines[0] != "# docsync ledger format=2 extract=1 repo=api commit=7c1e2a scanned_at=2026-09-06T12:00:00Z" {
		t.Errorf("header = %q", lines[0])
	}
	if lines[1] != strings.Join(ledgerColumns, "\t") {
		t.Errorf("columns = %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "sess-save-k7m2p4xq\t") || !strings.HasPrefix(lines[3], "sess-ttl-p2c4y7mk\t") {
		t.Errorf("rows not sorted: %q %q", lines[2], lines[3])
	}
	if strings.Count(text, "\n") != 4 {
		t.Errorf("row count: %q", text)
	}
	back, err := DecodeLedger(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	if back.Header.Kind != KindLedger || back.Header.Format != Format || back.Header.Repo != "api" || back.Header.Commit != "7c1e2a" || !back.Header.ScannedAt.Equal(when) {
		t.Errorf("header = %+v", back.Header)
	}
	want := l.Rows
	// Decoded order is file order (sorted), so compare via Index.
	got := back.Index()
	for _, w := range want {
		g, ok := got[w.ID]
		if !ok {
			t.Fatalf("missing %s", w.ID)
		}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("row %s = %#v, want %#v", w.ID, g, w)
		}
	}
	// Second encode is byte-identical.
	var buf2 bytes.Buffer
	if err := back.Encode(&buf2); err != nil {
		t.Fatal(err)
	}
	if buf2.String() != text {
		t.Errorf("encode not stable:\n%s\n---\n%s", text, buf2.String())
	}
}

func TestFromBlockToBlock(t *testing.T) {
	t.Parallel()
	b := block.Block{ID: "x-aaaaaaaa", Kind: block.KindFunc, Symbol: "F", Pos: block.Position{File: "a.go", Start: 1, End: 3}, Args: map[string]string{"owner": "@o", "stability": "api", "env": "prod"}}
	b.SetContent("body")
	r := FromBlock("api", b)
	if r.Owner != "@o" || r.Stability != block.StabilityAPI || r.Env != "prod" || r.Hash != b.Hash || r.File != "a.go" {
		t.Errorf("FromBlock = %+v", r)
	}
	back := r.ToBlock()
	if back.ID != b.ID || back.Pos != b.Pos || back.Hash != b.Hash || back.Symbol != "F" || back.Stability() != block.StabilityAPI {
		t.Errorf("ToBlock = %+v", back)
	}
}

func TestRefsRoundTripAndByID(t *testing.T) {
	t.Parallel()
	r := Refs{Header: Header{Repo: "docs"}, Rows: []RefRow{
		{ID: "b-bbbbbbbb", Repo: "docs", Doc: "docs/z.md", Line: 3, Verb: "block", Carrier: block.CarrierLink, AckedHash: "9f", SentenceHash: "s1"},
		{ID: "a-aaaaaaaa", Repo: "docs", Doc: "docs/a.md", Line: 15, Verb: "cfg", Carrier: block.CarrierLink, Env: "prod", Args: map[string]string{"format": "code"}},
		{ID: "a-aaaaaaaa", Repo: "docs", Doc: "docs/a.md", Line: 15, Verb: "block", Carrier: block.CarrierLink},
		{ID: "", Repo: "docs", Doc: "docs/a.md", Line: 9, Verb: "claim", Carrier: block.CarrierComment, Args: map[string]string{"owner": "@p"}},
		{ID: "z-zzzzzzzz", Repo: "api", Doc: "x.go", Line: 1, Verb: "block", Carrier: block.CarrierLink},
		{ID: "y-yyyyyyyy", Repo: "api", Doc: "x.go", Line: 1, Verb: "block", Carrier: block.CarrierLink},
	}}
	var buf bytes.Buffer
	if err := r.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	// Sorted: repo api first (y before z on the id tie-break), then docs/a.md:9
	// claim, docs/a.md:15 block, docs/a.md:15 cfg, docs/z.md:3.
	order := []string{"y-yyyyyyyy\tapi\tx.go\t1", "z-zzzzzzzz\tapi\tx.go\t1", "\tdocs/a.md\t9\tclaim", "\tdocs/a.md\t15\tblock", "\tdocs/a.md\t15\tcfg", "\tdocs/z.md\t3\tblock"}
	for i, o := range order {
		if !strings.Contains(lines[2+i], o) {
			t.Errorf("row %d = %q, want containing %q", i, lines[2+i], o)
		}
	}
	// A blank line between rows is tolerated.
	withBlank := strings.Replace(buf.String(), "\ny-yyyyyyyy", "\n\ny-yyyyyyyy", 1)
	back, err := DecodeRefs(strings.NewReader(withBlank))
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Rows) != 6 || back.Header.Kind != KindRefs {
		t.Fatalf("decoded %+v", back)
	}
	byID := back.ByID()
	if len(byID["a-aaaaaaaa"]) != 2 || len(byID["b-bbbbbbbb"]) != 1 || len(byID[""]) != 0 {
		t.Errorf("ByID = %v", byID)
	}
	var cfg RefRow
	for _, x := range byID["a-aaaaaaaa"] {
		if x.Verb == "cfg" {
			cfg = x
		}
	}
	if cfg.Env != "prod" || cfg.Args["format"] != "code" || cfg.Line != 15 {
		t.Errorf("cfg row = %+v", cfg)
	}
	ref := block.Reference{Verb: "block", ID: "z-zzzzzzzz", Pos: block.Position{File: "d.md", Start: 4}, Carrier: block.CarrierLink, Args: map[string]string{"env": "staging"}}
	ref.SetSentence("A sentence.")
	rr := FromReference("docs", ref)
	if rr.Env != "staging" || rr.Doc != "d.md" || rr.Line != 4 || rr.SentenceHash == "" || rr.AckedHash != "" {
		t.Errorf("FromReference = %+v", rr)
	}
}

func TestAcksRoundTripAndLatest(t *testing.T) {
	t.Parallel()
	a := Acks{Header: Header{Repo: "docs"}, Rows: []Ack{
		{At: when, Actor: "khanakia", ActorKind: ActorHuman, ID: "x-aaaaaaaa", Repo: "docs", Doc: "d.md", Line: 3, BlockHash: "h1", SentenceHash: "s1", Note: "first\nline"},
		{At: when.Add(time.Hour), Actor: "claude", ActorKind: ActorAgent, DelegatedBy: "khanakia", ID: "x-aaaaaaaa", Repo: "docs", Doc: "d.md", Line: 3, BlockHash: "h2", SentenceHash: "s1", Note: "re-ack"},
		{At: when, Actor: "someone", ActorKind: ActorHuman, ID: "y-bbbbbbbb", Repo: "docs", Doc: "d.md", Line: 9, Env: "prod", BlockHash: "h3"},
	}}
	var buf bytes.Buffer
	if err := a.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "\n") != 5 {
		t.Errorf("acks lines: %q", buf.String())
	}
	back, err := DecodeAcks(strings.NewReader(buf.String() + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Rows, a.Rows) {
		t.Errorf("acks = %#v\nwant %#v", back.Rows, a.Rows)
	}
	latest := back.Latest()
	if got := latest[AckKey{Repo: "docs", Doc: "d.md", Line: 3, ID: "x-aaaaaaaa"}]; got.BlockHash != "h2" || got.ActorKind != ActorAgent || got.DelegatedBy != "khanakia" {
		t.Errorf("latest = %+v", got)
	}
	if got := latest[AckKey{Repo: "docs", Doc: "d.md", Line: 9, ID: "y-bbbbbbbb", Env: "prod"}]; got.BlockHash != "h3" {
		t.Errorf("latest env row = %+v", got)
	}
	if len(ActorKindValues) != 2 {
		t.Error("ActorKindValues")
	}
}

func TestEscaping(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "plain", "tab\there", "new\nline", "cr\r", "back\\slash", "\\t literal", "mix\t\n\\\r"} {
		if got := unesc(esc(s)); got != s {
			t.Errorf("unesc(esc(%q)) = %q", s, got)
		}
		if strings.ContainsAny(esc(s), "\t\n\r") {
			t.Errorf("esc(%q) still has a breaking char", s)
		}
	}
	if got := unesc(`\q`); got != `\q` {
		t.Errorf("unknown escape kept literally: %q", got)
	}
	if got := unesc(`trail\`); got != `trail\` {
		t.Errorf("trailing backslash kept: %q", got)
	}
	args := map[string]string{"desc": "a b=c%d", "id": "x", "e": ""}
	enc := encodeArgs(args)
	if enc != "desc=a%20b%3Dc%25d e= id=x" {
		t.Errorf("encodeArgs = %q", enc)
	}
	dec, err := decodeArgs(enc)
	if err != nil || !reflect.DeepEqual(dec, args) {
		t.Errorf("decodeArgs = %v, %v", dec, err)
	}
	if m, err := decodeArgs(""); m != nil || err != nil {
		t.Error("empty args")
	}
	for _, bad := range []string{"noequals", "=v", "k=%zz", "k=%2"} {
		if _, err := decodeArgs(bad); !errors.Is(err, ErrField) {
			t.Errorf("decodeArgs(%q) err = %v", bad, err)
		}
	}
	if encodeArgs(nil) != "" {
		t.Error("nil args")
	}
}

// promise:format-by-name
func TestDecodeErrors(t *testing.T) {
	t.Parallel()
	head := "# docsync ledger format=2 extract=1 repo=api commit=c scanned_at=2026-09-06T12:00:00Z\n" + strings.Join(ledgerColumns, "\t") + "\n"
	goodRow := "a-aaaaaaaa\tapi\tfunc\tf.go\tF\t1-2\thash\t\tstable\t\t\n"
	for name, tc := range map[string]struct {
		in      string
		decode  func(string) error
		wantErr error
	}{
		"empty":            {"", ledgerErr, ErrBadHeader},
		"not docsync":      {"# other ledger format=2\n", ledgerErr, ErrBadHeader},
		"wrong kind":       {"# docsync refs format=1\ncols\n", ledgerErr, ErrKind},
		"newer format":     {"# docsync ledger format=99 repo=x commit=y\ncols\n", ledgerErr, ErrFormat},
		"zero format":      {"# docsync ledger repo=x\ncols\n", ledgerErr, ErrFormat},
		"bad format value": {"# docsync ledger format=x\ncols\n", ledgerErr, ErrBadHeader},
		"bad scanned_at":   {"# docsync ledger format=2 scanned_at=nope\ncols\n", ledgerErr, ErrBadHeader},
		"no column line":   {"# docsync ledger format=2\n", ledgerErr, ErrBadHeader},
		"column count":     {head + "a\tb\n", ledgerErr, ErrColumns},
		"bad lines":        {head + strings.Replace(goodRow, "1-2", "x", 1), ledgerErr, ErrField},
		"bad lines end":    {head + strings.Replace(goodRow, "1-2", "1-y", 1), ledgerErr, ErrField},
		"bad args":         {head + strings.Replace(goodRow, "\t\n", "\tnoeq\n", 1), ledgerErr, ErrField},
		"refs wrong kind":  {"# docsync ledger format=2\ncols\n", refsErr, ErrKind},
		"refs bad line":    {"# docsync refs format=2\ncols\na\tr\td\tx\tblock\tlink\t\t\t\t\t\n", refsErr, ErrField},
		"refs bad args":    {"# docsync refs format=2\ncols\na\tr\td\t1\tblock\tlink\t\t\t\t\tbad\n", refsErr, ErrField},
		"refs columns":     {"# docsync refs format=2\ncols\na\n", refsErr, ErrColumns},
		"acks wrong kind":  {"# docsync refs format=2\ncols\n", acksErr, ErrKind},
		"acks bad time":    {"# docsync acks format=2\ncols\nnope\ta\thuman\t\ti\tr\td\t1\t\th\ts\t\n", acksErr, ErrField},
		"acks bad line":    {"# docsync acks format=2\ncols\n2026-09-06T12:00:00Z\ta\thuman\t\ti\tr\td\tx\t\th\ts\t\n", acksErr, ErrField},
		"acks columns":     {"# docsync acks format=2\ncols\na\n", acksErr, ErrColumns},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := tc.decode(tc.in); !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
	// Blank lines between rows are tolerated; a single-line lines field decodes.
	l, err := DecodeLedger(strings.NewReader(head + "\n" + strings.Replace(goodRow, "1-2", "7", 1)))
	if err != nil || len(l.Rows) != 1 || l.Rows[0].Start != 7 || l.Rows[0].End != 7 {
		t.Errorf("single line field: %+v %v", l, err)
	}
	// Header without scanned_at encodes an empty value and decodes to zero.
	var buf bytes.Buffer
	if err := (Ledger{Header: Header{Repo: "r"}}).Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "scanned_at=\n") {
		t.Errorf("empty scanned_at: %q", buf.String())
	}
	if l, err := DecodeLedger(strings.NewReader(buf.String())); err != nil || !l.Header.ScannedAt.IsZero() {
		t.Errorf("zero scanned_at round trip: %v %v", l.Header, err)
	}
}

func ledgerErr(s string) error { _, err := DecodeLedger(strings.NewReader(s)); return err }
func refsErr(s string) error   { _, err := DecodeRefs(strings.NewReader(s)); return err }
func acksErr(s string) error   { _, err := DecodeAcks(strings.NewReader(s)); return err }

// failWriter fails on the Nth write to cover encoder error paths.
type failWriter struct{ n, calls int }

func (f *failWriter) Write(p []byte) (int, error) {
	f.calls++
	if f.calls >= f.n {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

func TestEncodeWriteErrors(t *testing.T) {
	t.Parallel()
	l := sampleLedger()
	if err := l.Encode(&failWriter{n: 1}); err == nil {
		t.Error("ledger encode must surface write errors")
	}
	r := Refs{Rows: []RefRow{{ID: "a", Doc: "d", Line: 1}}}
	if err := r.Encode(&failWriter{n: 1}); err == nil {
		t.Error("refs encode must surface write errors")
	}
	a := Acks{Rows: []Ack{{At: when, ID: "a"}}}
	if err := a.Encode(&failWriter{n: 1}); err == nil {
		t.Error("acks encode must surface write errors")
	}
}

func TestByHash(t *testing.T) {
	t.Parallel()
	l := Ledger{Rows: []Row{{ID: "a", Hash: "h"}, {ID: "b", Hash: "h"}, {ID: "c", Hash: "i"}}}
	m := l.ByHash()
	if len(m["h"]) != 2 || len(m["i"]) != 1 {
		t.Errorf("ByHash = %v", m)
	}
}

func TestLinesHelper(t *testing.T) {
	t.Parallel()
	if lines(3, 3) != "3" || lines(3, 9) != "3-9" {
		t.Error("lines")
	}
}

func TestBytesRoundTrip(t *testing.T) {
	t.Parallel()
	l := Ledger{Header: Header{Repo: "r", Commit: "c"}, Rows: []Row{{ID: "a-b2c3d4e5", Repo: "r", Kind: block.KindFunc, File: "f.go", Start: 1, End: 2, Hash: "h"}}}
	got, err := DecodeLedger(bytes.NewReader(l.Bytes()))
	if err != nil || len(got.Rows) != 1 || got.Rows[0].ID != "a-b2c3d4e5" {
		t.Errorf("ledger bytes round trip: %+v %v", got, err)
	}
	r := Refs{Rows: []RefRow{{ID: "a-b2c3d4e5", Doc: "d.md", Line: 3, Verb: "block", Carrier: block.CarrierLink}}}
	if got, err := DecodeRefs(bytes.NewReader(r.Bytes())); err != nil || len(got.Rows) != 1 {
		t.Errorf("refs bytes round trip: %+v %v", got, err)
	}
	a := Acks{Rows: []Ack{{At: time.Unix(0, 0).UTC(), Actor: "k", ActorKind: ActorHuman, ID: "a-b2c3d4e5", Doc: "d.md", Line: 3}}}
	if got, err := DecodeAcks(bytes.NewReader(a.Bytes())); err != nil || len(got.Rows) != 1 {
		t.Errorf("acks bytes round trip: %+v %v", got, err)
	}
}

func TestToReference(t *testing.T) {
	t.Parallel()
	row := RefRow{ID: "a-b2c3d4e5", Repo: "docs", Doc: "d.md", Line: 4, Verb: "block", Carrier: block.CarrierLink, SentenceHash: "sh", Args: map[string]string{"id": "a-b2c3d4e5", "env": "prod"}}
	ref := row.ToReference()
	if ref.ID != row.ID || ref.Pos.File != "d.md" || ref.Pos.Start != 4 || ref.Pos.End != 4 || ref.Verb != "block" || ref.Carrier != block.CarrierLink || ref.SentenceHash != "sh" || ref.Args["env"] != "prod" {
		t.Errorf("ToReference = %+v", ref)
	}
}

func TestShards(t *testing.T) {
	t.Parallel()
	l := Ledger{Header: Header{Repo: "r"}, Rows: []Row{
		{ID: "c-x", File: "internal/c.go"}, {ID: "a-x", File: "docs/a.md"}, {ID: "b-x", File: "top.go"}, {ID: "d-x", File: "internal/d.go"},
	}}
	sh := l.Shard()
	if len(sh) != 3 || len(sh["internal"].Rows) != 2 || len(sh[RootShard].Rows) != 1 || sh["docs"].Header.Repo != "r" {
		t.Errorf("shards = %+v", sh)
	}
	back := Unshard(sh)
	if len(back.Rows) != 4 || back.Rows[0].ID != "a-x" || back.Rows[3].ID != "d-x" || back.Header.Repo != "r" {
		t.Errorf("unshard = %+v", back)
	}
	if got := Unshard(nil); len(got.Rows) != 0 {
		t.Error("empty")
	}
}

// TestRefsFormat1StillReads pins the compatibility promise of the format 2
// bump: a refs file written by an older tool has no seen_hash column, and it
// must load with every other field intact rather than being rejected. The
// missing baseline is simply empty, which the next scan fills.
// promise:refs-format1
func TestRefsFormat1StillReads(t *testing.T) {
	t.Parallel()
	old := "# docsync refs format=1 repo=api commit=c scanned_at=2026-09-06T12:00:00Z\n" +
		strings.Join(refsColumnsV1, "\t") + "\n" +
		"sess-save-k7m2p4xq\tapi\tdocs/a.md\t3\tblock\tlink\tackedhash\tsenthash\tprod\tid=sess-save-k7m2p4xq\n"
	got, err := DecodeRefs(strings.NewReader(old))
	if err != nil {
		t.Fatalf("a format 1 refs file must still read: %v", err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows = %d", len(got.Rows))
	}
	r := got.Rows[0]
	if r.ID != "sess-save-k7m2p4xq" || r.Repo != "api" || r.Doc != "docs/a.md" || r.Line != 3 {
		t.Errorf("identity fields lost: %+v", r)
	}
	if r.Verb != "block" || r.Carrier != "link" {
		t.Errorf("verb/carrier lost: %+v", r)
	}
	// The columns after the inserted one must not be shifted.
	if r.AckedHash != "ackedhash" || r.SentenceHash != "senthash" || r.Env != "prod" {
		t.Errorf("columns shifted: %+v", r)
	}
	if r.SeenHash != "" {
		t.Errorf("format 1 carries no seen hash, got %q", r.SeenHash)
	}
	if r.Args["id"] != "sess-save-k7m2p4xq" {
		t.Errorf("args lost: %v", r.Args)
	}
	// Re-encoding upgrades the file in place.
	var buf strings.Builder
	if err := got.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "format=2") || !strings.Contains(buf.String(), "seen_hash") {
		t.Errorf("re-encode must upgrade:\n%s", buf.String())
	}
	// A format newer than this tool is still refused.
	if _, err := DecodeRefs(strings.NewReader("# docsync refs format=99\ncols\n")); !errors.Is(err, ErrFormat) {
		t.Errorf("a newer format must be refused, got %v", err)
	}
}

// TestForeignRoundTrip pins the snapshot format: a per-row commit, because
// rows come from several repositories and a per-file header cannot carry
// them, and a stable sort so a resync reads as a diff.
func TestForeignRoundTrip(t *testing.T) {
	t.Parallel()
	f := Foreign{
		Header: Header{Repo: "code", Commit: "c0ffee", ScannedAt: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)},
		Rows: []ForeignRow{
			{Row: Row{ID: "z-k7m2p4xq", Repo: "web", Kind: block.KindFunc, File: "a.ts", Symbol: "Save", Start: 3, End: 9, Hash: "bbb", Stability: block.StabilityAPI, Args: map[string]string{"id": "z-k7m2p4xq"}}, Commit: "w1"},
			{Row: Row{ID: "a-h3v8n2wd", Repo: "docs", Kind: block.KindSection, File: "s.md", Symbol: "Depth", Start: 4, End: 6, Hash: "aaa", Owner: "@arch", Args: map[string]string{"id": "a-h3v8n2wd"}}, Commit: "d1"},
		},
	}
	raw := f.Bytes()
	if !strings.Contains(string(raw), "docsync foreign format=2") {
		t.Fatalf("header = %q", strings.SplitN(string(raw), "\n", 2)[0])
	}
	// Sorted by id, so a resync is a readable diff rather than a reshuffle.
	body := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")[2:]
	if !strings.HasPrefix(body[0], "a-h3v8n2wd") || !strings.HasPrefix(body[1], "z-k7m2p4xq") {
		t.Errorf("rows not sorted by id: %v", body)
	}
	got, err := DecodeForeign(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("rows = %d", len(got.Rows))
	}
	a := got.Rows[0]
	if a.ID != "a-h3v8n2wd" || a.Repo != "docs" || a.Commit != "d1" || a.Hash != "aaa" || a.Owner != "@arch" {
		t.Errorf("row = %+v", a)
	}
	if a.Start != 4 || a.End != 6 || a.Kind != block.KindSection || a.Symbol != "Depth" {
		t.Errorf("position lost: %+v", a)
	}
	z := got.Rows[1]
	if z.Stability != block.StabilityAPI || z.Commit != "w1" {
		t.Errorf("row = %+v", z)
	}
	// Blocks rebuilds what a frozen check resolves citations against.
	blocks := got.Blocks()
	if len(blocks) != 2 || blocks[0].ID != "a-h3v8n2wd" || blocks[0].Hash != "aaa" {
		t.Errorf("blocks = %+v", blocks)
	}
	// One id can have several rows when the def is per environment; env is
	// the tiebreak so the order stays stable across syncs.
	perEnv := Foreign{Rows: []ForeignRow{
		{Row: Row{ID: "cfg-k7m2p4xq", Repo: "api", Env: "staging", Hash: "s"}, Commit: "a1"},
		{Row: Row{ID: "cfg-k7m2p4xq", Repo: "api", Env: "prod", Hash: "p"}, Commit: "a1"},
	}}
	rows := strings.Split(strings.TrimRight(string(perEnv.Bytes()), "\n"), "\n")[2:]
	if !strings.Contains(rows[0], "\tprod\t") || !strings.Contains(rows[1], "\tstaging\t") {
		t.Errorf("rows not sorted by env within an id: %v", rows)
	}
	// Blank lines are skipped, so a hand-edited file still reads.
	spaced, err := DecodeForeign(strings.NewReader(string(raw) + "\n\n"))
	if err != nil || len(spaced.Rows) != 2 {
		t.Errorf("blank lines must be skipped: %d %v", len(spaced.Rows), err)
	}

	// Malformed input is refused rather than half-read.
	head := "# docsync foreign format=2 extract=1 repo=code commit=c scanned_at=2026-09-06T12:00:00Z\n" + strings.Join(foreignColumns, "\t") + "\n"
	for name, tc := range map[string]struct {
		in   string
		want error
	}{
		"wrong kind": {"# docsync ledger format=2\ncols\n", ErrKind},
		"columns":    {head + "a\n", ErrColumns},
		"bad lines":  {head + "a\tr\tc\tsection\tf\ts\tnope\th\to\tstable\t\t\n", ErrField},
		"bad args":   {head + "a\tr\tc\tsection\tf\ts\t1-2\th\to\tstable\t\tbad\n", ErrField},
	} {
		if _, err := DecodeForeign(strings.NewReader(tc.in)); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

// TestHeaderExtractRule pins the field that makes a stored hash
// interpretable: which extraction rule produced it. A file written before
// it existed is rule 1 by definition, and a decode/encode round trip must
// not change that.
// Pins bug 12.
func TestHeaderExtractRule(t *testing.T) {
	t.Parallel()
	// Absent reads as 1.
	old := "# docsync ledger format=1 repo=api commit=c scanned_at=2026-09-06T12:00:00Z\n" + strings.Join(ledgerColumns, "\t") + "\n"
	got, err := DecodeLedger(strings.NewReader(old))
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.Extract != 1 {
		t.Errorf("absent extract = %d, want 1", got.Header.Extract)
	}
	// Present is carried, and survives a round trip.
	at2 := "# docsync ledger format=2 extract=2 repo=api commit=c scanned_at=2026-09-06T12:00:00Z\n" + strings.Join(ledgerColumns, "\t") + "\n"
	got, err = DecodeLedger(strings.NewReader(at2))
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.Extract != 2 {
		t.Fatalf("extract = %d", got.Header.Extract)
	}
	var buf strings.Builder
	if err := got.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "extract=2") {
		t.Errorf("re-encode lost the rule: %s", buf.String())
	}
	// A rule that is not a positive integer is a malformed header, not a
	// silent zero.
	for _, bad := range []string{"extract=0", "extract=x", "extract=-1"} {
		in := "# docsync ledger format=2 " + bad + " repo=api\ncols\n"
		if _, err := DecodeLedger(strings.NewReader(in)); !errors.Is(err, ErrBadHeader) {
			t.Errorf("%s = %v", bad, err)
		}
	}
}

// TestAcksFormat3 pins the ack log's own layout: rule and sentence round
// trip, a format 2 log still reads with both empty, and a bad rule is
// refused rather than read as zero.
func TestAcksFormat3(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	in := Acks{Rows: []Ack{{At: at, Actor: "k", ActorKind: ActorHuman, ID: "a-a2b6f8jk", Repo: "r", Doc: "d.md", Line: 3, BlockHash: "h", SentenceHash: "s", Note: "n", Rule: 4, Sentence: "Every write\tgoes through it."}}}
	out, err := DecodeAcks(bytes.NewReader(in.Bytes()))
	if err != nil || !reflect.DeepEqual(out.Rows, in.Rows) || out.Header.Format != AcksFormat {
		t.Fatalf("round trip = %+v %v", out, err)
	}
	v2 := "# docsync acks format=2 extract=3 repo=r commit=c scanned_at=\n" + strings.Join(acksColumnsV2, "\t") + "\n" +
		"2026-09-06T12:00:00Z\tk\thuman\t\ta-a2b6f8jk\tr\td.md\t3\t\th\ts\tn\n"
	old, err := DecodeAcks(strings.NewReader(v2))
	if err != nil || len(old.Rows) != 1 || old.Rows[0].Rule != 0 || old.Rows[0].Sentence != "" || old.Rows[0].Note != "n" {
		t.Fatalf("format 2 = %+v %v", old, err)
	}
	bad := strings.Replace(string(in.Bytes()), "\t4\t", "\tfour\t", 1)
	if _, err := DecodeAcks(strings.NewReader(bad)); !errors.Is(err, ErrField) {
		t.Errorf("a bad rule = %v", err)
	}
	// The published files keep Format; only the ack log moved on.
	if AcksFormat <= Format {
		t.Errorf("AcksFormat %d must be newer than Format %d", AcksFormat, Format)
	}
	if _, err := DecodeLedger(strings.NewReader(strings.Replace(string(Ledger{}.Bytes()), "format=2", "format=3", 1))); !errors.Is(err, ErrFormat) {
		t.Errorf("a ledger claiming the acks format = %v", err)
	}
}
