// Package config reads `.ds/config.toml` (docs/SPEC.md §23) and the
// workspace file, with defaults for everything the spec gives a default.
//
// It parses the TOML subset the config actually uses, tables and dotted
// tables, `key = value` with strings, integers, floats, booleans, and arrays
// of scalars, and refuses anything else with a line number. A full TOML
// parser is a dependency the root module does not take (§37.1); the shape
// here is small, fixed, and covered by the conformance fixtures, and a value
// the reader does not understand is an error rather than a silent default.
package config

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/internal/duration"
)

// Spec is the specification version this config schema follows.
const Spec = "1.0"

// Defaults from the spec.
const (
	DefaultPrefix         = "ds"
	DefaultIncludeMode    = IncludeBuild
	DefaultMaxLines       = 40
	DefaultFuzzyThreshold = 0.8
	DefaultUnacked        = UnackedError
	DefaultSentence       = SentenceWording
	DefaultMaxFileKB      = 512
	DefaultMaxLineChars   = 2000
	DefaultRunTimeout     = "30s"
	DefaultURLTTL         = "7d"
	DefaultURLRate        = 30
	DefaultEnv            = ""
	DefaultRecords        = RecordsFrontmatter
	DefaultSuffixLength   = 8
	DefaultSuffixAlphabet = "23456789abcdefghjkmnpqrstuvwxyz"
	DefaultMaxDefsPerRun  = 20
	DefaultEscalateAfter  = "7d"
	DefaultSourceTTL      = "24h"
)

// Closed-set values.
const (
	IncludeBuild = "build"
	IncludeRepo  = "repo"

	UnackedError = "error"
	UnackedWarn  = "warn"

	RecordsFrontmatter = "frontmatter"
	RecordsSQLite      = "sqlite"
	RecordsHTTP        = "http"

	// SentenceWording holds an ack to the words it approved: a cited
	// sentence rewritten since its ack is unacked (SPEC §18).
	// SentencePosition holds it to the citation's place only, the older
	// behaviour, for a repository that opts out knowingly.
	SentenceWording  = "wording"
	SentencePosition = "position"
)

// IncludeModeValues, UnackedValues, RecordsValues are the canonical lists.
var (
	IncludeModeValues = []string{IncludeBuild, IncludeRepo}
	UnackedValues     = []string{UnackedError, UnackedWarn}
	RecordsValues     = []string{RecordsFrontmatter, RecordsSQLite, RecordsHTTP}
	SentenceValues    = []string{SentenceWording, SentencePosition}
)

// Sentinel errors.
var (
	ErrSyntax   = errors.New("config: syntax error")
	ErrType     = errors.New("config: wrong value type")
	ErrValue    = errors.New("config: invalid value")
	ErrUnknown  = errors.New("config: unknown key")
	ErrSpec     = errors.New("config: spec version not supported by this tool")
	ErrNoScan   = errors.New("config: scan.code and scan.docs are both empty; nothing would be scanned")
	ErrRequired = errors.New("config: required key missing")
)

// Config mirrors the file. Field docs are the spec's meaning; see §23.
type Config struct {
	Spec      string
	Prefix    string
	Workspace string
	Scan      ScanConfig
	Include   IncludeConfig
	Check     CheckConfig
	Policy    PolicyConfig
	Owners    map[string][]string
	Secret    SecretConfig
	Env       EnvConfig
	Resolve   ResolveConfig
	Run       RunConfig
	URL       URLConfig
	Sources   map[string]SourceConfig
	Records   RecordsConfig
	Notify    NotifyConfig
	Agents    AgentsConfig
	ID        IDConfig
	Plugins   PluginsConfig
	Review    ReviewConfig
	Ledger    LedgerConfig
}

// ReviewConfig is [review]: the command `review --ai` pipes findings and
// context into, which must print a unified diff (§22). The model is the
// user's choice; the tool never calls one itself.
type ReviewConfig struct {
	Command string
}

// LedgerConfig is [ledger]: shard = true writes .ds/ledger/<dir>.tsv per
// top-level directory instead of one file (§ Scale).
type LedgerConfig struct {
	Shard bool
}

// PluginsConfig is [plugins] (§9.9, §37.4): names of process plugins the
// CLI should look for on PATH as `ds-<verb>` and `ds-pick-<scheme>`.
type PluginsConfig struct {
	Verbs []string
	Picks []string
}

// ScanConfig is [scan] and [scan.limits].
type ScanConfig struct {
	Code         []string
	Docs         []string
	Exclude      []string
	Generated    []string
	MaxFileKB    int
	MaxLineChars int
}

