// Package cli is the `ds` command: every subcommand is a thin call into
// github.com/ubgo/docsync (docs/SPEC.md §37.5). It owns what the library
// refuses to: paths, files, git, printing, and exit codes.
//
// Embed it to ship a custom binary with extra extractors and verbs:
//
//	func main() {
//	    cli.Main(cli.WithName("pds"), cli.WithExtractor(mytier.Kotlin()), cli.WithVerb("ticket"))
//	}
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ubgo/docsync/block"

	"github.com/spf13/cobra"
	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ext/records/sqlite"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/ledger"
	"github.com/ubgo/docsync/records"
	"github.com/ubgo/docsync/workspace"
)

// DefaultName is the binary name when WithName is not given.
const DefaultName = "ds"

// Exit codes. Findings decide `check`'s code (§17); everything else is a
// usage or runtime failure.
const (
	ExitOK       = 0
	ExitFindings = 1
	ExitError    = 2
)

// Errors.
var (
	// ErrNotInitialised means no directory at or above where the command
	// started holds a config. Below one that does, the root is found
	// instead (App.locate), so this never sends someone to create a second
	// .ds inside an initialised repository.
	ErrNotInitialised = errors.New("no .ds/config.toml here or in any parent directory; run `init` first")
	// ErrNestedInit refuses `init` under an initialised repository unless
	// --force says a separate docsync root is wanted there.
	ErrNestedInit = errors.New("a parent directory already has .ds/config.toml")
	ErrExists     = errors.New(".ds/config.toml already exists; pass --force to overwrite")
	ErrUsage      = errors.New("usage")
)

// App is one configured invocation.
type App struct {
	name string
	// dir is the repository root every command works in: the nearest
	// directory at or above cwd that holds .ds/config.toml, found the way
	// git finds .git. cwd is where the command was started (or --dir), and
	// the directory a path the user types is relative to.
	dir string
	cwd string
	// gitVCS reports that vcs is the default git client for dir rather than
	// one the embedder injected, so --dir may point it somewhere else.
	gitVCS bool
	// dirFlag records that --dir named the directory, which then wins
	// over the workspace an LSP client names.
	dirFlag        bool
	stdin          io.Reader
	stdout, stderr io.Writer
	extractors     []extract.Extractor
	pickers        []docsync.Picker
	registry       *extract.Registry
	verbs          []string
	configDefaults func(*config.Config)
	vcs            VCS
	now            func() time.Time
	// fetchIndex makes system() refresh the workspace index first; `check`
	// sets it (§16 pass 0), other commands read the cached copy.
	fetchIndex bool
	// frozen makes system() resolve foreign blocks from the committed
	// snapshot rather than the index, so a check is reproducible (§21).
	frozen bool
	// urlCheck is set by `check --resolve`.
	urlCheck func(href string) check.URLResult
	// atFS and atCommit are set by `render --at`: system() scans that
	// commit's tree instead of the work tree and stamps permalinks with
	// that commit, so the page, its values, its line ranges and its links
	// all come from one commit (bug 60).
	atFS     fs.FS
	atCommit string
	// httpClient is what --resolve uses; tests inject a server's client.
	httpClient *http.Client
	// notifyStateImpl replaces where `ds notify` keeps its dedupe and
	// escalation memory; nil means .ds/notified.json.
	notifyStateImpl NotifyState
	// pluginLookPath finds ds-* plugin executables; nil means PATH.
	pluginLookPath func(name string) (string, error)
	resolveHook    check.Resolver
	storedHashes   map[string]string
	removed        map[string]string
	// indexFS and indexEntries are the synced workspace index, set by
	// workspaceOptions, so by-hash body reads can reach other repositories'
	// published blocks (§20.1).
	indexFS      fs.FS
	indexEntries []workspace.Entry
	// closers are handles a command opened (the sqlite record source);
	// Run closes them when the command returns.
	closers []io.Closer
}

// Option configures an App.
type Option func(*App)

