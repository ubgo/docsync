'use strict';

// remarkDocsync renders a page through `ds render` at build time, so every
// `ds:block`, `ds:cfg`, `ds:run`, `ds:table`, and `ds:url` directive in a
// Docusaurus doc becomes the code, value, result, table, or link it stands
// for (spec §24 "docs site", build order 8). The rendered markdown is
// re-parsed with the processor's own parser and replaces the tree, so
// every other remark plugin sees plain markdown afterwards.
//
// Options:
//   command  the docsync binary, default "ds"
//   args     extra arguments before "render", default []
//   cwd      the repository root `ds` runs in; paths are made relative to it
//   exec     (tests) a function (command, args, options) => stdout string,
//            default child_process.execFileSync
//   onError  "throw" (default) fails the build; "keep" leaves the page as
//            written and calls `warn(message)` instead
//   warn     where "keep" reports, default console.warn
//   sourceUrl  base URL of the repository's files, for example
//            "https://github.com/org/repo/blob/main/". A link `ds render`
//            wrote to a source file (`internal/a.go#L4-L6`) is a path in the
//            repository, not a page of the site, so Docusaurus resolved it
//            under the page's route and failed the build's broken-link
//            check. With sourceUrl it becomes sourceUrl + the repository
//            path; without it the link text is kept and the link dropped.
//            A `[check] permalink` template in .ds/config.toml makes
//            `ds render` write absolute links, which are left alone.
//
// The plugin must run before Docusaurus's own remark plugins, in
// `beforeDefaultRemarkPlugins`: it replaces the page's tree, and in
// `remarkPlugins` it ran after them and discarded what they had added, the
// table-of-contents export among them, which crashed every page at render.
// It refuses to run there with an error that says so.

const fs = require('node:fs');
const path = require('node:path');
const { execFileSync } = require('node:child_process');

const DEFAULT_COMMAND = 'ds';
const RENDER_SUBCOMMAND = 'render';
const ON_ERROR_THROW = 'throw';
const ON_ERROR_KEEP = 'keep';
// SOURCE_LINK is a relative link with a line fragment: what `ds render`
// writes for a cited block without a permalink template. Group 1 is the
// path, group 2 the `#Lstart-Lend` fragment. A scheme, a leading slash, or
// a query means someone wrote the link on purpose, and it is left alone.
const SOURCE_LINK = /^(?![a-z][a-z0-9+.-]*:)(?![/?#])([^#?]+)(#L\d+(?:-L\d+)?)$/i;
// Docusaurus's toc plugin appends an ESM export node built in memory, so
// with no source position, to every page; finding one means this plugin is
// running after the defaults. An import or export an author wrote has a
// position.
const ERR_AFTER_DEFAULTS =
  'remarkDocsync runs after Docusaurus\'s default remark plugins and would discard the page\'s table of contents; ' +
  'list it under beforeDefaultRemarkPlugins, not remarkPlugins';

// repoPath maps a link `ds render` wrote to a path from the repository
// root: links are repository-relative today, and a link relative to the
// page is accepted too, by whichever names a file that exists.
function repoPath(cwd, docDir, link) {
  const fromDoc = path.resolve(docDir, link);
  const abs = !fs.existsSync(path.resolve(cwd, link)) && fs.existsSync(fromDoc) ? fromDoc : path.resolve(cwd, link);
  return path.relative(cwd, abs).split(path.sep).join('/');
}

// rewriteSourceLinks applies sourceUrl to every source link in tree, or
// unwraps the link to its text when there is none.
function rewriteSourceLinks(node, rewrite) {
  if (!node.children) {
    return;
  }
  const out = [];
  for (const child of node.children) {
    const m = child.type === 'link' ? SOURCE_LINK.exec(child.url) : null;
    if (m) {
      const url = rewrite(m[1], m[2]);
      if (url === null) {
        out.push(...child.children);
        continue;
      }
      child.url = url;
    }
    rewriteSourceLinks(child, rewrite);
    out.push(child);
  }
  node.children = out;
}

function defaultExec(command, args, options) {
  return execFileSync(command, args, { ...options, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
}

function remarkDocsync(options = {}) {
  const {
    command = DEFAULT_COMMAND,
    args = [],
    cwd = process.cwd(),
    exec = defaultExec,
    onError = ON_ERROR_THROW,
    warn = console.warn,
    sourceUrl = '',
  } = options;
  const base = sourceUrl && !sourceUrl.endsWith('/') ? `${sourceUrl}/` : sourceUrl;
  if (onError !== ON_ERROR_THROW && onError !== ON_ERROR_KEEP) {
    throw new Error(`remarkDocsync: onError must be "${ON_ERROR_THROW}" or "${ON_ERROR_KEEP}", got "${onError}"`);
  }
  const processor = this;
  return function transformer(tree, file) {
    if (!file || !file.path) {
      // A string processed without a path has no file for `ds render` to
      // find; it is left as written.
      return tree;
    }
    if (tree.children.some((n) => n.type === 'mdxjsEsm' && !n.position)) {
      throw new Error(ERR_AFTER_DEFAULTS);
    }
    const rel = path.relative(cwd, file.path).split(path.sep).join('/');
    let rendered;
    try {
      rendered = exec(command, [...args, RENDER_SUBCOMMAND, rel], { cwd });
    } catch (err) {
      const message = `docsync: ${command} ${RENDER_SUBCOMMAND} ${rel} failed: ${err && err.stderr ? String(err.stderr).trim() : err.message}`;
      if (onError === ON_ERROR_KEEP) {
        warn(message);
        return tree;
      }
      throw new Error(message);
    }
    const next = processor.parse(rendered);
    const docDir = path.dirname(file.path);
    rewriteSourceLinks(next, (link, fragment) => (base ? base + repoPath(cwd, docDir, link) + fragment : null));
    tree.children = next.children;
    return tree;
  };
}

module.exports = remarkDocsync;
module.exports.ON_ERROR_THROW = ON_ERROR_THROW;
module.exports.ON_ERROR_KEEP = ON_ERROR_KEEP;
module.exports.ERR_AFTER_DEFAULTS = ERR_AFTER_DEFAULTS;
