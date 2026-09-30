# docsync — Specification 1.0

Keep every document bound to the code, config, and facts it describes, in any file, across any number of repositories, with no new markup.

| | |
|---|---|
| Version | 1.0, frozen for the first implementation |
| Date | 2026-09-06 |
| Product name | docsync |
| Prefix and CLI | `ds` |
| Status of this document | normative; the conformance suite (Part VIII) is the tie-breaker where prose is ambiguous |
| JSON contract | `json_format = 1`, section 26.2; independent of the ledger `format` |
| Ledger contract | `format = 2` (section 15); `format = 1` is still read |

**How to read this.** Part I says what problem exists and what the tool promises. Part II gives the mental model in one page. Part III is the language reference: every directive, key, and rule. Part IV explains how checking works. Part V is how to run it, including the agent surface in section 26. Part VI is scenarios. Part VII is the edge-case rulebook. Part VIII covers guarantees, conformance, decisions, and the build order. A reader who wants only to write docs needs Parts II and III.

---

## Part I — Why

### 1. The problem

A document points at code and nothing tells anyone when the code moves, changes, or disappears. Every existing answer covers one corner:

| Approach | What it fixes | Why docs still rot |
|---|---|---|
| docs next to code, reviewed in PRs | proximity | reviewers read code, not prose; nothing flags a doc when code changes |
| generated reference (godoc, OpenAPI) | signatures | never the why, the architecture, the runbook |
| executable docs (doctest, notebooks) | claims that can run | prose about design cannot run |
| snippet sync tools | one pasted block | prose citations still float; proprietary; snippet-only |
| ADRs | never edited, only superseded | only decisions get written that way |
| wikis | easy to write | easiest to rot; no signal of any kind |
| "let the AI rewrite the docs" | effort | it must reread everything on every commit because nothing records which sentence depends on which code |

Each row is a missing **identity** (what is this block, stably) or a missing **back-reference** (who depends on it). docsync adds both and nothing else.

### 2. Principles

1. **No new markup.** Markdown stays markdown, code stays code. Everything the tool needs rides inside the comments each file format already has.
2. **One directive shape.** `ds:<verb> key=value …`. The scanner looks for the prefix and nothing else; verbs are a registry.
3. **Define once, cite everywhere.** A fact or a block has exactly one home. Every other mention is a pointer resolved at build time. Pointers cannot drift.
4. **The tool is the bookkeeper, a reviewer is the judge.** It knows what changed and who depends on it. It never decides whether a sentence is still true. A person or an AI does, and that decision is recorded against a hash.
5. **Any file can define, any file can refer.** Code, config, markdown, plain text, CSV, diagrams. Known formats are handled precisely; unknown ones fall back to lines and spans.
6. **Secrets are addresses, never values.** The tool may verify that an address resolves. It may never store, render, or log what it points to.
7. **Nothing surprising.** Source is written only by three commands (`def`, `adopt`, `rename`), each with a dry run and an undo, and every automated judgment is a warning until a human makes it a rule.

### 3. The promise, in three lines

- **A cited fact cannot drift.** Pages contain pointers, not values; the build fills them in.
- **A cited block cannot silently change.** Moves and renames are absorbed. Changes and deletions stop the merge until someone acks or edits, at the exact sentence.
- **Prose is only as fresh as its last ack.** The tool asks; a reviewer answers; the answer is stored with the hash it approved, so the next change asks again.

Section 34 lists what remains outside these promises and the guard for each.

---

## Part II — The model in one page

### 4. Five words

| Word | Meaning | Written as |
|---|---|---|
| **define** | give one block, line, or value a permanent identity, at the place it lives | `ds:def id=…` |
| **cite** | point at a defined thing from anywhere else; the build resolves it | `ds:block?id=…`, `ds:cfg?id=…` |
| **check** | compare everything defined against what was last recorded and report, per sentence, what may now be wrong | `ds check` |
| **ack** | a reviewer states that a sentence is still true for the current hash | `ds ack` |
| **chain** | the declared path a value travels, from its single truth through its copies | `from=`, `truth=` |

### 5. The lifecycle

```
define  →  cite  →  publish  →  change happens  →  check  →  ack or edit  →  (repeat)
  │          │         │                              │
  id in      pointer   ledger + refs go to the        finding at doc line,
  source     in prose  workspace index                with class and diff
```

Every arrow is one CLI command. Nothing is inferred that was not declared; nothing declared is ever silently dropped.

### 6. What lives where

| Thing | Lives in | Written by |
|---|---|---|
| directive | a comment in any file, or a markdown link target | authors, or `ds def` / `ds adopt` |
| ledger | `.ds/ledger.tsv`, generated, committed | `ds scan` |
| reverse index | `.ds/refs.tsv`, generated, committed | `ds scan` |
| foreign snapshot | `.ds/foreign.tsv`, generated, committed; the foreign blocks this repo cites, so `check` is reproducible | `ds sync` |
| block bodies | `.ds/blocks/<hash>`, generated, committed; never holds a secret or local block; pruned by `ds prune` | `ds scan` |
| acks and audit log | `.ds/acks.tsv`, append-only, committed; each row carries actor kind; merged with git's union driver through the `.ds/.gitattributes` that `ds init` writes | `ds ack`, `ds publish`, resolvers |
| workspace index | a git repo or URL holding every repo's ledger, refs, and block bodies | `ds publish` |
| rendered output | wherever the site builds; never in the repo | the renderer, `ds render` |
| machine-local state | `.ds/cache/`, `.ds/journal.tsv`, `.ds/index/`, `.ds/{urls,runs,notified,hashes,metrics}.json`; **never committed**, excluded by the `.ds/.gitignore` that `ds init` writes | `scan`, `def`, `sync`, `check` |

The split matters for more than noise. The ledger, refs, acks and block bodies are shared facts and hold hashes or non-secret text. The machine-local files hold raw source: the extraction cache stores block bodies so a scan can skip re-reading, and the journal stores the lines an edit replaced so `undo` can reverse it. Committing either puts that text in history, where rotating a credential cannot remove it. `ds doctor` reports an ignore list that is missing or has fallen behind.

The ack log is append-only, so two branches that each record an ack both add rows at its end, and git's default merge reports that as a conflict — in a team, on nearly every pair of pull requests that touch docs. `ds init` therefore writes `.ds/.gitattributes` with `acks.tsv merge=union`, which keeps both sides' rows, and the newest ack for a sentence is chosen by its timestamp, not by where the merge put it. The ledger, refs, and foreign snapshot are deliberately not union-merged: they are rewritten rather than appended, and a union would keep a row's old and new versions side by side. `ds doctor` reports a missing attribute. A merge that does conflict is reported as one, with the remedy for that file: take either side of the ledger or the snapshot and rescan or resync, but keep every row from both sides of the refs, because dropping one loses first-seen hashes nothing else can recreate.

---

## Part III — Language reference

### 7. The directive

```
ds:<verb> key=value key=value …
```

| Element | Rule |
|---|---|
| prefix | `ds:` glued to the verb; configurable per workspace; the only token the scanner searches for |
| verb | `[a-z][a-z0-9_]*`; resolved through a handler registry |
| arguments | `key=value` only, no positionals; keys `[a-z][a-z0-9_]*`; a repeated key is an error |
| values | bare up to whitespace; `"…"` when containing spaces; `'…'` when containing double quotes; a value needing both goes on a continuation line as raw text; lists are comma separated inside one value; there is no escape character |
| continuation | a comment line directly below starting with whitespace and `key=value` folds into the directive above |
| unknowns | an unknown verb or key is one warning and is otherwise ignored, so older tools read newer files; `--strict` makes it an error |
| required keys | each verb declares them; a missing one is an error naming it |

Why glued: `// ds is the datastore` must never parse as a directive. Why no positionals: one grammar for every verb, every argument self-describing.

#### 7.1 Carriers: where a directive may sit

The same text, byte for byte, inside whatever the host already treats as a comment, or inside a markdown link target as a URL scheme with the arguments as a query string.

| Host | Written as | What `ds:def` binds to |
|---|---|---|
| Go, TypeScript, Rust, C, any language with a tree-sitter grammar | `// ds:def id=…` | the next declaration: function, method, type, const, statement; decorators, attributes, and doc comments in between are skipped |
| Python, shell | `# ds:def id=…` | the next declaration; the directive may sit anywhere inside the leading comment or docstring block |
| SQL, Lua | `-- ds:def id=…` | the next statement |
| CSS, C block comments | `/* ds:def id=… */` | the next rule or symbol |
| YAML, TOML, INI, env, properties, HCL | `# ds:def id=…` on or above the line | that key's value; `span=+N` widens |
| Markdown | `<!-- ds:def id=… -->` above a heading or paragraph | a heading binds its section until the next heading of the same or higher level; a paragraph binds that paragraph; `span=+N` overrides |
| MDX | `{/* ds:def id=… */}` | as markdown; MDX has no HTML comments |
| HTML, XML, SVG | `<!-- ds:def id=… -->` | the next element |
| plain text, CSV, logs, unknown extensions | a bare line `ds:def id=…` | the following lines until a blank line, or `span=+N`; renderers strip the line |
| markdown, block position, referring | `<!-- ds:block id=… -->` | |
| markdown, inline, referring | `[text](ds:block?id=…&lines=1-6)` | |
| any comment in any file, referring | `// implements ds:block?id=…` | hover in the editor, row in the reverse index |

Rules that apply to every carrier:
- A directive inside a string literal, a code fence, an indented code block, or an inline code span is not a directive; in markdown these follow CommonMark (a span pairs backtick runs of equal length; indentation of four columns is code after a blank line or a heading, but inside a list it is a continuation of the item). `ds adopt` skips the same examples. Fixture: `code-examples-are-not-carriers`.
- Only **source** files are scanned: the markdown or MDX a site is built from, never `public/` or `dist/`; the template a shortcode lives in, never its expansion.
- `vendor/`, `node_modules/`, generated paths, and git submodules are skipped unless a submodule is a workspace member in its own right.

### 8. Ids

`<prefix>-<suffix>`, for example `sess-save-k7m2p4xq`.

| Part | Rule | Why |
|---|---|---|
| prefix | human slug, lowercase, dashes, two to four words | so `grep sess-save` finds it |
| suffix | eight characters from `23456789abcdefghjkmnpqrstuvwxyz`, generated | about 10^12 values; four was too few, collisions appear in the low thousands |
| identity | **the suffix is the identity, the prefix is a label** | `sess-save-k7m2p4xq` and `session-persist-k7m2p4xq` are one block; `ds rename` changes labels, never identity |
| uniqueness | across the whole workspace; minting consults the merged index and re-rolls on collision | |
| characters | lowercase ASCII only | |

### 9. Verbs

Eight built-in verbs. One defines; the rest refer. The verb says **what to do with the thing you point at**: what the renderer outputs there, and what the checker asks about it.

| Verb | Points at | Renders as | The checker asks | Lives in |
|---|---|---|---|---|
| `def` | nothing; it is the thing | invisible | did this block change, move, or vanish? | any file that can hold a comment or a bare line |
| `block` | a defined block | a link to it, or the code itself when in block position | did the code behind this sentence change? | docs, and comments in code |
| `cfg` | a defined one-line value | the current value, inline in the sentence | did the value change? | docs, link form only |
| `chain` | a defined value that has `from=` hops | the whole path from its truth through its copies | is the chain complete, single-rooted, and consistent? | docs |
| `run` | a runnable def, a command, or a file | the command and its last result | did it exit as expected? | docs |
| `table` | a kind of record in a configured source | a table | nothing; it is a view | docs |
| `claim` | nothing in the repo | invisible badge | has this sentence passed its review date? | docs |
| `url` | an address outside the workspace | a normal link | is the page still there and still the same? | docs |

Read them as sentences: `ds:block` says "link to that function"; `ds:cfg` says "put the current number right here"; `ds:chain` says "show where this secret comes from and where it is copied"; `ds:url` says "an outside link, keep an eye on it". `cfg` and `chain` could in principle be modes of `block`; they are separate verbs so a sentence in a doc reads as what it means.

#### 9.1 `ds:def`

```go
// ds:def id=sess-save-k7m2p4xq owner=@auth stability=api
//   desc="dual-write guard, remove after task-120"
func (s *Store) SaveSession(ctx context.Context, sess Session) error {
```

```yaml
auth:
  port: 8081             # ds:def id=auth-port-h3v8n2wd
  session_ttl_days: 30   # ds:def id=sess-ttl-p2c4y7mk
```

```sql
-- ds:def id=sess-sweep-t4k2b9rf runnable=true
DELETE FROM sessions WHERE expires_at < now() - interval '30 days';
```

```markdown
<!-- ds:def id=sess-policy-h2n8wq4t -->
## Session policy
Sessions live thirty days and rotate on refresh.
```

```text
ds:def id=oncall-rota-m3k9v2pd span=+2
Week 37  khanakia
Week 38  someone
```

| Key | Required | Meaning |
|---|---|---|
| `id` | yes | the identity, section 8 |
| `owner` | | a person, or a team `@auth` resolved through `[owners]` in config; notified when a citing sentence goes unacked |
| `tags` | | comma list, filterable in reports and tables |
| `stability` | | which change classes flag prose, section 20: `frozen`, `stable` (default), `api`, `volatile` |
| `span` | | `+N` lines for grammarless files; `block` forces tree-sitter binding |
| `pick` | | how to extract one value or one range from the bound text, section 10; each host has a default |
| `type` | | `url email int float percent semver date duration host opref`; validates the picked value, enables `format=` on cites |
| `file` | | remote def: the target lives in another file, repo-relative; for formats that cannot hold a comment, and for sidecar mode |
| `local` | | `true`: the target exists only on some machines; elsewhere `unverifiable`, never `broken` |
| `env` | | environment this definition belongs to; the same id may be defined once per environment |
| `secret` | | `true`: `cfg` renders the name or address only, `block` refuses, value never stored |
| `source` | | provider of a secret when the address shape cannot tell: `env github 1password aws gcp vault file` |
| `from` | | id of the hop this value is copied from; builds a chain |
| `truth` | | `true` marks the single authoritative root of a chain |
| `sync` | | path of the script or job that copies this hop from its `from` |
| `runnable` | | `true` permits `ds:run` on this block |
| `deprecated` | | date; cites render a badge from then on |
| `sunset` | | date; after it any cite is an error |
| `desc` | | one line for hovers and the reverse index |
| `doc` | | `path#anchor`, declares the home page from the defining side |

