# Field findings, 2026-09-29: adopting docsync on five production repositories

Context: `ds` (dev build from this tree, commit 663055b) was adopted on five production repositories (two Go services, a TanStack app, a web frontend and a Shopify extension) in one sitting. Everything below was reproduced on a scratch repository or on those repos; nothing here is inferred. Each item ends with what the user had to do instead.

## Status

Every finding below is fixed, and each is pinned by a case in `scripts/e2e/field-findings.sh` that runs against the built binary, so none can return without `task` going red. That matrix fails 14 of its 28 cases on the build before these fixes, which is how it is known to test what it claims. Nothing below was rewritten; the findings stand as reported.

| # | Finding | Fixed by |
|---|---|---|
| 1 | `adopt` rejects heading links to files that exist | links resolve against the linking file (then the repo root); a named anchor into another page is left alone silently |
| 2 | unknown types get a bare directive | every writer refuses a type with no comment carrier; `.pkl` takes `//` |
| 3 | YAML keys containing a colon are unaddressable | a YAML key ends at a colon followed by space; `tasks.wfsys:up` and `tasks."a.b"` both resolve |
| 4 | `ds init` writes a `push:` CI trigger | `init` and all three shipped workflows trigger manually, with the automatic triggers in a header to paste back |
| 5 | things that worked | pinned, so they keep working |
| 6 | `go.work` broken by a bare directive | `go.mod` and `go.work` take `//`; existing damage is reported and `ds repair --apply` comments it |
| 7 | JSON gets an uncommented insert | refused outright, file untouched, `--dry-run` predicts it |
| 8 | a TS object-literal property cannot bind | object-literal properties bind as `server.port`; any directive that would not bind is refused before the file is touched |

## 1. `ds adopt` rejects markdown heading links to files that exist

Scratch repo: `README.md` with a `## Target` heading, `CONTRIBUTING.md` at the root containing `[t](./README.md#target)`, and `docs/a.md` containing `[t](../README.md#target)`. `ds adopt --dry-run` prints `left alone CONTRIBUTING.md:1 ./README.md#target: file not found` and the same for `docs/a.md`. Both files exist. Two things look wrong: the relative path is resolved against the repository root rather than the directory of the file that holds the link (the `../README.md` case from `docs/` would resolve correctly only from the root), and a `#fragment` that names a heading is treated as a symbol to look up instead of a plain document anchor. `adopt` should leave heading links alone silently; today it leaves them alone with a misleading reason, and a run on one of those repositories printed nine such lines against real links.

Workaround: none needed; the links are unchanged. The noise hides real findings.

## 2. Unknown file types get a bare directive with no comment carrier

`ds def modules/config/pkl/env/sample/app.pkl:46 --dry-run` on an Apple Pkl file (`//` line comments) would insert a line containing only `ds:def id=sample-dbname-bdsqgk5y`, with no comment prefix. Applied, that breaks `pkl eval`. A hand-written `dbName = "demo_db"  // ds:def id=sample-dbname-test0001` on the same line evaluates fine but `ds scan` reports 0 defs, so `.pkl` is not scanned at all. Either outcome is silent.

Suggested behaviour: when the extension has no known comment syntax, refuse the def with a message naming the file type and the `[scan]`/extractor option to add one, never insert an uncommented line. Separately, `//` line comments are the carrier for enough languages (Pkl, Kotlin, Swift, Rust, C family) that the heuristic code tier could accept them by default.

Workaround: none; Pkl config values (database names, ports, feature flags) are the facts our onboarding docs most need to cite, and they cannot be bound today.

## 3. YAML keys containing a colon cannot be addressed

Taskfile task names are keys such as `wfsys:up`, `docs:check`, `dev:portless`. `ds def Taskfile.yml#tasks.wfsys:up`, `tasks."wfsys:up"`, `tasks/wfsys:up` and `wfsys:up` all return `symbol not found`. `tasks.migrate` (no colon) works. There is no documented escape for a colon in a key path.

Workaround: `ds def Taskfile.yml:<line>` by line number, which survives edits above it only through the ledger's move detection.

## 4. `ds init` writes a CI snippet with a `push:` trigger

`.ds/ci-github.yml` proposes `on: pull_request` and `on: push`. For a private repository with metered Actions minutes that is the wrong default to hand out; a `workflow_dispatch:` skeleton with the push lines in a comment would be safer.

## 5. Things that worked first time

Line-addressed defs on YAML (`.docker/compose.wfsys.yml:48` bound the Hatchet image pin), Go symbol defs including methods (`selfcheck.go#Service.StorageSelfCheck`), `ds doctor`, `ds scan` on trees of 3,000 to 3,600 files in a few seconds, `ds report --gaps` ordering files by churn.

## 6. `ds def go.work:<line>` breaks the Go workspace

`ds def go.work:2 --label go-version` (the `go 1.26.4` line) inserted a line reading only `ds:def id=go-version-9pzfnkr2 …` at the top of `go.work`. Every subsequent `go build` in the workspace failed with `go: errors parsing go.work: ../../go.work:1: unknown directive: ds:def`. `go.work` and `go.mod` use `//` line comments, so the carrier exists; the file type simply is not recognised. Rewriting the line by hand to `// ds:def id=…` restored the build, but `ds scan` then reported 9 defs instead of 10: `go.work` is not scanned at all, so the directive is invisible whether or not it is a comment, and the def had to be removed. Same root cause as finding 2: an unknown extension must never receive an uncommented directive, and `go.mod` / `go.work` deserve first-class recognition given how central they are to a Go repository.

## 7. JSON gets the same uncommented insert, and JSON has no comment syntax

`ds def portless.json:2 --dry-run` would insert a bare `ds:def id=…` line into a JSON file. Unlike Pkl and `go.work`, JSON has no comment carrier at all, so there is nothing to fall back to: `def` on a `.json` target must be refused with a pointer to `ds:cfg` style value binding from elsewhere (or a sidecar), never inserted. The dry run is the only reason this did not corrupt `portless.json`.

## 8. A line def placed before an object property cannot bind in TypeScript

`ds def vite.config.ts:<line of "spa: {">` inserts the directive, then `ds scan` reports `extract: ds:def has nothing after it to bind to`. The tree-sitter tier binds a def to the next declaration, and an object-literal property is not one, so configuration expressed as nested objects (Vite, Vitest, ESLint, Tailwind configs) cannot be bound this way. `pick=` on the enclosing declaration might be the intended route; the error should say so and the dry run should predict it rather than the scan discovering it after the file was edited.
