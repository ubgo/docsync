package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/extract"
)

func frame(msgs ...string) string {
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "Content-Length: %d\r\n\r\n%s", len(m), m)
	}
	return b.String()
}

func lspRun(t *testing.T, dir string, v VCS, input string) []rpcResponse {
	t.Helper()
	var out, errb bytes.Buffer
	Run([]string{"lsp"}, WithDir(dir), WithIO(strings.NewReader(input), &out, &errb), WithVCS(v), WithClock(func() time.Time { return clock }))
	r := bufio.NewReader(&out)
	var resps []rpcResponse
	for {
		raw, err := readMessage(r)
		if err != nil {
			break
		}
		var resp rpcResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("bad response %q: %v", raw, err)
		}
		if len(resp.ID) == 0 && resp.Error == nil {
			// Server notifications (diagnostics) carry no id; they are
			// checked by TestLSPDiagnostics.
			continue
		}
		resps = append(resps, resp)
	}
	return resps
}

func docReq(id int, method, uri string, line int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"%s","params":{"textDocument":{"uri":"%s"},"position":{"line":%d,"character":3}}}`, id, method, uri, line)
}

func TestLSP(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "docs/cover.md", "---\nds:\n  covers: [sess-save-k7m2p4xq]\n---\n# Cover\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	abs, _ := filepath.Abs(dir)
	docURI := "file://" + filepath.Join(abs, "docs/sessions.md")
	goURI := "file://" + filepath.Join(abs, "internal/store/write.go")
	src, _ := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	v.files["abc1234:internal/store/write.go"] = src
	write(t, dir, "internal/store/write.go", goV2)
	resps := lspRun(t, dir, v, frame(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"initialized"}`,
		docReq(2, "textDocument/codeLens", goURI, 0),
		docReq(3, "textDocument/hover", docURI, 0),
		docReq(4, "textDocument/hover", docURI, 1),
		docReq(5, "textDocument/hover", docURI, 4),
		docReq(6, "textDocument/definition", docURI, 0),
		docReq(7, "textDocument/definition", docURI, 4),
		docReq(8, "textDocument/definition", docURI, 1),
		`{"jsonrpc":"2.0","id":9,"method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+docURI+`","text":"Fresh [x](ds:cfg?id=auth-port-h3v8n2wd).\n"}}}`,
		docReq(10, "textDocument/hover", docURI, 0),
		`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"`+docURI+`"},"contentChanges":[{"text":"Changed [x](ds:block?id=sess-save-k7m2p4xq).\n"}]}}`,
		docReq(11, "textDocument/definition", docURI, 0),
		`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"`+docURI+`"},"contentChanges":[]}}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"`+goURI+`","text":"package store\n\n// ds:def id=sess-save-k7m2p4xq\nfunc Save() {}\n\n// ds:def id=fresh-b3c7g9kl\nfunc Fresh() {}\n"}}}`,
		docReq(15, "textDocument/codeLens", goURI, 0),
		`{"jsonrpc":"2.0","method":"textDocument/didClose","params":{"textDocument":{"uri":"`+docURI+`"}}}`,
		`{"jsonrpc":"2.0","method":"workspace/didChangeConfiguration","params":{}}`,
		docReq(12, "textDocument/hover", docURI, 0),
		`{"jsonrpc":"2.0","id":13,"method":"workspace/nope"}`,
		`{"jsonrpc":"2.0","method":"$/cancelRequest"}`,
		`{"jsonrpc":"2.0","id":14,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
		`{"jsonrpc":"2.0","id":99,"method":"initialize"}`,
	))
	// initialize, codeLens, 3 hovers, 3 definitions, hover, definition,
	// codeLens on the open buffer, hover, the unknown method, and shutdown
	// reply; notifications do not.
	if len(resps) != 14 {
		t.Fatalf("responses = %d", len(resps))
	}
	text := func(i int) string { raw, _ := json.Marshal(resps[i].Result); return string(raw) }
	if !strings.Contains(text(0), `"hoverProvider":true`) || !strings.Contains(text(0), `"codeLensProvider"`) {
		t.Errorf("initialize = %s", text(0))
	}
	if !strings.Contains(text(1), `"title":"3 dependent(s) · sess-save-k7m2p4xq"`) || !strings.Contains(text(1), `"line":2`) {
		t.Errorf("codeLens = %s", text(1))
	}
	if !strings.Contains(text(2), "**sess-save-k7m2p4xq**") || !strings.Contains(text(2), "unacked") || !strings.Contains(text(2), "```diff") {
		t.Errorf("hover = %s", text(2))
	}
	if text(3) != "null" {
		t.Errorf("hover on a plain line = %s", text(3))
	}
	if !strings.Contains(text(4), "is not defined") {
		t.Errorf("hover on broken = %s", text(4))
	}
	if !strings.Contains(text(5), "internal/store/write.go") || !strings.Contains(text(5), `"line":3`) {
		t.Errorf("definition = %s", text(5))
	}
	if text(6) != "null" || text(7) != "null" {
		t.Errorf("definition on broken/plain = %s %s", text(6), text(7))
	}
	// Document sync methods never reply, even when sent with an id.
	if !strings.Contains(text(8), "**auth-port-h3v8n2wd**") {
		t.Errorf("hover on the open buffer = %s", text(8))
	}
	if !strings.Contains(text(9), "internal/store/write.go") {
		t.Errorf("definition after didChange = %s", text(9))
	}
	if !strings.Contains(text(10), "fresh-b3c7g9kl") || !strings.Contains(text(10), `"title":"0 dependent(s) · fresh-b3c7g9kl"`) {
		t.Errorf("codeLens on the open buffer = %s", text(10))
	}
	if !strings.Contains(text(11), "**sess-save-k7m2p4xq**") {
		t.Errorf("hover after didClose reads the tree = %s", text(11))
	}
	if resps[12].Error == nil || resps[12].Error.Code != rpcMethodNotFound {
		t.Errorf("unknown method = %+v", resps[12])
	}
	if resps[13].Error != nil || string(resps[13].ID) != "14" {
		t.Errorf("shutdown = %+v", resps[13])
	}
	// Framing errors and failures.
	if r := lspRun(t, dir, v, "Content-Length: x\r\n\r\n"); len(r) != 0 {
		t.Errorf("bad length = %+v", r)
	}
	if r := lspRun(t, dir, v, "X-Other: 1\r\n\r\n{}"); len(r) != 0 {
		t.Errorf("missing length = %+v", r)
	}
	if r := lspRun(t, dir, v, "Content-Length: 5\r\n\r\n{}"); len(r) != 0 {
		t.Errorf("short body = %+v", r)
	}
	if r := lspRun(t, dir, v, frame("not json")); len(r) != 1 || r[0].Error == nil || r[0].Error.Code != rpcParseError {
		t.Errorf("parse error = %+v", r)
	}
	if code := Run([]string{"lsp"}, WithDir(dir), WithIO(strings.NewReader(frame("not json")), failWriter{}, failWriter{}), WithVCS(v)); code != ExitError {
		t.Errorf("write failure on parse error = %d", code)
	}
	if code := Run([]string{"lsp"}, WithDir(dir), WithIO(strings.NewReader(frame(`{"jsonrpc":"2.0","id":1,"method":"shutdown"}`)), failWriter{}, failWriter{}), WithVCS(v)); code != ExitError {
		t.Errorf("write failure = %d", code)
	}
	// A broken tree makes every document request an internal error.
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"[\"]\ndocs = [\"docs/**\"]\n")
	resps = lspRun(t, dir, v, frame(docReq(1, "textDocument/codeLens", goURI, 0), docReq(2, "textDocument/hover", docURI, 0), docReq(3, "textDocument/definition", docURI, 0)))
	for i, r := range resps {
		if r.Error == nil || r.Error.Code != rpcInternal {
			t.Errorf("request %d with a broken tree = %+v", i, r)
		}
	}
	// An uninitialised repo fails every document request too.
	os.Remove(filepath.Join(dir, ".ds", "config.toml"))
	resps = lspRun(t, dir, v, frame(docReq(1, "textDocument/hover", docURI, 0)))
	if len(resps) != 1 || resps[0].Error == nil || !strings.Contains(resps[0].Error.Message, "init") {
		t.Errorf("uninitialised = %+v", resps)
	}
	// An open buffer under a registry with no tier for it fails clearly.
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	var out, errb bytes.Buffer
	open := `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"` + docURI + `","text":"x\n"}}}`
	Run([]string{"lsp"}, WithDir(dir), WithIO(strings.NewReader(frame(open, docReq(1, "textDocument/hover", docURI, 0))), &out, &errb), WithVCS(v), WithRegistry(extract.NewRegistry()))
	if !strings.Contains(out.String(), "no extractor") {
		t.Errorf("empty registry = %s", out.String())
	}
	// URIs outside the repo and unparsable ones pass through.
	s := &lspServer{app: &App{dir: dir}, docs: map[string]string{}}
	if got := s.relPath("file:///elsewhere/x.md"); got != "/elsewhere/x.md" {
		t.Errorf("outside uri = %q", got)
	}
	if got := s.relPath("::bad"); got != "::bad" {
		t.Errorf("unparsable uri = %q", got)
	}
	if got := s.relPath("https://example.com/x"); got != "https://example.com/x" {
		t.Errorf("non-file uri = %q", got)
	}
	if !strings.HasSuffix(s.uri("docs/a.md"), "/docs/a.md") || !strings.HasPrefix(s.uri("docs/a.md"), "file://") {
		t.Errorf("uri = %q", s.uri("docs/a.md"))
	}
}

