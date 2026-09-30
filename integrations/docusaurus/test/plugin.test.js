'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const docsync = require('../index');

const status = { commit: 'abc1234', refs: [{ doc: 'docs/a.md', line: 1, id: 'x-a2b6f8jk', state: 'ok' }, { doc: 'docs/a.md', line: 3, state: 'broken' }, { doc: 'docs/b.md', line: 2, id: 'y-b3c7g9kl', state: 'unacked' }] };

test('loads status and exposes it as global data keyed by doc', async () => {
  const calls = [];
  const plugin = docsync({}, { exec: (c, a, o) => { calls.push([c, a, o]); return JSON.stringify(status); }, cwd: '/repo', args: ['--dir', '/repo'] });
  assert.equal(plugin.name, docsync.PLUGIN_NAME);
  const content = await plugin.loadContent();
  assert.deepEqual(calls, [['ds', ['--dir', '/repo', 'status', '--json'], { cwd: '/repo' }]]);
  let data;
  await plugin.contentLoaded({ content, actions: { setGlobalData: (d) => { data = d; } } });
  assert.equal(data.commit, 'abc1234');
  assert.equal(data.byDoc['docs/a.md'].length, 2);
  assert.equal(data.byDoc['docs/b.md'][0].state, 'unacked');
  assert.equal(data.refs.length, 3);
});

test('a status without refs is an error', async () => {
  const plugin = docsync({}, { exec: () => '{}' });
  await assert.rejects(plugin.loadContent(), /returned no refs/);
  const nul = docsync({}, { exec: () => 'null' });
  await assert.rejects(nul.loadContent(), /returned no refs/);
});

test('the default exec runs a real command and defaults are applied', async () => {
  const plugin = docsync({}, { command: process.execPath, args: ['-e', `process.stdout.write(${JSON.stringify(JSON.stringify(status))})`] });
  const content = await plugin.loadContent();
  assert.equal(content.refs.length, 3);
  const bare = docsync({});
  assert.equal(bare.name, 'docusaurus-plugin-docsync');
  assert.equal(docsync.remark.name, 'remarkDocsync');
  assert.equal(docsync.plugin, docsync);
});
