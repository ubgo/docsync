# docsync for VS Code

A thin client for `ds lsp`: code lenses on every `ds:def` listing its dependents, hover on a cite showing the block, its state, and the diff since the acked hash, go-to-definition from a cite to its block, a click on a `ds:` link in markdown that opens the block it names, and warnings when a defined symbol is renamed or deleted in the open buffer.

Settings: `docsync.path` (the `ds` binary), `docsync.args` (arguments before `lsp`, such as `--dir`), `docsync.languages` (language ids the server attaches to). Commands: `docsync: show dependents of this block` (bound to the code lens) and `docsync: restart the language server`.

Build with `npm install && npx @vscode/vsce package`; `npm test` runs the unit tests with 100% coverage enforced.
