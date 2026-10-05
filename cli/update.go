package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/ubgo/docsync/cli/selfupdate"
	"github.com/ubgo/docsync/config"
	"golang.org/x/mod/semver"
)

// Where ds's own releases live: tags "ds/v0.1.7" in ubgo/docsync, beside
// the library's and the ext modules' tags, which carry no binaries.
const (
	releaseRepo      = "ubgo/docsync"
	releaseTagPrefix = "ds/"
	// pluginPrefix names the companion executables a release archive
	// carries beside ds; they are updated with it so the two never disagree.
	pluginPrefix = "ds-resolve-"
	// goInstallPath is what a `go install` user re-runs to update.
	goInstallPath = "github.com/ubgo/docsync/cli/cmd/ds"
)

// The update knobs a user sets.
const (
	updateCmdName = "update"
	// envUpdate takes one of config.UpdateModeValues for every repository
	// on the machine: DS_UPDATE=off in a shell profile turns automatic
	// updates off for good.
	envUpdate = "DS_UPDATE"
	// flagNoUpdate skips the automatic check for one run.
	flagNoUpdate = "no-update"
	flagCheck    = "check"
	flagVersion  = "version"
	// envNoColor is the informal standard (no-color.org): any value turns
	// colour off.
	envNoColor = "NO_COLOR"
	envTerm    = "TERM"
	termDumb   = "dumb"
)

// Timings. updateEvery keeps the automatic check to one network request a
// day; checkTimeout bounds what that request may add to a command, so a
// slow or absent network costs at most that, once a day; downloadTimeout
// bounds a whole update, which moves tens of megabytes.
const (
	updateEvery     = 24 * time.Hour
	checkTimeout    = 2 * time.Second
	downloadTimeout = 5 * time.Minute
)

// bytesPerMB turns an archive's size into the megabytes `ds update` prints.
const bytesPerMB = 1 << 20

// stateFile is the throttle file under the user's cache directory. It only
// decides when to ask again and which version already failed to install;
// deleting it costs one extra check and changes no result.
const (
	stateDir  = "docsync"
	stateFile = "update.json"
)

// noAutoUpdate lists the commands the automatic check never runs before:
// update itself, the ones asked about the binary (doctor reports the update
// state and must not change it while reporting), the editor and agent
// servers (which speak a protocol on stdout), and cobra's own.
var noAutoUpdate = []string{updateCmdName, "version", "doctor", "help", "completion", "lsp", "mcp", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd}

// updater is how releases are found and installed. Each field is a seam a
// test replaces; Run fills the real ones.
type updater struct {
	source selfupdate.Source
	// release is the version stamped into a release build, empty in every
	// other build; only a release build updates itself automatically.
	release string
	// build is the version `ds version` shows, for messages.
	build       string
	executable  func() (string, error)
	statePath   func() (string, error)
	getenv      func(string) string
	interactive func() bool
	reexec      func(path string, args []string) (int, error)
	goos        string
	goarch      string
	now         func() time.Time
}

// defaultUpdater is the real one. A binary that is not named ds, or was not
// stamped by a release, has no release stream to follow and never updates
// itself automatically.
func (a *App) defaultUpdater() updater {
	rel := releaseVersion
	if a.name != DefaultName {
		rel = ""
	}
	return updater{
		source:     selfupdate.GitHub{Repo: releaseRepo, TagPrefix: releaseTagPrefix, Token: os.Getenv(envGitHubToken)},
		release:    rel,
		build:      stamped(buildInfo(debug.ReadBuildInfo), releaseVersion).Version,
		executable: os.Executable,
		statePath: func() (string, error) {
			dir, err := os.UserCacheDir()
			return filepath.Join(dir, stateDir, stateFile), err
		},
		getenv:      os.Getenv,
		interactive: func() bool { return isTerminal(a.stdout) && isTerminal(a.stderr) },
		reexec: func(path string, args []string) (int, error) {
			return reexec(path, args, a.stdin, a.stdout, a.stderr)
		},
		goos:   runtime.GOOS,
		goarch: runtime.GOARCH,
		now:    time.Now,
	}
}

