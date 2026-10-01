# Changelog

All notable changes to **docsync** are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- The VS Code extension never started: VS Code's language client runs the server as `ds lsp --stdio`, and `ds lsp` rejected the flag and exited. `ds lsp` now accepts `--stdio`, and the extension no longer asks for it, so it also works with an older `ds` (bug 130).
- In VS Code, clicking a `[text](ds:cfg?id=…)` or `ds:block` link in markdown failed with "Unable to resolve resource". The extension now opens what it names: it reveals the block in its file and shows the id, location and current body (bug 131).

## [0.1.5] - 2026-10-01

A release of the `ds` binary and the `cli` module (0.1.5), the library (0.1.3), and the tier modules (`ext/structured` 0.1.1, `ext/treesitter` 0.1.2, `ext/records/sqlite` 0.1.1): 81 fixes from a sweep that ran every documented behaviour against the binary. Read Upgrading first.

### Changed

- `--resolve` now also needs `[resolve] enabled = true`, as `--run` needs `[run] enabled`; without it `ds` says so on stderr and contacts nothing. Add the key to a nightly job's config if it used `--resolve`.
- Config keys that were accepted and did nothing are refused when the config loads, naming the key: `notify.github_issues`, `[sources.*]`, `[workspace.id]` and `[workspace.env]`.
- `env.known` applies to every environment name: `env.default`, `[run.env.<name>]`, `--env` on `check`, `render` and `def`, and `env=` on defs and citations.
- `[id] suffix_length` and `suffix_alphabet` are checked when the config loads.
- Agent acks (MCP and `ds ack --agent`) require a delegate listed in `[owners]`; MCP acks are recorded under the client's name.
- `ds def`, `ds adopt` and `ds def --fix` derive an id's suffix from the repository, the file, the line and its content, so a dry run prints the id the real run writes.
- Rendered links are relative to the page, so links in `docs/` work on GitHub; `{rel}` joins `{file}` in `[check] permalink`. A repo-mode copy written with the old root-relative links is accepted as it is, and the next `ds refresh` rewrites it.
- The `ds check` text summary counts passing findings as `ok` instead of `none`, and prints the diff under each finding.
- A citation must carry its block's current label: one with a stale label is `broken` and names the id it meant (the spec said the suffix alone identified it).
- `<!-- ds:cfg -->` in block position is a scan problem, since render cannot show it.
- Reports `ds` writes (`check --json`, `audit --export`) are not scanned, so they create no phantom citations; the CI template from `ds init` writes its report to `$RUNNER_TEMP`.
- The scanner skips any checkout nested in the repository (a directory with its own `.git`: a worktree, a submodule, a cloned dependency), as the spec already said it did for submodules.

### Added

- `ds-resolve-env`, shipped in the release archives and install scripts: `source=env` hops are checked against the environment `ds` runs in.
- A `stale copy` finding: after a rotation, a copy that still holds the old value.
- A `run failed` finding for a `ds:run` that fails under `check --run`; a run not executed is `skipped` with the reason.
- `ds doctor` rows for the workspace, the reachability of its index, and each `resolve.providers` plugin.
- `ds init --agents` registers `ds mcp` with Cursor, VS Code and Gemini configs where they exist, and honours `agents.mcp` and `agents.session_hook`.
- The MCP `impact` tool takes `staged`; `ds why` lists citations from other repositories; `ds context --since <commit>` works.
- The GitHub action commits acks recorded under the `docs-acked` label to the pull request branch; the publish workflow template produces the JUnit report it publishes.
- The Docusaurus plugin gains `sourceUrl` and refuses the `remarkPlugins` position with a clear error.
- `scripts/e2e/determinism.sh`, `round-trips.sh`, `upgrade.sh`, `resolvers.sh`, `def-targets.sh`, `cli-commands.sh`, `classify-and-diff.sh`, `workspace-surfaces.sh`, and source-scanning tests that config keys take effect, that messages name real keys and commands, and that SPEC's findings table and library API match the code.

