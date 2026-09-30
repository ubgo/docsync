package ledger

import (
	"sort"
	"time"
)

// Carry decides, for each current citation, which previous refs row it
// continues, so a citation keeps its baselines when it moves. It returns one
// index into prev per current row, or -1 for a citation that is new.
//
// Why it exists: baselines used to be looked up by the exact doc and line.
// A citation that moved missed that lookup and was treated as new, and a new
// citation is baselined at the block's current hash — so adding one line
// above a citation, or renaming its doc, silently accepted every change to
// the cited block that nobody had reviewed. That is the one thing the tool
// exists to prevent, and it was reached by the most ordinary edit there is.
//
// The rule it follows is that carrying an older baseline can only add a
// report, never remove one, while pairing two citations of the same block
// wrongly can remove one: if the acked citation's baseline lands on the
// unreviewed one, its drift disappears. So it pairs only where the pairing
// is certain, and everywhere else it is conservative.
//
//  1. Same id and env with the same, non-empty sentence hash: the sentence
//     itself moved. Same doc first, in line order, then across docs, which
//     is a rename.
//  2. What remains, per doc: one previous and one current citation of the
//     id is a certain pairing. Anything else is ambiguous.
//  3. What remains in docs with no previous citation of the id, against
//     previous rows whose doc has none left: one and one is a rename whose
//     sentence also changed. Anything else is ambiguous.
//  4. An ambiguous citation gets the candidate whose baseline most needs
//     reporting: the first, in doc and line order, whose effective baseline
//     differs from the block's current hash. Over-reporting costs an ack;
//     under-reporting is a wrong "up to date".
//
// effective returns the baseline a previous row stood for (the latest ack at
// its position, else its acked hash, else its seen hash); current returns the
// hash now of the block a current citation resolves to — per citation, since
// its env and branch pick among defs sharing an id — or "" when it is not
// known. Both come from the caller because they are state this package does
// not hold.
//
// Invariant: rows are grouped by id and env and never paired across groups,
// and for citations at distinct positions the result is independent of the
// order prev and cur arrive in (two citations of one id on one line are
// indistinguishable, and every consumer keys them by position anyway).
func Carry(prev, cur []RefRow, effective func(RefRow) string, current func(RefRow) string) []int {
	out := make([]int, len(cur))
	for i := range out {
		out[i] = -1
	}
	type group struct{ prev, cur []int }
	groups := map[refIdentity]*group{}
	for j, p := range prev {
		k := refIdentity{p.ID, p.Env}
		if groups[k] == nil {
			groups[k] = &group{}
		}
		groups[k].prev = append(groups[k].prev, j)
	}
	for i, c := range cur {
		k := refIdentity{c.ID, c.Env}
		if groups[k] == nil {
			groups[k] = &group{}
		}
		groups[k].cur = append(groups[k].cur, i)
	}
	byPos := func(rows []RefRow) func(a, b int) bool {
		return func(a, b int) bool {
			if rows[a].Doc != rows[b].Doc {
				return rows[a].Doc < rows[b].Doc
			}
			return rows[a].Line < rows[b].Line
		}
	}
	for _, g := range groups {
		sortIdx(g.prev, byPos(prev))
		sortIdx(g.cur, byPos(cur))
		takenPrev := map[int]bool{}
		done := map[int]bool{}
		pair := func(c, p int) { out[c], takenPrev[p], done[c] = p, true, true }

		// 1. The sentence moved: same doc first, then any doc.
		for _, sameDoc := range []bool{true, false} {
			for _, c := range g.cur {
				if done[c] || cur[c].SentenceHash == "" {
					continue
				}
				for _, p := range g.prev {
					if takenPrev[p] || prev[p].SentenceHash != cur[c].SentenceHash || (sameDoc && prev[p].Doc != cur[c].Doc) {
						continue
					}
					pair(c, p)
					break
				}
			}
		}

		// 2. Per doc, what is left.
		leftPrev := byDoc(g.prev, prev, takenPrev)
		leftCur := byDoc(g.cur, cur, done)
		var orphanCur []int
		for doc, cs := range leftCur {
			ps := leftPrev[doc]
			switch {
			case len(ps) == 0:
				orphanCur = append(orphanCur, cs...)
			case len(ps) == 1 && len(cs) == 1:
				pair(cs[0], ps[0])
			default:
				for _, c := range cs {
					out[c], done[c] = conservative(ps, prev, cur[c], effective, current), true
				}
				for _, p := range ps {
					takenPrev[p] = true
				}
			}
		}

		// 3. Docs that gained the citation, against docs that lost it.
		var vanished []int
		for doc, ps := range leftPrev {
			if len(leftCur[doc]) != 0 {
				continue
			}
			for _, p := range ps {
				if !takenPrev[p] {
					vanished = append(vanished, p)
				}
			}
		}
		sortIdx(vanished, byPos(prev))
		sortIdx(orphanCur, byPos(cur))
		switch {
		case len(vanished) == 0:
		case len(vanished) == 1 && len(orphanCur) == 1:
			pair(orphanCur[0], vanished[0])
		default:
			for _, c := range orphanCur {
				out[c] = conservative(vanished, prev, cur[c], effective, current)
			}
		}
	}
	return out
}

