// Package procplugin implements both sides of the process-plugin protocol
// (docs/SPEC.md §37.4): executables named `ds-<kind>-<name>` on PATH that
// speak one JSON object per line over stdin and stdout, so a plugin can be
// written in any language and a Go plugin can be shipped as a process with
// no extra code.
//
// The host enforces what the plugin is not trusted with: a timeout, a cap on
// response size, a protocol handshake, and the rule that a resolver reply
// may contain nothing but existence and a hash. A resolver that returns a
// value fails here, before anything downstream can see it.
package procplugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Protocol is the handshake version. A plugin answering a different number
// is refused by name, never by misparsing.
const Protocol = 1

// Plugin kinds and the executable name pattern `ds-<kind>-<name>`.
const (
	KindPick    = "pick"
	KindResolve = "resolve"
	KindVerb    = "verb"
	KindRecords = "records"

	namePrefix = "ds-"
)

// KindValues is the canonical list.
var KindValues = []string{KindPick, KindResolve, KindVerb, KindRecords}

// Operations.
const (
	OpHandshake = "handshake"
	OpPick      = "pick"
	OpResolve   = "resolve"
	OpCheck     = "check"
	OpRender    = "render"
	OpQuery     = "query"
)

// Resolver wants.
const (
	WantExists = "exists"
	WantHash   = "hash"
)

// Defaults. The timeout is per call; the size cap bounds one response line.
const (
	DefaultTimeout  = 10 * time.Second
	DefaultMaxBytes = 1 << 20
)

// Errors.
var (
	ErrNotFound  = errors.New("procplugin: plugin executable not found")
	ErrProtocol  = errors.New("procplugin: protocol mismatch or malformed reply")
	ErrTooLarge  = errors.New("procplugin: reply exceeds the size cap")
	ErrValueLeak = errors.New("procplugin: resolver reply carried more than existence and hash")
	ErrPlugin    = errors.New("procplugin: plugin reported an error")
	ErrTimeout   = errors.New("procplugin: plugin timed out")
)

// Request is one host→plugin message. Fields are per operation; unused ones
// are omitted on the wire.
type Request struct {
	Op       string `json:"op"`
	Protocol int    `json:"protocol,omitempty"`
	// pick
	File string `json:"file,omitempty"`
	Expr string `json:"expr,omitempty"`
	// resolve
	Addr string `json:"addr,omitempty"`
	Want string `json:"want,omitempty"`
	// verb
	Ref       json.RawMessage `json:"ref,omitempty"`
	StateView json.RawMessage `json:"state_view,omitempty"`
	// records
	Query json.RawMessage `json:"query,omitempty"`
}

// Range is a picked line range.
type Range struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

// Response is one plugin→host message.
type Response struct {
	// handshake
	Protocol int      `json:"protocol,omitempty"`
	Name     string   `json:"name,omitempty"`
	Kinds    []string `json:"kinds,omitempty"`
	// pick
	Value *string `json:"value,omitempty"`
	Range *Range  `json:"range,omitempty"`
	// resolve
	Exists *bool  `json:"exists,omitempty"`
	Hash   string `json:"hash,omitempty"`
	// verb and records
	Findings json.RawMessage `json:"findings,omitempty"`
	Node     json.RawMessage `json:"node,omitempty"`
	Records  json.RawMessage `json:"records,omitempty"`
	// any
	Error string `json:"error,omitempty"`
}

// handshakeLine is the first message of every call. It is a constant so the
// only encoding that can fail is the caller's request.
var handshakeLine = fmt.Sprintf("{\"op\":%q,\"protocol\":%d}\n", OpHandshake, Protocol)

// Name returns the executable name for a kind and plugin name.
func Name(kind, name string) string { return namePrefix + kind + "-" + name }

// Host runs plugins. The zero value uses exec.LookPath, DefaultTimeout, and
// DefaultMaxBytes. LookPath and Args exist so tests and embedders can point
// at a specific binary; neither is read from the environment by this package.
type Host struct {
	Timeout  time.Duration
	MaxBytes int
	LookPath func(name string) (string, error)
	// Args are prepended arguments for every invocation (a test harness
	// re-executing itself needs them; normal plugins take none).
	Args []string
	// Stderr receives the plugin's stderr; nil discards it.
	Stderr io.Writer
}