### Fixed

- Checking and diffs: a finding's diff comes from the stored body of the hash its citation was acked or first seen at, and `ds ack` stores the body it approves, so the diff is never the wrong old version or missing. A change checked without a prior scan is never classified `body` by default, so `stability=api` no longer passes a signature change; a changed one-line constant is `value`. `ds find`, `ds map`, `ds report --stalest` and same-line findings are ordered by place, not by random id suffix.
- `ds def`: it no longer writes a trailing directive into `.properties`, INI and `.env` values (where it became part of the value); `path#Name` finds every name a scan records (`Holder.Member`, Python constants and class attributes, TypeScript object paths, HCL and TOML tables, shell functions); `path:A-B` binds that range or refuses; a dotfile gets a label; a target on an existing directive returns that def instead of adding a second one.
- Directives: `.mts`, `.cts`, `.cjs`, `.pyi`, `.tfvars`, `.markdown` and `.env.*` take directives; a directive comment over several lines is read; a link-form directive with spaces is written `[text](<ds:…>)` and one markdown cannot read is reported instead of dropped; quoted link values lose their quotes; `span=` works in YAML, TOML and HCL; `type=` and ids are validated; two inline facts in one sentence, and two remote defs picking different values on one line, are separate blocks.
- Render, context and commands: `render --at` takes everything from that commit; `ds:block at=` renders its snapshot; `.txt` pages lose the bare directive line; `context` shows block-position citations and the same diff as `check`; `ds lsp` uses the client's workspace root; `github comment --dry-run` needs no token; `review --out` works without `--ai`; `undo --list` labels moved committed writes correctly; a renewed claim clears at once; `ack --from-commit` records `note=`; `refresh` with nothing to record writes nothing.
- Secrets, URLs and runs: 1Password addresses resolve through the shipped `ds-resolve-onepassword`; Vault addresses lose their `vault:` prefix; a signed-out `op` is `unverifiable`; a `local=true` def is checked where its file exists; `ds:run` honours `timeout=` and `expect=<status>` and requires a zero exit; `ds:url` honours `expect=`, and a link that gets no answer is `unverifiable` and never cached as dead.
- Cross-repo: `ds publish` commits a local git index; `stale_after_commits` and `index` take effect; another repository's citation says which repository and where to ack it; a git-hosted index is cloned by the first command that needs it; repo-mode copies of another repository's block carry its body and are not printed twice.
- A repo-mode copy whose code is all still in its block, only moved (a def added inside it, a `lines=` window shifted), is `stale` for `ds refresh`, not `tampered`.
- `cli.WithName` reaches `version`, remedies and doctor rows; the Hugo status partial lists only the current page's references; the spec's findings table, library API, scan-skip wording and `ds init` docs default match the code.

### Upgrading

Two changes may report once after upgrading. Repo-mode copies are accepted with their old links, so nothing is needed there; run `ds refresh` when convenient to make their links relative to the page. If a nightly job runs `ds check --resolve`, add `[resolve] enabled = true` to `.ds/config.toml`, or it will check nothing.

## [0.1.4] - 2026-10-01

A release of the `ds` binary and the `cli` module (0.1.4), the library (0.1.2) and the tree-sitter tier (0.1.1). Read Upgrading before you scan with it: Go blocks containing strings report once.

### Changed

- docsync is now licensed under the Apache License 2.0 instead of the GNU AGPL v3.0, for every module, the VS Code client and the Docusaurus plugin. Releases up to and including ds 0.1.3, cli 0.1.3 and the library 0.1.1 remain available under the AGPL terms they were published with.

### Fixed