// refIdentity is what two citations must share to be the same citation.
type refIdentity struct{ id, env string }

// byDoc groups the untaken indexes by the doc of the row they point at.
func byDoc(idx []int, rows []RefRow, taken map[int]bool) map[string][]int {
	m := map[string][]int{}
	for _, i := range idx {
		if !taken[i] {
			m[rows[i].Doc] = append(m[rows[i].Doc], i)
		}
	}
	return m
}

// conservative picks, from candidates already in doc and line order, the
// first whose baseline differs from the block's current hash, so that an
// unreviewed change any of them stood for is still reported. When none
// differs, or the current hash is unknown, the first is as good as any.
func conservative(cands []int, prev []RefRow, row RefRow, effective func(RefRow) string, current func(RefRow) string) int {
	now := current(row)
	if now != "" {
		for _, p := range cands {
			if b := effective(prev[p]); b != "" && b != now {
				return p
			}
		}
	}
	return cands[0]
}

// sortIdx orders indexes by a comparison over the rows they point at, with
// the index itself as the tiebreak so the order is total.
func sortIdx(idx []int, less func(a, b int) bool) {
	sort.SliceStable(idx, func(a, b int) bool {
		if less(idx[a], idx[b]) {
			return true
		}
		if less(idx[b], idx[a]) {
			return false
		}
		return idx[a] < idx[b]
	})
}

// Baseline is what one current citation is measured against: the hash it
// was last acked at, and the hash it was first seen at. Either may be empty;
// an empty Seen means the citation is new and the caller baselines it at the
// block's current hash.
type Baseline struct {
	Acked string
	Seen  string
	// Ack is the ack row Acked came from, when it came from one: the one
	// at the citation's position or, for a moved citation, the one carried
	// from its old position, or the one behind a hash refs.tsv carried
	// (carriedAck). It carries the sentence the ack approved, which check
	// compares with the sentence there now (SPEC §18). Nil when no ack row
	// names the baseline.
	Ack *Ack
}

