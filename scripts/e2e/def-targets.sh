#!/bin/sh
# What `ds def` accepts as a target and writes, and how the directive grammar
# reads it back, driven through the built binary: one case per bug in that
# area (bugs 40-59). These span commands -- def, then scan, check or render --
# and a real parser where one exists, so a unit test of either half alone
# would not show them.
#
# Run through scripts/e2e/run.sh, which puts the binary built from this tree
# first on PATH.
S=$(mktemp -d)
command -v cygpath >/dev/null 2>&1 && S=$(cygpath -m "$S")
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0; n=0
ok() { echo "  PASS  $1"; pass=$((pass+1)); }
no() { echo "  FAIL  $1"; fail=$((fail+1)); }
sk() { echo "  SKIP  $1"; }
setup() { n=$((n+1)); W="$S/d$n"; mkdir -p "$W"; cd "$W" || exit 1; git init -q .; git config user.email t@t; git config user.name t; ds init >/dev/null; }

# --- bug 40: a key-value format's value must survive ds def ---
setup
printf 'db.host=localhost\ndb.port=5432\n' > app.properties
printf '[server]\nport = 8080\n' > app.ini
ds def 'app.properties#db.port' >/dev/null 2>&1 && ds def app.ini#server.port >/dev/null 2>&1
if grep -q '^db.port=5432$' app.properties && grep -q '^port = 8080$' app.ini; then ok "properties and INI: the key line is untouched (bug 40)"; else no "a key line was rewritten: $(cat app.properties app.ini)"; fi
if command -v python3 >/dev/null 2>&1; then
  v=$(python3 -c 'import configparser;c=configparser.ConfigParser();c.read("app.ini");print(c["server"]["port"])')
  if [ "$v" = 8080 ]; then ok "configparser still reads port = 8080"; else no "configparser reads port as: $v"; fi
else sk "python3 is not installed"; fi
if [ "$(ds scan 2>&1 | sed -E 's/.*, ([0-9]+) problems.*/\1/')" = 0 ] && ds find --file app.ini | grep -q server-port; then ok "the directive above the key binds it"; else no "scan after def: $(ds scan 2>&1)"; fi

# --- bugs 41, 42, 48: names the scan records are names ds def finds ---
setup
printf 'class Config:\n    retries = 3\n' > a.py
printf 'export default {\n  server: {\n    port: 5173,\n  },\n};\n' > b.ts
printf 'variable "region" {\n  default = "us-east-1"\n}\n' > c.tf
printf '#!/bin/sh\ndeploy() {\n  echo hi\n}\n' > e.sh
printf 'package f\n\ntype Limits struct {\n\tMinLength int\n}\n' > f.go
for t in a.py#Config.retries b.ts#server.port c.tf#variable.region e.sh#deploy f.go#Limits.MinLength; do
  if ds def "$t" >/dev/null 2>&1; then ok "ds def $t (bugs 41, 42 and 48)"; else no "ds def $t: $(ds def "$t" 2>&1)"; fi
done
if ds find deploy | grep -q 'e.sh'; then ok "a shell function is recorded under its name (bug 42)"; else no "shell function symbol: $(ds find --file e.sh)"; fi

# --- bug 43: a range is a range ---
setup
printf 'one\ntwo\nthree\nfour\n' > n.txt
ds def n.txt:1-2 >/dev/null 2>&1
if head -1 n.txt | grep -q 'span=+2' && ds scan >/dev/null 2>&1 && ds find --file n.txt | grep -q 'n.txt:2-3'; then ok "n.txt:1-2 binds exactly those lines (bug 43)"; else no "range: $(cat n.txt; ds find --file n.txt)"; fi

# --- bug 44: render strips a plain-text directive ---
setup
printf 'ds:def id=rota-m3k9v2pq\nWeek 37 a\n' > rota.txt
if ds render rota.txt | grep -q 'ds:def'; then no "render left the directive in: $(ds render rota.txt)"; else ok "render strips the bare directive line (bug 44)"; fi

# --- bugs 45 and 53: remote JSON defs on one line ---
setup
mkdir -p docs
printf '{\n  "a": 1,\n  "port": 8081, "host": "x"\n}\n' > app.json
printf '<!-- ds:def id=port-m3k9v2pd file=app.json pick=json:$.port -->\n<!-- ds:def id=host-m3k9v2pe file=app.json pick=json:$.host -->\n' > docs/r.md
out=$(ds scan 2>&1)
if echo "$out" | grep -q ' 0 problems'; then ok "two picks on one JSON line are two blocks (bug 53)"; else no "remote picks: $out"; fi
if ds locate port-m3k9v2pd 2>&1 | grep -q 'app.json:3'; then ok "a JSON pick is recorded at its own line (bug 45)"; else no "json position: $(ds locate port-m3k9v2pd 2>&1)"; fi

