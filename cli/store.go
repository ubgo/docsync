package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/directive"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/scan"
)

// Store is the `.ds/` directory: config plus the three committed TSV files
// (§6). It is the default `Store` capability; a database or the workspace
// index can replace it later without touching commands.
type Store struct {
	Root string
	// Now stamps journal entries; nil means time.Now().UTC().
	Now func() time.Time
	// lock takes the .ds/ lock on an open file; nil means the platform's
	// lockFile. It is a field so a test can make locking fail.
	lock func(*os.File) error
}

// File names under .ds/.
const (
	DirName       = scan.StateDir
	ConfigFile    = "config.toml"
	LedgerFile    = ledger.LedgerFile
	RefsFile      = ledger.RefsFile
	AcksFile      = ledger.AcksFile
	CISnippet     = "ci-github.yml"
	AgentsFile    = "AGENTS.md"
	GitignoreFile = ".gitignore"
	// GitattributesFile gives .ds/ files the merge behaviour they need.
	GitattributesFile = ".gitattributes"
	filePerm          = 0o644
	dirPerm           = 0o755
	writeTempExt      = ".tmp"
)

// gitignoreBody excludes the parts of .ds/ that are machine-local. The
// ledger, refs, acks and block bodies are shared facts and are committed;
// these four are not, and committing them has consequences beyond noise:
//
//   - cache/ stores extracted block bodies, and journal.tsv stores the
//     source lines an edit replaced. Either can hold the value of a secret
//     block, which §12 says is never stored — the scanner now refuses to
//     cache a secret file at all, and this keeps the remaining copy out of
//     history, where rotating the credential could not remove it.
//   - index/ is a synced copy of another repository's published state, so
//     committing it duplicates someone else's data and goes stale.
//   - urls.json, runs.json, notified.json, hashes.json and metrics.json are
//     per-machine caches and counters that would conflict on every merge.
//
// The list is explicit rather than a blanket ignore with exceptions, so a
// new committed file is visible by default and a new local one has to be
// named here on purpose.
// dsself:def id=gitignorebody-jgek8ecm owner=@docsync stability=stable
const gitignoreBody = `# Machine-local docsync state; see SPEC section 6.
# The ledger, refs, acks and blocks/ ARE committed - do not add them here.
cache/
index/
journal.tsv
urls.json
runs.json
notified.json
hashes.json
metrics.json
lock
`

// gitattributesBody gives the ack log git's built-in union merge.
//
// acks.tsv is append-only: every writer adds rows at the end and none edits
// an old one (refresh used to, which is why it no longer does). So two
// branches that each record an ack both append at the same place, and git's
// default merge reports that as a conflict — in a team, on nearly every pair
// of pull requests that touch docs. A union merge keeps both sides' rows,
// which for an append-only log is exactly the right answer; Acks.Latest
// then picks the newest ack by time, not by where the merge put it.
//
// ledger.tsv, refs.tsv and foreign.tsv are deliberately NOT listed. They are
// rewritten rather than appended, and a union merge of a rewritten row keeps
// its old and new versions side by side.
const gitattributesBody = `# Merge behaviour for docsync state; see SPEC section 6.
# acks.tsv is append-only, so a union merge keeps both branches' acks.
# Do not add ledger.tsv, refs.tsv or foreign.tsv: they are rewritten.
acks.tsv merge=union
`

// conflictRemedy says how to finish a merge that left conflict markers in a
// .ds/ file. The advice differs because the files do: one is regenerated
// from source, one is append-only history, and one holds baselines nothing
// else can recreate — for that one, taking a side is the wrong answer.
var conflictRemedy = map[string]string{
	LedgerFile:         "keep either side, then run `ds scan`, which rewrites it from the source",
	RefsFile:           "keep every row from both sides, then run `ds scan`; dropping a side loses the first-seen hashes only it recorded, and the next scan would baseline those citations at today's hash",
	AcksFile:           "keep every row from both sides, since the log is append-only, and add `acks.tsv merge=union` to .ds/.gitattributes so it does not recur (ds doctor checks for it)",
	ledger.ForeignFile: "keep either side, then run `ds sync`, which rewrites it",
}

