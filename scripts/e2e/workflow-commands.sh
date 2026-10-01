#!/bin/sh
# the commands that rewrite files or route work, end to end: rename relabels
# ids without touching identity or unrelated text, adopt turns path links into
# defs and cites and undo puts every byte back, triage groups one mechanical
# change and --ack-group acks exactly that group, and notify dry-runs without
# recording and does not resend what it already sent.
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
pass=0; fail=0; n=0
ck() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
not() { case "$3" in *"$2"*) echo "  FAIL  $1 (found $2 in: $3)"; fail=$((fail+1));; *) echo "  PASS  $1"; pass=$((pass+1));; esac; }
same() { if [ "$2" = "$3" ]; then echo "  PASS  $1"; pass=$((pass+1)); else echo "  FAIL  $1"; fail=$((fail+1)); fi; }
setup() { n=$((n+1)); W="$S/c$n"; mkdir -p "$W/docs"; cd "$W" || exit 1; git init -q -b main .; git config user.email t@t; git config user.name t; }

# ---- rename
setup
printf 'package p\n\n// ds:def id=sess-save-k7m2p4xq\nfunc Save() int { return 1 }\n\n// ds:def id=sess-savex-h3v8n2wd\nfunc SaveX() int { return 1 }\n' > a.go
printf '# D\n\nThe sess-save flow [writes](ds:block?id=sess-save-k7m2p4xq). Also [x](ds:block?id=sess-savex-h3v8n2wd).\n' > docs/d.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm base
sedi 's/return 1/return 2/' a.go; ds scan >/dev/null
ds ack sess-save-k7m2p4xq --doc docs/d.md --line 3 --note ok >/dev/null
ds ack sess-savex-h3v8n2wd --doc docs/d.md --line 3 --note ok >/dev/null
before=$(cat a.go docs/d.md)
ds rename sess-save session-store --dry-run >/dev/null
same "rename --dry-run writes nothing" "$before" "$(cat a.go docs/d.md)"
ds rename sess-save session-store >/dev/null
ck "rename relabels the def" "id=session-store-k7m2p4xq" "$(cat a.go)"
ck "and the citation" "id=session-store-k7m2p4xq" "$(cat docs/d.md)"
ck "a longer label that starts the same is untouched" "id=sess-savex-h3v8n2wd" "$(cat a.go docs/d.md)"
ck "prose that mentions the label is untouched" "The sess-save flow" "$(cat docs/d.md)"
ds scan >/dev/null
ck "identity is the suffix: the acks still hold" "2 ok" "$(ds check 2>&1 | tail -1)"
after=$(cat a.go docs/d.md); ds rename sess-save session-store >/dev/null 2>&1
same "a second rename changes nothing" "$after" "$(cat a.go docs/d.md)"

# ---- adopt and undo
setup
printf 'package p\n\nfunc Save() int {\n\treturn 1\n}\n' > a.go
printf '# D\n\nSaving is [here](a.go#L3-L5).\n' > docs/d.md
ds init >/dev/null; git add -A; git commit -qm base
orig_code=$(cat a.go); orig_doc=$(cat docs/d.md)
ds adopt >/dev/null
ck "adopt writes a def into the code" "ds:def id=" "$(cat a.go)"
ck "and turns the link into a citation" "ds:block?id=" "$(cat docs/d.md)"
ds scan >/dev/null
ck "the adopted citation checks clean" " ok" "$(ds check 2>&1 | tail -1)"
adopted=$(cat a.go docs/d.md); ds adopt >/dev/null 2>&1
same "a second adopt changes nothing" "$adopted" "$(cat a.go docs/d.md)"
ds undo >/dev/null 2>&1
same "undo restores the code byte for byte" "$orig_code" "$(cat a.go)"
same "and the doc" "$orig_doc" "$(cat docs/d.md)"

# ---- triage
setup
printf 'package p\n\n// ds:def id=one-k7m2p4xq\nfunc One() error { return legacy() }\n\n// ds:def id=two-h3v8n2wd\nfunc Two() error { return legacy() }\n\n// ds:def id=three-t4k2b9rf\nfunc Three() int { return 1 }\n' > a.go
printf '# D\n\nOne [x](ds:block?id=one-k7m2p4xq).\n\nTwo [y](ds:block?id=two-h3v8n2wd).\n\nThree [z](ds:block?id=three-t4k2b9rf).\n' > docs/d.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm base
sedi 's/return legacy()/return modern()/; s/return 1/return 7/' a.go; ds scan >/dev/null
ck "three unreviewed changes" "3 error" "$(ds check 2>&1 | tail -1)"
groups=$(ds triage 2>&1)
ck "the identical change is one group" "one-k7m2p4xq" "$(echo "$groups" | grep -A3 '^group 1\|^1')"
g=$(ds triage --json 2>/dev/null | python3 -c "import json,sys; d=json.load(sys.stdin); gs=d if isinstance(d,list) else d.get('groups',[]); print(next((i+1 for i,g in enumerate(gs) if len(g.get('findings',g.get('members',[])))==2), 0))")
ds triage --ack-group "$g" --note "same mechanical change" >/dev/null 2>&1
ck "--ack-group acks exactly that group" "1 error" "$(ds check 2>&1 | tail -1)"
ck "the other change is still reported" "three-t4k2b9rf" "$(ds check 2>&1)"

# ---- notify
setup
printf 'package p\n\n// ds:def id=alpha-k7m2p4xq owner=@core\nfunc A() int { return 1 }\n' > a.go
printf '# D\n\nA [x](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm base
sedi 's/return 1/return 2/' a.go; ds scan >/dev/null
dry=$(ds notify --dry-run 2>&1)
ck "notify --dry-run shows the digest" "@core" "$dry"
[ ! -f .ds/notified.json ] && { echo "  PASS  and records nothing"; pass=$((pass+1)); } || { echo "  FAIL  --dry-run recorded state"; fail=$((fail+1)); }
first=$(ds notify 2>&1)
ck "a real notify sends the finding" "alpha-k7m2p4xq" "$first"
second=$(ds notify 2>&1)
not "a second notify does not resend it" "alpha-k7m2p4xq" "$second"

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
