package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/spf13/cobra"
	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/match"
)

// `ds lsp` (§24 editor): a Language Server over stdio with Content-Length
// framing. Code lenses on every def list dependents; hover on a cite shows
// the block, its state, and the diff since the acked hash; go-to-definition
// jumps from a cite to its def. Open documents are read from the editor's
// buffer; everything else comes from the tree through the library.
const (
	lspCodeLensCommand = "docsync.why"
	lspSyncFull        = 1
	lspMarkdown        = "markdown"
	lspHeaderLength    = "Content-Length:"
	// lspMaxMessage bounds one message; editors never send more for these
	// requests.
	lspMaxMessage = 16 << 20
)

// lspServer holds the open-document overlay.
type lspServer struct {
	app  *App
	docs map[string]string // repo-relative path -> buffer text
}

func (a *App) lspCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "lsp",
		Short: "serve code lenses, hover, and go-to-definition over stdio (LSP)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s := &lspServer{app: a, docs: map[string]string{}}
			return s.serve(cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	// Editor clients name the transport on the command line; VS Code's
	// language client starts every stdio server as `<command> --stdio`.
	// stdio is the only transport here, so the flag is accepted and changes
	// nothing. Rejecting it made the server exit at once, and the VS Code
	// extension never started (bug 130).
	c.Flags().Bool(flagStdio, true, "use stdin and stdout (the only transport; accepted because editor clients pass it)")
	return c
}

// flagStdio is the transport flag editor clients pass.
const flagStdio = "stdio"

// readMessage parses one framed message.
func readMessage(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, lspHeaderLength) {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, lspHeaderLength)))
			if err != nil || n < 0 || n > lspMaxMessage {
				return nil, fmt.Errorf("lsp: bad Content-Length %q", line)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, errors.New("lsp: missing Content-Length")
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func writeMessage(w io.Writer, v any) error {
	raw, _ := json.Marshal(v)
	_, err := fmt.Fprintf(w, "%s %d\r\n\r\n%s", lspHeaderLength, len(raw), raw)
	return err
}

// serve runs until exit or EOF.
func (s *lspServer) serve(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	for {
		raw, err := readMessage(r)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var req rpcRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			if err := writeMessage(out, rpcResponse{JSONRPC: jsonrpcVersion, Error: &rpcError{rpcParseError, err.Error()}}); err != nil {
				return err
			}
			continue
		}
		if req.Method == "exit" {
			return nil
		}
		resp, reply, notes := s.handle(req)
		for _, n := range notes {
			if err := writeMessage(out, n); err != nil {
				return err
			}
		}
		if !reply {
			continue
		}
		if err := writeMessage(out, resp); err != nil {
			return err
		}
	}
}

// lsp wire types (the subset used).
type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

// lspHover is a hover result: markdown shown beside the cursor. A nil
// *lspHover encodes as JSON null, which LSP reads as "no hover here".
type lspHover struct {
	Contents lspMarkup `json:"contents"`
}

// lspMarkup is LSP MarkupContent; Kind is lspMarkdown for every hover.
type lspMarkup struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// markdownHover wraps markdown text as a hover result.
func markdownHover(md string) *lspHover {
	return &lspHover{Contents: lspMarkup{Kind: lspMarkdown, Value: md}}
}

type lspCodeLens struct {
	Range   lspRange   `json:"range"`
	Command lspCommand `json:"command"`
}

type lspCommand struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments"`
}