Rules: one `def` per block, one block per id. `def` is the only directive the tool writes into source, and only through `ds def`, `ds adopt`, and `ds rename` (which rewrites ids in place). Removing the line is, for every citing doc, deleting the block. There is deliberately **no `value=` key**: a value hidden in a comment is invisible to readers or duplicated beside a visible copy; the directive marks where the visible value is.

#### 9.2 `ds:block`

```markdown
The guard is [`SaveSession`](ds:block?id=sess-save-k7m2p4xq). It writes the legacy row first.

<!-- ds:block id=sess-save-k7m2p4xq lines=1-6 title="the guard" -->

<!-- ds:block id=sess-save-k7m2p4xq at=9f3a1c -->
```

As a link: a citation, rendered as a permalink with hover preview; the sentence depends on the block. In block position: renders the block or a fragment at build time. Same verb, the carrier decides the shape.

| Key | Required | Meaning |
|---|---|---|
| `id` | yes | the block |
| `lines` | | fragment `a-b` or `a`, relative to the block: digits only, counting from 1, `a <= b`; `check`, `render`, and `ds read --lines` share this grammar |
| `at` | | commit sha; a snapshot, frozen on purpose, never refreshed, rendered with an "as of" badge |
| `branch` | | the block as published from a named branch, for release docs |
| `translates` | | `true` on a paragraph in a translated page that follows a defined source paragraph |
| `assert` | | `true` when the block is a test: the sentence also flags if that test is deleted, skipped, or failed in the last published CI run |
| `title`, `strip=comments`, `collapse=true`, `lang` | | rendering |
| `label` | | sub-claim name, only to make ack messages readable |

Copies: **the repository never holds a copy of a block.** Rendering happens at build from live source. Rendered blocks above `include.max_lines` (default 40) warn "too large, cite it instead." Snapshots are the one deliberate copy and any size is allowed. `include.mode = repo` is an opt-in for teams whose docs are read raw on GitHub: `ds refresh` writes a fence between `<!-- ds:block … -->` and `<!-- /ds:block -->` and `check` reports hand edits as `tampered` and drift as `stale`. Nobody types a fence by hand in either mode.

Why the link is generated: a hand-written `#L40-L58` points at the wrong lines after any edit above it, a commit permalink points at old code forever, a file link cannot name a function, and none of them can report that the code behind them changed.

#### 9.3 `ds:cfg`

```markdown
Auth listens on [8081](ds:cfg?id=auth-port-h3v8n2wd) in every environment.
Deploy [this version](ds:cfg?id=api-version-c8t2m6qp&format=code) to [prod](ds:cfg?id=app-host-d4k8w2mn&format=host).
We serve [1.2M](ds:cfg?query="sql:select count(*) from users"&ttl=24h&format=compact) users.
```

| Key | Meaning |
|---|---|
| `id` | a def whose pick yields one line |
| `query` | instead of `id`: one value from a configured source, `sql:` or `http:`; rendered with an "as of" timestamp |
| `ttl` | reuse window for `query`; default from config |
| `env` | which environment's definition; default from config |
| `format` | `raw` (default) `code` `quote` `host` `link` `compact` |

Link form only. The link text is the last known value so raw markdown reads; the build replaces it. A pick that yields more than one line is refused with "use ds:block".

#### 9.4 `ds:run`

```markdown
<!-- ds:run id=sess-sweep-t4k2b9rf env=staging expect=rows -->
<!-- ds:run cmd="task test" expect=ok -->
<!-- ds:run file=scripts/smoke.sh expect=ok timeout=60s -->
```

| Key | Meaning |
|---|---|
| exactly one of `id` `cmd` `file` | what to run; `id` requires `runnable=true` on the def |
| `expect` | `ok` (exit zero), `rows`, an HTTP status, or a quoted substring |
| `env`, `timeout`, `show=output\|command\|both\|none` | |

Rendered as the command plus its last result and timestamp. Executed only with `--run`, only where enabled, never on pull requests from forks, and `cmd=` and `file=` only in docs matching `run.allow`; `file=` must name a regular file inside the repository (relative, no `..`, no symlink) and reaches the shell as one quoted word. This is code execution from a text file and is treated as such.

#### 9.5 `ds:table`

```markdown
<!-- ds:table kind=task where="service=svc-auth and state!=done" cols=title,due,owner sort=due -->
```

| Key | Meaning |
|---|---|
| `kind` | a record kind the configured record source knows |
| `where` `cols` `sort` `limit` `empty` | the query and the empty-state text |

Needs no def; needs a record source in `[records]`. The first source is a directory of markdown files with frontmatter; SQLite and HTTP are adapters. With no source registered the verb warns and renders nothing.

#### 9.6 `ds:claim`

```markdown
We chose Postgres over Redis because ops already runs Postgres. <!-- ds:claim owner=@platform reviewed=2026-09-06 expires=90d -->
```

| Key | Meaning |
|---|---|
| `owner` `reviewed` `expires` | lifecycle; `ds ack` sets `reviewed` to today |
| `about` | comma list of ids; a change to any also flags the claim |

For sentences you want nagged. Design prose without a claim is left alone.

#### 9.7 `ds:url`

```markdown
See the [TOAST docs](ds:url?href=https://www.postgresql.org/docs/current/storage-toast.html&title="TOAST").
```

| Key | Meaning |
|---|---|
| `href` | the external URL |
| `title` | expected page title or substring; a change means moved or rewritten |
| `expect` | HTTP status, default 200; redirects followed and the final URL recorded |

Checked with `--resolve`, cached per `url.ttl`, rate limited. Findings `dead`, `moved`, `retitled`.

#### 9.8 `ds:chain`

```markdown
Stripe credentials: <!-- ds:chain id=app-stripe-key-m4w8k2qn -->
```

Renders the declared `from=` path of any value from its `truth=true` root, hop by hop, with the sync job for each copy. Section 12 explains chains.

#### 9.9 Adding a verb

A verb is a handler: name, allowed carriers, required and known keys, `render`, `check`. Projects register verbs in config or as executables named `ds-<verb>` on `PATH`. The scanner, the ledger, and the file formats do not change.

### 10. Extractors and `pick`

For every file the scanner uses the strongest extractor available and falls back to text. Any file works; known formats work better.

| Tier | Files | Boundary and value | Precision |
|---|---|---|---|
| structured | yaml toml json ini env csv properties hcl | a real parser; the def binds to a key path, the value is that key's value | exact |
| syntax | any tree-sitter grammar | declaration boundaries; a value only when the block is a single literal | exact for blocks |
| document | markdown mdx html asciidoc rst | sections, paragraphs, link text, elements | exact |
| text | plain text (`.txt`, `.text`), and reading any file no other tier claims | bare `ds:def` line, `span`, blank-line boundary | good enough, universal |
| asset | images, diagrams, PDFs, generated files | remote def with `pick=file`; whole file hashed | whole-file only |

**A directive is only ever written where it will bind.** Before any writer returns its edit, the edit is applied in memory and the result extracted with the same tier a scan will use. If the new id does not bind cleanly the write is refused with that tier's reason, so `--dry-run` predicts the refusal and no file is touched. The line matcher that resolves `path#symbol` and the grammar tier that scans a file can disagree, and before this check a directive was written and only the next scan said it bound nothing.

**A directive is only ever written as a comment the file's own language accepts.** Every file type docsync will write a directive into has an entry in the carrier table that says how: a line prefix (`//`, `#`, `--`), a block comment, or — for plain text only — a line of its own. A type with no entry is refused with `no comment carrier for this file type`, naming the type and the way round it (a remote def from a file that can carry one), and nothing is written. This holds for every writer: `ds def`, `ds adopt`, and anything added later go through one function. The reason it is a refusal and not a fallback is that the fallback shipped: an unrecognised type used to receive the directive as a bare line, which is invalid syntax wherever syntax exists. A bare line in a `go.work` stopped every build in that workspace with "unknown directive", and the same call on a `.json` would have left the file unparseable — JSON has no comment syntax at all, so there is nothing to fall back to. `go.mod`, `go.work` and `.pkl` take `//`.

The table is keyed by the file's base name before its extension, so a name that decides its syntax (`go.mod`, `taskfile.yml`, `dockerfile`) is matched as itself rather than as some `.mod` or `.yml`. A bare directive line found in a type that does carry comments, or in a format with no comment syntax at all (JSON, CSV, `go.sum`), is reported as `directive is not inside a comment`, with its file and line, so a tree that was damaged before the refusal existed can find the places to fix and `ds repair` can comment them. The code tiers read such a line too, rather than only reading comments, so damage in a Pkl or Go file is not invisible. The same line in plain text is correct and is not reported.

**A def binds the declaration below it, or reports.** Whatever the tier, the block a `ds:def` binds must begin on the first line below the directive that is not blank, a comment, or a decorator. A tier with a grammar walks forward looking for something it recognises, so a construct its tables miss would otherwise bind the *next* declaration it does recognise, arbitrarily far down, and the sentence citing the def would be measured against code its author never read. That is a finding, not a binding. Two ids that end up on the same lines are a finding for the same reason: both hash the same bytes, so a change flags both or neither, and the scanner cannot know which directive went astray — it names them all.

**Body members are declarations too.** The named members of a declaration's body are defs in their own right: a struct field, an interface method, an enum member, a TypeScript interface property or class field, a Python class attribute. A member binds itself and, where it has one, its own body — never the members below it and never the brace that closes the holder. Its symbol carries the holder, `Limits.MinLength`, which is how the language itself refers to it; an embedded field or interface is named by the type it embeds. An import is a member of its group and is asked for by its path with the quotes removed, or by its alias where it has one, because a doc naming a dependency names it the way a reader greps for it. A tier with no grammar binds each member to its own line but does not synthesise a name for it, since the leading identifier is the name in Go and TypeScript and a modifier in the C family — so `path#Name` finds members by comparing against the name the caller asked for, which can be right or absent but never wrong.

**Group entries are declarations.** `const ( … )`, `var ( … )` and `type ( … )` hold one declaration per entry. A directive above an entry binds that entry and nothing below it; a directive above the group's own opening line binds the whole group; and `path#Name` resolves an entry by name. Whether a line like `MaxLength = 256` declares anything is not decidable from the line alone — inside a group it declares a constant, inside a function body the same text assigns to a variable — so a tier without a grammar reads the lines above it to tell the two apart.

`pick` is one vocabulary across tiers:

| `pick=` | Tier |
|---|---|
| `yaml:a.b.c` `toml:a.b` `json:$.a.b` `ini:section.key` `env:NAME` `csv:col=name` `csv:r2c3` `hcl:resource.name.attr` | structured |
| `symbol:Store.SaveSession` | syntax |
| `section:"Session policy"` `heading` `paragraph:2` `link:1` | document |
| `line:N` `regex:'…'` (first group) `url` `after:'…'` `between:'a','b'` | any |
| `file` | asset |

Defaults make `pick` rare: a yaml line picks its key's value, a markdown link picks its text, a code symbol picks its block, a bare-text def picks to the next blank line.

**Key paths.** A YAML key ends at a colon followed by whitespace or the end of the line, so a colon anywhere else is part of the key: `tasks.wfsys:up` addresses the Taskfile task `wfsys:up` with no quoting. A segment that contains a dot is quoted, `tasks."a.b".desc`, and quoting any segment is always allowed. Outside YAML the first colon still ends a key, since a Java properties file writes `key:value` with no space. Paths are compared segment by segment, never as joined strings, which is what makes a key holding a dot reachable at all.

**Object literals.** A property of an object literal binds, named by the keys that lead to it: `server.port` for `server: { port: 5173 }`. Configuration in the JavaScript ecosystem is an object, and a bare `port` is ambiguous in any config with more than one server. A computed key (`[k]: 1`) binds but has no name.

**The rule that keeps `pick` small:** it returns exactly one line (a value) or one contiguous range (a block). No transforms, arithmetic, or joins. Display formatting is `format=` on the cite, from a fixed list.

**Remote defs.** JSON, plain CSV, and lock files cannot hold a comment. The def lives in any file that can and points with `file=`:

```markdown
<!-- ds:def id=api-port-h3v8n2wd file=config/app.json pick=json:$.server.port type=int -->
```

Remote defs hash the extracted value, report `pick failed` when the key disappears, and do not follow a moved target. This is also sidecar mode for teams that will not put directives in source, with that same known weakness.

**Custom extractors** are executables `ds-pick-<format>` that receive a file and a pick expression and print one value or one range.

**Normalization before hashing:** CRLF to LF, leading BOMs stripped, trailing whitespace removed. CRLF is read as LF before a `pick=` is evaluated too, including the raw target of a `file=` def, so a Windows checkout with `core.autocrlf` resolves every def and hash exactly as an LF checkout of the same commit does. Leading byte order marks are dropped when a file is read, before any parsing, not only before hashing: a mark left in front of line 1 would glue itself to the first key's symbol, hide a directive or front matter on line 1, and make a file saved by a Windows editor read as a different file. A source write puts the marks back where they were. Files above `scan.limits.max_file_kb`, binary files, and — in tiers that are not prose — files with a line longer than `scan.limits.max_line_chars` are not scanned; the line limit is a sign of minified code, and a paragraph written on one line is not that, so markdown, AsciiDoc, rst and plain text are exempt. `ds scan` names every such file on stderr, a file that held citations or blocks keeps its last recorded state, and `check` reports it as `unscanned` until it can be read again. Dropping it instead lost its citations' first-seen hashes, so when it came back an unreviewed change counted as new and passed.

### 11. Facts

A fact is a def whose text is one line. The rule that keeps it honest: **the value is visible text at the def's location, never an attribute of the directive.** One copy, seen by the reader and read by the tool.

Preferred form in markdown, the inline def, where the link text is the value and can be anything:

```markdown
The API runs on port [8081](ds:def?id=api-port-h3v8n2wd&type=int) and ships as version [2.14.0](ds:def?id=api-version-c8t2m6qp&type=semver).
Sessions expire after [30 days](ds:def?id=sess-ttl-p2c4y7mk&type=duration). On-call lead is [Aman](ds:def?id=oncall-lead-r9k1w5zb).
The app is hosted at [https://example.com](ds:def?id=app-host-d4k8w2mn&type=url).
```

