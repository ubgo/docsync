package cli

import (
	"os"
	"testing"
)

// ambientEnv is every environment variable the CLI reads to decide how to
// behave. A CI runner sets several of them -- CI switches `check` to --frozen,
// GITHUB_EVENT_PATH feeds the fork guard -- so a suite that inherits them
// tests the runner's configuration rather than the code.
var ambientEnv = []string{ciEnv, githubEventPath, envGitHubToken, envGitHubRepository, envGitHubAPIURL, envGitHubServerURL}

// TestMain clears ambientEnv so the suite gives one answer on a laptop and on
// any runner. A test about one of these variables sets it with t.Setenv.
//
// It was added when the suite first ran in CI: nineteen tests failed on
// `.ds/foreign.tsv not found`, because CI=true had made every check frozen.
// Nothing was wrong with the code, and nothing had ever run the tests there.
func TestMain(m *testing.M) {
	for _, k := range ambientEnv {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