// IncludeConfig is [include].
type IncludeConfig struct {
	Mode     string
	MaxLines int
}

// CheckConfig is [check].
type CheckConfig struct {
	FuzzyThreshold float64
	Unacked        string
	Permalink      string
	// SnapshotMaxAge, e.g. "30d", makes `check --frozen` warn when
	// .ds/foreign.tsv is older than that. Empty (the default) means never:
	// a pinned check that also failed with age would stop being
	// reproducible, which is the one thing --frozen exists to guarantee. It
	// is a nudge for a CI that never syncs, and a warning is the strongest
	// it may be.
	SnapshotMaxAge string
	// Sentence is what an ack holds a citation to: its wording
	// (SentenceWording, the default) or only its position.
	Sentence string
}

// PolicyConfig is [policy].
type PolicyConfig struct {
	RequireDoc []string
}

// SecretConfig is [secret].
type SecretConfig struct {
	Paths []string
}

// EnvConfig is [env].
type EnvConfig struct {
	Default string
	Known   []string
}

// ResolveConfig is [resolve].
type ResolveConfig struct {
	Enabled   bool
	StoreHash bool
	Providers []string
}

// RunConfig is [run] and [run.env.<name>].
type RunConfig struct {
	Enabled bool
	Allow   []string
	Timeout string
	Env     map[string]map[string]string
}

// URLConfig is [url].
type URLConfig struct {
	TTL           string
	RatePerMinute int
}

// SourceConfig is one [sources.<name>].
type SourceConfig struct {
	DSN string
	URL string
	TTL string
}

// RecordsConfig is [records].
type RecordsConfig struct {
	Source string
	Path   string
	// Table is the default table for the sqlite source when a `ds:table`
	// names no kind=.
	Table string
}

// SnapshotNotifyConfig is [notify.snapshot] (§21): when a citing repo is
// told that the upstream it pinned has moved.
//
// Drift is what triggers a message, never age on its own — a snapshot ninety
// days old against an upstream that has not moved costs nobody anything.
// Urgency comes from the change class, which `check` already computes, so
// the same rule decides whether prose flags and whether a person is
// interrupted.
type SnapshotNotifyConfig struct {
	// Enabled defaults to true where a workspace is configured and is
	// always false without one: a repo that cites nothing foreign has no
	// snapshot to go stale.
	Enabled bool
	// Immediate and Digest rank the classes that flag. A class named in
	// both is a configuration error; a class that flags but is named in
	// neither is treated as Immediate, because guessing the other way fails
	// toward silence.
	Immediate []string
	Digest    []string
	// DigestAfter is how long a digest-tier drift waits before it is worth
	// mentioning at all.
	DigestAfter string
	// OnDeleted ranks the case that is not a class: the upstream block is
	// gone. It has no second version to compare, and its consequence is
	// known in advance — the next sync turns its citations broken — so it
	// is its own key rather than a member of a class list.
	OnDeleted string
	// Owner receives snapshot messages. Empty falls back to the owners of
	// the citing files, because the people who must run `ds sync` and
	// resolve what it surfaces are in this repo, not the one that changed.
	Owner string
}

// The tiers a snapshot drift can reach, in order of urgency. They are the
// vocabulary for Immediate, Digest and OnDeleted alike, so there is one set
// of words rather than a bespoke one per key.
const (
	TierNever     = "never"
	TierDigest    = "digest"
	TierImmediate = "immediate"
)

// TierValues is the canonical order, lowest first.
var TierValues = []string{TierNever, TierDigest, TierImmediate}

// Defaults for [notify.snapshot].
const (
	DefaultDigestAfter = "14d"
	DefaultOnDeleted   = TierImmediate
)

// DefaultImmediateClasses are the classes that change what a citation
// asserts; DefaultDigestClasses are the ones that do not but are still
// worth knowing eventually.
var (
	DefaultImmediateClasses = []string{"signature", "type", "renamed", "value"}
	DefaultDigestClasses    = []string{"body", "comment"}
)

// NotifyConfig is [notify].
type NotifyConfig struct {
	Snapshot      SnapshotNotifyConfig
	Slack         string
	GitHubIssues  bool
	EscalateAfter string
}

// AgentsConfig is [agents].
type AgentsConfig struct {
	MaxDefsPerRun int
	MCP           bool
	SessionHook   string
}

// IDConfig is [id].
type IDConfig struct {
	SuffixAlphabet string
	SuffixLength   int
}

