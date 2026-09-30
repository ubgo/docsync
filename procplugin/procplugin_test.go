package procplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// The host tests re-execute the test binary as the plugin. helperMode is
// selected through a flag argument rather than the environment so the
// package under test never reads env; only the test process does.
const helperFlag = "-procplugin-helper="

func TestMain(m *testing.M) {
	for _, a := range os.Args {
		if strings.HasPrefix(a, helperFlag) {
			os.Exit(helper(strings.TrimPrefix(a, helperFlag)))
		}
	}
	os.Exit(m.Run())
}

// helper is the fake plugin. Modes exercise each host guard.
func helper(mode string) int {
	switch mode {
	case "good":
		_ = Serve(os.Stdin, os.Stdout, "fake", []string{KindPick, KindResolve, KindRecords, KindVerb}, func(req Request) Response {
			switch req.Op {
			case OpPick:
				if req.Expr == "range" {
					return Response{Range: &Range{Start: 1, End: 2, Text: "a\nb"}}
				}
				if req.Expr == "neither" {
					return Response{}
				}
				v := "picked:" + req.File
				return Response{Value: &v}
			case OpResolve:
				yes := true
				if req.Addr == "leaky" {
					return Response{Exists: &yes, Hash: "h", Node: json.RawMessage(`"the secret value"`)}
				}
				if req.Addr == "noexists" {
					return Response{Hash: strings.Repeat("a", digestLen)}
				}
				if req.Addr == "secret-in-hash" {
					return Response{Exists: &yes, Hash: "hunter2"}
				}
				h := ""
				if req.Want == WantHash {
					h = testDigest
				}
				return Response{Exists: &yes, Hash: h}
			case OpQuery:
				return Response{Records: json.RawMessage(`[{"title":"a"}]`)}
			case OpCheck:
				return Response{Findings: json.RawMessage(`[]`)}
			case OpRender:
				return Response{Node: req.Ref}
			}
			return Response{Error: "unsupported op " + req.Op}
		})
	case "oldproto":
		fmt.Println(`{"protocol":0,"name":"old"}`)
		fmt.Println(`{"value":"x"}`)
	case "garbagehs":
		fmt.Println(`{"protocol":"one"}`)
		fmt.Println(`{"value":"x"}`)
	case "garbage":
		fmt.Println(`{"protocol":1}`)
		fmt.Println(`not json`)
	case "huge":
		fmt.Println(`{"protocol":1}`)
		fmt.Println(`{"value":"` + strings.Repeat("x", 5000) + `"}`)
	case "silent":
		fmt.Println(`{"protocol":1}`)
	case "slow":
		fmt.Println(`{"protocol":1}`)
		time.Sleep(5 * time.Second)
	case "exit1":
		fmt.Println(`{"protocol":1}`)
		return 1
	case "badtype":
		fmt.Println(`{"protocol":1}`)
		fmt.Println(`{"value":5}`)
	case "noflush":
		// Reply without a trailing newline, then exit: still a valid line.
		fmt.Println(`{"protocol":1}`)
		fmt.Print(`{"value":"tail"}`)
	}
	return 0
}

func host(t *testing.T, mode string) Host {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Host{
		LookPath: func(name string) (string, error) {
			if strings.HasPrefix(name, "ds-") {
				return exe, nil
			}
			return "", errors.New("no")
		},
		Args:    []string{helperFlag + mode},
		Timeout: 3 * time.Second,
		Stderr:  io.Discard,
	}
}

