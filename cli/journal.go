package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/ledger"
)

// JournalFile records every source write so `undo` can reverse the last one
// (§33 safety). One line per edit: batch, kind, time, file, line, old, new;
// tabs, newlines, and backslashes in the text fields are escaped the way the
// ledger does.
//
// The file is machine-local and never committed (§6): `old` and `new` are
// raw source lines, which on a file under `[secret] paths` means the line
// holds a value. It is listed in `.ds/.gitignore` so that copy never enters
// history, where rotating the credential could not remove it.
const JournalFile = "journal.tsv"

// journalFields is the current column count. Older layouts are still read:
// journalFieldsV1 carried neither kind nor time, and journalFieldsV2 had no op
// column because no write could delete a line. The journal is machine-local
// (never committed), so these only have to survive one machine's upgrade.
const (
	journalFields   = 8
	journalFieldsV2 = 7
	journalFieldsV1 = 5
)

// journalOpDelete marks an entry that removed a line. Its `new` column holds
// the line that followed the removed one, which is how undo finds where to put
// it back. The empty op is an insert when `old` is empty and a replacement
// otherwise, which is all the journal could record before.
const journalOpDelete = "delete"

// Errors.
var (
	// ErrNothingToUndo is returned when the journal is empty.
	ErrNothingToUndo = errors.New("nothing to undo")
	// ErrUndoCommitted is returned when the next entry is already in git
	// history and --force was not given.
	ErrUndoCommitted = errors.New("nothing to undo since the last commit")
	// ErrUndoOrphans is returned when undoing would remove a def that
	// sentences still cite and --orphan was not given.
	ErrUndoOrphans = errors.New("undo would orphan a cited def")
)

// The commands that write source (§22 "source is written only by def and
// adopt"; rename rewrites docs). The journal records which one made a write
// so `undo --list` can say what it is offering to reverse.
const (
	WriteDef    = "def"
	WriteAdopt  = "adopt"
	WriteRename = "rename"
	// WriteRepair comments directives an older build left bare, so `undo`
	// can put them back exactly as they were found.
	WriteRepair = "repair"
)

// WriteKinds is the canonical order.
var WriteKinds = []string{WriteDef, WriteAdopt, WriteRename, WriteRepair}

// journalEntry is one applied edit.
type journalEntry struct {
	Batch int
	// Kind is the command that made the write, one of WriteKinds. Empty for
	// an entry written before the journal recorded it.
	Kind string
	// At is when the write was applied. Zero for an older entry; callers
	// render that as "unknown" rather than as the epoch.
	At   time.Time
	Edit docsync.Edit
}

// Journal appends a batch of applied edits. Batch numbers increase by one
// per call so `undo` reverses one command's writes together. kind names the
// command that made them, one of WriteKinds.
func (s *Store) Journal(kind string, edits []docsync.Edit) error {
	if len(edits) == 0 {
		return nil
	}
	// Under the lock, like every read-modify-write of an append-only file:
	// two concurrent writers each appending to the copy they read would
	// lose one's batch, and undo could then not reverse it.
	return s.withLock(func() error { return s.appendJournal(kind, edits) })
}

func (s *Store) appendJournal(kind string, edits []docsync.Edit) error {
	entries, err := s.journal()
	if err != nil {
		return err
	}
	batch := 1
	if len(entries) > 0 {
		batch = entries[len(entries)-1].Batch + 1
	}
	now := s.now()
	for _, e := range edits {
		entries = append(entries, journalEntry{Batch: batch, Kind: kind, At: now, Edit: e})
	}
	return s.writeLocked(JournalFile, encodeJournal(entries))
}

// now is the clock the journal stamps with; tests replace it so ages are
// reproducible.
func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// encodeJournal renders entries as the file format.
func encodeJournal(entries []journalEntry) []byte {
	var b bytes.Buffer
	for _, e := range entries {
		at := ""
		if !e.At.IsZero() {
			at = e.At.UTC().Format(time.RFC3339)
		}
		op, nw := "", e.Edit.New
		if e.Edit.Delete {
			op, nw = journalOpDelete, e.Edit.Next
		}
		fmt.Fprintf(&b, "%d\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n", e.Batch, esc(e.Kind), at, esc(e.Edit.File), e.Edit.Line, esc(e.Edit.Old), esc(nw), op)
	}
	return b.Bytes()
}

