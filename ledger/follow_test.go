package ledger

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

const (
	hashOld = "old"
	hashNow = "now"
)

// ref builds a refs row. seen is its baseline; an empty sentence models a
// comment-carried citation, which has none.
func ref(id, doc string, line int, sentence, seen string) RefRow {
	return RefRow{ID: id, Repo: "api", Doc: doc, Line: line, Verb: "block", SentenceHash: sentence, SeenHash: seen}
}

func effectiveSeen(r RefRow) string {
	if r.AckedHash != "" {
		return r.AckedHash
	}
	return r.SeenHash
}

func nowFor(RefRow) string { return hashNow }

func TestCarry(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		prev []RefRow
		cur  []RefRow
		want []int
	}{
		{
			name: "nothing moved",
			prev: []RefRow{ref("a", "d.md", 3, "s1", hashOld)},
			cur:  []RefRow{ref("a", "d.md", 3, "s1", "")},
			want: []int{0},
		},
		{
			name: "a line inserted above a linked citation",
			prev: []RefRow{ref("a", "d.md", 3, "s1", hashOld)},
			cur:  []RefRow{ref("a", "d.md", 5, "s1", "")},
			want: []int{0},
		},
		{
			name: "a line inserted above a comment-carried citation",
			prev: []RefRow{ref("a", "d.md", 3, "", hashOld)},
			cur:  []RefRow{ref("a", "d.md", 5, "", "")},
			want: []int{0},
		},
		{
			name: "the doc renamed, linked",
			prev: []RefRow{ref("a", "old.md", 3, "s1", hashOld)},
			cur:  []RefRow{ref("a", "new.md", 3, "s1", "")},
			want: []int{0},
		},
		{
			name: "the doc renamed, comment-carried",
			prev: []RefRow{ref("a", "old.md", 3, "", hashOld)},
			cur:  []RefRow{ref("a", "new.md", 3, "", "")},
			want: []int{0},
		},
		{
			// The trap: position alone would give the first citation the
			// second one's baseline once both shift past each other's lines.
			name: "two linked citations shift past each other's old lines",
			prev: []RefRow{ref("a", "d.md", 3, "s1", hashOld), ref("a", "d.md", 7, "s2", hashNow)},
			cur:  []RefRow{ref("a", "d.md", 7, "s1", ""), ref("a", "d.md", 11, "s2", "")},
			want: []int{0, 1},
		},
		{
			name: "a sentence edited in place keeps its position's baseline",
			prev: []RefRow{ref("a", "d.md", 3, "s1", hashOld)},
			cur:  []RefRow{ref("a", "d.md", 3, "s1-edited", "")},
			want: []int{0},
		},
		{
			name: "a new citation is new",
			prev: nil,
			cur:  []RefRow{ref("a", "d.md", 3, "s1", "")},
			want: []int{-1},
		},
		{
			name: "another id is never a predecessor",
			prev: []RefRow{ref("b", "d.md", 3, "s1", hashOld)},
			cur:  []RefRow{ref("a", "d.md", 3, "s1", "")},
			want: []int{-1},
		},
		{
			name: "another env is never a predecessor",
			prev: []RefRow{{ID: "a", Doc: "d.md", Line: 3, Env: "prod", SentenceHash: "s1", SeenHash: hashOld}},
			cur:  []RefRow{{ID: "a", Doc: "d.md", Line: 3, Env: "dev", SentenceHash: "s1"}},
			want: []int{-1},
		},
		{
			// Ambiguous: two comment citations became three. Every one of
			// them gets the baseline that still reports the drift.
			name: "sentence-less citations added in one doc are ambiguous",
			prev: []RefRow{ref("a", "d.md", 3, "", hashNow), ref("a", "d.md", 7, "", hashOld)},
			cur:  []RefRow{ref("a", "d.md", 4, "", ""), ref("a", "d.md", 8, "", ""), ref("a", "d.md", 12, "", "")},
			want: []int{1, 1, 1},
		},
		{
			// One deleted, one added: order would pair the acked row with
			// the unreviewed one. Conservative says both report.
			name: "sentence-less delete plus add is ambiguous",
			prev: []RefRow{ref("a", "d.md", 3, "", hashNow), ref("a", "d.md", 7, "", hashOld)},
			cur:  []RefRow{ref("a", "d.md", 7, "", ""), ref("a", "d.md", 10, "", "")},
			want: []int{1, 1},
		},
		{
			name: "ambiguous with nothing drifted takes the first",
			prev: []RefRow{ref("a", "d.md", 3, "", hashNow), ref("a", "d.md", 7, "", hashNow)},
			cur:  []RefRow{ref("a", "d.md", 4, "", ""), ref("a", "d.md", 8, "", ""), ref("a", "d.md", 9, "", "")},
			want: []int{0, 0, 0},
		},
		{
			name: "two docs merged into one new doc is ambiguous",
			prev: []RefRow{ref("a", "x.md", 3, "", hashNow), ref("a", "y.md", 3, "", hashOld)},
			cur:  []RefRow{ref("a", "z.md", 3, "", ""), ref("a", "z.md", 9, "", "")},
			want: []int{1, 1},
		},
		{
			// A doc that still cites the id keeps its own rows; only a doc
			// that lost every citation is a rename candidate.
			name: "a doc that still cites the id is not a rename source",
			prev: []RefRow{ref("a", "x.md", 3, "", hashOld), ref("a", "x.md", 9, "", hashNow)},
			cur:  []RefRow{ref("a", "x.md", 3, "", ""), ref("a", "y.md", 3, "", "")},
			want: []int{0, -1},
		},
		{
			name: "a sentence moved to another doc follows it",
			prev: []RefRow{ref("a", "x.md", 3, "s1", hashOld), ref("a", "x.md", 9, "s2", hashNow)},
			cur:  []RefRow{ref("a", "x.md", 9, "s2", ""), ref("a", "y.md", 1, "s1", "")},
			want: []int{1, 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Carry(tc.prev, tc.cur, effectiveSeen, nowFor); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Carry = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCarryUnknownCurrentHash covers a citation whose block is not known now
// (deleted, or foreign and unsynced): nothing can be compared, so an
// ambiguous citation takes the first candidate rather than guessing.
func TestCarryUnknownCurrentHash(t *testing.T) {
	t.Parallel()
	prev := []RefRow{ref("a", "d.md", 3, "", hashNow), ref("a", "d.md", 7, "", hashOld)}
	cur := []RefRow{ref("a", "d.md", 4, "", ""), ref("a", "d.md", 8, "", ""), ref("a", "d.md", 9, "", "")}
	got := Carry(prev, cur, effectiveSeen, func(RefRow) string { return "" })
	if !reflect.DeepEqual(got, []int{0, 0, 0}) {
		t.Errorf("Carry = %v", got)
	}
}

// TestCarryNeverLaunders is the guarantee stated directly, over edits no
// hand-written table would think of: a citation whose true predecessor had
// an unreviewed change must still be measured against a baseline that
// differs from the current hash, however its doc was edited. The generator
// shifts lines, renames docs, deletes citations, adds citations, and edits
// sentences; it knows each current citation's true predecessor and asks
// only that the one Carry chose does not hide what that predecessor showed.
func TestCarryNeverLaunders(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(20260921))
	const trials = 20000
	for trial := 0; trial < trials; trial++ {
		var prev, cur []RefRow
		var truth []int
		docs := 1 + rng.Intn(3)
		for d := 0; d < docs; d++ {
			doc := fmt.Sprintf("d%d.md", d)
			n := 1 + rng.Intn(4)
			line := 1
			for k := 0; k < n; k++ {
				line += 1 + rng.Intn(5)
				sentence := ""
				if rng.Intn(2) == 0 {
					sentence = fmt.Sprintf("s-%d-%d", d, k)
				}
				seen := hashNow
				if rng.Intn(2) == 0 {
					seen = hashOld
				}
				prev = append(prev, ref("a", doc, line, sentence, seen))
			}
		}
		// Edit: per doc, maybe rename, shift, and per citation maybe
		// delete, maybe edit the sentence; then maybe add new citations.
		renamed := map[string]string{}
		shift := map[string]int{}
		for d := 0; d < docs; d++ {
			doc := fmt.Sprintf("d%d.md", d)
			renamed[doc] = doc
			if rng.Intn(4) == 0 {
				renamed[doc] = fmt.Sprintf("r%d.md", d)
			}
			shift[doc] = rng.Intn(6)
		}
		for j, p := range prev {
			if rng.Intn(5) == 0 {
				continue // deleted
			}
			c := ref("a", renamed[p.Doc], p.Line+shift[p.Doc], p.SentenceHash, "")
			if c.SentenceHash != "" && rng.Intn(5) == 0 {
				c.SentenceHash += "-edited"
			}
			cur = append(cur, c)
			truth = append(truth, j)
		}
		for k := rng.Intn(3); k > 0; k-- {
			doc := renamed[fmt.Sprintf("d%d.md", rng.Intn(docs))]
			cur = append(cur, ref("a", doc, 100+rng.Intn(50), "", ""))
			truth = append(truth, -1)
		}
		rng.Shuffle(len(cur), func(a, b int) { cur[a], cur[b], truth[a], truth[b] = cur[b], cur[a], truth[b], truth[a] })

		got := Carry(prev, cur, effectiveSeen, nowFor)
		for i, j := range truth {
			if j < 0 || effectiveSeen(prev[j]) == hashNow {
				continue // nothing unreviewed to lose
			}
			if got[i] < 0 || effectiveSeen(prev[got[i]]) == hashNow {
				t.Fatalf("trial %d: citation %+v had unreviewed drift through %+v, but Carry chose %d, which hides it\nprev %+v\ncur  %+v", trial, cur[i], prev[j], got[i], prev, cur)
			}
		}
	}
}

// TestCarryOrderIndependent: the same citations in any arrival order pair
// the same way, because a parallel scan does not promise an order.
func TestCarryOrderIndependent(t *testing.T) {
	t.Parallel()
	prev := []RefRow{ref("a", "x.md", 3, "s1", hashOld), ref("a", "x.md", 9, "", hashNow), ref("b", "y.md", 2, "", hashOld), ref("a", "z.md", 4, "", hashOld)}
	cur := []RefRow{ref("a", "x.md", 5, "s1", ""), ref("a", "x.md", 11, "", ""), ref("b", "y.md", 2, "", ""), ref("a", "w.md", 4, "", "")}
	pairs := func(p, c []RefRow) map[string]string {
		m := map[string]string{}
		for i, j := range Carry(p, c, effectiveSeen, nowFor) {
			k := fmt.Sprintf("%s@%s:%d", c[i].ID, c[i].Doc, c[i].Line)
			if j < 0 {
				m[k] = "new"
				continue
			}
			m[k] = fmt.Sprintf("%s:%d", p[j].Doc, p[j].Line)
		}
		return m
	}
	want := pairs(prev, cur)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		p := append([]RefRow(nil), prev...)
		c := append([]RefRow(nil), cur...)
		rng.Shuffle(len(p), func(a, b int) { p[a], p[b] = p[b], p[a] })
		rng.Shuffle(len(c), func(a, b int) { c[a], c[b] = c[b], c[a] })
		if got := pairs(p, c); !reflect.DeepEqual(got, want) {
			t.Fatalf("order changed the pairing:\n got %v\nwant %v", got, want)
		}
	}
}

// TestBaselines pins which ack covers a citation, one case per decision.
// The cases that matter most are the refusals: an ack found at a moved
// citation's new position is only trusted when it provably belongs to it.
func TestBaselines(t *testing.T) {
	t.Parallel()
	scanned := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	before, after := scanned.Add(-time.Hour), scanned.Add(time.Hour)
	ack := func(doc string, line int, hash, sentence string, at time.Time) Ack {
		return Ack{At: at, ID: "a", Repo: "api", Doc: doc, Line: line, BlockHash: hash, SentenceHash: sentence}
	}
	latestOf := func(as ...Ack) map[AckKey]Ack {
		return Acks{Rows: as}.Latest()
	}
	withAcked := func(r RefRow, h string) RefRow { r.AckedHash = h; return r }
	for _, tc := range []struct {
		name      string
		prev, cur []RefRow
		latest    map[AckKey]Ack
		scannedAt time.Time
		want      Baseline
	}{
		{
			name: "a new citation with no ack has no baseline",
			cur:  []RefRow{ref("a", "d.md", 3, "s1", "")},
			want: Baseline{},
		},
		{
			name:   "a new citation acked where it stands takes that ack",
			cur:    []RefRow{ref("a", "d.md", 3, "s1", "")},
			latest: latestOf(ack("d.md", 3, "h-ack", "s1", after)),
			want:   Baseline{Acked: "h-ack"},
		},
		{
			name:   "an unmoved citation: its ack wins over the carried hash",
			prev:   []RefRow{withAcked(ref("a", "d.md", 3, "s1", hashOld), "h-carried")},
			cur:    []RefRow{ref("a", "d.md", 3, "s1", "")},
			latest: latestOf(ack("d.md", 3, "h-ack", "", before)),
			want:   Baseline{Acked: "h-ack", Seen: hashOld},
		},
		{
			name: "an unmoved citation with no ack keeps the carried hashes",
			prev: []RefRow{withAcked(ref("a", "d.md", 3, "s1", hashOld), "h-carried")},
			cur:  []RefRow{ref("a", "d.md", 3, "s1", "")},
			want: Baseline{Acked: "h-carried", Seen: hashOld},
		},
		{
			name:   "moved: the ack at its old position follows it",
			prev:   []RefRow{ref("a", "d.md", 3, "s1", hashOld)},
			cur:    []RefRow{ref("a", "d.md", 5, "s1", "")},
			latest: latestOf(ack("d.md", 3, "h-old-pos", "s1", before)),
			want:   Baseline{Acked: "h-old-pos", Seen: hashOld},
		},
		{
			name: "moved, no ack anywhere: the carried acked hash",
			prev: []RefRow{withAcked(ref("a", "d.md", 3, "s1", hashOld), "h-carried")},
			cur:  []RefRow{ref("a", "d.md", 5, "s1", "")},
			want: Baseline{Acked: "h-carried", Seen: hashOld},
		},
		{
			name:   "moved: an ack at the new position for the same sentence is trusted",
			prev:   []RefRow{ref("a", "d.md", 3, "s1", hashOld)},
			cur:    []RefRow{ref("a", "d.md", 5, "s1", "")},
			latest: latestOf(ack("d.md", 5, "h-new-pos", "s1", before)),
			want:   Baseline{Acked: "h-new-pos", Seen: hashOld},
		},
		{
			// The swap: the ack at line 5 was for another sentence that used
			// to sit there. Taking it would cover this one's drift.
			name:   "moved: an ack at the new position for another sentence is refused",
			prev:   []RefRow{ref("a", "d.md", 3, "s1", hashOld)},
			cur:    []RefRow{ref("a", "d.md", 5, "s1", "")},
			latest: latestOf(ack("d.md", 5, "h-someone-else", "s-other", after)),
			want:   Baseline{Seen: hashOld},
		},
		{
			name:      "moved without a sentence: an ack made after the last scan is trusted",
			prev:      []RefRow{ref("a", "d.md", 3, "", hashOld)},
			cur:       []RefRow{ref("a", "d.md", 5, "", "")},
			latest:    latestOf(ack("d.md", 5, "h-new-pos", "", after)),
			scannedAt: scanned,
			want:      Baseline{Acked: "h-new-pos", Seen: hashOld},
		},
		{
			name:      "moved without a sentence: an ack older than the last scan is refused",
			prev:      []RefRow{ref("a", "d.md", 3, "", hashOld)},
			cur:       []RefRow{ref("a", "d.md", 5, "", "")},
			latest:    latestOf(ack("d.md", 5, "h-stale", "", before)),
			scannedAt: scanned,
			want:      Baseline{Seen: hashOld},
		},
		{
			name:   "moved without a sentence and no scan time: refused, nothing to measure by",
			prev:   []RefRow{ref("a", "d.md", 3, "", hashOld)},
			cur:    []RefRow{ref("a", "d.md", 5, "", "")},
			latest: latestOf(ack("d.md", 5, "h-new-pos", "", after)),
			want:   Baseline{Seen: hashOld},
		},
		{
			// Ambiguous: which previous row stood for a change is decided by
			// each one's effective baseline — the ack at its position first,
			// then its acked hash, then its seen hash.
			name: "ambiguous: the ack at a candidate's position decides",
			prev: []RefRow{ref("a", "d.md", 3, "", hashOld), ref("a", "d.md", 7, "", hashOld)},
			cur:  []RefRow{ref("a", "d.md", 4, "", ""), ref("a", "d.md", 8, "", ""), ref("a", "d.md", 9, "", "")},
			// Line 3 was acked at the current hash, so line 7 is the one
			// that still shows a change, through its seen hash.
			latest: latestOf(ack("d.md", 3, hashNow, "", before)),
			want:   Baseline{Seen: hashOld},
		},
		{
			name: "ambiguous: a carried acked hash is a candidate's baseline",
			prev: []RefRow{withAcked(ref("a", "d.md", 3, "", hashOld), hashNow), withAcked(ref("a", "d.md", 7, "", hashNow), hashOld)},
			cur:  []RefRow{ref("a", "d.md", 4, "", ""), ref("a", "d.md", 8, "", ""), ref("a", "d.md", 9, "", "")},
			want: Baseline{Acked: hashOld, Seen: hashNow},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := Baselines(tc.prev, tc.cur, tc.latest, tc.scannedAt, nowFor)
			// Every current citation must resolve the same way here; for the
			// single-citation cases that is the only one.
			for i, b := range got {
				if b.Acked != tc.want.Acked || b.Seen != tc.want.Seen {
					t.Errorf("Baselines[%d] = %+v, want %+v", i, b, tc.want)
				}
				// The ack row, when there is one, is where Acked came from.
				if b.Ack != nil && b.Ack.BlockHash != b.Acked {
					t.Errorf("Baselines[%d].Ack = %+v does not carry Acked %q", i, b.Ack, b.Acked)
				}
			}
		})
	}
}

