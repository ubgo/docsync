package docsync

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// bugRefRE reads the forms this repository uses to say which bug a test pins:
// "bug 3", "(bug 18)", and "bugs 4, 6 and 7". The numbers are docsync's own bug
// numbers, not GitHub issue numbers: the original reports are archived
// privately, and a "#18" would link to whatever issue 18 of the public
// repository happens to be. A bare "#7" or "7" is not a reference -- a
// pull-request number in a fixture looks the same and would count as proof
// of nothing.
var bugRefRE = regexp.MustCompile(`\bbugs? (\d+(?:(?:,\s*|\s+and\s+)\d+)*)`)

// fixedBugsFile lists the bugs that are fixed; see its header.
const fixedBugsFile = "testdata/fixed-bugs.txt"

// TestEveryFixedBugIsPinned makes "is every fixed bug still covered?" a
// check that runs on every change, rather than an audit someone has to
// remember. Each bug in fixed-bugs.txt must be referenced by a test or an
// e2e script that proves the fix; one that is not has nothing standing between
// it and a quiet regression.
//
// It was written after exactly that audit, done by hand, found eight fixed
// bugs with no test naming them. The tests existed; the link between a bug
// and the test that proves it did not, so nobody could tell a covered fix from
// an uncovered one without reading every test.
func TestEveryFixedBugIsPinned(t *testing.T) {
	t.Parallel()
	fixed := readFixedBugs(t)
	if len(fixed) == 0 {
		t.Fatal("no fixed bugs listed: this check would pass on nothing")
	}
	pinned := map[int][]string{}
	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", "bin", "coverage":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") && !(strings.HasPrefix(p, "scripts"+string(filepath.Separator)) && strings.HasSuffix(p, ".sh")) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, n := range bugRefs(string(b)) {
			pinned[n] = append(pinned[n], p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for n, title := range fixed {
		if len(pinned[n]) == 0 {
			missing = append(missing, "bug "+strconv.Itoa(n)+" "+title)
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("fixed bug has no test referencing it: %s", m)
	}
}

// bugRefs returns every bug number referenced in s in a recognised form.
func bugRefs(s string) []int {
	var out []int
	for _, m := range bugRefRE.FindAllString(s, -1) {
		for _, d := range regexp.MustCompile(`\d+`).FindAllStringSubmatch(m, -1) {
			n, _ := strconv.Atoi(d[0])
			out = append(out, n)
		}
	}
	return out
}

// readFixedBugs parses fixed-bugs.txt: a number, then its title.
func readFixedBugs(t *testing.T) map[int]string {
	t.Helper()
	f, err := os.Open(fixedBugsFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[int]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		num, title, _ := strings.Cut(line, " ")
		n, err := strconv.Atoi(num)
		if err != nil {
			t.Fatalf("%s: %q does not start with a bug number", fixedBugsFile, line)
		}
		out[n] = strings.TrimSpace(title)
	}
	// A read error part-way through would otherwise return a short list, and
	// the gate would pass for every bug it never read.
	if err := sc.Err(); err != nil {
		t.Fatalf("%s: %v", fixedBugsFile, err)
	}
	return out
}

// TestBugRefs pins the reference forms, including the one it must not
// accept: a bare number, which a fixture's pull-request number also is.
func TestBugRefs(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]string{
		"// the fix for this (bug 18).":          "18",
		"# ds undo: listing (bug 3)":             "3",
		"# prune (bugs 4, 6 and 7)":              "4,6,7",
		"// Pins bugs 10, 11.":                   "10,11",
		"// bugs 1 and 8":                        "1,8",
		`{"number":7}`:                           "",
		"see PR #7 in the fixture":               "",
		"a debug 7 build":                        "",
		"every one of the first twelve bugs was": "",
	} {
		var got []string
		for _, n := range bugRefs(s) {
			got = append(got, strconv.Itoa(n))
		}
		if strings.Join(got, ",") != want {
			t.Errorf("bugRefs(%q) = %v, want %s", s, got, want)
		}
	}
}
