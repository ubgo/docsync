package scan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/internal/glob"
	"github.com/ubgo/docsync/pick"
)

func sets(t *testing.T, pats ...string) glob.Set {
	t.Helper()
	s, err := glob.CompileAll(pats)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func tree() fstest.MapFS {
	return fstest.MapFS{
		"internal/store/session.go": {Data: []byte("package store\n\n// ds:def id=save-k7m2p4xq owner=@auth\nfunc Save() {\n\treturn\n}\n")},
		"config/auth.yaml":          {Data: []byte("auth:\n  port: 8081 # ds:def id=port-h3v8n2wd\n")},
		"config/app.json":           {Data: []byte(`{"server":{"port":8443},"list":["a","b"]}`)},
		"docs/sessions.md":          {Data: []byte("---\nds:\n  covers: [save-k7m2p4xq]\n  review_every: 90d\n---\n# S\n\nSee [save](ds:block?id=save-k7m2p4xq) and [port](ds:cfg?id=port-h3v8n2wd).\n\n<!-- ds:def id=json-port-m4w8k2qn file=config/app.json pick=json:$.server.port type=int -->\n<!-- ds:def id=json-all-q7n2m4kt file=./config/app.json -->\n<!-- ds:def id=json-list-r9k1w5zb file=config/app.json pick=json:$.list -->\n<!-- ds:def id=missing-s2t6x8bc file=config/none.json -->\n<!-- ds:def id=badpick-t3u7y9cd file=config/app.json pick=json:$.nope -->\n<!-- ds:def id=escape-u4v8z2de file=../secret -->\n")},
		"docs/dup.md":               {Data: []byte("<!-- ds:def id=save-k7m2p4xq -->\nA paragraph that reuses an id.\n\n<!-- ds:def owner=noid -->\nExtractor problem gets the file name.\n\n<!-- ds:def id=remote-secret-c4d8h2lm file=secrets/prod.env pick=env:KEY -->\n")},
		"gen/types.pb.go":           {Data: []byte("// ds:def id=gen-v5w9a3ef\ntype T struct{}\n")},
		"secrets/prod.env":          {Data: []byte("KEY=abc # ds:def id=key-w6x2b4fg\n")},
		"vendor/lib/x.go":           {Data: []byte("// ds:def id=vend-x7y3c5gh\nfunc V() {}\n")},
		"node_modules/m/i.js":       {Data: []byte("// ds:def id=nm-y8z4d6hi\nfunction f() {}\n")},
		".git/config":               {Data: []byte("# ds:def id=git-z9a5e7ij\n[core]\n")},
		"build/out.md":              {Data: []byte("<!-- ds:def id=built-a2b6f8jk -->\nx\n")},
		"assets/logo.png":           {Data: []byte("\x89PNG\x00binary")},
		"big.txt":                   {Data: []byte(strings.Repeat("x", 600*1024))},
		"minified.js":               {Data: []byte("// ds:def id=min-b3c7g9kl\n" + strings.Repeat("a", 3000) + "\n")},
		"README.md":                 {Data: []byte("plain readme\n")},
	}
}

func opts(t *testing.T) Options {
	t.Helper()
	return Options{
		Prefix:    "ds",
		Repo:      "api",
		Include:   sets(t, "internal/**", "config/**", "docs/**", "gen/**", "secrets/**", "vendor/**", "node_modules/**", ".git/**", "build/**", "assets/**", "*.txt", "*.js", "README.md"),
		Exclude:   sets(t, "build/**"),
		Generated: sets(t, "**/*.pb.go"),
		Secret:    sets(t, "secrets/**"),
	}
}

func TestScan(t *testing.T) {
	t.Parallel()
	res, err := Scan(context.Background(), tree(), opts(t))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string][]block.Block{}
	for _, d := range res.Defs {
		byID[d.ID] = append(byID[d.ID], d)
	}
	// Local defs with file set.
	if s := byID["save-k7m2p4xq"]; len(s) != 2 || s[0].Pos.File != "docs/dup.md" || s[1].Pos.File != "internal/store/session.go" {
		t.Errorf("save defs = %+v", s)
	}
	if p := byID["port-h3v8n2wd"]; len(p) != 1 || p[0].Pos.File != "config/auth.yaml" || p[0].Symbol != "auth.port" {
		t.Errorf("port def = %+v", p)
	}
	// Remote defs resolved against the target file.
	if jp := byID["json-port-m4w8k2qn"]; len(jp) != 1 || jp[0].Content != "8443" || jp[0].Kind != block.KindKey || jp[0].Pos.File != "config/app.json" || jp[0].DirectivePos.File != "docs/sessions.md" || jp[0].Symbol != "json:$.server.port" {
		t.Errorf("json port = %+v", jp)
	}
	if ja := byID["json-all-q7n2m4kt"]; len(ja) != 1 || ja[0].Kind != block.KindFile || !strings.HasPrefix(ja[0].Content, `{"server"`) {
		t.Errorf("json whole file = %+v", ja)
	}
	if jl := byID["json-list-r9k1w5zb"]; len(jl) != 1 || jl[0].Kind != block.KindSpan || !strings.Contains(jl[0].Content, `"a"`) {
		t.Errorf("json list range = %+v", jl)
	}
	// Secret glob marks the def, including a remote def whose target is secret.
	if k := byID["key-w6x2b4fg"]; len(k) != 1 || !k[0].IsSecret() {
		t.Errorf("secret def = %+v", k)
	}
	// A value read out of a [secret] paths file is withheld: that file holds
	// values by definition, and the tool never renders one (§12). This line
	// used to assert the value "abc" came back, which is the leak itself --
	// render, facts, context, read and export all printed it. The hash is
	// kept, so a changed value is still detected.
	if rs := byID["remote-secret-c4d8h2lm"]; len(rs) != 1 || !rs[0].IsSecret() || rs[0].Content != "" || rs[0].Hash == "" {
		t.Errorf("remote secret def = %+v", rs)
	}
	// A local def in the same file is withheld the same way.
	if k := byID["key-w6x2b4fg"]; len(k) != 1 || k[0].Content != "" || k[0].Hash == "" {
		t.Errorf("globbed secret content = %+v", k)
	}
	// Generated file def exists but is flagged.
	if g := byID["gen-v5w9a3ef"]; len(g) != 1 {
		t.Errorf("generated def = %+v", g)
	}
	// Skipped directories and files never appear.
	for _, id := range []string{"vend-x7y3c5gh", "nm-y8z4d6hi", "git-z9a5e7ij", "built-a2b6f8jk", "min-b3c7g9kl"} {
		if _, ok := byID[id]; ok {
			t.Errorf("%s should have been skipped", id)
		}
	}
	// References carry the file.
	if len(res.Refs) != 2 || res.Refs[0].Pos.File != "docs/sessions.md" || res.Refs[0].Sentence == "" {
		t.Errorf("refs = %+v", res.Refs)
	}
	// Problems: duplicate (twice), generated, remote missing, remote pick, escape.
	kinds := map[error]int{}
	for _, p := range res.Problems {
		for _, s := range []error{ErrDuplicateID, ErrDefInGenerated, ErrRemoteMissing, ErrRemotePick} {
			if errors.Is(p.Err, s) {
				kinds[s]++
			}
		}
		if p.Pos.File == "" {
			t.Errorf("problem without file: %+v", p)
		}
	}
	if kinds[ErrDuplicateID] != 2 || kinds[ErrDefInGenerated] != 1 || kinds[ErrRemoteMissing] != 2 || kinds[ErrRemotePick] != 1 {
		t.Errorf("problem kinds = %v (%+v)", kinds, res.Problems)
	}
	extractorProblem := false
	for _, p := range res.Problems {
		if errors.Is(p.Err, extract.ErrNoID) && p.Pos.File == "docs/dup.md" && p.Pos.Start == 4 {
			extractorProblem = true
		}
	}
	if !extractorProblem {
		t.Errorf("extractor problem must carry the file: %+v", res.Problems)
	}
	// Skips with reasons.
	reasons := map[SkipReason][]string{}
	for _, s := range res.Skipped {
		reasons[s.Reason] = append(reasons[s.Reason], s.File)
	}
	if reasons[SkipExcluded][0] != "build/out.md" || reasons[SkipTooLarge][0] != "big.txt" || reasons[SkipLongLine][0] != "minified.js" || reasons[SkipBinary][0] != "assets/logo.png" {
		t.Errorf("skips = %v", reasons)
	}
	if res.Files == 0 || res.Tier["docs/sessions.md"] != "markdown" || res.Tier["config/auth.yaml"] != "config" {
		t.Errorf("files/tier = %d %v", res.Files, res.Tier)
	}
	if pg, ok := res.Pages["docs/sessions.md"]; !ok || len(pg.Covers) != 1 || pg.Covers[0] != "save-k7m2p4xq" || pg.ReviewEvery != "90d" {
		t.Errorf("pages = %+v", res.Pages)
	}
	// Deterministic ordering: defs by directive file then line.
	for i := 1; i < len(res.Defs); i++ {
		a, b := res.Defs[i-1].DirectivePos, res.Defs[i].DirectivePos
		if a.File > b.File || (a.File == b.File && a.Start > b.Start) {
			t.Fatalf("defs not sorted at %d: %v %v", i, a, b)
		}
	}
	if len(SkipReasonValues) != 6 {
		t.Error("SkipReasonValues")
	}
}