// isTerminal reports whether w is a terminal, which decides colour and
// whether anyone is there to see an automatic update.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// reexec runs the freshly installed binary with the original arguments,
// passing the terminal through, and returns its exit code. The child gets
// DS_UPDATE=off so it cannot start a second update.
func reexec(path string, args []string, in io.Reader, out, errw io.Writer) (int, error) {
	c := exec.Command(path, args...)
	c.Stdin, c.Stdout, c.Stderr = in, out, errw
	c.Env = append(os.Environ(), envUpdate+"="+config.UpdateOff)
	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, err
}

// palette colours a terminal's output and leaves everything else plain.
type palette struct{ on bool }

// ANSI SGR codes.
const (
	sgrGreen  = "32"
	sgrYellow = "33"
	sgrRed    = "31"
	sgrDim    = "2"
	sgrBold   = "1"
)

func (p palette) paint(code, s string) string {
	if !p.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p palette) ok(s string) string   { return p.paint(sgrGreen, "✓ ") + s }
func (p palette) up(s string) string   { return p.paint(sgrYellow, "↑ ") + s }
func (p palette) warn(s string) string { return p.paint(sgrRed, "! ") + s }
func (p palette) dim(s string) string  { return p.paint(sgrDim, s) }
func (p palette) bold(s string) string { return p.paint(sgrBold, s) }

// colours decides colour for one writer: a terminal, NO_COLOR unset, and a
// terminal type that understands it.
func (u updater) colours(terminal bool) palette {
	return palette{on: terminal && u.getenv(envNoColor) == "" && u.getenv(envTerm) != termDumb}
}

// Where the update mode in effect came from, which `ds update`, the
// automatic messages and `ds doctor` name so nobody has to guess why a
// binary is or is not updating itself.
const (
	updateFromDefault = "default"
	updateFromRepo    = ".ds/config.toml [update] mode"
	// updateFromBuild is the reason a build that no release made never
	// updates itself.
	updateFromBuild = "not a release build"
)

// updateHint is what every update message offers as the way to change it.
const updateHint = "change with DS_UPDATE=notify|off or [update] mode in .ds/config.toml"

// mode is the strictest of the built-in default (auto), DS_UPDATE and the
// repository's [update] mode, and which of them decided it: either can turn
// updating down, neither can turn it up past the other. An unrecognised
// DS_UPDATE is reported and read as notify, so a typo never silently means
// "install".
func (u updater) mode(repo string, warn func(string)) (mode, from string) {
	mode, from = config.DefaultUpdateMode, updateFromDefault
	if env := u.getenv(envUpdate); env != "" {
		if !slices.Contains(config.UpdateModeValues, env) {
			warn(fmt.Sprintf("%s=%s is not one of %s; treating it as %s", envUpdate, env, strings.Join(config.UpdateModeValues, ", "), config.UpdateNotify))
			env = config.UpdateNotify
		}
		mode, from = env, envUpdate
	}
	if repo != "" && stricter(mode, repo) != mode {
		mode, from = repo, updateFromRepo
	}
	return mode, from
}

// updateMode is the mode in effect for this command and where it came
// from; a build no release made is off, for that reason.
func (a *App) updateMode(warn func(string)) (mode, from string) {
	if a.upd.release == "" {
		return config.UpdateOff, updateFromBuild
	}
	return a.upd.mode(a.repoUpdateMode(), warn)
}

// modeLine is the closing line of `ds update`: the automatic mode, its
// source, and how to change it.
func modeLine(p palette, mode, from string) string {
	if from == updateFromBuild {
		return p.dim(fmt.Sprintf("Automatic updates: %s (%s)", mode, from))
	}
	return p.dim(fmt.Sprintf("Automatic updates: %s (%s) · %s", mode, from, updateHint))
}

// stricter is whichever of two modes comes later in UpdateModeValues.
func stricter(a, b string) string {
	if slices.Index(config.UpdateModeValues, b) > slices.Index(config.UpdateModeValues, a) {
		return b
	}
	return a
}

// files is what an update installs: the program, and the plugins that ship
// in its archive.
func (u updater) files() (main string, want func(string) bool) {
	exe := ""
	if u.goos == "windows" {
		exe = ".exe"
	}
	return DefaultName + exe, func(name string) bool { return strings.HasPrefix(name, pluginPrefix) }
}

