package docsync

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/ledger"
)

func ruleHeader(kind string, rule int) ledger.Header {
	return ledger.Header{Kind: kind, Format: ledger.Format, Extract: rule}
}

// TestNewerExtractionRuleIsRefused pins §33's "an older tool refuses a newer
// format by name, never by misparsing" for the one version a format number
// does not cover. Hashes taken under a rule this build does not implement
// cannot be compared with its own: accepting them would report unchanged
// blocks as drift and let a scan rewrite the files under the older rule. The
// refusal names the file, both rules, and the fix for a pre-release repo.
// promise:extract-rule
func TestNewerExtractionRuleIsRefused(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"docs/a.md": {Data: []byte("x\n")}}
	newer := extract.Rule + 1
	cur := func(kind string) ledger.Header { return ruleHeader(kind, extract.Rule) }
	cases := map[string]Option{
		"ledger": WithPrevious(ledger.Ledger{Header: ruleHeader(ledger.KindLedger, newer)}, ledger.Refs{Header: cur(ledger.KindRefs)}),
		"refs":   WithPrevious(ledger.Ledger{Header: cur(ledger.KindLedger)}, ledger.Refs{Header: ruleHeader(ledger.KindRefs, newer)}),
	}
	for name, opt := range cases {
		_, err := New(WithFS(fsys), opt)
		if !errors.Is(err, ErrNewerRule) {
			t.Errorf("%s under rule %d: err = %v, want ErrNewerRule", name, newer, err)
			continue
		}
		for _, want := range []string{name + ".tsv says extract=" + strconv.Itoa(newer), "this build implements rule " + strconv.Itoa(extract.Rule), "set extract=1"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %q does not say %q", name, err, want)
			}
		}
	}
	// The current rule, a header that predates the field (0 reads as 1), and
	// a first run are accepted.
	for _, rule := range []int{extract.Rule, 0} {
		if _, err := New(WithFS(fsys), WithPrevious(ledger.Ledger{Header: ruleHeader(ledger.KindLedger, rule)}, ledger.Refs{Header: ruleHeader(ledger.KindRefs, rule)})); err != nil {
			t.Errorf("rule %d refused: %v", rule, err)
		}
	}
	if err := CheckRule(ledger.Ledger{}, ledger.Refs{}); err != nil {
		t.Errorf("first run: %v", err)
	}
}

// TestOlderExtractionRuleWarns pins the upgrade path: a file from an older rule
// is named with ErrOtherRule, which hosts print as a warning, and is never
// refused, since the scan it would block is what restamps it. Run against a
// current rule of 3, because while rule 1 is the only released one no real
// file can be older.
// promise:extract-rule
func TestOlderExtractionRuleWarns(t *testing.T) {
	t.Parallel()
	err := checkRule(ledger.Ledger{Header: ruleHeader(ledger.KindLedger, 3)}, ledger.Refs{Header: ruleHeader(ledger.KindRefs, 2)}, 3)
	if !errors.Is(err, ErrOtherRule) || !strings.Contains(err.Error(), "refs.tsv says extract=2, this build implements rule 3") {
		t.Errorf("older refs: %v", err)
	}
	// A newer file anywhere wins over an older one: the refusal is the finding.
	err = checkRule(ledger.Ledger{Header: ruleHeader(ledger.KindLedger, 2)}, ledger.Refs{Header: ruleHeader(ledger.KindRefs, 4)}, 3)
	if !errors.Is(err, ErrNewerRule) {
		t.Errorf("older ledger, newer refs: %v, want ErrNewerRule", err)
	}
	// Two older files report the first.
	err = checkRule(ledger.Ledger{Header: ruleHeader(ledger.KindLedger, 1)}, ledger.Refs{Header: ruleHeader(ledger.KindRefs, 2)}, 3)
	if !errors.Is(err, ErrOtherRule) || !strings.Contains(err.Error(), "ledger.tsv") {
		t.Errorf("both older: %v", err)
	}
}

// TestAckRowsUnderAnotherRuleAreAccepted pins that the ack log is not refused:
// its header is never restamped, and a row under another rule is reported by
// name by check (the ack-under-another-* conformance fixtures).
// promise:extract-rule
func TestAckRowsUnderAnotherRuleAreAccepted(t *testing.T) {
	t.Parallel()
	newer := extract.Rule + 1
	acks := ledger.Acks{Header: ruleHeader(ledger.KindAcks, newer), Rows: []ledger.Ack{{ID: "x-k7m2p4xq", Doc: "docs/a.md", Line: 3, Rule: newer}}}
	if _, err := New(WithFS(fstest.MapFS{"docs/a.md": {Data: []byte("x\n")}}), WithAcks(acks)); err != nil {
		t.Errorf("acks under rule %d refused: %v", newer, err)
	}
}
