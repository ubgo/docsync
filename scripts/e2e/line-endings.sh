#!/bin/sh
# a CRLF checkout is the same repository: state scanned and committed on an
# LF machine checks clean after every file, the .ds state included, is
# rewritten with CRLF endings — what core.autocrlf does on a Windows clone.
# Before, a file= def with pick=line:N failed to resolve and a $-anchored
# regex matched nothing, so CI on Windows reported drift nobody had made.
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
S=$(mktemp -d)
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
ck() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
crlf() { find . -type f -not -path './.git/*' | while read -r f; do perl -pi -e 's/\r?\n/\r\n/' "$f"; done; }
W="$S/w"; mkdir -p "$W/docs" "$W/config" "$W/internal"; cd "$W" || exit 1
git init -q .; git config user.email t@t; git config user.name t
printf 'package store\n\n// ds:def id=save-k7m2p4xq\nfunc Save() error {\n\treturn nil\n}\n' > internal/store.go
printf 'auth:\n  port: 8081 # ds:def id=auth-port-h3v8n2wd\n' > config/auth.yaml
printf 'A=1\nport: 9090\n' > config/plain.txt
printf 'ds:def id=line-port-a2b6f8jk file=config/plain.txt pick=line:2\nds:def id=re-port-r4t6x2mb file=config/plain.txt pick="regex:^port: (\\d+)$"\n' > config/remote.txt
printf '# D\n\nAuth on [8081](ds:cfg?id=auth-port-h3v8n2wd), plain on [9090](ds:cfg?id=re-port-r4t6x2mb) and [port: 9090](ds:cfg?id=line-port-a2b6f8jk).\n\n<!-- ds:block id=save-k7m2p4xq lines=2 -->\n' > docs/d.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm lf
lf=$(ds check 2>&1 | tail -1)
ck "the LF repository checks clean" " none" "$lf"
ck "every citation resolved on LF" "4 none" "$lf"
crlf
ck "the conversion really wrote CRLF, one per line" "2 CR" "$(tr -cd '\r' < config/plain.txt | wc -c | tr -d ' ') CR"
ck "a CRLF checkout of the same commit checks as clean" "$lf" "$(ds check 2>&1 | tail -1)"
out=$(ds render docs/d.md 2>&1)
ck "render reads the regex pick on CRLF" "plain on 9090" "$out"
ck "render reads the line pick on CRLF" "and port: 9090" "$out"
case "$out" in *"$(printf '\r')"*) echo "  FAIL  a carriage return leaked into rendered values"; fail=$((fail+1));; *) echo "  PASS  no carriage return leaks into rendered values"; pass=$((pass+1));; esac
cp .ds/ledger.tsv "$S/ledger.before"
ds scan >/dev/null
tr -d '\r' < "$S/ledger.before" | sed 1d > "$S/a"; tr -d '\r' < .ds/ledger.tsv | sed 1d > "$S/b"
ck "scanning the CRLF checkout rewrites no hash" "same" "$(cmp -s "$S/a" "$S/b" && echo same || diff "$S/a" "$S/b")"
echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
