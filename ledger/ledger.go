// Package ledger reads and writes the three committed state files
// (docs/SPEC.md §15, §19): the ledger of every defined block, the reverse index
// of every reference, and the append-only ack log. All three are TSV with a
// one-line header that carries a format version, the repo name, and the commit
// they were produced at, so `publish` can ship them and `sync` can merge them.
//
// Why TSV and not JSON: one row per id or per reference, sorted, means a move
// is a one-line diff in a pull request and a human can read the file. Why a
// format version on every file: a newer tool upgrades older files in place, an
// older tool refuses a newer file by name instead of misparsing it (§32).
//
// Field escaping is the minimum that keeps a row on one line: tab, newline,
// carriage return, and backslash are escaped with a backslash. Nothing else is
// touched, so grep still works on ids, paths, and hashes.
package ledger

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ubgo/docsync/block"
)

// Format is the version of all three file layouts this package writes. Bump
// when a column is added or changes meaning; Decode refuses anything newer
// and reads every version down to MinFormat, so a ledger written by an older
// tool keeps working and only loses what its columns never carried.
//
// 2 added refs.seen_hash: the hash a block had when a citation was first
// recorded, which is the only baseline a citation that was never acked has.
// Without it a rescan silently adopted whatever the block said at that
// moment, and a change nobody had reviewed became invisible.
const Format = 2

// AcksFormat is the ack log's own layout version. The ack log alone gained
// columns — 3 added rule and sentence — and it is never published, so it
// versions apart: bumping Format for it would stamp the published ledger and
// refs too, and a repository still on an older ds would refuse the whole
// workspace index for two columns it never reads.
const AcksFormat = 3

// MinFormat is the oldest layout this tool still reads.
const MinFormat = 1

// File kinds, written into the header so a refs file can never be read as a
// ledger by mistake.
const (
	KindLedger  = "ledger"
	KindRefs    = "refs"
	KindAcks    = "acks"
	KindForeign = "foreign"
)

// Default file names under `.ds/`.
const (
	LedgerFile  = "ledger.tsv"
	RefsFile    = "refs.tsv"
	AcksFile    = "acks.tsv"
	ForeignFile = "foreign.tsv"
)

// utf8BOM is the byte order mark some editors prepend to a text file.
const utf8BOM = "\ufeff"

// Sentinel errors.
var (
	ErrBadHeader = errors.New("ledger: missing or malformed header line")
	ErrKind      = errors.New("ledger: file kind does not match")
	ErrFormat    = errors.New("ledger: format version not supported by this tool")
	ErrColumns   = errors.New("ledger: column count mismatch")
	ErrField     = errors.New("ledger: bad field value")
	// ErrConflict is a line git left behind when a merge conflicted. It is
	// reported as itself, not as the column-count mismatch it also is, so
	// the reader is told to finish the merge rather than to repair a row.
	ErrConflict = errors.New("ledger: unresolved merge conflict")
)

// Header is the metadata line at the top of every file.
type Header struct {
	Kind   string
	Format int
	// Extract is the version of the extraction rules the hashes in this
	// file were computed under (extract.Rule). A hash is only comparable
	// with another computed under the same rule, and nothing in a hash says
	// which rule produced it — so the rule is recorded beside them. Absent,
	// which is every file written before it existed, reads as 1.
	Extract   int
	Repo      string
	Commit    string
	ScannedAt time.Time
}

// Row is one defined block as stored. It is a flattened block.Block plus the
// repo that owns it. Args carries every directive key so nothing is lost; the
// named columns duplicate the ones reports sort and filter on.
type Row struct {
	ID        string
	Repo      string
	Kind      block.Kind
	File      string
	Symbol    string
	Start     int
	End       int
	Hash      string
	Owner     string
	Stability block.Stability
	Env       string
	Args      map[string]string
}

// FromBlock flattens a scanned block into a row.
func FromBlock(repo string, b block.Block) Row {
	return Row{
		ID: b.ID, Repo: repo, Kind: b.Kind, File: b.Pos.File, Symbol: b.Symbol,
		Start: b.Pos.Start, End: b.Pos.End, Hash: b.Hash, Owner: b.Owner(),
		Stability: b.Stability(), Env: b.Env(), Args: b.Args,
	}
}

