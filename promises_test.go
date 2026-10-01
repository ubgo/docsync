package docsync

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// promisesFile lists the spec's absolute promises; see its header.
const promisesFile = "testdata/promises.txt"

// specFiles are the documents whose promises are gated: the normative spec,
// and the README, which makes promises of its own to everyone who reads the
// repository and is the first thing a user takes at its word.
var specFiles = []string{"docs/SPEC.md", "README.md"}

// promiseKindTest marks a promise a test must pin. Any other kind starts with
// promiseKindProse and carries the reason it is not a checkable property.
const (
	promiseKindTest  = "test"
	promiseKindProse = "prose: "
)

// promiseWordRE finds the words that make a sentence an absolute promise. A
// sentence using one is either listed in promises.txt or the gate fails: that
// is what stops a new "never" from reaching the spec without a test.
var promiseWordRE = regexp.MustCompile(`(?i)\b(never|always|refuses?|refused|must not|deterministic|hard error)\b`)

// promiseRefRE is how a test says which promise it pins: `promise:<id>`.
var promiseRefRE = regexp.MustCompile(`promise:([a-z0-9-]+)`)

// sentenceEndRE ends a sentence at . ! or ? followed by whitespace or the end
// of the line. The spec is never hard-wrapped, so a line is a paragraph, a
// bullet or a table row, and no sentence spans two lines.
var sentenceEndRE = regexp.MustCompile(`[.!?](\s+|$)`)

type promise struct {
	id, kind, quote string
}

// TestSpecPromisesArePinned makes "is every promise in the spec tested?" a
// check that runs on every change. It was written after a sweep of the spec's
// absolute sentences, run by hand against the built binary, found five that
// no test checked and that did not hold: the fork guard failed open, doctor
// exited 0 on a failure, a newer extraction rule was read silently, whitespace
// was claimed never to be reported where a reindent was, and generated paths
// accepted a minted def. Each had been written as a promise and never pinned.
func TestSpecPromisesArePinned(t *testing.T) {
	t.Parallel()
	promises := readPromises(t)
	texts := map[string]string{}
	all := ""
	for _, f := range specFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		texts[f] = string(b)
		all += string(b) + "\n"
	}

	// Every listed quote still reads verbatim in one of the documents.
	for _, p := range promises {
		if !strings.Contains(all, p.quote) {
			t.Errorf("%s: %q no longer appears in %v; reword the list in the same change as the document", p.id, p.quote, specFiles)
		}
	}

	// Every promise sentence is covered by a listed quote.
	for _, f := range specFiles {
		checkCovered(t, f, texts[f], promises)
	}

	// Every `test` promise is pinned by a test that names it.
	pinned := promiseRefsInTests(t)
	seen := map[string]bool{}
	var missing []string
	for _, p := range promises {
		if p.kind != promiseKindTest || seen[p.id] {
			continue
		}
		seen[p.id] = true
		if !pinned[p.id] {
			missing = append(missing, p.id)
		}
	}
	sort.Strings(missing)
	for _, id := range missing {
		t.Errorf("promise %q has no test referencing promise:%s", id, id)
	}
	// And every reference names a listed promise, so a typo cannot pin
	// nothing while looking like it pins something.
	for id := range pinned {
		if !listed(promises, id) {
			t.Errorf("a test references promise:%s, which %s does not list", id, promisesFile)
		}
	}
}

// checkCovered fails for every promise sentence in the document at path
// that contains none of the listed quotes.
func checkCovered(t *testing.T, path, text string, promises []promise) {
	t.Helper()
	for _, s := range promiseSentences(text) {
		covered := false
		for _, p := range promises {
			if strings.Contains(s.text, p.quote) {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("%s:%d states a promise that is not in %s: %q", path, s.line, promisesFile, s.text)
		}
	}
}

func listed(promises []promise, id string) bool {
	for _, p := range promises {
		if p.id == id {
			return true
		}
	}
	return false
}

type specSentence struct {
	line int
	text string
}

// promiseSentences returns every sentence in md, outside fenced code, that
// uses a promise word.
func promiseSentences(md string) []specSentence {
	var out []specSentence
	fence := false
	for i, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		start := 0
		ends := append(sentenceEndRE.FindAllStringIndex(line, -1), []int{len(line), len(line)})
		for _, end := range ends {
			s := strings.TrimSpace(line[start:end[1]])
			start = end[1]
			if s != "" && promiseWordRE.MatchString(s) {
				out = append(out, specSentence{line: i + 1, text: s})
			}
		}
	}
	return out
}

// promiseRefsInTests collects the promise ids named by `promise:<id>` in any
// Go test or shell script under the repository, across every module.
func promiseRefsInTests(t *testing.T) map[string]bool {
	t.Helper()
	refs := map[string]bool{}
	err := filepath.WalkDir(".", func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if otherCheckout(p) {
				return filepath.SkipDir
			}
			switch d.Name() {
			case ".git", "node_modules", "testdata", "bin", "coverage":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") && !(strings.HasPrefix(p, "scripts"+string(filepath.Separator)) && strings.HasSuffix(p, ".sh")) {
			return nil
		}
		// This file names the syntax in its own comments; it pins nothing.
		if p == "promises_test.go" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range promiseRefRE.FindAllStringSubmatch(string(b), -1) {
			refs[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return refs
}

// readPromises parses promises.txt: id, kind, quote, tab-separated.
func readPromises(t *testing.T) []promise {
	t.Helper()
	f, err := os.Open(promisesFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []promise
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] == "" || fields[2] == "" {
			t.Fatalf("%s:%d: want id<TAB>kind<TAB>quote, got %q", promisesFile, n, line)
		}
		kind := fields[1]
		if kind != promiseKindTest && (!strings.HasPrefix(kind, promiseKindProse) || len(kind) == len(promiseKindProse)) {
			t.Fatalf("%s:%d: kind %q is neither %q nor %q followed by a reason", promisesFile, n, kind, promiseKindTest, promiseKindProse)
		}
		out = append(out, promise{id: fields[0], kind: kind, quote: fields[2]})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("%s: %v", promisesFile, err)
	}
	// A list that read as empty would pass every check on nothing.
	if len(out) == 0 {
		t.Fatalf("%s lists no promises", promisesFile)
	}
	return out
}
