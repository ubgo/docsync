package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ubgo/docsync/procplugin"
	"github.com/ubgo/docsync/render"
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
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[resolve]\nenabled = true\nstore_hash = true\nproviders = [\"github\", \"1password\", \"vault\"]\n")
	lookup := plugins(t, map[string]string{
		"ds-resolve-github":      `{"exists":true}`,
		"ds-resolve-onepassword": `{"exists":true,"hash":"1111111111111111111111111111111111111111111111111111111111111111"}`,
		"ds-resolve-vault":       `{"exists":true,"hash":"2222222222222222222222222222222222222222222222222222222222222222"}`,
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
		"ds-resolve-github":      `{"exists":true}`,
		"ds-resolve-onepassword": `{"exists":false}`,
		"ds-resolve-vault":       `{"exists":true,"hash":"2222222222222222222222222222222222222222222222222222222222222222"}`,
	})
	r = run2("check", "--resolve")
	if !strings.Contains(r.out, "resolve failed") {
		t.Errorf("missing address = %+v", r)
	}
	write(t, dir, ".ds/hashes.json", `{"op-stripe-p9c2v7ld":"OLD"}`)
	lookup = plugins(t, map[string]string{
		"ds-resolve-github":      `{"exists":true}`,
		"ds-resolve-onepassword": `{"exists":true,"hash":"2222222222222222222222222222222222222222222222222222222222222222"}`,
		"ds-resolve-vault":       `{"exists":true,"hash":"2222222222222222222222222222222222222222222222222222222222222222"}`,
	})
	r = run2("check", "--resolve")
	if !strings.Contains(r.out, "rotated") || strings.Contains(r.out, "out of sync") {
		t.Errorf("rotated = %+v", r)
	}
	// A provider that is not installed, or not allowed, is unverifiable; a
	// plugin that leaks a value is refused.
	lookup = plugins(t, map[string]string{"ds-resolve-onepassword": `{"exists":true,"hash":"2222222222222222222222222222222222222222222222222222222222222222","value":"leak"}`})
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[resolve]\nenabled = true\nproviders = [\"1password\"]\n")
	r = run2("check", "--resolve")
	if !strings.Contains(r.out, "not in resolve.providers") || !strings.Contains(r.out, "unexpected field") {
		t.Errorf("guards = %+v", r)
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[resolve]\nenabled = true\n")
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
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[resolve]\nenabled = true\nstore_hash = true\n")
	lookup = plugins(t, map[string]string{"ds-resolve-onepassword": `{"exists":true,"hash":"1111111111111111111111111111111111111111111111111111111111111111"}`, "ds-resolve-github": `{"exists":true}`, "ds-resolve-vault": `{"exists":true,"hash":"1111111111111111111111111111111111111111111111111111111111111111"}`})
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

// TestResolveNeedsEnabled pins resolve.enabled, which was parsed and never
// read (bug 83): --resolve without it contacts no plugin and no link, says
// so on stderr, and leaves --json a single document. With it, a hop whose
// plugin is missing carries a remedy about the plugin, not about links.
// promise:resolve-gated
func TestResolveNeedsEnabled(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	write(t, dir, "config/secrets.env", "STRIPE_KEY=op://Platform/stripe/credential   # ds:def id=op-stripe-p9c2v7ld secret=true truth=true\nAWS=arn:aws:secretsmanager:eu:1:secret:s   # ds:def id=aws-stripe-c3d4e5f6 secret=true from=op-stripe-p9c2v7ld\n")
	write(t, dir, "docs/runbook.md", "Stripe: <!-- ds:chain id=aws-stripe-c3d4e5f6 -->\n")
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[resolve]\nproviders = [\"1password\", \"aws\"]\n")
	asked := filepath.Join(t.TempDir(), "asked")
	lookup := func(name string) (string, error) {
		_ = os.WriteFile(asked, []byte(name), 0o644)
		return "", errors.New("not installed")
	}
	check := func(args ...string) result {
		var out, errb bytes.Buffer
		code := Run(args, WithDir(dir), WithIO(nil, &out, &errb), WithVCS(v), WithPluginLookup(lookup))
		return result{code, out.String(), errb.String()}
	}
	r := check("check", "--resolve", "--json")
	if !strings.Contains(r.err, resolveDisabled) || strings.Contains(r.out, resolveDisabled) || strings.Contains(r.out, "not reachable") {
		t.Errorf("disabled = %+v", r)
	}
	if _, err := os.Stat(asked); err == nil {
		t.Error("a plugin was looked up with resolve.enabled off")
	}
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[resolve]\nenabled = true\nproviders = [\"1password\", \"aws\"]\n")
	r = check("check", "--resolve")
	if strings.Contains(r.err, resolveDisabled) || !strings.Contains(r.out, "ds-resolve-onepassword") || !strings.Contains(r.out, "install the ds-resolve plugin for 1password") || strings.Contains(r.out, "external link") {
		t.Errorf("enabled = %+v", r)
	}
}

// TestLocalTargetPresent: check reads a local=true target on the machine
// running it, relative to the repository or at an absolute path, and a
// present one is no longer unverifiable (bug 85).
func TestLocalTargetPresent(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	abs := filepath.ToSlash(filepath.Join(t.TempDir(), "laptop.env"))
	write(t, dir, "docs/local.md", "<!-- ds:def id=prod-env-x4y5z6a7 file=.env.prod local=true pick=env:STRIPE_KEY -->\n<!-- ds:def id=laptop-d7e8f9a2 file="+abs+" local=true -->\n")
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	if got := stateAt(t, run(t, dir, v, "check", "--json").out, "docs/local.md", 1, "prod-env-x4y5z6a7"); got != "unverifiable" {
		t.Errorf("absent target = %q", got)
	}
	write(t, dir, ".env.prod", "STRIPE_KEY=op://Platform/stripe/credential\n")
	if err := os.WriteFile(filepath.FromSlash(abs), []byte("X=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := run(t, dir, v, "check", "--json").out
	if got := stateAt(t, out, "docs/local.md", 1, "prod-env-x4y5z6a7"); got != "ok" {
		t.Errorf("present relative target = %q", got)
	}
	if got := stateAt(t, out, "docs/local.md", 2, "laptop-d7e8f9a2"); got != "ok" {
		t.Errorf("present absolute target = %q", got)
	}
}

// TestShippedResolverPlugins holds every provider ds infers from an address
// shape, and env, which a def names with source=env, to a plugin that ships
// under the exact executable name ds runs: a command under cmd/, an entry in
// the release's extra_binaries, and a name the generated install scripts
// install. op:// addresses mapped to provider 1password and ds ran
// ds-resolve-1password, while the release shipped ds-resolve-onepassword,
// so 1Password resolve never worked and nothing noticed (bug 80).
func TestShippedResolverPlugins(t *testing.T) {
	t.Parallel()
	read := func(p string) string {
		raw, err := os.ReadFile(filepath.FromSlash(p))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	volt, sh, ps1 := read("cmd/ds/.volt.yml"), read("../install.sh"), read("../install.ps1")
	providers := append(append([]string{}, render.AddressProviderValues...), render.ProviderEnv)
	for _, p := range providers {
		exe := procplugin.Name(procplugin.KindResolve, resolverPlugin(p))
		if info, err := os.Stat(filepath.Join("cmd", exe, "main.go")); err != nil || info.IsDir() {
			t.Errorf("provider %s: ds runs %s, but cmd/%s/main.go does not exist", p, exe, exe)
		}
		if !strings.Contains(volt, "- ../"+exe+"\n") {
			t.Errorf("provider %s: %s is not in cmd/ds/.volt.yml extra_binaries, so no release archive carries it", p, exe)
		}
		for name, script := range map[string]string{"install.sh": sh, "install.ps1": ps1} {
			if !slices.Contains(strings.Fields(installList(script)), exe) {
				t.Errorf("provider %s: %s does not install %s; run `volt gen install cli/cmd/ds`", p, name, exe)
			}
		}
	}
}

// installList returns the quoted list of companion binaries a generated
// install script installs: the first double-quoted string on the line that
// assigns EXTRA_BINARIES (sh) or $ExtraBinaries (PowerShell).
func installList(script string) string {
	for _, l := range strings.Split(script, "\n") {
		if strings.HasPrefix(l, "EXTRA_BINARIES=") || strings.HasPrefix(l, "$ExtraBinaries =") {
			if _, rest, ok := strings.Cut(l, `"`); ok {
				list, _, _ := strings.Cut(rest, `"`)
				return list
			}
		}
	}
	return ""
}