// target is the installed program: the running executable with symlinks
// resolved, which must carry the name the release archive gives it.
func (u updater) target() (string, error) {
	self, err := u.executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	main, _ := u.files()
	if filepath.Base(self) != main {
		return "", fmt.Errorf("the running program is %s, not %s; reinstall it with install.sh, or update it the way it was installed", self, main)
	}
	return self, nil
}

// install puts rel in place of the running program and its plugins.
func (u updater) install(ctx context.Context, rel selfupdate.Release) (string, selfupdate.Result, error) {
	self, err := u.target()
	if err != nil {
		return "", selfupdate.Result{}, err
	}
	main, want := u.files()
	in := selfupdate.Install{
		Source:  u.source,
		Release: rel,
		Asset:   selfupdate.AssetName(DefaultName, rel.Version, u.goos, u.goarch),
		Dir:     filepath.Dir(self),
		Main:    main,
		Want:    want,
	}
	res, err := in.Run(ctx)
	if errors.Is(err, os.ErrPermission) {
		err = fmt.Errorf("%w; %s is not writable: re-run `ds update` with sudo, or reinstall to a directory you own", err, in.Dir)
	}
	return self, res, err
}

// autoUpdate runs before every command. In a release build, at a terminal,
// outside CI, it asks at most once a day whether a newer release exists;
// in auto mode it installs that release and re-runs the command on it, in
// notify mode it says so. Every failure is one line on stderr and the
// command runs on the current version: an update never blocks work.
func (a *App) autoUpdate(cmd *cobra.Command) error {
	u := a.upd
	if u.release == "" || slices.Contains(noAutoUpdate, cmd.Name()) || flagSet(cmd, flagNoUpdate) || flagSet(cmd, flagJSON) ||
		u.getenv(ciEnv) != "" || !u.interactive() {
		return nil
	}
	p := u.colours(true)
	warn := func(s string) { fmt.Fprintln(a.stderr, p.warn(s)) }
	mode, _ := u.mode(a.repoUpdateMode(), warn)
	if mode == config.UpdateOff {
		return nil
	}
	path, err := u.statePath()
	if err != nil {
		return nil
	}
	st := selfupdate.LoadState(path)
	fresh := false
	if st.Due(u.now(), updateEvery) {
		ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
		rel, err := u.source.Latest(ctx)
		cancel()
		st.CheckedAt = u.now()
		if err == nil {
			st.Latest, fresh = rel.Version, true
		}
		_ = st.Save(path)
	}
	if !selfupdate.Newer(u.release, st.Latest) {
		return nil
	}
	if mode == config.UpdateNotify || st.Latest == st.Failed {
		if fresh {
			fmt.Fprintln(a.stderr, p.up(fmt.Sprintf("%s %s is available (you have %s): run %s", a.name, st.Latest, u.release, p.bold("`ds update`")))+p.dim(fmt.Sprintf(" · %s=%s to stop these", envUpdate, config.UpdateOff)))
		}
		return nil
	}
	fmt.Fprintln(a.stderr, p.up(fmt.Sprintf("Updating %s %s → %s before running %s", a.name, u.release, p.bold(st.Latest), cmd.Name())))
	fmt.Fprintln(a.stderr, p.dim(fmt.Sprintf("  %s=%s to only be told, %s=%s to stop · %s update --help", envUpdate, config.UpdateNotify, envUpdate, config.UpdateOff, a.name)))
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()
	rel := u.source.Release(st.Latest)
	self, _, err := u.install(ctx, rel)
	if err != nil {
		st.Failed = st.Latest
		_ = st.Save(path)
		warn(fmt.Sprintf("automatic update failed: %v", err))
		warn(fmt.Sprintf("running %s on %s; `ds update` retries", cmd.Name(), u.release))
		return nil
	}
	fmt.Fprintln(a.stderr, p.ok(fmt.Sprintf("Updated to %s", st.Latest))+p.dim("  what changed: "+rel.URL))
	code, err := u.reexec(self, a.args)
	if err != nil {
		return err
	}
	return exitCode(code)
}

// flagSet reports whether a command has a flag of that name and it was set.
func flagSet(cmd *cobra.Command, name string) bool {
	f := cmd.Flags().Lookup(name)
	return f != nil && f.Changed
}

