# Secrets, runs and URLs

This page covers the three features that reach outside the repository: `ds:run` executes a command named in a doc, `ds:url` watches an external link, and secret chains document where a credential lives and where it is copied, optionally verified against the provider. It is for whoever owns runbooks, operational docs, or the scheduled job that runs `ds check --run --resolve`.

All three are off unless you ask for them. A plain `ds check` executes nothing, fetches nothing, and contacts no provider; `--run` and `--resolve` are the switches, and both are refused on pull requests from forks. Everything below was run with `ds` against throwaway repositories, with a local web server and stand-in plugins where a real site or provider was not reachable.

## ds:run: commands in runbooks

<!-- doctest
git init -q -b main .
ds init
-->

### Turn it on

Add a `[run]` table to `.ds/config.toml`:

```toml file=.ds/config.toml append=true

[run]
enabled = true
allow = ["runbooks/**"]     # docs where cmd= and file= may appear
timeout = "30s"             # per command, the default; a positive Go duration
shell = "sh"                # the default; looked up on PATH

[run.env.staging]
DATABASE_URL = "$STAGING_DATABASE_URL"
```

### Three ways to name what runs

```markdown file=runbooks/deploy.md
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

The script the `file=` form names, and the def the `id=` form names:

```sh file=scripts/smoke.sh
echo "smoke: all endpoints answered"
```

```sh file=db/count.sh
# ds:def id=count-users-q7n2m4kt runnable=true
echo "users: 42 in $DS_ENV ($DATABASE_URL)"
```

And one more page, outside `run.allow`:

```markdown file=docs/readme.md
# Readme

<!-- ds:run cmd="echo hi" expect=ok -->
```

`expect=` decides success, and every mode first needs exit status zero: `ok` (or nothing) asks for nothing more; `rows` means some output; an HTTP status such as `200` means the last status the output reports, the last `HTTP/…` status line when there is one (as `curl -sIL` prints) or else the last non-blank line (as `curl -s -o /dev/null -w '%{http_code}'` prints); any other value is a substring the output must contain. `show=command`, `show=output`, or `show=none` limit what `ds render` prints; the default shows both. Every command gets `run.timeout` unless its directive says `timeout=`, a positive duration that replaces it for that command; a `timeout=` that does not parse is a `problem` finding and the command does not run.

<!-- doctest
ds scan
-->

### Run it

Without `--run`, every `ds:run` is reported as `skipped` (severity info) and nothing executes:

```console
$ ds check
docs/readme.md
  3	info     skipped            run not executed
      fix: pass --run to execute ds:run directives where they are enabled
runbooks/deploy.md
…
4 info
```

<!-- doctest
perl -pi -e 's/^enabled = true/enabled = false/' .ds/config.toml
-->

With `--run` but `enabled = false`, `ds` says so and runs nothing:

```console
$ ds check --run
run.enabled is false; nothing executed
docs/readme.md
  3	info     skipped            run not executed
      fix: pass --run to execute ds:run directives where they are enabled
…
```

<!-- doctest
perl -pi -e 's/^enabled = false/enabled = true/' .ds/config.toml
-->

Enabled:

```console
$ STAGING_DATABASE_URL=postgres://staging-db/app ds check --run
docs/readme.md:3  run skipped: cmd= is allowed only in docs matching run.allow
runbooks/deploy.md:3  run ok: go version
runbooks/deploy.md:5  run ok: sh 'scripts/smoke.sh'
runbooks/deploy.md:7  run ok: echo "users: 42 in $DS_ENV ($DATABASE_URL)"
docs/readme.md
  3	info     skipped            run not executed: cmd= is allowed only in docs matching run.allow
1 info, 3 ok
```

Two more lines in the runbook, one with a timeout shorter than the command and one that fails:

```markdown file=runbooks/deploy.md append=true

Slow: <!-- ds:run cmd="sleep 2" expect=ok timeout=1s -->

Fails: <!-- ds:run cmd="exit 3" expect=ok -->
```

<!-- doctest
ds scan
-->

The `sleep 2` is stopped at its directive's `timeout=1s`, which replaces the 30 seconds of `run.timeout` for that line. A command that misses its expectation or its time limit prints `run FAILED` and makes `ds check` exit 1, although it adds no row to the findings summary:

```console
$ STAGING_DATABASE_URL=postgres://staging-db/app ds check --run; echo "exit=$?"
…
runbooks/deploy.md:9  run FAILED: sleep 2
runbooks/deploy.md:11  run FAILED: exit 3
docs/readme.md
  3	info     skipped            run not executed: cmd= is allowed only in docs matching run.allow
