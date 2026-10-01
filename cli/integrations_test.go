package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/workspace"
)

// readIntegration reads a file under integrations/github.
func readIntegration(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "integrations", "github", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAckLabelCommitIsWiredEndToEnd pins bug 109: the action's README said
// the workflow commits .ds/acks.tsv after the docs-acked label, the action
// had no step that did, and the pull request template's token could not
// have pushed one (contents: read) from a checkout it could not push from
// (the merge commit). The README, the action, and the template now agree.
func TestAckLabelCommitIsWiredEndToEnd(t *testing.T) {
	t.Parallel()
	action := readIntegration(t, "action.yml")
	for _, want := range []string{
		"commit-acks:",
		"github.event.action == 'labeled'",
		"github.event.pull_request.head.repo.full_name == github.repository",
		"git add .ds/acks.tsv",
		`git push -q origin "HEAD:refs/heads/$HEAD_REF"`,
	} {
		if !strings.Contains(action, want) {
			t.Errorf("action.yml lacks %q", want)
		}
	}
	pr := readIntegration(t, "workflows/docsync-pr.yml")
	for _, want := range []string{"contents: write", "ref: ${{ github.event.pull_request.head.sha }}"} {
		if !strings.Contains(pr, want) {
			t.Errorf("docsync-pr.yml lacks %q", want)
		}
	}
	if readme := readIntegration(t, "README.md"); !strings.Contains(readme, "the action then commits `.ds/acks.tsv` to the pull request branch") {
		t.Error("README no longer says what the action does with the acks")
	}
}

// junitFileRE and testsArgRE find where the publish template writes the
// JUnit report and what it passes to `ds publish --tests`.
var (
	junitFileRE = regexp.MustCompile(`--junitfile "([^"]+)"`)
	testsArgRE  = regexp.MustCompile(`ds publish --tests "([^"]+)"`)
)

// TestPublishTemplateWritesTheReportItPublishes pins bug 110: the publish
// template ran `go test -json > /dev/null` and then `ds publish --tests
// junit.xml`, a file nothing wrote, so every run fell back to publishing
// without outcomes and every assert= citation had nothing to check against.
// The template now writes the report where it then reads it, in the format
// ParseJUnit reads.
func TestPublishTemplateWritesTheReportItPublishes(t *testing.T) {
	t.Parallel()
	wf := readIntegration(t, "workflows/docsync-publish.yml")
	written, read := junitFileRE.FindStringSubmatch(wf), testsArgRE.FindStringSubmatch(wf)
	if written == nil || read == nil || written[1] != read[1] {
		t.Fatalf("report written to %v, published from %v", written, read)
	}
	if strings.Contains(wf, "> /dev/null") {
		t.Error("the template still discards test output")
	}
	// gotestsum's report, as v1.13.0 writes it.
	report := `<?xml version="1.0" encoding="UTF-8"?>
<testsuites tests="2" failures="1" errors="0" time="0.570073">
	<testsuite tests="2" failures="1" time="0.570000" name="x" timestamp="2026-10-01T11:49:52+05:30">
		<properties>
			<property name="go.version" value="go1.27.1 darwin/arm64"></property>
		</properties>
		<testcase classname="x" name="TestSave" time="0.000000"></testcase>
		<testcase classname="x" name="TestLoad" time="0.000000"><failure message="Failed" type="">x_test.go:3: no</failure></testcase>
	</testsuite>
</testsuites>`
	got, err := workspace.ParseJUnit(strings.NewReader(report))
	if err != nil || got["TestSave"] != check.TestPassed || got["TestLoad"] != check.TestFailed {
		t.Errorf("gotestsum report = %v %v", got, err)
	}
}
