package docsync

import (
	"context"
	"errors"
	"fmt"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/match"
	"github.com/ubgo/docsync/pick"
	"github.com/ubgo/docsync/render"
	"github.com/ubgo/docsync/scan"
)

// Capability interfaces (docs/SPEC.md §37.3). A plugin implements the
// smallest one that fits; each is registered through one functional option
// and there is no second, quieter way to do the same thing.

// KeySpec declares a verb's keys so unknown keys warn and missing required
// keys are problems, the same rule the built-in verbs follow.
type KeySpec struct {
	Required []string
	Known    []string
}

// Verb is a plugin directive verb (§9.9). Check returns findings for one
// reference; Render returns the text that replaces the directive, inline
// for a link in prose and as a block otherwise. ok=false means "leave the
// source as written".
type Verb interface {
	Name() string
	Carriers() []block.Carrier
	Keys() KeySpec
	Check(ref block.Reference, st *VerbState) []check.Finding
	Render(ref block.Reference, st *VerbState, inline bool) (text string, ok bool, err error)
}

// VerbState is the read-only view a verb gets: the defs of the tree and
// the merged workspace, by id.
type VerbState struct {
	Repo string
	Defs map[string]block.Block
}

// Picker is a plugin `pick=` scheme.
type Picker interface {
	Scheme() string
	Pick(arg, content string) (pick.Result, error)
}

// Classifier replaces change classification (§20); it is match.Classifier.
type Classifier = match.Classifier

// Renderer turns the rendered node sequence into bytes: markdown by
// default, html or a site generator's own tree when replaced.
type Renderer interface {
	Render(nodes []render.Node) ([]byte, error)
}

// Store is where committed state lives. The default is the `.ds/` TSV
// files in the CLI; a database or the workspace index can stand in.
type Store interface {
	Load(ctx context.Context) (ledger.Ledger, ledger.Refs, ledger.Acks, error)
	Save(ctx context.Context, l ledger.Ledger, r ledger.Refs, a ledger.Acks) error
}

// Notifier delivers findings somewhere.
type Notifier interface {
	Notify(ctx context.Context, findings []check.Finding) error
}

// Observer sees every finding and every ack as it happens: audit sinks and
// metrics.
type Observer interface {
	OnFinding(check.Finding)
	OnAck(ledger.Ack)
}

// ErrNoStore is returned by SaveState when no Store was given.
var ErrNoStore = errors.New("docsync: no store configured (WithStore)")

// WithVerbHandler registers a verb implementation. Its name is also added
// to the known verbs, so `WithVerb` is not needed alongside.
func WithVerbHandler(v Verb) Option {
	return func(s *System) error {
		if v == nil || v.Name() == "" {
			return fmt.Errorf("%w: verb handler without a name", ErrNotFound)
		}
		s.verbs[v.Name()] = true
		s.verbHandlers[v.Name()] = v
		return nil
	}
}

// WithPicker registers a `pick=` scheme.
func WithPicker(p Picker) Option {
	return func(s *System) error {
		if p == nil || p.Scheme() == "" {
			return fmt.Errorf("%w: picker without a scheme", ErrNotFound)
		}
		s.pickers[p.Scheme()] = p.Pick
		return nil
	}
}

// WithClassifier replaces change classification entirely.
func WithClassifier(c Classifier) Option {
	return func(s *System) error { s.classifier = c; return nil }
}

// WithRenderer replaces the markdown output of Render.
func WithRenderer(r Renderer) Option {
	return func(s *System) error { s.renderer = r; return nil }
}

// WithStore loads the previous ledger, refs, and acks from st and keeps it
// for SaveState. It replaces WithPrevious and WithAcks.
func WithStore(st Store) Option {
	return func(s *System) error {
		l, r, a, err := st.Load(context.Background())
		if err != nil {
			return err
		}
		s.prev, s.prevRefs, s.acks, s.store = l, r, a, st
		return nil
	}
}

// WithNotifier adds a delivery channel used by Notify.
func WithNotifier(n Notifier) Option {
	return func(s *System) error { s.notifiers = append(s.notifiers, n); return nil }
}

// WithObserver adds a sink for findings and acks.
func WithObserver(o Observer) Option {
	return func(s *System) error { s.observers = append(s.observers, o); return nil }
}

// Notify sends the report's non-ok findings to every notifier; the first
// failure is returned after the rest were tried.
func (s *System) Notify(ctx context.Context, rep Report) error {
	var open []check.Finding
	for _, f := range rep.Findings {
		if f.Severity == check.SeverityError || f.Severity == check.SeverityWarning {
			open = append(open, f)
		}
	}
	var first error
	for _, n := range s.notifiers {
		if err := n.Notify(ctx, open); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// SaveState writes the snapshot of res and the given ack log through the
// Store.
func (s *System) SaveState(ctx context.Context, res scan.Result, acks ledger.Acks) error {
	if s.store == nil {
		return ErrNoStore
	}
	l, r := s.Snapshot(res)
	return s.store.Save(ctx, l, r, acks)
}

// verbState builds the view handlers see.
func (s *System) verbState(defs []block.Block) *VerbState {
	st := &VerbState{Repo: s.repo, Defs: map[string]block.Block{}}
	for _, b := range append(append([]block.Block{}, defs...), s.merged...) {
		if _, seen := st.Defs[b.ID]; !seen {
			st.Defs[b.ID] = b
		}
	}
	return st
}

// checkHandlers adapts registered verbs to check's hooks.
func (s *System) checkHandlers(defs []block.Block) (map[string]check.VerbHandler, map[string][]string, map[string][]string) {
	if len(s.verbHandlers) == 0 {
		return nil, nil, nil
	}
	st := s.verbState(defs)
	handlers := map[string]check.VerbHandler{}
	known := map[string][]string{}
	required := map[string][]string{}
	for name, v := range s.verbHandlers {
		v := v
		handlers[name] = func(ref block.Reference) []check.Finding { return v.Check(ref, st) }
		spec := v.Keys()
		known[name] = append(append([]string{block.KeyID}, spec.Known...), spec.Required...)
		required[name] = spec.Required
	}
	return handlers, known, required
}

// renderVerbs adapts registered verbs to render's hook.
func (s *System) renderVerbs(defs []block.Block) map[string]render.VerbRenderer {
	if len(s.verbHandlers) == 0 {
		return nil
	}
	st := s.verbState(defs)
	out := map[string]render.VerbRenderer{}
	for name, v := range s.verbHandlers {
		v := v
		out[name] = func(ref block.Reference, inline bool) (string, bool, error) { return v.Render(ref, st, inline) }
	}
	return out
}

func (s *System) observeFindings(fs []check.Finding) {
	for _, o := range s.observers {
		for _, f := range fs {
			o.OnFinding(f)
		}
	}
}

func (s *System) observeAck(a ledger.Ack) {
	for _, o := range s.observers {
		o.OnAck(a)
	}
}