type lspDocParams struct {
	TextDocument struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"textDocument"`
	Position       lspPosition `json:"position"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

// handle dispatches one request or notification. notes are notifications
// the server sends first (diagnostics after an edit).
func (s *lspServer) handle(req rpcRequest) (resp rpcResponse, reply bool, notes []any) {
	resp = rpcResponse{JSONRPC: jsonrpcVersion, ID: req.ID}
	var p lspDocParams
	_ = json.Unmarshal(req.Params, &p)
	path := s.relPath(p.TextDocument.URI)
	switch req.Method {
	case "initialize":
		notes = s.adoptRoot(req.Params)
		resp.Result = map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":   lspSyncFull,
				"hoverProvider":      true,
				"definitionProvider": true,
				"codeLensProvider":   map[string]any{"resolveProvider": false},
			},
			"serverInfo": map[string]any{"name": mcpServerName, "version": docsync.SpecVersion},
		}
		return resp, true, notes
	case "initialized", "$/cancelRequest":
		return resp, false, nil
	case "shutdown":
		resp.Result = nil
	case "textDocument/didOpen":
		s.docs[path] = p.TextDocument.Text
		return resp, false, []any{s.publishDiagnostics(p.TextDocument.URI, path)}
	case "textDocument/didChange":
		if len(p.ContentChanges) > 0 {
			s.docs[path] = p.ContentChanges[len(p.ContentChanges)-1].Text
		}
		return resp, false, []any{s.publishDiagnostics(p.TextDocument.URI, path)}
	case "textDocument/didClose":
		delete(s.docs, path)
		return resp, false, nil
	case "textDocument/codeLens":
		lenses, err := s.codeLens(path)
		if err != nil {
			resp.Error = &rpcError{rpcInternal, err.Error()}
		} else {
			resp.Result = lenses
		}
	case "textDocument/hover":
		// Assigned only when non-nil: a nil *lspHover stored in Result would
		// be a non-nil interface holding a nil pointer. MarshalJSON writes
		// the null either way; this keeps Result == nil meaning "no result".
		h, err := s.hover(path, p.Position)
		switch {
		case err != nil:
			resp.Error = &rpcError{rpcInternal, err.Error()}
		case h != nil:
			resp.Result = h
		}
	case "textDocument/definition":
		loc, err := s.definition(path, p.Position)
		switch {
		case err != nil:
			resp.Error = &rpcError{rpcInternal, err.Error()}
		case loc != nil:
			resp.Result = loc
		}
	default:
		if len(req.ID) == 0 {
			return resp, false, nil
		}
		resp.Error = &rpcError{rpcMethodNotFound, "method not found: " + req.Method}
	}
	return resp, true, nil
}

// Diagnostics (§24 "warning on rename or delete of a defined symbol"): the
// open buffer is compared with the file as scanned. A def present on disk
// and absent from the buffer, or bound to a differently named symbol, is a
// warning carrying the dependent count; an extraction problem in the
// buffer is a warning too. Severity is always warning: the tool cannot
// know whether the edit is a mistake, only that dependents exist.
const (
	lspSeverityWarning = 2
	lspSource          = "docsync"
	methodDiagnostics  = "textDocument/publishDiagnostics"
)

type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

type lspNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// publishDiagnostics builds the notification for one buffer; a buffer the
// tool cannot analyse gets an empty list, which also clears stale marks.
func (s *lspServer) publishDiagnostics(uri, path string) lspNotification {
	diags := s.diagnostics(path)
	if diags == nil {
		diags = []lspDiagnostic{}
	}
	return lspNotification{JSONRPC: jsonrpcVersion, Method: methodDiagnostics, Params: map[string]any{"uri": uri, "diagnostics": diags}}
}

func (s *lspServer) diagnostics(path string) []lspDiagnostic {
	_, rep, ov, err := s.load(path)
	if err != nil {
		return nil
	}
	deps := dependents(rep)
	// Keyed by id and environment: one file may define an id once per
	// environment, and comparing prod on disk with dev in the buffer warned
	// of a rename nobody made.
	inBuffer := map[string]block.Block{}
	for _, b := range ov.defs {
		inBuffer[match.BlockKey(b)] = b
	}
	var out []lspDiagnostic
	warn := func(line int, msg string) {
		out = append(out, lspDiagnostic{Range: lspRange{Start: lspPosition{Line: line - 1}, End: lspPosition{Line: line - 1}}, Severity: lspSeverityWarning, Source: lspSource, Message: msg})
	}
	for _, disk := range rep.Scan.Defs {
		if disk.Pos.File != path {
			continue
		}
		now, ok := inBuffer[match.BlockKey(disk)]
		switch {
		case !ok:
			warn(disk.DirectivePos.Start, fmt.Sprintf("%s (%s) is no longer defined in this file; %d dependent(s) cite it", disk.Symbol, disk.ID, deps[disk.ID]))
		case now.Symbol != disk.Symbol && disk.Symbol != "":
			warn(now.DirectivePos.Start, fmt.Sprintf("%s renamed to %s (%s); %d dependent(s) cite it", disk.Symbol, now.Symbol, disk.ID, deps[disk.ID]))
		}
	}
	for _, p := range ov.problems {
		warn(p.Pos.Start, p.Err.Error())
	}
	return out
}

