#!/bin/sh
# drift in a block another repo publishes: flags, survives scans, classified, upstream delete (bugs 1, 8)
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
set -e
S=$(mktemp -d)
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
check() { if [ "$2" = "$3" ]; then echo "  PASS  $1"; pass=$((pass+1)); else echo "  FAIL  $1 (want $2, got $3)"; fail=$((fail+1)); fi; }

W="$S/m_xrepo"; /bin/rm -rf "$W"; mkdir -p "$W/index" "$W/docs/spec" "$W/code/pkg"
cd "$W/docs"; git init -q -b main . >/dev/null; git config user.email t@t; git config user.name t
printf '# S\n\n### Depth\n\nAt most 12 deep.\n' > spec/SPEC.md
ds init >/dev/null
python3 -c "
import pathlib,sys
p=pathlib.Path('.ds/config.toml'); t=p.read_text()
p.write_text('workspace = \"$W/index\"\n'+t.replace('code = [\"**\"]','code = [\"spec/**\"]').replace('docs = [\"**/*.md\"]','docs = [\"spec/**/*.md\"]'))"
ID=$(ds def "spec/SPEC.md#Depth" --label depth | tail -1)
ds scan >/dev/null; git add -A; git commit -qm a; ds publish >/dev/null

cd "$W/code"; git init -q -b main . >/dev/null; git config user.email t@t; git config user.name t
printf 'package pkg\n\n// MaxDepth — implements ds:block?id=%s\nconst MaxDepth = 12\n' "$ID" > pkg/depth.go
ds init >/dev/null
python3 -c "
import pathlib
p=pathlib.Path('.ds/config.toml'); t=p.read_text()
p.write_text('workspace = \"$W/index\"\n'+t.replace('code = [\"**\"]','code = [\"pkg/**\"]').replace('docs = [\"**/*.md\"]','docs = [\"pkg/**\"]'))"
ds sync >/dev/null; ds scan >/dev/null; git add -A; git commit -qm b
set +e; ds check --full >/dev/null 2>&1; check "xrepo: clean baseline" 0 $?; set -e

upstream_edit() { cd "$W/docs"; sed -i '' "s/$1/$2/" spec/SPEC.md; ds scan >/dev/null; git add -A; git commit -qm e >/dev/null; ds publish >/dev/null; cd "$W/code"; ds sync >/dev/null; }

upstream_edit "12 deep" "6 deep"
set +e; ds check --full >/dev/null 2>&1; check "xrepo: never-acked drift flags" 1 $?; set -e
ds scan >/dev/null
set +e; ds check --full >/dev/null 2>&1; check "xrepo: SURVIVES local scan" 1 $?; set -e

ds ack "$ID" --doc pkg/depth.go --line 3 --note ok >/dev/null
set +e; ds check --full >/dev/null 2>&1; check "xrepo: ack clears" 0 $?; set -e

upstream_edit "6 deep" "3 deep"
set +e; ds check --full >/dev/null 2>&1; check "xrepo: acked drift flags" 1 $?; set -e
CLASS=$(ds check --full --json 2>/dev/null | python3 -c "import json,sys;d=json.load(sys.stdin);print(d['findings'][0].get('class'))")
check "xrepo: classified (not unknown)" "['body']" "$CLASS"
ds scan >/dev/null
set +e; ds check --full >/dev/null 2>&1; check "xrepo: SURVIVES scan again" 1 $?; set -e
ds ack "$ID" --doc pkg/depth.go --line 3 --note ok2 >/dev/null
set +e; ds check --full >/dev/null 2>&1; check "xrepo: re-ack clears" 0 $?; set -e

# upstream deletes the def
cd "$W/docs"; printf '# S\n\nGone.\n' > spec/SPEC.md; ds scan >/dev/null; git add -A; git commit -qm d >/dev/null; ds publish >/dev/null
cd "$W/code"; ds sync >/dev/null
set +e; ds check --full >/dev/null 2>&1; check "xrepo: upstream delete is broken" 1 $?; set -e

echo
echo "  ---- $pass passed, $fail failed ----"
[ "$fail" = "0" ]
