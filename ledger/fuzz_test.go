package ledger

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ubgo/docsync/block"
)

// The fuzz targets below state properties that must hold for every input,
// so their expectations cannot share an assumption with the code the way a
// hand-written fixture can. Plain `go test` replays the seeds and anything
// saved under testdata/fuzz; `task fuzz` searches for new inputs.

// FuzzEsc: esc and unesc are inverse for every string, and esc's output
// never contains a byte that would split a row or a line.
func FuzzEsc(f *testing.F) {
	for _, s := range []string{"", "plain", "a\tb", "a\nb", "a\rb", `a\b`, `\t`, `\\t`, "\\", "trailing\\"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		e := esc(s)
		if strings.ContainsAny(e, "\t\n\r") {
			t.Fatalf("esc(%q) = %q still holds a separator", s, e)
		}
		if got := unesc(e); got != s {
			t.Fatalf("unesc(esc(%q)) = %q", s, got)
		}
	})
}

// FuzzPct: the args value escape is inverse, and its output never contains
// the space or `=` that split the args column back apart.
func FuzzPct(f *testing.F) {
	for _, s := range []string{"", "a b", "a=b", "100%", "%41", "\t\n", "%"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		e := pctEsc(s)
		if strings.ContainsAny(e, " =\t\n") {
			t.Fatalf("pctEsc(%q) = %q still holds a separator", s, e)
		}
		got, err := pctUnesc(e)
		if err != nil || got != s {
			t.Fatalf("pctUnesc(pctEsc(%q)) = %q, %v", s, got, err)
		}
	})
}

// FuzzHeaderRoundTrip: whatever a repo or commit is called, the header that
// names it reads back as the same name.
func FuzzHeaderRoundTrip(f *testing.F) {
	for _, s := range [][2]string{{"api", "abc1234"}, {"My Docs", "c"}, {"", ""}, {"a=b", "x y"}, {"tab\there", "nl\nhere"}} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, repo, commit string) {
		in := Ledger{Header: Header{Repo: repo, Commit: commit, Extract: 2}}
		got, err := DecodeLedger(bytes.NewReader(in.Bytes()))
		if err != nil {
			t.Fatalf("repo %q commit %q: %v\n%s", repo, commit, err, in.Bytes())
		}
		if got.Header.Repo != repo || got.Header.Commit != commit {
			t.Fatalf("header read back as repo %q commit %q, want %q %q", got.Header.Repo, got.Header.Commit, repo, commit)
		}
	})
}

// FuzzRowRoundTrip: a legal row survives encode and decode unchanged. Legal
// means what a scan can produce: 1-based lines with start <= end, and args
// keys in the directive grammar. Every string field is otherwise free.
func FuzzRowRoundTrip(f *testing.F) {
	f.Add("a-k7m2p4xq", "api", "func", "a b/c.go", "Store.Save", 1, 0, "h", "@o", "stable", "prod", "desc", "x y=z%")
	f.Fuzz(func(t *testing.T, id, repo, kind, file, symbol string, start, span int, hash, owner, stability, env, argVal, argVal2 string) {
		if start < 1 || span < 0 || start > 1<<30 || span > 1<<20 {
			t.Skip()
		}
		row := Row{
			ID: id, Repo: repo, Kind: block.Kind(kind), File: file, Symbol: symbol,
			Start: start, End: start + span, Hash: hash, Owner: owner, Stability: block.Stability(stability), Env: env,
			Args: map[string]string{"desc": argVal, "note_2": argVal2},
		}
		got, err := DecodeLedger(bytes.NewReader(Ledger{Rows: []Row{row}}.Bytes()))
		if err != nil {
			t.Fatalf("%+v: %v", row, err)
		}
		if len(got.Rows) != 1 || !reflect.DeepEqual(got.Rows[0], row) {
			t.Fatalf("round trip:\n got %+v\nwant %+v", got.Rows, row)
		}
	})
}

