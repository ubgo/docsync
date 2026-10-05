package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const full = `
spec = "1.0"
prefix = "ds"
workspace = "github.com/org/ds-index"

[scan]
code = ["internal/**", "cmd/**", "config/**", "db/**"]
docs = ["docs/**/*.md", "docs/**/*.mdx", "runbooks/**/*.md", "README.md"]
exclude = ["**/testdata/**", "**/*_test.go", "public/**", "dist/**"]
generated = ["**/*.pb.go", "**/gen/**", "**/*_gen.ts"]

[scan.limits]
max_file_kb = 512
max_line_chars = 2_000

[include]
mode = "build"                               # build | repo
max_lines = 40

[check]
fuzzy_threshold = 0.8
unacked = "error"                            # error | warn
permalink = "https://github.com/org/repo/blob/{sha}/{file}#L{start}-L{end}"

[policy]
require_doc = ["pkg/api/**"]

[owners]
"@auth" = ["khanakia"]
"@platform" = ["someone", "another"]

[secret]
paths = ["**/.env*", "**/secrets/**"]

[env]
default = "prod"
known = ["prod", "staging", "dev"]

[resolve]
enabled = false
store_hash = false
providers = ["github", "1password"]

[run]
enabled = false
allow = ["runbooks/**"]
timeout = "30s"
shell = "bash"
[run.env.staging]
DATABASE_URL = "$STAGING_DATABASE_URL"
[run.env.prod]
DATABASE_URL = '$PROD "literal" url'

[url]
ttl = "7d"
rate_per_minute = 30

[records]
source = "frontmatter"                       # frontmatter | sqlite | http
path = "records/"
table = "tasks"

[notify]
slack = "$DS_SLACK_WEBHOOK"
escalate_after = "7d"

[agents]
max_defs_per_run = 20
mcp = true
session_hook = "ds map --budget 2000"

[performance]
incremental_check_seconds = 2
full_scan_seconds = 60

[id]
suffix_alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
suffix_length = 8

[plugins]
verbs = ["ticket"]
picks = ["hcl"]

[review]
command = "claude -p"

[ledger]
shard = true
`

func TestParseFull(t *testing.T) {
	t.Parallel()
	c, err := Parse(strings.NewReader(full))
	if err != nil {
		t.Fatal(err)
	}
	if c.Workspace != "github.com/org/ds-index" || c.Prefix != "ds" || c.Spec != "1.0" {
		t.Errorf("top = %+v", c)
	}
	if len(c.Scan.Code) != 4 || len(c.Scan.Docs) != 4 || c.Scan.Exclude[2] != "public/**" || c.Scan.Generated[0] != "**/*.pb.go" || c.Scan.MaxLineChars != 2000 || c.Scan.MaxFileKB != 512 {
		t.Errorf("scan = %+v", c.Scan)
	}
	if c.Include.Mode != IncludeBuild || c.Include.MaxLines != 40 || c.Check.FuzzyThreshold != 0.8 || c.Check.Unacked != UnackedError || !strings.Contains(c.Check.Permalink, "{sha}") {
		t.Errorf("include/check = %+v %+v", c.Include, c.Check)
	}
	if !reflect.DeepEqual(c.Owners, map[string][]string{"@auth": {"khanakia"}, "@platform": {"someone", "another"}}) {
		t.Errorf("owners = %v", c.Owners)
	}
	if c.Env.Default != "prod" || len(c.Env.Known) != 3 || c.Resolve.Enabled || len(c.Resolve.Providers) != 2 {
		t.Errorf("env/resolve = %+v %+v", c.Env, c.Resolve)
	}
	if c.Run.Enabled || c.Run.Shell != "bash" || c.Run.Allow[0] != "runbooks/**" || c.Run.Env["staging"]["DATABASE_URL"] != "$STAGING_DATABASE_URL" || c.Run.Env["prod"]["DATABASE_URL"] != `$PROD "literal" url` {
		t.Errorf("run = %+v", c.Run)
	}
	if c.URL.TTL != "7d" || c.URL.RatePerMinute != 30 {
		t.Errorf("url = %+v", c.URL)
	}
	if c.Records.Source != RecordsFrontmatter || c.Records.Path != "records/" || c.Records.Table != "tasks" || c.Notify.Slack == "" || c.Notify.EscalateAfter != "7d" {
		t.Errorf("records/notify = %+v %+v", c.Records, c.Notify)
	}
	if c.Agents.MaxDefsPerRun != 20 || !c.Agents.MCP || c.Agents.SessionHook != "ds map --budget 2000" || c.ID.SuffixLength != 8 || c.ID.SuffixAlphabet != DefaultSuffixAlphabet {
		t.Errorf("agents/id = %+v %+v", c.Agents, c.ID)
	}
	if c.Review.Command != "claude -p" || !c.Ledger.Shard {
		t.Errorf("review/ledger = %+v %+v", c.Review, c.Ledger)
	}
	if len(c.Plugins.Verbs) != 1 || c.Plugins.Verbs[0] != "ticket" || c.Plugins.Picks[0] != "hcl" {
		t.Errorf("plugins = %+v", c.Plugins)
	}
	if len(IncludeModeValues) != 2 || len(UnackedValues) != 2 || len(RecordsValues) != 3 {
		t.Error("value lists")
	}
}