// Baselines resolves, for each current citation, the acked and seen hashes
// it is measured against, following it across moves (see Carry). It is the
// one place that decision is made, so `scan` writing refs.tsv and `check`
// reading it cannot disagree about which ack covers which sentence.
//
// An ack recorded at a citation's current position is the newest statement
// about the sentence there, and wins — with one exception, which is the
// point of this function. When the citation moved, the ack at its new
// position may belong to a different citation that used to sit on that
// line; taking it would let an acked sentence's approval cover an unreviewed
// one. So for a moved citation that ack is trusted only when it names the
// same sentence (both sentence hashes non-empty and equal), or, for a
// citation with no sentence to compare, when it was recorded after
// scannedAt — the last scan, whose layout the move is measured from.
func Baselines(prev []RefRow, cur []RefRow, latest map[AckKey]Ack, scannedAt time.Time, current func(RefRow) string) []Baseline {
	effective := func(x RefRow) string {
		if a, ok := latest[x.Key()]; ok && a.BlockHash != "" {
			return a.BlockHash
		}
		if x.AckedHash != "" {
			return x.AckedHash
		}
		return x.SeenHash
	}
	from := Carry(prev, cur, effective, current)
	out := make([]Baseline, len(cur))
	for i, row := range cur {
		here, ackedHere := latest[row.Key()]
		j := from[i]
		if j < 0 {
			if ackedHere {
				out[i].Acked, out[i].Ack = here.BlockHash, &here
			}
			continue
		}
		p := prev[j]
		out[i].Seen = p.SeenHash
		moved := p.Key() != row.Key()
		if ackedHere && (!moved || ackFits(here, row, scannedAt)) {
			out[i].Acked, out[i].Ack = here.BlockHash, &here
			continue
		}
		if a, ok := latest[p.Key()]; ok {
			out[i].Acked, out[i].Ack = a.BlockHash, &a
			continue
		}
		out[i].Acked = p.AckedHash
		out[i].Ack = carriedAck(latest, row, p.AckedHash)
	}
	return out
}

// carriedAck finds the ack row behind an acked hash that refs.tsv carried
// to a citation's position, so the sentence that ack approved still stands
// beside the one there now (SPEC §18).
//
// Why it exists: a citation acked at one line and then moved keeps its
// acked hash through refs.tsv, but its ack row stays at the old line. The
// first check after the move follows it there; once a scan has recorded the
// new line, the old row no longer names any citation, and without this the
// wording check silently stopped applying to every acked citation that had
// ever moved.
//
// The candidates are the latest acks of the same id, env and repository at
// that same block hash. Several citations of one block acked together are
// indistinguishable by hash, so the question asked of them is the one the
// check needs answered: did any of them approve the sentence there now? If
// one did, it is the baseline and nothing is reported. If none did, the
// sentence is not one that was approved, and the candidate in the same doc
// (else the newest) stands for it, so the rewrite is reported rather than
// missed — the rule Carry follows: over-reporting costs an ack. Nil when no
// ack row names the hash, which leaves a hash-only baseline as it was.
func carriedAck(latest map[AckKey]Ack, row RefRow, hash string) *Ack {
	var best *Ack
	for _, a := range latest {
		if a.ID != row.ID || a.Env != row.Env || a.Repo != row.Repo || a.BlockHash != hash {
			continue
		}
		if a.SentenceHash != "" && a.SentenceHash == row.SentenceHash {
			return &a
		}
		if best == nil || preferCarried(a, *best, row.Doc) {
			best = &a
		}
	}
	return best
}

// preferCarried orders two candidate acks for a citation in doc: the same
// doc first, then the newest, then position, so the pick never depends on
// map order.
func preferCarried(a, b Ack, doc string) bool {
	if (a.Doc == doc) != (b.Doc == doc) {
		return a.Doc == doc
	}
	if !a.At.Equal(b.At) {
		return a.At.After(b.At)
	}
	if a.Doc != b.Doc {
		return a.Doc < b.Doc
	}
	return a.Line < b.Line
}

// ackFits reports whether an ack found at a moved citation's new position is
// about that citation rather than one that used to be there.
func ackFits(a Ack, row RefRow, scannedAt time.Time) bool {
	if a.SentenceHash != "" && row.SentenceHash != "" {
		return a.SentenceHash == row.SentenceHash
	}
	return !scannedAt.IsZero() && a.At.After(scannedAt)
}
