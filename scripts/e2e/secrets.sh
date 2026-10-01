#!/bin/sh
# promise:secret-never-shown
# A secret's value never reaches any output (spec §12: the tool "never stores,
# renders, or logs a value"). Every command that prints block content -- and
# the MCP tools that hand content to an agent -- is run over a repository that
# cites a secret, in three states: clean, changed but not scanned, and changed
# and scanned. The value must appear in none of them.
#
# Why a sweep rather than one test per command: the leak this pins was found in
# seven commands at once, because each read a def's content and nothing stopped
# a secret's from being a real credential. A new command that prints content is
# a new place to leak, so the list below is the thing to extend.
#
# The other half matters as much: an address is safe to render and must still
# render, and a rotated secret must still be detected, or "no leak" was bought
# by breaking the feature.
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
# sedi edits a file in place the one way BSD, GNU and busybox sed all accept:
# a backup suffix attached to -i, and the backup removed.
sedi() { for _f in "$@"; do :; done; sed -i.bak "$@" && /bin/rm -f "$_f.bak"; }
S=$(mktemp -d)
# Under Git Bash, mktemp gives an MSYS path (/tmp/...) that only MSYS programs
# understand; ds.exe reads it from a config file as a path on no drive. The
# mixed form (C:/...) is one both sides accept.
command -v cygpath >/dev/null 2>&1 && S=$(cygpath -m "$S")
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
ok() { echo "  PASS  $1"; pass=$((pass+1)); }
no() { echo "  FAIL  $1"; fail=$((fail+1)); }
W="$S/w"; mkdir -p "$W/docs" "$W/config" "$W/.github" "$W/out"; cd "$W" || exit 1
git init -q .; git config user.email t@t; git config user.name t
printf 'api_key: sk-live-SENTINELOLD # ds:def id=key-k7m2p4xq secret=true\nport: 8080 # ds:def id=port-h3v8n2wd\n' > config/app.yaml
printf 'env:\n  STRIPE_KEY: ${{ secrets.STRIPE_KEY }} # ds:def id=gh-key-t4k2b9rf secret=true\n' > .github/deploy.yml
printf '# D\n\nThe [key](ds:cfg?id=key-k7m2p4xq) is set, the [ref](ds:cfg?id=gh-key-t4k2b9rf) is named, and the [port](ds:cfg?id=port-h3v8n2wd) too.\n\n<!-- ds:block id=key-k7m2p4xq -->\n' > docs/d.md
# A source=env hop whose variable holds a value: `check --resolve` runs the
# shipped ds-resolve-env over it, and neither the plugin nor any finding may
# print what the variable holds (bug 83).
printf 'env_var: DS_E2E_SECRET # ds:def id=app-key-m4w8k2qn secret=true source=env from=gh-key-t4k2b9rf\n' > config/env.yaml
DS_E2E_SECRET=sk-live-SENTINELENV; export DS_E2E_SECRET
ds init >/dev/null; printf '\n[resolve]\nenabled = true\nproviders = ["env"]\n' >> .ds/config.toml
git add -A; git commit -qm a; ds scan >/dev/null; git add -A; git commit -qm b

mcp() { # $1 tool, $2 arguments as JSON
  printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"$1\",\"arguments\":$2}}" \
    | ds mcp 2>/dev/null
}

sweep() { # $1 describes the state
  leaked=""
  for cmd in "check" "check --json" "check --full --json" "render docs/d.md" "facts" "facts --json" \
             "context docs/d.md" "context key-k7m2p4xq" "read key-k7m2p4xq" "why key-k7m2p4xq" \
             "why key-k7m2p4xq --history" "find key" "find --json key" "locate key-k7m2p4xq" \
             "blame docs/d.md 3" "status" "report" "triage" "graph" "review" "map" "impact" "audit" \
             "export hugo --out out" "check --resolve" "check --resolve --json" \
             "render docs/d.md --at HEAD" "context docs/d.md --since HEAD" \
             "context key-k7m2p4xq --since HEAD" "context docs/d.md --mode diff" "context docs/d.md --since ack"; do
    # $cmd is split into words on purpose: it is a command and its arguments.
    o=$(ds $cmd 2>&1; cat out/*.json 2>/dev/null)
    /bin/rm -f out/*.json
    case "$o" in *SENTINEL*) leaked="$leaked [ds $cmd]";; esac
  done
  for t in read facts context why check; do
    case $t in read|why) a='{"id":"key-k7m2p4xq"}';; context) a='{"target":"key-k7m2p4xq"}';; *) a='{}';; esac
    case "$(mcp "$t" "$a")" in *SENTINEL*) leaked="$leaked [mcp $t]";; esac
  done
  if [ -z "$leaked" ]; then ok "$1: the value appears in no command and no MCP tool"; else no "$1: the value leaked through$leaked"; fi
}

sweep "a clean tree"
# The resolve sweep above is only evidence if the env plugin actually ran.
case "$(ds check --resolve 2>&1)" in
  *"provider env not reachable"*|*"resolve.enabled is false"*) no "check --resolve never asked ds-resolve-env: $(ds check --resolve 2>&1 | tail -4)";;
  *) ok "check --resolve asked ds-resolve-env about the variable";;
esac
sedi 's/SENTINELOLD/SENTINELNEW/' config/app.yaml
sweep "a changed secret, not yet scanned"
# A rotated value is still detected: only the content is withheld, never the hash.
if ds check 2>&1 | grep -q "key-k7m2p4xq changed"; then ok "a rotated secret is still reported as changed"; else no "a rotated secret went unnoticed: $(ds check 2>&1 | tail -3)"; fi
ds scan >/dev/null
sweep "a changed secret, scanned"
# An address is safe to render and still renders.
r=$(ds render docs/d.md 2>/dev/null)
if echo "$r" | grep -q '\${{ secrets.STRIPE_KEY }}\|STRIPE_KEY'; then ok "a secret's address still renders"; else no "an address stopped rendering: $r"; fi
if echo "$r" | grep -q "The key is set"; then ok "a withheld value renders as the author's link text"; else no "a withheld value rendered as: $r"; fi
if ds facts 2>&1 | grep -q '8080'; then ok "an ordinary value is unaffected"; else no "facts lost an ordinary value: $(ds facts 2>&1)"; fi

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
