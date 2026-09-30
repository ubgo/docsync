package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestGitHubResolver(t *testing.T) {
	calls := 0
	run = func(args ...string) ([]byte, error) {
		calls++
		if strings.Join(args, " ") != "gh secret list --json name" {
			t.Errorf("args = %v", args)
		}
		switch calls {
		case 1, 2:
			return []byte(`[{"name":"STRIPE_KEY"},{"name":"OTHER"}]`), nil
		case 3:
			return nil, errors.New("not logged in")
		}
		return []byte("not json"), nil
	}
	in := strings.Join([]string{
		`{"op":"handshake","protocol":1}`,
		`{"op":"resolve","addr":"STRIPE_KEY","want":"exists"}`,
		`{"op":"resolve","addr":"NOPE","want":"exists"}`,
		`{"op":"resolve","addr":"X"}`,
		`{"op":"resolve","addr":"X"}`,
		`{"op":"pick"}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serve(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	want := []string{`"protocol":1`, `"exists":true`, `"exists":false`, `not logged in`, `gh output`, `unsupported op`}
	for i, w := range want {
		if !strings.Contains(lines[i], w) {
			t.Errorf("line %d = %s, want %q", i, lines[i], w)
		}
	}
	// main runs serve over stdio; with an empty stdin it exits cleanly.
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
