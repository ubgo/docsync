#!/bin/sh
# Behaviour that must hold for files a syntax tier reads (Go, TypeScript,
# Python through tree-sitter) exactly as for files the heuristic code tier
# reads. Unit tests stand a fake tier in for the real ones; this drives the
# shipped binary, whose tiers are the real grammars. Two features keyed on
# the code tier's name and so did nothing in a Go repository (bug 20).
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
ok() { echo "  PASS  $1"; pass=$((pass+1)); }
no() { echo "  FAIL  $1"; fail=$((fail+1)); }
has() { case "$2" in *"$3"*) ok "$1";; *) no "$1 (no \"$3\" in: $(printf '%s' "$2" | head -3))";; esac; }

# ---- policy.require_doc covers files every tier reads (bug 20) -------------
cd "$S" && git init -q . && git config user.email t@t && git config user.name t
mkdir -p internal/auth docs
printf 'package auth\n\nfunc Refresh() {}\n' > internal/auth/refresh.go
printf 'export function Rotate() {}\n' > internal/auth/rotate.ts
printf 'def Revoke():\n    pass\n' > internal/auth/revoke.py
printf 'pub fn Renew() {}\n' > internal/auth/renew.rs
printf '# Auth\n' > docs/auth.md
ds init >/dev/null
printf '\n[policy]\nrequire_doc = ["internal/auth/**"]\n' >> .ds/config.toml
ds scan >/dev/null
out=$(ds check --full 2>&1)
for f in refresh.go:Refresh rotate.ts:Rotate revoke.py:Revoke renew.rs:Renew; do
  has "require_doc reports ${f#*:} in ${f%%:*}" "$out" "internal/auth/${f%%:*}"
done
out=$(ds report 2>&1)
has "report lists Refresh in a Go file as unmarked" "$out" "Refresh"

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
