package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/ledger"
)

// The same-line property (bug 157): when several sentences on one line
// depend on one block, each is a statement of its own. Whatever a user runs,
// in whatever order, a sentence is ok exactly when the block is as it was
// when that sentence was last acked (or first seen) and, once acked, its
// wording is what was acked; and acking the line or the id acks every
// sentence on it, once each. Before, acks were found by line, so the
// sentences on a line shared one ack: one of them could never be acked, and
// was compared with its neighbour's wording.
//
// The cases are generated: every sequence of sameLineDepth steps over
// sameLineSteps, for one, two and three sentences on the line, checked
// after every step against a model of what each sentence should report.

// sameLineStep is one thing a user does.
type sameLineStep string

// The steps. editBlock changes the cited block; rewriteFirst and
// rewriteLast change the wording of the first and last sentence on the line
// (the same one when there is one); scan rewrites refs.tsv; ackLine acks the
// line by --doc and --line, ackAll by --all.
const (
	stepEditBlock    sameLineStep = "edit-block"
	stepRewriteFirst sameLineStep = "rewrite-first"
	stepRewriteLast  sameLineStep = "rewrite-last"
	stepLineScan     sameLineStep = "scan"
	stepAckLine      sameLineStep = "ack-line"
	stepAckAll       sameLineStep = "ack-all"
)

// sameLineSteps is the alphabet for citations. Claims are renewed by line
// only (a claim has no id for --all), and a claim's wording is not checked,
// so their alphabet is the subset that applies.
var (
	sameLineSteps      = []sameLineStep{stepEditBlock, stepRewriteFirst, stepRewriteLast, stepLineScan, stepAckLine, stepAckAll}
	sameLineClaimSteps = []sameLineStep{stepEditBlock, stepLineScan, stepAckLine}
)

// sameLineDepth is the length of every sequence. Each is checked after every
// step, so every shorter sequence is checked as a prefix; three steps holds
// "ack, rewrite, rewrite", "rewrite, ack, scan" and "edit, ack-all, check",
// the shapes the bug took.
const sameLineDepth = 3

const (
	sameLineID  = "save-k7m2p4xq"
	sameLineDoc = "docs/page.md"
)

// sameLineWorld is one repository and the model of what each sentence on
// line 1 of its page should report.
type sameLineWorld struct {
	t      *testing.T
	dir    string
	vcs    fakeVCS
	claims bool
	block  int   // the block's version; every edit makes a new one
	text   []int // each sentence's wording version
	// ackedText is each sentence's wording when last acked, -1 when never;
	// ackedBlock the block's version then, or when the sentence was first
	// seen.
	ackedText  []int
	ackedBlock []int
}

func newSameLineWorld(t *testing.T, n int, claims bool) *sameLineWorld {
	t.Helper()
	w := &sameLineWorld{t: t, dir: t.TempDir(), vcs: fakeVCS{head: "d1", files: map[string][]byte{}}, claims: claims}
	for range n {
		w.text, w.ackedText, w.ackedBlock = append(w.text, 0), append(w.ackedText, -1), append(w.ackedBlock, 0)
	}
	write(t, w.dir, ".ds/config.toml", "[scan]\ncode = [\"internal/**\"]\ndocs = [\"docs/**\"]\n")
	w.writeBlock()
	w.writePage()
	w.must("scan")
	return w
}

func (w *sameLineWorld) writeBlock() {
	write(w.t, w.dir, "internal/store.go", fmt.Sprintf("package store\n\n// ds:def id=%s\nfunc Save() error {\n\treturn e%d\n}\n", sameLineID, w.block))
}

// sentence is sentence i at its wording version; the "S<i> " prefix is how
// a finding is matched back to it.
func (w *sameLineWorld) sentence(i int) string {
	if w.claims {
		// A claim comment trails the sentence it is about.
		return fmt.Sprintf("S%d holds v%d. <!-- ds:claim owner=@t reviewed=2026-09-01 expires=3650d about=%s -->", i, w.text[i], sameLineID)
	}
	return fmt.Sprintf("S%d [saves](ds:block?id=%s) v%d.", i, sameLineID, w.text[i])
}

