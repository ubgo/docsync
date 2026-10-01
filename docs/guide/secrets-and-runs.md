# Secrets, runs and URLs

This page covers the three features that reach outside the repository: `ds:run` executes a command named in a doc, `ds:url` watches an external link, and secret chains document where a credential lives and where it is copied, optionally verified against the provider. It is for whoever owns runbooks, operational docs, or the scheduled job that runs `ds check --run --resolve`.

All three are off unless you ask for them. A plain `ds check` executes nothing, fetches nothing, and contacts no provider; `--run` and `--resolve` are the switches, and both are refused on pull requests from forks. Everything below was run with `ds` against throwaway repositories, with stand-in plugins where a real provider was not reachable.

## ds:run: commands in runbooks

### Turn it on

```toml
# .ds/config.toml
[run]
enabled = true
allow = ["runbooks/**"]     # docs where cmd= and file= may appear
timeout = "30s"             # per command, the default; a positive Go duration
shell = "sh"                # the default; looked up on PATH

[run.env.staging]
DATABASE_URL = "$STAGING_DATABASE_URL"
```

### Three ways to name what runs

```markdown
# Deploy

Check the toolchain: <!-- ds:run cmd="go version" expect=ok -->

Smoke test: <!-- ds:run file=scripts/smoke.sh expect="all endpoints answered" -->

Count users: <!-- ds:run id=count-users-q7n2m4kt env=staging expect=rows -->
```

| Form | Runs | Allowed where |
|---|---|---|
| `cmd="…"` | `<shell> -c "<cmd>"` | only in docs matching `run.allow` |
| `file=path` | `<shell> <path>`; the path must be a clean, relative path to a regular file inside the repository (no `..`, no leading `/` or `-`, no symlink) and is passed as one argument, never parsed by the shell | only in docs matching `run.allow` |
| `id=…` | the content of a def marked `runnable=true` | any doc; a def without `runnable=true` is refused |

The def for the `id=` form above is a one-line script:

```sh
# db/count.sh
# ds:def id=count-users-q7n2m4kt runnable=true
echo "users: 42 in $DS_ENV ($DATABASE_URL)"
```

`expect=` decides success: `ok` (or nothing) means exit status zero; `rows` means exit zero and some output; any other value is a substring the output must contain, and the exit status is then not consulted. `show=command`, `show=output`, or `show=none` limit what `ds render` prints; the default shows both. Every command gets `run.timeout`: a `timeout=` key on the directive is accepted without a warning but not applied (`cmd="sleep 2" timeout=1s` ran to completion in testing), so set the limit in config.

### Run it

Without `--run`, every `ds:run` is reported as `skipped` (severity info) and nothing executes. With `--run` but `enabled = false`, `ds` says so and runs nothing:

```console
$ ds check --run
run.enabled is false; nothing executed
docs/readme.md
  3	info     skipped            run not executed
      fix: pass --run to execute ds:run directives where they are enabled
…
```

Enabled:

```console
$ STAGING_DATABASE_URL=postgres://staging-db/app ds check --run
docs/readme.md:3  run skipped: cmd= is allowed only in docs matching run.allow
runbooks/deploy.md:3  run ok: go version
runbooks/deploy.md:5  run ok: sh 'scripts/smoke.sh'
runbooks/deploy.md:7  run ok: echo "users: 42 in $DS_ENV ($DATABASE_URL)"
4 none
```

A command that misses its expectation prints `run FAILED` and makes `ds check` exit 1, although it adds no row to the findings summary:

```console
$ ds check --run              # runbooks/deploy.md:11 is <!-- ds:run cmd="exit 3" expect=ok -->
…
runbooks/deploy.md:11  run FAILED: exit 3
6 none
$ echo $?
1
```

### Environments

A `ds:run` with `env=staging` (or a run under `ds check --run --env staging`, or `[env] default`) gets `DS_ENV=staging` plus every variable in `[run.env.staging]`. Values are expanded from the environment of the `ds` process, so `"$STAGING_DATABASE_URL"` keeps the credential out of the committed config; the CI job that runs `--run` provides it.

### Results and rendering

Each outcome is stored in `.ds/runs.json`, keyed by doc and line, with the command, its output (capped at 64 KiB), whether it passed, and when:

