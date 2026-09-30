# Security Policy

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Report them privately instead:

- Use **[GitHub Security Advisories](https://github.com/ubgo/docsync/security/advisories/new)** (preferred), or
- Email **khanakia@gmail.com** with the details.

Please include a description of the vulnerability and its impact, steps to reproduce (a proof of concept if possible), and the affected version and environment.

We will acknowledge your report within a few days and keep you updated on the fix. Please allow a reasonable window for a release before any public disclosure; we are happy to credit you in the advisory.

## What is in scope

docsync reads repository text and, in some commands, runs programs and talks to the network, so these areas matter most:

- **Secret values.** Blocks under `[secret] paths` must never be written to the cache, the journal, the published index, or any output. A path by which one leaks is a vulnerability.
- **Prompt injection through MCP.** Every payload `ds mcp` returns is fenced behind a `data:` delimiter, and `run`, `resolve`, `undo`, `publish` and `adopt` are not exposed to agents. A way around either is a vulnerability.
- **Command execution.** `ds:run` executes only where `run.allow` permits, and never for a fork's pull request; process plugins are invoked by argument vector, never through a shell.
- **Path handling.** A `file=` def, a symbolic link, or a path argument that reaches outside the repository.

## Supported versions

docsync has no tagged release yet; security fixes land on `main`. Once releases begin, fixes will be applied to the latest one.
