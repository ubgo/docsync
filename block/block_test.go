package block

import (
	"reflect"
	"testing"

	"github.com/ubgo/docsync/internal/textnorm"
)

func TestParseStability(t *testing.T) {
	t.Parallel()
	for _, v := range StabilityValues {
		got, ok := ParseStability(string(v))
		if !ok || got != v {
			t.Errorf("ParseStability(%q) = %q,%v", v, got, ok)
		}
	}
	if _, ok := ParseStability("solid"); ok {
		t.Error("unknown stability accepted")
	}
	if _, ok := ParseStability(""); ok {
		t.Error("empty stability accepted")
	}
}

func TestFlags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		st      Stability
		classes []Class
		want    bool
	}{
		// whitespace and moved never flag
		{StabilityFrozen, []Class{ClassWhitespace}, false},
		{StabilityFrozen, []Class{ClassMoved}, false},
		{StabilityStable, []Class{ClassWhitespace, ClassMoved}, false},
		// comment only
		{StabilityFrozen, []Class{ClassComment}, true},
		{StabilityStable, []Class{ClassComment}, false},
		{StabilityAPI, []Class{ClassComment}, false},
		{StabilityVolatile, []Class{ClassComment}, false},
		// body only
		{StabilityFrozen, []Class{ClassBody}, true},
		{StabilityStable, []Class{ClassBody}, true},
		{StabilityAPI, []Class{ClassBody}, false},
		{StabilityVolatile, []Class{ClassBody}, false},
		// api-relevant classes
		{StabilityAPI, []Class{ClassSignature}, true},
		{StabilityAPI, []Class{ClassType}, true},
		{StabilityAPI, []Class{ClassRenamed}, true},
		{StabilityAPI, []Class{ClassValue}, true},
		{StabilityStable, []Class{ClassValue}, true},
		{StabilityFrozen, []Class{ClassSignature}, true},
		{StabilityVolatile, []Class{ClassSignature, ClassBody, ClassValue}, false},
		// mixed: any flagging class wins
		{StabilityAPI, []Class{ClassBody, ClassSignature}, true},
		{StabilityAPI, []Class{ClassMoved, ClassBody}, false},
		// unknown: wider than body on purpose, because an unclassified
		// difference may be a signature change and `api` exists to catch
		// those; only volatile opts out.
		{StabilityFrozen, []Class{ClassUnknown}, true},
		{StabilityStable, []Class{ClassUnknown}, true},
		{StabilityAPI, []Class{ClassUnknown}, true},
		{StabilityVolatile, []Class{ClassUnknown}, false},
		{StabilityAPI, []Class{ClassMoved, ClassUnknown}, true},
		// empty
		{StabilityFrozen, nil, false},
	} {
		if got := Flags(tc.st, tc.classes); got != tc.want {
			t.Errorf("Flags(%s, %v) = %v, want %v", tc.st, tc.classes, got, tc.want)
		}
	}
}

func TestValuesSlicesComplete(t *testing.T) {
	t.Parallel()
	if len(KindValues) != 12 || len(CarrierValues) != 4 || len(StabilityValues) != 4 || len(ClassValues) != 9 || len(KeyValues) != 20 {
		t.Errorf("a *Values slice is out of sync with its constants: %d %d %d %d %d", len(KindValues), len(CarrierValues), len(StabilityValues), len(ClassValues), len(KeyValues))
	}
	seen := map[string]bool{}
	for _, k := range KeyValues {
		if seen[k] {
			t.Errorf("duplicate key %q", k)
		}
		seen[k] = true
	}
}