func TestHostHappyPaths(t *testing.T) {
	t.Parallel()
	h := host(t, "good")
	ctx := context.Background()
	if r, err := h.Pick(ctx, "json", "pkg.json", "version"); err != nil || r.Value == nil || *r.Value != "picked:pkg.json" {
		t.Errorf("pick value = %+v %v", r, err)
	}
	if r, err := h.Pick(ctx, "json", "f", "range"); err != nil || r.Range == nil || r.Range.Text != "a\nb" {
		t.Errorf("pick range = %+v %v", r, err)
	}
	if exists, hash, err := h.Resolve(ctx, "github", "STRIPE_KEY", WantExists); err != nil || !exists || hash != "" {
		t.Errorf("resolve exists = %v %q %v", exists, hash, err)
	}
	if exists, hash, err := h.Resolve(ctx, "1password", "op://a/b", WantHash); err != nil || !exists || hash != testDigest {
		t.Errorf("resolve hash = %v %q %v", exists, hash, err)
	}
	if recs, err := h.Records(ctx, "frontmatter", json.RawMessage(`{"kind":"task"}`)); err != nil || string(recs) != `[{"title":"a"}]` {
		t.Errorf("records = %s %v", recs, err)
	}
	if r, err := h.Verb(ctx, "ticket", OpCheck, json.RawMessage(`{"id":"x"}`), nil); err != nil || string(r.Findings) != "[]" {
		t.Errorf("verb check = %+v %v", r, err)
	}
	if r, err := h.Verb(ctx, "ticket", OpRender, json.RawMessage(`{"id":"x"}`), nil); err != nil || string(r.Node) != `{"id":"x"}` {
		t.Errorf("verb render = %+v %v", r, err)
	}
	if Name(KindPick, "json") != "ds-pick-json" || len(KindValues) != 4 {
		t.Error("names")
	}
	// Reply without trailing newline.
	if r, err := host(t, "noflush").Pick(ctx, "x", "f", "e"); err != nil || *r.Value != "tail" {
		t.Errorf("noflush = %+v %v", r, err)
	}
}

// promise:resolver-no-value
func TestHostGuards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := host(t, "good").Call(ctx, "not-a-plugin", Request{Op: OpPick}); !errors.Is(err, ErrNotFound) {
		t.Errorf("lookup: %v", err)
	}
	if _, err := host(t, "oldproto").Call(ctx, "ds-pick-x", Request{Op: OpPick}); !errors.Is(err, ErrProtocol) {
		t.Errorf("old protocol: %v", err)
	}
	if _, err := host(t, "garbage").Call(ctx, "ds-pick-x", Request{Op: OpPick}); !errors.Is(err, ErrProtocol) {
		t.Errorf("garbage: %v", err)
	}
	if _, err := host(t, "garbagehs").Call(ctx, "ds-pick-x", Request{Op: OpPick}); !errors.Is(err, ErrProtocol) {
		t.Errorf("garbage handshake: %v", err)
	}
	h := host(t, "huge")
	h.MaxBytes = 1024
	if _, err := h.Call(ctx, "ds-pick-x", Request{Op: OpPick}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("huge: %v", err)
	}
	if _, err := host(t, "silent").Call(ctx, "ds-pick-x", Request{Op: OpPick}); !errors.Is(err, ErrProtocol) {
		t.Errorf("silent: %v", err)
	}
	slow := host(t, "slow")
	slow.Timeout = 300 * time.Millisecond
	if _, err := slow.Call(ctx, "ds-pick-x", Request{Op: OpPick}); !errors.Is(err, ErrTimeout) {
		t.Errorf("slow: %v", err)
	}
	good := host(t, "good")
	if _, _, err := good.Resolve(ctx, "p", "leaky", WantHash); !errors.Is(err, ErrValueLeak) {
		t.Errorf("leak: %v", err)
	}
	if _, _, err := good.Resolve(ctx, "p", "noexists", WantExists); !errors.Is(err, ErrProtocol) {
		t.Errorf("resolve without exists: %v", err)
	}
	if _, err := good.Pick(ctx, "p", "f", "neither"); !errors.Is(err, ErrProtocol) {
		t.Errorf("pick neither: %v", err)
	}
	if _, err := good.Call(ctx, "ds-x", Request{Op: "bogus"}); !errors.Is(err, ErrPlugin) {
		t.Errorf("plugin error: %v", err)
	}
	if _, err := host(t, "exit1").Call(ctx, "ds-pick-x", Request{Op: OpPick}); !errors.Is(err, ErrPlugin) {
		t.Errorf("exit 1: %v", err)
	}
	if _, err := host(t, "badtype").Call(ctx, "ds-pick-x", Request{Op: OpPick}); !errors.Is(err, ErrProtocol) {
		t.Errorf("bad type: %v", err)
	}
	if _, err := good.Call(ctx, "ds-x", Request{Op: OpCheck, Ref: json.RawMessage("not json")}); !errors.Is(err, ErrProtocol) {
		t.Errorf("unencodable request: %v", err)
	}
	missing := host(t, "good")
	missing.LookPath = func(string) (string, error) { return "/nonexistent/ds-plugin", nil }
	if _, err := missing.Call(ctx, "ds-x", Request{Op: OpPick}); err == nil || errors.Is(err, ErrPlugin) {
		t.Errorf("start failure surfaces as is: %v", err)
	}
	if _, err := host(t, "oldproto").Pick(ctx, "x", "f", "e"); !errors.Is(err, ErrProtocol) {
		t.Errorf("pick propagates: %v", err)
	}
	if _, err := host(t, "oldproto").Records(ctx, "x", nil); !errors.Is(err, ErrProtocol) {
		t.Errorf("records propagates: %v", err)
	}
	if _, _, err := host(t, "oldproto").Resolve(ctx, "x", "a", WantExists); !errors.Is(err, ErrProtocol) {
		t.Errorf("resolve propagates: %v", err)
	}
	zeroTimeout := host(t, "good")
	zeroTimeout.Timeout = 0
	if _, err := zeroTimeout.Pick(ctx, "x", "f", "e"); err != nil {
		t.Errorf("default timeout: %v", err)
	}
	// Zero-value defaults resolve through the real PATH and fail cleanly.
	if _, err := (Host{}).Call(ctx, "ds-definitely-not-installed-anywhere", Request{Op: OpPick}); !errors.Is(err, ErrNotFound) {
		t.Errorf("zero host: %v", err)
	}
	if got := extraKeys(map[string]json.RawMessage{"b": nil, "a": nil, "exists": nil}, "exists"); len(got) != 2 || got[0] != "a" {
		t.Errorf("extraKeys = %v", got)
	}
}