func TestLSPDiagnostics(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	abs, _ := filepath.Abs(dir)
	goURI := "file://" + filepath.Join(abs, "internal/store/write.go")
	open := func(text string) string {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": goURI, "text": text}}})
		return string(raw)
	}
	change := func(text string) string {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didChange", "params": map[string]any{"textDocument": map[string]any{"uri": goURI}, "contentChanges": []map[string]any{{"text": text}}}})
		return string(raw)
	}
	var out, errb bytes.Buffer
	input := frame(
		open("package store\n"),
		change("package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Store() error {\n\treturn nil\n}\n"),
		change("package store\n\n// ds:def id=sess-save-k7m2p4xq owner=@auth\nfunc (s *Store) Save() error {\n\treturn nil\n}\n\n// ds:def\nfunc Nope() {}\n"),
		change("x"),
		`{"jsonrpc":"2.0","id":9,"method":"exit"}`,
	)
	Run([]string{"lsp"}, WithDir(dir), WithIO(strings.NewReader(input), &out, &errb), WithVCS(v), WithClock(func() time.Time { return clock }))
	r := bufio.NewReader(&out)
	var msgs []map[string]any
	for {
		raw, err := readMessage(r)
		if err != nil {
			break
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, m)
	}
	if len(msgs) != 4 {
		t.Fatalf("messages = %d: %v", len(msgs), msgs)
	}
	diags := func(i int) []any {
		params := msgs[i]["params"].(map[string]any)
		if msgs[i]["method"] != methodDiagnostics || params["uri"] != goURI {
			t.Fatalf("message %d = %v", i, msgs[i])
		}
		return params["diagnostics"].([]any)
	}
	// Deleted def: one warning naming the dependents.
	if d := diags(0); len(d) != 1 || !strings.Contains(d[0].(map[string]any)["message"].(string), "Store.Save (sess-save-k7m2p4xq) is no longer defined") || !strings.Contains(d[0].(map[string]any)["message"].(string), "dependent(s)") {
		t.Errorf("deleted = %v", d)
	}
	// Renamed symbol.
	if d := diags(1); len(d) != 1 || !strings.Contains(d[0].(map[string]any)["message"].(string), "Store.Save renamed to Store.Store") {
		t.Errorf("renamed = %v", d)
	}
	// Same symbol back, plus an extraction problem in the buffer.
	if d := diags(2); len(d) != 1 || !strings.Contains(d[0].(map[string]any)["message"].(string), "without id") {
		t.Errorf("problem = %v", d)
	}
	// An empty buffer that no longer parses: still one deletion warning.
	if d := diags(3); len(d) != 1 {
		t.Errorf("garbage buffer = %v", d)
	}
	// A failing writer surfaces on the notification itself.
	if code := Run([]string{"lsp"}, WithDir(dir), WithIO(strings.NewReader(frame(open("package store\n"))), failWriter{}, failWriter{}), WithVCS(v)); code != ExitError {
		t.Errorf("write failure on diagnostics = %d", code)
	}
	// A path the registry cannot extract gets an empty list.
	s := &lspServer{app: &App{dir: dir, vcs: v, name: DefaultName, now: func() time.Time { return clock }}, docs: map[string]string{"weird.unknownext": "x"}}
	if n := s.publishDiagnostics("file:///x", "weird.unknownext"); len(n.Params.(map[string]any)["diagnostics"].([]lspDiagnostic)) != 0 {
		t.Errorf("unextractable = %+v", n)
	}
}

