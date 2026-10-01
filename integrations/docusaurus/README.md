# docusaurus-plugin-docsync

Build-time rendering for docsync directives in a Docusaurus site, with `ds render` doing the work and `ds status --json` supplying freshness. Tested against Docusaurus 3.10.

## Install

The package is not published to npm yet. Install it from a checkout of the docsync repository, as a local dependency of the site:

```sh
npm install --save-dev /path/to/docsync/integrations/docusaurus
```

or copy the `integrations/docusaurus` directory into the site (for example to `plugins/docsync/`) and `require` it by path. It has no dependencies of its own.

## Configure

```js
// docusaurus.config.js
const docsync = require('docusaurus-plugin-docsync');
module.exports = {
  // Docs are CommonMark, so the HTML comments directives live in
  // (<!-- ds:block id=… -->) parse; MDX rejects them.
  markdown: { format: 'detect' },
  plugins: [[docsync, { cwd: __dirname }]],
  presets: [['classic', {
    docs: {
      // beforeDefaultRemarkPlugins, not remarkPlugins: see below.
      beforeDefaultRemarkPlugins: [[docsync.remark, { cwd: __dirname, sourceUrl: 'https://github.com/org/repo/blob/main/' }]],
    },
  }]],
};
```

`cwd` is the repository root, where `.ds/` is. Two of these settings are required by Docusaurus 3, and a real 3.10 build failed without each; the third decides where links to the source go:

- **`beforeDefaultRemarkPlugins`.** The remark plugin replaces the page with what `ds render` returns, so it has to run before Docusaurus's own remark plugins. Listed under `remarkPlugins` it ran after them and threw away what they had added, the page's table of contents among it, and every page crashed at render with `Cannot read properties of undefined (reading 'length')`. It now refuses to run there with an error naming the fix.
- **`markdown: { format: 'detect' }`.** Block directives are HTML comments. Docusaurus compiles `.md` files as MDX unless told otherwise, and MDX has no HTML comments: a site created with the 3.10 template (`future: { v4: true }`) stops on the first one with `Unexpected character '!'`. With `detect`, `.md` files are CommonMark and `.mdx` files stay MDX. Keep pages with directives in `.md`.
- **`sourceUrl`.** A rendered citation links to the cited code, and without a permalink template that link is a path in the repository (`internal/a.go#L4-L6`), which Docusaurus resolves under the page's route and the broken-link check rejects. `sourceUrl` is the base URL the repository's files are served from; such a link becomes `sourceUrl` + its repository path. Without it the link text is kept and the link dropped, so the build passes with no links into the source. A `[check] permalink` template in `.ds/config.toml` makes `ds render` write absolute links itself, and those are left alone.

## What it does

The remark plugin runs `ds render <doc>` for every page and replaces the page with the rendered markdown, so `ds:block` becomes the code, `ds:cfg` the value, `ds:table` the table. With `onError: "keep"` a page that fails to render is left as written and a warning is printed; the default fails the build, because a doc that cannot be rendered is a doc that cannot be trusted.

The plugin exposes `ds status --json` as global data (`usePluginData('docusaurus-plugin-docsync')`) keyed by doc, so a swizzled component can paint green, amber, and red dots beside cited sentences with the ack note on hover.

`ds` must be on the PATH of the build, or pass `command` with a path. `npm test` runs the unit tests with 100% coverage enforced.
