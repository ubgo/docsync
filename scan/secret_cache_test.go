package scan

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/internal/glob"
)

// recordingCache remembers what it was asked to store, so a test can assert
// on what never reached it rather than on what a file happens to contain.
type recordingCache struct {
	put map[string]extract.Found
}

func (c *recordingCache) Get(string, string) (extract.Found, bool) { return extract.Found{}, false }

func (c *recordingCache) Put(p, _ string, f extract.Found) {
	if c.put == nil {
		c.put = map[string]extract.Found{}
	}
	c.put[p] = f
}

// TestSecretsNeverReachTheCache pins SPEC section 12 — "never stores a
// value" — at the one place anything is handed to a Cache. A cache entry
// holds block bodies verbatim, and a cache travels: into git, CI caches and
// backups. The guard is in the caller so a custom Cache cannot skip it.
// Pins bug 2.
// promise:cache-no-secret
func TestSecretsNeverReachTheCache(t *testing.T) {
	t.Parallel()
	const planted = "hunter2-PLANTED-SECRET"
	fsys := fstest.MapFS{
		// Secret by configured path.
		"conf/creds.yaml": {Data: []byte("token: " + planted + "   # ds:def id=tok-k7m2p4xq\n")},
		// Secret by its own directive, in a path no glob matches.
		"app/env.yaml": {Data: []byte("key: " + planted + "   # ds:def id=key-h3v8n2wd secret=true\n")},
		// Local by its own directive.
		"app/local.yaml": {Data: []byte("host: " + planted + "   # ds:def id=loc-t4k2b9rf local=true\n")},
		// Ordinary: must still be cached, or every scan pays full cost.
		"app/app.yaml": {Data: []byte("port: 8080   # ds:def id=port-m4w8k2qn\n")},
	}
	secret, err := glob.CompileAll([]string{"conf/**"})
	if err != nil {
		t.Fatal(err)
	}
	include, err := glob.CompileAll([]string{"**"})
	if err != nil {
		t.Fatal(err)
	}
	cache := &recordingCache{}
	if _, err := Scan(context.Background(), fsys, Options{
		Prefix: "ds", Repo: "api", Include: include, Secret: secret, Cache: cache,
	}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"conf/creds.yaml", "app/env.yaml", "app/local.yaml"} {
		if _, cached := cache.put[p]; cached {
			t.Errorf("%s must never be handed to a cache", p)
		}
	}
	if _, cached := cache.put["app/app.yaml"]; !cached {
		t.Error("an ordinary file must still be cached")
	}
	// Whatever did reach the cache must not carry the value.
	for p, f := range cache.put {
		for _, d := range f.Defs {
			if d.Block.Content != "" && contains(d.Block.Content, planted) {
				t.Errorf("%s leaked the planted value into the cache", p)
			}
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