// dependents counts citations per id: refs plus page covers.
func dependents(rep docsync.Report) map[string]int {
	count := map[string]int{}
	for _, r := range rep.Scan.Refs {
		count[r.ID]++
	}
	for _, pg := range rep.Scan.Pages {
		for _, id := range pg.Covers {
			count[id]++
		}
	}
	return count
}

// lspCtx is the context requests run under; the server has no cancellation
// beyond process exit.
func lspCtx() context.Context { return context.Background() }

// rpcInternal is the JSON-RPC internal error code, used for tool failures
// the client should surface rather than retry.
const rpcInternal = -32603

// relPath turns a file URI into a repo-relative slash path.
func (s *lspServer) relPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	abs, _ := filepath.Abs(s.app.dir)
	rel, err := filepath.Rel(abs, filepath.FromSlash(uriToPath(u.Path, runtime.GOOS)))
	if err != nil || strings.HasPrefix(rel, "..") {
		return u.Path
	}
	return filepath.ToSlash(rel)
}

// uri builds the file URI for a repo-relative path. It goes through
// url.URL so the path is escaped: written raw, a `#` began a fragment, a
// `?` a query, and a `%` an invalid escape, and the editor opened the wrong
// file or none. relPath reverses it.
func (s *lspServer) uri(rel string) string {
	abs, _ := filepath.Abs(filepath.Join(s.app.dir, filepath.FromSlash(rel)))
	return (&url.URL{Scheme: "file", Path: pathToURI(filepath.ToSlash(abs))}).String()
}

// pathToURI gives an absolute slash path the leading slash a file URI's path
// needs: a Windows path is written file:///C:/x, not file://C:/x, where C:
// would read as the host.
func pathToURI(p string) string {
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

// uriToPath reverses pathToURI on Windows, where a file URI's path is
// /C:/x and the file is C:/x. Elsewhere the path is used as it is.
func uriToPath(p, goos string) string {
	if goos == windowsOS && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		return p[1:]
	}
	return p
}

// windowsOS is runtime.GOOS on Windows.
const windowsOS = "windows"

// overlay is one document as the editor sees it: extracted from the open
// buffer when there is one, else from the scan.
type overlay struct {
	defs     []block.Block
	refs     []block.Reference
	problems []extract.Problem
	// text is the document as the editor sees it: the open buffer, or the
	// file on disk. It is what a cursor's character is measured against;
	// "" when the file cannot be read, and lookups fall back to the line.
	text string
}

// load runs a check and extracts the requested document from the overlay
// when it is open, so unsaved edits are honoured.
func (s *lspServer) load(path string) (loaded, docsync.Report, overlay, error) {
	ld, err := s.app.system()
	if err != nil {
		return loaded{}, docsync.Report{}, overlay{}, err
	}
	rep, err := ld.sys.Check(lspCtx(), docsync.CheckOptions{})
	if err != nil {
		return loaded{}, docsync.Report{}, overlay{}, err
	}
	var ov overlay
	if text, open := s.docs[path]; open {
		reg := ld.sys.Registry()
		ex, err := reg.For(path)
		if err != nil {
			return loaded{}, docsync.Report{}, overlay{}, err
		}
		found := ex.Extract(path, []byte(text), ld.sys.Config().Prefix)
		for _, d := range found.Defs {
			d.Block.Pos.File, d.Block.DirectivePos.File = path, path
			ov.defs = append(ov.defs, d.Block)
		}
		for _, r := range found.Refs {
			r.Reference.Pos.File = path
			ov.refs = append(ov.refs, r.Reference)
		}
		ov.problems = found.Problems
		ov.text = text
		return ld, rep, ov, nil
	}
	if b, err := os.ReadFile(filepath.Join(s.app.dir, path)); err == nil {
		ov.text = string(b)
	}
	for _, b := range rep.Scan.Defs {
		if b.Pos.File == path {
			ov.defs = append(ov.defs, b)
		}
	}
	for _, r := range rep.Scan.Refs {
		if r.Pos.File == path {
			ov.refs = append(ov.refs, r)
		}
	}
	return ld, rep, ov, nil
}