// WithName sets the binary name shown in help and remedies.
func WithName(name string) Option { return func(a *App) { a.name = name } }

// WithDir sets the repository root; default is the working directory.
func WithDir(dir string) Option { return func(a *App) { a.dir = dir } }

// WithIO replaces stdin, stdout, and stderr.
func WithIO(in io.Reader, out, errw io.Writer) Option {
	return func(a *App) { a.stdin, a.stdout, a.stderr = in, out, errw }
}

// WithExtractor registers extra tiers ahead of the built-in ones.
func WithExtractor(es ...extract.Extractor) Option {
	return func(a *App) { a.extractors = append(a.extractors, es...) }
}

// WithPicker registers Go `pick=` schemes for a custom binary; process
// plugins (`ds-pick-<scheme>`) need no registration.
func WithPicker(ps ...docsync.Picker) Option {
	return func(a *App) { a.pickers = append(a.pickers, ps...) }
}

// WithRegistry replaces tier selection entirely (§37.3); WithExtractor then
// prepends to it. The escape hatch for a binary that wants no built-in tier.
func WithRegistry(r *extract.Registry) Option { return func(a *App) { a.registry = r } }

// WithVerb registers plugin verb names.
func WithVerb(names ...string) Option { return func(a *App) { a.verbs = append(a.verbs, names...) } }

// WithConfigDefaults adjusts the defaults of an organisation-wide binary:
// every repository's config is read on top of them, so a key its file does
// not set falls back to the organisation's value, and `init` writes them
// into the keys it writes. Before, config was read on top of the built-in
// defaults only, so everything the hook set outside init's few keys was
// silently lost — an agent cap of 7 ran as 20.
func WithConfigDefaults(f func(*config.Config)) Option { return func(a *App) { a.configDefaults = f } }

// WithVCS replaces the git adapter.
func WithVCS(v VCS) Option { return func(a *App) { a.vcs = v } }

// WithClock replaces time.Now.
func WithClock(f func() time.Time) Option { return func(a *App) { a.now = f } }

// WithHTTPClient replaces the client `check --resolve` uses for ds:url.
func WithHTTPClient(c *http.Client) Option { return func(a *App) { a.httpClient = c } }

// WithNotifyState replaces where `ds notify` keeps its dedupe and
// escalation memory. The default is `.ds/notified.json`, which a CI runner
// discards between runs; an organisation whose CI cannot cache that file
// supplies a store that survives instead.
func WithNotifyState(s NotifyState) Option { return func(a *App) { a.notifyStateImpl = s } }

// WithPluginLookup replaces PATH lookup for `ds-*` process plugins.
func WithPluginLookup(f func(name string) (string, error)) Option {
	return func(a *App) { a.pluginLookPath = f }
}

// Standard is the built-in set; it exists so a custom binary reads
// `cli.WithDefaults(cli.Standard())` and gains new built-ins by upgrading.
func Standard() Option { return func(*App) {} }

// WithDefaults applies a preset such as Standard.
func WithDefaults(o Option) Option { return o }

// Exit is what Main calls with the exit code. It is a variable so a test or
// an embedder that must not terminate the process can replace it; nothing
// else in the package exits.
var Exit = os.Exit

// Main runs with os.Args and exits with the resulting code.
func Main(opts ...Option) {
	Exit(Run(os.Args[1:], opts...))
}

// Run executes args and returns the exit code. It never calls os.Exit, so
// embedders and tests can drive it.
func Run(args []string, opts ...Option) int {
	a := &App{name: DefaultName, dir: ".", stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, now: func() time.Time { return time.Now().UTC() }}
	for _, o := range opts {
		o(a)
	}
	if a.vcs == nil {
		a.vcs, a.gitVCS = Git{Dir: a.dir}, true
	}
	root := a.root()
	if args == nil {
		// cobra reads os.Args when given nil; an embedder passing nil means
		// "no arguments", so make that explicit.
		args = []string{}
	}
	root.SetArgs(args)
	root.SetIn(a.stdin)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	defer func() {
		for _, c := range a.closers {
			_ = c.Close()
		}
	}()
	if err := root.Execute(); err != nil {
		var ec exitCode
		if errors.As(err, &ec) {
			return int(ec)
		}
		// Errors name the commands that fix them; a custom build names
		// itself there too (bug 126).
		fmt.Fprintf(a.stderr, "%s: %s\n", a.name, a.cmdText(err.Error()))
		return ExitError
	}
	return ExitOK
}

