// Command ds-resolve-gcp is the Google Secret Manager resolver plugin
// (§12, §37.4). It reads the latest version through the `gcloud` CLI,
// which must be installed and authenticated, hashes the value in memory,
// and returns existence and the hash. The value never leaves this process.
// The address is the secret name, optionally `projects/<p>/secrets/<name>`.
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
const notFound = "NOT_FOUND"

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
	return procplugin.Serve(in, out, "gcp", []string{procplugin.KindResolve}, handle)
}

func handle(req procplugin.Request) procplugin.Response {
	if req.Op != procplugin.OpResolve {
		return procplugin.Response{Error: "unsupported op " + req.Op}
	}
	raw, stderr, err := run("gcloud", "secrets", "versions", "access", "latest", "--secret", req.Addr)
	if err != nil {
		if strings.Contains(stderr, notFound) {
			no := false
			return procplugin.Response{Exists: &no}
		}
		return procplugin.Response{Error: "gcloud secrets versions access: " + strings.TrimSpace(err.Error()+" "+stderr)}
	}
	yes := true
	resp := procplugin.Response{Exists: &yes}
	if req.Want == procplugin.WantHash {
		sum := sha256.Sum256(raw)
		resp.Hash = hex.EncodeToString(sum[:])
	}
	return resp
}