// decodeError labels a failure to read a .ds/ file with the file's name
// and, when a merge was left unfinished, how to finish it. kind is the file
// whose rules apply: a ledger shard follows the ledger's.
func decodeError(name, kind string, err error) error {
	if errors.Is(err, ledger.ErrConflict) {
		return fmt.Errorf("%s: %w; %s", name, err, conflictRemedy[kind])
	}
	return fmt.Errorf("%s: %w", name, err)
}

// NewStore points at dir/.ds.
func NewStore(dir string) *Store { return &Store{Root: dir} }

func (s *Store) path(name string) string { return filepath.Join(s.Root, DirName, name) }

// ancestorRoot finds the nearest parent directory of s.Root holding a
// config, stopping at the filesystem root. Abs fails only for a relative
// root when the working directory is gone; dir is then "", the walk checks
// "." — that same unreachable directory, which holds no config — once, and
// reports no ancestor rather than a guess.
func (s *Store) ancestorRoot() (string, bool) {
	dir, _ := filepath.Abs(s.Root)
	for parent := filepath.Dir(dir); parent != dir; dir, parent = parent, filepath.Dir(parent) {
		if NewStore(parent).Exists() {
			return parent, true
		}
	}
	return "", false
}

// Exists reports whether the config file is present.
func (s *Store) Exists() bool {
	_, err := os.Stat(s.path(ConfigFile))
	return err == nil
}

// LoadConfig parses .ds/config.toml on top of the built-in defaults.
func (s *Store) LoadConfig() (config.Config, error) { return s.LoadConfigOnto(config.Default()) }

// LoadConfigOnto parses .ds/config.toml on top of base: what the file sets
// wins, and everything else keeps base's value (config.ParseOnto).
func (s *Store) LoadConfigOnto(base config.Config) (config.Config, error) {
	raw, err := os.ReadFile(s.path(ConfigFile))
	if errors.Is(err, fs.ErrNotExist) {
		return config.Config{}, ErrNotInitialised
	}
	if err != nil {
		return config.Config{}, err
	}
	return config.ParseOnto(base, bytes.NewReader(raw))
}

// LedgerDir holds sharded ledgers when [ledger] shard = true.
const LedgerDir = "ledger"

// LoadState reads the ledger, refs, and acks; a missing file is an empty
// value, a malformed one is an error. A sharded ledger directory is read
// when present and takes precedence over a single file.
func (s *Store) LoadState() (ledger.Ledger, ledger.Refs, ledger.Acks, error) {
	var l ledger.Ledger
	var r ledger.Refs
	var a ledger.Acks
	if shards, err := s.loadShards(); err != nil {
		return l, r, a, err
	} else if shards != nil {
		l = ledger.Unshard(shards)
	} else if raw, ok, err := s.read(LedgerFile); err != nil {
		return l, r, a, err
	} else if ok {
		if l, err = ledger.DecodeLedger(bytes.NewReader(raw)); err != nil {
			return l, r, a, decodeError(LedgerFile, LedgerFile, err)
		}
	}
	if raw, ok, err := s.read(RefsFile); err != nil {
		return l, r, a, err
	} else if ok {
		if r, err = ledger.DecodeRefs(bytes.NewReader(raw)); err != nil {
			return l, r, a, decodeError(RefsFile, RefsFile, err)
		}
	}
	if raw, ok, err := s.read(AcksFile); err != nil {
		return l, r, a, err
	} else if ok {
		if a, err = ledger.DecodeAcks(bytes.NewReader(raw)); err != nil {
			return l, r, a, decodeError(AcksFile, AcksFile, err)
		}
	}
	return l, r, a, nil
}

