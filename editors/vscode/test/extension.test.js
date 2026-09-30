'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const ext = require('../extension');

function fakeDeps(settings = {}) {
  const clients = [];
  const commands = {};
  const terminals = [];
  const config = { get: (k) => settings[k] };
  class LanguageClient {
    constructor(id, name, server, options) {
      Object.assign(this, { id, name, server, options, started: 0, stopped: 0 });
      clients.push(this);
    }
    async start() { this.started++; }
    async stop() { this.stopped++; }
  }
  const vscode = {
    workspace: { getConfiguration: (section) => { assert.equal(section, 'docsync'); return config; } },
    commands: { registerCommand: (name, fn) => { commands[name] = fn; return { dispose() {} }; } },
    window: { createTerminal: (options) => { const t = { options, shown: 0, show() { this.shown++; } }; terminals.push(t); return t; } },
  };
  return { deps: { vscode, LanguageClient, TransportKind: { stdio: 'stdio' } }, clients, commands, terminals };
}

test('activate starts the server from configuration and registers commands', async () => {
  const f = fakeDeps({ path: '/Applications/My Tools/ds', args: ['--dir', '/repo'], languages: ['markdown', 'go'] });
  const context = { subscriptions: [] };
  await ext.activate(context, f.deps);
  assert.equal(f.clients.length, 1);
  assert.deepEqual(f.clients[0].server, { command: '/Applications/My Tools/ds', args: ['--dir', '/repo', 'lsp'], transport: 'stdio' });
  assert.deepEqual(f.clients[0].options.documentSelector, [{ scheme: 'file', language: 'markdown' }, { scheme: 'file', language: 'go' }]);
  assert.equal(f.clients[0].started, 1);
  assert.equal(context.subscriptions.length, 3);
  f.commands[ext.COMMAND_WHY]('sess-save-k7m2p4xq');
  assert.equal(f.terminals[0].shown, 1);
  // A path with a space is one argument, never split by a shell.
  assert.deepEqual(f.terminals[0].options, { name: 'docsync', shellPath: '/Applications/My Tools/ds', shellArgs: ['--dir', '/repo', 'why', 'sess-save-k7m2p4xq'] });
  await f.commands[ext.COMMAND_RESTART]();
  assert.equal(f.clients[0].stopped, 1);
  assert.equal(f.clients.length, 2);
  await ext.deactivate();
  assert.equal(f.clients[1].stopped, 1);
  await ext.deactivate();
  context.subscriptions[2].dispose();
});

test('defaults apply when nothing is configured', async () => {
  const f = fakeDeps({});
  const context = { subscriptions: [] };
  await ext.activate(context, f.deps);
  assert.deepEqual(f.clients[0].server, { command: 'ds', args: ['lsp'], transport: 'stdio' });
  assert.deepEqual(f.clients[0].options.documentSelector, []);
  f.commands[ext.COMMAND_WHY]('x');
  assert.deepEqual(f.terminals[0].options, { name: 'docsync', shellPath: 'ds', shellArgs: ['why', 'x'] });
  await ext.deactivate();
});

test('the default dependencies come from the editor modules', async () => {
  await assert.rejects(ext.activate({ subscriptions: [] }), /Cannot find module 'vscode'/);
});