// Default returns a config with every spec default filled in and no scan
// paths; Validate rejects it until scan.code or scan.docs is set.
func Default() Config {
	return Config{
		Spec:    Spec,
		Prefix:  DefaultPrefix,
		Scan:    ScanConfig{MaxFileKB: DefaultMaxFileKB, MaxLineChars: DefaultMaxLineChars},
		Include: IncludeConfig{Mode: DefaultIncludeMode, MaxLines: DefaultMaxLines},
		Check:   CheckConfig{FuzzyThreshold: DefaultFuzzyThreshold, Unacked: DefaultUnacked, Sentence: DefaultSentence},
		Owners:  map[string][]string{},
		Env:     EnvConfig{Default: DefaultEnv},
		Run:     RunConfig{Timeout: DefaultRunTimeout, Env: map[string]map[string]string{}},
		URL:     URLConfig{TTL: DefaultURLTTL, RatePerMinute: DefaultURLRate},
		Sources: map[string]SourceConfig{},
		Records: RecordsConfig{Source: DefaultRecords},
		Notify: NotifyConfig{
			EscalateAfter: DefaultEscalateAfter,
			Snapshot: SnapshotNotifyConfig{
				// On by default. Without a workspace there is no snapshot
				// to go stale, so the notifier returns early and this never
				// matters; with one, silence should be something an
				// operator chose rather than something they forgot.
				Enabled:     true,
				Immediate:   append([]string{}, DefaultImmediateClasses...),
				Digest:      append([]string{}, DefaultDigestClasses...),
				DigestAfter: DefaultDigestAfter,
				OnDeleted:   DefaultOnDeleted,
			},
		},
		Agents: AgentsConfig{MaxDefsPerRun: DefaultMaxDefsPerRun, MCP: true},
		ID:     IDConfig{SuffixAlphabet: DefaultSuffixAlphabet, SuffixLength: DefaultSuffixLength},
	}
}

// Parse reads TOML text into a Config over the defaults. Unknown keys are
// errors: a misspelled key that silently did nothing is the classic config
// failure and the spec's "unknown warns" rule applies to directives, not to
// the file that configures the tool.
func Parse(r io.Reader) (Config, error) { return ParseOnto(Default(), r) }

// ParseOnto reads a config file on top of base instead of the built-in
// defaults: a key the file sets wins, and every key it does not set keeps
// base's value. It is how an organisation-wide binary's defaults
// (cli.WithConfigDefaults) reach a repository whose config.toml does not
// mention them — reading onto the built-in defaults instead dropped them all.
//
// base is not modified. Its maps are copied first, because applying a file
// writes into them, and an organisation hook that shares one map between
// calls would otherwise carry one repository's owners into the next.
func ParseOnto(base Config, r io.Reader) (Config, error) {
	src, err := io.ReadAll(r)
	if err != nil {
		return Config{}, err
	}
	doc, err := parseTOML(string(src))
	if err != nil {
		return Config{}, err
	}
	c := cloneMaps(base)
	if err := c.apply(doc); err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate checks closed-set values and ranges.
func (c Config) Validate() error {
	if c.Spec != Spec {
		return fmt.Errorf("%w: file says %q, tool implements %q", ErrSpec, c.Spec, Spec)
	}
	if c.Prefix == "" || !isIdent(c.Prefix) {
		return fmt.Errorf("%w: prefix %q must be a lowercase identifier", ErrValue, c.Prefix)
	}
	if len(c.Scan.Code) == 0 && len(c.Scan.Docs) == 0 {
		return ErrNoScan
	}
	if !in(c.Include.Mode, IncludeModeValues) {
		return fmt.Errorf("%w: include.mode %q", ErrValue, c.Include.Mode)
	}
	if !in(c.Check.Unacked, UnackedValues) {
		return fmt.Errorf("%w: check.unacked %q", ErrValue, c.Check.Unacked)
	}
	// Empty means the default, as for the duration keys: a config built in
	// code need not set it.
	if c.Check.Sentence != "" && !in(c.Check.Sentence, SentenceValues) {
		return fmt.Errorf("%w: check.sentence %q", ErrValue, c.Check.Sentence)
	}
	if c.Check.FuzzyThreshold <= 0 || c.Check.FuzzyThreshold > 1 {
		return fmt.Errorf("%w: check.fuzzy_threshold %v must be in (0,1]", ErrValue, c.Check.FuzzyThreshold)
	}
	if c.Records.Source == "" || !isIdent(strings.ReplaceAll(c.Records.Source, "-", "_")) {
		// Built-in sources plus any `ds-records-<name>` plugin name.
		return fmt.Errorf("%w: records.source %q", ErrValue, c.Records.Source)
	}
	if c.Include.MaxLines < 1 || c.Scan.MaxFileKB < 1 || c.Scan.MaxLineChars < 1 || c.URL.RatePerMinute < 1 || c.Agents.MaxDefsPerRun < 0 {
		return fmt.Errorf("%w: a limit is below its minimum", ErrValue)
	}
	if c.Env.Default != "" && len(c.Env.Known) > 0 && !in(c.Env.Default, c.Env.Known) {
		return fmt.Errorf("%w: env.default %q is not in env.known", ErrValue, c.Env.Default)
	}
	if err := c.Notify.Snapshot.validate(); err != nil {
		return err
	}
	return c.validateDurations()
}

// validateDurations refuses a duration key that does not parse. Each
// consumer used to fall back to the default on a bad value, so a typo such
// as `ttl = "7 days"` loaded cleanly and ran with a setting nobody chose.
// An empty value is allowed and means the default; a config built in code
// leaves these zero. run.timeout is a Go duration because it bounds a
// process; the rest use the day-aware form of internal/duration.
func (c Config) validateDurations() error {
	days := []struct{ key, v string }{
		{"url.ttl", c.URL.TTL},
		{"notify.escalate_after", c.Notify.EscalateAfter},
		{"check.snapshot_max_age", c.Check.SnapshotMaxAge},
	}
	for _, name := range sortedKeys(c.Sources) {
		days = append(days, struct{ key, v string }{"sources." + name + ".ttl", c.Sources[name].TTL})
	}
	for _, d := range days {
		if d.v == "" {
			continue
		}
		if _, err := duration.Parse(d.v); err != nil {
			return fmt.Errorf("%w: %s %q", ErrValue, d.key, d.v)
		}
	}
	if c.Run.Timeout != "" {
		if t, err := time.ParseDuration(c.Run.Timeout); err != nil || t <= 0 {
			return fmt.Errorf("%w: run.timeout %q must be a positive duration such as 30s", ErrValue, c.Run.Timeout)
		}
	}
	return nil
}

func in(v string, set []string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func isIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' && i > 0 || c == '_') {
			return false
		}
	}
	return s != ""
}

