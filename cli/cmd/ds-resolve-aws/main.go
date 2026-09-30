// Command ds-resolve-aws is the AWS Secrets Manager resolver plugin (§12,
// §37.4). It reads the secret through the `aws` CLI, which must be
// installed and configured, hashes the value in memory, and returns
// existence and the hash. The value never leaves this process; the host
// rejects any reply that carries more. The address is the secret id or
// ARN.
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
const notFound = "ResourceNotFoundException"

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
	return procplugin.Serve(in, out, "aws", []string{procplugin.KindResolve}, handle)
}

func handle(req procplugin.Request) procplugin.Response {
	if req.Op != procplugin.OpResolve {
		return procplugin.Response{Error: "unsupported op " + req.Op}
	}
	raw, stderr, err := run("aws", "secretsmanager", "get-secret-value", "--secret-id", req.Addr, "--query", "SecretString", "--output", "text")
	if err != nil {
		if strings.Contains(stderr, notFound) {
			no := false
			return procplugin.Response{Exists: &no}
		}
		return procplugin.Response{Error: "aws secretsmanager get-secret-value: " + strings.TrimSpace(err.Error()+" "+stderr)}
	}
	yes := true
	resp := procplugin.Response{Exists: &yes}
	if req.Want == procplugin.WantHash {
		sum := sha256.Sum256(raw)
		resp.Hash = hex.EncodeToString(sum[:])
	}
	return resp
}
