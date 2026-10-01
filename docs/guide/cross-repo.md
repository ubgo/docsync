# Cross-repo workspaces

This page shows how a citation in one repository is checked against a block defined in another: a docs repository publishes its blocks, a code repository cites them, and each side hears when the other changes. It is for teams whose docs, specs, and services live in separate repositories, and for anyone deciding how CI should treat upstream changes.

Every command and output below was captured from two throwaway git repositories, `docs` and `api`, sharing an index directory. The ids are the ones `ds def` minted there; yours will differ.

## The pieces

| Piece | What it is |
|---|---|
| index | where repositories meet: a directory on disk, or a git repository that `ds` clones into `.ds/index/`. Each repository writes only `repos/<name>/` in it |
| `ds-workspace.toml` | an optional file at the root of the index naming the workspace and listing its repositories |
| `workspace = "…"` | the top-level key in each repository's `.ds/config.toml` that points at the index |
| `ds publish` | writes this repository's ledger, refs, block bodies, and optionally test outcomes into the index; default branch only |
| `ds sync` | fetches the index and rewrites `.ds/foreign.tsv`, the committed record of every foreign block this repository cites |
| `ds check --frozen` | checks foreign citations against `.ds/foreign.tsv` without touching the index; the default when `CI` is set |

Ids are unique across the workspace, so a citation never names the repository: `ds:block?id=backoff-zztatnzq` resolves to whichever repository publishes that id.

## Set up the index

The index here is a plain directory next to both repositories. A workspace file is optional, but with one `ds publish` refuses any repository it does not list, which is what keeps a fork from overwriting the real repository's ledger.

<!-- doctest
mkdir -p ../index ../docs/spec ../api/retry
git -C ../docs init -q -b main .
git -C ../api init -q -b main .
ds --dir ../docs init
ds --dir ../api init
-->

```toml file=../index/ds-workspace.toml
[workspace]
name = "platform"
repos = ["github.com/org/docs", "github.com/org/api"]
default_branch = "main"
```

The keys are `name` and `repos` (both required), `default_branch` (the only branch `publish` runs from; `main` when unset), and `[workspace.id]` and `[workspace.env]` tables. `index` and `stale_after_commits` are accepted but change nothing in this build: each repository finds the index through its own `workspace` key, and the only staleness warning is by age (`index for docs is N days old` after seven days without a publish). A repository's name in the index is the last segment of its URL, so two listed repositories with the same last segment are refused when the file loads. Without a git remote, a repository's name is its directory name, and `publish` requires that name to be listed.

Each repository then points at the index from its own config. The key is top-level, so it goes above the first `[table]`. This is the config `ds init` wrote in the docs repository, with that one line added:

```toml file=../docs/.ds/config.toml
spec = "1.0"
prefix = "ds"
workspace = "../index"

[scan]
code = ["**"]
docs = ["**/*.md"]
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

<!-- doctest
cp ../docs/.ds/config.toml ../api/.ds/config.toml
-->

The `api` repository gets the same line. A relative path is resolved against the repository root.

## Publish from the docs repository

The docs repository starts with one spec page:

```markdown file=../docs/spec/retries.md
# Retries

## Backoff

A failed call is retried at most 5 times, doubling the wait each time.
```

```console
$ cd ../docs
$ ds def 'spec/retries.md#Backoff' --label backoff
backoff-zztatnzq
$ cat spec/retries.md
# Retries

<!-- ds:def id=backoff-zztatnzq -->
## Backoff