// exitCode carries a non-zero code from a command that already printed its
// output (check with findings) through cobra's error return.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// root builds the command tree.
func (a *App) root() *cobra.Command {
	var dir string
	root := &cobra.Command{
		Use:           a.name,
		Version:       stamped(buildInfo(debug.ReadBuildInfo), releaseVersion).Line(a.name),
		Short:         "keep docs bound to the code they describe",
		SilenceUsage:  true,
		SilenceErrors: true,
		// --dir runs every command as if started in that directory, as
		// `git -C` does; from there, as from the working directory, the
		// repository root is discovered upward. The editor and docs-site
		// integrations document --dir for a repository that is not their
		// working directory. `init` makes a root where it is started, so it
		// does not discover one.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			a.dirFlag = dir != ""
			return a.locate(dir, cmd.Name() != initCmdName)
		},
	}
	root.PersistentFlags().StringVar(&dir, flagDir, "", "run as if started in this directory (the repository root)")
	// The version line already names the program; cobra's default template
	// would print "ds version ds v…".
	root.SetVersionTemplate("{{.Version}}\n")
	root.AddCommand(
		a.initCmd(), a.doctorCmd(), a.defCmd(), a.scanCmd(), a.checkCmd(), a.ackCmd(), a.refreshCmd(), a.renderCmd(),
		a.mapCmd(), a.contextCmd(), a.factsCmd(), a.whyCmd(), a.findCmd(), a.readCmd(), a.locateCmd(), a.impactCmd(), a.statusCmd(),
		a.triageCmd(), a.auditCmd(), a.renameCmd(), a.graphCmd(), a.blameCmd(), a.reportCmd(), a.adoptCmd(), a.repairCmd(), a.versionCmd(), a.undoCmd(), a.pruneCmd(),
		a.syncCmd(), a.publishCmd(), a.mcpCmd(), a.notifyCmd(), a.lspCmd(), a.reviewCmd(), a.githubCmd(), a.exportCmd(),
	)
	return root
}

// cmdText rewrites the commands a message names from `ds` to this binary's
// name (cli.WithName), the same rule the library applies to remedies.
func (a *App) cmdText(s string) string { return check.CommandText(s, a.name) }

// flagDir is the global --dir.
const flagDir = "dir"

// ErrNoDir is a --dir that is not a directory.
var ErrNoDir = fmt.Errorf("%w: --%s is not a directory", ErrUsage, flagDir)

// initCmdName is the one command that does not discover a root.
const initCmdName = "init"

// locate sets where the command starts and the root it works in. start is
// --dir, taken from the directory the App was given when relative, or that
// directory itself. With discover set the root is the nearest directory at
// or above start holding a config, as git finds .git; without one, start
// is the root and `init` or the not-initialised error follow. The default
// git client follows the root; an injected VCS is the embedder's.
func (a *App) locate(dir string, discover bool) error {
	start := a.dir
	if dir != "" {
		start = dir
		if !filepath.IsAbs(dir) {
			start = filepath.Join(a.dir, dir)
		}
		if info, err := os.Stat(start); err != nil || !info.IsDir() {
			return fmt.Errorf("%w: %s", ErrNoDir, start)
		}
	}
	a.cwd, a.dir = start, start
	if discover {
		if root, ok := findRoot(start); ok {
			a.dir = root
		}
	}
	if a.gitVCS {
		a.vcs = Git{Dir: a.dir}
	}
	return nil
}

