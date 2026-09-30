#!/bin/sh
# drift in one repo: never-acked drift survives a scan, an ack clears it, volatile stays silent (bug 1)
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
set -e
# sedi edits a file in place the one way BSD, GNU and busybox sed all accept:
# a backup suffix attached to -i, and the backup removed.
sedi() { for _f in "$@"; do :; done; sed -i.bak "$@" && /bin/rm -f "$_f.bak"; }
S=$(mktemp -d)
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
check() { # name expected_exit actual_exit
  if [ "$2" = "$3" ]; then echo "  PASS  $1"; pass=$((pass+1));
  else echo "  FAIL  $1 (want exit $2, got $3)"; fail=$((fail+1)); fi
}

# ---------- local repo ----------
W="$S/m_local"; /bin/rm -rf "$W"; mkdir -p "$W/docs"; cd "$W"
git init -q -b main . >/dev/null; git config user.email t@t; git config user.name t
printf '# S\n\n### Depth\n\nAt most 12 deep.\n' > docs/spec.md
ds init >/dev/null
ID=$(ds def "docs/spec.md#Depth" --label depth | tail -1)
printf 'Twelve, see [rule](ds:block?id=%s).\n' "$ID" > docs/guide.md
ds scan >/dev/null; git add -A; git commit -qm a
set +e; ds check >/dev/null 2>&1; check "local: clean baseline" 0 $?; set -e

sedi 's/12 deep/6 deep/' docs/spec.md
set +e; ds check >/dev/null 2>&1; check "local: never-acked drift, no scan" 1 $?; set -e
ds scan >/dev/null
set +e; ds check >/dev/null 2>&1; check "local: never-acked drift SURVIVES scan" 1 $?; set -e

ds ack "$ID" --doc docs/guide.md --line 1 --note ok >/dev/null
set +e; ds check >/dev/null 2>&1; check "local: ack clears" 0 $?; set -e
ds scan >/dev/null
set +e; ds check >/dev/null 2>&1; check "local: still clear after scan" 0 $?; set -e

sedi 's/6 deep/3 deep/' docs/spec.md
ds scan >/dev/null
set +e; ds check >/dev/null 2>&1; check "local: acked drift SURVIVES scan" 1 $?; set -e

# volatile must stay quiet
W="$S/m_vol"; /bin/rm -rf "$W"; mkdir -p "$W/docs"; cd "$W"
git init -q -b main . >/dev/null; git config user.email t@t; git config user.name t
printf '# S\n\n<!-- ds:def id=v-k7m2p4xq stability=volatile -->\n### D\n\nAt most 12.\n' > docs/spec.md
printf 'See [rule](ds:block?id=v-k7m2p4xq).\n' > docs/guide.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm a
sedi 's/12\./6./' docs/spec.md; ds scan >/dev/null
set +e; ds check >/dev/null 2>&1; check "local: volatile stays silent" 0 $?; set -e

echo
echo "  ---- $pass passed, $fail failed ----"
[ "$fail" = "0" ]
