# Getting started

This page takes you from nothing to a repository where `ds check` fails when the code behind a sentence changes: install, `ds init`, your first defs and citations, one full change-check-fix-ack round, committing `.ds/`, CI, and converting the line links you already have. It is for a developer setting docsync up in an existing repository; every command below was run and its output is shown as printed.

If you want the model before the steps, read [docsync at a glance](how-it-works.md) first.

## 1. Install

macOS (Apple silicon), Linux and Windows, amd64 or arm64. On macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/ubgo/docsync/main/install.sh | sh
```

It downloads the newest `ds` release for your machine, checks it against the release's `checksums.txt`, and installs `ds` and its six secret-resolver plugins (`ds-resolve-aws`, `-env`, `-gcp`, `-github`, `-onepassword`, `-vault`) to `/usr/local/bin`, using sudo if it needs to. No Go toolchain is involved. Options go on the `sh` side of the pipe:

```sh
curl -fsSL https://raw.githubusercontent.com/ubgo/docsync/main/install.sh | INSTALL_DIR=$HOME/.local/bin sh   # no sudo
curl -fsSL https://raw.githubusercontent.com/ubgo/docsync/main/install.sh | VERSION=v0.1.6 sh                # a specific release
```

On Windows (PowerShell), which installs to `%LOCALAPPDATA%\ds` and adds it to your PATH:

```powershell
irm https://raw.githubusercontent.com/ubgo/docsync/main/install.ps1 | iex
```

With Go 1.26 or later and a C compiler (`ds` includes the tree-sitter parsers, which are C). This is the way to install on an Intel Mac, for which there is no prebuilt binary:

```sh
go install github.com/ubgo/docsync/cli/cmd/ds@latest
go install github.com/ubgo/docsync/cli/cmd/ds-resolve-aws@latest   # only if you use `ds check --resolve`: -aws -env -gcp -github -onepassword -vault
```

Or download an archive from the [releases page](https://github.com/ubgo/docsync/releases): the `ds/v…` releases carry `ds` and the resolver plugins in one archive for darwin/arm64, linux/amd64, linux/arm64, windows/amd64 and windows/arm64, with a `checksums.txt`.

Check what you are running with `ds version`. On Windows, `ds:run` and `ds review --ai` run their commands under `sh`, which Git for Windows provides; see [Secrets and runs](secrets-and-runs.md) for naming another shell.

## 2. Initialise the repository

Run `ds init` at the repository root:

<!-- doctest
git init -q -b main .
mkdir -p billing docs
printf 'package billing\n\nimport "time"\n\n// GraceDays is how long an unpaid invoice stays open before it is suspended.\nconst GraceDays = 14\n\n// DueDate is when an invoice issued at t must be paid.\nfunc DueDate(t time.Time) time.Time {\n\treturn t.AddDate(0, 0, 30)\n}\n' > billing/invoice.go
printf '# Billing\n\nAn invoice is due thirty days after it is issued.\n' > docs/billing.md
printf 'Suspending an account\n\nCheck the invoice is past its grace period.\nEmail the billing contact.\nSet the account to suspended.\n\nEscalate if the customer disputes it.\n' > docs/runbook.txt
git add -A
git commit -qm billing
-->

```
$ ds init
wrote .ds/config.toml
wrote .ds/ledger.tsv
wrote .ds/refs.tsv
wrote .ds/acks.tsv
wrote .ds/ci-github.yml
wrote .ds/.gitignore
wrote .ds/.gitattributes
next: ds scan, then commit .ds/
```

What it wrote:

- `.ds/config.toml`: which files are code and which are docs. Every file (`**`) is scanned for defs; docs are `docs/**` and `README.md` when the repository has a `docs/` directory, and `**/*.md` when it does not. `testdata`, `node_modules`, `vendor`, `dist` and `public` are excluded, and common generated-file patterns are marked as generated. See [Configuration](configuration.md).
- `.ds/ledger.tsv`, `.ds/refs.tsv`, `.ds/acks.tsv`: the empty ledger of blocks, the reverse index of citations, and the ack log.
- `.ds/ci-github.yml`: a GitHub Actions workflow to copy into `.github/workflows/`; see [step 9](#9-add-the-ci-check).
- `.ds/.gitignore` and `.ds/.gitattributes`: keep machine-local state out of git and let the ack log merge without conflicts; see [step 8](#8-commit-ds).

The generated config:

```
$ cat .ds/config.toml
spec = "1.0"
prefix = "ds"