// TestLSPURIsRoundTrip pins both directions of the file URI mapping over the
// paths editors actually send: a URI the server builds for a location is
// well-formed — a `#` or `%` in a path used to be written raw, so `#` began
// a fragment and the jump opened the wrong file — and reading it back gives
// the same repo-relative path.
func TestLSPURIsRoundTrip(t *testing.T) {
	t.Parallel()
	s := &lspServer{app: &App{dir: t.TempDir()}}
	for _, rel := range []string{"docs/a.md", "docs/My Docs/a b.md", "docs/c#sharp.md", "docs/100%.md", "docs/ü.md", "docs/q?x.md"} {
		u := s.uri(rel)
		parsed, err := url.Parse(u)
		if err != nil || parsed.Scheme != "file" || parsed.Fragment != "" || parsed.RawQuery != "" {
			t.Errorf("%s: %q is not a clean file URI (%v)", rel, u, err)
			continue
		}
		if got := s.relPath(u); got != rel {
			t.Errorf("%s: relPath(uri) = %q", rel, got)
		}
	}
}

// TestURIPathShapes pins the Windows half of the mapping, which a Unix host
// never exercises through relPath: a drive path gets the leading slash a
// file URI needs, and loses it again on the way back.
func TestURIPathShapes(t *testing.T) {
	t.Parallel()
	if got := pathToURI("C:/Users/k/a.md"); got != "/C:/Users/k/a.md" {
		t.Errorf("pathToURI(windows) = %q", got)
	}
	if got := pathToURI("/home/k/a.md"); got != "/home/k/a.md" {
		t.Errorf("pathToURI(unix) = %q", got)
	}
	if got := uriToPath("/C:/Users/k/a.md", windowsOS); got != "C:/Users/k/a.md" {
		t.Errorf("uriToPath(windows) = %q", got)
	}
	if got := uriToPath("/C:/odd/unix/dir", "linux"); got != "/C:/odd/unix/dir" {
		t.Errorf("uriToPath must leave a unix path alone: %q", got)
	}
}

