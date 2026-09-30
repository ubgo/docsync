// Package workspace merges what every repository in a workspace published
// into one id table and one reverse index (docs/SPEC.md §21), and reads the
// index layout that `publish` writes and `sync` reads:
//
//	repos/<name>/ledger.tsv    the repo's ledger at its published commit
//	repos/<name>/refs.tsv      its reverse index
//	repos/<name>/tests.tsv     optional; test outcomes for assert= (§9.2)
//	repos/<name>/blocks/<hash> optional; block bodies by content hash (§20.1),
//	                           so a consumer can classify a citation whose
//	                           acked hash is older than the published ledger
//
// Everything here is pure over the bytes it is given; the CLI fetches the
// index, hands the files over, and writes what publish returns.
package workspace

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/check"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ledger"
)

// Index layout.
const (
	ReposDir  = "repos"
	TestsFile = "tests.tsv"
	// BlocksDir holds published block bodies keyed by content hash. It is
	// content-addressed, so republishing an unchanged block writes nothing
	// and history accumulates without duplication.
	BlocksDir = "blocks"
	// testsFormat is the tests.tsv header version.
	testsFormat = 1
)

// Errors.
var (
	ErrDuplicateAcrossRepos = errors.New("workspace: id defined in more than one repository")
	ErrNotPublished         = errors.New("workspace: repository has not published")
	ErrTestsFormat          = errors.New("workspace: tests.tsv format not understood")
)

// Entry is one repository's published state: what publish writes and what
// sync reads back.
type Entry struct {
	Repo string
	// Branch is empty for the default branch; a non-default branch
	// publishes under repos/<name>/@<branch>/ and its blocks carry
	// block.KeyBranch after the merge (Part VII "Ids and branches").
	Branch string
	Ledger ledger.Ledger
	Refs   ledger.Refs
	Tests  map[string]check.TestOutcome
	// Bodies are block bodies keyed by content hash (§20.1). Secret and
	// local blocks are excluded upstream, in System.Bodies, because the
	// index may be readable by people who cannot read the source repo.
	Bodies map[string]string
}

// branchPrefix marks a branch directory inside a repo's index directory.
const branchPrefix = "@"

// Duplicate is an id published by more than one repository.
type Duplicate struct {
	ID    string   `json:"id"`
	Repos []string `json:"repos"`
}

// Merged is the workspace view a check consumes.
type Merged struct {
	// Defs are every published block, each carrying its repo in Args under
	// RepoKey, so a citing repo can build permalinks into the right place.
	Defs []block.Block
	// RepoOf maps an id to the repository that defines it.
	RepoOf map[string]string
	// Refs are the references each repository published from its default
	// branch, with the repo. A branch's are in BranchRefs: they carried no
	// branch marker, so an unmerged branch's citations were checked
	// upstream as the default branch's, and one doc line reported twice.
	Refs []ledger.RefRow
	// BranchRefs are references published from non-default branches, for a
	// question where erring towards "still cited" is the safe answer.
	BranchRefs []ledger.RefRow
	// Tests are the union of published test outcomes.
	Tests map[string]check.TestOutcome
	// Duplicates are rejected at publish (§21); a sync that still finds
	// them reports them so the offending publish can be fixed.
	Duplicates []Duplicate
	// Published records each repo's commit and scan time for staleness.
	Published map[string]Published
	// BranchPublished is Published for each non-default branch, keyed by
	// BranchKey, so a snapshot row taken from a branch records that
	// branch's commit rather than the default branch's.
	BranchPublished map[string]Published
	// Removed maps ids published by repositories that are no longer in the
	// workspace to that repo; MergeFor fills it, Merge leaves it empty.
	Removed map[string]string
}

// Published is one repo's index stamp.
type Published struct {
	Commit    string    `json:"commit"`
	ScannedAt time.Time `json:"scanned_at"`
}

// RepoKey is the block arg under which a merged def carries its repository
// (block.KeyRepo). It is not a directive key an author writes; the merge
// adds it so a citation into another repo knows where the block lives.
const RepoKey = block.KeyRepo

// Merge combines entries. Entries are sorted by repo name first so the
// result is deterministic regardless of the order the index was read.
func Merge(entries []Entry) Merged {
	return MergeFor(entries, nil)
}

