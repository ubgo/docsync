package id

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		c       Config
		wantErr error
	}{
		{"default ok", Default(), nil},
		{"min length ok", Config{Alphabet: DefaultAlphabet, SuffixLength: minSuffixLength}, nil},
		{"max length ok", Config{Alphabet: DefaultAlphabet, SuffixLength: maxSuffixLength}, nil},
		{"too short", Config{Alphabet: DefaultAlphabet, SuffixLength: 5}, ErrBadLength},
		{"too long", Config{Alphabet: DefaultAlphabet, SuffixLength: 33}, ErrBadLength},
		{"zero value", Config{}, ErrBadLength},
		{"alphabet too small", Config{Alphabet: "abcdefghijklmno", SuffixLength: 8}, ErrBadAlphabet},
		{"alphabet uppercase", Config{Alphabet: "ABCDEFGHIJKLMNOPQRSTUVWXYZ012345", SuffixLength: 8}, ErrBadAlphabet},
		{"alphabet repeat", Config{Alphabet: "aabcdefghijklmnopqrstuvwxyz01234", SuffixLength: 8}, ErrBadAlphabet},
		{"alphabet punctuation", Config{Alphabet: "abcdefghijklmnopqrstuvwxyz0123-_", SuffixLength: 8}, ErrBadAlphabet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.c.Validate()
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestMint(t *testing.T) {
	t.Parallel()
	c := Default()
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		s, err := Mint(c)
		if err != nil {
			t.Fatal(err)
		}
		if len(s) != c.SuffixLength {
			t.Fatalf("len = %d", len(s))
		}
		for j := 0; j < len(s); j++ {
			if strings.IndexByte(c.Alphabet, s[j]) < 0 {
				t.Fatalf("char %q outside alphabet", s[j])
			}
		}
		if seen[s] {
			t.Fatalf("collision after %d mints: %s", i, s)
		}
		seen[s] = true
	}
}

func TestMintDistributionCoversAlphabet(t *testing.T) {
	t.Parallel()
	// Rejection sampling must be able to produce every symbol; with 2000 mints
	// of 8 chars the chance a symbol never appears is negligible.
	c := Default()
	counts := map[byte]int{}
	for i := 0; i < 2000; i++ {
		s, err := Mint(c)
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < len(s); j++ {
			counts[s[j]]++
		}
	}
	for i := 0; i < len(c.Alphabet); i++ {
		if counts[c.Alphabet[i]] == 0 {
			t.Errorf("symbol %q never produced", c.Alphabet[i])
		}
	}
}

func TestMintInvalidConfig(t *testing.T) {
	t.Parallel()
	if _, err := Mint(Config{}); !errors.Is(err, ErrBadLength) {
		t.Errorf("err = %v", err)
	}
}

// failingReader returns an error on every read so the entropy path is covered.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// rejectAllThenGood returns bytes >= limit first (all rejected) to prove the
// sampler loops rather than truncating, then good bytes.
type rejectAllThenGood struct{ calls int }

func (r *rejectAllThenGood) Read(p []byte) (int, error) {
	r.calls++
	for i := range p {
		if r.calls == 1 {
			p[i] = 255 // >= limit for a 32-symbol alphabet (limit is 256)
			// 256%32==0 so limit==256 and 255 is accepted; use a 31-symbol
			// alphabet in the test to make 255 rejectable.
		} else {
			p[i] = byte(i % 31)
		}
	}
	return len(p), nil
}

func TestMintEntropyPaths(t *testing.T) {
	// Not parallel: swaps the package reader.
	orig := reader
	t.Cleanup(func() { reader = orig })

	reader = failingReader{}
	if _, err := Mint(Default()); !errors.Is(err, ErrEntropySource) {
		t.Fatalf("err = %v, want ErrEntropySource", err)
	}

	// 31-symbol alphabet: limit = 256 - 256%31 = 248, so 255 is rejected and
	// the sampler must read again.
	c := Config{Alphabet: "23456789abcdefghjkmnpqrstuvwxy", SuffixLength: 8}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	r := &rejectAllThenGood{}
	reader = r
	s, err := Mint(c)
	if err != nil {
		t.Fatal(err)
	}
	if r.calls < 2 {
		t.Fatalf("sampler accepted rejected bytes; calls=%d", r.calls)
	}
	if s != "23456789" {
		t.Fatalf("deterministic mint = %q", s)
	}
}

func TestNewSplitValidSame(t *testing.T) {
	t.Parallel()
	c := Default()
	full, err := New(c, "Store.SaveSession")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(full, "store-savesession-") {
		t.Fatalf("New = %q", full)
	}
	p, s, err := Split(c, full)
	if err != nil || p != "store-savesession" || len(s) != 8 {
		t.Fatalf("Split(%q) = %q,%q,%v", full, p, s, err)
	}
	if !Valid(c, full) {
		t.Errorf("Valid(%q) = false", full)
	}
	relabelled := "session-persist-" + s
	if !Same(c, full, relabelled) {
		t.Errorf("Same must ignore prefix: %q vs %q", full, relabelled)
	}
	other, _ := New(c, "x")
	if Same(c, full, other) {
		t.Errorf("distinct mints reported Same")
	}
	if Same(c, full, "bad") || Same(c, "bad", "bad") {
		t.Errorf("invalid ids must never be Same")
	}
}

func TestNewErrors(t *testing.T) {
	t.Parallel()
	if _, err := New(Default(), "!!!"); !errors.Is(err, ErrBadPrefix) {
		t.Errorf("empty slug err = %v", err)
	}
	if _, err := New(Config{}, "ok"); !errors.Is(err, ErrBadLength) {
		t.Errorf("bad config err = %v", err)
	}
}

func TestSplitErrors(t *testing.T) {
	t.Parallel()
	c := Default()
	for _, tc := range []struct {
		in      string
		wantErr error
	}{
		{"", ErrEmpty},
		{"nodash", ErrNoSeparator},
		{"-k7m2p4xq", ErrNoSeparator},
		{"prefix-", ErrNoSeparator},
		{"Bad-k7m2p4xq", ErrBadPrefix},
		{"a--b-k7m2p4xq", ErrBadPrefix},
		{"sess-save-k7m2", ErrBadSuffix},
		{"sess-save-k7m2p4x0", ErrBadSuffix},
		{"sess-save-K7M2P4XQ", ErrBadSuffix},
	} {
		if _, _, err := Split(c, tc.in); !errors.Is(err, tc.wantErr) {
			t.Errorf("Split(%q) err = %v, want %v", tc.in, err, tc.wantErr)
		}
	}
	if _, _, err := Split(Config{}, "a-bcdefghi"); !errors.Is(err, ErrBadLength) {
		t.Errorf("invalid config err = %v", err)
	}
}

func TestSlug(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"Store.SaveSession", "store-savesession"},
		{"Session policy", "session-policy"},
		{"  --Hello,   World!! ", "hello-world"},
		{"already-a-slug", "already-a-slug"},
		{"ünïcödé läuft", "n-c-d-l-uft"},
		{"", ""},
		{"!!!", ""},
		{"a", "a"},
		{"CamelCase123", "camelcase123"},
	} {
		if got := Slug(tc.in); got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDerive pins what a dry run relies on (bug 33): one seed, one suffix,
// every time, valid under the config; another seed, another suffix; and the
// rejection sampling reaches past the first hash when a short alphabet
// rejects many bytes.
func TestDerive(t *testing.T) {
	t.Parallel()
	c := Default()
	a1, err := Derive(c, []byte("repo\x00a.go\x003"))
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := Derive(c, []byte("repo\x00a.go\x003"))
	b, _ := Derive(c, []byte("repo\x00a.go\x004"))
	if a1 != a2 || a1 == b || len(a1) != c.SuffixLength || !allIn(a1, c.Alphabet) {
		t.Errorf("Derive = %q %q %q", a1, a2, b)
	}
	long := Config{Alphabet: "abcdefghijklmnopq", SuffixLength: maxSuffixLength}
	if s, err := Derive(long, []byte("x")); err != nil || len(s) != maxSuffixLength || !allIn(s, long.Alphabet) {
		t.Errorf("long Derive = %q %v", s, err)
	}
	if _, err := Derive(Config{}, nil); !errors.Is(err, ErrBadLength) {
		t.Errorf("invalid config = %v", err)
	}
	full, err := NewDerived(c, "Store.Save", []byte("s"))
	if p, s, _ := Split(c, full); err != nil || p != "store-save" || s != mustDerive(t, c, "s") {
		t.Errorf("NewDerived = %q %v", full, err)
	}
	if _, err := NewDerived(c, "!!", nil); !errors.Is(err, ErrBadPrefix) {
		t.Errorf("empty prefix = %v", err)
	}
	if _, err := NewDerived(Config{}, "x", nil); !errors.Is(err, ErrBadLength) {
		t.Errorf("invalid config = %v", err)
	}
}

func mustDerive(t *testing.T, c Config, seed string) string {
	t.Helper()
	s, err := Derive(c, []byte(seed))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
