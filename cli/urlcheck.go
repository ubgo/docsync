package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
)

// `ds:url` checking (§9.7): under `check --resolve` only, cached per
// url.ttl in .ds/urls.json, rate limited by url.rate_per_minute. Redirects
// are followed and the final URL recorded; the page title is read from the
// first titleReadCap bytes.
const (
	URLCacheFile = "urls.json"
	urlTimeout   = 15 * time.Second
	titleReadCap = 64 << 10
	userAgent    = "docsync/" + "1.0" + " (+https://github.com/ubgo/docsync)"
)

var titleRE = regexp.MustCompile(`(?is)<title[^>]*>\s*(.*?)\s*</title>`)

// URLEntry is one cached outcome.
type URLEntry struct {
	Status    int       `json:"status"`
	Final     string    `json:"final"`
	Title     string    `json:"title,omitempty"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// LoadURLs reads the cache; missing means empty.
func (s *Store) LoadURLs() (map[string]URLEntry, error) {
	raw, ok, err := s.read(URLCacheFile)
	if err != nil || !ok {
		return map[string]URLEntry{}, err
	}
	out := map[string]URLEntry{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", URLCacheFile, err)
	}
	return out, nil
}

// SaveURLs writes the cache.
func (s *Store) SaveURLs(m map[string]URLEntry) error {
	raw, _ := json.MarshalIndent(m, "", "  ")
	return s.Write(URLCacheFile, raw)
}

// urlChecker returns the hook `check --resolve` gives the library and a
// function that persists the cache afterwards. A cache that cannot be read
// starts empty and is reported through the save step.
func (a *App) urlChecker(cfg config.Config, st *Store) (func(href string) check.URLResult, func() error) {
	cache, loadErr := st.LoadURLs()
	if cache == nil {
		cache = map[string]URLEntry{}
	}
	// url.ttl is validated when the config is loaded; empty means the default.
	ttl, _ := check.ParseDuration(orDefault(cfg.URL.TTL, config.DefaultURLTTL))
	interval := time.Duration(0)
	if cfg.URL.RatePerMinute > 0 {
		interval = time.Minute / time.Duration(cfg.URL.RatePerMinute)
	}
	client := a.httpClient
	if client == nil {
		client = &http.Client{Timeout: urlTimeout}
	}
	var last time.Time
	hook := func(href string) check.URLResult {
		if e, ok := cache[href]; ok && a.now().Sub(e.CheckedAt) < ttl {
			return e.result()
		}
		if interval > 0 && !last.IsZero() {
			if wait := interval - time.Since(last); wait > 0 {
				time.Sleep(wait)
			}
		}
		last = time.Now()
		e := fetch(client, href)
		e.CheckedAt = a.now()
		cache[href] = e
		return e.result()
	}
	save := func() error {
		if loadErr != nil {
			return loadErr
		}
		return st.SaveURLs(cache)
	}
	return hook, save
}

func (e URLEntry) result() check.URLResult {
	r := check.URLResult{Checked: true, Status: e.Status, Final: e.Final, Title: e.Title}
	if e.Error != "" {
		r.Err = fmt.Errorf("%s", e.Error)
	}
	return r
}

// fetch performs one GET and reads the title.
func fetch(client *http.Client, href string) URLEntry {
	req, err := http.NewRequest(http.MethodGet, href, nil)
	if err != nil {
		return URLEntry{Error: err.Error()}
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return URLEntry{Error: err.Error()}
	}
	defer resp.Body.Close()
	e := URLEntry{Status: resp.StatusCode, Final: resp.Request.URL.String()}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, titleReadCap))
	if m := titleRE.FindSubmatch(body); m != nil {
		e.Title = string(m[1])
	}
	return e
}
