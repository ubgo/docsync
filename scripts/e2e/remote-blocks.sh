#!/bin/sh
# Remote defs that bind a whole block without touching the target file: a YAML
# mapping (a Taskfile task), a TOML table, a JSON object, and a code symbol
# (bugs 132 and 133). The cases are the report in GitHub issue 3 run against
# the built binary: before the fix, the YAML pick was refused as a multi-line
# value and `pick=symbol:` was unsupported, so the only way to bind a task or
# a Go var was `ds def`, which writes a comment into the file -- into
# files that belonged to other people, and in one compose file it broke a test.
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
one() { printf '%s' "$1" | tr '\n' ' ' | cut -c1-600; }
sedi() { for _f in "$@"; do :; done; sed -i.bak "$@" && /bin/rm -f "$_f.bak"; }

cd "$S" && git init -q . && git config user.email t@t && git config user.name t
mkdir docs
printf "version: '3'\ntasks:\n  deploy:\n    desc: Run one image tag\n    cmds:\n      - lath run deploy release\n" > Taskfile.yml
printf 'package params\n\n// Manifest lists every key.\nvar Manifest = []string{\n\t"APP_KEY",\n\t"DB_PASSWORD",\n}\n' > manifest.go
printf '[server]\nport = 8080\nhost = "a"\n\n[db]\nname = "x"\n' > app.toml
printf '{\n  "scripts": {\n    "build": "tsc",\n    "test": "vitest"\n  }\n}\n' > package.json
printf '# Run\n\n<!-- ds:def id=task-deploy-a1a1a1a1 file=Taskfile.yml pick=yaml:tasks.deploy -->\n<!-- ds:def id=manifest-b2b2b2b2 file=manifest.go pick=symbol:Manifest -->\n<!-- ds:def id=server-c3c3c3c3 file=app.toml pick=toml:server -->\n<!-- ds:def id=scripts-d4d4d4d4 file=package.json pick=json:scripts -->\n\nDeploy with [the deploy task](ds:block?id=task-deploy-a1a1a1a1). Keys are [the manifest](ds:block?id=manifest-b2b2b2b2). The server is [configured](ds:block?id=server-c3c3c3c3). Scripts are [these](ds:block?id=scripts-d4d4d4d4).\n' > docs/run.md
ds init >/dev/null
for f in Taskfile.yml manifest.go app.toml package.json; do cp "$f" "$S/$f.orig"; done
git add -A; git commit -qm init
out=$(ds scan 2>&1)
case "$out" in *"4 defs"*"0 problems"*) ok "all four remote block defs bind with no problem";; *) no "scan: $(one "$out")";; esac
same=yes; for f in Taskfile.yml manifest.go app.toml package.json; do cmp -s "$f" "$S/$f.orig" || same=no; done
[ "$same" = yes ] && ok "no target file was written to" || no "a target file changed"
loc=$(ds locate task-deploy-a1a1a1a1 2>&1); case "$loc" in "Taskfile.yml:3-6 @"*) ok "yaml:tasks.deploy is the task's key and body (lines 3-6)";; *) no "locate task: $loc";; esac
loc=$(ds locate manifest-b2b2b2b2 2>&1); case "$loc" in "manifest.go:4-7 @"*) ok "symbol:Manifest is the var declaration (lines 4-7)";; *) no "locate manifest: $loc";; esac
loc=$(ds locate server-c3c3c3c3 2>&1); case "$loc" in "app.toml:1-3 @"*) ok "toml:server is the table up to the next header (lines 1-3)";; *) no "locate server: $loc";; esac
body=$(ds read manifest-b2b2b2b2 2>&1); case "$body" in *'var Manifest = []string{'*'"DB_PASSWORD",'*) ok "read gives the symbol's code";; *) no "read: $(one "$body")";; esac
out=$(ds check 2>&1); [ "$?" = 0 ] && ok "check is clean on the tree as cited" || no "check: $(one "$out")"
git add -A; git commit -qm scan

# Moving a block within its file still resolves: the pick is by key or name.
printf "version: '3'\nvars:\n  X: 1\ntasks:\n  build:\n    cmds:\n      - go build\n  deploy:\n    desc: Run one image tag\n    cmds:\n      - lath run deploy release\n" > Taskfile.yml
printf 'package params\n\nconst Other = 1\n\n// Manifest lists every key.\nvar Manifest = []string{\n\t"APP_KEY",\n\t"DB_PASSWORD",\n}\n' > manifest.go
out=$(ds check 2>&1); code=$?
case "$out" in *"moved from Taskfile.yml:3-6"*"moved from manifest.go:4-7"*) ok "a moved task and a moved var are reported moved, not changed";; *) no "move: $(one "$out")";; esac
[ "$code" = 0 ] && ok "a move does not fail check" || no "a move failed check ($code)"

# Changing anything inside a block flags the sentence that cites it.
sedi 's/deploy release/deploy release --wait/' Taskfile.yml
sedi 's/DB_PASSWORD/DB_PASS/' manifest.go
sedi 's/port = 8080/port = 9090/' app.toml
sedi 's/"vitest"/"vitest run"/' package.json
out=$(ds check 2>&1)
for id in task-deploy-a1a1a1a1 manifest-b2b2b2b2 server-c3c3c3c3 scripts-d4d4d4d4; do
  case "$out" in *"unacked            $id changed"*) ok "a change inside $id flags its citation";; *) no "$id not flagged: $(one "$out")";; esac
done
case "$out" in *"+      - lath run deploy release --wait"*) ok "the finding shows the task's diff";; *) no "no task diff: $(one "$out")";; esac

# symbol: on a YAML file binds the task by name, and its block is the file's
# own lines: the in-memory directive used to find it trails the key there,
# and it used to end up in the block's text.
printf '<!-- ds:def id=symtask-g7g7g7g7 file=Taskfile.yml pick=symbol:tasks.deploy -->\n' > docs/sym.md
ds scan >/dev/null 2>&1
body=$(ds read symtask-g7g7g7g7 2>&1)
case "$body" in *"ds:def"*) no "the block carries a directive: $(one "$body")";; *"deploy:"*"desc: Run one image tag"*) ok "symbol: on a YAML task binds the task's own lines";; *) no "symbol: on YAML: $(one "$body")";; esac
/bin/rm -f docs/sym.md

# A pick that names nothing is a problem at the directive, not a silent def.
printf '<!-- ds:def id=nope-e5e5e5e5 file=manifest.go pick=symbol:Missing -->\n<!-- ds:def id=noyaml-f6f6f6f6 file=Taskfile.yml pick=yaml:tasks.missing -->\n' > docs/bad.md
out=$(ds scan 2>&1)
case "$out" in *"docs/bad.md:1"*"symbol not found"*) ok "an unknown symbol is reported at its directive";; *) no "unknown symbol: $(one "$out")";; esac
case "$out" in *"docs/bad.md:2"*"nothing matched"*) ok "an unknown YAML key is reported at its directive";; *) no "unknown key: $(one "$out")";; esac

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
