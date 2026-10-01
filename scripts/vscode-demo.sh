#!/bin/sh
# vscode-demo.sh [dir]: build a small repository where a cited constant has
# changed since it was cited, then open it in a VS Code window running the
# docsync extension straight from editors/vscode (no install). What to look
# for: a code lens above the def in a.go, hover and go-to-definition on the
# citation in docs/d.md, and the docsync output channel for the server's log.
set -e
root=$(cd "$(dirname "$0")/.." && pwd)
T=${TMPDIR:-/tmp}; D=${1:-${T%/}/ds-vscode-demo}
/bin/rm -rf "$D"; mkdir -p "$D/docs"; cd "$D"
git init -q .; git config user.email demo@example.com; git config user.name demo
printf 'package p\n\n// ds:def id=ttl-k7m2p4xq\nconst TTL = 30\n' > a.go
printf '# Sessions\n\nSessions last [30](ds:cfg?id=ttl-k7m2p4xq) minutes.\n' > docs/d.md
"$root/bin/ds" init >/dev/null
"$root/bin/ds" scan >/dev/null
git add -A; git commit -qm init
sed 's/30/45/' a.go > a.go.new && mv a.go.new a.go
"$root/bin/ds" scan >/dev/null
printf '{\n  "docsync.path": "%s"\n}\n' "$root/bin/ds" > .vscode-settings.json
mkdir -p .vscode && mv .vscode-settings.json .vscode/settings.json
echo "demo repository: $D"
"$root/bin/ds" check || true
code --new-window --extensionDevelopmentPath="$root/editors/vscode" "$D"
