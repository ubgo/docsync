#!/bin/sh
# a file the scan cannot read keeps its state and is reported: a long prose
# line is not a reason to skip a doc; a doc that outgrows the size limit
# fails the check instead of silently dropping its citations; and when it is
# readable again, an unreviewed change it carried is still reported. Before
# this, a doc with one long paragraph was skipped whole, every citation in
# it vanished, and the check went green.
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
# sedi edits a file in place the one way BSD, GNU and busybox sed all accept:
# a backup suffix attached to -i, and the backup removed.
sedi() { for _f in "$@"; do :; done; sed -i.bak "$@" && /bin/rm -f "$_f.bak"; }
S=$(mktemp -d)
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
ck() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
W="$S/w"; mkdir -p "$W/docs"; cd "$W" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
printf 'package p\n\n// ds:def id=alpha-k7m2p4xq\nfunc A() int { return 1 }\n' > a.go
long=$(printf 'This paragraph is written on one line, as unwrapped markdown is. %.0s' $(seq 1 50))
printf '# D\n\n%s\n\nA returns [one](ds:block?id=alpha-k7m2p4xq).\n' "$long" > docs/d.md
ds init >/dev/null
printf '\n[scan.limits]\nmax_file_kb = 8\n' >> .ds/config.toml
ds scan >/dev/null; git add -A; git commit -qm base
ck "a doc with a 3,000-character paragraph is scanned" "alpha-k7m2p4xq" "$(awk -F'\t' 'NR>2{print $1}' .ds/refs.tsv)"

sedi 's/return 1/return 2/' a.go; ds scan >/dev/null
ck "the change is flagged" "1 error" "$(ds check 2>&1 | tail -1)"

cp docs/d.md "$S/d.small"
for i in 1 2 3; do printf '\n%s\n' "$long" >> docs/d.md; done
warn=$(ds scan 2>&1 >/dev/null)
ck "scan names the unreadable doc" "docs/d.md not scanned (too-large)" "$warn"
out=$(ds check 2>&1); code=$?
ck "check reports it instead of going green" "unscanned" "$out"
ck "and fails" "1" "$code"

cp "$S/d.small" docs/d.md; ds scan >/dev/null
ck "readable again: the unreviewed change is still reported" "1 error" "$(ds check 2>&1 | tail -1)"

printf 'var x = "%s";\n' "$(printf 'x%.0s' $(seq 1 3000))" > min.js
printf '\000\001' > docs/.DS_Store
# ck matches a substring, so "nothing printed" needs its own test.
junk=$(ds scan 2>&1 >/dev/null)
if [ -z "$junk" ]; then echo "  PASS  a new minified file or binary junk is skipped without a warning"; pass=$((pass+1))
else echo "  FAIL  junk was named: $junk"; fail=$((fail+1)); fi
printf '// ds:def id=bee-h3v8n2wd\nfunction b() { return "%s"; }\n' "$(printf 'y%.0s' $(seq 1 10))" > b.js; ds scan >/dev/null
printf '// ds:def id=bee-h3v8n2wd\nfunction b() { return "%s"; }\n' "$(printf 'y%.0s' $(seq 1 3000))" > b.js
ck "a code file that held a block and turns minified is named" "b.js not scanned (long-line)" "$(ds scan 2>&1 >/dev/null)"

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