- In Go, the text inside a string literal was not part of a block's hash: changing `errors.New("account locked")` to `"account suspended"`, a URL, a query or a flag name inside a function never flagged the sentences citing it (bug 22). Go's grammar gives a string no node for its text, and the hash was built from the parse tree's leaves. Text that no child node covers is now hashed with its node. Only Go interpreted strings (`"…"`) were affected; raw strings, runes, and strings in TypeScript, JavaScript, Python and SQL already hashed their text, and their hashes do not change. The extraction rule stays at 1. See Upgrading.
- The extraction cache is now keyed on the `ds` build as well as the rule, prefix, tiers and line limit. Keyed on the rule alone, an upgrade that fixes what a hash covers without changing the rule kept serving unchanged files their old hashes until `ds check --full`, so the fix appeared to apply only to files someone had edited. Each new build re-extracts once.
- `policy.require_doc` and the unmarked view of `ds report` did nothing for Go, TypeScript, TSX, JavaScript, Python or SQL files: they only looked at files the heuristic code tier had read, and the standard `ds` reads those languages with their tree-sitter grammars. A file now counts by its extension, whichever tier read it.
- `ds github comment`: the closing fence of each block diff was written on the diff's last line, so the code block never closed and everything after it in the comment rendered as code.

### Upgrading

After upgrading, a Go block whose code contains a `"…"` string has a new hash although its code did not change, so `ds check` reports each citation of it once, as a change. Re-ack those after confirming the code really is unchanged. A coding agent can do it with this prompt:

```text
docsync was upgraded, and its hash now includes the text inside Go string
literals. Citations of Go blocks that contain a "..." string may be reported
once even though their code did not change. Clear only those, and nothing else:

1. Run `ds scan`, then `ds check --json`. Work only on findings whose block
   file ends in .go.
2. For each one, find when that citation was last acked: `ds audit --id <id>`
   prints one line per ack, starting with its time. Take the commit the
   repository was at then: `git log -1 --before=<time> --format=%H`. If the id
   was never acked, or that prints nothing, use the last commit that changed
   .ds/refs.tsv. Compare the
   block's lines at that commit with the working tree: `ds locate <id>` gives
   the file and line range, and `git diff <commit> -- <file>` the changes.
3. If those lines did not change, the finding is the upgrade. Read the citing
   sentence once to be sure it is still true, then run
   `ds ack <id> --doc <doc> --line <line> --note "re-ack after the Go string hashing fix; code unchanged"`.
4. If the lines did change, this is a real finding. Do not ack it: fix the
   sentence, or report it, as you would any other.
5. Run `ds check` again and report what you acked and what you left.
```

## [0.1.3] - 2026-10-01

A release of the `ds` binary and the `cli` module (0.1.3), and of the library (0.1.1), which adds `run.shell` to the config. The end-to-end matrices now run on Linux and on Windows under Git Bash as well as on macOS.

### Added

- `[run] shell` names the shell that `ds:run` commands and the `[review]` command run under (default `sh`).

### Fixed

- When the shell is not on PATH, `ds check --run` and `ds review --ai` now stop with an error naming the shell and the `[run] shell` key. Before, every command was recorded as a failed run with no output, which on a Windows machine without Git for Windows read as the runbook being broken.
- On Windows, a `ds check` running beside a `ds scan` could fail, or make the scan fail, because Windows refuses to replace a file another process has open at that instant. Reads and writes under `.ds/` now wait that out.
- A `ds:run file=` script is handed to the shell as an argument of its own instead of inside a quoted command line.

## [0.1.2] - 2026-09-30

A release of the `ds` binary and the `cli` module, after the first run of docsync on Windows.

### Added

- A `ds` build for Windows on ARM64 (`ds_v0.1.2_windows_arm64.zip`), installed by `install.ps1`.
- `.github/workflows/windows.yml` and `scripts/windows-smoke.ps1`: every module's tests, a build from source, and the published release, run on Windows amd64 and ARM64. Manual trigger.

### Fixed

- On Windows, a rooted path with no drive letter (`/etc/hosts`, `\etc\hosts`) was accepted as a path inside the repository, and `ds:run file="/etc/hosts"` was not refused. A path argument is now taken from the root, and `file=` is judged by its text on every platform: a leading slash of either kind, or a drive, is refused.
- On Windows, a file sitting where `.ds/blocks` or `.ds/ledger` should be was read as an empty store instead of an error.