```json
{
  "runbooks/deploy.md:7": {
    "command": "echo \"users: 42 in $DS_ENV ($DATABASE_URL)\"",
    "output": "users: 42 in staging (postgres://staging-db/app)\n",
    "ok": true,
    "expect": "rows",
    "at": "2026-10-01T03:39:00.600576Z"
  }
}
```

`ds render` shows the command and its last result:

~~~~console
$ ds render runbooks/deploy.md
# Deploy

Check the toolchain:

```sh
go version
```

_ok · as of 2026-10-01T03:39:00Z_

```text
go version go1.27.1 darwin/arm64
```
…
~~~~

`.ds/runs.json` is machine-local and ignored by the `.ds/.gitignore` that `ds init` writes. Output is stored and rendered as the command printed it, so a command that echoes a credential, as the example above echoes `DATABASE_URL`, puts it in that file and on the rendered page. Have runbook commands print counts and statuses, not connection strings.

### The shell

Commands run under `sh` by default, found on PATH; on Windows, Git for Windows provides it. A team whose commands are written for another shell names it with `[run] shell`, and `ds` never substitutes one by itself. When a command is about to run and the shell is missing, the check stops instead of skipping:

```console
$ ds check --run
docs/readme.md:3  run skipped: cmd= is allowed only in docs matching run.allow
ds: the shell is not on PATH: pwsh (on Windows, Git for Windows provides sh; or name another shell with [run] shell in .ds/config.toml)
$ echo $?
2
```

A repository with nothing allowed to run is never asked for a shell. A `run.timeout` that does not parse stops every command at config load, naming the key: `config: invalid value: run.timeout "half a minute" must be a positive duration such as 30s`.

### Guards

- `--run` and `--resolve` are refused on a pull request from a fork, detected from `GITHUB_EVENT_PATH`: `ds: --run and --resolve are disabled on pull requests from forks`.
- `cmd=` and `file=` only run in docs matching `run.allow`; `id=` only runs a def marked `runnable=true`.
- Over MCP, agents have no way to run anything.

Run `--run` from a scheduled job with credentials, never on every pull request. [CI](ci.md) has the workflow.

## ds:url: external links

```markdown
See the [storage guide](ds:url?href=https://example.com/guide&title=Storage%20guide).

<!-- ds:url href=https://example.com/guide title="Storage guide" -->
```

`href` is the link; `title` is a substring the page's `<title>` must contain. Without `--resolve` each one is a warning that it was not checked. With it, `ds` fetches the page, follows redirects, and reports:

```console
$ ds check --resolve
docs/links.md
  5	warning  url moved          http://127.0.0.1:8765/old redirects to http://127.0.0.1:8765/new
      fix: the link http://127.0.0.1:8765/old now redirects to http://127.0.0.1:8765/new; update it at docs/links.md:5
  7	error    dead               http://127.0.0.1:8765/toast returned 404
      fix: the link http://127.0.0.1:8765/toast returned 404; update or remove it at docs/links.md:7
  9	warning  retitled           title is now "Storage guide, 2nd edition"
      fix: the page at http://127.0.0.1:8765/guide no longer has title "\"Manual\""; confirm it is still the right page
1 error, 2 warning
```

| State | Severity | Meaning |
|---|---|---|
| `ok` | none | status below 400, no redirect, title matches |
| `dead` | error | status 400 or above, or the request failed |
| `url moved` | warning | the final URL after redirects differs from `href` |
| `retitled` | warning | the page title no longer contains `title` |
| `unverifiable` | warning | not checked: no `--resolve` |

### Cache and rate

Results are cached in `.ds/urls.json` (machine-local) for `url.ttl`, default `7d`, and requests are spaced by `url.rate_per_minute`, default `30`, so a page with a hundred links does not hammer one host:

```toml
[url]
ttl = "7d"             # whole number with m, h, d or w
rate_per_minute = 30
```

### Writing the link form

Three things to know, all seen in testing:

