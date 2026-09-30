#!/bin/sh
# Go groups related constants, limits and defaults far more often than it
# declares them alone, and those are exactly the values documentation restates.
# A grouped constant could not be defined at all, and a directive written above
# one bound the NEXT top-level declaration in silence, so the sentence citing
# it was checked against an unrelated constant: a change to the constant never
# flagged, and a change to the stranger did (bug 18, reported by ubgo/auth).
#
# This spans commands — def writes the directive, scan binds it, check measures
# it — which is why it lives here and not in a unit test.
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
ok() { echo "  PASS  $1"; pass=$((pass+1)); }
no() { echo "  FAIL  $1"; fail=$((fail+1)); }
setup() { n=$((n+1)); W="$S/c$n"; mkdir -p "$W/docs"; cd "$W" || exit 1; git init -q .; git config user.email t@t; git config user.name t; }

group_source() {
  printf 'package p\n\nconst (\n\t// MinLength is the shortest password accepted.\n\tMinLength = 8\n\t// MaxLength bounds the input the hash reads.\n\tMaxLength = 256\n\t// ResetPath is where the reset link points.\n\tResetPath = "/reset-password"\n)\n\n// Standalone is outside the group.\nconst Standalone = 42\n' > a.go
}

# --- ds def finds every entry in the group, and each binds its own line ---
setup; group_source; ds init >/dev/null
for sym in MinLength MaxLength ResetPath Standalone; do
  if ds def "a.go#$sym" >/dev/null 2>&1; then ok "def resolves $sym"; else no "def cannot resolve $sym"; fi
done
ds scan >/dev/null
# Four distinct ids on four distinct blocks: the failure was several ids
# landing on one, which the scanner now reports.
if [ "$(cut -f1 .ds/ledger.tsv | tail -n +3 | sort -u | wc -l | tr -d ' ')" = 4 ]; then ok "four ids"; else no "four ids: $(cut -f1 .ds/ledger.tsv | tail -n +3 | tr '\n' ' ')"; fi
if [ "$(cut -f6 .ds/ledger.tsv | tail -n +3 | sort -u | wc -l | tr -d ' ')" = 4 ]; then ok "each id binds its own line"; else no "ids share lines: $(cut -f1,5,6 .ds/ledger.tsv | tail -n +3 | tr '\n' ';')"; fi
if [ "$(ds scan 2>&1 | head -1 | sed -E 's/.*([0-9]+) defs.*/\1/')" = 4 ] && [ "$(ds scan 2>&1 | head -1 | sed -E 's/.*, ([0-9]+) problems.*/\1/')" = 0 ]; then ok "four defs and no problems"; else no "scan: $(ds scan 2>&1)"; fi
# The value is the entry's own, not a neighbour's: this is what a doc restates.
if ds facts | grep -q '"/reset-password"'; then ok "facts reports the entry's own value"; else no "facts lost the value: $(ds facts)"; fi
if [ "$(ds facts | grep -c '  8 ')" -ge 1 ] || ds facts | grep -qE '\b8\b'; then ok "the numeric entry keeps its value"; else no "numeric value: $(ds facts)"; fi

# --- a cited entry flags when it changes, and not when a neighbour changes ---
setup; group_source; ds init >/dev/null
# The id has to be real before anything downstream means a thing: a failed def
# would leave a citation with no id=, and `ds check` would then exit non-zero
# for that instead of for the drift these cases are about -- a pass that proves
# nothing. Every later assertion here reads a state name, not just an exit code.
id=$(ds def a.go#MinLength 2>/dev/null)
case $id in
  *-*) ok "def minted an id for the grouped entry";;
  *)   no "def could not define the grouped entry (got '$id'); the rest of this case cannot run"; echo; echo "  ---- $pass passed, $((fail+3)) failed ----"; exit 1;;
esac
printf '# Limits\n\nPasswords are at least [%s](ds:block?id=%s) characters.\n' 8 "$id" > docs/limits.md
ds scan >/dev/null; git add -A; git commit -qm b >/dev/null
# A neighbour in the same group changing must not touch this citation.
sedi 's/MaxLength = 256/MaxLength = 512/' a.go
if [ "$(ds check 2>&1 | tail -1)" = "1 none" ]; then ok "a neighbour in the group changing leaves the citation alone"; else no "a neighbour changing disturbed the citation: $(ds check 2>&1 | tail -3)"; fi
# The cited entry changing must flag it.
sedi 's/MinLength = 8/MinLength = 12/' a.go
if ds check 2>&1 | grep -q unacked; then ok "the cited entry changing flags its citation as unacked"; else no "the cited entry changed and nothing flagged: $(ds check 2>&1 | tail -3)"; fi
if ds check 2>&1 | grep unacked | grep -q "$id"; then ok "the finding names the cited id"; else no "the finding names something else: $(ds check 2>&1 | tail -3)"; fi

