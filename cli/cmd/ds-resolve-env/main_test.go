package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// TestEnvResolver pins the plugin that makes source=env hops checkable
// (bug 83): a set variable exists and hashes, an unset one is an error and
// never "does not exist", a non-name is refused, and the value never leaves.
func TestEnvResolver(t *testing.T) {
	lookup = func(name string) (string, bool) {
		if name == "STRIPE_KEY" {
			return "sk_live_SENTINEL", true
		}
		return "", false
	}
	in := strings.Join([]string{
		`{"op":"handshake","protocol":1}`,
		`{"op":"resolve","addr":"STRIPE_KEY","want":"hash"}`,
		`{"op":"resolve","addr":"STRIPE_KEY","want":"exists"}`,
		`{"op":"resolve","addr":"UNSET_KEY","want":"hash"}`,
		`{"op":"resolve","addr":"key := os.Getenv(\"STRIPE_KEY\")","want":"hash"}`,
		`{"op":"query"}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serve(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	sum := sha256.Sum256([]byte("sk_live_SENTINEL"))
	want := []string{`"name":"env"`, `"hash":"` + hex.EncodeToString(sum[:]) + `"`, `"exists":true`, `UNSET_KEY is not set`, `not an environment variable name`, `unsupported op`}
	if len(lines) != len(want) {
		t.Fatalf("replies = %v", lines)
	}
	for i, w := range want {
		if !strings.Contains(lines[i], w) {
			t.Errorf("line %d = %s, want %q", i, lines[i], w)
		}
	}
	if strings.Contains(lines[2], "hash") {
		t.Error("exists-only must not hash")
	}
	if strings.Contains(lines[3], "exists") || strings.Contains(lines[4], "exists") {
		t.Errorf("an unset or malformed name must be an error, never an answer: %s / %s", lines[3], lines[4])
	}
	if strings.Contains(out.String(), "SENTINEL") {
		t.Error("the value must never leave the plugin")
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