// apply walks the parsed document into the typed config. Every accepted key
// appears here; anything else is ErrUnknown with its path.
func (c *Config) apply(doc map[string]value) error {
	for _, k := range sortedKeys(doc) {
		v := doc[k]
		var err error
		switch k {
		case "spec":
			c.Spec, err = v.str()
		case "prefix":
			c.Prefix, err = v.str()
		case "workspace":
			c.Workspace, err = v.str()
		case "scan":
			err = c.applyScan(v)
		case "include":
			err = applyTable(v, map[string]func(value) error{
				"mode":      func(x value) (e error) { c.Include.Mode, e = x.str(); return },
				"max_lines": func(x value) (e error) { c.Include.MaxLines, e = x.integer(); return },
			})
		case "check":
			err = applyTable(v, map[string]func(value) error{
				"fuzzy_threshold":  func(x value) (e error) { c.Check.FuzzyThreshold, e = x.float(); return },
				"unacked":          func(x value) (e error) { c.Check.Unacked, e = x.str(); return },
				"permalink":        func(x value) (e error) { c.Check.Permalink, e = x.str(); return },
				"snapshot_max_age": func(x value) (e error) { c.Check.SnapshotMaxAge, e = x.str(); return },
				"sentence":         func(x value) (e error) { c.Check.Sentence, e = x.str(); return },
			})
		case "policy":
			err = applyTable(v, map[string]func(value) error{
				"require_doc": func(x value) (e error) { c.Policy.RequireDoc, e = x.strs(); return },
			})
		case "owners":
			err = c.applyOwners(v)
		case "secret":
			err = applyTable(v, map[string]func(value) error{
				"paths": func(x value) (e error) { c.Secret.Paths, e = x.strs(); return },
			})
		case "env":
			err = applyTable(v, map[string]func(value) error{
				"default": func(x value) (e error) { c.Env.Default, e = x.str(); return },
				"known":   func(x value) (e error) { c.Env.Known, e = x.strs(); return },
			})
		case "resolve":
			err = applyTable(v, map[string]func(value) error{
				"enabled":    func(x value) (e error) { c.Resolve.Enabled, e = x.boolean(); return },
				"store_hash": func(x value) (e error) { c.Resolve.StoreHash, e = x.boolean(); return },
				"providers":  func(x value) (e error) { c.Resolve.Providers, e = x.strs(); return },
			})
		case "run":
			err = c.applyRun(v)
		case "url":
			err = applyTable(v, map[string]func(value) error{
				"ttl":             func(x value) (e error) { c.URL.TTL, e = x.str(); return },
				"rate_per_minute": func(x value) (e error) { c.URL.RatePerMinute, e = x.integer(); return },
			})
		case "sources":
			err = c.applySources(v)
		case "records":
			err = applyTable(v, map[string]func(value) error{
				"source": func(x value) (e error) { c.Records.Source, e = x.str(); return },
				"path":   func(x value) (e error) { c.Records.Path, e = x.str(); return },
				"table":  func(x value) (e error) { c.Records.Table, e = x.str(); return },
			})
		case "notify":
			err = applyTable(v, map[string]func(value) error{
				"slack":          func(x value) (e error) { c.Notify.Slack, e = x.str(); return },
				"github_issues":  func(x value) (e error) { c.Notify.GitHubIssues, e = x.boolean(); return },
				"escalate_after": func(x value) (e error) { c.Notify.EscalateAfter, e = x.str(); return },
				"snapshot":       c.applySnapshotNotify,
			})
		case "agents":
			err = applyTable(v, map[string]func(value) error{
				"max_defs_per_run": func(x value) (e error) { c.Agents.MaxDefsPerRun, e = x.integer(); return },
				"mcp":              func(x value) (e error) { c.Agents.MCP, e = x.boolean(); return },
				"session_hook":     func(x value) (e error) { c.Agents.SessionHook, e = x.str(); return },
			})
		case "id":
			err = applyTable(v, map[string]func(value) error{
				"suffix_alphabet": func(x value) (e error) { c.ID.SuffixAlphabet, e = x.str(); return },
				"suffix_length":   func(x value) (e error) { c.ID.SuffixLength, e = x.integer(); return },
			})
		case "review":
			err = applyTable(v, map[string]func(value) error{
				"command": func(x value) (e error) { c.Review.Command, e = x.str(); return },
			})
		case "ledger":
			err = applyTable(v, map[string]func(value) error{
				"shard": func(x value) (e error) { c.Ledger.Shard, e = x.boolean(); return },
			})
		case "plugins":
			err = applyTable(v, map[string]func(value) error{
				"verbs": func(x value) (e error) { c.Plugins.Verbs, e = x.strs(); return },
				"picks": func(x value) (e error) { c.Plugins.Picks, e = x.strs(); return },
			})
		case "performance":
			// Targets for the suite, not tunables (§23); accepted and ignored.
			_, err = v.table()
		default:
			err = fmt.Errorf("%w: %s (line %d)", ErrUnknown, k, v.line)
		}
		if err != nil {
			return wrapKey(k, err)
		}
	}
	return nil
}