// MergeFor merges only the repositories named (by their index name) and
// records every id the others had published, so a citing repo can say
// "repo removed" instead of "deleted" (§21). A nil list keeps every entry.
func MergeFor(entries []Entry, repos []string) Merged {
	// An entry is kept by its index name, the one config.RepoName gives the
	// listed URL: the same rule publish names the directory by, so the two
	// cannot disagree about which repository a directory belongs to.
	keep := map[string]bool{}
	for _, r := range repos {
		keep[config.RepoName(r)] = true
	}
	sorted := append([]Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Repo != sorted[j].Repo {
			return sorted[i].Repo < sorted[j].Repo
		}
		return sorted[i].Branch < sorted[j].Branch
	})
	m := Merged{RepoOf: map[string]string{}, Tests: map[string]check.TestOutcome{}, Published: map[string]Published{}, BranchPublished: map[string]Published{}, Removed: map[string]string{}}
	owners := map[string][]string{}
	for _, e := range sorted {
		if repos != nil && !keep[e.Repo] {
			for _, row := range e.Ledger.Rows {
				m.Removed[row.ID] = e.Repo
			}
			continue
		}
		pub := Published{Commit: e.Ledger.Header.Commit, ScannedAt: e.Ledger.Header.ScannedAt}
		if e.Branch == "" {
			m.Published[e.Repo] = pub
		} else {
			m.BranchPublished[BranchKey(e.Repo, e.Branch)] = pub
		}
		for _, row := range e.Ledger.Rows {
			b := row.ToBlock()
			b.Args = withRepo(b.Args, e.Repo)
			if e.Branch != "" {
				b.Args[block.KeyBranch] = e.Branch
			}
			m.Defs = append(m.Defs, b)
			if _, seen := m.RepoOf[row.ID]; !seen {
				m.RepoOf[row.ID] = e.Repo
			}
			owners[row.ID] = appendUnique(owners[row.ID], e.Repo)
		}
		for _, r := range e.Refs.Rows {
			r.Repo = e.Repo
			if e.Branch == "" {
				m.Refs = append(m.Refs, r)
			} else {
				m.BranchRefs = append(m.BranchRefs, r)
			}
		}
		// Test outcomes are the default branch's published run: one map
		// keyed by test id has no room for a branch, and taking every
		// entry's let a red feature branch — merged after main — mark each
		// assert=true citation on main as a failing test.
		if e.Branch == "" {
			for id, o := range e.Tests {
				m.Tests[id] = o
			}
		}
	}
	for id, repos := range owners {
		if len(repos) > 1 {
			m.Duplicates = append(m.Duplicates, Duplicate{ID: id, Repos: repos})
		}
	}
	sort.Slice(m.Duplicates, func(i, j int) bool { return m.Duplicates[i].ID < m.Duplicates[j].ID })
	return m
}

