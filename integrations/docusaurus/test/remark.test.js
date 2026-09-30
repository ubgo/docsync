'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const remarkDocsync = require('../remark');

function processor() {
  return { parse: (s) => ({ type: 'root', children: [{ type: 'text', value: s }] }) };
}

test('renders through ds render relative to cwd and replaces the tree', () => {
  const calls = [];
  const exec = (command, args, options) => {
    calls.push({ command, args, options });
    return '# rendered';
  };
  const transformer = remarkDocsync.call(processor(), { exec, cwd: '/repo', args: ['--dir', '/repo'] });
  const tree = { type: 'root', children: [{ type: 'text', value: 'source' }] };
  const out = transformer(tree, { path: path.join('/repo', 'docs', 'a.md') });
  assert.equal(out, tree);
  assert.deepEqual(tree.children, [{ type: 'text', value: '# rendered' }]);
  assert.deepEqual(calls, [{ command: 'ds', args: ['--dir', '/repo', 'render', 'docs/a.md'], options: { cwd: '/repo' } }]);
});

test('a file without a path is left alone', () => {
  const transformer = remarkDocsync.call(processor(), { exec: () => { throw new Error('must not run'); } });
  const tree = { type: 'root', children: [] };
  assert.equal(transformer(tree, {}), tree);
  assert.equal(transformer(tree, undefined), tree);
});

test('failures throw by default with stderr in the message', () => {
  const exec = () => { const e = new Error('exit 2'); e.stderr = 'ds: docs/a.md: not found\n'; throw e; };
  const transformer = remarkDocsync.call(processor(), { exec, cwd: '/repo' });
  assert.throws(() => transformer({ type: 'root', children: [] }, { path: '/repo/docs/a.md' }), /ds render docs\/a.md failed: ds: docs\/a.md: not found/);
  const plain = () => { throw new Error('spawn ds ENOENT'); };
  const t2 = remarkDocsync.call(processor(), { exec: plain, cwd: '/repo' });
  assert.throws(() => t2({ type: 'root', children: [] }, { path: '/repo/docs/a.md' }), /spawn ds ENOENT/);
});

test('onError keep warns and keeps the page', () => {
  const warnings = [];
  const exec = () => { throw new Error('boom'); };
  const transformer = remarkDocsync.call(processor(), { exec, cwd: '/repo', onError: 'keep', warn: (m) => warnings.push(m) });
  const tree = { type: 'root', children: [{ type: 'text', value: 'kept' }] };
  assert.equal(transformer(tree, { path: '/repo/docs/a.md' }), tree);
  assert.equal(tree.children[0].value, 'kept');
  assert.match(warnings[0], /boom/);
});

test('an unknown onError is rejected at attach time', () => {
  assert.throws(() => remarkDocsync.call(processor(), { onError: 'ignore' }), /onError must be "throw" or "keep"/);
  assert.equal(remarkDocsync.ON_ERROR_KEEP, 'keep');
});

test('the default exec runs a real command', () => {
  const transformer = remarkDocsync.call(processor(), { command: process.execPath, args: ['-e', 'process.stdout.write("ok " + process.argv.slice(1).join(" "))'], cwd: process.cwd() });
  const tree = { type: 'root', children: [] };
  transformer(tree, { path: path.join(process.cwd(), 'docs', 'x.md') });
  assert.deepEqual(tree.children, [{ type: 'text', value: 'ok render docs/x.md' }]);
  const failing = remarkDocsync.call(processor(), { command: process.execPath, args: ['-e', 'process.stderr.write("bad"); process.exit(3)'], cwd: process.cwd(), onError: 'keep', warn: () => {} });
  assert.equal(failing(tree, { path: path.join(process.cwd(), 'docs', 'x.md') }), tree);
});