func (c *Config) applyScan(v value) error {
	return applyTable(v, map[string]func(value) error{
		"code":      func(x value) (e error) { c.Scan.Code, e = x.strs(); return },
		"docs":      func(x value) (e error) { c.Scan.Docs, e = x.strs(); return },
		"exclude":   func(x value) (e error) { c.Scan.Exclude, e = x.strs(); return },
		"generated": func(x value) (e error) { c.Scan.Generated, e = x.strs(); return },
		"limits": func(x value) error {
			return applyTable(x, map[string]func(value) error{
				"max_file_kb":    func(y value) (e error) { c.Scan.MaxFileKB, e = y.integer(); return },
				"max_line_chars": func(y value) (e error) { c.Scan.MaxLineChars, e = y.integer(); return },
			})
		},
	})
}

func (c *Config) applyOwners(v value) error {
	t, err := v.table()
	if err != nil {
		return err
	}
	for _, team := range sortedKeys(t) {
		people, err := t[team].strs()
		if err != nil {
			return wrapKey(team, err)
		}
		c.Owners[team] = people
	}
	return nil
}

func (c *Config) applyRun(v value) error {
	return applyTable(v, map[string]func(value) error{
		"enabled": func(x value) (e error) { c.Run.Enabled, e = x.boolean(); return },
		"allow":   func(x value) (e error) { c.Run.Allow, e = x.strs(); return },
		"timeout": func(x value) (e error) { c.Run.Timeout, e = x.str(); return },
		"env": func(x value) error {
			envs, err := x.table()
			if err != nil {
				return err
			}
			for _, name := range sortedKeys(envs) {
				vars, err := envs[name].table()
				if err != nil {
					return wrapKey(name, err)
				}
				m := map[string]string{}
				for _, k := range sortedKeys(vars) {
					s, err := vars[k].str()
					if err != nil {
						return wrapKey(name+"."+k, err)
					}
					m[k] = s
				}
				c.Run.Env[name] = m
			}
			return nil
		},
	})
}