## [0.1.1] - 2026-09-30

A release of the `ds` binary and the `cli` module; the library and the tier modules stay at 0.1.0.

### Changed

- The `ds` release archives carry the five secret-resolver plugins (`ds-resolve-aws`, `ds-resolve-gcp`, `ds-resolve-github`, `ds-resolve-onepassword`, `ds-resolve-vault`) beside `ds`, and `install.sh` / `install.ps1` install them with it. In 0.1.0 they could only be built with `go install`, so `ds check --resolve` needed a Go toolchain.

## [0.1.0] - 2026-09-30

The first release: the root library `github.com/ubgo/docsync`, the tier modules `ext/structured`, `ext/treesitter` and `ext/records/sqlite`, the `cli` module, and the `ds` binary for darwin/arm64, linux/amd64, linux/arm64 and windows/amd64, installable with `install.sh` / `install.ps1` or `go install`. `ds` carries the tree-sitter tier and is built with cgo, so there is no prebuilt Intel Mac binary; `go install github.com/ubgo/docsync/cli/cmd/ds@v0.1.0` with a C compiler covers it. The five secret-resolver plugins install with `go install` as well.

### Added

- `install.sh` (macOS, Linux) and `install.ps1` (Windows): download the newest `ds` release for the machine, verify it against the release's `checksums.txt`, and install it. Generated by `volt gen install cli/cmd/ds`.
- `ds version` reports the release version a release build stamps (`-X github.com/ubgo/docsync/cli.releaseVersion=…`), instead of the pseudo-version the toolchain records for a build from a checkout.
- A spec-promise gate: every sentence in `docs/SPEC.md` saying "never", "always", "refuses", "must not", "deterministic" or "hard error" is listed in `testdata/promises.txt`, and each testable one is pinned by a test naming it (`promise:<id>`). `task` fails on an unlisted promise, a listed one reworded, or one with no test.
- The root library, the `ds` CLI, and the structured (YAML, TOML, HCL), syntax (tree-sitter: Go, TypeScript, TSX, JavaScript, Python, SQL) and document (Markdown, HTML, AsciiDoc, reStructuredText) tiers.
- An MCP server (`ds mcp`) and a language server (`ds lsp`), with a VS Code client.
- A GitHub Action and workflows, pre-commit hooks, a Docusaurus plugin, and a Hugo module.
- Workspaces: citations across repositories through a published index, with a committed snapshot so checks are reproducible offline.
- Sentence-level acks: a cited sentence rewritten since its ack is reported, with the old and new wording. Every ack records the extraction rule it was made under (rule 1). An ack made under another rule still holds when its sentence is unchanged; only when both the rule and the wording differ is it reported, once, as recorded under another rule rather than as a rewrite.
- `--dry-run` for `ack` and `publish`; root discovery from any subdirectory.
- `task perf`, measuring the performance targets on a generated 100,000-file repository.
- Group entries and body members are defs in their own right: an entry of `const ( … )`, `var ( … )` or `type ( … )`, a struct field, an interface method, an import, a TypeScript interface property, enum member or object-literal property (`server.port`). Each binds its own line and is found by `path#Name`.
- `ds repair`: finds directives an older build wrote as bare lines into files that cannot hold one, comments them, or deletes them where the format has no comment syntax. Prints by default; `--apply` writes, and `ds undo` reverses it byte for byte, deletions included.
- `ds version` and `ds --version`: the commit a binary was built from and whether that checkout was dirty.
- Go module and workspace files (`go.mod`, `go.work`) and Pkl take `//` comments.

### Changed

- The extraction cache skips reading files whose size and modification time are unchanged; `check --full` still reads every file.
- `ds init` and the shipped GitHub workflows trigger manually. Their automatic triggers are in each file's header, to paste back where Actions minutes are not metered.
- `ds adopt` resolves a relative link against the page holding it, then the repository root, and leaves a heading link into another page alone.
- An id minted for a block the lookup cannot name takes the scanning tier's name (`server-port-…`) rather than the file's.