- **No spaces inside a link target.** Markdown ends a link at a space, so `[x](ds:url?href=…&title="Storage guide")` is not a link at all and the directive is silently ignored. Write the space as `%20` or `+`, or use the comment form, where quotes work: `<!-- ds:url href=… title="Storage guide" -->`.
- **No quotes around `title` in the link form.** They are kept as part of the value, so `title="Storage"` looks for a title containing `"Storage"` with the quote marks and reports `retitled` (the output above shows it). Write `title=Storage`.
- **A network failure is cached as `dead`.** On a machine that cannot reach the host, `--resolve` reports every link `dead … returned 0` and keeps that result for `url.ttl`, so links stay dead after the network returns. Delete `.ds/urls.json` to check again.

## Secrets: addresses, never values

docsync treats a secret as an address: `${{ secrets.STRIPE_KEY }}`, `op://Platform/stripe-prod/credential`, an AWS Secrets Manager ARN, a GCP `projects/…/secrets/…` name, a `vault:` path, or an environment variable name. It hashes and renders the address and never stores, prints, or renders a value. Mark a def `secret=true`, or list files in `[secret] paths`:

```toml
[secret]
paths = ["**/.env*", "**/secrets/**"]
```

A secret def whose content is not an address shape is blanked by the scanner and only its hash is kept, so a change is still reported, as class `unknown`, without anything showing what changed:

```console
$ cat local.env
API_TOKEN=tok_abc123   # ds:def id=api-token-h7j8k9m2 secret=true
$ ds read api-token-h7j8k9m2        # prints nothing
$ grep -rl tok_abc123 .ds || echo "value not under .ds"
value not under .ds
```

### Chains

Most teams keep a secret in one place and copy it to others. `truth=true` marks the single root; `from=` links each copy to where it came from; `sync=` names the job that copies it.

```sh
# .env.tpl
STRIPE_KEY=op://Platform/stripe-prod/credential   # ds:def id=op-stripe-key-p9c2v7ld secret=true truth=true
```

```yaml
# .github/workflows/deploy.yml
    env:
      STRIPE_KEY: ${{ secrets.STRIPE_KEY }}   # ds:def id=gh-stripe-key-r4t6x2mb secret=true from=op-stripe-key-p9c2v7ld sync=scripts/sync-secrets.sh
```

```go
// internal/pay/stripe.go
	// ds:def id=app-stripe-key-m4w8k2qn secret=true source=env from=gh-stripe-key-r4t6x2mb pick=regex:'"(\w+)"'
	key := os.Getenv("STRIPE_KEY")
```

The provider is read from the address shape; a bare environment variable name says nothing, so that hop declares `source=env`. The `pick=` keeps just the variable name, so the doc renders `STRIPE_KEY` rather than the whole statement.

A page cites the chain and the name:

```markdown
Stripe credentials: <!-- ds:chain id=app-stripe-key-m4w8k2qn -->

The app reads [`STRIPE_KEY`](ds:cfg?id=app-stripe-key-m4w8k2qn) from its environment.
```

```console
$ ds why app-stripe-key-m4w8k2qn --chain
…
app-stripe-key-m4w8k2qn  STRIPE_KEY  internal/pay/stripe.go:7
  from gh-stripe-key-r4t6x2mb  ${{ secrets.STRIPE_KEY }}  .github/workflows/deploy.yml:7
    from op-stripe-key-p9c2v7ld  op://Platform/stripe-prod/credential  .env.tpl:1  TRUTH
$ ds render docs/payments.md
# Payments

Stripe credentials:

- `app-stripe-key-m4w8k2qn` env `STRIPE_KEY` — [internal/pay/stripe.go:7](internal/pay/stripe.go#L7-L7)
  - from `gh-stripe-key-r4t6x2mb` github `${{ secrets.STRIPE_KEY }}` — [.github/workflows/deploy.yml:7](.github/workflows/deploy.yml#L7-L7) · synced by `scripts/sync-secrets.sh`
    - from `op-stripe-key-p9c2v7ld` 1password `op://Platform/stripe-prod/credential` — [.env.tpl:1](.env.tpl#L1-L1) · **truth**

The app reads STRIPE_KEY from its environment.
```

Every check validates the shape of each chain from the files alone, with no provider access:

```console
$ ds check                 # truth=true removed from the root, and a secret with no source added
.env.tpl
  1	error    chain broken       chain of 3 has 0 truth defs
      fix: the chain rooted at op-stripe-key-p9c2v7ld has 0 truth=true defs; exactly one is required
