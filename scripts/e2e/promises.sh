#!/bin/sh
# The spec's absolute promises -- "never", "always", "refused", "deterministic"
# -- each driven through the real binary. A promise stated in prose is a claim;
# this matrix is what makes it a checked one. Each case names the SPEC section
# it pins, so a failure says which sentence stopped being true.
#
# Promises pinned elsewhere, so not repeated here: secrets never leave the
# scanner (secrets.sh); read-only commands write nothing and git never reads a
# value as an option (query-commands.sh, workflow-commands.sh); staleness and
# notify never change an exit code (snapshot-staleness.sh).
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
# sedi edits a file in place the one way BSD, GNU and busybox sed all accept:
# a backup suffix attached to -i, and the backup removed.
sedi() { for _f in "$@"; do :; done; sed -i.bak "$@" && /bin/rm -f "$_f.bak"; }
S=$(mktemp -d)
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
ok() { echo "  PASS  $1"; pass=$((pass+1)); }
no() { echo "  FAIL  $1"; fail=$((fail+1)); }
ck() { if [ "$2" = "$3" ]; then ok "$1"; else no "$1 (want $2, got $3)"; fi; }
has() { case "$2" in *"$3"*) ok "$1";; *) no "$1 (no \"$3\" in: $(printf '%s' "$2" | head -3))";; esac; }
repo() { # $1 dir: a fresh git repo with an identity
  mkdir -p "$1"; cd "$1" || exit 1
  git init -q .; git config user.email t@t; git config user.name t
}

# ---- §26.4 order is deterministic for identical inputs --------------------
# promise:deterministic-order
# Thirty defs, each cited, eight of them drifted: enough entries that a map
# iterated without sorting shows up. Built twice, files created in opposite
# orders, so filesystem order cannot stand in for a sort either.
mk() { # $1 dir, $2 fwd|rev
  repo "$1"; mkdir -p docs src
  L=abcdefghijklmnopqrstuvwxyzabcdefgh
  s=$(seq 1 30); [ "$2" = rev ] && s=$(seq 30 -1 1)
  for i in $s; do
    id=val$i-k7m2p4$(echo $L | cut -c$i-$((i+1)))
    printf 'package s\n\nconst V%d = %d // ds:def id=%s\n' "$i" "$i" "$id" > src/v$i.go
    printf '# P%d\n\nSee [v](ds:cfg?id=%s).\n\n<!-- ds:block id=%s -->\nValue is %d.\n' "$i" "$id" "$id" "$i" > docs/p$i.md
  done
  ds init >/dev/null; git add -A; git commit -qm a; ds scan >/dev/null; git add -A; git commit -qm b
  for i in 1 5 9 13 17 21 25 29; do sedi "s/= $i \/\//= 9$i \/\//" src/v$i.go; done
}
mk "$S/fwd" fwd; mk "$S/rev" rev
# Only what differs by construction is dropped: the clock, the repo name
# (the temp dir), and the commit sha.
norm() { sed -e "s/$(basename "$S")//g" -e 's/fwd//g' -e 's/rev//g' | grep -v -e generated_at -e '"commit"' -e scanned_at; }
for c in "check" "check --json" "context --json docs" "map --json" "facts --json" "status --json" "impact --json" "report --json" "triage --json" "graph --json"; do
  # $c is split into words on purpose: it is a command and its arguments.
  (cd "$S/fwd"; ds $c 2>&1 | norm) > "$S/first"
  lines=$(wc -l < "$S/first" | tr -d ' ')
  if [ "$lines" -lt 5 ]; then no "§26.4 ds $c produced $lines lines; the comparison would be vacuous"; continue; fi
  same=yes
  for k in 1 2 3 4 5; do (cd "$S/fwd"; ds $c 2>&1 | norm) | cmp -s - "$S/first" || same=no; done
  ck "§26.4 ds $c: identical across repeated runs" yes $same
  (cd "$S/rev"; ds $c 2>&1 | norm) | cmp -s - "$S/first" && x=yes || x=no
  ck "§26.4 ds $c: identical whatever order the files were created in" yes $x
done

