# docusaurus-plugin-docsync

Build-time rendering for docsync directives in a Docusaurus site, with `ds render` doing the work and `ds status --json` supplying freshness.

```js
// docusaurus.config.js
const docsync = require('docusaurus-plugin-docsync');
module.exports = {
  plugins: [[docsync, { cwd: __dirname }]],
  presets: [['classic', { docs: { remarkPlugins: [[docsync.remark, { cwd: __dirname }]] } }]],
};
```

The remark plugin runs `ds render <doc>` for every page and replaces the page with the rendered markdown, so `ds:block` becomes the code, `ds:cfg` the value, `ds:table` the table. With `onError: "keep"` a page that fails to render is left as written and a warning is printed; the default fails the build, because a doc that cannot be rendered is a doc that cannot be trusted.

The plugin exposes `ds status --json` as global data (`usePluginData('docusaurus-plugin-docsync')`) keyed by doc, so a swizzled component can paint green, amber, and red dots beside cited sentences with the ack note on hover.

`ds` must be on the PATH of the build, or pass `command` with a path. `npm test` runs the unit tests with 100% coverage enforced.
