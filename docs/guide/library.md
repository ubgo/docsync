# Using docsync as a Go library

This page shows how to embed docsync in a Go program: scanning a tree, checking it, proposing new definitions, and building your own `ds` binary. It is for Go developers who want docsync's checks inside their own tool, test suite, or platform instead of (or alongside) the `ds` command.

## Install

The root module imports only the Go standard library. The heavier tiers live in their own modules, so you pull in a parser only when you want it.

```sh
go get github.com/ubgo/docsync@latest                  # the library: stdlib only
go get github.com/ubgo/docsync/ext/structured@latest   # YAML, TOML and HCL by parsed extent
go get github.com/ubgo/docsync/ext/treesitter@latest   # Go, TypeScript, TSX, JavaScript, Python, SQL (cgo, needs a C compiler)
go get github.com/ubgo/docsync/cli@latest              # only if you want to build your own ds binary
```

Each program on this page was compiled and run against the published modules (`github.com/ubgo/docsync v0.1.1`, `ext/structured v0.1.0`, `ext/treesitter v0.1.0`, `cli v0.1.3`), and the page's own test builds them against the current source.

## The contract: the library never touches the world

A `docsync.System` is built from values you hand it, and every method returns values. The library never writes a file, never reads the environment or `$HOME`, and never opens the network. That has three practical consequences:

- **You read the files.** The config, ledger, refs, and ack log are parsed by you (`config.Parse`, `ledger.DecodeLedger`, `ledger.DecodeRefs`, `ledger.DecodeAcks`) and passed in with options. The tree itself is an `fs.FS`, usually `os.DirFS(repo)`.
- **You apply writes.** Operations that would change a source file, such as `Define`, return a `docsync.Edit`. Your program decides whether to apply it, with `Edit.Apply`, and writes the result itself.
- **You supply anything that reaches outside.** Git history, secret providers, URL checks, test results, and record sources come in as functional options that take a function or an interface. Without them the library degrades the way the CLI does offline: a change it cannot describe is classed `unknown`, a URL it cannot check is `unverifiable`.

The CLI is one consumer of this contract: `cli/cli.go` builds its `System` from the `.ds/` files and git, and every `ds` command is a thin call into a method on it.

## Check a repository

The examples use a small repository with one Go function, `SaveSession`, defined and cited from a page:

```go file=internal/store/session.go
package store

import "time"

const SweepInterval = time.Hour

// ds:def id=savesession-73km8a3x owner=@auth
func SaveSession(id string) error {
	return nil
}
```

```markdown file=docs/sessions.md
# Sessions

Every write goes through [`SaveSession`](ds:block?id=savesession-73km8a3x), which never fails.
```

<!-- doctest
git init -q -b main .
ds init
ds scan
git add -A
git commit -qm init
-->

Then the function body changes, and nobody touches the page:

```go file=internal/store/session.go
package store

import "time"

const SweepInterval = time.Hour

// ds:def id=savesession-73km8a3x owner=@auth
func SaveSession(id string) error {
	if id == "" { return errEmpty }
	return nil
}

var errEmpty = error(nil)
```

This program runs the same check as `ds check` over a repository that `ds init` and `ds scan` have set up, and prints one line per finding. It lives in its own module next to the repository, in `../prog/dscheck`.

<!-- doctest
mkdir -p ../prog
cd ../prog
printf 'module example.com/dsdemo\n\ngo 1.26\n' > go.mod
go mod edit -require=github.com/ubgo/docsync@v0.0.0 -replace=github.com/ubgo/docsync=$DOCSYNC_ROOT
go mod edit -require=github.com/ubgo/docsync/ext/structured@v0.0.0 -replace=github.com/ubgo/docsync/ext/structured=$DOCSYNC_ROOT/ext/structured
go mod edit -require=github.com/ubgo/docsync/ext/treesitter@v0.0.0 -replace=github.com/ubgo/docsync/ext/treesitter=$DOCSYNC_ROOT/ext/treesitter
go mod edit -require=github.com/ubgo/docsync/cli@v0.0.0 -replace=github.com/ubgo/docsync/cli=$DOCSYNC_ROOT/cli
export GOWORK=off
export GOFLAGS=-mod=mod
cd ../repo
-->

