package render

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/block"
)

func def(id, file string, start int, content string, args map[string]string) block.Block {
	if args == nil {
		args = map[string]string{}
	}
	args[block.KeyID] = id
	b := block.Block{ID: id, Kind: block.KindFunc, Pos: block.Position{File: file, Start: start, End: start + strings.Count(content, "\n")}, Args: args}
	b.SetContent(content)
	return b
}

var (
	save   = def("sess-save-k7m2p4xq", "internal/store/write.go", 12, "func Save() {\n\t// legacy first\n\treturn nil\n}", nil)
	port   = def("auth-port-h3v8n2wd", "config/auth.yaml", 3, "8081", nil)
	host   = def("app-host-d4k8w2mn", "docs/facts.md", 2, "https://example.com/api/v1", nil)
	users  = def("users-x8b3n7qr", "docs/facts.md", 3, "1200000", nil)
	multi  = def("multi-p2c4y7mk", "a.go", 1, "a\nb", nil)
	secret = def("op-stripe-key-p9c2v7ld", ".env.tpl", 3, "op://Platform/stripe-prod/credential", map[string]string{"secret": "true", "truth": "true"})
	ghKey  = def("gh-stripe-key-r4t6x2mb", ".github/workflows/deploy.yml", 9, "${{ secrets.STRIPE_KEY }}", map[string]string{"secret": "true", "from": secret.ID, "sync": "scripts/sync-secrets.sh"})
	appKey = def("app-stripe-key-m4w8k2qn", "internal/pay/stripe.go", 12, "STRIPE_KEY", map[string]string{"secret": "true", "source": "env", "from": ghKey.ID})
	sweep  = def("sess-sweep-t4k2b9rf", "db/sweep.sql", 2, "DELETE FROM sessions WHERE expires_at < now();", map[string]string{"runnable": "true"})
	prodH  = def("host-env-a2b6f8jk", "prod.yaml", 1, "prod.example", map[string]string{"env": "prod"})
	stagH  = def("host-env-a2b6f8jk", "staging.yaml", 1, "staging.example", map[string]string{"env": "staging"})
	all    = []block.Block{save, port, host, users, multi, secret, ghKey, appKey, sweep, prodH, stagH}
)

func render(t *testing.T, src string, opts Options) (string, []Note) {
	t.Helper()
	out, notes := Render(Input{Doc: "docs/x.md", Src: []byte(src), Defs: all}, opts)
	return string(out), notes
}

func TestInlineForms(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"The guard is [`SaveSession`](ds:block?id=sess-save-k7m2p4xq). It listens on [8080](ds:cfg?id=auth-port-h3v8n2wd).",
		"Deploy to [x](ds:cfg?id=app-host-d4k8w2mn&format=host) or [x](ds:cfg?id=app-host-d4k8w2mn&format=link); serving [n](ds:cfg?id=users-x8b3n7qr&format=compact) users, version [v](ds:cfg?id=auth-port-h3v8n2wd&format=code) / [q](ds:cfg?id=auth-port-h3v8n2wd&format=quote).",
		"Port [8081](ds:def?id=auth-port-h3v8n2wd&type=int) and home [https://example.com](ds:def?id=app-home-c8t2m6qp&type=url).",
		"See the [TOAST docs](ds:url?href=https://www.postgresql.org/docs/toast.html&title=TOAST).",
		"We chose Postgres. <!-- ds:claim owner=@p reviewed=2026-09-06 expires=90d -->",
		"Staging host is [s](ds:cfg?id=host-env-a2b6f8jk&env=staging), prod is [p](ds:cfg?id=host-env-a2b6f8jk).",
		"Chain link: [stripe](ds:chain?id=app-stripe-key-m4w8k2qn).",
		"A plain [link](https://example.com) and ![img](a.png) stay.",
		"[a](https://x) [8080](ds:cfg?id=auth-port-h3v8n2wd) <!-- keep -->",
	}, "\n")
	out, notes := render(t, src, Options{Commit: "7c1e2a", Permalink: "https://g/{sha}/{file}#L{start}-L{end}", Env: "prod"})
	if len(notes) != 0 {
		t.Fatalf("notes = %+v", notes)
	}
	want := []string{
		"The guard is [`SaveSession`](https://g/7c1e2a/internal/store/write.go#L12-L15). It listens on 8081.",
		"Deploy to example.com or [https://example.com/api/v1](https://example.com/api/v1); serving 1.2M users, version `8081` / \"8081\".",
		"Port 8081 and home [https://example.com](https://example.com).",
		"See the [TOAST docs](https://www.postgresql.org/docs/toast.html).",
		"We chose Postgres.",
		"Staging host is staging.example, prod is prod.example.",
		"Chain link: [stripe](https://g/7c1e2a/internal/pay/stripe.go#L12-L12).",
		"A plain [link](https://example.com) and ![img](a.png) stay.",
		"[a](https://x) 8081 <!-- keep -->",
	}
	got := strings.Split(out, "\n")
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i+1, got[i], want[i])
		}
	}
}