extra.env
  1	warning  unsourced          secret with no declared source
      fix: sentry-dsn-a2b3c4d5 is a secret with no from= and no truth=true; declare where it is copied from or mark it the truth
…
```

A `from=` naming an undefined id, or a `from=` cycle, is also `chain broken`. The files-only check does not compare names between hops: renaming the GitHub secret to `STRIPE_SECRET` while the code still reads `STRIPE_KEY` produced no finding in testing. To be told when a hop changes, cite that hop's def from the runbook, since a `ds:chain` citation is held to its own id only.

### A truth on one machine

A value that lives only in a file outside git, such as a local `.env.prod`, can still be declared with a remote def and `local=true`. It is reported as `unverifiable` (a warning), which is the honest state for something CI cannot read:

```markdown
<!-- ds:def id=prod-env-file-x4y5z6a7 file=.env.prod local=true pick=env:STRIPE_KEY -->
```

```console
  8	warning  unverifiable       local=true def is only readable on its own machine
      fix: prod-env-file-x4y5z6a7 is local=true and cannot be read on this machine; this is expected in CI
```

In testing this warning appeared on the machine holding the file as well.

## Verifying secrets with --resolve

`ds check --resolve` asks a plugin per provider whether each secret address in a chain exists and, where the provider can be read, for a SHA-256 of the value. The plugin is an executable named `ds-resolve-<provider>` on PATH. It hashes in its own memory and returns only existence and the hash; the host refuses any other reply.

### The shipped plugins

The install script and the release archives include five, each wrapping a CLI that must be installed and logged in:

| Executable | Wraps | Answers |
|---|---|---|
| `ds-resolve-github` | `gh secret list --json name` | existence only; GitHub never returns values |
| `ds-resolve-onepassword` | `op read --no-newline <ref>` | existence and hash |
| `ds-resolve-aws` | `aws secretsmanager get-secret-value --secret-id <arn>` | existence and hash |
| `ds-resolve-gcp` | `gcloud secrets versions access latest --secret <name>` | existence and hash |
| `ds-resolve-vault` | `vault kv get -field=<field> <path>`; the address is `<mount>/<path>#<field>`, with `value` as the default field | existence and hash |

`ds` looks up a plugin by the provider name the address implies: `github`, `1password`, `aws`, `gcp`, `vault`, or whatever `source=` names. For `op://` addresses that name is `1password`, so it runs `ds-resolve-1password`, while the shipped executable is `ds-resolve-onepassword`. Until the names agree, link one to the other:

```sh
ln -s "$(command -v ds-resolve-onepassword)" "$(dirname "$(command -v ds-resolve-onepassword)")/ds-resolve-1password"
```

With that link in place the shipped 1Password plugin answered correctly in testing (against a stand-in `op`). It reports an address as not existing whenever `op read` fails, so a locked or signed-out `op` shows up as `resolve failed` rather than `unverifiable`.

### Configure it

```toml
[resolve]
providers = ["1password", "aws", "github"]   # only these are consulted
store_hash = true                            # keep truth hashes in .ds/hashes.json to detect rotation
```

`--resolve` is the switch that turns resolution on; the `enabled` key in `[resolve]` is read but does not change what `--resolve` does. A provider outside `providers`, or one whose plugin is missing, makes that hop `unverifiable` (a warning) rather than failing:

```console
$ PATH=/usr/bin:/bin ds check --resolve
.env.tpl
  1	warning  unverifiable       provider 1password not reachable: procplugin: plugin executable not found: ds-resolve-1password
      fix: external link checks need --resolve with network access
…
internal/pay/stripe.go
  6	warning  unverifiable       provider env not reachable: procplugin: plugin executable not found: ds-resolve-env
…
```

No `ds-resolve-env` ships, so `source=env` hops stay `unverifiable` under `--resolve`; leave `env` out of `providers` and the message says why. The `fix:` line on these findings mentions external links; for a secret it means the plugin above.

### What it reports

The repository above gained a second copy of the key in AWS:

```sh
STRIPE_AWS=arn:aws:secretsmanager:eu-west-1:123456789012:secret:stripe   # ds:def id=aws-stripe-key-c3d4e5f6 secret=true from=op-stripe-key-p9c2v7ld sync=scripts/push-aws.sh
```

