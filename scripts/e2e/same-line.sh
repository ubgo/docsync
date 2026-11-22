#!/bin/sh
# Several sentences on one line citing one block (bug 157, GitHub issue 9):
# each is a statement of its own, acked on its own. Acks used to be found by
# line, so the sentences on a line shared one: `ds ack --line` and `--all`
# recorded the first sentence only (twice, for --all), and the other stayed
# unacked forever, reported as the first one rewritten. Also a directive
# comment that shares its line with prose (bugs 160 and 161), and the
# upgrade from rule 1 that fix moved the extraction rule from.
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
pass=0; fail=0; n=0
ck() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
ID=demo-add-k7m2p4xq
# setup: the repository from the issue's replay script. Line 3 holds two
# sentences citing Add; the block then changes, so both are unacked.
setup() {
  n=$((n+1)); W="$S/case$n"; mkdir -p "$W"; cd "$W" || exit 1
  git init -q -b main .; git config user.email t@t; git config user.name t
  printf 'module example.com/demo\n\ngo 1.22\n' > go.mod
  printf 'package demo\n\n// ds:def id=%s owner=@x stability=stable\nfunc Add(a, b int) int {\n\treturn a + b\n}\n' "$ID" > demo.go
  printf '# Demo\n\n[`Add`](ds:block?id=%s) adds two numbers. [`Add`](ds:block?id=%s) never fails.\n' "$ID" "$ID" > README.md
  ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm init
  sedi 's/return a + b/return b + a/' demo.go; git commit -qam change; ds scan >/dev/null
}
acks() { grep -c "^[0-9]" .ds/acks.tsv 2>/dev/null || echo 0; }

# The issue, as reported: --line.
setup
ck "before the ack, both sentences are unacked" "2 error" "$(ds check 2>&1 | tail -1)"
out=$(ds ack $ID --doc README.md --line 3 --actor t 2>&1)
ck "ack --line acks the first sentence" "adds two numbers" "$out"
ck "and the second" "never fails" "$out"
ck "one ack row per sentence" "2" "$(acks)"
ck "after ack --line the check is clean" "2 ok" "$(ds check 2>&1 | tail -1)"
ck "and stays clean after a scan" "2 ok" "$(ds scan >/dev/null; ds check 2>&1 | tail -1)"

# The issue, as reported: --all, which made one request per finding and
# recorded the first sentence twice.
setup
ds ack $ID --all --actor t >/dev/null 2>&1
ck "ack --all records each sentence once" "2" "$(acks)"
ck "after ack --all the check is clean" "2 ok" "$(ds check 2>&1 | tail -1)"

# A commit message ack (ds:ack id=…) acks every citation of the id.
setup
git commit -q --allow-empty -m "Swap the operands

ds:ack id=$ID"
ds ack --from-commit HEAD >/dev/null 2>&1
ck "a commit-message ack acks both sentences" "2 ok" "$(ds check 2>&1 | tail -1)"

# A dry run quotes each sentence it would ack, each its own.
setup
out=$(ds ack $ID --doc README.md --line 3 --dry-run 2>&1)
ck "the dry run quotes the first sentence" "adds two numbers" "$out"
ck "and the second" "never fails" "$out"
ck "and records nothing" "0" "$(acks)"

# A sentence rewritten after the ack is reported against its own old
# wording, and its neighbour stays ok.
setup
ds ack $ID --doc README.md --line 3 >/dev/null 2>&1
sedi 's/never fails/cannot fail/' README.md
out=$(ds check 2>&1)
ck "the rewritten sentence is unacked" "sentence rewritten since the ack" "$out"
ck "its diff shows its own old wording" "-[\`Add\`](ds:block?id=$ID) never fails." "$out"
ck "its neighbour stays ok" "1 error, 1 ok" "$(echo "$out" | tail -1)"
ds ack $ID --doc README.md --line 3 >/dev/null 2>&1
ck "acking the line again clears it" "2 ok" "$(ds check 2>&1 | tail -1)"

# Acks of the two sentences made on two branches merge, and both count.
setup
git add -A; git commit -qm scanned
git checkout -qb one; ds ack $ID --doc README.md --line 3 --note one >/dev/null; git add -A; git commit -qm one
git checkout -q main; git checkout -qb two
sedi 's/never fails/cannot fail/' README.md; git commit -qam reword
git merge -q --no-edit one >/dev/null 2>&1 && merged=clean || merged=CONFLICT
ck "the ack merges into a branch that rewrote one sentence" "clean" "$merged"
out=$(ds check 2>&1)
ck "after the merge, the rewritten sentence alone is unacked" "1 error, 1 ok" "$(echo "$out" | tail -1)"

# A directive comment after a sentence whose paragraph goes on below it is
# about that sentence (bug 160): rewriting it is reported. It used to bind
# the next line's sentence, so the rewrite went unnoticed.
setup
printf '# Demo\n\nAdd sums its operands. <!-- ds:block id=%s -->\nIt is pure.\n' "$ID" > README.md
ds scan >/dev/null; ds ack $ID --doc README.md --line 3 >/dev/null 2>&1
ck "acked, the sentence a trailing comment follows is ok" "1 ok" "$(ds check 2>&1 | tail -1)"
sedi 's/sums its operands/adds two numbers/' README.md
ck "rewriting that sentence is reported" "sentence rewritten since the ack" "$(ds check 2>&1)"

# A comment that starts a line with prose after it leaves the prose in the
# hash of the section around it (bug 161): editing the prose flags the
# section's citations. The line used to be a carrier, left out of the hash.
setup
printf '# Demo\n\n<!-- ds:def id=policy-h3v8n2wd -->\n## Policy\n\n<!-- ds:claim reviewed=2026-09-01 expires=3650d --> Sessions last a day.\n\n## Use\n\nSee [the policy](ds:block?id=policy-h3v8n2wd).\n' > docs.md
ds scan >/dev/null; git add -A; git commit -qm policy
sedi 's/last a day/last a week/' docs.md
ck "editing prose after a leading comment flags the section's citation" "policy-h3v8n2wd changed" "$(ds check 2>&1)"

# The upgrade from rule 1 (0.1.9 and earlier): check warns, scan restamps,
# and nothing is refused.
setup
sedi '1s/extract=[0-9]*/extract=1/' .ds/ledger.tsv; sedi '1s/extract=[0-9]*/extract=1/' .ds/refs.tsv
ck "a rule 1 ledger is warned about, not refused" "older extraction rule" "$(ds check 2>&1)"
ds scan >/dev/null 2>&1
ck "the next scan restamps it under the current rule" "extract=5" "$(head -1 .ds/ledger.tsv)"

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