Rendered, each is plain text; only `type=url` becomes a link; the `ds:def` target disappears. The first place a fact is written becomes its home; every other page cites it with `ds:cfg`. Other homes: a standalone line under a comment def, a prose paragraph with `pick=url`, a config key. A facts page is a markdown list of inline defs. `ds facts` prints every one with its current value and who cites it.

A fact with no file the tool can see, an environment variable set in a dashboard, a number in someone's head, gets a home: one line in a facts page. The tool cannot guard what it cannot read.

### 12. Secrets, chains, and environments

Secrets reach the repo as **addresses**: `${{ secrets.STRIPE_KEY }}`, `op://Platform/stripe-prod/credential`, an ARN, a Vault path, an env var name in code. The tool works on addresses and never stores, renders, or logs a value.

That promise is enforced at the two places the tool would otherwise persist text, not left to each caller:

- **The body store** (section 15.1) takes no body from a block that is `secret=true` or `local=true`, whether by directive or by a `[secret] paths` match.
- **The extraction cache** is never given a file that a secret glob matches, or that holds a def declaring `secret=` or `local=`. The guard sits in the scanner at the single point where anything is handed to a `Cache`, so a replacement cache implementation cannot skip it; such a file is re-extracted on every scan instead.

The remaining copy of a secret line outside its own source file is `.ds/journal.tsv`, which `undo` needs and which is machine-local and never committed (section 6).

**Nothing renders a value either.** A secret def's content is kept only when it is an address — a `${{ secrets.X }}` expression, an `op://` reference, an AWS Secrets Manager ARN, a GCP Secret Manager resource, a `vault:` path — or when the def declares itself secret and names a `source=`, as a bare environment variable name needs. Anything else is blanked by the scanner, with its hash kept, so a changed value is still reported and every command, the MCP tools and the change diff all have nothing to show. An address is judged before any `pick` as well as after, so `STRIPE_KEY` picked out of `${{ secrets.STRIPE_KEY }}` still renders. In a `[secret] paths` file only an address shape is trusted, because such a file holds values by definition. A bare uppercase name is not treated as an address, since an AWS access key id looks like one. The change diff is withheld for every secret and local block regardless, since a def that became secret since its last scan has a clean previous row and a current content that is the value.

**Addresses are facts.**

```yaml
# .github/workflows/deploy.yml
STRIPE_KEY: ${{ secrets.STRIPE_KEY }}   # ds:def id=gh-stripe-key-r4t6x2mb secret=true pick=regex:'secrets\.(\w+)'
```

```bash
# .env.tpl   (1Password convention; op inject produces .env)
STRIPE_KEY=op://Platform/stripe-prod/credential   # ds:def id=op-stripe-key-p9c2v7ld secret=true truth=true env=prod
```

```go
// ds:def id=app-stripe-key-m4w8k2qn secret=true source=env from=gh-stripe-key-r4t6x2mb
key := os.Getenv("STRIPE_KEY")
```

Their values are the strings `STRIPE_KEY` and `op://…`: names, safe to hash and render. A runbook cites them and is flagged when the workflow renames the variable or the template drops the line, with no access to any provider.

**Provider is declared, not guessed.** Address shape identifies it when possible: `${{ secrets.X }}` GitHub, `op://` 1Password, `arn:aws:secretsmanager:` AWS, `projects/…/secrets/` GCP, `vault:` Vault. A bare env var name says nothing; write `source=`.

**Chains.** Most teams own a secret in one place and copy it elsewhere. `truth=true` marks the single root, `from=` links each copy to its origin, `sync=` names the copying job.

```yaml
STRIPE_KEY: ${{ secrets.STRIPE_KEY }}   # ds:def id=gh-stripe-key-r4t6x2mb secret=true from=op-stripe-key-p9c2v7ld sync=scripts/sync-secrets.sh
```

```
$ ds why app-stripe-key-m4w8k2qn --chain
app-stripe-key-m4w8k2qn    env STRIPE_KEY                                 internal/pay/stripe.go:12
  from gh-stripe-key-r4t6x2mb    github secret STRIPE_KEY               .github/workflows/deploy.yml:9   synced by scripts/sync-secrets.sh
    from op-stripe-key-p9c2v7ld  1password op://Platform/stripe-prod/credential   .env.tpl:3   TRUTH
```

Checked from files alone, every run: exactly one `truth` per chain; every copy has a `from=` or it is `unsourced`; names agree hop to hop within an environment or the chain is `broken` at that hop.

Checked with `--resolve` on a logged-in machine: provider plugins confirm each address exists. GitHub lists names only, so those hops are existence-only. Where a provider can be read, truth and copy are hashed in memory and compared; different is `out of sync`; with `resolve.store_hash` the truth's hash is kept so a later run can say `rotated`. Off by default, never on fork PRs. Rotation makes downstream copies `stale copy` until the sync runs, and every runbook citing the chain gets one `unacked` line.

**A truth on one laptop.** A `.env.prod` never in git can still be the declared truth: a remote def with `local=true`. CI reports `unverifiable`, the chain rendering says the truth lives on a machine, and most teams then move it to a vault.

**Environments.** `env=` on a def and on a cite. Same id, one def per environment; a cite without `env=` gets the workspace default; `check` runs per environment and says which one is unsourced. Changes are tracked per environment too: each (id, env) is its own def from scan to scan, so renaming one environment's file reports no change, a change to prod is not read against dev, a claim `about=` an id expires when any of its environments changes, and `export hugo` writes the def the default environment resolves to.

```bash
STRIPE_KEY=op://Platform/stripe-prod/credential      # ds:def id=op-stripe-key-p9c2v7ld env=prod secret=true truth=true
STRIPE_KEY=op://Platform/stripe-staging/credential   # ds:def id=op-stripe-key-p9c2v7ld env=staging secret=true truth=true
```

### 13. Ownership of pages

```markdown
---
title: Sessions
ds:
  covers: [sess-save-k7m2p4xq, sess-sweep-t4k2b9rf]
  review_every: 180d
---
```

`covers` makes a page the home of ids, enabling coverage and orphan reports and the reverse index in the editor; it is optional. `review_every` schedules a re-review of acked prose even when nothing cited has changed. `ds:def doc=` is the same declaration from the defining side.

### 14. Directive cheat sheet

| Want | Write |
|---|---|
| give code an identity | `// ds:def id=name-xxxxxxxx` |
| give a config line an identity | `key: v   # ds:def id=name-xxxxxxxx` |
| define a fact in prose | `[8081](ds:def?id=name-xxxxxxxx&type=int)` |
| cite a block | `[text](ds:block?id=…)` |
| show a block | `<!-- ds:block id=… lines=1-6 -->` |
| freeze a block | `<!-- ds:block id=… at=sha -->` |
| show a fact | `[8081](ds:cfg?id=…)` |
| a live number | `[1.2M](ds:cfg?query="sql:…"&ttl=24h)` |
| bind a sentence to a test | `[test](ds:block?id=…&assert=true)` |
| a translated paragraph | `<!-- ds:block id=… translates=true -->` |
| run something | `<!-- ds:run id=… expect=ok -->` |
| a table over records | `<!-- ds:table kind=task where="…" -->` |
| a sentence that must age out | `<!-- ds:claim owner=@t reviewed=date expires=90d -->` |
| an external link | `[text](ds:url?href=…)` |
| a secret's path | `<!-- ds:chain id=… -->` |
| define from another file | `<!-- ds:def id=… file=path pick=json:$.a.b -->` |

---

## Part IV — How checking works

### 15. The ledger and the reverse index

Generated by `ds scan`, committed so the previous state exists to diff against, never edited by hand. Sorted, one row per id or per reference, so a move is a one-line diff. Each file carries a header with `format=`, the repo name, and the commit it was scanned at.

```
# .ds/ledger.tsv
id                     repo   kind     file                        symbol             lines  hash    owner   stability  env
sess-save-k7m2p4xq     api    func     internal/store/session.go   Store.SaveSession  5-13   9f3a1c  @auth   api        -
sess-sweep-t4k2b9rf    api    stmt     db/sweep.sql                -                  1-1    b02e77  -       stable     -
auth-port-h3v8n2wd     api    line     config/auth.yaml            auth.port          3-3    4d1f09  -       stable     -
sess-policy-h2n8wq4t   docs   section  docs/policy.md              Session policy     12-19  e17c40  -       stable     -
op-stripe-key-p9c2v7ld api    line     .env.tpl                    STRIPE_KEY         3-3    77a0c1  @auth   stable     prod
```

```
# .ds/refs.tsv
id                     repo   doc                      line  verb   carrier  acked_hash  seen_hash  sentence_hash
sess-save-k7m2p4xq     docs   docs/sessions.md         15    block  inline   9f3a1c      9f3a1c     1c0e…
sess-save-k7m2p4xq     docs   docs/sessions.md         17    block  block    9f3a1c      9f3a1c     -
auth-port-h3v8n2wd     infra  runbooks/latency.md      22    cfg    inline   4d1f09      4d1f09     88f2…
sess-policy-h2n8wq4t   api    internal/store/write.go  3     block  comment  -           e17c40     -
```

`hash` is sha256 of the normalized block or extracted value with the directive lines removed. "The directive lines" means every docsync carrier, not only the block's own: a section runs to the next heading and `ds def` writes its carrier on the line above the heading it anchors, so a block's extent ends before any trailing run of blank lines and standalone carriers. A carrier can also sit *inside* a block — anchoring `### Details` writes its carrier inside the `## Overview` section that contains it, and a YAML child key's carrier sits inside its parent map — so every standalone carrier within an extent is left out of the hash too, and `span=+N` counts N content lines, skipping carriers, so an anchor added inside a span does not push its last line out. Without these rules, anchoring one heading changed the hash of every section above or around it and flagged every sentence citing them although none of their text had changed. `Content` keeps the carriers, because it is what renders; only the hash leaves them out. A directive is metadata about text, never text. A *trailing* directive is exempt, because it shares its line with real content. `acked_hash` is the block hash a citation was last approved against. `seen_hash` is the block hash the first scan that recorded the citation saw; it is written once and never revised, so a citation nobody has acked still has something to be measured against. `sentence_hash` is the hash of the enclosing sentence (section 18). It is what an ack is held to — a sentence rewritten since its ack is unacked — and how a citation keeps its baselines when it moves — a line inserted above it, its doc renamed — which scan and check decide in one place: a citation is paired with the previous one of the same id and env that has the same sentence, then, where exactly one of that id remains in a doc, with that one. Where the pairing is ambiguous, every candidate takes the baseline that still reports a change, because carrying an older baseline can only add an ack to do while pairing two citations wrongly can hide an unreviewed change. Looking baselines up by exact line instead treated every moved citation as new and baselined it at the block's current hash, which silently accepted whatever the block now said.

<!-- dsself:block id=rule-7y7umdv9 -->

**Which rules produced a hash.** A hash is only comparable with another computed under the same extraction rules, and nothing in a hash says which rules those were. So the rule version is recorded beside them: `extract=` in every header, and on every ack row. Rule 1 is the first released set (`extract.Rule`); a file with no `extract=` was written before the field existed and is read as rule 1. Any change to what bytes a block's hash covers, or to what prose a citation binds to, bumps it by one. The extraction cache is keyed on the rule too, so the first scan after an upgrade cannot be served stale extents and have the change surface later, detached from its cause. There is no automatic re-baselining: a repo whose stored hashes were taken under an earlier rule sees the affected citations as drift and acks them once, with a note saying the boundary moved, and an ack made under another rule reports once as recorded under that rule (section 18) rather than as a rewrite.

`refs.tsv` is `format=2`; `format=1`, which has no `seen_hash`, is still read and is upgraded by the next scan, which fills the missing baseline from the block's current hash. A repo upgrading from `format=1` therefore starts measuring its never-acked citations from the moment it upgrades; acked citations are unaffected, because their baseline is the ack.

#### 15.1 The body store

`hash` identifies a block but does not describe it, and answering "what did this block say when the sentence was acked" needs the text. That text can be arbitrarily far back — many scans in this repo, or in another repository whose published ledger carries hashes and no bodies — so neither the previous ledger nor the VCS at the ledger's commit can supply it.

Bodies are therefore kept content-addressed, one file per hash:

```
.ds/blocks/<hash>                  written by `scan`, committed
repos/<name>/blocks/<hash>         written by `publish`, in the workspace index
```

- Content-addressed, so a block that did not change writes nothing and history accumulates without duplication.
- **Secret and local blocks are never stored.** A block under `[secret] paths`, one carrying `secret=true`, and one carrying `local=true` contribute no body to either store. The index may be readable by people who cannot read the source repo, and a body written there cannot be recalled. This is enforced where the bodies are computed, not at each call site.
- A missing body is not an error. It degrades classification to `unknown` (section 20), which still flags. That is also what bounds the cost of an over-eager prune: the worst case is a finding with no diff, never a wrong `ok`.

**Liveness.** A body `h` is live when `h` appears in the current `ledger.tsv`, in any `acked_hash` or `seen_hash` in `refs.tsv`, in the latest ack per key in `acks.tsv`, or in `foreign.tsv`. For the index store it is the union of those across every published repo, because one repo's ack can name a body only the defining repo's directory holds. Everything else is dead, and `ds prune` removes the complement of the live set — never a list of candidates it assembled some other way.

`prune` refuses outright when any file the live set depends on cannot be read: a partial view would classify live bodies as dead. Dead bodies younger than `--keep` (30 days by default) are kept anyway, because a repo that has not synced lately may be about to ack against one. `--index` additionally requires the default branch and a fresh fetch, since pruning the shared store from a stale view of who cites what is precisely how a live body is lost. `ds prune --hash <h> --force` removes one body regardless of liveness — the only route out for a value that was secret before anyone marked it — and records the removal in the ack log. `ds doctor` reports the store's size and how much of it is reachable.

### 16. The passes of `ds check`