A failed call is retried at most 5 times, doubling the wait each time.
$ ds scan
1 files, 1 defs, 0 refs, 0 problems, 0 skipped
$ git add -A && git commit -qm 'retry spec'
$ ds publish
published docs: 1 defs, 0 refs, 0 test outcomes into ../index
$ find ../index -type f | sort
../index/ds-workspace.toml
../index/repos/docs/blocks/b0f1c9545a72b6ca037227506866f872c2921fc7ba489d88dbd1dfc20fd37e8a
../index/repos/docs/ledger.tsv
../index/repos/docs/refs.tsv
```

Publish after committing: the published ledger records the commit it was scanned at, and permalinks resolve against it. Block bodies go into `repos/<name>/blocks/` so another repository can show a diff for a change it never scanned; secret and local blocks publish a hash and no body.

## Cite it from the code repository

The citing side writes an ordinary citation. Here it is a comment on the constant that implements the spec:

```go file=../api/retry/retry.go
package retry

// MaxAttempts follows the retry spec: implements ds:block?id=backoff-zztatnzq
const MaxAttempts = 5
```

Scan first, so `refs.tsv` knows what is cited, then sync to record the foreign blocks, then commit both:

```console
$ cd ../api
$ ds scan
1 files, 0 defs, 1 refs, 0 problems, 0 skipped
$ ds sync
REPO  COMMIT   PUBLISHED             DEFS  REFS
docs  61a2c79  2026-10-01T03:33:50Z  1     0
  + backoff-zztatnzq now cited at b0f1c95
$ cat .ds/foreign.tsv
# docsync foreign format=2 extract=1 repo=api commit= scanned_at=2026-10-01T03:34:01Z
id	repo	commit	kind	file	symbol	lines	hash	owner	stability	env	args
backoff-zztatnzq	docs	61a2c79	section	spec/retries.md	Backoff	4-6	b0f1c9545a72b6ca037227506866f872c2921fc7ba489d88dbd1dfc20fd37e8a		stable		id=backoff-zztatnzq repo=docs
$ git add -A && git commit -qm retry
$ ds status
snapshot  .ds/foreign.tsv   synced just now
  docs     up to date
retry/retry.go:3	ok	backoff-zztatnzq
```

Run `ds sync` before the first scan and it records nothing (`foreign.tsv: no change to record`), because no citation exists yet. `foreign.tsv` holds only the ids this repository cites, so an unrelated upstream edit never churns it.

Then publish the citing repository too. That is how the docs repository learns who depends on its blocks:

```console
$ ds publish --dry-run
would publish api: 0 defs, 1 refs, 0 test outcomes into ../index
index would change: 1 refs added
$ ds publish
published api: 0 defs, 1 refs, 0 test outcomes into ../index
```

## When upstream changes

An author edits the spec in the docs repository. Before committing, `ds impact` already names the citation in the other repository:

```console
$ cd ../docs
$ perl -pi -e 's/at most 5 times/at most 3 times/' spec/retries.md
$ ds impact
retry/retry.go (1)
  3  unacked  backoff-zztatnzq
owner (none): 1
```

`ds check` in the docs repository fails on the same citation:

```console
$ ds check
retry/retry.go
  3	error    unacked            backoff-zztatnzq changed (body) since this sentence was first cited
      still true: ds ack backoff-zztatnzq --doc retry/retry.go --line 3 --note '…'
      otherwise:  edit the sentence at retry/retry.go:3, then ack
1 error
```

`retry/retry.go` is a file in the `api` repository, not this one. The text output does not say so; `ds check --json` does, with `"doc_repo": "api"` on the finding. The ack has to be recorded in the repository that holds the citation; run in the docs repository it is refused with `no reference at that doc line`. So the docs author's choices are to coordinate with the citing repository, or to publish and let the citing repository review the change on its own schedule, which is what the snapshot is for.

The docs repository commits and publishes:

```console
$ ds scan && git add -A && git commit -qm 'three retries' && ds publish
1 files, 1 defs, 0 refs, 0 problems, 0 skipped
published docs: 1 defs, 0 refs, 0 test outcomes into ../index
```

In the citing repository, three commands now give three different, deliberate answers:

```console
$ cd ../api
$ ds check --frozen          # what CI runs: the committed snapshot, unchanged
1 none
$ ds status                  # how far the snapshot is behind; never changes the exit code
snapshot  .ds/foreign.tsv   synced just now
  docs     1 of 1 cited blocks behind upstream   (snapshot 61a2c79 -> index a9d5509)
            backoff-zztatnzq   b0f1c95 -> 0494cb1   body
