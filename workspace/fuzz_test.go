package workspace

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/ubgo/docsync/check"
)

// FuzzTestsRoundTrip: whatever ids a repo publishes test outcomes for,
// tests.tsv reads back the same outcomes. An id holding a tab or a newline
// split its row, and one starting "id\t" was skipped as the column line.
func FuzzTestsRoundTrip(f *testing.F) {
	for _, s := range []string{"a-k7m2p4xq", "tab\there", "nl\nhere", "id\tlike-the-header", `back\slash`, "", " "} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, id string) {
		in := map[string]check.TestOutcome{id: check.TestPassed, "other-h3v8n2wd": check.TestFailed}
		got, err := DecodeTests(bytes.NewReader(EncodeTests(in)))
		if err != nil {
			t.Fatalf("id %q: %v", id, err)
		}
		if !reflect.DeepEqual(got, in) {
			t.Fatalf("round trip lost an outcome:\n got %v\nwant %v", got, in)
		}
		// tests.tsv is published into the index repo and may be checked out
		// with CRLF endings; ids are escaped, so every \n is a line end.
		crlf, err := DecodeTests(bytes.NewReader(bytes.ReplaceAll(EncodeTests(in), []byte("\n"), []byte("\r\n"))))
		if err != nil || !reflect.DeepEqual(crlf, in) {
			t.Fatalf("CRLF endings changed what was read: %v %v", crlf, err)
		}
	})
}

// FuzzParseJUnit: no input panics the JUnit reader, which parses whatever a
// test runner wrote.
func FuzzParseJUnit(f *testing.F) {
	for _, s := range []string{`<testsuite><testcase name="TestA"/></testsuite>`, `<testsuites><testsuite><testcase name="TestB"><failure/></testcase></testsuite></testsuites>`, `<`, ``, `<testcase name="x"><skipped/></testcase>`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = ParseJUnit(strings.NewReader(s))
	})
}

// TestTestsIDNamedID pins the header-skip fix directly: a block whose id is
// the column name keeps its outcome.
func TestTestsIDNamedID(t *testing.T) {
	t.Parallel()
	in := map[string]check.TestOutcome{"id": check.TestFailed}
	got, err := DecodeTests(bytes.NewReader(EncodeTests(in)))
	if err != nil || got["id"] != check.TestFailed {
		t.Errorf("DecodeTests = %v, %v", got, err)
	}
}