| Pass | Does |
|---|---|
| 0 sync | fetch the workspace index; merge every published ledger and refs; skipped for a workspace of one |
| 1 scan definitions | walk configured paths; extract directives per carrier; for `def` resolve the boundary and record kind, symbol, lines, hash, keys |
| 2 scan references | same walk; `block cfg run table claim url chain` and frontmatter `covers`; record file, line, verb, carrier, keys |
| 3 match definitions | against the merged previous ledger, per id: same hash and place `ok`; same hash new place `moved`; new hash `changed` with a class (section 20); id gone but hash found elsewhere `moved-unmarked`; id gone and diff ratio ≥ 0.8 `rewritten?`; nothing `deleted`; chains validated; picks re-run |
| 4 evaluate references | each reference becomes a finding, section 17. Coverage is decided against the reference's own baseline — its `acked_hash`, or its `seen_hash` when nothing has acked it — never against the scan-to-scan change table, which answers a different question and is absent whenever the ack is not from the previous scan |
| 5 report | grouped by doc then line; `--json` for machines; exit code from severities |

<!-- dsself:block id=cacheinputs-vyhx6vbz -->

Incremental by default: a file whose bytes did not change is not re-extracted, and a file whose size and modification time match those the cache recorded for it is not even read, as git trusts its index; `--full` reads and extracts every file. A time within two seconds of when it was recorded is not trusted, because a filesystem clock that ticks in whole seconds gives two writes in one tick the same time. The one change the shortcut cannot see is content rewritten with its size and time put back, as `cp -p` or `touch -r` can do; `check --full` sees it. A cache entry is valid only for the inputs that produced it, so the cache file records them and is discarded wholesale when they differ: the extraction rule version, the directive prefix, the extractor tiers in precedence order, and `max_line_chars`, since a file served by its time skips the long-line test that admitted it. The rule is part of the key rather than a format number someone must remember to bump, which is the convention that let the extent fix silently not apply. Without that, editing `prefix` served entries extracted under the old one and reported defs and citations that existed nowhere in the tree.

### 17. Findings

<!-- dsself:block id=statevalues-vwvk2kq3 -->

| Finding | Meaning | Severity | Cleared by |
|---|---|---|---|
| `ok` | reference resolves, hash equals its baseline (`acked_hash`, else `seen_hash`) | none | |
| `moved` | same hash, new file or lines | none, ledger updated | automatic |
| `unacked` | the block changed since the sentence was acked; carries class and diff | error (config may downgrade) | `ds ack`, or edit then ack |
| `broken` | id not found, no hash match, or `at=` commit missing, or repo removed from workspace | error | fix the reference or the def |
| `pick failed` | the def's extractor yields nothing, or many lines where one is required | error | fix the def |
| `too-large` | a rendered block over the line cap | error | `lines=` or cite |
| `range` | a fragment beyond the block's current length | warning | fix `lines=` |
| `stale` / `tampered` | repo-mode fence differs from source / was hand-edited | error | `ds refresh` |
| `expired` | a claim or a page past `expires` or `review_every` | error | review, then `ds ack --doc D --line N` renews a claim and `ds ack --doc D` records a page review |
| `sunset` | a cite of a def past its `sunset` date | error | remove the cite |
| `deprecated` | a cite of a def past its `deprecated` date | info, badge | |
| `assert failed` | cited test failed, was skipped, or was removed in the last published run | error | fix the test or the sentence |
| `translation stale` | source paragraph changed since the translation was acked | error | update and ack |
| `unsourced` | a secret copy with no `from=` | warning | declare the chain |
| `chain broken` | names disagree between hops, or zero or many truths | error | fix the chain |
| `out of sync` / `rotated` | with `--resolve`, copy differs from truth / truth changed since stored hash | error / warning | run the sync; ack runbooks |
| `resolve failed` | with `--resolve`, an address does not exist at its provider | error | fix the address |
| `unverifiable` | a `local=true` def absent here, or a provider not logged in, or no network | warning | none required |
| `dead` / `moved` / `retitled` | `ds:url` outcomes | error / warning / warning | update the link |
| `orphan` / `uncovered` | `covers` names a missing id / a def nobody cites | warning / info | tidy |
| `undocumented export` | policy: an exported symbol under a required path has no def with a home | error | add a def and a page |
| `unknown` | unknown verb or key | warning (`--strict`: error) | |
| `undocumented` | an exported declaration under a `policy.require_doc` path with no `def` (section 23) | error | `ds def <file>#<symbol>` and cite it |
| `unscanned` | a file that held citations or blocks at the last scan could not be read this time — too large, a line past the limit in a tier that is not prose, binary, or unreadable. Its last recorded state is kept, so an unreviewed change it carried is still reported once it is readable | error | make the file readable, or raise the `[scan.limits]` value the finding names |

Exit code 1 on any error-severity finding. `--strict` promotes warnings to errors, except `moved` and `deprecated`, which never affect the exit code.

### 18. What a reference binds to

A reference in prose binds to its enclosing **sentence**, read across its whole paragraph: in markdown a paragraph ends at a blank line, a heading, a list item, a table row, a fence or indented code, a whole-line comment, a thematic break, or a change into or out of a blockquote, and a line break inside it is a space, so a sentence wrapped across lines is one sentence. `.`, `!`, or `?` ends a sentence only when whitespace follows and then an uppercase letter, a digit, a quote or backtick, or an opening bracket, or the paragraph ends — so `v1.2 and` and `Fig. 3` do not split — and never after `e.g. i.e. etc. vs. cf. viz. Fig. No. Dr. Mr. Mrs. Ms. St. U.S. U.K.` or inside a run of three or more dots. A terminator inside a link or an inline code span is not a boundary, so `[e.g. Save](…)` stays in one sentence with the words around it; a list item's `[x]` is a task checkbox only when whitespace follows it. Inside a list item or table cell it binds to the whole item or cell. HTML comments are not prose, and runs of whitespace collapse to one space, so re-wrapping, re-spacing, or editing a trailing directive is never rewriting. Two references in one sentence share one ack.

The ack records id, block hash, doc, the sentence's hash and text, and the extraction rule it was made under. A paragraph that moves keeps its acks, including after a scan has recorded its new line and the ack row still names the old one: the ack is found by the acked hash `refs.tsv` carried, and where several citations of the block were acked at that hash, the citation stands approved if any of them approved its current sentence; **a rewritten sentence needs a new one**: with its block unchanged, a citation whose sentence hash differs from its ack's is `unacked` — `sentence rewritten since the ack`, with the old and new wording as the diff. An ack made under another extraction rule bound its sentence differently and cannot say whether the wording changed, so its citation is `unacked` once as `ack was recorded under sentence rule old, not new`, never as a rewrite, until it is acked again; after an upgrade that bumps the rule, every acked citation reports this way once, and `ds ack --all --dry-run` per doc shows what re-acking them approves. A block-position citation has no sentence and is held to its block alone. `[check] sentence = "position"` opts out: an ack then holds by position, whatever the sentence there now says.

### 19. Acks

`ds ack <id>… [--doc path] [--group N] [--note text] [--all]`

- Default scope is the doc line named; `--all` widens on purpose. With no id, `--doc D --line N` renews the claim on that line and `--doc D` alone records that the whole page was reread, for `review_every`; a page review is an ack row with no id, line, or hash, and is refused for a page that declares no `review_every`.
- An ack is an append-only event: actor, actor kind (human or agent, with `delegated_by` for agents), time, id, environment where the def is per environment, block hash, sentence hash, note. `ds audit` exports the log.
- Acks are keyed by hash, so reverting a commit restores an already-acked hash and the finding disappears with no action.
- `ds triage` groups `unacked` findings by diff similarity so a mechanical change across many blocks is one decision with one note.
- A commit message containing `ds:ack id=…` (with the configured prefix, like every directive) records the ack with the change, so the developer who changed the code confirms the doc in the same PR.
- `ds review --ai` proposes prose edits as a patch and never records an ack. A person, or an agent explicitly delegated, does.

### 20. Change classification and stability

Where a grammar exists, a changed block is classified so a finding reads like a sentence and a policy can act on it.

| Class | Example |
|---|---|
| `renamed` | `Store.SaveSession → Store.Persist` |
| `moved` | `session.go:5 → write.go:12` |
| `signature` | `parameter env string added`; `return type error → (Token, error)` |
| `type` | field added on a struct; enum value added |
| `body` | statements changed, signature unchanged |
| `comment` | comment lines only |
| `whitespace` | formatting only; never reported: trailing whitespace and line endings in every tier, and re-indentation where the syntax tier hashes the token stream (section 33 **What changed**); in a file with no grammar, indentation is content, because in YAML or Python it is meaning |
| `value` | for facts: `8081 → 8443` |
| `unknown` | the versions differ but the older body was not available to classify |

`unknown` is not a guess. It is reported when a citation drifted from a hash whose body the body store cannot supply — pruned, never stored, or withheld because the block is secret — and it must never be recorded as `body`, because `api` does not flag on `body` and a silently dropped signature change is a wrong `ok`.

`stability` on the def decides which classes flag prose:

| `stability` | Flags on |
|---|---|
| `frozen` | everything except whitespace; any touch is an error on its own |
| `stable` (default) | everything except whitespace and comment |
| `api` | `signature`, `type`, `renamed`, `value`, `unknown` |
| `volatile` | nothing |

`unknown` flags wherever `api` flags, deliberately wider than `body`: an unclassified difference could be a signature change, and reporting one the reader judges irrelevant costs an ack, while staying silent costs a missed breaking change. Only `volatile`, which opts out of prose flagging entirely, stays quiet.

Files without a grammar produce `body` or `value` only. Every finding carries the class and the diff.

### 21. Workspaces

A workspace is a set of repos whose blocks and docs refer to each other freely: a central docs repo plus ten services, or a single repo.

```toml
# ds-workspace.toml, in the docs repo or a small ds-index repo
[workspace]
name  = "platform"
repos = ["github.com/org/api", "github.com/org/web", "github.com/org/infra", "github.com/org/docs"]
index = "github.com/org/ds-index"     # git repo; an https endpoint is an adapter
[workspace.id]
suffix_length = 8
[workspace.env]
default = "prod"
known = ["prod", "staging", "dev"]
```

- Ledgers flow **from** every repo that defines; refs flow **back** to every repo that is referred to. Both are two small files. Block bodies flow with the ledger, content-addressed under `repos/<name>/blocks/` (section 15.1), so a consumer can classify a citation whose baseline predates the published ledger; secret and local blocks publish a hash and no body.
- A cross-repo citation is checked against its own baseline exactly as a local one is. The defining repo is not matched by the consumer's scan, so the consumer's change table says nothing about it; the `acked_hash`, or the `seen_hash` recorded when the citation was first scanned, is what decides coverage.
- `ds publish` runs **only on the default branch after merge** and writes the repo's ledger, refs, commit, and optionally test outcomes (`--tests junit.xml`) into the index under `repos/<name>/`. Pull requests never publish; a PR's `check` compares its local scan against the last published ledger plus its own diff. The index records what shipped.
- `ds check` runs `ds sync` first: fetch the index, merge into one id table and one reverse index. Ids are globally unique, so no `repo/` prefix appears in directives; the merged table records the owning repo.
- Cross-repo references are ordinary references. The docs repo cites `sess-save-k7m2p4xq`; the permalink resolves to the api repo at its published commit; the api repo's `check` sees from merged refs that a docs page depends on the block it is about to delete.
- Duplicate ids across repos are rejected at publish. Forks and mirrors cannot publish; only the canonical URL may: `publish` refuses a remote that is not in `workspace.repos`, comparing URLs in one canonical form (`git@github.com:org/api.git` is `https://github.com/org/api`), and without a remote requires the directory name to be a listed repository's. Each repo writes only its own directory, so concurrent publishes cannot collide. That needs each repository's directory name — the last segment of its URL — to be distinct, so a workspace listing two repos with the same last segment (`github.com/org/api`, `gitlab.com/partner/api`), or a URL with none, is refused when it loads.
- A published ledger older than N commits behind its repo's default branch produces `index for api is 14 commits behind` in every consumer.
- Network unavailable: `check` uses the last synced copy kept in `.ds/index/`, warns about its age, never fails on the network alone. `--strict` may fail closed.
- A repo removed from the workspace makes references into it `broken: repo removed`, distinct from deleted code.
- A workspace of one repo uses itself as the index; `sync` is a no-op; same code path.

**Reproducibility.** Without a committed record of what it depends on, `check` in a citing repo answers differently for the same commit depending on what upstream last published and on when the index was last synced: CI goes red with no change in the repo under test, the failure cannot be bisected, and re-running an old commit does not give the answer it gave at the time. `.ds/foreign.tsv` is that record — one row per cited foreign block (`id, repo, commit, kind, file, symbol, lines, hash, owner, stability, env, args`), cited ids only so an unrelated upstream edit does not churn every downstream repo. A published branch is recorded only when a citation selects it with `branch=`, with that branch's commit, so publishing a branch upstream leaves every other repo's snapshot as it was; `sync` summarises changes per def (id, environment, branch).

- `ds sync` fetches the index, rewrites the snapshot, and prints what it recorded: `+` newly cited, `~` content changed, `>` moved with the same content, `-` no longer published. `ds check` never writes it.
- The summary compares content **and** position, because either rewrites the file: a block that moved upstream produces a real diff, and a sync that called that "unchanged" would describe a reviewable change as nothing. The file is rewritten on every sync regardless, because its header records when the pin was last confirmed and both `status` and `snapshot_max_age` read that — so the summary distinguishes "no change to record" from "upstream commits recorded; no cited block changed" rather than implying the file was left alone.
- `ds check --frozen` does not sync and resolves foreign blocks from the snapshot alone, so the same commit gives the same answer on any machine at any time, with no network. It is the default when `CI` is set; `--sync` opts back in, and asking for both is a usage error.
- With no snapshot, `--frozen` fails naming `ds sync`. It never degrades to "no foreign blocks", which would pass vacuously and report green for citations it never looked at.
- A citation a frozen run cannot resolve is `broken: cited but is not recorded in foreign.tsv`, not `broken: not defined`. The two are different problems: a new cross-repo citation reaches the first the moment CI runs before anyone has synced, and the id is perfectly well defined upstream. The remedy names both possibilities — sync and commit, or fix the id if no repo publishes it — because a frozen run genuinely cannot tell them apart without the index it is refusing to consult.
- The division of labour is a lockfile's: `sync` is the deliberate act of taking upstream's changes, it appears as a diff in a pull request, and that is where the resulting findings get reviewed — rather than landing on whoever pushes next.
- `ds status` reports how far the snapshot is behind: per upstream repo, how many cited blocks have moved and between which commits, and per block the old and new hash with the change class, so a reader can tell a signature change from a comment before deciding whether syncing is urgent. A cited id the publishing repo no longer defines is reported as gone, never as up to date: the next `ds sync` turns its citations `broken`, so hearing it beforehand is the point. It never changes the exit code — staleness is information, and the whole point of the pin is that time does not change the answer. Where the comparison cannot be made (no index, or a repo the index does not publish) it says so rather than reporting "up to date", which would be a claim nobody checked.
- `[check] snapshot_max_age`, e.g. `"30d"`, makes `check --frozen` print a **warning** when the snapshot is older. Off by default, and never more than a warning: a pinned check that also failed with age would depend on the clock, which is the one property `--frozen` exists to remove.
- A block that moved upstream with its content unchanged is reported as `moved`, against the snapshot's position — so "since" means since the last `ds sync`, not since the last commit. Under `--frozen` the snapshot is both sides of the comparison and nothing can move, which is correct rather than a gap.
- A frozen check classifies from the bodies the index already holds at `repos/<name>/blocks/<hash>` (section 15.1). A body that was never published, or was pruned, degrades that finding to `unknown`; `sync` warns how many are missing.

