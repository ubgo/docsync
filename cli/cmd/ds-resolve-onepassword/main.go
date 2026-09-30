// Command ds-resolve-onepassword is the 1Password resolver plugin (§12,
// §37.4). It reads an `op://` reference through the `op` CLI, hashes the
// value in memory, and returns existence and the hash. The value never
// leaves this process; the host rejects any reply that carries more.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/ubgo/docsync/procplugin"
)

// run executes `op read`; tests replace it.
var run = func(args ...string) ([]byte, error) {
	return exec.Command(args[0], args[1:]...).Output()
}

// exit is swapped by tests.
var exit = os.Exit

func main() {
	if err := serve(os.Stdin, os.Stdout); err != nil {
		exit(1)
	}
}

func serve(in io.Reader, out io.Writer) error {
	return procplugin.Serve(in, out, "1password", []string{procplugin.KindResolve}, handle)
}

func handle(req procplugin.Request) procplugin.Response {
	if req.Op != procplugin.OpResolve {
		return procplugin.Response{Error: "unsupported op " + req.Op}
	}
	raw, err := run("op", "read", "--no-newline", req.Addr)
	if err != nil {
		// op exits non-zero for a missing item; that is "does not exist",
		// not a plugin failure, when the reference is well formed.
		if strings.HasPrefix(req.Addr, "op://") {
			no := false
			return procplugin.Response{Exists: &no}
		}
		return procplugin.Response{Error: "op read: " + err.Error()}
	}
	yes := true
	resp := procplugin.Response{Exists: &yes}
	if req.Want == procplugin.WantHash {
		sum := sha256.Sum256(raw)
		resp.Hash = hex.EncodeToString(sum[:])
	}
	return resp
}