retry/retry.go:3	unacked	backoff-zztatnzq
$ ds check                   # interactive: reads the index first
retry/retry.go
  3	error    unacked            backoff-zztatnzq changed (body) since this sentence was first cited
      still true: ds ack backoff-zztatnzq --doc retry/retry.go --line 3 --note '…'
      otherwise:  edit the sentence at retry/retry.go:3, then ack
1 error
```

A plain `ds check` merges the index but never writes `foreign.tsv`. Taking the change is `ds sync`, which shows up in the diff of the pull request that reviews it:

```console
$ ds sync
REPO  COMMIT   PUBLISHED             DEFS  REFS
api   1081ae3  2026-10-01T03:34:07Z  0     1
docs  a9d5509  2026-10-01T03:34:26Z  1     0
  ~ backoff-zztatnzq b0f1c95 -> 0494cb1
$ git diff --stat
 .ds/foreign.tsv | 4 ++--
 1 file changed, 2 insertions(+), 2 deletions(-)
$ CI=true ds check
retry/retry.go
  3	error    unacked            backoff-zztatnzq changed (body) since this sentence was first cited
…
1 error
```

The sync summary marks each cited block `+` newly cited, `~` content changed, `>` moved with the same content, or `-` no longer published. Fix the code, ack the sentence, scan, commit, and republish so the docs repository sees the ack:

```console
$ perl -pi -e 's/= 5/= 3/' retry/retry.go
$ ds ack backoff-zztatnzq --doc retry/retry.go --line 3 --note 'spec lowered retries to 3; constant updated'
acked backoff-zztatnzq at retry/retry.go:3 (human)
$ ds scan && git add -A && git commit -qm 'follow spec'
1 files, 0 defs, 1 refs, 0 problems, 0 skipped
$ CI=true ds check
1 none
$ ds publish
published api: 0 defs, 1 refs, 0 test outcomes into ../index
$ cd ../docs && ds check
1 none
```

A block that moved upstream with its content unchanged is reported as `moved` (severity none) by a syncing check, and a block the upstream deletes turns its citations `broken` after the next sync.

## Frozen or syncing

| Mode | When | Reads | Gives |
|---|---|---|---|
| `ds check --frozen` | default when the `CI` environment variable is set | `.ds/foreign.tsv` only, no network | the same answer for the same commit on any machine, forever |
| `ds check` / `ds check --sync` | default interactively; `--sync` forces it under CI | the index, fetched first | upstream's current state |

Asking for both is a usage error (`--frozen and --sync ask for opposite things`). Two frozen failures are worth recognising:

```console
$ rm .ds/foreign.tsv; ds check --frozen
ds: .ds/foreign.tsv not found; run `ds sync` to record the foreign blocks this repo cites
```

<!-- doctest
git show HEAD:.ds/foreign.tsv > .ds/foreign.tsv
cd ../docs
printf '\n## Timeouts\n\nEach attempt times out after 2 seconds.\n' >> spec/retries.md
ds def 'spec/retries.md#Timeouts' --label timeout
ds scan
git add -A
git commit -qm timeouts
ds publish
cd ../api
printf '\n// AttemptTimeout: implements ds:block?id=%s\nconst AttemptTimeout = 2\n' $(grep -o 'timeout-[a-z0-9]*' ../docs/spec/retries.md) >> retry/retry.go
ds scan
git add -A
git commit -qm timeout
-->

```console
$ CI=true ds check           # a new cross-repo citation, committed before anyone synced
retry/retry.go
  6	error    broken             timeout-peqncuha is cited but is not recorded in foreign.tsv
      fix: run `ds sync` and commit foreign.tsv to record it; if no repo in the workspace publishes timeout-peqncuha, fix the id in retry/retry.go:6
