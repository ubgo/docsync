package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestResolver(t *testing.T) {
	orig := run
	calls := 0
	run = func(args ...string) ([]byte, string, error) {
		calls++
		joined := strings.Join(args, " ")
		switch calls {
		case 1:
			if joined != "vault kv get -field=value secret/app" {
				t.Errorf("args = %q", joined)
			}
			return []byte("s3cret"), "", nil
		// The vault: prefix ds infers the provider from never reaches the
		// CLI (bug 81).
		case 2:
			if joined != "vault kv get -field=token secret/app" {
				t.Errorf("args = %q", joined)
			}
			return []byte("s3cret"), "", nil
		case 3:
			return nil, "error: " + notFound + " for that name", errors.New("exit status 1")
		}
		return nil, "permission denied", errors.New("exit status 2")
	}
	in := strings.Join([]string{
		`{"op":"handshake","protocol":1}`,
		`{"op":"resolve","addr":"secret/app","want":"exists"}`,
		`{"op":"resolve","addr":"vault:secret/app#token","want":"hash"}`,
		`{"op":"resolve","addr":"missing","want":"exists"}`,
		`{"op":"resolve","addr":"denied"}`,
		`{"op":"pick"}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serve(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	want := []string{`"protocol":1`, `"exists":true`, `"hash":"`, `"exists":false`, `permission denied`, `unsupported op`}
	for i, w := range want {
		if !strings.Contains(lines[i], w) {
			t.Errorf("line %d = %s, want %q", i, lines[i], w)
		}
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Error("the value must never leave the plugin")
	}
	if strings.Contains(lines[1], `"hash"`) {
		t.Error("exists-only must not hash")
	}
	// The real runner separates stdout from stderr and reports non-exit
	// failures too.
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
