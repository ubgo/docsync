#!/bin/sh
# Upgrading from the last release must not flag what did not change. The
# previous release's ds (the newest ds/v* tag) scans and acks a repository;
# then the ds built from this tree scans and checks the same repository, and
# no finding may appear except one the CHANGELOG's Upgrading section
# explains, listed in EXPECTED below with the release it belongs to.
#
# Why it exists: every other gate compares a build with itself. A change to
# what a hash covers, a ledger column or the ack log's shape passes all of
# them and still reports drift on every acked citation in every repository
# that upgrades -- a release-wide false alarm only a run of the old binary
# followed by the new one can see. `task extract:diff` compares hashes
# block by block; this compares the whole user-visible outcome, acks and
# all, across real binaries.
#
# The old binary is built from `git archive` of the tag (no worktree is
# added to the repository) with the network off. When it cannot be built
# offline -- no tag, no toolchain, a dependency not in the module cache --
# the matrix prints SKIP with the reason and passes nothing.
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
S=$(mktemp -d)
command -v cygpath >/dev/null 2>&1 && S=$(cygpath -m "$S")
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
ok() { echo "  PASS  $1"; pass=$((pass+1)); }
no() { echo "  FAIL  $1"; fail=$((fail+1)); }
skip() { echo "  SKIP  $1"; echo; echo "  ---- $pass passed, $fail failed ----"; exit 0; }

# EXPECTED lists findings the CHANGELOG's Upgrading section explains for an
# upgrade from the tag below, one per line as "<state> <id>". Empty: an
# upgrade from the last release reports nothing on this fixture.
EXPECTED=""

tag=$(git -C "$root" tag -l 'ds/v*' --sort=-v:refname 2>/dev/null | head -1)
[ -n "$tag" ] || skip "no ds/v* release tag in this repository"
command -v go >/dev/null 2>&1 || skip "go is not installed"
mkdir -p "$S/old"
git -C "$root" archive "$tag" | tar -x -C "$S/old" 2>/dev/null || skip "git archive $tag failed"
# The tag's own go.work joins its modules, so the old cli builds against the
# old library; without one, its go.mod's published requirements are used.
if ! (cd "$S/old/cli" && GOPROXY=off go build -o "$S/oldds" ./cmd/ds >"$S/build.log" 2>&1) &&
  ! (cd "$S/old/cli" && GOWORK=off GOPROXY=off go build -o "$S/oldds" ./cmd/ds >>"$S/build.log" 2>&1); then
  skip "cannot build $tag offline: $(tail -1 "$S/build.log")"
fi
old="$S/oldds"
echo "  previous release: $tag ($("$old" version 2>/dev/null))"

W="$S/w"; mkdir -p "$W/docs" "$W/internal" "$W/config" "$W/web"; cd "$W" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
# One def per tier the standard binary ships, with no Go string literal in
# the Go block (Upgrading for 0.1.4 covers those).
printf 'package internal\n\n// ds:def id=limit-k7m2p4xq\nfunc Limit(n int) int {\n\tif n > 10 {\n\t\treturn 10\n\t}\n\treturn n\n}\n' > internal/limit.go
printf 'server:\n  port: 8081   # ds:def id=port-h3v8n2wd\n  host: localhost\n' > config/app.yaml
printf '// ds:def id=greet-m4w8k2qn\nexport function greet(n: number): number {\n  return n + 1\n}\n' > web/greet.ts
printf '# ds:def id=area-p9c2v7ld\ndef area(w, h):\n    return w * h\n' > internal/area.py
printf '# Guide\n\nLimit caps at ten, see [Limit](ds:block?id=limit-k7m2p4xq).\nThe port is [8081](ds:cfg?id=port-h3v8n2wd).\nGreeting adds one: [greet](ds:block?id=greet-m4w8k2qn).\nArea multiplies: [area](ds:block?id=area-p9c2v7ld).\n' > docs/guide.md
"$old" init >/dev/null 2>&1 || { no "the old ds could not init"; echo; echo "  ---- $pass passed, $fail failed ----"; exit 1; }
"$old" scan >/dev/null 2>&1; git add -A; git commit -qm base
# A real change, acked with the old binary, so the ack log it wrote is read
# by the new one.
sed 's/return 10/return 12/' internal/limit.go > internal/limit.go.new && mv internal/limit.go.new internal/limit.go
"$old" scan >/dev/null 2>&1
line=$(grep -n 'limit-k7m2p4xq' docs/guide.md | cut -d: -f1)
"$old" ack limit-k7m2p4xq --doc docs/guide.md --line "$line" --note "cap raised" >/dev/null 2>&1
if "$old" check >/dev/null 2>&1; then ok "the old release scans, acks, and checks clean"; else no "the old release's own check is not clean: $("$old" check 2>&1 | tail -3)"; fi
git add -A; git commit -qm acked

out=$(ds scan 2>&1); code=$?
[ "$code" -eq 0 ] && ok "the new build scans the old release's state" || no "new scan failed: $out"
json=$(ds check --json 2>&1)
# Each finding is an object whose fields sit six spaces in: collect its
# state and id, print "<state> <id>" at its closing brace, keep all but ok.
found=$(printf '%s\n' "$json" | awk '
  /^      "state": / { st = $0; sub(/^ *"state": "/, "", st); sub(/",?$/, "", st) }
  /^      "id": /    { id = $0; sub(/^ *"id": "/, "", id); sub(/",?$/, "", id) }
  /^    }/           { if (st != "" && st != "ok") print st, id; st = ""; id = "" }
' | sort -u)
[ -n "$(printf '%s\n' "$json" | grep '"findings"')" ] || no "check --json printed no findings list: $json"
unexplained=""
for f in $(printf '%s\n' "$found" | tr ' ' '|'); do
  f=$(echo "$f" | tr '|' ' ')
  case "
$EXPECTED
" in *"
$f
"*) ;; *) unexplained="$unexplained$f; ";; esac
done
if [ -z "$unexplained" ]; then ok "the upgrade reports nothing the changelog does not explain"
else no "findings after upgrading from $tag that no Upgrading note explains: $unexplained"; fi
if ds check >/dev/null 2>&1 || [ -n "$EXPECTED" ]; then ok "check passes after the upgrade"; else no "check fails after the upgrade: $(ds check 2>&1 | tail -3)"; fi

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