// repoUpdateMode is the [update] mode of the repository the command runs
// in, or empty outside one or when its config does not load (the command
// itself reports that).
func (a *App) repoUpdateMode() string {
	st := NewStore(a.dir)
	if !st.Exists() {
		return ""
	}
	c, err := a.loadConfig(st)
	if err != nil {
		return ""
	}
	return c.Update.Mode
}

// updateRow is `ds doctor`'s update row: the automatic mode, what decided
// it, and what the last daily check found, read from the state file, so
// doctor asks the network nothing. A newer release than the running one is
// WARN, which does not change doctor's exit code.
func (a *App) updateRow() []string {
	mode, from := a.updateMode(func(string) {})
	if from == updateFromBuild {
		return []string{updateCmdName, doctorOK, fmt.Sprintf("%s (%s): rebuild it, or `go install` a newer version", mode, from)}
	}
	detail := fmt.Sprintf("%s (from %s)", mode, from)
	path, err := a.upd.statePath()
	if err != nil {
		return []string{updateCmdName, doctorOK, detail + "; no cache directory, so nothing is remembered between runs"}
	}
	st := selfupdate.LoadState(path)
	if st.CheckedAt.IsZero() {
		return []string{updateCmdName, doctorOK, detail + "; not checked yet"}
	}
	when := ago(st.CheckedAt, a.upd.now())
	if selfupdate.Newer(a.upd.release, st.Latest) {
		return []string{updateCmdName, doctorWarn, fmt.Sprintf("%s; %s is available (checked %s): run `ds update`", detail, st.Latest, when)}
	}
	return []string{updateCmdName, doctorOK, fmt.Sprintf("%s; %s is the latest (checked %s)", detail, a.upd.release, when)}
}

// UpdateReport is `ds update --json`.
type UpdateReport struct {
	// Current is the running version; Latest the release it was compared
	// with (or the one --version named).
	Current string `json:"current"`
	Latest  string `json:"latest"`
	// Available says Latest is newer than Current.
	Available bool `json:"update_available"`
	// Updated says files were replaced; Installed lists them.
	Updated   bool     `json:"updated"`
	Installed []string `json:"installed,omitempty"`
	// URL is the release page with its notes.
	URL string `json:"url"`
	// AutoUpdate is the automatic mode in effect (auto, notify, off) and
	// AutoUpdateFrom what decided it: "default", "DS_UPDATE", the
	// repository's config, or "not a release build".
	AutoUpdate     string `json:"auto_update"`
	AutoUpdateFrom string `json:"auto_update_from"`
}

// buildKind names a build that is not a release archive, so `ds update`
// can say how it should be updated instead.
func (u updater) buildKind() (string, bool) {
	v := u.build
	if semver.IsValid(v) && semver.Prerelease(v) == "" && semver.Build(v) == "" {
		return fmt.Sprintf("this %s was installed with `go install`; update it the same way: go install %s@latest", DefaultName, goInstallPath), true
	}
	return fmt.Sprintf("this is a development build (%s), made from a checkout; rebuild it there, or pass --%s to replace it with a release", v, flagForce), false
}