func (c *Config) applySources(v value) error {
	t, err := v.table()
	if err != nil {
		return err
	}
	for _, name := range sortedKeys(t) {
		var sc SourceConfig
		sc.TTL = DefaultSourceTTL
		err := applyTable(t[name], map[string]func(value) error{
			"dsn": func(x value) (e error) { sc.DSN, e = x.str(); return },
			"url": func(x value) (e error) { sc.URL, e = x.str(); return },
			"ttl": func(x value) (e error) { sc.TTL, e = x.str(); return },
		})
		if err != nil {
			return wrapKey(name, err)
		}
		c.Sources[name] = sc
	}
	return nil
}

// applyTable dispatches each key of a table to its setter and rejects
// unknown keys.
func applyTable(v value, setters map[string]func(value) error) error {
	t, err := v.table()
	if err != nil {
		return err
	}
	for _, k := range sortedKeys(t) {
		set, ok := setters[k]
		if !ok {
			return fmt.Errorf("%w: %s (line %d)", ErrUnknown, k, t[k].line)
		}
		if err := set(t[k]); err != nil {
			return wrapKey(k, err)
		}
	}
	return nil
}

func wrapKey(k string, err error) error {
	return fmt.Errorf("%s: %w", k, err)
}

// ------------------------------------------------------------ TOML subset

// value is a parsed TOML value with the line it came from.
type value struct {
	kind  kind
	s     string
	i     int64
	f     float64
	b     bool
	arr   []value
	tbl   map[string]value
	line  int
	isInt bool
}

type kind int

const (
	kindString kind = iota
	kindInt
	kindFloat
	kindBool
	kindArray
	kindTable
)

func (v value) str() (string, error) {
	if v.kind != kindString {
		return "", fmt.Errorf("%w: want string (line %d)", ErrType, v.line)
	}
	return v.s, nil
}

func (v value) integer() (int, error) {
	if v.kind != kindInt {
		return 0, fmt.Errorf("%w: want integer (line %d)", ErrType, v.line)
	}
	return int(v.i), nil
}

func (v value) float() (float64, error) {
	switch v.kind {
	case kindFloat:
		return v.f, nil
	case kindInt:
		return float64(v.i), nil
	}
	return 0, fmt.Errorf("%w: want number (line %d)", ErrType, v.line)
}

func (v value) boolean() (bool, error) {
	if v.kind != kindBool {
		return false, fmt.Errorf("%w: want true or false (line %d)", ErrType, v.line)
	}
	return v.b, nil
}

func (v value) strs() ([]string, error) {
	if v.kind != kindArray {
		return nil, fmt.Errorf("%w: want array of strings (line %d)", ErrType, v.line)
	}
	out := make([]string, 0, len(v.arr))
	for _, e := range v.arr {
		s, err := e.str()
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (v value) table() (map[string]value, error) {
	if v.kind != kindTable {
		return nil, fmt.Errorf("%w: want table (line %d)", ErrType, v.line)
	}
	return v.tbl, nil
}

// sortedKeys gives a map's keys in order, so every walk over one (and every
// error it reports) is deterministic.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// parseTOML parses the subset: comments, blank lines, `[a.b]` headers,
// `key = value`. Keys are bare or quoted. Values are basic strings with the
// common escapes, literal strings, integers, floats, booleans, and single-line
// arrays of scalars. Duplicate keys are errors.
func parseTOML(src string) (map[string]value, error) {
	root := map[string]value{}
	cur := root
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	for i, raw := range lines {
		n := i + 1
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if strings.HasPrefix(line, "[[") {
				return nil, fmt.Errorf("%w: line %d: arrays of tables are not supported", ErrSyntax, n)
			}
			if !strings.HasSuffix(line, "]") {
				return nil, fmt.Errorf("%w: line %d: unterminated table header", ErrSyntax, n)
			}
			path, err := splitKeyPath(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"), n)
			if err != nil {
				return nil, err
			}
			t, err := descend(root, path, n)
			if err != nil {
				return nil, err
			}
			cur = t
			continue
		}
		k, rest, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%w: line %d: expected key = value", ErrSyntax, n)
		}
		path, err := splitKeyPath(strings.TrimSpace(k), n)
		if err != nil {
			return nil, err
		}
		val, err := parseValue(strings.TrimSpace(rest), n)
		if err != nil {
			return nil, err
		}
		target := cur
		if len(path) > 1 {
			target, err = descend(cur, path[:len(path)-1], n)
			if err != nil {
				return nil, err
			}
		}
		leaf := path[len(path)-1]
		if _, dup := target[leaf]; dup {
			return nil, fmt.Errorf("%w: line %d: duplicate key %q", ErrSyntax, n, leaf)
		}
		target[leaf] = val
	}
	return root, nil
}

// stripComment removes a `#` comment outside quotes.
func stripComment(s string) string {
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == '\\' && q == '"' {
				i++
			} else if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '#':
			return s[:i]
		}
	}
	return s
}