func TestInlineNotes(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"[x](ds:block?id=nope-h3v8n2wd)",
		"[x](ds:cfg?id=nope-h3v8n2wd) [y](ds:cfg?id=multi-p2c4y7mk) [z](ds:cfg?query=sql:select)",
		"[x](ds:url) [y](ds:table?kind=task) ![i](ds:block?id=sess-save-k7m2p4xq)",
		"[x](ds:block?id=) text <!-- ds:bad key -->",
		"[x](ds:cfg?id=host-env-a2b6f8jk&env=dev)",
		"[x](ds:block?id=sess-save-k7m2p4xq&at=abc)",
		"[x](ds:cfg?id=a&id=b) [y](ds:?id=1)",
	}, "\n")
	out, notes := render(t, src, Options{Env: "prod"})
	lines := strings.Split(out, "\n")
	if lines[0] != "x" || lines[1] != "x y z" || lines[2] != "x [y](ds:table?kind=task) ![i](ds:block?id=sess-save-k7m2p4xq)" {
		t.Errorf("degraded lines = %q", lines[:3])
	}
	if lines[3] != "x text <!-- ds:bad key -->" {
		t.Errorf("unparseable comment kept: %q", lines[3])
	}
	if lines[4] != "x" {
		t.Errorf("missing env falls back to text: %q", lines[4])
	}
	if lines[5] != "[x](internal/store/write.go#L12-L15)" {
		t.Errorf("at= link uses the default template: %q", lines[5])
	}
	perLine := map[int]int{}
	for _, n := range notes {
		perLine[n.Line]++
	}
	if lines[6] != "[x](ds:cfg?id=a&id=b) [y](ds:?id=1)" {
		t.Errorf("unparseable links kept: %q", lines[6])
	}
	if perLine[1] != 1 || perLine[2] != 3 || perLine[3] != 3 || perLine[4] != 2 || perLine[5] != 1 || perLine[7] != 2 {
		t.Errorf("notes per line = %v: %+v", perLine, notes)
	}
	for i := 1; i < len(notes); i++ {
		if notes[i-1].Line > notes[i].Line {
			t.Fatal("notes not sorted")
		}
	}
}

