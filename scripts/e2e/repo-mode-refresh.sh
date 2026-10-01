#!/bin/sh
# repo mode: `ds refresh` writes a copy of each cited block into the doc and
# is a fixed point — a second refresh changes nothing, a copy whose code
# mentions a citation is still one copy, a CRLF doc keeps its line endings,
# a changed block is reported stale and refreshed, and a hand edit inside a
# copy is reported tampered.
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

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