// findRoot is start when it is initialised, else its nearest initialised
// ancestor.
func findRoot(start string) (string, bool) {
	st := NewStore(start)
	if st.Exists() {
		return start, true
	}
	return st.ancestorRoot()
}

// ErrOutsideRepo refuses a path argument that leaves the repository.
var ErrOutsideRepo = fmt.Errorf("%w: path is outside the repository", ErrUsage)

// repoPath turns a path the user typed — relative to where they are, as git
// reads it — into the repository-relative, slash-separated path every
// command and every output uses. A path that resolves outside the root is
// refused rather than silently turned into something under it.
//
// Abs fails only when the working directory is gone; the path is then
// unresolvable, Rel fails or leaves the root, and it is refused like any
// path outside the repository.
func (a *App) repoPath(p string) (string, error) {
	root, _ := filepath.Abs(a.dir)
	full := filepath.FromSlash(p)
	// A rooted path is taken from the root, not from where the user is. On
	// Windows IsAbs is false for `\etc\hosts` (rooted, no drive letter), so
	// joining it onto the start directory turned a path outside the
	// repository into one inside it, which was then accepted.
	if !filepath.IsAbs(full) && !rooted(full) {
		full = filepath.Join(a.startDir(), full)
	}
	full, _ = filepath.Abs(full)
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrOutsideRepo, p)
	}
	return filepath.ToSlash(rel), nil
}

// rooted reports whether p starts at a root: a leading separator, which on
// Windows is either slash. filepath.IsAbs misses that case there, because a
// path needs a drive letter to be absolute.
func rooted(p string) bool {
	return p != "" && os.IsPathSeparator(p[0])
}

// repoTarget is repoPath for a def target, `<file>#<symbol>` or
// `<file>:<line>`: the file part is typed from where the user is, the rest
// is kept. A target of neither shape is passed on for Define to reject.
func (a *App) repoTarget(t string) (string, error) {
	if p, sym, ok := strings.Cut(t, "#"); ok && sym != "" {
		rp, err := a.repoPath(p)
		return rp + "#" + sym, err
	}
	if i := strings.LastIndex(t, ":"); i > 0 {
		// `path:line` or `path:start-end`; the library validates the numbers.
		// A range was left unresolved here, so `ds def x.txt:2-4` from a
		// subdirectory looked for x.txt at the repository root (bug 43).
		from, _, _ := strings.Cut(t[i+1:], "-")
		if n, err := strconv.Atoi(from); err == nil && n > 0 {
			rp, err := a.repoPath(t[:i])
			return rp + t[i:], err
		}
	}
	return t, nil
}

// isFile reports whether p, typed from where the user is, names a file.
func (a *App) isFile(p string) bool {
	full := filepath.FromSlash(p)
	if !filepath.IsAbs(full) {
		full = filepath.Join(a.startDir(), full)
	}
	info, err := os.Stat(full)
	return err == nil && !info.IsDir()
}

// startDir is where the command was started: cwd once located, else the
// directory the App was given.
func (a *App) startDir() string {
	if a.cwd != "" {
		return a.cwd
	}
	return a.dir
}

// orgDefaults is the base every config is read on top of: the built-in
// defaults with the embedder's hook applied. It deliberately leaves out the
// scan globs init writes, so a hand-written config that omits `exclude`
// does not suddenly inherit init's list.
func (a *App) orgDefaults() config.Config {
	c := config.Default()
	if a.configDefaults != nil {
		a.configDefaults(&c)
	}
	return c
}

// loadConfig reads a repository's config the one way every command does,
// on top of orgDefaults.
func (a *App) loadConfig(st *Store) (config.Config, error) {
	return st.LoadConfigOnto(a.orgDefaults())
}

