package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `github comment --dry-run` posts nothing, so it runs with no token, no
// repository and no pull request (bug 69); the links take the repository
// from origin, or a named placeholder. A label applied in the event acks
// nothing under --dry-run.
func TestGitHubCommentDryRunNeedsNoEnvironment(t *testing.T) {
	// Not parallel: environment variables.
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	write(t, dir, "internal/store/write.go", goV2)
	for _, k := range []string{envGitHubToken, envGitHubRepository, githubEventPath} {
		t.Setenv(k, "")
	}
	runGH := func(vcs VCS, args ...string) result {
		t.Helper()
		var out, errb strings.Builder
		code := Run(append([]string{"github", "comment"}, args...), WithDir(dir), WithIO(nil, &out, &errb), WithVCS(vcs), WithClock(func() time.Time { return clock }))
		return result{code, out.String(), errb.String()}
	}
	r := runGH(v, "--dry-run")
	if r.code != ExitFindings || !strings.Contains(r.out, "https://github.com/"+placeholderRepo+"/blob/abc1234/docs/sessions.md#L1") || !strings.Contains(r.err, "links use "+placeholderRepo) {
		t.Errorf("dry run, no environment = %+v", r)
	}
	withOrigin := v
	withOrigin.remote = "git@github.com:org/api.git"
	if r := runGH(withOrigin, "--dry-run"); r.code != ExitFindings || !strings.Contains(r.out, "https://github.com/org/api/blob/") || r.err != "" {
		t.Errorf("dry run, repository from origin = %+v", r)
	}
	// Without --dry-run the environment is still required.
	if r := runGH(v); r.code != ExitError || !strings.Contains(r.err, "--dry-run needs none of them") {
		t.Errorf("real run, no environment = %+v", r)
	}
	acks, _ := os.ReadFile(filepath.Join(dir, ".ds/acks.tsv"))
	write(t, dir, "event.json", `{"action":"labeled","label":{"name":"docs-acked"},"pull_request":{"number":7,"labels":[{"name":"docs-acked"}]},"sender":{"login":"r"}}`)
	t.Setenv(githubEventPath, filepath.Join(dir, "event.json"))
	if r := runGH(v, "--dry-run"); r.code != ExitFindings || !strings.Contains(r.out, "nothing acked (--dry-run)") {
		t.Errorf("label under dry run = %+v", r)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".ds/acks.tsv")); string(after) != string(acks) {
		t.Error("a dry run wrote acks")
	}
}

func TestGitHubRepoFromRemote(t *testing.T) {
	t.Parallel()
	for remote, want := range map[string]string{
		"git@github.com:org/api.git":       "org/api",
		"https://github.com/org/api":       "org/api",
		"https://github.com/org/api.git/":  "org/api",
		"ssh://git@github.com/org/api.git": "org/api",
		"https://gitlab.com/org/api.git":   "",
		"https://github.com/org":           "",
		"https://github.com/org/api/extra": "",
		"":                                 "",
	} {
		if got := githubRepoFromRemote(remote); got != want {
			t.Errorf("githubRepoFromRemote(%q) = %q, want %q", remote, got, want)
		}
	}
}