func TestScanNotIncludedAndNoInclude(t *testing.T) {
	t.Parallel()
	o := opts(t)
	o.Include = sets(t, "docs/**")
	res, err := Scan(context.Background(), tree(), o)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range res.Skipped {
		if s.File == "config/auth.yaml" && s.Reason == SkipNotIn {
			found = true
		}
	}
	if !found {
		t.Error("non-included file must be reported as skipped")
	}
	o.Include = nil
	if _, err := Scan(context.Background(), tree(), o); !errors.Is(err, ErrNoInclude) {
		t.Errorf("empty include err = %v", err)
	}
}

func TestScanCancelAndWalkError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, tree(), opts(t)); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel err = %v", err)
	}
	// A walk error surfaces.
	if _, err := Scan(context.Background(), badFS{}, opts(t)); err == nil {
		t.Error("walk error must surface")
	}
}

// badFS fails to open anything so WalkDir reports an error at the root.
type badFS struct{}

func (badFS) Open(string) (fs.File, error) { return nil, errors.New("boom") }

func TestReadFileErrors(t *testing.T) {
	t.Parallel()
	m := fstest.MapFS{"a.txt": {Data: []byte("ok\n")}}
	o := Options{MaxFileKB: 1, MaxLineChars: 10}
	if _, r := readFile(m, "missing.txt", o); r != SkipReadError {
		t.Errorf("missing = %s", r)
	}
	if src, r := readFile(m, "a.txt", o); r != "" || string(src) != "ok\n" {
		t.Errorf("ok file = %q %s", src, r)
	}
	// Stat succeeds but the read fails: still a read-error skip, not a crash.
	if _, r := readFile(readFailFS{m}, "a.txt", o); r != SkipReadError {
		t.Errorf("read failure = %s", r)
	}
}

