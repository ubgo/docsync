package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/procplugin"
	"github.com/ubgo/docsync/render"
)

// Secret resolution under `check --resolve` (§12): each provider is a
// process plugin `ds-resolve-<provider>` on PATH speaking the procplugin
// protocol, so any language can add one and no provider SDK enters this
// module. GitHub answers existence only; providers that can read a value
// return its hash and never the value, which the host enforces. Truth hashes
// are kept in .ds/hashes.json when resolve.store_hash is on, so the next run
// can report `rotated`.
const HashesFile = "hashes.json"

// LoadHashes reads stored truth hashes; missing means none.
func (s *Store) LoadHashes() (map[string]string, error) {
	raw, ok, err := s.read(HashesFile)
	if err != nil || !ok {
		return map[string]string{}, err
	}
	out := map[string]string{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", HashesFile, err)
	}
	return out, nil
}

// SaveHashes writes them.
func (s *Store) SaveHashes(m map[string]string) error {
	raw, _ := json.MarshalIndent(m, "", "  ")
	return s.Write(HashesFile, raw)
}

// resolver builds the library hook from the plugin host. Providers outside
// resolve.providers (when set) are reported unreachable rather than called,
// so a machine without a login for one provider still checks the others.
func (a *App) resolver(cfg config.Config) check.Resolver {
	host := procplugin.Host{LookPath: a.pluginLookPath}
	allowed := map[string]bool{}
	for _, p := range cfg.Resolve.Providers {
		allowed[p] = true
	}
	return func(provider, addr string) check.ResolveResult {
		if len(allowed) > 0 && !allowed[provider] {
			return check.ResolveResult{Err: fmt.Errorf("provider %s is not in resolve.providers", provider)}
		}
		want := procplugin.WantHash
		if provider == render.ProviderGitHub {
			want = procplugin.WantExists
		}
		exists, hash, err := host.Resolve(context.Background(), provider, secretName(provider, addr), want)
		if err != nil {
			return check.ResolveResult{Err: err}
		}
		return check.ResolveResult{Checked: true, Exists: exists, Hash: hash}
	}
}

// secretName reduces an address to what its provider is asked about: the
// variable name for GitHub, the address itself elsewhere.
func secretName(provider, addr string) string {
	if provider == render.ProviderGitHub {
		if i := strings.Index(addr, "secrets."); i >= 0 {
			name := addr[i+len("secrets."):]
			if j := strings.IndexAny(name, " }"); j >= 0 {
				name = name[:j]
			}
			return name
		}
	}
	return addr
}
