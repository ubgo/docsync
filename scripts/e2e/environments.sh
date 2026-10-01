#!/bin/sh
# one id defined per environment: a fresh repo checks clean, a change in one
# environment flags only that environment's citations, an ack of it clears
# only it, and render shows each citation its own environment's value.
# Before, every citation was recorded against whichever def came last, and a
# fresh repo with per-env defs reported changes nothing had made.
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
W="$S/w"; mkdir -p "$W/docs" "$W/config"; cd "$W" || exit 1
git init -q .; git config user.email t@t; git config user.name t
printf 'port: 8080 # ds:def id=port-k7m2p4xq env=dev\n' > config/dev.yaml
printf 'port: 443 # ds:def id=port-k7m2p4xq env=prod\n' > config/prod.yaml
printf '# D\n\nProd listens on [443](ds:cfg?id=port-k7m2p4xq&env=prod).\n\nDev listens on [8080](ds:cfg?id=port-k7m2p4xq&env=dev).\n' > docs/d.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm b
ck "a fresh repo with per-env defs checks clean" "2 none" "$(ds check 2>&1 | tail -1)"
ck "render shows prod its value" "Prod listens on 443" "$(ds render docs/d.md 2>/dev/null)"
ck "render shows dev its value" "Dev listens on 8080" "$(ds render docs/d.md 2>/dev/null)"
sedi 's/443/8443/' config/prod.yaml
ds scan >/dev/null
out=$(ds check 2>&1)
ck "changing prod flags the prod citation" "3	error" "$(echo "$out" | tr -s ' ')"
case "$out" in *"  5	error"*) echo "  FAIL  the dev citation flagged for a prod change"; fail=$((fail+1));; *) echo "  PASS  the dev citation is untouched"; pass=$((pass+1));; esac
ds ack port-k7m2p4xq --doc docs/d.md --line 3 --note ok >/dev/null 2>&1
ck "acking the prod citation, as its remedy says, clears it" "2 none" "$(ds check 2>&1 | tail -1)"
# Renaming one environment's file changes the order the defs are listed in.
# Changes were matched by id alone, so prod was compared with dev's row: a
# stable def read as changed, and the claim about it expired with nothing
# changed.
F="$S/f"; mkdir -p "$F/docs" "$F/config"; cd "$F" || exit 1
git init -q .; git config user.email t@t; git config user.name t
printf 'port: 8080 # ds:def id=port-k7m2p4xq env=dev stability=stable\n' > config/b-dev.yaml
printf 'port: 443 # ds:def id=port-k7m2p4xq env=prod stability=stable\n' > config/c-prod.yaml
printf '# D\n\nProd listens on [443](ds:cfg?id=port-k7m2p4xq&env=prod).\n\nWe run two ports. <!-- ds:claim owner=@p reviewed=2026-09-01 expires=900d about=port-k7m2p4xq -->\n' > docs/d.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm b
git mv config/c-prod.yaml config/a-prod.yaml
ck "renaming one environment's file reports no change" "2 none" "$(ds check 2>&1 | tail -1)"
printf 'port: 8443 # ds:def id=port-k7m2p4xq env=prod stability=stable\n' > config/a-prod.yaml
out=$(ds check 2>&1)
ck "a real change to prod still expires the claim about it" "which changed" "$out"
# Pins bug 16 through the CLI's own old-content hook, which reads each ledger row's
# file at the commit the ledger records. The library's cases use a fake hook;
# this is the real one, and it needs what a real repository has: a ledger
# scanned after a commit, so there is a commit to read the old value from.
#
# It is checked before the edit is scanned. Once a scan records the new value
# the diff comes from the content-addressed body store and the hook is never
# asked -- an assertion made after the scan passed with the bug put back.
H="$S/h"; mkdir -p "$H/docs" "$H/config"; cd "$H" || exit 1
git init -q .; git config user.email t@t; git config user.name t
printf 'port: 8080 # ds:def id=port-k7m2p4xq env=dev\n' > config/dev.yaml
printf 'port: 443 # ds:def id=port-k7m2p4xq env=prod\n' > config/prod.yaml
printf '# D\n\nProd listens on [443](ds:cfg?id=port-k7m2p4xq&env=prod).\n\nDev listens on [8080](ds:cfg?id=port-k7m2p4xq&env=dev).\n' > docs/d.md
ds init >/dev/null; git add -A; git commit -qm a
ds scan >/dev/null; git add -A; git commit -qm b
sedi 's/443/8443/' config/prod.yaml
diff=$(ds check --json 2>/dev/null | python3 -c 'import json,sys; print(" ".join(f.get("diff") or "" for f in json.load(sys.stdin)["findings"] if f["state"]!="ok"))' | tr -d '\r' | tr '\n' ' ')
ck "prod's change is diffed against prod's own old value (bug 16)" "-443 +8443" "$diff"
case "$diff" in *8080*) echo "  FAIL  prod was diffed against dev's body: $diff"; fail=$((fail+1));; *) echo "  PASS  dev's body is never used for prod's diff"; pass=$((pass+1));; esac

# env.known is the closed set of environments once it lists any (bug 120):
# a typo in --env, in a citation's env=, or in a [run.env.<name>] table used
# to run quietly against an environment nothing defines.
cd "$W" || exit 1
printf '\n[env]\nknown = ["dev", "prod"]\n' >> .ds/config.toml
ck "a listed --env runs" "none" "$(ds check --env prod 2>&1 | tail -1)"
ck "a misspelled --env is refused naming the list" "env.known is dev, prod" "$(ds check --env prdo 2>&1)"
ck "render --env is held to the same list" "environment not in env.known" "$(ds render docs/d.md --env stage 2>&1)"
printf '\nStage listens on [x](ds:cfg?id=port-k7m2p4xq&env=stage).\n' >> docs/d.md; ds scan >/dev/null 2>&1
ck "a citation's env= outside the list is reported unknown" "env=stage is not in env.known" "$(ds check 2>&1)"
printf '[run.env.stagng]\nX = "1"\n' >> .ds/config.toml
ck "a [run.env] table outside the list stops the load" "run.env.stagng names an environment that is not in env.known" "$(ds check 2>&1)"
# [id] is checked when the config loads, so doctor fails on it (bug 121).
printf '[id]\nsuffix_length = 4\n' >> .ds/config.toml
ck "doctor fails a suffix_length the minting rules refuse" "FAIL" "$(ds doctor 2>&1 | head -1)"

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
