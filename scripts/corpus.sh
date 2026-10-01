#!/bin/sh
# Run ds over real third-party source and assert only invariants -- properties
# that hold whatever the code says, so there are no expected values for the
# author of this script to get wrong.
#
# Why it exists: every other gate here tests docsync against code shaped like
# the code in this repository. 245 e2e cases, five modules at 100% statements,
# fuzzing and dogfooding all share one oracle, which is the person who wrote
# them, and that oracle writes Go a particular way. Bug 18 was a constant
# declared inside `const ( … )`: 18 such groups exist across all of docsync's
# fixtures, and 211 in the single repository that reported the bug. This gate
# has no oracle of intent, so what it finds is what a stranger's tree finds.
#
# CORPUS is a colon-separated list of directories, default the sibling repos
# that exist. CORPUS_FILES and CORPUS_LINES bound the work per repository: the
# defs cost about 45ms each and the point is a gate that runs, not a soak. Each is copied to a scratch tree first: nothing here writes to a
# repository it did not create.
#
# Run with `task corpus`. It is not part of `task` because the corpus is not
# vendored; it prints SKIP and succeeds when none of the paths exist.
# No `set -e` past setup: an invariant that returns non-zero is a counted
# failure, not a reason to abandon the run. An earlier version had it on, and a
# `grep -c` with no matches killed the script after three passes with no tally
# printed -- a run that looked like a pass because it ended quietly.
DS=${DS:-./bin/ds}
[ -x "$DS" ] || { echo "corpus: $DS is not built; run task cli:build"; exit 1; }
DS=$(cd "$(dirname "$DS")" && pwd)/$(basename "$DS")

# Refuse to run against a binary older than the source. This gate is the one
# that must not lie, and a stale build made it report failures that had been
# fixed minutes earlier -- the run looked like evidence and was not. `task
# corpus` rebuilds first; running the script by hand does not.
root=$(cd "$(dirname "$0")/.." && pwd)
newest=$(find "$root" -name '*.go' -newer "$DS" -not -path '*/testdata/*' -print 2>/dev/null | head -1)
if [ -n "$newest" ]; then
  echo "corpus: $DS is older than $newest; run task cli:build first"
  exit 1
fi

