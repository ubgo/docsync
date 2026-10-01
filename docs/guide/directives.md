# Directives

This page is the reference for everything you write into source and docs for docsync to read: the directive syntax, ids, each verb with every argument it takes, and what `ds check` reports for it. It is for authors who already ran `ds init` (see [Getting started](getting-started.md)) and want to know exactly what to type; the per-language details of where a `ds:def` goes and what it binds are in [Languages](languages.md).

Every example below is run against the `ds` binary by the guide's test harness, each section in a fresh repository, and the output shown is what `ds` prints.

<!-- doctest
git init -q -b main .
ds init
-->

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

```go file=store/cont.go
package store

// ds:def id=cont-func-b5c6d7e8 owner=@auth
//   desc="dual-write guard, remove after task-120"
func Cont() {}
```

The def carries the keys from both lines:

```
$ ds scan
1 files, 1 defs, 0 refs, 0 problems, 0 skipped
$ ds find cont-func --json
…
    "args": {
      "desc": "dual-write guard, remove after task-120",
      "id": "cont-func-b5c6d7e8",
      "owner": "@auth"
    }
  }
]
```

Continuation works for line comments (`//`, `#`, `--`). Do not split one HTML comment (`<!-- … -->`) across lines in markdown: a directive broken that way is not read at all and nothing is reported, so keep markdown directives on one line.

- **Unknown verbs and keys** are warnings, so an older `ds` can read files written for a newer one. `ds check --strict` turns them into errors:

```markdown file=docs/verbs.md
# Verbs

<!-- ds:frob id=x -->

[guard](ds:block?id=cont-func-b5c6d7e8&bogus=1)
```

```
$ ds check
docs/verbs.md
  3	warning  unknown            unknown verb "frob"
      fix: frob is not a registered verb; register a handler or fix the directive at docs/verbs.md:3
  5	warning  unknown            unknown key(s) bogus on ds:block
      fix: unknown key(s) bogus on ds:block at docs/verbs.md:5; check the spelling against the verb's key table
2 warning, 1 ok
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

```go file=store/impl.go
package store

// implements ds:block?id=cont-func-b5c6d7e8
func Rotate() {}
```

```
$ ds check --explain
WHERE            TIER      WHAT      CARRIER  ID
store/cont.go:3  go        def func  comment  cont-func-b5c6d7e8
docs/verbs.md:3  markdown  ds:frob   block    x
docs/verbs.md:5  markdown  ds:block  link     cont-func-b5c6d7e8
store/impl.go:3  go        ds:block  link     cont-func-b5c6d7e8
docs/verbs.md
  3	warning  unknown            unknown verb "frob"
      fix: frob is not a registered verb; register a handler or fix the directive at docs/verbs.md:3
  5	warning  unknown            unknown key(s) bogus on ds:block
      fix: unknown key(s) bogus on ds:block at docs/verbs.md:5; check the spelling against the verb's key table
2 warning, 2 ok
```

Directives inside code fences, indented code blocks, inline code spans and string literals are not directives, which is why this page can show them.

## Ids

<!-- doctest
mkdir ../ids
cd ../ids
git init -q -b main .
ds init
-->

An id looks like `sess-save-k7m2p4xq`: a human label, a dash, and an eight-character suffix from `23456789abcdefghjkmnpqrstuvwxyz`.

Let `ds def` mint ids rather than typing them. It derives the label from the symbol (or `--label`), generates the suffix, inserts the directive, and prints the id. Given this file, where `SaveSession` already has a def:

```go file=store/store.go
package store

import "context"

// Limits for sessions.
const (
	MaxSessions = 5
	TTLDays     = 30
)

type Session struct {
	ID   string
	User string
}

type Store struct{}

// SaveSession writes the session.
// ds:def id=store-savesession-m6twuucd
func (s *Store) SaveSession(ctx context.Context, sess Session) error {
	if sess.ID == "" {
		return nil
	}
	return nil
}
```

```
$ ds def store/store.go#MaxSessions --owner @auth
maxsessions-n4kpvzvf
$ ds def store/store.go#TTLDays --label sess-ttl --dry-run
sess-ttl-9hz97fhy
would insert at store/store.go:9:
	// ds:def id=sess-ttl-9hz97fhy
```

Run on a block that already has a def, `ds def` prints the existing id and changes nothing, so it is safe to call to look an id up:

```
$ ds def store/store.go#Store.SaveSession
store-savesession-m6twuucd
```

`ds def --fix` re-mints every copy after the first when an id has been duplicated (for example by copying a function).

Ids you type by hand are not validated for shape: `id=Foo_Bar` scans without complaint. Follow the lowercase `label-suffix` form anyway, because `ds rename` and the "did you mean" suggestions work on it.

### Renaming a label

`ds rename` relabels an id everywhere it appears, in source and docs. Either the full id or just the label works:

```
$ ds rename store-savesession sess-save --dry-run
store-savesession-m6twuucd -> sess-save-m6twuucd
1 line(s) would change (--dry-run)
$ ds rename store-savesession sess-save
store-savesession-m6twuucd -> sess-save-m6twuucd
1 line(s) changed; run ds scan
$ ds scan
1 files, 2 defs, 0 refs, 0 problems, 0 skipped
```

Acks and first-seen hashes carry over because the suffix is unchanged. A citation still has to use the current full id; one written with an old label is reported as broken, with the right id suggested:

```markdown file=docs/label.md
# Label