// ToBlock rebuilds a block (without Content) from a row, for matching and
// rendering permalinks without re-reading source.
func (r Row) ToBlock() block.Block {
	return block.Block{ID: r.ID, Kind: r.Kind, Symbol: r.Symbol, Pos: block.Position{File: r.File, Start: r.Start, End: r.End}, Hash: r.Hash, Args: r.Args}
}

// ForeignRow is one block another repository published that this one cites:
// a published ledger row plus the commit that repo published it at, which a
// per-file header cannot carry because the rows come from several repos.
type ForeignRow struct {
	Row
	// Commit is the publishing repo's commit for this row.
	Commit string
}

// Foreign is the committed snapshot of the foreign blocks a repo cites
// (docs/SPEC.md §21). It is what makes `check` reproducible across repos:
// without it the answer for one commit depends on what upstream last
// published and on when the index was last synced, so CI can go red with no
// change in the repo under test and an old commit cannot be re-run.
//
// It holds cited ids only, never the whole merged ledger, so an unrelated
// upstream edit does not churn every downstream repo's `.ds/`.
type Foreign struct {
	Header Header
	Rows   []ForeignRow
}

// Bytes renders the snapshot.
func (f Foreign) Bytes() []byte {
	var b bytes.Buffer
	_ = f.Encode(&b)
	return b.Bytes()
}

// Encode writes rows sorted by id, so a resync is a readable diff.
func (f Foreign) Encode(w io.Writer) error {
	bw := bufio.NewWriter(w)
	h := f.Header
	h.Kind, h.Format = KindForeign, Format
	writeHeader(bw, h, foreignColumns)
	rows := append([]ForeignRow(nil), f.Rows...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ID != rows[j].ID {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Env < rows[j].Env
	})
	for _, r := range rows {
		writeRow(bw, []string{r.ID, r.Repo, r.Commit, string(r.Kind), r.File, r.Symbol, lines(r.Start, r.End), r.Hash, r.Owner, string(r.Stability), r.Env, encodeArgs(r.Args)})
	}
	return bw.Flush()
}

// DecodeForeign reads a snapshot.
func DecodeForeign(r io.Reader) (Foreign, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	h, err := parseHeader(sc, KindForeign)
	if err != nil {
		return Foreign{}, err
	}
	out := Foreign{Header: h}
	for n := 3; sc.Scan(); n++ {
		if sc.Text() == "" {
			continue
		}
		f, err := splitRow(sc.Text(), len(foreignColumns), n)
		if err != nil {
			return Foreign{}, err
		}
		start, end, err := parseLines(f[6])
		if err != nil {
			return Foreign{}, fmt.Errorf("line %d: %w", n, err)
		}
		args, err := decodeArgs(f[11])
		if err != nil {
			return Foreign{}, err
		}
		out.Rows = append(out.Rows, ForeignRow{
			Row:    Row{ID: f[0], Repo: f[1], Kind: block.Kind(f[3]), File: f[4], Symbol: f[5], Start: start, End: end, Hash: f[7], Owner: f[8], Stability: block.Stability(f[9]), Env: f[10], Args: args},
			Commit: f[2],
		})
	}
	return out, sc.Err()
}

// Blocks rebuilds the foreign rows as blocks, for a check that resolves
// citations from the snapshot instead of the live index.
func (f Foreign) Blocks() []block.Block {
	out := make([]block.Block, 0, len(f.Rows))
	for _, r := range f.Rows {
		out = append(out, r.ToBlock())
	}
	return out
}