```go file=../prog/dscheck/main.go
// Command dscheck runs docsync's check over a repository that `ds init`
// set up, and prints one line per finding.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ext/structured"
	"github.com/ubgo/docsync/ext/treesitter"
	"github.com/ubgo/docsync/ledger"
)

func main() {
	repo := os.Args[1]
	state := filepath.Join(repo, ".ds")

	// The library reads nothing by itself: the caller opens the files and
	// hands over parsed values.
	cfg := must(parse(state, "config.toml", config.Parse))
	prev := must(parse(state, "ledger.tsv", ledger.DecodeLedger))
	refs := must(parse(state, "refs.tsv", ledger.DecodeRefs))
	acks := must(parse(state, "acks.tsv", ledger.DecodeAcks))

	sys, err := docsync.New(
		docsync.WithFS(os.DirFS(repo)),
		docsync.WithConfig(cfg),
		docsync.WithPrevious(prev, refs),
		docsync.WithAcks(acks),
		// The same tiers the standard ds binary carries, so hashes match the
		// ones it recorded in the ledger.
		docsync.WithExtractor(structured.All()...),
		docsync.WithExtractor(treesitter.All()...),
		docsync.WithPicker(structured.HCLPicker()),
		// Bodies of earlier hashes, for diffs and change classes. Without
		// this hook a change is still reported, with class "unknown".
		docsync.WithBodyAt(func(hash string) (string, bool) {
			b, err := os.ReadFile(filepath.Join(state, "blocks", hash))
			return string(b), err == nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	rep, err := sys.Check(context.Background(), docsync.CheckOptions{})
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range rep.Findings {
		fmt.Printf("%s:%d\t%s\t%s\t%s\n", f.Doc, f.Line, f.Severity, f.State, f.Message)
		if f.Remedy.IfStillTrue != "" {
			fmt.Printf("\tstill true: %s\n", f.Remedy.IfStillTrue)
		}
	}
	os.Exit(rep.ExitCode)
}

func parse[T any](dir, name string, decode func(io.Reader) (T, error)) (T, error) {
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		var zero T
		return zero, err
	}
	defer f.Close()
	return decode(f)
}

func must[T any](v T, err error) T {
	if err != nil {
		log.Fatal(err)
	}
	return v
}
```

`ds check` and this program agree about the edited function:

```console
$ ds check
docs/sessions.md
  3	error    unacked            savesession-73km8a3x changed (moved, body) since this sentence was first cited
      still true: ds ack savesession-73km8a3x --doc docs/sessions.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/sessions.md:3, then ack
1 error
$ cd ../prog
$ go build -o bin/dscheck ./dscheck && bin/dscheck ../repo; echo "exit=$?"
docs/sessions.md:3	error	unacked	savesession-73km8a3x changed (moved, body) since this sentence was first cited
	still true: ds ack savesession-73km8a3x --doc docs/sessions.md --line 3 --note '…'
exit=1
```

`rep` is a `docsync.Report`. Its exported fields carry the same JSON tags as `ds check --json` (`json_format`, `summary`, `states`, `findings`, `exit_code`), so `json.Marshal(rep)` gives you the machine contract, and `rep.Findings` is a slice of `check.Finding` with `State`, `Severity`, `Doc`, `Line`, `ID`, `Classes`, `Diff`, and a `Remedy` holding either `IfStillTrue` and `IfNot` (for `unacked`) or `Fix` (for everything else). `rep.Scan` holds the scan the report came from, which `Graph`, `Blame`, `Report`, and `ContextFor` take so they do not rescan.

### Register the same tiers as the binary that wrote the ledger

A block's hash depends on which extractor bound it. The standard `ds` binary carries the structured and tree-sitter tiers; a `System` built with no `WithExtractor` uses only the built-in, grammarless tiers. Pointed at a ledger written by `ds`, the two disagree about unchanged code. Here the edit is put back to the committed version, and `dscheck-stdlib` is the program above with the three lines naming `structured` and `treesitter` (and their imports) removed. The same file at the same commit reports drift:

<!-- doctest
cp ../repo/internal/store/session.go session.edited
git -C ../repo show HEAD:internal/store/session.go > ../repo/internal/store/session.go
mkdir -p dscheck-stdlib
grep -v -e structured -e treesitter dscheck/main.go > dscheck-stdlib/main.go
go build -o bin/dscheck-stdlib ./dscheck-stdlib
-->

