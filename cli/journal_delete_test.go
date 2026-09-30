package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ubgo/docsync"
)

// TestUndoRestoresADeletion is the guarantee that let `ds repair` delete at
// all: every source write is reversible. A deletion is journaled with the line
// that followed it, and undo puts the line back only where that line now sits,
// so it restores the file byte for byte and refuses rather than guesses once
// the file has moved around it.
// promise:undo-bytes
func TestUndoRestoresADeletion(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		src   string
		edits []docsync.Edit
	}{
		"a middle line":    {"a\nX\nb\n", []docsync.Edit{{File: "f", Line: 2, Old: "X", Delete: true, Next: "b"}}},
		"the last line":    {"a\nX\n", []docsync.Edit{{File: "f", Line: 2, Old: "X", Delete: true, Next: ""}}},
		"no final newline": {"a\nX", []docsync.Edit{{File: "f", Line: 2, Old: "X", Delete: true, Next: ""}}},
		"CRLF":             {"a\r\nX\r\nb\r\n", []docsync.Edit{{File: "f", Line: 2, Old: "X", Delete: true, Next: "b"}}},
		// Two consecutive lines, deleted bottom-up as Repair orders them, each
		// naming the first line that survives.
		"consecutive lines": {"X\n  k=v\na,b\n", []docsync.Edit{
			{File: "f", Line: 2, Old: "  k=v", Delete: true, Next: "a,b"},
			{File: "f", Line: 1, Old: "X", Delete: true, Next: "a,b"},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, ".ds"), dirPerm); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "f")
			if err := os.WriteFile(path, []byte(tc.src), filePerm); err != nil {
				t.Fatal(err)
			}
			st := NewStore(dir)
			if err := st.applyAll(WriteRepair, tc.edits); err != nil {
				t.Fatalf("apply: %v", err)
			}
			// The journal must survive a round trip through its file format,
			// which is where a deletion's op and Next could be lost.
			entries, err := st.journal()
			if err != nil || len(entries) != len(tc.edits) || !entries[0].Edit.Delete {
				t.Fatalf("journal = %+v %v", entries, err)
			}
			if _, err := st.Undo(); err != nil {
				t.Fatalf("undo: %v", err)
			}
			if got, _ := os.ReadFile(path); string(got) != tc.src {
				t.Errorf("undo gave %q, want %q", got, tc.src)
			}
		})
	}
	// A file edited around the deletion since is refused, not guessed at.
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".ds"), dirPerm)
	path := filepath.Join(dir, "f")
	_ = os.WriteFile(path, []byte("a\nX\nb\n"), filePerm)
	st := NewStore(dir)
	if err := st.applyAll(WriteRepair, []docsync.Edit{{File: "f", Line: 2, Old: "X", Delete: true, Next: "b"}}); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(path, []byte("a\nsomething else\n"), filePerm)
	if _, err := st.Undo(); err == nil {
		t.Error("undo must refuse once the file has moved around the deletion")
	}
	if got, _ := os.ReadFile(path); string(got) != "a\nsomething else\n" {
		t.Errorf("a refused undo must not touch the file: %q", got)
	}
}

// TestJournalReadsOlderLayouts pins that an existing journal survives the
// upgrade that added the op column: its entries are inserts and replacements,
// and they undo exactly as they would have.
func TestJournalReadsOlderLayouts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, ".ds/journal.tsv", "1\tdef\t2026-09-06T12:00:00Z\tf\t1\t\t// ds:def id=a-k7m2p4xq\n")
	st := NewStore(dir)
	entries, err := st.journal()
	if err != nil || len(entries) != 1 || entries[0].Edit.Delete || entries[0].Edit.New != "// ds:def id=a-k7m2p4xq" {
		t.Errorf("a seven-column journal = %+v %v", entries, err)
	}
	// An op this build does not know is a corrupt journal, not a silent insert.
	write(t, dir, ".ds/journal.tsv", "1\tdef\t\tf\t1\ta\tb\tsplice\n")
	if _, err := st.journal(); err == nil {
		t.Error("an unknown op must be an error")
	}
}