// splitKeyPath splits `a.b."c d"` into segments.
func splitKeyPath(s string, n int) ([]string, error) {
	var out []string
	var cur strings.Builder
	var q byte
	flush := func() error {
		k := strings.TrimSpace(cur.String())
		if k == "" {
			return fmt.Errorf("%w: line %d: empty key segment", ErrSyntax, n)
		}
		out = append(out, k)
		cur.Reset()
		return nil
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == q {
				q = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '"' || c == '\'':
			q = c
		case c == '.':
			if err := flush(); err != nil {
				return nil, err
			}
		default:
			cur.WriteByte(c)
		}
	}
	if q != 0 {
		return nil, fmt.Errorf("%w: line %d: unterminated quoted key", ErrSyntax, n)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

// descend returns the table at path under root, creating tables as needed.
func descend(root map[string]value, path []string, n int) (map[string]value, error) {
	cur := root
	for _, seg := range path {
		next, ok := cur[seg]
		if !ok {
			next = value{kind: kindTable, tbl: map[string]value{}, line: n}
			cur[seg] = next
		}
		if next.kind != kindTable {
			return nil, fmt.Errorf("%w: line %d: %q is a value, not a table", ErrSyntax, n, seg)
		}
		cur = next.tbl
	}
	return cur, nil
}

// parseValue parses one scalar or a single-line array.
func parseValue(s string, n int) (value, error) {
	switch {
	case s == "":
		return value{}, fmt.Errorf("%w: line %d: missing value", ErrSyntax, n)
	case s == "true", s == "false":
		return value{kind: kindBool, b: s == "true", line: n}, nil
	case s[0] == '"':
		str, rest, err := parseBasicString(s, n)
		if err != nil {
			return value{}, err
		}
		if strings.TrimSpace(rest) != "" {
			return value{}, fmt.Errorf("%w: line %d: trailing text after string", ErrSyntax, n)
		}
		return value{kind: kindString, s: str, line: n}, nil
	case s[0] == '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 || strings.TrimSpace(s[end+2:]) != "" {
			return value{}, fmt.Errorf("%w: line %d: bad literal string", ErrSyntax, n)
		}
		return value{kind: kindString, s: s[1 : end+1], line: n}, nil
	case s[0] == '[':
		return parseArray(s, n)
	}
	if i, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 10, 64); err == nil {
		return value{kind: kindInt, i: i, line: n, isInt: true}, nil
	}
	if f, err := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64); err == nil {
		return value{kind: kindFloat, f: f, line: n}, nil
	}
	return value{}, fmt.Errorf("%w: line %d: cannot parse value %q", ErrSyntax, n, s)
}

// parseBasicString reads a double-quoted string with \" \\ \n \t \r escapes
// and returns the remainder of the line.
func parseBasicString(s string, n int) (string, string, error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			return b.String(), s[i+1:], nil
		case '\\':
			i++
			if i >= len(s) {
				return "", "", fmt.Errorf("%w: line %d: bad escape", ErrSyntax, n)
			}
			switch s[i] {
			case '"', '\\':
				b.WriteByte(s[i])
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				return "", "", fmt.Errorf("%w: line %d: unsupported escape \\%c", ErrSyntax, n, s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", "", fmt.Errorf("%w: line %d: unterminated string", ErrSyntax, n)
}

// parseArray reads `[a, b, c]` of scalars on one line.
func parseArray(s string, n int) (value, error) {
	if !strings.HasSuffix(s, "]") {
		return value{}, fmt.Errorf("%w: line %d: unterminated array (arrays must be on one line)", ErrSyntax, n)
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	out := value{kind: kindArray, line: n}
	if inner == "" {
		return out, nil
	}
	for _, item := range splitTopLevel(inner) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue // trailing comma
		}
		v, err := parseValue(item, n)
		if err != nil {
			return value{}, err
		}
		if v.kind == kindArray {
			return value{}, fmt.Errorf("%w: line %d: nested arrays are not supported", ErrSyntax, n)
		}
		out.arr = append(out.arr, v)
	}
	return out, nil
}

// splitTopLevel splits on commas outside quotes.
func splitTopLevel(s string) []string {
	var out []string
	var cur strings.Builder
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			cur.WriteByte(c)
			if c == '\\' && q == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
			} else if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
			cur.WriteByte(c)
		case c == ',':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	out = append(out, cur.String())
	return out
}

