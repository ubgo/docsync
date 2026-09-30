package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
)

// `ds notify` (§22, §24 nightly): routes open error findings to owners with
// dedupe and escalation. A finding is sent once; it is sent again, marked
// escalated, when it is still open after notify.escalate_after. State lives
// in .ds/notified.json. The only built-in channel is a Slack incoming
// webhook (notify.slack, `$VAR` expanded from the environment by the CLI);
// without one the digest is printed, which is what a cron job mails.
const (
	NotifiedFile  = "notified.json"
	notifyTimeout = 15 * time.Second
	flagDryRunN   = "dry-run"
)

// NotifyState is where `ds notify` keeps the memory that makes it quiet:
// which findings it has already sent, and when, so a second run says
// nothing new and `escalate_after` can measure from a first sighting.
//
// It is an interface because that memory has to outlive a process, and
// where it outlives it is the operator's decision. A CI runner checks the
// repository out fresh every night and cannot write anything back, so the
// default file is empty on every run there and dedupe silently never holds
// — a notifier that repeats itself nightly is one people mute. The shipped
// nightly template restores the file from a cache; a workspace index or a
// database can implement this instead without the command changing.
//
// A missing state is an empty map and no error: that is a first run. An
// unreadable one is an error, because treating a corrupt file as empty
// would silently reset dedupe, which looks exactly like working.
type NotifyState interface {
	// Load reports the remembered state and whether any was found. The
	// second value is not len(state) > 0: a repo that is green records an
	// empty state, and treating that as "no memory" would make every later
	// run announce itself as a first run.
	Load(ctx context.Context) (state map[string]Notified, found bool, err error)
	Save(ctx context.Context, state map[string]Notified) error
}

// fileNotifyState is the default: .ds/notified.json. It is a separate type
// rather than methods on Store, because Store already carries Load and Save
// for the plugin ledger capability.
type fileNotifyState struct{ store *Store }

func (f fileNotifyState) Load(context.Context) (map[string]Notified, bool, error) {
	return f.store.loadNotified()
}

func (f fileNotifyState) Save(_ context.Context, state map[string]Notified) error {
	return f.store.SaveNotified(state)
}

// notifyState is the configured state, or the local file.
func (a *App) notifyState(st *Store) NotifyState {
	if a.notifyStateImpl != nil {
		return a.notifyStateImpl
	}
	return fileNotifyState{store: st}
}

// Notified is one delivered finding.
type Notified struct {
	FirstSent time.Time `json:"first_sent"`
	LastSent  time.Time `json:"last_sent"`
	Escalated bool      `json:"escalated"`
	// Tier is the urgency a snapshot alert was last sent at, so a drift
	// that worsens is news and a second edit at the same tier is not.
	// Empty for an ordinary finding.
	Tier string `json:"tier,omitempty"`
	// Misses counts consecutive runs that could not compare a repo against
	// the index. Not comparing is not the same as nothing to report: a
	// broken index would otherwise hide staleness indefinitely, which is
	// the vacuous pass this whole series has been about.
	Misses int `json:"misses,omitempty"`
}

// LoadNotified reads the dedupe state.
func (s *Store) LoadNotified() (map[string]Notified, error) {
	state, _, err := s.loadNotified()
	return state, err
}

// loadNotified also reports whether a state file was there at all.
func (s *Store) loadNotified() (map[string]Notified, bool, error) {
	raw, ok, err := s.read(NotifiedFile)
	if err != nil || !ok {
		return map[string]Notified{}, false, err
	}
	out := map[string]Notified{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false, fmt.Errorf("%s: %w", NotifiedFile, err)
	}
	return out, true, nil
}

// SaveNotified writes it.
func (s *Store) SaveNotified(m map[string]Notified) error {
	raw, _ := json.MarshalIndent(m, "", "  ")
	return s.Write(NotifiedFile, raw)
}

// findingKey identifies a finding across runs: place, state, and hash.
func findingKey(f check.Finding) string {
	return f.Doc + ":" + fmt.Sprint(f.Line) + ":" + f.ID + ":" + string(f.State) + ":" + f.Hash.Current
}

// digest is what goes to owners.
type digest struct {
	Owner    string
	Findings []check.Finding
	Escalate []check.Finding
}

