#!/bin/sh
# One case per finding in docs/FIELD-FINDINGS-2026-09-29.md, the report from
# putting docsync on five real repositories in one sitting. Each finding was a
# thing a user hit; each case here is that exact thing, run against the built
# binary, so none of them can come back without this matrix going red.
#
# The numbering follows the report. Findings 5 ("things that worked first
# time") and the ones fixed here are all pinned, because "worked" is a claim
# about behaviour too.
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
setup() { n=$((n+1)); W="$S/c$n"; mkdir -p "$W"; cd "$W" || exit 1; git init -q .; git config user.email t@t; git config user.name t; }

# --- 1. adopt and markdown heading links ---
setup; mkdir -p docs internal
printf '# P\n\n## Target\n\nText.\n' > README.md
printf 'See [t](./README.md#target).\n' > CONTRIBUTING.md
printf 'See [t](../README.md#target) and [s](../internal/s.go#Save).\n' > docs/a.md
printf 'package internal\n\nfunc Save() error { return nil }\n' > internal/s.go
ds init >/dev/null; git add -A; git commit -qm x >/dev/null
out=$(ds adopt --dry-run 2>&1)
if echo "$out" | grep -q "file not found"; then no "1: heading links reported as file not found: $out"; else ok "1: heading links to files that exist are not reported"; fi
# The dry run prints each rewritten line whole, so a line holding a heading
# link AND an adopted code link shows both; the files after a real run are the
# evidence that the heading link itself was left exactly as written.
cp CONTRIBUTING.md "$S/contrib.orig"
ds adopt >/dev/null 2>&1
if grep -q '\[t\](\.\./README\.md#target)' docs/a.md && cmp -s CONTRIBUTING.md "$S/contrib.orig"; then ok "1: heading links are left exactly as written"; else no "1: a heading link was rewritten: $(cat docs/a.md) / $(cat CONTRIBUTING.md)"; fi
if echo "$out" | grep -q "1 link(s) would be adopted"; then ok "1: a code link from a subdirectory resolves against the linking file"; else no "1: ../internal/s.go#Save did not adopt: $out"; fi

# --- 2. unknown file types never receive an uncommented directive ---
setup
printf 'dbName = "demo_db"\n' > app.pkl
ds init >/dev/null
ds def app.pkl:1 >/dev/null 2>&1
if head -1 app.pkl | grep -q '^// ds:def'; then ok "2: a .pkl receives a // comment, not a bare line"; else no "2: .pkl got: $(head -1 app.pkl)"; fi
if command -v pkl >/dev/null 2>&1; then
  if pkl eval app.pkl >/dev/null 2>&1; then ok "2: pkl eval still accepts it"; else no "2: pkl eval rejects it"; fi
fi
if [ "$(ds scan 2>&1 | sed -E 's/.* ([0-9]+) defs.*/\1/')" = 1 ]; then ok "2: .pkl is scanned, so the def is read back"; else no "2: .pkl def not read: $(ds scan 2>&1 | head -1)"; fi

# --- 3. YAML keys containing a colon ---
setup
printf "version: '3'\ntasks:\n  wfsys:up:\n    cmds:\n      - echo up\n  docs:check:\n    desc: Check\n" > Taskfile.yml
ds init >/dev/null
for k in 'tasks.wfsys:up' 'tasks."wfsys:up"' 'wfsys:up' 'tasks.docs:check.desc'; do
  if ds def "Taskfile.yml#$k" --dry-run >/dev/null 2>&1; then ok "3: $k is addressable"; else no "3: $k: $(ds def "Taskfile.yml#$k" --dry-run 2>&1)"; fi
done

# --- 4. ds init hands out a manual CI trigger ---
setup; ds init >/dev/null
if awk '/^on:/{f=1;next} f&&/^[^ ]/{f=0} f' .ds/ci-github.yml | grep -qE '^  (push|pull_request|schedule):'; then no "4: ci-github.yml triggers automatically"; else ok "4: ci-github.yml has no automatic trigger"; fi
if grep -q 'workflow_dispatch' .ds/ci-github.yml; then ok "4: it can be started by hand"; else no "4: no workflow_dispatch"; fi
if grep -q '^#   on:' .ds/ci-github.yml; then ok "4: the automatic triggers are in the header to paste back"; else no "4: no way back to automatic triggers is written down"; fi

# --- 5. what worked first time keeps working ---
setup
printf 'services:\n  app:\n    image: hatchet:1.2.3\n' > compose.yml
printf 'package p\n\ntype Service struct{}\n\nfunc (s *Service) StorageSelfCheck() error { return nil }\n' > svc.go
ds init >/dev/null
if ds def compose.yml:3 >/dev/null 2>&1; then ok "5: a line def on YAML binds"; else no "5: yaml line def failed"; fi
if ds def 'svc.go#Service.StorageSelfCheck' >/dev/null 2>&1; then ok "5: a Go method symbol binds"; else no "5: Go method def failed"; fi
if ds doctor >/dev/null 2>&1; then ok "5: doctor runs"; else no "5: doctor failed"; fi

