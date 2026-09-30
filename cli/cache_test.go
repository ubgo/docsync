package cli

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
	"github.com/ubgo/docsync/extract"
)

// The cache must round-trip everything extraction produced, including the
// fields the public JSON shape hides.
func TestCacheRoundTrip(t *testing.T) {
	t.Parallel()
	found := extract.Found{
		Defs: []extract.Def{{
			Directive: directive.Directive{Verb: "def", Args: map[string]string{"id": "a-b3c7g9kl"}, Keys: []string{"id"}},
			Block:     block.Block{ID: "a-b3c7g9kl", Kind: block.KindFunc, Pos: block.Position{Start: 1, End: 3}, Content: "func A() {}", Hash: "h"},
		}},
		Refs: []extract.Ref{{
			Directive: directive.Directive{Verb: "block", Args: map[string]string{"id": "a-b3c7g9kl"}, Keys: []string{"id"}},
			Reference: block.Reference{Verb: "block", ID: "a-b3c7g9kl", Pos: block.Position{Start: 4, End: 4}, Sentence: "s", Region: &block.Region{Start: 1, End: 2, Hash: "rh", Text: "region"}},
		}, {
			Reference: block.Reference{Verb: "cfg", ID: "x"},
		}},
		Page: &extract.Page{Covers: []string{"a-b3c7g9kl"}},
	}
	c := &extractCache{entries: map[string]cacheEntry{}}
	if _, ok := c.Get("f.go", "h1"); ok {
		t.Error("empty cache hit")
	}
	c.Put("f.go", "h1", found)
	raw, _ := json.Marshal(cacheFile{Format: cacheFormat, Entries: c.entries})
	var back cacheFile
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	c2 := &extractCache{entries: back.Entries}
	got, ok := c2.Get("f.go", "h1")
	if !ok || !reflect.DeepEqual(got, found) {
		t.Errorf("round trip lost data:\n got %+v\nwant %+v", got, found)
	}
	if _, ok := c2.Get("f.go", "h2"); ok {
		t.Error("stale hash must miss")
	}
	// Every json:"-" field on the cached types is covered above; a new one
	// fails this reflection check so the cache wire type gets extended.
	for _, typ := range []reflect.Type{reflect.TypeOf(block.Block{}), reflect.TypeOf(block.Reference{}), reflect.TypeOf(block.Region{})} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.Tag.Get("json") == "-" && f.Name != "Content" && f.Name != "Text" {
				t.Errorf("%s.%s is hidden from JSON and not carried by the cache", typ.Name(), f.Name)
			}
		}
	}
}

