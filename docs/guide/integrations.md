# Integrations

This page shows how to install and configure each integration that ships with docsync: the Hugo module, the Docusaurus plugin, the VS Code extension, the pre-commit hooks, and the GitHub Action. It is for whoever maintains a docs site, an editor setup, or a pipeline, and wants a minimal configuration that is known to work.

Every integration runs the `ds` binary; none of them reimplements docsync. Install `ds` first (see [Getting started](getting-started.md)) and make sure it is on the `PATH` of whatever runs the integration.

| Integration | Where it lives | What it does |
|---|---|---|
| Hugo | `integrations/hugo` | renders cited blocks in pages from data that `ds export hugo` writes, plus freshness markers |
| Docusaurus | `integrations/docusaurus` | renders every page through `ds render` at build time, and exposes `ds status --json` to theme components |
| VS Code | `editors/vscode` | a client for `ds lsp`: code lenses, hover, go-to-definition, rename warnings |
| pre-commit | `.pre-commit-hooks.yaml` | `ds impact --staged` and `ds report --literals` before each commit |
| GitHub | `integrations/github` | a composite Action that checks and comments findings on pull requests, plus workflow templates |

The examples use a repository whose `store/session.go` defines `sess-save` (a function) and `sess-ttl` (a constant), cited from `docs/sessions.md`.

<!-- doctest
git init -q -b main .
ds init
mkdir -p store docs
printf 'package store\n\n// ds:def id=sess-ttl owner=@auth stability=stable\nconst SessionTTL = 30\n\n// ds:def id=sess-save owner=@auth stability=stable\nfunc SaveSession(id string) error {\n\treturn nil\n}\n' > store/session.go
printf '# Sessions\n\nSessions expire after [30](ds:cfg?id=sess-ttl) minutes.\n\nEvery session write goes through SaveSession:\n\n\074!\055\055 ds:block id=sess-save \055\055\076\n' > docs/sessions.md
ds scan
git add -A
git commit -qm init
-->

For any other static site generator, `ds render <doc>` prints a page with every directive expanded to plain markdown (the code, the value, the table), and `ds status --json` gives the state of every cited sentence. Both integrations below are built on those two commands.

~~~console
$ ds render docs/sessions.md
# Sessions

Sessions expire after 30 minutes.

Every session write goes through SaveSession:

