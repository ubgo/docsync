#!/bin/sh
# a claim ages out and is renewed from the CLI: an expired claim's remedy is
# `ds ack --doc D --line N`, and that command renews it (it used to be a
# usage error, so no claim could be renewed); the renewal survives a line
# added above the claim, is not inherited by a rewritten sentence, and an
# id-less ack on a line without a claim is refused.
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
S=$(mktemp -d)
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
ck() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
W="$S/w"; mkdir -p "$W/docs"; cd "$W" || exit 1
git init -q .; git config user.email t@t; git config user.name t
printf 'package p\n' > a.go
printf '# D\n\nWe chose Postgres. <!-- ds:claim owner=@p reviewed=2020-01-01 expires=90d -->\n\nNo claim here.\n' > docs/d.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm b
out=$(ds check 2>&1)
ck "an old claim is expired" "expired" "$out"
ck "and the remedy says how to renew it" "ds ack --doc docs/d.md --line 3" "$out"
ck "that exact command renews it" "renewed the claim at docs/d.md:3" "$(ds ack --doc docs/d.md --line 3 --note 'still true' 2>&1)"
ck "the claim is now current" "1 none" "$(ds check 2>&1 | tail -1)"
printf '# D\n\nAn intro.\n\nWe chose Postgres. <!-- ds:claim owner=@p reviewed=2020-01-01 expires=90d -->\n\nNo claim here.\n' > docs/d.md
ck "the renewal survives a line added above the claim" "1 none" "$(ds check 2>&1 | tail -1)"
printf '# D\n\nAn intro.\n\nWe moved to MySQL. <!-- ds:claim owner=@p reviewed=2020-01-01 expires=90d -->\n\nNo claim here.\n' > docs/d.md
ck "a rewritten sentence does not inherit it" "expired" "$(ds check 2>&1)"
ck "an id-less ack where there is no claim is refused" "no reference" "$(ds ack --doc docs/d.md --line 7 2>&1 | tr 'A-Z' 'a-z')"
echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