// TestCarriedAck pins the ack found behind a hash refs.tsv carried: the one
// that approved the sentence there now when any did, and otherwise the one
// that stands for a rewrite, so an acked citation that moved and was
// re-scanned is still held to its wording.
func TestCarriedAck(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	ack := func(doc string, line int, hash, sentence string, at time.Time) Ack {
		return Ack{At: at, ID: "a", Repo: "api", Doc: doc, Line: line, BlockHash: hash, SentenceHash: sentence}
	}
	row := func(doc, sentence string) RefRow {
		r := ref("a", doc, 9, sentence, hashOld)
		r.AckedHash = "h"
		return r
	}
	for _, tc := range []struct {
		name string
		acks []Ack
		row  RefRow
		want string // "doc:line" of the ack picked, "" for none
	}{
		{name: "no ack names the hash", acks: []Ack{ack("d.md", 3, "other", "s1", t0)}, row: row("d.md", "s1"), want: ""},
		{name: "the one ack that names it", acks: []Ack{ack("d.md", 3, "h", "s-old", t0)}, row: row("d.md", "s1"), want: "d.md:3"},
		{
			name: "the ack that approved this sentence wins over a newer one in the same doc",
			acks: []Ack{ack("d.md", 3, "h", "s-other", t0.Add(time.Hour)), ack("e.md", 4, "h", "s1", t0)},
			row:  row("d.md", "s1"),
			want: "e.md:4",
		},
		{
			name: "none approved it: the same doc before a newer one elsewhere",
			acks: []Ack{ack("e.md", 3, "h", "s-x", t0.Add(time.Hour)), ack("d.md", 4, "h", "s-y", t0)},
			row:  row("d.md", "s1"),
			want: "d.md:4",
		},
		{
			name: "none approved it, none in the doc: the newest",
			acks: []Ack{ack("e.md", 3, "h", "s-x", t0), ack("f.md", 4, "h", "s-y", t0.Add(time.Hour))},
			row:  row("d.md", "s1"),
			want: "f.md:4",
		},
		{
			name: "a tie in time falls to doc, then line, never to map order",
			acks: []Ack{ack("f.md", 3, "h", "s-x", t0), ack("e.md", 5, "h", "s-y", t0), ack("e.md", 4, "h", "s-z", t0)},
			row:  row("d.md", "s1"),
			want: "e.md:4",
		},
		{
			name: "an empty sentence hash never counts as approving",
			acks: []Ack{ack("e.md", 3, "h", "", t0.Add(time.Hour)), ack("d.md", 4, "h", "s-y", t0)},
			row:  row("d.md", ""),
			want: "d.md:4",
		},
		{
			name: "another id, env or repository is never a candidate",
			acks: []Ack{
				{At: t0, ID: "b", Repo: "api", Doc: "d.md", Line: 1, BlockHash: "h", SentenceHash: "s1"},
				{At: t0, ID: "a", Repo: "api", Env: "prod", Doc: "d.md", Line: 2, BlockHash: "h", SentenceHash: "s1"},
				{At: t0, ID: "a", Repo: "web", Doc: "d.md", Line: 3, BlockHash: "h", SentenceHash: "s1"},
			},
			row:  row("d.md", "s1"),
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := carriedAck(Acks{Rows: tc.acks}.Latest(), tc.row, "h")
			where := ""
			if got != nil {
				where = fmt.Sprintf("%s:%d", got.Doc, got.Line)
			}
			if where != tc.want {
				t.Errorf("carriedAck = %q, want %q", where, tc.want)
			}
		})
	}
}