// RefRow is one reference as stored. AckedHash, SeenHash and SentenceHash
// are what `check` compares to decide ok versus unacked.
type RefRow struct {
	ID      string
	Repo    string
	Doc     string
	Line    int
	Verb    string
	Carrier block.Carrier
	// AckedHash is the block hash a human or agent last accepted for this
	// sentence. Empty until something acks it.
	AckedHash string
	// SeenHash is the block hash the first scan that recorded this citation
	// saw, and it is written once and never revised: it is the fallback
	// baseline for a citation nobody has acked, so that an unreviewed change
	// is still reported. Revising it on every scan would recreate exactly
	// the hole it exists to close. Empty when read from a format 1 file, and
	// filled by the next scan.
	SeenHash     string
	SentenceHash string
	Env          string
	Args         map[string]string
}

// FromReference flattens a scanned reference. AckedHash and SeenHash start
// empty; acks fill the first and Snapshot fills the second.
func FromReference(repo string, r block.Reference) RefRow {
	return RefRow{ID: r.ID, Repo: repo, Doc: r.Pos.File, Line: r.Pos.Start, Verb: r.Verb, Carrier: r.Carrier, SentenceHash: r.SentenceHash, Env: r.Args[block.KeyEnv], Args: r.Args}
}

// Key is the position an ack for this citation is recorded against.
func (r RefRow) Key() AckKey {
	return AckKey{Repo: r.Repo, Doc: r.Doc, Line: r.Line, ID: r.ID, Env: r.Env}
}

// ToReference rebuilds a reference from a row for merged-workspace checks;
// Sentence is not stored, only its hash.
func (r RefRow) ToReference() block.Reference {
	return block.Reference{Verb: r.Verb, ID: r.ID, Pos: block.Position{File: r.Doc, Start: r.Line, End: r.Line}, Carrier: r.Carrier, SentenceHash: r.SentenceHash, Args: r.Args}
}

// ActorKind says who made an ack (§19, §26.7).
type ActorKind string

// The actor kinds. An agent ack must name the human who delegated it
// (Ack.DelegatedBy); System.Ack refuses one that does not.
const (
	ActorHuman ActorKind = "human"
	ActorAgent ActorKind = "agent"
)

// ActorKindValues is the canonical order.
var ActorKindValues = []ActorKind{ActorHuman, ActorAgent}

// Ack is one append-only approval event.
type Ack struct {
	At           time.Time
	Actor        string
	ActorKind    ActorKind
	DelegatedBy  string
	ID           string
	Repo         string
	Doc          string
	Line         int
	Env          string
	BlockHash    string
	SentenceHash string
	Note         string
	// Rule is the extraction rule (extract.Rule) the ack was recorded
	// under, and Sentence the text of the sentence it approved. A sentence
	// hash is only comparable with one bound by the same rule, so an ack
	// from an older rule cannot say whether the wording changed; and a
	// rewritten sentence is only reviewable when the old text is there to
	// show beside the new. Both are empty in an ack log written before
	// AcksFormat 3.
	Rule     int
	Sentence string
}

// Ledger is a header plus rows.
type Ledger struct {
	Header Header
	Rows   []Row
}

// Refs is a header plus reference rows.
type Refs struct {
	Header Header
	Rows   []RefRow
}

// Acks is a header plus ack events, in the order they were recorded.
type Acks struct {
	Header Header
	Rows   []Ack
}

// Column lists per kind. Order is the file order; tests pin them so a
// reordering is a deliberate format bump.
var (
	ledgerColumns = []string{"id", "repo", "kind", "file", "symbol", "lines", "hash", "owner", "stability", "env", "args"}
	refsColumns   = []string{"id", "repo", "doc", "line", "verb", "carrier", "acked_hash", "seen_hash", "sentence_hash", "env", "args"}
	// refsColumnsV1 is the format 1 layout, still readable: it is
	// refsColumns without seen_hash.
	refsColumnsV1  = []string{"id", "repo", "doc", "line", "verb", "carrier", "acked_hash", "sentence_hash", "env", "args"}
	acksColumns    = []string{"at", "actor", "actor_kind", "delegated_by", "id", "repo", "doc", "line", "env", "block_hash", "sentence_hash", "note", "rule", "sentence"}
	acksColumnsV2  = acksColumns[:12]
	foreignColumns = []string{"id", "repo", "commit", "kind", "file", "symbol", "lines", "hash", "owner", "stability", "env", "args"}
)