# --- bug 46: .mts .cts .cjs .pyi .tfvars take directives ---
setup
printf 'export const a = 1;\n' > i.mts
printf 'module.exports = 1;\n' > j.cjs
printf 'X: int\n' > k.pyi
printf 'x = 1\n' > h.tfvars
for f in i.mts j.cjs k.pyi h.tfvars; do
  if ds def "$f:1" >/dev/null 2>&1; then ok "ds def $f:1 (bug 46)"; else no "ds def $f:1: $(ds def "$f:1" 2>&1)"; fi
done
if [ "$(ds scan 2>&1 | sed -E 's/.* ([0-9]+) defs.*/\1/')" = 4 ]; then ok "a scan reads every one of them back"; else no "scan: $(ds scan 2>&1)"; fi

# --- bugs 50, 51, 52, 54, 55, 59: the directive grammar, through check ---
setup
mkdir -p docs
cat > docs/a.md <<'EOF'
<!-- ds:def id=policy-h2n8wq4t
     owner=@auth -->
## Policy
Sessions live thirty days.

Port [8081](ds:def?id=api-port-h3v8n2wd&type=int) and version [2.14.0](ds:def?id=api-version-c8t2m6qp&type=semver).
Bad [abc](ds:def?id=bad-port-k7m2p4xq&type=int).
<!-- ds:def id=Foo_Bar -->
Para.

Users [1.2M](ds:cfg?query="sql:select count(*) from users").
See [p](ds:block?id=policy-h2n8wq4t) and [v](ds:cfg?id=api-version-c8t2m6qp) and [x](ds:cfg?id=api-port-h3v8n2wd).
EOF
out=$(ds scan 2>&1)
if ds find --file docs/a.md | grep -q 'policy-h2n8wq4t'; then ok "a multi-line comment directive is read (bug 50)"; else no "multi-line directive dropped: $out"; fi
if echo "$out" | grep -q 'docs/a.md:11.*not a markdown link'; then ok "a link with a space is reported, not dropped (bug 51)"; else no "spaced link: $out"; fi
if echo "$out" | grep -q 'bad-port-k7m2p4xq.*not of type int'; then ok "type= is checked (bug 54)"; else no "type check: $out"; fi
if echo "$out" | grep -q 'malformed id: "Foo_Bar"'; then ok "a malformed id is reported (bug 55)"; else no "id check: $out"; fi
if echo "$out" | grep -q 'same block'; then no "two facts on one line collided (bug 59): $out"; else ok "two facts on one line are two blocks (bug 59)"; fi
if ds render docs/a.md 2>/dev/null | grep -q 'owner=@auth'; then no "render left the multi-line directive in"; else ok "render removes the multi-line def comment"; fi

# --- bug 52: a quoted link title is the title ---
setup
mkdir -p docs
printf 'See [pg](ds:url?href=https://example.com/a&title="TOAST").\n' > docs/u.md
ds scan >/dev/null 2>&1
if grep -q 'title=TOAST' .ds/refs.tsv && ! grep -q 'title="TOAST"' .ds/refs.tsv; then ok "the quotes are removed from title= (bug 52)"; else no "refs: $(cat .ds/refs.tsv)"; fi

# --- bug 57: a dotfile labels itself ---
setup
printf 'node_modules\n' > .gitignore
if ds def .gitignore:1 2>&1 | grep -q '^gitignore-'; then ok ".gitignore:1 mints gitignore-… (bug 57)"; else no ".gitignore: $(ds def .gitignore:1 2>&1)"; fi

# --- bug 58: a directive line names its def; crossing is refused and reported ---
setup
printf 'ds:def id=rota-k7m2p4xq span=+2\nWeek 37\nWeek 38\nWeek 39\n' > n.txt
if [ "$(ds def n.txt:1 2>&1)" = rota-k7m2p4xq ] && [ "$(grep -c 'ds:def' n.txt)" = 1 ]; then ok "def on the directive line returns its id (bug 58)"; else no "directive line: $(cat n.txt)"; fi
if ds def n.txt:3 >/dev/null 2>&1; then no "a crossing def was written: $(cat n.txt)"; else ok "a def that would cross another is refused"; fi
printf 'ds:def id=rota-k7m2p4xq span=+2\nWeek 37\nds:def id=late-k7m2p4xr\nWeek 38\nWeek 39\n' > n.txt
if ds scan 2>&1 | grep -q 'overlap without one containing'; then ok "crossing blocks are reported by scan"; else no "crossing not reported: $(ds scan 2>&1)"; fi

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