// TestLSPPicksTheCitationUnderTheCursor pins hover and definition on a line
// holding two citations: each answers for the one the cursor is on. Before,
// both answered with the first. The line also carries text outside ASCII
// and outside the Basic Multilingual Plane before the second link, so a
// server counting the cursor in bytes rather than UTF-16 units lands on the
// wrong one.
func TestLSPPicksTheCitationUnderTheCursor(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	line := "Café ☕☕☕ 𝄞𝄞𝄞 [save](ds:block?id=sess-save-k7m2p4xq) — port [8081](ds:cfg?id=auth-port-h3v8n2wd)."
	write(t, dir, "docs/two.md", "# T\n\n"+line+"\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	abs, _ := filepath.Abs(dir)
	uri := "file://" + filepath.Join(abs, "docs/two.md")
	// utf16At is where s begins in line, in UTF-16 code units.
	utf16At := func(s string) int {
		return len(utf16.Encode([]rune(line[:strings.Index(line, s)])))
	}
	req := func(id int, method string, ch int) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"%s","params":{"textDocument":{"uri":"%s"},"position":{"line":2,"character":%d}}}`, id, method, uri, ch)
	}
	cases := []struct {
		name, method string
		ch           int
		want         string
	}{
		{"hover on the first link's text", "textDocument/hover", utf16At("[save]") + 1, "sess-save-k7m2p4xq"},
		{"hover on the second link's text", "textDocument/hover", utf16At("[8081]") + 1, "auth-port-h3v8n2wd"},
		{"hover between the links belongs to the next", "textDocument/hover", utf16At("port ["), "auth-port-h3v8n2wd"},
		{"hover past the end of the line", "textDocument/hover", 500, "auth-port-h3v8n2wd"},
		{"definition of the first", "textDocument/definition", utf16At("[save]") + 1, "internal/store/write.go"},
		{"definition of the second", "textDocument/definition", utf16At("[8081]") + 1, "config/auth.yaml"},
	}
	msgs := []string{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`}
	for i, c := range cases {
		msgs = append(msgs, req(i+2, c.method, c.ch))
	}
	resps := lspRun(t, dir, v, frame(msgs...))
	if len(resps) != len(cases)+1 {
		t.Fatalf("responses = %d", len(resps))
	}
	for i, c := range cases {
		if got := fmt.Sprint(resps[i+1].Result); !strings.Contains(got, c.want) {
			t.Errorf("%s: %s", c.name, got)
		}
	}
	// Read as a byte offset, the second link's character falls inside the
	// first link: that is what tells the two encodings apart. If the text
	// before it changes so this no longer holds, the test above proves
	// nothing about encodings.
	if utf16At("[8081]")+1 >= strings.Index(line, ")") {
		t.Fatal("the fixture no longer separates UTF-16 units from bytes")
	}
}