1 error, 1 none
```

<!-- doctest
ds sync
git add -A
git commit -qm sync
-->

Both are fixed the same way: `ds sync`, then commit `.ds/foreign.tsv`. CI never syncs for you, because a build that turns red for a change outside its own diff cannot be bisected. To be warned when the snapshot gets old, set `snapshot_max_age` under `[check]`, for example `"30d"`; `ds check --frozen` then prints a warning naming `check.snapshot_max_age`, and the exit code is unchanged even with `--strict`.

## A git-hosted index

Point `workspace` at a git URL instead of a directory and `ds` clones it into `.ds/index/` (machine-local and ignored), pulls before syncing, and commits and pushes after publishing:

```toml
workspace = "https://github.com/org/ds-index"
```

Here the index is a bare git repository on disk, reached through a `file://` URL, and the publishing repository is a third one, `handbook`, listed in that index's workspace file:

<!-- doctest
git init -q --bare -b main ../index.git
git clone -q ../index.git ../seed
printf '[workspace]\nname = "platform"\nrepos = ["github.com/org/handbook"]\n' > ../seed/ds-workspace.toml
git -C ../seed add -A
git -C ../seed commit -qm workspace
git -C ../seed push -q origin main
mkdir -p ../handbook
git -C ../handbook init -q -b main .
ds --dir ../handbook init
perl -pi -e 's|^prefix = "ds"$|prefix = "ds"\nworkspace = "file://'"$(cd .. && pwd -P)"'/index.git"|' ../handbook/.ds/config.toml
printf '# Handbook\n\n## On call\n\nPages go to the primary first.\n' > ../handbook/oncall.md
-->

```console
$ cd ../handbook
$ ds sync
cloned file://…
REPO  COMMIT  PUBLISHED  DEFS  REFS
foreign.tsv: no change to record
$ ds def 'oncall.md#On call' --label oncall
oncall-q2w3e4r5
$ ds scan && git add -A && git commit -qm handbook
1 files, 1 defs, 0 refs, 0 problems, 0 skipped
$ ds publish
published handbook: 1 defs, 0 refs, 0 test outcomes into .ds/index
pushed
```

Run `ds sync` once in a fresh checkout before anything else. Until the clone exists, commands that load the workspace, such as `ds def` and `ds scan`, stop with `workspace index unreachable and no cached copy`. After that, a failed fetch is a warning and the cached copy is used. A local directory needs no clone and no push. Private index repositories use your existing git credentials.

## Release branches

A release branch can publish its own view of the blocks, so release notes cite what shipped rather than what `main` says today. `publish` refuses a branch that is not the default; `--branch` publishes it under its own directory:

```console
$ cd ../docs
$ git switch -q -c release-1
$ ds publish
ds: publish runs only on the default branch (§21); pass --force to override: on "release-1", default is "main"
$ ds publish --branch
published docs: 2 defs, 0 refs, 0 test outcomes into ../index
$ find ../index/repos -maxdepth 2 | sort
…
../index/repos/docs/@release-1
../index/repos/docs/blocks
../index/repos/docs/ledger.tsv
../index/repos/docs/refs.tsv
$ git switch -q main
```

A citation selects the branch with `branch=`:

```markdown file=../api/docs/release-1.md
# Release 1 notes

Release 1 retries a failed call [three times](ds:block?id=backoff-zztatnzq&branch=release-1).
```

<!-- doctest
cd ../api
ds scan
ds sync
git add -A
git commit -qm release-notes
cd ../docs
perl -pi -e 's/at most 3 times/at most 4 times/' spec/retries.md
ds scan
git add -A
git commit -qm four
ds publish
cd ../api
-->