```console
$ bin/dscheck-stdlib ../repo     # built without the structured and tree-sitter tiers, tree unchanged
docs/sessions.md:3	error	unacked	savesession-73km8a3x changed (body) since this sentence was first cited
	still true: ds ack savesession-73km8a3x --doc docs/sessions.md --line 3 --note '…'
```

With the tiers registered, the unchanged tree is clean:

```console
$ bin/dscheck ../repo     # built with them, tree unchanged
docs/sessions.md:3	none	ok	up to date
```

So if your program shares a ledger with `ds`, register what `cli/cmd/ds` registers: `structured.All()`, `treesitter.All()`, and `structured.HCLPicker()`. If your program owns its ledger end to end, any consistent set works.

## Propose a definition, apply it yourself

`Define` finds a symbol or line and returns the id it would use plus the `Edit` that inserts the directive. Nothing is written until you apply the edit.

<!-- doctest
cp session.edited ../repo/internal/store/session.go
-->

```go file=../prog/define/main.go
// Command define mints an id for path#Symbol and prints the edit the library
// proposes; with -w it applies the edit itself.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/ext/treesitter"
)

func main() {
	write := flag.Bool("w", false, "apply the edit")
	flag.Parse()
	repo, target := flag.Arg(0), flag.Arg(1)

	sys, err := docsync.New(
		docsync.WithFS(os.DirFS(repo)),
		docsync.WithExtractor(treesitter.All()...),
	)
	if err != nil {
		log.Fatal(err)
	}
	res, err := sys.Define(context.Background(), target, docsync.DefineOptions{Owner: "@auth"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("id=%s existing=%v\nedit: %s line %d insert %q\n", res.ID, res.Existing, res.Edit.File, res.Edit.Line, res.Edit.New)
	if !*write || res.Edit.IsZero() {
		return
	}
	path := filepath.Join(repo, res.Edit.File)
	src, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	out, err := res.Edit.Apply(src) // refuses if the file changed since the edit was computed
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		log.Fatal(err)
	}
}
```

```console
$ go build -o bin/define ./define
$ bin/define ../repo internal/store/session.go#SweepInterval
id=sweepinterval-kue77789 existing=false
edit: internal/store/session.go line 5 insert "// ds:def id=sweepinterval-kue77789 owner=@auth"
$ bin/define ../repo internal/store/session.go#SaveSession
id=savesession-73km8a3x existing=true
edit:  line 0 insert ""
```

The target is the same `path#Symbol` or `path:line` that `ds def` takes. A block that already has a def returns its id with `Existing` set and a zero `Edit`. A new id is random, so two calls mint two different ids; keep the one you apply. `Edit` has three shapes (insert, replace, delete), documented on the type, and `Apply` refuses when the line it expects is no longer there, so an edit computed against a stale read cannot land on the wrong line. `DefineOptions` carries the keys `ds def` can set: `Label`, `Owner`, `Stability`, `Tags`, `Desc`, `Env`.

`Rename`, `FixDuplicates` (what `ds def --fix` calls), `Repair`, and `Adopt` follow the same pattern: they return results holding edits and never write.

## Produce a ledger

`Scan` walks the tree and `Snapshot` turns the result into the ledger and refs a first `ds scan` would write:

```go file=../prog/snap/main.go
// Command snap scans a tree and prints the ledger a first `ds scan` would write.
package main

import (
	"context"
	"log"
	"os"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/ext/treesitter"
)

func main() {
	sys, err := docsync.New(
		docsync.WithFS(os.DirFS(os.Args[1])),
		docsync.WithRepo("api"),
		docsync.WithExtractor(treesitter.All()...),
	)
	if err != nil {
		log.Fatal(err)
	}
	res, err := sys.Scan(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	l, _ := sys.Snapshot(res)
	if err := l.Encode(os.Stdout); err != nil {
		log.Fatal(err)
	}
}
```

```console
$ go build -o bin/snap ./snap
$ bin/snap ../repo
# docsync ledger format=2 extract=1 repo=api commit= scanned_at=2026-10-01T03:42:59Z
id	repo	kind	file	symbol	lines	hash	owner	stability	env	args
savesession-73km8a3x	api	func	internal/store/session.go	SaveSession	8-11	73e47ea1f7bfc260d14e34ab7193bda9b627579dc01931009b0a17d5a0d6d86e	@auth	stable		id=savesession-73km8a3x owner=@auth
```