The [guard](ds:block?id=old-label-m6twuucd) works.
```

```
$ ds check
docs/label.md
  3	error    broken             old-label-m6twuucd is not defined; did you mean sess-save-m6twuucd
…
```

## ds:def — give something an identity

A `ds:def` sits directly above the thing it names (or at the end of the line, for a config key), and binds the block below it: a function, a type, a constant, a config key, a markdown section, a paragraph. What counts as "the block" depends on the language; see [Languages](languages.md).

<!-- doctest
mkdir ../defs
cd ../defs
git init -q -b main .
ds init
-->

```go file=store/save.go
package store

// ds:def id=store-save-k7m2p4xq
func Save(id string) error {
	return nil
}
```

```yaml file=config/app.yaml
server:
  port: 8081   # ds:def id=server-port-6btxuz6q
```

```sql file=db/sweep.sql
-- ds:def id=delete-u3e84e69
DELETE FROM sessions WHERE expires_at < now() - interval '30 days';
```

```markdown file=docs/policy.md
# Policy

<!-- ds:def id=sess-policy-h2n8wq4t -->
## Session policy

Sessions live thirty days and rotate on refresh.
```

```text file=docs/rota.txt
ds:def id=span-two-c2d3e4f5 span=+2
Week 37  alex
Week 38  someone
Week 39  third
```

`ds map` shows the line range each def bound (they are `uncovered` because no page cites them yet):

```
$ ds scan
5 files, 5 defs, 0 refs, 0 problems, 0 skipped
$ ds map
PAGE  COVERS  CITES  STATE

DEF                   FILE                 CITED BY  STATE
server-port-6btxuz6q  config/app.yaml:2-2  0         uncovered
delete-u3e84e69       db/sweep.sql:2-2     0         uncovered
sess-policy-h2n8wq4t  docs/policy.md:4-6   0         uncovered
span-two-c2d3e4f5     docs/rota.txt:2-3    0         uncovered
store-save-k7m2p4xq   store/save.go:4-6    0         uncovered
66 tokens used, 0 omitted
```

One def per block and one block per id. Deleting the directive line deletes the block, and every sentence citing it becomes `broken`.

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

<!-- doctest
mkdir ../stability
cd ../stability
git init -q -b main .
ds init
-->

Each change to a cited block is classified (`body`, `signature`, `type`, `renamed`, `moved`, `value`, `comment`, `unknown`; whitespace is never reported). A one-line constant changes by `value` when its value changes and by `type` when what it declares changes. The def's `stability` decides which classes flag the sentences citing it:

| `stability` | Flags on |
|---|---|
| `frozen` | any change except whitespace |
| `stable` (default) | everything except whitespace and comment-only changes |
| `api` | `signature`, `type`, `renamed`, `value`, and changes it could not classify — not a body-only change |
| `volatile` | nothing |

Three functions, cited from one sentence:

```go file=store/stab.go
package store

// ds:def id=stab-api-t3u4v5w6 stability=api
func Api(a int) int {
	return a + 1
}

// ds:def id=stab-volatile-u3v4w5x6 stability=volatile
func Vol(a int) int {
	return a + 1
}

// ds:def id=stab-frozen-v3w4x5y6 stability=frozen
func Frozen(a int) int {
	return a + 1
}
```

```markdown file=docs/stab.md
# Stab

[api](ds:block?id=stab-api-t3u4v5w6) and [vol](ds:block?id=stab-volatile-u3v4w5x6) and [frozen](ds:block?id=stab-frozen-v3w4x5y6) add one.
```

<!-- doctest
ds scan
git add -A
git commit -q -m stability
-->

A body-only edit to each flags only the frozen one:

```
$ sed -i.bak 's/return a + 1/return a + 2/' store/stab.go && rm store/stab.go.bak
$ ds check
docs/stab.md
  3	error    unacked            stab-frozen-v3w4x5y6 changed (body) since this sentence was first cited
      | -	return a + 1
      | +	return a + 2
      still true: ds ack stab-frozen-v3w4x5y6 --doc docs/stab.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/stab.md:3, then ack
1 error, 2 ok
```

Changing the parameter type of the `api` function is a `signature` change and is flagged, but only after `ds scan` has recorded the new block; run `ds scan` before `ds check` when relying on `stability=api`:

```
$ sed -i.bak 's/func Api(a int)/func Api(a int64)/' store/stab.go && rm store/stab.go.bak
$ ds scan
2 files, 3 defs, 3 refs, 0 problems, 0 skipped
$ ds check
docs/stab.md
  3	error    unacked            stab-api-t3u4v5w6 changed (signature, body) since this sentence was first cited
…
```

The full classification rules are in [SPEC §20](../SPEC.md#20-change-classification-and-stability).

## Facts: inline defs in prose

<!-- doctest
mkdir ../facts
cd ../facts
git init -q -b main .
ds init
-->

A fact is a def whose text is one line. In markdown, write it as a link whose text is the value. The first place a fact is written is its home; other pages cite it with `ds:cfg`.

```markdown file=docs/facts.md
# Facts

