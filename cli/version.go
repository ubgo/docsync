package cli

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// The build-setting keys the Go toolchain writes for a build made inside a git
// checkout. They are the toolchain's names, kept verbatim.
const (
	settingRevision = "vcs.revision"
	settingModified = "vcs.modified"
	settingTime     = "vcs.time"
	// settingTrue is how the toolchain spells a set boolean setting.
	settingTrue = "true"
)

// unknownBuild stands for a build field the binary does not carry. It is
// printed as a word rather than left blank, because a blank reads as "the
// value is empty" when the truth is "nobody recorded it".
const unknownBuild = "unknown"

// BuildInfo is what `ds version` reports about the running binary.
type BuildInfo struct {
	// Version is the module version when installed with `go install …@v`,
	// else "(devel)" for a build from a checkout.
	Version string `json:"version"`
	// Commit is the full revision the binary was built from.
	Commit string `json:"commit"`
	// Dirty says the checkout had uncommitted changes, so Commit alone does
	// not describe the code that is running.
	Dirty bool `json:"dirty"`
	// Time is the commit's time, not the build's.
	Time string `json:"time"`
	// Go is the toolchain that built it.
	Go string `json:"go"`
}

// releaseVersion is the version a release build stamps with
// `-ldflags "-X github.com/ubgo/docsync/cli.releaseVersion=v1.2.3"` (volt does,
// from cli/cmd/ds/.volt.yml). It wins over the toolchain's module version:
// a release binary is built from a checkout, where the toolchain can only
// record a pseudo-version, and a user comparing `ds version` with the release
// they downloaded must see the release's own number. Empty in every other
// build, which then reports what the toolchain recorded.
var releaseVersion string

// stamped returns b with the release version, when one was stamped, in place
// of the toolchain's.
func stamped(b BuildInfo, release string) BuildInfo {
	if release != "" {
		b.Version = release
	}
	return b
}

// buildInfo reads what the Go toolchain embedded in the binary.
//
// Why it exists: the development build is installed as a symlink into the
// repository's bin/, so `ds` follows every rebuild -- and there was no way to
// ask it which commit it was. A stale build produced misleading results in
// this repository more than once, found only after the fact. The toolchain
// stamps the revision and a dirty flag into every build made inside a git
// checkout, so this needs no build flags and cannot drift from the truth.
func buildInfo(read func() (*debug.BuildInfo, bool)) BuildInfo {
	out := BuildInfo{Version: unknownBuild, Commit: unknownBuild, Time: unknownBuild, Go: unknownBuild}
	bi, ok := read()
	if !ok {
		return out
	}
	out.Go = bi.GoVersion
	if bi.Main.Version != "" {
		out.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case settingRevision:
			out.Commit = s.Value
		case settingModified:
			out.Dirty = s.Value == settingTrue
		case settingTime:
			out.Time = s.Value
		}
	}
	return out
}

// String is the one line `ds version` and `ds --version` print.
func (b BuildInfo) String() string {
	commit := b.Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	if b.Dirty {
		commit += " (dirty: uncommitted changes)"
	}
	return fmt.Sprintf("ds %s, commit %s, %s, %s", b.Version, commit, b.Time, b.Go)
}

func (a *App) versionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "version",
		Short:   "the build of ds that is running: version, commit, whether the tree was dirty",
		Example: "  ds version\n  ds version --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b := stamped(buildInfo(debug.ReadBuildInfo), releaseVersion)
			if asJSON {
				return printJSON(cmd.OutOrStdout(), b)
			}
			fmt.Fprintln(cmd.OutOrStdout(), b)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	return cmd
}
