package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/docsync/extract"
)

// The extraction cache (§16 "incremental by default") lives in
// .ds/cache/extract.json: per file, the content hash and what extracting
// those bytes produced. A file whose bytes did not change is not
// re-extracted. Files with problems are never cached (the library rule), so
// a fixed directive is always re-read.
const (
	CacheDir  = "cache"
	CacheFile = "extract.json"
	// cacheFormat guards the file's shape; a different number is ignored,
	// not misread. The extraction rules are not its business: extract.Rule
	// is part of every entry's inputs (cacheInputs), so a rule change can
	// never be served old extents, however this number is left.
	cacheFormat = 2
)

// cachedFound is extract.Found as it survives JSON. Problems carry errors
// and are never cached anyway. Two fields are `json:"-"` on the public
// types because the goldens must not carry source text (block content and
// region text); the cache needs them, so it stores them beside the value
// and puts them back on load. A field added to Block or Reference with
// `json:"-"` must be added here too; the round-trip test pins the current
// set.
type cachedFound struct {
	Defs []cachedDef   `json:"defs"`
	Refs []cachedRef   `json:"refs"`
	Page *extract.Page `json:"page,omitempty"`
}

type cachedDef struct {
	extract.Def
	Content string `json:"content"`
}

type cachedRef struct {
	extract.Ref
	RegionText string `json:"region_text,omitempty"`
}

func toCached(f extract.Found) cachedFound {
	c := cachedFound{Page: f.Page}
	for _, d := range f.Defs {
		c.Defs = append(c.Defs, cachedDef{Def: d, Content: d.Block.Content})
	}
	for _, r := range f.Refs {
		cr := cachedRef{Ref: r}
		if r.Reference.Region != nil {
			cr.RegionText = r.Reference.Region.Text
		}
		c.Refs = append(c.Refs, cr)
	}
	return c
}

func fromCached(c cachedFound) extract.Found {
	f := extract.Found{Page: c.Page}
	for _, d := range c.Defs {
		d.Def.Block.Content = d.Content
		f.Defs = append(f.Defs, d.Def)
	}
	for _, r := range c.Refs {
		if r.Ref.Reference.Region != nil {
			reg := *r.Ref.Reference.Region
			reg.Text = r.RegionText
			r.Ref.Reference.Region = &reg
		}
		f.Refs = append(f.Refs, r.Ref)
	}
	return f
}

type cacheEntry struct {
	Hash  string      `json:"hash"`
	Found cachedFound `json:"found"`
	// Size and Mod (Unix nanoseconds) are the file's metadata when this
	// entry was last confirmed against its bytes, so a later scan can skip
	// reading it (Unchanged). Zero when never stamped, which never matches:
	// an entry from before the stamp existed is confirmed by reading once.
	Size int64 `json:"size,omitempty"`
	Mod  int64 `json:"mod,omitempty"`
}

type cacheFile struct {
	Format int `json:"format"`
	// Inputs is cacheInputs at the time of writing; see extractCache.
	Inputs  string                `json:"inputs"`
	Entries map[string]cacheEntry `json:"entries"`
}

// cacheInputs fingerprints everything other than a file's bytes that its
// extraction depends on: the extraction rule version, the directive prefix
// the scanner searches for, the extractor tiers in precedence order, and
// the long-line limit — an entry served by size and modification time
// (Unchanged) skips the long-line test that admitted it, so a lowered limit
// must not be served entries admitted under the old one — and the program
// that extracted it (buildStamp). A cache entry is valid only for the inputs
// that produced it.
//
// Why it exists: the cache was keyed on content alone. After the extent fix
// it served the old extents, so the fix silently did not apply; editing
// `prefix` served entries extracted under the old one — on this repo 14
// defs and 110 refs that existed nowhere in the tree. The rule was once
// guarded by bumping cacheFormat by hand, a convention easy to forget; it is
// part of the key instead. The rule alone is not enough either: a fix to
// what a hash covers that keeps the rule (Go string text, bug 22) left
// unchanged files served their old hashes until `--full`, so the program
// is part of the key as well.
// dsself:def id=cacheinputs-vyhx6vbz owner=@docsync stability=stable
func cacheInputs(rule int, prefix string, tiers []string, maxLineChars int, build string) string {
	return strconv.Itoa(rule) + "\x00" + prefix + "\x00" + strings.Join(tiers, ",") + "\x00" + strconv.Itoa(maxLineChars) + "\x00" + build
}