// Call runs one request against the named plugin executable and returns its
// reply. Both the handshake and the request are written up front and the
// process is run to completion, so the exchange has no interleaving to race
// on; a plugin speaking another protocol is refused before its reply to the
// operation is read.
func (h Host) Call(ctx context.Context, executable string, req Request) (Response, error) {
	lookPath := h.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	path, err := lookPath(executable)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %s", ErrNotFound, executable)
	}
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxBytes := h.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	var in bytes.Buffer
	in.WriteString(handshakeLine)
	if err := json.NewEncoder(&in).Encode(req); err != nil {
		return Response{}, fmt.Errorf("%w: request is not encodable: %v", ErrProtocol, err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, h.Args...)
	cmd.Stdin = &in
	out := &capWriter{max: maxBytes}
	cmd.Stdout = out
	cmd.Stderr = h.Stderr
	runErr := cmd.Run()
	switch {
	case out.over:
		return Response{}, ErrTooLarge
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return Response{}, fmt.Errorf("%w: after %s", ErrTimeout, timeout)
	case runErr != nil:
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			return Response{}, fmt.Errorf("%w: exit status %d", ErrPlugin, exit.ExitCode())
		}
		return Response{}, runErr
	}
	lines := replyLines(out.buf.Bytes())
	if len(lines) < 2 {
		return Response{}, fmt.Errorf("%w: expected a handshake and a reply, got %d line(s)", ErrProtocol, len(lines))
	}
	hs, _, err := decodeReply(lines[0])
	if err != nil {
		return Response{}, err
	}
	if hs.Protocol != Protocol {
		return Response{}, fmt.Errorf("%w: plugin speaks protocol %d, host %d", ErrProtocol, hs.Protocol, Protocol)
	}
	resp, raw, err := decodeReply(lines[1])
	if err != nil {
		return Response{}, err
	}
	if resp.Error != "" {
		return resp, fmt.Errorf("%w: %s", ErrPlugin, resp.Error)
	}
	if req.Op == OpResolve {
		if leaked := extraKeys(raw, "exists", "hash", "error"); len(leaked) > 0 {
			return Response{}, fmt.Errorf("%w: unexpected field(s) %s", ErrValueLeak, strings.Join(leaked, ", "))
		}
		if resp.Hash != "" && !isDigest(resp.Hash) {
			// The key check alone let a value through inside `hash`, and the
			// truth hash is printed in every out-of-sync finding. The reply
			// is refused without echoing it.
			return Response{}, fmt.Errorf("%w: hash is not %d lowercase hex characters", ErrValueLeak, digestLen)
		}
	}
	return resp, nil
}

// capWriter collects stdout up to max bytes and refuses the rest, which ends
// the copy and lets Run return; the host then reports ErrTooLarge instead of
// buffering an unbounded reply.
type capWriter struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.max {
		w.over = true
		return 0, ErrTooLarge
	}
	return w.buf.Write(p)
}

// replyLines splits stdout into non-blank lines.
func replyLines(b []byte) [][]byte {
	var out [][]byte
	for _, l := range bytes.Split(b, []byte("\n")) {
		if l = bytes.TrimSpace(l); len(l) > 0 {
			out = append(out, l)
		}
	}
	return out
}

// decodeReply parses one reply both as a Response and as a raw key set, so
// callers can see fields the struct does not name.
func decodeReply(line []byte) (Response, map[string]json.RawMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return Response{}, nil, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return Response{}, nil, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	return resp, raw, nil
}

// extraKeys lists keys of raw that are not in allowed, sorted.
func extraKeys(raw map[string]json.RawMessage, allowed ...string) []string {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	var out []string
	for k := range raw {
		if !ok[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Pick asks the `ds-pick-<format>` plugin for a value or range.
func (h Host) Pick(ctx context.Context, format, file, expr string) (Response, error) {
	resp, err := h.Call(ctx, Name(KindPick, format), Request{Op: OpPick, File: file, Expr: expr})
	if err != nil {
		return Response{}, err
	}
	if resp.Value == nil && resp.Range == nil {
		return Response{}, fmt.Errorf("%w: pick reply has neither value nor range", ErrProtocol)
	}
	return resp, nil
}

// Resolve asks the `ds-resolve-<provider>` plugin whether an address exists
// and, with WantHash, for its hash. The value itself can never come back.
func (h Host) Resolve(ctx context.Context, provider, addr, want string) (exists bool, hash string, err error) {
	resp, err := h.Call(ctx, Name(KindResolve, provider), Request{Op: OpResolve, Addr: addr, Want: want})
	if err != nil {
		return false, "", err
	}
	if resp.Exists == nil {
		return false, "", fmt.Errorf("%w: resolve reply without exists", ErrProtocol)
	}
	return *resp.Exists, resp.Hash, nil
}

// Verb asks the `ds-<verb>` plugin to check or render a reference.
func (h Host) Verb(ctx context.Context, verb, op string, ref, stateView json.RawMessage) (Response, error) {
	return h.Call(ctx, namePrefix+verb, Request{Op: op, Ref: ref, StateView: stateView})
}

// Records asks the `ds-records-<source>` plugin for rows.
func (h Host) Records(ctx context.Context, source string, query json.RawMessage) (json.RawMessage, error) {
	resp, err := h.Call(ctx, Name(KindRecords, source), Request{Op: OpQuery, Query: query})
	if err != nil {
		return nil, err
	}
	return resp.Records, nil
}

// Handler answers plugin-side requests. The handshake is answered by Serve.
type Handler func(Request) Response

// Serve is the plugin side: it reads requests from r until EOF, answers the
// handshake with name and kinds, and delegates everything else to handle.
// A request the handler cannot serve should return Response{Error: …}.
func Serve(r io.Reader, w io.Writer, name string, kinds []string, handle Handler) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), DefaultMaxBytes)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			if err := enc.Encode(Response{Error: "malformed request: " + err.Error()}); err != nil {
				return err
			}
			continue
		}
		var resp Response
		if req.Op == OpHandshake {
			resp = Response{Protocol: Protocol, Name: name, Kinds: kinds}
		} else {
			resp = handle(req)
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

// digestLen is the length of the SHA-256 hex digest a resolver's hash must
// be (§37.4): every shipped resolver sends one, and truth and copy are
// compared as strings, so any other shape could never match anyway.
const digestLen = 64

// isDigest reports a lowercase hex SHA-256 digest. It is what stops a
// resolver that returns a secret in the `hash` field instead of its hash;
// a secret that is itself 64 hex characters cannot be told from a digest,
// which is why the value must still never leave the plugin.
func isDigest(s string) bool {
	if len(s) != digestLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
