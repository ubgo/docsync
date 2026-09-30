#!/bin/sh
# perf.sh measures the built ds binary against the SPEC §33 performance
# targets on a generated git repository of FILES files (default 100000):
# a full scan under a minute, an incremental check under two seconds, and
# `impact --staged` under one second. It exits 1 when a target is missed.
#
# One file in DEF_EVERY defines a block and one in DOC_EVERY is a doc citing
# the next def after it (DOC_EVERY must be a multiple of DEF_EVERY), so the tree is mostly files the scanner must look at
# and pass over, which is what a large monorepo is. The targets are the
# SPEC's; the shape of the tree is this script's assumption, stated here.
#
# Run through `task perf` (FILES=… to size it); it is not part of `task`
# because generating and committing the tree takes minutes.
FILES=${FILES:-100000}
DEF_EVERY=${DEF_EVERY:-20}
DOC_EVERY=${DOC_EVERY:-200}
# The SPEC §33 targets, in milliseconds.
SCAN_MS=60000
CHECK_MS=2000
IMPACT_MS=1000

# PERF_DIR keeps the generated tree for profiling; by default it is a temp
# directory removed on exit.
if [ -n "$PERF_DIR" ]; then S=$PERF_DIR; mkdir -p "$S"; else S=$(mktemp -d); trap '/bin/rm -rf "$S"' EXIT; fi
cd "$S" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
python3 - "$FILES" "$DEF_EVERY" "$DOC_EVERY" <<'PY'
import os, sys
files, def_every, doc_every = map(int, sys.argv[1:])
for i in range(files):
    d = f"pkg/p{i // 1000}"
    os.makedirs(d, exist_ok=True)
    os.makedirs(f"docs/d{i // 1000}", exist_ok=True)
    if i % doc_every == 0:
        n = i + def_every  # a def file: doc_every is a multiple of def_every
        with open(f"docs/d{i // 1000}/f{i}.md", "w") as f:
            f.write(f"# Page {i}\n\nF{n} [returns {n}](ds:block?id=f{n}-k7m2p4xq).\n")
    elif i % def_every == 0:
        with open(f"{d}/f{i}.go", "w") as f:
            f.write(f"package p\n\n// ds:def id=f{i}-k7m2p4xq\nfunc F{i}() int {{ return {i} }}\n")
    else:
        with open(f"{d}/f{i}.go", "w") as f:
            f.write(f"package p\n\nfunc G{i}() int {{ return {i} }}\n")
PY
git add -A >/dev/null; git commit -qm base
ds init >/dev/null

# ms runs a command and prints how long it took in milliseconds.
ms() { python3 -c 'import subprocess,sys,time; t=time.monotonic(); r=subprocess.run(sys.argv[1:],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL); print(int((time.monotonic()-t)*1000), r.returncode)' "$@"; }
pass=0; fail=0
target() { # name, measured "ms code", limit, allowed exit codes
  set -- "$1" $2 "$3" "$4"
  case " $5 " in *" $3 "*) ;; *) echo "  FAIL  $1 exited $3"; fail=$((fail+1)); return;; esac
  if [ "$2" -le "$4" ]; then echo "  PASS  $1: ${2}ms (target ${4}ms)"; pass=$((pass+1)); else echo "  FAIL  $1: ${2}ms, over the ${4}ms target"; fail=$((fail+1)); fi
}
echo "  $FILES files, a def in 1/$DEF_EVERY, a citing doc in 1/$DOC_EVERY"
target "full scan" "$(ms ds scan)" "$SCAN_MS" "0"
git add -A >/dev/null; git commit -qm scanned
# One cited block changes: check is incremental against the scan above.
n=$DEF_EVERY
sed -i '' "s/return $n }/return 0 }/" pkg/p0/f$n.go 2>/dev/null || sed -i "s/return $n }/return 0 }/" pkg/p0/f$n.go
target "incremental check, one block changed" "$(ms ds check)" "$CHECK_MS" "1"
# The timing is only worth something if the check found what changed.
got=$(ds check 2>&1 | tail -1)
case "$got" in "1 error"*) ;; *) echo "  FAIL  the check reported \"$got\", want exactly 1 error"; fail=$((fail+1));; esac
git add pkg/p0/f$n.go
target "impact --staged" "$(ms ds impact --staged)" "$IMPACT_MS" "0 1"
echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