On the first run with `store_hash = true`, everything matched and the truth's hash was stored. Then the 1Password item was rotated and the AWS copy was not:

```console
$ ds check --resolve
.env.tpl
  1	warning  rotated            op-stripe-key-p9c2v7ld was rotated since its hash was stored
      fix: the truth op-stripe-key-p9c2v7ld changed since its stored hash; run the syncs of its copies and ack the runbooks
  2	error    out of sync        aws-stripe-key-c3d4e5f6 differs from truth op-stripe-key-p9c2v7ld
      fix: aws-stripe-key-c3d4e5f6 differs from its truth op-stripe-key-p9c2v7ld; run the sync (scripts/push-aws.sh) and ack the runbooks that cite the chain
…
1 error, 2 warning, 1 info, 2 none
```

And with the GitHub secret deleted:

```console
.github/workflows/deploy.yml
  7	error    resolve failed     ${{ secrets.STRIPE_KEY }} does not exist at github
      fix: ${{ secrets.STRIPE_KEY }} does not exist at github; fix the address in .github/workflows/deploy.yml:7
```

| State | Severity | Meaning |
|---|---|---|
| `resolve failed` | error | the provider says the address does not exist |
| `out of sync` | error | a copy's hash differs from its truth's |
| `rotated` | warning | the truth's hash differs from the one stored in `.ds/hashes.json` (needs `store_hash`) |
| `unverifiable` | warning | the plugin is missing, not allowed, failed, or broke the protocol |

GitHub hops are existence-only, since GitHub lists names and never values, so they can be `resolve failed` but never `out of sync`. Run `--resolve` on a scheduled job where the provider CLIs are logged in, not on pull requests.

## Write your own resolver

A resolver is any executable named `ds-resolve-<provider>` that speaks the process-plugin protocol: `ds` writes two JSON lines to its stdin and reads two JSON lines from its stdout.

```text
host → plugin   {"op":"handshake","protocol":1}
host → plugin   {"op":"resolve","addr":"op://Platform/stripe-prod/credential","want":"hash"}
plugin → host   {"protocol":1,"name":"1password","kinds":["resolve"]}
plugin → host   {"exists":true,"hash":"<64 lowercase hex characters>"}
```

`want` is `exists` or `hash`; GitHub addresses are asked for `exists` only, with the address reduced to the secret's name. The reply may carry only `exists`, `hash`, and `error`. The host enforces that, not the plugin: a reply with any other field, or a `hash` that is not 64 lowercase hex characters, is refused before anything downstream sees it. Each call has a 10 second timeout and a 1 MiB reply cap, and a plugin answering another protocol number is refused by name.

This shell script is a complete resolver. It answers from a file of `address value` lines instead of a real store, which is how the outputs above were produced; install it under the provider names you want (`ds-resolve-1password`, `ds-resolve-aws`, …):

```sh
#!/bin/sh
# fake-resolver: a stand-in secret store for trying `ds check --resolve`.
# Install it under the name ds looks for (ds-resolve-<provider>). It answers
# from $FAKE_SECRETS, a file of `address value` lines, and never prints a
# value: only whether the address exists and, when asked, a SHA-256 of it.
provider=${0##*/ds-resolve-}
read -r handshake   # {"op":"handshake","protocol":1}
read -r request     # {"op":"resolve","addr":"…","want":"exists"|"hash"}
echo "{\"protocol\":1,\"name\":\"$provider\",\"kinds\":[\"resolve\"]}"
addr=$(printf '%s' "$request" | sed -n 's/.*"addr":"\([^"]*\)".*/\1/p')
value=$(awk -v a="$addr" '$1 == a { print $2 }' "$FAKE_SECRETS")
case "$value:$request" in
  :*)                echo '{"exists":false}' ;;
  *'"want":"hash"'*) echo "{\"exists\":true,\"hash\":\"$(printf '%s' "$value" | shasum -a 256 | cut -d' ' -f1)\"}" ;;
  *)                 echo '{"exists":true}' ;;
esac
```

Test a plugin by hand before pointing `ds` at it:

