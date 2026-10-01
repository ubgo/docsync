#!/bin/sh
# what a finding says about a change, and what the commands around check
# report: the diff comes from the body the citation was measured against
# (bugs 23, 24 and 25), a change is classified even with no scan since it
# (bug 28) and a one-value constant changes by value (bug 27), the text
# output shows the diff (bug 26), passing findings are counted as ok (bug
# 30), a failed run is a finding (bug 29), adopt --dry-run shows the ids the
# real run writes (bug 33), and report does not ask for defs in doc pages
# (bug 34).
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
S=$(mktemp -d)
# Under Git Bash, mktemp gives an MSYS path (/tmp/...) that only MSYS programs
# understand; ds.exe reads it from a config file as a path on no drive. The
# mixed form (C:/...) is one both sides accept.
command -v cygpath >/dev/null 2>&1 && S=$(cygpath -m "$S")
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
ck() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
no() { case "$3" in *"$2"*) echo "  FAIL  $1 (did not want $2, got: $3)"; fail=$((fail+1));; *) echo "  PASS  $1"; pass=$((pass+1));; esac; }
# sedi edits a file in place the one way BSD, GNU and busybox sed all accept.
sedi() { for _f in "$@"; do :; done; sed -i.bak "$@" && /bin/rm -f "$_f.bak"; }
fresh() { W="$S/$1"; mkdir -p "$W/docs"; cd "$W" || exit 1; git init -q .; git config user.email t@t; git config user.name t; printf 'module x\n\ngo 1.22\n' > go.mod; }

# ---------- a signature and a constant under stability=api, no scan since ----------
fresh api
printf 'package lim\n\nconst MaxRetries = 5\n\n// Timeout returns the timeout.\nfunc Timeout(a int) int {\n\treturn 30\n}\n' > lim.go
ds init >/dev/null
T=$(ds def 'lim.go#Timeout' --label timeout --stability api | tail -1)
R=$(ds def 'lim.go#MaxRetries' --label retries --stability api | tail -1)
printf '# G\n\nTimeout takes a, see [t](ds:block?id=%s).\n\nIt retries [five](ds:block?id=%s) times.\n' "$T" "$R" > docs/g.md
ds scan >/dev/null; git add -A; git commit -qm a
sedi 's/Timeout(a int)/Timeout(a, b int)/; s/= 5/= 3/' lim.go
out=$(ds check 2>&1); code=$?
ck "a signature change with no scan since fails an api block (bug 28)" "1" "$code"
ck "and is classified as a signature change" "$T changed (signature)" "$out"
ck "a one-value constant changes by value (bug 27)" "$R changed (value)" "$out"
ck "the text output shows the diff under the finding (bug 26)" "| +func Timeout(a, b int) int {" "$out"
ck "and the constant's old and new value" "| -5" "$out"
ds scan >/dev/null
ck "a scan changes nothing about either" "2 error" "$(ds check 2>&1 | tail -1)"

# ---------- the diff is against the acked body, with and without scans ----------
fresh acked
printf 'package lim\n\nfunc Timeout() int {\n\treturn 30\n}\n' > lim.go
ds init >/dev/null
T=$(ds def 'lim.go#Timeout' --label timeout | tail -1)
printf '# G\n\nThe timeout is thirty, see [t](ds:block?id=%s).\n' "$T" > docs/g.md
ds scan >/dev/null; git add -A; git commit -qm a
sedi 's/return 30/return 45/' lim.go
ds ack "$T" --doc docs/g.md --line 3 --note 'now 45' >/dev/null
sedi 's/return 45/return 60/' lim.go
out=$(ds check 2>&1)
ck "an ack with no scan before it stores the body it approves (bug 25)" "$T changed (body) since this sentence was acked" "$out"
ck "and the diff starts from the acked body" "| -	return 45" "$out"
no "not from the first-seen one" "return 30" "$out"
ds scan >/dev/null; git add -A; git commit -qm b
out=$(ds check 2>&1)
ck "after a scan and a commit the diff is still against the ack (bugs 23 and 24)" "| -	return 45" "$out"
no "never the body at the ledger's recorded commit" "return 30" "$out"
ds ack "$T" --doc docs/g.md --line 3 --note 'now 60' >/dev/null
ck "passing findings are counted as ok (bug 30)" "1 ok" "$(ds check 2>&1 | tail -1)"
no "never as none" "none" "$(ds check 2>&1 | tail -1)"

# ---------- a failed run is a finding ----------
fresh runs
ds init >/dev/null
printf '\n[run]\nenabled = true\nallow = ["docs/**"]\n' >> .ds/config.toml
printf '# R\n\n<!-- ds:run cmd="false" -->\n\n<!-- ds:run cmd="true" -->\n' > docs/r.md
ds scan >/dev/null
out=$(ds check --run 2>&1); code=$?
ck "a failed run fails the check (bug 29)" "1" "$code"
ck "and is a finding naming the command" "run failed         run failed: false" "$out"
ck "counted in the summary" "1 error, 1 ok" "$(echo "$out" | tail -1)"
ck "and in --json, which stays one document" '"run failed": 1' "$(ds check --run --json 2>/dev/null)"

# ---------- adopt --dry-run shows the ids the real run writes ----------
fresh adopt
printf 'package lim\n\nfunc Refund() int {\n\treturn 1\n}\n' > lim.go
printf '# A\n\nSee [Refund](../lim.go#Refund).\n' > docs/a.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm a
dry=$(ds adopt --dry-run 2>&1 | grep -o 'refund-[a-z0-9]*' | sort -u)
ds adopt >/dev/null 2>&1
real=$(grep -o 'refund-[a-z0-9]*' lim.go)
ck "adopt --dry-run printed an id" "refund-" "$dry"
ck "and the real run wrote that same id (bug 33)" "$dry" "$real"

# ---------- report asks for defs in code, not in pages ----------
fresh report
printf 'package lim\n\nfunc A() int { return 1 }\n' > lim.go
printf '# P\n\nProse.\n' > docs/p.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm a
printf '# P\n\nMore prose.\n' > docs/p.md; git add -A; git commit -qm b
out=$(ds report --unmarked --gaps 2>&1)
ck "a changed code file with no defs is unmarked" "lim.go  changed" "$out"
no "a changed page is not (bug 34)" "docs/p.md" "$out"

echo
echo "  ---- $pass passed, $fail failed ----"
[ "$fail" = "0" ]