# ---- §17 --strict never promotes moved or deprecated ----------------------
# promise:strict-moved
repo "$S/strict"; mkdir -p docs src
printf 'package s\n\n// ds:def id=old-a2b6f8jk deprecated=2026-01-01\nvar Old = 1\n' > src/a.go
printf 'package s\n\n// ds:def id=mv-c4d8h2lm\nfunc Mv() int {\n\treturn 1\n}\n' > src/m.go
printf '# P\n\nOld is [x](ds:block?id=old-a2b6f8jk).\n\nMv is [y](ds:block?id=mv-c4d8h2lm).\n' > docs/p.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm a
out=$(ds check --strict 2>&1); rc=$?
has "§17 a deprecation is reported" "$out" "deprecated since 2026-01-01"
ck "§17 --strict with only a deprecation exits 0" 0 $rc
# One citation carries one state, so the move gets its own def.
/bin/rm src/m.go; printf 'package s\n\n// ds:def id=mv-c4d8h2lm\nfunc Mv() int {\n\treturn 1\n}\n' > src/n.go
out=$(ds check --strict 2>&1); rc=$?
has "§17 a move is reported" "$out" "moved from src/m.go"
ck "§17 --strict with a move and a deprecation exits 0" 0 $rc

# ---- §33 an older tool refuses a newer format by name ---------------------
# promise:format-by-name promise:extract-rule
repo "$S/fmt"; mkdir -p docs src
printf 'package s\n\nconst V = 1 // ds:def id=val-k7m2p4xq\n' > src/v.go
printf '# P\n\nSee [v](ds:cfg?id=val-k7m2p4xq).\n' > docs/p.md
ds init >/dev/null; ds scan >/dev/null
ds ack val-k7m2p4xq --doc docs/p.md --line 3 --note ok >/dev/null; ds scan >/dev/null; git add -A; git commit -qm a
for f in ledger refs acks; do
  cp .ds/$f.tsv "$S/orig"
  sedi "1s/format=[0-9]*/format=99/" .ds/$f.tsv; cp .ds/$f.tsv "$S/newer"
  out=$(ds check 2>&1); rc=$?
  ck "§33 $f.tsv with format=99: check refuses" 2 $rc
  has "§33 $f.tsv with format=99: refused by name" "$out" "file format 99"
  # The downgrade a refusal exists to stop: an older scan rewriting the file.
  ds scan >/dev/null 2>&1
  cmp -s .ds/$f.tsv "$S/newer" && x=kept || x=rewritten
  ck "§33 $f.tsv with format=99: an older scan leaves it untouched" kept $x
  out=$(ds doctor 2>&1); rc=$?
  ck "§33 $f.tsv with format=99: doctor fails" 2 $rc
  has "§33 $f.tsv with format=99: doctor says why" "$out" "FAIL"
  cp "$S/orig" .ds/$f.tsv
done
# A newer extraction rule is refused the same way, naming both rules and the
# fix: its hashes cannot be compared with this build's, and an older scan
# would rewrite the files under the older rule.
for f in ledger refs; do
  cp .ds/$f.tsv "$S/orig"
  sedi "1s/extract=[0-9]*/extract=99/" .ds/$f.tsv; cp .ds/$f.tsv "$S/newer"
  out=$(ds check 2>&1); rc=$?
  ck "§33 $f.tsv with extract=99: check refuses" 2 $rc
  has "§33 $f.tsv with extract=99: refused naming both rules" "$out" "$f.tsv says extract=99, this build implements rule"
  has "§33 $f.tsv with extract=99: the refusal gives the pre-release fix" "$out" "set extract=1"
  ds scan >/dev/null 2>&1; rc=$?
  cmp -s .ds/$f.tsv "$S/newer" && x=kept || x=rewritten
  ck "§33 $f.tsv with extract=99: scan refuses" 2 $rc
  ck "§33 $f.tsv with extract=99: an older scan leaves it untouched" kept $x
  out=$(ds doctor 2>&1); rc=$?
  ck "§33 $f.tsv with extract=99: doctor fails" 2 $rc
  has "§33 $f.tsv with extract=99: doctor names it" "$out" "extract=99"
  # The fix the refusal gives for a repo a pre-release build wrote.
  sedi "1s/extract=99/extract=1/" .ds/$f.tsv
  ds check >/dev/null 2>&1; ck "§33 $f.tsv: setting extract=1 clears the refusal" 0 $?
  cp "$S/orig" .ds/$f.tsv
done
# The ack log's header is written once and never restamped, so it says
# nothing about its rows; each row's rule is what check reports.
cp .ds/acks.tsv "$S/orig"; sedi "1s/extract=[0-9]*/extract=99/" .ds/acks.tsv
out=$(ds check 2>&1); rc=$?
ck "§33 acks.tsv header rule is not treated as the rows' rule" 0 $rc
cp "$S/orig" .ds/acks.tsv
ds check >/dev/null 2>&1; ck "§33 restored state checks clean" 0 $?

