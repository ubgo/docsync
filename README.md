<h1 align="center">docsync</h1>
<p align="center"><strong>Know the moment a sentence in your docs stops being true.</strong></p>
<p align="center">Documentation drift detection for teams working with AI coding agents: an open-source Go CLI, library, MCP server and language server that binds docs to the code, config and facts they describe.</p>

<p align="center">
  <a href="LICENSE"><img alt="License: Apache-2.0" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
  <img alt="Go 1.26" src="https://img.shields.io/badge/go-1.26-00ADD8?logo=go&logoColor=white">
  <img alt="Test coverage: 100% of statements" src="https://img.shields.io/badge/coverage-100%25-brightgreen">
  <img alt="Core library: Go standard library only" src="https://img.shields.io/badge/core-stdlib%20only-informational">
  <img alt="Works with MCP clients" src="https://img.shields.io/badge/MCP-server-8A2BE2">
</p>

Keep documentation bound to the code, configuration, and facts it describes, and find out the moment the thing behind a sentence changes. docsync is a docs-as-code tool for stale documentation: it works with any AI coding agent that speaks MCP (Claude Code, Cursor, and others), in editors through its language server and the VS Code extension, in CI through a GitHub Action and pre-commit hooks, and in Hugo and Docusaurus sites at build time. It is open source, runs locally, and needs no account or API key.

You mark a block once, in the file that owns it, with a comment. You cite it from prose with a link. `ds check` tells you which sentences are now about code that changed, moved, or vanished, with the diff and the exact command that clears each finding. Nothing is copied into the repository; permalinks, values, and code snippets are produced at build time from live source.

```go
// ds:def id=sess-save-k7m2p4xq owner=@auth stability=api
func (s *Store) SaveSession(ctx context.Context, sess Session) error {
```

```yaml
auth:
  port: 8081   # ds:def id=auth-port-h3v8n2wd
```

```markdown
Every write goes through [`SaveSession`](ds:block?id=sess-save-k7m2p4xq). It listens on [8081](ds:cfg?id=auth-port-h3v8n2wd).
```

**Documentation:** the [guide](docs/guide/README.md) covers setup, every directive, command, config key, language and integration, with real output throughout; start with [docsync at a glance](docs/guide/how-it-works.md) or [Getting started](docs/guide/getting-started.md). The full design is in [docs/SPEC.md](docs/SPEC.md). It is normative; the conformance fixtures under `testdata/conformance` are the tie-breaker where prose is ambiguous.

## Contents