// TestRefAtFallbacks covers the lookups a live editor can reach when the
// text and the extraction disagree: every one answers with the line's first
// citation rather than nothing or the wrong line.
func TestRefAtFallbacks(t *testing.T) {
	t.Parallel()
	a := block.Reference{ID: "a-a2b6f8jk", Pos: block.Position{Start: 1}}
	b := block.Reference{ID: "b-h3v8n2wd", Pos: block.Position{Start: 1}}
	refs := []block.Reference{a, b, {ID: "", Pos: block.Position{Start: 1}}}
	for name, tc := range map[string]struct {
		text string
		pos  lspPosition
		want string
	}{
		"no text":                  {"", lspPosition{Line: 0, Character: 40}, a.ID},
		"text no longer holds ids": {"rewritten since", lspPosition{Line: 0, Character: 3}, a.ID},
		"second without a ')'":     {"[x](ds:block?id=a-a2b6f8jk) [y](ds:block?id=b-h3v8n2wd", lspPosition{Line: 0, Character: 30}, b.ID},
	} {
		got, ok := refAt(refs, tc.text, tc.pos)
		if !ok || got.ID != tc.want {
			t.Errorf("%s: %+v %v", name, got, ok)
		}
	}
	if _, ok := refAt(refs, "", lspPosition{Line: 5}); ok {
		t.Error("a line with no citation has none")
	}
	// The extraction has citations on line 4, but the text is shorter.
	a4, b4 := a, b
	a4.Pos.Start, b4.Pos.Start = 4, 4
	if got, ok := refAt([]block.Reference{a4, b4}, "one line", lspPosition{Line: 3, Character: 9}); !ok || got.ID != a.ID {
		t.Errorf("line beyond the text: %+v %v", got, ok)
	}
	if byteOffset("é𝄞x", 0) != 0 || byteOffset("é𝄞x", 1) != 2 || byteOffset("é𝄞x", 3) != 6 || byteOffset("é𝄞x", 99) != 7 {
		t.Errorf("byteOffset = %d %d %d %d", byteOffset("é𝄞x", 0), byteOffset("é𝄞x", 1), byteOffset("é𝄞x", 3), byteOffset("é𝄞x", 99))
	}
}

