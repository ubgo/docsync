# Troubleshooting

This page explains what `ds doctor` checks, what every finding state from `ds check` means and how to clear it, and how to recover from the mistakes people most often hit. It is for anyone looking at red output and wanting to know what to do next.

All output below was captured from `ds` in throwaway repositories. Most of it comes from one small repository, a constant and a function in Go, a JSON config, and a page citing them:

```go file=internal/limits.go
package internal

// MaxRetries bounds retries.
// ds:def id=maxretries-ybq9jjbb
const MaxRetries = 5

// ds:def id=backoff-k9gez332
func Backoff(n int) int {
	return n * 2
}
```

```json file=config.json
{
  "port": 8080
}
```

```markdown file=docs/limits.md
# Limits

We retry at most [5](ds:cfg?id=maxretries-ybq9jjbb) times.

<!-- ds:def id=api-port-h3v8n2wd file=config.json pick=json:$.port type=int -->

The API listens on [8080](ds:cfg?id=api-port-h3v8n2wd).

See [Backoff](ds:block?id=backoff-k9gez332) for the doubling.
```

<!-- doctest
git init -q -b main .
ds init
ds scan
git add -A
git commit -qm init
-->

## Start with ds doctor

`ds doctor` checks the setup rather than the docs: the config, every scan glob, the tiers that will extract files, the ledger's format and extraction rule, git, and the `.ds/` housekeeping files.

```console
$ ds doctor
config          ok    spec 1.0, prefix ds
glob **         ok    3 files
glob docs/**    ok    1 files
glob README.md  WARN  matches no files
extractors      ok    sql, python, javascript, tsx, typescript, go, hcl, toml, yaml, markdown, document, config, code, text
ledger          ok    format 2
git             ok    HEAD 18d7b18
gitignore       ok    machine-local state excluded
gitattributes   ok    acks.tsv merges without conflicts
blocks          ok    3 bodies, 3 live
notify          ok    no state yet (first notify will create .ds/notified.json)
```

It exits non-zero when any row is `FAIL`, so a setup script can run it as a gate; a `WARN` does not change the exit code.

