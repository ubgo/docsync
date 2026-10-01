// Command ds-resolve-env is the environment-variable resolver plugin (§12,
// §37.4), for the hops a def marks `source=env`: code that reads a variable
// such as STRIPE_KEY from its process environment. The address is the
// variable's name. The plugin looks the name up in its own environment,
// which it inherits from `ds check --resolve`, hashes the value in memory,
// and returns existence and the hash; the value never leaves this process.
//
// A variable that is not set is an error, which ds reports as unverifiable,
// not as "does not exist": whether a variable is set is a fact about the
// process running the check, not about the deployment the chain describes,
// so its absence on a laptop is no evidence that production lacks it. Run
// the check where the variables are set -- the deploy job, a container
// started like production -- and a copy that differs from its truth is
// `out of sync`. Before this plugin shipped, every source=env hop was
// unverifiable for want of an executable (bug 83).
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"regexp"

	"github.com/ubgo/docsync/procplugin"
)

// providerName is what the handshake reports, the provider ds names.
const providerName = "env"

// nameRE is a portable environment variable name. Anything else -- a
// whole statement such as `os.Getenv("STRIPE_KEY")` left unpicked -- is
// refused with the reason rather than looked up and reported unset.
var nameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// lookup reads the environment; tests replace it.
var lookup = os.LookupEnv

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
	if !nameRE.MatchString(req.Addr) {
		return procplugin.Response{Error: "not an environment variable name; pick= the name out of the def's line"}
	}
	value, ok := lookup(req.Addr)
	if !ok {
		return procplugin.Response{Error: req.Addr + " is not set in the environment ds check runs in"}
	}
	yes := true
	resp := procplugin.Response{Exists: &yes}
	if req.Want == procplugin.WantHash {
		sum := sha256.Sum256([]byte(value))
		resp.Hash = hex.EncodeToString(sum[:])
	}
	return resp
}