// buildStamp identifies the running ds program by its executable's size and
// modification time, so a cache written by one build is never served to
// another. Any rebuild or upgrade drops the cache once, which costs one
// full extraction; serving a different build's extents and hashes would
// report drift that is not there, or hide drift that is. exe and stat are
// os.Executable and os.Stat; they are parameters so the failure paths are
// testable. When the executable cannot be found the stamp is empty, which
// still keys the cache on every other input.
func buildStamp(exe func() (string, error), stat func(string) (os.FileInfo, error)) string {
	p, err := exe()
	if err != nil {
		return ""
	}
	fi, err := stat(p)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(fi.Size(), 10) + ":" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
}

// extractCache implements scan.Cache over the store. inputs is the
// fingerprint of everything other than a file's bytes that its extraction
// depended on; entries written under a different one are discarded rather
// than served.
type extractCache struct {
	entries map[string]cacheEntry
	inputs  string
	dirty   bool
	// now is the clock Stamp measures a modification time against.
	now func() time.Time
}

// racyWindow is how recent a modification time may be and still be trusted
// by Stamp. A file written twice within one tick of its filesystem's clock
// keeps the same time, and some filesystems tick in whole seconds (FAT in
// two); git calls such an entry "racily clean" and re-reads it. A time
// within this window of the stamp is not recorded, so the next scan reads
// that file again, which is the only cost.
const racyWindow = 2 * time.Second

func (c *extractCache) Get(p, hash string) (extract.Found, bool) {
	e, ok := c.entries[p]
	if !ok || e.Hash != hash {
		return extract.Found{}, false
	}
	return fromCached(e.Found), true
}

func (c *extractCache) Put(p, hash string, f extract.Found) {
	c.entries[p] = cacheEntry{Hash: hash, Found: toCached(f)}
	c.dirty = true
}

// Unchanged implements scan.StatCache: the entry for p, when the size and
// modification time Stamp recorded for it are the file's now.
func (c *extractCache) Unchanged(p string, size int64, mod time.Time) (extract.Found, bool) {
	e, ok := c.entries[p]
	if !ok || e.Mod == 0 || e.Size != size || e.Mod != mod.UnixNano() {
		return extract.Found{}, false
	}
	return fromCached(e.Found), true
}

// Stamp implements scan.StatCache: it records size and mod on p's entry,
// unless mod is too recent to tell a later write in the same tick apart
// (racyWindow), or in the future, in which case any older stamp is cleared.
func (c *extractCache) Stamp(p string, size int64, mod time.Time) {
	e, ok := c.entries[p]
	if !ok {
		return
	}
	s, m := size, mod.UnixNano()
	if c.now().Sub(mod) < racyWindow {
		s, m = 0, 0
	}
	if e.Size == s && e.Mod == m {
		return
	}
	e.Size, e.Mod = s, m
	c.entries[p] = e
	c.dirty = true
}

// LoadCache reads the cache; a missing file, a foreign format, and entries
// extracted under different inputs are all empty, so a stale entry is never
// preferred to re-reading the file. now is the clock Stamp measures
// modification times against.
func (s *Store) LoadCache(inputs string, now func() time.Time) (*extractCache, error) {
	c := &extractCache{entries: map[string]cacheEntry{}, inputs: inputs, now: now}
	raw, ok, err := s.read(path.Join(CacheDir, CacheFile))
	if err != nil || !ok {
		return c, err
	}
	var cf cacheFile
	if err := json.Unmarshal(raw, &cf); err != nil {
		return nil, fmt.Errorf("%s: %w", CacheFile, err)
	}
	if cf.Format == cacheFormat && cf.Inputs == inputs && cf.Entries != nil {
		c.entries = cf.Entries
	}
	return c, nil
}

// SaveCache writes the cache when anything changed.
func (s *Store) SaveCache(c *extractCache) error {
	if !c.dirty {
		return nil
	}
	if err := os.MkdirAll(s.path(CacheDir), dirPerm); err != nil {
		return err
	}
	raw, _ := json.Marshal(cacheFile{Format: cacheFormat, Inputs: c.inputs, Entries: c.entries})
	return s.Write(path.Join(CacheDir, CacheFile), raw)
}
