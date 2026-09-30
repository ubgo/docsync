package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/docsync/cli"
)

func TestMainRuns(t *testing.T) {
	code := -1
	cli.Exit = func(c int) { code = c }
	defer func() { cli.Exit = os.Exit }()
	os.Args = []string{"ds", "--help"}
	main()
	if code != cli.ExitOK {
		t.Errorf("exit = %d", code)
	}
}

// The standard binary carries the ext tiers: a TOML key and a Go
// declaration extract through them, and `hcl:` picks resolve.
func TestStandardTiers(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		".ds/config.toml":   "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n",
		"config/app.toml":   "[server]\nport = 8081   # ds:def id=port-a2b6f8jk\n",
		"internal/store.go": "package store\n\n// ds:def id=save-b3c7g9kl\nfunc Save() error {\n\treturn nil\n}\n",
		"infra/main.tf":     "variable \"env\" {\n  default = \"prod\"\n}\n",
		"docs/a.md":         "<!-- ds:def id=env-c4d8h2lm file=infra/main.tf pick=hcl:variable.env.default -->\n\nPort [8081](ds:cfg?id=port-a2b6f8jk), env [prod](ds:cfg?id=env-c4d8h2lm), and [save](ds:block?id=save-b3c7g9kl).\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	code := -1
	cli.Exit = func(c int) { code = c }
	defer func() { cli.Exit = os.Exit }()
	var out strings.Builder
	realStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	t.Chdir(dir)
	os.Args = []string{"ds", "check", "--explain"}
	main()
	_ = w.Close()
	os.Stdout = realStdout
	raw, _ := io.ReadAll(r)
	out.Write(raw)
	if code != cli.ExitOK || !strings.Contains(out.String(), "config/app.toml") || !strings.Contains(out.String(), "internal/store.go") || !strings.Contains(out.String(), "toml") {
		t.Errorf("exit = %d out = %s", code, out.String())
	}
}
