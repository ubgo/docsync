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
  const providers = {};
  const vscode = {
    workspace: {
      getConfiguration: (section) => { assert.equal(section, 'docsync'); return config; },
      registerTextDocumentContentProvider: (scheme, p) => { providers[scheme] = p; return { dispose() {} }; },
    },
    commands: { registerCommand: (name, fn) => { commands[name] = fn; return { dispose() {} }; } },
    window: { createTerminal: (options) => { const t = { options, shown: 0, show() { this.shown++; } }; terminals.push(t); return t; } },
  };
  return { deps: { vscode, LanguageClient }, clients, commands, terminals, providers };
}

test('activate starts the server from configuration and registers commands', async () => {
  const f = fakeDeps({ path: '/Applications/My Tools/ds', args: ['--dir', '/repo'], languages: ['markdown', 'go'] });
  const context = { subscriptions: [] };
  await ext.activate(context, f.deps);
  assert.equal(f.clients.length, 1);
  assert.deepEqual(f.clients[0].server, { command: '/Applications/My Tools/ds', args: ['--dir', '/repo', 'lsp'] });
  assert.deepEqual(f.clients[0].options.documentSelector, [{ scheme: 'file', language: 'markdown' }, { scheme: 'file', language: 'go' }]);
  assert.equal(f.clients[0].started, 1);
  assert.equal(context.subscriptions.length, 4);
  assert.ok(f.providers.ds, 'ds: links have a provider');
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
  context.subscriptions[3].dispose();
});

test('defaults apply when nothing is configured', async () => {
  const f = fakeDeps({});
  const context = { subscriptions: [] };
  await ext.activate(context, f.deps);
  assert.deepEqual(f.clients[0].server, { command: 'ds', args: ['lsp'] });
  assert.deepEqual(f.clients[0].options.documentSelector, []);
  f.commands[ext.COMMAND_WHY]('x');
  assert.deepEqual(f.terminals[0].options, { name: 'docsync', shellPath: 'ds', shellArgs: ['why', 'x'] });
  await ext.deactivate();
});

test('the default dependencies come from the editor modules', async () => {
  await assert.rejects(ext.activate({ subscriptions: [] }), /Cannot find module 'vscode'/);
});

// A ds: link in markdown opens what it names (bug 131): the code is revealed
// in its file and the link opens a page with the id, location and body.
function linkDeps({ settings = {}, folders, run }) {
  const shown = [];
  const config = { get: (k) => settings[k] };
  const vscode = {
    workspace: { getConfiguration: () => config, workspaceFolders: folders },
    window: { showTextDocument: (uri, opts) => { shown.push({ uri, opts }); } },
    Uri: { file: (p) => ({ fsPath: p }) },
    Range: class { constructor(a, b, c, d) { Object.assign(this, { a, b, c, d }); } },
  };
  return { deps: { vscode, run }, shown };
}

test('a ds: link reveals the block and shows its location and body', async () => {
  const calls = [];
  const run = async (cmd, args, cwd) => {
    calls.push([cmd, args, cwd]);
    return args.includes('locate') ? 'a.go:4-6 @ ee555fb\n' : '45\n';
  };
  const f = linkDeps({ settings: { path: '/bin/ds', args: ['--dir', '/r'] }, folders: [{ uri: { fsPath: '/r' } }], run });
  const text = await ext.linkProvider(f.deps).provideTextDocumentContent({ query: 'id%3Dttl-k7m2p4xq', toString: () => 'ds:cfg?id%3Dttl-k7m2p4xq' });
  assert.equal(text, 'ttl-k7m2p4xq · a.go:4-6 @ ee555fb\n\n45\n');
  assert.deepEqual(calls, [['/bin/ds', ['--dir', '/r', 'locate', 'ttl-k7m2p4xq'], '/r'], ['/bin/ds', ['--dir', '/r', 'read', 'ttl-k7m2p4xq'], '/r']]);
  assert.equal(f.shown[0].uri.fsPath, '/r/a.go');
  assert.deepEqual({ ...f.shown[0].opts.selection }, { a: 3, b: 0, c: 5, d: 0 });
});

test('a one-line block, no workspace, a failure, and a link with no id', async () => {
  const one = linkDeps({ folders: [{ uri: { fsPath: '/r' } }], run: async (c, a) => (a.includes('locate') ? 'a.go:4 @ x' : 'v') });
  await ext.linkProvider(one.deps).provideTextDocumentContent({ query: 'id=k' });
  assert.deepEqual({ ...one.shown[0].opts.selection }, { a: 3, b: 0, c: 3, d: 0 });
  const nofolder = linkDeps({ run: async () => 'a.go:4 @ x' });
  assert.equal(await ext.linkProvider(nofolder.deps).provideTextDocumentContent({ query: 'id=k' }), 'k · a.go:4 @ x\n\na.go:4 @ x');
  assert.equal(nofolder.shown.length, 0);
  const odd = linkDeps({ folders: [{ uri: { fsPath: '/r' } }], run: async () => 'not a location' });
  await ext.linkProvider(odd.deps).provideTextDocumentContent({ query: 'id=k' });
  assert.equal(odd.shown.length, 0);
  const failing = linkDeps({ folders: [], run: async () => { throw new Error('k is not defined'); } });
  assert.equal(await ext.linkProvider(failing.deps).provideTextDocumentContent({ query: 'id=k' }), 'ds could not resolve k: k is not defined\n');
  const bare = linkDeps({ run: async () => '' });
  assert.match(await ext.linkProvider(bare.deps).provideTextDocumentContent({ toString: () => 'ds:block' }), /names no id=/);
  assert.equal(ext.idOf({}), '');
});

test('run passes arguments as a list and reports stderr on failure', async () => {
  assert.equal(await ext.run(process.execPath, ['-e', 'process.stdout.write(process.argv[1])', 'a b'], undefined), 'a b');
  await assert.rejects(ext.run(process.execPath, ['-e', 'process.stderr.write("nope "); process.exit(3)'], undefined), /^Error: nope$/);
  await assert.rejects(ext.run(process.execPath, ['-e', 'process.exit(3)'], undefined), /Command failed/);
});