// readFailFS stats normally but every Read returns an error.
type readFailFS struct{ fs.FS }

func (r readFailFS) Open(name string) (fs.File, error) {
	f, err := r.FS.Open(name)
	if err != nil {
		return nil, err
	}
	return readFailFile{f}, nil
}

type readFailFile struct{ fs.File }

func (readFailFile) Read([]byte) (int, error) { return 0, errors.New("io failure") }

func TestCustomRegistryWithoutFallback(t *testing.T) {
	t.Parallel()
	o := opts(t)
	o.Registry = extract.NewRegistry(extract.Markdown{})
	res, err := Scan(context.Background(), tree(), o)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Skipped {
		if s.File == "internal/store/session.go" && s.Reason == SkipNotIn {
			return
		}
	}
	t.Error("file with no extractor must be reported as skipped")
}

func TestWithKeyDoesNotMutate(t *testing.T) {
	t.Parallel()
	orig := map[string]string{"a": "1"}
	out := withKey(orig, "b", "2")
	if _, ok := orig["b"]; ok || out["b"] != "2" || out["a"] != "1" {
		t.Error("withKey")
	}
	if out := withKey(nil, "k", "v"); out["k"] != "v" {
		t.Error("withKey nil")
	}
}

// keyStub is a tier that labels a separator-less line as a key, which the
// built-in config tier never does; it pins that the default pick leaves
// such content alone instead of blanking it.
type keyStub struct{}

func (keyStub) Name() string        { return "keystub" }
func (keyStub) Match(p string) bool { return strings.HasSuffix(p, "k.txt") }
func (keyStub) Extract(string, []byte, string) extract.Found {
	b := block.Block{ID: "nosep-a2b6f8jk", Kind: block.KindKey, Pos: block.Position{Start: 1, End: 1}, Args: map[string]string{"id": "nosep-a2b6f8jk"}}
	b.SetContent("nosep")
	return extract.Found{Defs: []extract.Def{{Block: b}}}
}

func TestApplyPick(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"c.yaml": {Data: []byte("auth:\n  port: 8081   # ds:def id=port-a2b6f8jk\n  host: \"h\" # ds:def id=host-b3c7g9kl pick=regex:'\"(.*)\"'\n")},
		"a.go":   {Data: []byte("// ds:def id=fn-c4d8h2lm pick=line:2\nfunc A() {\n\treturn 1\n}\n\n// ds:def id=bad-d5e9j3mn pick=regex:'zzz'\nfunc B() {}\n\n// ds:def id=url-e6f2k4np pick=url\n// see https://example.com/x\nvar u = 1\n\n// ds:def id=rng-f7g3l5pq pick=file\nfunc C() {\n\tx := 1\n\treturn x\n}\n")},
		"k.txt":  {Data: []byte("nosep\n")},
	}
	inc, _ := glob.CompileAll([]string{"**"})
	reg := extract.Default()
	reg.Prepend(keyStub{})
	res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]block.Block{}
	for _, b := range res.Defs {
		got[b.ID] = b
	}
	if got["port-a2b6f8jk"].Content != "8081" || got["port-a2b6f8jk"].Kind != block.KindKey {
		t.Errorf("default key pick = %+v", got["port-a2b6f8jk"])
	}
	if got["host-b3c7g9kl"].Content != "h" {
		t.Errorf("explicit regex pick = %+v", got["host-b3c7g9kl"])
	}
	if b := got["fn-c4d8h2lm"]; b.Content != "\treturn 1" || b.Pos.Start != 2 || b.Pos.End != 4 {
		t.Errorf("value pick keeps the position = %+v", b)
	}
	if b := got["rng-f7g3l5pq"]; b.Content != "func C() {\n\tx := 1\n\treturn x\n}" || b.Pos.Start != 14 || b.Pos.End != 17 {
		t.Errorf("range pick maps positions into the file = %+v", b)
	}
	if b := got["nosep-a2b6f8jk"]; b.Content != "nosep" {
		t.Errorf("key without separator keeps its text = %+v", b)
	}
	if b := got["bad-d5e9j3mn"]; !strings.HasPrefix(b.Content, "func B") {
		t.Errorf("failed pick keeps the text = %+v", b)
	}
	found := false
	for _, p := range res.Problems {
		if errors.Is(p.Err, ErrPick) && p.Pos.Start == 6 {
			found = true
		}
	}
	if !found {
		t.Errorf("failed pick must be a problem: %+v", res.Problems)
	}
	if got["url-e6f2k4np"].Content != "" {
		// pick=url on a var line with no url: pick fails and content stays.
		if !strings.HasPrefix(got["url-e6f2k4np"].Content, "var u") {
			t.Errorf("url pick without url = %+v", got["url-e6f2k4np"])
		}
	}
}