func TestParseMinimalAndDefaults(t *testing.T) {
	t.Parallel()
	c, err := Parse(strings.NewReader("[scan]\ndocs = [\"docs/**\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	d := Default()
	d.Scan.Docs = []string{"docs/**"}
	if !reflect.DeepEqual(c, d) {
		t.Errorf("defaults not applied:\n%+v\n%+v", c, d)
	}
	if err := Default().Validate(); !errors.Is(err, ErrNoScan) {
		t.Errorf("bare default must fail validation: %v", err)
	}
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		in      string
		wantErr error
	}{
		"unknown top key":     {"bogus = 1\n[scan]\ndocs=[\"d\"]\n", ErrUnknown},
		"unknown table key":   {"[scan]\ndocs=[\"d\"]\nnope = 1\n", ErrUnknown},
		"unknown nested key":  {"[scan]\ndocs=[\"d\"]\n[scan.limits]\nx = 1\n", ErrUnknown},
		"wrong type string":   {"[scan]\ndocs = \"not an array\"\n", ErrType},
		"wrong type array el": {"[scan]\ndocs = [1, 2]\n", ErrType},
		"wrong type int":      {"[scan]\ndocs=[\"d\"]\n[include]\nmax_lines = \"40\"\n", ErrType},
		"wrong type bool":     {"[scan]\ndocs=[\"d\"]\n[resolve]\nenabled = 1\n", ErrType},
		"wrong type float":    {"[scan]\ndocs=[\"d\"]\n[check]\nfuzzy_threshold = \"x\"\n", ErrType},
		"wrong type table":    {"include = 1\n[scan]\ndocs=[\"d\"]\n", ErrType},
		"owners not array":    {"[scan]\ndocs=[\"d\"]\n[owners]\n\"@a\" = \"one\"\n", ErrType},
		"owners not table":    {"owners = 1\n[scan]\ndocs=[\"d\"]\n", ErrType},
		"run env not table":   {"[scan]\ndocs=[\"d\"]\n[run]\nenv = 1\n", ErrType},
		"run env inner":       {"[scan]\ndocs=[\"d\"]\n[run.env]\nstaging = 1\n", ErrType},
		"run env value":       {"[scan]\ndocs=[\"d\"]\n[run.env.staging]\nX = 1\n", ErrType},
		// Bug 120: keys the spec names but nothing reads are refused, not
		// accepted and ignored.
		"sources not implemented":       {"[scan]\ndocs=[\"d\"]\n[sources.sql]\ndsn = \"x\"\n", ErrNotImplemented},
		"github_issues not implemented": {"[scan]\ndocs=[\"d\"]\n[notify]\ngithub_issues = true\n", ErrNotImplemented},
		// Bug 120: env.known holds every environment the config names.
		"run.env not in env.known": {"[scan]\ndocs=[\"d\"]\n[env]\nknown = [\"prod\"]\n[run.env.stagng]\nX = \"1\"\n", ErrValue},
		// Bug 121: [id] is validated when the config loads, not at mint.
		"suffix_length too short":   {"[scan]\ndocs=[\"d\"]\n[id]\nsuffix_length = 4\n", ErrValue},
		"suffix_length too long":    {"[scan]\ndocs=[\"d\"]\n[id]\nsuffix_length = 33\n", ErrValue},
		"suffix_alphabet too short": {"[scan]\ndocs=[\"d\"]\n[id]\nsuffix_alphabet = \"abc\"\n", ErrValue},
		"performance shape":         {"performance = 1\n[scan]\ndocs=[\"d\"]\n", ErrType},
		"plugins shape":             {"[scan]\ndocs=[\"d\"]\n[plugins]\nverbs = 1\n", ErrType},
		"review shape":              {"[scan]\ndocs=[\"d\"]\n[review]\ncommand = 1\n", ErrType},
		"ledger shape":              {"[scan]\ndocs=[\"d\"]\n[ledger]\nshard = \"yes\"\n", ErrType},
		"spec version":              {"spec = \"9.0\"\n[scan]\ndocs=[\"d\"]\n", ErrSpec},
		"bad prefix":                {"prefix = \"Bad Prefix\"\n[scan]\ndocs=[\"d\"]\n", ErrValue},
		"no scan":                   {"prefix = \"ds\"\n", ErrNoScan},
		"bad include mode":          {"[scan]\ndocs=[\"d\"]\n[include]\nmode = \"copy\"\n", ErrValue},
		"bad unacked":               {"[scan]\ndocs=[\"d\"]\n[check]\nunacked = \"ignore\"\n", ErrValue},
		"bad threshold":             {"[scan]\ndocs=[\"d\"]\n[check]\nfuzzy_threshold = 1.5\n", ErrValue},
		"bad records":               {"[scan]\ndocs=[\"d\"]\n[records]\nsource = \"Not Valid\"\n", ErrValue},
		"bad limit":                 {"[scan]\ndocs=[\"d\"]\n[include]\nmax_lines = 0\n", ErrValue},
		"env default unknown":       {"[scan]\ndocs=[\"d\"]\n[env]\ndefault = \"qa\"\nknown = [\"prod\"]\n", ErrValue},
		"syntax no equals":          {"[scan]\ndocs\n", ErrSyntax},
		"syntax array table":        {"[[scan]]\n", ErrSyntax},
		"syntax bad header":         {"[scan\n", ErrSyntax},
		"syntax empty key":          {"[scan]\n.docs = [\"d\"]\n", ErrSyntax},
		"syntax dup key":            {"[scan]\ndocs=[\"d\"]\ndocs=[\"e\"]\n", ErrSyntax},
		"syntax value table":        {"[scan]\ndocs=[\"d\"]\n[scan.docs]\nx=1\n", ErrSyntax},
		"syntax missing val":        {"[scan]\ndocs =\n", ErrSyntax},
		"syntax bad string":         {"[scan]\ndocs = [\"unterminated]\n", ErrSyntax},
		"syntax trailing":           {"prefix = \"ds\" extra\n", ErrSyntax},
		"syntax bad literal":        {"prefix = 'open\n", ErrSyntax},
		"syntax literal tail":       {"prefix = 'a' b\n", ErrSyntax},
		"syntax bad escape":         {"prefix = \"\\q\"\n", ErrSyntax},
		"syntax dangling esc":       {"prefix = \"abc\\", ErrSyntax},
		"syntax bad value":          {"prefix = nope\n", ErrSyntax},
		"syntax multi array":        {"[scan]\ndocs = [\"a\",\n", ErrSyntax},
		"syntax nested array":       {"[scan]\ndocs = [[\"a\"]]\n", ErrSyntax},
		"syntax quoted key":         {"\"unterminated = 1\n", ErrSyntax},
		"syntax header path":        {"[scan.]\n", ErrSyntax},
		"syntax dotted value":       {"prefix = \"ds\"\nprefix.x = 1\n", ErrSyntax},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(strings.NewReader(tc.in))
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestTOMLSubsetDetails(t *testing.T) {
	t.Parallel()
	doc, err := parseTOML("a = \"x # not comment\" # comment\nb = 'lit # kept'\n\"quoted key\" = 1\nc.d.e = true\n[t.\"u v\"]\nn = -3\nf = 1_000.5\nempty = []\nmixed = [\"a\", 'b', 3, ]\nesc = \"q\\\"\\\\\\n\\t\\r\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if doc["a"].s != "x # not comment" || doc["b"].s != "lit # kept" || doc["quoted key"].i != 1 {
		t.Errorf("strings/comments = %+v", doc)
	}
	if !doc["c"].tbl["d"].tbl["e"].b {
		t.Error("dotted key")
	}
	tu := doc["t"].tbl["u v"].tbl
	if tu["n"].i != -3 || tu["f"].f != 1000.5 || len(tu["empty"].arr) != 0 || len(tu["mixed"].arr) != 3 || tu["esc"].s != "q\"\\\n\t\r" {
		t.Errorf("table values = %+v", tu)
	}
	// float accepts an int.
	if f, err := (value{kind: kindInt, i: 2}).float(); err != nil || f != 2 {
		t.Error("float from int")
	}
	if _, err := (value{kind: kindBool}).float(); !errors.Is(err, ErrType) {
		t.Error("float from bool")
	}
	if !isIdent("ds_2") || isIdent("2ds") || isIdent("") || isIdent("Ds") {
		t.Error("isIdent")
	}
	if got := splitTopLevel(`"a,b", 'c,d', e`); len(got) != 3 {
		t.Errorf("splitTopLevel = %q", got)
	}
	if got := splitTopLevel(`"esc\",x", y`); len(got) != 2 {
		t.Errorf("splitTopLevel escapes = %q", got)
	}
	if got := stripComment(`"a\"#b" # c`); got != `"a\"#b" ` {
		t.Errorf("stripComment = %q", got)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("io") }

func TestParseReadError(t *testing.T) {
	t.Parallel()
	if _, err := Parse(errReader{}); err == nil {
		t.Error("read error must surface")
	}
}

// TestSnapshotMaxAge pins the one config key that carries a duration: it is
// validated where it is read, so a typo is refused at parse time rather than
// silently disabling the warning it was meant to turn on.
func TestSnapshotMaxAge(t *testing.T) {
	t.Parallel()
	// A parseable config needs something to scan; the key under test is the
	// only interesting part.
	scan := "[scan]\ndocs = [\"**/*.md\"]\n"
	c, err := Parse(strings.NewReader(scan + "[check]\nsnapshot_max_age = \"30d\"\n"))
	if err != nil || c.Check.SnapshotMaxAge != "30d" {
		t.Fatalf("= %q %v", c.Check.SnapshotMaxAge, err)
	}
	// Empty is the default and means never: a pinned check that also failed
	// with age would stop being reproducible.
	c, err = Parse(strings.NewReader(scan + "[check]\nsnapshot_max_age = \"\"\n"))
	if err != nil || c.Check.SnapshotMaxAge != "" {
		t.Errorf("empty = %q %v", c.Check.SnapshotMaxAge, err)
	}
	if def := Default(); def.Check.SnapshotMaxAge != "" {
		t.Errorf("the default must be off, got %q", def.Check.SnapshotMaxAge)
	}
	// A malformed duration and a non-string are both refused.
	for _, bad := range []string{`snapshot_max_age = "soon"`, `snapshot_max_age = "3y"`, "snapshot_max_age = 30"} {
		if _, err := Parse(strings.NewReader(scan + "[check]\n" + bad + "\n")); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// TestSnapshotNotifyConfig pins [notify.snapshot], including the two ways
// its class lists can be ambiguous. A notifier that silently disagrees with
// its configuration is worse than one that refuses to start.
func TestSnapshotNotifyConfig(t *testing.T) {
	t.Parallel()
	scan := "[scan]\ndocs = [\"**/*.md\"]\n"

	// Defaults: on, with the shipped tiers.
	def := Default().Notify.Snapshot
	if !def.Enabled || def.OnDeleted != TierImmediate || def.DigestAfter != DefaultDigestAfter {
		t.Errorf("defaults = %+v", def)
	}
	if len(def.Immediate) == 0 || len(def.Digest) == 0 {
		t.Errorf("default class lists = %+v", def)
	}

	// A full section.
	c, err := Parse(strings.NewReader(scan + `[notify.snapshot]
enabled = false
immediate = ["signature", "type"]
digest = ["body"]
digest_after = "7d"
on_deleted = "digest"
owner = "@platform"
`))
	if err != nil {
		t.Fatal(err)
	}
	s := c.Notify.Snapshot
	if s.Enabled || s.DigestAfter != "7d" || s.OnDeleted != TierDigest || s.Owner != "@platform" {
		t.Errorf("parsed = %+v", s)
	}
	if len(s.Immediate) != 2 || len(s.Digest) != 1 {
		t.Errorf("lists = %+v", s)
	}

	// A section that does not mention `enabled` is on: silence should be
	// chosen, not forgotten.
	c, err = Parse(strings.NewReader(scan + "[notify.snapshot]\ndigest_after = \"3d\"\n"))
	if err != nil || !c.Notify.Snapshot.Enabled {
		t.Errorf("implicit enabled = %+v %v", c.Notify.Snapshot, err)
	}

	// The ways it refuses to load.
	for name, body := range map[string]string{
		"unknown immediate class": "[notify.snapshot]\nimmediate = [\"nope\"]\n",
		"unknown digest class":    "[notify.snapshot]\ndigest = [\"nope\"]\n",
		"class in both lists":     "[notify.snapshot]\nimmediate = [\"body\"]\ndigest = [\"body\"]\n",
		"unknown tier":            "[notify.snapshot]\non_deleted = \"loud\"\n",
		"bad digest_after":        "[notify.snapshot]\ndigest_after = \"soon\"\n",
		"unknown key":             "[notify.snapshot]\nnope = 1\n",
		"wrong type":              "[notify.snapshot]\nenabled = \"yes\"\n",
	} {
		if _, err := Parse(strings.NewReader(scan + body)); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}

	// An empty digest_after is allowed and means the default.
	if _, err := Parse(strings.NewReader(scan + "[notify.snapshot]\ndigest_after = \"\"\n")); err != nil {
		t.Errorf("empty digest_after = %v", err)
	}
	// The tier vocabulary is the same everywhere.
	for _, tier := range TierValues {
		if !isTier(tier) {
			t.Errorf("%q must be a tier", tier)
		}
	}
	if isTier("loud") || isClass("nope") {
		t.Error("unknown values must be rejected")
	}
	if !isClass("signature") {
		t.Error("a real class must be accepted")
	}
}

// TestDurationKeys pins that every duration key refuses a value it cannot
// parse and names the key. Each consumer used to fall back to the default,
// so a typo loaded cleanly and ran with a setting nobody chose.
func TestDurationKeys(t *testing.T) {
	const scan = "[scan]\ncode = [\"**\"]\n"
	for _, tc := range []struct {
		section, key, good string
		bad                []string
	}{
		{"url", "ttl", "12h", []string{"soon", "7 days", "-1d", "7"}},
		{"notify", "escalate_after", "2w", []string{"weird", "1y", "d"}},
		{"check", "snapshot_max_age", "30d", []string{"soon", "30 days"}},
		{"run", "timeout", "300ms", []string{"nonsense", "0s", "-1s", "7d"}},
	} {
		key := tc.section + "." + tc.key
		for _, v := range tc.bad {
			_, err := Parse(strings.NewReader(scan + "[" + tc.section + "]\n" + tc.key + " = \"" + v + "\"\n"))
			if !errors.Is(err, ErrValue) || !strings.Contains(err.Error(), key+" \""+v+"\"") {
				t.Errorf("%s = %q must be refused naming the key, got %v", key, v, err)
			}
		}
		for _, v := range []string{tc.good, ""} {
			if _, err := Parse(strings.NewReader(scan + "[" + tc.section + "]\n" + tc.key + " = \"" + v + "\"\n")); err != nil {
				t.Errorf("%s = %q must load: %v", key, v, err)
			}
		}
	}
	// The defaults themselves must pass, or every config would be refused.
	if err := Default().validateDurations(); err != nil {
		t.Errorf("defaults = %v", err)
	}
	// A config built in code, not parsed, is held to the same rule.
	c := Default()
	c.Scan.Code = []string{"**"}
	c.Run.Timeout = "forever"
	if err := c.Validate(); !errors.Is(err, ErrValue) {
		t.Errorf("code-built bad timeout = %v", err)
	}
}

// TestValidateChecksConfigsBuiltInCode pins that every rule applies to a
// config that never went through the TOML reader — an organisation's
// defaults, a library caller. notify.snapshot and snapshot_max_age were
// checked only while parsing, so such a config ran with class lists a
// notifier disagreed with and an age limit that silently never fired.
func TestValidateChecksConfigsBuiltInCode(t *testing.T) {
	t.Parallel()
	base := func() Config {
		c := Default()
		c.Scan.Code = []string{"**"}
		return c
	}
	for name, mutate := range map[string]func(*Config){
		"unknown immediate class": func(c *Config) { c.Notify.Snapshot.Immediate = []string{"nope"} },
		"unknown digest class":    func(c *Config) { c.Notify.Snapshot.Digest = []string{"nope"} },
		"class in both lists": func(c *Config) {
			c.Notify.Snapshot.Immediate, c.Notify.Snapshot.Digest = []string{"body"}, []string{"body"}
		},
		"unknown tier":         func(c *Config) { c.Notify.Snapshot.OnDeleted = "loud" },
		"bad digest_after":     func(c *Config) { c.Notify.Snapshot.DigestAfter = "soon" },
		"bad snapshot_max_age": func(c *Config) { c.Check.SnapshotMaxAge = "soon" },
	} {
		c := base()
		mutate(&c)
		if err := c.Validate(); !errors.Is(err, ErrValue) {
			t.Errorf("%s: %v, want ErrValue", name, err)
		}
	}
	// An empty on_deleted means the default tier, as an empty duration
	// means the default duration.
	c := base()
	c.Notify.Snapshot.OnDeleted = ""
	if err := c.Validate(); err != nil {
		t.Errorf("empty on_deleted = %v", err)
	}
}

// TestSentenceMode pins [check] sentence: wording by default, position when
// a repository opts out, and nothing else.
func TestSentenceMode(t *testing.T) {
	t.Parallel()
	const scan = "[scan]\ncode = [\"**\"]\n"
	c, err := Parse(strings.NewReader(scan))
	if err != nil || c.Check.Sentence != SentenceWording {
		t.Errorf("default = %q %v", c.Check.Sentence, err)
	}
	c, err = Parse(strings.NewReader(scan + "[check]\nsentence = \"position\"\n"))
	if err != nil || c.Check.Sentence != SentencePosition {
		t.Errorf("position = %q %v", c.Check.Sentence, err)
	}
	if _, err := Parse(strings.NewReader(scan + "[check]\nsentence = \"loose\"\n")); !errors.Is(err, ErrValue) {
		t.Errorf("an unknown mode = %v", err)
	}
	if _, err := Parse(strings.NewReader(scan + "[check]\nsentence = 3\n")); err == nil {
		t.Error("a number is not a mode")
	}
	for _, v := range SentenceValues {
		if _, err := Parse(strings.NewReader(scan + "[check]\nsentence = \"" + v + "\"\n")); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
}

// TestUpdateMode pins [update] mode: auto by default, the three modes
// accepted, anything else refused, and an empty mode (a config built in
// code) left to the default.
func TestUpdateMode(t *testing.T) {
	t.Parallel()
	const scan = "[scan]\ncode = [\"**\"]\n"
	c, err := Parse(strings.NewReader(scan))
	if err != nil || c.Update.Mode != UpdateAuto {
		t.Errorf("default = %q %v", c.Update.Mode, err)
	}
	for _, v := range UpdateModeValues {
		c, err := Parse(strings.NewReader(scan + "[update]\nmode = \"" + v + "\"\n"))
		if err != nil || c.Update.Mode != v {
			t.Errorf("%s: %q %v", v, c.Update.Mode, err)
		}
	}
	if _, err := Parse(strings.NewReader(scan + "[update]\nmode = \"never\"\n")); !errors.Is(err, ErrValue) {
		t.Errorf("an unknown mode = %v", err)
	}
	if _, err := Parse(strings.NewReader(scan + "[update]\nauto = false\n")); !errors.Is(err, ErrUnknown) {
		t.Errorf("an unknown key = %v", err)
	}
	c.Update.Mode = ""
	if err := c.Validate(); err != nil {
		t.Errorf("empty mode: %v", err)
	}
}