func (a *App) notifyCmd() *cobra.Command {
	var dry bool
	cmd := &cobra.Command{
		Use:   "notify",
		Short: "route open findings to owners with dedupe and escalation",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ld, err := a.system()
			if err != nil {
				return err
			}
			rep, err := ld.sys.Check(cmd.Context(), docsync.CheckOptions{})
			if err != nil {
				return err
			}
			store := a.notifyState(ld.st)
			state, hadState, err := store.Load(cmd.Context())
			if err != nil {
				return err
			}
			// No memory and open findings means one of two things, and they
			// cannot be told apart without the memory that is missing: a
			// genuine first run, or a run whose state was lost. Both want
			// the same thing — one message rather than one per owner — so
			// the command does not pretend to distinguish them. What it
			// must not do is send N digests that look like N new problems.
			cfg := ld.sys.Config()
			// escalate_after is validated when the config is loaded.
			escalateAfter, _ := check.ParseDuration(orDefault(cfg.Notify.EscalateAfter, config.DefaultEscalateAfter))
			now := a.now()
			byOwner := map[string]*digest{}
			open := map[string]bool{}
			for _, f := range rep.Findings {
				if f.Severity != check.SeverityError {
					continue
				}
				key := findingKey(f)
				open[key] = true
				d := byOwner[f.Owner]
				if d == nil {
					d = &digest{Owner: f.Owner}
					byOwner[f.Owner] = d
				}
				prev, seen := state[key]
				switch {
				case !seen:
					d.Findings = append(d.Findings, f)
					state[key] = Notified{FirstSent: now, LastSent: now}
				case !prev.Escalated && now.Sub(prev.FirstSent) >= escalateAfter:
					d.Escalate = append(d.Escalate, f)
					prev.Escalated, prev.LastSent = true, now
					state[key] = prev
				}
			}
			// Closed findings leave the state so a recurrence notifies
			// again. Snapshot records are keyed in their own namespace and
			// are not findings, so they are not pruned here — they are
			// cleared when the snapshot reaches the hash they describe.
			for key := range state {
				if !open[key] && !isSnapshotKey(key) {
					delete(state, key)
				}
			}
			// Snapshot staleness is a different kind of news from a
			// finding: it is not in the report, it never touches an exit
			// code, and it goes to the people who must run `ds sync` rather
			// than to whoever owns the block that changed.
			snapText := a.snapshotMessages(ld, cfg, state)
			if !hadState {
				return a.notifyReset(cmd, store, byOwner, state, cfg, dry, snapText)
			}
			owners := make([]string, 0, len(byOwner))
			for o := range byOwner {
				owners = append(owners, o)
			}
			sort.Strings(owners)
			out := cmd.OutOrStdout()
			sent := 0
			for _, o := range owners {
				d := byOwner[o]
				if len(d.Findings) == 0 && len(d.Escalate) == 0 {
					continue
				}
				text := formatDigest(d, cfg.Owners[o])
				sent++
				if dry {
					fmt.Fprint(out, text)
					continue
				}
				if hook := os.ExpandEnv(cfg.Notify.Slack); hook != "" {
					if err := a.postSlack(hook, text); err != nil {
						return err
					}
					fmt.Fprintf(out, "notified %s via slack: %d new, %d escalated\n", orNone(o), len(d.Findings), len(d.Escalate))
				} else {
					fmt.Fprint(out, text)
				}
			}
			for _, text := range snapText {
				sent++
				if dry {
					fmt.Fprint(out, text)
					continue
				}
				if hook := os.ExpandEnv(cfg.Notify.Slack); hook != "" {
					if err := a.postSlack(hook, text); err != nil {
						return err
					}
					fmt.Fprintln(out, "notified snapshot staleness via slack")
				} else {
					fmt.Fprint(out, text)
				}
			}
			if sent == 0 {
				fmt.Fprintln(out, "nothing new to notify")
			}
			if dry {
				return nil
			}
			return store.Save(cmd.Context(), state)
		},
	}
	cmd.Flags().BoolVar(&dry, flagDryRunN, false, "print digests without sending or recording")
	return cmd
}

