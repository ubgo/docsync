package cli

import (
	"encoding/json"
	"runtime/debug"
	"strings"
	"testing"
)

// TestBuildInfo pins what `ds version` reports, including the case it exists
// for: a build from a checkout with uncommitted changes must say so, because a
// commit hash alone would then describe code that is not what is running.
func TestBuildInfo(t *testing.T) {
	t.Parallel()
	full := func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			GoVersion: "go1.27.1",
			Main:      debug.Module{Version: "v1.2.3"},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "174b43f7fa32e3874894838fd8481dbd687ee2e4"},
				{Key: "vcs.modified", Value: "true"},
				{Key: "vcs.time", Value: "2026-09-29T14:38:46Z"},
				{Key: "GOOS", Value: "darwin"},
			},
		}, true
	}
	b := buildInfo(full)
	if b.Version != "v1.2.3" || b.Commit != "174b43f7fa32e3874894838fd8481dbd687ee2e4" || !b.Dirty || b.Time != "2026-09-29T14:38:46Z" || b.Go != "go1.27.1" {
		t.Errorf("buildInfo = %+v", b)
	}
	s := b.String()
	if !strings.Contains(s, "174b43f7fa32") || strings.Contains(s, "174b43f7fa32e") {
		t.Errorf("the line must carry a 12-character commit: %q", s)
	}
	if !strings.Contains(s, "dirty") {
		t.Errorf("a dirty build must say so: %q", s)
	}
	// A clean build says nothing about dirt.
	clean := func() (*debug.BuildInfo, bool) {
		bi, _ := full()
		bi.Settings[1].Value = "false"
		return bi, true
	}
	if b := buildInfo(clean); b.Dirty || strings.Contains(b.String(), "dirty") {
		t.Errorf("a clean build must not claim dirt: %+v", b)
	}
	// A binary with no embedded information -- a test binary, or one built
	// outside a checkout -- says "unknown" for each field rather than leaving it
	// blank, which would read as "empty" when the truth is "not recorded".
	for _, read := range []func() (*debug.BuildInfo, bool){
		func() (*debug.BuildInfo, bool) { return nil, false },
		func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{}, true },
	} {
		b := buildInfo(read)
		if b.Version != unknownBuild || b.Commit != unknownBuild || b.Time != unknownBuild {
			t.Errorf("missing fields must read %q: %+v", unknownBuild, b)
		}
	}
	// A short commit is not padded or cut.
	if s := (BuildInfo{Version: "v", Commit: "abc", Time: "t", Go: "g"}).String(); !strings.Contains(s, "commit abc,") {
		t.Errorf("short commit = %q", s)
	}
}

// TestVersionCommand covers both spellings and the JSON form.
func TestVersionCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, args := range [][]string{{"version"}, {"--version"}} {
		r := run(t, dir, fakeVCS{}, args...)
		// The program is named once, not twice.
		if r.code != 0 || !strings.HasPrefix(r.out, "ds ") || strings.HasPrefix(r.out, "ds version ds") {
			t.Errorf("%v = %+v", args, r)
		}
	}
	r := run(t, dir, fakeVCS{}, "version", "--json")
	var b BuildInfo
	if err := json.Unmarshal([]byte(r.out), &b); err != nil || r.code != 0 || b.Commit == "" || b.Go == "" {
		t.Errorf("version --json = %+v (%v)", r, err)
	}
}

// TestReleaseVersionWins pins that a stamped release version replaces the
// toolchain's: a release binary is built from a checkout, where the toolchain
// records only a pseudo-version, and `ds version` must name the release the
// user downloaded. Unstamped, the toolchain's version stands.
func TestReleaseVersionWins(t *testing.T) {
	t.Parallel()
	dev := BuildInfo{Version: "v0.0.0-20260930-abcdef", Commit: "abc"}
	if got := stamped(dev, "v0.1.0"); got.Version != "v0.1.0" || got.Commit != "abc" {
		t.Errorf("stamped release = %+v", got)
	}
	if got := stamped(dev, ""); got.Version != dev.Version {
		t.Errorf("unstamped = %+v", got)
	}
}