---

## Part V — Operating it

### 22. Command reference

Every command finds the repository root the way git finds `.git`: the nearest directory at or above where it was started that holds `.ds/config.toml`. A path argument (`render <doc>`, `blame <doc>`, `context <doc>`, `def <file>#…`, `--doc`, `--file`) is read from where the user is and converted to the repository-relative path every output uses, and a path that resolves outside the repository is refused; a file flag such as `--out` is likewise relative to where the user is. `--dir <path>` runs as if started in that directory, as `git -C` does, and discovers from there, so it may name a subdirectory. `init` makes a root where it is started rather than discovering one, and refuses to nest a second `.ds` inside an initialised repository unless `--force`.

| Command | Does |
|---|---|
| `ds init [--agents]` | writes `.ds/config.toml`, empty ledger, CI snippet; `--agents` also writes the agent rules fragment, registers `ds mcp`, and installs the session-start hook |
| `ds mcp` | serves the agent surface over MCP, section 26.1 |
| `ds map [--budget N] [--json]` | token-bounded table of contents of a repo or workspace |
| `ds find <query> \| --file path \| --tag t` | ids by symbol, text, file, or tag |
| `ds read <id> [--lines a-b]` | the body of a block |
| `ds locate <id>` | file and line range at the current commit |
| `ds doctor` | checks grammars, resolver logins, index reachability, globs that match nothing, and whether this tool can read the ledger and which extraction rule it records; exits non-zero when any row is `FAIL`, so a setup step that runs it stops on a broken repo, while a `WARN` does not change the exit code |
| `ds def <file>#<symbol>` \| `<file>:<line>` | returns the existing id for that block or mints one and inserts the directive; prints the id |
| `ds adopt [--dry-run]` | converts existing `path#L10-L20` and `path#symbol` links in docs into defs and cites (a reversed `#L20-L10` is the same range, as on GitHub, and becomes a forward `lines=`); resolves a relative link against the directory of the page holding it, as the page renders, falling back to the repository root and treating a leading `/` as root-relative; leaves a named anchor into another page (`README.md#target`) alone and unreported, since that is navigation between pages rather than a reference to code; lists what it could not resolve; proposes chains from matching secret names for confirmation |
| `ds repair [--apply] [--json]` | finds directives an older build wrote as bare lines into files that cannot hold one and mends them: in a format with comments the line is commented in the file's own syntax, keeping its id so citations still resolve; in one without (JSON, CSV, `go.sum`) it is deleted. Prints by default and writes only with `--apply`. Every edit is journaled, a deletion together with the line that followed it, so `ds undo` restores the file byte for byte and refuses once the file has moved around the change |
| `ds version [--json]` | the build that is running: version, the commit it was built from, and whether that checkout had uncommitted changes, read from what the Go toolchain stamped into the binary; a field it did not record reads `unknown` |
| `ds scan` | rebuilds ledger and refs |
| `ds check [--json] [--full] [--strict] [--run] [--resolve] [--env name] [--frozen] [--sync]` | the six passes; the CI gate. `--frozen` resolves foreign blocks from the committed `.ds/foreign.tsv` without syncing, and is the default when `CI` is set |
| `ds impact [--staged] [--json]` | before committing: which sentences, pages, repos, and owners this change will flag |
| `ds ack <id>… [--doc] [--line] [--group N] [--note] [--all] [--delegated-by human] [--dry-run]` | records approval against the current hash and sentence, with actor kind; `--dry-run` lists each sentence it would approve, quoted, and at which hash, and records nothing |
| `ds triage [--ack-group N --note …]` | groups `unacked` findings by diff similarity |
| `ds refresh [--dry-run]` | updates ledger locations for moved blocks; in repo mode rewrites fences |
| `ds render <doc> [--at tag-or-sha]` | expands directives to plain markdown with permalinks; `--at` renders the doc consistent with that commit |
| `ds status [--json]` | per-reference state for renderers to paint freshness on the page |
| `ds facts [--json] [--cited-by doc]` | every one-line def with its current value and citers |
| `ds why <id> [--chain] [--history]` | every reference to or cover of the id; `--chain` the path to its truth; `--history` every ack, note, rename, and move in order |
| `ds blame <doc> <line>` | the block behind a reference and its last three changes |
| `ds context <doc>` \| `<id>` `[--budget N] [--since ack\|sha] [--mode auto]` | the doc plus every block it cites, current, ranked and budgeted, diffs since ack; or every paragraph about a block |
| `ds graph [--dot] [--json]` | defs, refs, chains, translations, asserts as a graph |
| `ds report [--unmarked] [--uncovered] [--stalest] [--literals] [--orphaned-owners] [--gaps] [--metrics]` | hygiene; `--literals` hand-typed copies of fact values; `--gaps` what to document next; `--metrics` freshness per page and team, mean time to ack, and context bytes served versus file bytes |
| `ds review --ai` | proposes prose edits for current findings as a patch; never acks |
| `ds notify [--dry-run]` | routes open findings to owners with dedupe and escalation |
| `ds audit [--since] [--actor-kind human\|agent] [--export]` | the append-only event log |
| `ds rename <old-prefix> <new-prefix>` | relabels ids everywhere; identity unchanged |
| `ds publish [--tests junit.xml] [--dry-run]` | writes ledger, refs, commit, and test outcomes into the index; default branch only; `--dry-run` writes and pushes nothing and says in one line how the index would change (`index would change: 1 defs changed, 3 refs added`, or `index unchanged`) |
| `ds sync` | fetches the index and builds the merged tables; run by `check` |
| `ds prune [--dry-run] [--index] [--keep 30d] [--hash h --force]` | remove block bodies no ledger, ack, citation, or snapshot still needs; `--index` prunes the workspace stores from the default branch after a fresh sync |
| `ds undo [--list] [--dry-run] [--force] [--orphan]` | reverses the last **uncommitted** source write made by `def`, `adopt`, or `rename` from the journal; `--list` shows the stack and writes nothing |

Every command that rewrites something a person wrote or cannot rebuild — `def`, `adopt`, `rename`, `refresh`, `prune`, `undo` — every command that sends — `notify`, `github comment` — and the two whose writes reach others — `ack`, into the permanent approval log, and `publish`, into the shared index — has `--dry-run`; `cli.dryRunCommands` pins the list. The rest regenerate state from the tree (`scan`, `init`, `export`), and have none. Source is written only by `def`, `adopt`, and `rename`, and by `undo` reversing them.

**`undo` refuses more than it reverses.** The journal reaches back to the first write a checkout ever made, so a bare `undo` is bounded twice over:

- **The last commit is the boundary.** A write already in history is not a recent mistake: docs cite it, and with a workspace index other repos may too. Reversing it is a deliberate change that belongs in an ordinary edit and a review, and `--force` says so. The test is the written line's presence at its line in `HEAD`, not a stored commit id, so it stays correct across amends and rebases. With no git, or no commits, there is no history to protect and every write is undoable.
- **A cited def is not orphaned silently.** When the entry would remove a def that sentences still cite, `undo` names them and stops; `--orphan` accepts the breakage. The citers come from this repo's `refs.tsv` *and* from the merged workspace index, because a citation published by another repository is exactly the one the author cannot see. An absent or unreachable index degrades to the local answer rather than failing the undo.

Every run reports what the next entry is, because the failure this guards against is pressing `undo` once more than intended. `--list` shows the whole stack — kind, position, id, age, and whether each entry is committed or cited — and writes nothing.

### 23. Configuration

```toml
# .ds/config.toml
spec = "1.0"
prefix = "ds"
workspace = "github.com/org/ds-index"      # omit for a single-repo workspace

[scan]
code = ["internal/**", "cmd/**", "config/**", "db/**"]
docs = ["docs/**/*.md", "docs/**/*.mdx", "runbooks/**/*.md", "README.md"]
exclude = ["**/testdata/**", "**/*_test.go", "public/**", "dist/**"]
generated = ["**/*.pb.go", "**/gen/**", "**/*_gen.ts"]

[scan.limits]
max_file_kb = 512
max_line_chars = 2000

[include]
mode = "build"                               # build | repo
max_lines = 40

[check]
fuzzy_threshold = 0.8
unacked = "error"                            # error | warn
sentence = "wording"                         # wording | position: what an ack holds a citation to (section 18)
permalink = "https://github.com/org/repo/blob/{sha}/{file}#L{start}-L{end}"

[policy]
require_doc = ["pkg/api/**"]                 # exported symbols here must have a def with a home

[owners]
"@auth" = ["khanakia"]
"@platform" = ["someone"]

[secret]
paths = ["**/.env*", "**/secrets/**"]

[env]
default = "prod"
known = ["prod", "staging", "dev"]

[resolve]
enabled = false                              # opt in; never on fork PRs
store_hash = false
providers = ["github", "1password"]

[run]
enabled = false
allow = ["runbooks/**"]
timeout = "30s"
shell = "sh"                                 # what runs the commands; looked up on PATH
[run.env.staging]
DATABASE_URL = "$STAGING_DATABASE_URL"

[url]
ttl = "7d"
rate_per_minute = 30

[sources.sql]
dsn = "$DOCS_READONLY_DSN"
ttl = "24h"

[records]
source = "frontmatter"                       # frontmatter | sqlite | http
path = "records/"

[agents]
max_defs_per_run = 20
mcp = true
session_hook = "ds map --budget 2000"

[notify]
slack = "$DS_SLACK_WEBHOOK"
github_issues = true
escalate_after = "7d"

[id]
suffix_alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
suffix_length = 8
```

`run.shell` names the program that `ds:run` commands and the `[review]` command run under: `<shell> -c <command>` for `cmd=`, `id=` and the review command, and `<shell> <file>` for `file=`. The default is `sh`, found on PATH; on Windows, Git for Windows provides it, and a team whose commands are written for another shell names that one (`shell = "pwsh"`). docsync never substitutes a shell by itself, because the same command text means different things to different shells. When a command is about to run and the shell is not on PATH, `ds check --run` and `ds review --ai` stop with an error naming the shell and this key; nothing is recorded as run, and a repository with nothing to run is not asked for a shell.

Duration keys are checked when the config loads: `run.timeout` is a positive Go duration (`300ms`, `30s`, `2m`), and `url.ttl`, `sources.*.ttl`, `notify.escalate_after` and `notify.snapshot.digest_after` take a whole number with `m`, `h`, `d` or `w` (`90m`, `24h`, `7d`, `2w`). A value that does not parse stops the load with an error naming the key rather than falling back to the default, because a typo that runs with a setting nobody chose looks healthy. An empty value means the default.

### 24. CI, hooks, editor

| Where | What |
|---|---|
| pull request | `ds check --json`; one comment per doc with findings linked to the doc line and block diff; merge blocked on exit 1; applying a `docs-acked` label lets a reviewer `ack --all` (only the act of applying it acks; a later push needs it re-applied) for a PR that changed behaviour and docs together |
| default branch, after merge | `ds publish --tests junit.xml` |
| nightly | `ds check --run --resolve` where credentials exist; `ds notify`; a PR with findings for a reviewer or assistant |

**Notifier memory.** `ds notify` is quiet on a second run because it remembers what it sent, and `escalate_after` measures from a first sighting — both from `.ds/notified.json`, which is machine-local and so absent on a fresh CI checkout. Without something to carry it between runs, dedupe never holds and escalation never fires, while everything looks healthy: a notifier that repeats itself nightly is one people mute, and a muted notifier fails without anyone deciding to turn it off.

- Where that memory lives is a seam, not a fixed file. The CLI's `NotifyState` has `Load` and `Save`; the default is the local file, and an installation whose CI cannot cache it supplies a store that survives. A missing state is an empty one and a first run; an **unreadable** state is an error, because treating a corrupt file as empty would silently reset dedupe, which looks exactly like working.
- The shipped nightly template restores the file with `actions/cache`, which needs no permission the job does not already have. Un-ignoring the file is not an alternative: CI still cannot write it back, so it would only ever hold stale state committed by hand.
- With no memory and open findings, `notify` sends **one** message naming everything open and saying why, not one digest per owner. A first run and a lost-state run cannot be told apart from inside a process that has lost the record — and both want the same thing, which is not a burst that reads as a pile of new problems.
- `ds doctor` reports this under `CI`, so the failure is visible rather than inferred from a channel going quiet.

**Snapshot staleness notifications** (`[notify.snapshot]`). `status` says how far the pin has fallen behind and never interrupts anyone; this is the part that speaks. Drift triggers it, never age on its own: a snapshot ninety days old against an upstream that has not moved costs nobody anything.

Urgency comes from the change class, which `check` already computes, so one rule decides both whether prose flags and whether a person is interrupted. `stability` gates and the class lists rank — and the composition is specific: **rank over the classes that flag individually for that block's stability**, not over the change as a whole. An `api` block whose change is `signature, body` is immediate because `signature` flags on its own; the `body` neither dilutes nor adds, since `api` does not flag on `body` at all. Ranking the whole change would let a class that never flags — `moved` — drag a digest-tier change up to immediate.

