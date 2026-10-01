#!/bin/sh
# Round trips over every command that writes, in Go, TypeScript, Python, YAML
# and markdown: what one command does, its inverse undoes byte for byte, and
# what a preview promises is what the real run does.
#
#   def, then undo           the file is back byte for byte
#   rename, then rename back every file is back byte for byte
#   refresh, then refresh    the second changes nothing
#   adopt --dry-run, adopt   the same lines change, to the same text
#   ack, then audit          the audit shows each ack and its note
#
# Each property holds for any input, so a writer that drops a byte, a
# preview that drifts from the real run, or a second run that is not a
# no-op is caught whatever the fixture holds.
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
eq() { if [ "$2" = "$3" ]; then echo "  PASS  $1"; pass=$((pass+1)); else echo "  FAIL  $1 (want $2, got $3)"; fail=$((fail+1)); fi; }
ne() { if [ "$2" != "$3" ]; then echo "  PASS  $1"; pass=$((pass+1)); else echo "  FAIL  $1 (both $2)"; fail=$((fail+1)); fi; }
has() { case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
# tree is a checksum of every file outside .ds/, so a write anywhere shows.
tree() { find . -path ./.ds -prune -o -path ./.git -prune -o -type f -print | LC_ALL=C sort | while read -r f; do printf '%s ' "$f"; cksum < "$f"; done | cksum; }
# state is tree plus the committed state ds keeps in .ds/.
state() { { tree; cat .ds/ledger.tsv .ds/refs.tsv .ds/acks.tsv 2>/dev/null; } | cksum; }
# noid replaces every id suffix, so two runs that mint different suffixes
# compare by what they change.
noid() { sed -E 's/(id=[a-z0-9-]*-)[a-z0-9]{8}/\1ID/g'; }

W="$S/w"; mkdir -p "$W/src" "$W/docs"; cd "$W" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
printf 'package src\n\n// Total adds.\nfunc Total(a, b int) int {\n\treturn a + b\n}\n' > src/svc.go
printf 'export function greet(name: string): string {\n  return "hi " + name;\n}\n' > src/web.ts
printf 'def run(job):\n    return job.go()\n' > src/job.py
printf 'server:\n  port: 8080\n  host: example.com\n' > src/conf.yaml
printf '# Notes\n\n## Deploy\n\nRun the deploy script.\n\n## Rollback\n\nRevert the tag.\n' > docs/notes.md
printf '# Page\n\nTotal is [here](../src/svc.go#L4-L6), greet [here](../src/web.ts#L1-L3), run [here](../src/job.py#L1-L2), port [here](../src/conf.yaml#L2).\n' > docs/page.md
ds init >/dev/null; git add -A; git commit -qm base

set +e
# def, then undo, per language.
for target in "src/svc.go#Total" "src/web.ts#greet" "src/job.py#run" "src/conf.yaml#server.host" "docs/notes.md#Deploy"; do
  f=${target%%#*}
  before=$(cksum < "$f")
  ds def "$target" >/dev/null 2>&1
  ne "def $target writes the file" "$before" "$(cksum < "$f")"
  ds undo >/dev/null 2>&1
  eq "def $target, then undo, restores it byte for byte" "$before" "$(cksum < "$f")"
done

# adopt --dry-run, then adopt: the same lines, to the same text.
preview=$(ds adopt --dry-run 2>&1 | grep -E '^[^ ]+:[0-9]+: ' | noid)
ds adopt >/dev/null 2>&1
actual=$(echo "$preview" | while IFS= read -r l; do
  loc=${l%%: *}; f=${loc%:*}; n=${loc##*:}
  printf '%s: %s\n' "$loc" "$(sed -n "${n}p" "$f")"
done | noid)
ne "adopt --dry-run previews something" "" "$preview"
eq "adopt changes exactly the lines adopt --dry-run named, to the same text" "$preview" "$actual"
changed=$(git diff --name-only | LC_ALL=C sort | tr '\n' ' ')
previewed=$(echo "$preview" | sed -E 's/:[0-9]+: .*//' | LC_ALL=C sort -u | tr '\n' ' ')
eq "adopt touches no file the preview did not name" "$previewed" "$changed"
ds def docs/notes.md#Deploy >/dev/null; ds def src/conf.yaml#server.host >/dev/null
ds scan >/dev/null; git add -A; git commit -qm adopted

# rename, then rename back: every file is back.
for label in total greet run server-port deploy; do
  before=$(tree)
  ds rename "$label" "$label-x" >/dev/null 2>&1
  ne "rename $label writes" "$before" "$(tree)"
  ds rename "$label-x" "$label" >/dev/null 2>&1
  eq "rename $label, then back, restores every file" "$before" "$(tree)"
done

# refresh, then refresh: the second is a no-op, in build and in repo mode.
ds refresh >/dev/null 2>&1
s1=$(state); sleep 1; ds refresh >/dev/null 2>&1
eq "refresh twice: the second changes nothing, not even scanned_at (bug 77)" "$s1" "$(state)"
printf '\n<!-- ds:block id=%s -->\n' "$(grep -o 'total-[a-z0-9]*' docs/page.md | head -1)" >> docs/page.md
sed -i.bak 's/mode = "build"/mode = "repo"/' .ds/config.toml && /bin/rm -f .ds/config.toml.bak
ds scan >/dev/null; ds refresh >/dev/null 2>&1
has "repo mode: refresh writes the copy" "return a + b" "$(cat docs/page.md)"
s1=$(state); sleep 1; ds refresh >/dev/null 2>&1
eq "repo mode: refresh twice, the second changes nothing" "$s1" "$(state)"
git checkout -q -- .ds/config.toml docs/page.md; ds scan >/dev/null

# ack, then audit, per language.
sed -i.bak 's/a + b/b + a/; s/"hi "/"hello "/; s/job.go()/job.start()/; s/8080/8081/' src/svc.go src/web.ts src/job.py src/conf.yaml && /bin/rm -f src/*.bak
for label in total greet run server-port; do
  id=$(grep -o "$label-[a-z0-9]*" docs/page.md | head -1)
  ds ack "$id" --doc docs/page.md --line 3 --note "still true for $label" >/dev/null 2>&1
done
audit=$(ds audit 2>&1)
for label in total greet run server-port; do
  has "ack $label, then audit shows it with its note" "still true for $label" "$audit"
done
eq "after the acks, check is clean" 0 "$(ds check >/dev/null 2>&1; echo $?)"
set -e
echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
