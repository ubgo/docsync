#!/bin/sh
# Every file type docsync will write a directive into must still parse
# afterwards, checked with that language's OWN parser, and every type it will
# not write into must come back refused with the file untouched.
#
# Why a matrix and not a unit test: the failure this guards is not a wrong
# string, it is a file that no longer parses. `ds def` inserted a bare
# `ds:def id=…` line into any type it did not recognise, which is invalid
# syntax wherever syntax exists -- it stopped every build in a Go workspace
# with "unknown directive" and would have left a JSON file unparseable. A test
# asserting the inserted text would have passed while the file was broken, so
# the assertion here is the real parser's exit code.
#
# Parsers that are absent are reported SKIP by name rather than passing quietly.
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
S=$(mktemp -d)
# Under Git Bash, mktemp gives an MSYS path (/tmp/...) that only MSYS programs
# understand; ds.exe reads it from a config file as a path on no drive. The
# mixed form (C:/...) is one both sides accept.
command -v cygpath >/dev/null 2>&1 && S=$(cygpath -m "$S")
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0; n=0
ok() { echo "  PASS  $1"; pass=$((pass+1)); }
no() { echo "  FAIL  $1"; fail=$((fail+1)); }
sk() { echo "  SKIP  $1"; }
setup() { n=$((n+1)); W="$S/c$n"; mkdir -p "$W"; cd "$W" || exit 1; git init -q .; git config user.email t@t; git config user.name t; ds init >/dev/null; }

# writes: <name> <file> <line> <content...>  then a parser check
# --- Go source, the baseline that has always worked ---
setup
printf 'package p\n\nfunc F() int {\n\treturn 1\n}\n' > a.go
printf 'module example.com/m\n\ngo 1.26\n' > go.mod
if ds def a.go#F >/dev/null 2>&1 && gofmt -e a.go >/dev/null 2>&1; then ok ".go: directive inserted and gofmt still parses it"; else no ".go: gofmt rejects the file after def"; fi

# --- go.mod and go.work: parsed by the go tool itself ---
setup
printf 'module example.com/m\n\ngo 1.26\n' > go.mod
if ds def go.mod:1 >/dev/null 2>&1; then
  if go mod edit -json >/dev/null 2>&1; then ok "go.mod: the go tool still parses it"; else no "go.mod: go mod edit -json fails after def: $(go mod edit -json 2>&1 | head -1)"; fi
  if head -1 go.mod | grep -q '^// ds:def'; then ok "go.mod: the directive is a comment"; else no "go.mod: directive is not commented: $(head -1 go.mod)"; fi
else no "go.mod: def refused, but go.mod takes // comments"; fi

setup
printf 'go 1.26\n\nuse .\n' > go.work
printf 'module example.com/m\n\ngo 1.26\n' > go.mod
if ds def go.work:1 >/dev/null 2>&1; then
  if go work edit -json >/dev/null 2>&1; then ok "go.work: the go tool still parses it"; else no "go.work: go work edit -json fails after def: $(go work edit -json 2>&1 | head -1)"; fi
else no "go.work: def refused, but go.work takes // comments"; fi

# --- Pkl: its own evaluator is the proof ---
setup
printf 'dbName = "demo_db"\nport = 5432\n' > app.pkl
if ! command -v pkl >/dev/null 2>&1; then sk ".pkl: pkl is not installed"
elif ds def app.pkl:1 >/dev/null 2>&1; then
  if pkl eval app.pkl >/dev/null 2>&1; then ok ".pkl: pkl eval still accepts it"; else no ".pkl: pkl eval rejects it after def: $(pkl eval app.pkl 2>&1 | head -1)"; fi
else no ".pkl: def refused, but Pkl takes // comments"; fi

# --- plain text: the bare line is the carrier, and it must read back ---
setup
printf 'one\ntwo\n' > notes.txt
if ds def notes.txt:1 >/dev/null 2>&1 && head -1 notes.txt | grep -q '^ds:def'; then ok ".txt: the bare line is written, as the text tier reads it"; else no ".txt: bare directive not written: $(head -1 notes.txt)"; fi
if [ "$(ds scan 2>&1 | sed -E 's/.*, ([0-9]+) problems.*/\1/')" = 0 ]; then ok ".txt: a bare line there is not reported as damage"; else no ".txt: legitimate bare line reported: $(ds scan 2>&1 | tail -1)"; fi

# --- YAML and Python, whose comment forms differ ---
setup
printf 'server:\n  port: 8080\n' > c.yaml
if ds def c.yaml#server.port >/dev/null 2>&1 && python3 -c 'import sys,yaml;yaml.safe_load(open("c.yaml"))' 2>/dev/null; then ok ".yaml: still parses after def"
elif ! python3 -c 'import yaml' 2>/dev/null; then sk ".yaml: python yaml module not installed"
else no ".yaml: broken after def: $(cat c.yaml)"; fi

setup
printf 'def f():\n    return 1\n' > a.py
if ds def a.py#f >/dev/null 2>&1; then
  if python3 -c 'import ast,sys;ast.parse(open("a.py").read())' 2>/dev/null; then ok ".py: python still parses it"; else no ".py: python rejects it after def"; fi
else no ".py: def refused on a type with # comments"; fi

# --- the refusals: the file must be byte-identical afterwards ---
setup
printf '{\n  "port": 8080\n}\n' > p.json
printf 'a,b\n1,2\n' > d.csv
cp p.json "$S/p.orig"; cp d.csv "$S/d.orig"
if ds def p.json:2 >/dev/null 2>&1; then no ".json: def was allowed into a format with no comment syntax"; else ok ".json: def refused"; fi
if cmp -s p.json "$S/p.orig"; then ok ".json: the file is untouched"; else no ".json: the file was modified by a refused def"; fi
if python3 -c 'import json;json.load(open("p.json"))' 2>/dev/null; then ok ".json: still valid JSON"; else no ".json: no longer parses"; fi
if ds def d.csv:2 >/dev/null 2>&1; then no ".csv: def was allowed into a format with no comment syntax"; else ok ".csv: def refused"; fi
if cmp -s d.csv "$S/d.orig"; then ok ".csv: the file is untouched"; else no ".csv: the file was modified by a refused def"; fi
# The refusal has to say what to do instead, or it is just a wall.
msg=$(ds def p.json:2 2>&1)
if echo "$msg" | grep -q "remote def"; then ok ".json: the refusal names the way round it"; else no ".json: unhelpful refusal: $msg"; fi

# --- damage already in a tree is reported, not accepted ---
setup
printf 'ds:def id=broken-k7m2p4xq\ngo 1.26\n\nuse .\n' > go.work
printf 'module example.com/m\n\ngo 1.26\n' > go.mod
out=$(ds scan 2>&1)
if echo "$out" | grep -q "not inside a comment"; then ok "a bare directive already in go.work is reported"; else no "existing damage passed silently: $out"; fi
if echo "$out" | grep -q "go.work:1"; then ok "the report names the file and line to fix"; else no "the report does not locate the damage: $out"; fi

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
