#!/bin/sh
# Runs every end-to-end matrix in this directory against the ds binary built
# from this tree (bin/ds), never whatever is on PATH, and fails if any case
# fails or any matrix does not report its tally.
#
# These exist because every one of the first twelve bugs was found by running the
# binary on a real repository, none by the unit tests: they cover what unit
# tests cannot — several commands in sequence, git history, more than one repo.
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
PATH="$root/bin:$PATH"; export PATH
# ds behaves differently under CI on purpose (check defaults to --frozen, fork
# pull requests are refused, notify reads the GitHub variables). The matrices
# describe interactive use and set these themselves where CI is the subject,
# so a run on a CI machine must not inherit them -- the same list the cli
# tests clear in TestMain.
unset CI GITHUB_EVENT_PATH GITHUB_TOKEN GITHUB_REPOSITORY GITHUB_API_URL GITHUB_SERVER_URL
total=0; bad=0
for m in "$here"/*.sh; do
  [ "$(basename "$m")" = run.sh ] && continue
  out=$(sh "$m" 2>&1)
  tally=$(echo "$out" | grep -E -- "---- [0-9]+ passed, [0-9]+ failed ----" | tail -1)
  name=$(basename "$m" .sh)
  if [ -z "$tally" ]; then
    echo "FAIL  $name: no tally (the matrix stopped early)"; echo "$out" | tail -15 | sed 's/^/      /'; bad=$((bad+1)); continue
  fi
  # A shell error means a check never ran — an undefined helper printed
  # "command not found" and the matrix still tallied 0 failed. That is a
  # hole in the gate, not a pass.
  if echo "$out" | grep -qE "command not found|: line [0-9]+: |syntax error"; then
    echo "FAIL  $name: the shell reported an error, so some checks never ran"; echo "$out" | grep -E "command not found|: line [0-9]+: |syntax error" | sed 's/^/      /'; bad=$((bad+1)); continue
  fi
  p=$(echo "$tally" | sed -E 's/.*---- ([0-9]+) passed, ([0-9]+) failed.*/\1/')
  f=$(echo "$tally" | sed -E 's/.*---- ([0-9]+) passed, ([0-9]+) failed.*/\2/')
  total=$((total+p+f))
  if [ "$f" -ne 0 ]; then
    echo "FAIL  $name: $p passed, $f failed"; echo "$out" | grep "FAIL" | sed 's/^/      /'; bad=$((bad+1))
  else
    echo "ok    $name: $p passed"
  fi
done
echo "e2e: $total cases across $(ls "$here"/*.sh | grep -vc run.sh) matrices"
[ "$bad" -eq 0 ]
