#!/bin/sh
# every read-only command prints the same thing for the same repository:
# run twice on one tree, the output is byte for byte the same; and on two
# trees built by the same steps, whose only differences are what a person
# cannot choose -- the random suffixes ids were minted with, and the second
# on the clock when each ack landed -- the output is the same once those
# are written out of it. The second half is the one that finds bugs: ds
# find listed defs by id, so two defs with one label came out in the order
# of their suffixes (bug 32), and ds report ranked pages acked seconds
# apart by the second, so rows showing the same date swapped (bug 31).
# Running each command twice on one tree saw neither, because both are
# deterministic given the tree; they are not deterministic given the steps.
#
# Normalised before comparing, and only these: id suffixes, hex hashes and
# commit shas, ISO dates and times, durations (the mean time to ack measures the
# second the two trees deliberately differ by), and the temporary path. A
# command that cannot run here (no network, nothing to say) still has its
# output compared, error text included.
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
sedi() { for _f in "$@"; do :; done; sed -i.bak "$@" && /bin/rm -f "$_f.bak"; }
S=$(mktemp -d)
# Under Git Bash, mktemp gives an MSYS path (/tmp/...) that only MSYS programs
# understand; ds.exe reads it from a config file as a path on no drive. The
# mixed form (C:/...) is one both sides accept.
command -v cygpath >/dev/null 2>&1 && S=$(cygpath -m "$S")
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
same() { # name a b
  if [ "$2" = "$3" ]; then echo "  PASS  $1"; pass=$((pass+1)); return; fi
  echo "  FAIL  $1"; fail=$((fail+1))
  printf '%s\n' "$2" > "$S/want"; printf '%s\n' "$3" > "$S/got"
  diff "$S/want" "$S/got" | head -12 | sed 's/^/        /'
}

# A run that straddles midnight UTC would put the two trees' acks on two
# days, which is a real difference in staleness, not noise. Wait it out.
[ "$(date -u +%H%M%S)" -lt 235930 ] || sleep 45

# build DIR SUFFIX_A SUFFIX_B SLOW: one repository, by fixed steps. The two
# defs labelled `limit` take the suffixes given, so one tree can have them in
# id order and the other against it. With SLOW, the acks land in different
# seconds, oldest first on the page that sorts last.
build() {
  W="$1"; mkdir -p "$W/docs" "$W/internal"; cd "$W" || exit 1
  git init -q -b main .; git config user.email t@t; git config user.name t
  printf 'module x\n\ngo 1.22\n' > go.mod
  printf 'package p\n\n// ds:def id=limit-%s stability=api\nconst MinLimit = 1\n\n// ds:def id=limit-%s stability=api\nconst MaxLimit = 10\n\n// ds:def id=timeout-k7m2p4xq owner=@core tags=store\nfunc Timeout() int {\n\treturn 30\n}\n\n// ds:def id=orphan-t4k2b9rf\nfunc Orphan() {}\n' "$2" "$3" > internal/a.go
  # Both limits are cited on one line, so findings for that line tie on
  # doc and line; each page below cites only the timeout, so its oldest
  # ack is the one ack this script makes on it.
  printf '# Limits\n\nAt least [one](ds:block?id=limit-%s) and at most [ten](ds:block?id=limit-%s).\n' "$2" "$3" > docs/limits.md
  for d in zeta alpha mid; do
    printf '# %s\n\nThe timeout is [thirty](ds:block?id=timeout-k7m2p4xq) seconds.\n' "$d" > "docs/$d.md"
  done
  ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm base
  ds ack timeout-k7m2p4xq --doc docs/zeta.md --line 3 --note z >/dev/null
  [ "$4" = slow ] && sleep 1
  ds ack timeout-k7m2p4xq --doc docs/alpha.md --line 3 --note a >/dev/null
  ds ack timeout-k7m2p4xq --doc docs/mid.md --line 3 --note m >/dev/null
  # Drift for check to report: a body changed, a constant's value changed.
  sedi 's/return 30/return 45/; s/MaxLimit = 10/MaxLimit = 12/' internal/a.go
  git add -A; git commit -qm drift
}

