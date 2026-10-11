#!/bin/sh
# repo mode: `ds refresh` writes a copy of each cited block into the doc and
# is a fixed point — a second refresh changes nothing, a copy whose code
# mentions a citation is still one copy, a CRLF doc keeps its line endings,
# a changed block is reported stale and refreshed, and a hand edit inside a
# copy is reported tampered. A copy refresh cannot write is named by
# refresh and reported by check for what it is, never as stale (bug 136).
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
same() { if [ "$2" = "$3" ]; then echo "  PASS  $1"; pass=$((pass+1)); else echo "  FAIL  $1"; fail=$((fail+1)); fi; }
W="$S/w"; mkdir -p "$W/docs"; cd "$W" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
printf 'package p\n\n// ds:def id=alpha-k7m2p4xq\nfunc A() int {\n\t// see <!-- ds:block id=alpha-k7m2p4xq -->\n\treturn 1\n}\n' > a.go
printf '# D\r\n\r\nIntro.\r\n\r\n<!-- ds:block id=alpha-k7m2p4xq -->\r\n\r\nThe end.\r\n' > docs/d.md
ds init >/dev/null
sedi 's/^mode = "build"/mode = "repo"/' .ds/config.toml
ds scan >/dev/null
ds refresh >/dev/null
first=$(cat docs/d.md)
ck "refresh writes the copy" "return 1" "$first"
ck "and closes it" "/ds:block hash=" "$first"
ds scan >/dev/null; ds refresh >/dev/null
same "a second refresh changes nothing" "$first" "$(cat docs/d.md)"
ck "one copy, although its code mentions a citation" "1" "$(grep -c '/ds:block hash=' docs/d.md)"
# Counted as bytes: a grep for a trailing CR depends on whether this
# platform's grep reads text mode, which is not what is under test.
crlf=$(tr -cd '\r' < docs/d.md | wc -c | tr -d ' '); lines=$(wc -l < docs/d.md | tr -d ' ')
ck "the CRLF doc keeps its line endings" "$lines" "$crlf"
ck "a fresh copy checks clean" " ok" "$(ds check 2>&1 | tail -1)"

sedi 's/return 1/return 2/' a.go; ds scan >/dev/null
ck "a changed block makes the copy stale" "stale" "$(ds check 2>&1)"
ds refresh >/dev/null
ck "refresh brings it up to date" "return 2" "$(cat docs/d.md)"

sedi 's/return 2/return 99/' docs/d.md
ck "a hand edit inside the copy is tampered" "tampered" "$(ds check 2>&1)"

# GitHub issue 4 (bug 136): a lines= range one past the end of a 45-line
# block. refresh skipped the copy and said only "0 repo-mode copies
# rewritten"; check called it stale and told the reader to run refresh,
# forever, and ack did not clear it. The whole sequence from the report.
I="$S/issue4"; mkdir -p "$I"; cd "$I" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
{ printf 'package cfg\n\n// Config holds server settings.\ntype Config struct {\n'
  i=1; while [ $i -le 43 ]; do printf '\tField%02d string\n' $i; i=$((i+1)); done
  printf '}\n'; } > config.go
ds init >/dev/null
sedi 's/^mode = "build"/mode = "repo"/' .ds/config.toml
id=$(ds def config.go#Config 2>&1 | grep -o 'config-[a-z0-9]*' | head -1)
ck "ds def mints an id for the 45-line struct" "config-" "$id"
printf '# Config\n\n<!-- ds:block id=%s lines=1-46 -->\n<!-- /ds:block hash=000000 -->\n' "$id" > doc.md
out=$(ds refresh 2>&1)
ck "bug 136: refresh names the skipped copy and why" "skipped doc.md:3: lines=1-46 is outside the block's 45 lines" "$out"
ck "bug 136: and counts it" "1 repo-mode copies skipped" "$out"
before=$(cat doc.md)
ds ack "$id" --doc doc.md --line 3 --note ok >/dev/null 2>&1
out=$(ds check 2>&1); code=$?
ck "bug 136: check reports range as an error, after ack too" "error    range              lines=1-46 outside the block's 45 lines" "$out"
ck "bug 136: its fix is to adjust lines=, not to refresh" "adjust lines= at doc.md:3 to fit the block's 45 lines" "$out"
case "$out" in *stale*) echo "  FAIL  bug 136: check still says stale"; fail=$((fail+1));; *) echo "  PASS  bug 136: check no longer says stale"; pass=$((pass+1));; esac
ck "bug 136: check exits 1" "1" "$code"
out2=$(ds refresh 2>&1)
ck "bug 136: a second refresh reports the same skip" "skipped doc.md:3: lines=1-46 is outside the block's 45 lines" "$out2"
same "bug 136: and changes nothing" "$before" "$(cat doc.md)"
# 45 lines is over the default cap of 40 (too-large, as the report
# expects), so the cap is raised to see the whole copy written.
sedi 's/^max_lines = .*/max_lines = 50/' .ds/config.toml
sedi 's/lines=1-46/lines=1-45/' doc.md
out=$(ds refresh 2>&1)
ck "bug 136: fixed to lines=1-45, refresh writes the copy" "1 repo-mode copies rewritten" "$out"
case "$out" in *skipped*) echo "  FAIL  bug 136: refresh still skips the fixed copy"; fail=$((fail+1));; *) echo "  PASS  bug 136: nothing skipped once lines= fits"; pass=$((pass+1));; esac
ck "bug 136: the copy holds the block's last line" "Field43 string" "$(cat doc.md)"
ck "bug 136: and check goes clean" "1 ok" "$(ds check 2>&1 | tail -1)"

# The same shape, found by the sweep for it: a copy refresh could not
# write that check called stale. env=prod was rendered from the first def
# of the id instead of prod's (bug 137); a secret is never rendered, so its
# copy is a problem with another carrier as the fix (bug 138).
E="$S/env"; mkdir -p "$E"; cd "$E" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
printf 'port: 8080 # ds:def id=port-k7m2p4xq env=dev\n' > dev.yaml
printf 'port: 443 # ds:def id=port-k7m2p4xq env=prod\n' > prod.yaml
printf 'key: op://vault/stripe # ds:def id=key-h3v8n2wd secret=true truth=true\n' > secret.yaml
printf '# P\n\n<!-- ds:block id=port-k7m2p4xq env=prod -->\n\n<!-- ds:block id=key-h3v8n2wd -->\n' > doc.md
ds init >/dev/null
sedi 's/^mode = "build"/mode = "repo"/' .ds/config.toml
out=$(ds refresh 2>&1)
ck "bug 137: the env=prod copy is written" "1 repo-mode copies rewritten" "$out"
ck "bug 137: from prod's block" "443" "$(sed -n 3,10p doc.md)"
ck "bug 138: the secret's copy is named as skipped" "skipped doc.md:" "$out"
out=$(ds check 2>&1)
case "$out" in *stale*) echo "  FAIL  bugs 137 and 138: check says stale: $out"; fail=$((fail+1));; *) echo "  PASS  bugs 137 and 138: nothing is stale"; pass=$((pass+1));; esac
ck "bug 138: the secret's copy is a problem with a carrier that works as the fix" "ds:cfg?id=key-h3v8n2wd" "$out"

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
