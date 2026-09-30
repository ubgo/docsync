#!/bin/sh
# a citation that moves keeps its baselines: a line inserted above it, a
# renamed doc, a comment carrier, check without scan — and the reverse: an
# acked citation stays acked, and one citation's ack never covers another's.
#
# Before ledger.Carry, adding one line above a citation and scanning turned an
# unreviewed change into "up to date".
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
n=0
setup() {
  n=$((n+1)); W="$S/case$n"; mkdir -p "$W/docs"; cd "$W" || exit 1
  git init -q -b main .; git config user.email t@t; git config user.name t
  printf 'package p\n\n// ds:def id=alpha-k7m2p4xq\nfunc A() int { return 1 }\n' > a.go
}
base() { ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm base; }
drift() { sedi 's/return 1/return 2/' a.go; ds scan >/dev/null; }
ck() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
last() { ds check 2>&1 | tail -1; }

setup; printf '# D\n\nA returns [one](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md; base
drift; printf '# D\n\nIntro.\n\nA returns [one](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md; ds scan >/dev/null
ck "line inserted above a linked cite, then scan" "1 error" "$(last)"

setup; printf '# D\n\nA returns [one](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md; base
drift; git mv docs/d.md docs/renamed.md; ds scan >/dev/null
ck "doc renamed, then scan" "1 error" "$(last)"

setup; printf '# D\n\n<!-- ds:block id=alpha-k7m2p4xq -->\nA returns one.\n' > docs/d.md; base
drift; printf '# D\n\nIntro.\n\n<!-- ds:block id=alpha-k7m2p4xq -->\nA returns one.\n' > docs/d.md; ds scan >/dev/null
ck "comment-carried cite shifted, then scan" "1 error" "$(last)"

setup; printf '# D\n\nA returns [one](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md; base
drift; printf '# D\n\nIntro.\n\nA returns [one](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md
ck "linked cite shifted, check without scan" "1 error" "$(last)"

setup; printf '# D\n\nA returns [one](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md; base
drift; ds ack alpha-k7m2p4xq --doc docs/d.md --line 3 --note ok >/dev/null
printf '# D\n\nIntro.\n\nA returns [one](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md; ds scan >/dev/null
ck "an acked cite that moves stays acked" "none" "$(last)"

# Two citations of one block; the first is acked after the change, the second
# is not. Inserting lines puts the first on the second's old line, where
# position alone would hand it the wrong baseline.
setup; printf '# D\n\nFirst [x](ds:block?id=alpha-k7m2p4xq).\n\nSecond [y](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md; base
drift; ds ack alpha-k7m2p4xq --doc docs/d.md --line 3 --note ok >/dev/null
printf '# D\n\nPad.\n\nFirst [x](ds:block?id=alpha-k7m2p4xq).\n\nSecond [y](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md; ds scan >/dev/null
out=$(ds check 2>&1)
flagged=$(echo "$out" | awk '$2=="error"{print $1}' | tr '\n' ' ')
ck "swap: only the unreviewed cite flags" "7 " "$flagged"
ck "swap: exactly one finding" "1 error" "$(echo "$out" | tail -1)"

# An acked citation that moves and is re-scanned keeps its ack's sentence:
# refs.tsv carries the acked hash to the new line while the ack row still
# names the old one, and the wording check used to lose it there.
setup; printf '# D\n\nA returns [one](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md; base
drift; ds ack alpha-k7m2p4xq --doc docs/d.md --line 3 --note ok >/dev/null
printf '# D\n\nIntro.\n\nA returns [one](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md; ds scan >/dev/null
ck "moved and re-scanned, same wording: still acked" "none" "$(last)"
ds scan >/dev/null
printf '# D\n\nIntro.\n\nA now returns [one](ds:block?id=alpha-k7m2p4xq).\n' > docs/d.md
ck "moved, re-scanned, then rewritten: reported as a rewrite" "sentence rewritten" "$(ds check 2>&1)"
ck "and the was/now diff names the old wording" "-A returns [one]" "$(ds check --json 2>&1)"

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