# The read-only commands, one per line. Each runs in the tree's root.
cat > "$S/commands" <<'EOF'
check
check --json
check --expand
find limit
find --file internal/
find --json limit
report
report --stalest
report --unmarked
report --gaps
report --json
map
map --json
facts
facts --json
graph
graph --json
graph --dot
status
status --json
why timeout-k7m2p4xq
why --history timeout-k7m2p4xq
why --json limit-SUFA
impact
impact --json
audit
audit --json
triage
triage --json
context docs/limits.md
context timeout-k7m2p4xq
context --json docs/mid.md
render docs/limits.md
render docs/zeta.md
locate limit-SUFB
locate timeout-k7m2p4xq
blame docs/limits.md 3
blame --json docs/alpha.md 3
EOF

# norm writes out what differs by construction between the two trees. BSD
# sed has no \b, so a boundary is a non-word character or the line's end,
# kept by a group; each rule runs twice because a boundary consumed by one
# match cannot start the next.
norm() {
  sed -E \
    -e "s#$S#<tmp>#g" \
    -e 's/"mean_time_to_ack_ns": [0-9]+/"mean_time_to_ack_ns": <dur>/' \
    -e 's/-[2-9a-hjkmnp-z]{8}([^a-z0-9]|$)/-<id>\1/g' \
    -e 's/(^|[^a-z0-9-])[0-9a-f]{7,64}([^a-z0-9]|$)/\1<hex>\2/g' \
    -e 's/(^|[^a-z0-9-])[0-9a-f]{7,64}([^a-z0-9]|$)/\1<hex>\2/g' \
    -e 's/[0-9]{4}-[0-9]{2}-[0-9]{2}(T[0-9:.]+(Z|[+-][0-9:]+))?/<date>/g' \
    -e 's/(^|[^a-z0-9])[0-9]+(\.[0-9]+)?(ns|µs|us|ms|s|m|h)([^a-z0-9]|$)/\1<dur>\4/g'
}

# runall DIR SUFA SUFB: every command twice, the pair compared byte for byte
# (generated_at aside, a clock reading by contract), the first kept.
runall() {
  cd "$1" || exit 1
  : > "$S/out-$(basename "$(dirname "$1")")"
  while IFS= read -r c; do
    [ -z "$c" ] && continue
    args=$(echo "$c" | sed "s/SUFA/$2/; s/SUFB/$3/")
    first=$(eval "ds $args" 2>&1 | sed -E 's/"generated_at": "[^"]*"/"generated_at": ""/')
    second=$(eval "ds $args" 2>&1 | sed -E 's/"generated_at": "[^"]*"/"generated_at": ""/')
    same "same tree, twice: ds $args" "$first" "$second"
    printf '== ds %s\n%s\n' "$c" "$first" >> "$S/out-$(basename "$(dirname "$1")")"
  done < "$S/commands"
}

# Tree a has the limit suffixes in id order and its acks in one second;
# tree b has them against id order and its acks a second apart. Both repos
# are named `repo`, so the name ds derives from the directory is the same.
build "$S/a/repo" aaaaaaaa zzzzzzzz fast
build "$S/b/repo" zzzzzzzz aaaaaaaa slow
runall "$S/a/repo" aaaaaaaa zzzzzzzz
runall "$S/b/repo" zzzzzzzz aaaaaaaa

# Across trees, command by command, so a failure names the command.
csplit_cmds() { awk -v want="$2" '/^== ds /{on = ($0 == "== ds " want)} on' "$1"; }
while IFS= read -r c; do
  [ -z "$c" ] && continue
  ta=$(csplit_cmds "$S/out-a" "$c" | norm)
  tb=$(csplit_cmds "$S/out-b" "$c" | norm)
  same "same steps, other suffixes and seconds: ds $c" "$ta" "$tb"
done < "$S/commands"

echo
echo "  ---- $pass passed, $fail failed ----"
[ "$fail" = "0" ]
