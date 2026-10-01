package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// promise:resolver-unreachable-unverifiable (the plugin half: a locked op is an error reply)
func TestOnePasswordResolver(t *testing.T) {
	orig := run
	run = func(args ...string) ([]byte, string, error) {
		if args[0] != "op" || args[1] != "read" {
			t.Errorf("args = %v", args)
		}
		switch args[3] {
		case "op://v/i/f":
			return []byte("s3cret"), "", nil
		case "op://v/missing":
			return nil, `[ERROR] 2026/10/01 could not read secret: "missing" isn't an item in the "v" vault`, errors.New("exit status 1")
		case "op://v/locked":
			// A signed-out or locked op is not evidence the item is gone:
			// it must come back as an error (unverifiable), never as
			// exists=false (bug 82).
			return nil, "[ERROR] 2026/10/01 You are not currently signed in.", errors.New("exit status 1")
		}
		return nil, "", errors.New("bad reference")
	}
	in := strings.Join([]string{
		`{"op":"handshake","protocol":1}`,
		`{"op":"resolve","addr":"op://v/i/f","want":"hash"}`,
		`{"op":"resolve","addr":"op://v/i/f","want":"exists"}`,
		`{"op":"resolve","addr":"op://v/missing","want":"hash"}`,
		`{"op":"resolve","addr":"not-a-ref","want":"hash"}`,
		`{"op":"resolve","addr":"op://v/locked","want":"hash"}`,
		`{"op":"query"}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serve(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if !strings.Contains(lines[1], `"hash":"`) || strings.Contains(out.String(), "s3cret") {
		t.Errorf("hash reply = %s (value must never appear)", lines[1])
	}
	if strings.Contains(lines[2], "hash") || !strings.Contains(lines[2], `"exists":true`) {
		t.Errorf("exists-only reply = %s", lines[2])
	}
	if !strings.Contains(lines[3], `"exists":false`) || !strings.Contains(lines[4], "op read:") || !strings.Contains(lines[5], "not currently signed in") || strings.Contains(lines[5], "exists") || !strings.Contains(lines[6], "unsupported op") {
		t.Errorf("replies = %v", lines)
	}
	if out, stderr, err := orig("sh", "-c", "echo out; echo err >&2; exit 3"); err == nil || string(out) != "out\n" || stderr != "err\n" {
		t.Errorf("runner = %q %q %v", out, stderr, err)
	}
	if _, stderr, err := orig("/nonexistent/binary"); err == nil || stderr != "" {
		t.Errorf("missing binary = %q %v", stderr, err)
	}
	// main runs serve over stdio; an empty stdin exits cleanly and an
	// unwritable stdout reports failure through the exit hook.
	code := 0
	exit = func(c int) { code = c }
	r, w, _ := os.Pipe()
	_ = w.Close()
	os.Stdin = r
	main()
	if code != 0 {
		t.Errorf("clean exit = %d", code)
	}
	r2, w2, _ := os.Pipe()
	_, _ = w2.WriteString(`{"op":"handshake"}` + "\n")
	_ = w2.Close()
	os.Stdin = r2
	ro, _ := os.Open(os.DevNull)
	realStdout := os.Stdout
	os.Stdout = ro
	main()
	os.Stdout = realStdout
	if code != 1 {
		t.Errorf("write failure exit = %d", code)
	}
}