runbooks/deploy.md
  11	error    run failed         run failed: exit 3
      fix: run `exit 3` by hand to see why; fix the command, or the sentence at runbooks/deploy.md:11 if it no longer holds
1 error, 1 info, 4 ok
exit=1
```

### Environments

A `ds:run` with `env=staging` (or a run under `ds check --run --env staging`, or `[env] default`) gets `DS_ENV=staging` plus every variable in `[run.env.staging]`. Values are expanded from the environment of the `ds` process, so `"$STAGING_DATABASE_URL"` keeps the credential out of the committed config; the CI job that runs `--run` provides it.

### Results and rendering

Each outcome is stored in `.ds/runs.json`, keyed by doc and line, with the command, its output (capped at 64 KiB), whether it passed, and when:

```console
$ grep -A6 '"runbooks/deploy.md:7"' .ds/runs.json
  "runbooks/deploy.md:7": {
    "command": "echo \"users: 42 in $DS_ENV ($DATABASE_URL)\"",
    "output": "users: 42 in staging (postgres://staging-db/app)\n",
    "ok": true,
    "expect": "rows",
    "at": "2026-10-01T03:39:00.600576Z"
  },
```

`ds render` shows the command and its last result:

~~~~console
$ ds render runbooks/deploy.md
# Deploy

Check the toolchain:

```sh
go version
```

_ok · as of 2026-10-01T03:39:00Z…

```text
go version …
```
…
~~~~

`.ds/runs.json` is machine-local and ignored by the `.ds/.gitignore` that `ds init` writes. Output is stored and rendered as the command printed it, so a command that echoes a credential, as the example above echoes `DATABASE_URL`, puts it in that file and on the rendered page. Have runbook commands print counts and statuses, not connection strings.

### The shell

Commands run under `sh` by default, found on PATH; on Windows, Git for Windows provides it. A team whose commands are written for another shell names it with `[run] shell`, and `ds` never substitutes one by itself. When a command is about to run and the shell is missing, the check stops instead of skipping. With `shell = "elvish"` on a machine that has no `elvish`:

<!-- doctest
perl -pi -e 's/^shell = "sh"/shell = "elvish"/' .ds/config.toml
-->

```console
$ ds check --run; echo "exit=$?"
docs/readme.md:3  run skipped: cmd= is allowed only in docs matching run.allow
ds: the shell is not on PATH: elvish (on Windows, Git for Windows provides sh; or name another shell with [run] shell in .ds/config.toml)
exit=2
```

<!-- doctest
perl -pi -e 's/^shell = "elvish"/shell = "sh"/' .ds/config.toml
-->

A repository with nothing allowed to run is never asked for a shell. A `run.timeout` that does not parse stops every command at config load, naming the key:

<!-- doctest
perl -pi -e 's/^timeout = "30s"/timeout = "half a minute"/' .ds/config.toml
-->

```console
$ ds check
ds: config: invalid value: run.timeout "half a minute" must be a positive duration such as 30s
```

<!-- doctest
perl -pi -e 's/^timeout = "half a minute"/timeout = "30s"/' .ds/config.toml
-->

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

`href` is the link; `title` is a substring the page's `<title>` must contain. Without `--resolve` each one is a warning that it was not checked. With it, and `[resolve] enabled = true` in the config, `ds` fetches the page, follows redirects, and reports what it found.

The examples here run against a small local server standing in for the web: `/guide` answers with the title "Storage guide, 2nd edition", `/old` redirects to `/new`, and everything else is a 404.

```python file=../server.py
import http.server

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/old":
            self.send_response(301)
            self.send_header("Location", "/new")
            self.end_headers()
            return
        if self.path in ("/new", "/guide"):
            self.send_response(200)
            self.send_header("Content-Type", "text/html")
            self.end_headers()
            self.wfile.write(b"<html><title>Storage guide, 2nd edition</title></html>")
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, *args):
        pass

http.server.HTTPServer(("127.0.0.1", 8765), Handler).serve_forever()
```

<!-- doctest
python3 ../server.py >/dev/null 2>&1 & echo $! > ../server.pid
sleep 1
mkdir -p ../links
cd ../links
git init -q -b main .
ds init
-->

```markdown file=docs/links.md
# Links