func (w *sameLineWorld) writePage() {
	parts := make([]string, len(w.text))
	for i := range w.text {
		parts[i] = w.sentence(i)
	}
	// The line is a paragraph of its own: a comment that ends a line
	// followed by more of its paragraph binds the next line's sentence,
	// which is a separate matter from this property.
	write(w.t, w.dir, sameLineDoc, strings.Join(parts, " ")+"\n\nAnother paragraph.\n")
}

func (w *sameLineWorld) must(args ...string) result {
	w.t.Helper()
	r := run(w.t, w.dir, w.vcs, args...)
	if r.code != 0 {
		w.t.Fatalf("%v = %+v", args, r)
	}
	return r
}

// apply runs one step and updates the model.
func (w *sameLineWorld) apply(s sameLineStep) {
	w.t.Helper()
	switch s {
	case stepEditBlock:
		w.block++
		w.writeBlock()
	case stepRewriteFirst, stepRewriteLast:
		i := 0
		if s == stepRewriteLast {
			i = len(w.text) - 1
		}
		w.text[i] = w.maxText() + 1
		w.writePage()
	case stepLineScan:
		w.must("scan")
	case stepAckLine, stepAckAll:
		args := []string{"ack", "--doc", sameLineDoc, "--line", "1", "--note", "n"}
		switch {
		case s == stepAckAll:
			args = []string{"ack", sameLineID, "--all", "--note", "n"}
		case !w.claims:
			args = append([]string{"ack", sameLineID}, args[1:]...)
		}
		r := w.must(args...)
		// One row per sentence: never one for the line, and never two
		// for one sentence.
		if got := strings.Count(r.out, "\n"); got != len(w.text) {
			w.t.Errorf("%v printed %d rows for %d sentences:\n%s", args, got, len(w.text), r.out)
		}
		for i := range w.text {
			w.ackedText[i], w.ackedBlock[i] = w.text[i], w.block
		}
	}
}

// maxText is the highest wording version so far, so a rewrite never
// returns to a wording some ack approved.
func (w *sameLineWorld) maxText() int {
	m := 0
	for _, v := range w.text {
		m = max(m, v)
	}
	return m
}

// want is what sentence i should report.
func (w *sameLineWorld) want(i int) check.State {
	if w.block != w.ackedBlock[i] {
		if w.claims {
			return check.StateExpired
		}
		return check.StateUnacked
	}
	if w.ackedText[i] >= 0 && w.text[i] != w.ackedText[i] {
		return check.StateUnacked
	}
	return check.StateOK
}

// states is what `ds check` reports for each sentence on line 1.
func (w *sameLineWorld) states() []check.State {
	w.t.Helper()
	r := run(w.t, w.dir, w.vcs, "check", "--json")
	var rep struct {
		Findings []check.Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(r.out), &rep); err != nil {
		w.t.Fatalf("check --json = %+v: %v", r, err)
	}
	out := make([]check.State, len(w.text))
	for _, f := range rep.Findings {
		if f.Doc != sameLineDoc || f.Line != 1 {
			continue
		}
		for i := range w.text {
			if strings.HasPrefix(f.Sentence, fmt.Sprintf("S%d ", i)) {
				if out[i] != "" {
					w.t.Fatalf("two findings for sentence %d in %s", i, r.out)
				}
				out[i] = f.State
			}
		}
	}
	for i, s := range out {
		if s == "" {
			w.t.Fatalf("no finding for sentence %d in %s", i, r.out)
		}
	}
	return out
}