func (s *Store) read(name string) ([]byte, bool, error) {
	raw, err := readFile(s.path(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

// ErrNotDir is a path docsync keeps a directory at that holds something else.
var ErrNotDir = errors.New("exists but is not a directory")

// listDir lists dir. A directory that does not exist is exists=false and no
// error, which callers read as "nothing stored yet"; anything else at that
// path is ErrNotDir.
//
// What the path is gets asked before it is listed, because the listing's own
// error differs by platform: reading a file as a directory is ENOTDIR on
// Unix and "path not found" on Windows, and the second satisfies
// fs.ErrNotExist. Treating that as absent read a corrupted store as an empty
// one, which for the body store means every body is dead, and for the ledger
// shards means no previous state at all.
func listDir(dir string) (entries []os.DirEntry, exists bool, err error) {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		return nil, true, fmt.Errorf("%s: %w", dir, ErrNotDir)
	}
	entries, err = os.ReadDir(dir)
	return entries, true, err
}

// loadShards reads .ds/ledger/*.tsv; nil when the directory is absent.
func (s *Store) loadShards() (map[string]ledger.Ledger, error) {
	entries, exists, err := listDir(s.path(LedgerDir))
	if err != nil || !exists {
		return nil, err
	}
	shards := map[string]ledger.Ledger{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tsv") {
			continue
		}
		raw, err := readFile(filepath.Join(s.path(LedgerDir), e.Name()))
		if err != nil {
			return nil, err
		}
		l, err := ledger.DecodeLedger(bytes.NewReader(raw))
		if err != nil {
			return nil, decodeError(LedgerDir+"/"+e.Name(), LedgerFile, err)
		}
		shards[strings.TrimSuffix(e.Name(), ".tsv")] = l
	}
	return shards, nil
}

// SaveLedger writes ledger and refs atomically. With shard set the ledger
// goes to .ds/ledger/<top-level dir>.tsv, one file per directory, and the
// single-file ledger is removed so the two never disagree.
func (s *Store) SaveLedger(l ledger.Ledger, r ledger.Refs) error {
	return s.SaveLedgerSharded(l, r, false)
}

// SaveLedgerSharded is SaveLedger with the layout chosen by shard.
func (s *Store) SaveLedgerSharded(l ledger.Ledger, r ledger.Refs, shard bool) error {
	if shard {
		if err := os.MkdirAll(s.path(LedgerDir), dirPerm); err != nil {
			return err
		}
		old, err := os.ReadDir(s.path(LedgerDir))
		if err != nil {
			return err
		}
		for _, e := range old {
			_ = os.Remove(filepath.Join(s.path(LedgerDir), e.Name()))
		}
		for name, sh := range l.Shard() {
			if err := s.Write(filepath.Join(LedgerDir, name+".tsv"), sh.Bytes()); err != nil {
				return err
			}
		}
		if len(l.Rows) == 0 {
			if err := s.Write(filepath.Join(LedgerDir, ledger.RootShard+".tsv"), l.Bytes()); err != nil {
				return err
			}
		}
		_ = os.Remove(s.path(LedgerFile))
	} else {
		if err := s.Write(LedgerFile, l.Bytes()); err != nil {
			return err
		}
		_ = os.RemoveAll(s.path(LedgerDir))
	}
	return s.Write(RefsFile, r.Bytes())
}

// AppendAcks adds rows to the ack log on disk, under the .ds/ lock: it
// re-reads the log as it is now, appends the rows it does not already hold,
// and writes it back. It is the only way the log is written.
//
// Why re-read: an ack command loads the log when it starts and used to write
// that copy back with its own rows appended, so two acks recorded at once —
// an agent's parallel tool calls, a triage group alongside a manual ack —
// each wrote a log without the other's row. Ten concurrent acks recorded
// one, and every command reported success. Rows already on disk are skipped
// by their encoded form, so a caller may pass the whole log it holds.
func (s *Store) AppendAcks(rows []ledger.Ack) error {
	return s.withLock(func() error {
		_, _, disk, err := s.LoadState()
		if err != nil {
			return err
		}
		if disk.Header.Kind == "" {
			disk.Header = ledger.Header{Kind: ledger.KindAcks, Format: ledger.Format}
		}
		have := map[string]bool{}
		for _, r := range disk.Rows {
			have[ackKey(r)] = true
		}
		for _, r := range rows {
			if k := ackKey(r); !have[k] {
				have[k] = true
				disk.Rows = append(disk.Rows, r)
			}
		}
		return s.writeLocked(AcksFile, disk.Bytes())
	})
}

// ackKey is an ack row's encoded form, which is what makes two rows the same
// row: every field, to the second.
func ackKey(r ledger.Ack) string {
	return string(ledger.Acks{Rows: []ledger.Ack{r}}.Bytes())
}

// LockFile is the machine-local file .ds/ writers lock (see withLock).
const LockFile = "lock"

// withLock runs fn holding the exclusive .ds/ lock, so read-modify-write
// sequences on the append-only files cannot interleave between processes.
// The lock is the kernel's, released when the process exits, so a crashed
// command cannot leave .ds/ locked.
func (s *Store) withLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Join(s.Root, DirName), dirPerm); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path(LockFile), os.O_CREATE|os.O_RDWR, filePerm)
	if err != nil {
		return err
	}
	// Close and unlock errors are dropped on purpose: the lock file holds no
	// data, so there is no write to lose, and closing the descriptor releases
	// the lock even if the explicit unlock failed.
	defer f.Close()
	lock := s.lock
	if lock == nil {
		lock = lockFile
	}
	if err := lock(f); err != nil {
		if lockUnsupported(err) {
			// Some network filesystems refuse locks outright. Failing every
			// write there would make the tool unusable on a network home
			// directory, so the write goes ahead unlocked — what every
			// write did before the lock existed.
			return fn()
		}
		return err
	}
	defer unlockFile(f)
	return fn()
}