// fuzzCanonical is the shared property of every decoder: arbitrary bytes
// never panic, whatever decodes is a fixed point of encode then decode, so
// re-saving a file a person edited by hand never drifts on its own, and a
// CRLF copy of an LF file reads the same — the .ds files are committed, and
// a Windows checkout with core.autocrlf rewrites their line endings.
func fuzzCanonical[T any](t *testing.T, data []byte, decode func(*bytes.Reader) (T, error), encode func(T) []byte) {
	t.Helper()
	first, err := decode(bytes.NewReader(data))
	if err != nil {
		return
	}
	once := encode(first)
	if !bytes.Contains(data, []byte("\r")) {
		crlf, err := decode(bytes.NewReader(bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))))
		if err != nil {
			t.Fatalf("an LF file that decodes must decode with CRLF endings too: %v\ninput:\n%q", err, data)
		}
		if got := encode(crlf); !bytes.Equal(got, once) {
			t.Fatalf("line endings changed what was read:\nlf:\n%q\ncrlf:\n%q", once, got)
		}
	}
	second, err := decode(bytes.NewReader(once))
	if err != nil {
		t.Fatalf("a decoded file does not re-read after encoding: %v\ninput:\n%q\nencoded:\n%q", err, data, once)
	}
	if twice := encode(second); !bytes.Equal(once, twice) {
		t.Fatalf("encoding is not stable:\nonce:\n%q\ntwice:\n%q", once, twice)
	}
}

func seeds(f *testing.F, kind string, cols []string, rows ...string) {
	f.Helper()
	head := "# docsync " + kind + " format=2 extract=2 repo=api commit=c scanned_at=2026-09-06T12:00:00Z\n" + strings.Join(cols, "\t") + "\n"
	f.Add([]byte(head))
	for _, r := range rows {
		f.Add([]byte(head + r + "\n"))
	}
	f.Add([]byte("<<<<<<< HEAD\n"))
	f.Add([]byte("\xef\xbb\xbf" + head))
	f.Add([]byte("\xef\xbb\xbf\xef\xbb\xbf" + head))
	f.Add([]byte(strings.ReplaceAll(head, "\n", "\r\n")))
}

func FuzzDecodeLedger(f *testing.F) {
	seeds(f, KindLedger, ledgerColumns,
		"a-k7m2p4xq\tapi\tfunc\ta.go\tA\t3-9\th\t@o\tstable\t\tdesc=x%20y",
		"b-h3v8n2wd\tapi\tsection\tb.md\t\t5\th2\t\tapi\tprod\t",
		"c\tr\tk\tf\ts\t9-3\th\to\ts\te\t",
	)
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzCanonical(t, data, func(r *bytes.Reader) (Ledger, error) { return DecodeLedger(r) }, Ledger.Bytes)
	})
}

func FuzzDecodeRefs(f *testing.F) {
	seeds(f, KindRefs, refsColumns,
		"a-k7m2p4xq\tapi\td.md\t3\tblock\tlink\th1\th0\ts\t\t",
		"a-k7m2p4xq\tapi\td.md\t3\tblock\tlink\t\t\t\tprod\tenv=prod",
		"a-k7m2p4xq\tapi\td.md\t3\tblock\tlink\t\t\t\tdev\tenv=dev",
	)
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzCanonical(t, data, func(r *bytes.Reader) (Refs, error) { return DecodeRefs(r) }, Refs.Bytes)
	})
}

func FuzzDecodeAcks(f *testing.F) {
	seeds(f, KindAcks, acksColumns,
		"2026-09-06T12:00:00Z\tk\thuman\t\ta-k7m2p4xq\tapi\td.md\t3\t\th\ts\tstill true",
		"2026-09-06T12:00:00+05:30\tk\tagent\tkh\ta\tapi\td.md\t3\t\th\ts\tnote\\twith tab",
	)
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzCanonical(t, data, func(r *bytes.Reader) (Acks, error) { return DecodeAcks(r) }, Acks.Bytes)
	})
}

func FuzzDecodeForeign(f *testing.F) {
	seeds(f, KindForeign, foreignColumns,
		"a-k7m2p4xq\tapi\tabc\tfunc\ta.go\tA\t3-9\th\t@o\tstable\t\t",
	)
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzCanonical(t, data, func(r *bytes.Reader) (Foreign, error) { return DecodeForeign(r) }, Foreign.Bytes)
	})
}

// TestParseTimeRange pins the boundary FuzzDecodeAcks found: a timestamp
// that parses but whose UTC form RFC 3339 cannot write is rejected on read,
// because the alternative is a file that loads once and never again.
func TestParseTimeRange(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"2026-09-06T12:00:00Z", true},
		{"2026-09-06T12:00:00+05:30", true},
		{"0000-01-01T00:00:00Z", true},
		{"9999-12-31T23:59:59Z", true},
		// Year 0 at +00:10 is year -1 in UTC.
		{"0000-01-01T00:00:00+00:10", false},
		// Year 9999 at -01:00 is year 10000 in UTC.
		{"9999-12-31T23:30:00-01:00", false},
		{"not a time", false},
		{"", false},
	} {
		got, err := ParseTime(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ParseTime(%q) err = %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if !tc.ok {
			continue
		}
		// The invariant, stated directly: what it returns writes back to
		// something it accepts.
		if _, err := ParseTime(got.UTC().Format(time.RFC3339)); err != nil {
			t.Errorf("%q does not survive a UTC round trip: %v", tc.in, err)
		}
	}
	if _, err := ParseTime("0000-01-01T00:00:00+00:10"); !errors.Is(err, ErrTimeRange) {
		t.Errorf("out of range must be ErrTimeRange, got %v", err)
	}
}

