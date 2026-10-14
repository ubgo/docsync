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

// The lifecycle property: whatever order a user runs the everyday commands
// in, a sentence's baseline moves only when it is acked, and a sentence
// whose block changed is never `ok` until it is acked. Every bug in this
// family was one ordering nobody had written down — a scan between the edit
// and the check (bug 145), a block published by another repository (bug
// 146), a deleted block (bug 149), a definition added for another
// environment (bug 152) — so the orderings are generated, not chosen: every
// sequence of lifecycleSteps up to lifecycleDepth steps, over every subject
// and origin.

// lifecycleStep is one thing a user does.
type lifecycleStep string

// The steps. editBody and editSig change the main block in a way its
// (default, stable) stability flags; addDef and removeDef add and remove a
// second definition of the same id for another environment; deleteDef
// removes the main block and its def; scan and sync rewrite committed state;
// check reads it; ack is the only step allowed to move a baseline; prune
// deletes bodies the store thinks dead.
const (
	stepEditBody  lifecycleStep = "edit-body"
	stepEditSig   lifecycleStep = "edit-signature"
	stepAddDef    lifecycleStep = "add-def"
	stepRemoveDef lifecycleStep = "remove-def"
	stepDeleteDef lifecycleStep = "delete"
	stepScan      lifecycleStep = "scan"
	stepCheck     lifecycleStep = "check"
	stepAck       lifecycleStep = "ack"
	stepPrune     lifecycleStep = "prune"
	stepSync      lifecycleStep = "sync"
)

// lifecycleSteps is the alphabet the sequences are drawn from.
var lifecycleSteps = []lifecycleStep{stepEditBody, stepEditSig, stepAddDef, stepRemoveDef, stepDeleteDef, stepScan, stepCheck, stepAck, stepPrune, stepSync}

// lifecycleDepth is the longest sequence generated. Three steps is every
// pair of steps with something before and after it: enough for "edit, scan,
// check", "ack, edit, scan", "add-def, scan, check" and "edit, prune,
// check", which are the shapes the bugs had; 10³ sequences per scenario
// keeps the run under a minute.
const lifecycleDepth = 3

// Axis one, the subject: what the sentence uses to say it depends on the
// block. A citation is measured against its own acked/seen hash and reads
// only the default environment's def; a claim's about= covers every
// environment of the id and is measured against the claim's baseline,
// renewed by an ack of the claim.
type lifecycleSubject string

const (
	subjectCitation lifecycleSubject = "citation"
	subjectClaim    lifecycleSubject = "claim"
)

// Axis two, the origin: where the block lives. A local block is in the
// docs repository itself; a foreign one is in another repository of the
// workspace, so a change reaches the docs repository only through that
// repository's scan and publish, and through the index.
type lifecycleOrigin string

const (
	originLocal   lifecycleOrigin = "local"
	originForeign lifecycleOrigin = "foreign"
)

// lifecycleID is the block every scenario is about.
const lifecycleID = "save-k7m2p4xq"

// lifecycleDoc is the sentence's page; the sentence is on its first line.
const lifecycleDoc = "docs/page.md"

// lifecycleStaging is the second definition addDef writes: the same id for
// env=staging, in a file of its own.
const (
	lifecycleStagingFile = "internal/staging.go"
	lifecycleStaging     = "package store\n\n// ds:def id=" + lifecycleID + " env=staging\nfunc SaveStaging() error {\n\treturn nil\n}\n"
)

// lifecycleDefs is the state of the id's definitions: the main def's
// version (or deleted) and whether the staging def exists. Two equal
// values are the same blocks with the same hashes.
type lifecycleDefs struct {
	version int  // the main block's version; every edit makes a new one
	deleted bool // the main block and its def are gone
	staging bool // the staging def exists
}

// cited is the def a citation with no env= resolves to: the main def, which
// matches the default environment, else the first def left, which is the
// staging one (check.ResolveDef); empty when there is none.
func (d lifecycleDefs) cited() string {
	switch {
	case !d.deleted:
		return fmt.Sprintf("main@%d", d.version)
	case d.staging:
		return "staging"
	}
	return ""
}

// lifecycleWorld is one scenario's repositories and the model of what the
// sentence should report.
type lifecycleWorld struct {
	t        *testing.T
	subject  lifecycleSubject
	origin   lifecycleOrigin
	api      string // the repository holding a foreign block
	docs     string // the repository holding the sentence (and a local block)
	apiVCS   fakeVCS
	docsVCS  fakeVCS
	now      lifecycleDefs // the definitions as they are
	reviewed lifecycleDefs // the definitions as the sentence was last reviewed
	acked    bool          // an ack has happened since the docs repository last scanned
}

// lifecycleSource is the main block at a version: the body and the
// signature each carry a counter, so editBody and editSig each give
// content never seen before.
func lifecycleSource(param, body int) string {
	return fmt.Sprintf("package store\n\n// ds:def id=%s\nfunc Save(a T%d) error {\n\treturn e%d\n}\n", lifecycleID, param, body)
}