**SaveSession** · [`store/session.go:7-9`](../store/session.go#L7-L9)

```go
func SaveSession(id string) error {
	return nil
}
```
~~~

## Hugo

Hugo cannot run a program while it builds, so docsync renders ahead of the build into Hugo data files, and the module's shortcode and partial read them.

### 1. Export the data before each build

Run this in the repository that holds the docs, with `--out` pointing at the site's `data/docsync` directory:

```console
$ ds export hugo --out site/data/docsync
exported 2 blocks and 2 references to site/data/docsync
```

```console
$ cat site/data/docsync/status.json
{
  "json_format": 1,
  …
  "refs": [
    {
      "doc": "docs/sessions.md",
      "line": 3,
      "id": "sess-ttl",
      "state": "ok",
      "severity": "none"
    },
…
```

It writes two files:

- `blocks.json`: every defined block by id, with `symbol`, `file`, `start`, `end`, `lang`, the raw `content`, and `rendered` markdown (a fenced code block).
- `status.json`: the state of every cited sentence, the same shape as `ds status --json` (`doc`, `line`, `id`, `state`, `severity`, and the ack's `note`, `acked_by`, `acked_at` when there is one).

`--out` is required. Run the export in CI right before `hugo`, so the site never shows blocks older than the commit being built.

### 2. Import the module

Hugo modules need Go installed. In the site:

```sh
hugo mod init example.com/site
```

then add the import to `hugo.toml`:

```toml
[module]
  [[module.imports]]
    path = "github.com/ubgo/docsync/integrations/hugo"
```

and fetch it:

```sh
hugo mod get github.com/ubgo/docsync/integrations/hugo
```

The module requires Hugo 0.110.0 or newer. If you would rather not use modules, copy `integrations/hugo/layouts/` into the site's `layouts/`; the result is the same.

### 3. Cite a block in content

```markdown
---
title: Sessions
---

Sessions are saved by:

{{< ds id="sess-save" >}}
```

The shortcode renders the block with a caption naming the symbol and its location, inside `<div class="docsync-block" data-ds-id="sess-save" data-ds-change="…">`, and the code goes through Hugo's own highlighter. An id that was not exported renders a visible marker rather than nothing:

```html
<mark class="docsync-missing">docsync: sess-nope not exported; run <code>ds export hugo</code></mark>
```

### 4. Freshness markers (optional)

Include the status partial in a base template:

```go-html-template
{{ partial "docsync/status.html" . }}
```

It emits a hidden element with one `<span>` per reference cited on the current page, each carrying `data-ds-doc`, `data-ds-line`, `data-ds-id`, `data-ds-state`, `data-ds-severity` (`none`, `warning`, `error`) and, when the citation was acked, `data-ds-note`, `data-ds-acked-by`, `data-ds-acked-at`:

```html
<div class="docsync-status" hidden data-ds-commit="8fcbb8a">
<span data-ds-doc="docs/sessions.md" data-ds-line="3" data-ds-id="sess-ttl" data-ds-state="unacked" data-ds-severity="error"></span>
<span data-ds-doc="docs/sessions.md" data-ds-line="6" data-ds-id="sess-save" data-ds-state="ok" data-ds-severity="none"></span>
</div>
```

A reference belongs to a page when its doc, a path from the repository root, ends in `/` plus the page's path in the content directory, so `docs/sessions.md` belongs to the page built from `content/docs/sessions.md`. When two directories hold files of the same name, set the content directory's path from the repository root and the match becomes exact:

```toml
[params.docsync]
contentDir = "site/content"
```

A page with no file, such as a taxonomy list, gets the element with no references.

## Docusaurus

The Docusaurus integration has two parts. The remark plugin runs `ds render` on every page at build time and replaces the page with the rendered markdown, so a `ds:block` becomes the code and a `ds:cfg` the value. The site plugin runs `ds status --json` once and exposes it as global data for a theme component.

### Install

The package is not published to npm. Install it from a checkout of the docsync repository:

```sh
npm install /path/to/docsync/integrations/docusaurus
```

It needs Node 20 or newer and Docusaurus 3, and `ds` on the build's `PATH` (or pass `command` with a path).

### Configure

This configuration was verified with Docusaurus 3.10 and the `classic` preset, with the site in `website/` inside the repository and `.ds/` at the repository root:

```js
// website/docusaurus.config.js
import path from 'node:path';
import docsync from 'docusaurus-plugin-docsync';

// The repository root: where .ds/ lives and where `ds` runs.
const repoRoot = path.resolve(__dirname, '..');

const config = {
  // …title, url, baseUrl…

  // .md pages are parsed as CommonMark, so <!-- ds:… --> comments are allowed.
  markdown: { format: 'detect' },

  plugins: [[docsync, { cwd: repoRoot }]],

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.js',
          beforeDefaultRemarkPlugins: [[docsync.remark, { cwd: repoRoot, sourceUrl: 'https://github.com/org/repo/blob/main/' }]],
        },
      },
    ],
  ],
};

export default config;
```

Two settings in that file are there because the build fails without them, and the third decides where source links go:

- **`beforeDefaultRemarkPlugins`, not `remarkPlugins`.** The transformer replaces the page's whole tree with the rendered one. Registered under `remarkPlugins`, which run after Docusaurus's own remark plugins, it would discard what they added, the page's table of contents among it, so it stops the build there with `remarkDocsync runs after Docusaurus's default remark plugins and would discard the page's table of contents; list it under beforeDefaultRemarkPlugins, not remarkPlugins`. Running it before the defaults lets Docusaurus process the rendered page as if it had been written that way.
- **`markdown: { format: 'detect' }`.** Docusaurus parses `.md` files as MDX by default, and MDX rejects HTML comments, so a page with a block-form directive such as `<!-- ds:block id=sess-save -->` fails to parse before the plugin ever sees it. With `detect`, `.md` files are CommonMark and `.mdx` files stay MDX. Link-form directives (`[30](ds:cfg?id=sess-ttl)`) parse either way.
- **`sourceUrl`.** A rendered block carries a caption linking to its source, such as `store/session.go#L7-L9`, a path in the repository that is not a page of the site, and Docusaurus's broken-link check (`onBrokenLinks: 'throw'`, the default) would stop the build on it. The plugin makes each such link `sourceUrl` plus the file's repository path; without `sourceUrl` it keeps the link text and drops the link, so the default broken-link check still passes. A `[check] permalink` template in `.ds/config.toml` makes `ds render` write absolute links instead, and the plugin leaves those alone.

`cwd` must be the repository root, because the plugin passes each page's path relative to it to `ds render`. Pages are rendered from the file on disk, so the docs must live inside that repository.

### Options

Both parts accept `command` (the `ds` binary, default `ds`), `args` (extra arguments placed before the subcommand), and `cwd` (default the process's working directory). The remark plugin also accepts `onError`: the default `"throw"` fails the build when a page cannot be rendered, because a doc that cannot be rendered cannot be trusted; `"keep"` leaves that page as written and prints a warning instead. And it accepts `sourceUrl`, the base URL the repository's files are served from, described above.

### Freshness data in a component

The site plugin publishes the status under its name, `docusaurus-plugin-docsync`, as `{ commit, refs, byDoc }`, where `byDoc` maps each doc path to its rows. A swizzled component reads it with Docusaurus's `usePluginData`:

```js
import { usePluginData } from '@docusaurus/useGlobalData';

const { byDoc } = usePluginData('docusaurus-plugin-docsync');
const rows = byDoc['website/docs/sessions.md'] || [];
// each row: { doc, line, id, state, severity, note?, acked_by?, acked_at? }
```

Paint each cited line by `severity` (`none`, `warning`, `error`) and show `note` on hover.

## VS Code

The extension in `editors/vscode` starts `ds lsp` and attaches it to Markdown and the languages your defs live in. It is not on the Marketplace; build and install it from a checkout:

```sh
cd editors/vscode
npm install
npx @vscode/vsce package --allow-missing-repository --skip-license
code --install-extension docsync-0.1.0.vsix
```

The two `vsce` flags answer prompts it raises for fields the extension's `package.json` does not set.

The minimal configuration is none, when `ds` is on `PATH` and the workspace folder is the repository root. Otherwise, in `settings.json`:

```json
{
  "docsync.path": "/usr/local/bin/ds",
  "docsync.args": ["--dir", "/path/to/repo"]
}
```

`docsync.languages` lists the language ids it attaches to. The language server and what it shows are described in [Agents and editors](agents.md#ds-lsp-the-language-server).

## pre-commit

`.pre-commit-config.yaml`:

```yaml
repos:
  - repo: https://github.com/ubgo/docsync
    rev: ds/v0.1.5
    hooks:
      - id: docsync-impact
        verbose: true
      - id: docsync-literals
        verbose: true
```

`docsync-impact` runs `ds impact --staged` and lists the sentences your staged changes will flag; `docsync-literals` runs `ds report --literals` and lists values typed into docs that a cite should carry. Both use the `ds` on `PATH` and never fail the commit, so `verbose: true` is what makes their output visible. Details and sample output are in [CI](ci.md#pre-commit-hooks).

## GitHub

The Action runs `ds check` and, on a pull request, posts one comment per doc with its findings. The minimal workflow, saved as `.github/workflows/docsync.yml`:

```yaml
name: docsync
on:
  pull_request:
    types: [opened, synchronize, reopened, labeled]
permissions:
  contents: read
  pull-requests: write
jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: ubgo/docsync/integrations/github@main
```

The Action's inputs, the `docs-acked` label, the publish and nightly templates, and the fork rules are covered in [CI](ci.md#github-actions).