// defaults returns the config `init` writes, after the embedder's hook.
func (a *App) defaults() config.Config {
	c := config.Default()
	c.Scan.Code = []string{"**"}
	c.Scan.Docs = []string{"docs/**", "README.md"}
	c.Scan.Exclude = []string{"**/testdata/**", "**/node_modules/**", "**/vendor/**", "dist/**", "public/**"}
	c.Scan.Generated = []string{"**/*.pb.go", "**/gen/**", "**/*_gen.go", "**/*_gen.ts"}
	if a.configDefaults != nil {
		a.configDefaults(&c)
	}
	return c
}

// loaded is everything a command needs: the library System, the store, and
// the ack log as read, so `ack` can append without a second read.
type loaded struct {
	sys  *docsync.System
	st   *Store
	acks ledger.Acks
	// prev and refs are the committed state system() already read, so a
	// command that needs them does not read the same files twice — and so
	// no command carries an error branch for a read that has just
	// succeeded.
	prev  ledger.Ledger
	refs  ledger.Refs
	cache *extractCache
	cfg   config.Config
}

// flush persists what a command changed in the derived stores: the
// extraction cache today. Commands call it before returning.
func (ld loaded) flush() error { return ld.st.SaveCache(ld.cache) }

// system loads the store and builds the library System for this tree.
// readLocal reads a `local=true` def's target on this machine, for check
// to tell a present file from an absent one (bug 85). A relative path is
// under the repository root and an absolute one is taken as written,
// because a local target is by nature a file outside git.
func (a *App) readLocal(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.dir, filepath.FromSlash(path))
	}
	return os.ReadFile(path)
}