| Setting | Meaning |
|---|---|
| `enabled` | on by default; without a workspace there is no snapshot to go stale |
| `immediate` | classes worth interrupting for; default `signature`, `type`, `renamed`, `value` |
| `digest` | classes worth knowing eventually; default `body`, `comment` |
| `digest_after` | how long a digest-tier drift waits before it is mentioned; default `14d` |
| `on_deleted` | the tier for a cited block the upstream no longer defines; default `immediate` |
| `owner` | who hears it; empty falls back to the owners of the defs in the citing files |

A class named in both lists is a load error, and a class that flags but is named in neither is treated as `immediate`, because guessing the other way fails toward silence. `unknown` is therefore immediate wherever it flags, which is the same conservative choice §20 makes.

- Messages are sent once per `(repo, id, hash, tier)`, and again only when the drift worsens by tier — `never < digest < immediate` — or when `escalate_after` passes with it unresolved. A deletion keys on `(repo, id, gone)` instead, because it has no new hash and is a different event from any change to the same block. A block the snapshot catches up with is forgotten, so a later drift on it is news again.
- The message names the upstream repo, each block's old and new hash with its class, **the `file:line` of every citation in this repo**, and the one instruction that resolves it. Citing locations are the part that makes it actionable; "3 blocks behind" with no file names is a message people learn to ignore.
- It routes to this repo — the people who must run `ds sync` — not to the owner of the block that changed, which is where an ordinary finding goes.
- **Not comparing is not the same as nothing to report.** A repo the index stops publishing is counted, and after three consecutive runs without a comparison `notify` says so once, keyed on that repo. Otherwise a broken index hides staleness indefinitely.
- It never touches an exit code. `check --frozen` keeps its single meaning: same commit, same answer.
| pre-commit | `ds impact --staged`; `ds report --literals` |
| editor (LSP) | code lens on every `ds:def` listing dependents; hover on a cite shows the block and the diff since the acked hash, and on a line with several cites answers for the one under the cursor (positions in UTF-16 code units, the protocol default); go-to-definition, likewise per cite; warning on rename or delete of a defined symbol |
| docs site | `ds status --json` paints green, amber, red dots beside cited sentences, with the ack note on hover: each row carries `severity` (`none`, `warning`, `error`) and, from the latest ack of that citation at its baseline hash, `note`, `acked_by`, `acked_at` |

### 25. Rules for an AI writer and reviewer

The tool knows which sentence depends on which block and whether the block changed. The AI can judge whether the sentence is still true, but cannot afford to reread everything. `check` produces the exact short list; the AI reads only that; the ack is recorded against the hash. Trigger manually, nightly, or in CI: same command, same JSON.

Writing:
1. Never cite a path and line. `ds def` for the block, then `ds:block?id=…`.
2. Never paste code. Write a `ds:block` directive; the build renders it.
3. Never type a fact. Run `ds facts`; cite with `ds:cfg` if it exists; otherwise define once with an inline `ds:def`, then cite.
4. External links go through `ds:url`. Behaviour claims cite the test with `assert=true` when one exists.
5. Add `covers` for every id a new page introduces.
6. Start a session with `ds map`. When editing a page, start from `ds context <doc> --budget N --since ack`; nothing outside it needs rereading. Use `find`, `read`, `locate` instead of grep and file reads.
7. Run `ds check` before declaring done. Run `ds impact --staged` before proposing a code change that touches defined blocks.
8. After a code change, `ds check --json` is the complete work list.
9. Before deleting or renaming code, `ds why <id>`, and handle dependents in the same change.

Reviewing:
1. `ds check --json` is the whole list; run `ds triage` first.
2. For each `unacked`, read the sentence, the class, and the diff. Still true: `ack --note`. Not true: edit, then `ack --note`. Never ack a page wholesale.
3. For each `broken`, decide from the diff whether the block was deleted or moved without its def; re-add the def or rewrite and remove the reference.
4. As `review --ai`, produce the patch and stop; a person or an explicitly delegated agent records acks.
5. Leave anything needing a human unacked and say why.

### 26. The agent surface

Everything above serves a human at a terminal. This section is the same tool as an AI would use it: through typed tools, a stable JSON contract, token-bounded context, and rules about what an agent may do unattended. The goal is that an agent working on docs reads a few kilobytes of exactly the right material instead of the repository, and that nothing it does can approve its own work silently.

#### 26.1 `ds mcp`

An MCP server exposing the read tools and a bounded set of write tools, each with a schema derived from the JSON contract below.

| Tool | Read or write | Purpose |
|---|---|---|
| `map` | read | token-bounded overview of a repo or workspace |
| `find` | read | ids by symbol, text, file, or tag; agents do not know ids |
| `read` | read | the body of a block by id, or a `lines=` fragment |
| `locate` | read | file and line range for an id |
| `facts` | read | every one-line def with current value and citers |
| `why` | read | who depends on an id; `chain`; `history` |
| `context` | read | a page or an id with its dependencies, budgeted and delta-capable |
| `check` | read | findings for the working tree |
| `impact` | read | what a staged change will flag |
| `def` | write | mint or return an id and insert the directive; scope-limited |
| `ack` | write | only with `delegated_by`; recorded as such |

`run`, `resolve`, `undo`, `publish`, and `adopt` are not exposed over MCP. An agent that needs them asks a human to run them.

#### 26.2 The JSON contract

Every command with `--json`, and every MCP tool, returns `{ "json_format": 1, "generated_at": …, "repo": …, "commit": …, … }`. The contract is versioned separately from the ledger and changes only additively within a major version. `baseline` on a finding is `ack` or `seen`, saying which hash the current one was compared against, so a consumer can tell a reviewed statement that went stale from one nobody has reviewed. Shapes:

```json
// ds check --json
{ "json_format": 1, "commit": "7c1e2a", "summary": {"error": 2, "warning": 1, "ok": 5},
  "findings": [
    { "state": "unacked", "severity": "error",
      "doc": "docs/sessions.md", "line": 13, "sentence": "Every session write goes through SaveSession. It writes the legacy row first, then the sessions table.",
      "id": "sess-save-k7m2p4xq", "repo": "api", "file": "internal/store/write.go", "lines": [12, 21],
      "class": ["renamed", "body"], "baseline": "ack", "hash": {"acked": "9f3a1c", "current": "71be04"},
      "diff": "-  if err := s.legacy.Save(ctx, sess); err != nil {\n+  if err := s.sessions.Insert(ctx, sess); err != nil {",
      "owner": "@auth",
      "remedy": { "if_still_true": "ds ack sess-save-k7m2p4xq --doc docs/sessions.md --line 13 --note '…'",
                  "if_not": "edit the sentence at docs/sessions.md:13, then ack" } } ] }
```

```json
// ds context docs/sessions.md --budget 8000 --since ack --json
{ "json_format": 1, "target": "docs/sessions.md", "budget_tokens": 8000, "used_tokens": 3120,
  "items": [
    { "rank": 1, "why": "unacked", "id": "sess-save-k7m2p4xq", "file": "internal/store/write.go", "lines": [12, 21],
      "tokens": 410, "mode": "diff", "content": "…" },
    { "rank": 2, "why": "cited, unchanged", "id": "sess-ttl-p2c4y7mk", "tokens": 12, "mode": "value", "content": "30" } ],
  "omitted": [ { "id": "sess-sweep-t4k2b9rf", "reason": "unchanged since ack; over budget" } ] }
```

```json
// ds map --budget 2000 --json
{ "json_format": 1, "workspace": "platform", "repos": ["api", "docs", "infra"],
  "pages": [ { "path": "docs/sessions.md", "covers": 5, "state": {"ok": 4, "unacked": 1}, "owner": "@auth" } ],
  "defs":  [ { "id": "sess-save-k7m2p4xq", "desc": "dual-write guard", "file": "internal/store/write.go", "cited_by": 3, "stability": "api" } ],
  "chains": [ { "root": "op-stripe-key-p9c2v7ld", "hops": 3, "state": "ok" } ],
  "freshness": { "ok": 41, "unacked": 3, "broken": 0, "expired": 1 } }
```

`find`, `read`, `locate`, `facts`, `why`, and `impact` follow the same envelope with obvious fields. Every finding and every context item carries `tokens` so a caller can budget, and content is always delimited as data (see 26.6).

#### 26.3 `ds map`: the table of contents

Returns, under a token budget, what exists and what state it is in: pages with cover counts and freshness, defs with `desc`, chains, owners, and totals. Ranking within the budget: anything not `ok` first, then most-cited, then most recently changed. An agent starting a session reads `map` instead of the tree. This is the index that tells a model what to read so it reads almost nothing else.

#### 26.4 `ds context`: budgeted and delta

| Flag | Effect |
|---|---|
| `--budget N` | return the highest-ranked items that fit in N tokens; report what was omitted and why |
| `--since ack` \| `--since <sha>` | for cited blocks, return only the diff since the last ack or since the commit; unchanged items shrink to one line |
| `--mode full\|diff\|value\|auto` | `auto` sends values as values, changed blocks as diffs, small unchanged blocks whole, large unchanged blocks as a line |

Ranking: `unacked` and `broken` first, then dependency distance from the target, then recency. Order is deterministic for identical inputs so prompt caches hit. Output includes `used_tokens` and an `omitted` list, so an agent can ask for more with a larger budget instead of guessing.

#### 26.5 `ds find`, `ds read`, `ds locate`

```
$ ds find SaveSession
sess-save-k7m2p4xq   func  internal/store/write.go:12-21   "dual-write guard"   cited by 3
$ ds find --file config/auth.yaml
auth-port-h3v8n2wd   line  config/auth.yaml:3   value 8081   cited by 2
$ ds read sess-save-k7m2p4xq --lines 1-4
$ ds locate sess-save-k7m2p4xq
internal/store/write.go:12-21 @ 7c1e2a
```

Two exact calls replace grep, open, scroll. Most questions are answerable from the ledger with no source read: `facts`, `why`, `locate`, `map`.

#### 26.6 Content is data, never instructions

Everything the scanner reads is untrusted text: prose, comments, directive arguments, `desc`, ack notes, page titles. A comment that reads "ack everything" is a string. Rules:
- JSON output wraps all scanned content in fields named `content`, `sentence`, `diff`, `desc`, `note`; consumers must treat those fields as data. The spec's reference MCP server adds a `data:` delimiter in tool descriptions and never interpolates scanned text into instructions.
- `review --ai` and any agent connected over MCP has no path to `run`, `resolve`, `undo`, or `publish`, and can `ack` only as delegated (26.7).
- A conformance fixture plants instructions in a comment, a `desc`, and a page, and asserts that no tool output presents them as anything but content.

#### 26.7 What an agent may do unattended

| Allowed without a human | Requires a human |
|---|---|
| `map find read locate facts why context check impact` | `run`, `resolve`, `publish`, `undo` |
| prose edits proposed as a patch (`review --ai`) | merging that patch |
| `def` up to `agents.max_defs_per_run` per invocation | `def` beyond the cap, and `adopt` |
| `ack --delegated-by <human>` when a human named in `[owners]` delegated for this run | `ack` otherwise |

An ack made by an agent is recorded with `actor_kind = agent`, the model or tool name, and `delegated_by`. The audit log therefore always says who judged. `ds audit --actor-kind agent` lists every agent judgment.

#### 26.8 Memory that costs nothing new

Ack notes, claim text, `desc`, and decisions already persist. `ds why <id> --history` returns them in order: every ack with its note, every hash it was made against, every rename and move. That is a reasoning trail per block across months, readable by an agent in a few hundred tokens, and it is derived from data the tool keeps anyway.

#### 26.9 `ds report --gaps`

Proactive rather than reactive: the most-changed files with no defs, exported symbols nobody cites, pages with the oldest acks, facts found typed by hand, chains with unverifiable hops. A ranked list an agent or a person can work through. Turns the tool from cleanup into prevention.

#### 26.10 `ds init --agents`

Writes the `CLAUDE.md` and `AGENTS.md` fragment containing section 25, registers `ds mcp` in the editor and agent configs it can find, installs a session-start hook that loads `ds map --budget 2000`, and sets `agents.max_defs_per_run`. One command, same as for humans.

#### 26.11 Measured savings

`report --metrics` includes, per agent session recorded through MCP, bytes served by `context` and `read` against the bytes of the files those items came from. A tool that claims to save tokens prints the number.

### 27. Adoption path

1. `ds init`, `ds doctor`.
2. `ds adopt --dry-run` on the docs; review the proposed defs and rewritten links; run it.
3. Turn on `check` in CI as warnings for two weeks; watch `report --metrics`.
4. Flip `unacked` to `error`. Add `policy.require_doc` for the paths that matter.
5. Add the workspace when a second repo needs it; nothing in the first repo changes.
6. Enable `--resolve` and `--run` last, on a scheduled job with credentials, never on PRs.

---

## Part VI — Use cases

Each scenario is a fixture in the conformance suite.

### 28. The core loop, end to end

**Before.** Code, config, and one page.

```go
// internal/store/session.go
// ds:def id=sess-save-k7m2p4xq owner=@auth stability=api
func (s *Store) SaveSession(ctx context.Context, sess Session) error {
	if err := s.legacy.Save(ctx, sess); err != nil {
		return fmt.Errorf("legacy save: %w", err)
	}
	return s.sessions.Insert(ctx, sess)
}

// ds:def id=sess-interval-q9x1z6ch
const sweepInterval = time.Hour
```

```sql
-- db/sweep.sql
-- ds:def id=sess-sweep-t4k2b9rf runnable=true
DELETE FROM sessions WHERE expires_at < now() - interval '30 days';
```

```yaml
# config/auth.yaml
auth:
  port: 8081            # ds:def id=auth-port-h3v8n2wd
  session_ttl_days: 30  # ds:def id=sess-ttl-p2c4y7mk
```