func TestServe(t *testing.T) {
	t.Parallel()
	in := strings.Join([]string{
		`{"op":"handshake","protocol":1}`,
		``,
		`{"op":"pick","file":"f","expr":"e"}`,
		`nope`,
		`{"op":"other"}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	err := Serve(strings.NewReader(in), &out, "demo", []string{KindPick}, func(req Request) Response {
		if req.Op == OpPick {
			v := req.File + ":" + req.Expr
			return Response{Value: &v}
		}
		return Response{Error: "unsupported"}
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 || lines[0] != `{"protocol":1,"name":"demo","kinds":["pick"]}` || lines[1] != `{"value":"f:e"}` || !strings.Contains(lines[2], "malformed request") || lines[3] != `{"error":"unsupported"}` {
		t.Errorf("serve output = %q", lines)
	}
	// A write failure ends Serve with the error.
	if err := Serve(strings.NewReader(`{"op":"handshake"}`+"\n"), failWriter{}, "d", nil, nil); err == nil {
		t.Error("write error must surface")
	}
	if err := Serve(strings.NewReader("nope\n"), failWriter{}, "d", nil, nil); err == nil {
		t.Error("write error on malformed reply must surface")
	}
	// An over-long line is a scanner error.
	if err := Serve(strings.NewReader(strings.Repeat("x", DefaultMaxBytes+10)+"\n"), &out, "d", nil, nil); err == nil {
		t.Error("oversized request line must error")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

// TestResolverHashMustBeADigest pins the value-leak check on the hash field
// itself: a resolver that answers with the secret where its hash belongs is
// refused — the key check alone let it through, and the truth hash is
// printed in every out-of-sync finding — and the refusal never echoes it.
func TestResolverHashMustBeADigest(t *testing.T) {
	t.Parallel()
	digest := strings.Repeat("0123456789abcdef", 4)
	for _, tc := range []struct {
		hash string
		ok   bool
	}{
		{digest, true},
		{"", true}, // existence only
		{"hunter2", false},
		{"ghp_" + strings.Repeat("x", 60), false},
		{strings.ToUpper(digest), false},
		{digest + "0", false},
		{digest[:63] + "g", false},
	} {
		got := isDigest(tc.hash) || tc.hash == ""
		if got != tc.ok {
			t.Errorf("hash %q accepted = %v, want %v", tc.hash, got, tc.ok)
		}
	}
	ctx := context.Background()
	h := host(t, "good")
	if _, _, err := h.Resolve(ctx, "p", "secret-in-hash", WantHash); !errors.Is(err, ErrValueLeak) || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("secret in the hash field = %v", err)
	}
}

// testDigest is a well-formed SHA-256 hex digest for fake resolver replies.
const testDigest = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
