package ledger

import "sort"

// CiteKey identifies one citation: the position its acks are recorded at
// and the hash of its sentence. Two citations of one id on one line differ
// only by their sentence, so every per-citation state is keyed by this, not
// by the position alone (bug 157). Two citations with the same sentence at
// the same position are the same statement twice and share one state.
type CiteKey struct {
	AckKey
	Sentence string
}

// CiteKey is the key a citation's state is held under.
func (r RefRow) CiteKey() CiteKey {
	return CiteKey{AckKey: r.Key(), Sentence: r.SentenceHash}
}

// key is the position an ack row was recorded at.
func (a Ack) key() AckKey {
	return AckKey{Repo: a.Repo, Doc: a.Doc, Line: a.Line, ID: a.ID, Env: a.Env}
}

// AckIndex is the ack log arranged to answer which ack covers a citation.
//
// Why it exists: acks are recorded by position (doc, line, id, env), and
// Latest keeps the newest one per position. When two sentences on one line
// cite the same id, both are at that position, so Latest kept one ack for
// the two and the other sentence could never be acked: every ack of it
// either replaced its neighbour's or was compared with its neighbour's
// wording (bug 157). The index keeps the newest ack of every sentence
// recorded at a position, and Assign hands each citation its own.
type AckIndex struct {
	// at holds, per position, the newest ack of each sentence recorded
	// there, newest first.
	at map[AckKey][]indexed
}

// indexed is an ack and its place in the log, which breaks ties in time.
type indexed struct {
	Ack
	n int
}

// Index arranges the log. Within one position and sentence the newest ack
// wins, file order breaking a tie, as in Latest; the acks of one position
// are ordered newest first by the same rule, so the first is what Latest
// would return.
func (a Acks) Index() AckIndex {
	type slot struct {
		k        AckKey
		sentence string
	}
	best := map[slot]int{}
	for i, x := range a.Rows {
		s := slot{x.key(), x.SentenceHash}
		if j, ok := best[s]; ok && x.At.Before(a.Rows[j].At) {
			continue
		}
		best[s] = i
	}
	byPos := map[AckKey][]int{}
	for s, i := range best {
		byPos[s.k] = append(byPos[s.k], i)
	}
	idx := AckIndex{at: map[AckKey][]indexed{}}
	for k, is := range byPos {
		sort.Slice(is, func(p, q int) bool {
			x, y := a.Rows[is[p]], a.Rows[is[q]]
			if !x.At.Equal(y.At) {
				return x.At.After(y.At)
			}
			return is[p] > is[q]
		})
		for _, i := range is {
			idx.at[k] = append(idx.at[k], indexed{a.Rows[i], i})
		}
	}
	return idx
}

// Newest is the newest ack recorded at a position, whatever its sentence.
func (x AckIndex) Newest(k AckKey) (Ack, bool) {
	if as := x.at[k]; len(as) > 0 {
		return as[0].Ack, true
	}
	return Ack{}, false
}

// All is every ack the index holds: the newest of each sentence at each
// position. Order is unspecified.
func (x AckIndex) All() []Ack {
	var out []Ack
	for _, as := range x.at {
		for _, a := range as {
			out = append(out, a.Ack)
		}
	}
	return out
}

// Assign returns, for each row, the ack recorded at its position that is
// its own, or nil.
//
// A position whose citations all have one sentence (the usual case: one
// citation on the line) takes the newest ack there, as it always has, so a
// rewritten sentence is compared with the wording last acked. A position
// holding several sentences shares its acks out:
//
//  1. An ack whose sentence is one of them belongs to that sentence and to
//     no other.
//  2. Each remaining sentence, in the order the rows give, takes the newest
//     ack not yet taken: it was rewritten, and some ack approved its old
//     wording. Acks made together (one `ds ack` run, so one time) are taken
//     in log order, which is the order the run found the sentences in, so
//     two sentences rewritten at once are each shown their own old wording.
//     In other cases which one is a guess, but never a neighbour's that is
//     still on the line, so the rewrite is reported.
//  3. A sentence left with none has no ack: it was never acked, and is
//     measured against its other baselines.
func (x AckIndex) Assign(rows []RefRow) []*Ack {
	out := make([]*Ack, len(rows))
	groups := map[AckKey][]int{}
	var order []AckKey
	for i, r := range rows {
		k := r.Key()
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}
	for _, k := range order {
		cands := append([]indexed(nil), x.at[k]...)
		if len(cands) == 0 {
			continue
		}
		is := groups[k]
		var sentences []string
		present := map[string]bool{}
		for _, i := range is {
			if s := rows[i].SentenceHash; !present[s] {
				present[s] = true
				sentences = append(sentences, s)
			}
		}
		own := map[string]Ack{}
		if len(sentences) == 1 {
			own[sentences[0]] = cands[0].Ack
		} else {
			// Newest first, and within one time in log order.
			sort.SliceStable(cands, func(p, q int) bool {
				if !cands[p].At.Equal(cands[q].At) {
					return cands[p].At.After(cands[q].At)
				}
				return cands[p].n < cands[q].n
			})
			taken := make([]bool, len(cands))
			for c, a := range cands {
				// The index holds one ack per sentence, so no sentence is
				// claimed twice here.
				if a.SentenceHash != "" && present[a.SentenceHash] {
					own[a.SentenceHash], taken[c] = a.Ack, true
				}
			}
			for _, s := range sentences {
				if _, ok := own[s]; ok {
					continue
				}
				for c, a := range cands {
					if !taken[c] {
						own[s], taken[c] = a.Ack, true
						break
					}
				}
			}
		}
		for _, i := range is {
			if a, ok := own[rows[i].SentenceHash]; ok {
				out[i] = &a
			}
		}
	}
	return out
}
