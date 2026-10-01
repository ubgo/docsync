package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ds lsp serves the workspace the client names in initialize, not the
// directory the editor happened to start it in (bug 68); --dir still wins.
func TestLSPServesTheClientsRoot(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	abs, _ := filepath.Abs(dir)
	goURI := fileURI(filepath.Join(abs, "internal/store/write.go"))
	elsewhere := t.TempDir()
	lens := func(args []string, init string) string {
		var out, errb bytes.Buffer
		Run(append(args, "lsp"), WithDir(elsewhere), WithIO(strings.NewReader(frame(init, docReq(2, "textDocument/codeLens", goURI, 0))), &out, &errb), WithVCS(v), WithClock(func() time.Time { return clock }))
		return out.String()
	}
	initWith := func(params string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` + params + `}`
	}
	root, _ := json.Marshal(fileURI(abs))
	for name, params := range map[string]string{
		"rootUri":          `{"rootUri":` + string(root) + `}`,
		"workspaceFolders": `{"rootUri":null,"workspaceFolders":[{"uri":` + string(root) + `}]}`,
		"rootPath":         `{"rootPath":` + mustJSON(abs) + `}`,
	} {
		out := lens(nil, initWith(params))
		if !strings.Contains(out, "sess-save-k7m2p4xq") {
			t.Errorf("%s: no lens for the client's workspace: %s", name, out)
		}
	}
	// An explicit --dir names the repository, whatever the client says.
	if out := lens([]string{"--dir", elsewhere}, initWith(`{"rootUri":`+string(root)+`}`)); strings.Contains(out, "sess-save-k7m2p4xq") {
		t.Errorf("--dir lost to rootUri: %s", out)
	}
	// A root that is not a directory goes to the editor's log.
	bad, _ := json.Marshal(fileURI(filepath.Join(abs, "nope")))
	if out := lens(nil, initWith(`{"rootUri":`+string(bad)+`}`)); !strings.Contains(out, "window/logMessage") || !strings.Contains(out, "not a directory") {
		t.Errorf("bad root = %s", out)
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestLSPRootPath(t *testing.T) {
	t.Parallel()
	for params, want := range map[string]string{
		`{}`:                        "",
		`{"rootUri":"http://x/y"}`:  "",
		`{"rootUri":"file:///a/b"}`: "/a/b",
		`{"rootPath":"/c"}`:         "/c",
		`{"rootUri":"%zz"}`:         "",
		`{"workspaceFolders":[{"uri":"file:///w"}],"rootUri":"file:///r"}`: "/w",
	} {
		if got := lspRootPath(json.RawMessage(params)); got != filepath.FromSlash(want) && got != want {
			t.Errorf("lspRootPath(%s) = %q, want %q", params, got, want)
		}
	}
}
