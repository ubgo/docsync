package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubgo/docsync/ledger"
)

// TestGitattributesRow covers the states doctor reports for the merge
// attributes: absent, and present. Absent is the ordinary state of every
// repo initialised before the file existed, so the remedy names the line.
func TestGitattributesRow(t *testing.T) {
	t.Parallel()
	s := NewStore(t.TempDir())
	row := s.gitattributesRow()
	if row[0] != "gitattributes" || row[1] != "WARN" || !strings.Contains(row[2], "acks.tsv merge=union") || !strings.Contains(row[2], "conflict") {
		t.Errorf("absent = %v", row)
	}
	if strings.Contains(row[2], "--force") {
		t.Error("the remedy must not suggest overwriting the config and ledgers")
	}
	if err := s.Write(GitattributesFile, []byte(gitattributesBody)); err != nil {
		t.Fatal(err)
	}
	if row := s.gitattributesRow(); row[1] != "ok" {
		t.Errorf("present = %v", row)
	}
}

// TestGitattributesUnionOnlyTheAckLog guards the one decision the file
// makes: only the append-only log is union-merged. A union merge of a file
// that is rewritten keeps a row's old and new versions side by side.
func TestGitattributesUnionOnlyTheAckLog(t *testing.T) {
	t.Parallel()
	var union []string
	for _, l := range strings.Split(gitattributesBody, "\n") {
		if strings.HasPrefix(l, "#") || !strings.Contains(l, "merge=union") {
			continue
		}
		union = append(union, strings.Fields(l)[0])
	}
	if len(union) != 1 || union[0] != AcksFile {
		t.Errorf("union-merged paths = %v, want only %s", union, AcksFile)
	}
}

// TestInitWritesGitattributes pins that a fresh repo merges its acks
// without conflicts from the first pull request.
func TestInitWritesGitattributes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v := fakeVCS{head: "abc1234", files: map[string][]byte{}}
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatalf("init = %+v", r)
	}
	raw, err := os.ReadFile(filepath.Join(dir, DirName, GitattributesFile))
	if err != nil || string(raw) != gitattributesBody {
		t.Fatalf("init must write the attributes: %q %v", raw, err)
	}
	if r := run(t, dir, v, "doctor"); !strings.Contains(r.out, "acks.tsv merges without conflicts") {
		t.Errorf("doctor on a fresh repo = %s", r.out)
	}
}

// TestConflictRemedyPerFile pins that a merge left unfinished is named as
// such, for every .ds/ file, with the advice right for that file — above all
// that refs.tsv must keep both sides, since taking one loses baselines
// nothing else can recreate.
func TestConflictRemedyPerFile(t *testing.T) {
	t.Parallel()
	for kind, want := range map[string]string{
		LedgerFile:         "ds scan",
		RefsFile:           "keep every row from both sides",
		AcksFile:           "merge=union",
		ledger.ForeignFile: "ds sync",
	} {
		err := decodeError(kind, kind, ledger.ErrConflict)
		if !errors.Is(err, ledger.ErrConflict) || !strings.Contains(err.Error(), want) || !strings.HasPrefix(err.Error(), kind+":") {
			t.Errorf("%s: %v, want it to name the file and say %q", kind, err, want)
		}
	}
	// Any other failure is only labelled.
	if err := decodeError(AcksFile, AcksFile, ledger.ErrColumns); err.Error() != AcksFile+": "+ledger.ErrColumns.Error() || !errors.Is(err, ledger.ErrColumns) {
		t.Errorf("plain = %v", err)
	}
	// A shard follows the ledger's rule under its own name.
	if err := decodeError(LedgerDir+"/api.tsv", LedgerFile, ledger.ErrConflict); !strings.HasPrefix(err.Error(), LedgerDir+"/api.tsv:") || !strings.Contains(err.Error(), "ds scan") {
		t.Errorf("shard = %v", err)
	}
}

// TestConflictedAcksEndToEnd reads a real conflicted ack log through the
// command a user runs, so the message they see is the one pinned above.
func TestConflictedAcksEndToEnd(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	path := filepath.Join(dir, DirName, AcksFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	conflicted := string(raw) + "<<<<<<< HEAD\n=======\n>>>>>>> feature\n"
	if err := os.WriteFile(path, []byte(conflicted), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, dir, v, "check")
	if r.code != ExitError || !strings.Contains(r.err, "unresolved merge conflict") || !strings.Contains(r.err, "keep every row from both sides") {
		t.Errorf("check on a conflicted ack log = %+v", r)
	}
}

// TestScanNamesUnreadableFiles pins the warning ds scan prints: a file it
// could not read that held blocks or citations is named with the reason, on
// stderr, and one that never held anything is not — every Mac directory has
// a binary .DS_Store, and naming each would bury the warning that matters.
func TestScanNamesUnreadableFiles(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	// The store file held a block; now it carries a minified line. A binary
	// file appears that never held anything.
	write(t, dir, "internal/store/write.go", goV1+"var blob = \""+strings.Repeat("x", 3000)+"\"\n")
	write(t, dir, "docs/.DS_Store", "\x00\x01binary")
	r := run(t, dir, v, "scan")
	if r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	if !strings.Contains(r.err, "warning: internal/store/write.go not scanned (long-line); its last recorded state is kept") {
		t.Errorf("stderr = %q", r.err)
	}
	if strings.Contains(r.err, ".DS_Store") {
		t.Errorf("a file that never held anything must not be named: %q", r.err)
	}
}

// TestOutPathsResolveTheSameWay pins --out for every command that takes it:
// an absolute path is used as given, a relative one is under the repository
// ds was pointed at — never the process's working directory, and never the
// repository with an absolute path glued on. export used to write
// --out /tmp/site into <repo>/tmp/site while printing /tmp/site.
func TestOutPathsResolveTheSameWay(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	abs := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"export, absolute", []string{"export", "hugo", "--out", filepath.Join(abs, "site")}, filepath.Join(abs, "site", "blocks.json")},
		{"export, relative", []string{"export", "hugo", "--out", "data/docsync"}, filepath.Join(dir, "data", "docsync", "blocks.json")},
		{"render, absolute", []string{"render", "docs/sessions.md", "--out", filepath.Join(abs, "page.md")}, filepath.Join(abs, "page.md")},
		{"render, relative", []string{"render", "docs/sessions.md", "--out", "page.md"}, filepath.Join(dir, "page.md")},
	} {
		r := run(t, dir, v, tc.args...)
		if r.code > ExitFindings {
			t.Errorf("%s: %+v", tc.name, r)
			continue
		}
		if _, err := os.Stat(tc.want); err != nil {
			t.Errorf("%s: nothing at %s: %v", tc.name, tc.want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, strings.TrimPrefix(abs, string(filepath.Separator)))); err == nil {
		t.Error("an absolute --out was written under the repository")
	}
	// --out without --ai has no patch to write: a usage error, not a flag
	// accepted and silently ignored.
	if r := run(t, dir, v, "review", "--out", "review.patch"); r.code != ExitError || !strings.Contains(r.err, "--ai") {
		t.Errorf("review --out without --ai = %+v", r)
	}
	if r := run(t, dir, v, "export", "hugo", "--out", filepath.Join(abs, "x")); !strings.Contains(r.out, filepath.Join(abs, "x")) {
		t.Errorf("export must report where it wrote: %q", r.out)
	}
}