func TestEnvDuplicatesAndLocal(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"prod.yaml":    {Data: []byte("host: p   # ds:def id=host-a2b6f8jk env=prod\n")},
		"staging.yaml": {Data: []byte("host: s   # ds:def id=host-a2b6f8jk env=staging\n")},
		"again.yaml":   {Data: []byte("host: s2  # ds:def id=host-a2b6f8jk env=staging\n")},
		"defs.md":      {Data: []byte("<!-- ds:def id=laptop-m2q1s8vt file=/Users/me/.env.prod local=true secret=true truth=true -->\n")},
	}
	inc, _ := glob.CompileAll([]string{"**"})
	res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc})
	if err != nil {
		t.Fatal(err)
	}
	dups := 0
	for _, p := range res.Problems {
		if errors.Is(p.Err, ErrDuplicateID) {
			dups++
			if p.Pos.File == "prod.yaml" {
				t.Error("prod is the only prod def and must not be a duplicate")
			}
		}
	}
	if dups != 2 {
		t.Errorf("duplicates = %d, want the two staging defs", dups)
	}
	var local block.Block
	for _, b := range res.Defs {
		if b.ID == "laptop-m2q1s8vt" {
			local = b
		}
	}
	if local.ID == "" || local.Content != "" || local.Kind != block.KindFile || local.Pos.File != "/Users/me/.env.prod" || local.DirectivePos.File != "defs.md" || !local.IsTruth() {
		t.Errorf("local def = %+v", local)
	}
	for _, p := range res.Problems {
		if p.Pos.File == "defs.md" {
			t.Errorf("local def must not be a problem: %v", p.Err)
		}
	}
}

// errFS fails every Open so walk errors have a source.
type errFS struct{}

func (errFS) Open(string) (fs.File, error) { return nil, errors.New("boom") }

func TestCountMatches(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"a.go": {Data: []byte("x")}, "docs/b.md": {Data: []byte("x")}, "node_modules/c.go": {Data: []byte("x")}}
	if n, err := CountMatches(fsys, "**/*.go"); err != nil || n != 1 {
		t.Errorf("go files = %d %v (node_modules must be skipped)", n, err)
	}
	if n, _ := CountMatches(fsys, "docs/**"); n != 1 {
		t.Errorf("docs = %d", n)
	}
	if n, _ := CountMatches(fsys, "nothing/**"); n != 0 {
		t.Errorf("no match = %d", n)
	}
	if _, err := CountMatches(fsys, "["); err == nil {
		t.Error("bad pattern")
	}
	if _, err := CountMatches(errFS{}, "**"); err == nil {
		t.Error("walk error")
	}
}

// promise:ds-dir-not-scanned
func TestStateDirSkipped(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		".ds/journal.tsv": {Data: []byte("1\ta.go\t1\t\t// ds:def id=ghost-a2b6f8jk\n")},
		".ds/ledger.tsv":  {Data: []byte("ds:def id=ghost2-b3c7g9kl\n")},
		"a.go":            {Data: []byte("package a\n\n// ds:def id=real-c4d8h2lm\nvar A = 1\n")},
	}
	inc, _ := glob.CompileAll([]string{"**"})
	res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Defs) != 1 || res.Defs[0].ID != "real-c4d8h2lm" {
		t.Errorf("state dir must not be scanned: %+v", res.Defs)
	}
	if n, _ := CountMatches(fsys, "**"); n != 1 {
		t.Errorf("CountMatches skips the state dir too: %d", n)
	}
}

func TestMatchAny(t *testing.T) {
	t.Parallel()
	if ok, err := MatchAny([]string{"runbooks/**"}, "runbooks/a.md"); err != nil || !ok {
		t.Errorf("match = %v %v", ok, err)
	}
	if ok, _ := MatchAny([]string{"runbooks/**"}, "docs/a.md"); ok {
		t.Error("no match")
	}
	if _, err := MatchAny([]string{"["}, "x"); err == nil {
		t.Error("bad pattern")
	}
}

func TestPickerPlugins(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"infra/main.tf": {Data: []byte("# ds:def id=bucket-a2b6f8jk pick=hcl:resource.aws_s3_bucket.name\nresource \"aws_s3_bucket\" \"logs\" {}\n")},
		"docs/f.md":     {Data: []byte("<!-- ds:def id=remote-b3c7g9kl file=infra/main.tf pick=hcl:x -->\n")},
	}
	inc, _ := glob.CompileAll([]string{"**"})
	hcl := func(arg, content string) (pick.Result, error) {
		return pick.Result{Kind: pick.KindValue, Value: "logs:" + arg, Start: 2, End: 2}, nil
	}
	res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc, Pickers: map[string]pick.Picker{"hcl": hcl}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, b := range res.Defs {
		got[b.ID] = b.Content
	}
	if got["bucket-a2b6f8jk"] != "logs:resource.aws_s3_bucket.name" || got["remote-b3c7g9kl"] != "logs:x" {
		t.Errorf("plugin picks = %v", got)
	}
	res, _ = Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc})
	if len(res.Problems) != 2 {
		t.Errorf("without the plugin both picks fail: %+v", res.Problems)
	}
}

type memCache struct {
	m         map[string]extract.Found
	hits, put int
}