[scan]
code = ["**"]
docs = ["docs/**", "README.md"]
exclude = ["**/testdata/**", "**/node_modules/**", "**/vendor/**", "dist/**", "public/**"]
generated = ["**/*.pb.go", "**/gen/**", "**/*_gen.go", "**/*_gen.ts"]

[include]
mode = "build"
max_lines = 40

[check]
fuzzy_threshold = 0.8
unacked = "error"

[env]
default = ""
```

`ds doctor` confirms the setup. A `WARN` here only says the repository has no `README.md` yet:

```
$ ds doctor
config          ok    spec 1.0, prefix ds
glob **         ok    3 files
glob docs/**    ok    2 files
glob README.md  WARN  matches no files
extractors      ok    sql, python, javascript, tsx, typescript, go, hcl, toml, yaml, markdown, document, config, code, text
ledger          ok    format 2
git             ok    HEAD 26d6cb2
gitignore       ok    machine-local state excluded
gitattributes   ok    acks.tsv merges without conflicts
blocks          ok    0 bodies, 0 live
notify          ok    no state yet (first notify will create .ds/notified.json)
workspace       ok    none; this repository is its own workspace
```

## 3. Define your first blocks

The example repository has one Go file and a runbook:

```go file=billing/invoice.go
package billing

import "time"

// GraceDays is how long an unpaid invoice stays open before it is suspended.
const GraceDays = 14

// DueDate is when an invoice issued at t must be paid.
func DueDate(t time.Time) time.Time {
	return t.AddDate(0, 0, 30)
}
```

**By symbol.** Name the file and the declaration. `ds def` mints an id, writes the directive above the declaration, and prints the id. Flags such as `--owner`, `--stability`, `--tags` and `--desc` become keys on the directive:

```
$ ds def billing/invoice.go#DueDate --owner @billing
duedate-ctdp6ew3
```

**By line.** Name the file and a line number. The def binds forward from that line exactly as a directive written above it would, so line 6 (the `const`) binds the constant:

```
$ ds def billing/invoice.go:6
gracedays-nbgvqwva
```

The file now reads:

```go
// GraceDays is how long an unpaid invoice stays open before it is suspended.
// ds:def id=gracedays-nbgvqwva
const GraceDays = 14

// DueDate is when an invoice issued at t must be paid.
// ds:def id=duedate-ctdp6ew3 owner=@billing
func DueDate(t time.Time) time.Time {
	return t.AddDate(0, 0, 30)
}
```

The line form also marks files with no declarations. In plain text the def covers the lines below it up to the next blank line:

```
$ ds def docs/runbook.txt:3
runbook-bw796cm3
```

```text
Suspending an account

ds:def id=runbook-bw796cm3
Check the invoice is past its grace period.
Email the billing contact.
Set the account to suspended.

Escalate if the customer disputes it.
```

To cover a fixed number of lines instead, add `span=+N` to the directive by hand (`ds:def id=… span=+2` covers the next two lines). `ds def` has no range form: in `path:N` only `N` is used.

Add `--dry-run` to see the edit without writing it:

<!-- doctest
printf 'package billing\n\n// LateFee is the flat fee added to an overdue invoice, in cents.\nconst LateFee = 2500\n' > billing/fees.go
-->

```
$ ds def billing/fees.go#LateFee --dry-run
latefee-9d8g8sw8
would insert at billing/fees.go:4:
// ds:def id=latefee-9d8g8sw8
```

<!-- doctest
rm billing/fees.go
-->

Each language marks its blocks a little differently; the [one-snippet-per-language table](how-it-works.md#what-a-def-looks-like-in-each-language) and [Languages](languages.md) cover them.

## 4. Cite them from a doc

A citation is a markdown link whose target is a directive. `ds:block` says "this sentence depends on that block"; `ds:cfg` says "this is that one-line value", and `ds render` replaces the link text with the current value:

```markdown file=docs/billing.md
# Billing

An invoice is due thirty days after it is issued; see [`DueDate`](ds:block?id=duedate-ctdp6ew3).

An unpaid invoice is suspended after [14](ds:cfg?id=gracedays-nbgvqwva) days.
```

The citation binds to the sentence around it (or to the whole list item or table cell). That sentence is what an ack approves. The other verbs and keys are in [Directives](directives.md).

## 5. Scan and check

`ds scan` records every block and every citation in `.ds/`. `ds check` compares the tree with that record:

```
$ ds scan
3 files, 3 defs, 2 refs, 0 problems, 0 skipped
$ ds check
docs/runbook.txt
  3	info     uncovered          defined but never cited or covered
      fix: runbook-bw796cm3 is defined but nothing cites or covers it; cite it from a page or remove the def
1 info, 2 ok
```

The last line counts findings by severity: two citations are up to date (`ok`), and the runbook def is `uncovered` because nothing cites it yet. `info` does not fail the check; the exit code is 0. `ds check` exits 1 only on an error-severity finding.

`ds render` shows what a reader of the built site would see:

```
$ ds render docs/billing.md
# Billing

An invoice is due thirty days after it is issued; see [`DueDate`](../billing/invoice.go#L11-L13).

An unpaid invoice is suspended after 14 days.
```

## 6. Break it and read the finding

<!-- doctest
git add -A
git commit -qm bind
-->

Change the payment terms to 45 days without opening the doc:

```go
	return t.AddDate(0, 0, 45)
```

<!-- doctest
printf 'package billing\n\nimport "time"\n\n// GraceDays is how long an unpaid invoice stays open before it is suspended.\n// ds:def id=gracedays-nbgvqwva\nconst GraceDays = 14\n\n// DueDate is when an invoice issued at t must be paid.\n// ds:def id=duedate-ctdp6ew3 owner=@billing\nfunc DueDate(t time.Time) time.Time {\n\treturn t.AddDate(0, 0, 45)\n}\n' > billing/invoice.go
-->

```
$ ds check
docs/billing.md
  3	error    unacked            duedate-ctdp6ew3 changed (body) since this sentence was first cited
      | -	return t.AddDate(0, 0, 30)
      | +	return t.AddDate(0, 0, 45)
      still true: ds ack duedate-ctdp6ew3 --doc docs/billing.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/billing.md:3, then ack
docs/runbook.txt
  3	info     uncovered          defined but never cited or covered
      fix: runbook-bw796cm3 is defined but nothing cites or covers it; cite it from a page or remove the def
1 error, 1 info, 1 ok
```

Each finding reads left to right: the doc and line of the sentence, the severity, the state, the id, and what happened to the block (`changed (body)`: its body changed and its signature did not). Below it are both ways out: the exact command if the sentence is still true, and where to edit if it is not. It exits 1.

`ds review` prints the same findings as a checklist with the citing sentence, and with the block's diff when docsync has the previous version of the block (an example is in [docsync at a glance](how-it-works.md#one-round-end-to-end)). `ds check --json` gives the same data for scripts.

## 7. Fix the sentence and ack

The sentence says "thirty days", which is now wrong. Edit it to "forty-five days", then record that it is true for the code as it is now:

<!-- doctest
printf '# Billing\n\nAn invoice is due forty-five days after it is issued; see [`DueDate`](ds:block?id=duedate-ctdp6ew3).\n\nAn unpaid invoice is suspended after [14](ds:cfg?id=gracedays-nbgvqwva) days.\n' > docs/billing.md
-->

```
$ ds ack duedate-ctdp6ew3 --doc docs/billing.md --line 3 --note "terms moved to net-45"
acked duedate-ctdp6ew3 at docs/billing.md:3 (human)
$ ds check
docs/runbook.txt
  3	info     uncovered          defined but never cited or covered
      fix: runbook-bw796cm3 is defined but nothing cites or covers it; cite it from a page or remove the def
1 info, 2 ok
```

`ds ack` needs `--doc` and `--line`, or `--all` to ack every citation of the id on purpose. The ack stores the block's hash, the sentence's hash and text, who acked (git `user.name` by default) and the note, so the next change to either the code or the sentence asks again. If the sentence had still been true, you would run the same `ds ack` without editing anything.

When many sentences flag for one mechanical change, `ds triage` groups them by diff so one ack with one note covers the group; see [CLI](cli.md).

## 8. Commit `.ds/`

Run `ds scan` again so the ledger records the new state, then commit `.ds/` with the code and doc changes:

```
$ ds scan
3 files, 3 defs, 2 refs, 0 problems, 0 skipped
$ git status --short
 M .ds/acks.tsv
 M .ds/ledger.tsv
 M .ds/refs.tsv
 M billing/invoice.go
 M docs/billing.md
?? .ds/blocks/e1b399a6aef1b7b44fa8e77679bacb9fbddc7d35338ca65d9ec8e771ff824f3c
```

Commit `config.toml`, `ledger.tsv`, `refs.tsv`, `acks.tsv` and `blocks/` (and `foreign.tsv` once you use a [workspace](cross-repo.md)). The `.ds/.gitignore` that `ds init` wrote already keeps out the machine-local files (`cache/`, `index/`, `journal.tsv`, `urls.json`, `runs.json`, `notified.json`, `hashes.json`, `metrics.json`, `lock`), which is why they do not appear above. Never commit those: the cache and journal hold raw source lines. `.ds/.gitattributes` sets `acks.tsv merge=union`, so acks recorded on two branches merge cleanly.

`ds check` changes no committed file, so it is safe as a gate; `ds scan` is the step that updates the committed record.

## 9. Add the CI check

`ds init` wrote `.ds/ci-github.yml`, a workflow that installs `ds` and runs `ds check --json`, failing the job on an error finding. Copy it to `.github/workflows/docsync.yml`. It starts with `on: workflow_dispatch` (run by hand from the Actions tab) because Actions minutes are billed on private repositories; its header comment has the `pull_request` and `push` triggers to paste in when you want it on every change:

```yaml
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
      - run: ds check --json > "$RUNNER_TEMP/docsync.json" || (cat "$RUNNER_TEMP/docsync.json"; exit 1)
```

The GitHub Action, pull request comments and pre-commit hooks are in [CI](ci.md).

## 10. Adopt the links you already have

<!-- doctest
git add -A
git commit -qm net45
printf 'package billing\n\nimport "errors"\n\nvar ErrTooLate = errors.New("refund window closed")\n\nfunc Refund(days int) error {\n\tif days > 14 {\n\t\treturn ErrTooLate\n\t}\n\treturn nil\n}\n' > billing/refund.go
-->

If your docs already link to code by line, such as `[Refund](../billing/refund.go#L7-L12)`, `ds adopt` turns each one into a def on the code and a citation in the doc. Preview first:

```markdown file=docs/refunds.md
# Refunds

A refund after the window fails with [`ErrTooLate`](../billing/refund.go#L5).
The check lives in [`Refund`](../billing/refund.go#L7-L12).
```

```
$ ds adopt --dry-run
billing/refund.go:5: // ds:def id=errtoolate-2gahx6gg
billing/refund.go:8: // ds:def id=refund-z59d26qe
docs/refunds.md:3: A refund after the window fails with [`ErrTooLate`](ds:block?id=errtoolate-2gahx6gg).
docs/refunds.md:4: The check lives in [`Refund`](ds:block?id=refund-z59d26qe).
2 link(s) would be adopted (--dry-run)
$ ds adopt
2 link(s) adopted, 4 edit(s); run ds scan
```

<!-- doctest
ds scan
git add -A
git commit -qm adopt
printf '# Grace\n\nSee [`GraceDays`](../billing/invoice.go#GraceDays) and [`DueDate`](../billing/invoice.go#DueDate).\n' > docs/grace.md
-->

The real run writes the ids the dry run printed, because a new id's suffix is derived from the repository, the file, the line and the file's content rather than drawn at random, so a dry run and the run after it over the same tree agree:

```go
// ds:def id=errtoolate-2gahx6gg
var ErrTooLate = errors.New("refund window closed")

// ds:def id=refund-z59d26qe
func Refund(days int) error {
```

```markdown
A refund after the window fails with [`ErrTooLate`](ds:block?id=errtoolate-2gahx6gg).
The check lives in [`Refund`](ds:block?id=refund-z59d26qe).
```

Links by symbol, such as `[GraceDays](../billing/invoice.go#GraceDays)`, are adopted the same way, and a block that already has a def keeps its id:

```
$ ds adopt --dry-run
docs/grace.md:3: See [`GraceDays`](ds:block?id=gracedays-nbgvqwva) and [`DueDate`](ds:block?id=duedate-ctdp6ew3).
2 link(s) would be adopted (--dry-run)
```

Then `ds scan` and commit. Before you commit, `ds undo` reverses the last uncommitted write made by `ds adopt`, `ds def` or `ds rename`. Here it reverses the adoption of `docs/grace.md` and stops at the earlier adoption, which is already committed:

```
$ ds adopt
2 link(s) adopted, 1 edit(s); run ds scan
$ ds undo
undid docs/grace.md:3
next: nothing uncommitted left to undo; the next entry is committed (f7e78d5): adopt billing/refund.go:5 +3 more errtoolate-2gahx6gg
```

## Next

- [Directives](directives.md): `ds:block` in block position, `ds:cfg` formats, claims, URLs, runs and tables.
- [Configuration](configuration.md): scan globs, what counts as docs, and check severities.
- [CI](ci.md) and [Integrations](integrations.md): make the check part of every pull request and editor.
- [Agents](agents.md): `ds init --agents` and the MCP server.
- [Troubleshooting](troubleshooting.md).
