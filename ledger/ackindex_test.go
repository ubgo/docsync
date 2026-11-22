package ledger

import (
	"testing"
	"time"
)

// ackOn is an ack of id "a" at d.md:3 for one sentence.
func ackOn(sentence, hash string, at time.Time) Ack {
	return Ack{At: at, ID: "a", Repo: "api", Doc: "d.md", Line: 3, BlockHash: hash, SentenceHash: sentence, Sentence: "text of " + sentence}
}

// TestAckIndexAssign pins bug 157: of two sentences on one line citing one
// id, each is covered by its own ack. By position alone they shared one,
// so acking the line approved only one of them and the other was compared
// with its neighbour's wording.
func TestAckIndexAssign(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	row := func(sentence string) RefRow { return ref("a", "d.md", 3, sentence, hashOld) }
	for _, tc := range []struct {
		name string
		acks []Ack
		rows []RefRow
		want []string // the sentence hash of each row's ack, "-" for none
	}{
		{
			name: "one citation: the newest ack at its position, whatever its sentence",
			acks: []Ack{ackOn("s-old", "h1", t0), ackOn("s-other", "h2", t0.Add(time.Hour))},
			rows: []RefRow{row("s1")},
			want: []string{"s-other"},
		},
		{
			name: "two sentences, both acked: each its own",
			acks: []Ack{ackOn("s1", "h1", t0), ackOn("s2", "h2", t0)},
			rows: []RefRow{row("s1"), row("s2")},
			want: []string{"s1", "s2"},
		},
		{
			name: "two sentences, one acked: the other has none, never its neighbour's",
			acks: []Ack{ackOn("s1", "h1", t0)},
			rows: []RefRow{row("s1"), row("s2")},
			want: []string{"s1", "-"},
		},
		{
			name: "a rewritten sentence takes the ack its neighbour did not",
			acks: []Ack{ackOn("s1", "h1", t0), ackOn("s2-old", "h2", t0)},
			rows: []RefRow{row("s1"), row("s2")},
			want: []string{"s1", "s2-old"},
		},
		{
			name: "two rewritten at once: acks made together go in log order",
			acks: []Ack{ackOn("s1-old", "h", t0), ackOn("s2", "h", t0), ackOn("s3-old", "h", t0)},
			rows: []RefRow{row("s1"), row("s2"), row("s3")},
			want: []string{"s1-old", "s2", "s3-old"},
		},
		{
			name: "a rewritten sentence takes the newest untaken ack",
			acks: []Ack{ackOn("s2-older", "h1", t0), ackOn("s2-old", "h2", t0.Add(time.Hour)), ackOn("s1", "h", t0)},
			rows: []RefRow{row("s1"), row("s2")},
			want: []string{"s1", "s2-old"},
		},
		{
			name: "two identical sentences share one ack",
			acks: []Ack{ackOn("s1", "h1", t0)},
			rows: []RefRow{row("s1"), row("s1")},
			want: []string{"s1", "s1"},
		},
		{
			name: "an ack recorded without a sentence goes to a sentence left without one",
			acks: []Ack{ackOn("", "h1", t0), ackOn("s1", "h1", t0)},
			rows: []RefRow{row("s1"), row("s2")},
			want: []string{"s1", ""},
		},
		{
			name: "another position has none",
			acks: []Ack{ackOn("s1", "h1", t0)},
			rows: []RefRow{ref("a", "d.md", 4, "s1", hashOld), ref("b", "d.md", 3, "s1", hashOld)},
			want: []string{"-", "-"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Acks{Rows: tc.acks}.Index().Assign(tc.rows)
			for i, a := range got {
				s := "-"
				if a != nil {
					s = a.SentenceHash
				}
				if s != tc.want[i] {
					t.Errorf("row %d (%s) got the ack of %q, want %q", i, tc.rows[i].SentenceHash, s, tc.want[i])
				}
			}
		})
	}
}

// TestAckIndexNewestAgreesWithLatest pins that the index's newest ack at a
// position is the one Latest picks, ties included, so a line with one
// citation is decided exactly as before bug 157.
func TestAckIndexNewestAgreesWithLatest(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	acks := Acks{Rows: []Ack{
		ackOn("s1", "a", t0.Add(time.Hour)), ackOn("s2", "b", t0), ackOn("s3", "c", t0.Add(time.Hour)), ackOn("s1", "d", t0),
	}}
	k := AckKey{Repo: "api", Doc: "d.md", Line: 3, ID: "a"}
	got, ok := acks.Index().Newest(k)
	if want := acks.Latest()[k]; !ok || got != want {
		t.Errorf("Newest = %+v, want %+v", got, want)
	}
	if _, ok := acks.Index().Newest(AckKey{Doc: "none.md"}); ok {
		t.Error("Newest found an ack at a position with none")
	}
	if n := len(acks.Index().All()); n != 3 {
		t.Errorf("All = %d acks, want one per sentence: 3", n)
	}
}

// TestBaselinesTwoSentencesOnOneLine is bug 157 through Baselines, which
// scan and check share: two sentences on one line, the first acked at the
// current hash and the second never, are measured each on its own. Before,
// the first one's ack covered both.
func TestBaselinesTwoSentencesOnOneLine(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	prev := []RefRow{ref("a", "d.md", 3, "s1", hashOld), ref("a", "d.md", 3, "s2", hashOld)}
	cur := []RefRow{ref("a", "d.md", 3, "s1", ""), ref("a", "d.md", 3, "s2", "")}
	got := Baselines(prev, cur, Acks{Rows: []Ack{ackOn("s1", hashNow, t0)}}.Index(), time.Time{}, nowFor)
	if got[0].Acked != hashNow || got[0].Ack == nil || got[0].Ack.SentenceHash != "s1" {
		t.Errorf("the acked sentence = %+v, want its own ack", got[0])
	}
	if got[1].Acked != "" || got[1].Ack != nil || got[1].Seen != hashOld {
		t.Errorf("the sentence never acked = %+v, want only its seen hash", got[1])
	}
}

// TestCarriedAckSkipsANeighboursAck pins the other half of bug 157: a hash
// refs.tsv carried for a sentence is not explained by the ack of another
// sentence still on its line, which would compare it with that sentence's
// wording.
func TestCarriedAckSkipsANeighboursAck(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	idx := Acks{Rows: []Ack{ackOn("s1", "h", t0)}}.Index()
	row := ref("a", "d.md", 3, "s2", hashOld)
	if got := carriedAck(idx, row, "h", map[string]bool{"s1": true, "s2": true}); got != nil {
		t.Errorf("carriedAck = %+v, want none: s1's ack belongs to s1", got)
	}
	if got := carriedAck(idx, row, "h", map[string]bool{"s2": true}); got == nil {
		t.Error("carriedAck missed the ack of a sentence no longer on the line")
	}
}
