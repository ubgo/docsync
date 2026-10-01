package config

import (
	"strings"
	"testing"
)

// FuzzParse: no input panics the config parser, and a config it accepts
// passes its own validation — a value that parses but that Validate would
// refuse must be refused at parse time, not discovered by a later command.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"", "spec = \"1.0\"\nprefix = \"ds\"\n", "[scan]\ncode = [\"**\"]\n[scan.limits]\nmax_file_kb = 1\n", "prefix = \"Bad Prefix\"", "[[x]]\n", "a = [1, \"b\"]\n", "k = \"unterminated\n", "[scan.limits]\nmax_line_chars = -1\n"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c, err := Parse(strings.NewReader(s))
		if err != nil {
			return
		}
		if verr := c.Validate(); verr != nil {
			t.Fatalf("Parse accepted a config Validate refuses: %v\n%q", verr, s)
		}
	})
}

// TestParseOntoLeavesTheBaseAlone pins that reading a file onto a base never
// writes into the base's maps. An organisation hook that shares one map
// between calls would otherwise carry one repository's owners or
// run environment into the next repository's config.
func TestParseOntoLeavesTheBaseAlone(t *testing.T) {
	t.Parallel()
	base := Default()
	base.Owners["@org"] = []string{"alice"}
	base.Run.Env["staging"] = map[string]string{"A": "1"}
	file := "[scan]\ncode = [\"**\"]\n[owners]\n\"@repo\" = [\"bob\"]\n[run.env.staging]\nB = \"2\"\n"
	got, err := ParseOnto(base, strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Owners["@org"]; !ok || len(got.Owners["@repo"]) != 1 {
		t.Errorf("result owners = %v, want the base's and the file's", got.Owners)
	}
	if _, ok := base.Owners["@repo"]; ok {
		t.Error("the file's owners leaked into the base")
	}
	if _, ok := base.Run.Env["staging"]["B"]; ok {
		t.Error("the file's run env leaked into the base's inner map")
	}
}