After `main` changed the same block again, only the citation of `main` flags:

```console
$ ds status
snapshot  .ds/foreign.tsv   synced just now
  docs     1 of 3 cited blocks behind upstream   (snapshot cb4e5ee -> index ff05734)
            backoff-zztatnzq   0494cb1 -> 0daa4c4   body
docs/release-1.md:3	ok	backoff-zztatnzq
retry/retry.go:3	unacked	backoff-zztatnzq
retry/retry.go:6	ok	timeout-peqncuha
```

`--force` publishes a non-default branch as if it were the default; reach for `--branch` instead unless you mean that.

## Repo mode: copies for docs read raw on GitHub

By default the repository never holds a copy of a block; `ds render` and the site integrations expand citations at build time. Teams whose docs are read as raw markdown can opt into committed copies instead:

```toml
[include]
mode = "repo"
```

This example is a fourth, single repository, `limits`, with no workspace:

```go file=../limits/limits.go
package limits

// ds:def id=maxbody-k7m2p4xq
func MaxBody() int {
	return 1 << 20
}
```

```markdown file=../limits/docs/limits.md
# Limits

The request body cap:

<!-- ds:block id=maxbody-k7m2p4xq -->

Larger bodies are rejected.
```

<!-- doctest
cd ../limits
git init -q -b main .
ds init
perl -pi -e 's/^mode = "build"/mode = "repo"/' .ds/config.toml
ds scan
-->

`ds refresh` then writes each block-position citation's current content between the directive and a closing marker that carries the block's short hash:

~~~~console
$ ds refresh
1 repo-mode copies rewritten
0 moved; ledger updated
$ cat docs/limits.md
# Limits

The request body cap:

<!-- ds:block id=maxbody-k7m2p4xq -->
**MaxBody** · [`limits.go:4-6`](limits.go#L4-L6)

```go
func MaxBody() int {
	return 1 << 20
}
```
<!-- /ds:block hash=eb672b -->

Larger bodies are rejected.
~~~~

<!-- doctest
git add -A
git commit -qm limits
perl -pi -e 's/1 << 20/2 << 20/' limits.go
ds scan
-->

`ds check` compares each copy with its source. A copy whose source moved on is `stale`; a copy someone edited by hand is `tampered`. Both are fixed by `ds refresh`, which never needs a human to type a fence:

```console
$ ds check                    # after MaxBody changed to 2 << 20 and a scan
docs/limits.md
  5	error    stale              copy rendered from eb672b, block is now cde7f3
      fix: the copy at docs/limits.md:5 was rendered from an older maxbody-k7m2p4xq; run `ds refresh`
1 error
$ ds refresh
1 repo-mode copies rewritten
0 moved; ledger updated
$ perl -pi -e 's/2 << 20/4 << 20/' docs/limits.md
$ ds check                    # after editing inside the copy
docs/limits.md
  5	error    tampered           copy differs from what refresh would write
      fix: the copy at docs/limits.md:5 was edited by hand; edit the source block instead, then `ds refresh`
1 error
```

`ds refresh --dry-run` reports moved blocks and writes nothing. Two limits seen in testing: a copy of a block from another repository is written as its title and a link only, with no body, and the link is relative to the defining repository, so it does not resolve inside the citing one; and `ds render` on a page that already holds copies prints each block twice. Use repo mode for blocks in the same repository, and keep build-time rendering for cross-repo pages.

## What to commit

In a citing repository, commit `.ds/foreign.tsv` alongside the ledger, refs, acks, and `blocks/`. Never commit `.ds/index/`, the clone of a git-hosted index; `ds init` puts it in `.ds/.gitignore`. The index itself is written only by `ds publish` from the default branch (or `--branch`), so a pull request never changes what other repositories see.

See also: [CI](ci.md) for wiring the frozen check and the publish step into a pipeline, and [the spec](../SPEC.md#21-workspaces) for the full rules.