func TestBlockPosition(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"<!-- ds:def id=sess-policy-h2n8wq4t -->",
		"## Policy",
		"",
		"<!-- ds:block id=sess-save-k7m2p4xq -->",
		"",
		"<!-- ds:block id=sess-save-k7m2p4xq lines=1-2 title=\"the guard\" strip=comments collapse=true lang=golang -->",
		"",
		"<!-- ds:block id=sess-save-k7m2p4xq lines=3-9 -->",
		"<!-- ds:block id=sess-save-k7m2p4xq lines=x -->",
		"<!-- ds:block id=nope-h3v8n2wd -->",
		"<!-- ds:block id=op-stripe-key-p9c2v7ld -->",
		"<!-- ds:block id=sess-save-k7m2p4xq at=9f3a1c -->",
		"<!-- ds:cfg id=auth-port-h3v8n2wd -->",
		"<!-- ds:url href=https://example.com -->",
		"<!-- ds:url -->",
		"<!-- ds:frob id=1 -->",
		"<!-- ds:block -- broken -->",
		"<!-- not a directive -->",
	}, "\n")
	out, notes := render(t, src, Options{Commit: "abc"})
	if strings.Contains(out, "ds:def") || !strings.HasPrefix(out, "## Policy\n") {
		t.Errorf("def line must vanish without touching the heading:\n%s", out)
	}
	if !strings.Contains(out, "**sess-save-k7m2p4xq** · [`internal/store/write.go:12-15`](internal/store/write.go#L12-L15)\n\n```go\nfunc Save() {\n\t// legacy first\n\treturn nil\n}\n```") {
		t.Errorf("full block:\n%s", out)
	}
	if !strings.Contains(out, "<details>\n<summary>**the guard** · [`internal/store/write.go:12-15`](internal/store/write.go#L12-L15)</summary>\n\n```golang\nfunc Save() {\n```\n\n</details>") {
		t.Errorf("collapsed fragment with comments stripped:\n%s", out)
	}
	if !strings.Contains(out, "[`internal/store/write.go:12-15`](internal/store/write.go#L12-L15) · as of `9f3a1c`") {
		t.Errorf("snapshot without hook renders caption only:\n%s", out)
	}
	for _, kept := range []string{"<!-- ds:block id=sess-save-k7m2p4xq lines=3-9 -->", "<!-- ds:block id=sess-save-k7m2p4xq lines=x -->", "<!-- ds:block id=nope-h3v8n2wd -->", "<!-- ds:block id=op-stripe-key-p9c2v7ld -->", "<!-- ds:cfg id=auth-port-h3v8n2wd -->", "<!-- ds:url -->", "<!-- ds:frob id=1 -->", "<!-- ds:block -- broken -->", "<!-- not a directive -->"} {
		if !strings.Contains(out, kept) {
			t.Errorf("line should be kept verbatim: %s", kept)
		}
	}
	if !strings.Contains(out, "\n<https://example.com>\n") {
		t.Error("url in block position renders an autolink")
	}
	if len(notes) != 9 {
		t.Errorf("notes = %d: %+v", len(notes), notes)
	}
}

// promise:at-frozen
func TestSnapshotTooLargeAndLink(t *testing.T) {
	t.Parallel()
	big := def("big-k7m2p4xq", "a.py", 1, strings.Repeat("x\n", 5)+"x", nil)
	fenced := def("fence-h3v8n2wd", "a.md", 1, "````md\ncode\n````", nil)
	defs := []block.Block{big, fenced, save}
	src := "<!-- ds:block id=big-k7m2p4xq -->\n<!-- ds:block id=big-k7m2p4xq lines=1-3 -->\n<!-- ds:block id=fence-h3v8n2wd -->\n<!-- ds:block id=sess-save-k7m2p4xq at=9f3a1c -->\n<!-- ds:block id=sess-save-k7m2p4xq at=old -->\n"
	out, notes := Render(Input{Src: []byte(src), Defs: defs}, Options{MaxLines: 4, Link: func(b block.Block) string { return "L:" + b.ID }, Snapshot: func(id, sha string) (string, bool) {
		if sha == "9f3a1c" {
			return "old body", true
		}
		return "", false
	}})
	s := string(out)
	if !strings.Contains(s, "**big-k7m2p4xq** · [`a.py:1-6`](L:big-k7m2p4xq)\n**big-k7m2p4xq** · [`a.py:1-6`](L:big-k7m2p4xq)\n\n```python\nx\nx\nx\n```") {
		t.Errorf("too-large then fragment:\n%s", s)
	}
	if !strings.Contains(s, "`````markdown\n````md\ncode\n````\n`````") {
		t.Errorf("fence longer than content:\n%s", s)
	}
	if !strings.Contains(s, "· as of `9f3a1c`\n\n```go\nold body\n```") {
		t.Errorf("snapshot hook content:\n%s", s)
	}
	if !strings.Contains(s, "· as of `old`\n") || strings.Contains(s, "```go\nfunc Save") {
		t.Errorf("missing snapshot must not fall back to live code:\n%s", s)
	}
	if len(notes) != 2 || !strings.Contains(notes[0].Message, "over the cap") || !strings.Contains(notes[1].Message, "no snapshot") {
		t.Errorf("notes = %+v", notes)
	}
}

