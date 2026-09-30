#!/bin/sh
# ds undo: listing, dry run, the commit boundary, and the citation guard (bug 3)
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
set -e
S=$(mktemp -d)
# Under Git Bash, mktemp gives an MSYS path (/tmp/...) that only MSYS programs
# understand; ds.exe reads it from a config file as a path on no drive. The
# mixed form (C:/...) is one both sides accept.
command -v cygpath >/dev/null 2>&1 && S=$(cygpath -m "$S")
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
ck() { if [ "$2" = "$3" ]; then echo "  PASS  $1"; pass=$((pass+1)); else echo "  FAIL  $1 (want $2, got $3)"; fail=$((fail+1)); fi; }
W="$S/m3"; /bin/rm -rf "$W"; mkdir -p "$W/conf" "$W/docs"; cd "$W"
git init -q -b main . >/dev/null; git config user.email t@t; git config user.name t
printf 'svc:\n  port: 8081\n' > conf/app.yaml
ds init >/dev/null
python3 -c "
import pathlib
p=pathlib.Path('.ds/config.toml'); t=p.read_text()
p.write_text(t.replace('code = [\"**\"]','code = [\"conf/**\"]').replace('docs = [\"**/*.md\"]','docs = [\"docs/**/*.md\"]'))"
ID=$(ds def "conf/app.yaml#svc.port" --label port | tail -1)
printf '# N\n\n[p](ds:cfg?id=%s).\n' "$ID" > docs/notes.md
ds scan >/dev/null; git add -A; git commit -qm a
printf 'log:\n  level: info\n' > conf/log.yaml
ds def "conf/log.yaml#log.level" --label lvl >/dev/null
set +e
ds undo --list >/dev/null 2>&1; ck "undo --list works" 0 $?
BEFORE=$(cksum < conf/log.yaml); ds undo --dry-run >/dev/null 2>&1; ck "undo --dry-run exits 0" 0 $?
AFTER=$(cksum < conf/log.yaml); ck "undo --dry-run writes nothing" "$BEFORE" "$AFTER"
ds undo >/dev/null 2>&1; ck "undo reverses the uncommitted write" 0 $?
ds undo >/dev/null 2>&1; ck "undo STOPS at the commit boundary" 2 $?
ds undo --force >/dev/null 2>&1; ck "--force still blocked by the citation guard" 2 $?
git status --short conf/app.yaml | wc -l | tr -d ' ' | xargs -I{} sh -c 'test {} = 0'; ck "a refused undo leaves the file alone" 0 $?
ds undo --force --orphan >/dev/null 2>&1; ck "--force --orphan proceeds" 0 $?
set -e
echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" = "0" ]
