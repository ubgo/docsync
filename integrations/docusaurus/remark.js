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

const path = require('node:path');
const { execFileSync } = require('node:child_process');

const DEFAULT_COMMAND = 'ds';
const RENDER_SUBCOMMAND = 'render';
const ON_ERROR_THROW = 'throw';
const ON_ERROR_KEEP = 'keep';

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
  } = options;
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
    tree.children = next.children;
    return tree;
  };
}

module.exports = remarkDocsync;
module.exports.ON_ERROR_THROW = ON_ERROR_THROW;
module.exports.ON_ERROR_KEEP = ON_ERROR_KEEP;