```console
$ printf '%s\n' '{"op":"handshake","protocol":1}' '{"op":"resolve","addr":"op://Platform/stripe-prod/credential","want":"hash"}' | ds-resolve-1password
{"protocol":1,"name":"1password","kinds":["resolve"]}
{"exists":true,"hash":"92e4d4207cb202d43ce235e432afe7569cec29152e3a01d070577b2006484929"}
```

A plugin that tries to return the value is stopped by the host:

```console
$ cat ds-resolve-aws
#!/bin/sh
read -r a; read -r b
echo '{"protocol":1,"name":"aws","kinds":["resolve"]}'
echo '{"exists":true,"value":"sk_live_one"}'
$ ds check --resolve
.env.tpl
  2	warning  unverifiable       provider aws not reachable: procplugin: resolver reply carried more than existence and hash: unexpected field(s) value
```

In Go, `procplugin.Serve` does the handshake and the framing:

```go
// Command ds-resolve-aws is a stand-in AWS resolver: it answers from a map
// instead of calling AWS. A real one reads the secret, hashes it in memory,
// and returns the same two fields.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"

	"github.com/ubgo/docsync/procplugin"
)

var store = map[string]string{
	"arn:aws:secretsmanager:eu-west-1:123456789012:secret:stripe": "sk_live_two",
}

func main() {
	err := procplugin.Serve(os.Stdin, os.Stdout, "aws", []string{procplugin.KindResolve}, func(req procplugin.Request) procplugin.Response {
		if req.Op != procplugin.OpResolve {
			return procplugin.Response{Error: "unsupported op " + req.Op}
		}
		value, ok := store[req.Addr]
		resp := procplugin.Response{Exists: &ok}
		if ok && req.Want == procplugin.WantHash {
			sum := sha256.Sum256([]byte(value))
			resp.Hash = hex.EncodeToString(sum[:])
		}
		return resp
	})
	if err != nil {
		os.Exit(1)
	}
}
```

The same protocol serves other plugin kinds: `ds-pick-<scheme>` for a new `pick=` scheme and `ds-<verb>` for a new directive verb, each listed in config (`[plugins] picks = […]`, `verbs = […]`), and `ds-records-<source>` for a `ds:table` source named in `[records] source`. The request and reply shapes for each are in [the spec](../SPEC.md#374-process-plugins-for-any-language).

## Environments

The same id can be defined once per environment with `env=`; a citation without `env=` resolves to the default environment, and `ds check --env` or `ds render --env` picks another.

```toml
[env]
default = "prod"
known = ["prod", "staging"]
```

```sh
# deploy/prod.env
API_HOST=api.example.com   # ds:def id=api-host-d4k8w2mn env=prod
# deploy/staging.env
API_HOST=api.staging.example.com   # ds:def id=api-host-d4k8w2mn env=staging
```

```markdown
Clients call [api.example.com](ds:cfg?id=api-host-d4k8w2mn).

The staging stack answers on [api.staging.example.com](ds:cfg?id=api-host-d4k8w2mn&env=staging).
```

```console
$ ds facts
ID                 VALUE                    WHERE                 CITED BY
api-host-d4k8w2mn  api.example.com          deploy/prod.env:1     2
api-host-d4k8w2mn  api.staging.example.com  deploy/staging.env:1  2
$ ds render docs/hosts.md --env staging | sed -n 3p
Clients call api.staging.example.com.
```

Each environment's def is tracked separately, so a change to prod is never compared against staging. Two consequences to expect:

- Under `ds check --env staging`, a citation with no `env=` resolves to the staging def. A sentence acked against prod's value then reports `unacked` with class `value`, which is the tool telling you the sentence is not true in staging.
- A citation of an environment with no def is `broken`: `api-host-d4k8w2mn is not defined for env=dev`, with the fix `add one or cite a defined environment`.

Secrets work the same way: one `truth=true` def per environment for the same id.

## Where this state lives

| File | Holds | Committed |
|---|---|---|
| `.ds/runs.json` | last outcome of each `ds:run` | no |
| `.ds/urls.json` | cached `ds:url` results | no |
| `.ds/hashes.json` | stored truth hashes for `rotated` | no |

All three are machine-local and in the `.ds/.gitignore` that `ds init` writes. In CI they start empty on every run unless your workflow caches them. See [Configuration](configuration.md) for every key, and [the spec](../SPEC.md#12-secrets-chains-and-environments) for the full rules.
