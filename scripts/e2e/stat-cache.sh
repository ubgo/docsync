#!/bin/sh
# the extraction cache trusts a file's size and modification time (git's
# index does the same), so a check need not read files that did not change.
# That shortcut must never hide an edit: an edit moves the time, a same-size
# edit included; a file edited within the racy window is re-read; and the
# one case it cannot see — content changed with its time put back, as
# `cp -p` or `touch -r` do — is caught by `check --full`, which reads all.
#
# Files are aged with touch -t rather than by sleeping, so the cache trusts
# them at once.
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
age() { touch -t 202601010000 "$@"; }
W="$S/w"; mkdir -p "$W/docs"; cd "$W" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
printf 'package p\n\n// ds:def id=alpha-k7m2p4xq\nfunc A() int { return 1 }\n' > a.go
printf '# D\n\nA returns [one](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md
ds init >/dev/null; age a.go docs/d.md; ds scan >/dev/null; git add -A; git commit -qm base
ck "the scan stamped the aged file" '"mod"' "$(cat .ds/cache/extract.json)"
ck "a clean tree checks clean" "1 ok" "$(ds check 2>&1 | tail -1)"

sedi 's/return 1/return 2/' a.go
ck "a same-size edit moves the time and is seen" "1 error" "$(ds check 2>&1 | tail -1)"
sedi 's/return 2/return 1/' a.go
ck "and the revert is seen too" "1 ok" "$(ds check 2>&1 | tail -1)"

# A filesystem whose clock ticks in whole seconds gives two writes in one
# second the same time. Simulated with touch -t: the file is checked with an
# untrustworthy time, then rewritten at the same size and given that same time
# again. The first check must not have trusted it, or the second edit is
# invisible.
#
# The stamp is a minute AHEAD, not the current second. The rule is that a time
# within racyWindow (2s) of the scan is not recorded, so stamping "now" only
# holds while the check runs inside those two seconds -- true alone, false
# under the load of a full run, where this case failed intermittently and the
# second edit really was invisible. A time in the future is never trustworthy
# however long the run takes, which is the same branch of the same rule with
# no dependence on how fast the machine is. Clock skew makes it a real case
# rather than only a convenient one.
soon=$(date -v+1M +%Y%m%d%H%M.%S 2>/dev/null || date -d '+1 minute' +%Y%m%d%H%M.%S)
[ -n "$soon" ] || { echo "  FAIL  cannot compute a future timestamp on this date(1)"; fail=$((fail+1)); soon=$(date +%Y%m%d%H%M.%S); }
touch -t "$soon" a.go
ck "checked clean at a time the scan cannot trust" "1 ok" "$(ds check 2>&1 | tail -1)"
sedi 's/return 1/return 5/' a.go; touch -t "$soon" a.go
ck "a same-size edit under an untrusted time is seen" "1 error" "$(ds check 2>&1 | tail -1)"
sedi 's/return 5/return 1/' a.go; age a.go; ds check >/dev/null 2>&1

# Content changed with its size and time put back, as cp -p does.
cp -p a.go "$S/keep.go"
printf 'package p\n\n// ds:def id=alpha-k7m2p4xq\nfunc A() int { return 7 }\n' > a.go; touch -r "$S/keep.go" a.go
ck "a time put back hides the edit from a plain check" "1 ok" "$(ds check 2>&1 | tail -1)"
ck "check --full reads every file and sees it" "1 error" "$(ds check --full 2>&1 | tail -1)"

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