func TestChain(t *testing.T) {
	t.Parallel()
	out, notes := render(t, "<!-- ds:chain id=app-stripe-key-m4w8k2qn -->\n", Options{})
	if trailing, _ := render(t, "Stripe: <!-- ds:chain id=app-stripe-key-m4w8k2qn -->", Options{}); !strings.HasPrefix(trailing, "Stripe:\n\n- `app-stripe-key-m4w8k2qn`") {
		t.Errorf("trailing chain renders under the line:\n%s", trailing)
	}
	if kept, _ := render(t, "Missing: <!-- ds:chain id=absent-a2b6f8jk -->", Options{}); kept != "Missing: <!-- ds:chain id=absent-a2b6f8jk -->" {
		t.Errorf("unrenderable trailing directive is kept: %q", kept)
	}
	want := "- `app-stripe-key-m4w8k2qn` env `STRIPE_KEY` — [internal/pay/stripe.go:12](internal/pay/stripe.go#L12-L12)\n" +
		"  - from `gh-stripe-key-r4t6x2mb` github `${{ secrets.STRIPE_KEY }}` — [.github/workflows/deploy.yml:9](.github/workflows/deploy.yml#L9-L9) · synced by `scripts/sync-secrets.sh`\n" +
		"    - from `op-stripe-key-p9c2v7ld` 1password `op://Platform/stripe-prod/credential` — [.env.tpl:3](.env.tpl#L3-L3) · **truth**\n"
	if out != want || len(notes) != 0 {
		t.Errorf("chain:\n%s\nwant:\n%s\nnotes %+v", out, want, notes)
	}
	cycA := def("ca-e6f2k4np", "z", 1, "", map[string]string{"from": "cb-f7g3l5pq", "local": "true"})
	cycB := def("cb-f7g3l5pq", "z", 2, "b", map[string]string{"from": cycA.ID})
	dang := def("dg-q9x1z6ch", "z", 3, "c", map[string]string{"from": "missing-m2q1s8vt"})
	src := "<!-- ds:chain id=ca-e6f2k4np -->\n<!-- ds:chain id=dg-q9x1z6ch -->\n<!-- ds:chain id=absent-a2b6f8jk -->\n"
	o, n := Render(Input{Src: []byte(src), Defs: []block.Block{cycA, cycB, dang}}, Options{})
	s := string(o)
	if !strings.Contains(s, "lives on a machine") || !strings.Contains(s, "- from `missing-m2q1s8vt` — **not defined**") || !strings.Contains(s, "<!-- ds:chain id=absent-a2b6f8jk -->") {
		t.Errorf("degraded chains:\n%s", s)
	}
	if len(n) != 3 || !strings.Contains(n[0].Message, "cycle") || !strings.Contains(s, "- `ca-e6f2k4np` — [z:1]") {
		t.Errorf("chain notes = %+v\n%s", n, s)
	}
	// Without a default env, an env-only id resolves to its first def.
	first, _ := render(t, "[x](ds:cfg?id=host-env-a2b6f8jk)", Options{})
	if first != "prod.example" {
		t.Errorf("first def fallback = %q", first)
	}
}