// TestLSPDiagnosticsPerEnvironment pins the rename warning for a file that
// defines one id per environment. The buffer's defs were keyed by id, so
// the prod def on disk was compared with the dev def in the buffer and a
// rename nobody made was reported on every open.
func TestLSPDiagnosticsPerEnvironment(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	envs := "prod:\n  port: 443 # ds:def id=port-k7m2p4xq env=prod\ndev:\n  port: 8080 # ds:def id=port-k7m2p4xq env=dev\n"
	write(t, dir, "config/envs.yaml", envs)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	abs, _ := filepath.Abs(dir)
	uri := "file://" + filepath.Join(abs, "config/envs.yaml")
	open := func(text string) string {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{"textDocument": map[string]any{"uri": uri, "text": text}}})
		return string(raw)
	}
	diagnostics := func(text string) []string {
		var out bytes.Buffer
		Run([]string{"lsp"}, WithDir(dir), WithIO(strings.NewReader(frame(open(text), `{"jsonrpc":"2.0","id":9,"method":"exit"}`)), &out, &out), WithVCS(v), WithClock(func() time.Time { return clock }))
		raw, err := readMessage(bufio.NewReader(&out))
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			Params struct {
				Diagnostics []struct {
					Message string `json:"message"`
				} `json:"diagnostics"`
			} `json:"params"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		var msgs []string
		for _, d := range m.Params.Diagnostics {
			msgs = append(msgs, d.Message)
		}
		return msgs
	}
	if got := diagnostics(envs); len(got) != 0 {
		t.Errorf("an unchanged per-environment file warns: %v", got)
	}
	renamed := strings.Replace(envs, "prod:\n  port:", "prod:\n  listen:", 1)
	got := diagnostics(renamed)
	if len(got) != 1 || !strings.Contains(got[0], "prod.port renamed to prod.listen") {
		t.Errorf("renaming prod's key = %v", got)
	}
}

// TestRPCResponseCarriesExactlyOneOfResultAndError pins the JSON-RPC 2.0
// shape both servers write: a success has "result", null when the method
// returned nothing, and no "error"; a failure has "error" and no "result".
// Before, result was omitempty, so `shutdown` and a hover off a citation
// answered with neither member.
func TestRPCResponseCarriesExactlyOneOfResultAndError(t *testing.T) {
	t.Parallel()
	id := json.RawMessage(`1`)
	for _, tc := range []struct {
		name string
		resp rpcResponse
		want string
	}{
		{"no result is null", rpcResponse{JSONRPC: jsonrpcVersion, ID: id}, `{"jsonrpc":"2.0","id":1,"result":null}`},
		{"a result", rpcResponse{JSONRPC: jsonrpcVersion, ID: id, Result: []int{1}}, `{"jsonrpc":"2.0","id":1,"result":[1]}`},
		{"an error has no result", rpcResponse{JSONRPC: jsonrpcVersion, ID: id, Result: []int{1}, Error: &rpcError{rpcInternal, "boom"}}, `{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"boom"}}`},
	} {
		got, err := json.Marshal(tc.resp)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
	// Through the server: shutdown, and a hover and a definition that land
	// on no citation, each answer with a null result.
	// The doc is real and cites nothing, so hover and definition reach the
	// "no citation here" answer rather than an error.
	dir, v := initialised(t)
	write(t, dir, "docs/plain.md", "# Plain\n\nNothing is cited here.\n")
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &lspServer{app: &App{dir: dir, vcs: v, name: DefaultName, now: time.Now}, docs: map[string]string{}}
	params := json.RawMessage(`{"textDocument":{"uri":"file://` + filepath.Join(abs, "docs/plain.md") + `"},"position":{"line":2,"character":0}}`)
	for _, method := range []string{"shutdown", "textDocument/hover", "textDocument/definition"} {
		resp, reply, _ := s.handle(rpcRequest{JSONRPC: jsonrpcVersion, ID: id, Method: method, Params: params})
		if !reply {
			t.Fatalf("%s: no reply", method)
		}
		got, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), `"result":null`) {
			t.Errorf("%s answered %s, want a null result", method, got)
		}
		if resp.Result != nil {
			t.Errorf("%s: Result holds %#v; a missing result must be a nil interface", method, resp.Result)
		}
	}
}