// sameLineSequences is every sequence of exactly depth steps.
func sameLineSequences(alphabet []sameLineStep, depth int) [][]sameLineStep {
	out := [][]sameLineStep{nil}
	for range depth {
		var next [][]sameLineStep
		for _, seq := range out {
			for _, s := range alphabet {
				next = append(next, append(append([]sameLineStep(nil), seq...), s))
			}
		}
		out = next
	}
	return out
}

// promise:same-line-own-ack
// TestSameLineSentencesAreEachTheirOwn runs every generated sequence on a
// fresh repository, for one to three citing sentences and for two claims on
// one line, and checks every sentence after every step (bug 157).
func TestSameLineSentencesAreEachTheirOwn(t *testing.T) {
	t.Parallel()
	depth := sameLineDepth
	if testing.Short() {
		depth = 2
	}
	type scenario struct {
		n      int
		claims bool
	}
	for _, sc := range []scenario{{1, false}, {2, false}, {3, false}, {2, true}} {
		alphabet := sameLineSteps
		name := fmt.Sprintf("%d-citations", sc.n)
		if sc.claims {
			alphabet, name = sameLineClaimSteps, fmt.Sprintf("%d-claims", sc.n)
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, seq := range sameLineSequences(alphabet, depth) {
				w := newSameLineWorld(t, sc.n, sc.claims)
				for step, s := range seq {
					w.apply(s)
					got := w.states()
					for i := range got {
						if want := w.want(i); got[i] != want {
							t.Errorf("%s: after %v, sentence %d = %s, want %s", name, seq[:step+1], i, got[i], want)
						}
					}
					if t.Failed() {
						return
					}
				}
			}
		})
	}
}

// The generator is what makes the property worth anything: every sequence
// of the length, each once.
func TestSameLineSequences(t *testing.T) {
	t.Parallel()
	seqs := sameLineSequences(sameLineSteps, 2)
	n := len(sameLineSteps)
	if len(seqs) != n*n {
		t.Fatalf("got %d sequences, want %d", len(seqs), n*n)
	}
	seen := map[string]bool{}
	for _, s := range seqs {
		if k := fmt.Sprint(s); seen[k] {
			t.Errorf("duplicate %s", k)
		} else {
			seen[k] = true
		}
	}
}

// TestAckOfALineQuotesEachSentence pins what `ds ack` prints and records
// for a line with two sentences citing one id (bug 157): a row for each,
// each quoting its own sentence, and no duplicate rows from --all, which
// makes one request per finding. The dry run quotes each row's own
// sentence; it looked the sentence up by line and id and quoted the same
// one twice.
func TestAckOfALineQuotesEachSentence(t *testing.T) {
	t.Parallel()
	w := newSameLineWorld(t, 2, false)
	w.apply(stepEditBlock)
	dry := w.must("ack", sameLineID, "--doc", sameLineDoc, "--line", "1", "--dry-run")
	if !strings.Contains(dry.out, `"S0 [saves]`) || !strings.Contains(dry.out, `"S1 [saves]`) {
		t.Errorf("dry run = %s; want each sentence quoted", dry.out)
	}
	r := w.must("ack", sameLineID, "--all")
	if !strings.Contains(r.out, `— "S0 [saves]`) || !strings.Contains(r.out, `— "S1 [saves]`) {
		t.Errorf("ack --all = %s; want each row to quote its sentence", r.out)
	}
	raw, err := os.ReadFile(filepath.Join(w.dir, ".ds", ledger.AcksFile))
	if err != nil {
		t.Fatal(err)
	}
	acks, err := ledger.DecodeAcks(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if len(acks.Rows) != 2 || acks.Rows[0].SentenceHash == acks.Rows[1].SentenceHash {
		t.Errorf("ack log = %+v; want one row per sentence", acks.Rows)
	}
	// A line with one sentence prints as it always has, with no quote.
	one := newSameLineWorld(t, 1, false)
	if r := one.must("ack", sameLineID, "--doc", sameLineDoc, "--line", "1"); strings.Contains(r.out, "—") {
		t.Errorf("one sentence = %s; want no quote", r.out)
	}
}