```markdown
---
title: Sessions
ds:
  covers: [sess-save-k7m2p4xq, sess-interval-q9x1z6ch, sess-sweep-t4k2b9rf, auth-port-h3v8n2wd, sess-ttl-p2c4y7mk]
---

# Sessions

Auth listens on [8081](ds:cfg?id=auth-port-h3v8n2wd) and sessions live for [30](ds:cfg?id=sess-ttl-p2c4y7mk) days.

## Writes

Every session write goes through [`SaveSession`](ds:block?id=sess-save-k7m2p4xq). It writes the legacy row first, then the sessions table.

<!-- ds:block id=sess-save-k7m2p4xq lines=1-4 title="the guard" -->

## Sweep

Expired rows are deleted every [hour](ds:block?id=sess-interval-q9x1z6ch) by [this statement](ds:block?id=sess-sweep-t4k2b9rf).

<!-- ds:run id=sess-sweep-t4k2b9rf env=staging expect=rows -->

## Open work

<!-- ds:table kind=task where="service=svc-auth and state!=done" cols=title,due,owner -->
```

`ds check`: all ok. Rendered: live values, a four-line fragment, the statement with its last staging result, a table of open tasks. The markdown holds none of that content.

**The refactor.** A developer moves `SaveSession` to `write.go`, renames it `Persist`, reverses the write order, changes the interval to thirty minutes, and moves the port to an environment variable, deleting the yaml line. The defs travel with the code. The docs are not opened.

```
$ ds check
docs/sessions.md
  L9   auth-port-h3v8n2wd     BROKEN    def not found; no hash match; last seen config/auth.yaml:3
  L9   sess-ttl-p2c4y7mk      ok
  L13  sess-save-k7m2p4xq     UNACKED   renamed Store.SaveSession → Store.Persist; moved session.go:5 → write.go:12
                                        class: body    hash 9f3a1c → 71be04
                                        -  if err := s.legacy.Save(ctx, sess); err != nil {
                                        +  if err := s.sessions.Insert(ctx, sess); err != nil {
  L15  sess-save-k7m2p4xq     ok        fragment renders from the new location
  L19  sess-interval-q9x1z6ch UNACKED   class: value   time.Hour → 30 * time.Minute
  L19  sess-sweep-t4k2b9rf    ok
  L21  run                    skipped   pass --run to execute
summary: 1 broken, 2 unacked, 3 ok, 1 skipped
exit 1
```

Note the `api` stability on the guard: had only the body changed with the same signature, L13 would not have flagged. Here the rename is a class that `api` flags.

**Fixing it.** The reviewer edits three sentences:

```markdown
Auth listens on the port given by `AUTH_PORT` and sessions live for [30](ds:cfg?id=sess-ttl-p2c4y7mk) days.
Every session write goes through [`Persist`](ds:block?id=sess-save-k7m2p4xq). It writes the sessions table first, then the legacy row.
Expired rows are deleted every [thirty minutes](ds:block?id=sess-interval-q9x1z6ch) by [this statement](ds:block?id=sess-sweep-t4k2b9rf).
```

```
$ ds ack sess-save-k7m2p4xq sess-interval-q9x1z6ch --note "order and interval changed, prose updated"
$ ds check
summary: 5 ok, 1 skipped
```

The port sentence lost its cite because the value moved to an environment variable the tool cannot see. Correct: the doc must not claim verification it does not have. The fix is a facts-page def for `AUTH_PORT`'s home.

**Later, from the code side.** Someone opens `write.go`, sees the code lens `docs: sessions.md#writes` above `Persist`, deletes the function anyway. Their PR fails with two `BROKEN` findings naming the doc lines. They cannot merge without touching the doc.

### 29. Facts

A facts page:

```markdown
# Facts
- API port: [8081](ds:def?id=api-port-h3v8n2wd&type=int)
- API version: [2.14.0](ds:def?id=api-version-c8t2m6qp&type=semver)
- Hosted at: [https://example.com](ds:def?id=app-host-d4k8w2mn&type=url)
- On-call lead: [Aman](ds:def?id=oncall-lead-r9k1w5zb)
```

Every other page cites. Change `8081` to `8443` here: every cite renders `8443`, every sentence around a cite flags once for an ack, `ds why api-port-h3v8n2wd` lists them before the change. A hand-typed `8081` elsewhere is caught by `report --literals` in CI.

### 30. Secrets

The full chain from section 12, with a runbook:

```markdown
Stripe credentials: <!-- ds:chain id=app-stripe-key-m4w8k2qn -->
To rotate: change the item in 1Password, then run <!-- ds:run id=sync-secrets-job-q7n2m4kt expect=ok -->.
```

Someone rotates in 1Password and forgets the sync. Nightly `check --resolve` reports `out of sync` on the GitHub hop and `unacked` on this runbook line. The on-call runs the `ds:run` from the doc, hashes match, the runbook is acked. No value was ever written anywhere.

### 31. Translations, deprecation, tests, spec-first

**Translation.** The English paragraph is defined; the German cites it with `translates=true`. An English edit flags the German paragraph with the English diff attached.

**Deprecation.** `deprecated=2026-09-01 sunset=2027-01-01` on an endpoint's def paints a badge on every cite today and turns every remaining cite into an error on the sunset date.

**Tests.** "The old token stays valid for sixty seconds, see [the test](ds:block?id=test-rotate-grace-b7k2m9qx&assert=true)." The test is renamed, skipped, deleted, or fails in the published run: the sentence flags. Behaviour claims become executable without writing anything new.

**Spec-first.** A design paragraph is defined; the implementing function's comment says `// implements ds:block?id=spec-rotation-grace-w4n8t2pk`. The paragraph is edited; the code repo's next `check` says "spec changed since this was implemented" at the function. Drift runs both ways with one mechanism.

### 32. Working at scale

**A refactor across fifty functions.** Fifty `unacked` findings. `ds triage` shows one group of forty-seven with an identical parameter-rename diff and three that differ. One `ack --group 1 --note "parameter rename only"`, three real reviews. Without this the first big refactor teaches everyone to `ack --all` blindly.

**Impact before commit.** `ds impact --staged` prints "12 sentences on 4 pages in 3 repos, owners @auth @sre @web" while the change is still in the developer's hands.

**A live number.** `[1.2M](ds:cfg?query="sql:select count(*) from users"&ttl=24h)` renders "1.24M (as of 2026-09-06)" from the read-only source, cached a day.

**A diagram that must match the code.** A remote def with `pick=file` on `arch.svg`; the paragraph explaining it cites it. Regenerate the diagram, the paragraph flags.

**Versioned docs.** `ds render docs/sessions.md --at v2.3.0`: blocks, values, and permalinks from that tag. What the docs said when the customer signed.

**Grounding an AI writer.** `ds context docs/sessions.md` returns the page plus exactly the blocks it depends on, current, with diffs since ack. A writer or agent reads that and nothing else. This is the index the memory problem needed: what to read, not everything.

**Undocumented exports.** `policy.require_doc = ["pkg/api/**"]`. A PR adding a public function there with no def and no home fails with `undocumented export`. Prevention, per path, opt in.

**Link rot.** `ds:url` findings: `MOVED https://postgresql.org/docs/13/… → /docs/current/… (301)`, `DEAD https://linear.app/org/issue/AUTH-42 (404)`.

**Reverting.** A bad change flags twelve sentences; the commit is reverted; the hashes return to acked values; the findings vanish with no action.

**Owners as teams.** `owner=@auth` with `[owners]` in config. A person leaves, one line changes. `report --orphaned-owners` lists the rest.

**Readers see freshness.** `ds status --json` drives green, amber, red dots in the page margin with the ack note on hover. Readers notice rot first; this makes what they notice actionable.

---

## Part VII — Edge-case rulebook

Each line is a rule the implementation follows and a fixture in the suite.

**Reading directives.** A directive inside a string, a code fence, an indented code block, or inline code is not one. Strings exist only in code: in markdown, HTML, AsciiDoc and reStructuredText a quote is punctuation, so an apostrophe before a trailing directive does not hide it. A continued directive is read the same way by `check` and `render`, keys from every line. One directive per comment. If comments already use `ds:`, change the prefix; `check --explain` prints every match. Double quotes for spaces, single quotes for values containing double quotes, a continuation line for both; no escapes. Ids are lowercase ASCII.

**Finding the block.** Decorators, attributes, and doc comments between the directive and the code are skipped; in Python and Go the directive may sit anywhere in the leading comment block. Nested declarations bind to the innermost, recorded as `Class.method`. Frontmatter is never part of a block. A markdown section ends at the next heading of level ≤ the defining one, so skipped levels are handled. Nothing to bind to is an error. Generated paths refuse minting.

**What changed.** Hashes are computed after normalization and, where a grammar exists, over the token stream, so formatters never cry wolf. Comment-only edits flag `frozen` and not `stable`. A fragment beyond the block's length renders what exists and reports `range`. `rewritten?` uses a diff ratio of 0.8 or higher.

**Ids and branches.** A duplicated `def` line is a hard error; `ds def --fix` re-mints the second and prints old→new; the tool never guesses which is original. Prefix collisions are allowed; `report` lists busy prefixes. Ids ignore branches; the index keeps one row per published branch; `branch=` selects. A published branch is a def of its own everywhere a def is compared — change tracking, snapshot staleness, the editor — so publishing a feature branch never makes main look changed or behind; and `assert=` reads the default branch's published test run, never a branch's, so a red feature branch cannot fail main's citations. A branch's published citations are likewise not the default branch's: upstream `check` does not see them, and only the `undo` guard, where a def still cited on an open branch must not be stranded, counts them.

**Docs and acks.** A typo in an id is `broken` with "did you mean" at edit distance ≤ 2. Acks are per sentence, `--all` widens. Renamed docs keep refs through git rename detection; a page may define itself at the top to be addressable. Contradictory pages are not a tool error; `ds why` shows them together. Permalink templates are per repo.

**Workspaces.** Private repos use existing git credentials. Removed repos give `broken: repo removed`. Publishes cannot collide. Forks cannot publish. No network means last synced copy plus an age warning.

**Security.** `run` and `resolve` are off by default and never run on fork PRs; `cmd=` and `file=` only in allow-listed docs, `file=` quoted and confined to the repository; `id=` only with `runnable=true`. Values the tool passes to git — a commit from the ledger or an `at=`, a path, the workspace URL — follow `--end-of-options` or `--`, so a committed value starting with `-` is never read as an option (git 2.24 or later). `secret=true` and the `secret` globs stop any value from rendering. `file=` resolves under the repo root with symlinks rejected and the same `max_file_kb` and binary limits as any scanned file, except `local=true` defs, which are read only on the machine running `check` and never in CI. Resolvers hold values in memory only and return existence and a hash; a resolver that prints a value fails conformance.

**Scale.** Configured paths only; incremental by git diff; ledger sharded per top-level directory; tree-sitter in parallel. Findings on a heavily cited id collapse with a count. One sorted row per id.

**Adoption.** `ds adopt` converts existing path links. Sidecar mode exists and is documented as weaker. Without the editor extension a directive is a comment and nothing breaks; `ds render` gives a fully expanded copy.

---

## Part VIII — Guarantees, conformance, decisions

### 33. Versioning, conformance, audit, offline, safety, performance

- **Versioning.** Ledgers and index files carry `format=`; repo config carries `spec=`. A newer tool upgrades older formats in place; an older tool refuses a newer format by name, never by misparsing. The extraction rule (`extract=`, section 15) is held to the same rule: a ledger or refs file recording a rule newer than the tool implements is refused by every command, `scan` included, naming the file and both rules, because its hashes cannot be compared with the ones the tool computes and a scan would rewrite the file under the older rule; `doctor` reports it as `FAIL`. An older rule is the upgrade path, so it is a warning: `check` names it, `doctor` reports `WARN`, and `scan` says it rewrote the files under the current rule. Builds before the first release stamped `extract=2` to `4` for what is now rule 1; such a repository is refused once, and setting `extract=1` on the first line of `.ds/ledger.tsv` and `.ds/refs.tsv`, then running `ds scan`, is the whole fix, as the refusal says. The ack log's header rule is not consulted: it is written once and never restamped, and each ack row carries its own rule, which `check` reports by name (section 18). Foreign ledgers are exempt, since their hashes are only ever compared with others the same upstream wrote. Directive additions are backwards compatible because unknown keys and verbs warn.
- **Conformance suite is the spec.** Every rule here is a fixture: an input tree and the expected `check --json`. Around two hundred cases at 1.0. Parsers, plugins, and rewrites are judged by the suite. A rule without a fixture is not yet a rule.
- **Audit.** Acks, resolves, publishes, and source writes are append-only events with actor, time, ids, hashes, note. `ds audit` exports them. Secret values never appear in it.
- **Offline.** Everything local works with no network: scan, ledger, check, render without permalinks. Network-dependent findings say `unverifiable`.
- **Safety.** Source is written only by `def`, `adopt`, and `rename` (and by `undo` reversing them); every command that rewrites or sends has `--dry-run`, `undo` included; every write is journaled, and `ds undo` reverses the last one that is neither committed nor still cited, requiring `--force` or `--orphan` to go further.
- **Performance targets**, measured by `task perf` against the built binary on a generated hundred-thousand-file git repository: incremental `check` under two seconds; full scan under a minute; `impact --staged` under one second. A release that misses them does not ship. It is not part of `task`, because building that tree takes minutes, so it is run before a release and after a change to scanning, the extraction cache or ledger loading.

### 34. What remains possible, and its guard

| Drift still possible | Guard |
|---|---|
| a value typed by hand instead of cited | `report --literals` in CI; the AI writing rule |
| a fact whose real home is outside the repo | give it a home in a facts page; `ds:claim` with an expiry for the rest |
| the text is unchanged but its meaning changed | out of reach of any tool; `stability=frozen` where meaning matters |
| a sentence with no reference | untouched by design; add a `ds:claim` to make it age out |
| the code is unchanged but behaviour regressed | `assert=true` on a cited test |
| an external page moved or died | `ds:url` with `--resolve`; `unverifiable` offline |
| a secret copy drifted from its truth | `--resolve` on a logged-in machine; otherwise names and chain shape only |
| the truth of a secret lives on one laptop | `local=true` says so on every check; the fix is a vault |
| the workspace index is stale | reported by age |
| a reviewer acks without reading | the audit trail; nothing else can fix that |
| an agent approves its own edits | agents cannot ack except as delegated, and every agent ack is labelled |
| a doc or comment tries to instruct the reviewing agent | scanned content is data in every output; agents have no path to `run`, `resolve`, `undo`, `publish` |

