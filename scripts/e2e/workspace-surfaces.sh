#!/bin/sh
# what a workspace shows across commands: the local index commit (bug 100),
# the commits-behind warning (bug 101), another repository's citation named
# with its repository in check, impact and its remedy (bug 102) and in why
# (bug 103), a git-URL index cloned by the first command that needs it
# (bug 104), and repo-mode copies of another repository's block (bug 105).
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
ck() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
no() { case "$3" in *"$2"*) echo "  FAIL  $1 (did not want $2, got: $3)"; fail=$((fail+1));; *) echo "  PASS  $1"; pass=$((pass+1));; esac; }
gitid() { git config user.email t@t; git config user.name t; }
point() { printf 'workspace = "%s"\n' "$1" | cat - .ds/config.toml > .ds/c && mv .ds/c .ds/config.toml; }

W="$S/w"; mkdir -p "$W/index" "$W/docs/spec" "$W/api/retry" "$W/api/docs"
cd "$W/index" || exit 1
git init -q -b main .; gitid
printf '[workspace]\nname = "p"\nrepos = ["github.com/org/docs", "github.com/org/api"]\nstale_after_commits = 1\n' > ds-workspace.toml
git add -A; git commit -qm ws

cd "$W/docs" || exit 1
git init -q -b main .; gitid; ds init >/dev/null; point ../index
printf '# Retries\n\n## Backoff\n\nA failed call is retried at most 5 times.\n' > spec/retries.md
ID=$(ds def 'spec/retries.md#Backoff' --label backoff | tail -1)
ds scan >/dev/null; git add -A; git commit -qm spec
out=$(ds publish 2>&1)
ck "bug 100: publish into a local git index commits there" "committed in ../index" "$out"
no "bug 100: the index is clean after publish" "repos/" "$(git -C ../index status --porcelain)"
ck "bug 100: the index commit names the repo" "docsync publish docs @" "$(git -C ../index log -1 --format=%s)"

cd "$W/api" || exit 1
git init -q -b main .; gitid; ds init >/dev/null; point ../index
printf 'package retry\n\n// MaxAttempts: implements ds:block?id=%s\nconst MaxAttempts = 5\n' "$ID" > retry/retry.go
ds scan >/dev/null; ds sync >/dev/null 2>&1; git add -A; git commit -qm cite; ds publish >/dev/null

cd "$W/docs" || exit 1
sed -i.bak 's/at most 5 times/at most 3 times/' spec/retries.md && /bin/rm -f spec/retries.md.bak
ds scan >/dev/null
out=$(ds check 2>&1)
ck "bug 102: check names the citation's repository" "retry/retry.go (in the api repository)" "$out"
ck "bug 102: the ack remedy says where it runs" "still true: in api: ds ack $ID --doc retry/retry.go --line 3" "$out"
ck "bug 102: impact names the citation's repository" "retry/retry.go (in the api repository) (1)" "$(ds impact 2>&1)"
ck "bug 102: the remedy works where it says" "acked $ID at retry/retry.go:3" "$(cd ../api && ds sync >/dev/null 2>&1; ds ack "$ID" --doc retry/retry.go --line 3 --note x 2>&1)"
ck "bug 103: why lists the other repository's citer" "retry/retry.go:3 (in the api repository)  ds:block" "$(ds why "$ID" 2>&1)"

# Two commits past the publish, with stale_after_commits = 1.
git add -A; git commit -qm three; printf 'x\n' > notes.txt; git add -A; git commit -qm notes
ck "bug 101: the publishing repo hears it is commits behind" "index for docs is 2 commits behind main (workspace.stale_after_commits = 1)" "$(ds scan 2>&1)"
ds publish >/dev/null 2>&1
no "bug 101: publishing clears it" "commits behind" "$(ds scan 2>&1)"

# A git-URL index, and the first command is def.
git init -q --bare -b main "$W/remote.git"
git clone -q "$W/remote.git" "$W/seed" 2>/dev/null
printf '[workspace]\nname = "p"\nrepos = ["github.com/org/hb"]\n' > "$W/seed/ds-workspace.toml"
(cd "$W/seed" && gitid && git add -A && git commit -qm ws && git push -q origin main)
mkdir -p "$W/hb"; cd "$W/hb" || exit 1
git init -q -b main .; gitid; ds init >/dev/null; point "file://$W/remote.git"
printf '# H\n\n## On call\n\nPrimary first.\n' > oncall.md
out=$(ds def 'oncall.md#On call' --label oncall 2>&1)
ck "bug 104: def before any sync mints an id" "oncall-" "$out"
ck "bug 104: and the index is cloned" "ds-workspace.toml" "$(ls .ds/index 2>&1)"

# Repo mode with a block from another repository.
cd "$W/api" || exit 1
sed -i.bak 's/^mode = "build"/mode = "repo"/' .ds/config.toml && /bin/rm -f .ds/config.toml.bak
printf '# Notes\n\n<!-- ds:block id=%s -->\n\nend\n' "$ID" > docs/notes.md
ds scan >/dev/null 2>&1
out=$(ds refresh --dry-run 2>&1)
ck "bug 105: refresh --dry-run lists the copy" "1 repo-mode copies would be rewritten
  docs/notes.md" "$out"
no "bug 105: and writes nothing" "/ds:block" "$(cat docs/notes.md)"
ds refresh >/dev/null 2>&1
page=$(cat docs/notes.md)
ck "bug 105: the copy carries the published body" "A failed call is retried at most 3 times." "$page"
ck "bug 105: the copy names the repository instead of a link" '`spec/retries.md:4-6` (in docs)' "$page"
ds scan >/dev/null 2>&1
no "bug 105: a fresh copy is not tampered" "tampered" "$(ds check 2>&1)"
no "bug 105: nor under a frozen check" "tampered" "$(git add -A; git commit -qm copy; CI=true ds check 2>&1)"
out=$(ds render docs/notes.md 2>&1)
ck "bug 105: render shows the block once" "1" "$(printf '%s\n' "$out" | grep -c 'retried at most 3 times')"
no "bug 105: render drops the closer" "/ds:block" "$out"

echo
echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