- [Install](#install)
- [Why docsync?](#why-docsync)
- [What is here](#what-is-here)
- [Using the CLI](#using-the-cli)
- [docsync on docsync](#docsync-on-docsync)
- [What in `.ds/` is committed](#what-in-ds-is-committed)
- [Using the library](#using-the-library)
- [Development](#development)
- [Limits](#limits)
- [Status](#status)
- [Where the implementation extends the spec](#where-the-implementation-extends-the-spec)
- [FAQ](#faq)
- [License](#license)

## Install

macOS (Apple silicon), Linux and Windows, amd64 or arm64. On macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/ubgo/docsync/main/install.sh | sh
```

It downloads the newest `ds` release for your machine, checks it against the release's `checksums.txt`, and installs `ds` and its five secret-resolver plugins (`ds-resolve-aws`, `-gcp`, `-github`, `-onepassword`, `-vault`) to `/usr/local/bin`, using sudo if it needs to. No Go toolchain is involved. Options go on the `sh` side of the pipe:

```sh
curl -fsSL https://raw.githubusercontent.com/ubgo/docsync/main/install.sh | INSTALL_DIR=$HOME/.local/bin sh   # no sudo
curl -fsSL https://raw.githubusercontent.com/ubgo/docsync/main/install.sh | VERSION=v0.1.4 sh                # a specific release
```

On Windows (PowerShell), which installs to `%LOCALAPPDATA%\ds` and adds it to your PATH:

```powershell
irm https://raw.githubusercontent.com/ubgo/docsync/main/install.ps1 | iex
```

With Go 1.26 or later and a C compiler (`ds` includes the tree-sitter parsers, which are C). This is the way to install on an Intel Mac, for which there is no prebuilt binary:

```sh
go install github.com/ubgo/docsync/cli/cmd/ds@latest
go install github.com/ubgo/docsync/cli/cmd/ds-resolve-aws@latest   # a secret-resolver plugin, only if you use `ds check --resolve`: -aws -gcp -github -onepassword -vault
```

Or download an archive from the [releases page](https://github.com/ubgo/docsync/releases): the `ds/v…` releases carry `ds` and the resolver plugins in one archive for darwin/arm64, linux/amd64, linux/arm64, windows/amd64 and windows/arm64, with a `checksums.txt`. The Windows builds are run on real Windows machines by `.github/workflows/windows.yml` (the Go tests of every module, then `scripts/windows-smoke.ps1` against the published binary); `ds:run` and `ds review --ai` run their commands under `sh`, which Git for Windows provides; name another shell with `[run] shell = "pwsh"`, and if the shell is missing `ds` says so and stops rather than skipping. Check what you are running with `ds version`.

The library, for Go programs that embed docsync (standard library only):

```sh
go get github.com/ubgo/docsync@latest
go get github.com/ubgo/docsync/ext/structured@latest    # YAML, TOML, HCL tier
go get github.com/ubgo/docsync/ext/treesitter@latest    # Go, TypeScript, Python, SQL tier (cgo)
```

In CI, the GitHub Action installs `ds` itself: `uses: ubgo/docsync/integrations/github@main` (see [integrations/github](integrations/github/README.md)). For pre-commit, install `ds` first, then add:

```yaml
- repo: https://github.com/ubgo/docsync
  rev: v0.1.0   # any tag or commit; the hooks run the ds on your PATH
  hooks: [{id: docsync-impact}, {id: docsync-literals}]
```

## Why docsync?

An AI coding agent can rewrite a function in seconds; the paragraph that explains it stays exactly as it was, and nothing tells anyone. Code review sees the diff, not the page three directories away that now describes behaviour that no longer exists. docsync closes that gap: a sentence cites the code it is about, and when that code changes, `ds check` names the sentence, shows the diff, and prints the command that clears it — for a human, or for the agent that made the change.

- **Cites by id, not by path and line.** A block is marked once in the file that owns it; the id travels with it through moves, renames and reformatting, so a doc keeps pointing at the right code.
- **Nothing is copied.** Values, code snippets and permalinks are rendered from live source at build time, so a doc cannot hold a stale copy.
- **Every finding says what to do.** Each one carries the diff since the sentence was last approved and the exact command to approve it or the place to edit it.
- **An approval is for one sentence, and it is recorded.** An ack names the sentence, the block's hash, and who approved it; rewriting the sentence needs a new one. Over MCP an agent may ack only by naming the human who delegated it.
- **Built for agents.** `ds mcp` gives agents `map`, `find`, `read`, `context`, `impact`, `check`, `def` and `ack`, with every payload fenced so repository text is never read as an instruction; `ds init --agents` wires it up.
- **Boring core.** The library is Go's standard library only, never writes a file or opens the network itself, holds 100% statement coverage, and is specified by a conformance suite rather than by its own behaviour.

## What is here

This repository is the root library: `github.com/ubgo/docsync`, standard library only, pure over its inputs. It never writes a file, reads the environment, or touches the network. A CLI, an MCP server, and the tree-sitter and structured-format extractors are separate modules that build on it.

| Package | Holds |
|---|---|
| `directive` | the one directive grammar: `ds:<verb> key=value…`, comment and link carriers, continuation folding, parse and format round-trip |
| `id` | ids as `<label>-<8-char suffix>`; the suffix is the identity, the label is a hint |
| `block` | blocks, references, kinds, carriers, stability policies, change classes |
| `extract` | the built-in tiers: markdown and HTML, AsciiDoc and reStructuredText (the document tier), config (yaml, toml, ini, env, properties), heuristic code, plain text; `Locate` for binding a symbol or line without a directive; exported hooks (`ScanCode`, `Prelude`, `NewDef`) for grammar tiers outside the root |
| `pick` | `pick=` schemes for narrowing a block to one value or range |
| `sentence` | binding a link to the sentence, list item, or table cell that contains it |
| `scan` | walking an `fs.FS` under globs, remote `file=` defs, duplicate detection |
| `ledger` | the committed TSV state: ledger, reverse index, append-only acks |
| `match` | previous ledger against current scan: ok, moved, changed, moved-unmarked, rewritten, deleted, new; change classification |
| `check` | the findings engine: every state in the spec's table with severity, remedy, and exit code |
| `render` | build-time expansion of directives to permalinks, values, code, chains, tables |
| `config` | `.ds/config.toml` in the TOML subset the spec uses |
| `procplugin` | both sides of the process-plugin protocol for `ds-pick-*`, `ds-resolve-*`, `ds-records-*`, `ds-<verb>` executables |
| `workspace` | merging published ledgers and refs across repositories, the index layout, JUnit outcomes for `assert=` |
| `records` | the frontmatter record source for `ds:table`: a directory of markdown files, filtered by `kind`, `where`, `sort`, `cols`, `limit` |
| `ext/structured` (own module) | the structured tier: YAML (yaml.v3), TOML (go-toml), and HCL (hcl/v2) extractors that bind keys, tables, blocks, and attributes by parsed extent; `structured.All()` registers the three |
| `ext/treesitter` (own module, cgo) | the syntax tier: Go, TypeScript, TSX, JavaScript, Python, and SQL through tree-sitter; a def binds to the next declaration as the grammar sees it, nested declarations become `Class.method`, single literals are values, and hashes are taken over the token stream so formatters never cry wolf; `treesitter.New(Grammar{…})` registers any other grammar |
| `ext/records/sqlite` (own module) | the SQLite record source for `ds:table` on modernc.org/sqlite (pure Go); `kind=` names the table, the rest of the query is the shared records language |
| `integrations/github` | the composite GitHub Action and the three workflows from spec §24: pull request comments, publish after merge, the nightly job that alone enables `--run` and `--resolve` |
| `integrations/hugo`, `integrations/docusaurus`, `editors/vscode`, `.pre-commit-hooks.yaml` | the Hugo module reading `ds export hugo` data, the Docusaurus remark plugin over `ds render` plus freshness from `ds status --json`, the VS Code client for `ds lsp`, and pre-commit hooks for `impact --staged` and `report --literals` (at the repository root, which is the only place pre-commit looks) |
| root | `New` with functional options; `Scan`, `Check`, `Snapshot`, `Render`, `Define` (returns an `Edit`), `Facts`, `Why`, `Map`, `Context`, `Impact`, `Find`, `Read`, `Ack`, `Triage`, `Audit`, `Rename`, `Graph`, `Blame`, `Report`, `Adopt` |

## Using the CLI

```sh
ds init                   # .ds/config.toml, empty ledgers, a CI snippet
ds def internal/store/write.go#Store.Save --owner @auth   # prints the id; inserts the directive
ds scan                   # writes .ds/ledger.tsv and .ds/refs.tsv; commit them
ds check                  # exit 1 on unacked, broken, expired…; --json for machines
ds ack <id> --doc docs/a.md --line 12 --note "still true"
ds render docs/a.md       # plain markdown with permalinks, values, and live code
ds map | ds context docs/a.md --since ack | ds facts | ds why <id> --chain --history | ds impact
ds triage                 # group unacked findings by diff; --ack-group N acks one group with one note
ds adopt                  # turn hand-written path#L10-L20 links into defs and cites; ds undo reverses it
ds report --gaps          # what to document next; --literals finds facts typed by hand
ds audit | ds blame docs/a.md 12 | ds graph --dot | ds rename sess session
ds publish --tests junit.xml   # default branch only: this repo's ledger, refs, and test outcomes into the workspace index
ds sync                        # fetch the index; ds check does this first when `workspace` is set in .ds/config.toml
ds mcp                         # the agent surface over MCP on stdin/stdout: map find read locate facts why context check impact def ack
ds check --run --resolve       # execute ds:run where allowed; check ds:url with a TTL cache; ask ds-resolve-<provider> plugins about secret addresses
ds render docs/a.md --at v1.2  # the page and its blocks as they were at a commit
ds impact --staged             # pre-commit: only what the staged files will flag
ds ack --from-commit HEAD      # honour `ds:ack id=…` written in the commit message (your configured prefix, if not ds)
ds notify                      # digest open findings per owner; Slack webhook when notify.slack is set
ds lsp                         # editor server: code lens with dependents on every def, hover with block and diff, go-to-definition, rename/delete warnings
ds review                      # the review worklist; --ai pipes it as JSON into [review] command and prints the patch it returns; never acks
ds check --expand --full       # list every finding instead of collapsing heavily cited ids; bypass the extraction cache
ds github comment              # in Actions: one PR comment per doc with findings and diffs; applying the docs-acked label acks them for the reviewer who applied it
ds export hugo --out data/docsync   # blocks.json and status.json for the Hugo module; Docusaurus uses the remark plugin instead
ds publish --branch            # publish a release branch under repos/<name>/@<branch>; docs select it with branch=
ds repair                      # mend directives an older build left bare: comment them, or delete them where the format has no comments; --apply writes, ds undo reverses it
ds version                     # the commit this binary was built from, and whether the tree was dirty; --json for machines
ds init --agents               # CLAUDE.md and AGENTS.md rules, a Claude Code skill, .mcp.json, and a SessionStart hook loading `ds map`
```

Over MCP, `def` is capped per session by `agents.max_defs_per_run`, `ack` is accepted only with `delegated_by` and is recorded as an agent ack, and `run`, `resolve`, `undo`, `publish`, and `adopt` are not exposed. Every payload sits behind a `data:` delimiter so scanned text is never read as an instruction.

## docsync on docsync

The repo tracks its own docs, under `prefix = "dsself"` in `.ds/config.toml`. The distinct prefix is not decoration: docsync's source and fixtures are full of `ds:` directives written as *examples*, and with the default prefix the scanner reads them as real, so the tool could not be pointed at itself at all. `task dogfood` (part of `task`) runs `ds check --full` with the binary built from the tree, and it checks rather than scans, so the gate never rewrites committed state. Updating a baseline stays a deliberate `ds scan` plus an `ds ack`.

What it binds today is small on purpose: the claims in these docs that restate a value in the code, such as the machine-local ignore list below and the extraction-rule constant. Adding one is `ds def <file>#<symbol> --owner @docsync --stability stable`, then an HTML comment `<!-- dsself:block id=… -->` above the sentence that depends on it. Pick the stability from what you want to hear about — `api` deliberately stays silent on a body-only change, which is the right answer for a signature and the wrong one for a list whose contents are the contract.

It has already paid for itself twice. The extraction cache was keyed on file content alone, so editing `prefix` served entries extracted under the old one and reported defs and citations that existed nowhere in the tree; and an unrecognised `stability=` fell back to `stable` in silence, although the code comment claimed the scanner reported it, so a typo in `frozen` quietly relaxed the policy the author asked for.

## What in `.ds/` is committed

`.ds/` holds two kinds of file and they are not interchangeable. **Commit** `config.toml`, `ledger.tsv`, `refs.tsv`, `acks.tsv`, `blocks/` and, once a workspace is set up, `foreign.tsv`: these are shared facts, and a check is only reproducible on another machine because they are in the tree. **Never commit** `cache/`, `index/`, `journal.tsv`, `urls.json`, `runs.json`, `notified.json`, `hashes.json`, `metrics.json` or `lock` (the file concurrent `ds` commands lock while they write): they are per-machine and would conflict on every merge, and two of them are worse than noise — `cache/` stores extracted block bodies and `journal.tsv` stores the source lines an edit replaced, so on a repo with `[secret] paths` either can hold the value of a secret block, in history, where rotating the credential cannot remove it.

<!-- dsself:block id=gitignorebody-jgek8ecm -->
`ds init` writes a `.ds/.gitignore` covering exactly that second list, so a repo started with it is correct by default. A repo initialised before a name was added to the list is not, and that is what `ds doctor` checks: the `gitignore` row names the missing lines, and adding them is the whole fix — never `init --force`, which would overwrite the config and the ledgers to repair a one-line omission. If a local file was already committed before the line existed, `git rm --cached` it as well; the ignore alone does not untrack what git is already tracking.

`ds init` also writes `.ds/.gitattributes` with `acks.tsv merge=union`. The ack log is append-only, so without it any two branches that each record an ack conflict when merged; with it git keeps both sides' rows, and the newest ack for a sentence wins by its timestamp. Nothing else in `.ds/` is union-merged, because the other files are rewritten, not appended. `ds doctor` checks for the attribute, and a merge that still conflicts is reported as a conflict with the right remedy for that file — for `refs.tsv`, keep both sides.

`ds doctor` reports config, globs that match nothing, extractors, ledger format, the extraction rule it records, and git, and exits non-zero when any row is `FAIL`. Every command is a thin call into the library; the `cli` package exports `Main` and `Run` so an organisation can ship its own binary with extra extractors and verbs.

## Using the library

```go
sys, err := docsync.New(
    docsync.WithFS(os.DirFS(repo)),
    docsync.WithConfig(cfg),               // parsed by you; the library never reads files it was not given
    docsync.WithPrevious(ledger, refs),    // the committed state to diff against; zero for a first run
    docsync.WithAcks(acks),
    docsync.WithOldContent(gitShow),       // func(ledger.Row) (string, bool): the body a previous row had, for diffs and change classes
)
report, err := sys.Check(ctx, docsync.CheckOptions{})
for _, f := range report.Findings {
    fmt.Println(f.State, f.Doc, f.Line, f.Remedy)
}
res, err := sys.Define(ctx, "internal/store/write.go#Store.Persist", docsync.DefineOptions{Owner: "@auth"})
// res.Edit is a proposed change to the source file; you decide whether to apply it
```

Every operation returns values. The caller applies edits, touches git, and prints.

## Development

Requirements: Go 1.26, [Task](https://taskfile.dev), `gofumpt`, `staticcheck`, and a C compiler for the tree-sitter tier.

The repository holds several Go modules: the root library, `cli`, `ext/structured`, `ext/treesitter`, `ext/records/sqlite` and `integrations/hugo`. The root `go.work` joins them, so a change in the library is seen by the `cli` build without a release. Each module's own `go.mod` requires the others at published versions, which is what `go install` and `go get` read.

```sh
task install            # build ds and the resolver plugins into bin/, symlinked onto PATH (DEST=~/.local/bin)
task uninstall          # remove those symlinks
task                    # fmt check, vet, staticcheck, race tests, 100% coverage gate
task conformance        # only the conformance suite and the JSON contract goldens
task conformance:update # regenerate expectations after an intentional rule change, then read the diff
task fuzz -- ./directive FuzzParse
```

`task install` symlinks rather than copies, so a later `task cli:build` is live immediately with no reinstall; it refuses to overwrite anything in the destination that is not already one of its own links, and `task install DEST=/some/dir` picks another directory.

Every package holds 100% statement coverage and the gate fails otherwise. A rule without a conformance fixture is not yet a rule: `TestConformanceStatesCovered` fails when a finding state has no fixture producing it.

### Releasing

Releases are cut with [volt](https://github.com/khanakia/voltkit) (`volt status` lists what is unreleased). Modules release in dependency order, because a module's `go.mod` may only require versions that already exist:

1. the root library: `volt release . vX.Y.Z` (tag `vX.Y.Z`);
2. `ext/structured`, `ext/treesitter`, `ext/records/sqlite` after their `go.mod` requires the new root version: `volt release ext/structured vX.Y.Z` and so on (tags `ext/structured/vX.Y.Z`);
3. `cli` after its `go.mod` requires those: `volt release cli vX.Y.Z`, then the binary, `volt release cli/cmd/ds vX.Y.Z` (tag `ds/vX.Y.Z`).

Pin with `GOWORK=off go mod tidy` in the module, so the checksum lines come from the published versions rather than the workspace. `cli/cmd/ds/.volt.yml` builds `ds` with cgo, using `zig cc` for Linux and Windows and the host compiler for darwin/arm64 (the release is cut on an Apple silicon Mac, with volt v0.2.0 or later); an Intel Mac binary cannot be cross-built with cgo, which is why there is none. Its `extra_binaries` packs the five resolver plugins into the same archive, so a release that changes only `ds` or only a plugin is still one `volt release cli/cmd/ds`. `install.sh` and `install.ps1` are generated by `volt gen install cli/cmd/ds`; do not edit them by hand.

## Limits

What docsync cannot do, stated here rather than discovered in use. Each of these is a real boundary, not a todo list; where there is a workaround it is named.

**It cannot tell you that meaning changed while text did not.** The whole mechanism is a hash over bytes. A comment rewritten to say the opposite of what it said, a constant renamed with its value kept, a function whose contract changed without its signature changing — none of these flag, because none of them change what is hashed. `stability = frozen` is the blunt instrument for a block where this matters: it flags on any change at all, including a comment.

<!-- dsself:block id=flags-zfrzu4vz -->

**`stability = api` does not flag a body-only change.** That is the point of it — a signature is the contract and the body is not — but it is the wrong choice for a block whose *contents* are the contract, such as a list of allowed values or an ignore list. Use `stable` there. The full table is section 20 of the spec, and the cost of choosing wrong is silence, not noise.

**Symbol lookup is less precise than scanning.** `ds def path#Name`, `ds adopt` and `ds locate` resolve names through a line-based matcher in the root library, which imports only the standard library and so has no grammar. Scanning uses the tree-sitter tiers. So a name that binds cleanly on a scan may not be findable by `#Name`, and the two disagree on the classification of a line more often than is comfortable. Concretely: the grammarless matcher will not synthesise a name for a body member, because the leading identifier is the name in Go and TypeScript and a modifier in the C family — `private int x;` would become a declaration called `private`, and `path#Name` would then bind the wrong line. Lookup therefore compares against the name you asked for, which can be right or absent but never wrong.

**A directive is written only where it will bind.** `ds def` applies its edit in memory and extracts the result with the same tier a scan uses; if the new id does not bind cleanly, it refuses with that tier's reason and writes nothing, so `--dry-run` predicts the refusal instead of a scan discovering it after the file was edited. A computed key in an object literal (`[k]: 1`) binds but has no name, since there is nothing a reader could search for. A directive that cannot bind the declaration directly below it reports `ds:def cannot bind the declaration below it` rather than binding something further down.

**A file with no comment syntax cannot hold a directive.** JSON, CSV, lockfiles and any type docsync has no carrier for are refused with `no comment carrier for this file type` and left byte-for-byte untouched; bind a value in one from a file that can carry a comment, with a remote def (`file=config.json pick=json:$.port`). This is a refusal rather than a fallback because the fallback shipped once — an unrecognised type received the directive as a bare line, which broke a Go workspace's build and would have made a JSON file unparseable. If your repository was touched by that, `ds scan` reports every bare directive sitting in such a file, with the file and line, and `ds repair --apply` mends them all -- commenting each one where the format allows, deleting it where it does not -- reversibly, through `ds undo`. Plain text is the one exception: a `.txt` file carries a directive as a line of its own, which is what the text tier reads.

**Remote defs do not follow a moved target.** A def pointing at another file with `file=` hashes the extracted value and reports `pick failed` when the key disappears, but it will not notice that the key moved and re-bind. Sidecar mode — keeping directives out of source entirely — inherits that weakness in full.

**Secret and local blocks are checked without being read.** A block matched by `[secret] paths`, or carrying `secret=` or `local=`, publishes a hash and no body. A change to one is therefore reported as class `unknown` rather than described, and `unknown` flags wherever `api` flags: you learn that something changed and not what. The same applies after `ds prune` removes a body an old baseline still referenced — the 30-day grace period makes that unlikely, not impossible.

**An extraction-rule change makes affected citations report drift once.** What bytes a block covers is versioned (`extract=` in the ledger header). When that version bumps, the citations of every affected block report as changed and are acked once with a note; there is no automatic re-baselining, because proving a stored hash was in sync before the change needs the old bytes and the old rule, and a repo cannot be assumed to have either. `task extract:diff` exists so a rule change is a decision rather than a surprise.

**The evidence that an upgrade is safe covers only the repositories it was run against.** `task extract:diff` compares two builds block by block and fails when a block naming the same symbol over the same lines hashes differently — but it can only compare the trees in `CORPUS`, which defaults to the sibling repositories present on the machine. A repository with defs on constructs neither corpus contains could still move. Widen it before a release: `CORPUS=/path/a:/path/b task extract:diff`. That is the honest limit of the claim, and no amount of local green changes it.

**`ds version` reports only what the toolchain recorded.** It reads the revision and a dirty flag the Go toolchain stamps into every build made inside a git checkout, so a binary built outside one says `unknown` for those fields rather than guessing. The development build is a symlink into this repo's `bin/`, so `ds version` saying `dirty` is how you tell a running binary does not match any commit — and `task corpus` still refuses to run when a source file is newer than the binary, because a stale build has produced misleading results here more than once.

**Notify escalation moves only when the nightly runs.** The shipped nightly workflow is manual by default, for the reason below. `ds notify` still keeps its dedupe memory between runs through the Actions cache, but `escalate_after` is judged when a run happens, so an item escalates at the first run after its interval, not on the day the interval ends. Restore the schedule from the workflow's header where minutes are not metered.

**The CI template is not wired for metered runners.** `ds init` writes `.ds/ci-github.yml` with `push` and `pull_request` triggers. On a private repository those minutes are billed, and a workflow that fails for billing teaches everyone to ignore the gate. Change the triggers to `workflow_dispatch:` before copying it into `.github/workflows/`, and treat the local `task` as the real gate.

**Documenting a tool whose subject matter is directives needs a different prefix.** docsync's own source is full of `ds:` directives written as examples; scanning it with the default prefix read 40 of them as real. Set `prefix` in `.ds/config.toml` to something else, as this repo does with `dsself`.

## Status

The library is at `v0.1.2` and the `ds` binary at `v0.1.4` (see [CHANGELOG.md](CHANGELOG.md)). Every module is built and gated at 100% statement coverage: the root library, the `cli` module with `init doctor def scan check ack refresh render map context facts why find read locate impact status triage audit rename graph blame report adopt undo publish sync mcp lsp notify review github export`, the `ext/structured` tier (YAML, TOML, HCL), the `ext/treesitter` tier (Go, TypeScript, TSX, JavaScript, Python, SQL), the `ext/records/sqlite` source, and the JavaScript integrations (Docusaurus plugin, VS Code client) under `node --test` with 100% line, branch, and function coverage. Secret resolvers ship as process plugins under `cli/cmd/`: `ds-resolve-github` (existence through `gh`), and `ds-resolve-onepassword`, `ds-resolve-aws`, `ds-resolve-gcp`, `ds-resolve-vault` (existence and a hash through `op`, `aws`, `gcloud`, `vault`; the value never leaves the plugin); any provider can add one in any language by speaking the procplugin protocol. Scale features are in: parallel extraction with a persisted cache under `.ds/cache/` (`check --full` bypasses it), a per-directory sharded ledger with `[ledger] shard = true`, collapsed findings for heavily cited ids, and `report --metrics` printing bytes served to agents against source bytes plus busy id prefixes. Repo mode (`include.mode = "repo"`), the HTTP record source, fork pull request refusal of `--run` and `--resolve`, and doc rename detection keeping acks are built too.

A citation keeps its baselines when it moves — a line inserted above it, its doc renamed — by matching the sentence it sits in, and where a move is ambiguous every candidate takes the baseline that still reports a change. A file the scan cannot read (too large, binary, or a line past the limit in a tier that is not prose) keeps its last recorded state and fails the check as `unscanned` rather than taking its citations with it. Coverage of a citation is decided against that citation's own baseline — the hash it was acked at, or the hash recorded the first time it was seen — and never against the scan-to-scan change table. Blocks bodies are kept content-addressed under `.ds/blocks/` and, for a workspace, under `repos/<name>/blocks/` in the index, so a change can still be classified when its baseline is many scans old or lives in another repository; secret and local blocks publish a hash and no body, and a body the store cannot supply yields the class `unknown`, which flags wherever `api` flags. `refs.tsv` is `format = 2` (it gained `seen_hash`); `format = 1` files are still read and are upgraded by the next scan. A citing repo also commits `.ds/foreign.tsv`, the snapshot of the foreign blocks it cites, written by `ds sync`; `ds check --frozen` resolves against it without syncing, so the same commit gives the same answer on any machine and with no network, and it is the default when `CI` is set. `ds status` reports how far that snapshot is behind upstream, per repo and per block with the change class, without ever changing the exit code, and `[check] snapshot_max_age` adds an opt-in warning. `ds prune` removes block bodies no ledger, ack, citation, or snapshot still needs, with a 30-day grace period, a `--dry-run`, an `--index` mode gated on the default branch and a fresh sync, and a targeted `--hash … --force` for a value that should never have been stored.

## Where the implementation extends the spec

These are deliberate and recorded so the spec can catch up rather than drift:

- The `.ds/` directory is never scanned, since the ledger, refs, acks, and journal quote directives and would define and cite themselves.
- `triage` groups diff-less findings by block id, so a first run without git history still yields one decision per block.
- Merged workspace references carry `doc_repo` in findings, additively, so a repo can tell which other repo's page depends on its block.
- Config keys can be one def per environment: duplicates are keyed by id and `env=` together.
- `ds:run` outcomes live in `.ds/runs.json`, url checks in `.ds/urls.json`, notify state in `.ds/notified.json`, stored truth hashes in `.ds/hashes.json`, and the source-write journal in `.ds/journal.tsv`; the spec named the behaviours and left the files to the implementation.
- Repo-mode copies end with `<!-- /ds:block hash=… -->` carrying the short block hash, which is how `check` tells `stale` (source moved on) from `tampered` (copy edited) without storing anything else; permalinks inside a committed copy are relative to the page and carry no commit, so a new commit does not touch every copy.
- Secret resolvers are process plugins only (`ds-resolve-<provider>`); `resolve.providers` in config limits which are consulted, and a plugin reply carrying anything but existence and a hash is refused by the host.
- The MCP server lives in the `cli` module (`ds mcp`) rather than a separate `mcp` module; it is a thin JSON-RPC loop over the same library calls and would only add a module boundary today.
- The HTTP record source lives in the `cli` module, since the root must stay free of `net/http`; the SQLite source is its own module because of its driver.
- The extraction cache carries block content and region text beside the public JSON shape (which hides both) so a cache hit is byte-identical to a fresh extraction; a test pins the set of hidden fields.
- `ds github comment` under the `docs-acked` label records the acks itself (actor `github:<login>`) and prints what to commit; the spec left how the reviewer's `ack --all` is recorded to the implementation. The ack happens only on the `labeled` event, credited to whoever applied the label: a later push to a pull request that still carries it acks nothing and says to re-apply it, because the sender of a push is its author, and acking there let every change after the review through unreviewed.
- Hugo cannot run a program at build time, so `ds export hugo` writes the rendered blocks and freshness as data files and the Hugo module reads them from a shortcode and a partial; Docusaurus, which can, calls `ds render` from a remark plugin.

## FAQ

**Does docsync send my code or docs anywhere?** No. The library never opens the network, and scanning, checking, acking and rendering work offline. The CLI reaches the network only for features you turn on: syncing a shared workspace index through git, `check --resolve` for URLs and secret-existence plugins, `ds notify` posting to a Slack webhook you configure, `ds github comment` commenting on a pull request inside GitHub Actions, and a `ds:table` record source you point at an HTTP URL.

**Does it need an API key or an account?** No. It is a local binary and a Go library. `ds review --ai` can pipe its worklist to a command you choose, but nothing calls a model on its own.

**Which AI coding agents does it work with?** Any agent that speaks MCP can use `ds mcp`. `ds init --agents` writes `CLAUDE.md` and `AGENTS.md` rules, a Claude Code skill, `.mcp.json`, and a hook that loads `ds map` at the start of a session.

**Can an agent approve its own changes?** Not silently. Over MCP, `ack` is refused unless it names the human who delegated the run, and it is recorded as an agent ack under that name; `run`, `resolve`, `undo`, `publish` and `adopt` are not exposed to agents at all.

**What happens when I move or rename code?** The id is in a comment beside the code, so it moves with it; `ds check` reports the citation as moved rather than broken. A citation that moves inside a doc, or a doc that is renamed, keeps its approval too.

**Which languages and formats does it understand?** Docs in Markdown, HTML, AsciiDoc and reStructuredText; code in Go, TypeScript, TSX, JavaScript, Python and SQL through tree-sitter, and any other language through a heuristic tier; config in YAML, TOML, HCL, INI, `.env` and properties files.

**How is this different from reviewing docs by hand?** A reviewer sees the files in a diff, not every page that describes them. docsync knows which sentences cite which blocks, so a change to a block names each sentence that depends on it, across the repository and across repositories that share a workspace.

**Will it slow down CI?** A check reads only the files that changed since the last run: on a generated repository of 100,000 files an incremental `ds check` takes under a second, and `task perf` measures it against the targets in the spec.

## License

docsync is licensed under the [Apache License 2.0](LICENSE). You may use, modify and distribute it, including in closed-source and commercial work, as long as you keep the license and copyright notices; the license also grants a patent license from contributors.
