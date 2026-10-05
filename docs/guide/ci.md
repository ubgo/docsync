# Running docsync in CI

This page shows how to make `ds check` a gate: in GitHub Actions, in GitLab or any other CI that runs shell commands, and before a commit with pre-commit. It is for whoever owns the repository's pipeline and wants a pull request that changes documented code to say which sentences now need a look.

## What CI runs

The whole gate is one command. `ds check` exits `0` when nothing is wrong, `1` when there is at least one error-severity finding, and `2` on a usage or setup error, so any CI system can fail a job on it without parsing anything.

The examples on this page use a repository whose `store/session.go` defines `sess-ttl` (a constant, changed from 30 to 45 since it was cited) and `sess-save` (a function), both cited from `docs/sessions.md`.

<!-- doctest
git init -q -b main .
ds init
mkdir -p store docs
printf 'package store\n\n// ds:def id=sess-ttl owner=@auth stability=stable\nconst SessionTTL = 30\n\n// ds:def id=sess-save owner=@auth stability=stable\nfunc SaveSession(id string) error {\n\treturn nil\n}\n' > store/session.go
printf '# Sessions\n\nSessions expire after [30](ds:cfg?id=sess-ttl) minutes.\n\nEvery session write goes through SaveSession:\n\n\074!\055\055 ds:block id=sess-save \055\055\076\n' > docs/sessions.md
ds scan
git add -A
git commit -qm init
sed -i.bak 's/= 30/= 45/' store/session.go
rm -f store/session.go.bak
-->

```console
$ ds check; echo $?
docs/sessions.md
  3	error    unacked            sess-ttl changed (value) since this sentence was first cited
      | -30
      | +45
      still true: ds ack sess-ttl --doc docs/sessions.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/sessions.md:3, then ack
1 error, 1 ok
1
```

`ds check --json` prints the same findings as a JSON report (`json_format: 1`, a `summary`, a `findings` array and an `exit_code`), which is what the GitHub integration below consumes. `--strict` makes warnings fail the run as well.

`ds check` does not need a `ds scan` first: it compares the working tree with the committed ledger, so a fresh checkout is enough.

## Installing ds on a runner

Two ways, both verified against the published `v0.1.3` release.

With a Go toolchain (Go 1.26 or newer):

```sh
go install github.com/ubgo/docsync/cli/cmd/ds@latest
```

Without one, the release installer downloads the prebuilt binary and checks it against the release checksums. `INSTALL_DIR` keeps it out of `/usr/local/bin` so no `sudo` is needed:

```sh
curl -fsSL https://raw.githubusercontent.com/ubgo/docsync/main/install.sh | INSTALL_DIR=$HOME/.local/bin sh
```

Pin a release in CI with `@v0.1.7` (Go) or `VERSION=v0.1.7` (installer) if you do not want `latest` to change under you.

## Frozen by default under CI

When the environment variable `CI` is set, which every major CI system does, `ds check` behaves as if `--frozen` were given. This matters only for a repository that cites blocks from another repository through a workspace (see [Cross-repo](cross-repo.md)):

- `--frozen` resolves foreign blocks from the committed `.ds/foreign.tsv` snapshot and never fetches the workspace index, so the same commit gives the same answer on any machine at any time. A build cannot go red because another repository published something.
- `--sync` fetches the workspace index first even under CI, for a job whose purpose is to notice upstream changes (a nightly, for example).
- Asking for both is a usage error:

```console
$ ds check --frozen --sync; echo $?
ds: usage: --frozen and --sync ask for opposite things
2
```

In a repository with no workspace configured, `--frozen` changes nothing; `CI=true ds check` and `ds check` give the same result.

Taking upstream changes is a deliberate act: run `ds sync` locally, commit the updated snapshot, and the resulting findings show up in that pull request rather than on whoever pushes next.

## GitHub Actions

The repository ships a composite action at `ubgo/docsync/integrations/github`. It installs `ds`, runs `ds check --json`, and on a pull request posts the findings with `ds github comment`: one comment per doc, each finding linked to its doc line, with the block diff folded underneath. Later runs edit the same comments, and a doc whose findings cleared is marked resolved. The job exits with the check's code, so making it a required status check blocks the merge on errors.

### A complete workflow

Save as `.github/workflows/docsync.yml`:

```yaml
name: docsync
on:
  pull_request:
    types: [opened, synchronize, reopened, labeled]
permissions:
  contents: read
  pull-requests: write
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: ubgo/docsync/integrations/github@main
```

`fetch-depth: 0` (full history) matches the shipped templates; `pull-requests: write` lets the action post comments. The `labeled` event type is only needed for the ack label described below.

### Action inputs

| Input | Default | What it does |
|---|---|---|
| `version` | `latest` | version of `ds` installed with `go install` (a tag or `latest`); ignored when `source` is set |
| `source` | empty | path to a checked-out docsync repository to build `ds` from, for pinning to a commit |
| `go-version` | `1.26` | Go version for the install step |
| `ack-label` | `docs-acked` | pull request label that acks every finding for the reviewer who applied it; empty disables |
| `args` | empty | extra arguments for `ds check`, for example `--full` or `--strict` |

