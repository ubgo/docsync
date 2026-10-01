package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
)

// addOwners appends an [owners] team listing people to dir's config, so an
// agent ack can name one of them as its delegate (§26.7, bug 106).
func addOwners(t *testing.T, dir string, people ...string) {
	t.Helper()
	p := filepath.Join(dir, ".ds", "config.toml")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	list := `"` + strings.Join(people, `", "`) + `"`
	write(t, dir, ".ds/config.toml", string(raw)+"\n[owners]\n\"@delegates\" = ["+list+"]\n")
}

// TestForeignCitationsNameTheirRepository pins bug 102: when another
// repository's citation flagged in this repository's check, the text output
// printed it under its bare path -- a file that is not here -- and its
// printed `ds ack` failed with "no reference at that doc line" when run
// where it was printed. Every text surface now names the repository that
// holds the citation and says the remedy runs there.
func TestForeignCitationsNameTheirRepository(t *testing.T) {
	t.Parallel()
	api, docs, _ := twoRepos(t)
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatal(r)
	}
	if r := run(t, docs, docsVCS, "publish"); r.code != 0 {
		t.Fatal(r)
	}
	if r := run(t, api, apiVCS, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	src, _ := os.ReadFile(filepath.Join(api, "internal/store.go"))
	apiVCS.files["a1:internal/store.go"] = src
	write(t, api, "internal/store.go", strings.Replace(string(src), "legacy.Save()", "sessions.Insert()", 1))
	r := run(t, api, apiVCS, "check")
	for _, want := range []string{"docs/runbook.md (in the docs repository)\n", "still true: in docs: ds ack sess-save-k7m2p4xq --doc docs/runbook.md", "otherwise:  in docs: edit the sentence"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("check text lacks %q:\n%s", want, r.out)
		}
	}
	if r := run(t, api, apiVCS, "impact"); !strings.Contains(r.out, "docs/runbook.md (in the docs repository) (1)") {
		t.Errorf("impact = %+v", r)
	}
	// `ds why` lists the other repository's citer too (bug 103).
	if r := run(t, api, apiVCS, "why", "sess-save-k7m2p4xq"); !strings.Contains(r.out, "  docs/runbook.md:1 (in the docs repository)  ds:block\n") {
		t.Errorf("why = %+v", r)
	}
	// JSON keeps the bare command, with doc_repo beside it.
	if r := run(t, api, apiVCS, "check", "--json"); !strings.Contains(r.out, `"if_still_true": "ds ack sess-save-k7m2p4xq`) {
		t.Errorf("json remedy changed: %s", r.out)
	}

	foreign := check.Finding{Doc: "docs/runbook.md", DocRepo: "docs", Line: 1, ID: "x-a2b6f8jk", State: check.StateUnacked, Severity: check.SeverityError, Message: "m", Remedy: check.Remedy{IfStillTrue: "ds ack x-a2b6f8jk", IfNot: "edit it"}}
	fixOnly := foreign
	fixOnly.Remedy = check.Remedy{Fix: "run it"}
	var w bytes.Buffer
	printWorklist(&w, []reviewItem{{Finding: foreign}, {Finding: fixOnly}})
	for _, want := range []string{"- [ ] docs/runbook.md:1 (in the docs repository)", "still true: in docs: ds ack", "fix: in docs: run it"} {
		if !strings.Contains(w.String(), want) {
			t.Errorf("review worklist lacks %q:\n%s", want, w.String())
		}
	}
	body := commentBodies(docsync.Report{Findings: []check.Finding{foreign, fixOnly}}, "https://github.com/org/api", "abc")["docs/runbook.md"]
	for _, want := range []string{"line 1 in the `docs` repository", "still true (in `docs`): `ds ack x-a2b6f8jk`; otherwise: in docs: edit it", "fix: in docs: run it"} {
		if !strings.Contains(body, want) {
			t.Errorf("comment lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "/blob/") {
		t.Errorf("comment links another repository's file into this one:\n%s", body)
	}
	if msg := formatDigest(&digest{Findings: []check.Finding{foreign}, Escalate: []check.Finding{foreign}}, nil); !strings.Contains(msg, "  docs/runbook.md:1 (in the docs repository)  unacked") || !strings.Contains(msg, "ESCALATED docs/runbook.md:1 (in the docs repository)") {
		t.Errorf("notify digest = %s", msg)
	}
}

// TestRepoModeCopiesOfForeignBlocks pins bug 105 through the CLI: a copy of
// another repository's block held only its title and a link relative to
// that repository, `ds render` printed every copy twice, and `refresh
// --dry-run` said nothing about copies.
func TestRepoModeCopiesOfForeignBlocks(t *testing.T) {
	t.Parallel()
	api, docs, _ := twoRepos(t)
	apiVCS := fakeVCS{head: "a1", branch: "main", remote: "git@github.com:org/api.git", files: map[string][]byte{}}
	docsVCS := fakeVCS{head: "d1", branch: "main", remote: "https://github.com/org/docs", files: map[string][]byte{}}
	if r := run(t, api, apiVCS, "publish"); r.code != 0 {
		t.Fatal(r)
	}
	cfg, _ := os.ReadFile(filepath.Join(docs, ".ds/config.toml"))
	write(t, docs, ".ds/config.toml", string(cfg)+"[include]\nmode = \"repo\"\n")
	page := "# Save\n\n<!-- ds:block id=sess-save-k7m2p4xq -->\n\nend\n"
	write(t, docs, "docs/save.md", page)
	if r := run(t, docs, docsVCS, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	r := run(t, docs, docsVCS, "refresh", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "1 repo-mode copies would be rewritten\n  docs/save.md\n") {
		t.Errorf("dry run = %+v", r)
	}
	if got, _ := os.ReadFile(filepath.Join(docs, "docs/save.md")); string(got) != page {
		t.Errorf("dry run wrote:\n%s", got)
	}
	if r := run(t, docs, docsVCS, "refresh"); r.code != 0 || !strings.Contains(r.out, "1 repo-mode copies rewritten") {
		t.Fatalf("refresh = %+v", r)
	}
	got, _ := os.ReadFile(filepath.Join(docs, "docs/save.md"))
	if !strings.Contains(string(got), "`internal/store.go:4-6` (in api)\n\n```go\nfunc (s *Store) Save() error {") {
		t.Errorf("foreign copy:\n%s", got)
	}
	if r := run(t, docs, docsVCS, "check"); r.code != 0 || strings.Contains(r.out, "tampered") {
		t.Errorf("fresh foreign copy = %+v", r)
	}
	r = run(t, docs, docsVCS, "render", "docs/save.md")
	if strings.Count(r.out, "func (s *Store) Save()") != 1 || !strings.Contains(r.out, "(https://github.com/org/api/blob/a1/internal/store.go#L4-L6)") || strings.Contains(r.out, "/ds:block") {
		t.Errorf("render = %+v", r)
	}
	// A repository the workspace file does not list gets no link.
	link := foreignLink(indexState{})
	if _, ok := link(block.Block{Args: map[string]string{block.KeyRepo: "web", block.KeyBranch: "b"}}); ok {
		t.Error("unlisted repository linked")
	}
}