func (c *memCache) Get(path, hash string) (extract.Found, bool) {
	f, ok := c.m[path+"@"+hash]
	if ok {
		c.hits++
	}
	return f, ok
}

func (c *memCache) Put(path, hash string, f extract.Found) {
	c.put++
	c.m[path+"@"+hash] = f
}

func TestCacheAndParallel(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{}
	for i := 0; i < 40; i++ {
		fsys[fmt.Sprintf("p%02d/f.go", i)] = &fstest.MapFile{Data: []byte(fmt.Sprintf("package p\n\n// ds:def id=f%02d-a2b6f8jk\nfunc F() {}\n", i))}
	}
	fsys["bad.go"] = &fstest.MapFile{Data: []byte("package p\n\n// ds:def\nfunc G() {}\n")}
	inc, _ := glob.CompileAll([]string{"**"})
	c := &memCache{m: map[string]extract.Found{}}
	first, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc, Cache: c, Workers: 4})
	if err != nil || len(first.Defs) != 40 || c.put != 40 || c.hits != 0 {
		t.Fatalf("first scan = %d defs put=%d hits=%d %v (files with problems are not cached)", len(first.Defs), c.put, c.hits, err)
	}
	second, _ := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc, Cache: c, Workers: 1})
	if c.hits != 40 || len(second.Defs) != 40 || len(second.Problems) != 1 {
		t.Errorf("second scan hits=%d defs=%d problems=%d", c.hits, len(second.Defs), len(second.Problems))
	}
	for i := range first.Defs {
		if first.Defs[i].ID != second.Defs[i].ID || first.Defs[i].Pos != second.Defs[i].Pos {
			t.Fatalf("order differs at %d", i)
		}
	}
	fsys["p00/f.go"] = &fstest.MapFile{Data: []byte("package p\n\n// ds:def id=f00-a2b6f8jk\nfunc F() { changed() }\n")}
	third, _ := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc, Cache: c})
	if c.put != 41 || third.Defs[0].Content != "func F() { changed() }" {
		t.Errorf("changed file re-extracted: put=%d content=%q", c.put, third.Defs[0].Content)
	}
}

// promise:file-confined
func TestSymlinkTargetRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real.json"), []byte(`{"v":"1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "real.json"), filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs/f.md"), []byte("<!-- ds:def id=a-a2b6f8jk file=link.json pick=json:v -->\n<!-- ds:def id=b-b3c7g9kl file=real.json pick=json:v -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inc, _ := glob.CompileAll([]string{"**"})
	res, err := Scan(context.Background(), os.DirFS(dir), Options{Prefix: "ds", Include: inc})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Defs) != 1 || res.Defs[0].ID != "b-b3c7g9kl" {
		t.Errorf("only the real file binds: %+v", res.Defs)
	}
	found := false
	for _, p := range res.Problems {
		if errors.Is(p.Err, ErrSymlink) {
			found = true
		}
	}
	if !found {
		t.Errorf("symlink must be refused: %+v", res.Problems)
	}
}

// TestUnknownStabilityIsAFinding makes true what block.Stability's doc
// comment already claimed: that the scanner reports an invalid policy. It
// did not — ParseStability had exactly one caller, which fell back to the
// default and said nothing, so `stability=frozan` silently became `stable`
// and a block the author wanted to flag on every touch stopped flagging on
// most of them. Found by running docsync on its own source.
func TestUnknownStabilityIsAFinding(t *testing.T) {
	t.Parallel()
	const src = "package p\n\n// ds:def id=a-k7m2p4xq stability=frozan\nfunc A() {}\n\n// ds:def id=b-h3v8n2wd stability=frozen\nfunc B() {}\n\n// ds:def id=c-t4k2b9rf\nfunc C() {}\n"
	fsys := fstest.MapFS{"a.go": &fstest.MapFile{Data: []byte(src)}}
	inc, _ := glob.CompileAll([]string{"**"})
	res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc})
	if err != nil {
		t.Fatal(err)
	}
	// Only the typo is a problem; a valid policy and an absent one are not.
	if len(res.Problems) != 1 || !errors.Is(res.Problems[0].Err, ErrBadStability) {
		t.Fatalf("problems = %+v", res.Problems)
	}
	if got := res.Problems[0].Pos.Start; got != 3 {
		t.Errorf("the finding points at the directive, not the block: line %d", got)
	}
	// The message names the value and every accepted one, so the fix does
	// not need the spec open beside it.
	msg := res.Problems[0].Err.Error()
	if !strings.Contains(msg, `"frozan"`) {
		t.Errorf("message must quote the bad value: %s", msg)
	}
	for _, s := range block.StabilityValues {
		if !strings.Contains(msg, string(s)) {
			t.Errorf("message must list %s: %s", s, msg)
		}
	}
	// The def is still scanned; an unknown policy is a finding, not a reason
	// to drop the block and report it as missing everywhere it is cited.
	if len(res.Defs) != 3 {
		t.Errorf("defs = %d, want 3", len(res.Defs))
	}
}

