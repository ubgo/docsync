package config

import (
	"fmt"
	"io"
	"strings"
)

// WorkspaceFile is the name of the workspace declaration (§21), kept in the
// docs repo or a small index repo.
const WorkspaceFile = "ds-workspace.toml"

// Workspace is `ds-workspace.toml`.
type Workspace struct {
	Name  string
	Repos []string
	// Index is where published ledgers meet: a git repository URL or a
	// local path. An https endpoint is an adapter for later.
	Index string
	// DefaultBranch is the only branch `publish` runs from; empty means the
	// repository's own default as reported by the VCS.
	DefaultBranch string
	// StaleAfterCommits is how far a published ledger may fall behind its
	// repo's default branch before consumers warn (§21); 0 disables.
	StaleAfterCommits int
	ID                IDConfig
	Env               EnvConfig
}

// Workspace errors.
var (
	ErrWorkspaceName  = fmt.Errorf("%w: workspace.name is required", ErrRequired)
	ErrWorkspaceRepos = fmt.Errorf("%w: workspace.repos must list at least one repository", ErrRequired)
)

// ParseWorkspace reads a workspace file. Missing id and env sections take
// the same defaults as a repo config so a workspace of one behaves like no
// workspace at all.
func ParseWorkspace(r io.Reader) (Workspace, error) {
	src, err := io.ReadAll(r)
	if err != nil {
		return Workspace{}, err
	}
	doc, err := parseTOML(string(src))
	if err != nil {
		return Workspace{}, err
	}
	w := Workspace{ID: IDConfig{SuffixAlphabet: DefaultSuffixAlphabet, SuffixLength: DefaultSuffixLength}}
	for _, k := range sortedKeys(doc) {
		if k != "workspace" {
			return Workspace{}, fmt.Errorf("%w: %s (line %d)", ErrUnknown, k, doc[k].line)
		}
	}
	ws, ok := doc["workspace"]
	if !ok {
		return Workspace{}, ErrWorkspaceName
	}
	err = applyTable(ws, map[string]func(value) error{
		"name":                func(x value) (e error) { w.Name, e = x.str(); return },
		"repos":               func(x value) (e error) { w.Repos, e = x.strs(); return },
		"index":               func(x value) (e error) { w.Index, e = x.str(); return },
		"default_branch":      func(x value) (e error) { w.DefaultBranch, e = x.str(); return },
		"stale_after_commits": func(x value) (e error) { w.StaleAfterCommits, e = x.integer(); return },
		"id": func(x value) error {
			return applyTable(x, map[string]func(value) error{
				"suffix_alphabet": func(y value) (e error) { w.ID.SuffixAlphabet, e = y.str(); return },
				"suffix_length":   func(y value) (e error) { w.ID.SuffixLength, e = y.integer(); return },
			})
		},
		"env": func(x value) error {
			return applyTable(x, map[string]func(value) error{
				"default": func(y value) (e error) { w.Env.Default, e = y.str(); return },
				"known":   func(y value) (e error) { w.Env.Known, e = y.strs(); return },
			})
		},
	})
	if err != nil {
		return Workspace{}, wrapKey("workspace", err)
	}
	if err := w.Validate(); err != nil {
		return Workspace{}, err
	}
	return w, nil
}

// Validate checks the required fields.
func (w Workspace) Validate() error {
	if w.Name == "" {
		return ErrWorkspaceName
	}
	if len(w.Repos) == 0 {
		return ErrWorkspaceRepos
	}
	// The index names each repository's directory, and every row's repo=,
	// by the last segment of its URL. Two repositories with the same last
	// segment — github.com/org/api and gitlab.com/partner/api — would publish
	// into one directory and overwrite each other's ledgers, and a URL with
	// no last segment would publish into the index's repos directory itself.
	seen := map[string]string{}
	for _, url := range w.Repos {
		name := RepoName(url)
		if name == "" {
			return fmt.Errorf("%w: workspace.repos %q has no repository name", ErrValue, url)
		}
		if other, dup := seen[name]; dup {
			return fmt.Errorf("%w: workspace.repos %q and %q are both named %q in the index", ErrValue, other, url, name)
		}
		seen[name] = url
	}
	if w.StaleAfterCommits < 0 {
		return fmt.Errorf("%w: workspace.stale_after_commits %d is negative", ErrValue, w.StaleAfterCommits)
	}
	if w.Env.Default != "" && len(w.Env.Known) > 0 && !in(w.Env.Default, w.Env.Known) {
		return fmt.Errorf("%w: workspace.env.default %q is not in env.known", ErrValue, w.Env.Default)
	}
	return nil
}

// CanonicalURL reduces the ways one repository's URL is written to one
// form, host/path: `git@github.com:org/api.git`, `https://github.com/org/api`,
// `ssh://git@github.com/org/api` and `github.com/org/api` are the same
// repository. Case is folded, since hosts and most forges ignore it.
func CanonicalURL(url string) string {
	u := strings.ToLower(strings.TrimSpace(url))
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if at := strings.Index(u, "@"); at >= 0 && at < strings.IndexAny(u+"/", ":/") {
		u = u[at+1:]
	}
	// scp-like syntax: host:path.
	if colon := strings.Index(u, ":"); colon >= 0 && colon < strings.Index(u+"/", "/") {
		u = u[:colon] + "/" + u[colon+1:]
	}
	u = strings.TrimSuffix(strings.TrimRight(u, "/"), ".git")
	return u
}

// Member reports whether url is one of the workspace's repositories, by
// canonical URL. It is what lets only the canonical repository publish: a
// fork or a mirror shares the last path segment, and so the index
// directory, but not the URL.
func (w Workspace) Member(url string) bool {
	c := CanonicalURL(url)
	for _, r := range w.Repos {
		if CanonicalURL(r) == c {
			return true
		}
	}
	return false
}

// RepoName returns the last path segment of a repo URL, which is how the
// index names its per-repo directory and how `repo=` appears in rows.
func RepoName(url string) string {
	name := url
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '/' {
			name = url[i+1:]
			break
		}
	}
	if len(name) > 4 && name[len(name)-4:] == ".git" {
		name = name[:len(name)-4]
	}
	return name
}
