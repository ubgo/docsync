package docsync

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/ledger"
)

// sameLineDoc holds two sentences on one line citing one id, then a line
// with one citation.
const sameLineDoc = "Save [writes](ds:block?id=sess-save-k7m2p4xq) once. Save [never](ds:block?id=sess-save-k7m2p4xq) fails.\nAlone [x](ds:block?id=sess-save-k7m2p4xq) here.\n"

// sameLineSystems scans a repository holding doc, then returns a system
// over the same repository with the cited block changed and acks as given.
func sameLineSystems(t *testing.T, doc string, acks ...ledger.Ack) (*System, Report) {
	t.Helper()
	before := repo(false)
	before["docs/line.md"] = &fstest.MapFile{Data: []byte(doc)}
	first := newSys(t, before)
	res, err := first.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	prev, prevRefs := first.Snapshot(res)
	after := repo(true)
	after["docs/line.md"] = &fstest.MapFile{Data: []byte(doc)}
	s := newSys(t, after, WithPrevious(prev, prevRefs), WithAcks(ledger.Acks{Rows: acks}))
	rep, err := s.Check(context.Background(), CheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return s, rep
}

// lineFindings is what check reports on docs/line.md, by line.
func lineFindings(rep Report) map[int][]check.Finding {
	out := map[int][]check.Finding{}
	for _, f := range rep.Findings {
		if f.Doc == "docs/line.md" {
			out[f.Line] = append(out[f.Line], f)
		}
	}
	return out
}

// TestAckEachAcksEverySentenceOnTheLine is bug 157 end to end in the
// library: two sentences on one line cite one id, the block changes, and
// an ack of the line approves both, each with its own sentence. Before, the
// ack recorded the first sentence only, its row covered both by position,
// and the second was then reported as the first one rewritten.
func TestAckEachAcksEverySentenceOnTheLine(t *testing.T) {
	t.Parallel()
	s, rep := sameLineSystems(t, sameLineDoc)
	if got := lineFindings(rep)[1]; len(got) != 2 || got[0].State != check.StateUnacked || got[1].State != check.StateUnacked {
		t.Fatalf("before the ack, line 1 = %+v; want both sentences unacked", got)
	}
	rows, err := s.AckEach(rep.Scan, AckRequest{ID: "sess-save-k7m2p4xq", Doc: "docs/line.md", Line: 1, Actor: "k"})
	if err != nil || len(rows) != 2 || rows[0].SentenceHash == rows[1].SentenceHash || !strings.Contains(rows[0].Sentence, "once") || !strings.Contains(rows[1].Sentence, "fails") {
		t.Fatalf("AckEach = %+v, %v; want one row per sentence, in order", rows, err)
	}
	// Ack keeps its one-row contract: the first sentence's row.
	if one, err := s.Ack(rep.Scan, AckRequest{ID: "sess-save-k7m2p4xq", Doc: "docs/line.md", Line: 1, Actor: "k"}); err != nil || one.SentenceHash != rows[0].SentenceHash {
		t.Errorf("Ack = %+v, %v; want the first sentence's row", one, err)
	}

	_, both := sameLineSystems(t, sameLineDoc, rows...)
	if got := lineFindings(both)[1]; len(got) != 2 || got[0].State != check.StateOK || got[1].State != check.StateOK {
		t.Errorf("after acking the line, line 1 = %+v; want both ok", got)
	}
	if got := lineFindings(both)[2]; len(got) != 1 || got[0].State != check.StateUnacked {
		t.Errorf("the line not acked = %+v; want it still unacked", got)
	}

	// With only the first sentence acked, the second is unacked for the
	// change, never "rewritten" against the first one's wording.
	_, one := sameLineSystems(t, sameLineDoc, rows[0])
	got := lineFindings(one)[1]
	if len(got) != 2 {
		t.Fatalf("line 1 = %+v", got)
	}
	for _, f := range got {
		acked := strings.Contains(f.Sentence, "once")
		if acked != (f.State == check.StateOK) || strings.Contains(f.Message, "rewritten") {
			t.Errorf("with the first sentence acked, %q = %s %q", f.Sentence, f.State, f.Message)
		}
	}
}

// TestAckEachSelects pins which citations on a line one request acks: every
// one with the id of the first match, of the same kind (claim or not) and
// env; two identical sentences once.
func TestAckEachSelects(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		line string
		req  AckRequest
		want int
	}{
		{name: "two identical sentences give one row", line: "Save [a](ds:block?id=sess-save-k7m2p4xq). Save [a](ds:block?id=sess-save-k7m2p4xq).", req: AckRequest{ID: "sess-save-k7m2p4xq"}, want: 1},
		{name: "another id on the line is not acked", line: "Save [a](ds:block?id=sess-save-k7m2p4xq). TTL [b](ds:block?id=sess-ttl-p2c4y7mk).", req: AckRequest{ID: "sess-save-k7m2p4xq"}, want: 1},
		{name: "no id takes the first citation's id", line: "Save [a](ds:block?id=sess-save-k7m2p4xq). TTL [b](ds:block?id=sess-ttl-p2c4y7mk). Save [c](ds:block?id=sess-save-k7m2p4xq) too.", req: AckRequest{}, want: 2},
		{name: "a citation for another env is not acked", line: "Save [a](ds:block?id=sess-save-k7m2p4xq). Save [b](ds:block?id=sess-save-k7m2p4xq&env=prod) too.", req: AckRequest{ID: "sess-save-k7m2p4xq"}, want: 1},
		{name: "a claim is not acked with a citation", line: "Save [a](ds:block?id=sess-save-k7m2p4xq). <!-- ds:claim reviewed=2026-09-01 expires=30d --> It holds.", req: AckRequest{}, want: 1},
		{name: "two claims are renewed together", line: "One holds. <!-- ds:claim reviewed=2026-09-01 expires=30d --> Two holds. <!-- ds:claim reviewed=2026-09-01 expires=30d -->", req: AckRequest{Claim: true}, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := repo(false)
			fsys["docs/line.md"] = &fstest.MapFile{Data: []byte(tc.line + "\n")}
			s := newSys(t, fsys)
			res, err := s.Scan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			req := tc.req
			req.Doc, req.Line, req.Actor = "docs/line.md", 1, "k"
			rows, err := s.AckEach(res, req)
			if err != nil || len(rows) != tc.want {
				t.Errorf("AckEach = %d rows %+v, %v; want %d", len(rows), rows, err, tc.want)
			}
		})
	}
}

