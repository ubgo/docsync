#!/bin/sh
# Compare what two builds of ds extract from the SAME source, block by block.
#
# Why it exists: the danger in changing an extractor is not a new bug, it is a
# changed hash. A block that already bound and still binds, but whose bytes or
# extent moved, makes every sentence citing it report drift in every repository
# that had it acked -- a silent, repo-wide false alarm that no unit test sees,
# because each test asserts what the current build does and nothing compares
# builds. So this compares them.
#
# The tree is prepared ONCE with directives inserted by awk, never by ds, so
# neither build influences what the other is asked to extract. Both then scan
# byte-identical copies and the ledgers are compared by id.
#
# Usage: REF=<git ref> sh scripts/extract-diff.sh [dir ...]
#   REF      the baseline to build (default: the previous commit)
#   CORPUS   colon-separated trees to compare over (default: the corpus list)
# Exit 1 when any block that bound under both builds changed its hash.
set -u
root=$(cd "$(dirname "$0")/.." && pwd)
REF=${REF:-HEAD~1}
# The sibling repositories that use docsync for real: a Go corpus (ubgo/auth)
# and any private ones listed in scripts/corpus.local (gitignored; one path
# per line, relative to the repository root), such as a markdown-heavy docs
# pilot, so that the code and document tiers are both exercised without a
# private repository's name entering this public tree. Absent ones are skipped.
CORPUS=${CORPUS:-../../ubgo/auth:../../../khanakia/ubgo/auth$(sed -n 's/^\([^#].*\)$/:\1/p' "$root/scripts/corpus.local" 2>/dev/null | tr -d '\n')}
S=$(mktemp -d)
trap '/bin/rm -rf "$S"' EXIT

# --- the baseline binary, built from REF in a detached worktree ---
wt=$S/base
git -C "$root" worktree add -q --detach "$wt" "$REF" 2>/dev/null || {
  echo "extract-diff: cannot check out $REF"; exit 1; }
cleanup_wt() { git -C "$root" worktree remove --force "$wt" >/dev/null 2>&1 || true; }
(cd "$wt/cli" && go build -o "$S/ds-base" ./cmd/ds) || { cleanup_wt; echo "extract-diff: baseline build failed"; exit 1; }
(cd "$root/cli" && go build -o "$S/ds-new" ./cmd/ds) || { cleanup_wt; echo "extract-diff: current build failed"; exit 1; }
base_sha=$(git -C "$wt" rev-parse --short HEAD)
cleanup_wt
echo "baseline $REF ($base_sha)  vs  working tree"

# --- anchor deterministically, with no help from either binary ---
# A fixed suffix alphabet and a counter, so both builds see identical ids and a
# rerun is reproducible. ds is never invoked here on purpose.
anchor() { # $1 file, $2 file index
  awk '
    BEGIN { a="23456789abcdefghjkmnpqrstuvwxyz"; L=length(a); n=0 }
    # fx is the index of this file, passed in by the caller.
    # Plain base-L of i, eight symbols, little end first. An earlier version
    # advanced i wrongly and produced the SAME suffix for many counters; the
    # join then multiplied rows and reported a million changed hashes on a tree
    # with a few thousand blocks. The uniqueness check below refuses to compare
    # ledgers whose ids repeat, so that cannot be mistaken for a result again.
    function suffix(i,   s, j) {
      s = ""
      for (j = 0; j < 8; j++) { s = substr(a, (i % L) + 1, 1) s; i = int(i / L) }
      return s
    }
    # Top-level declarations, group entries, and body members: the same net the
    # corpus gate uses, minus the keywords that start a statement.
    /^(func|type|const|var) / ||
    /^\t[A-Za-z_][A-Za-z0-9_]*( +[A-Za-z0-9_.*\[\]]+)? *=/ ||
    /^\t[A-Za-z_][A-Za-z0-9_]* +[A-Za-z*\[]/ ||
    /^\t[A-Za-z_][A-Za-z0-9_]*\(/ ||
    /^\t"[^"]+"$/ {
      if ($0 !~ /^[\t ]*(case|default|return|if|else|for|switch|select|go|defer|range|break|continue|fallthrough|panic)\b/) {
        n++
        indent = $0; sub(/[^\t ].*$/, "", indent)
        # The suffix is the identity, so it carries the file index too. The
        # counter restarts for every file, so ids collided across files.
        print indent "//ds:def id=probe-" suffix(fx * 4096 + n)
      }
    }
    { print }
  ' fx="$2" "$1" > "$1.anchored" && mv "$1.anchored" "$1"
}