// Write stores a file under .ds/ atomically — temp file then rename, so a
// crash mid-write never leaves a truncated ledger for the next check — and
// under the .ds/ lock, so two commands writing the same file cannot share
// its temp path: without the lock one rename failed, and interleaved writes
// to the shared temp file could rename a mixture of both into place.
func (s *Store) Write(name string, data []byte) error {
	return s.withLock(func() error { return s.writeLocked(name, data) })
}

// writeLocked is Write for a caller already holding the lock. flock is per
// open file, so taking it again from inside withLock would wait on itself.
func (s *Store) writeLocked(name string, data []byte) error {
	final := s.path(name)
	tmp := final + writeTempExt
	if err := os.WriteFile(tmp, data, filePerm); err != nil {
		return err
	}
	return retrySharing(func() error { return os.Rename(tmp, final) })
}

// readFile is os.ReadFile for a file under .ds/ that another ds command may
// be replacing at this instant (see retrySharing).
func readFile(p string) (raw []byte, err error) {
	err = retrySharing(func() (e error) { raw, e = os.ReadFile(p); return e })
	return raw, err
}

// ApplyEdit applies a library Edit to its file in the tree: the only source
// write the tool makes (§33 safety), and only from `def`. The file is read
// again here rather than trusting an earlier read, so an edit computed
// against stale content is refused by Edit.Apply instead of landing on the
// wrong line.
func (s *Store) ApplyEdit(e docsync.Edit) error {
	p := filepath.Join(s.Root, filepath.FromSlash(e.File))
	info, err := os.Stat(p)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	out, err := e.Apply(src)
	if err != nil {
		return err
	}
	return os.WriteFile(p, out, info.Mode().Perm())
}

// ConfigTOML renders a config as the TOML `init` writes. Only the keys a
// new repo needs appear; the rest fall back to defaults and are documented
// in the spec.
func ConfigTOML(c config.Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "spec = %q\nprefix = %q\n\n[scan]\ncode = %s\ndocs = %s\nexclude = %s\ngenerated = %s\n\n", c.Spec, c.Prefix, tomlList(c.Scan.Code), tomlList(c.Scan.Docs), tomlList(c.Scan.Exclude), tomlList(c.Scan.Generated))
	fmt.Fprintf(&b, "[include]\nmode = %q\nmax_lines = %d\n\n[check]\nfuzzy_threshold = %g\nunacked = %q\n", c.Include.Mode, c.Include.MaxLines, c.Check.FuzzyThreshold, c.Check.Unacked)
	if c.Check.Permalink != "" {
		fmt.Fprintf(&b, "permalink = %q\n", c.Check.Permalink)
	}
	fmt.Fprintf(&b, "\n[env]\ndefault = %q\n", c.Env.Default)
	return b.String()
}

func tomlList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = fmt.Sprintf("%q", s)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// ciSnippet is the GitHub Actions job `init` writes beside the config (§24).
// It is a file to copy, not an edit to .github/, because CI layout is the
// team's decision.
//
// It triggers manually, with the automatic triggers kept in its header to
// paste back. The version it replaced ran on every push and pull request, which
// on a private repository bills minutes by default and fails for billing
// rather than for code once they run out -- a red check everyone learns to
// ignore. A tool handing out a default should hand out the safe one.
const ciSnippet = `# Copy into .github/workflows/docsync.yml
#
# Triggers: manual only, by default. GitHub Actions minutes are billed on a
# private repository, and a workflow that goes red for billing rather than for
# the code teaches everyone to ignore it. Run ds check locally as the real
# gate, and start this from the Actions tab when you want CI to agree.
# On a public repository, or once budget is there, replace on: below with:
#
#   on:
#     pull_request:
#     push:
#       branches: [main]
name: docsync
on:
  workflow_dispatch:
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }
      - uses: actions/setup-go@v5
        with: { go-version: "1.26" }
      - run: go install github.com/ubgo/docsync/cli/cmd/ds@latest
      - run: ds check --json > docsync.json || (cat docsync.json; exit 1)
`