See the [storage guide](ds:url?href=http://127.0.0.1:8765/guide&title="Storage guide").

The [old page](ds:url?href=http://127.0.0.1:8765/old) moved.

The [TOAST notes](ds:url?href=http://127.0.0.1:8765/toast&title=TOAST) are gone.

The [manual](ds:url?href=http://127.0.0.1:8765/guide&title=Manual) was retitled.

Quoted: [guide](ds:url?href=http://127.0.0.1:8765/guide&title="Storage").

Escaped: [guide](ds:url?href=http://127.0.0.1:8765/guide&title=Storage%20guide).

<!-- ds:url href=http://127.0.0.1:8765/guide title="Storage guide" -->
```

A link expected to answer something other than success says so with `expect=`, the status the final response must have:

```markdown file=docs/links.md append=true

The [retired API](ds:url?href=http://127.0.0.1:8765/v1&expect=404) is expected to be gone.
```

`--resolve` contacts nothing until the repository consents in its config, as `[run] enabled` does for `--run`:

```toml file=.ds/config.toml append=true

[resolve]
enabled = true
```

<!-- doctest
ds scan
-->

```console
$ ds check
docs/links.md
  5	warning  unverifiable       external link not checked
      fix: external link checks need --resolve with network access
…
$ ds check --resolve
docs/links.md
  5	warning  url moved          http://127.0.0.1:8765/old redirects to http://127.0.0.1:8765/new
      fix: the link http://127.0.0.1:8765/old now redirects to http://127.0.0.1:8765/new; update it at docs/links.md:5
  7	error    dead               http://127.0.0.1:8765/toast returned 404
      fix: the link http://127.0.0.1:8765/toast returned 404; update or remove it at docs/links.md:7
  9	warning  retitled           title is now "Storage guide, 2nd edition"
      fix: the page at http://127.0.0.1:8765/guide no longer has title "Manual"; confirm it is still the right page
  11	warning  retitled           title is now "Storage guide, 2nd edition"
      fix: the page at http://127.0.0.1:8765/guide no longer has title "\"Storage\""; confirm it is still the right page
1 error, 3 warning, 3 none
```

Line 3 is missing from both runs, and line 11 is reported although the page title does contain "Storage"; [Writing the link form](#writing-the-link-form) explains both. Lines 13, 15 and 18 are `ok` (line 18 answers the 404 it expects) and, like every `ok`, are counted in the summary rather than listed.

<!-- doctest
kill $(cat ../server.pid)
-->

| State | Severity | Meaning |
|---|---|---|
| `ok` | none | status below 400, or the one `expect=` names; no redirect; title matches |
| `dead` | error | status 400 or above, or with `expect=`, any status other than it |
| `url moved` | warning | the final URL after redirects differs from `href` |
| `retitled` | warning | the page title no longer contains `title` |
| `unverifiable` | warning | not checked: no `--resolve`, `resolve.enabled` off, or the request got no answer |

### Cache and rate

Results are cached in `.ds/urls.json` (machine-local) for `url.ttl`, default `7d`, and requests are spaced by `url.rate_per_minute`, default `30`, so a page with a hundred links does not hammer one host:

```toml
[url]
ttl = "7d"             # whole number with m, h, d or w
rate_per_minute = 30
```

Only an HTTP answer is cached. On a machine that cannot reach the host, `--resolve` reports the link `unverifiable` with the request's error and caches nothing for it, so the next run with a network checks it again.

### Writing the link form

Two things to know, both seen in testing:

- **No spaces inside a link target.** Markdown ends a link at a space, so `[x](ds:url?href=…&title="Storage guide")` is not a link at all and the directive is silently ignored, as line 3 above was. Write the space as `%20` or `+`, or use the comment form, where quotes work: `<!-- ds:url href=… title="Storage guide" -->`.
- **No quotes around `title` in the link form.** They are kept as part of the value, so `title="Storage"` looks for a title containing `"Storage"` with the quote marks and reports `retitled`, as line 11 above shows. Write `title=Storage`.

## Secrets: addresses, never values

docsync treats a secret as an address: `${{ secrets.STRIPE_KEY }}`, `op://Platform/stripe-prod/credential`, an AWS Secrets Manager ARN, a GCP `projects/…/secrets/…` name, a `vault:` path, or an environment variable name. It hashes and renders the address and never stores, prints, or renders a value. Mark a def `secret=true`, or list files in `[secret] paths`:

```toml
[secret]
paths = ["**/.env*", "**/secrets/**"]
```

<!-- doctest
mkdir -p ../pay
cd ../pay
git init -q -b main .
ds init
-->

A secret def whose content is not an address shape is blanked by the scanner and only its hash is kept, so a change is still reported, as class `unknown`, without anything showing what changed:

```sh file=local.env
API_TOKEN=tok_abc123   # ds:def id=api-token-h7j8k9m2 secret=true
```

```console
$ ds scan
1 files, 1 defs, 0 refs, 0 problems, 0 skipped
$ ds read api-token-h7j8k9m2        # prints nothing
$ grep -rl tok_abc123 .ds || echo "value not under .ds"
value not under .ds
```

<!-- doctest
rm local.env
-->

### Chains

Most teams keep a secret in one place and copy it to others. `truth=true` marks the single root; `from=` links each copy to where it came from; `sync=` names the job that copies it.

```sh file=.env.tpl
STRIPE_KEY=op://Platform/stripe-prod/credential   # ds:def id=op-stripe-key-p9c2v7ld secret=true truth=true
```

```yaml file=.github/workflows/deploy.yml
name: deploy
on: workflow_dispatch
jobs:
  deploy:
    runs-on: ubuntu-latest
    env:
      STRIPE_KEY: ${{ secrets.STRIPE_KEY }}   # ds:def id=gh-stripe-key-r4t6x2mb secret=true from=op-stripe-key-p9c2v7ld sync=scripts/sync-secrets.sh
    steps:
      - run: echo deploy
```

```go file=internal/pay/stripe.go
package pay

import "os"

func key() string {
	// ds:def id=app-stripe-key-m4w8k2qn secret=true source=env from=gh-stripe-key-r4t6x2mb pick=regex:'"(\w+)"'
	key := os.Getenv("STRIPE_KEY")
	return key
}
```

The provider is read from the address shape; a bare environment variable name says nothing, so that hop declares `source=env`. The `pick=` keeps just the variable name, so the doc renders `STRIPE_KEY` rather than the whole statement.

A page cites the chain and the name:

```markdown file=docs/payments.md
# Payments

Stripe credentials: <!-- ds:chain id=app-stripe-key-m4w8k2qn -->

The app reads [`STRIPE_KEY`](ds:cfg?id=app-stripe-key-m4w8k2qn) from its environment.
```

<!-- doctest
ds scan
git add -A
git commit -qm chain
-->

```console
$ ds why app-stripe-key-m4w8k2qn --chain
…
app-stripe-key-m4w8k2qn  STRIPE_KEY  internal/pay/stripe.go:7
  from gh-stripe-key-r4t6x2mb  ${{ secrets.STRIPE_KEY }}  .github/workflows/deploy.yml:7
    from op-stripe-key-p9c2v7ld  op://Platform/stripe-prod/credential  .env.tpl:1  TRUTH
$ ds render docs/payments.md
# Payments

Stripe credentials:

- `app-stripe-key-m4w8k2qn` env `STRIPE_KEY` — [internal/pay/stripe.go:7](../internal/pay/stripe.go#L7-L7)
  - from `gh-stripe-key-r4t6x2mb` github `${{ secrets.STRIPE_KEY }}` — [.github/workflows/deploy.yml:7](../.github/workflows/deploy.yml#L7-L7) · synced by `scripts/sync-secrets.sh`
    - from `op-stripe-key-p9c2v7ld` 1password `op://Platform/stripe-prod/credential` — [.env.tpl:1](../.env.tpl#L1-L1) · **truth**

The app reads STRIPE_KEY from its environment.
```

Every check validates the shape of each chain from the files alone, with no provider access. Here `truth=true` was taken off the root, and a secret with no source was added in `extra.env`:

<!-- doctest
perl -pi -e 's/ truth=true//' .env.tpl
printf 'SENTRY_DSN=op://Platform/sentry/dsn   # ds:def id=sentry-dsn-a2b3c4d5 secret=true\n' > extra.env
-->

```console
$ ds check
.env.tpl
  1	error    chain broken       chain of 3 has 0 truth defs
      fix: the chain rooted at op-stripe-key-p9c2v7ld has 0 truth=true defs; exactly one is required
extra.env
  1	warning  unsourced          secret with no declared source
      fix: sentry-dsn-a2b3c4d5 is a secret with no from= and no truth=true; declare where it is copied from or mark it the truth
…
```

<!-- doctest
git show HEAD:.env.tpl > .env.tpl
rm extra.env
-->

A `from=` naming an undefined id, or a `from=` cycle, is also `chain broken`. The files-only check does not compare names between hops: renaming the GitHub secret to `STRIPE_SECRET` while the code still reads `STRIPE_KEY` produces no finding:

```console
$ perl -pi -e 's/secrets.STRIPE_KEY/secrets.STRIPE_SECRET/' .github/workflows/deploy.yml
$ ds check
2 ok
```

<!-- doctest
git show HEAD:.github/workflows/deploy.yml > .github/workflows/deploy.yml
-->

To be told when a hop changes, cite that hop's def from the runbook, since a `ds:chain` citation is held to its own id only.

### A truth on one machine

A value that lives only in a file outside git, such as a local `.env.prod`, can still be declared with a remote def and `local=true`. Where the file is absent, as in CI, it is reported as `unverifiable` (a warning), which is the honest state for something that machine cannot read:

```markdown file=docs/payments.md append=true

<!-- ds:def id=prod-env-file-x4y5z6a7 file=.env.prod local=true pick=env:STRIPE_KEY -->
```

<!-- doctest
ds scan
-->

```console
$ ds check
docs/payments.md
  7	warning  unverifiable       local=true def is only readable on its own machine
      fix: prod-env-file-x4y5z6a7 is local=true and cannot be read on this machine; this is expected in CI
…
```

On the machine holding the file, `ds check` reads it and the def is `ok` once its `pick=` finds the key there, so the warning goes away and the def counts among the `ok` findings (here it is still `uncovered`, since no page cites it). Nothing read from the file is hashed into the ledger or stored, since the ledger is shared and the file is not:

```console
$ printf 'STRIPE_KEY=op://Platform/stripe-prod/credential\n' > .env.prod
$ ds check
docs/payments.md
  7	info     uncovered          defined but never cited or covered
      fix: prod-env-file-x4y5z6a7 is defined but nothing cites or covers it; cite it from a page or remove the def
1 info, 3 none
```

A file that is there but lacks the key is `pick failed`, an error, because that is a mistake on the machine that can see it:

```console
$ printf 'OTHER=1\n' > .env.prod
$ ds check
docs/payments.md
  7	error    pick failed        .env.prod is present here but pick=env:STRIPE_KEY failed: pick: nothing matched: env STRIPE_KEY
      fix: fix the def's file= or pick= at docs/payments.md:7
  7	info     uncovered          defined but never cited or covered
      fix: prod-env-file-x4y5z6a7 is defined but nothing cites or covers it; cite it from a page or remove the def
1 error, 1 info, 2 none
```

<!-- doctest
rm .env.prod
git show HEAD:docs/payments.md > docs/payments.md
ds scan
-->

## Verifying secrets with --resolve

`ds check --resolve` asks a plugin per provider whether each secret address in a chain exists and, where the provider can be read, for a SHA-256 of the value. The plugin is an executable named `ds-resolve-<provider>` on PATH. It hashes in its own memory and returns only existence and the hash; the host refuses any other reply.

### The shipped plugins

The install script and the release archives include six. Five wrap a CLI that must be installed and logged in; `ds-resolve-env` reads its own environment:

| Executable | Wraps | Answers |
|---|---|---|
| `ds-resolve-github` | `gh secret list --json name` | existence only; GitHub never returns values |
| `ds-resolve-onepassword` | `op read --no-newline <ref>` | existence and hash; a signed-out or locked `op` is an error (`unverifiable`), and only `op` saying the vault, item or field does not exist is `resolve failed` |
| `ds-resolve-aws` | `aws secretsmanager get-secret-value --secret-id <arn>` | existence and hash |
| `ds-resolve-gcp` | `gcloud secrets versions access latest --secret <name>` | existence and hash |
| `ds-resolve-vault` | `vault kv get -field=<field> <path>`; the address is `vault:<mount>/<path>#<field>`, the prefix dropped before the call, with `value` as the default field | existence and hash |
| `ds-resolve-env` | the environment `ds check` runs in; the address is a variable name, from a `source=env` def | existence and hash; an unset variable is an error (`unverifiable`) |

The examples below put a directory of stand-ins first on PATH: the [stand-in resolver](#write-your-own-resolver) as `ds-resolve-github` and `ds-resolve-aws`, answering from a file of `address value` lines, and a stand-in `op` reading the same file, so the shipped 1Password plugin runs for real.

<!--
```sh file=../plugins/fake-resolver
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
  *'"want":"hash"'*) echo "{\"exists\":true,\"hash\":\"$(printf '%s' "$value" | { sha256sum 2>/dev/null || shasum -a 256; } | cut -d' ' -f1)\"}" ;;
  *)                 echo '{"exists":true}' ;;
esac
```

```sh file=../plugins/op
#!/bin/sh
# A stand-in for `op read --no-newline <ref>`: prints the value $FAKE_SECRETS holds for <ref>.
awk -v a="$3" '$1 == a { printf "%s", $2; found = 1 } END { exit !found }' "$FAKE_SECRETS"
```
-->

<!-- doctest
chmod +x ../plugins/fake-resolver ../plugins/op
ln -s fake-resolver ../plugins/ds-resolve-github
ln -s fake-resolver ../plugins/ds-resolve-aws
printf 'op://Platform/stripe-prod/credential sk_live_one\narn:aws:secretsmanager:eu-west-1:123456789012:secret:stripe sk_live_one\nSTRIPE_KEY present\n' > ../secrets.txt
export FAKE_SECRETS=../secrets.txt
-->

`ds` asks the plugin for the provider the address implies: `github`, `1password`, `aws`, `gcp`, `vault`, or whatever `source=` names. The executable is `ds-resolve-<provider>`, except for `1password`, whose plugin is `ds-resolve-onepassword`. A `source=env` hop is answered by `ds-resolve-env`, which hashes the variable from the environment `ds check` runs in.

`--resolve` asks; the repository must also consent. Without `[resolve] enabled = true`, it says so on stderr and contacts nothing:

```console
$ PATH="$PWD/../plugins:$PATH" ds check --resolve 2>&1 | head -1
resolve.enabled is false; no provider or link was contacted
```

### Configure it

```toml file=.ds/config.toml append=true

[resolve]
enabled = true                                      # --resolve contacts providers and links
providers = ["1password", "aws", "env", "github"]   # only these are consulted
store_hash = true                                   # keep truth hashes in .ds/hashes.json to detect rotation
```

A provider outside `providers` is `unverifiable` with the reason. So is a hop whose plugin is missing, or whose CLI is signed out or locked: only a provider saying the address does not exist makes `resolve failed`. An environment variable that is not set where `ds check` runs is `unverifiable` too, since its absence on one machine says nothing about the deployment:

```console
$ PATH="$PWD/../plugins:$PATH" ds check --resolve
internal/pay/stripe.go
  6	warning  unverifiable       provider env not reachable: procplugin: plugin reported an error: STRIPE_KEY is not set in the environment ds check runs in
      fix: install the ds-resolve plugin for env on PATH and log in to the CLI it wraps, or leave env out of resolve.providers; the message says what failed
1 warning, 2 none
```

The runs below set it, as the deploy job that runs `--resolve` would.

### What it reports

The repository gains a second copy of the key, in AWS:

```sh file=.env.tpl append=true
STRIPE_AWS=arn:aws:secretsmanager:eu-west-1:123456789012:secret:stripe   # ds:def id=aws-stripe-key-c3d4e5f6 secret=true from=op-stripe-key-p9c2v7ld sync=scripts/push-aws.sh
```

<!-- doctest
ds scan
-->

On the first run every hop matches, the environment variable included, and the truth's hash is stored:

```console
$ STRIPE_KEY=sk_live_one PATH="$PWD/../plugins:$PATH" ds check --resolve
.env.tpl
  2	info     uncovered          defined but never cited or covered
      fix: aws-stripe-key-c3d4e5f6 is defined but nothing cites or covers it; cite it from a page or remove the def
1 info, 2 none
$ cat .ds/hashes.json
{
  "op-stripe-key-p9c2v7ld": "a245b332a780393cd4fafdcb467e7bb40ae2880f160782704d5bc2fdcab6bd8f"
}
```

Then the 1Password item is rotated and neither copy is synced. Each copy that still holds the old value is a `stale copy`, and the stored hash is left as it was, so the next run says the same until the syncs run:

<!-- doctest
perl -pi -e 's/^(op:\S+) sk_live_one/$1 sk_live_two/' ../secrets.txt
-->

```console
$ STRIPE_KEY=sk_live_one PATH="$PWD/../plugins:$PATH" ds check --resolve
.env.tpl
  1	warning  rotated            op-stripe-key-p9c2v7ld was rotated since its hash was stored
      fix: the truth op-stripe-key-p9c2v7ld changed since its stored hash; run the syncs of its copies and ack the runbooks
  2	error    stale copy         aws-stripe-key-c3d4e5f6 still holds the value from before op-stripe-key-p9c2v7ld was rotated
      fix: aws-stripe-key-c3d4e5f6 still holds the value op-stripe-key-p9c2v7ld had before it was rotated; run the sync (scripts/push-aws.sh)
  2	info     uncovered          defined but never cited or covered
      fix: aws-stripe-key-c3d4e5f6 is defined but nothing cites or covers it; cite it from a page or remove the def
internal/pay/stripe.go
  6	error    stale copy         app-stripe-key-m4w8k2qn still holds the value from before op-stripe-key-p9c2v7ld was rotated
      fix: app-stripe-key-m4w8k2qn still holds the value op-stripe-key-p9c2v7ld had before it was rotated; run the sync (no sync= declared)
2 error, 1 warning, 1 info, 2 none
```

A copy that holds neither value is `out of sync`, the cause unknown:

```console
$ STRIPE_KEY=sk_live_typo PATH="$PWD/../plugins:$PATH" ds check --resolve
…
internal/pay/stripe.go
  6	error    out of sync        app-stripe-key-m4w8k2qn differs from truth op-stripe-key-p9c2v7ld
      fix: app-stripe-key-m4w8k2qn differs from its truth op-stripe-key-p9c2v7ld; run the sync (no sync= declared) and ack the runbooks that cite the chain
2 error, 1 warning, 1 info, 2 none
```

And with the GitHub secret deleted:

<!-- doctest
perl -pi -e 's/^STRIPE_KEY present/OTHER present/' ../secrets.txt
-->

```console
$ STRIPE_KEY=sk_live_one PATH="$PWD/../plugins:$PATH" ds check --resolve
…
.github/workflows/deploy.yml
  7	error    resolve failed     ${{ secrets.STRIPE_KEY }} does not exist at github
      fix: ${{ secrets.STRIPE_KEY }} does not exist at github; fix the address in .github/workflows/deploy.yml:7
…
```

<!-- doctest
perl -pi -e 's/^OTHER present/STRIPE_KEY present/' ../secrets.txt
-->

| State | Severity | Meaning |
|---|---|---|
| `resolve failed` | error | the provider says the address does not exist |
| `out of sync` | error | a copy's hash differs from its truth's |
| `stale copy` | error | after a rotation, a copy still holds the truth's previous value; the stored hash is kept until no copy does, so this and `rotated` repeat until the sync runs |
| `rotated` | warning | the truth's hash differs from the one stored in `.ds/hashes.json` (needs `store_hash`) |
| `unverifiable` | warning | the plugin is missing, not allowed, failed (a signed-out CLI, an unset variable), or broke the protocol |

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

This shell script is a complete resolver, the one the outputs above came from. It answers from a file of `address value` lines instead of a real store; install it under the provider names you want (`ds-resolve-github`, `ds-resolve-aws`, …):

```sh file=../plugins/fake-resolver
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
  *'"want":"hash"'*) echo "{\"exists\":true,\"hash\":\"$(printf '%s' "$value" | { sha256sum 2>/dev/null || shasum -a 256; } | cut -d' ' -f1)\"}" ;;
  *)                 echo '{"exists":true}' ;;
esac
```

Test a plugin by hand before pointing `ds` at it:

```console
$ printf '%s\n' '{"op":"handshake","protocol":1}' '{"op":"resolve","addr":"arn:aws:secretsmanager:eu-west-1:123456789012:secret:stripe","want":"hash"}' | ../plugins/ds-resolve-aws
{"protocol":1,"name":"aws","kinds":["resolve"]}
{"exists":true,"hash":"a245b332a780393cd4fafdcb467e7bb40ae2880f160782704d5bc2fdcab6bd8f"}
```

A plugin that tries to return the value is stopped by the host:

```sh file=../leaky/ds-resolve-aws
#!/bin/sh
read -r a; read -r b
echo '{"protocol":1,"name":"aws","kinds":["resolve"]}'
echo '{"exists":true,"value":"sk_live_one"}'
```

<!-- doctest
chmod +x ../leaky/ds-resolve-aws
-->

```console
$ STRIPE_KEY=sk_live_one PATH="$PWD/../leaky:$PWD/../plugins:$PATH" ds check --resolve
.env.tpl
…
  2	warning  unverifiable       provider aws not reachable: procplugin: resolver reply carried more than existence and hash: unexpected field(s) value
…
```

In Go, `procplugin.Serve` does the handshake and the framing:

```go file=../goresolver/main.go
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

<!-- doctest
printf 'module example.com/resolver\n\ngo 1.26\n' > ../goresolver/go.mod
go -C ../goresolver mod edit -require=github.com/ubgo/docsync@v0.0.0 -replace=github.com/ubgo/docsync=$DOCSYNC_ROOT
export GOWORK=off
export GOFLAGS=-mod=mod
-->

Built and put first on PATH, it holds the rotated value, and the variable is set to it as well, so every copy agrees with its truth. The stale copies are gone and the new hash is stored; this run still says `rotated`, and the next one does not:

```console
$ go -C ../goresolver build -o ../gobin/ds-resolve-aws .
$ STRIPE_KEY=sk_live_two PATH="$PWD/../gobin:$PWD/../plugins:$PATH" ds check --resolve
.env.tpl
  1	warning  rotated            op-stripe-key-p9c2v7ld was rotated since its hash was stored
      fix: the truth op-stripe-key-p9c2v7ld changed since its stored hash; run the syncs of its copies and ack the runbooks
  2	info     uncovered          defined but never cited or covered
      fix: aws-stripe-key-c3d4e5f6 is defined but nothing cites or covers it; cite it from a page or remove the def
1 warning, 1 info, 2 none
$ STRIPE_KEY=sk_live_two PATH="$PWD/../gobin:$PWD/../plugins:$PATH" ds check --resolve
.env.tpl
  2	info     uncovered          defined but never cited or covered
      fix: aws-stripe-key-c3d4e5f6 is defined but nothing cites or covers it; cite it from a page or remove the def
1 info, 2 none
```

The same protocol serves other plugin kinds: `ds-pick-<scheme>` for a new `pick=` scheme and `ds-<verb>` for a new directive verb, each listed in config (`[plugins] picks = […]`, `verbs = […]`), and `ds-records-<source>` for a `ds:table` source named in `[records] source`. The request and reply shapes for each are in [the spec](../SPEC.md#374-process-plugins-for-any-language).

## Environments

The same id can be defined once per environment with `env=`; a citation without `env=` resolves to the default environment, and `ds check --env` or `ds render --env` picks another.

```toml
[env]
default = "prod"
known = ["prod", "staging"]
```

<!-- doctest
mkdir -p ../envs
cd ../envs
git init -q -b main .
ds init
perl -pi -e 's/^default = ""/default = "prod"\nknown = ["prod", "staging"]/' .ds/config.toml
-->

```sh file=deploy/prod.env
API_HOST=api.example.com   # ds:def id=api-host-d4k8w2mn env=prod
```

```sh file=deploy/staging.env
API_HOST=api.staging.example.com   # ds:def id=api-host-d4k8w2mn env=staging
```

```markdown file=docs/hosts.md
# Hosts

Clients call [api.example.com](ds:cfg?id=api-host-d4k8w2mn).

The staging stack answers on [api.staging.example.com](ds:cfg?id=api-host-d4k8w2mn&env=staging).
```

<!-- doctest
ds scan
-->

```console
$ ds facts
ID                 VALUE                    WHERE                 CITED BY
api-host-d4k8w2mn  api.example.com          deploy/prod.env:1     2
api-host-d4k8w2mn  api.staging.example.com  deploy/staging.env:1  2
$ ds render docs/hosts.md --env staging | sed -n 3p
Clients call api.staging.example.com.
```

Each environment's def is tracked separately, so a change to prod is never compared against staging. Two consequences to expect. Under `ds check --env staging`, a citation with no `env=` resolves to the staging def, so a sentence written about prod's value reports `unacked` with class `value`, which is the tool telling you the sentence is not true in staging:

```console
$ ds check --env staging
docs/hosts.md
  3	error    unacked            api-host-d4k8w2mn changed (value) since this sentence was first cited
      | -api.example.com
      | +api.staging.example.com
      still true: ds ack api-host-d4k8w2mn --doc docs/hosts.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/hosts.md:3, then ack
1 error, 1 ok
```

And a citation of an environment with no def is `broken`. Here `dev` is also missing from `env.known`, so the citation is reported `unknown` as well; an environment `env.known` lists but that has no def is `broken` alone:

```markdown file=docs/hosts.md append=true

Dev uses [localhost](ds:cfg?id=api-host-d4k8w2mn&env=dev).
```

```console
$ ds scan
…
$ ds check
docs/hosts.md
  7	warning  unknown            env=dev is not in env.known
      fix: env=dev at docs/hosts.md:7 is not in [env] known (prod, staging); fix the name or add it to the list
  7	error    broken             api-host-d4k8w2mn is not defined for env=dev
      fix: api-host-d4k8w2mn has no definition for env=dev; add one or cite a defined environment
1 error, 1 warning, 2 none
```

Secrets work the same way: one `truth=true` def per environment for the same id.

## Where this state lives

| File | Holds | Committed |
|---|---|---|
| `.ds/runs.json` | last outcome of each `ds:run` | no |
| `.ds/urls.json` | cached `ds:url` results | no |
| `.ds/hashes.json` | stored truth hashes for `rotated` | no |

All three are machine-local and in the `.ds/.gitignore` that `ds init` writes. In CI they start empty on every run unless your workflow caches them. See [Configuration](configuration.md) for every key, and [the spec](../SPEC.md#12-secrets-chains-and-environments) for the full rules.