### Security

- The fork guard on `check --run` and `check --resolve` failed open: a deleted fork, a pull request whose head repository was missing, or an event file that could not be read or parsed was treated as not a fork, so committed commands ran. It now fails closed, and `review --ai`, which runs the committed `[review]` command and had no guard, has the same one.
- A secret's value is never shown. `secret=true` on a line holding a real value, or any line in a `[secret] paths` file, was printed by `render`, `facts`, `context`, `read`, `blame`, `triage`, `review`, `export hugo`, the MCP `read`, `facts`, `context` and `check` tools, and the change diff in `check --json` — sixteen paths in all. Only an address (`${{ secrets.X }}`, `op://…`, an AWS or GCP secret resource, a `vault:` path) is shown now; everything else is withheld with its hash kept, so a rotated secret is still reported.

### Fixed

- The pre-commit hooks file moved to the repository root. Under `integrations/pre-commit/`, the documented `repo: https://github.com/ubgo/docsync` entry could not find it, since pre-commit reads it only at the root.
- `ds def` and `ds adopt` refuse to mint a def in a file under `[scan] generated`, naming the file; the spec promised this, but both wrote the directive, and the next code generation erased it while every sentence citing it stayed. An id already in such a file is still returned.
- The spec said the `whitespace` class is never reported; a re-indent is silent only where the syntax tier hashes the token stream (Go and the other tree-sitter languages). In a file with no grammar, indentation is content and a re-indent is a body change. The spec now says so.
- A ledger or refs file recorded under a newer extraction rule was compared silently: unchanged blocks reported drift, and `ds scan` rewrote the files under the older rule. Every command now refuses it, naming the file and both rules, and `doctor` reports `FAIL`. An older rule is a warning instead, since that is the upgrade path. Repositories that ran a pre-release build hold `extract=2` to `4` for what is now rule 1; setting `extract=1` on the first line of `.ds/ledger.tsv` and `.ds/refs.tsv` and running `ds scan` fixes them.
- `ds doctor` exits non-zero when any row is `FAIL`; it exited 0 even with a ledger it could not read.
- A duplicated id's remedy names `ds def --fix` instead of the generic "fix the directive".
- JSON-RPC responses with no result now carry `"result": null`, as the protocol requires.
- A change made since the last scan is diffed against its previous body as the scan produced it, not the raw lines of the old file: a config value changed from 443 to 8443 is shown as `-443 / +8443`, not as the whole old line with its directive comment against the new value.
- A directive is never written into a file that cannot hold one. `ds def` and `ds adopt` used to insert it bare into any type they did not recognise, which broke a Go workspace's build and would have left JSON unparseable; they now refuse, name the file type, and change nothing. `ds scan` reports a bare directive already sitting in such a file.
- A directive is never written where it would not bind: the edit is checked in memory with the scanning tier first, so `--dry-run` predicts a refusal instead of a later scan finding it.
- A directive above a grouped constant bound the next top-level declaration instead (bug 18), and a directive above any construct a grammar did not recognise did the same. It now binds the declaration below it or reports.
- Two ids bound to the same block are reported.
- An unknown `stability=` is reported instead of silently becoming `stable`.
- A typed standalone Go constant or variable (`const X T = v`) can be found by name (bug 19).
- A YAML key containing a colon, such as a Taskfile task `wfsys:up`, can be addressed.
- An ack from another extraction rule no longer asks for its unchanged sentence to be re-read (bug 17).
- The extraction cache is keyed on the directive prefix and the extractor tiers as well as the rule, so changing either is never served stale results.

<!-- Release process: move the [Unreleased] entries under a new version heading, date it (## [1.2.0] - YYYY-MM-DD), tag the release, and update the link references below. -->

[Unreleased]: https://github.com/ubgo/docsync/commits/main