func newLifecycleWorld(t *testing.T, subject lifecycleSubject, origin lifecycleOrigin) *lifecycleWorld {
	t.Helper()
	index := t.TempDir()
	write(t, index, "ds-workspace.toml", "[workspace]\nname = \"platform\"\nrepos = [\"github.com/org/api\", \"github.com/org/docs\"]\ndefault_branch = \"main\"\n")
	w := &lifecycleWorld{
		t: t, subject: subject, origin: origin, api: t.TempDir(), docs: t.TempDir(),
		apiVCS:  fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}},
		docsVCS: fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}},
	}
	for _, d := range []string{w.api, w.docs} {
		write(t, d, ".ds/config.toml", "workspace = "+strconvQuote(index)+"\n[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	}
	write(t, w.api, "docs/api.md", "Internal note.\n")
	sentence := "Saves go through [Save](ds:block?id=" + lifecycleID + ").\n"
	if subject == subjectClaim {
		sentence = "Saves are dual-written. <!-- ds:claim owner=@t reviewed=2026-09-01 expires=3650d about=" + lifecycleID + " -->\n"
	}
	write(t, w.docs, lifecycleDoc, sentence)
	write(t, w.home(), "internal/store.go", lifecycleSource(0, 0))
	w.must(w.api, w.apiVCS, "scan")
	w.must(w.api, w.apiVCS, "publish")
	w.must(w.docs, w.docsVCS, "sync")
	w.must(w.docs, w.docsVCS, "scan")
	return w
}

// home is the repository the block lives in.
func (w *lifecycleWorld) home() string {
	if w.origin == originForeign {
		return w.api
	}
	return w.docs
}

func (w *lifecycleWorld) must(dir string, v fakeVCS, args ...string) result {
	w.t.Helper()
	r := run(w.t, dir, v, args...)
	if r.code != 0 {
		w.t.Fatalf("%v = %+v", args, r)
	}
	return r
}

// upstream makes a change to the blocks visible where the sentence is read:
// for a foreign block that is its repository's scan and publish; a local
// block is read from the working tree.
func (w *lifecycleWorld) upstream() {
	if w.origin == originForeign {
		w.must(w.api, w.apiVCS, "scan")
		w.must(w.api, w.apiVCS, "publish")
	}
}

// apply runs one step and updates the model.
func (w *lifecycleWorld) apply(s lifecycleStep) {
	w.t.Helper()
	switch s {
	case stepEditBody, stepEditSig:
		if w.now.deleted {
			return // nothing left to edit
		}
		w.now.version++
		param, body := 0, 0
		// The two counters move independently, so an edit-signature leaves
		// the body as it was and the reverse; each edit is still new
		// content because the counter only grows.
		if s == stepEditSig {
			param = w.now.version
		} else {
			body = w.now.version
		}
		write(w.t, w.home(), "internal/store.go", lifecycleSource(param, body))
		w.upstream()
	case stepAddDef:
		if w.now.staging {
			return
		}
		write(w.t, w.home(), lifecycleStagingFile, lifecycleStaging)
		w.now.staging = true
		w.upstream()
	case stepRemoveDef:
		if !w.now.staging {
			return
		}
		if err := os.Remove(filepath.Join(w.home(), lifecycleStagingFile)); err != nil {
			w.t.Fatal(err)
		}
		w.now.staging = false
		w.upstream()
	case stepDeleteDef:
		if w.now.deleted {
			return
		}
		write(w.t, w.home(), "internal/store.go", "package store\n")
		w.now.deleted = true
		w.upstream()
	case stepScan:
		before := w.baseline()
		w.must(w.docs, w.docsVCS, "scan")
		if after := w.baseline(); !w.acked && after != before {
			w.t.Errorf("scan moved the baseline without an ack: %q -> %q", before, after)
		}
		w.acked = false
	case stepCheck:
		run(w.t, w.docs, w.docsVCS, "check")
	case stepAck:
		if w.subject == subjectCitation && w.now.cited() == "" {
			// A citation of nothing cannot be acked; the command says so.
			if r := run(w.t, w.docs, w.docsVCS, "ack", lifecycleID, "--doc", lifecycleDoc, "--line", "1", "--note", "n"); r.code == 0 {
				w.t.Errorf("acking a citation of a deleted block = %+v", r)
			}
			return
		}
		args := []string{"ack", "--doc", lifecycleDoc, "--line", "1", "--note", "n"}
		if w.subject == subjectCitation {
			args = append(args[:1], append([]string{lifecycleID}, args[1:]...)...)
		}
		w.must(w.docs, w.docsVCS, args...)
		w.reviewed, w.acked = w.now, true
	case stepPrune:
		w.must(w.docs, w.docsVCS, "prune", "--keep", "0d")
		w.must(w.docs, w.docsVCS, "prune", "--index", "--keep", "0d")
	case stepSync:
		w.must(w.docs, w.docsVCS, "sync")
	}
}

// baseline is the sentence's recorded baseline in the docs repository's
// refs.tsv: what scan carries forward, which only an ack may move.
func (w *lifecycleWorld) baseline() string {
	w.t.Helper()
	f, err := os.Open(filepath.Join(w.docs, ".ds", ledger.RefsFile))
	if err != nil {
		w.t.Fatal(err)
	}
	defer f.Close()
	refs, err := ledger.DecodeRefs(f)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, r := range refs.Rows {
		if r.Doc == lifecycleDoc && r.Line == 1 {
			return r.AckedHash + " " + r.SeenHash
		}
	}
	w.t.Fatalf("no refs row for the sentence in %+v", refs.Rows)
	return ""
}

// state is what `ds check` reports for the sentence.
func (w *lifecycleWorld) state() check.State {
	w.t.Helper()
	r := run(w.t, w.docs, w.docsVCS, "check", "--json")
	var rep struct {
		Findings []check.Finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(r.out), &rep); err != nil {
		w.t.Fatalf("check --json = %+v: %v", r, err)
	}
	for _, f := range rep.Findings {
		if f.Doc == lifecycleDoc && f.Line == 1 {
			return f.State
		}
	}
	w.t.Fatalf("no finding for the sentence in %s", r.out)
	return ""
}

// invariant is what the sentence must report in the model's state, so the
// property catches a missed change and a false alarm alike.
//
// A citation reads the one def it resolves to (cited): broken when there is
// none, unacked while that differs from the one last acked, ok otherwise. A
// claim is about every
// definition of the id: broken when none is left, expired while the set of
// definitions or any of their versions differs from the one last reviewed,
// ok otherwise.
func (w *lifecycleWorld) invariant() check.State {
	if w.subject == subjectCitation {
		switch {
		case w.now.cited() == "":
			return check.StateBroken
		case w.now.cited() != w.reviewed.cited():
			return check.StateUnacked
		}
		return check.StateOK
	}
	switch {
	case w.now.deleted && !w.now.staging:
		return check.StateBroken
	case w.now != w.reviewed:
		return check.StateExpired
	}
	return check.StateOK
}

// lifecycleSequences is every sequence of steps of length 1 to depth.
func lifecycleSequences(depth int) [][]lifecycleStep {
	var out [][]lifecycleStep
	var grow func(prefix []lifecycleStep)
	grow = func(prefix []lifecycleStep) {
		if len(prefix) > 0 {
			out = append(out, append([]lifecycleStep(nil), prefix...))
		}
		if len(prefix) == depth {
			return
		}
		for _, s := range lifecycleSteps {
			grow(append(prefix, s))
		}
	}
	grow(nil)
	return out
}

// promise:claim-baseline-ack-only promise:claim-undefined-broken
// TestLifecycleBaselineMovesOnlyOnAck runs every generated sequence on a
// fresh world per subject and origin, and checks the invariant after every
// step (bugs 145, 146, 149 and 152).
func TestLifecycleBaselineMovesOnlyOnAck(t *testing.T) {
	t.Parallel()
	depth := lifecycleDepth
	if testing.Short() {
		depth = 2
	}
	seqs := lifecycleSequences(depth)
	for _, subject := range []lifecycleSubject{subjectCitation, subjectClaim} {
		for _, origin := range []lifecycleOrigin{originLocal, originForeign} {
			t.Run(string(subject)+"/"+string(origin), func(t *testing.T) {
				t.Parallel()
				for _, seq := range seqs {
					w := newLifecycleWorld(t, subject, origin)
					if got := w.state(); got != check.StateOK {
						t.Fatalf("%v: before any step = %s", seq, got)
					}
					for i, s := range seq {
						w.apply(s)
						if got, want := w.state(), w.invariant(); got != want {
							t.Errorf("%s/%s: after step %d (%s): got %s, want %s", subject, origin, i+1, strings.Join(stepNames(seq[:i+1]), " → "), got, want)
							break
						}
					}
				}
			})
		}
	}
}

func stepNames(seq []lifecycleStep) []string {
	out := make([]string, len(seq))
	for i, s := range seq {
		out[i] = string(s)
	}
	return out
}

// The generator is what makes the property worth anything: it must produce
// every sequence, each once.
func TestLifecycleSequences(t *testing.T) {
	t.Parallel()
	seqs := lifecycleSequences(2)
	n := len(lifecycleSteps)
	if len(seqs) != n+n*n {
		t.Fatalf("got %d sequences, want %d", len(seqs), n+n*n)
	}
	seen := map[string]bool{}
	for _, s := range seqs {
		k := fmt.Sprint(s)
		if seen[k] {
			t.Errorf("duplicate %s", k)
		}
		seen[k] = true
	}
}
