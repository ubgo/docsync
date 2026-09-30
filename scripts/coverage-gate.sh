#!/bin/sh
# coverage-gate.sh <coverprofile>
#
# Fails unless every package in the profile has 100.0% statement coverage.
# Why a script and not `-cover` output: `go test -cover` prints per-package
# percentages but never exits non-zero on a threshold, so CI would pass at 60%.
# We parse the profile with `go tool cover -func` and compute per-package
# totals ourselves so the bar is enforced, not reported.
set -eu
profile="$1"
if [ ! -s "$profile" ]; then
  echo "coverage-gate: profile $profile is missing or empty" >&2
  exit 1
fi
# go tool cover -func prints "file:line: func pct%"; the last line is "total:".
# We aggregate by package directory so one uncovered helper in a package fails
# that package even if the repo total is high.
go tool cover -func="$profile" | awk '
  $1 == "total:" { next }
  {
    split($1, parts, ":"); file = parts[1]
    n = split(file, seg, "/"); pkg = ""
    for (i = 1; i < n; i++) pkg = pkg (i > 1 ? "/" : "") seg[i]
    pct = $NF; sub("%", "", pct)
    if (pct + 0 < 100.0) { low[pkg] = low[pkg] "\n    " $1 " " $2 " " $NF }
    seen[pkg] = 1
  }
  END {
    bad = 0
    for (p in seen) {
      if (p in low) { bad = 1; printf("coverage-gate: %s below 100%%:%s\n", p, low[p]) }
    }
    if (bad) exit 1
    print "coverage-gate: every package at 100% statements"
  }'
