# Directives

This page is the reference for everything you write into source and docs for docsync to read: the directive syntax, ids, each verb with every argument it takes, and what `ds check` reports for it. It is for authors who already ran `ds init` (see [Getting started](getting-started.md)) and want to know exactly what to type; the per-language details of where a `ds:def` goes and what it binds are in [Languages](languages.md).

Every example below was run through `ds scan` and `ds check` in a throwaway repository, and the output shown is what the binary printed.

## Contents

- [Anatomy of a directive](#anatomy-of-a-directive)
- [Where a directive can sit](#where-a-directive-can-sit)
- [Ids](#ids)
- [ds:def — give something an identity](#dsdef--give-something-an-identity)
- [Facts: inline defs in prose](#facts-inline-defs-in-prose)
- [Remote defs: files that cannot hold a comment](#remote-defs-files-that-cannot-hold-a-comment)
- [pick — take one value or range out of a block](#pick--take-one-value-or-range-out-of-a-block)
- [ds:block — cite or show a block](#dsblock--cite-or-show-a-block)
- [ds:cfg — put a value in a sentence](#dscfg--put-a-value-in-a-sentence)
- [ds:claim — a sentence that must be re-reviewed](#dsclaim--a-sentence-that-must-be-re-reviewed)
- [ds:url — an outside link that is watched](#dsurl--an-outside-link-that-is-watched)
- [ds:run — run something and record the result](#dsrun--run-something-and-record-the-result)
- [ds:table — a table over records](#dstable--a-table-over-records)
- [Secrets and chains](#secrets-and-chains)
- [Environments](#environments)
- [Deprecation and sunset](#deprecation-and-sunset)
- [Translations](#translations)
- [Page frontmatter: covers and review_every](#page-frontmatter-covers-and-review_every)
- [Mistakes ds check catches](#mistakes-ds-check-catches)
- [Cheat sheet](#cheat-sheet)

## Anatomy of a directive

```
ds:<verb> key=value key=value …
```

- **Prefix.** `ds` followed by a colon, glued to the verb. The colon is what keeps the English word "ds" in a comment from ever starting a directive. The prefix is set per repository with `prefix = "…"` in `.ds/config.toml`; with `prefix = "acme"` the scanner reads `acme:def` and `acme:block` and ignores `ds:def`, and `ds def` writes `// acme:def id=…`.
- **Verb.** Lowercase, `[a-z][a-z0-9_]*`. The built-in verbs are `def`, `block`, `cfg`, `run`, `table`, `claim`, `url` and `chain`.
- **Arguments.** `key=value` only, no positional arguments. Keys are lowercase identifiers. A key may appear once; a repeated key is a `problem` finding.
- **Values.** Bare up to the next whitespace. Use `"…"` for a value with spaces and `'…'` for a value that contains double quotes. Lists are comma separated inside one value: `tags=auth,session`. There is no escape character.
- **Continuation.** In a line comment, a comment line directly below that starts with whitespace and `key=value` folds into the directive above it:

```go
// ds:def id=cont-func-b5c6d7e8 owner=@auth
//   desc="dual-write guard, remove after task-120"
func Cont() {}
```

The ledger records both lines' keys (`desc=… id=cont-func-b5c6d7e8 owner=@auth`). Continuation works for line comments (`//`, `#`, `--`). Do not split one HTML comment (`<!-- … -->`) across lines in markdown: a directive broken that way is not read at all and nothing is reported, so keep markdown directives on one line.

- **Unknown verbs and keys** are warnings, so an older `ds` can read files written for a newer one. `ds check --strict` turns them into errors:

```
docs/verbs.md
  23	warning  unknown            unknown verb "frob"
      fix: frob is not a registered verb; register a handler or fix the directive at docs/verbs.md:23
  25	warning  unknown            unknown key(s) bogus on ds:block
      fix: unknown key(s) bogus on ds:block at docs/verbs.md:25; check the spelling against the verb's key table
```

## Where a directive can sit

The same text works in three carriers. `ds check --explain` prints every directive the scan matched, with the tier that read it and the carrier, which is the quickest way to confirm a directive is being seen.

| Carrier | Looks like | Used for |
|---|---|---|
| comment | `// ds:def id=…`, `# ds:def id=…`, `-- ds:def id=…`, `/* ds:def id=… */`, `<!-- ds:block id=… -->`, `{/* ds:def id=… */}` in MDX | defs in code and config; block-position cites in docs |
| link | `[text](ds:block?id=…&lines=1-6)` | inline cites, facts, values in prose |
| bare line | `ds:def id=… span=+2` on a line of its own | plain text files only (`.txt`, `.text`, unknown extensions) |

The link form is a markdown link whose target is the directive with `?` after the verb and `&` between arguments. A value in the link form cannot contain a space, because markdown ends the link target at whitespace; write `%20` instead (`title=The%20Go%20Programming%20Language%20Specification`). A link whose target contains a raw space is not a link and is silently not a directive.

A comment anywhere in code can cite a block too, which puts the code in the reverse index next to the docs:

```go
// implements ds:block?id=sess-policy-h2n8wq4t
func Rotate() {}
```

`ds check --explain` lists it as `store/impl.go:3  go  ds:block  link  sess-policy-h2n8wq4t`.

Directives inside code fences, indented code blocks, inline code spans and string literals are not directives, which is why this page can show them.

## Ids

An id looks like `sess-save-k7m2p4xq`: a human label, a dash, and an eight-character suffix from `23456789abcdefghjkmnpqrstuvwxyz`.

Let `ds def` mint ids rather than typing them. It derives the label from the symbol (or `--label`), generates the suffix, inserts the directive, and prints the id:

```
$ ds def store/store.go#Store.SaveSession
store-savesession-m6twuucd
$ ds def store/store.go#MaxSessions --owner @auth
maxsessions-n4kpvzvf
$ ds def store/store.go#TTLDays --label sess-ttl --dry-run
sess-ttl-9hz97fhy
would insert at store/store.go:9:
	// ds:def id=sess-ttl-9hz97fhy
```

Run on a block that already has a def, `ds def` prints the existing id and changes nothing, so it is safe to call to look an id up. `ds def --fix` re-mints every copy after the first when an id has been duplicated (for example by copying a function).

Ids you type by hand are not validated for shape: `id=Foo_Bar` scans without complaint. Follow the lowercase `label-suffix` form anyway, because `ds rename` and the "did you mean" suggestions work on it.

### Renaming a label

`ds rename` relabels an id everywhere it appears, in source and docs. Either the full id or just the label works:

```
$ ds rename store-savesession sess-save --dry-run
store-savesession-m6twuucd -> sess-save-m6twuucd
7 line(s) would change (--dry-run)
$ ds rename store-savesession sess-save
store-savesession-m6twuucd -> sess-save-m6twuucd
7 line(s) changed; run ds scan
```

Then run `ds scan`. Acks and first-seen hashes carry over because the suffix is unchanged. A citation still has to use the current full id; one written with an old label is reported as broken, with the right id suggested:

```
3	error    broken             old-label-m6twuucd is not defined; did you mean sess-save-m6twuucd
```

## ds:def — give something an identity

A `ds:def` sits directly above the thing it names (or at the end of the line, for a config key), and binds the block below it: a function, a type, a constant, a config key, a markdown section, a paragraph. What counts as "the block" depends on the language; see [Languages](languages.md).

```go
// ds:def id=store-savesession-m6twuucd
func (s *Store) SaveSession(ctx context.Context, sess Session) error {
```

```yaml
server:
  port: 8081   # ds:def id=server-port-6btxuz6q
```

```sql
-- ds:def id=delete-u3e84e69
DELETE FROM sessions WHERE expires_at < now() - interval '30 days';
```

```markdown
<!-- ds:def id=sess-policy-h2n8wq4t -->
## Session policy

Sessions live thirty days and rotate on refresh.
```

```text
ds:def id=span-two-c2d3e4f5 span=+2
Week 37  alex
Week 38  someone
```

One def per block and one block per id. Deleting the directive line deletes the block, and every sentence citing it becomes `broken`:

```
  7	error    broken             legacy-save-q2w3e4r5 was deleted (last seen store/old.go:4)
      fix: the id legacy-save-q2w3e4r5 is not defined; fix the id in docs/more.md:7 or re-add the ds:def on the block it meant
```

There is deliberately no `value=` key. The value is the visible text at the def's location, so a reader and the tool see the same thing.

### ds:def arguments

| Key | Meaning |
|---|---|
| `id` | required; the identity |
| `owner` | a person or team (`@auth`); recorded in the ledger, used by `ds notify` and reports |
| `tags` | comma list, recorded in the ledger; `ds find --tag auth` lists the defs carrying a tag |
| `stability` | which kinds of change flag the prose that cites it: `frozen`, `stable` (default), `api`, `volatile`; see below |
| `span` | `+N`: widen the block. In plain text it binds exactly N lines below the directive; in code, markdown and INI-style config it binds the first line plus N more. YAML, TOML and HCL ignore it; see [Languages](languages.md) |
| `pick` | take one value or one range out of the bound text; see [pick](#pick--take-one-value-or-range-out-of-a-block) |
| `type` | the kind of value; `type=url` makes the fact render as a link |
| `file` | a remote def: the target is in another file; see [Remote defs](#remote-defs-files-that-cannot-hold-a-comment) |
| `local` | `true`: the target only exists on some machines; elsewhere it is `unverifiable` (a warning), never `broken` |
| `env` | the environment this def belongs to; the same id may be defined once per environment |
| `secret` | `true`: the value is never stored or shown, and `ds:block` refuses to render it |
| `source` | the provider of a secret whose address does not say: `env`, `github`, `1password`, `aws`, `gcp`, `vault`, `file` |
| `from` | the id this value is copied from; builds a chain |
| `truth` | `true` marks the single authoritative root of a chain |
| `sync` | the script or job that copies this hop from its `from` |
| `runnable` | `true` lets `ds:run id=…` execute this block |
| `deprecated` | a date (`YYYY-MM-DD`); citations get an info finding from then on |
| `sunset` | a date; after it every citation is an error |
| `desc` | one line, shown in hovers and the reverse index |
| `doc` | `path#anchor`: the def's home page, declared from the defining side |

### stability

Each change to a cited block is classified (`body`, `signature`, `type`, `renamed`, `value`, `comment`, …), and the def's `stability` decides which classes flag the sentences citing it:

| `stability` | Flags on |
|---|---|
| `frozen` | any change except whitespace |
| `stable` (default) | everything except whitespace and comment-only changes |
| `api` | `signature`, `type`, `renamed`, `value`, and changes it could not classify — not a body-only change |
| `volatile` | nothing |

With three Go functions cited from one sentence and a body-only edit to each, only the frozen one is flagged:

```go
// ds:def id=stab-api-t3u4v5w6 stability=api
func Api(a int) int {
	return a + 1
}
```

```
docs/stab.md
  3	error    unacked            stab-frozen-v3w4x5y6 changed (body) since this sentence was first cited
```

Changing the parameter type of the `api` function is a `signature` change and is flagged, but only after `ds scan` has recorded the new block; run `ds scan` before `ds check` when relying on `stability=api` (the CI snippet from `ds init` and [CI](ci.md) cover this):

```
$ ds scan
$ ds check
d.md
  3	error    unacked            stab-api-t3u4v5w6 changed (signature) since this sentence was first cited
```

The full classification rules are in [SPEC §20](../SPEC.md#20-change-classification-and-stability).

## Facts: inline defs in prose

A fact is a def whose text is one line. In markdown, write it as a link whose text is the value. The first place a fact is written is its home; other pages cite it with `ds:cfg`.

```markdown
- The API runs on port [8081](ds:def?id=api-port-h3v8n2wd&type=int).
- Sessions expire after [30 days](ds:def?id=sess-ttl-p2c4y7mk&type=duration).
- The app is hosted at [https://example.com](ds:def?id=app-host-d4k8w2mn&type=url).
```

`ds facts` lists every one-line def in the repository, from prose and from code and config alike, with its current value and how many places cite it:

```
$ ds facts
ID                     VALUE                 WHERE              CITED BY
api-port-h3v8n2wd      8081                  docs/facts.md:3    0
sess-ttl-p2c4y7mk      30 days               docs/facts.md:4    0
app-host-d4k8w2mn      https://example.com   docs/facts.md:5    0
json-port-k3m4n5p6     8081                  config/app.json:1  1
maxsessions-n4kpvzvf   8                     store/store.go:8   1
…
```

`ds render` prints each inline def as its plain text; `type=url` renders as a link.

## Remote defs: files that cannot hold a comment

JSON, CSV and `go.sum` have no comment syntax, so a directive cannot go inside them. Put the def in any file that can hold one (usually the doc that talks about the value) and point at the target with `file=` and `pick=`:

```markdown
<!-- ds:def id=json-port-k3m4n5p6 file=config/app.json pick=json:$.server.port type=int -->

Port is [8081](ds:cfg?id=json-port-k3m4n5p6).
```

`ds render` drops the def line and prints `Port is 8081.` A remote def hashes the picked value, so the citation is flagged when `server.port` changes. If the key disappears, the citation is `pick failed`. Unlike a def in the file itself, a remote def does not follow its target if the value moves to another file.

`ds def` refuses to write into a file with no comment syntax, and says so:

```
$ ds def doc/app.json:1
ds: docsync: no comment carrier for this file type: .json has no comment syntax docsync knows, so a directive cannot be written into it; bind it from a file that does with a remote def (`file=doc/app.json pick=…`), …
```

A bare `ds:def` line already present in such a file is reported as `directive is not inside a comment`, and `ds repair --apply` removes it; in a file that does have comments, `ds repair --apply` comments it instead.

## pick — take one value or range out of a block

`pick=` on a def narrows what is extracted. It returns exactly one line (a value, what `ds:cfg` shows) or one contiguous range (what `ds:block` shows). Most defs need no `pick`: a config key picks its value, a markdown fact picks its link text, a code symbol picks its block, a plain-text def picks to the next blank line.

| `pick=` | Takes |
|---|---|
| `json:$.server.port` | a JSON value by path |
| `yaml:server.port`, `toml:server.port`, `ini:server.port`, `env:API_KEY`, `hcl:resource.aws_instance.web.instance_type` | a key's value |
| `csv:r2c2`, `csv:col=name` | a CSV cell |
| `line:3` | one line of the target |
| `regex:'secrets\.(\w+)'` | the first capture group of the first match |
| `after:'…'`, `between:'(',')'` | the text after a marker, or between two |
| `url` | the first URL in the target |
| `heading`, `section:"Session policy"`, `paragraph:2`, `link:1` | parts of a markdown target |
| `file` | the whole file, hashed (for images, PDFs and generated files) |

Examples that ran:

```markdown
<!-- ds:def id=json-port-p2q3r4s5 file=doc/app.json pick=json:$.server.port -->
<!-- ds:def id=csv-port-t2u3v4w5 file=doc/svc.csv pick=csv:r2c2 -->
<!-- ds:def id=notes-first-x2y3z4a5 file=doc/NOTES pick=line:3 -->
```

```go
// ds:def id=app-stripe-key-m4w8k2qn secret=true source=env from=gh-stripe-key-r4t6x2mb pick=regex:'"(\w+)"'
key := os.Getenv("STRIPE_KEY")
```

The last one picks `STRIPE_KEY` out of the line.

A `pick` argument that contains a space must have the whole value quoted, because a value is only quoted when it starts with a quote: write `pick="after:'version: '"`, not `pick=after:'version: '` (the second is a `problem`: `key must match [a-z][a-z0-9_]*`).

Two remote defs whose picks land on the same line of the same file are reported as `two ids bound to the same block`, even when the picks differ (for example `pick=paragraph:2` and `pick=link:1` on a one-line paragraph). Keep one remote def per target line.

## ds:block — cite or show a block

`ds:block` has two shapes, chosen by the carrier.

**As a link**, it is a citation: the sentence around it depends on the block, and is flagged when the block changes.

```markdown
The guard is [`SaveSession`](ds:block?id=store-savesession-m6twuucd). It returns early when the id is empty.
```

`ds render` turns it into a permalink: ``The guard is [`SaveSession`](store/store.go#L22-L27).``

**In block position** (an HTML comment on its own line), it shows the code itself when the page is rendered:

```markdown
<!-- ds:block id=store-savesession-m6twuucd lines=1-3 title="the guard" -->
```

renders as

````markdown
**the guard** · [`store/store.go:22-27`](store/store.go#L22-L27)

```go
func (s *Store) SaveSession(ctx context.Context, sess Session) error {
	if sess.ID == "" {
		return nil
```
````

The repository never holds the copy; it is produced at build time from live source.

| Key | Meaning |
|---|---|
| `id` | required; the block |
| `lines` | a fragment `a-b` or `a`, counted from 1 within the block |
| `title` | the caption shown above a rendered block |
| `at` | a commit: a deliberate snapshot, rendered with an "as of" badge and never flagged |
| `translates` | `true` on a translated paragraph; see [Translations](#translations) |
| `assert` | `true` when the cited block is a test, so the sentence also depends on that test passing in the last published CI run |
| `label` | a short name for the claim, to make ack messages readable |
| `branch` | the block as published from a named branch, in a workspace |
| `env` | which environment's def to cite |

What `ds check` reports:

- `ok` while the block is unchanged, `moved` (no severity) when it only moved.
- `unacked` when it changed since the sentence was acked or first cited. Read the sentence; if it is still true, ack it with the command shown, otherwise edit it and then ack:

```
docs/sessions.md
  3	error    unacked            store-savesession-m6twuucd changed (body) since this sentence was first cited
      still true: ds ack store-savesession-m6twuucd --doc docs/sessions.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/sessions.md:3, then ack
```

- `broken` when the id is not defined.
- `range` when `lines=` falls outside the block:

```
  9	warning  range              lines=4-30 outside the block's 6 lines
      fix: adjust lines= at docs/more.md:9 to fit the block's 6 lines
```

- `too-large` when a block-position cite would render more than `[include] max_lines` lines (default 40): `error    too-large          rendering 52 lines exceeds the cap of 40`. Add `lines=` or cite it with a link instead.

`at=` is a snapshot and `ds check` reports it as `ok` with "snapshot pinned". The snapshot's code is produced by `ds render --at <commit>`; a plain `ds render` prints the as-of badge and link and notes `no snapshot for … rendering the link only`.

## ds:cfg — put a value in a sentence

`ds:cfg` inlines the current value of a one-line def. Link form only. The link text is the last known value, so the raw markdown still reads well; the build replaces it.

```markdown
A user may hold at most [5](ds:cfg?id=maxsessions-n4kpvzvf) sessions.
```

`ds render` prints `A user may hold at most 5 sessions.` A Go constant, a YAML key, a TOML key, a fact and a remote def all work as the target.

| Key | Meaning |
|---|---|
| `id` | required; a def whose extracted value is one line |
| `format` | `raw` (default), `code`, `quote`, `host`, `link`, `compact` |
| `env` | which environment's def; default from `[env] default` or `ds render --env` |

Formats, all from one def holding `https://example.com` and one holding `8081`:

| `format=` | Rendered |
|---|---|
| `raw` | `https://example.com` |
| `code` | `` `https://example.com` `` |
| `quote` | `"https://example.com"` |
| `host` | `example.com` |
| `link` | `[https://example.com](https://example.com)` |
| `compact` | `8.1K` (from 8081) |

Citing a block that yields more than one line is refused:

```
  11	warning  range              ds:cfg on a 6-line block
      fix: the def yields 6 lines; ds:cfg needs one line, use ds:block or a narrower pick=
```

A changed value is `unacked`, like a changed block.

## ds:claim — a sentence that must be re-reviewed

A claim needs no def. It puts a review date on a sentence, for statements you want to be reminded about even when no code changed.

```markdown
We chose Postgres over Redis because ops already runs Postgres. <!-- ds:claim owner=@platform reviewed=2026-09-06 expires=90d -->
```

| Key | Meaning |
|---|---|
| `owner` | who reviews it |
| `reviewed` | the last review date, `YYYY-MM-DD` |
| `expires` | how long a review lasts, such as `90d` |
| `about` | comma list of ids; a change to any of them also expires the claim |

Before the date the claim is `ok` ("claim valid until 2026-12-05"). After it:

```
  13	error    expired            claim reviewed 2026-01-01 expired after 30d
      fix: review the claim at docs/verbs.md:13 and run ds ack --doc docs/verbs.md --line 13 to renew it
```

With `about=`, a change to a listed block expires the claim at once:

```
  9	error    expired            claim is about maxsessions-n4kpvzvf, which changed (body)
      fix: review the claim at docs/cover.md:9 and run ds ack --doc docs/cover.md --line 9 to renew it
```

Renew it with `ds ack --doc <doc> --line <n> --note '…'`, which prints `renewed the claim at docs/cover.md:9`. For an `about=` claim, run `ds scan` after the ack so the finding clears.

## ds:url — an outside link that is watched

```markdown
See the [Go spec](ds:url?href=https://go.dev/ref/spec&title=Specification).
```

| Key | Meaning |
|---|---|
| `href` | required; the external URL |
| `title` | the expected page title, or a substring of it; a change means the page moved or was rewritten |
| `expect` | the expected HTTP status, default 200 |

`ds render` turns it into an ordinary link. Without network access the check is skipped with a warning:

```
3 unverifiable external link not checked — fix: external link checks need --resolve with network access
```

With `ds check --resolve`:

```
docs/url.md
  3	warning  retitled           title is now "The Go Programming Language Specification - The Go Programming Language"
      fix: the page at https://go.dev/ref/spec no longer has title "Rust"; confirm it is still the right page
  5	warning  url moved          http://go.dev/ref/spec redirects to https://go.dev/ref/spec
      fix: the link http://go.dev/ref/spec now redirects to https://go.dev/ref/spec; update it at docs/url.md:5
  7	error    dead               https://go.dev/this-page-does-not-exist-xyz returned 404
```

Remember the link-form rule: a `title` with spaces must be written with `%20`.

## ds:run — run something and record the result

```markdown
<!-- ds:run id=hello-task-q3r4s5t6 expect=ok -->
<!-- ds:run cmd="echo hi" expect="hi" -->
```

| Key | Meaning |
|---|---|
| exactly one of `id`, `cmd`, `file` | what to run; `id` needs `runnable=true` on the def |
| `expect` | `ok` (exit zero), `rows`, an HTTP status, or a quoted substring of the output |
| `env`, `timeout`, `show` | environment, time limit, and what the renderer shows (`output`, `command`, `both`, `none`) |

Nothing runs unless `[run] enabled = true` and the doc matches `[run] allow`, and then only with `ds check --run`. Without `--run` the directive is `skipped` (info). With it:

```
docs/run.md:3  run ok: echo hello from task
docs/run.md:5  run skipped: not-runnable-r3s4t5u6 is not runnable=true
docs/run.md:7  run ok: echo hi
```

The def for the first one is a shell line marked runnable:

```bash
# ds:def id=hello-task-q3r4s5t6 runnable=true
echo hello from task
```

[Secrets and runs](secrets-and-runs.md) covers the run configuration and its safety rules.

## ds:table — a table over records

```markdown
<!-- ds:table kind=task where="state!=done" cols=title,owner -->
```

Keys: `kind`, `where`, `cols`, `sort`, `limit`, `empty`. It needs a record source under `[records]` in the config (see [Configuration](configuration.md)); without one it is reported and renders nothing:

```
  19	warning  unverifiable       no record source configured
      fix: register a record source in [records] to render ds:table
```

## Secrets and chains

Secrets reach the repository as addresses (`${{ secrets.STRIPE_KEY }}`, `op://…`, an env var name), and docsync works on those addresses only. Mark the def `secret=true`; link copies of one value with `from=` and mark the single root `truth=true`. `ds:chain` renders the path:

```bash
# .env.tpl
STRIPE_KEY=op://Platform/stripe-prod/credential   # ds:def id=op-stripe-key-p9c2v7ld secret=true truth=true
```

```yaml
# .github/workflows/deploy.yml
env:
  STRIPE_KEY: ${{ secrets.STRIPE_KEY }}   # ds:def id=gh-stripe-key-r4t6x2mb secret=true from=op-stripe-key-p9c2v7ld sync=scripts/sync-secrets.sh
```

```markdown
Stripe: <!-- ds:chain id=app-stripe-key-m4w8k2qn -->
```

```
$ ds why app-stripe-key-m4w8k2qn --chain
app-stripe-key-m4w8k2qn  STRIPE_KEY  store/pay.go:7
  from gh-stripe-key-r4t6x2mb  ${{ secrets.STRIPE_KEY }}  .github/workflows/deploy.yml:2
    from op-stripe-key-p9c2v7ld  op://Platform/stripe-prod/credential  .env.tpl:1  TRUTH
```

`ds:chain` takes `id` and `env`. A `ds:block` of a secret def renders nothing (`ds:block refuses to render it`). Provider checks, rotation, and the chain findings (`unsourced`, `chain broken`, `out of sync`) are in [Secrets and runs](secrets-and-runs.md) and [SPEC §12](../SPEC.md#12-secrets-chains-and-environments).

## Environments

The same id can be defined once per environment with `env=`, and cites choose one with `env=`:

```bash
# config/prod.env
API_HOST=api.example.com   # ds:def id=api-host-r2s3t4u5 env=prod
# config/staging.env
API_HOST=staging.example.com   # ds:def id=api-host-r2s3t4u5 env=staging
```

```markdown
Prod is [api](ds:cfg?id=api-host-r2s3t4u5&env=prod), staging is [s](ds:cfg?id=api-host-r2s3t4u5&env=staging).
```

renders `Prod is api.example.com, staging is staging.example.com.` A cite without `env=` uses `[env] default`, or `--env` on `ds check` and `ds render`; set the default rather than relying on whichever def is found first. Citing an environment with no def is broken:

```
3 broken api-host-r2s3t4u5 is not defined for env=dev
```

## Deprecation and sunset

```go
// ds:def id=legacy-save-q2w3e4r5 deprecated=2026-09-01 desc="old writer"
func LegacySave() {}

// ds:def id=older-save-t6y7u8i9 sunset=2026-09-01
func OlderSave() {}
```

Every citation of the first gets an info finding from its date; every citation of the second fails after its date:

```
  7	info     deprecated         legacy-save-q2w3e4r5 deprecated since 2026-09-01
      fix: legacy-save-q2w3e4r5 is deprecated since 2026-09-01; plan to move the reference at docs/more.md:7
  7	error    sunset             older-save-t6y7u8i9 reached sunset 2026-09-01
      fix: older-save-t6y7u8i9 passed its sunset date 2026-09-01; remove the reference at docs/more.md:7
```

## Translations

Define the source paragraph, then mark the translated paragraph with `translates=true`:

```markdown
<!-- ds:def id=retention-para-w4x5y6z7 -->
Logs are kept for ninety days.
```

```markdown
<!-- ds:block id=retention-para-w4x5y6z7 translates=true -->
Los registros se guardan noventa días.
```

When the source changes:

```
docs/es.md
  3	error    translation stale  source retention-para-w4x5y6z7 changed (body)
      fix: the source paragraph retention-para-w4x5y6z7 changed; update the translation at docs/es.md:3 and ack
```

## Page frontmatter: covers and review_every

A markdown page can declare itself the home of ids in its frontmatter:

```markdown
---
title: Store
ds:
  covers: [session-user-yk7dsbtd, gone-thing-abcdefgh]
  review_every: 180d
---
```

A covered id no longer counts as `uncovered`. A covered id that is not defined is an orphan:

```
docs/cover.md
  1	warning  orphan             covers gone-thing-abcdefgh, which is not defined
      fix: the page docs/cover.md covers gone-thing-abcdefgh, which is not defined; remove it from covers or restore the def
```

`review_every` asks for the whole page to be re-read on a schedule even when nothing it cites changed; `ds ack --doc <page>` records the review. A def that nothing cites or covers is reported as `uncovered` (info), which never fails a run.

## Mistakes ds check catches

| You wrote | `ds check` says |
|---|---|
| a def with no `id=` | `error problem extract: ds:def without id=` |
| the same key twice | `error problem column 39: directive: duplicate key` |
| an id nobody defines | `error broken nothing-here-abcdefgh is not defined` |
| a misspelled verb or key | `warning unknown` (an error with `--strict`) |
| a bare `ds:def` line in Go or JSON | `error problem scan: directive is not inside a comment: …; ds repair --apply comments it` (or removes it, in JSON) |
| two defs over the same lines | `error problem scan: two ids bound to the same block: …` |
| a def with nothing below it | `error problem extract: ds:def has nothing after it to bind to` |
| a `pick=` that matches nothing | `error pick failed scan: remote def pick failed: t/a.yaml: pick: nothing matched: yaml server.nope` |

The full list of finding states and severities is in [SPEC §17](../SPEC.md#17-findings).

## Cheat sheet

| Want | Write |
|---|---|
| give code an identity | `ds def path#Symbol` or `ds def path:line`, which writes `// ds:def id=name-xxxxxxxx` |
| give a config line an identity | `key: v   # ds:def id=name-xxxxxxxx` |
| define a fact in prose | `[8081](ds:def?id=name-xxxxxxxx&type=int)` |
| define a value in a JSON or CSV file | `<!-- ds:def id=… file=path pick=json:$.a.b -->` |
| cite a block | `[text](ds:block?id=…)` |
| show a block | `<!-- ds:block id=… lines=1-6 title="…" -->` |
| freeze a block | `<!-- ds:block id=… at=<commit> -->` |
| show a value | `[8081](ds:cfg?id=…&format=code)` |
| bind a sentence to a test | `[test](ds:block?id=…&assert=true)` |
| a translated paragraph | `<!-- ds:block id=… translates=true -->` |
| run something | `<!-- ds:run id=… expect=ok -->` |
| a table over records | `<!-- ds:table kind=task where="…" -->` |
| a sentence that must age out | `<!-- ds:claim owner=@t reviewed=2026-09-06 expires=90d -->` |
| an external link | `[text](ds:url?href=…&title=…)` |
| a secret's path | `<!-- ds:chain id=… -->` |
| cite from a code comment | `// implements ds:block?id=…` |