func (a *App) updateCmd() *cobra.Command {
	var check, force, asJSON bool
	var version string
	cmd := &cobra.Command{
		Use:   updateCmdName,
		Short: "update this binary and its plugins to the newest release",
		Long: `Updates ds, and the ds-resolve-* plugins beside it, to the newest release,
verified against the release's checksums.txt before anything is replaced.

A release build also does this on its own: at most once a day, before a
command run at a terminal, it checks for a newer release and installs it
first. It never does so in CI, under --json, when output is not a terminal,
or in a development or go-install build. Turn it down with DS_UPDATE=notify
(only say so) or DS_UPDATE=off, for one run with --no-update, or for everyone
in a repository with [update] mode in .ds/config.toml.`,
		Example: "  ds update\n  ds update --check\n  ds update --dry-run\n  ds update --version v0.1.6\n  ds update --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u := a.upd
			out := cmd.OutOrStdout()
			p := u.colours(!asJSON && u.interactive())
			say := func(s string) {
				if !asJSON {
					fmt.Fprintln(out, s)
				}
			}
			current := u.release
			if current == "" {
				current = u.build
			}
			if u.release == "" && !check && !force {
				msg, _ := u.buildKind()
				return errors.New(msg)
			}
			say(p.dim("Current version: ") + p.bold(current))
			ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
			defer cancel()
			var rel selfupdate.Release
			if version != "" {
				if !semver.IsValid(version) {
					return fmt.Errorf("%w: --%s %q is not a version like v0.1.6", ErrUsage, flagVersion, version)
				}
				rel = u.source.Release(version)
			} else {
				say(p.dim("Checking for updates to latest version..."))
				var err error
				if rel, err = u.source.Latest(ctx); err != nil {
					return fmt.Errorf("could not find the latest release: %w", err)
				}
			}
			rep := UpdateReport{Current: current, Latest: rel.Version, Available: selfupdate.Newer(current, rel.Version), URL: rel.URL}
			rep.AutoUpdate, rep.AutoUpdateFrom = a.updateMode(func(s string) { fmt.Fprintln(a.stderr, p.warn(s)) })
			finish := func(rep UpdateReport) error {
				say(modeLine(p, rep.AutoUpdate, rep.AutoUpdateFrom))
				return a.updateJSON(asJSON, out, rep)
			}
			if path, err := u.statePath(); err == nil && version == "" {
				st := selfupdate.LoadState(path)
				st.CheckedAt, st.Latest = u.now(), rel.Version
				_ = st.Save(path)
			}
			switch {
			case check:
				if rep.Available {
					say(p.up(fmt.Sprintf("%s is available: run %s", p.bold(rel.Version), p.bold("`ds update`"))) + p.dim("  "+rel.URL))
				} else {
					say(p.ok(fmt.Sprintf("%s is up to date (%s)", a.name, current)))
				}
				return finish(rep)
			case rel.Version == current && !force:
				say(p.ok(fmt.Sprintf("%s is up to date (%s)", a.name, current)))
				return finish(rep)
			case !rep.Available && version == "" && !force:
				// A build newer than the newest release (a release being
				// cut) is not "updated" backwards without being asked.
				say(p.ok(fmt.Sprintf("%s %s is newer than the latest release (%s); nothing to do", a.name, current, rel.Version)))
				return finish(rep)
			}
			say(p.up(fmt.Sprintf("Updating %s %s → %s", a.name, current, p.bold(rel.Version))))
			self, res, err := u.install(ctx, rel)
			if err != nil {
				return err
			}
			rep.Updated = true
			for _, f := range res.Installed {
				rep.Installed = append(rep.Installed, filepath.Join(filepath.Dir(self), f))
			}
			say(p.ok(fmt.Sprintf("Downloaded and verified %s", selfupdate.AssetName(DefaultName, rel.Version, u.goos, u.goarch))) + p.dim(fmt.Sprintf("  (%.1f MB, sha256 matches checksums.txt)", float64(res.Bytes)/bytesPerMB)))
			plugins := len(res.Installed) - 1
			say(p.ok(fmt.Sprintf("Installed %s and %s in %s", res.Installed[0], plural(plugins, "plugin"), filepath.Dir(self))))
			if path, err := u.statePath(); err == nil {
				st := selfupdate.LoadState(path)
				st.Failed = ""
				_ = st.Save(path)
			}
			say(p.ok(fmt.Sprintf("%s is now %s", a.name, p.bold(rel.Version))) + p.dim("  what changed: "+rel.URL))
			return finish(rep)
		},
	}
	cmd.Flags().BoolVar(&check, flagCheck, false, "only report whether a newer release exists; change nothing")
	cmd.Flags().BoolVar(&check, flagDryRun, false, "the same as --check: say what would be installed, change nothing")
	cmd.Flags().BoolVar(&force, flagForce, false, "install even when up to date, or over a development build")
	cmd.Flags().StringVar(&version, flagVersion, "", "install this release instead of the newest (also to go back to an older one)")
	cmd.Flags().BoolVar(&asJSON, flagJSON, false, "machine output")
	return cmd
}

// updateJSON prints the report when --json asked for it.
func (a *App) updateJSON(asJSON bool, out io.Writer, rep UpdateReport) error {
	if !asJSON {
		return nil
	}
	return printJSON(out, rep)
}
