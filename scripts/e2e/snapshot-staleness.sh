#!/bin/sh
# the committed foreign snapshot: moves, frozen checks, and how far behind upstream it is (bugs 6, 8)
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
set -u
# sedi edits a file in place the one way BSD, GNU and busybox sed all accept:
# a backup suffix attached to -i, and the backup removed.
sedi() { for _f in "$@"; do :; done; sed -i.bak "$@" && /bin/rm -f "$_f.bak"; }
S=$(mktemp -d)
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
ck() { if [ "$2" = "$3" ]; then echo "  PASS  $1"; pass=$((pass+1)); else echo "  FAIL  $1 (want $2, got $3)"; fail=$((fail+1)); fi; }
W="$S/m5"; /bin/rm -rf "$W"; mkdir -p "$W/index" "$W/docs/spec" "$W/code/pkg"
cd "$W/docs" || exit 1
git init -q -b main . >/dev/null; git config user.email t@t; git config user.name t
printf '# Spec\n\n### Depth\n\nAt most 12 deep.\n' > spec/SPEC.md
ds init >/dev/null
python3 -c "
import pathlib
p=pathlib.Path('.ds/config.toml'); t=p.read_text()
p.write_text('workspace = \"$W/index\"\n'+t.replace('code = [\"**\"]','code = [\"spec/**\"]').replace('docs = [\"**/*.md\"]','docs = [\"spec/**/*.md\"]'))"
ID=$(ds def "spec/SPEC.md#Depth" --label depth | tail -1)
ds scan >/dev/null; git add -A; git commit -qm a >/dev/null; ds publish >/dev/null
cd "$W/code" || exit 1
git init -q -b main . >/dev/null; git config user.email t@t; git config user.name t
printf 'package pkg\n\n// MaxDepth — implements ds:block?id=%s\nconst MaxDepth = 12\n' "$ID" > pkg/depth.go
ds init >/dev/null
python3 -c "
import pathlib
p=pathlib.Path('.ds/config.toml'); t=p.read_text()
p.write_text('workspace = \"$W/index\"\n'+t.replace('code = [\"**\"]','code = [\"pkg/**\"]').replace('docs = [\"**/*.md\"]','docs = [\"pkg/**\"]'))"
ds scan >/dev/null; ds sync >/dev/null
ds ack "$ID" --doc pkg/depth.go --line 3 --note ok >/dev/null
ds scan >/dev/null; git add -A; git commit -qm b >/dev/null
ds status 2>/dev/null | grep -q "up to date"; ck "bug 6: fresh snapshot is up to date" 0 $?

# upstream MOVES the block, content unchanged
cd "$W/docs" || exit 1
printf '# Spec\n\nIntro.\n\nMore intro.\n\n<!-- ds:def id=%s -->\n### Depth\n\nAt most 12 deep.\n' "$ID" > spec/SPEC.md
ds scan >/dev/null; git add -A; git commit -qm move >/dev/null; ds publish >/dev/null
cd "$W/code" || exit 1
ds check --full 2>/dev/null | grep -q "moved from"; ck "bug 8: cross-repo moved is reported" 0 $?
ds check --full >/dev/null 2>&1; ck "bug 8: a move is not a failure" 0 $?
ds check --frozen 2>/dev/null | grep -q "moved from"; ck "bug 8: frozen reports no move" 1 $?

# upstream CHANGES it
cd "$W/docs" || exit 1
sedi 's/At most 12 deep/At most 6 deep/' spec/SPEC.md
ds scan >/dev/null; git add -A; git commit -qm change >/dev/null; ds publish >/dev/null
cd "$W/code" || exit 1
ds status 2>/dev/null | grep -q "cited blocks behind"; ck "bug 6: status reports how far behind" 0 $?
ds status >/dev/null 2>&1; ck "bug 6: staleness never changes the exit code" 0 $?
# promise:check-no-write-foreign -- sync is the only writer of the snapshot,
# so a check after upstream changed leaves it byte for byte as it was.
cp .ds/foreign.tsv "$S/foreign.before"
ds check >/dev/null 2>&1; ds check --full >/dev/null 2>&1
cmp -s .ds/foreign.tsv "$S/foreign.before"; ck "check never writes the snapshot" 0 $?
# promise:staleness-exit promise:status-quiet promise:max-age-warning promise:notify-exit
# SPEC §692-693, §867: time and notification never change a frozen check's answer.
ds check --frozen >/dev/null 2>&1; ck "frozen check against a snapshot that fell behind exits 0" 0 $?
ds check --frozen --strict >/dev/null 2>&1; ck "--strict does not turn staleness into a failure" 0 $?
cp .ds/config.toml "$S/config.bak"; cp .ds/foreign.tsv "$S/foreign.bak"
printf '\n[check]\nsnapshot_max_age = "1d"\n' >> .ds/config.toml
sed -i.orig '1s/scanned_at=[^ ]*/scanned_at=2020-01-01T00:00:00Z/' .ds/foreign.tsv
out=$(ds check --frozen --strict 2>&1); rc=$?
case "$out" in *"older than check.snapshot_max_age"*) w=0;; *) w=1;; esac
ck "snapshot_max_age warns about an old snapshot" 0 $w
ck "snapshot_max_age is never more than a warning, even with --strict" 0 $rc
ds notify --dry-run 2>/dev/null | grep -q unacked; ck "notify reports the open finding" 0 $?
ds notify >/dev/null 2>&1; ck "notify never touches the exit code" 0 $?
cp "$S/config.bak" .ds/config.toml; cp "$S/foreign.bak" .ds/foreign.tsv; /bin/rm -f .ds/foreign.tsv.orig
ds status --json 2>/dev/null | grep -q '"snapshot"'; ck "bug 6: json carries the snapshot section" 0 $?
ds sync >/dev/null 2>&1
ds status 2>/dev/null | grep -q "up to date"; ck "bug 6: syncing clears it" 0 $?
echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" = "0" ]
