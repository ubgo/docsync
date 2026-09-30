package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitignoreRow covers the three states doctor reports: covered, absent,
// and stale. Absent and stale are the same failure — a machine-local file
// would be committed — so both must name the lines to add, and neither may
// suggest a destructive remedy.
// promise:local-never-committed
func TestGitignoreRow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := NewStore(dir)

	// Absent.
	row := s.gitignoreRow()
	if row[1] != "WARN" || !strings.Contains(row[2], "cache/") || !strings.Contains(row[2], JournalFile) {
		t.Errorf("absent = %v", row)
	}
	if strings.Contains(row[2], "--force") {
		t.Error("the remedy must not suggest overwriting the config and ledgers")
	}

	// Stale: covers some entries, not all.
	if err := s.Write(GitignoreFile, []byte("cache/\nindex/\n")); err != nil {
		t.Fatal(err)
	}
	row = s.gitignoreRow()
	if row[1] != "WARN" || !strings.Contains(row[2], JournalFile) || strings.Contains(row[2], "cache/") {
		t.Errorf("stale = %v", row)
	}

	// Complete.
	if err := s.Write(GitignoreFile, []byte(gitignoreBody)); err != nil {
		t.Fatal(err)
	}
	if row := s.gitignoreRow(); row[1] != "ok" {
		t.Errorf("complete = %v", row)
	}
	// Comments and blank lines in the body are not requirements.
	if err := s.Write(GitignoreFile, []byte("cache/\nindex/\njournal.tsv\nurls.json\nruns.json\nnotified.json\nhashes.json\nmetrics.json\nlock\n")); err != nil {
		t.Fatal(err)
	}
	if row := s.gitignoreRow(); row[1] != "ok" {
		t.Errorf("comment-free file must satisfy the check: %v", row)
	}
}

// TestGitignoreBodyCoversLocalState is the guard that keeps the list honest:
// every machine-local path the CLI writes must be named, so adding a new one
// without ignoring it fails here rather than in someone's git history.
func TestGitignoreBodyCoversLocalState(t *testing.T) {
	t.Parallel()
	for _, name := range []string{CacheDir + "/", IndexDir + "/", JournalFile, URLCacheFile, RunsFile, NotifiedFile, HashesFile, MetricsFile, LockFile} {
		if !strings.Contains(gitignoreBody, "\n"+name+"\n") {
			t.Errorf("%s is machine-local and is not in the ignore list", name)
		}
	}
	// The shared facts must NOT be ignored; committing them is the point.
	for _, name := range []string{LedgerFile, RefsFile, AcksFile, ConfigFile, BlocksDir + "/"} {
		if strings.Contains(gitignoreBody, "\n"+name+"\n") {
			t.Errorf("%s is a committed fact and must not be ignored", name)
		}
	}
}

// TestInitWritesGitignore pins that a fresh repo is safe by default.
// promise:local-never-committed
func TestInitWritesGitignore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v := fakeVCS{head: "abc1234", files: map[string][]byte{}}
	if r := run(t, dir, v, "init"); r.code != 0 {
		t.Fatalf("init = %+v", r)
	}
	raw, err := os.ReadFile(filepath.Join(dir, DirName, GitignoreFile))
	if err != nil {
		t.Fatalf("init must write the ignore list: %v", err)
	}
	if string(raw) != gitignoreBody {
		t.Errorf("gitignore = %q", raw)
	}
	if row := NewStore(dir).gitignoreRow(); row[1] != "ok" {
		t.Errorf("a freshly initialised repo must pass doctor: %v", row)
	}
}