ledger_of() { # $1 tree, $2 binary
  (cd "$1" && "$2" init >/dev/null 2>&1; "$2" scan >/dev/null 2>&1
   awk -F'\t' 'NR>2 && $1 != "" {print $1"\t"$3"\t"$4"\t"$5"\t"$6"\t"$7}' .ds/ledger.tsv | sort)
}

total_same=0; total_moved=0; total_new=0; total_gone=0; total_meta=0; total_repaired=0; bad=0; used=0
: > "$S/seen"
for p in $(echo "$CORPUS" | tr ':' ' '); do
  case $p in /*) d=$p;; *) d=$root/$p;; esac
  [ -d "$d/.git" ] || continue
  real=$(cd "$d" && pwd -P)
  grep -qxF "$real" "$S/seen" 2>/dev/null && continue
  echo "$real" >> "$S/seen"
  name=$(basename "$real")
  used=$((used+1))

  # One prepared tree, then two byte-identical copies.
  src=$S/src-$name
  mkdir -p "$src"
  # The last commit, not the working tree: see scripts/corpus.sh.
  git -C "$real" archive --format=tar HEAD | (cd "$src" && tar xf -)
  /bin/rm -rf "$src/.ds"
  fx=0
  find "$src" -name '*.go' -not -path '*/testdata/*' | sort | while read -r f; do
    fx=$((fx+1)); anchor "$f" "$fx"
  done
  for side in base new; do
    cp -R "$src" "$S/t-$side-$name"
    (cd "$S/t-$side-$name" && git init -q . && git config user.email t@t && git config user.name t &&
      git add -A >/dev/null 2>&1 && git commit -qm x >/dev/null 2>&1)
  done
  if ! cmp -s "$S/t-base-$name/go.mod" "$S/t-new-$name/go.mod" 2>/dev/null; then : ; fi
  ledger_of "$S/t-base-$name" "$S/ds-base" > "$S/L-base"
  ledger_of "$S/t-new-$name" "$S/ds-new"  > "$S/L-new"

  # A join on a repeated key multiplies rows, so a duplicate id turns this
  # comparison into noise that looks like a catastrophic result. Refuse.
  for side in base new; do
    dup=$(cut -f1 "$S/L-$side" | sort | uniq -d | head -3)
    if [ -n "$dup" ]; then
      echo "  $name: ids repeat in the $side ledger, so no comparison is possible: $(echo "$dup" | tr '\n' ' ')"
      bad=$((bad+1)); continue 2
    fi
  done

  # Compare by id and partition the changes. Comparing against a pre-fix
  # baseline must show differences -- that is the fix -- so "changed" is not the
  # question. The question is whether a block naming the SAME thing over the
  # SAME lines is now hashed differently, because only that makes a repository
  # with acked citations report drift for no reason.
  #
  # Symbol and extent together are the test. An earlier version used only the
  # start line and called 23 repairs regressions: the old build had bound an
  # interface method's first PARAMETER, which begins on the same line as the
  # method, so `Operation.ctx` and `Operation.Invoke` looked like one block
  # moving when they are two different things.
  join -t "$(printf '\t')" -j1 "$S/L-base" "$S/L-new" > "$S/J" 2>/dev/null
  awk -F'\t' '$6!=$11 && $4==$9 && $5==$10 {print $1"\t"$3"\t"$4"\t"$5"\t"substr($6,1,8)"\t"substr($11,1,8)}' "$S/J" > "$S/regress"
  awk -F'\t' '$6!=$11 && ($4!=$9 || $5!=$10) {print $1"\t"$3"\t"$4"->"$9"\t"$5"->"$10}' "$S/J" > "$S/repaired"
  same=$(awk -F'\t' '$6==$11 {c++} END {print c+0}' "$S/J")
  regress=$(wc -l < "$S/regress" | tr -d ' ')
  repaired=$(wc -l < "$S/repaired" | tr -d ' ')
  meta=$(awk -F'\t' '$6==$11 && ($2!=$7 || $4!=$9) {c++} END {print c+0}' "$S/J")
  onlynew=$(join -t "$(printf '\t')" -j1 -v2 "$S/L-base" "$S/L-new" | wc -l | tr -d ' ')

  # Same partition for the ids that stopped binding: one that had been bound to
  # the wrong place and is now refused is an improvement, not a loss.
  : > "$S/lost"
  join -t "$(printf '\t')" -j1 -v1 "$S/L-base" "$S/L-new" | while IFS="$(printf '\t')" read -r id kb fb sb lb hb; do
    [ -n "$id" ] || continue
    f=$S/t-base-$name/$fb
    [ -f "$f" ] || continue
    dl=$(grep -n "ds:def id=$id\$" "$f" 2>/dev/null | head -1 | cut -d: -f1)
    [ -n "$dl" ] || continue
    want=$(awk -v s="$dl" 'NR>s { x=$0; gsub(/^[ \t]+/,"",x); if (x=="" || x ~ /^\/\//) next; print NR; exit }' "$f")
    # An id that stopped binding is only a loss if what it named is not now
    # bound under another id: the anchoring inserts one directive per candidate
    # line, so a construct the old build reached from the wrong directive is
    # still reachable from the right one.
    if [ "${lb%%-*}" = "$want" ] && ! awk -F'\t' -v s="$sb" '$4==s {found=1} END {exit !found}' "$S/L-new"; then
      printf '%s\t%s\t%s\t%s\n' "$id" "$fb" "$lb" "$sb" >> "$S/lost"
    fi
  done
  lost=$(wc -l < "$S/lost" | tr -d ' ')
  refused=$(( $(join -t "$(printf '\t')" -j1 -v1 "$S/L-base" "$S/L-new" | wc -l | tr -d ' ') - lost ))

  echo "  $name:"
  echo "    $same blocks bind to exactly the same bytes"
  echo "    $repaired were bound to the wrong place before and are now correct"
  echo "    $refused were bound to the wrong place before and are now refused"
  echo "    $onlynew newly bindable, $meta kind or symbol only"
  if [ "$regress" -gt 0 ]; then
    echo "    REGRESSION: $regress name the same thing over the same lines and hash differently:"
    awk -F'\t' '{print "      " $1 "  " $2 ":" $4 "  " $3 "  hash " $5 " -> " $6}' "$S/regress" | head -10
    bad=$((bad+regress))
  else
    echo "    no block that was bound correctly before has moved"
  fi
  if [ "$lost" -gt 0 ]; then
    echo "    REGRESSION: $lost were bound correctly before and no longer bind:"
    awk -F'\t' '{print "      " $1 "  " $2 ":" $3 "  " $4}' "$S/lost" | head -10
    bad=$((bad+lost))
  fi
  total_gone=$((total_gone+lost)); moved=$regress
  total_same=$((total_same+same)); total_moved=$((total_moved+moved))
  total_new=$((total_new+onlynew)); total_meta=$((total_meta+meta))
  total_repaired=$((total_repaired+repaired))
done

if [ "$used" -eq 0 ]; then
  echo "  SKIP  no corpus repository found (set CORPUS=/path/one:/path/two)"
  exit 0
fi
echo
echo "  totals: $total_same identical, $total_repaired repaired, $total_new newly bindable, $total_meta kind/symbol only"
if [ "$bad" -gt 0 ]; then
  echo "  ---- FAIL: $bad blocks that were bound correctly have moved; acked citations of them would report drift ----"
  exit 1
fi
echo "  ---- OK: every block that was bound correctly still binds to the same bytes ----"