// TestEmptyRangeRoundTrips pins why parseLines is lenient: an empty file's
// whole-file pick is stored as 1-0 and has to read back, or the ledger
// holding it cannot be loaded at all.
func TestEmptyRangeRoundTrips(t *testing.T) {
	t.Parallel()
	row := Row{ID: "empty-k7m2p4xq", Repo: "api", Kind: block.KindFile, File: "empty.json", Start: 1, End: 0, Hash: "h"}
	got, err := DecodeLedger(bytes.NewReader(Ledger{Rows: []Row{row}}.Bytes()))
	if err != nil {
		t.Fatalf("a 1-0 row must decode: %v", err)
	}
	if got.Rows[0].Start != 1 || got.Rows[0].End != 0 {
		t.Errorf("range = %d-%d, want 1-0", got.Rows[0].Start, got.Rows[0].End)
	}
}

// TestConflictMarkersAreNamed pins that a merge git could not finish is
// reported as a merge conflict, in every file and at every marker, instead
// of as the column-count mismatch it also is — which sent readers to repair
// a row when the fix was to finish the merge.
func TestConflictMarkersAreNamed(t *testing.T) {
	t.Parallel()
	head := "# docsync acks format=2 extract=2 repo=api commit=c scanned_at=\n" + strings.Join(acksColumns, "\t") + "\n"
	row := "2026-09-06T12:00:00Z\tk\thuman\t\ta\tapi\td.md\t3\t\th\ts\tnote\n"
	for name, in := range map[string]string{
		"ours":      head + "<<<<<<< HEAD\n" + row,
		"separator": head + row + "=======\n" + row,
		"theirs":    head + row + ">>>>>>> feature\n",
		"diff3":     head + "||||||| base\n",
		"header":    "<<<<<<< HEAD\n" + head,
	} {
		if _, err := DecodeAcks(strings.NewReader(in)); !errors.Is(err, ErrConflict) {
			t.Errorf("%s: err = %v, want ErrConflict", name, err)
		}
	}
	// Every file kind, not only acks.
	for kind, decode := range map[string]func(string) error{
		KindLedger:  func(s string) error { _, err := DecodeLedger(strings.NewReader(s)); return err },
		KindRefs:    func(s string) error { _, err := DecodeRefs(strings.NewReader(s)); return err },
		KindForeign: func(s string) error { _, err := DecodeForeign(strings.NewReader(s)); return err },
	} {
		in := "# docsync " + kind + " format=2 repo=api\ncols\n=======\n"
		if err := decode(in); !errors.Is(err, ErrConflict) {
			t.Errorf("%s: err = %v, want ErrConflict", kind, err)
		}
	}
	// A value that merely starts with = is a row, not a marker.
	ok := head + "2026-09-06T12:00:00Z\t=======\thuman\t\ta\tapi\td.md\t3\t\th\ts\tnote\n"
	if _, err := DecodeAcks(strings.NewReader(ok)); err != nil {
		t.Errorf("a row whose field is ======= must still read: %v", err)
	}
}

// TestHeaderAfterByteOrderMarks pins that a header behind one or several
// byte order marks — what a Windows editor, or two of them, leaves — reads.
func TestHeaderAfterByteOrderMarks(t *testing.T) {
	t.Parallel()
	head := "# docsync ledger format=2 repo=api\ncols\n"
	for _, marks := range []string{"", utf8BOM, utf8BOM + utf8BOM} {
		if l, err := DecodeLedger(strings.NewReader(marks + head)); err != nil || l.Header.Repo != "api" {
			t.Errorf("%d marks: %+v %v", len(marks)/len(utf8BOM), l.Header, err)
		}
	}
}

// TestEscapeFieldIsTheLedgerGrammar pins the exported escaper to the one the
// ledger uses, so another TSV writer cannot drift from it.
func TestEscapeFieldIsTheLedgerGrammar(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"plain", "a\tb", "a\nb", `a\b`} {
		if EscapeField(s) != esc(s) || UnescapeField(EscapeField(s)) != s {
			t.Errorf("%q", s)
		}
	}
}
