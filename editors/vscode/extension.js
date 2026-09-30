'use strict';

// The VS Code client for `ds lsp` (spec §24 "editor (LSP)", build order 7).
// Everything language-aware lives in the server; this file starts it over
// stdio, attaches it to the configured languages, and wires the code lens
// command to a terminal running `ds why <id>`. Dependencies are injected
// through `deps` so the logic is unit-tested without VS Code: `activate`
// receives the real modules when the editor loads it.

const CONFIG_SECTION = 'docsync';
const SERVER_SUBCOMMAND = 'lsp';
const CLIENT_ID = 'docsync';
const CLIENT_NAME = 'docsync';
const COMMAND_WHY = 'docsync.why';
const COMMAND_RESTART = 'docsync.restart';
const TERMINAL_NAME = 'docsync';
const WHY_SUBCOMMAND = 'why';

let client = null;

function defaultDeps() {
  // Required lazily so tests never load the editor modules.
  const vscode = require('vscode');
  const { LanguageClient, TransportKind } = require('vscode-languageclient/node');
  return { vscode, LanguageClient, TransportKind };
}

// serverOptions builds the command from configuration; exported for tests
// and for anyone wiring the server into another client.
function serverOptions(config, TransportKind) {
  const command = config.get('path') || 'ds';
  const args = [...(config.get('args') || []), SERVER_SUBCOMMAND];
  return { command, args, transport: TransportKind.stdio };
}

function documentSelector(config) {
  return (config.get('languages') || []).map((language) => ({ scheme: 'file', language }));
}

async function start(deps) {
  const { vscode, LanguageClient, TransportKind } = deps;
  const config = vscode.workspace.getConfiguration(CONFIG_SECTION);
  client = new LanguageClient(CLIENT_ID, CLIENT_NAME, serverOptions(config, TransportKind), { documentSelector: documentSelector(config) });
  await client.start();
  return client;
}

async function stop() {
  if (!client) {
    return;
  }
  const running = client;
  client = null;
  await running.stop();
}

async function activate(context, deps = defaultDeps()) {
  const { vscode } = deps;
  await start(deps);
  context.subscriptions.push(
    vscode.commands.registerCommand(COMMAND_WHY, (id) => {
      const config = vscode.workspace.getConfiguration(CONFIG_SECTION);
      // The terminal runs the binary itself, arguments as a list. Typing a
      // joined command line into a shell broke on a path with a space in
      // it — the Windows default, C:\Program Files — and quoting differs
      // between bash, PowerShell, and cmd.
      const terminal = vscode.window.createTerminal({
        name: TERMINAL_NAME,
        shellPath: config.get('path') || 'ds',
        shellArgs: [...(config.get('args') || []), WHY_SUBCOMMAND, id],
      });
      terminal.show();
    }),
    vscode.commands.registerCommand(COMMAND_RESTART, async () => {
      await stop();
      await start(deps);
    }),
    { dispose: () => { stop(); } },
  );
}

function deactivate() {
  return stop();
}

module.exports = { activate, deactivate, serverOptions, documentSelector, COMMAND_WHY, COMMAND_RESTART };
