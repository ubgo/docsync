package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestOnePasswordResolver(t *testing.T) {
	run = func(args ...string) ([]byte, error) {
		if args[0] != "op" || args[1] != "read" {
			t.Errorf("args = %v", args)
		}
		switch args[3] {
		case "op://v/i/f":
			return []byte("s3cret"), nil
		case "op://v/missing":
			return nil, errors.New("exit 1")
		}
		return nil, errors.New("bad reference")
	}
	in := strings.Join([]string{
		`{"op":"handshake","protocol":1}`,
		`{"op":"resolve","addr":"op://v/i/f","want":"hash"}`,
		`{"op":"resolve","addr":"op://v/i/f","want":"exists"}`,
		`{"op":"resolve","addr":"op://v/missing","want":"hash"}`,
		`{"op":"resolve","addr":"not-a-ref","want":"hash"}`,
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
	if !strings.Contains(lines[3], `"exists":false`) || !strings.Contains(lines[4], "op read:") || !strings.Contains(lines[5], "unsupported op") {
		t.Errorf("replies = %v", lines)
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