// TestUnreadableReasons pins which skips are about a file's form, and so
// keep its state and get reported, versus a choice made in config, which
// drops it on purpose. Adding a reason means deciding which it is here.
func TestUnreadableReasons(t *testing.T) {
	t.Parallel()
	want := map[SkipReason]bool{SkipTooLarge: true, SkipLongLine: true, SkipBinary: true, SkipReadError: true, SkipExcluded: false, SkipNotIn: false}
	for _, r := range SkipReasonValues {
		w, ok := want[r]
		if !ok {
			t.Fatalf("%s has no decision here: is a file skipped for it unreadable, or excluded on purpose?", r)
		}
		if r.Unreadable() != w {
			t.Errorf("%s.Unreadable() = %v, want %v", r, r.Unreadable(), w)
		}
	}
}

// TestProseIsNotSkippedForLongLines pins the exemption: a markdown doc with
// a paragraph longer than the line limit is scanned, while code with the
// same line is still skipped as minified.
func TestProseIsNotSkippedForLongLines(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("word ", 1000)
	fsys := fstest.MapFS{
		"docs/d.md": {Data: []byte("# D\n\n" + long + "\n\nSee [a](ds:block?id=a-k7m2p4xq).\n")},
		"min.js":    {Data: []byte("var x = \"" + long + "\";\n")},
	}
	inc, _ := glob.CompileAll([]string{"**"})
	res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Refs) != 1 {
		t.Errorf("the doc must be scanned: refs = %+v", res.Refs)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].File != "min.js" || res.Skipped[0].Reason != SkipLongLine {
		t.Errorf("skipped = %+v, want only min.js as long-line", res.Skipped)
	}
}

// TestIDWithWhitespaceIsAFinding pins that an id a link could never carry is
// reported instead of accepted in silence, and the def is still scanned so
// its citations do not all turn broken over it.
func TestIDWithWhitespaceIsAFinding(t *testing.T) {
	t.Parallel()
	const src = "package p\n\n// ds:def id=\"bad id\"\nfunc A() {}\n\n// ds:def id=\"tab\there\"\nfunc B() {}\n\n// ds:def id=good-k7m2p4xq\nfunc C() {}\n"
	inc, _ := glob.CompileAll([]string{"**"})
	res, err := Scan(context.Background(), fstest.MapFS{"a.go": {Data: []byte(src)}}, Options{Prefix: "ds", Include: inc})
	if err != nil {
		t.Fatal(err)
	}
	bad := 0
	for _, p := range res.Problems {
		if errors.Is(p.Err, ErrBadID) {
			bad++
		}
	}
	if bad != 2 {
		t.Errorf("bad ids reported = %d, want 2: %+v", bad, res.Problems)
	}
	if len(res.Defs) != 3 {
		t.Errorf("defs = %d, want all three still scanned", len(res.Defs))
	}
}

// TestRemoteTargetHonoursReadLimits pins that a file= target is read like
// any scanned file: over max_file_kb or binary, it is a problem at the
// directive and nothing binds. A plain read loaded any size whole and
// picked through binary bytes.
func TestRemoteTargetHonoursReadLimits(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"defs.txt":  {Data: []byte("ds:def id=ok-a2b6f8jk file=small.txt\nds:def id=big-h3v8n2wd file=big.txt\nds:def id=bin-k7m2p4xq file=blob.bin\nds:def id=gone-t4k2b9rf file=missing.txt\n")},
		"small.txt": {Data: []byte("\xef\xbb\xbfhello\n")},
		"big.txt":   {Data: []byte(strings.Repeat("x", 2048) + "\n")},
		"blob.bin":  {Data: []byte("text\x00more\n")},
	}
	inc, _ := glob.CompileAll([]string{"**"})
	res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc, MaxFileKB: 1})
	if err != nil {
		t.Fatal(err)
	}
	bound := map[string]string{}
	for _, b := range res.Defs {
		bound[b.ID] = b.Content
	}
	if bound["ok-a2b6f8jk"] != "hello" || len(bound) != 1 {
		t.Errorf("only the readable target binds, BOM stripped: %v", bound)
	}
	want := map[string]error{"big.txt": ErrRemoteSkipped, "blob.bin": ErrRemoteSkipped, "missing.txt": ErrRemoteMissing}
	for _, p := range res.Problems {
		for file, sentinel := range want {
			if errors.Is(p.Err, sentinel) && strings.Contains(p.Err.Error(), file) {
				delete(want, file)
			}
		}
	}
	if len(want) != 0 {
		t.Errorf("unreported targets: %v\nproblems: %v", want, res.Problems)
	}
}

