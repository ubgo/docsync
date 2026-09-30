package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ledger"
)

// Snapshot notifications (§21). `--frozen` made a check reproducible by
// pinning the foreign blocks it resolves against, and `status` says how far
// that pin has fallen behind. Neither interrupts anyone, so a repo can stay
// green for a month while the spec it claims to implement moves underneath
// it. This is the part that speaks.
//
// Two rules shape everything here. Drift triggers, never age on its own: a
// snapshot ninety days old against an upstream that has not moved costs
// nobody anything. And urgency comes from the change class, which `check`
// already computes — so one rule decides both whether prose flags and
// whether a person is interrupted.

// tierRank orders the tiers so "more serious than last time" is a
// comparison between three values rather than between eight unordered
// classes. `gone` is above them all: it is not a class, it has no second
// version, and its consequence is known in advance.
var tierRank = map[string]int{
	config.TierNever:     0,
	config.TierDigest:    1,
	config.TierImmediate: 2,
	tierGone:             3,
}

// tierGone is the tier of a cited block the upstream no longer defines.
const tierGone = "gone"

// snapshotTier ranks one drift.
//
// The composition is the part with two natural readings, so it is written
// once here: rank over the classes that flag **individually** for this
// block's stability, not over the change as a whole. An `api` block whose
// change is `signature, body` is immediate because `signature` flags on its
// own — the `body` neither dilutes it nor adds to it, since `api` does not
// flag on `body` at all. Reading it the other way (rank every class, then
// filter) gives `api` blocks a different answer.
//
// A class that flags but appears in neither list is immediate. Guessing the
// other way fails toward silence, and silence is the failure this whole
// series of issues has been about.
func snapshotTier(b StaleBlock, stability block.Stability, cfg config.SnapshotNotifyConfig) string {
	if b.Gone {
		return cfg.OnDeleted
	}
	// An unresolved stability is `stable`, the documented default (§20).
	// Leaving it empty would make block.Flags say no to everything, so a
	// block the index could not tell us about would go silent — the safe
	// direction is the default policy, not none.
	if stability == "" {
		stability = block.StabilityStable
	}
	immediate := set(cfg.Immediate)
	digest := set(cfg.Digest)
	best := config.TierNever
	for _, c := range b.Classes {
		if !block.Flags(stability, []block.Class{c}) {
			continue
		}
		tier := config.TierImmediate
		switch {
		case immediate[string(c)]:
			tier = config.TierImmediate
		case digest[string(c)]:
			tier = config.TierDigest
		}
		if tierRank[tier] > tierRank[best] {
			best = tier
		}
	}
	return best
}

func set(names []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		out[n] = true
	}
	return out
}

// snapshotAlert is one cited block worth telling someone about.
type snapshotAlert struct {
	Repo string
	ID   string
	Tier string
	From string
	To   string
	Gone bool
	// Classes describe the change; empty for a deletion.
	Classes []block.Class
	// Cites are the file:line of every sentence in this repo that points at
	// the block. They are the part that makes the message actionable: "3
	// blocks behind" with no file names is a message people learn to
	// ignore.
	Cites []string
}

// key identifies an alert across runs. A deletion has no `to` hash to key
// on and is a different event from any change to the same block, so it
// keys on the tier instead — a block already notified for a signature
// change and then deleted notifies again.
func (s snapshotAlert) key() string {
	if s.Gone {
		return "snapshot:" + s.Repo + ":" + s.ID + ":" + tierGone
	}
	return "snapshot:" + s.Repo + ":" + s.ID + ":" + s.To + ":" + s.Tier
}