func (a *App) system() (loaded, error) {
	st := NewStore(a.dir)
	cfg, err := a.loadConfig(st)
	if err != nil {
		return loaded{}, err
	}
	prev, refs, acks, err := st.LoadState()
	if err != nil {
		return loaded{}, err
	}
	commit, _ := a.vcs.Head()
	var fsys fs.FS = os.DirFS(a.dir)
	if a.atFS != nil {
		fsys, commit = a.atFS, a.atCommit
	}
	repo := a.repoName(cfg)
	wsOpts, warnings, err := a.workspaceOptions(cfg, repo, a.fetchIndex)
	if err != nil {
		return loaded{}, err
	}
	for _, w := range warnings {
		fmt.Fprintf(a.stderr, "warning: %s\n", w)
	}
	// sysRef is filled in once New returns; see the OldContent hook below.
	var sysRef *docsync.System
	opts := []docsync.Option{
		docsync.WithFS(fsys), docsync.WithConfig(cfg), docsync.WithRepo(repo), docsync.WithCommit(commit),
		docsync.WithPrevious(prev, refs), docsync.WithAcks(acks), docsync.WithClock(a.now),
	}
	if a.registry != nil {
		opts = append(opts, docsync.WithRegistry(a.registry))
	}
	// Placed after the workspaceOptions call above, which is what learns
	// where the index is and sets the fields this lookup reads. The same
	// lookup is the first source of a previous ledger row's body, so the
	// change table and the per-citation drift read one store.
	bodies := bodyLookup(st, a.indexFS, a.indexEntries)
	opts = append(opts,
		docsync.WithExtractor(a.extractors...), docsync.WithVerb(a.verbs...),
		docsync.WithCommitLookup(a.vcs.Exists),
		// The hook runs the old file through the scan pipeline, which needs
		// the System this call is building; it is only ever called during a
		// check, after New has returned, so it reaches the System through a
		// reference filled in below.
		docsync.WithOldContent(oldContent(bodies, a.vcs, prev, func(path string, src []byte) ([]block.Block, error) {
			return sysRef.ExtractFile(context.Background(), path, src)
		})),
		docsync.WithBodyAt(bodies),
	)
	opts = append(opts, wsOpts...)
	for _, p := range a.pickers {
		opts = append(opts, docsync.WithPicker(p))
	}
	opts = append(opts, a.pluginOptions(cfg)...)
	if len(a.removed) > 0 {
		opts = append(opts, docsync.WithRemoved(a.removed))
	}
	cache, err := st.LoadCache(cacheInputs(extract.Rule, cfg.Prefix, a.tiers(), cfg.Scan.MaxLineChars, buildStamp(os.Executable, os.Stat)), a.now)
	if err != nil {
		return loaded{}, err
	}
	// The extraction cache describes the work tree, keyed on what a file
	// looks like on disk; a commit view has no such metadata, so it is
	// neither read nor fed from one.
	if a.atFS == nil {
		opts = append(opts, docsync.WithExtractCache(cache))
	}
	if a.urlCheck != nil {
		opts = append(opts, docsync.WithURLCheck(a.urlCheck))
	}
	opts = append(opts, docsync.WithLocalReader(a.readLocal))
	// `ds:block … at=<sha>` renders the block as it was at that commit in
	// every render, not only under `render --at` (bug 66). It is lazy: no
	// git call happens unless a page holds such a snapshot.
	opts = append(opts, docsync.WithSnapshotBlock(a.snapshotAt(commit, func(path string, src []byte) ([]block.Block, error) {
		return sysRef.ExtractFile(context.Background(), path, src)
	})))
	if a.resolveHook != nil {
		opts = append(opts, docsync.WithResolver(a.resolveHook), docsync.WithStoredHashes(a.storedHashes))
	}
	switch {
	case cfg.Records.Source == config.RecordsFrontmatter && cfg.Records.Path != "":
		opts = append(opts, docsync.WithRecords(records.Frontmatter(os.DirFS(a.dir), strings.TrimSuffix(filepath.ToSlash(cfg.Records.Path), "/"))))
	case cfg.Records.Source == config.RecordsSQLite && cfg.Records.Path != "":
		src, closer, err := sqlite.Open(filepath.Join(a.dir, filepath.FromSlash(cfg.Records.Path)), cfg.Records.Table)
		if err != nil {
			return loaded{}, err
		}
		a.closers = append(a.closers, closer)
		opts = append(opts, docsync.WithRecords(src))
	case cfg.Records.Source == config.RecordsHTTP && cfg.Records.Path != "":
		client := a.httpClient
		if client == nil {
			client = &http.Client{Timeout: urlTimeout}
		}
		opts = append(opts, docsync.WithRecords(httpRecords(client, cfg.Records.Path)))
	}
	opts = append(opts, docsync.WithCommandName(a.name))
	sys, err := docsync.New(opts...)
	sysRef = sys
	if err != nil {
		return loaded{}, err
	}
	return loaded{sys: sys, st: st, acks: acks, prev: prev, refs: refs, cache: cache, cfg: cfg}, nil
}

// tiers names the extractor registry this App scans with, in precedence
// order, mirroring how the library assembles one: an explicitly registered
// set if there is one, otherwise the default tiers, with every WithExtractor
// addition in front. `ds doctor` prints it and the extraction cache is keyed
// on it, so both have to see what the scan will actually use.
//
// Invariant: it never mutates a.registry. Prepending to the caller's
// registry would double its entries on a second command in the same
// process.
func (a *App) tiers() []string {
	out := make([]string, 0, len(a.extractors))
	for i := len(a.extractors) - 1; i >= 0; i-- {
		out = append(out, a.extractors[i].Name())
	}
	reg := a.registry
	if reg == nil {
		reg = extract.Default()
	}
	return append(out, reg.Names()...)
}

// repoName is the last path element of the root, or the workspace-declared
// name once workspaces exist; it labels rows in a merged index. Abs fails
// only when the working directory is gone, in which case Base of the
// relative path is the best label there is.
func repoName(dir string, _ config.Config) string {
	abs, _ := filepath.Abs(dir)
	return filepath.Base(abs)
}

// printJSON writes v with the two-space indent every `--json` uses.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// table writes tab-separated rows aligned for a terminal.
func table(w io.Writer, rows [][]string) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	_ = tw.Flush()
}