# --- 6. go.work and go.mod ---
setup
printf 'go 1.26\n\nuse .\n' > go.work
printf 'module example.com/m\n\ngo 1.26\n' > go.mod
ds init >/dev/null
ds def go.work:1 >/dev/null 2>&1; ds def go.mod:1 >/dev/null 2>&1
if go work edit -json >/dev/null 2>&1; then ok "6: go.work still parses after def"; else no "6: go.work broken: $(go work edit -json 2>&1 | head -1)"; fi
if go mod edit -json >/dev/null 2>&1; then ok "6: go.mod still parses after def"; else no "6: go.mod broken"; fi
if [ "$(ds scan 2>&1 | sed -E 's/.* ([0-9]+) defs.*/\1/')" = 2 ]; then ok "6: both are scanned, so the defs are read back"; else no "6: $(ds scan 2>&1 | head -1)"; fi
# ...and a workspace already damaged by the old behaviour is found and fixed.
setup
printf 'ds:def id=w-t4k2b9rf\ngo 1.26\n\nuse .\n' > go.work
printf 'module example.com/m\n\ngo 1.26\n' > go.mod
ds init >/dev/null; git add -A; git commit -qm x >/dev/null
if ds scan 2>&1 | grep -q "go.work:1"; then ok "6: existing damage is reported with its file and line"; else no "6: damage not reported"; fi
ds repair --apply >/dev/null 2>&1
if go work edit -json >/dev/null 2>&1; then ok "6: ds repair --apply makes it parse again"; else no "6: still broken after repair"; fi

# --- 7. JSON is refused, never written ---
setup
printf '{\n  "port": 8080\n}\n' > portless.json
cp portless.json "$S/p.orig"
ds init >/dev/null
if ds def portless.json:2 >/dev/null 2>&1; then no "7: def was allowed into JSON"; else ok "7: def into JSON is refused"; fi
if ds def portless.json:2 --dry-run >/dev/null 2>&1; then no "7: the dry run did not predict the refusal"; else ok "7: the dry run predicts the refusal"; fi
if cmp -s portless.json "$S/p.orig"; then ok "7: the file is byte-identical"; else no "7: the file was modified"; fi

# ...and a JSON file the old behaviour already broke is repaired by deleting
# the line, since JSON has no comment to put it in, and undo gives it back.
setup
printf 'ds:def id=j-k7m2p4xq\n{\n  "port": 8080\n}\n' > portless.json
cp portless.json "$S/broken.json"
ds init >/dev/null; git add -A; git commit -qm x >/dev/null
if python3 -c 'import json;json.load(open("portless.json"))' 2>/dev/null; then no "7: the damaged fixture already parses, so this case proves nothing"; else ok "7: the damaged JSON does not parse, as reported"; fi
if ds scan 2>&1 | grep -q "portless.json:1"; then ok "7: existing JSON damage is reported"; else no "7: JSON damage not reported"; fi
ds repair --apply >/dev/null 2>&1
if python3 -c 'import json;json.load(open("portless.json"))' 2>/dev/null; then ok "7: ds repair --apply makes the JSON parse again"; else no "7: still broken after repair: $(cat portless.json)"; fi
ds undo >/dev/null 2>&1
if cmp -s portless.json "$S/broken.json"; then ok "7: ds undo restores the deleted line byte for byte"; else no "7: undo did not restore: $(cat portless.json)"; fi

# --- 8. a TypeScript object-literal property ---
setup
printf 'export default {\n  server: {\n    port: 5173,\n  },\n};\n' > vite.config.ts
ds init >/dev/null
id=$(ds def vite.config.ts:3 2>&1)
case $id in server-port-*) ok "8: the property binds and its id is named server-port";; *) no "8: got '$id'";; esac
if ds scan 2>&1 | grep -q "0 problems"; then ok "8: the scan reads it back with no problem"; else no "8: $(ds scan 2>&1 | tail -2)"; fi
if ds facts 2>&1 | grep -q 5173; then ok "8: facts reports its value"; else no "8: facts: $(ds facts 2>&1)"; fi
# Whatever cannot bind is refused BEFORE the file is touched.
printf 'export default {\n  [k]: {\n  },\n};\n' > weird.ts
cp weird.ts "$S/w.orig"
ds def weird.ts:3 >/dev/null 2>&1
if cmp -s weird.ts "$S/w.orig"; then ok "8: a line that cannot bind leaves the file untouched"; else no "8: a directive was written where it could not bind"; fi

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