func withRepo(args map[string]string, repo string) map[string]string {
	out := make(map[string]string, len(args)+1)
	for k, v := range args {
		out[k] = v
	}
	out[RepoKey] = repo
	return out
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// Others returns the merged defs and refs that do not belong to repo: what
// a repo's own check should see in addition to its live scan.
func (m Merged) Others(repo string) ([]block.Block, []ledger.RefRow) {
	var defs []block.Block
	for _, b := range m.Defs {
		if b.Args[RepoKey] != repo {
			defs = append(defs, b)
		}
	}
	var refs []ledger.RefRow
	for _, r := range m.Refs {
		if r.Repo != repo {
			refs = append(refs, r)
		}
	}
	return defs, refs
}

// Snapshot builds the committed record of the foreign blocks `cited` names
// (§21). Only cited ids are included: the whole merged ledger would churn
// every downstream repo's `.ds/` on an unrelated upstream edit, which would
// teach people to ignore the diff that is the point of the file.
//
// Each row carries the publishing repo's commit, so a later sync can say
// what moved and `status` can report how far behind a citation is.
func (m Merged) Snapshot(repo string, cited map[Cite]bool, h ledger.Header) ledger.Foreign {
	out := ledger.Foreign{Header: h}
	out.Header.Kind, out.Header.Format = ledger.KindForeign, ledger.Format
	for _, b := range m.Defs {
		owner, branch := b.Args[RepoKey], b.Args[block.KeyBranch]
		if owner == repo || !cited[Cite{ID: b.ID, Branch: branch}] {
			continue
		}
		commit := m.Published[owner].Commit
		if branch != "" {
			commit = m.BranchPublished[BranchKey(owner, branch)].Commit
		}
		row := ledger.FromBlock(owner, b)
		out.Rows = append(out.Rows, ledger.ForeignRow{Row: row, Commit: commit})
	}
	return out
}

// Cite is what a citation asks for from the index: an id, and the branch it
// selects with branch=, empty for the default branch. A snapshot records
// the defs cited this way and no others: taking every published branch of
// a cited id rewrote each downstream foreign.tsv whenever anyone published
// a branch, and stamped those rows with the default branch's commit.
type Cite struct{ ID, Branch string }

// BranchKey names one branch of one repository.
func BranchKey(repo, branch string) string { return repo + branchPrefix + branch }

// CheckDuplicate reports whether publishing ledger for repo would define an
// id another repository already owns (§21 "duplicate ids across repos are
// rejected at publish").
func (m Merged) CheckDuplicate(repo string, l ledger.Ledger) error {
	for _, row := range l.Rows {
		if owner, ok := m.RepoOf[row.ID]; ok && owner != repo {
			return fmt.Errorf("%w: %s is owned by %s", ErrDuplicateAcrossRepos, row.ID, owner)
		}
	}
	return nil
}

// Dir is the entry's directory inside the index.
func (e Entry) Dir() string {
	if e.Branch != "" {
		return path.Join(ReposDir, e.Repo, branchPrefix+e.Branch)
	}
	return path.Join(ReposDir, e.Repo)
}

// Files renders the entry as index-relative paths and contents: what
// `publish` writes for one repo.
func (e Entry) Files() map[string][]byte {
	dir := e.Dir()
	out := map[string][]byte{
		path.Join(dir, ledger.LedgerFile): e.Ledger.Bytes(),
		path.Join(dir, ledger.RefsFile):   e.Refs.Bytes(),
	}
	if len(e.Tests) > 0 {
		out[path.Join(dir, TestsFile)] = EncodeTests(e.Tests)
	}
	for hash, body := range e.Bodies {
		out[e.BodyPath(hash)] = []byte(body)
	}
	return out
}

// BodyPath is where one block body lives, index-relative.
func (e Entry) BodyPath(hash string) string {
	return path.Join(e.Dir(), BlocksDir, hash)
}

// testsColumns is tests.tsv's column line.
const testsColumns = "id\toutcome"

// EncodeTests renders test outcomes as TSV with a versioned header, sorted
// by id.
func EncodeTests(tests map[string]check.TestOutcome) []byte {
	ids := make([]string, 0, len(tests))
	for id := range tests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b bytes.Buffer
	fmt.Fprintf(&b, "# docsync tests format=%d\n%s\n", testsFormat, testsColumns)
	for _, id := range ids {
		// Escaped like every ledger column: a tab or newline in an id split
		// its row, and tests.tsv was the one TSV that did not escape.
		fmt.Fprintf(&b, "%s\t%s\n", ledger.EscapeField(id), tests[id])
	}
	return b.Bytes()
}

// DecodeTests reads tests.tsv.
func DecodeTests(r io.Reader) (map[string]check.TestOutcome, error) {
	sc := bufio.NewScanner(r)
	if !sc.Scan() || !strings.HasPrefix(sc.Text(), fmt.Sprintf("# docsync tests format=%d", testsFormat)) {
		return nil, ErrTestsFormat
	}
	out := map[string]check.TestOutcome{}
	for n := 2; sc.Scan(); n++ {
		line := sc.Text()
		// Line 2 is the column line, skipped only there and only exactly:
		// skipping any line that started "id\t" also dropped the outcome of
		// a block whose id was "id".
		if line == "" || (n == 2 && line == testsColumns) {
			continue
		}
		escID, outcome, ok := strings.Cut(line, "\t")
		id := ledger.UnescapeField(escID)
		if !ok {
			return nil, fmt.Errorf("%w: line %d", ErrTestsFormat, n)
		}
		switch check.TestOutcome(outcome) {
		case check.TestPassed, check.TestFailed, check.TestSkipped:
			out[id] = check.TestOutcome(outcome)
		default:
			return nil, fmt.Errorf("%w: line %d: outcome %q", ErrTestsFormat, n, outcome)
		}
	}
	return out, sc.Err()
}

// Read loads every published repo under an index tree. A repo directory
// with no ledger is skipped; a malformed file is an error naming it.
func Read(fsys fs.FS) ([]Entry, error) {
	dirs, err := fs.ReadDir(fsys, ReposDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var entries []Entry
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := path.Join(ReposDir, d.Name())
		e, ok, err := readEntry(fsys, dir, d.Name(), "")
		if err != nil {
			return nil, err
		}
		if ok {
			entries = append(entries, e)
		}
		subs, _ := fs.ReadDir(fsys, dir)
		for _, sub := range subs {
			if !sub.IsDir() || !strings.HasPrefix(sub.Name(), branchPrefix) {
				continue
			}
			be, ok, err := readEntry(fsys, path.Join(dir, sub.Name()), d.Name(), strings.TrimPrefix(sub.Name(), branchPrefix))
			if err != nil {
				return nil, err
			}
			if ok {
				entries = append(entries, be)
			}
		}
	}
	return entries, nil
}

// readEntry loads one repo (or branch) directory; ok is false without a
// ledger.
func readEntry(fsys fs.FS, dir, repo, branch string) (Entry, bool, error) {
	raw, err := fs.ReadFile(fsys, path.Join(dir, ledger.LedgerFile))
	if err != nil {
		return Entry{}, false, nil
	}
	e := Entry{Repo: repo, Branch: branch}
	if e.Ledger, err = ledger.DecodeLedger(bytes.NewReader(raw)); err != nil {
		return Entry{}, false, fmt.Errorf("%s: %w", dir, err)
	}
	if raw, err := fs.ReadFile(fsys, path.Join(dir, ledger.RefsFile)); err == nil {
		if e.Refs, err = ledger.DecodeRefs(bytes.NewReader(raw)); err != nil {
			return Entry{}, false, fmt.Errorf("%s: %w", dir, err)
		}
	}
	if raw, err := fs.ReadFile(fsys, path.Join(dir, TestsFile)); err == nil {
		if e.Tests, err = DecodeTests(bytes.NewReader(raw)); err != nil {
			return Entry{}, false, fmt.Errorf("%s: %w", dir, err)
		}
	}
	return e, true, nil
}

// junit is the subset of the JUnit XML report that names outcomes.
type junit struct {
	XMLName xml.Name     `xml:"testsuites"`
	Suites  []junitSuite `xml:"testsuite"`
	Cases   []junitCase  `xml:"testcase"`
}

type junitSuite struct {
	Name  string      `xml:"name,attr"`
	Cases []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string    `xml:"name,attr"`
	ClassName string    `xml:"classname,attr"`
	Failure   *struct{} `xml:"failure"`
	Error     *struct{} `xml:"error"`
	Skipped   *struct{} `xml:"skipped"`
}

// ParseJUnit reads a JUnit XML report (a `<testsuites>` root or a single
// `<testsuite>`) into outcomes keyed by test name.
func ParseJUnit(r io.Reader) (map[string]check.TestOutcome, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var cases []junitCase
	var root junit
	if err := xml.Unmarshal(raw, &root); err == nil {
		for _, s := range root.Suites {
			cases = append(cases, s.Cases...)
		}
		cases = append(cases, root.Cases...)
	} else {
		var single junitSuite
		if err2 := xml.Unmarshal(raw, &single); err2 != nil {
			return nil, err
		}
		cases = single.Cases
	}
	out := map[string]check.TestOutcome{}
	for _, c := range cases {
		switch {
		case c.Failure != nil, c.Error != nil:
			out[c.Name] = check.TestFailed
		case c.Skipped != nil:
			out[c.Name] = check.TestSkipped
		default:
			out[c.Name] = check.TestPassed
		}
	}
	return out, nil
}

// MatchTests maps JUnit outcomes onto defined test blocks by symbol: a def
// whose symbol is `TestSave` or `Suite.TestSave` takes the outcome of the
// testcase named `TestSave`. Defined tests with no result are recorded as
// skipped, which `assert=` treats as a failure (§9.2): a deleted or
// renamed test must not read as passing.
func MatchTests(defs []block.Block, byName map[string]check.TestOutcome) map[string]check.TestOutcome {
	out := map[string]check.TestOutcome{}
	for _, b := range defs {
		if b.Kind != block.KindFunc || !strings.HasPrefix(lastSegment(b.Symbol), "Test") {
			continue
		}
		if o, ok := byName[lastSegment(b.Symbol)]; ok {
			out[b.ID] = o
		} else {
			out[b.ID] = check.TestSkipped
		}
	}
	return out
}

func lastSegment(sym string) string {
	if i := strings.LastIndex(sym, "."); i >= 0 {
		return sym[i+1:]
	}
	return sym
}

// StaleAge reports how long ago a repo published, for the "index for api is
// N commits behind" style warning when commit distance is unknown.
func (m Merged) StaleAge(repo string, now time.Time) (time.Duration, error) {
	p, ok := m.Published[repo]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrNotPublished, repo)
	}
	return now.Sub(p.ScannedAt), nil
}
