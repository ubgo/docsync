#!/bin/sh
# several ds processes at once: ten acks recorded in parallel all land, scans
# running together leave a ledger that reads, and defs written together each
# get a journal batch undo can reverse. Before the .ds/ lock, ten parallel
# acks recorded one while every command reported success.
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
# notes counts the ack rows carrying note $1, by column: the note is not the
# last column since the ack log grew rule and sentence columns.
notes() { awk -F'\t' -v n="$1" 'NR>2 && $12==n' .ds/acks.tsv | grep -c .; }
W="$S/w"; mkdir -p "$W/docs"; cd "$W" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
printf 'package p\n' > a.go; printf '# D\n\n' > docs/d.md
i=0; while [ $i -lt 10 ]; do
  printf '\n// ds:def id=f%d-k7m2p4xq\nfunc F%d() int { return 1 }\n' $i $i >> a.go
  printf 'F%d [x](ds:block?id=f%d-k7m2p4xq).\n\n' $i $i >> docs/d.md
  i=$((i+1)); done
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm base
sedi 's/return 1/return 2/' a.go; ds scan >/dev/null
ck "ten unreviewed changes" "10 error" "$(ds check 2>&1 | tail -1)"
i=0; while [ $i -lt 10 ]; do ds ack f$i-k7m2p4xq --doc docs/d.md --line $((3 + i*2)) --note c >/dev/null 2>&1 & i=$((i+1)); done; wait
ck "ten parallel acks all land" "10" "$(awk 'NR>2' .ds/acks.tsv | grep -c .)"
ck "and the check agrees" "10 ok" "$(ds check 2>&1 | tail -1)"

i=0; while [ $i -lt 8 ]; do ds scan >/dev/null 2>&1 & i=$((i+1)); done; wait
ck "eight parallel scans leave a ledger that reads" "10 ok" "$(ds check 2>&1 | tail -1)"

printf 'package q\n\nfunc G() int { return 1 }\n' > b.go
printf 'package q\n\nfunc H() int { return 1 }\n' > c.go
git add -A; git commit -qm more
ds def b.go#G >/dev/null 2>&1 & ds def c.go#H >/dev/null 2>&1 & wait
ck "two parallel defs are two journal batches" "2" "$(ds undo --list 2>&1 | grep -c ' def ')"

# Acks and scans at once: a scan reads the ack log and refs before it writes
# refs, so one that started before an ack must not write that ack away.
git add -A; git commit -qm acked
sedi 's/return 2/return 3/' a.go; ds scan >/dev/null
ck "ten new unreviewed changes" "10 error" "$(ds check 2>&1 | tail -1)"
i=0; while [ $i -lt 10 ]; do
  ds ack f$i-k7m2p4xq --doc docs/d.md --line $((3 + i*2)) --note c2 >/dev/null 2>&1 &
  [ $((i % 2)) -eq 0 ] && { ds scan >/dev/null 2>&1 & }
  i=$((i+1)); done; wait
ck "acks racing scans all land" "10" "$(notes c2)"
ck "and none is written away by a racing scan" "10 ok" "$(ds check 2>&1 | tail -1)"
ds scan >/dev/null
ck "nor by the scan after" "10 ok" "$(ds check 2>&1 | tail -1)"

# Readers while writers run: a check never sees a half-written file. Every
# write is a temp file renamed into place, so each check reads one whole
# ledger or the other and exits 0 or 1, never 2.
sedi 's/return 3/return 4/' a.go
codes=$S/codes; : > "$codes"
i=0; while [ $i -lt 8 ]; do
  ds scan >/dev/null 2>&1 &
  ( ds check >/dev/null 2>&1; echo $? >> "$codes" ) &
  i=$((i+1)); done; wait
ck "eight checks during eight scans all read whole files" "bad=0" "bad=$(grep -cv '^[01]$' "$codes")"
ck "and all eight ran" "8" "$(grep -c . "$codes")"
ck "the tree settles on ten unreviewed changes" "10 error" "$(ds check 2>&1 | tail -1)"

# The lock waits, and a crash cannot leave it held: while another process
# holds .ds/lock an ack waits rather than failing, and once that process is
# killed with SIGKILL the ack goes through.
# Started through sh -c so it is not this shell's job: the shell would
# otherwise print its "Killed" line, which run.sh reads as a shell error.
# The holder takes the lock the way ds does on each platform: flock on Unix,
# and on Windows a one-byte range lock at offset 0 (cli/lock_windows.go).
cat > "$S/hold.py" <<'HOLD'
import sys, time
f = open(sys.argv[1], "a+")
try:
    import fcntl
    fcntl.flock(f, fcntl.LOCK_EX)
except ImportError:
    import msvcrt
    f.seek(0)
    msvcrt.locking(f.fileno(), msvcrt.LK_LOCK, 1)
open(sys.argv[2], "w").close()
time.sleep(60)
HOLD
sh -c 'python3 "$1" "$2" "$3" & echo $! > "$4"' _ "$S/hold.py" .ds/lock "$S/held" "$S/holder"
holder=$(cat "$S/holder")
# Bounded: a holder that never took the lock fails the cases below instead of
# hanging the matrix.
n=0; while [ ! -f "$S/held" ] && [ $n -lt 100 ]; do sleep 0.1; n=$((n+1)); done
ck "the holder took the lock" "yes" "$([ -f "$S/held" ] && echo yes || echo no)"
ds ack f0-k7m2p4xq --doc docs/d.md --line 3 --note waited >/dev/null 2>&1 &
acker=$!
sleep 1
ck "an ack waits while the lock is held" "0" "$(notes waited)"
kill -9 $holder; wait $acker; code=$?
ck "and lands once the holder is killed" "0" "$code"
ck "exactly once" "1" "$(notes waited)"

# A temp file a crash left behind is overwritten, not read.
printf 'garbage\n' > .ds/refs.tsv.tmp; printf 'garbage\n' > .ds/ledger.tsv.tmp
ds scan >/dev/null 2>&1
ck "a stale temp file does not break the next scan" "9 error" "$(ds check 2>&1 | tail -1)"
ck "and is gone after it" "left=0" "left=$(ls .ds/*.tmp 2>/dev/null | grep -c .)"

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
