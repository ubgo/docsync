#!/bin/sh
# every read-only command, on a repo with drift, an ack, a fact and history:
# each exits 0 or 1 and never panics, writes nothing that git tracks, emits
# JSON that parses when asked for --json, and names the id it was asked
# about. A query that quietly rewrote a ledger, or printed JSON a tool
# could not read, would be found here rather than in someone's CI.
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
ck() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
W="$S/w"; mkdir -p "$W/docs" "$W/internal"; cd "$W" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
printf 'package p\n\n// ds:def id=alpha-k7m2p4xq owner=@core tags=store\nfunc Alpha() int { return 1 }\n\n// ds:def id=limit-h3v8n2wd\nconst Limit = 10\n\n// ds:def id=orphan-t4k2b9rf\nfunc Orphan() {}\n' > internal/a.go
printf '# Guide\n\nAlpha [returns one](ds:block?id=alpha-k7m2p4xq).\n\nThe limit is [10](ds:cfg?id=limit-h3v8n2wd).\n' > docs/d.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm base
sedi 's/return 1/return 2/' internal/a.go
ds scan >/dev/null; ds ack limit-h3v8n2wd --doc docs/d.md --line 5 --note fine >/dev/null 2>&1; git add -A; git commit -qm drift
tracked() { git ls-files -co --exclude-standard | sort | xargs cat 2>/dev/null | cksum; }
id=alpha-k7m2p4xq
while IFS='|' read -r mention args; do
  [ -z "$args" ] && continue
  before=$(tracked)
  out=$(eval "ds $args" 2>&1); code=$?
  after=$(tracked)
  bad=""
  [ "$code" -le 1 ] || bad="exit $code"
  case "$out" in *"panic:"*|*"goroutine "*) bad="panicked";; esac
  [ "$before" = "$after" ] || bad="${bad:+$bad; }changed tracked files"
  case "$args" in *--json*)
    printf '%s' "$out" | python3 -c 'import json,sys; json.load(sys.stdin)' 2>/dev/null || bad="${bad:+$bad; }json does not parse";;
  esac
  if [ -n "$mention" ]; then case "$out" in *"$mention"*) ;; *) bad="${bad:+$bad; }does not mention $mention";; esac; fi
  if [ -z "$bad" ]; then echo "  PASS  ds $args"; pass=$((pass+1)); else echo "  FAIL  ds $args: $bad"; echo "$out" | head -3 | sed 's/^/          /'; fail=$((fail+1)); fi
done <<EOF
$id|why $id
$id|why $id --json
$id|why $id --chain
$id|why $id --history
|impact
|impact --json
|impact --staged
$id|find alpha
$id|find alpha --json
$id|find --file internal/a.go
$id|find --tag store
Alpha|read $id
Alpha|read $id --lines 1-1
internal/a.go|locate $id
$id|map
$id|map --json
|map --budget 50
10|facts
10|facts --json
|facts --cited-by docs/d.md
$id|context docs/d.md
$id|context docs/d.md --json
Alpha|context $id
|context docs/d.md --since ack
$id|graph
$id|graph --json
$id|graph --dot
$id|blame docs/d.md 3
$id|blame docs/d.md 3 --json
limit-h3v8n2wd|audit
limit-h3v8n2wd|audit --json
limit-h3v8n2wd|audit --id limit-h3v8n2wd
|audit --actor-kind agent
|report
|report --json
|report --gaps
|report --stalest
|report --uncovered
|report --literals
|report --unmarked
|report --orphaned-owners
|report --metrics
|status
|status --json
|review
|export hugo --out $S/hugo
EOF
[ -f "$S/hugo/blocks.json" ] && python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$S/hugo/blocks.json" && { echo "  PASS  export hugo writes blocks.json that parses"; pass=$((pass+1)); } || { echo "  FAIL  export hugo did not write readable blocks.json"; fail=$((fail+1)); }
# --dir, as the editor and docs-site integrations document it: from an
# unrelated directory the command sees the repository, and `ds --dir R lsp`
# starts rather than exiting on an unknown flag.
out=$(cd "$S" && ds --dir "$W" check 2>&1 | tail -1)
ck "--dir checks the repository from elsewhere" "$(ds check 2>&1 | tail -1)" "$out"
ck "--dir works with status --json, as the Docusaurus plugin runs it" '"refs"' "$(cd "$S" && ds --dir "$W" status --json 2>&1)"
lsp=$(cd "$S" && printf 'Content-Length: 58\r\n\r\n{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' | ds --dir "$W" lsp 2>&1)
ck "ds --dir R lsp starts, as the VS Code settings describe" '"hoverProvider":true' "$lsp"
ck "a --dir that is not a directory is refused" "is not a directory" "$(ds --dir "$S/nope" check 2>&1)"
# From a subdirectory the root is found, as git finds .git, and paths are
# read from where the user is and printed repository-relative.
ck "check from a subdirectory finds the root" "$(ds check 2>&1 | tail -1)" "$(cd "$W/docs" && ds check 2>&1 | tail -1)"
ck "render takes a path relative to the subdirectory" "The limit is 10" "$(cd "$W/docs" && ds render d.md 2>&1)"
ck "a path outside the repository is refused" "outside the repository" "$(cd "$W/docs" && ds render ../../x.md 2>&1)"
echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
