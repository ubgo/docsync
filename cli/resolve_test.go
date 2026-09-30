package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// plugins writes, for each name, a program answering the procplugin protocol
// with the given reply line, and returns a lookup that finds it by name. The
// program is a real child process on every platform: a shell script on Unix,
// and a batch file on Windows, where a script with a #! line is not
// executable. Replies must not hold a character cmd.exe interprets
// (& | < > ^ %); the JSON the tests use does not.
func plugins(t *testing.T, replies map[string]string) func(string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	file := func(name string) string {
		if runtime.GOOS == windowsOS {
			return filepath.Join(dir, name+".bat")
		}
		return filepath.Join(dir, name)
	}
	for name, reply := range replies {
		body := "#!/bin/sh\nprintf '{\"protocol\":1}\\n" + reply + "\\n'\n"
		if runtime.GOOS == windowsOS {
			body = "@echo off\r\necho {\"protocol\":1}\r\necho " + reply + "\r\n"
		}
		if err := os.WriteFile(file(name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return func(name string) (string, error) {
		if _, ok := replies[name]; ok {
			return file(name), nil
		}
		return "", errors.New("not installed")
	}
}

func TestResolveSecrets(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "config/secrets.env", "STRIPE_KEY=op://Platform/stripe/credential   # ds:def id=op-stripe-p9c2v7ld secret=true truth=true\nGONE=op://Platform/gone   # ds:def id=gone-a2b6f8jk secret=true from=op-stripe-p9c2v7ld\n")
	write(t, dir, ".github/workflows/deploy.yml", "env:\n  STRIPE_KEY: ${{ secrets.STRIPE_KEY }}   # ds:def id=gh-stripe-r4t6x2mb secret=true from=op-stripe-p9c2v7ld\n  VAULT: vault:kv/stripe   # ds:def id=vault-b3c7g9kl secret=true from=op-stripe-p9c2v7ld\n")
	write(t, dir, "docs/runbook.md", "Stripe: <!-- ds:chain id=gh-stripe-r4t6x2mb -->\n[v](ds:block?id=vault-b3c7g9kl) [g](ds:block?id=gone-a2b6f8jk)\n")
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[resolve]\nstore_hash = true\nproviders = [\"github\", \"1password\", \"vault\"]\n")
	lookup := plugins(t, map[string]string{
		"ds-resolve-github":    `{"exists":true}`,
		"ds-resolve-1password": `{"exists":true,"hash":"1111111111111111111111111111111111111111111111111111111111111111"}`,
		"ds-resolve-vault":     `{"exists":true,"hash":"2222222222222222222222222222222222222222222222222222222222222222"}`,
	})
	run2 := func(args ...string) result {
		var out, errb bytes.Buffer
		code := Run(args, WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithPluginLookup(lookup))
		return result{code, out.String(), errb.String()}
	}
	// Without --resolve nothing is asked; with it, the vault copy is out of
	// sync with the 1Password truth and the truth hash is stored.
	if r := run2("check"); strings.Contains(r.out, "out of sync") {
		t.Errorf("no resolve = %+v", r)
	}
	r := run2("check", "--resolve")
	if r.code != ExitFindings || !strings.Contains(r.out, "out of sync") || !strings.Contains(r.out, "vault-b3c7g9kl differs from truth") {
		t.Errorf("resolve = %+v", r)
	}
	hashes, err := NewStore(dir).LoadHashes()
	if err != nil || hashes["op-stripe-p9c2v7ld"] != "1111111111111111111111111111111111111111111111111111111111111111" {
		t.Errorf("stored hashes = %v %v", hashes, err)
	}
	// The gone address is asked at 1password too (same plugin answers exists);
	// swap the plugin to say it does not exist and rotate the truth.
	lookup = plugins(t, map[string]string{
		"ds-resolve-github":    `{"exists":true}`,
		"ds-resolve-1password": `{"exists":false}`,
		"ds-resolve-vault":     `{"exists":true,"hash":"2222222222222222222222222222222222222222222222222222222222222222"}`,
	})
	r = run2("check", "--resolve")
	if !strings.Contains(r.out, "resolve failed") {
		t.Errorf("missing address = %+v", r)
	}
	write(t, dir, ".ds/hashes.json", `{"op-stripe-p9c2v7ld":"OLD"}`)
	lookup = plugins(t, map[string]string{
		"ds-resolve-github":    `{"exists":true}`,
		"ds-resolve-1password": `{"exists":true,"hash":"2222222222222222222222222222222222222222222222222222222222222222"}`,
		"ds-resolve-vault":     `{"exists":true,"hash":"2222222222222222222222222222222222222222222222222222222222222222"}`,
	})
	r = run2("check", "--resolve")
	if !strings.Contains(r.out, "rotated") || strings.Contains(r.out, "out of sync") {
		t.Errorf("rotated = %+v", r)
	}
	// A provider that is not installed, or not allowed, is unverifiable; a
	// plugin that leaks a value is refused.
	lookup = plugins(t, map[string]string{"ds-resolve-1password": `{"exists":true,"hash":"2222222222222222222222222222222222222222222222222222222222222222","value":"leak"}`})
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[resolve]\nproviders = [\"1password\"]\n")
	r = run2("check", "--resolve")
	if !strings.Contains(r.out, "not in resolve.providers") || !strings.Contains(r.out, "unexpected field") {
		t.Errorf("guards = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	lookup = plugins(t, nil)
	if r := run2("check", "--resolve"); !strings.Contains(r.out, "not reachable") {
		t.Errorf("no plugins = %+v", r)
	}
	// Corrupt hashes file; hash write failure.
	write(t, dir, ".ds/hashes.json", "{")
	if r := run2("check", "--resolve"); r.code != ExitError {
		t.Errorf("corrupt hashes = %+v", r)
	}
	os.Remove(filepath.Join(dir, ".ds", "hashes.json"))
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[resolve]\nstore_hash = true\n")
	lookup = plugins(t, map[string]string{"ds-resolve-1password": `{"exists":true,"hash":"1111111111111111111111111111111111111111111111111111111111111111"}`, "ds-resolve-github": `{"exists":true}`, "ds-resolve-vault": `{"exists":true,"hash":"1111111111111111111111111111111111111111111111111111111111111111"}`})
	if err := os.Mkdir(filepath.Join(dir, ".ds", HashesFile+writeTempExt), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run2("check", "--resolve"); r.code != ExitError {
		t.Errorf("hash write failure = %+v", r)
	}
	if secretName("github", "${{ secrets.STRIPE_KEY }}") != "STRIPE_KEY" || secretName("github", "plain") != "plain" || secretName("vault", "vault:x") != "vault:x" {
		t.Error("secretName")
	}
}