# ---- §15 seen_hash is written once and never revised ----------------------
# promise:seen-hash-once
cd "$S/fmt" || exit 1
printf '# P\n\nSee [v](ds:cfg?id=val-k7m2p4xq).\n\nAnd a second [v](ds:cfg?id=val-k7m2p4xq) citation.\n' > docs/p.md
ds scan >/dev/null
first=$(awk -F'\t' '$1=="val-k7m2p4xq" && $4=="5" {print $8}' .ds/refs.tsv)
[ -n "$first" ]; ck "§15 a new citation records a seen_hash" 0 $?
for v in 2 3 4; do sedi "s/= [0-9] /= $v /" src/v.go; ds scan >/dev/null; done
now=$(awk -F'\t' '$1=="val-k7m2p4xq" && $4=="5" {print $8}' .ds/refs.tsv)
ck "§15 seen_hash survives three changed scans unrevised" "$first" "$now"
out=$(ds check 2>&1)
has "§15 the unacked citation is still measured against it" "$out" "since this sentence was first cited"

# ---- §7 what is and is not a directive, and what is scanned ---------------
# promise:datastore-not-directive promise:source-only promise:frontmatter
repo "$S/scan"; mkdir -p docs src public dist
printf 'package a\n\n// ds is the datastore\nconst Port = 8080 // ds:def id=port-h3v8n2wd\n' > src/a.go
printf 'Cite [p](ds:cfg?id=nope-zzzzzzzz).\n<!-- ds:def id=built-q9x1z6ch -->\nBuilt.\n' > public/p.md; cp public/p.md dist/p.md
printf -- '---\ntitle: One\n---\n<!-- ds:def id=sec-t4k2b9rf -->\n# Width\n\nAt most 4.\n' > docs/spec.md
printf '# P\n\nPort [8080](ds:cfg?id=port-h3v8n2wd); width [b](ds:block?id=sec-t4k2b9rf).\n' > docs/p.md
ds init >/dev/null; out=$(ds scan 2>&1); git add -A; git commit -qm a
has "§7 exactly the two real defs are found" "$out" "2 defs"
ck "§7 '// ds is the datastore' is not a directive" 0 "$(grep -c datastore .ds/ledger.tsv)"
ck "§7 public/ and dist/ are never scanned" 0 "$(grep -c -e built- -e nope- .ds/ledger.tsv .ds/refs.tsv | awk -F: '{s+=$2} END {print s}')"
lines=$(awk -F'\t' '$1=="sec-t4k2b9rf" {print $6}' .ds/ledger.tsv)
ck "Part VII: frontmatter is never part of a block" 5-7 "$lines"
sedi 's/title: One/title: Two/' docs/spec.md
ds check >/dev/null 2>&1; ck "Part VII: editing only the frontmatter changes no block" 0 $?
git add -A; git commit -qm frontmatter

# ---- §22 a path outside the repository is refused -------------------------
# promise:path-outside
mkdir -p "$S/outside"; echo x > "$S/outside/o.md"
for c in "render $S/outside/o.md" "blame $S/outside/o.md 1" "context $S/outside/o.md" "def $S/outside/o.md#X" \
         "ack port-h3v8n2wd --doc $S/outside/o.md --line 1 --note x" "render ../outside/o.md" "render /etc/hosts"; do
  out=$(ds $c 2>&1); rc=$?
  ck "§22 ds $(echo "$c" | sed "s|$S|\$S|g"): exits 2" 2 $rc
  has "§22 ds $(echo "$c" | sed "s|$S|\$S|g"): says why" "$out" "outside the repository"
done
git status --porcelain | grep -q . && x=dirty || x=clean
ck "§22 the refused commands wrote nothing" clean $x

# ---- Part VII: a duplicated def is a hard error; the tool never guesses ---------
# promise:duplicate-def
repo "$S/dup"; mkdir -p docs src
printf 'package s\n\nconst V = 1 // ds:def id=val-k7m2p4xq\n' > src/v.go
printf 'package s\n\nconst W = 1 // ds:def id=val-k7m2p4xq\n' > src/w.go
printf '# P\n\nSee [v](ds:cfg?id=val-k7m2p4xq).\n' > docs/p.md
ds init >/dev/null; ds scan >/dev/null 2>&1; git add -A; git commit -qm a
out=$(ds check 2>&1); rc=$?
ck "Part VII: a duplicated id fails check" 1 $rc
has "Part VII: the remedy names ds def --fix" "$out" "ds def --fix"
out=$(ds def --fix --dry-run 2>&1)
has "Part VII: def --fix re-mints the second copy and prints old -> new" "$out" "val-k7m2p4xq@src/w.go:3 ->"
git status --porcelain | grep -q . && x=dirty || x=clean
ck "Part VII: --dry-run writes nothing" clean $x