func TestBlockAccessors(t *testing.T) {
	t.Parallel()
	b := Block{Args: map[string]string{
		KeyStability: "api", KeyOwner: "@auth", KeyEnv: "prod", KeyFrom: "x-aaaaaaaa",
		KeySecret: "true", KeyTruth: "true", KeyLocal: "yes", KeyRunnable: "TRUE", KeyTags: "a, b,,c",
	}}
	if b.Stability() != StabilityAPI || b.Owner() != "@auth" || b.Env() != "prod" || b.From() != "x-aaaaaaaa" {
		t.Errorf("accessors: %#v", b)
	}
	if !b.IsSecret() || !b.IsTruth() {
		t.Error("secret/truth should be true")
	}
	if b.IsLocal() || b.IsRunnable() {
		t.Error("only the exact spelling `true` is true; `yes` and `TRUE` are not")
	}
	if got := b.Tags(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("Tags = %v", got)
	}
	var empty Block
	if empty.Stability() != DefaultStability {
		t.Errorf("default stability = %q", empty.Stability())
	}
	if empty.Tags() != nil || empty.Owner() != "" {
		t.Error("empty block accessors")
	}
	bad := Block{Args: map[string]string{KeyStability: "solid"}}
	if bad.Stability() != DefaultStability {
		t.Error("invalid stability must fall back to default")
	}
}

func TestSetContent(t *testing.T) {
	t.Parallel()
	var b Block
	b.SetContent("func f() {}\r\n")
	if b.Content != "func f() {}\r\n" {
		t.Error("Content must be stored verbatim")
	}
	if b.Hash != textnorm.Hash([]byte("func f() {}")) {
		t.Error("Hash must be over normalized content")
	}
}

func TestReference(t *testing.T) {
	t.Parallel()
	r := Reference{Verb: "block", ID: "a-bbbbbbbb", Args: map[string]string{"lines": "1-6"}}
	r.SetSentence("The guard is here.")
	if r.Sentence == "" || r.SentenceHash != textnorm.Hash([]byte("The guard is here.")) {
		t.Errorf("SetSentence: %#v", r)
	}
	r.SetSentence("")
	if r.Sentence != "" || r.SentenceHash != "" {
		t.Errorf("clearing sentence must clear hash: %#v", r)
	}
	d := r.Directive()
	if d.Verb != "block" || d.Args["lines"] != "1-6" {
		t.Errorf("Directive() = %#v", d)
	}
}

func TestSetContentHashed(t *testing.T) {
	t.Parallel()
	var a, b, c Block
	a.SetContent("x = 1")
	b.SetContentHashed("x  =  1 // note", "x = 1")
	c.SetContentHashed("x = 1", "x = 1")
	if a.Hash != b.Hash || b.Content != "x  =  1 // note" || c.Hash != a.Hash {
		t.Errorf("hashed = %+v %+v", a, b)
	}
}

// TestSecretAddress pins the line between a secret's address, which the spec
// calls safe to render, and its value, which the tool never shows (§12). A
// false positive here prints a credential, so the negatives matter as much as
// the positives -- above all an AWS access key id, which is uppercase like an
// env var name and must never pass for one.
func TestSecretAddress(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]bool{
		"${{ secrets.STRIPE_KEY }}":                   true,
		"STRIPE_KEY: ${{ secrets.STRIPE_KEY }}":       true,
		"${{secrets.X}}":                              true,
		"op://Platform/stripe-prod/credential":        true,
		"STRIPE_KEY=op://Platform/stripe/credential":  true,
		"arn:aws:secretsmanager:us-east-1:1:secret:x": true,
		"projects/p1/secrets/stripe/versions/latest":  true,
		"vault:secret/data/stripe#key":                true,
		"sk-live-OLDSECRET":                           false,
		"sk_live_51H8abc":                             false,
		"AKIAIOSFODNN7EXAMPLE":                        false,
		"STRIPE_KEY":                                  false,
		"hunter2":                                     false,
		"password: hunter2  # see secrets.md":         false,
		"https://example.com/op://not-a-ref-at-start": true,
		"": false,
	} {
		if got := SecretAddress(s); got != want {
			t.Errorf("SecretAddress(%q) = %v, want %v", s, got, want)
		}
	}
}
