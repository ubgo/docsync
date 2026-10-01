# docsync guide

The user guide for docsync: what it does, how to set it up, and the reference for every directive, command, config key and integration. Every command and every output shown in these pages was run against the `ds` binary; where the binary does less than [the spec](../SPEC.md) describes, the pages say what it does today.

## Start here

| Page | Read it when |
| --- | --- |
| [docsync at a glance](how-it-works.md) | you are deciding whether docsync fits: the problem, the vocabulary, one full round of the loop with real output, and a def in every language on one screen |
| [Getting started](getting-started.md) | you are setting it up: install, `ds init`, your first defs and citations, a change caught by `ds check`, the fix and the ack, committing `.ds/`, adopting existing links |

## Writing docs and defs

| Page | What it covers |
| --- | --- |
| [Directives](directives.md) | every directive (`ds:def`, `ds:block`, `ds:cfg`, `ds:claim`, `ds:url`, `ds:run`, `ds:table`, `ds:chain`), every argument, ids and labels, `pick`, environments, deprecation, translations |
| [Languages](languages.md) | per language and format: the comment a def goes in, what `ds def file#Name` can address, exactly which lines a def binds |

## Running it

| Page | What it covers |
| --- | --- |
| [Command reference](cli.md) | every `ds` command with its flags, exit codes and a real run, grouped by job |
| [Configuration](configuration.md) | every key in `.ds/config.toml`, its default, and how the file is validated |
| [CI](ci.md) | `ds check` as a gate in GitHub Actions, GitLab and pre-commit; frozen checks; PR comments; notifications |
| [Troubleshooting](troubleshooting.md) | `ds doctor`, every finding state and how to clear it, common mistakes, `ds undo`, FAQ |

## Beyond one repository

| Page | What it covers |
| --- | --- |
| [Cross-repo workspaces](cross-repo.md) | a docs repo and a code repo citing each other: `ds publish`, `ds sync`, frozen and syncing checks, branches, repo mode |
| [Secrets, runs and URLs](secrets-and-runs.md) | `ds:run` (and `[run] shell`), `ds:url`, secret chains and `--resolve`, writing a resolver plugin |
| [Agents and editors](agents.md) | `ds mcp` and its tools, `ds lsp`, `ds map` and `ds context` budgets, the VS Code client |
| [Integrations](integrations.md) | Hugo, Docusaurus, VS Code, pre-commit and the GitHub Action |
| [Using docsync as a Go library](library.md) | embedding the checks in a Go program, the no-writes contract, building your own `ds` |

The normative design, with a conformance fixture behind every rule, is [docs/SPEC.md](../SPEC.md).