// journal reads every entry in order.
func (s *Store) journal() ([]journalEntry, error) {
	raw, ok, err := s.read(JournalFile)
	if err != nil || !ok {
		return nil, err
	}
	var out []journalEntry
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		// A journal written before kind and time existed has five columns.
		// It is widened here so nothing downstream has to know, and the two
		// missing values read as "unknown" rather than as defaults that
		// would claim more than the file says.
		if len(f) == journalFieldsV1 {
			f = []string{f[0], "", "", f[1], f[2], f[3], f[4]}
		}
		// A journal written before a write could delete has no op column;
		// every entry in it is an insert or a replacement.
		if len(f) == journalFieldsV2 {
			f = append(f, "")
		}
		if len(f) != journalFields {
			return nil, fmt.Errorf("%s: line %d: want %d fields, got %d", JournalFile, n, journalFields, len(f))
		}
		batch, err1 := strconv.Atoi(f[0])
		ln, err2 := strconv.Atoi(f[4])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("%s: line %d: bad number", JournalFile, n)
		}
		var at time.Time
		if f[2] != "" {
			parsed, err := ledger.ParseTime(f[2])
			if err != nil {
				return nil, fmt.Errorf("%s: line %d: bad time %q", JournalFile, n, f[2])
			}
			at = parsed
		}
		e := docsync.Edit{File: unesc(f[3]), Line: ln, Old: unesc(f[5]), New: unesc(f[6])}
		switch f[7] {
		case "":
		case journalOpDelete:
			e.New, e.Next, e.Delete = "", unesc(f[6]), true
		default:
			return nil, fmt.Errorf("%s: line %d: unknown op %q", JournalFile, n, f[7])
		}
		out = append(out, journalEntry{Batch: batch, Kind: unesc(f[1]), At: at, Edit: e})
	}
	return out, nil
}

// Undo reverses the last batch: edits are undone in reverse order, each
// checked against the file so a later hand edit is never overwritten. It
// returns the reversed edits.
func (s *Store) Undo() ([]docsync.Edit, error) {
	var reversed []docsync.Edit
	err := s.withLock(func() error {
		var err error
		reversed, err = s.undoLocked()
		return err
	})
	return reversed, err
}

func (s *Store) undoLocked() ([]docsync.Edit, error) {
	entries, err := s.journal()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, ErrNothingToUndo
	}
	last := entries[len(entries)-1].Batch
	var batch []journalEntry
	for _, e := range entries {
		if e.Batch == last {
			batch = append(batch, e)
		}
	}
	var reversed []docsync.Edit
	for i := len(batch) - 1; i >= 0; i-- {
		e := batch[i].Edit
		p := filepath.Join(s.Root, filepath.FromSlash(e.File))
		info, err := os.Stat(p)
		if err != nil {
			return reversed, err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return reversed, err
		}
		// Split on "\n" only, as Edit.Apply does, so every line undo does
		// not touch keeps its own ending; normalising CRLF here rewrote a
		// whole Windows file and undo could not give back its bytes.
		lines := strings.Split(string(raw), "\n")
		if e.Delete {
			// The deleted line's successor must now sit where it was; only then
			// is it certain where the line goes back. A file edited around the
			// deletion since is refused rather than guessed at.
			i := e.Line - 1
			switch {
			case i >= 0 && i < len(lines) && strings.TrimSuffix(lines[i], docsync.LineCR(lines[i])) == e.Next:
			case i == len(lines) && e.Next == "":
				// It was the last line of a file with no final newline.
			default:
				return reversed, fmt.Errorf("%s:%d changed since the write; undo refused", e.File, e.Line)
			}
			cr := ""
			if i < len(lines) {
				cr = docsync.LineCR(lines[i])
			} else if len(lines) > 0 {
				cr = docsync.LineCR(lines[len(lines)-1])
			}
			lines = append(lines[:i], append([]string{e.Old + cr}, lines[i:]...)...)
		} else {
			if e.Line < 1 || e.Line > len(lines) || strings.TrimSuffix(lines[e.Line-1], docsync.LineCR(lines[e.Line-1])) != e.New {
				return reversed, fmt.Errorf("%s:%d changed since the write; undo refused", e.File, e.Line)
			}
			if e.Old == "" {
				lines = append(lines[:e.Line-1], lines[e.Line:]...)
			} else {
				lines[e.Line-1] = e.Old + docsync.LineCR(lines[e.Line-1])
			}
		}
		// One write for every shape, so a deletion's undo fails the same way,
		// through the same path, as an insertion's does.
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), info.Mode().Perm()); err != nil {
			return reversed, err
		}
		reversed = append(reversed, e)
	}
	var keep []journalEntry
	for _, e := range entries {
		if e.Batch != last {
			keep = append(keep, e)
		}
	}
	return reversed, s.writeLocked(JournalFile, encodeJournal(keep))
}

// applyAll applies edits in order, journals the ones that landed, and
// returns the first failure. A partial batch is still journaled so undo can
// reverse what did land.
func (s *Store) applyAll(kind string, edits []docsync.Edit) error {
	var done []docsync.Edit
	var firstErr error
	for _, e := range edits {
		if err := s.ApplyEdit(e); err != nil {
			firstErr = err
			break
		}
		done = append(done, e)
	}
	if err := s.Journal(kind, done); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func esc(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "\t", `\t`, "\n", `\n`, "\r", `\r`)
	return r.Replace(s)
}

func unesc(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
