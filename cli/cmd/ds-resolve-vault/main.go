// Command ds-resolve-vault is the HashiCorp Vault resolver plugin (§12,
// §37.4). It reads a KV secret through the `vault` CLI, which must be
// installed and logged in, hashes the value in memory, and returns
// existence and the hash. The value never leaves this process. The address
// is `vault:<mount>/<path>#<field>`, the `vault:` optional; without
// `#<field>` the field is `value`.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/ubgo/docsync/procplugin"
)

// notFound is what the CLI prints on stderr for a missing secret; that
// answer is "does not exist", not a plugin failure.
const notFound = "No value found"

// addressPrefix marks a Vault address in a repository (§12).
const addressPrefix = "vault:"

// defaultField is the KV field read when the address names none.
const defaultField = "value"

// run executes the provider CLI and returns stdout, and stderr on failure;
// tests replace it.
var run = func(args ...string) ([]byte, string, error) {
	out, err := exec.Command(args[0], args[1:]...).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out, string(ee.Stderr), err
	}
	return out, "", err
}

// exit is swapped by tests.
var exit = os.Exit

func main() {
	if err := serve(os.Stdin, os.Stdout); err != nil {
		exit(1)
	}
}

func serve(in io.Reader, out io.Writer) error {
	return procplugin.Serve(in, out, "vault", []string{procplugin.KindResolve}, handle)
}

func handle(req procplugin.Request) procplugin.Response {
	if req.Op != procplugin.OpResolve {
		return procplugin.Response{Error: "unsupported op " + req.Op}
	}
	// ds infers this provider from the `vault:` prefix (§12) and passes the
	// address whole; the CLI knows no such prefix, so it is dropped here.
	// It used to be passed on, and every vault: address failed (bug 81).
	path, field, ok := strings.Cut(strings.TrimPrefix(req.Addr, addressPrefix), "#")
	if !ok || field == "" {
		field = defaultField
	}
	raw, stderr, err := run("vault", "kv", "get", "-field="+field, path)
	if err != nil {
		if strings.Contains(stderr, notFound) {
			no := false
			return procplugin.Response{Exists: &no}
		}
		return procplugin.Response{Error: "vault kv get: " + strings.TrimSpace(err.Error()+" "+stderr)}
	}
	yes := true
	resp := procplugin.Response{Exists: &yes}
	if req.Want == procplugin.WantHash {
		sum := sha256.Sum256(raw)
		resp.Hash = hex.EncodeToString(sum[:])
	}
	return resp
}