// TestTwoClaimsOnOneLineRenewEachOnItsOwn is bug 157 for claims: two
// claims on one line, both expired. Renewing one leaves the other expired,
// and renewing the line renews both. By position the two shared one
// renewal, which held for whichever sentence it named.
func TestTwoClaimsOnOneLineRenewEachOnItsOwn(t *testing.T) {
	t.Parallel()
	doc := "One holds. <!-- ds:claim reviewed=2026-01-01 expires=30d --> Two holds. <!-- ds:claim reviewed=2026-01-01 expires=30d -->\n"
	s, rep := sameLineSystems(t, doc)
	if got := lineFindings(rep)[1]; len(got) != 2 || got[0].State != check.StateExpired || got[1].State != check.StateExpired {
		t.Fatalf("line 1 = %+v; want two expired claims", got)
	}
	rows, err := s.AckEach(rep.Scan, AckRequest{Doc: "docs/line.md", Line: 1, Actor: "k", Claim: true})
	if err != nil || len(rows) != 2 {
		t.Fatalf("AckEach = %+v, %v", rows, err)
	}
	_, one := sameLineSystems(t, doc, rows[0])
	var ok, expired int
	for _, f := range lineFindings(one)[1] {
		switch f.State {
		case check.StateOK:
			ok++
		case check.StateExpired:
			expired++
		}
	}
	if ok != 1 || expired != 1 {
		t.Errorf("one claim renewed: %+v; want one ok and one expired", lineFindings(one)[1])
	}
	_, both := sameLineSystems(t, doc, rows...)
	for _, f := range lineFindings(both)[1] {
		if f.State != check.StateOK {
			t.Errorf("both renewed: %+v", f)
		}
	}
}

