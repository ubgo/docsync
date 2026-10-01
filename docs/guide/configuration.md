# Configuration

This page describes `.ds/config.toml`: every section and key `ds` accepts, its type and default, what it changes, and how the file is checked when it loads. It is for whoever sets docsync up in a repository and for anyone tuning it later. The command side is in the [Command reference](cli.md).

## Contents

- [The file](#the-file)
- [How the file is checked](#how-the-file-is-checked)
- [Top-level keys](#top-level-keys)
- [`[scan]` and `[scan.limits]`](#scan)
- [`[include]`](#include)
- [`[check]`](#check)
- [`[policy]`](#policy)
- [`[owners]`](#owners)
- [`[secret]`](#secret)
- [`[env]`](#env)
- [`[resolve]`](#resolve)
- [`[run]` and `[run.env.<name>]`](#run)
- [`[url]`](#url)
- [`[records]`](#records)
- [`[review]`](#review)
- [`[notify]` and `[notify.snapshot]`](#notify)
- [`[agents]`](#agents)
- [`[id]`](#id)
- [`[ledger]`](#ledger)
- [`[plugins]`](#plugins)
- [Keys that are accepted but do nothing yet](#keys-that-are-accepted-but-do-nothing-yet)
- [A complete example](#a-complete-example)

## The file

The examples on this page run in a small repository: a Go file with two defs and a page that cites them.

```text file=go.mod
module example.com/demo

go 1.22
```

```go file=internal/auth/session.go
package auth

import "time"

// SessionTTL is how long a login lasts.
// ds:def id=sessionttl-r7xkm5bw owner=@auth
const SessionTTL = 30 * time.Minute

// Login checks a password and returns a session token.
// ds:def id=login-j3nq87mh owner=@auth stability=api
func Login(user, password string) (string, error) {
	if password == "" {
		return "", ErrEmpty
	}
	return "tok-" + user, nil
}
```

```markdown file=docs/auth.md
# Auth

A session lasts [30 minutes](ds:block?id=sessionttl-r7xkm5bw).

[Login](ds:block?id=login-j3nq87mh) returns a session token.
```

<!-- doctest
git init -q -b main .
ds init
ds scan
git add -A
git commit -qm init
cp .ds/config.toml ../good.toml
-->

`ds init` writes a starting config, and every command finds the repository by looking for `.ds/config.toml` in the current directory and its parents:

```console
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

Commit it. Anything you leave out keeps its default, so a config only needs the keys you want to change.

The file is a small subset of TOML: `[table]` and `[dotted.table]` headers, `key = value`, `#` comments, and values that are strings (`"…"` or `'…'`), integers, floats, booleans, or arrays of those. Two limits matter in practice:

- An array must be on one line. `exclude = [` followed by items on later lines is a syntax error (`unterminated array (arrays must be on one line)`).
- Arrays of tables (`[[x]]`) and nested arrays are not supported.

A table header may appear more than once, and the keys from both places are merged. The same key twice is an error.

## How the file is checked

The config is checked in full every time a command loads it. A mistake stops the command with exit code `2` and a message that names the key, rather than running with a value nobody chose. `ds doctor` shows the same message as a `FAIL` row.

**An unknown key is an error**, with its line number. This catches a misspelled key, which would otherwise be silently ignored. With `fuzzy_threshold` misspelled:

<!-- doctest
perl -pi -e 's/fuzzy_threshold/fuzy_threshold/' .ds/config.toml
-->

```console
$ ds check
ds: check: config: unknown key: fuzy_threshold (line 15)
$ ds doctor
config	FAIL	check: config: unknown key: fuzy_threshold (line 15)
```

<!-- doctest
cp ../good.toml .ds/config.toml
-->

An unknown table is reported the same way (`colour: config: unknown key: colour (line 21)`).

**A value of the wrong type is an error**. With `max_lines = "40"`:

<!-- doctest
perl -pi -e 's/max_lines = 40/max_lines = "40"/' .ds/config.toml
-->

```console
$ ds check
ds: include: max_lines: config: wrong value type: want integer (line 12)
```

<!-- doctest
cp ../good.toml .ds/config.toml
-->

**A closed set must hold one of its values** (`include.mode`, `check.unacked`, `check.sentence`, the `notify.snapshot` tiers and classes). With `unacked = "warning"`:

<!-- doctest
perl -pi -e 's/unacked = "error"/unacked = "warning"/' .ds/config.toml
-->

```console
$ ds check
ds: config: invalid value: check.unacked "warning"
```

<!-- doctest
cp ../good.toml .ds/config.toml
-->

Other checks made at load time: `spec` must be `"1.0"`; `prefix` must be a lowercase identifier; at least one of `scan.code` and `scan.docs` must be non-empty; `check.fuzzy_threshold` must be above 0 and at most 1; `include.max_lines`, `scan.limits.*` and `url.rate_per_minute` must be at least 1; `agents.max_defs_per_run` must not be negative; and `env.default`, when set, must be listed in `env.known` if that list is non-empty.

<!-- doctest
perl -pi -e 's/spec = "1.0"/spec = "2.0"/' .ds/config.toml
-->

```console
$ ds check
ds: config: spec version not supported by this tool: file says "2.0", tool implements "1.0"
```

<!-- doctest
cp ../good.toml .ds/config.toml
perl -pi -e 's/prefix = "ds"/prefix = "My-Docs"/' .ds/config.toml
-->

```console
$ ds check
ds: config: invalid value: prefix "My-Docs" must be a lowercase identifier
```

<!-- doctest
cp ../good.toml .ds/config.toml
perl -pi -e 's/^default = ""$/default = "prod"\nknown = ["staging"]/' .ds/config.toml
-->

```console
$ ds check
ds: config: invalid value: env.default "prod" is not in env.known
```

<!-- doctest
cp ../good.toml .ds/config.toml
-->

### Durations

Duration keys use one of two forms, and a value that does not parse is an error naming the key. An empty string means the default.

| Keys | Form | Examples |
|---|---|---|
| `run.timeout` | A positive Go duration. | `300ms`, `30s`, `2m` |
| `url.ttl`, `check.snapshot_max_age`, `notify.escalate_after`, `notify.snapshot.digest_after`, `sources.<name>.ttl` | A whole number followed by `m` (minutes), `h` (hours), `d` (days) or `w` (weeks). Months and years are not accepted, because their length varies; write `90d`. | `90m`, `24h`, `7d`, `2w` |

```toml file=.ds/config.toml append=true

[url]
ttl = "7 days"
```

```console
$ ds check
ds: config: invalid value: url.ttl "7 days"
```

<!-- doctest
cp ../good.toml .ds/config.toml
-->

```toml file=.ds/config.toml append=true

[run]
timeout = "1d"
```

```console
$ ds check
ds: config: invalid value: run.timeout "1d" must be a positive duration such as 30s
```

<!-- doctest
cp ../good.toml .ds/config.toml
-->

`run.timeout` does not accept `d`, and the other keys do not accept `s`.

## Top-level keys

| Key | Type | Default | What it does |
|---|---|---|---|
| `spec` | string | `"1.0"` | The config schema version. Any other value stops the load. |
| `prefix` | string | `"ds"` | The directive prefix: `ds:def`, `ds:block?id=…`. Use another one when your source already contains `ds:` text that is not a directive, such as examples in docs about docsync. Lowercase letters, digits (not first) and `_`. Changing it invalidates the extraction cache. |
| `workspace` | string | empty | Where the workspace index lives, for repositories whose docs cite each other. An existing local directory is used in place; anything else is cloned into `.ds/index/` and pushed to by `publish`. Empty means a single repository. See [Cross-repo](cross-repo.md). |

```toml
spec = "1.0"
prefix = "mydocs"
workspace = "github.com/org/ds-index"
```

## scan

`[scan]` says which files are read and how.

| Key | Type | Default | What it does |
|---|---|---|---|
| `code` | array of globs | empty (`["**"]` from `init`) | Files where defs may live. |
| `docs` | array of globs | empty (`["docs/**", "README.md"]` from `init`) | Pages whose citations are checked. |
| `exclude` | array of globs | empty | Files never read, even if `code` or `docs` matches them. |
| `generated` | array of globs | empty | Generated files. They are scanned, but a `ds:def` inside one is reported, because the next generation run would delete it. |

At least one of `code` and `docs` must be non-empty. `ds doctor` reports each glob with the number of files it matches, and `WARN` for a glob that matches none.

```toml
[scan]
code = ["internal/**", "cmd/**", "go.mod"]
docs = ["docs/**/*.md", "README.md"]
exclude = ["**/testdata/**", "**/*_test.go", "dist/**"]
generated = ["**/*.pb.go", "**/gen/**"]
```

A def in a generated file (the config from `init` marks `**/gen/**` as generated):

```go file=gen/x.go
package gen

// ds:def id=x-k3m8x2pq
const X = 1
```

```console
$ ds scan
4 files, 3 defs, 2 refs, 1 problems, 0 skipped
  gen/x.go:3  scan: ds:def in a generated file will be overwritten by the next generation
```

### scan.limits

| Key | Type | Default | What it does |
|---|---|---|---|
| `max_file_kb` | integer | `512` | Files larger than this, in KB, are skipped. |
| `max_line_chars` | integer | `2000` | A file with a longer line is skipped, unless it is prose. |

`ds scan` counts skipped files. A skipped file that held defs or citations at the last scan is also named, and `check` reports it as an `unscanned` error, with its last recorded state kept, so a file that grows past the limit is not silently dropped. With a 1 KB limit, after `gen/x.go` grows past it:

```toml file=.ds/config.toml append=true

[scan.limits]
max_file_kb = 1
```

<!-- doctest
perl -e 'print "// filler line\n" x 100' >> gen/x.go
-->

```console
$ ds scan
3 files, 2 defs, 2 refs, 0 problems, 1 skipped
warning: gen/x.go not scanned (too-large); its last recorded state is kept
$ ds check
gen/x.go
  1	error    unscanned          could not be scanned (too-large); 0 citations and 1 block are not being checked
      fix: gen/x.go could not be scanned (too-large): raise [scan.limits] max_file_kb, or split the file; until it is, the citations in it are not checked and its blocks are kept as they were last seen
…
```

<!-- doctest
cp ../good.toml .ds/config.toml
rm -r gen
ds scan
-->

```toml
[scan.limits]
max_file_kb = 1024
max_line_chars = 4000
```

## include

`[include]` controls blocks rendered into a page with `<!-- ds:block id=… -->` on a line of its own.

| Key | Type | Default | What it does |
|---|---|---|---|
| `mode` | `"build"` or `"repo"` | `"build"` | `build`: the code is inserted only when the page is rendered (`ds render`, the site plugins). `repo`: `ds refresh` writes the code into the page between `<!-- ds:block … -->` and `<!-- /ds:block -->`, for docs read raw on GitHub, and `check` reports the copy as `stale` when the code changes and `tampered` when someone edits it by hand. |
| `max_lines` | integer | `40` | A rendered block longer than this is a `too-large` error; show a fragment with `lines=a-b` or cite it with a link instead. |

```toml
[include]
mode = "repo"
max_lines = 40
```

A page that renders `Login`, which is six lines, under `max_lines = 3`:

```markdown file=docs/code.md
# Login code

<!-- ds:block id=login-j3nq87mh -->
```

<!-- doctest
perl -pi -e 's/max_lines = 40/max_lines = 3/' .ds/config.toml
-->

```console
$ ds check
docs/code.md
  3	error    too-large          rendering 6 lines exceeds the cap of 3
      fix: add lines=a-b to show a fragment, or cite it with a link instead of rendering it
…
```

<!-- doctest
cp ../good.toml .ds/config.toml
rm docs/code.md
-->

## check

`[check]` tunes how findings are decided.

| Key | Type | Default | What it does |
|---|---|---|---|
| `unacked` | `"error"` or `"warn"` | `"error"` | The severity of `unacked`. With `"warn"`, a changed block no longer fails `check` unless `--strict` is passed. Useful while adopting docsync in a repository with many existing citations. |
| `sentence` | `"wording"` or `"position"` | `"wording"` | What an ack holds a citation to. `wording`: rewriting an acked sentence makes it `unacked` again (`sentence rewritten since the ack`). `position`: the ack holds for that place in the page whatever it says. |
| `fuzzy_threshold` | float in (0, 1] | `0.8` | How similar a block's new body must be to a vanished one for the match to call it `rewritten?` rather than `deleted`. |
| `permalink` | string | empty | A link template for rendered citations, with `{sha}`, `{file}`, `{start}` and `{end}`. Empty renders repository-relative links (`internal/auth/session.go#L7-L7`). |
| `snapshot_max_age` | day duration | empty | In a workspace, `check --frozen` prints a warning when `.ds/foreign.tsv` is older than this. Empty means never. It is only ever a warning. |

```toml
[check]
unacked = "warn"
sentence = "wording"
fuzzy_threshold = 0.8
permalink = "https://github.com/org/repo/blob/{sha}/{file}#L{start}-L{end}"
snapshot_max_age = "30d"
```

With `unacked = "warn"`, after the session length changes in the code:

<!-- doctest
perl -pi -e 's/unacked = "error"/unacked = "warn"/' .ds/config.toml
-->

```console
$ perl -pi -e 's/30 \* time/60 * time/' internal/auth/session.go
$ ds check; echo "exit $?"
docs/auth.md
  3	warning  unacked            sessionttl-r7xkm5bw changed (body) since this sentence was first cited
      still true: ds ack sessionttl-r7xkm5bw --doc docs/auth.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/auth.md:3, then ack
1 warning, 1 none
exit 0
$ ds check --strict; echo "exit $?"
…
exit 1
```

<!-- doctest
cp ../good.toml .ds/config.toml
perl -pi -e 's/60 \* time/30 * time/' internal/auth/session.go
-->

With `permalink` set, `ds render` writes links from the template:

```toml file=.ds/config.toml append=true

[check]
permalink = "https://github.com/org/demo/blob/{sha}/{file}#L{start}-L{end}"
```

```console
$ ds render docs/auth.md | grep 'A session'
A session lasts [30 minutes](https://github.com/org/demo/blob/0f6d00f/internal/auth/session.go#L7-L7).
```

<!-- doctest
cp ../good.toml .ds/config.toml
-->

## policy

| Key | Type | Default | What it does |
|---|---|---|---|
| `require_doc` | array of globs | empty | An exported declaration in a matching file that has no `ds:def` is an `undocumented export` error. |

```toml
[policy]
require_doc = ["pkg/api/**"]
```

A declaration counts as exported when a line declares a name that starts with a capital letter using `func`, `type`, `export function`, `export class`, `pub fn` or `def`. With the policy on `internal/auth/**` and a new exported function there:

```toml file=.ds/config.toml append=true

[policy]
require_doc = ["internal/auth/**"]
```

```go file=internal/auth/refresh.go
package auth

// Refresh renews a session.
func Refresh() {}
```

```console
$ ds check
internal/auth/refresh.go
  4	error    undocumented export Refresh is exported under a require_doc path and has no def
      fix: policy.require_doc covers internal/auth/refresh.go; run `ds def internal/auth/refresh.go#Refresh` and cite it from a page
…
```

<!-- doctest
cp ../good.toml .ds/config.toml
-->

## owners

`[owners]` maps a team name, as used in `owner=` on a def, to the people in it. Quote the key, because it starts with `@`.

| Key | Type | Default | What it does |
|---|---|---|---|
| `"@team"` | array of strings | none | The people who own blocks marked `owner=@team`. `ds notify` groups findings by owner, and `ds report --orphaned-owners` lists owners used in defs but missing here. |

```toml
[owners]
"@auth" = ["alice", "bob"]
"@platform" = ["carol"]
```

Without an `[owners]` table nothing is reported as orphaned. Once it lists teams, an owner used in a def but missing from it is:

```console
$ ds report --orphaned-owners
orphaned owners (0):
```

```toml file=.ds/config.toml append=true

[owners]
"@platform" = ["carol"]
```

```console
$ ds report --orphaned-owners
orphaned owners (1): @auth
```

<!-- doctest
cp ../good.toml .ds/config.toml
-->

## secret

| Key | Type | Default | What it does |
|---|---|---|---|
| `paths` | array of globs | empty | Defs in matching files are secrets: only a hash of the value is recorded, the body is never stored in `.ds/blocks/` or printed, and a secret with no declared source is reported as `unsourced`. |

```toml
[secret]
paths = ["**/.env*", "**/secrets/**"]
```

With that in the config and a key defined in an env file:

```toml file=.ds/config.toml append=true

[secret]
paths = ["**/.env*", "**/secrets/**"]
```

```text file=.env.prod
API_KEY=abc123 # ds:def id=apikey-k3m8x2pa
```

```console
$ ds read apikey-k3m8x2pa

$ ds check
.env.prod
  1	warning  unsourced          secret with no declared source
      fix: apikey-k3m8x2pa is a secret with no from= and no truth=true; declare where it is copied from or mark it the truth
…
```

<!-- doctest
cp ../good.toml .ds/config.toml
rm .env.prod
-->

Chains, `from=` and `truth=true` are covered in [Secrets and runs](secrets-and-runs.md).

## env

| Key | Type | Default | What it does |
|---|---|---|---|
| `default` | string | `""` | The environment used for citations that have no `env=`, when `--env` is not given to `check` or `render`. |
| `known` | array of strings | empty | The environments that exist. When it is set, `default` must be one of them. |

```toml
[env]
default = "prod"
known = ["prod", "staging", "dev"]
```

## resolve

These keys apply to `ds check --resolve`, which asks `ds-resolve-<provider>` plugins whether secret addresses exist and fetches `ds:url` links. Resolution happens only when `--resolve` is passed and `enabled` is true: the flag asks for one run, the key is the repository's consent, as with `[run]`.

| Key | Type | Default | What it does |
|---|---|---|---|
| `providers` | array of strings | empty | When set, only these providers are called; a hop for any other provider is `unverifiable` with that reason. Empty allows every provider that has a plugin on `PATH`. Providers are named as addresses and `source=` name them: `github`, `1password` (its plugin is `ds-resolve-onepassword`), `aws`, `gcp`, `vault`, `env`. |
| `store_hash` | boolean | `false` | Keep the hashes that resolution returns in `.ds/hashes.json`, so the next run can tell that the truth has changed (`rotated`). |
| `enabled` | boolean | `false` | Whether `--resolve` contacts anything. When false, `check --resolve` prints `resolve.enabled is false; no provider or link was contacted` on stderr and checks as if the flag were absent, so links and secret hops are `unverifiable`. |

```toml
[resolve]
enabled = true
providers = ["github", "1password"]
store_hash = true
```

## run

`[run]` controls `ds:run`, which executes a command written in a doc. It runs only under `ds check --run`, and only where this section allows it.

| Key | Type | Default | What it does |
|---|---|---|---|
| `enabled` | boolean | `false` | Whether `--run` executes anything at all. When false, `check --run` prints `run.enabled is false; nothing executed`. |
| `allow` | array of globs | empty | The docs whose `cmd=` and `file=` directives may run. |
| `timeout` | Go duration | `"30s"` | How long one command may run; a `timeout=` on a `ds:run` directive replaces it for that command. |
| `shell` | string | `"sh"` | The program commands run under, found on `PATH`: `<shell> -c <command>` for `cmd=` and `id=` (and for `[review] command`), `<shell> <file>` for `file=`. On Windows, Git for Windows provides `sh`; a team whose commands are written for PowerShell sets `"pwsh"`. docsync never picks another shell on its own. |

`[run.env.<name>]` sets environment variables for commands run with `env=<name>`. Values may reference the caller's environment with `$VAR`, which is expanded when the command runs, so the secret itself stays out of the file.

```toml
[run]
enabled = true
allow = ["docs/runbooks/**"]
timeout = "30s"
shell = "sh"

[run.env.staging]
DATABASE_URL = "$STAGING_DATABASE_URL"
```

With running enabled for one runbook:

```toml file=.ds/config.toml append=true

[run]
enabled = true
allow = ["docs/runbooks/**"]
```

```markdown file=docs/runbooks/smoke.md
# Smoke

Run the smoke test: <!-- ds:run cmd="echo hello" expect=ok -->
```

```console
$ ds check --run
docs/runbooks/smoke.md:3  run ok: echo hello
…
```

When the shell is missing, `check --run` and `review --ai` stop instead of skipping, and nothing is recorded as run:

```toml file=.ds/config.toml append=true
shell = "nosuchsh"
```

```console
$ ds check --run
ds: the shell is not on PATH: nosuchsh (on Windows, Git for Windows provides sh; or name another shell with [run] shell in .ds/config.toml)
```

<!-- doctest
cp ../good.toml .ds/config.toml
rm -r docs/runbooks
-->

A repository with nothing to run never needs the shell.

## url

These keys apply to `ds:url` links, which are checked under `ds check --resolve` when `resolve.enabled` is true.

| Key | Type | Default | What it does |
|---|---|---|---|
| `ttl` | day duration | `"7d"` | How long a link's last answer is trusted before it is fetched again. Answers are kept in `.ds/urls.json`; a request that got no answer (no network) is not kept, so it is tried again on the next run. |
| `rate_per_minute` | integer | `30` | At most this many requests a minute. |

```toml
[url]
ttl = "7d"
rate_per_minute = 30
```

## records

`[records]` is the data source for `ds:table`, which renders a table of records into a page.

| Key | Type | Default | What it does |
|---|---|---|---|
| `source` | string | `"frontmatter"` | `frontmatter`: a directory of markdown files, one record per file. `sqlite`: a SQLite database. `http`: a URL that returns records. Any other name runs a `ds-records-<name>` plugin from `PATH`. |
| `path` | string | empty | The directory (frontmatter), database file (sqlite) or URL (http). Required for the three built-in sources; with it empty, no source is configured. |
| `table` | string | empty | For `sqlite`: the table to read when a `ds:table` names no `kind=`. |

```toml
[records]
source = "frontmatter"
path = "records/"
```

## review

| Key | Type | Default | What it does |
|---|---|---|---|
| `command` | string | empty | The command `ds review --ai` runs. It receives the findings as JSON on stdin and must print a unified diff on stdout. It runs under `[run] shell`. Which tool or model it calls is up to you; docsync calls none itself. |

```toml
[review]
command = "my-review-tool --patch"
```

Without it, `ds review --ai` stops with `--ai needs [review] command in .ds/config.toml`.

## notify

`[notify]` configures `ds notify`.

| Key | Type | Default | What it does |
|---|---|---|---|
| `slack` | string | empty | A Slack incoming webhook URL. `$VAR` is expanded from the environment, so the URL can stay out of the file. Empty prints the digests only. |
| `escalate_after` | day duration | `"7d"` | A finding still open this long after it was first sent is sent again as an escalation. |
| `github_issues` | boolean | `false` | Accepted; see [below](#keys-that-are-accepted-but-do-nothing-yet). |

```toml
[notify]
slack = "$DS_SLACK_WEBHOOK"
escalate_after = "7d"
```

### notify.snapshot

In a workspace, `ds notify` also says when blocks this repository cites have changed upstream since `.ds/foreign.tsv` was last synced. Drift triggers it, never age alone. How urgent a drift is depends on its change class. It never affects an exit code.

| Key | Type | Default | What it does |
|---|---|---|---|
| `enabled` | boolean | `true` | Turn snapshot messages on or off. Without `workspace` there is no snapshot and nothing is sent either way. |
| `immediate` | array of classes | `["signature", "type", "renamed", "value"]` | Change classes worth interrupting someone for. |
| `digest` | array of classes | `["body", "comment"]` | Classes worth mentioning eventually. |
| `digest_after` | day duration | `"14d"` | How long a digest-tier drift waits before it is mentioned. |
| `on_deleted` | `"never"`, `"digest"` or `"immediate"` | `"immediate"` | The tier for a cited block that upstream no longer defines. |
| `owner` | string | empty | Who receives these messages. Empty sends them to the owners of the defs in the citing files. |

The classes are the change classes `check` reports, such as `body`, `signature` and `moved` (see [How it works](how-it-works.md)). A class listed in both `immediate` and `digest` is a load error, and a class that is in neither is treated as `immediate`.

```toml
[notify.snapshot]
immediate = ["signature", "type", "renamed", "value"]
digest = ["body", "comment"]
digest_after = "14d"
on_deleted = "immediate"
owner = "@docs"
```

Naming a class in both lists:

```toml file=.ds/config.toml append=true

[notify.snapshot]
immediate = ["body"]
```

```console
$ ds check
ds: config: invalid value: notify.snapshot "body" is in both immediate and digest
```

<!-- doctest
cp ../good.toml .ds/config.toml
-->

## agents

| Key | Type | Default | What it does |
|---|---|---|---|
| `max_defs_per_run` | integer | `20` | How many `def` calls one `ds mcp` session may make. `0` removes the cap. |
| `mcp`, `session_hook` | boolean, string | `true`, empty | Accepted; see [below](#keys-that-are-accepted-but-do-nothing-yet). |

```toml
[agents]
max_defs_per_run = 20
```

## id

| Key | Type | Default | What it does |
|---|---|---|---|
| `suffix_alphabet` | string | `"23456789abcdefghjkmnpqrstuvwxyz"` | The characters a new id suffix is made of: unique lowercase ASCII letters and digits, at least 16 of them. |
| `suffix_length` | integer | `8` | The length of a new suffix, from 6 to 32. |

These apply when `ds def` mints an id. A value outside the allowed range is reported then, not when the config loads:

```toml file=.ds/config.toml append=true

[id]
suffix_length = 4
```

```console
$ ds doctor
config          ok    spec 1.0, prefix ds
…
$ ds def internal/auth/refresh.go#Refresh --dry-run
ds: id: suffix length out of range: 4 not in [6,32]
```

<!-- doctest
cp ../good.toml .ds/config.toml
-->

Changing them does not touch existing ids. The workspace file has its own `[workspace.id]` section with the same two keys; see [Cross-repo](cross-repo.md).

## ledger

| Key | Type | Default | What it does |
|---|---|---|---|
| `shard` | boolean | `false` | Write the ledger as one file per top-level directory, `.ds/ledger/<dir>.tsv`, instead of one `.ds/ledger.tsv`. For large repositories, where one ledger file conflicts on every merge. |

```toml file=.ds/config.toml append=true

[ledger]
shard = true
```

```console
$ ds scan
4 files, 2 defs, 2 refs, 0 problems, 0 skipped
$ ls .ds/ledger
internal.tsv
```

<!-- doctest
cp ../good.toml .ds/config.toml
rm -r .ds/ledger
ds scan
-->

## plugins

Process plugins are separate executables on `PATH` that speak docsync's plugin protocol. This section names the ones `ds` should load.

| Key | Type | Default | What it does |
|---|---|---|---|
| `verbs` | array of strings | empty | For each name, `ds-<name>` handles the directive verb `<name>`. |
| `picks` | array of strings | empty | For each scheme, `ds-pick-<scheme>` handles `pick=<scheme>:…`. |

```toml
[plugins]
verbs = ["jira"]
picks = ["jq"]
```

Resolver plugins (`ds-resolve-<provider>`) are found by provider name and need no entry here, and record plugins are named by `[records] source`.

## Keys that are accepted but do nothing yet

The parser accepts these keys, so a config written from the [specification's example](../SPEC.md#23-configuration) loads, but the current build does not act on them:

| Key | Status |
|---|---|
| `notify.github_issues` | Slack is the only channel. |
| `agents.mcp` | `ds init --agents` registers `ds mcp` either way. |
| `agents.session_hook` | `ds init --agents` always installs `ds map --budget 2000`. |
| `env.known` | Only checked against `env.default`. |
| `[sources.<name>]` with `dsn`, `url`, `ttl` | Parsed, and `ttl` must be a valid day duration (default `"24h"`), but no command reads them. |
| `[performance]` | Targets for the project's own performance suite; any keys are accepted and ignored. |

## A complete example

Every key that has an effect, with a comment on what it does. This file loads, and `ds doctor` and `ds check` run against it:

```toml file=.ds/config.toml
# .ds/config.toml
spec = "1.0"                        # must be "1.0"
prefix = "ds"                       # directives are ds:def, ds:block?id=…
# workspace = "github.com/org/ds-index"   # only when docs cite other repositories

[scan]
code = ["internal/**", "cmd/**", "go.mod"]           # where defs live
docs = ["docs/**/*.md", "README.md"]                 # pages whose citations are checked
exclude = ["**/testdata/**", "**/*_test.go", "dist/**"]
generated = ["**/*.pb.go", "**/gen/**"]              # a ds:def here is reported

[scan.limits]
max_file_kb = 512                   # larger files are skipped
max_line_chars = 2000               # files with longer lines are skipped, except prose

[include]
mode = "build"                      # "repo" writes rendered blocks into the page on ds refresh
max_lines = 40                      # a longer rendered block is too-large

[check]
fuzzy_threshold = 0.8               # similarity for "rewritten?" rather than "deleted"
unacked = "error"                   # "warn" while adopting
sentence = "wording"                # rewriting an acked sentence needs a new ack
permalink = "https://github.com/org/repo/blob/{sha}/{file}#L{start}-L{end}"
snapshot_max_age = "30d"            # warn when a frozen check uses an old foreign.tsv

[policy]
require_doc = ["pkg/api/**"]        # exported symbols here need a def

[owners]
"@auth" = ["alice", "bob"]

[secret]
paths = ["**/.env*", "**/secrets/**"]   # hash only; body never stored or printed

[env]
default = "prod"                    # for citations without env=
known = ["prod", "staging", "dev"]

[resolve]
enabled = true                      # ds check --resolve contacts providers and links
providers = ["github", "1password"]     # only these providers' plugins are called
store_hash = false                  # true keeps resolved hashes in .ds/hashes.json

[run]
enabled = true                      # ds check --run executes ds:run
allow = ["docs/runbooks/**"]        # where cmd= and file= may run
timeout = "30s"                     # Go duration
shell = "sh"                        # looked up on PATH; "pwsh" for PowerShell

[run.env.staging]
DATABASE_URL = "$STAGING_DATABASE_URL"  # expanded when the command runs

[url]
ttl = "7d"                          # recheck a ds:url after this long
rate_per_minute = 30

[records]
source = "frontmatter"              # or sqlite, http, or a ds-records-<name> plugin
path = "records/"

[review]
command = "my-review-tool --patch"  # reads findings as JSON, prints a unified diff

[notify]
slack = "$DS_SLACK_WEBHOOK"         # empty prints digests only
escalate_after = "7d"

[notify.snapshot]
enabled = true
immediate = ["signature", "type", "renamed", "value"]
digest = ["body", "comment"]
digest_after = "14d"
on_deleted = "immediate"
owner = "@auth"

[agents]
max_defs_per_run = 20               # per ds mcp session; 0 removes the cap

[id]
suffix_alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
suffix_length = 8                   # 6 to 32

[ledger]
shard = false                       # true writes .ds/ledger/<dir>.tsv

[plugins]
verbs = []                          # ds-<verb> executables
picks = []                          # ds-pick-<scheme> executables
```

```console
$ ds doctor
config             ok    spec 1.0, prefix ds
glob internal/**   ok    2 files
glob cmd/**        WARN  matches no files
glob go.mod        ok    1 files
glob docs/**/*.md  ok    1 files
glob README.md     WARN  matches no files
…
$ ds check
2 none
```

The workspace file, `ds-workspace.toml`, is a separate file in the index repository with its own keys (`name`, `repos`, `index`, `default_branch`, `stale_after_commits`, and `[workspace.id]` and `[workspace.env]`); it is described in [Cross-repo](cross-repo.md).