// TestSharedBlockIsAFinding covers two ids bound to the same lines. Every
// later pass hashes the block, so two ids on one block are indistinguishable:
// a sentence citing either is measured against the other's, and a change
// flags both or neither. It is almost always a directive that missed what its
// author aimed it at and landed on a neighbour (bug 18).
// promise:def-binds-below
func TestSharedBlockIsAFinding(t *testing.T) {
	t.Parallel()
	const src = "package p\n\n// ds:def id=first-k7m2p4xq\n// ds:def id=second-h3v8n2wd\nconst Shared = 1\n\n// ds:def id=alone-t4k2b9rf\nconst Alone = 2\n"
	fsys := fstest.MapFS{"a.go": &fstest.MapFile{Data: []byte(src)}}
	inc, _ := glob.CompileAll([]string{"**"})
	res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc})
	if err != nil {
		t.Fatal(err)
	}
	// Both directives are reported, because the scanner cannot know which one
	// went astray, and neither is named as the original.
	if len(res.Problems) != 2 {
		t.Fatalf("problems = %+v", res.Problems)
	}
	for i, p := range res.Problems {
		if !errors.Is(p.Err, ErrSharedBlock) {
			t.Errorf("problem %d = %v", i, p.Err)
		}
		// The message names both ids and the block, so the remedy does not
		// need a second command to find the other one.
		for _, want := range []string{"first-k7m2p4xq", "second-h3v8n2wd", "a.go:5-5"} {
			if !strings.Contains(p.Err.Error(), want) {
				t.Errorf("problem %d must name %q: %v", i, want, p.Err)
			}
		}
	}
	if got := []int{res.Problems[0].Pos.Start, res.Problems[1].Pos.Start}; got[0] != 3 || got[1] != 4 {
		t.Errorf("reported at %v, want the two directive lines 3 and 4", got)
	}
	// Three defs survive: a finding never drops a block, or every citation of
	// it would report broken as well.
	if len(res.Defs) != 3 {
		t.Errorf("defs = %d, want 3", len(res.Defs))
	}
}

// TestSharedBlockExemptsExtentlessDefs pins the other side: a def with no
// line range shares nothing, and several may legitimately name one target.
func TestSharedBlockExemptsExtentlessDefs(t *testing.T) {
	t.Parallel()
	// Two local defs on the same file: local=true means the target is not read
	// here, so both are positioned at the file with no range (§9.1, §12).
	const src = "package p\n\n// ds:def id=one-k7m2p4xq file=secrets.env local=true\n// ds:def id=two-h3v8n2wd file=secrets.env local=true\nvar x = 1\n"
	fsys := fstest.MapFS{"a.go": &fstest.MapFile{Data: []byte(src)}}
	inc, _ := glob.CompileAll([]string{"**"})
	res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Problems {
		if errors.Is(p.Err, ErrSharedBlock) {
			t.Errorf("an extentless def shares no block: %v", p.Err)
		}
	}
}

// TestSharedBlockEdges enumerates what does and does not count as two ids on
// one block, because a false positive here is a finding on correct code.
func TestSharedBlockEdges(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		files fstest.MapFS
		want  int
	}{
		// Three ids on one block report three times, one per directive: the
		// scanner cannot know which one went astray, so it names none as the
		// original.
		"three ids on one block": {fstest.MapFS{"a.go": file("package p\n\n// ds:def id=a-k7m2p4xq\n// ds:def id=b-h3v8n2wd\n// ds:def id=c-t4k2b9rf\nconst A = 1\n")}, 3},
		// One id written twice is ErrDuplicateID, which says it precisely.
		// Reporting it here as well would print the id twice in one message
		// and double the problem count.
		"one id written twice": {fstest.MapFS{"a.go": file("package p\n\n// ds:def id=same-k7m2p4xq\n// ds:def id=same-k7m2p4xq\nconst A = 1\n")}, 0},
		// The same line numbers in different files are different blocks.
		"same lines, different files": {fstest.MapFS{
			"a.go": file("package p\n\n// ds:def id=a-k7m2p4xq\nconst A = 1\n"),
			"b.go": file("package p\n\n// ds:def id=b-h3v8n2wd\nconst B = 2\n"),
		}, 0},
		// One def per id per environment, so two environments are two blocks
		// as far as every later pass is concerned.
		"different environments": {fstest.MapFS{"a.go": file("package p\n\n// ds:def id=a-k7m2p4xq env=prod\n// ds:def id=b-h3v8n2wd env=staging\nconst A = 1\n")}, 0},
		// Adjacent declarations are not shared, however close.
		"adjacent declarations": {fstest.MapFS{"a.go": file("package p\n\n// ds:def id=a-k7m2p4xq\nconst A = 1\n\n// ds:def id=b-h3v8n2wd\nconst B = 2\n")}, 0},
		// Two entries of one group are two blocks now, which is the fix: they
		// used to land on the same line and would be reported here.
		"two entries of one group": {fstest.MapFS{"a.go": file("package p\n\nconst (\n\t// ds:def id=a-k7m2p4xq\n\tA = 1\n\t// ds:def id=b-h3v8n2wd\n\tB = 2\n)\n")}, 0},
	} {
		t.Run(name, func(t *testing.T) {
			inc, _ := glob.CompileAll([]string{"**"})
			res, err := Scan(context.Background(), tc.files, Options{Prefix: "ds", Include: inc})
			if err != nil {
				t.Fatal(err)
			}
			got := 0
			for _, p := range res.Problems {
				if errors.Is(p.Err, ErrSharedBlock) {
					got++
				}
			}
			if got != tc.want {
				t.Errorf("shared-block findings = %d, want %d (all problems: %+v)", got, tc.want, res.Problems)
			}
		})
	}
}

// file is the one-liner the tables above use for a MapFS entry.
func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

