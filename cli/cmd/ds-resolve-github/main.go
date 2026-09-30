// Command ds-resolve-github is the GitHub secret resolver plugin (§12,
// §37.4). It answers existence only, because GitHub lists secret names and
// never values, through the `gh` CLI which must be installed and logged in.
package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"

	"github.com/ubgo/docsync/procplugin"
)

// ghList is the command that lists repository secret names as JSON.
var ghList = []string{"gh", "secret", "list", "--json", "name"}

// run executes a command; tests replace it.
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
	return procplugin.Serve(in, out, "github", []string{procplugin.KindResolve}, handle)
}

func handle(req procplugin.Request) procplugin.Response {
	if req.Op != procplugin.OpResolve {
		return procplugin.Response{Error: "unsupported op " + req.Op}
	}
	raw, err := run(ghList...)
	if err != nil {
		return procplugin.Response{Error: "gh secret list: " + err.Error()}
	}
	var names []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &names); err != nil {
		return procplugin.Response{Error: "gh output: " + err.Error()}
	}
	exists := false
	for _, n := range names {
		if n.Name == req.Addr {
			exists = true
		}
	}
	return procplugin.Response{Exists: &exists}
}
