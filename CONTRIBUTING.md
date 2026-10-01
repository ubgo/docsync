# Contributing to docsync

Thanks for your interest in improving **docsync**. This guide covers setup, the rules the code holds itself to, and what a pull request needs before it can merge.

By participating, you agree to abide by the [Code of Conduct](CODE_OF_CONDUCT.md).

## Getting started

Requirements: Go 1.26, [Task](https://taskfile.dev), `gofumpt`, `staticcheck`, and a C compiler for the tree-sitter tier. The root `go.work` joins the repository's modules, so edits across them build together without a release. Node is needed for the JavaScript integrations, and Hugo only for the Hugo end-to-end case (it is skipped when absent).

```sh
git clone https://github.com/ubgo/docsync.git
cd docsync
task install   # build ds and its plugins into bin/, symlinked onto PATH
task           # the full gate: format, vet, staticcheck, race tests, 100% coverage, dogfood, end-to-end, cross-builds
```

## Ways to contribute

- **Report a bug** — open an issue with the bug template: the commands you ran, what `ds` printed, and what you expected. A minimal repository that reproduces it is the most useful thing you can attach.
- **Request a feature** — open an issue with the feature template; describe the problem first, then the change you have in mind.
- **Send a pull request** — for anything beyond a small fix, open an issue first so we can agree on the approach before you write code.

## The rules the code holds itself to

These are enforced by `task`, and a pull request that works around one will not be merged.

- **The spec is normative.** Read [docs/SPEC.md](docs/SPEC.md) before changing behaviour. Every rule has a fixture under `testdata/conformance`; where the spec and a fixture disagree, the fixture wins and the spec is fixed in the same change.
- **The root module imports the standard library only**, and never writes a file, reads the environment, or opens the network. Anything that needs a dependency lives in a sub-module (`cli/`, `ext/...`).
- **Every package holds 100% statement coverage.** Reach it by testing the behaviour — never by deleting a branch or excluding a file.
- **A test is evidence only once it has been seen to fail.** Break the code it guards, watch it fail, then restore it.
- **Closed sets are named constants** with a `*Values` list beside them; no bare state, verb, key or severity strings at use sites.
- **A new finding state** needs an entry in `check.StateValues`, the severity table, a remedy, and a conformance fixture.
- **Behaviour that spans commands** (git history, merges, several repositories) gets a case in `scripts/e2e/`.
- **No suppressed diagnostics** — no `//nolint`, no skipped tests, no loosened assertions. Fix the cause.

`task conformance:update` regenerates expected outputs; read the resulting diff line by line, because a changed expectation is a changed rule and needs a reason in the commit message. The JSON goldens under `testdata/golden` are a public contract: changes must be additive.

## How docsync is tested

`task` runs every check below; each one exists because a class of bug got past the others. Most of the 81 bugs fixed in 0.1.5 went unnoticed through full unit coverage, because the spec, the code and the tests shared one author's assumptions: every check agreed with every other check. The checks after the first two each take "correct" from somewhere outside the code's author.

| Check | Command | What decides "correct" | What it catches |
|---|---|---|---|
| Unit tests at 100% statement coverage | `task` (`cover`, `cli`, `ext`, ...) | the author's expectation | a function breaking its contract |
| Conformance fixtures | `task conformance` | a fixture per rule in the spec | a finding state changing meaning |
| End-to-end matrices | `task e2e` (`scripts/e2e/*.sh`) | sequences of real commands on real git repositories | bugs across commands: ack, scan, commit, merge, more than one repository |
| Guide examples | `task docs:test` | the user guide, read as a user would | the binary and its documentation disagreeing; behaviour wired differently in the shipped `ds` than in a test |
| One-character hash property | `cli/cmd/ds/hashprop_test.go` | a rule over every input | any code character a block's hash leaves out, in every language `ds` ships |
| Determinism sweep | `scripts/e2e/determinism.sh` | the same command run twice | output whose order depends on random id suffixes or on timing |
| Round trips | `scripts/e2e/round-trips.sh` | the inverse operation | `def` then `undo`, a rename and its reverse, `refresh` twice, a dry run against the real run |
| Upgrade | `scripts/e2e/upgrade.sh` | the previous release, run on the same repository | a change that makes existing acks report drift after upgrading |
| Source consistency | `config/consistency_test.go`, `cli/messages_test.go`, `spec_states_test.go`, `spec_api_test.go` | the source itself | a config key that does nothing, a message naming a key or command that does not exist, the spec's tables drifting from the code |
| Spec promises | `promises_test.go` | every absolute sentence in the spec and README | a promise with no test behind it |
| Real repositories | `task corpus` | code other people wrote | code shaped unlike this repository's fixtures |
| Build comparison | `task extract:diff` | the previous commit's build | a change that silently re-hashes blocks in every repository using docsync |
| Windows and Linux | `.github/workflows/windows.yml` (manual) | other operating systems | paths, file locking, shells, and line endings |

Two habits sit beside them. Features the spec describes but this build does not implement are refused when the config loads, not accepted and ignored, so a gap cannot pass for success. And a bug found anywhere, by a user or a field report, becomes a test of one of these kinds before it is fixed, listed by number in `testdata/fixed-bugs.txt`.

## Writing guide pages

Every page in `docs/guide/` is a test: `task docs:test` runs its examples against the built `ds` and fails when a page shows output `ds` does not print. Run one page with `task docs:test PAGE=../docs/guide/<page>.md`. The runner is `cli/internal/doctest`; its package comment has the full format. In short:

- A code block whose info string carries `file=PATH` is written to that path: ```` ```go file=billing/invoice.go ````. Add `append=true` to append instead.
- A block whose first line starts with `$ ` is a session: each `$ ` line is a command, and the lines after it are the output it prints. `$ cd DIR` and `$ export K=V` carry to later commands.
- `<!-- doctest ... -->` holds setup a reader does not need to see (git commits, fixtures), one command per line; GitHub does not render it.
- `<!-- doctest:skip REASON -->` before a block skips it. The reason is printed and the skip counted. Skip only what cannot run in a sandbox, such as a network download.
- A line that is only `…` matches any number of lines; a line ending in `…` matches any rest of the line. Use it to shorten long output, never to hide a disagreement.

Each page runs top to bottom in one fresh folder, with `bin/ds` first on `PATH`. Commit hashes, timestamps, durations and the folder's path are normalised before comparing. Ids are paired: once the page shows an id where `ds` printed a freshly minted one, later commands and files use the real id, so a page can `$ ds def file#Name` and then ack or cite the id it showed.

When a behaviour change breaks an example, fix whichever side is wrong: the page if the new output is right, the binary if the page is.

## Commits

- Branch off `main` with a short descriptive name (`fix/...`, `feat/...`, `docs/...`).
- Write the subject as a plain sentence saying what the change does ("Follow a moved citation's ack through a rescan"), and use the body for why.
- One logical change per commit where practical.

## Pull request checklist

- [ ] `task` passes locally.
- [ ] New behaviour has tests, and each was seen to fail without the change.
- [ ] A user-visible behaviour is shown in a `docs/guide/` page, and `task docs:test` passes.
- [ ] `docs/SPEC.md`, the README and `CHANGELOG.md` are updated for user-facing changes.
- [ ] If a sentence docsync tracks in its own docs changed, it was re-read and acked (`task dogfood` names it).

## Changelog

User-facing changes go under `[Unreleased]` in [CHANGELOG.md](CHANGELOG.md), following [Keep a Changelog](https://keepachangelog.com/).

## Questions

Open an [issue](https://github.com/ubgo/docsync/issues). We're happy to help you land your first contribution.