// TestBareDirectiveIsAFinding covers damage already written into a tree. `ds
// def` used to insert a line holding only the directive into any file type it
// did not recognise; in a go.work that stopped every build in the workspace
// with "unknown directive". The write is refused now, but repositories already
// hit need to find the places, and a scan that accepts the line silently is no
// help at all.
func TestBareDirectiveIsAFinding(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		file string
		src  string
		want bool
	}{
		// go.work and go.mod take // comments, so a bare line there is damage.
		"bare line in go.work": {"go.work", "ds:def id=a-k7m2p4xq\ngo 1.26\n\nuse .\n", true},
		"bare line in go.mod":  {"go.mod", "ds:def id=a-k7m2p4xq\nmodule example.com/m\n", true},
		// A commented one in the same file is how it should look.
		"commented in go.work": {"go.work", "// ds:def id=a-k7m2p4xq\ngo 1.26\n\nuse .\n", false},
		// JSON and CSV have no comment syntax, so a directive line breaks the
		// file outright. These are the formats the old fallback reached.
		"bare line in json": {"p.json", "ds:def id=a-k7m2p4xq\n{\"a\": 1}\n", true},
		"bare line in csv":  {"d.csv", "ds:def id=a-k7m2p4xq\na,b\n", true},
		// Plain text carries a directive as a bare line by design, so the same
		// shape there is correct and must not be reported.
		"bare line in plain text": {"notes.txt", "ds:def id=a-k7m2p4xq\nline\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			fsys := fstest.MapFS{tc.file: file(tc.src)}
			inc, _ := glob.CompileAll([]string{"**"})
			res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc})
			if err != nil {
				t.Fatal(err)
			}
			got := false
			for _, p := range res.Problems {
				if errors.Is(p.Err, ErrBareDirective) {
					got = true
					// The reader has to be sent to the line to fix.
					if p.Pos.File != tc.file || p.Pos.Start != 1 {
						t.Errorf("reported at %s:%d, want %s:1", p.Pos.File, p.Pos.Start, tc.file)
					}
				}
			}
			if got != tc.want {
				t.Errorf("reported = %v, want %v (problems %+v)", got, tc.want, res.Problems)
			}
			// The def is kept either way: a finding never drops a block, or
			// every citation of it would report broken as well.
			if len(res.Defs) != 1 {
				t.Errorf("defs = %d, want 1", len(res.Defs))
			}
		})
	}
}

// TestSecretContentIsWithheld is the leak found while testing bug 16: render,
// facts, context, read and export all printed a secret's value, because a
// secret def's content was whatever the line held. The scanner now blanks it
// unless it is an address, keeping the hash so a changed value still flags.
// Every downstream consumer reads Content, so this one place covers them all.
func TestSecretContentIsWithheld(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		// A real value marked secret: withheld.
		"app.yaml": file("api_key: sk-live-VALUE # ds:def id=raw-k7m2p4xq secret=true\n"),
		// An address: safe to render, per the spec.
		"tpl.env": file("KEY=op://Platform/stripe/credential # ds:def id=op-h3v8n2wd secret=true\n"),
		// A pick that narrows a reference to its name: still an address.
		"deploy.yml": file("STRIPE_KEY: ${{ secrets.STRIPE_KEY }} # ds:def id=gh-t4k2b9rf secret=true pick=regex:'secrets\\.(\\w+)'\n"),
		// source= vouches for a bare env name on a def that declares itself
		// secret, as the spec asks.
		"main.go": file("package p\n\n// ds:def id=env-b3c7g9kl secret=true source=env\nvar key = os.Getenv(\"STRIPE_KEY\")\n"),
		// ...but not inside a [secret] paths file, which holds values.
		"secrets/prod.env": file("KEY=sk-live-GLOBBED # ds:def id=glob-w8n4r6vc source=env\n"),
		// Not secret at all: untouched.
		"plain.yaml": file("port: 8080 # ds:def id=port-p2c4y7mk\n"),
	}
	inc, _ := glob.CompileAll([]string{"**"})
	sec, _ := glob.CompileAll([]string{"secrets/**"})
	res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc, Secret: sec})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]block.Block{}
	for _, d := range res.Defs {
		byID[d.ID] = d
	}
	for id, want := range map[string]string{
		"raw-k7m2p4xq":  "",
		"op-h3v8n2wd":   "op://Platform/stripe/credential",
		"gh-t4k2b9rf":   "STRIPE_KEY",
		"env-b3c7g9kl":  `var key = os.Getenv("STRIPE_KEY")`,
		"glob-w8n4r6vc": "",
		"port-p2c4y7mk": "8080",
	} {
		b, ok := byID[id]
		if !ok {
			t.Errorf("no def %s", id)
			continue
		}
		if b.Content != want {
			t.Errorf("%s content = %q, want %q", id, b.Content, want)
		}
		// Withheld content keeps its hash: a rotated value is still detected.
		if b.Hash == "" {
			t.Errorf("%s lost its hash", id)
		}
	}
	// The withheld value appears nowhere in the result.
	for _, d := range res.Defs {
		if strings.Contains(d.Content, "sk-live") {
			t.Errorf("%s leaks its value: %q", d.ID, d.Content)
		}
	}
	// And two different values still hash differently, so a change flags.
	a, _ := Scan(context.Background(), fstest.MapFS{"a.yaml": file("k: one # ds:def id=raw-k7m2p4xq secret=true\n")}, Options{Prefix: "ds", Include: inc})
	b, _ := Scan(context.Background(), fstest.MapFS{"a.yaml": file("k: two # ds:def id=raw-k7m2p4xq secret=true\n")}, Options{Prefix: "ds", Include: inc})
	if a.Defs[0].Hash == b.Defs[0].Hash {
		t.Error("a withheld secret must still change hash when its value changes")
	}
}
