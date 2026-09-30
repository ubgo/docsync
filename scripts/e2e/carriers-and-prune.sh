#!/bin/sh
# a neighbour anchor never changes a block hash; Locate in asciidoc and rst; prune dry run (bugs 4, 6, 7)
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
set -u
# sedi edits a file in place the one way BSD, GNU and busybox sed all accept:
# a backup suffix attached to -i, and the backup removed.
sedi() { for _f in "$@"; do :; done; sed -i.bak "$@" && /bin/rm -f "$_f.bak"; }
S=$(mktemp -d)
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
ck() { if [ "$2" = "$3" ]; then echo "  PASS  $1"; pass=$((pass+1)); else echo "  FAIL  $1 (want $2, got $3)"; fail=$((fail+1)); fi; }

# ---- bug 4 markdown / asciidoc / rst carriers, bug 7 Locate ----
W="$S/m4a"; /bin/rm -rf "$W"; mkdir -p "$W"; cd "$W" || exit 1
git init -q -b main . >/dev/null; git config user.email t@t; git config user.name t
printf '# S\n\n### Depth\n\nAt most 12 deep.\n\n### Width\n\nAt most 4 wide.\n' > spec.md
printf '= D\n\n== First\n\nOne.\n\n== Second\n\nTwo.\n' > doc.adoc
printf 'Title\n=====\n\nIntro.\n\nSecond\n======\n\nMore.\n' > doc.rst
ds init >/dev/null
A=$(ds def "spec.md#Depth" --label depth | tail -1)
printf 'See [d](ds:block?id=%s).\n' "$A" > guide.md
ds scan >/dev/null; ds ack "$A" --doc guide.md --line 1 --note ok >/dev/null; ds scan >/dev/null
H1=$(grep "^$A" .ds/ledger.tsv | cut -f7)
ds def "spec.md#Width" --label width >/dev/null; ds scan >/dev/null
H2=$(grep "^$A" .ds/ledger.tsv | cut -f7)
ck "bug 4: anchoring a neighbour leaves the hash alone" "$H1" "$H2"
ds check >/dev/null 2>&1; ck "bug 4: no false unacked" 0 $?
ds def "doc.adoc#First" --label adoc >/dev/null 2>&1; ck "bug 7: ds def on .adoc#Symbol" 0 $?
ds def "doc.rst#Second" --label rst  >/dev/null 2>&1; ck "bug 7: ds def on .rst#Symbol" 0 $?

# ---- bug 6 prune ----
W="$S/m4b"; /bin/rm -rf "$W"; mkdir -p "$W"; cd "$W" || exit 1
git init -q -b main . >/dev/null; git config user.email t@t; git config user.name t
printf '# S\n\n### D\n\nAt most 12 deep.\n' > spec.md
ds init >/dev/null
B=$(ds def "spec.md#D" --label d | tail -1)
printf 'See [d](ds:block?id=%s).\n' "$B" > guide.md
ds scan >/dev/null; ds ack "$B" --doc guide.md --line 1 --note ok >/dev/null; ds scan >/dev/null
for n in 11 10 9; do sedi "s/At most [0-9]* deep/At most $n deep/" spec.md; ds scan >/dev/null; done
N1=$(ls .ds/blocks | wc -l | tr -d ' ')
ds prune --dry-run >/dev/null 2>&1; ck "bug 6: prune --dry-run exits 0" 0 $?
N2=$(ls .ds/blocks | wc -l | tr -d ' ')
ck "bug 6: --dry-run removes nothing" "$N1" "$N2"
ds prune --keep 0h >/dev/null 2>&1
N3=$(ls .ds/blocks | wc -l | tr -d ' ')
ck "bug 6: prune keeps exactly the live set" 2 "$N3"
ds check --json 2>/dev/null | grep -q '"body"'; ck "bug 6: a pruned store still classifies" 0 $?
ds doctor 2>/dev/null | grep -q "blocks"; ck "bug 6: doctor reports the store" 0 $?

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" = "0" ]
