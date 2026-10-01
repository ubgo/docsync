#!/bin/sh
# The commands outside check, across commits: render --at and at= snapshots,
# page-relative links, context baselines, claim renewal, acks from commits,
# undo's history labels, ds's own reports inside the tree, the LSP root, and
# the dry runs. Each case is one of bugs 60 to 76; every one was found by
# running the binary as a user would, and each passed every unit test.
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
# has NAME WANT GOT: GOT contains WANT. lacks NAME UNWANTED GOT: it does not.
has() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
lacks() { case "$3" in *"$2"*) echo "  FAIL  $1 (did not want $2, got: $3)"; fail=$((fail+1));; *) echo "  PASS  $1"; pass=$((pass+1));; esac; }
eq() { if [ "$2" = "$3" ]; then echo "  PASS  $1"; pass=$((pass+1)); else echo "  FAIL  $1 (want $2, got $3)"; fail=$((fail+1)); fi; }

W="$S/w"; mkdir -p "$W/billing" "$W/docs"; cd "$W" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
printf 'package billing\n\nimport "time"\n\n// GraceDays is how long.\nconst GraceDays = 14\n\n// DueDate is when.\nfunc DueDate(t time.Time) time.Time {\n\treturn t.AddDate(0, 0, 30)\n}\n' > billing/invoice.go
ds init >/dev/null
G=$(ds def billing/invoice.go#GraceDays | tail -1)
F=$(ds def billing/invoice.go#DueDate | tail -1)
printf '# Billing\n\nGrace is [14](ds:cfg?id=%s) days.\n\nAn invoice is [due](ds:block?id=%s) thirty days after.\n\n<!-- ds:block id=%s -->\n' "$G" "$F" "$F" > docs/billing.md
ds scan >/dev/null; git add -A; git commit -qm one
ONE=$(git rev-parse --short=7 HEAD)
# The second commit moves both defs three lines down and changes the value.
printf 'package billing\n\nimport "time"\n\n// a\n// b\n// c\n' > hdr; tail -n +4 billing/invoice.go >> hdr; mv hdr billing/invoice.go
sedi 's/GraceDays = 14/GraceDays = 21/' billing/invoice.go
printf '# Billing\n\nGrace is [21](ds:cfg?id=%s) days.\n\nAn invoice is [due](ds:block?id=%s) thirty days after.\n\n<!-- ds:block id=%s -->\n' "$G" "$F" "$F" > docs/billing.md
ds scan >/dev/null; ds ack "$G" --doc docs/billing.md --line 3 --note ok >/dev/null; git add -A; git commit -qm two

set +e
# bug 64: a link in docs/ is relative to the page.
has "a link from docs/ names ../billing (bug 64)" "[due](../billing/invoice.go#L15-L17)" "$(ds render docs/billing.md 2>&1)"
# bug 60: --at takes the blocks, values, lines and commit from that commit.
at=$(ds render docs/billing.md --at "$ONE" 2>&1)
has "render --at: the value at that commit (bug 60)" "Grace is 14 days." "$at"
has "render --at: the lines at that commit (bug 60)" "[due](../billing/invoice.go#L11-L13)" "$at"
printf '[check]\npermalink = "https://h/{sha}/{file}#L{start}-L{end}"\n' >> .ds/config.toml
has "render --at: the permalink names that commit (bug 60)" "https://h/$ONE/billing/invoice.go#L11-L13" "$(ds render docs/billing.md --at "$ONE" 2>&1)"
git checkout -q -- .ds/config.toml
miss=$(ds render docs/new.md --at "$ONE" 2>&1)
has "render --at: a page the commit lacks says so (bug 60)" "docs/new.md does not exist at $ONE" "$miss"
lacks "render --at: and does not blame the repository (bug 60)" "not a repository" "$miss"
# bug 66: a plain render shows an at= snapshot from its commit.
printf '# Then\n\n<!-- ds:block id=%s at=%s -->\n' "$F" "$ONE" > docs/then.md
snap=$(ds render docs/then.md 2>&1)
has "plain render: at= renders the code (bug 66)" "return t.AddDate(0, 0, 30)" "$snap"
has "plain render: at= links the lines at that commit (bug 66)" "invoice.go#L11-L13) · as of \`$ONE\`" "$snap"
lacks "plain render: no 'no snapshot' note (bug 66)" "no snapshot" "$snap"
/bin/rm -f docs/then.md
# bug 67: the block form of cfg fails check, where render would drop it.
printf '# C\n\n<!-- ds:cfg id=%s -->\n' "$G" > docs/cfg.md
c=$(ds check 2>&1); code=$?
eq "cfg in block position fails check (bug 67)" 1 "$code"
has "and says it is link form only (bug 67)" "ds:cfg is link form only" "$c"
/bin/rm -f docs/cfg.md

# bugs 61, 62, 63, 65: context.
sedi 's/0, 0, 30)/0, 0, 45)/' billing/invoice.go
ds scan >/dev/null
has "context --since <commit> diffs against that commit (bug 61)" "+	return t.AddDate(0, 0, 45)" "$(ds context docs/billing.md --since "$ONE" 2>&1)"
bad=$(ds context docs/billing.md --since nonsense 2>&1); code=$?
eq "context --since <unknown> is refused (bug 61)" 2 "$code"
has "and names what it accepts (bug 61)" "neither \"ack\" nor a commit" "$bad"
has "context --since ack after a scan still diffs since the ack (bug 62)" "-	return t.AddDate(0, 0, 30)" "$(ds context docs/billing.md --since ack 2>&1)"
has "context --mode diff shows the diff check --json has (bug 62)" "+	return t.AddDate(0, 0, 45)" "$(ds context docs/billing.md --mode diff 2>&1)"
has "context <id> lists the block-position citation (bug 63)" "docs/billing.md:7 shows it in block position" "$(ds context "$F" 2>&1)"
has "context with no budget says so (bug 65)" "tokens used, no budget" "$(ds context docs/billing.md 2>&1)"
git checkout -q -- billing/invoice.go .ds
ds scan >/dev/null

# bug 76: an ack must name its sentences.
r=$(ds ack "$F" --note "prose updated" 2>&1); code=$?
eq "ack <id> --note with no --doc/--line/--all is refused (bug 76)" 2 "$code"
has "and says what to add (bug 76)" "--doc and --line, or --all" "$r"
# bug 73: note= in a commit message reaches the ack log.
sedi 's/0, 0, 30)/0, 0, 31)/' billing/invoice.go; ds scan >/dev/null
git add -A; git commit -qm "Bump due date

ds:ack id=$F note=\"thirty-one is still about thirty\"" >/dev/null
ds ack --from-commit HEAD >/dev/null 2>&1
has "ack --from-commit records note= (bug 73)" "thirty-one is still about thirty" "$(ds audit 2>&1)"
git commit -q --allow-empty -m "x

ds:ack id=$F nte=typo"
ds ack --from-commit HEAD >/dev/null 2>&1; code=$?
eq "ack --from-commit refuses an unknown key (bug 73)" 2 "$code"

# bugs 74, 75: ds's own reports in the tree are not source.
sedi 's/0, 0, 31)/0, 0, 32)/' billing/invoice.go
ds check --json > report.json
ds audit --export audit.jsonl >/dev/null
before=$(cksum < audit.jsonl)
sc=$(ds scan 2>&1)
has "a saved check --json report is skipped (bug 75)" "0 problems, 2 skipped" "$sc"
lacks "and cites nothing (bug 75)" "report.json" "$(ds why "$F" 2>&1)"
ds rename duedate due >/dev/null 2>&1
eq "rename leaves an exported audit log alone (bug 74)" "$before" "$(cksum < audit.jsonl)"
has "rename still relabels the real citation (bug 74)" "ds:block?id=due-" "$(cat docs/billing.md)"
ds rename due duedate >/dev/null 2>&1
/bin/rm -f report.json audit.jsonl
git checkout -q -- billing/invoice.go
has "the init workflow writes its report outside the checkout (bug 75)" 'RUNNER_TEMP/docsync.json' "$(cat .ds/ci-github.yml)"

# bug 71: a committed def pushed down by a later commit is still committed,
# introduced by the commit that wrote it.
U="$S/u"; mkdir -p "$U"; cd "$U" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
printf 'package u\n\nconst Limit = 3\n' > u.go
ds init >/dev/null; ds def u.go#Limit >/dev/null; git add -A; git commit -qm def
INTRO=$(git rev-parse --short=7 HEAD)
printf 'package u\n\n// one\n// two\n' > hdr; tail -n +3 u.go >> hdr; mv hdr u.go; git add -A; git commit -qm later
l=$(ds undo --list 2>&1)
has "undo --list: a moved committed write is committed (bug 71)" "committed $INTRO" "$l"
lacks "undo --list: and is not called uncommitted (bug 71)" "uncommitted" "$l"
cd "$W" || exit 1

# bug 72: renewing a claim about a changed block clears it without a scan.
printf '# Claims\n\nGrace is generous. <!-- ds:claim owner=@b reviewed=2026-09-30 expires=3650d about=%s -->\n' "$G" > docs/claims.md
ds scan >/dev/null; git add -A; git commit -qm claims
sedi 's/GraceDays = 21/GraceDays = 28/' billing/invoice.go
has "a claim about a changed block expires (bug 72)" "claim is about $G, which changed" "$(ds check 2>&1)"
ds ack --doc docs/claims.md --line 3 --note ok >/dev/null
lacks "renewing it clears it with no scan (bug 72)" "docs/claims.md" "$(ds check 2>&1)"
git checkout -q -- billing/invoice.go .ds; /bin/rm -f docs/claims.md

# bug 70: review --out without --ai writes the worklist.
sedi 's/GraceDays = 21/GraceDays = 22/' billing/invoice.go
ds review --out "$S/review.txt" >/dev/null 2>&1; code=$?
eq "review --out without --ai succeeds (bug 70)" 0 "$code"
has "and writes the worklist (bug 70)" "- [ ] docs/billing.md:3" "$(cat "$S/review.txt" 2>&1)"
# bug 69: github comment --dry-run needs no token, repository or PR.
d=$(env -u GITHUB_TOKEN -u GITHUB_REPOSITORY -u GITHUB_EVENT_PATH ds github comment --dry-run 2>&1); code=$?
eq "github comment --dry-run runs with no environment (bug 69)" 1 "$code"
has "and prints the comment (bug 69)" "<!-- docsync:doc=docs/billing.md -->" "$d"
git checkout -q -- billing/invoice.go

# bug 68: ds lsp serves the workspace named in initialize.
cd "$S" || exit 1
uri="file://$W"
case "$W" in [A-Za-z]:*) uri="file:///$W";; esac
init="{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"rootUri\":\"$uri\"}}"
lens="{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"textDocument/codeLens\",\"params\":{\"textDocument\":{\"uri\":\"$uri/billing/invoice.go\"}}}"
o=$(printf 'Content-Length: %d\r\n\r\n%sContent-Length: %d\r\n\r\n%s' "${#init}" "$init" "${#lens}" "$lens" | ds lsp 2>&1)
has "lsp: code lenses for the client's rootUri, started elsewhere (bug 68)" "$F" "$o"
set -e
echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