func TestRunAndTable(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"<!-- ds:run id=sess-sweep-t4k2b9rf env=staging expect=rows -->",
		"<!-- ds:run cmd=\"task test\" expect=ok -->",
		"<!-- ds:run file=scripts/smoke.sh show=command -->",
		"<!-- ds:run cmd=x show=none -->",
		"<!-- ds:run cmd=x show=output -->",
		"<!-- ds:run id=nope-h3v8n2wd -->",
		"<!-- ds:run expect=ok -->",
		"<!-- ds:run id=auth-port-h3v8n2wd -->",
		"<!-- ds:table kind=task cols=title,due empty=\"nothing open\" -->",
		"<!-- ds:table kind=task -->",
		"<!-- ds:table kind=err -->",
		"<!-- ds:table kind=none empty=none -->",
	}, "\n")
	at := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	opts := Options{
		Runs: map[int]RunResult{1: {Output: "3 rows\n", OK: true, At: at}, 3: {Output: "hidden", OK: false, At: at}, 5: {Output: "", OK: false, At: at}},
		Records: func(args map[string]string) ([]map[string]string, error) {
			switch args["kind"] {
			case "task":
				return []map[string]string{{"title": "a|b", "due": "mon", "owner": "k"}, {"title": "c"}}, nil
			case "err":
				return nil, errors.New("boom")
			}
			return nil, nil
		},
	}
	out, notes := render(t, src, opts)
	s := string(out)
	for _, want := range []string{
		"```sh\nDELETE FROM sessions WHERE expires_at < now();\n```\n\n_ok · as of 2026-09-06T01:02:03Z_\n\n```text\n3 rows\n```",
		"```sh\ntask test\n```\n\n_not run yet_",
		"```sh\nscripts/smoke.sh\n```\n",
		"_failed · as of 2026-09-06T01:02:03Z_",
		"| title | due |\n|---|---|\n| a\\|b | mon |\n| c |  |",
		"| due | owner | title |\n|---|---|---|\n| mon | k | a\\|b |\n|  |  | c |",
		"|  |  | c |\nnone",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing:\n%s\nin:\n%s", want, s)
		}
	}
	if strings.Contains(s, "hidden") || strings.Contains(s, "```sh\nx\n```") {
		t.Errorf("show= not honoured:\n%s", s)
	}
	if !strings.Contains(s, "<!-- ds:run id=nope-h3v8n2wd -->") || !strings.Contains(s, "<!-- ds:run expect=ok -->") {
		t.Errorf("undefined run kept:\n%s", s)
	}
	msgs := make([]string, len(notes))
	for i, n := range notes {
		msgs[i] = n.Message
	}
	joined := strings.Join(msgs, "\n")
	for _, want := range []string{"not defined", "needs one of", "not runnable", "record source: boom"} {
		if !strings.Contains(joined, want) {
			t.Errorf("note %q missing in %q", want, joined)
		}
	}
	// No records hook: empty text and a note.
	o, n := render(t, "<!-- ds:table kind=task empty=\"none yet\" -->", Options{})
	if string(o) != "none yet" || len(n) != 1 {
		t.Errorf("no hook: %q %+v", o, n)
	}
}

func TestMaskAndCRLF(t *testing.T) {
	t.Parallel()
	src := "---\r\nds:\r\n  covers: [x]\r\n---\r\n```md\r\n[8080](ds:cfg?id=auth-port-h3v8n2wd)\r\n```\r\n~~~~\r\n```\r\n[8080](ds:cfg?id=auth-port-h3v8n2wd)\r\n~~~~\r\n[8080](ds:cfg?id=auth-port-h3v8n2wd)\r\n"
	out, notes := render(t, src, Options{})
	want := "---\nds:\n  covers: [x]\n---\n```md\n[8080](ds:cfg?id=auth-port-h3v8n2wd)\n```\n~~~~\n```\n[8080](ds:cfg?id=auth-port-h3v8n2wd)\n~~~~\n8081\n"
	if string(out) != want || len(notes) != 0 {
		t.Errorf("masked render:\n%q\nwant\n%q", out, want)
	}
	if out, _ := render(t, "---\n[8080](ds:cfg?id=auth-port-h3v8n2wd)\n", Options{}); out != "---\n8081\n" {
		t.Errorf("unterminated frontmatter is ordinary text: %q", out)
	}
}