With no `WithConfig`, the library uses `config.Default()` with `docs/**` and `**` as the scan globs, so a zero-config call still scans something. `Bodies(res)` returns the content-addressed block bodies `ds scan` stores under `.ds/blocks/`, already stripped of secret and local blocks.

## Options

Every input is a functional option to `docsync.New`. `WithFS` is the only required one.

| Option | What it supplies |
|---|---|
| `WithFS(fs.FS)` | the tree to scan (required) |
| `WithConfig(config.Config)` | the parsed `.ds/config.toml`; parse it with `config.Parse` or `config.ParseOnto(base, r)` |
| `WithPrevious(ledger, refs)` | the committed state to diff against; both zero means a first run where everything is new and `ok`. A ledger recorded under a newer extraction rule is refused with `ErrNewerRule` |
| `WithAcks(ledger.Acks)` | the append-only ack log |
| `WithStore(Store)` | an interface with `Load` and `Save` that replaces `WithPrevious` and `WithAcks`, for state kept somewhere other than `.ds/` files; `SaveState` writes back through it |
| `WithExtractor(...)` | extra tiers ahead of the built-in ones; the last given wins for a path several match |
| `WithRegistry(*extract.Registry)` | replaces tier selection entirely; the escape hatch when adding tiers is not enough |
| `WithPicker(Picker)` | an extra `pick=` scheme |
| `WithVerbHandler(Verb)` / `WithVerb(names...)` | a custom directive verb with its own check and render, or just names so references to them are not reported as unknown |
| `WithOldContent(func(ledger.Row) (string, bool))` | the body a block had in the previous ledger row; the CLI backs it with `git show` at the ledger's commit |
| `WithBodyAt(func(hash string) (string, bool))` | bodies by content hash, from `.ds/blocks/` or a workspace index; answers "what did this say when it was acked" across many scans |
| `WithCommit(sha)`, `WithCommitLookup(func(sha) bool)` | the current commit for permalinks and headers, and whether an `at=` commit exists |
| `WithRepo(name)` | this repository's name inside a workspace |
| `WithMerged(...)`, `WithMergedRefs(...)`, `WithPreviousForeign(...)`, `WithForeignSnapshot()`, `WithRemoved(...)` | the merged workspace view: other repos' defs, their citations of this repo, the committed foreign snapshot, and repos removed from the workspace (see [Cross-repo workspaces](cross-repo.md)) |
| `WithResolver(check.Resolver)`, `WithStoredHashes(map)` | secret address checks under `Resolve`, and the stored truth hashes that let a run report `rotated` (see [Secrets, runs and URLs](secrets-and-runs.md)) |
| `WithURLCheck(func(href) check.URLResult)` | answers `ds:url` under `Resolve` |
| `WithTestResults(map)` | published CI outcomes for `assert=true` citations |
| `WithRecords(func(args) ([]map[string]string, error))` | a record source for `ds:table`; `records.Frontmatter(fsys, dir)` is the built-in one |
| `WithSnapshot(func(id, sha) (string, bool))` | block content at a commit, for `at=` snapshots |
| `WithExtractCache(scan.Cache)` | incremental scans; a cache that also implements `scan.StatCache` skips reading unchanged files. `CheckOptions{Full: true}` bypasses it |
| `WithNotifier`, `WithObserver`, `WithRenderer`, `WithClassifier` | delivery of findings, audit hooks, output format, and change classification |
| `WithClock(func() time.Time)` | replaces `time.Now`, for reproducible output and tests |

`CheckOptions` has `Strict`, `Env`, `Run`, `Resolve`, and `Full`. `Run` and `Resolve` tell the library the caller will execute directives and reach providers; the library itself still runs nothing, so `Resolve` only has an effect through the resolver and URL hooks you passed in.

## What a System can do

`System` is safe to share between goroutines. Its methods map one to one onto the CLI commands: `Scan`, `ScanFull`, `Check`, `Snapshot`, `Bodies`, `Define`, `FixDuplicates`, `Rename`, `Repair`, `Adopt`, `Ack`, `Render`, `Fences` (repo-mode copies), `Context`, `ContextFor`, `Map`, `Facts`, `Find`, `FindBy`, `Read`, `LocateID`, `Why`, `Blame`, `Graph`, `Impact`, `Report`, `Audit`, `Notify`, `ExtractFile`, and `SaveState`. `Triage(rep)` is a package function that groups a report's `unacked` findings. Read signatures with `go doc github.com/ubgo/docsync System`.

