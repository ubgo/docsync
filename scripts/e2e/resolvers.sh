#!/bin/sh
# `ds check --resolve` and `--run` against the shipped plugins and the real
# binary, with stand-in provider CLIs (`op`, `vault`) first on PATH. Each case
# is one of bugs 80 to 87, all of which passed every unit test because each
# side was tested against a double of the other:
#   bug 80  op:// addresses ran ds-resolve-1password; the shipped plugin is
#           ds-resolve-onepassword, so 1Password never resolved.
#   bug 81  ds-resolve-vault passed the vault: prefix on to `vault kv get`.
#   bug 82  ds-resolve-onepassword called every `op read` failure "does not
#           exist", so a signed-out op was `resolve failed`.
#   bug 83  resolve.enabled was ignored; no ds-resolve-env existed.
#   bug 85  a local=true def was unverifiable even beside its file.
#   bug 86  ds:run timeout= was ignored; a substring passed a failed command.
#   bug 87  a ds:url with no network was `dead` and cached as dead.
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
# one prints output on a single line, so a failure shows all of it where
# run.sh reports only the FAIL line (as on a CI runner).
one() { printf '%s' "$1" | tr '\n' ' ' | cut -c1-1500; }
ck() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $(one "$3"))"; fail=$((fail+1));; esac; }
nk() { case "$3" in *"$2"*) echo "  FAIL  $1 (did not want $2, got: $(one "$3"))"; fail=$((fail+1));; *) echo "  PASS  $1"; pass=$((pass+1));; esac; }

# Stand-in provider CLIs. `op read --no-newline <ref>` prints the value held
# for <ref> in $S/secrets, or fails the way op does: signed out when
# $S/signed-out exists, otherwise "isn't an item". `vault kv get -field=F P`
# records its arguments and prints the value held for P.
B="$S/bin"; mkdir -p "$B"
cat > "$B/op" <<EOF
#!/bin/sh
if [ -f "$S/signed-out" ]; then echo '[ERROR] You are not currently signed in.' >&2; exit 1; fi
v=\$(awk -v a="\$3" '\$1 == a { print \$2 }' "$S/secrets")
if [ -z "\$v" ]; then echo "[ERROR] \"\$3\" isn't an item in the vault" >&2; exit 1; fi
printf '%s' "\$v"
EOF
cat > "$B/vault" <<EOF
#!/bin/sh
echo "\$@" >> "$S/vault-args"
v=\$(awk -v a="\$4" '\$1 == a { print \$2 }' "$S/secrets")
if [ -z "\$v" ]; then echo 'No value found at that path' >&2; exit 2; fi
printf '%s' "\$v"
EOF
chmod +x "$B/op" "$B/vault"
# On Windows the plugins are native programs and start a CLI through PATHEXT,
# which finds a .bat but never a #! script; a one-line wrapper hands each
# stand-in to Git Bash's sh, so the same stand-ins run on every platform.
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) for c in op vault; do printf '@sh "%%~dp0%s" %%*\r\n' "$c" > "$B/$c.bat"; done ;;
esac
# PATH is split on ':' in Git Bash, so a C:/ path there breaks in two; it
# takes the shell's own /c/ spelling of the directory.
PB=$B; command -v cygpath >/dev/null 2>&1 && PB=$(cygpath -u "$B")
PATH="$PB:$PATH"; export PATH
printf 'op://Platform/stripe/credential sk_live_one\nsecret/stripe sk_live_one\n' > "$S/secrets"

W="$S/w"; mkdir -p "$W/docs" "$W/runbooks"; cd "$W" || exit 1
git init -q .; git config user.email t@t; git config user.name t
printf 'STRIPE_KEY=op://Platform/stripe/credential   # ds:def id=op-stripe-p9c2v7ld secret=true truth=true\nVAULT_KEY=vault:secret/stripe   # ds:def id=vault-stripe-b3c7g9kl secret=true from=op-stripe-p9c2v7ld\n' > secrets.env
printf 'package pay\n\nimport "os"\n\nfunc key() string {\n\t// ds:def id=app-stripe-m4w8k2qn secret=true source=env from=op-stripe-p9c2v7ld pick=regex:\047"(\\w+)"\047\n\treturn os.Getenv("DS_E2E_STRIPE_KEY")\n}\n' > pay.go
printf '# Pay\n\nStripe: <!-- ds:chain id=app-stripe-m4w8k2qn -->\n\n[v](ds:cfg?id=vault-stripe-b3c7g9kl)\n\n<!-- ds:url href=http://127.0.0.1:1/gone -->\n\n<!-- ds:def id=prod-env-x4y5z6a7 file=.env.prod local=true pick=env:STRIPE_KEY -->\n' > docs/pay.md
ds init >/dev/null
printf '\n[secret]\npaths = []\n' >> .ds/config.toml
ds scan >/dev/null; git add -A; git commit -qm a