# ---- §9.3 a ds:cfg pick of more than one line is refused ------------------
# promise:cfg-one-line
repo "$S/pick"; mkdir -p docs src
printf 'package s\n\n// ds:def id=fn-k7m2p4xq\nfunc F() int {\n\treturn 1\n}\n' > src/v.go
printf '# P\n\nF is [x](ds:cfg?id=fn-k7m2p4xq).\n' > docs/p.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm a
out=$(ds render docs/p.md 2>&1)
has "§9.3 render refuses a multi-line cfg with 'use ds:block'" "$out" "use ds:block"
has "§9.3 render keeps the link text instead of substituting" "$out" "F is x."
out=$(ds check 2>&1)
has "§9.3 check reports the range" "$out" "ds:cfg on a 3-line block"
ds check --strict >/dev/null 2>&1; ck "§9.3 --strict fails on it" 1 $?

# ---- §19 whitespace is never reported where a grammar hashes the tokens ---
# promise:whitespace-silent promise:formatters
repo "$S/ws"; mkdir -p docs src
printf 'package s\n\n// ds:def id=depth-k7m2p4xq\nfunc Depth() int {\n\treturn 12\n}\n' > src/a.go
printf '// ds:def id=width-h3v8n2wd\nfn width() -> i32 {\n\treturn 4;\n}\n' > src/b.rs
printf '# P\n\nDepth [x](ds:block?id=depth-k7m2p4xq) and width [y](ds:block?id=width-h3v8n2wd).\n' > docs/p.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm a
printf 'package s\n\n// ds:def id=depth-k7m2p4xq\nfunc Depth() int {\n    return 12\n}\n' > src/a.go
printf '// ds:def id=width-h3v8n2wd\nfn width() -> i32 {\n    return 4;\n}\n' > src/b.rs
out=$(ds check 2>&1)
case "$out" in *depth-k7m2p4xq*) x=reported;; *) x=silent;; esac
ck "§19 re-indenting Go (token-stream tier) is never reported" silent $x
case "$out" in *"width-h3v8n2wd changed (body)"*) x=reported;; *) x=silent;; esac
ck "§19 re-indenting a file with no grammar is content, and is reported" reported $x

# ---- §15 a directive is metadata about text, never text ------------------
# promise:directive-not-text
repo "$S/meta"; mkdir -p docs src
printf 'package s\n\n// ds:def id=depth-k7m2p4xq\nfunc Depth() int {\n\treturn 12\n}\n' > src/a.go
printf '# P\n\nDepth [x](ds:block?id=depth-k7m2p4xq).\n' > docs/p.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm a
printf 'package s\n\n// ds:def id=depth-k7m2p4xq owner=@core stability=api desc=depth\nfunc Depth() int {\n\treturn 12\n}\n' > src/a.go
ds check >/dev/null 2>&1; ck "§15 editing only a def's directive is not a change to its block" 0 $?

# ---- Part VII: generated paths refuse minting -----------------------------
# promise:generated-no-mint
repo "$S/gen"; mkdir -p docs gen
printf 'package g\n\nfunc Gen() int { return 1 }\n' > gen/a.go
printf '# P\n' > docs/p.md
ds init >/dev/null; git add -A; git commit -qm a
out=$(ds def 'gen/a.go#Gen' 2>&1); rc=$?
ck "Part VII: ds def in a generated path is refused" 2 $rc
has "Part VII: the refusal says why" "$out" "matches [scan] generated"
git status --porcelain | grep -q . && x=dirty || x=clean
ck "Part VII: the refused def wrote nothing" clean $x

# ---- README: task install never clobbers a file it did not make --------------
# promise:install-no-clobber
IB="$S/ib"; ID="$S/id"; mkdir -p "$IB" "$ID"
printf '#!/bin/sh\necho dev\n' > "$IB/ds"; chmod +x "$IB/ds"
printf 'a real install\n' > "$ID/ds"
sh "$ROOT/scripts/install-links.sh" "$IB" "$ID" >/dev/null 2>&1
[ -L "$ID/ds" ] && x=clobbered || x=kept
ck "install leaves a file it did not make alone" kept $x
ck "install leaves its contents byte for byte" "a real install" "$(cat "$ID/ds")"
/bin/rm "$ID/ds"; sh "$ROOT/scripts/install-links.sh" "$IB" "$ID" >/dev/null 2>&1
[ -L "$ID/ds" ] && x=linked || x=missing
ck "install links where nothing is in the way" linked $x
sh "$ROOT/scripts/install-links.sh" "$IB" "$ID" >/dev/null 2>&1; ck "install replaces its own link again" 0 $?

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" = "0" ]