// TestBaselinesKeepsTheAckBehindACarriedHash is the case the wording check
// lost: acked at line 3, moved to line 9, re-scanned, so refs.tsv holds the
// acked hash at line 9 and the ack row still names line 3.
func TestBaselinesKeepsTheAckBehindACarriedHash(t *testing.T) {
	t.Parallel()
	a := Ack{At: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), ID: "a", Repo: "api", Doc: "d.md", Line: 3, BlockHash: "h", SentenceHash: "s1", Sentence: "The old wording."}
	prev := ref("a", "d.md", 9, "s1", hashOld)
	prev.AckedHash = "h"
	got := Baselines([]RefRow{prev}, []RefRow{ref("a", "d.md", 9, "s2", "")}, Acks{Rows: []Ack{a}}.Latest(), time.Time{}, nowFor)
	if got[0].Acked != "h" || got[0].Ack == nil || got[0].Ack.Sentence != a.Sentence {
		t.Fatalf("Baselines = %+v, want the line 3 ack behind hash h", got[0])
	}
}

// TestRefRowKey pins that a refs row and the ack recorded for it agree on
// the position they name; Baselines matches the two by it.
func TestRefRowKey(t *testing.T) {
	t.Parallel()
	r := RefRow{ID: "a", Repo: "api", Doc: "d.md", Line: 3, Env: "prod"}
	if got, want := r.Key(), (AckKey{Repo: "api", Doc: "d.md", Line: 3, ID: "a", Env: "prod"}); got != want {
		t.Errorf("Key = %+v, want %+v", got, want)
	}
}

// TestLatestIsNewestNotLast pins what a merged ack log needs: the newest ack
// for a sentence wins wherever it sits in the file, and file order only
// breaks a tie between acks made in the same second.
func TestLatestIsNewestNotLast(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	row := func(at time.Time, hash string) Ack {
		return Ack{At: at, ID: "a", Repo: "api", Doc: "d.md", Line: 3, BlockHash: hash}
	}
	key := AckKey{Repo: "api", Doc: "d.md", Line: 3, ID: "a"}
	// Branch two's newer ack merged in before branch one's older one.
	merged := Acks{Rows: []Ack{row(t0.Add(time.Hour), "newer"), row(t0, "older")}}
	if got := merged.Latest()[key].BlockHash; got != "newer" {
		t.Errorf("after a merge the newest ack must win, got %q", got)
	}
	tie := Acks{Rows: []Ack{row(t0, "first"), row(t0, "second")}}
	if got := tie.Latest()[key].BlockHash; got != "second" {
		t.Errorf("a tie goes to the later row, got %q", got)
	}
}