## The packages

The root module is split into packages that each own one grammar or one step, so you can use a piece without the rest:

| Package | Holds |
|---|---|
| `directive` | the directive grammar `ds:<verb> key=value…`, comment and link carriers, parse and format round trip |
| `id` | ids as `<label>-<8-char suffix>`; the suffix is the identity |
| `block` | blocks, references, kinds, carriers, stability policies, change classes |
| `extract` | the built-in tiers (markdown, HTML, AsciiDoc, reStructuredText, config formats, heuristic code, plain text), `Locate` for `path#symbol`, and hooks for grammar tiers outside the root |
| `pick` | the `pick=` schemes |
| `sentence` | binding a citation to its sentence, list item, or table cell |
| `scan` | walking an `fs.FS` under globs, remote `file=` defs, duplicate detection |
| `ledger` | the committed TSV files: ledger, refs, acks, foreign snapshot |
| `match` | previous ledger against current scan, and change classification |
| `check` | the findings engine: every state with its severity and remedy |
| `render` | build-time expansion of directives |
| `config` | `.ds/config.toml` and `ds-workspace.toml` |
| `procplugin` | both sides of the process-plugin protocol |
| `workspace` | merging published ledgers across repositories, the index layout, JUnit outcomes |
| `records` | the frontmatter record source for `ds:table` |

`ext/structured` exports `YAML()`, `TOML()`, `HCL()`, `All()`, and `HCLPicker()`. `ext/treesitter` exports `Go()`, `TypeScript()`, `TSX()`, `JavaScript()`, `Python()`, `SQL()`, `All()`, and `New(Grammar{…})` for registering any other tree-sitter grammar. `ext/records/sqlite` is a `ds:table` record source over SQLite.

## Build your own ds

The `cli` module exports `Main` and `Run`, so an organisation can ship its own binary with the standard commands plus its own extractors, pickers, verbs, and config defaults:

```go file=../prog/pds/main.go
// Command pds is an organisation's own build of ds: the standard commands
// and tiers, a different name, and a config default every repo inherits.
package main

import (
	"github.com/ubgo/docsync/cli"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ext/structured"
	"github.com/ubgo/docsync/ext/treesitter"
)

func main() {
	cli.Main(
		cli.WithName("pds"),
		cli.WithDefaults(cli.Standard()),
		cli.WithExtractor(structured.All()...),
		cli.WithExtractor(treesitter.All()...),
		cli.WithPicker(structured.HCLPicker()),
		cli.WithConfigDefaults(func(c *config.Config) {
			c.Owners = map[string][]string{"@platform": {"khanakia"}}
		}),
	)
}
```

It reads the same `.ds/` files as `ds` and gives the same findings on the same tree:

```console
$ go build -o bin/pds ./pds
$ cd ../repo
$ ../prog/bin/pds check
docs/sessions.md
  3	error    unacked            savesession-73km8a3x changed (moved, body) since this sentence was first cited
      still true: pds ack savesession-73km8a3x --doc docs/sessions.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/sessions.md:3, then ack
1 error
```

The name is used everywhere the binary names itself: the usage line, `pds version`, and every command a remedy, error or `doctor` row tells the reader to run. Directives keep their prefix (`ds:block`), which is configuration, not the binary's name. Other options include `cli.WithRegistry`, `cli.WithVerb` (verb names), `cli.WithPluginLookup` (where `ds-*` plugins are found), `cli.WithNotifyState` (where `ds notify` keeps its memory), `cli.WithVCS`, and `cli.WithHTTPClient`.

## Plugins in other languages

Anything that would pull a dependency or a vendor CLI into your process can run as a separate executable instead: `ds-resolve-<provider>`, `ds-pick-<scheme>`, `ds-records-<source>`, and `ds-<verb>`, speaking one JSON object per line. The `procplugin` package implements both sides, so a Go plugin is a `procplugin.Serve` call and a host is a `procplugin.Host`. [Secrets, runs and URLs](secrets-and-runs.md#write-your-own-resolver) has a working resolver in shell and in Go; the full protocol is in [the spec](../SPEC.md#374-process-plugins-for-any-language).
