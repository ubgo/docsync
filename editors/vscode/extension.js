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
// LINK_SCHEME is the scheme of a link-form directive, `[text](ds:cfg?id=…)`.
// VS Code's markdown support makes every such link clickable and, with no
// provider for the scheme, a click failed with "Unable to resolve resource".
const LINK_SCHEME = 'ds';
const LOCATE_SUBCOMMAND = 'locate';
const READ_SUBCOMMAND = 'read';
// LOCATION_RE reads `ds locate`'s output: `file:start[-end] @ commit`.
const LOCATION_RE = /^(.+):(\d+)(?:-(\d+))? @/;

let client = null;

function defaultDeps() {
  // Required lazily so tests never load the editor modules.
  const vscode = require('vscode');
  const { LanguageClient } = require('vscode-languageclient/node');
  return { vscode, LanguageClient, run };
}

// serverOptions builds the command from configuration; exported for tests
// and for anyone wiring the server into another client. It names no
// transport: the language client then talks over stdin and stdout without
// adding a flag. Naming TransportKind.stdio made it start `ds lsp --stdio`,
// which a ds before 0.1.6 rejected, so the server exited at once and the
// extension never started (bug 130).
function serverOptions(config) {
  const command = config.get('path') || 'ds';
  const args = [...(config.get('args') || []), SERVER_SUBCOMMAND];
  return { command, args };
}

function documentSelector(config) {
  return (config.get('languages') || []).map((language) => ({ scheme: 'file', language }));
}

async function start(deps) {
  const { vscode, LanguageClient } = deps;
  const config = vscode.workspace.getConfiguration(CONFIG_SECTION);
  client = new LanguageClient(CLIENT_ID, CLIENT_NAME, serverOptions(config), { documentSelector: documentSelector(config) });
  await client.start();
  return client;
}

// run executes ds with its arguments as a list (never through a shell) and
// resolves its stdout, or rejects with its stderr.
function run(command, args, cwd) {
  const { execFile } = require('node:child_process');
  return new Promise((resolve, reject) => {
    execFile(command, args, { cwd }, (err, stdout, stderr) => (err ? reject(new Error((stderr || err.message).trim())) : resolve(stdout)));
  });
}

// idOf returns the id a `ds:` link names (`ds:cfg?id=x`, `ds:block?id=x`),
// or '' when it names none. The query may arrive percent-encoded.
function idOf(uri) {
  return new URLSearchParams(decodeURIComponent(uri.query || '')).get('id') || '';
}

// linkProvider makes a `ds:` link open what it names: the block's code is
// revealed in its file, and the link itself opens a read-only page with the
// id, its location and its current body, from `ds locate` and `ds read`.
function linkProvider(deps) {
  const { vscode, run } = deps;
  return {
    async provideTextDocumentContent(uri) {
      const id = idOf(uri);
      if (!id) {
        return `${uri.toString()} names no id=; a docsync link is ${LINK_SCHEME}:block?id=… or ${LINK_SCHEME}:cfg?id=…\n`;
      }
      const config = vscode.workspace.getConfiguration(CONFIG_SECTION);
      const command = config.get('path') || 'ds';
      const pre = config.get('args') || [];
      const folder = (vscode.workspace.workspaceFolders || [])[0];
      const cwd = folder ? folder.uri.fsPath : undefined;
      try {
        const where = (await run(command, [...pre, LOCATE_SUBCOMMAND, id], cwd)).trim();
        const body = await run(command, [...pre, READ_SUBCOMMAND, id], cwd);
        const m = LOCATION_RE.exec(where);
        if (m && cwd) {
          const start = Number(m[2]) - 1;
          const end = Number(m[3] || m[2]) - 1;
          vscode.window.showTextDocument(vscode.Uri.file(`${cwd}/${m[1]}`), { selection: new vscode.Range(start, 0, end, 0) });
        }
        return `${id} · ${where}\n\n${body}`;
      } catch (err) {
        return `ds could not resolve ${id}: ${err.message}\n`;
      }
    },
  };
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
    vscode.workspace.registerTextDocumentContentProvider(LINK_SCHEME, linkProvider(deps)),
    { dispose: () => { stop(); } },
  );
}

function deactivate() {
  return stop();
}

module.exports = { activate, deactivate, serverOptions, documentSelector, linkProvider, idOf, run, COMMAND_WHY, COMMAND_RESTART };