// TestCacheKeyedOnExtractionInputs is the bug, not the mechanism. The cache
// was keyed on a file's content alone, so editing `prefix` in .ds/config.toml
// served entries extracted under the old prefix: on the docsync repo itself
// that reported 14 defs and 110 refs that existed nowhere in the tree, and
// clearing the cache by hand was the only way to see the truth. The tier list
// is in the fingerprint for the same reason — a binary built with a different
// extractor set reads the same bytes differently.
// Pins bug 12.
func TestCacheKeyedOnExtractionInputs(t *testing.T) {
	t.Parallel()
	const (
		prefix      = "ds"
		otherPrefix = "dsself"
	)
	tiers := []string{"markdown", "code", "text"}
	const lineLimit = 2000
	found := extract.Found{Page: &extract.Page{Covers: []string{"a-b3c7g9kl"}}}

	st := NewStore(t.TempDir())
	c, err := st.LoadCache(cacheInputs(extract.Rule, prefix, tiers, lineLimit), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	c.Put("f.go", "h1", found)
	if err := st.SaveCache(c); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		inputs string
		want   bool
	}{
		{"the inputs that wrote it", cacheInputs(extract.Rule, prefix, tiers, lineLimit), true},
		{"another prefix", cacheInputs(extract.Rule, otherPrefix, tiers, lineLimit), false},
		{"a tier removed", cacheInputs(extract.Rule, prefix, []string{"markdown", "text"}, lineLimit), false},
		{"the tiers reordered", cacheInputs(extract.Rule, prefix, []string{"code", "markdown", "text"}, lineLimit), false},
		{"another extraction rule", cacheInputs(extract.Rule+1, prefix, tiers, lineLimit), false},
		// A stamped entry skips the long-line test that admitted it.
		{"another long-line limit", cacheInputs(extract.Rule, prefix, tiers, lineLimit-1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			back, err := st.LoadCache(tc.inputs, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := back.Get("f.go", "h1"); ok != tc.want {
				t.Errorf("served = %v, want %v", ok, tc.want)
			}
		})
	}
	// The two parts are separated, not concatenated: without a delimiter a
	// prefix that ends where a tier name begins would collide.
	if cacheInputs(1, "a", []string{"b"}, 1) == cacheInputs(1, "ab", nil, 1) || cacheInputs(1, "2", nil, 1) == cacheInputs(12, "", nil, 1) || cacheInputs(1, "", []string{"1"}, 1) == cacheInputs(1, "", nil, 11) {
		t.Error("the fingerprint must distinguish its components")
	}
}

// TestTiersMirrorsTheLibraryRegistry pins what the cache is keyed on and what
// doctor prints: both must name the tiers the scan will actually use.
func TestTiersMirrorsTheLibraryRegistry(t *testing.T) {
	t.Parallel()
	a := &App{}
	def := a.tiers()
	if len(def) == 0 || def[len(def)-1] != "text" {
		t.Fatalf("the default registry ends in the universal fallback: %v", def)
	}
	// WithExtractor entries come first, highest precedence last-registered,
	// which is what Registry.Prepend does to them.
	a = &App{extractors: []extract.Extractor{namedTier("one"), namedTier("two")}}
	got := a.tiers()
	if got[0] != "two" || got[1] != "one" {
		t.Errorf("precedence = %v, want two, one first", got[:2])
	}
	// A replaced registry is honoured instead of the defaults, and asking
	// twice does not double it.
	reg := &extract.Registry{}
	reg.Add(namedTier("only"))
	a = &App{registry: reg}
	if first, second := a.tiers(), a.tiers(); !reflect.DeepEqual(first, []string{"only"}) || !reflect.DeepEqual(second, first) {
		t.Errorf("registry tiers = %v then %v", first, second)
	}
}

// namedTier is an extractor that exists only to be named; tiers() reads
// Name and nothing else.
type namedTier string

func (n namedTier) Name() string                               { return string(n) }
func (namedTier) Match(string) bool                            { return false }
func (namedTier) Extract(string, []byte, string) extract.Found { return extract.Found{} }

// TestStatCacheServesOnlyAConfirmedStamp pins the size-and-time shortcut:
// an entry is served without a read only for the exact size and time Stamp
// recorded, a time too close to the stamp is not recorded (a second write
// in the same clock tick would keep it), and the stamp survives a save.
func TestStatCacheServesOnlyAConfirmedStamp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	old := now.Add(-time.Hour)
	found := extract.Found{Page: &extract.Page{Covers: []string{"a-b3c7g9kl"}}}
	st := NewStore(t.TempDir())
	c, err := st.LoadCache("in", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	c.Stamp("none.go", 3, old) // no entry: nothing to stamp
	if _, ok := c.entries["none.go"]; ok {
		t.Fatal("Stamp created an entry")
	}
	c.Put("f.go", "h1", found)
	if _, ok := c.Unchanged("f.go", 3, old); ok {
		t.Error("an unstamped entry was served by metadata")
	}
	c.Stamp("f.go", 3, old)
	for _, tc := range []struct {
		name string
		size int64
		mod  time.Time
		want bool
	}{
		{"the stamped size and time", 3, old, true},
		{"another size", 4, old, false},
		{"another time", 3, old.Add(time.Nanosecond), false},
	} {
		if _, ok := c.Unchanged("f.go", tc.size, tc.mod); ok != tc.want {
			t.Errorf("%s: served = %v, want %v", tc.name, ok, tc.want)
		}
	}
	if _, ok := c.Unchanged("g.go", 3, old); ok {
		t.Error("a path with no entry was served")
	}
	if err := st.SaveCache(c); err != nil {
		t.Fatal(err)
	}
	back, err := st.LoadCache("in", func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := back.Unchanged("f.go", 3, old); !ok || got.Page == nil || got.Page.Covers[0] != "a-b3c7g9kl" {
		t.Errorf("after a save: %+v, %v", got, ok)
	}
	// Re-stamping what is already recorded does not dirty the cache.
	back.Stamp("f.go", 3, old)
	if back.dirty {
		t.Error("an unchanged stamp dirtied the cache")
	}
	// Racily clean: a time inside the window, or in the future, clears
	// the stamp rather than recording it.
	for _, recent := range []time.Time{now.Add(-racyWindow + time.Millisecond), now.Add(time.Minute)} {
		back.Stamp("f.go", 3, old)
		back.Stamp("f.go", 3, recent)
		if _, ok := back.Unchanged("f.go", 3, recent); ok {
			t.Errorf("a time %v from the stamp was trusted", recent.Sub(now))
		}
		if _, ok := back.Unchanged("f.go", 3, old); ok {
			t.Errorf("the older stamp survived a racy one at %v", recent.Sub(now))
		}
	}
	// Exactly the window old is trusted.
	back.Stamp("f.go", 3, now.Add(-racyWindow))
	if _, ok := back.Unchanged("f.go", 3, now.Add(-racyWindow)); !ok {
		t.Error("a time exactly racyWindow old was not trusted")
	}
}
