#!/bin/sh
# acks recorded on two branches merge: ds init writes .ds/.gitattributes with
# acks.tsv merge=union, so the append-only log never conflicts, and the newest
# ack for a sentence wins whatever order the merge put the rows in. A repo
# without the attribute is told by doctor; a conflict that does happen is
# named as one, with the right remedy.
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
# sedi edits a file in place the one way BSD, GNU and busybox sed all accept:
# a backup suffix attached to -i, and the backup removed.
sedi() { for _f in "$@"; do :; done; sed -i.bak "$@" && /bin/rm -f "$_f.bak"; }
S=$(mktemp -d)
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0; n=0
ck() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
setup() {
  n=$((n+1)); W="$S/case$n"; mkdir -p "$W/docs"; cd "$W" || exit 1
  git init -q -b main .; git config user.email t@t; git config user.name t
  printf 'package p\n\n// ds:def id=alpha-k7m2p4xq\nfunc A() int { return 1 }\n\n// ds:def id=beta-h3v8n2wd\nfunc B() int { return 1 }\n' > a.go
  printf '# D\n\nA is [one](ds:block?id=alpha-k7m2p4xq).\n\nB is [one](ds:block?id=beta-h3v8n2wd).\n' > docs/d.md
  ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm base
  sedi 's/return 1/return 2/g' a.go; ds scan >/dev/null; git add -A; git commit -qm drift
}
merged() { if git merge -q --no-edit "$1" >/dev/null 2>&1; then echo clean; else echo "CONFLICT"; git merge --abort 2>/dev/null; fi; }

# Two branches ack different citations.
setup
git checkout -qb one; ds ack alpha-k7m2p4xq --doc docs/d.md --line 3 --note one >/dev/null; git add -A; git commit -qm one
git checkout -q main; git checkout -qb two; ds ack beta-h3v8n2wd --doc docs/d.md --line 5 --note two >/dev/null; git add -A; git commit -qm two
git checkout -q one
ck "acks on two branches merge cleanly" "clean" "$(merged two)"
ck "both acks count after the merge" "2 none" "$(ds check 2>&1 | tail -1)"

# The same citation acked on two branches, the newer one against a newer block.
# Whichever order the branches merge in, the newer ack must be the one used.
for order in newer-last newer-first; do
  setup
  git checkout -qb old; ds ack alpha-k7m2p4xq --doc docs/d.md --line 3 --note old >/dev/null
  ds ack beta-h3v8n2wd --doc docs/d.md --line 5 --note b >/dev/null; git add -A; git commit -qm old
  git checkout -q main; git checkout -qb new; sleep 1
  sedi 's/func A() int { return 2 }/func A() int { return 3 }/' a.go; ds scan >/dev/null
  ds ack alpha-k7m2p4xq --doc docs/d.md --line 3 --note new >/dev/null
  ds ack beta-h3v8n2wd --doc docs/d.md --line 5 --note b >/dev/null; git add -A; git commit -qm new
  if [ "$order" = newer-last ]; then git checkout -q old; m=$(merged new); else git checkout -q new; m=$(merged old); fi
  ck "same citation acked on both branches ($order): merge" "clean" "$m"
  ds scan >/dev/null
  ck "same citation acked on both branches ($order): the newer ack wins" "2 none" "$(ds check 2>&1 | tail -1)"
done

# A repo initialised before the attribute existed.
setup; /bin/rm -f .ds/.gitattributes
ck "doctor names the missing merge attribute" "acks.tsv merge=union" "$(ds doctor 2>&1 | grep gitattributes)"

# A conflict that does happen is named, with the remedy for that file.
setup; printf '<<<<<<< HEAD\n=======\n>>>>>>> x\n' >> .ds/refs.tsv
out=$(ds check 2>&1)
ck "a conflicted refs.tsv is named as a merge conflict" "unresolved merge conflict" "$out"
ck "and told to keep both sides" "keep every row from both sides" "$out"

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