// TestFindingOfTakesTheCitationsOwnFinding pins bug 158: the editor's
// hover looked a citation's finding up by line and id, so the second of two
// sentences on a line citing one id showed the first one's state.
func TestFindingOfTakesTheCitationsOwnFinding(t *testing.T) {
	t.Parallel()
	ref := func(sentence string) block.Reference {
		r := block.Reference{ID: "a", Pos: block.Position{File: "d.md", Start: 3}}
		r.SetSentence(sentence)
		return r
	}
	findings := []check.Finding{
		{Doc: "d.md", Line: 3, ID: "a", Sentence: "First.", State: check.StateOK},
		{Doc: "d.md", Line: 3, ID: "a", Sentence: "Second.", State: check.StateUnacked},
	}
	if f, ok := FindingOf(findings, "d.md", ref("Second.")); !ok || f.State != check.StateUnacked {
		t.Errorf("FindingOf(second) = %+v, %v; want its own, unacked", f, ok)
	}
	// A sentence no finding names (the text moved under it) falls back to
	// the first on its line with its id.
	if f, ok := FindingOf(findings, "d.md", ref("Gone.")); !ok || f.Sentence != "First." {
		t.Errorf("FindingOf(gone) = %+v, %v; want the first on the line", f, ok)
	}
	if _, ok := FindingOf(findings, "d.md", block.Reference{ID: "b", Pos: block.Position{File: "d.md", Start: 3}}); ok {
		t.Error("FindingOf matched another id")
	}
	// An id-less reference, a claim, takes any id on its line.
	if _, ok := FindingOf(findings, "d.md", block.Reference{Pos: block.Position{File: "d.md", Start: 3}}); !ok {
		t.Error("FindingOf missed a claim's line")
	}
}

// TestBlameShowsTheReferencesOwnFinding pins that the finding Blame shows
// beside a reference is that reference's (FindingOf). Blame shows the first
// reference on a line, and findings keep reference order, so the line alone
// already found it; this keeps it so if either changes.
func TestBlameShowsTheReferencesOwnFinding(t *testing.T) {
	t.Parallel()
	s, rep := sameLineSystems(t, sameLineDoc)
	rows, err := s.AckEach(rep.Scan, AckRequest{ID: "sess-save-k7m2p4xq", Doc: "docs/line.md", Line: 1, Actor: "k"})
	if err != nil {
		t.Fatal(err)
	}
	// Only the second sentence acked: blame's reference is the first, which
	// is unacked.
	s2, rep2 := sameLineSystems(t, sameLineDoc, rows[1])
	b, err := s2.Blame(rep2, "docs/line.md", 1)
	if err != nil || b.Finding.State != check.StateUnacked || b.Finding.Sentence != b.Reference.Sentence {
		t.Errorf("blame = reference %q, finding %q %s; want the reference's own finding", b.Reference.Sentence, b.Finding.Sentence, b.Finding.State)
	}
}

// TestStalestCountsEachSentence pins bug 159: the report's stalest pages
// found a citation's ack by line, so a page whose acked line also held a
// citation nobody acked, of the same id or another, counted as reviewed.
func TestStalestCountsEachSentence(t *testing.T) {
	t.Parallel()
	s, rep := sameLineSystems(t, sameLineDoc)
	rows, err := s.AckEach(rep.Scan, AckRequest{ID: "sess-save-k7m2p4xq", Doc: "docs/line.md", Line: 1, Actor: "k"})
	if err != nil {
		t.Fatal(err)
	}
	line2, err := s.AckEach(rep.Scan, AckRequest{ID: "sess-save-k7m2p4xq", Doc: "docs/line.md", Line: 2, Actor: "k"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		acks  []ledger.Ack
		never bool
	}{
		{name: "one sentence of line 1 acked", acks: append([]ledger.Ack{rows[0]}, line2...), never: true},
		{name: "every sentence acked", acks: append(append([]ledger.Ack{}, rows...), line2...), never: false},
	} {
		s2, rep2 := sameLineSystems(t, sameLineDoc, tc.acks...)
		for _, st := range s2.Report(rep2, ReportOptions{}).Stalest {
			if st.Doc == "docs/line.md" && st.NeverAck != tc.never {
				t.Errorf("%s: stalest %+v, want NeverAck %v", tc.name, st, tc.never)
			}
		}
	}
}