// ---------------------------------------------------------------- encoding

// Write errors on a bufio.Writer are sticky: once one occurs every later
// write and the final Flush return it. The encoders therefore write without
// checking each call and return Flush's error, which is the first failure.
func writeHeader(w *bufio.Writer, h Header, columns []string) {
	ts := ""
	if !h.ScannedAt.IsZero() {
		ts = h.ScannedAt.UTC().Format(time.RFC3339)
	}
	// A header that does not say which rule its hashes came from is rule 1,
	// on write as on read: the field was added when the rules first
	// changed, so every file without it predates that. Writing it always
	// keeps a decode/encode round trip byte-stable. Snapshot stamps the
	// real rule and is tested for it, because a writer that forgot would
	// claim its hashes were older than they are and invite a migration
	// that is not needed.
	rule := h.Extract
	if rule == 0 {
		rule = 1
	}
	fmt.Fprintf(w, "# docsync %s format=%d extract=%d repo=%s commit=%s scanned_at=%s\n", h.Kind, h.Format, rule, escHeader(h.Repo), escHeader(h.Commit), ts)
	w.WriteString(strings.Join(columns, "\t") + "\n")
}

// Encode writes the ledger sorted by id. Sorting here, not in the caller,
// is what makes the file diff-stable no matter how it was produced.
func (l Ledger) Encode(w io.Writer) error {
	bw := bufio.NewWriter(w)
	h := l.Header
	h.Kind, h.Format = KindLedger, Format
	writeHeader(bw, h, ledgerColumns)
	rows := append([]Row(nil), l.Rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	for _, r := range rows {
		writeRow(bw, []string{r.ID, r.Repo, string(r.Kind), r.File, r.Symbol, lines(r.Start, r.End), r.Hash, r.Owner, string(r.Stability), r.Env, encodeArgs(r.Args)})
	}
	return bw.Flush()
}

// Encode writes references sorted by repo, doc, line, verb, id.
func (r Refs) Encode(w io.Writer) error {
	bw := bufio.NewWriter(w)
	h := r.Header
	h.Kind, h.Format = KindRefs, Format
	writeHeader(bw, h, refsColumns)
	rows := append([]RefRow(nil), r.Rows...)
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Doc != b.Doc {
			return a.Doc < b.Doc
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Verb != b.Verb {
			return a.Verb < b.Verb
		}
		return a.ID < b.ID
	})
	for _, x := range rows {
		writeRow(bw, []string{x.ID, x.Repo, x.Doc, strconv.Itoa(x.Line), x.Verb, string(x.Carrier), x.AckedHash, x.SeenHash, x.SentenceHash, x.Env, encodeArgs(x.Args)})
	}
	return bw.Flush()
}

// Encode writes acks in recorded order; the log is append-only and never
// sorted, because order is part of the audit trail.
func (a Acks) Encode(w io.Writer) error {
	bw := bufio.NewWriter(w)
	h := a.Header
	h.Kind, h.Format = KindAcks, AcksFormat
	writeHeader(bw, h, acksColumns)
	for _, x := range a.Rows {
		writeRow(bw, []string{x.At.UTC().Format(time.RFC3339), x.Actor, string(x.ActorKind), x.DelegatedBy, x.ID, x.Repo, x.Doc, strconv.Itoa(x.Line), x.Env, x.BlockHash, x.SentenceHash, x.Note, strconv.Itoa(x.Rule), x.Sentence})
	}
	return bw.Flush()
}

func writeRow(w *bufio.Writer, fields []string) {
	for i, f := range fields {
		if i > 0 {
			w.WriteByte('\t')
		}
		w.WriteString(esc(f))
	}
	w.WriteByte('\n')
}