- The API runs on port [8081](ds:def?id=api-port-h3v8n2wd&type=int).
- Sessions expire after [30 days](ds:def?id=sess-ttl-p2c4y7mk&type=duration).
- The app is hosted at [https://example.com](ds:def?id=app-host-d4k8w2mn&type=url).
```

`ds facts` lists every one-line def in the repository, from prose and from code and config alike, with its current value and how many places cite it:

```
$ ds facts
ID                 VALUE                WHERE            CITED BY
api-port-h3v8n2wd  8081                 docs/facts.md:3  0
sess-ttl-p2c4y7mk  30 days              docs/facts.md:4  0
app-host-d4k8w2mn  https://example.com  docs/facts.md:5  0
```

`ds render` prints each inline def as its plain text; `type=url` renders as a link:

```
$ ds render docs/facts.md
# Facts

- The API runs on port 8081.
- Sessions expire after 30 days.
- The app is hosted at [https://example.com](https://example.com).
```

## Remote defs: files that cannot hold a comment

JSON, CSV and `go.sum` have no comment syntax, so a directive cannot go inside them. Put the def in any file that can hold one (usually the doc that talks about the value) and point at the target with `file=` and `pick=`:

```json file=config/app.json
{"server": {"port": 8081, "host": "api.example.com"}}
```

```markdown file=docs/port.md
# Port

<!-- ds:def id=json-port-k3m4n5p6 file=config/app.json pick=json:$.server.port type=int -->

Port is [8081](ds:cfg?id=json-port-k3m4n5p6).
```

```
$ ds render docs/port.md
# Port


Port is 8081.
```

A remote def hashes the picked value, so the citation is flagged when `server.port` changes. If the key disappears, the citation is `pick failed`. Unlike a def in the file itself, a remote def does not follow its target if the value moves to another file.

`ds def` refuses to write into a file with no comment syntax, and says so:

```
$ ds def config/app.json:1
ds: docsync: no comment carrier for this file type: .json has no comment syntax docsync knows, so a directive cannot be written into it; bind it from a file that does with a remote def (`file=config/app.json pick=…`), …
```

A bare `ds:def` line already present in such a file is reported as `directive is not inside a comment`, and `ds repair --apply` removes it; in a file that does have comments, `ds repair --apply` comments it instead.

## pick — take one value or range out of a block

<!-- doctest
mkdir ../pick
cd ../pick
git init -q -b main .
ds init
-->

`pick=` on a def narrows what is extracted. It returns exactly one line (a value, what `ds:cfg` shows) or one contiguous range (what `ds:block` shows). Most defs need no `pick`: a config key picks its value, a markdown fact picks its link text, a code symbol picks its block, a plain-text def picks to the next blank line.

| `pick=` | Takes |
|---|---|
| `json:$.server.port` | a JSON value by path |
| `yaml:server.port`, `toml:server.port`, `ini:server.port`, `env:API_KEY`, `hcl:resource.aws_instance.web.instance_type` | a key's value |
| `csv:r2c2`, `csv:col=port` | a CSV cell |
| `line:3` | one line of the target |
| `regex:'secrets\.(\w+)'` | the first capture group of the first match |
| `after:'…'`, `between:'(',')'` | the text after a marker, or between two |
| `url` | the first URL in the target |
| `heading`, `section:"Session policy"`, `paragraph:2`, `link:1` | parts of a markdown target |
| `file` | the whole file, hashed (for images, PDFs and generated files) |

These targets:

```yaml file=t/a.yaml
server:
  port: 8081
```

```toml file=t/a.toml
[server]
port = 8082
```

```ini file=t/a.ini
[server]
port = 8083
```

```bash file=t/a.env
API_KEY=abc
```

```hcl file=t/a.tf
resource "aws_instance" "web" {
  instance_type = "t3.micro"
}
```

```csv file=t/a.csv
name,port
api,8084
```

```text file=t/a.txt
see https://example.com/x for more
version: 1.2.3 (stable)
```

```markdown file=t/b.md
# T

## Install

Run it.

Second para.
```

and these remote defs, one per target line:

```markdown file=docs/pick.md
# Pick

<!-- ds:def id=p-yaml-a2a2a2a2 file=t/a.yaml pick=yaml:server.port -->
<!-- ds:def id=p-toml-b2b2b2b2 file=t/a.toml pick=toml:server.port -->
<!-- ds:def id=p-ini-c2c2c2c2 file=t/a.ini pick=ini:server.port -->
<!-- ds:def id=p-env-d2d2d2d2 file=t/a.env pick=env:API_KEY -->
<!-- ds:def id=p-hcl-e2e2e2e2 file=t/a.tf pick=hcl:resource.aws_instance.web.instance_type -->
<!-- ds:def id=p-csvc-f2f2f2f2 file=t/a.csv pick=csv:col=port -->
<!-- ds:def id=p-url-g2g2g2g2 file=t/a.txt pick=url -->
<!-- ds:def id=p-after-h2h2h2h2 file=t/a.txt pick="after:'version: '" -->
<!-- ds:def id=p-head-k2k2k2k2 file=t/b.md pick=heading -->
<!-- ds:def id=p-para-n2n2n2n2 file=t/b.md pick=paragraph:2 -->
<!-- ds:def id=p-sect-m2m2m2m2 file=t/b.md pick=section:"Install" -->
```

give these values:

```
$ ds facts
ID                VALUE                  WHERE       CITED BY
p-yaml-a2a2a2a2   8081                   t/a.yaml:2  0
p-toml-b2b2b2b2   8082                   t/a.toml:2  0
p-ini-c2c2c2c2    8083                   t/a.ini:2   0
p-env-d2d2d2d2    abc                    t/a.env:1   0
p-hcl-e2e2e2e2    t3.micro               t/a.tf:2    0
p-csvc-f2f2f2f2   8084                   t/a.csv:2   0
p-url-g2g2g2g2    https://example.com/x  t/a.txt:1   0
p-after-h2h2h2h2  1.2.3 (stable)         t/a.txt:2   0
p-head-k2k2k2k2   T                      t/b.md:1    0
p-para-n2n2n2n2   Second para.           t/b.md:7    0
$ ds read p-sect-m2m2m2m2
## Install

Run it.

Second para.
```

A `regex` pick in a code comment:

```go
// ds:def id=app-stripe-key-m4w8k2qn secret=true source=env from=gh-stripe-key-r4t6x2mb pick=regex:'"(\w+)"'
key := os.Getenv("STRIPE_KEY")
```

picks `STRIPE_KEY` out of the line (see [Secrets and chains](#secrets-and-chains)).

A `pick` argument that contains a space must have the whole value quoted, because a value is only quoted when it starts with a quote: write `pick="after:'version: '"`, not `pick=after:'version: '`; the second is a `problem`, `key must match [a-z][a-z0-9_]*`.

Two remote defs whose picks land on the same line of the same file are reported as `two ids bound to the same block`, even when the picks differ. `ds scan` names both directives:

```markdown file=docs/pick.md append=true
<!-- ds:def id=p-betw-j2j2j2j2 file=t/a.txt pick=between:'(',')' -->
```

```
$ ds scan
9 files, 12 defs, 0 refs, 2 problems, 0 skipped
  docs/pick.md:10  scan: two ids bound to the same block: p-after-h2h2h2h2, p-betw-j2j2j2j2 at t/a.txt:2-2
  docs/pick.md:14  scan: two ids bound to the same block: p-after-h2h2h2h2, p-betw-j2j2j2j2 at t/a.txt:2-2
```

Keep one remote def per target line.

## ds:block — cite or show a block

<!-- doctest
mkdir ../block
cd ../block
git init -q -b main .
ds init
-->

`ds:block` has two shapes, chosen by the carrier. Both examples cite this function:

```go file=store/store.go
package store

import "context"

type Session struct {
	ID string
}

type Store struct{}

var ErrEmpty error

// SaveSession writes the session.
// ds:def id=store-savesession-m6twuucd
func (s *Store) SaveSession(ctx context.Context, sess Session) error {
	if sess.ID == "" {
		return ErrEmpty
	}
	return nil
}
```

**As a link**, it is a citation: the sentence around it depends on the block, and is flagged when the block changes. **In block position** (an HTML comment on its own line), it shows the code itself when the page is rendered:

```markdown file=docs/sessions.md
# Sessions

The guard is [`SaveSession`](ds:block?id=store-savesession-m6twuucd). It returns early when the id is empty.

<!-- ds:block id=store-savesession-m6twuucd lines=1-3 title="the guard" -->
```

````
$ ds render docs/sessions.md
# Sessions

The guard is [`SaveSession`](store/store.go#L15-L20). It returns early when the id is empty.

**the guard** · [`store/store.go:15-20`](store/store.go#L15-L20)

```go
func (s *Store) SaveSession(ctx context.Context, sess Session) error {
	if sess.ID == "" {
		return ErrEmpty
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

<!-- doctest
ds scan
git add -A
git commit -q -m sessions
-->

```
$ sed -i.bak 's/return nil/return save(sess)/' store/store.go && rm store/store.go.bak
$ ds check
docs/sessions.md
  3	error    unacked            store-savesession-m6twuucd changed (body) since this sentence was first cited
      | -	return nil
      | +	return save(sess)
      still true: ds ack store-savesession-m6twuucd --doc docs/sessions.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/sessions.md:3, then ack
  5	error    unacked            store-savesession-m6twuucd changed (body) since this sentence was first cited
      | -	return nil
      | +	return save(sess)
      still true: ds ack store-savesession-m6twuucd --doc docs/sessions.md --line 5 --note '…'
      otherwise:  edit the sentence at docs/sessions.md:5, then ack
2 error
$ ds ack store-savesession-m6twuucd --all --note 'still returns early'
acked store-savesession-m6twuucd at docs/sessions.md:3 (human)
acked store-savesession-m6twuucd at docs/sessions.md:5 (human)
$ ds check
2 ok
```

- `broken` when the id is not defined, `range` when `lines=` falls outside the block, and `too-large` when a block-position cite would render more than `[include] max_lines` lines (default 40):

```markdown file=docs/more.md
# More

[missing](ds:block?id=nothing-here-abcdefgh)

<!-- ds:block id=store-savesession-m6twuucd lines=4-30 -->
```

```
$ ds check
docs/more.md
  3	error    broken             nothing-here-abcdefgh is not defined
      fix: the id nothing-here-abcdefgh is not defined; fix the id in docs/more.md:3 or re-add the ds:def on the block it meant
  5	warning  range              lines=4-30 outside the block's 6 lines
      fix: adjust lines= at docs/more.md:5 to fit the block's 6 lines
1 error, 1 warning, 2 ok
```

Deleting a def breaks its citations and says where it was last seen:

```
$ sed -i.bak '/ds:def id=store-savesession/d' store/store.go && rm store/store.go.bak
$ ds check
docs/more.md
  3	error    broken             nothing-here-abcdefgh is not defined
      fix: the id nothing-here-abcdefgh is not defined; fix the id in docs/more.md:3 or re-add the ds:def on the block it meant
  5	error    broken             store-savesession-m6twuucd was deleted (last seen store/store.go:15)
      fix: the id store-savesession-m6twuucd is not defined; fix the id in docs/more.md:5 or re-add the ds:def on the block it meant
docs/sessions.md
  3	error    broken             store-savesession-m6twuucd was deleted (last seen store/store.go:15)
      fix: the id store-savesession-m6twuucd is not defined; fix the id in docs/sessions.md:3 or re-add the ds:def on the block it meant
  5	error    broken             store-savesession-m6twuucd was deleted (last seen store/store.go:15)
      fix: the id store-savesession-m6twuucd is not defined; fix the id in docs/sessions.md:5 or re-add the ds:def on the block it meant
4 error
```

`at=` is a snapshot and `ds check` reports it as `ok` with "snapshot pinned". The snapshot's code is produced by `ds render --at <commit>`; a plain `ds render` prints the as-of badge and link and notes `no snapshot for … rendering the link only`.

## ds:cfg — put a value in a sentence

<!-- doctest
mkdir ../cfg
cd ../cfg
git init -q -b main .
ds init
-->

`ds:cfg` inlines the current value of a one-line def. Link form only. The link text is the last known value, so the raw markdown still reads well; the build replaces it.

```go file=store/limits.go
package store

const (
	// ds:def id=maxsessions-k8p2w4rd owner=@auth
	MaxSessions = 5
)
```

```markdown file=docs/limits.md
# Limits

A user may hold at most [5](ds:cfg?id=maxsessions-k8p2w4rd) sessions.
```

```
$ ds render docs/limits.md
# Limits

A user may hold at most 5 sessions.
```

A Go constant, a YAML key, a TOML key, a fact and a remote def all work as the target.

| Key | Meaning |
|---|---|
| `id` | required; a def whose extracted value is one line |
| `format` | `raw` (default), `code`, `quote`, `host`, `link`, `compact` |
| `env` | which environment's def; default from `[env] default` or `ds render --env` |

Formats, all from one def holding `https://example.com` and one holding `8081`. Put each inline def on its own line: two facts on one line are reported as `two ids bound to the same block`.

```markdown file=docs/fmt.md
# Fmt

- The API runs on port [8081](ds:def?id=api-port-h3v8n2wd&type=int).
- It is hosted at [https://example.com](ds:def?id=app-host-d4k8w2mn&type=url).
- raw [x](ds:cfg?id=app-host-d4k8w2mn)
- code [x](ds:cfg?id=app-host-d4k8w2mn&format=code)
- quote [x](ds:cfg?id=app-host-d4k8w2mn&format=quote)
- host [x](ds:cfg?id=app-host-d4k8w2mn&format=host)
- link [x](ds:cfg?id=app-host-d4k8w2mn&format=link)
- compact [x](ds:cfg?id=api-port-h3v8n2wd&format=compact)
```

```
$ ds render docs/fmt.md
# Fmt

- The API runs on port 8081.
- It is hosted at [https://example.com](https://example.com).
- raw https://example.com
- code `https://example.com`
- quote "https://example.com"
- host example.com
- link [https://example.com](https://example.com)
- compact 8.1K
```

Citing a block that yields more than one line is refused, and a changed value is `unacked`, like a changed block:

```go file=store/save.go
package store

// ds:def id=save-func-w2x3y4z5
func Save() error {
	return nil
}
```

```markdown file=docs/limits.md append=true

Saving is [this](ds:cfg?id=save-func-w2x3y4z5).
```

<!-- doctest
ds scan
git add -A
git commit -q -m cfg
-->

```
$ sed -i.bak 's/MaxSessions = 5/MaxSessions = 8/' store/limits.go && rm store/limits.go.bak
$ ds check
docs/limits.md
  3	error    unacked            maxsessions-k8p2w4rd changed (value) since this sentence was first cited
      | -5
      | +8
      still true: ds ack maxsessions-k8p2w4rd --doc docs/limits.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/limits.md:3, then ack
  5	warning  range              ds:cfg on a 3-line block
      fix: the def yields 3 lines; ds:cfg needs one line, use ds:block or a narrower pick=
1 error, 1 warning, 6 ok
```

## ds:claim — a sentence that must be re-reviewed

<!-- doctest
mkdir ../claim
cd ../claim
git init -q -b main .
ds init
-->

A claim needs no def. It puts a review date on a sentence, for statements you want to be reminded about even when no code changed.

| Key | Meaning |
|---|---|
| `owner` | who reviews it |
| `reviewed` | the last review date, `YYYY-MM-DD` |
| `expires` | how long a review lasts, such as `90d` |
| `about` | comma list of ids; a change to any of them also expires the claim |

```go file=store/limits.go
package store

const (
	// ds:def id=maxsessions-k8p2w4rd
	MaxSessions = 5
)
```

```markdown file=docs/claims.md
# Claims

We chose Postgres over Redis because ops already runs Postgres. <!-- ds:claim owner=@platform reviewed=2026-09-06 expires=3650d -->

The cache is warmed nightly. <!-- ds:claim owner=@platform reviewed=2026-01-01 expires=30d -->

Sessions are capped. <!-- ds:claim owner=@auth reviewed=2026-09-20 expires=3650d about=maxsessions-k8p2w4rd -->
```

<!-- doctest
ds scan
git add -A
git commit -q -m claims
-->

The first claim is within its window; the second is past it:

```
$ ds check
docs/claims.md
  5	error    expired            claim reviewed 2026-01-01 expired after 30d
      fix: review the claim at docs/claims.md:5 and run ds ack --doc docs/claims.md --line 5 to renew it
1 error, 2 ok
```

With `about=`, a change to a listed block expires the claim at once:

```
$ sed -i.bak 's/MaxSessions = 5/MaxSessions = 9/' store/limits.go && rm store/limits.go.bak
$ ds check
docs/claims.md
  5	error    expired            claim reviewed 2026-01-01 expired after 30d
      fix: review the claim at docs/claims.md:5 and run ds ack --doc docs/claims.md --line 5 to renew it
  7	error    expired            claim is about maxsessions-k8p2w4rd, which changed (value)
      fix: review the claim at docs/claims.md:7 and run ds ack --doc docs/claims.md --line 7 to renew it
2 error, 1 ok
```

Renew a claim with `ds ack --doc <doc> --line <n> --note '…'`. For an `about=` claim, run `ds scan` after the ack so the finding clears:

```
$ ds ack --doc docs/claims.md --line 7 --note 'cap still applies'
renewed the claim at docs/claims.md:7 (human)
$ ds scan
2 files, 1 defs, 3 refs, 0 problems, 0 skipped
$ ds check
docs/claims.md
  5	error    expired            claim reviewed 2026-01-01 expired after 30d
      fix: review the claim at docs/claims.md:5 and run ds ack --doc docs/claims.md --line 5 to renew it
1 error, 2 ok
```

## ds:url — an outside link that is watched

<!-- doctest
mkdir ../url
cd ../url
git init -q -b main .
ds init
-->

```markdown file=docs/url.md
# Links

See the [Go spec](ds:url?href=https://go.dev/ref/spec&title=Rust).

The [old address](ds:url?href=http://go.dev/ref/spec) still works.

This [page](ds:url?href=https://go.dev/this-page-does-not-exist-xyz) is gone.
```

| Key | Meaning |
|---|---|
| `href` | required; the external URL |
| `title` | the expected page title, or a substring of it; a change means the page moved or was rewritten |
| `expect` | the HTTP status the final response must have, after redirects; without it any status below 400 is alive. A value that is not a status from 100 to 599 is a `problem` |

`ds render` turns it into an ordinary link. Without network access the check is skipped with a warning:

```
$ ds check
docs/url.md
  3	warning  unverifiable       external link not checked
      fix: external link checks need --resolve with network access
  5	warning  unverifiable       external link not checked
      fix: external link checks need --resolve with network access
  7	warning  unverifiable       external link not checked
      fix: external link checks need --resolve with network access
3 warning
```

With `[resolve] enabled = true` in the config and `ds check --resolve`, `ds` fetches each page and reports `dead` (an error), `retitled` and `url moved` (warnings). A request that gets no answer at all, such as on a machine with no network, stays `unverifiable` and is not cached:

<!-- doctest:skip needs network access to go.dev -->

```
$ ds check --resolve
docs/url.md
  3	warning  retitled           title is now "The Go Programming Language Specification - The Go Programming Language"
      fix: the page at https://go.dev/ref/spec no longer has title "Rust"; confirm it is still the right page
  5	warning  url moved          http://go.dev/ref/spec redirects to https://go.dev/ref/spec
      fix: the link http://go.dev/ref/spec now redirects to https://go.dev/ref/spec; update it at docs/url.md:5
  7	error    dead               https://go.dev/this-page-does-not-exist-xyz returned 404
```

Remember the link-form rule: a `title` with spaces must be written with `%20`.

## ds:run — run something and record the result

<!-- doctest
mkdir ../run
cd ../run
git init -q -b main .
ds init
-->

| Key | Meaning |
|---|---|
| exactly one of `id`, `cmd`, `file` | what to run; `id` needs `runnable=true` on the def |
| `expect` | every mode needs exit status zero, then: `ok` nothing more; `rows` some output; an HTTP status such as `200`, the last status the output reports (the last `HTTP/…` status line, else the last non-blank line); anything else a substring the output must contain |
| `timeout` | a positive duration such as `90s` that replaces `run.timeout` for this command; one that does not parse is a `problem` and the command does not run |
| `env`, `show` | environment, and what the renderer shows (`output`, `command`, `both`, `none`) |

The def for an `id=` run is a block marked runnable:

```bash file=scripts/tasks.sh
# ds:def id=hello-task-q3r4s5t6 runnable=true
echo hello from task

# ds:def id=not-runnable-r3s4t5u6
echo nope
```

```markdown file=docs/run.md
# Run

<!-- ds:run id=hello-task-q3r4s5t6 expect=ok -->

<!-- ds:run id=not-runnable-r3s4t5u6 expect=ok -->

<!-- ds:run cmd="echo hi" expect="hi" -->
```

Nothing runs unless the config enables it and the doc matches `allow`:

```toml file=.ds/config.toml append=true

[run]
enabled = true
allow = ["docs/**"]
```

and then only with `ds check --run`. Without `--run` each directive is `skipped` (info). With it:

```
$ ds check --run
docs/run.md:3  run ok: echo hello from task
docs/run.md:5  run skipped: not-runnable-r3s4t5u6 is not runnable=true
docs/run.md:7  run ok: echo hi
docs/run.md
  5	info     skipped            run not executed: not-runnable-r3s4t5u6 is not runnable=true
1 info, 2 ok
```

[Secrets and runs](secrets-and-runs.md) covers the run configuration and its safety rules.

## ds:table — a table over records

```markdown file=docs/tasks.md
# Tasks

<!-- ds:table kind=task where="state!=done" cols=title,owner -->
```

Keys: `kind`, `where`, `cols`, `sort`, `limit`, `empty`. It needs a record source under `[records]` in the config (see [Configuration](configuration.md)); without one it is reported and renders nothing:

```
$ ds check
…
docs/tasks.md
  3	warning  unverifiable       no record source configured
      fix: register a record source in [records] to render ds:table
…
```

## Secrets and chains

<!-- doctest
mkdir ../secrets
cd ../secrets
git init -q -b main .
ds init
-->

Secrets reach the repository as addresses (`${{ secrets.STRIPE_KEY }}`, `op://…`, an env var name), and docsync works on those addresses only. Mark the def `secret=true`; link copies of one value with `from=` and mark the single root `truth=true`. `ds:chain` renders the path:

```bash file=.env.tpl
STRIPE_KEY=op://Platform/stripe-prod/credential   # ds:def id=op-stripe-key-p9c2v7ld secret=true truth=true
```

```yaml file=.github/workflows/deploy.yml
env:
  STRIPE_KEY: ${{ secrets.STRIPE_KEY }}   # ds:def id=gh-stripe-key-r4t6x2mb secret=true from=op-stripe-key-p9c2v7ld sync=scripts/sync-secrets.sh
```

```go file=store/pay.go
package store

import "os"

func Key() string {
	// ds:def id=app-stripe-key-m4w8k2qn secret=true source=env from=gh-stripe-key-r4t6x2mb pick=regex:'"(\w+)"'
	key := os.Getenv("STRIPE_KEY")
	return key
}
```

```markdown file=docs/sec.md
# Secrets

Stripe: <!-- ds:chain id=app-stripe-key-m4w8k2qn -->
```

```
$ ds why app-stripe-key-m4w8k2qn --chain
app-stripe-key-m4w8k2qn  stmt  store/pay.go:7-7
  docs/sec.md:3  ds:chain  Stripe:
app-stripe-key-m4w8k2qn  STRIPE_KEY  store/pay.go:7
  from gh-stripe-key-r4t6x2mb  ${{ secrets.STRIPE_KEY }}  .github/workflows/deploy.yml:2
    from op-stripe-key-p9c2v7ld  op://Platform/stripe-prod/credential  .env.tpl:1  TRUTH
```

`ds:chain` takes `id` and `env`. A `ds:block` of a secret def renders nothing (`ds:block refuses to render it`). Provider checks, rotation, and the chain findings (`unsourced`, `chain broken`, `out of sync`, `stale copy`) are in [Secrets and runs](secrets-and-runs.md) and [SPEC §12](../SPEC.md#12-secrets-chains-and-environments).

## Environments

<!-- doctest
mkdir ../envs
cd ../envs
git init -q -b main .
ds init
-->

The same id can be defined once per environment with `env=`, and cites choose one with `env=`:

```bash file=config/prod.env
API_HOST=api.example.com   # ds:def id=api-host-r2s3t4u5 env=prod
```

```bash file=config/staging.env
API_HOST=staging.example.com   # ds:def id=api-host-r2s3t4u5 env=staging
```

```markdown file=docs/hosts.md
# Hosts

Prod is [api](ds:cfg?id=api-host-r2s3t4u5&env=prod), staging is [s](ds:cfg?id=api-host-r2s3t4u5&env=staging).
```

```
$ ds render docs/hosts.md
# Hosts

Prod is api.example.com, staging is staging.example.com.
```

A cite without `env=` uses `[env] default`, or `--env` on `ds check` and `ds render`; set the default rather than relying on whichever def is found first. Citing an environment with no def is broken:

```markdown file=docs/dev.md
# Dev

Dev is [d](ds:cfg?id=api-host-r2s3t4u5&env=dev).
```

```
$ ds check
docs/dev.md
  3	error    broken             api-host-r2s3t4u5 is not defined for env=dev
      fix: api-host-r2s3t4u5 has no definition for env=dev; add one or cite a defined environment
1 error, 2 ok
```

## Deprecation and sunset

<!-- doctest
mkdir ../sunset
cd ../sunset
git init -q -b main .
ds init
-->

```go file=store/old.go
package store

// ds:def id=legacy-save-q2w3e4r5 deprecated=2026-09-01 desc="old writer"
func LegacySave() {}

// ds:def id=older-save-t6y7u8i9 sunset=2026-09-01
func OlderSave() {}
```

```markdown file=docs/old.md
# Old

Use [legacy](ds:block?id=legacy-save-q2w3e4r5) or [older](ds:block?id=older-save-t6y7u8i9).
```

Every citation of the first gets an info finding from its date; every citation of the second fails after its date:

```
$ ds check
docs/old.md
  3	info     deprecated         legacy-save-q2w3e4r5 deprecated since 2026-09-01
      fix: legacy-save-q2w3e4r5 is deprecated since 2026-09-01; plan to move the reference at docs/old.md:3
  3	error    sunset             older-save-t6y7u8i9 reached sunset 2026-09-01
      fix: older-save-t6y7u8i9 passed its sunset date 2026-09-01; remove the reference at docs/old.md:3
1 error, 1 info
```

## Translations

<!-- doctest
mkdir ../i18n
cd ../i18n
git init -q -b main .
ds init
-->

Define the source paragraph, then mark the translated paragraph with `translates=true`:

```markdown file=docs/policy.md
# Policy

<!-- ds:def id=retention-para-w4x5y6z7 -->
Logs are kept for ninety days.
```

```markdown file=docs/es/policy.md
# Política

<!-- ds:block id=retention-para-w4x5y6z7 translates=true -->
Los registros se guardan noventa días.
```

<!-- doctest
ds scan
git add -A
git commit -q -m i18n
-->

When the source changes:

```
$ sed -i.bak 's/ninety days/sixty days/' docs/policy.md && rm docs/policy.md.bak
$ ds check
docs/es/policy.md
  3	error    translation stale  source retention-para-w4x5y6z7 changed (body)
      | -Logs are kept for ninety days.
      | +Logs are kept for sixty days.
      fix: the source paragraph retention-para-w4x5y6z7 changed; update the translation at docs/es/policy.md:3 and ack
1 error
```

## Page frontmatter: covers and review_every

<!-- doctest
mkdir ../covers
cd ../covers
git init -q -b main .
ds init
-->

A markdown page can declare itself the home of ids in its frontmatter:

```go file=store/session.go
package store

type Session struct {
	// ds:def id=session-user-yk7dsbtd
	User string
}
```

```markdown file=docs/store.md
---
title: Store
ds:
  covers: [session-user-yk7dsbtd, gone-thing-abcdefgh]
  review_every: 180d
---
# Store

Each session belongs to one user.
```

A covered id no longer counts as `uncovered`. A covered id that is not defined is an orphan:

```
$ ds check
docs/store.md
  1	warning  orphan             covers gone-thing-abcdefgh, which is not defined
      fix: the page docs/store.md covers gone-thing-abcdefgh, which is not defined; remove it from covers or restore the def
1 warning
```

`review_every` asks for the whole page to be re-read on a schedule even when nothing it cites changed; `ds ack --doc <page>` records the review. A def that nothing cites or covers is reported as `uncovered` (info), which never fails a run.

## Mistakes ds check catches

<!-- doctest
mkdir ../mistakes
cd ../mistakes
git init -q -b main .
ds init
-->

```markdown file=docs/bad.md
# Bad

<!-- ds:block id=x-abcdefgh id=y -->

<!-- ds:def owner=@x -->
Para with no id.
```

```go file=store/bad.go
package store

ds:def id=bad-go-e3f4g5h6
var C = 1
```

```
$ ds check
docs/bad.md
  3	error    problem            column 23: directive: duplicate key
      fix: fix the directive at docs/bad.md:3: column 23: directive: duplicate key
  5	error    problem            extract: ds:def without id=
      fix: fix the directive at docs/bad.md:5: extract: ds:def without id=
store/bad.go
  3	error    problem            scan: directive is not inside a comment: store/bad.go carries comments, so a bare directive line is probably not valid there; ds repair --apply comments it
      fix: fix the directive at store/bad.go:3: scan: directive is not inside a comment: store/bad.go carries comments, so a bare directive line is probably not valid there; ds repair --apply comments it
  3	info     uncovered          defined but never cited or covered
      fix: bad-go-e3f4g5h6 is defined but nothing cites or covers it; cite it from a page or remove the def
3 error, 1 info
$ ds repair
store/bad.go:3
  - ds:def id=bad-go-e3f4g5h6
  + // ds:def id=bad-go-e3f4g5h6
1 line(s) would be repaired, 0 need a person (run with --apply to write)
```

| You wrote | `ds check` says |
|---|---|
| a def with no `id=` | `error problem extract: ds:def without id=` |
| the same key twice | `error problem column N: directive: duplicate key` |
| an id nobody defines | `error broken nothing-here-abcdefgh is not defined` |
| a misspelled verb or key | `warning unknown` (an error with `--strict`) |
| a bare `ds:def` line in Go or JSON | `error problem scan: directive is not inside a comment: …` (`ds repair --apply` comments it, or removes it in JSON) |
| two defs over the same lines | `error problem scan: two ids bound to the same block: …` |
| a def with nothing below it | `error problem extract: ds:def has nothing after it to bind to` |
| a `pick=` that matches nothing | `error pick failed scan: remote def pick failed: …: pick: nothing matched: …` |

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
