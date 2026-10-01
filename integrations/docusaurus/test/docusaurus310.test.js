'use strict';

// Bug 111: a real Docusaurus 3.10 site did not build with this plugin as
// its README configured it. These pin the plugin's half of the fixes; the
// README carries the configuration half.

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const remarkDocsync = require('../remark');

// parsed returns a processor whose parse yields the given tree.
function parsed(children) {
  return { parse: () => ({ type: 'root', children }) };
}

const link = (url, text) => ({ type: 'link', url, children: [{ type: 'text', value: text }] });

test('running after the default plugins is refused with the fix, not a crash at render', () => {
  const transformer = remarkDocsync.call(parsed([]), { exec: () => { throw new Error('must not run'); }, cwd: '/repo' });
  // What Docusaurus's toc plugin leaves: an ESM export built in memory.
  const tree = { type: 'root', children: [{ type: 'paragraph', children: [] }, { type: 'mdxjsEsm', value: '' }] };
  assert.throws(() => transformer(tree, { path: '/repo/docs/a.md' }), (err) => err.message === remarkDocsync.ERR_AFTER_DEFAULTS && /beforeDefaultRemarkPlugins/.test(err.message));
  // An import the author wrote has a position and is not mistaken for it.
  const authored = { type: 'root', children: [{ type: 'mdxjsEsm', value: 'import X from "x"', position: { start: { line: 1 } } }] };
  const ok = remarkDocsync.call(parsed([]), { exec: () => '', cwd: '/repo' });
  assert.equal(ok(authored, { path: '/repo/docs/a.md' }), authored);
});

test('source links are unlinked without sourceUrl, and other links are left alone', () => {
  const children = [{
    type: 'paragraph',
    children: [
      link('internal/a.go#L4-L6', 'internal/a.go:4-6'),
      link('../x.go#L1', 'x'),
      link('https://github.com/o/r/blob/s/a.go#L4-L6', 'abs'),
      link('/docs/other#L1', 'rooted'),
      link('./other.md', 'doc'),
      link('#L1', 'anchor'),
      { type: 'emphasis', children: [link('a.go#L2-L3', 'nested')] },
    ],
  }];
  const transformer = remarkDocsync.call(parsed(children), { exec: () => '', cwd: '/repo' });
  const tree = { type: 'root', children: [] };
  transformer(tree, { path: '/repo/docs/a.md' });
  const p = tree.children[0].children;
  assert.deepEqual(p[0], { type: 'text', value: 'internal/a.go:4-6' });
  assert.deepEqual(p[1], { type: 'text', value: 'x' });
  assert.deepEqual(p.slice(2, 6).map((n) => n.url), ['https://github.com/o/r/blob/s/a.go#L4-L6', '/docs/other#L1', './other.md', '#L1']);
  assert.deepEqual(p[6].children[0], { type: 'text', value: 'nested' });
});

test('sourceUrl turns a source link into a link into the repository', () => {
  const repo = fs.mkdtempSync(path.join(os.tmpdir(), 'ds-docusaurus-'));
  fs.mkdirSync(path.join(repo, 'docs'));
  fs.mkdirSync(path.join(repo, 'internal'));
  fs.writeFileSync(path.join(repo, 'internal', 'a.go'), 'package a\n');
  fs.writeFileSync(path.join(repo, 'docs', 'near.go'), 'package docs\n');
  const children = [{ type: 'paragraph', children: [link('internal/a.go#L4-L6', 'a'), link('near.go#L1', 'page-relative'), link('gone.go#L1', 'missing')] }];
  for (const sourceUrl of ['https://github.com/o/r/blob/main', 'https://github.com/o/r/blob/main/']) {
    const transformer = remarkDocsync.call(parsed(JSON.parse(JSON.stringify(children))), { exec: () => '', cwd: repo, sourceUrl });
    const tree = { type: 'root', children: [] };
    transformer(tree, { path: path.join(repo, 'docs', 'a.md') });
    assert.deepEqual(tree.children[0].children.map((n) => n.url), [
      'https://github.com/o/r/blob/main/internal/a.go#L4-L6',
      'https://github.com/o/r/blob/main/docs/near.go#L1',
      'https://github.com/o/r/blob/main/gone.go#L1',
    ]);
  }
  fs.rmSync(repo, { recursive: true, force: true });
});