```yaml
      - uses: ubgo/docsync/integrations/github@main
        with:
          version: v0.1.7
          args: --strict
```

On any event other than `pull_request` the action skips the comment step and runs a plain `ds check`, so the same step works on a push or a manual run.

### The ack label

Some pull requests change behaviour and its documentation together, and every finding they raise is expected. A reviewer who has read them can apply the `docs-acked` label: the run triggered by that `labeled` event acks every finding on the reviewer's behalf (the actor is recorded as their GitHub login) and passes, printing `N findings acked under label docs-acked; commit .ds/acks.tsv`.

Two things to know:

- Only the act of applying the label acks. A push made afterwards is checked again, and the run says `label docs-acked is on the pull request, but it acks only when it is applied: re-apply it to accept these findings`.
- The shipped action then commits `.ds/acks.tsv` to the pull request branch, crediting the reviewer in the message. That push needs `contents: write` and a checkout of the pull request head (`ref: ${{ github.event.pull_request.head.sha }}`), both of which `docsync-pr.yml` sets; set the action's `commit-acks` input to `false` to commit them yourself. A fork's branch is never pushed to: there the author records the same acks locally (`ds ack <id> --doc <doc> --line <n> --note '…'`) and pushes `.ds/acks.tsv`.

### Previewing the comment

`ds github comment` reads a saved report and needs `GITHUB_TOKEN`, `GITHUB_REPOSITORY`, and a pull request number (`--pr`, or the event file at `GITHUB_EVENT_PATH`). `--dry-run` prints the comment bodies instead of posting them. Save the report outside the repository, as the action does: a JSON file inside it is scanned like any other file, and the sentences quoted in it read as citations.

~~~console
$ ds check --json > ../report.json
$ GITHUB_TOKEN=x GITHUB_REPOSITORY=acme/api ds github comment --report ../report.json --pr 7 --dry-run
<!-- docsync:doc=docs/sessions.md -->
### docsync: `docs/sessions.md`