### 35. Non-goals

- Not a markup language, renderer, or site generator.
- Not a semantic checker; it never decides whether prose is true.
- Not a reference-doc generator.
- Not for prose that refers to nothing.
- Not a secrets manager; it verifies addresses and never holds values.

### 36. Decisions record

Questions that were open during design and how they were closed.

| Question | Decision | Why |
|---|---|---|
| directive placement where the leading comment is a docstring | accepted anywhere inside the leading comment block | matches how Go and Python authors write |
| fuzzy match for `rewritten?` | diff ratio ≥ 0.8 | what reviewers expect a diff tool to mean |
| require `covers` | no; `uncovered` is informational | forcing it slows adoption without adding safety |
| index transport | git first, HTTPS as an adapter | auditable, uses existing credentials |
| what decides whether a citation is still covered | the citation's own baseline: `acked_hash`, else `seen_hash` | the scan-to-scan change table answers "what changed since the last scan", which is a different question and is empty whenever the ack is older than the previous scan or the block lives in another repo. Anchoring on it made `ds scan` clear open findings and made every cross-repo citation pass |
| baseline for a citation nobody has acked | `seen_hash`, written once at the first scan that records the citation and never revised | without it there is no baseline at all, so an unreviewed change is invisible; revising it each scan would silently adopt whatever the block says now, which is the same hole |
| where the body behind a hash lives | content-addressed under `.ds/blocks/` and `repos/<name>/blocks/` | a baseline hash can be many scans old or in another repo, so neither the previous ledger nor the VCS at its commit can answer; content addressing makes the store append-only and self-deduplicating |
| a drift whose older body is unavailable | class `unknown`, flagging wherever `api` flags | recording it as `body` would let `api` drop it silently, and a missed signature change is worse than an ack nobody needed |
| record source for `table` | frontmatter directory first; SQLite and HTTP adapters | the least new infrastructure |
| `query=` as its own verb | no; stays on `cfg` | one verb for one value |
| markdown section boundary with skipped heading levels | ends at the next heading of level ≤ the defining one | the only rule that survives real documents |
| positional arguments | none | one grammar for every verb |
| a `value=` key on `def` | none | a value in a comment is invisible or duplicated |
| copies of code in the repo | none by default; opt-in repo mode | size, diff noise, and drift |
| id suffix length | eight characters | four collides in the low thousands |
| where truth of a secret is declared | `truth=true` on exactly one def per chain | direction can never be inferred from where a name appears |
| name and prefix | docsync, `ds` | short, greppable, not a word people type in comments |
| agent access | MCP for reads and two bounded writes; no `run`, `resolve`, `undo`, `publish`; `ack` only delegated | an agent must not be able to approve its own work silently |
| JSON versioning | `json_format` separate from ledger `format` | consumers and storage evolve at different speeds |
| context delivery | budgeted, ranked, delta by default | the token saving is the product for agents |
| library and CLI | dependency-free root module, heavy tiers in `ext/` sub-modules, CLI and MCP as separate modules, every CLI operation an exported function | modelled on ubgo/dotenv; lets any Go project embed docsync or ship a custom binary |
| plugins | Go capability interfaces plus a JSON process protocol | Go for speed and typing, processes for any language |
| who writes files | never the library; it returns `Edit` values and the caller applies them | no hidden state, testable without a filesystem |

### 37. Architecture: library first, CLI second, plugins everywhere

Modelled on `github.com/ubgo/dotenv`: a dependency-free root library where every operation is an exported function, a separate CLI module so library users never inherit Cobra, capability interfaces for extension, and invariants pinned by tests. Another Go project can import the library, register its own verbs, extractors, and resolvers, and ship a custom binary without forking.

#### 37.1 Modules

| Module | Imports | Holds |
|---|---|---|
| `github.com/ubgo/docsync` | stdlib only | directive parser, id minting, ledger and refs model, matching, findings, change classes, sentence binding, acks, the text and markdown extractors, the process-plugin protocol, `Check`, `Scan`, `Context`, `Map` as functions over an `fs.FS` |
| `github.com/ubgo/docsync/ext/treesitter` | tree-sitter bindings | the syntax tier for Go, TypeScript, Python, SQL, and any grammar the caller registers |
| `github.com/ubgo/docsync/ext/structured` | yaml, toml, hcl parsers | the structured tier; json, ini, env, csv, properties need no dependency and live in the root |
| `github.com/ubgo/docsync/ext/resolve/{github,onepassword,aws,gcp,vault}` | each provider's SDK or CLI | resolvers, one module per provider |
| `github.com/ubgo/docsync/ext/records/{frontmatter,sqlite,http}` | per source | record sources for `table` |
| `github.com/ubgo/docsync/cli` | cobra, the ext modules it enables | the `ds` binary; every subcommand is a thin call into a library function |
| `github.com/ubgo/docsync/mcp` | an MCP server library | the agent surface over the same library functions |

Rules: the root imports nothing outside the standard library and never will. Nothing in the root knows a vendor name, a git host, or a CI system. Anything that shells out (git, `gh`, `op`) lives in an `ext` module and documents the external program at the point of use. No package-level state, no cache directory, no `$HOME` reads in the library; every function takes its inputs and returns its outputs, and the CLI owns paths and config.

#### 37.2 Library API shape

```go
import "github.com/ubgo/docsync"

sys, err := docsync.New(
    docsync.WithFS(os.DirFS(repo)),
    docsync.WithConfig(cfg),                       // parsed by the caller; the library never reads files it was not given
    docsync.WithExtractor(treesitter.Go(), treesitter.TypeScript()),
    docsync.WithExtractor(structured.YAML(), structured.TOML()),
    docsync.WithVerb(myorg.LinearTicketVerb{}),    // a custom verb
    docsync.WithResolver(onepassword.New(opClient)),
    docsync.WithRecordSource(frontmatter.Dir("records")),
    docsync.WithPrevious(ledger, refs),            // the committed state to diff against; nil for a first run
)

report, err := sys.Check(ctx, docsync.CheckOptions{Full: false, Resolve: false, Env: "prod"})
for _, f := range report.Findings { … }          // the same struct the JSON contract serialises

ctxBundle, err := sys.Context(ctx, "docs/sessions.md", docsync.ContextOptions{Budget: 8000, Since: docsync.SinceAck})
id, edit, err := sys.Define(ctx, "internal/store/write.go", "Store.Persist", docsync.DefineOptions{Owner: "@auth"})
// edit is a proposed change to the source file; the caller decides whether to apply it
```

Functional options only. Every operation that would write to source returns an `Edit` value; the library never writes a file. `Check`, `Scan`, `Context`, `Map`, `Facts`, `Why`, `Impact`, `Triage`, `Render` are pure over the inputs given. The CLI applies edits, touches git, and prints.

#### 37.3 Capability interfaces

A plugin implements the smallest interface that fits; optional interfaces upgrade behaviour without configuration.

| Interface | Method sketch | Used for |
|---|---|---|
| `Extractor` | `Match(path) bool; Blocks(src []byte) ([]Block, error)` | a new file format or language tier |
| `Picker` | `Pick(block Block, expr string) (Value, error)` | a new `pick=` scheme |
| `Verb` | `Name() string; Carriers() []Carrier; Keys() KeySpec; Check(ref Ref, st *State) []Finding; Render(ref Ref, st *State) (Node, error)` | a new directive verb |
| `Classifier` | `Classify(old, new Block) []Class` | change classes for a language |
| `Resolver` | `Provider() string; Exists(ctx, addr) (bool, error)` and optionally `Hash(ctx, addr) ([32]byte, error)` | secret address verification; `Hash` is the optional upgrade |
| `RecordSource` | `Kinds() []string; Query(ctx, q Query) ([]Record, error)` | `table` and `cfg query=` |
| `Renderer` | `Render(node Node) ([]byte, error)` | markdown, html, terminal, a site generator's AST |
| `Store` | `Load(ctx) (Ledger, Refs, Acks, error); Save(ctx, …) error` | where state lives; default is the `.ds/` TSV files, alternatives are a database or the workspace index |
| `Notifier` | `Notify(ctx, findings []Finding) error` | Slack, issues, email |
| `Observer` | `OnFinding(Finding); OnAck(Ack)` | audit sinks and metrics |

Every interface accepts and returns standard-library types where one exists: `context.Context`, `fs.FS`, `io.Reader`, `io.Writer`, `time.Time`. None invents a logger or a config type; the caller passes a `*slog.Logger` if it wants logs.

Each primitive has exactly one escape hatch, named, documented, and tested: `WithExtractor` replaces the tier selection entirely; `WithClassifier` replaces classification; `WithStore` replaces persistence. There is no second, quieter way to do any of those.

#### 37.4 Process plugins, for any language

Plugins that are not Go are executables discovered on `PATH` by name, speaking JSON over stdin and stdout, versioned by a `handshake` message:

| Executable | Protocol |
|---|---|
| `ds-pick-<format>` | request `{file, expr}`, response `{value}` or `{range: {start, end, text}}` or `{error}` |
| `ds-resolve-<provider>` | request `{addr, want: "exists"\|"hash"}`, response `{exists, hash?}`; `hash` is the value's SHA-256 as 64 lowercase hex characters, and any other key or shape is refused as a leak; must never return the value |
| `ds-<verb>` | request `{op: "check"\|"render", ref, state_view}`, response `{findings}` or `{node}` |
| `ds-records-<source>` | request `{query}`, response `{records}` |

The library's `procplugin` package implements both sides, so a Go plugin can also be shipped as a process plugin with no extra code. Timeouts, a size cap on responses, and the rule that a resolver response containing anything but existence and a hash is rejected are enforced by the host, not trusted to the plugin.

#### 37.5 A custom binary in twenty lines

```go
package main

import (
    "github.com/ubgo/docsync/cli"
    "github.com/ubgo/docsync/ext/treesitter"
    "example.com/platform/docsync/linear"     // a private verb: ds:ticket id=… renders Linear state
    "example.com/platform/docsync/vaultx"     // a private resolver for an internal secret store
)

func main() {
    cli.Main(
        cli.WithName("pds"),
        cli.WithDefaults(cli.Standard()),      // the built-in verbs, extractors, and commands
        cli.WithExtractor(treesitter.Kotlin()),
        cli.WithVerb(linear.Verb{}),
        cli.WithResolver(vaultx.New()),
        cli.WithConfigDefaults(platformDefaults),
    )
}
```

`pds` reads the same directives, ledgers, and workspace index as `ds`; only its registry differs. Conformance fixtures run against any binary built this way, so a custom build is provably still docsync.

#### 37.6 Invariants pinned by tests

- Parse → format a directive → parse: identical.
- `Scan` on an unchanged tree produces a byte-identical ledger.
- `Check` with `WithPrevious(nil)` on a tree, then `Check` again with that ledger as previous: every finding is `ok`.
- A resolver that returns a value fails the conformance run.
- No function in the root module reads the environment, the home directory, or the network. A test enforces it by building with a stub `fs.FS` and a network-denying `http.Transport`.
- The JSON contract is generated from the Go structs and a golden file per shape; a diff fails CI unless `json_format` was bumped.

#### 37.7 Versioning

The Go API follows semver independently of the spec version and the `json_format`. The library states which spec version it implements (`docsync.SpecVersion`) and refuses a ledger `format` it does not know. Breaking Go API changes wait for a major version; new capabilities arrive as new optional interfaces so existing plugins keep compiling.

### 38. Build order

1. Root library first: directive parser, ids, ledger model, matching, findings, text and markdown extractors, `Scan`, `Check`, `Define` returning edits, process-plugin protocol; conformance fixtures for Parts III and IV run against the library. Then `ext/treesitter` for Go, TypeScript, Python, SQL and `ext/structured` for yaml and toml. Then the `cli` module: `init doctor def scan check ack refresh render`, verbs `def block cfg`, single-repo workspace, every subcommand a thin call into the library.
2. `publish`, `sync`, git index, multi-repo `check`, `--tests`.
3. GitHub action posting findings as PR comments.
4. `run`, `table`, `claim`, `url`, `chain`; `env=`, `from=`, `truth=`; resolvers for GitHub and 1Password behind `--resolve`; scheduled job.
5. Change classification with stability policies; `impact`, `triage`, `context`, `facts`, `status`, `graph`, `undo`, `render --at`, `adopt`, `report --metrics --literals`, `notify`, `audit`, `review --ai`; `translates=`, `assert=`, `deprecated=`, `sunset=`, `review_every`, `policy.require_doc`, asset hashing, `cfg query=`. Suite reaches full coverage of this document.
6. Agent surface: `mcp`, `map`, `find`, `read`, `locate`, `context --budget --since`, `report --gaps`, `init --agents`, `why --history`, delegated acks, injection fixtures, `json_format = 1` frozen.
7. LSP with code lens and hover; VS Code client.
8. Hugo and Docusaurus plugins for build-time rendering; `ds render` as the generic fallback.
9. AI rules shipped as a `CLAUDE.md` fragment and a skill, generated by `init --agents`.

### 39. Glossary

| Term | Meaning |
|---|---|
| block | a unit worth citing: a declaration, a config key, a statement, a markdown section or paragraph, a line range, a whole asset file |
| def | the directive that gives a block its identity |
| cite, reference | any directive that points at a def |
| fact | a def whose text is one line |
| chain | the declared path of a value from its truth through its copies |
| truth | the single authoritative def in a chain |
| ledger | the generated table of every def with its location and hash |
| refs | the generated reverse index of every reference |
| index | the workspace-wide collection of every repo's published ledger and refs |
| ack | a recorded judgment that a sentence is true for a given block hash |
| finding | one row of `check` output: a doc line, a state, a class, a diff |
| carrier | the comment syntax or link target a directive sits in |
| extractor, pick | how a value or range is taken out of a bound block |
| resolver | a plugin that confirms an external address exists, without storing its value |
| agent surface | the MCP tools, JSON contract, and unattended-action rules in section 26 |
| delegated ack | an ack recorded by an agent on behalf of a named human |
| capability interface | a small Go interface a plugin implements to add an extractor, verb, resolver, record source, renderer, store, or notifier |
| process plugin | an executable named `ds-<kind>-<name>` speaking JSON over stdin and stdout, for plugins in any language |

---

*End of specification 1.0.*
