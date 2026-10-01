package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"runtime"
)

// lspMessageWarning is window/logMessage's MessageType for a warning.
const lspMessageWarning = 2

// lspInitParams is the part of `initialize` that names the workspace:
// rootUri, its deprecated predecessor rootPath, and workspaceFolders.
type lspInitParams struct {
	RootURI          string `json:"rootUri"`
	RootPath         string `json:"rootPath"`
	WorkspaceFolders []struct {
		URI string `json:"uri"`
	} `json:"workspaceFolders"`
}

// lspRootPath is the local directory an `initialize` names: the first
// workspace folder, else rootUri, else rootPath; "" when it names none, or
// names something that is not a file URI.
func lspRootPath(raw json.RawMessage) string {
	var p lspInitParams
	_ = json.Unmarshal(raw, &p)
	uri := p.RootURI
	if len(p.WorkspaceFolders) > 0 {
		uri = p.WorkspaceFolders[0].URI
	}
	if uri == "" {
		return p.RootPath
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return uriToPath(u.Path, runtime.GOOS)
}

// adoptRoot serves the repository the editor opened. `ds lsp` used to work
// in whatever directory the editor happened to start it in, ignoring the
// workspace the client named, so an editor that starts servers from its
// own directory got lenses and hovers for nothing (bug 68). An explicit
// --dir still wins: it is the user saying which repository. A root that is
// not a directory is reported to the editor's log, not served silently.
func (s *lspServer) adoptRoot(params json.RawMessage) []any {
	if s.app.dirFlag {
		return nil
	}
	root := lspRootPath(params)
	if root == "" {
		return nil
	}
	if err := s.app.locate(root, true); err != nil {
		return []any{map[string]any{"jsonrpc": jsonrpcVersion, "method": "window/logMessage", "params": map[string]any{
			"type": lspMessageWarning, "message": fmt.Sprintf("ds lsp: workspace %s: %v; serving %s", root, err, s.app.dir),
		}}}
	}
	return nil
}
