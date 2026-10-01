# docsync at a glance

This page explains the whole docsync model on one screen: what problem it solves, the handful of words it uses, the loop you run, and what one round of that loop looks like with real output. Read it if you are deciding whether docsync fits your project; when you are ready to set it up, go to [Getting started](getting-started.md).

## The problem

A doc says "a failed delivery is retried five times". Six months later someone changes the retry limit to three in a one-line pull request. The code review sees the diff; nobody opens the page three directories away, and the sentence is now false. Nothing anywhere records that this sentence depends on that function, so nothing can tell anyone.

docsync adds exactly that record. You mark the code once, cite it from the sentence, and from then on `ds check` names every sentence whose code changed, moved, or disappeared since a person last confirmed it, and prints the command that clears each one. It does not decide whether the sentence is still true; a person (or an agent acting for one) does, and that decision is recorded.

## The vocabulary

The spec names five words ([SPEC section 4](../SPEC.md#4-five-words)); a few more nouns fall out of them.

| Word | What it means | Written as |
|---|---|---|
| define | give one block of code, a config value, a doc section or a few lines a permanent id, in the file where it lives | a `ds:def id=…` comment |
| block | the thing a def marks: a function, a type, a key's value, a markdown section, a span of lines | |
| cite | point at a defined block from a doc, as a link or a comment; this is a *reference* | `[text](ds:block?id=…)`, `[8081](ds:cfg?id=…)` |
| check | compare every block with what was last recorded and report, per citing sentence, what may now be wrong | `ds check` |
| ack | a reviewer's statement that a sentence is still true for the block as it is now | `ds ack` |
| ledger | the committed record of every block (where it is, its hash) and every citation (which sentence, which hash it was approved against) | `.ds/ledger.tsv`, `.ds/refs.tsv`, `.ds/acks.tsv` |
| chain | the declared path a value travels from its one source of truth through its copies, for secrets and config | `from=`, `truth=` on a def; see [Secrets and runs](secrets-and-runs.md) |

The id looks like `maxattempts-ck543sgy`: a readable label, then an eight-character suffix. The suffix is the identity; the label is only a hint, so `ds rename maxattempts retry-limit` rewrites it to `retry-limit-ck543sgy` in the def and every citation without breaking anything.

## The lifecycle

```
  def             cite             scan               change            check             fix + ack
 ─────────      ─────────       ──────────        ──────────────     ──────────────     ──────────────
 mark the   →   link to it  →   record each   →   code is edited  →  sentence is    →   edit the sentence
 block in       from the        block's hash      (moved, renamed,   flagged, with      if it is now wrong,
 its source     sentence        and each          body changed,      the remedy         then ds ack; the
 file           that needs it   citation in .ds/  deleted)           command            ack stores the hash
     │                                                                                        │
     └────────────────────────────── commit .ds/ with the change ◄────────────────────────────┘
```

Every arrow is one command. A block that only moves (another file, other lines, reformatted) is absorbed and reported as `moved`, with nothing to do. A block whose content changes flags every sentence that cites it until each is acked. A block that is deleted makes its citations `broken`.

## One round, end to end

<!-- doctest
git init -q -b main .
ds init
-->

A Go function that a doc will describe:

```go file=webhook/retry.go
package webhook

// MaxAttempts is how many times a failed delivery is tried before it is dropped.
func MaxAttempts() int {
	return 5
}
```

Mark the function. `ds def` mints an id, writes the directive above the declaration, and prints the id:

```
$ ds def webhook/retry.go#MaxAttempts --owner @platform
maxattempts-ck543sgy
```

```go
// MaxAttempts is how many times a failed delivery is tried before it is dropped.
// ds:def id=maxattempts-ck543sgy owner=@platform
func MaxAttempts() int {
	return 5
}
```

Cite it from the doc with an ordinary markdown link whose target is `ds:block?id=…`:

```markdown file=docs/webhooks.md
# Webhooks

A failed delivery is retried until [`MaxAttempts`](ds:block?id=maxattempts-ck543sgy) is reached: five tries, then it is dropped.
```

Record the state and check it (here the code and doc were committed first, then the scan, then `.ds/`):

<!-- doctest
git add -A
git commit -qm webhooks
-->

```
$ ds scan
2 files, 1 defs, 1 refs, 0 problems, 0 skipped
$ ds check
1 none
```

<!-- doctest
git add -A
git commit -qm scan
printf 'package webhook\n\n// MaxAttempts is how many times a failed delivery is tried before it is dropped.\n// ds:def id=maxattempts-ck543sgy owner=@platform\nfunc MaxAttempts() int {\n\treturn 3\n}\n' > webhook/retry.go
-->

The summary counts findings by severity; `none` means up to date. `.ds/` is committed like any other file.

Now someone changes the limit to 3 and does not open the doc:

```
$ ds check
docs/webhooks.md
  3	error    unacked            maxattempts-ck543sgy changed (body) since this sentence was first cited
      still true: ds ack maxattempts-ck543sgy --doc docs/webhooks.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/webhooks.md:3, then ack
1 error
```

It exits 1, so CI fails. `ds review` shows the same worklist with the sentence and the change in the block:

```
$ ds review
- [ ] docs/webhooks.md:3  unacked  maxattempts-ck543sgy changed (body) since this sentence was first cited
      sentence: A failed delivery is retried until [`MaxAttempts`](ds:block?id=maxattempts-ck543sgy) is reached: five tries, then it is dropped.
      | -	return 5
      | +	return 3
      still true: ds ack maxattempts-ck543sgy --doc docs/webhooks.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/webhooks.md:3, then ack
```

The sentence says "five tries", so it is wrong. Fix it to "three tries", then record that it is true again:

```markdown file=docs/webhooks.md
# Webhooks

A failed delivery is retried until [`MaxAttempts`](ds:block?id=maxattempts-ck543sgy) is reached: three tries, then it is dropped.
```

```
$ ds ack maxattempts-ck543sgy --doc docs/webhooks.md --line 3 --note 'retries cut to 3'
acked maxattempts-ck543sgy at docs/webhooks.md:3 (human)
$ ds check
1 none
```

<!-- doctest
ds scan
git add -A
git commit -qm ack
printf '# Webhooks\n\nA failed delivery is retried until [`MaxAttempts`](ds:block?id=maxattempts-ck543sgy) is reached: three tries, then it is discarded.\n' > docs/webhooks.md
-->

The ack is held to both the block's hash and the sentence's wording. Rewording the sentence later ("dropped" becomes "discarded"), with the code unchanged, flags it again, because the person who acked approved different words:

```
$ ds check
docs/webhooks.md
  3	error    unacked            sentence rewritten since the ack
…
```

<!-- doctest
printf '# Webhooks\n\nA failed delivery is retried until [`MaxAttempts`](ds:block?id=maxattempts-ck543sgy) is reached: three tries, then it is dropped.\n' > docs/webhooks.md
printf 'package webhook\n' > webhook/retry.go
-->

Deleting the function makes the citation broken:

```
$ ds check
docs/webhooks.md
  3	error    broken             maxattempts-ck543sgy was deleted (last seen webhook/retry.go:5)
      fix: the id maxattempts-ck543sgy is not defined; fix the id in docs/webhooks.md:3 or re-add the ds:def on the block it meant
…
```

<!-- doctest
printf 'package webhook\n\n// MaxAttempts is how many times a failed delivery is tried before it is dropped.\n// ds:def id=maxattempts-ck543sgy owner=@platform\nfunc MaxAttempts() int {\n\treturn 3\n}\n' > webhook/retry.go
git mv webhook/retry.go webhook/policy.go
printf 'package webhook\n\nfunc Backoff() int {\n\treturn 1\n}\n\n// MaxAttempts is how many times a failed delivery is tried before it is dropped.\n// ds:def id=maxattempts-ck543sgy owner=@platform\nfunc MaxAttempts() int {\n\treturn 3\n}\n' > webhook/policy.go
-->

Moving it to another file, with the directive travelling with it, needs nothing:

```
$ ds check
docs/webhooks.md
  3	none     moved              moved from webhook/retry.go:5-7
1 none
```

## What a def looks like in each language

<!-- doctest
mkdir ../langs
cd ../langs
git init -q -b main .
ds init
-->

A def is a comment in whatever comment syntax the file already has, placed above (or, for one-line config values, beside) the thing it marks. `ds def <file>#<symbol>` or `ds def <file>:<line>` writes it for you, and refuses rather than write a directive the file's syntax would not accept. Every snippet below is what `ds def` wrote. [Languages](languages.md) covers each one in depth.

| File | How you mark it | What it binds |
|---|---|---|
| Go | `ds def src/store.go#Store.Save` | the function, method, type or const below; symbol `Store.Save` |
| TypeScript, JavaScript | `ds def src/api.ts#retryDelay`; a method by its bare name, `#get` | the function, class, interface, const or method below; a method is recorded as `Cache.get` |
| Python | `ds def src/billing.py#grace_days`; a method by its bare name | the `def` or `class` below; a method is recorded as `Invoice.total` |
| SQL | `ds def db/schema.sql#sessions` | the statement below |
| YAML, TOML | `ds def config/app.yaml#server.port` | that key's value, by key path |
| JSON | a remote def in another file: `file=config/app.json pick=json:$.server.port` | the picked value |
| HCL (Terraform) | `ds def deploy/s3.tf:1` (by line) | the block or attribute below; recorded as `resource.aws_s3_bucket.logs` or `resource.aws_s3_bucket.logs.bucket` |
| Shell | `ds def scripts/release.sh:4` (by line) | the function below |
| Markdown | `ds def docs/policy.md#Retention` (a heading) or `:line` (a paragraph) | the heading's section, or the paragraph |
| Plain text | `ds def docs/runbook.txt:3` | the lines below up to the next blank line, or `span=+N` lines |

Go:

```go file=src/store.go
package store

type Store struct{}

// ds:def id=store-save-95mdgm26
func (s *Store) Save() error {
	return nil
}

// ds:def id=maxconns-hcffgtr3
const maxConns = 10
```

TypeScript and JavaScript (a method inside a class gets an indented directive and is recorded as `Cache.get`):

```ts file=src/api.ts
// ds:def id=retrydelay-4z36zrpq
export function retryDelay(attempt: number): number {
  return Math.min(1000 * 2 ** attempt, 30000);
}

export class Cache {
  // ds:def id=get-6xe25m8w
  get(key: string): string {
    return key;
  }
}
```

```js file=src/util.js
// ds:def id=max-retries-ytrfkpce
export const MAX_RETRIES = 5;
```

Python:

```python file=src/billing.py
# ds:def id=grace-days-4bhschkr
def grace_days():
    return 14
```

SQL:

```sql file=db/schema.sql
-- ds:def id=sessions-ypd8qhnn
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,
  expires_at TIMESTAMP NOT NULL
);
```

YAML and TOML put the directive on the key's own line, so the def is that key's value and nothing else:

```yaml file=config/app.yaml
server:
  port: 8081   # ds:def id=server-port-zcrvprs3
  timeout: 30s
```

```toml file=config/app.toml
[server]
port = 8081   # ds:def id=server-port-f4t23dny
workers = 4
```

JSON has no comments:

```json file=config/app.json
{
  "server": {
    "port": 8081
  }
}
```

So `ds def config/app.json:3` refuses and says what to do instead: write a remote def in a file that can hold a comment, pointing at the JSON with `file=` and a `pick=` path.

```
$ ds def config/app.json:3
ds: docsync: no comment carrier for this file type: .json has no comment syntax docsync knows, so a directive cannot be written into it; bind it from a file that does with a remote def (`file=config/app.json pick=…`). The comment syntaxes are built in, not configured: if this type does take comments, it needs an entry in docsync's carrier table
```

This one lives in a markdown page:

```markdown file=docs/facts.md
<!-- ds:def id=api-port-h3v8n2wd file=config/app.json pick=json:$.server.port type=int -->

The API listens on [8081](ds:cfg?id=api-port-h3v8n2wd).
```

HCL, by line:

```hcl file=deploy/s3.tf
# ds:def id=resource-aws-s3-bucket-logs-98vsbruu
resource "aws_s3_bucket" "logs" {
  bucket = "logs"
}
```

Shell, by line:

```sh file=scripts/release.sh
#!/bin/sh
set -e

# ds:def id=release-ahrgsntw
release() {
  echo "release"
}
```

Markdown, by heading. The def covers the section up to the next heading of the same or higher level:

```markdown file=docs/policy.md
# Policy

<!-- ds:def id=retention-nppm6hmc -->
## Retention

Sessions are kept for thirty days.
```

Plain text has no comment syntax, so the directive is a line of its own:

```text file=docs/runbook.txt
Suspending an account

ds:def id=runbook-bw796cm3
Check the invoice is past its grace period.
Email the billing contact.
Set the account to suspended.
```

Asked again for a block that already carries a directive, `ds def` prints the existing id rather than writing a second one, which is a quick way to see what a def binds:

```
$ ds def src/store.go#Store.Save
store-save-95mdgm26
$ ds def src/api.ts#get
get-6xe25m8w
$ ds def src/billing.py#grace_days
grace-days-4bhschkr
$ ds def db/schema.sql#sessions
sessions-ypd8qhnn
$ ds def config/app.yaml#server.port
server-port-zcrvprs3
$ ds def config/app.toml#server.port
server-port-f4t23dny
$ ds def deploy/s3.tf:1
resource-aws-s3-bucket-logs-98vsbruu
$ ds def scripts/release.sh:4
release-ahrgsntw
$ ds def docs/policy.md#Retention
retention-nppm6hmc
```

`ds:cfg` renders the current value in place, and a changed value flags the sentence:

```
$ ds scan
…
$ ds render docs/facts.md

The API listens on 8081.
```

<!-- doctest
printf '{\n  "server": {\n    "port": 9090\n  }\n}\n' > config/app.json
-->

Once the JSON says 9090:

```
$ ds render docs/facts.md

The API listens on 9090.
$ ds check
…
docs/facts.md
  3	error    unacked            api-port-h3v8n2wd changed (value) since this sentence was first cited
…
```

## What is in `.ds/` and what you commit

`ds init` creates `.ds/`. Two kinds of file live there, and the difference matters.

- **Commit**: `config.toml`, `ledger.tsv` (every block: file, symbol, lines, hash), `refs.tsv` (every citation: doc, line, the hash it was approved against), `acks.tsv` (the append-only log of who approved which sentence, when, and why), `blocks/` (block bodies by hash, so a finding can show what changed since an ack), and `foreign.tsv` once you cite blocks from another repository. A check is reproducible on another machine only because these are in the tree.
- **Never commit**: `cache/`, `index/`, `journal.tsv`, `urls.json`, `runs.json`, `notified.json`, `hashes.json`, `metrics.json`, `lock`. They are per-machine, and the cache and journal hold raw source text. `ds init` writes a `.ds/.gitignore` that excludes exactly these.

`ds init` also writes `.ds/.gitattributes` with `acks.tsv merge=union`, so two branches that each record an ack merge without a conflict. The full rules are in [SPEC section 6](../SPEC.md#6-what-lives-where).

## Where to go next

- [Getting started](getting-started.md): install, set up a repository, and run the loop above on your own code.
- [Directives](directives.md): every verb (`def`, `block`, `cfg`, `run`, `table`, `claim`, `url`, `chain`) and its keys.
- [Languages](languages.md): how each file type binds, and what to do with formats that cannot hold a comment.
- [CLI reference](cli.md) and [Configuration](configuration.md).
- [CI](ci.md): making `ds check` a required check.
- [Agents](agents.md): the MCP server and rules for coding agents.
- [Integrations](integrations.md): editors, pre-commit, Hugo and Docusaurus.
- [Library](library.md): embedding docsync in a Go program.
- [Cross-repo](cross-repo.md), [Secrets and runs](secrets-and-runs.md), [Troubleshooting](troubleshooting.md).
