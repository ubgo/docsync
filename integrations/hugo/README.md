# docsync for Hugo

Hugo cannot run a program while it builds, so docsync renders ahead of the build into data files and this module reads them.

1. Before `hugo`, run `ds export hugo --out data/docsync` in the repository that holds the docs. It writes `blocks.json` (every defined block, rendered) and `status.json` (the state of every cited sentence, the same shape as `ds status --json`).
2. Import the module in your site config: `[[module.imports]] path = "github.com/ubgo/docsync/integrations/hugo"`, or copy `layouts/` into the site.
3. In content, cite a block with `{{< ds id="sess-save-k7m2p4xq" >}}`. The shortcode renders the exported block with its caption; an id that was not exported renders a visible marker instead of silence.
4. Include `{{ partial "docsync/status.html" . }}` in a base template to emit one element per reference cited on the current page with `data-ds-state` and `data-ds-severity` (`none`, `warning`, `error`), so CSS or a few lines of script can paint green, amber, and red dots beside cited sentences, plus `data-ds-note`, `data-ds-acked-by` and `data-ds-acked-at` from the ack behind the citation for a hover.

A reference belongs to a page when its doc, a path from the repository root, ends in `/` plus the page's path in the content directory. Set the content directory's path from the repository root to make the match exact, which matters when two directories hold files of the same name:

```toml
[params.docsync]
contentDir = "site/content"
```

`ds render` remains the generic fallback for any generator: it rewrites a page with the blocks inlined.