| Row | A WARN or FAIL means | Fix |
|---|---|---|
| `config` | `.ds/config.toml` did not parse; unknown keys are errors, and duration keys must parse | fix the key it names |
| `glob …` | a `[scan]` pattern matches nothing (WARN) or is malformed (FAIL) | correct the pattern, or drop it |
| `ledger` | the ledger or refs were written under a different extraction rule; see [A newer extraction rule](#a-newer-extraction-rule) | as the row says |
| `git` | not a git repository, or no commit yet: no permalinks and no diffs | `git init` and commit |
| `gitignore` | `.ds/.gitignore` misses machine-local files, which would then be committed | add the lines it names; never `ds init --force` |
| `gitattributes` | `acks.tsv merge=union` is missing, so acks on two branches conflict at merge | add the line it names |
| `blocks` | how many stored block bodies are still needed | `ds prune --dry-run` shows what could go |

The two housekeeping rows look like this when a repository predates them:

<!-- doctest
perl -ni -e 'print unless /^(cache\/|journal\.tsv)$/' .ds/.gitignore
rm .ds/.gitattributes
-->

```console
$ ds doctor
…
gitignore       WARN  .ds/.gitignore does not cover cache/ journal.tsv; that state would be committed. Add those lines to it
gitattributes   WARN  .ds/.gitattributes does not cover acks.tsv merge=union; two branches that each record an ack will conflict when merged. Add those lines to it
…
```

<!-- doctest
git show HEAD:.ds/.gitignore > .ds/.gitignore
git show HEAD:.ds/.gitattributes > .ds/.gitattributes
-->

If a machine-local file was already committed before the ignore line existed, `git rm --cached` it as well; the ignore alone does not untrack it.

## Reading a finding

When `MaxRetries` changes and the page does not:

```console
$ perl -pi -e 's/MaxRetries = 5/MaxRetries = 3/' internal/limits.go
$ ds check
docs/limits.md
  3	error    unacked            maxretries-ybq9jjbb changed (body) since this sentence was first cited
      still true: ds ack maxretries-ybq9jjbb --doc docs/limits.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/limits.md:3, then ack
1 error, 2 none
```

Findings are grouped by the file holding the citation, then by line. Each has a severity, a state, a message, and the exact command or edit that clears it: `unacked` offers two (`still true:` and `otherwise:`), every other state one (`fix:`). The last line counts findings by severity. `ds check` exits 1 when any finding is an `error`; `--strict` makes warnings fail too, except `ok`, `moved`, `deprecated`, `skipped`, and `uncovered`, which never affect the exit code. `ds check --json` carries the same data in the stable machine format, and `ds check --explain` first lists every directive the scan matched with its tier and carrier, which is the quickest way to see whether a directive was read at all.

`[check] unacked = "warn"` in config downgrades `unacked` to a warning, which is useful while a team adopts the tool:

```console
$ perl -pi -e 's/^unacked = "error"/unacked = "warn"/' .ds/config.toml
$ ds check; echo "exit=$?"
docs/limits.md
  3	warning  unacked            maxretries-ybq9jjbb changed (body) since this sentence was first cited
      still true: ds ack maxretries-ybq9jjbb --doc docs/limits.md --line 3 --note '…'
      otherwise:  edit the sentence at docs/limits.md:3, then ack
1 warning, 2 none
exit=0
```

<!-- doctest
git show HEAD:.ds/config.toml > .ds/config.toml
git show HEAD:internal/limits.go > internal/limits.go
-->

## Every finding state

This is every state in `check.StateValues`, with the severity and fix the code assigns.

| State | Severity | Means | Clear it by |
|---|---|---|---|
| `ok` | none | the citation's block still has the hash it was acked at, or first seen at | nothing |
| `moved` | none | same content, new file or lines; the ledger follows | nothing |
| `unacked` | error (`warn` by config) | the block changed since the sentence was acked or first cited, or the sentence was rewritten since its ack; carries the change class and diff | `ds ack <id> --doc D --line N --note …` if still true; otherwise edit the sentence, then ack |
| `broken` | error | the id is not defined (with "did you mean" for near misses), was deleted, has no def for the cited environment, its repo left the workspace, its `at=` commit does not exist, or a frozen run has no record of it | fix the id, re-add the def, or `ds sync` for a foreign id |
| `pick failed` | error | a def's `pick=` or remote `file=` yields nothing, or many lines where one is needed | fix the def's `file=` or `pick=` |
| `too-large` | error | a block-position citation would render more than `include.max_lines` (default 40) | add `lines=a-b`, or cite it as a link |
| `range` | warning | `lines=` reaches past the block's end, or `ds:cfg` points at a multi-line block | adjust `lines=`, or use `ds:block` or a narrower `pick=` |
| `expired` | error | a `ds:claim` is past `reviewed` plus `expires`, a claim's `about=` id changed, or a page is past `review_every` | reread it, then `ds ack --doc D --line N` (claim) or `ds ack --doc D` (page) |
| `sunset` | error | a citation of a def past its `sunset=` date | remove the citation |
| `deprecated` | info | a citation of a def past its `deprecated=` date | plan to move the citation |
| `assert failed` | error | a test cited with `assert=true` failed, was skipped, or is missing in the last published run | fix the test or the sentence |
| `translation stale` | error | the source paragraph of a `translates=true` citation changed | update the translation, then ack |
| `unsourced` | warning | a secret def with no `from=` and no `truth=true` | declare where it comes from, or mark it the truth |
| `chain broken` | error | a chain has zero or several `truth=true` defs, a `from=` names an undefined id, or a `from=` cycle | fix the chain |
| `unverifiable` | warning | could not be checked here: a `local=true` def whose file is absent here, a `ds:url` without `--resolve` or whose request got no answer, a resolver plugin missing, not logged in, or failing, an `at=` snapshot with no git to look it up, a `ds:table` or `ds:cfg query=` with no record source | nothing required; run where it can be checked, or configure `[records]` |
| `skipped` | info | a `ds:run` not executed because `--run` was not given | pass `--run` where runs are enabled |
| `orphan` | warning | a page's `covers` names an id that is not defined | remove it from `covers`, or restore the def |
| `uncovered` | info | a def nothing cites or covers | cite it, or remove the def |
| `unknown` | warning (error with `--strict`) | an unknown verb or key in a directive | fix the spelling, or register the verb |
| `problem` | error | the scan could not use a directive: a duplicated id, a bare directive outside a comment, a missing key, a def in a generated file | as the message says; see below |
| `dead` | error | a `ds:url` returned 400 or above, or a status other than its `expect=` | update or remove the link |
| `retitled` | warning | a `ds:url` page title no longer contains `title=` | confirm it is still the right page |
| `url moved` | warning | a `ds:url` redirects | update the link |
| `undocumented export` | error | `[policy] require_doc` covers the file and an exported symbol in it has no def | `ds def <file>#<symbol>` and cite it |
| `resolve failed` | error | with `--resolve`, a secret address does not exist at its provider | fix the address |
| `out of sync` | error | with `--resolve`, a copy's hash differs from its truth's | run the sync job, then ack the runbooks |
| `stale copy` | error | with `--resolve`, after the truth was rotated, a copy still holds its previous value | run the sync job of that copy |
| `rotated` | warning | with `--resolve` and `store_hash`, the truth changed since its hash was stored | run the syncs of its copies, ack the runbooks |
| `stale` | error | a repo-mode copy was rendered from an older version of its block | `ds refresh` |
| `tampered` | error | a repo-mode copy was edited by hand | edit the source block, then `ds refresh` |
| `unscanned` | error | a file that held citations or blocks could not be read this time (too large, a line too long outside prose, binary, unreadable); its last state is kept | make it readable, or raise the `[scan.limits]` value named |

[Secrets, runs and URLs](secrets-and-runs.md) shows the run, URL, and resolver states in context; [Cross-repo workspaces](cross-repo.md) and the repo-mode section there show `stale`, `tampered`, and the frozen `broken`.

### What the common ones look like

Several states from one page, caught in one run:

```markdown file=docs/states.md
---
ds:
  covers: [maxretries-ybq9jjbb, gone-a2b3c4d5]
---

# States

We retry [MaxRetries](ds:block?id=maxretries-ybq9jjbb) times.

A typo: [MaxRetries](ds:block?id=maxretries-ybq9jjbc).

<!-- ds:block id=maxretries-ybq9jjbb lines=3-9 -->

We chose five. <!-- ds:claim owner=@platform reviewed=2026-01-01 expires=90d -->

Unknown verb: <!-- ds:frobnicate id=maxretries-ybq9jjbb -->

Unknown key: [x](ds:block?id=maxretries-ybq9jjbb&colour=red).
```

```console
$ ds scan
…
$ ds check
docs/states.md
  1	warning  orphan             covers gone-a2b3c4d5, which is not defined
      fix: the page docs/states.md covers gone-a2b3c4d5, which is not defined; remove it from covers or restore the def
  10	error    broken             maxretries-ybq9jjbc is not defined; did you mean maxretries-ybq9jjbb
      fix: the id maxretries-ybq9jjbc is not defined; fix the id in docs/states.md:10 or re-add the ds:def on the block it meant
  12	warning  range              lines=3-9 outside the block's 1 lines
      fix: adjust lines= at docs/states.md:12 to fit the block's 1 lines
  14	error    expired            claim reviewed 2026-01-01 expired after 90d
      fix: review the claim at docs/states.md:14 and run ds ack --doc docs/states.md --line 14 to renew it
  16	warning  unknown            unknown verb "frobnicate"
      fix: frobnicate is not a registered verb; register a handler or fix the directive at docs/states.md:16
  18	warning  unknown            unknown key(s) colour on ds:block
      fix: unknown key(s) colour on ds:block at docs/states.md:18; check the spelling against the verb's key table
2 error, 4 warning, 5 none
```

<!-- doctest
rm docs/states.md
ds scan
-->

Moving code, and renaming the key a remote def reads. `Backoff` moves, unchanged, to a file of its own, and `config.json` renames `port` to `listen`:

```go file=internal/limits.go
package internal

// MaxRetries bounds retries.
// ds:def id=maxretries-ybq9jjbb
const MaxRetries = 5
```

```go file=internal/backoff.go
package internal

// ds:def id=backoff-k9gez332
func Backoff(n int) int {
	return n * 2
}
```

```console
$ perl -pi -e 's/"port"/"listen"/' config.json
$ ds check
docs/limits.md
  5	error    pick failed        scan: remote def pick failed: config.json: pick: nothing matched: json path "$.port": no key "port"
      fix: fix the def's file= or pick= at docs/limits.md:5
  7	error    broken             api-port-h3v8n2wd was deleted (last seen config.json:1)
      fix: the id api-port-h3v8n2wd is not defined; fix the id in docs/limits.md:7 or re-add the ds:def on the block it meant
  9	none     moved              moved from internal/limits.go:8-10
2 error, 2 none
```

<!-- doctest
git show HEAD:internal/limits.go > internal/limits.go
git show HEAD:config.json > config.json
rm internal/backoff.go
ds scan
-->

A def that failed its pick also leaves its citations `broken`; fixing the pick fixes both. Lifecycle dates, size limits, and a documentation policy, with `include.max_lines` lowered to 5 and `[policy] require_doc = ["internal/**"]`:

```go file=internal/old.go
package internal

// ds:def id=oldapi-m3n4p5q6 deprecated=2026-01-01
func OldAPI() {}

// ds:def id=olderapi-r7s8t9u2 sunset=2026-06-01
func OlderAPI() {}

// ds:def id=bigfunc-v3w4x5y6
func Big() int {
	a := 1
	a++
	a++
	a++
	a++
	a++
	return a
}

func Jitter() int { return 3 }
```

```markdown file=docs/b.md
# B

Use [OldAPI](ds:block?id=oldapi-m3n4p5q6) for now.

Never [OlderAPI](ds:block?id=olderapi-r7s8t9u2).

<!-- ds:block id=bigfunc-v3w4x5y6 -->

The port is [8080](ds:cfg?id=bigfunc-v3w4x5y6).
```

<!-- doctest
perl -pi -e 's/^max_lines = 40/max_lines = 5/' .ds/config.toml
printf '\n[policy]\nrequire_doc = ["internal/**"]\n' >> .ds/config.toml
ds scan
-->

```console
$ ds check
docs/b.md
  3	info     deprecated         oldapi-m3n4p5q6 deprecated since 2026-01-01
      fix: oldapi-m3n4p5q6 is deprecated since 2026-01-01; plan to move the reference at docs/b.md:3
  5	error    sunset             olderapi-r7s8t9u2 reached sunset 2026-06-01
      fix: olderapi-r7s8t9u2 passed its sunset date 2026-06-01; remove the reference at docs/b.md:5
  7	error    too-large          rendering 9 lines exceeds the cap of 5
      fix: add lines=a-b to show a fragment, or cite it with a link instead of rendering it
  9	warning  range              ds:cfg on a 9-line block
      fix: the def yields 9 lines; ds:cfg needs one line, use ds:block or a narrower pick=
internal/old.go
  20	error    undocumented export Jitter is exported under a require_doc path and has no def
      fix: policy.require_doc covers internal/old.go; run `ds def internal/old.go#Jitter` and cite it from a page
3 error, 1 warning, 1 info, 3 none
```

<!-- doctest
git show HEAD:.ds/config.toml > .ds/config.toml
rm internal/old.go docs/b.md
ds scan
-->

A translation whose source paragraph changed:

```markdown file=docs/policy.md
# Policy

<!-- ds:def id=policy-en-k4m5n6p7 -->
Sessions last thirty days.
```

```markdown file=docs/policy.de.md
# Richtlinie

<!-- ds:block id=policy-en-k4m5n6p7 translates=true -->
Sitzungen dauern dreißig Tage.
```

<!-- doctest
ds scan
git add -A
git commit -qm translation
-->

```console
$ perl -pi -e 's/thirty days/fourteen days/' docs/policy.md
$ ds check
docs/policy.de.md
  3	error    translation stale  source policy-en-k4m5n6p7 changed (body)
      fix: the source paragraph policy-en-k4m5n6p7 changed; update the translation at docs/policy.de.md:3 and ack
…
```

<!-- doctest
git show HEAD:docs/policy.md > docs/policy.md
-->

A file that can no longer be read, here because a NUL byte made it binary:

```console
$ printf 'x\0y\n' >> docs/limits.md
$ ds check
docs/limits.md
  1	error    unscanned          could not be scanned (binary); 3 citations and 0 blocks are not being checked
      fix: docs/limits.md could not be scanned (binary): it contains a NUL byte; if it is text, remove the byte; until it is, the citations in it are not checked and its blocks are kept as they were last seen
internal/limits.go
  4	info     uncovered          defined but never cited or covered
      fix: maxretries-ybq9jjbb is defined but nothing cites or covers it; cite it from a page or remove the def
  7	info     uncovered          defined but never cited or covered
      fix: backoff-k9gez332 is defined but nothing cites or covers it; cite it from a page or remove the def
1 error, 2 info, 1 none
```

While the page cannot be read, the blocks it cited look uncited too; both clear once the file is readable again.

<!-- doctest
git show HEAD:docs/limits.md > docs/limits.md
-->

And a test that failed in the last published run. This needs a workspace, because test outcomes travel with `ds publish --tests`; here it is a second repository, `tests`, publishing to a local index:

<!-- doctest
mkdir -p ../tests ../tests-index
cd ../tests
git init -q -b main .
ds init
perl -pi -e 's|^prefix = "ds"$|prefix = "ds"\nworkspace = "../tests-index"|' .ds/config.toml
-->

```go file=backoff.go
package backoff

func Backoff(n int) int { return n * 2 }
```

```go file=backoff_test.go
package backoff

import "testing"

// ds:def id=test-backoff-b7k2m9qx
func TestBackoff(t *testing.T) {
	if Backoff(2) != 4 {
		t.Fatal("doubling")
	}
}
```

```markdown file=docs/backoff.md
# Backoff

Doubling is covered by [a test](ds:block?id=test-backoff-b7k2m9qx&assert=true).
```

```xml file=junit.xml
<testsuites><testsuite name="backoff"><testcase name="TestBackoff" classname="backoff"><failure message="doubling"/></testcase></testsuite></testsuites>
```

```console
$ ds scan && git add -A && git commit -qm tests
4 files, 1 defs, 1 refs, 0 problems, 0 skipped
$ ds publish --tests junit.xml
published tests: 1 defs, 1 refs, 1 test outcomes into ../tests-index
$ ds check
docs/backoff.md
  3	error    assert failed      test test-backoff-b7k2m9qx: failed
      fix: the cited test test-backoff-b7k2m9qx did not pass in the last published run; fix the test or rewrite the sentence at docs/backoff.md:3
1 error
```

`assert=true` reads outcomes published with `ds publish --tests junit.xml`; a test def is matched to a JUnit test case by its function name, and a defined test with no result counts as skipped, which fails the assertion.

<!-- doctest
cd ../repo
-->

## Common mistakes

### "I ran ds scan and the finding is still there"

That is by design. `ds scan` records where blocks are; it never approves anything. A citation is measured against the hash it was acked at, or the hash it had when first scanned if nobody has acked it, so rescanning cannot make a change disappear. Read the sentence, then `ds ack <id> --doc <doc> --line <n> --note '…'`, or edit it first. Several identical changes can be acked as one decision with `ds triage` and `ds triage --ack-group N`. Reverting the code also clears the finding, because the hash returns to the acked one.

### A directive in a file that has no comments

`ds def` refuses to write into a format with no comment syntax, and says how to bind the value instead:

```console
$ ds def config.json:2
ds: docsync: no comment carrier for this file type: .json has no comment syntax docsync knows, so a directive cannot be written into it; bind it from a file that does with a remote def (`file=config.json pick=…`), or add the type to [scan] if it does have comments
```

The remote def goes in any file that can hold a comment, usually the doc itself, as `docs/limits.md` above does:

```markdown
<!-- ds:def id=api-port-h3v8n2wd file=config.json pick=json:$.port type=int -->

The API listens on [8080](ds:cfg?id=api-port-h3v8n2wd).
```

A tree damaged by an older build, or by hand, can hold bare `ds:def` lines in such files. Here one sits on the first line of `config.json` and one in a `go.work`:

```json file=config.json
ds:def id=port-old-k2m4n6p8
{
  "port": 8080
}
```

```text file=go.work
go 1.26

ds:def id=work-z9y8x7w6
use .
```

`ds scan` and `ds check` name every one, and `ds repair` mends them: it comments the line where the format has comments and deletes it where it has none.

```console
$ ds scan
6 files, 5 defs, 4 refs, 3 problems, 0 skipped
  config.json:1  scan: directive is not inside a comment: config.json has no comment syntax, so this line breaks the file; ds repair --apply removes it (bind the value with a remote def instead)
  docs/limits.md:5  scan: remote def pick failed: config.json: pick: nothing matched: json: invalid character 'd' looking for beginning of value
  go.work:3  scan: directive is not inside a comment: go.work carries comments, so a bare directive line is probably not valid there; ds repair --apply comments it
$ ds repair
config.json:1  delete (no comment syntax here)
  - ds:def id=port-old-k2m4n6p8
go.work:3
  - ds:def id=work-z9y8x7w6
  + // ds:def id=work-z9y8x7w6
2 line(s) would be repaired, 0 need a person (run with --apply to write)
$ ds repair --apply
config.json:1  delete (no comment syntax here)
  - ds:def id=port-old-k2m4n6p8
go.work:3
  - ds:def id=work-z9y8x7w6
  + // ds:def id=work-z9y8x7w6
2 line(s) repaired, 0 need a person; ds undo reverses this, then run ds scan
$ head -1 config.json
{
```

<!-- doctest
rm go.work
ds scan
-->

`ds repair` prints by default and writes only with `--apply`, and `ds undo` puts every line back byte for byte. Plain text (`.txt`) is the exception: there a bare directive line is the correct carrier and is not reported.

### Two defs with the same id

Copying a declaration together with its directive leaves two blocks claiming one id:

```go file=internal/limits.go append=true

// ds:def id=maxretries-ybq9jjbb
const MaxAttempts = 5
```

Every command reports it, and the tool never guesses which copy is the original:

```console
$ ds scan
…
$ ds check
internal/limits.go
  4	error    problem            scan: id defined more than once: maxretries-ybq9jjbb (2 places)
      fix: internal/limits.go:4: scan: id defined more than once: maxretries-ybq9jjbb (2 places); run `ds def --fix` to re-mint every copy after the first (it prints old -> new), or delete the directive from the copy sentences do not mean
  12	error    problem            scan: id defined more than once: maxretries-ybq9jjbb (2 places)
      fix: internal/limits.go:12: scan: id defined more than once: maxretries-ybq9jjbb (2 places); run `ds def --fix` to re-mint every copy after the first (it prints old -> new), or delete the directive from the copy sentences do not mean
2 error, 4 none
$ ds def --fix --dry-run
maxretries-ybq9jjbb@internal/limits.go:12 -> maxretries-ydsm7jm6
1 def(s) would be re-minted (--dry-run)
$ ds def --fix
maxretries-ybq9jjbb@internal/limits.go:12 -> maxretries-w4u7h2qa
1 def(s) re-minted; run ds scan
```

`--fix` keeps the first copy's id, in file and line order, and re-mints the rest; the new id is random, so the dry run and the real run print different ones. If the citations meant the second copy, delete the directive from the first instead. The re-minted id keeps the old label; rename it with `ds rename` if the label now misleads.

### A newer extraction rule

Hashes are only comparable when computed under the same extraction rules, so every ledger records its rule (`extract=` on line 1). A `ds` older than the one that wrote the ledger refuses to touch it, `ds scan` included, rather than rewriting it under the older rule. Here line 1 of the ledger was edited to claim rule 2:

<!-- doctest
ds scan
cp .ds/ledger.tsv ../ledger.bak
perl -pi -e 's/extract=1/extract=2/ if $. == 1' .ds/ledger.tsv
-->

```console
$ ds check
ds: docsync: recorded under a newer extraction rule than this build implements: ledger.tsv says extract=2, this build implements rule 1; upgrade ds; if a pre-release ds wrote it, set extract=1 on line 1 of .ds/ledger.tsv and .ds/refs.tsv, then run `ds scan`
```

<!-- doctest
cp ../ledger.bak .ds/ledger.tsv
-->

Usually the fix is to upgrade `ds` (and to pin the same version in CI as on laptops). Repositories created with builds from before the first release recorded `extract=2` to `4` for what is now rule 1; for those, editing line 1 of both files as the message says is the whole fix. An older rule than the build is the normal upgrade path: `ds check` warns, `ds doctor` shows `WARN`, the next `ds scan` rewrites the files, and the affected citations report as changed once and are acked with a note.

### CI fails on a citation of another repository

Under CI, `ds check` reads foreign blocks only from the committed `.ds/foreign.tsv`. A fresh repository fails until someone syncs, and so does a new cross-repo citation. Here the `tests` repository from above, which has a workspace, gains a citation of an id nothing in it defines:

```markdown file=../tests/docs/retry.md
# Retry

Each attempt [times out](ds:block?id=timeout-peqncuha).
```

```console
$ cd ../tests
$ ds scan
…
$ CI=true ds check
ds: .ds/foreign.tsv not found; run `ds sync` to record the foreign blocks this repo cites
$ ds sync
…
$ CI=true ds check
docs/retry.md
  3	error    broken             timeout-peqncuha is cited but is not recorded in foreign.tsv
      fix: run `ds sync` and commit foreign.tsv to record it; if no repo in the workspace publishes timeout-peqncuha, fix the id in docs/retry.md:3
…
$ cd ../repo
```

Run `ds sync` locally and commit `.ds/foreign.tsv`. Do not make CI sync: a build that goes red because another repository changed cannot be bisected. [Cross-repo workspaces](cross-repo.md#frozen-or-syncing) explains the two modes.

### A git-hosted index says it is unreachable

<!-- doctest
mkdir -p ../fresh
git -C ../fresh init -q -b main .
ds --dir ../fresh init
perl -pi -e 's|^prefix = "ds"$|prefix = "ds"\nworkspace = "file:///nonexistent/ds-index.git"|' ../fresh/.ds/config.toml
-->

```console
$ ds --dir ../fresh scan
ds: workspace index unreachable and no cached copy
```

In a fresh checkout with `workspace` set to a git URL, run `ds sync` once to clone the index into `.ds/index/`. After that, a failed fetch only warns and the cached copy is used.

### Every external link is unverifiable

A `ds check --resolve` run on a machine that cannot reach the hosts reports each link `unverifiable` with the request's error (`external link not checked: … connection refused`) and caches nothing for it, so the next run with a network checks every link again. Builds before this one reported such links `dead … returned 0` and cached that for `url.ttl`; the cache entries they left are ignored, so nothing needs deleting.

### ds:run stops with "the shell is not on PATH"

Commands run under `sh`, or the program named by `[run] shell`. Install it (Git for Windows provides `sh` on Windows) or name a shell that exists. The check stops rather than skipping, because a check asked to run commands that ran none must not look like one whose commands passed.

### A comment that happens to contain ds:

The scanner reads anything starting with the prefix glued to a verb. A codebase whose comments or fixtures already use `ds:` for something else should set another prefix in config, as docsync's own repository does with `prefix = "dsself"`. `ds check --explain` lists everything the scan took for a directive.

## Undoing a source write

`ds def`, `ds def --fix`, `ds adopt`, `ds rename`, and `ds repair --apply` are the only commands that edit source files, and each write is journaled in `.ds/journal.tsv` (machine-local). `ds undo` reverses the most recent one:

```go file=internal/limits.go append=true

const Jitter = 3
```

```console
$ ds def internal/limits.go#Jitter
jitter-82vfr8j8
$ ds undo
undid internal/limits.go:15
next: def internal/limits.go:12 maxretries-w4u7h2qa
```

Every run says what the next entry is, so you can stop before going too far. `ds undo --list` shows the stack and writes nothing; `--dry-run` shows the edit it would make.

The re-minted `MaxAttempts` def from above is now cited:

<!-- doctest
perl -pi -e 'BEGIN { open my $f, "<", "internal/limits.go"; local $/; ($id) = <$f> =~ /(maxretries-(?!ybq9jjbb)[a-z0-9]{8})/ } s/^See \[Backoff\]/Retries are capped by [MaxAttempts](ds:block?id=$id).\n\nSee [Backoff]/' docs/limits.md
ds scan
-->

```console
$ ds undo --list
#  KIND    WHERE                  ID                   AGE       STATE
1  def     internal/limits.go:12  maxretries-w4u7h2qa  just now  uncommitted · cited by docs/limits.md:9
2  repair  config.json:1 +1 more  -                    just now  uncommitted
```

Two guards stop it:

- **A cited def is not removed silently.** If sentences cite the def an entry would remove, in this repository or, through the workspace index, in another, `undo` names them and stops; `--orphan` accepts the breakage.

  ```console
  $ ds undo
  ds: undo would orphan a cited def:
      maxretries-w4u7h2qa is cited by 1 sentence:
        docs/limits.md:9
      removing the def will make it `broken`. Re-run with ds undo --orphan to proceed.
  ```

- **The last commit is the boundary.** A write that is already in `HEAD` is history that others may depend on; reversing it needs `--force`, and is better done as an ordinary edit in a reviewed change.

`ds undo` reverses only `ds` writes. To revert your own edits, use your editor or git as usual, and remember that `git checkout <file>` discards every uncommitted change in that file, not just the last one.

## FAQ

**Why does a sentence flag when I only reformatted the code?** It should not for whitespace: trailing whitespace and line endings are normalised in every tier, and the tree-sitter tier hashes the token stream so re-indentation is invisible. In files without a grammar, such as YAML, indentation is meaning and does count. A comment-only change flags only `stability=frozen` defs.

**Why did a change not flag?** Check the def's `stability`. `api` flags signature, type, rename, value, and unknown changes, but not a body-only change; `volatile` flags nothing. Use `stable` (the default) for a block whose contents are the contract, such as a list of allowed values.

**What does class `unknown` mean?** The block changed, but the earlier body was not available to describe how: it was pruned, never stored, or the block is secret. It flags wherever `api` flags, because an unexplained change could be a signature change.

**Do I ack each citation separately?** An ack is for one sentence: the same id cited in two sentences needs two acks, or one `ds ack <id> --doc D --all` that approves every citation of it in that doc. Rewording a sentence after its ack makes it `unacked` again, with the old and new wording as the diff.

**How do I see what an ack would approve before recording it?** `ds ack … --dry-run` quotes each sentence and the hash it would approve, and records nothing.

**The ack log conflicts on every merge.** `.ds/.gitattributes` is missing `acks.tsv merge=union`; `ds doctor` reports it. Add the line and the union driver keeps both sides' rows; the newest ack per sentence wins by timestamp.

**Which files in `.ds/` go into git?** `config.toml`, `ledger.tsv`, `refs.tsv`, `acks.tsv`, `blocks/`, and in a workspace `foreign.tsv`. Never `cache/`, `index/`, `journal.tsv`, `urls.json`, `runs.json`, `notified.json`, `hashes.json`, `metrics.json`, or `lock`; `cache/` and `journal.tsv` can hold the text of secret blocks.

**`.ds/blocks/` keeps growing.** `ds prune --dry-run` shows how many stored bodies are still live; bodies no ledger, ack, citation, or snapshot needs are removed after a 30-day grace period (`--keep`).

```console
$ ds prune --dry-run
.ds/blocks: 9 bodies, 4 live, 0 dead (0 bytes), 5 within the 30d grace period
removed nothing (--dry-run)
```

**My Go program reports drift that `ds check` does not.** The program extracts with different tiers than the `ds` that wrote the ledger, so the same code hashes differently. Register the structured and tree-sitter tiers; see [Using docsync as a Go library](library.md#register-the-same-tiers-as-the-binary-that-wrote-the-ledger).

**Which build am I running?** `ds version` prints the version, the commit it was built from, and whether that tree had uncommitted changes.

```console
$ ds version
ds v…
```