func lines(start, end int) string {
	if start == end {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "-" + strconv.Itoa(end)
}

// esc escapes the four characters that would break a row.
func esc(s string) string {
	if !strings.ContainsAny(s, "\t\n\r\\") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// unesc reverses esc. An unknown escape is kept literally rather than
// rejected, so a hand-edited row degrades to a visible oddity, not a refusal.
func unesc(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b.WriteByte(c)
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
		case '\\':
			b.WriteByte('\\')
		case 's':
			// Only escHeader writes \s. esc doubles every backslash, so no
			// row field it wrote can contain one, and reading it as a space
			// everywhere keeps one grammar for the whole file.
			b.WriteByte(' ')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// EscapeField escapes a value for one column of a docsync TSV file, so a
// tab, newline, carriage return or backslash in it cannot split a row. It is
// exported so every TSV docsync writes — tests.tsv as well as the ledger —
// uses one grammar; UnescapeField reverses it.
func EscapeField(s string) string { return esc(s) }

// UnescapeField reverses EscapeField.
func UnescapeField(s string) string { return unesc(s) }

// escHeader escapes a header value. The header line is a space-separated
// list of key=value pairs, so on top of what esc handles a value must not
// carry a literal space: a repo checked out as `My Docs` otherwise reads
// back as `My`. unesc reverses it.
func escHeader(s string) string { return strings.ReplaceAll(esc(s), " ", `\s`) }

// encodeArgs renders a key=value map sorted by key, space separated, with
// values percent-escaped for space, `%`, and `=` so the column splits back.
func encodeArgs(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+pctEsc(m[k]))
	}
	return strings.Join(parts, " ")
}

func pctEsc(v string) string {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		switch c := v[i]; c {
		case ' ', '%', '=', '\t', '\n':
			fmt.Fprintf(&b, "%%%02X", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func decodeArgs(s string) (map[string]string, error) {
	if s == "" {
		return nil, nil
	}
	m := map[string]string{}
	for _, part := range strings.Split(s, " ") {
		k, v, ok := strings.Cut(part, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%w: args %q", ErrField, s)
		}
		u, err := pctUnesc(v)
		if err != nil {
			return nil, fmt.Errorf("%w: args %q: %v", ErrField, s, err)
		}
		m[k] = u
	}
	return m, nil
}

func pctUnesc(v string) (string, error) {
	if !strings.Contains(v, "%") {
		return v, nil
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] != '%' {
			b.WriteByte(v[i])
			continue
		}
		if i+2 >= len(v) {
			return "", errors.New("truncated percent escape")
		}
		n, err := strconv.ParseUint(v[i+1:i+3], 16, 8)
		if err != nil {
			return "", err
		}
		b.WriteByte(byte(n))
		i += 2
	}
	return b.String(), nil
}

// Years RFC 3339 can write. Go formats a year outside them with a sign or
// a fifth digit, which its own parser then rejects.
const (
	minTimeYear = 0
	maxTimeYear = 9999
)

// ErrTimeRange is a timestamp that parses but cannot be written back.
var ErrTimeRange = errors.New("ledger: timestamp outside the years RFC 3339 can write")

// ParseTime reads an RFC 3339 timestamp the way every docsync file stores
// one, and rejects a value whose UTC form falls outside the years RFC 3339
// can express.
//
// Why it exists: every writer formats times in UTC. A value such as
// 0000-01-01T00:00:00+00:10 parses, but is year -1 in UTC, so re-saving the
// file writes a timestamp no reader accepts — the file loads once and never
// again. Rejecting it on read keeps every file that decodes re-savable.
//
// Invariant: a time it returns formats, in UTC, to a string it accepts.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	if y := t.UTC().Year(); y < minTimeYear || y > maxTimeYear {
		return time.Time{}, fmt.Errorf("%w: %q is year %d in UTC", ErrTimeRange, s, y)
	}
	return t, nil
}

// ---------------------------------------------------------------- decoding

// parseHeader reads the first line and validates kind and format.
func parseHeader(sc *bufio.Scanner, wantKind string) (Header, error) {
	if !sc.Scan() {
		return Header{}, ErrBadHeader
	}
	// A UTF-8 byte order mark is what a Windows editor adds on save; it is
	// not content, and without this the header reads as malformed.
	line := sc.Text()
	for strings.HasPrefix(line, utf8BOM) {
		line = line[len(utf8BOM):]
	}
	if isConflictMarker(line) {
		return Header{}, fmt.Errorf("%w: line 1 is %q", ErrConflict, line)
	}
	// Split on the ASCII space escHeader protects, not on every Unicode
	// space as strings.Fields would: a value holding U+00A0 or U+0085 is
	// one value. Empty tokens, from a hand edit that doubled a space, are
	// dropped.
	var fields []string
	for _, f := range strings.Split(line, " ") {
		if f != "" {
			fields = append(fields, f)
		}
	}
	if len(fields) < 3 || fields[0] != "#" || fields[1] != "docsync" {
		return Header{}, fmt.Errorf("%w: %q", ErrBadHeader, line)
	}
	h := Header{Kind: fields[2]}
	if h.Kind != wantKind {
		return Header{}, fmt.Errorf("%w: file is %q, want %q", ErrKind, h.Kind, wantKind)
	}
	for _, kv := range fields[3:] {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "format":
			n, err := strconv.Atoi(v)
			if err != nil {
				return Header{}, fmt.Errorf("%w: format %q", ErrBadHeader, v)
			}
			h.Format = n
		case "extract":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Header{}, fmt.Errorf("%w: extract %q", ErrBadHeader, v)
			}
			h.Extract = n
		case "repo":
			h.Repo = unesc(v)
		case "commit":
			h.Commit = unesc(v)
		case "scanned_at":
			if v != "" {
				t, err := ParseTime(v)
				if err != nil {
					return Header{}, fmt.Errorf("%w: scanned_at %q", ErrBadHeader, v)
				}
				h.ScannedAt = t
			}
		}
	}
	if h.Extract == 0 {
		// Written before the rule was recorded, which is rule 1 by
		// definition: the field was added when the rules first changed.
		h.Extract = 1
	}
	newest := Format
	if wantKind == KindAcks {
		newest = AcksFormat
	}
	if h.Format < MinFormat || h.Format > newest {
		return Header{}, fmt.Errorf("%w: file format %d, this tool reads %d to %d", ErrFormat, h.Format, MinFormat, newest)
	}
	// Column line: present for readability; its content is not trusted over
	// the format version, so it is consumed and checked only for count.
	if !sc.Scan() {
		return Header{}, ErrBadHeader
	}
	return h, nil
}