// notifyReset is the first-run and lost-state path: one message naming
// everything that is open, then record it all. Sending a separate digest
// per owner here would read as a sudden burst of new problems, which is how
// a channel gets muted — and muting is the failure this whole seam exists
// to prevent.
func (a *App) notifyReset(cmd *cobra.Command, store NotifyState, byOwner map[string]*digest, state map[string]Notified, cfg config.Config, dry bool, snapText []string) error {
	// Every digest here holds at least one finding by construction: this
	// path runs only when the state was empty, so no finding can have been
	// seen before and each one is appended as new. An empty-digest guard
	// would be a branch nothing could reach.
	owners := make([]string, 0, len(byOwner))
	total := len(snapText)
	for o, d := range byOwner {
		owners = append(owners, o)
		total += len(d.Findings) + len(d.Escalate)
	}
	sort.Strings(owners)
	out := cmd.OutOrStdout()
	if total == 0 {
		fmt.Fprintln(out, "nothing new to notify")
		if dry {
			return nil
		}
		return store.Save(cmd.Context(), state)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", resetHeadline(total))
	for _, o := range owners {
		b.WriteString(formatDigest(byOwner[o], cfg.Owners[o]))
	}
	for _, s := range snapText {
		b.WriteString(s)
	}
	text := b.String()
	if dry {
		fmt.Fprint(out, text)
		return nil
	}
	if hook := os.ExpandEnv(cfg.Notify.Slack); hook != "" {
		if err := a.postSlack(hook, text); err != nil {
			return err
		}
		fmt.Fprintf(out, "notified via slack: %d open item(s), no previous state\n", total)
	} else {
		fmt.Fprint(out, text)
	}
	return store.Save(cmd.Context(), state)
}

// resetHeadline says plainly that this is everything open rather than
// everything new, so a reader is not told a quiet week suddenly broke.
func resetHeadline(n int) string {
	return fmt.Sprintf("docsync: no previous notifier state; %s open (this is the full list, not new problems)", plural(n, "item"))
}

// formatDigest renders one owner's digest as plain text.
func formatDigest(d *digest, people []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "docsync: %s", orNone(d.Owner))
	if len(people) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(people, ", "))
	}
	b.WriteString("\n")
	for _, f := range d.Findings {
		fmt.Fprintf(&b, "  %s:%d  %s  %s\n", f.Doc, f.Line, f.State, f.Message)
	}
	for _, f := range d.Escalate {
		fmt.Fprintf(&b, "  ESCALATED %s:%d  %s  still open\n", f.Doc, f.Line, f.State)
	}
	return b.String()
}

// postSlack sends one message to an incoming webhook.
func (a *App) postSlack(hook, text string) error {
	body, _ := json.Marshal(map[string]string{"text": text})
	client := a.httpClient
	if client == nil {
		client = &http.Client{Timeout: notifyTimeout}
	}
	resp, err := client.Post(hook, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("slack webhook: status %d", resp.StatusCode)
	}
	return nil
}

// notifyStateRow reports whether the notifier has memory between runs.
//
// It only speaks under CI, because that is where the failure is invisible:
// on a laptop the file is simply there, and on a runner it silently is not,
// so dedupe never holds and escalate_after never fires while everything
// looks healthy. A notifier that repeats itself nightly gets muted, and a
// muted notifier fails without anyone deciding to turn it off.
func (a *App) notifyStateRow(st *Store) []string {
	state, found, err := a.notifyState(st).Load(context.Background())
	if err != nil {
		return []string{"notify", doctorWarn, "state unreadable: " + err.Error()}
	}
	if os.Getenv(ciEnv) == "" {
		// A laptop with no state yet is normal, so this stays ok — but it
		// says what is actually there. Claiming a file that does not exist
		// is the kind of small untruth that makes a reader stop trusting
		// the rest of the report.
		if !found {
			return []string{"notify", doctorOK, "no state yet (first notify will create " + DirName + "/" + NotifiedFile + ")"}
		}
		return []string{"notify", doctorOK, fmt.Sprintf("%d item(s) remembered in %s/%s", len(state), DirName, NotifiedFile)}
	}
	// Under CI the memory has to have survived the checkout, and an empty
	// file is as useless as none: dedupe measures from a first sighting.
	if !found || len(state) == 0 {
		return []string{"notify", doctorWarn, "no state on this runner; dedupe and escalation will not work. Cache " + DirName + "/" + NotifiedFile + " between runs (see the nightly workflow template)"}
	}
	return []string{"notify", doctorOK, fmt.Sprintf("%d item(s) remembered", len(state))}
}
