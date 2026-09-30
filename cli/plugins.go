package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/pick"
	"github.com/ubgo/docsync/procplugin"
	"github.com/ubgo/docsync/records"
)

// Process plugins as capabilities (§37.4): `[plugins] verbs` and `picks` in
// config name executables on PATH that speak the procplugin protocol; a
// `[records] source` that is not built in names a `ds-records-<source>`.
// Each adapter below turns one executable into the library interface, so a
// plugin written in any language is indistinguishable from a Go one.

// procVerb is a `ds-<verb>` executable.
type procVerb struct {
	name string
	host procplugin.Host
}

func (v procVerb) Name() string { return v.name }

func (procVerb) Carriers() []block.Carrier {
	return []block.Carrier{block.CarrierLink, block.CarrierComment, block.CarrierBlock}
}

// Keys returns no spec: a process plugin's keys are not declared to the
// host, so nothing is unknown and nothing is required.
func (procVerb) Keys() docsync.KeySpec { return docsync.KeySpec{} }

// stateView is the JSON the plugin receives: the defs it may need.
type stateView struct {
	Repo string                 `json:"repo"`
	Defs map[string]block.Block `json:"defs"`
}

func (v procVerb) call(op string, ref block.Reference, st *docsync.VerbState) (procplugin.Response, error) {
	refJSON, _ := json.Marshal(ref)
	view := stateView{Repo: st.Repo, Defs: st.Defs}
	viewJSON, _ := json.Marshal(view)
	return v.host.Verb(context.Background(), v.name, op, refJSON, viewJSON)
}

// Check asks the plugin; a plugin failure is itself a finding at the
// reference so a broken plugin cannot silently pass.
func (v procVerb) Check(ref block.Reference, st *docsync.VerbState) []check.Finding {
	resp, err := v.call(procplugin.OpCheck, ref, st)
	if err != nil {
		return []check.Finding{{State: check.StateUnverifiable, Message: "plugin ds-" + v.name + ": " + err.Error()}}
	}
	var findings []check.Finding
	if len(resp.Findings) > 0 {
		if err := json.Unmarshal(resp.Findings, &findings); err != nil {
			return []check.Finding{{State: check.StateProblem, Message: "plugin ds-" + v.name + " returned malformed findings: " + err.Error()}}
		}
	}
	return findings
}

// Render asks the plugin for a node; the node is the replacement text as a
// JSON string.
func (v procVerb) Render(ref block.Reference, st *docsync.VerbState, _ bool) (string, bool, error) {
	resp, err := v.call(procplugin.OpRender, ref, st)
	if err != nil {
		return "", false, err
	}
	if len(resp.Node) == 0 {
		return "", false, nil
	}
	var text string
	if err := json.Unmarshal(resp.Node, &text); err != nil {
		return "", false, fmt.Errorf("plugin ds-%s returned a non-string node", v.name)
	}
	return text, true, nil
}

// procPicker is a `ds-pick-<scheme>` executable.
type procPicker struct {
	scheme string
	host   procplugin.Host
}

func (p procPicker) Scheme() string { return p.scheme }

func (p procPicker) Pick(arg, content string) (pick.Result, error) {
	// The protocol names a file; a picker over block content sends the
	// content as the file body under a synthetic name.
	resp, err := p.host.Pick(context.Background(), p.scheme, content, arg)
	if err != nil {
		return pick.Result{}, err
	}
	if resp.Value != nil {
		return pick.Result{Kind: pick.KindValue, Value: *resp.Value, Start: 1, End: 1}, nil
	}
	return pick.Result{Kind: pick.KindRange, Text: resp.Range.Text, Start: resp.Range.Start, End: resp.Range.End}, nil
}

// procRecords is a `ds-records-<source>` executable.
func procRecords(host procplugin.Host, source string) records.Source {
	return func(args map[string]string) ([]map[string]string, error) {
		q, _ := json.Marshal(args)
		raw, err := host.Records(context.Background(), source, q)
		if err != nil {
			return nil, err
		}
		var rows []map[string]string
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &rows); err != nil {
				return nil, fmt.Errorf("plugin ds-records-%s returned malformed rows: %w", source, err)
			}
		}
		return records.Apply(rows, args)
	}
}

// pluginOptions builds the library options for configured process plugins.
func (a *App) pluginOptions(cfg config.Config) []docsync.Option {
	host := procplugin.Host{LookPath: a.pluginLookPath}
	var opts []docsync.Option
	for _, v := range cfg.Plugins.Verbs {
		opts = append(opts, docsync.WithVerbHandler(procVerb{name: v, host: host}))
	}
	for _, p := range cfg.Plugins.Picks {
		opts = append(opts, docsync.WithPicker(procPicker{scheme: p, host: host}))
	}
	switch cfg.Records.Source {
	case config.RecordsFrontmatter, config.RecordsHTTP, "":
	default:
		opts = append(opts, docsync.WithRecords(procRecords(host, cfg.Records.Source)))
	}
	return opts
}

// Load implements docsync.Store over the .ds/ files.
func (s *Store) Load(context.Context) (ledger.Ledger, ledger.Refs, ledger.Acks, error) {
	return s.LoadState()
}

// Save implements docsync.Store: ledger, refs, and the whole ack log.
func (s *Store) Save(_ context.Context, l ledger.Ledger, r ledger.Refs, a ledger.Acks) error {
	if err := s.SaveLedger(l, r); err != nil {
		return err
	}
	// The whole log is passed; rows already on disk are skipped, so a
	// library caller holding an older copy cannot erase another writer's ack.
	return s.AppendAcks(a.Rows)
}

// slackNotifier implements docsync.Notifier over an incoming webhook.
type slackNotifier struct {
	app  *App
	hook string
}

func (n slackNotifier) Notify(_ context.Context, findings []check.Finding) error {
	if len(findings) == 0 {
		return nil
	}
	d := &digest{Findings: findings}
	return n.app.postSlack(n.hook, formatDigest(d, nil))
}

var (
	_ docsync.Verb     = procVerb{}
	_ docsync.Picker   = procPicker{}
	_ docsync.Store    = (*Store)(nil)
	_ docsync.Notifier = slackNotifier{}
)