// Conflict markers as git writes them. A data row always contains tabs and a
// header starts "# docsync", so none of these can be a legitimate line.
var conflictMarkers = []string{"<<<<<<< ", "||||||| ", ">>>>>>> "}

const conflictSeparator = "======="

// isConflictMarker reports a line git wrote to mark an unresolved merge.
func isConflictMarker(line string) bool {
	if line == conflictSeparator {
		return true
	}
	for _, m := range conflictMarkers {
		if strings.HasPrefix(line, m) {
			return true
		}
	}
	return false
}

// splitRow splits a data line and unescapes fields.
func splitRow(line string, want int, n int) ([]string, error) {
	if isConflictMarker(line) {
		return nil, fmt.Errorf("%w: line %d is %q", ErrConflict, n, line)
	}
	raw := strings.Split(line, "\t")
	if len(raw) != want {
		return nil, fmt.Errorf("%w: line %d has %d columns, want %d", ErrColumns, n, len(raw), want)
	}
	for i := range raw {
		raw[i] = unesc(raw[i])
	}
	return raw, nil
}

// parseLines reads a `lines` column: `N` or `N-M`.
//
// It does not reject end < start, although that looks like corruption: a
// whole-file pick of an empty file is 1-0 (pick.rangeResult over zero
// lines), and refusing it would make any ledger holding one unreadable.
// Every consumer that slices source by a stored range bounds-checks it
// instead, and treats an impossible range as unknown content.
func parseLines(s string) (int, int, error) {
	a, b, ok := strings.Cut(s, "-")
	start, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: lines %q", ErrField, s)
	}
	if !ok {
		return start, start, nil
	}
	end, err := strconv.Atoi(b)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: lines %q", ErrField, s)
	}
	return start, end, nil
}