# The defaults are the repositories on the author's machine, by relative path;
# any that are absent are skipped. They span the languages with a syntax tier
# and are all written by someone else: ubgo/auth and 99designs/gqlgen (Go),
# Aider-AI/aider (Python), vercel/ai-chatbot and stackblitz/bolt.new
# (TypeScript and TSX). One corpus in one language is how bug 18 got through: the
# oracle for every other gate writes Go one way.
# Private repositories to include on this machine go in scripts/corpus.local
# (gitignored; one path per line, relative to the repository root), so their
# names never enter this public tree.
CORPUS=${CORPUS:-$(sed -n 's/^\([^#].*\)$/\1:/p' "$(cd "$(dirname "$0")/.." && pwd)/scripts/corpus.local" 2>/dev/null | tr -d '\n')../../ubgo/auth:../../../khanakia/ubgo/auth:../../../../go1/gqlgen:../../../../ai/aider:../../../../ai/vercel-ai-chatbot1:../../../../ai/bolt.new}
S=${CORPUS_KEEP:-$(mktemp -d)}
mkdir -p "$S"
# balance.awk prints closers minus openers over its input, with quoted
# strings removed first so a bracket in a literal is not counted.
cat > "$S/balance.awk" <<'AWK'
{ t = $0
  gsub(/"([^"\\]|\\.)*"/, "", t)
  gsub(/'([^'\\]|\\.)*'/, "", t)
  gsub(/`[^`]*`/, "", t)
  o += gsub(/[{(]/, "", t); c += gsub(/[})]/, "", t) }
END { print c - o }
AWK
done_ok=0
# The tally prints from the trap, so a run that dies early still says so
# instead of ending quietly after a few passes.
finish() {
  [ -n "$CORPUS_KEEP" ] || /bin/rm -rf "$S"
  if [ "$done_ok" -eq 0 ]; then
    echo; echo "  ---- $pass passed, $((fail+1)) failed (the run ended early) ----"
  fi
}
trap finish EXIT
pass=0; fail=0; used=0
ok() { echo "  PASS  $1"; pass=$((pass+1)); }
no() { echo "  FAIL  $1"; fail=$((fail+1)); }

# invariants runs one tree and checks the properties that must hold for any
# source whatsoever.
invariants() {
  name=$1; dir=$2
  W="$S/$(echo "$name" | tr / _)"
  mkdir -p "$W"
  # A copy, so a corpus repository is never written to. Its own .ds/ is
  # dropped: this gate scans from scratch rather than trusting a baseline.
  # The copy is of the last commit, not the working tree: a corpus
  # repository is someone's live project, and their half-finished edits
  # (a docs move in progress, copies not yet refreshed) failed this gate for
  # reasons that had nothing to do with docsync.
  git -C "$dir" archive --format=tar HEAD | (cd "$W" && tar xf -)
  /bin/rm -rf "$W/.ds"
  cd "$W" || return 1
  git init -q .; git config user.email t@t; git config user.name t
  # Committed before anything is anchored: git ls-files below needs an index,
  # and check needs a commit to measure against. Forgetting this made an
  # earlier version of this gate scan an empty file list and pass on nothing.
  git add -A >/dev/null 2>&1; git commit -qm corpus >/dev/null 2>&1 || true
  "$DS" init >/dev/null 2>&1 || true

  # Anchor by LINE, not by symbol: `ds def <file>:<line>` needs no list of
  # names, so nothing here encodes what the author of this script expects the
  # corpus to contain. The candidates are top-level declarations and indented
  # group entries.
  #
  # The keyword filter is not cosmetic. Without it the pattern matched
  # `case err == nil:` as an entry named `case`, and the tool then correctly
  # reported that it could not bind a switch clause -- a failure of this gate,
  # not of docsync. An anchor point this script picks has to be one a person
  # could reasonably pick.
  #
  # Descending line order per file: each def inserts a directive line, which
  # would shift every target below it in the same file.
  # Per language, the lines a person would put a def above. Go: top-level
  # declarations, group entries, struct fields, interface methods, imports.
  # TypeScript and JavaScript: top-level declarations (exported or not) and the
  # members of an interface, type literal or class. Python: functions,
  # classes, methods, and module constants.
  for f in $(git ls-files '*.go' '*.ts' '*.tsx' '*.py' | grep -v '\.d\.ts$' | head -"${CORPUS_FILES:-30}"); do
    case $f in
      *.go) pat='^(func|type|const|var) |^\t[A-Za-z_][A-Za-z0-9_]*( +[A-Za-z0-9_.*\[\]]+)? *=|^\t[A-Za-z_][A-Za-z0-9_]* +[A-Za-z*\[]|^\t[A-Za-z_][A-Za-z0-9_]*\(|^\t"[^"]+"$'
            skip='case|default|return|if|else|for|switch|select|go|defer|range|break|continue|fallthrough|panic' ;;
      *.ts|*.tsx) pat='^(export +)?(default +)?(declare +)?(async +)?(function|class|interface|type|enum|const|let|abstract class) +[A-Za-z_$]|^  (readonly +|public +|private +|protected +|static +)*[A-Za-z_$][A-Za-z0-9_$]*\??: [^=]*;?$|^  (async +)?[A-Za-z_$][A-Za-z0-9_$]*\([^)]*\)[^;]*\{$'
            skip='case|default|return|if|else|for|while|switch|break|continue|throw|try|catch|await|yield|new|super' ;;
      *.py) pat='^(async +)?(def|class) +[A-Za-z_]|^    (async +)?def +[A-Za-z_]|^[A-Z][A-Z0-9_]* *='
            skip='return|if|elif|else|for|while|try|except|with|raise|import|from|pass' ;;
    esac
    grep -nE "$pat" "$f" 2>/dev/null \
      | grep -vE ":\s*($skip)\b" \
      | cut -d: -f1 | sort -rn | head -"${CORPUS_LINES:-8}" > "$S/lines.txt" || true
    while read -r ln; do
      [ -n "$ln" ] || continue
      "$DS" def "$f:$ln" >/dev/null 2>&1 || true
    done < "$S/lines.txt"
  done

  out=$("$DS" scan 2>&1) || true
  echo "        $name: $(echo "$out" | head -1)"

  # promise:def-binds-below
  # 1. Every def must start on the first line below its directive that is not
  #    blank and not a comment. This is the invariant bug 18 violated, recomputed
  #    here from the files themselves rather than read off the tool's own
  #    report, so it still holds if the in-tool guard is removed or wrong.
  #    Trailing directives (code and directive on one line) and remote defs
  #    (no extent) are exempt because neither binds a line below anything.
  off=0; checkedb=0
  awk -F'\t' 'NR>2 && $1 != "" {print $1"\t"$4"\t"$6}' .ds/ledger.tsv > "$S/binds.txt"
  while IFS="$(printf '\t')" read -r id file range; do
    [ -n "$file" ] && [ -f "$file" ] || continue
    dl=$(grep -n "ds:def id=$id\b" "$file" 2>/dev/null | head -1 | cut -d: -f1)
    [ -n "$dl" ] || continue
    # A trailing directive shares its line with the code it binds. The line
    # comment is `#` in Python and `//` elsewhere in this corpus.
    case $file in *.py) lc='#';; *) lc='//';; esac
    [ "$(sed -n "${dl}p" "$file" | awk -v c="$lc" '{i=index($0,c); if (i) $0=substr($0,1,i-1); print}' | tr -d ' \t')" = "" ] || continue
    checkedb=$((checkedb+1))
    # The first line below that is not blank, a line comment, a doc-comment
    # line (`/**`, ` *`, ` */`), or a decorator (`@` in Python and TypeScript).
    want=$(awk -v s="$dl" -v c="$lc" 'NR>s { t=$0; gsub(/^[ \t]+/,"",t); if (t=="" || index(t,c)==1 || t ~ /^\/\*/ || t ~ /^\*/ || t ~ /^@/) next; print NR; exit }' "$file")
    got=${range%%-*}
    if [ -n "$want" ] && [ "$want" != "$got" ]; then
      off=$((off+1)); [ "$off" -le 3 ] && echo "          $id in $file: directive on $dl, first code line $want, bound $got"
    fi
  done < "$S/binds.txt"
  if [ "$checkedb" -lt 20 ]; then
    no "$name: only $checkedb defs to check; this gate is not exercising binding"
  elif [ "$off" -eq 0 ]; then ok "$name: all $checkedb defs start on the first code line below their directive"
  else no "$name: $off of $checkedb defs bound something other than the line below them"; fi

  # 1b. And the tool must never bind past an unbindable construct in silence:
  #     with the net above, anything it cannot bind is a real anchor point.
  if echo "$out" | grep -q "cannot bind the declaration below it"; then
    no "$name: a def at a real anchor point could not be bound"
    echo "$out" | grep "cannot bind" | head -3 | sed 's/^/          /'
  else ok "$name: every anchor point bound"; fi

  # 2. No two ids on one block, computed from the ledger rather than from the
  #    tool's own finding. Reading the report would only confirm that the tool
  #    agrees with itself: run against a binary with no such check this passed
  #    while ten defs were in fact stacked on one block.
  dupes=$(awk -F'\t' 'NR>2 && $1 != "" && $6 != "" {k=$4":"$6"\x00"$10; if (k in seen) {print k} seen[k]=1}' .ds/ledger.tsv | sort -u | wc -l | tr -d ' ')
  if [ "$dupes" -eq 0 ]; then ok "$name: no two ids share a block"
  else
    no "$name: $dupes blocks carry more than one id"
    awk -F'\t' 'NR>2 && $1 != "" && $6 != "" {print $4":"$6"\t"$1}' .ds/ledger.tsv | sort | awk -F'\t' '{if ($1==p) print "          "$1" also has "$2; p=$1}' | head -3
  fi

  # 3. No block may swallow the closing brace or paren of whatever holds it.
  #    A member whose extent reaches the holder's `}` or `)` -- the bug 18 extent
  #    bug, in a group or in a body -- carries one more closer than opener.
  #    A block that ends on its own brace or paren is balanced, whatever line
  #    opened it: a multi-line TypeScript signature, a Python `return (...)`, a
  #    Go `const (` group. Brackets inside quotes on a line are ignored, which
  #    is approximate: a string or comment holding an unmatched bracket can
  #    only make a block look swallowed, never hide one that is.
  #    An earlier form of this check asked whether the first line ended in `{`
  #    or opened `const (`, which is Go's shape; it failed every multi-line
  #    TypeScript signature and every Python function ending in a call.
  if [ -f .ds/ledger.tsv ]; then
    bad=0; checkedx=0
    awk -F'\t' 'NR>2 && $6 ~ /-/ {print $4"\t"$6}' .ds/ledger.tsv > "$S/ext.txt"
    while IFS="$(printf '\t')" read -r file range; do
      [ -n "$file" ] && [ -f "$file" ] || continue
      # Code only: in prose a bracket need not close ("1)", a quoted
      # fragment), so a Markdown section proves nothing either way.
      case $file in *.md|*.mdx|*.markdown|*.rst|*.adoc|*.txt|*.html) continue;; esac
      checkedx=$((checkedx+1))
      first=${range%%-*}; last=${range##*-}
      extra=$(sed -n "${first},${last}p" "$file" 2>/dev/null | awk -f "$S/balance.awk")
      if [ "${extra:-0}" -gt 0 ]; then
        bad=$((bad+1)); [ "$bad" -le 3 ] && echo "          $file:$range closes $extra more bracket(s) than it opens"
      fi
    done < "$S/ext.txt"
    if [ "$bad" -eq 0 ]; then ok "$name: none of $checkedx multi-line blocks swallows the bracket that closes its holder"
    else no "$name: $bad blocks close more brackets than they open"; fi
  fi

  # 4. Every id the ledger recorded must locate back to the range the ledger
  #    holds. `locate` re-resolves through a different path, so a binding that
  #    is not reproducible -- the mis-binding of bug 18 in another form -- shows up
  #    as a disagreement rather than as a plausible-looking answer.
  mism=0; checked=0
  awk -F'\t' 'NR>2 && $1 != "" {print $1"\t"$4"\t"$6}' .ds/ledger.tsv > "$S/syms.txt"
  while IFS="$(printf '\t')" read -r id file range; do
    [ -n "$id" ] || continue
    checked=$((checked+1))
    got=$("$DS" locate "$id" 2>/dev/null | head -1)
    case $range in
      *-*) want="$file:$range";;
      *)   want="$file:$range-$range";;
    esac
    case $got in
      "$want"*) ;;
      *) mism=$((mism+1)); [ "$mism" -le 3 ] && echo "          $id: ledger $want, locate ${got:-none}";;
    esac
  done < "$S/syms.txt"
  if [ "$checked" -lt 20 ]; then
    no "$name: only $checked defs anchored; this gate is not exercising binding"
  elif [ "$mism" -eq 0 ]; then ok "$name: all $checked recorded ids locate back to their own block"
  else no "$name: $mism of $checked ids locate elsewhere"; fi

  # 5. Scanning twice must produce the same ledger: a scan is a function of the
  #    tree, and anything else means hidden state or a cache serving a stale
  #    answer, which is how the extraction-cache bugs presented.
  cp .ds/ledger.tsv "$S/first.tsv"
  "$DS" scan >/dev/null 2>&1 || true
  if [ "$(tail -n +2 "$S/first.tsv" | cksum)" = "$(tail -n +2 .ds/ledger.tsv | cksum)" ]; then
    ok "$name: a second scan of the same tree gives the same ledger"
  else no "$name: the ledger changed on a second scan of unchanged source"; fi

  # 6. check must be clean on a tree nothing but this gate has edited, and
  #    must not crash. The anchors above are edits: one inserted inside a
  #    block a repo-mode page copies moves that block's lines, so the copy
  #    is rightly `stale` (refresh would rewrite it). That is the one error
  #    the anchoring can cause, so exactly that finding -- stale, with only
  #    its lines or directives out of date -- is set aside, and any other
  #    error, a stale copy whose code changed included, still fails.
  errs=$("$DS" check --full --json 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
print(sum(1 for f in d.get("findings",[]) if f.get("severity")=="error" and not (f.get("state")=="stale" and f.get("message","").startswith("copy shows the block"))))' 2>/dev/null || echo crash)
  if [ "$errs" = "0" ]; then ok "$name: check is clean on an unedited tree (apart from copies the anchors made stale)"
  else
    # A finding is only acceptable if it is not an error-severity surprise.
    st=$("$DS" check --full 2>&1 | tail -1)
    no "$name: check on an unedited tree reports $st"
  fi
  used=$((used+1))
}

here=$(cd "$(dirname "$0")/.." && pwd)
: > "$S/seen_paths"
for p in $(echo "$CORPUS" | tr ':' ' '); do
  case $p in /*) d=$p;; *) d=$here/$p;; esac
  [ -d "$d/.git" ] || continue
  # Resolved, because the default list names the same tree by two relative
  # paths and running it twice doubles the tally without testing anything more.
  real=$(cd "$d" && pwd -P)
  grep -qxF "$real" "$S/seen_paths" 2>/dev/null && continue
  echo "$real" >> "$S/seen_paths"
  invariants "$(basename "$real")" "$real"
  cd "$here"
done

if [ "$used" -eq 0 ]; then
  done_ok=1
  echo "  SKIP  no corpus repository found (set CORPUS=/path/one:/path/two)"
  echo; echo "  ---- 0 passed, 0 failed ----"
  exit 0
fi
done_ok=1
echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
