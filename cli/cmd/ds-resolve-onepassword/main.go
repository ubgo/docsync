// Command ds-resolve-onepassword is the 1Password resolver plugin (§12,
// §37.4). It reads an `op://` reference through the `op` CLI, hashes the
// value in memory, and returns existence and the hash. The value never
// leaves this process; the host rejects any reply that carries more.
//
// ds asks this executable about the provider it calls "1password": addresses
// starting op://, and defs with source=1password.
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

// providerName is what the handshake reports: the provider ds names, which
// differs from this executable's name.
const providerName = "1password"

// notFound lists what `op read` prints on stderr when the reference names a
// vault, item or field that does not exist. Only these answer "does not
// exist". Every other failure -- signed out, locked, a dismissed prompt, no
// network, a malformed reference -- says nothing about the address and is
// returned as an error, which ds reports as unverifiable. The plugin used to
// call every failure "does not exist", so a locked op turned each 1Password
// hop into a `resolve failed` error (bug 82).
var notFound = []string{
	"isn't an item",
	"isn't a vault",
	"isn't a field",
	"does not have a field",
	"no item found",
}

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
	return procplugin.Serve(in, out, providerName, []string{procplugin.KindResolve}, handle)
}

func handle(req procplugin.Request) procplugin.Response {
	if req.Op != procplugin.OpResolve {
		return procplugin.Response{Error: "unsupported op " + req.Op}
	}
	raw, stderr, err := run("op", "read", "--no-newline", req.Addr)
	if err != nil {
		if missing(stderr) {
			no := false
			return procplugin.Response{Exists: &no}
		}
		return procplugin.Response{Error: "op read: " + strings.TrimSpace(err.Error()+" "+stderr)}
	}
	yes := true
	resp := procplugin.Response{Exists: &yes}
	if req.Want == procplugin.WantHash {
		sum := sha256.Sum256(raw)
		resp.Hash = hex.EncodeToString(sum[:])
	}
	return resp
}

// missing reports whether op's stderr says the reference names nothing.
func missing(stderr string) bool {
	for _, s := range notFound {
		if strings.Contains(stderr, s) {
			return true
		}
	}
	return false
}