// applySnapshotNotify reads [notify.snapshot]; SnapshotNotifyConfig.validate
// checks it, from Validate, because a notifier that silently disagrees with
// its configuration is worse than one that refuses to start.
func (c *Config) applySnapshotNotify(v value) error {
	enabled := false
	if err := applyTable(v, map[string]func(value) error{
		"enabled":      func(x value) (e error) { c.Notify.Snapshot.Enabled, e = x.boolean(); enabled = true; return },
		"immediate":    func(x value) (e error) { c.Notify.Snapshot.Immediate, e = x.strs(); return },
		"digest":       func(x value) (e error) { c.Notify.Snapshot.Digest, e = x.strs(); return },
		"digest_after": func(x value) (e error) { c.Notify.Snapshot.DigestAfter, e = x.str(); return },
		"on_deleted":   func(x value) (e error) { c.Notify.Snapshot.OnDeleted, e = x.str(); return },
		"owner":        func(x value) (e error) { c.Notify.Snapshot.Owner, e = x.str(); return },
	}); err != nil {
		return err
	}
	if !enabled {
		// Present but silent about `enabled` means on; a section nobody
		// wrote leaves the workspace rule to decide.
		c.Notify.Snapshot.Enabled = true
	}
	return nil
}

// validate refuses the two ways the class lists can be ambiguous, an
// unknown tier, and a digest_after that does not parse. It runs from
// Validate, not from the parser: checked only while reading TOML, a config
// built in code — an organisation's defaults, a library caller — skipped it,
// and a notifier ran with lists it disagreed with. An empty on_deleted means
// the default tier.
func (s SnapshotNotifyConfig) validate() error {
	for _, name := range s.Immediate {
		if !isClass(name) {
			return fmt.Errorf("%w: notify.snapshot.immediate %q", ErrValue, name)
		}
	}
	in := map[string]bool{}
	for _, name := range s.Immediate {
		in[name] = true
	}
	for _, name := range s.Digest {
		if !isClass(name) {
			return fmt.Errorf("%w: notify.snapshot.digest %q", ErrValue, name)
		}
		// A class in both lists has no defined tier, and picking one
		// silently is how a notifier ends up doing something nobody asked
		// for. Refuse to load instead.
		if in[name] {
			return fmt.Errorf("%w: notify.snapshot %q is in both immediate and digest", ErrValue, name)
		}
	}
	if s.OnDeleted != "" && !isTier(s.OnDeleted) {
		return fmt.Errorf("%w: notify.snapshot.on_deleted %q", ErrValue, s.OnDeleted)
	}
	if s.DigestAfter != "" {
		if _, err := duration.Parse(s.DigestAfter); err != nil {
			return fmt.Errorf("%w: notify.snapshot.digest_after %q", ErrValue, s.DigestAfter)
		}
	}
	return nil
}

// isClass reports whether name is one of block's change classes. The list
// lives there, so a class added later is accepted here without an edit.
func isClass(name string) bool {
	for _, c := range block.ClassValues {
		if string(c) == name {
			return true
		}
	}
	return false
}

func isTier(name string) bool {
	for _, t := range TierValues {
		if t == name {
			return true
		}
	}
	return false
}

// cloneMaps returns c with its maps copied, down to Run.Env's inner maps, so
// writing into the result never writes into c. Slices are replaced whole
// when a file sets them, so they need no copy.
func cloneMaps(c Config) Config {
	owners := make(map[string][]string, len(c.Owners))
	for k, v := range c.Owners {
		owners[k] = append([]string(nil), v...)
	}
	c.Owners = owners
	sources := make(map[string]SourceConfig, len(c.Sources))
	for k, v := range c.Sources {
		sources[k] = v
	}
	c.Sources = sources
	env := make(map[string]map[string]string, len(c.Run.Env))
	for k, v := range c.Run.Env {
		inner := make(map[string]string, len(v))
		for ik, iv := range v {
			inner[ik] = iv
		}
		env[k] = inner
	}
	c.Run.Env = env
	return c
}