func atoi(s string, n int) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%w: line %d: %q is not a number", ErrField, n, s)
	}
	return v, nil
}

// DecodeLedger reads a ledger file. Rows come back in file order.
func DecodeLedger(r io.Reader) (Ledger, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	h, err := parseHeader(sc, KindLedger)
	if err != nil {
		return Ledger{}, err
	}
	l := Ledger{Header: h}
	for n := 3; sc.Scan(); n++ {
		if sc.Text() == "" {
			continue
		}
		f, err := splitRow(sc.Text(), len(ledgerColumns), n)
		if err != nil {
			return Ledger{}, err
		}
		start, end, err := parseLines(f[5])
		if err != nil {
			return Ledger{}, err
		}
		args, err := decodeArgs(f[10])
		if err != nil {
			return Ledger{}, err
		}
		l.Rows = append(l.Rows, Row{ID: f[0], Repo: f[1], Kind: block.Kind(f[2]), File: f[3], Symbol: f[4], Start: start, End: end, Hash: f[6], Owner: f[7], Stability: block.Stability(f[8]), Env: f[9], Args: args})
	}
	return l, sc.Err()
}

// DecodeRefs reads a refs file.
func DecodeRefs(r io.Reader) (Refs, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	h, err := parseHeader(sc, KindRefs)
	if err != nil {
		return Refs{}, err
	}
	out := Refs{Header: h}
	// A format 1 file has no seen_hash column. Its rows are widened here so
	// the rest of the tool never has to know which version it came from; the
	// missing baseline is filled by the next scan.
	columns := refsColumns
	if h.Format < 2 {
		columns = refsColumnsV1
	}
	for n := 3; sc.Scan(); n++ {
		if sc.Text() == "" {
			continue
		}
		f, err := splitRow(sc.Text(), len(columns), n)
		if err != nil {
			return Refs{}, err
		}
		if h.Format < 2 {
			f = append(f[:7:7], append([]string{""}, f[7:]...)...)
		}
		line, err := atoi(f[3], n)
		if err != nil {
			return Refs{}, err
		}
		args, err := decodeArgs(f[10])
		if err != nil {
			return Refs{}, err
		}
		out.Rows = append(out.Rows, RefRow{ID: f[0], Repo: f[1], Doc: f[2], Line: line, Verb: f[4], Carrier: block.Carrier(f[5]), AckedHash: f[6], SeenHash: f[7], SentenceHash: f[8], Env: f[9], Args: args})
	}
	return out, sc.Err()
}

// DecodeAcks reads an ack log.
func DecodeAcks(r io.Reader) (Acks, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	h, err := parseHeader(sc, KindAcks)
	if err != nil {
		return Acks{}, err
	}
	out := Acks{Header: h}
	// Before AcksFormat 3 a row had no rule or sentence; they read as 0 and
	// "", which check treats as an ack made under another rule.
	columns := acksColumns
	if h.Format < AcksFormat {
		columns = acksColumnsV2
	}
	for n := 3; sc.Scan(); n++ {
		if sc.Text() == "" {
			continue
		}
		f, err := splitRow(sc.Text(), len(columns), n)
		if err != nil {
			return Acks{}, err
		}
		if h.Format < AcksFormat {
			f = append(f, "0", "")
		}
		rule, err := strconv.Atoi(f[12])
		if err != nil || rule < 0 {
			return Acks{}, fmt.Errorf("%w: line %d: rule %q", ErrField, n, f[12])
		}
		at, err := ParseTime(f[0])
		if err != nil {
			return Acks{}, fmt.Errorf("%w: line %d: at %q", ErrField, n, f[0])
		}
		line, err := atoi(f[7], n)
		if err != nil {
			return Acks{}, err
		}
		out.Rows = append(out.Rows, Ack{At: at, Actor: f[1], ActorKind: ActorKind(f[2]), DelegatedBy: f[3], ID: f[4], Repo: f[5], Doc: f[6], Line: line, Env: f[8], BlockHash: f[9], SentenceHash: f[10], Note: f[11], Rule: rule, Sentence: f[13]})
	}
	return out, sc.Err()
}