// agentsFragment is section 25 of the spec as a file an agent config can
// include (§26.10).
const agentsFragment = `# docsync rules for an AI writer and reviewer

The tool knows which sentence depends on which block and whether the block changed. Read only what ` + "`check`" + ` lists; acks are recorded against the hash.

Writing:
1. Never cite a path and line. ` + "`ds def <file>#<symbol>`" + ` for the block, then ` + "`ds:block?id=…`" + `.
2. Never paste code. Write a ` + "`ds:block`" + ` directive; the build renders it.
3. Never type a fact. Run ` + "`ds facts`" + `; cite with ` + "`ds:cfg`" + ` if it exists; otherwise define once with an inline ` + "`ds:def`" + `, then cite.
4. External links go through ` + "`ds:url`" + `. Behaviour claims cite the test with ` + "`assert=true`" + ` when one exists.
5. Add ` + "`covers`" + ` for every id a new page introduces.
6. Start a session with ` + "`ds map`" + `. When editing a page, start from ` + "`ds context <doc> --budget N --since ack`" + `; nothing outside it needs rereading. Use ` + "`find`, `read`, `locate`" + ` instead of grep and file reads.
7. Run ` + "`ds check`" + ` before declaring done. Run ` + "`ds impact`" + ` before proposing a code change that touches defined blocks.
8. After a code change, ` + "`ds check --json`" + ` is the complete work list.
9. Before deleting or renaming code, ` + "`ds why <id>`" + `, and handle dependents in the same change.

Reviewing:
1. ` + "`ds check --json`" + ` is the whole list.
2. For each ` + "`unacked`" + `, read the sentence, the class, and the diff. Still true: ` + "`ack --note`" + `. Not true: edit, then ` + "`ack --note`" + `. Never ack a page wholesale.
3. For each ` + "`broken`" + `, decide from the diff whether the block was deleted or moved without its def; re-add the def or rewrite and remove the reference.
4. Produce the patch and stop; a person or an explicitly delegated agent records acks (` + "`ack --agent --delegated-by <human>`" + `).
5. Leave anything needing a human unacked and say why.
`

// agentRules is agentsFragment for a repository's directive prefix. The
// rules tell an agent what to write; in a repository that changed its prefix
// they said `ds:block`, and an agent following them wrote directives the
// scanner ignores. Commands (`ds def`) keep the binary's name.
func agentRules(prefix string) string {
	return strings.ReplaceAll(agentsFragment, "`"+directive.DefaultPrefix+directive.Separator, "`"+prefix+directive.Separator)
}

// gitignoreRow reports whether .ds/.gitignore covers every machine-local
// path. See linesRow for how a missing or stale file is reported.
func (s *Store) gitignoreRow() []string {
	return s.linesRow("gitignore", GitignoreFile, gitignoreBody, "machine-local state excluded", "that state would be committed")
}

// gitattributesRow reports whether .ds/.gitattributes gives the ack log the
// union merge it needs. See linesRow.
func (s *Store) gitattributesRow() []string {
	return s.linesRow("gitattributes", GitattributesFile, gitattributesBody, "acks.tsv merges without conflicts", "two branches that each record an ack will conflict when merged")
}

// linesRow reports whether a file under .ds/ holds every non-comment line
// of the body ds init writes for it. A missing file and a stale one —
// written before a line was added to the body — fail the same way, so both
// are reported as the lines that are absent, and the remedy is to add
// exactly those. It never suggests `init --force`, which would overwrite
// the config and the ledgers to fix a one-line omission.
func (s *Store) linesRow(label, name, body, ok, consequence string) []string {
	raw, _ := os.ReadFile(s.path(name))
	have := map[string]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var missing []string
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") || have[l] {
			continue
		}
		missing = append(missing, l)
	}
	if len(missing) == 0 {
		return []string{label, doctorOK, ok}
	}
	return []string{label, doctorWarn, fmt.Sprintf("%s/%s does not cover %s; %s. Add those lines to it", DirName, name, strings.Join(missing, " "), consequence)}
}
