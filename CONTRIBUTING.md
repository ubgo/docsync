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

## Commits

- Branch off `main` with a short descriptive name (`fix/...`, `feat/...`, `docs/...`).
- Write the subject as a plain sentence saying what the change does ("Follow a moved citation's ack through a rescan"), and use the body for why.
- One logical change per commit where practical.

## Pull request checklist

- [ ] `task` passes locally.
- [ ] New behaviour has tests, and each was seen to fail without the change.
- [ ] `docs/SPEC.md`, the README and `CHANGELOG.md` are updated for user-facing changes.
- [ ] If a sentence docsync tracks in its own docs changed, it was re-read and acked (`task dogfood` names it).

## Changelog

User-facing changes go under `[Unreleased]` in [CHANGELOG.md](CHANGELOG.md), following [Keep a Changelog](https://keepachangelog.com/).

## Questions

Open an [issue](https://github.com/ubgo/docsync/issues). We're happy to help you land your first contribution.
