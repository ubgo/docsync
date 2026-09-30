#!/bin/sh
# the JavaScript and Hugo integrations against the real ds binary. Their own
# tests replace ds with a fake, which is how `--dir` stayed documented for a
# year without existing: every fake accepted it. Here the Docusaurus plugin
# and remark transformer run with their default executor, and a Hugo site
# builds from `ds export hugo`, so a change to a JSON field, a flag, or the
# render output that the integrations depend on fails here.
#
# Drives the real ds binary on throwaway git repos; run through scripts/e2e/run.sh,
# which puts the binary built from this tree first on PATH.
root=$(cd "$(dirname "$0")/../.." && pwd)
S=$(mktemp -d)
# Under Git Bash, mktemp gives an MSYS path (/tmp/...) that only MSYS programs
# understand; ds.exe reads it from a config file as a path on no drive. The
# mixed form (C:/...) is one both sides accept.
command -v cygpath >/dev/null 2>&1 && S=$(cygpath -m "$S")
trap '/bin/rm -rf "$S"' EXIT
pass=0; fail=0
ck() { [ -n "$2" ] || { echo "  FAIL  $1 (an empty expectation matches anything)"; fail=$((fail+1)); return; }; case "$3" in *"$2"*) echo "  PASS  $1"; pass=$((pass+1));; *) echo "  FAIL  $1 (want $2, got: $3)"; fail=$((fail+1));; esac; }
W="$S/w"; mkdir -p "$W/docs" "$W/internal"; cd "$W" || exit 1
git init -q -b main .; git config user.email t@t; git config user.name t
printf 'package p\n\n// ds:def id=alpha-k7m2p4xq\nfunc Alpha() int { return 1 }\n\n// ds:def id=limit-h3v8n2wd\nconst Limit = 10\n' > internal/a.go
printf '# Guide\n\nAlpha [returns one](ds:block?id=alpha-k7m2p4xq). The limit is [10](ds:cfg?id=limit-h3v8n2wd).\n\n<!-- ds:block id=alpha-k7m2p4xq -->\n' > docs/d.md
ds init >/dev/null; ds scan >/dev/null; git add -A; git commit -qm base
ds ack alpha-k7m2p4xq --doc docs/d.md --line 3 --note "checked" >/dev/null

if command -v node >/dev/null 2>&1; then
  out=$(cd "$S" && node -e '
    const [dir, pkg] = process.argv.slice(1);
    const docsync = require(pkg);
    (async () => {
      const plugin = docsync({}, { cwd: dir, args: ["--dir", dir] });
      const status = await plugin.loadContent();
      let data;
      await plugin.contentLoaded({ content: status, actions: { setGlobalData: (d) => { data = d; } } });
      const row = data.byDoc["docs/d.md"].find((r) => r.id === "alpha-k7m2p4xq" && r.line === 3);
      console.log("ROW", row.severity, row.note);
      const transformer = docsync.remark.call({ parse: (s) => ({ children: [{ value: s }] }) }, { cwd: dir, args: ["--dir", dir] });
      const tree = transformer({ children: [] }, { path: dir + "/docs/d.md" });
      console.log("RENDER", JSON.stringify(tree.children[0].value));
    })().catch((e) => { console.log("ERROR", e.message); });
  ' "$W" "$root/integrations/docusaurus" 2>&1)
  ck "Docusaurus plugin loads status from the real ds" "ROW none checked" "$out"
  ck "Docusaurus remark renders the cfg value through the real ds" "The limit is 10." "$out"
  ck "Docusaurus remark expands the block" "func Alpha() int { return 1 }" "$out"
else
  echo "  SKIP  node is not installed"
fi

if command -v hugo >/dev/null 2>&1; then
  site="$S/site"; mkdir -p "$site/content" "$site/layouts/_default"
  cp -R "$root/integrations/hugo/layouts/." "$site/layouts/"
  printf 'baseURL = "http://example.org/"\ntitle = "t"\ndisableKinds = ["taxonomy", "term", "RSS", "sitemap"]\n' > "$site/hugo.toml"
  printf '<!doctype html><html><body>{{ .Content }}{{ partial "docsync/status.html" . }}</body></html>\n' > "$site/layouts/_default/single.html"
  printf '<!doctype html><html><body>{{ .Content }}</body></html>\n' > "$site/layouts/_default/list.html"
  printf -- '---\ntitle: Guide\n---\n\n{{< ds id="alpha-k7m2p4xq" >}}\n\n{{< ds id="nope-a2b6f8jk" >}}\n' > "$site/content/guide.md"
  ck "export hugo writes the site data" "exported" "$(ds export hugo --out "$site/data/docsync" 2>&1)"
  build=$(hugo --source "$site" --quiet 2>&1) || echo "  hugo: $build"
  page=$(cat "$site/public/guide/index.html" 2>/dev/null)
  ck "Hugo renders an exported block through the shortcode" 'data-ds-id="alpha-k7m2p4xq"' "$page"
  # Hugo highlights the fenced code, so the words arrive split into spans.
  ck "Hugo renders the block's code as highlighted Go" 'data-lang="go"' "$page"
  ck "Hugo renders the block's caption from blocks.json" 'internal/a.go:4-4' "$page"
  ck "Hugo marks an id that was not exported" "nope-a2b6f8jk not exported" "$page"
  ck "Hugo status partial carries severity" 'data-ds-severity="none"' "$page"
  ck "Hugo status partial carries the ack note" 'data-ds-note="checked"' "$page"
else
  echo "  SKIP  hugo is not installed"
fi
echo; echo "  ---- $pass passed, $fail failed ----"
[ "$fail" -eq 0 ]