# --- two ids on one block is a finding, not silence ---
setup
printf 'package p\n\n// ds:def id=first-k7m2p4xq\n// ds:def id=second-h3v8n2wd\nconst Shared = 1\n' > a.go
ds init >/dev/null
out=$(ds scan 2>&1)
if echo "$out" | grep -q "same block"; then ok "two ids on one block is reported"; else no "two ids on one block passed silently: $out"; fi
if echo "$out" | grep -q first-k7m2p4xq && echo "$out" | grep -q second-h3v8n2wd; then ok "the report names both ids"; else no "the report names only one: $out"; fi

# --- a directive above the group's own line still binds the whole group ---
setup
printf 'package p\n\n// ds:def id=grp-t4k2b9rf\nconst (\n\tA = 1\n\tB = 2\n)\n' > a.go
ds init >/dev/null; ds scan >/dev/null
if [ "$(grep grp-t4k2b9rf .ds/ledger.tsv | cut -f6)" = "4-7" ]; then ok "a directive above the group binds the whole group"; else no "group extent = $(grep grp-t4k2b9rf .ds/ledger.tsv | cut -f6), want 4-7"; fi

# --- the members of a body: same shape, same original failure ---
setup
printf 'package p\n\ntype Limits struct {\n\t// MinLength is the shortest accepted.\n\tMinLength int\n\t// MaxLength bounds the input.\n\tMaxLength int\n}\n\ntype Store interface {\n\t// Save writes it.\n\tSave() error\n\tLoad() error\n}\n' > a.go
ds init >/dev/null
fid=$(ds def a.go#MinLength 2>/dev/null)
mid=$(ds def a.go#Save 2>/dev/null)
case $fid in *-*) ok "def resolves a struct field";; *) no "def cannot resolve a struct field (got '$fid')";; esac
case $mid in *-*) ok "def resolves an interface method";; *) no "def cannot resolve an interface method (got '$mid')";; esac
ds scan >/dev/null
# Each member binds its own line. The original failure ran to the body's
# closing brace, so one field covered every field below it.
if [ "$(grep "$fid" .ds/ledger.tsv | cut -f6)" = "$(grep "$fid" .ds/ledger.tsv | cut -f6 | sed 's/-.*//')" ] 2>/dev/null; then ok "the struct field binds one line"; else no "struct field extent = $(grep "$fid" .ds/ledger.tsv | cut -f6)"; fi
if [ "$(grep "$fid" .ds/ledger.tsv | cut -f5)" = "Limits.MinLength" ]; then ok "the field is named for its type"; else no "field symbol = $(grep "$fid" .ds/ledger.tsv | cut -f5)"; fi
if [ "$(grep "$mid" .ds/ledger.tsv | cut -f5)" = "Store.Save" ]; then ok "the method is named for its interface"; else no "method symbol = $(grep "$mid" .ds/ledger.tsv | cut -f5)"; fi
if [ "$(ds scan 2>&1 | head -1 | sed -E 's/.*, ([0-9]+) problems.*/\1/')" = 0 ]; then ok "members scan without problems"; else no "members: $(ds scan 2>&1 | tail -n +2)"; fi

# A sibling member changing must not disturb a citation of this one.
printf '# L\n\nAt least [x](ds:block?id=%s).\n' "$fid" > docs/l.md
ds scan >/dev/null; git add -A; git commit -qm b >/dev/null
sedi 's/MaxLength int/MaxLength int64/' a.go
# The citation itself must be untouched. The summary is not checked as a
# whole because this tree also holds an uncited def, which is its own finding
# and nothing to do with the sibling.
if ds check 2>&1 | grep -q unacked; then no "a sibling field disturbed the citation: $(ds check 2>&1 | tail -3)"; else ok "a sibling field changing leaves the citation alone"; fi
sedi 's/MinLength int/MinLength uint8/' a.go
if ds check 2>&1 | grep -q unacked; then ok "the cited field changing flags it"; else no "the cited field changed and nothing flagged: $(ds check 2>&1 | tail -3)"; fi

# --- an import entry names the dependency ---
setup
printf 'package p\n\nimport (\n\t"net/http"\n\tf "fmt"\n)\n\nvar _ = http.StatusOK\nvar _ = f.Sprint\n' > a.go
ds init >/dev/null
iid=$(ds def "a.go#net/http" 2>/dev/null)
case $iid in *-*) ok "def resolves an import by its path";; *) no "def cannot resolve an import path (got '$iid')";; esac
aid=$(ds def "a.go#f" 2>/dev/null)
case $aid in *-*) ok "def resolves an aliased import by its alias";; *) no "def cannot resolve an import alias (got '$aid')";; esac

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
