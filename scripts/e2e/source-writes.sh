#!/bin/sh
# ds writes into source files — def inserts a directive, adopt rewrites a
# link, undo reverses both — and each write must touch its one line and no
# other byte, whatever the file's shape: LF, CRLF, no final newline, a byte
# order mark. Before, a CRLF file came back with every line ending rewritten
# and undo could not restore its bytes.
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
setup() { n=$((n+1)); W="$S/c$n"; mkdir -p "$W/docs"; cd "$W" || exit 1; git init -q .; git config user.email t@t; git config user.name t; }
for shape in lf crlf noeol crlfnoeol bom; do
  setup
  case $shape in
    lf)        printf 'package p\n\nfunc Save() int {\n\treturn 1\n}\n' > a.go;;
    crlf)      printf 'package p\r\n\r\nfunc Save() int {\r\n\treturn 1\r\n}\r\n' > a.go;;
    noeol)     printf 'package p\n\nfunc Save() int {\n\treturn 1\n}' > a.go;;
    crlfnoeol) printf 'package p\r\n\r\nfunc Save() int {\r\n\treturn 1\r\n}' > a.go;;
    bom)       printf '\357\273\277package p\n\nfunc Save() int {\n\treturn 1\n}\n' > a.go;;
  esac
  cp a.go "$S/orig"; ds init >/dev/null
  ds def a.go#Save >/dev/null 2>&1
  if grep -q 'ds:def' a.go; then ok "$shape: def writes the directive"; else no "$shape: def wrote nothing"; fi
  # Both sides through the same grep: it ends an unterminated last line with
  # a newline, so comparing against the raw original would always differ.
  if [ "$(grep -v 'ds:def' a.go | cksum)" = "$(grep -v 'ds:def' "$S/orig" | cksum)" ]; then ok "$shape: def touches no other byte"; else no "$shape: def changed other lines"; fi
  case $shape in crlf*) if [ "$(grep -U 'ds:def' a.go | tr -cd '\r' | wc -c | tr -d ' ')" = 1 ]; then ok "$shape: the directive line ends in CRLF like its neighbours"; else no "$shape: the directive line ends in LF"; fi;; esac
  ds undo >/dev/null 2>&1
  if cmp -s a.go "$S/orig"; then ok "$shape: undo restores every byte"; else no "$shape: undo did not restore the file"; fi
done

setup
printf 'package p\r\n\r\nfunc Save() int {\r\n\treturn 1\r\n}\r\n' > a.go
printf '# D\r\n\r\nSaving is [here](a.go#L3-L5).\r\n' > docs/d.md
cp a.go "$S/a.orig"; cp docs/d.md "$S/d.orig"; ds init >/dev/null; git add -A; git commit -qm b
ds adopt >/dev/null 2>&1
if grep -q 'ds:block?id=' docs/d.md && [ "$(grep -U 'ds:block?id=' docs/d.md | tr -cd '\r' | wc -c | tr -d ' ')" = 1 ]; then ok "adopt on a CRLF doc keeps the line's CRLF"; else no "adopt on a CRLF doc lost its CRLF"; fi
ds scan >/dev/null; ds undo >/dev/null 2>&1
if cmp -s a.go "$S/a.orig" && cmp -s docs/d.md "$S/d.orig"; then ok "undo after adopt restores both CRLF files byte for byte"; else no "undo after adopt did not restore the CRLF files"; fi

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