func TestHelpers(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"1200000": "1.2M", "1500": "1.5K", "2000000000": "2B", "999": "999", "abc": "abc", "1,000": "1K"} {
		if got := compact(in); got != want {
			t.Errorf("compact(%q) = %q, want %q", in, got, want)
		}
	}
	if formatValue("not a url", FormatHost) != "not a url" || formatValue("v", "") != "v" || formatValue("v", "weird") != "v" {
		t.Error("formatValue fallbacks")
	}
	for addr, want := range map[string]string{"op://a/b": ProviderOnePassword, "${{ secrets.X }}": ProviderGitHub, "arn:aws:secretsmanager:x": ProviderAWS, "projects/p/secrets/s": ProviderGCP, "vault:kv/x": ProviderVault, "STRIPE_KEY": "src"} {
		if got := Provider(addr, "src"); got != want {
			t.Errorf("Provider(%q) = %q", addr, got)
		}
	}
	if _, ok := Value(block.Block{Content: "  \n"}); ok {
		t.Error("blank has no value")
	}
	if got := stripCommentLines([]string{"# c", "x"}, "file.unknownext"); len(got) != 2 {
		t.Error("unknown style keeps lines")
	}
	if got := stripCommentLines([]string{"/* c */", "x"}, "a.css"); len(got) != 2 {
		t.Error("block-only style keeps lines")
	}
	if fenceFor([]string{"``"}) != "```" || fenceFor([]string{"```"}) != "````" {
		t.Error("fenceFor")
	}
	if _, ok := wholeComment("<!-- a --> b <!-- c -->"); ok {
		t.Error("two comments are not one whole comment")
	}
	if len(FormatValues) != 6 || len(langByExt) == 0 {
		t.Error("constants")
	}
}

// TestSecretCfgSaysSecret pins bug 126's second half: a ds:cfg citing a
// secret keeps its link text, as before, and the note says why -- it used to
// say the value "yields more than one line; use ds:block", which is false
// and points at a verb that refuses secrets as well.
func TestSecretCfgSaysSecret(t *testing.T) {
	t.Parallel()
	blank := secret
	blank.SetContent("")
	out, notes := Render(Input{Src: []byte("Key [k](ds:cfg?id=" + secret.ID + ").\n"), Defs: []block.Block{blank}}, Options{})
	if string(out) != "Key k.\n" || len(notes) != 1 || !strings.Contains(notes[0].Message, "is a secret") || strings.Contains(notes[0].Message, "more than one line") {
		t.Errorf("secret cfg = %q %+v", out, notes)
	}
	// A secret that is an address, not a value, still renders: it is what
	// the page means to show.
	out, notes = Render(Input{Src: []byte("Reads [k](ds:cfg?id=" + appKey.ID + ").\n"), Defs: []block.Block{appKey}}, Options{})
	if string(out) != "Reads STRIPE_KEY.\n" || len(notes) != 0 {
		t.Errorf("address secret cfg = %q %+v", out, notes)
	}
}

// promise:secret-block-refused
func TestFragmentAndCloser(t *testing.T) {
	t.Parallel()
	ref := block.Reference{Verb: "block", ID: save.ID, Pos: block.Position{File: "d.md", Start: 3}, Carrier: block.CarrierBlock, Args: map[string]string{"id": save.ID, "lines": "1-2"}}
	text, ok, notes := Fragment(save, ref, "ds", 0)
	if !ok || len(notes) != 0 || !strings.Contains(text, "(internal/store/write.go#L12-L15)") || !strings.Contains(text, "```go\nfunc Save() {\n\t// legacy first\n```") {
		t.Errorf("fragment = %q ok=%v notes=%v", text, ok, notes)
	}
	if _, ok, notes := Fragment(secret, block.Reference{ID: secret.ID, Args: map[string]string{"id": secret.ID}}, "ds", 5); ok || len(notes) != 1 {
		t.Errorf("secret fragment = %v %v", ok, notes)
	}
	if text, ok, _ := Fragment(save, block.Reference{ID: save.ID}, "ds", 40); !ok || !strings.Contains(text, "return nil") {
		t.Errorf("nil args fragment = %q %v", text, ok)
	}
	if Closer("ds", "abc123") != "<!-- /ds:block hash=abc123 -->" {
		t.Error("closer")
	}
}

