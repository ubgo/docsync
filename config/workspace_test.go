package config

import (
	"errors"
	"strings"
	"testing"
)

func TestParseWorkspace(t *testing.T) {
	t.Parallel()
	src := `[workspace]
name  = "platform"
repos = ["github.com/org/api", "github.com/org/web.git"]
index = "github.com/org/ds-index"
default_branch = "main"
stale_after_commits = 20
`
	w, err := ParseWorkspace(strings.NewReader(src))
	if err != nil || w.Name != "platform" || len(w.Repos) != 2 || w.Index != "github.com/org/ds-index" || w.DefaultBranch != "main" || w.StaleAfterCommits != 20 {
		t.Errorf("workspace = %+v %v", w, err)
	}
	if w, err := ParseWorkspace(strings.NewReader("[workspace]\nname = \"one\"\nrepos = [\".\"]\n")); err != nil || w.Index != "" {
		t.Errorf("minimal = %+v %v", w, err)
	}
	for name, tc := range map[string]struct {
		in   string
		want error
	}{
		"no table":    {"x = 1\n", ErrUnknown},
		"empty":       {"", ErrWorkspaceName},
		"no name":     {"[workspace]\nrepos = [\"a\"]\n", ErrWorkspaceName},
		"no repos":    {"[workspace]\nname = \"n\"\n", ErrWorkspaceRepos},
		"unknown key": {"[workspace]\nname = \"n\"\nrepos = [\"a\"]\nnope = 1\n", ErrUnknown},
		"bad type":    {"[workspace]\nname = 1\nrepos = [\"a\"]\n", ErrType},
		// Bug 120: parsed but read by nothing, so refused as not implemented.
		"id not implemented":  {"[workspace]\nname = \"n\"\nrepos = [\"a\"]\n[workspace.id]\nsuffix_length = 8\n", ErrNotImplemented},
		"env not implemented": {"[workspace]\nname = \"n\"\nrepos = [\"a\"]\n[workspace.env]\nknown = [\"prod\"]\n", ErrNotImplemented},
		"syntax":              {"[workspace\n", ErrSyntax},
		"workspace type":      {"workspace = 1\n", ErrType},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseWorkspace(strings.NewReader(tc.in)); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := ParseWorkspace(errReader{}); err == nil {
		t.Error("read error")
	}
	for in, want := range map[string]string{"github.com/org/api": "api", "github.com/org/web.git": "web", "api": "api", "/x/y/": "", ".git": ".git"} {
		if got := RepoName(in); got != want {
			t.Errorf("RepoName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWorkspaceRepoNamesMustBeDistinct pins what the index can store. It
// names each repository's directory by the URL's last segment, so two repos
// sharing one would overwrite each other's published ledgers, and a URL
// with none would publish into the repos directory itself.
// promise:publish-canonical
func TestWorkspaceRepoNamesMustBeDistinct(t *testing.T) {
	t.Parallel()
	head := "[workspace]\nname = \"p\"\n"
	for name, repos := range map[string]string{
		"same last segment":  `["github.com/org/api", "gitlab.com/partner/api"]`,
		"same after .git":    `["github.com/org/api.git", "github.com/other/api"]`,
		"no last segment":    `["github.com/org/"]`,
		"negative staleness": `["github.com/org/api"]` + "\nstale_after_commits = -1",
	} {
		if _, err := ParseWorkspace(strings.NewReader(head + "repos = " + repos + "\n")); !errors.Is(err, ErrValue) {
			t.Errorf("%s: %v, want ErrValue", name, err)
		}
	}
	w, err := ParseWorkspace(strings.NewReader(head + `repos = ["github.com/org/api", "github.com/org/docs"]` + "\n"))
	if err != nil || len(w.Repos) != 2 {
		t.Errorf("distinct names load: %+v %v", w, err)
	}
	// A workspace built in code is held to the same rule.
	if err := (Workspace{Name: "p", Repos: []string{"a/x", "b/x"}}).Validate(); !errors.Is(err, ErrValue) {
		t.Errorf("code-built duplicate = %v", err)
	}
}

// TestCanonicalURLAndMember pins which URLs name the same repository: the
// forms git and forges print for one repo agree, and a fork does not.
func TestCanonicalURLAndMember(t *testing.T) {
	t.Parallel()
	same := []string{"github.com/org/api", "git@github.com:org/api.git", "https://github.com/org/api", "https://github.com/Org/API/", "ssh://git@github.com/org/api.git", "http://github.com/org/api.git"}
	for _, u := range same {
		if got := CanonicalURL(u); got != "github.com/org/api" {
			t.Errorf("CanonicalURL(%q) = %q", u, got)
		}
	}
	w := Workspace{Name: "p", Repos: []string{"github.com/org/api", "gitlab.example.com:8443/team/docs"}}
	for _, u := range same {
		if !w.Member(u) {
			t.Errorf("%q must be a member", u)
		}
	}
	for _, u := range []string{"git@github.com:someone/api.git", "github.com/org/api-fork", "gitlab.com/org/api", ""} {
		if w.Member(u) {
			t.Errorf("%q must not be a member", u)
		}
	}
	if !w.Member("https://gitlab.example.com:8443/team/docs.git") {
		t.Error("a port in the host is part of the host")
	}
}