// ---------------------------------------------------------------- lookups

// Bytes encodes to memory. Encode only fails on a writer error and a
// bytes.Buffer has none, so callers that need the bytes (stores, tests) get
// them without an error branch they could never exercise.
func (l Ledger) Bytes() []byte { var b bytes.Buffer; _ = l.Encode(&b); return b.Bytes() }

// Bytes encodes to memory; see Ledger.Bytes.
func (r Refs) Bytes() []byte { var b bytes.Buffer; _ = r.Encode(&b); return b.Bytes() }

// Bytes encodes to memory; see Ledger.Bytes.
func (a Acks) Bytes() []byte { var b bytes.Buffer; _ = a.Encode(&b); return b.Bytes() }

// Index maps id to row. The last row wins on duplicates, which cannot occur
// in a file this package wrote; merged workspace input is deduplicated by the
// caller with its own collision policy.
func (l Ledger) Index() map[string]Row {
	m := make(map[string]Row, len(l.Rows))
	for _, r := range l.Rows {
		m[r.ID] = r
	}
	return m
}

// ByHash maps a body hash to every row carrying it, for `moved-unmarked`
// detection: a vanished id whose hash still exists somewhere.
func (l Ledger) ByHash() map[string][]Row {
	m := map[string][]Row{}
	for _, r := range l.Rows {
		m[r.Hash] = append(m[r.Hash], r)
	}
	return m
}

// ByID groups references by the id they point at: the reverse index.
func (r Refs) ByID() map[string][]RefRow {
	m := map[string][]RefRow{}
	for _, x := range r.Rows {
		if x.ID != "" {
			m[x.ID] = append(m[x.ID], x)
		}
	}
	return m
}

// Latest returns the newest ack per sentence: the one with the latest At,
// with file order breaking a tie.
//
// Why not file order alone: one writer appends in time order, but a merge
// does not. Git's union driver, which keeps two branches' acks instead of
// conflicting, puts one branch's rows before the other's, so after a merge
// the last row for a sentence is whichever branch was merged second — not
// whichever ack was made last.
func (a Acks) Latest() map[AckKey]Ack {
	m := map[AckKey]Ack{}
	for _, x := range a.Rows {
		k := AckKey{Repo: x.Repo, Doc: x.Doc, Line: x.Line, ID: x.ID, Env: x.Env}
		if prev, ok := m[k]; ok && x.At.Before(prev.At) {
			continue
		}
		m[k] = x
	}
	return m
}

// AckKey identifies the sentence an ack applies to.
type AckKey struct {
	Repo string
	Doc  string
	Line int
	ID   string
	Env  string
}

// RootShard names the shard for files at the repository root.
const RootShard = "_root"

// Shard splits a ledger by the top-level directory of each row's file
// (§ Scale "ledger sharded per top-level directory"), so a large repo
// commits many small files that merge cleanly instead of one that
// conflicts. Every shard carries the same header.
func (l Ledger) Shard() map[string]Ledger {
	out := map[string]Ledger{}
	for _, row := range l.Rows {
		name := RootShard
		if i := strings.IndexByte(row.File, '/'); i > 0 {
			name = row.File[:i]
		}
		sh := out[name]
		sh.Header = l.Header
		sh.Rows = append(sh.Rows, row)
		out[name] = sh
	}
	return out
}

// Unshard merges shards back, sorted the way Encode sorts rows; the header
// is taken from the first shard by name.
func Unshard(shards map[string]Ledger) Ledger {
	names := make([]string, 0, len(shards))
	for n := range shards {
		names = append(names, n)
	}
	sort.Strings(names)
	var out Ledger
	for i, n := range names {
		if i == 0 {
			out.Header = shards[n].Header
		}
		out.Rows = append(out.Rows, shards[n].Rows...)
	}
	sort.Slice(out.Rows, func(i, j int) bool { return out.Rows[i].ID < out.Rows[j].ID })
	return out
}