func TestNodesAndVerbRenderers(t *testing.T) {
	t.Parallel()
	ticket := func(ref block.Reference, inline bool) (string, bool, error) {
		switch ref.ID {
		case "T-1":
			if inline {
				return "[T-1 · open](https://tracker/T-1)", true, nil
			}
			return "> **T-1** open", true, nil
		case "T-boom":
			return "", false, errors.New("tracker down")
		}
		return "", false, nil
	}
	src := "Plain line.\nSee [T-1](ds:ticket?id=T-1) and [x](ds:ticket?id=T-boom) and [y](ds:ticket?id=T-none).\n<!-- ds:ticket id=T-1 -->\n<!-- ds:ticket id=T-boom -->\n<!-- ds:ticket id=T-none -->\n"
	nodes, notes := RenderNodes(Input{Src: []byte(src)}, Options{Verbs: map[string]VerbRenderer{"ticket": ticket}})
	if len(nodes) != 6 || nodes[0].Kind != NodeText || nodes[1].Kind != NodeProse || nodes[2].Kind != NodeBlock || nodes[2].Text != "> **T-1** open" || nodes[3].Kind != NodeText || nodes[4].Kind != NodeText {
		t.Errorf("nodes = %+v", nodes)
	}
	if nodes[1].Text != "See [T-1 · open](https://tracker/T-1) and [x](ds:ticket?id=T-boom) and [y](ds:ticket?id=T-none)." {
		t.Errorf("inline plugin = %q", nodes[1].Text)
	}
	if len(notes) != 2 || !strings.Contains(notes[0].Message, "tracker down") || notes[1].Line != 4 {
		t.Errorf("notes = %+v", notes)
	}
	out, _ := Render(Input{Src: []byte(src)}, Options{Verbs: map[string]VerbRenderer{"ticket": ticket}})
	if !strings.HasPrefix(string(out), "Plain line.\nSee [T-1 · open]") {
		t.Errorf("render joins nodes = %s", out)
	}
}

func TestBranchSelection(t *testing.T) {
	t.Parallel()
	main := def("api-a2b6f8jk", "x.go", 1, "main", map[string]string{block.KeyRepo: "api"})
	rel := def("api-a2b6f8jk", "x.go", 1, "release", map[string]string{block.KeyRepo: "api", block.KeyBranch: "release-1"})
	src := "<!-- ds:block id=api-a2b6f8jk -->\n<!-- ds:block id=api-a2b6f8jk branch=release-1 -->\n<!-- ds:block id=api-a2b6f8jk branch=nope -->\n[l](ds:block?id=api-a2b6f8jk&branch=release-1)\n"
	out, _ := Render(Input{Src: []byte(src), Defs: []block.Block{main, rel}}, Options{})
	if strings.Count(string(out), "```go\nmain\n```") != 2 || strings.Count(string(out), "```go\nrelease\n```") != 1 {
		t.Errorf("branch render:\n%s", out)
	}
	out, _ = Render(Input{Src: []byte(src), Defs: []block.Block{rel}}, Options{})
	if strings.Count(string(out), "```go\nrelease\n```") != 3 {
		t.Errorf("branch-only defs serve plain cites:\n%s", out)
	}
}

// TestRunOutputWithFences pins that a run's command and output are fenced
// with a run of backticks longer than any inside them. Output that contains
// a fence of its own — `cat README.md`, a markdown linter — closed the fixed
// three-backtick fence early and the rest of the page rendered as code.
func TestRunOutputWithFences(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	out, _ := render(t, "<!-- ds:run cmd=\"cat README.md\" expect=ok -->\nAfter the run.", Options{
		Runs: map[int]RunResult{1: {Output: "# Title\n```go\nx := 1\n```\n", OK: true, At: at}},
	})
	// Every fence the renderer opened is closed by an equal run, and the text
	// after the run is outside any fence.
	var open string
	for _, l := range strings.Split(out, "\n") {
		line := strings.TrimSpace(l)
		run := len(line) - len(strings.TrimLeft(line, "`"))
		switch {
		case open == "" && run >= 3:
			open = line[:run]
		case open != "" && line == open:
			open = ""
		}
		if l == "After the run." && open != "" {
			t.Fatalf("the text after the run is inside a fence:\n%s", out)
		}
	}
	if open != "" {
		t.Fatalf("a fence was left open:\n%s", out)
	}
	if !strings.Contains(out, "````") {
		t.Errorf("output holding a three-backtick fence needs a longer one:\n%s", out)
	}
}