- [line 3](https://github.com/acme/api/blob/8fcbb8a/docs/sessions.md#L3) **unacked** `sess-ttl`: sess-ttl changed (value) since this sentence was first cited
  - still true: `ds ack sess-ttl --doc docs/sessions.md --line 3 --note '…'`; otherwise: edit the sentence at docs/sessions.md:3, then ack

  <details><summary>block diff</summary>

  ```diff
  -30
  +45
  ```
  </details>

1 error, 1 ok
~~~

The token is required even for `--dry-run`, though nothing is sent.

### The workflow templates

`integrations/github/workflows/` holds three jobs ready to copy into `.github/workflows/`:

| File | When | What it runs |
|---|---|---|
| `docsync-pr.yml` | pull requests | the action above |
| `docsync-publish.yml` | default branch, after merge | the Go tests through `gotestsum`, which writes a JUnit report, then `ds publish --tests` with it, which writes this repository's ledger and test outcomes into the workspace index (see [Cross-repo](cross-repo.md)); in another language, swap the test step for your runner's JUnit output |
| `docsync-nightly.yml` | on a schedule | `ds check --run --resolve` with credentials, then `ds notify`; each flag acts only where the repository consents in `.ds/config.toml` (`[run] enabled = true`, `[resolve] enabled = true`) |

All three ship with `on: workflow_dispatch:` only, so they run when started from the Actions tab and never spend minutes on their own. Each file's header comment carries the trigger lines to paste back (`pull_request`, `push` to `main`, or a `schedule`) once you want them automatic.

`ds init` also writes a minimal workflow to `.ds/ci-github.yml`, with the header `# Copy into .github/workflows/docsync.yml`. It installs `ds` with `go install` and fails the job on `ds check --json`, without pull request comments; it too is `workflow_dispatch:` only until you paste its triggers back.

### Pull requests from forks

A pull request from a fork can change anything in the repository, including `ds:run` commands, resolver configuration, and the `[review]` command. `ds` therefore refuses to execute any of them when the GitHub event says the head repository is not the base repository. Here `event.json` is a `pull_request` event from `someone/api` against `acme/api`:

<!-- doctest
printf '{"repository":{"full_name":"acme/api"},"pull_request":{"head":{"repo":{"full_name":"someone/api"}}}}' > event.json
-->

```console
$ GITHUB_EVENT_PATH=event.json ds check --run; echo $?
ds: --run and --resolve are disabled on pull requests from forks
2
```

The same applies to `--resolve` and to `ds review --ai`. The check fails closed: an event file that cannot be read or parsed, or a pull request event with no head repository (what GitHub sends after the fork is deleted), is treated as a fork. A plain `ds check` on a fork's pull request runs normally. Keep `--run` and `--resolve` for the nightly job, which runs with the repository's own code.

## GitLab and other CI systems

Anything that runs a shell works. A GitLab job:

```yaml
docsync:
  image: golang:1.26
  script:
    - go install github.com/ubgo/docsync/cli/cmd/ds@latest
    - ds check
```

GitLab sets `CI=true`, so the check is frozen as described above. To keep the report as an artifact, swap the last line for:

```yaml
    - ds check --json > docsync.json || { cat docsync.json; exit 1; }
```

and add `artifacts: { paths: [docsync.json], when: always }`. The same two lines, with either install method, are the whole job on Jenkins, Buildkite, CircleCI, or a plain cron script.

Use `--dir` when the job's working directory is not the repository root: `ds --dir path/to/repo check`.

## Pre-commit hooks

The repository root has a `.pre-commit-hooks.yaml` with two hooks. Both run the `ds` on your `PATH` (`language: system`), so install `ds` first.

| Hook id | Runs | Shows |
|---|---|---|
| `docsync-impact` | `ds impact --staged` | the sentences, pages, and owners that the staged changes will flag |
| `docsync-literals` | `ds report --literals` | fact values typed into docs by hand where a `ds:cfg` cite should carry them |

`.pre-commit-config.yaml`:

```yaml
repos:
  - repo: https://github.com/ubgo/docsync
    rev: ds/v0.1.7
    hooks:
      - id: docsync-impact
        verbose: true
      - id: docsync-literals
        verbose: true
```

Both hooks are informational: they always exit `0`, and pre-commit hides the output of a passing hook, which is why `verbose: true` is there. With a staged change to a cited constant:

<!-- doctest:skip pre-commit clones the hook repository from GitHub, which needs the network -->
```console
$ pre-commit run
docsync impact of staged changes.........................................Passed
- hook id: docsync-impact
- duration: 0.21s

docs/sessions.md (1)
  3  unacked  sess-ttl
owner @auth: 1

docsync typed literals...................................................Passed
- hook id: docsync-literals
- duration: 0.12s

literals (0)
```

`ds report --literals` only reports values of three characters or more, so a doc that types `8081` next to a defined port is caught while `30` is not:

<!-- doctest
printf '\n// ds:def id=api-port\nconst Port = 8081\n' >> store/session.go
printf '# Ops\n\nThe API listens on 8081.\n' > docs/ops.md
-->

```console
$ ds report --literals
literals (1)
  docs/ops.md:3  "8081" is api-port; cite it
```

If you want the commit itself to fail on findings, use a local hook running `ds check` instead.

## Notifying owners: ds notify

`ds notify` sends the open error findings to their owners, once each, and again marked as escalated when one is still open after `notify.escalate_after`. Without a channel configured it prints the digest, which is what a cron job mails:

```console
$ ds notify --dry-run
docsync: no previous notifier state; 1 item open (this is the full list, not new problems)

docsync: @auth
  docs/sessions.md:3  unacked  sess-ttl changed (value) since this sentence was first cited
```

The built-in channel is a Slack incoming webhook. In `.ds/config.toml`:

```toml
[notify]
slack = "$DS_SLACK_WEBHOOK"
escalate_after = "7d"
```

`$VAR` is expanded from the environment, so the webhook URL stays a CI secret. `escalate_after` takes a whole number with `m`, `h`, `d`, or `w`.

Its memory of what it already sent lives in `.ds/notified.json`, which is machine-local and gitignored. On a fresh CI checkout that file is absent, so dedupe never holds and escalation never fires; `ds notify` says so in its first line, and `CI=true ds doctor` reports it:

```console
$ CI=true ds doctor
…
notify         WARN  no state on this runner; dedupe and escalation will not work. Cache .ds/notified.json between runs (see the nightly workflow template)
workspace      ok    none; this repository is its own workspace
update         …
```

The nightly template restores the file with `actions/cache` before running `ds notify`. Do the same on any CI that runs it.

## ds:run on Windows runners

`ds check --run` executes `ds:run` commands under `sh` (`sh -c <command>`), looked up on `PATH`. On Windows, `sh` comes from Git for Windows; make sure the directory holding its `sh.exe` is on the runner's `PATH` for the step that runs `ds check --run`. When the shell is missing (below, `[run] shell` names one that is not installed), the check stops before running anything:

<!-- doctest
printf '\n\074!\055\055 ds:run cmd="echo hi" expect=ok \055\055\076\n' >> docs/sessions.md
printf '\n[run]\nenabled = true\nallow = ["docs/**"]\nshell = "nosuchsh"\n' >> .ds/config.toml
-->

```console
$ ds check --run
ds: the shell is not on PATH: nosuchsh (on Windows, Git for Windows provides sh; or name another shell with [run] shell in .ds/config.toml)
```

If your commands are written for another shell, name it:

```toml
[run]
shell = "pwsh"
```

docsync never picks a different shell by itself, because the same command text means different things to different shells. See [Secrets and runs](secrets-and-runs.md) for `ds:run` itself.