// codeLens lists dependents for every def in the document.
func (s *lspServer) codeLens(path string) ([]lspCodeLens, error) {
	_, rep, ov, err := s.load(path)
	if err != nil {
		return nil, err
	}
	count := dependents(rep)
	lenses := []lspCodeLens{}
	for _, b := range ov.defs {
		line := b.DirectivePos.Start - 1
		title := fmt.Sprintf("%d dependent(s) · %s", count[b.ID], b.ID)
		lenses = append(lenses, lspCodeLens{Range: lspRange{Start: lspPosition{Line: line}, End: lspPosition{Line: line}}, Command: lspCommand{Title: title, Command: lspCodeLensCommand, Arguments: []any{b.ID}}})
	}
	return lenses, nil
}

// refAt returns the first reference on a 0-based line.
func refAt(refs []block.Reference, text string, pos lspPosition) (block.Reference, bool) {
	var on []block.Reference
	for _, r := range refs {
		if r.Pos.Start == pos.Line+1 && r.ID != "" {
			on = append(on, r)
		}
	}
	if len(on) <= 1 {
		return firstRef(on)
	}
	// Several citations share the line: pick the one under the cursor.
	// Returning the first answered a hover over the second citation with
	// the first one's block. Each citation owns the text from the end of
	// the one before it to the ")" that closes its own link, found by
	// walking the ids in the order extraction reported them.
	lines := strings.Split(text, "\n")
	if pos.Line >= len(lines) {
		return firstRef(on)
	}
	line := lines[pos.Line]
	at := byteOffset(line, pos.Character)
	from := 0
	for _, r := range on {
		i := strings.Index(line[from:], r.ID)
		if i < 0 {
			// The buffer no longer matches what was extracted.
			return firstRef(on)
		}
		end := from + i + len(r.ID)
		if j := strings.IndexByte(line[end:], ')'); j >= 0 {
			end += j
		}
		if at <= end {
			return r, true
		}
		from = end
	}
	return on[len(on)-1], true
}

// firstRef is the first of refs, or false when there is none.
func firstRef(refs []block.Reference) (block.Reference, bool) {
	if len(refs) == 0 {
		return block.Reference{}, false
	}
	return refs[0], true
}

// byteOffset converts an LSP character — a count of UTF-16 code units, the
// protocol's default and the only encoding this server offers — into a
// byte offset in line. Counting bytes instead put the cursor past its real
// place on any line with non-ASCII text before it. A character past the
// end of the line is the end of the line.
func byteOffset(line string, character int) int {
	units := 0
	for i, r := range line {
		if units >= character {
			return i
		}
		units += utf16.RuneLen(r)
	}
	return len(line)
}

// hover shows the cited block, its state, and any diff since the ack; nil
// when the position is not on a citation.
func (s *lspServer) hover(path string, pos lspPosition) (*lspHover, error) {
	ld, rep, ov, err := s.load(path)
	if err != nil {
		return nil, err
	}
	ref, ok := refAt(ov.refs, ov.text, pos)
	if !ok {
		return nil, nil
	}
	b, found := ld.sys.LocateID(rep.Scan, ref.ID)
	if !found {
		return markdownHover(fmt.Sprintf("**%s** is not defined", ref.ID)), nil
	}
	state := check.StateOK
	var diff string
	for _, f := range rep.Findings {
		if f.ID == ref.ID && f.Doc == path && f.Line == ref.Pos.Start {
			state, diff = f.State, f.Diff
			break
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**%s** · `%s:%d-%d` · %s\n\n", b.ID, b.Pos.File, b.Pos.Start, b.Pos.End, state)
	if diff != "" {
		fmt.Fprintf(&sb, "```diff\n%s```\n\n", diff)
	}
	fmt.Fprintf(&sb, "```\n%s\n```", b.Content)
	return markdownHover(sb.String()), nil
}

// definition jumps from a cite to its def; nil when the position is not on
// a citation or the id is not defined.
func (s *lspServer) definition(path string, pos lspPosition) (*lspLocation, error) {
	ld, rep, ov, err := s.load(path)
	if err != nil {
		return nil, err
	}
	ref, ok := refAt(ov.refs, ov.text, pos)
	if !ok {
		return nil, nil
	}
	b, found := ld.sys.LocateID(rep.Scan, ref.ID)
	if !found {
		return nil, nil
	}
	return &lspLocation{URI: s.uri(b.Pos.File), Range: lspRange{Start: lspPosition{Line: b.Pos.Start - 1}, End: lspPosition{Line: b.Pos.End - 1}}}, nil
}
