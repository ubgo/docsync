package check

import (
	"strings"
	"testing"

	"github.com/ubgo/docsync/block"
)

// TestUnknownEnvIsReported pins bug 120's directive half: with [env] known
// set, an env= on this repository's own defs and citations that the list
// does not name is `unknown`, naming the value and the list. Without a list
// nothing is reported, and a citation published by another repository is
// held to that repository's list, not this one's.
func TestUnknownEnvIsReported(t *testing.T) {
	t.Parallel()
	good := def("dsn-k7m2p4xq", "a.go", 3, "x", map[string]string{block.KeyEnv: "prod"})
	typo := def("dsn-k7m2p4xq", "a.go", 9, "y", map[string]string{block.KeyEnv: "stagng"})
	cite := ref("block", good.ID, "docs/a.md", 5, map[string]string{block.KeyEnv: "prdo"})
	remote := RemoteRef{Repo: "web", Ref: ref("block", good.ID, "docs/w.md", 2, map[string]string{block.KeyEnv: "qa"})}
	in := Input{Repo: "api", Now: now, Defs: []block.Block{good, typo}, Refs: []block.Reference{cite}, MergedRefs: []RemoteRef{remote}}

	if fs := byState(Run(in))[StateUnknown]; len(fs) != 0 {
		t.Fatalf("no env.known means no env is unknown, got %+v", fs)
	}
	in.Opts.KnownEnvs = []string{"prod", "staging"}
	fs := byState(Run(in))[StateUnknown]
	if len(fs) != 2 {
		t.Fatalf("want the def and the citation reported, got %+v", fs)
	}
	var docs []string
	for _, f := range fs {
		docs = append(docs, f.Doc)
		if !strings.Contains(f.Remedy.Fix, "prod, staging") || f.Severity != SeverityWarning {
			t.Errorf("finding must name the known list and warn: %+v", f)
		}
	}
	joined := strings.Join(docs, " ")
	if !strings.Contains(joined, "a.go") || !strings.Contains(joined, "docs/a.md") || strings.Contains(joined, "docs/w.md") {
		t.Errorf("reported docs = %v, want a.go and docs/a.md only", docs)
	}
}