# bug 83: --resolve without resolve.enabled contacts nothing and says so.
out=$(ds check --resolve 2>&1)
ck "without resolve.enabled, --resolve says it contacted nothing" "resolve.enabled is false" "$out"
nk "and no plugin was asked" "not reachable" "$out"
nk "and no link was fetched" "dead" "$out"
printf '\n[resolve]\nenabled = true\nstore_hash = true\n' >> .ds/config.toml

# bugs 80, 81, 83: with the variable set to the truth's value every hop is
# asked through the shipped plugin and agrees.
out=$(DS_E2E_STRIPE_KEY=sk_live_one ds check --resolve 2>&1)
nk "op:// resolves through ds-resolve-onepassword (bug 80)" "ds-resolve-1password" "$out"
nk "the 1Password truth is reachable" "provider 1password not reachable" "$out"
nk "a vault: address reaches the CLI without its prefix (bug 81)" "provider vault not reachable" "$out"
ck "vault kv get is given the bare path" "-field=value secret/stripe" "$(cat "$S/vault-args" 2>/dev/null)"
nk "a source=env hop is checked by ds-resolve-env (bug 83)" "provider env not reachable" "$out"
nk "and agrees with its truth" "out of sync" "$out"
out=$(DS_E2E_STRIPE_KEY=sk_live_other ds check --resolve 2>&1)
ck "an environment variable that differs from its truth is out of sync" "app-stripe-m4w8k2qn differs from truth" "$out"
out=$(ds check --resolve 2>&1)
ck "an unset variable is unverifiable, not resolve failed" "DS_E2E_STRIPE_KEY is not set" "$out"
ck "and its fix is about the plugin, not links" "install the ds-resolve plugin for env" "$out"

# bug 82: a signed-out op is unverifiable; a missing item is resolve failed.
touch "$S/signed-out"
out=$(DS_E2E_STRIPE_KEY=sk_live_one ds check --resolve 2>&1)
ck "a signed-out op leaves the truth unverifiable" "not currently signed in" "$out"
nk "and is not reported as a missing address" "resolve failed" "$out"
/bin/rm -f "$S/signed-out"
printf 'secret/stripe sk_live_one\n' > "$S/secrets"
out=$(DS_E2E_STRIPE_KEY=sk_live_one ds check --resolve 2>&1)
ck "an item op says does not exist is resolve failed" "op://Platform/stripe/credential does not exist at 1password" "$out"

# bug 87: no network is unverifiable and is not cached as dead.
ck "an unreachable link is unverifiable" "external link not checked" "$out"
nk "not dead" "returned 0" "$out"
nk "and nothing is cached for it" "127.0.0.1:1/gone" "$(cat .ds/urls.json 2>/dev/null)"

# bug 85: a local=true def is unverifiable without its file and ok beside it.
ck "an absent local target is unverifiable" "local=true def is only readable on its own machine" "$(ds check 2>&1)"
printf 'STRIPE_KEY=op://Platform/stripe/credential\n' > .env.prod
nk "a present local target is no longer unverifiable" "local=true def is only readable" "$(ds check 2>&1)"
printf 'OTHER=1\n' > .env.prod
ck "a present target whose pick finds nothing is pick failed" "pick failed" "$(ds check 2>&1)"
/bin/rm -f .env.prod

# bug 86: timeout= on the directive is applied, and a substring does not
# pass a command that failed.
printf '\n[run]\nenabled = true\nallow = ["runbooks/**"]\ntimeout = "30s"\n' >> .ds/config.toml
printf '# R\n\nSlow: <!-- ds:run cmd="sleep 5" timeout=1s -->\n\nFails: <!-- ds:run cmd="echo hello; exit 3" expect=hello -->\n\nStatus: <!-- ds:run cmd="echo HTTP/1.1 301 Moved; echo HTTP/1.1 200 OK" expect=301 -->\n' > runbooks/r.md
ds scan >/dev/null
start=$(date +%s)
out=$(ds check --run 2>&1)
took=$(( $(date +%s) - start ))
ck "a command past its directive timeout fails" "runbooks/r.md:3  run FAILED: sleep 5" "$out"
if [ "$took" -lt 5 ]; then echo "  PASS  and is stopped at the directive's 1s, not run.timeout (${took}s)"; pass=$((pass+1)); else echo "  FAIL  timeout= was not applied: the check took ${took}s"; fail=$((fail+1)); fi
ck "a substring is not enough when the command failed" "runbooks/r.md:5  run FAILED" "$out"
ck "expect=<status> is the last status reported, not any 301 in the output" "runbooks/r.md:7  run FAILED" "$out"

echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