// snapshotAlerts turns a staleness report into the alerts the policy says
// are worth sending, newest classes first by urgency then by id.
func snapshotAlerts(st Staleness, cfg config.SnapshotNotifyConfig, refs ledger.Refs, stability map[string]block.Stability) []snapshotAlert {
	cites := citesByID(refs)
	var out []snapshotAlert
	for _, repo := range st.Repos {
		for _, b := range repo.Blocks {
			tier := snapshotTier(b, stability[b.ID], cfg)
			if tier == config.TierNever {
				continue
			}
			out = append(out, snapshotAlert{
				Repo: repo.Repo, ID: b.ID, Tier: tier,
				From: b.From, To: b.To, Gone: b.Gone, Classes: b.Classes,
				Cites: cites[b.ID],
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if tierRank[out[i].Tier] != tierRank[out[j].Tier] {
			return tierRank[out[i].Tier] > tierRank[out[j].Tier]
		}
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// citesByID maps an id to the places in this repo that cite it.
func citesByID(refs ledger.Refs) map[string][]string {
	out := map[string][]string{}
	for _, r := range refs.Rows {
		if r.ID == "" {
			continue
		}
		out[r.ID] = appendOnce(out[r.ID], fmt.Sprintf("%s:%d", r.Doc, r.Line))
	}
	for id := range out {
		sort.Strings(out[id])
	}
	return out
}

// formatSnapshotAlerts renders a message someone can act on without opening
// a terminal first: which upstream moved, what changed and how badly, where
// it is cited here, and the one instruction that resolves it.
func formatSnapshotAlerts(alerts []snapshotAlert, people []string) string {
	var b strings.Builder
	byRepo := map[string][]snapshotAlert{}
	var repos []string
	for _, a := range alerts {
		if _, seen := byRepo[a.Repo]; !seen {
			repos = append(repos, a.Repo)
		}
		byRepo[a.Repo] = append(byRepo[a.Repo], a)
	}
	sort.Strings(repos)
	for _, repo := range repos {
		in := byRepo[repo]
		fmt.Fprintf(&b, "docsync: %s moved under the pinned snapshot — %s behind\n", repo, plural(len(in), "cited block"))
		for _, a := range in {
			if a.Gone {
				fmt.Fprintf(&b, "  %-10s %s  %s -> gone (a sync will make its citations broken)\n", a.Tier, a.ID, short(a.From))
			} else {
				fmt.Fprintf(&b, "  %-10s %s  %s -> %s  %s\n", a.Tier, a.ID, short(a.From), short(a.To), classList(a.Classes))
			}
			for _, c := range a.Cites {
				fmt.Fprintf(&b, "               cited at %s\n", c)
			}
		}
	}
	if len(people) > 0 {
		fmt.Fprintf(&b, "  owners: %s\n", strings.Join(people, ", "))
	}
	b.WriteString("  run `ds sync`, then resolve the findings it produces\n")
	return b.String()
}

// noComparisonKey identifies the "could not compare this repo" record.
func noComparisonKey(repo string) string { return "snapshot-blind:" + repo }

// missesBeforeWarning is how many consecutive runs may fail to compare
// before that itself is worth saying. One failure is a flaky network; three
// nightly runs is an index nobody is watching.
const missesBeforeWarning = 3

// blindRepos returns the upstream repos this run could not compare, having
// updated their consecutive-miss counters. A repo that compares again
// clears its counter, so a recovered index goes quiet without anyone acting.
//
// It reports only on the run that crosses the threshold, keyed per repo, so
// a persistently broken index says this once rather than nightly.
func blindRepos(st Staleness, state map[string]Notified) []string {
	var crossed []string
	seen := map[string]bool{}
	for _, r := range st.Repos {
		key := noComparisonKey(r.Repo)
		seen[key] = true
		n := state[key]
		if st.Compared && r.Compared {
			delete(state, key)
			continue
		}
		n.Misses++
		state[key] = n
		if n.Misses == missesBeforeWarning {
			crossed = append(crossed, r.Repo)
		}
	}
	sort.Strings(crossed)
	return crossed
}

// formatBlindRepos says that the comparison did not happen, which is a
// different thing from nothing having changed.
func formatBlindRepos(repos []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "docsync: could not compare the pinned snapshot against %s for %d runs\n", plural(len(repos), "repo"), missesBeforeWarning)
	for _, r := range repos {
		fmt.Fprintf(&b, "  %s: no published state reachable; staleness is unknown, not absent\n", r)
	}
	b.WriteString("  check the workspace index is reachable and that the repo still publishes\n")
	return b.String()
}

// snapshotMessages is the snapshot half of a notify run: what is worth
// saying about the pinned upstream, and what has already been said.
//
// It returns text rather than sending, so the caller decides the channel
// and the dry run. Recording happens here because the dedupe state and the
// decision belong together; a dry run is handled by the caller not saving.
func (a *App) snapshotMessages(ld loaded, cfg config.Config, state map[string]Notified) []string {
	s := cfg.Notify.Snapshot
	if cfg.Workspace == "" || !s.Enabled {
		return nil
	}
	st := a.staleness(cfg, bodyLookup(ld.st, a.indexFS, a.indexEntries))
	if !st.Present {
		return nil
	}
	// A block the snapshot has caught up with is forgotten, so a later
	// drift on it is news again.
	clearResolved(state, st)
	var out []string
	if blind := blindRepos(st, state); len(blind) > 0 {
		out = append(out, formatBlindRepos(blind))
	}
	// The committed state system() already read: reading it again here
	// would add an error branch for a read that has just succeeded.
	local, refs := ld.prev, ld.refs
	// Stability decides which classes speak at all, and it belongs to the
	// upstream block, so it comes from the merged index rather than from
	// anything local.
	stability := map[string]block.Stability{}
	if idx, err := a.syncIndex(cfg, false); err == nil {
		for _, b := range idx.Merged.Defs {
			stability[b.ID] = b.Stability()
		}
	}
	// digest_after and escalate_after are validated when the config is parsed, so there is no
	// fallback here: a branch that cannot be reached is a branch nobody can
	// test and every reader still has to consider.
	digestAfter, _ := check.ParseDuration(orDefault(s.DigestAfter, config.DefaultDigestAfter))
	escalateAfter, _ := check.ParseDuration(orDefault(cfg.Notify.EscalateAfter, config.DefaultEscalateAfter))
	now := a.now()
	alerts := snapshotAlerts(st, s, refs, stability)
	var send []snapshotAlert
	for _, al := range alerts {
		key := al.key()
		prev, seen := state[key]
		switch {
		case !seen:
			// A drift that only worsens is news; a second edit at the same
			// tier is not. The previous tier for this block is whatever was
			// recorded for it, whichever hash it was at.
			if last, ok := lastTier(state, al); ok && tierRank[al.Tier] <= tierRank[last] {
				continue
			}
			// A digest-tier drift is recorded now and mentioned later: it
			// did not change what any sentence asserts, so it is not worth
			// interrupting anyone until it has been ignored for a while.
			// LastSent stays zero to mean "recorded, not yet said".
			state[key] = Notified{FirstSent: now, Tier: al.Tier}
			if al.Tier == config.TierDigest {
				continue
			}
			prev = state[key]
			prev.LastSent = now
			state[key] = prev
			send = append(send, al)
		case prev.LastSent.IsZero():
			// Recorded earlier and still waiting out digest_after.
			if now.Sub(prev.FirstSent) < digestAfter {
				continue
			}
			prev.LastSent = now
			state[key] = prev
			send = append(send, al)
		case !prev.Escalated && al.Tier != config.TierDigest && now.Sub(prev.FirstSent) >= escalateAfter:
			// Said once, still unresolved after escalate_after. Raised
			// again, once, using the timer notify already has.
			prev.Escalated, prev.LastSent = true, now
			state[key] = prev
			send = append(send, al)
		}
	}
	if len(send) == 0 {
		return out
	}
	people := cfg.Owners[s.Owner]
	if s.Owner == "" {
		people = citingOwners(cfg, local, send)
	}
	return append(out, formatSnapshotAlerts(send, people))
}

// lastTier finds the most serious tier already sent for this block, across
// whatever hash it was at.
func lastTier(state map[string]Notified, al snapshotAlert) (string, bool) {
	prefix := "snapshot:" + al.Repo + ":" + al.ID + ":"
	best, found := config.TierNever, false
	for k, n := range state {
		if !strings.HasPrefix(k, prefix) || n.Tier == "" {
			continue
		}
		found = true
		if tierRank[n.Tier] > tierRank[best] {
			best = n.Tier
		}
	}
	return best, found
}

// citingOwners are the people to tell when [notify.snapshot] owner is not
// set. The default is deliberately not the block's owner: `notify` routes a
// finding to whoever owns the block, which for a cross-repo citation is the
// team that made the change, not the team that must run `ds sync` and
// resolve what it surfaces.
//
// "The owners of the citing files" has to be derived, because nothing maps
// a file to a team: `[owners]` maps a team to people, and ownership is
// carried by `owner=` on a def. So the citing file's owners are the owners
// of the defs that live in it, expanded through `[owners]`. A citing file
// with no defs of its own yields nobody, and the message simply carries no
// owner line rather than inventing one.
func citingOwners(cfg config.Config, local ledger.Ledger, alerts []snapshotAlert) []string {
	teams := map[string]bool{}
	for _, al := range alerts {
		for _, c := range al.Cites {
			file := c
			if i := strings.LastIndex(c, ":"); i > 0 {
				file = c[:i]
			}
			for _, row := range local.Rows {
				if row.File == file && row.Owner != "" {
					teams[row.Owner] = true
				}
			}
		}
	}
	var out []string
	for team := range teams {
		for _, p := range cfg.Owners[team] {
			out = appendOnce(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// snapshotKeyPrefix namespaces the records `notify` keeps about the pinned
// snapshot, so the per-finding prune leaves them alone: they are not
// findings, and a run in which nothing is open must not forget what it has
// already said about upstream drift.
const snapshotKeyPrefix = "snapshot"

func isSnapshotKey(key string) bool {
	return strings.HasPrefix(key, snapshotKeyPrefix+":") || strings.HasPrefix(key, snapshotKeyPrefix+"-blind:")
}

// clearResolved drops the records for blocks the snapshot has caught up
// with, so a later drift on the same block is news again.
func clearResolved(state map[string]Notified, st Staleness) {
	behind := map[string]bool{}
	for _, r := range st.Repos {
		for _, b := range r.Blocks {
			behind[snapshotKeyPrefix+":"+r.Repo+":"+b.ID] = true
		}
	}
	for key := range state {
		if !strings.HasPrefix(key, snapshotKeyPrefix+":") {
			continue
		}
		// key is snapshot:<repo>:<id>:<rest>; keep it only while that
		// block is still behind.
		parts := strings.SplitN(key, ":", 4)
		if len(parts) < 4 {
			continue
		}
		if !behind[parts[0]+":"+parts[1]+":"+parts[2]] {
			delete(state, key)
		}
	}
}

// orDefault falls back when a config value is empty.
func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
