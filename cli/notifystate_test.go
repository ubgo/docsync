package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// memNotifyState is a NotifyState that survives in memory, standing in for
// the caches and databases a real installation would use.
type memNotifyState struct {
	state   map[string]Notified
	loads   int
	saves   int
	loadErr error
}

func (m *memNotifyState) Load(context.Context) (map[string]Notified, bool, error) {
	m.loads++
	if m.loadErr != nil {
		return nil, false, m.loadErr
	}
	out := map[string]Notified{}
	for k, v := range m.state {
		out[k] = v
	}
	return out, m.state != nil, nil
}

func (m *memNotifyState) Save(_ context.Context, state map[string]Notified) error {
	m.saves++
	m.state = map[string]Notified{}
	for k, v := range state {
		m.state[k] = v
	}
	return nil
}

// notifyFixture gives a repo with open error findings and an owner.
func notifyFixture(t *testing.T) (string, fakeVCS) {
	t.Helper()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	src, err := os.ReadFile(filepath.Join(dir, "internal/store/write.go"))
	if err != nil {
		t.Fatal(err)
	}
	v.files["abc1234:internal/store/write.go"] = src
	write(t, dir, "internal/store/write.go", goV2)
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[owners]\n\"@auth\" = [\"khanakia\"]\n[notify]\nescalate_after = \"2d\"\n")
	return dir, v
}

// TestNotifyStateSeam pins that the memory can live somewhere other than
// the local file, which is the whole point: a CI runner discards
// .ds/notified.json on every run, so an installation whose CI cannot cache
// it supplies a store that survives instead.
// Pins bugs 10, 11.
func TestNotifyStateSeam(t *testing.T) {
	// Not parallel: notify reads the webhook from the environment.
	dir, v := notifyFixture(t)
	mem := &memNotifyState{}
	notify := func(now time.Time) string {
		t.Helper()
		var out strings.Builder
		code := Run([]string{"notify"}, WithDir(dir), WithIO(nil, &out, &out), WithVCS(v),
			WithNotifyState(mem), WithClock(func() time.Time { return now }))
		if code != 0 {
			t.Fatalf("notify = %d: %s", code, out.String())
		}
		return out.String()
	}

	// First run: no memory, so one listing of everything open.
	first := notify(clock)
	if !strings.Contains(first, "no previous notifier state") {
		t.Errorf("first run = %s", first)
	}
	if mem.loads != 1 || mem.saves != 1 {
		t.Errorf("the seam must be read and written: %d loads %d saves", mem.loads, mem.saves)
	}
	if len(mem.state) == 0 {
		t.Fatal("the run recorded nothing")
	}
	// The local file was never touched: the seam replaced it entirely.
	if _, err := os.Stat(filepath.Join(dir, DirName, NotifiedFile)); err == nil {
		t.Error("a supplied state store must be used instead of the file, not as well as")
	}

	// Second run with the memory intact: quiet.
	if second := notify(clock); !strings.Contains(second, "nothing new") {
		t.Errorf("dedupe must hold across runs: %s", second)
	}

	// escalate_after fires once the recorded first-seen is old enough, which
	// is only possible because the memory survived.
	if esc := notify(clock.Add(3 * 24 * time.Hour)); !strings.Contains(esc, "ESCALATED") {
		t.Errorf("escalation needs persisted state: %s", esc)
	}
	if again := notify(clock.Add(4 * 24 * time.Hour)); !strings.Contains(again, "nothing new") {
		t.Errorf("escalation happens once: %s", again)
	}

	// Losing the memory sends one listing again, not one per owner.
	mem.state = nil
	reset := notify(clock)
	if !strings.Contains(reset, "no previous notifier state") {
		t.Errorf("lost state = %s", reset)
	}
	if strings.Count(reset, "no previous notifier state") != 1 {
		t.Errorf("exactly one reset message: %s", reset)
	}

	// A store that cannot be read stops the run. Treating it as empty would
	// silently reset dedupe, which looks exactly like working.
	mem.loadErr = errors.New("cache unavailable")
	var out strings.Builder
	if code := Run([]string{"notify"}, WithDir(dir), WithIO(nil, &out, &out), WithVCS(v), WithNotifyState(mem)); code != ExitError {
		t.Errorf("an unreadable store must fail the run, got %d", code)
	}
}

// TestNotifyStateDefaultsToTheFile pins that nothing changes for someone
// running on one machine.
func TestNotifyStateDefaultsToTheFile(t *testing.T) {
	dir, v := notifyFixture(t)
	if r := run(t, dir, v, "notify"); r.code != 0 {
		t.Fatalf("notify = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName, NotifiedFile)); err != nil {
		t.Fatalf("the default store is the local file: %v", err)
	}
	// A missing file is a first run, not an error; a corrupt one is an
	// error, not an empty state.
	st := NewStore(dir)
	if err := os.Remove(filepath.Join(dir, DirName, NotifiedFile)); err != nil {
		t.Fatal(err)
	}
	state, _, err := (fileNotifyState{store: st}).Load(context.Background())
	if err != nil || len(state) != 0 {
		t.Errorf("missing file = %v %v", state, err)
	}
	write(t, dir, ".ds/"+NotifiedFile, "{")
	if _, _, err := (fileNotifyState{store: st}).Load(context.Background()); err == nil {
		t.Error("a corrupt state file must be an error, never an empty state")
	}
	if r := run(t, dir, v, "notify"); r.code != ExitError {
		t.Errorf("and must stop the run: %+v", r)
	}
}

// TestDoctorNotifyRow makes the CI failure loud. On a laptop the file is
// simply there; on a runner it silently is not, and everything looks
// healthy while dedupe never holds.
func TestDoctorNotifyRow(t *testing.T) {
	dir, v := notifyFixture(t)

	// Off CI the row is informational — and truthful. With no file it must
	// not claim a state that is not there.
	t.Setenv(ciEnv, "")
	r := run(t, dir, v, "doctor")
	if strings.Contains(r.out, "will not work") {
		t.Errorf("off CI must not warn: %s", r.out)
	}
	if !strings.Contains(r.out, "no state yet") {
		t.Errorf("with no file it must say so: %s", r.out)
	}
	// Once there is one, it says how much it remembers.
	if r := run(t, dir, v, "notify"); r.code != 0 {
		t.Fatalf("notify = %+v", r)
	}
	if r := run(t, dir, v, "doctor"); !strings.Contains(r.out, "remembered in "+DirName+"/"+NotifiedFile) {
		t.Errorf("with a file it must say what it holds: %s", r.out)
	}

	// Under CI with no state, it warns and names the remedy.
	t.Setenv(ciEnv, "1")
	if err := os.Remove(filepath.Join(dir, DirName, NotifiedFile)); err != nil {
		t.Fatal(err)
	}
	r = run(t, dir, v, "doctor")
	for _, want := range []string{"notify", "WARN", "dedupe and escalation will not work", NotifiedFile} {
		if !strings.Contains(r.out, want) {
			t.Errorf("under CI with no state must mention %q: %s", want, r.out)
		}
	}

	// With state it is quiet again.
	if r := run(t, dir, v, "notify"); r.code != 0 {
		t.Fatalf("notify = %+v", r)
	}
	if r := run(t, dir, v, "doctor"); strings.Contains(r.out, "will not work") {
		t.Errorf("with state = %s", r.out)
	}

	// An unreadable store is reported rather than crashing the row.
	write(t, dir, ".ds/"+NotifiedFile, "{")
	if r := run(t, dir, v, "doctor"); !strings.Contains(r.out, "state unreadable") {
		t.Errorf("unreadable state = %s", r.out)
	}
}

// TestNightlyTemplateCachesNotifyState is a test over the shipped file,
// because a template nobody tests is a template that loses steps in an
// edit — and this step is the only thing making dedupe work on CI.
// Pins bugs 10, 11.
func TestNightlyTemplateCachesNotifyState(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "integrations", "github", "workflows", "docsync-nightly.yml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"actions/cache@v4",
		"path: " + DirName + "/" + NotifiedFile,
		"restore-keys: ds-notified-",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the nightly template must contain %q so notify has memory:\n%s", want, body)
		}
	}
	// The cache has to be restored before notify runs, or it restores
	// nothing useful. Matched on the command line itself rather than on any
	// mention of it, so the comment above the cache step does not satisfy
	// the ordering it is describing.
	cmd := strings.Index(body, "\n          ds notify")
	if cmd < 0 {
		t.Fatalf("the template must still run ds notify:\n%s", body)
	}
	if strings.Index(body, "actions/cache@v4") > cmd {
		t.Error("the cache step must come before the notify step")
	}
}

// TestNotifyResetEdges covers the reset path's other shapes: a first run
// with nothing open, and a dry run, which must print without recording.
func TestNotifyResetEdges(t *testing.T) {
	// Not parallel: notify reads the webhook from the environment.
	// A clean repo on its first run has no memory and nothing to say.
	clean := t.TempDir()
	write(t, clean, "docs/a.md", "# A\n\nNothing cited.\n")
	v := fakeVCS{head: "abc1234", branch: "main", files: map[string][]byte{}}
	if r := run(t, clean, v, "init"); r.code != 0 {
		t.Fatalf("init = %+v", r)
	}
	if r := run(t, clean, v, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	r := run(t, clean, v, "notify")
	if r.code != 0 || !strings.Contains(r.out, "nothing new to notify") {
		t.Errorf("clean first run = %+v", r)
	}
	if strings.Contains(r.out, "no previous notifier state") {
		t.Error("with nothing open there is nothing to announce")
	}
	// It still records, so the next run knows it has memory.
	if _, err := os.Stat(filepath.Join(clean, DirName, NotifiedFile)); err != nil {
		t.Errorf("a quiet first run must still record: %v", err)
	}
	// The same, dry: prints and records nothing.
	clean2 := t.TempDir()
	write(t, clean2, "docs/a.md", "# A\n\nNothing cited.\n")
	if r := run(t, clean2, v, "init"); r.code != 0 {
		t.Fatalf("init = %+v", r)
	}
	if r := run(t, clean2, v, "scan"); r.code != 0 {
		t.Fatalf("scan = %+v", r)
	}
	if r := run(t, clean2, v, "notify", "--dry-run"); r.code != 0 || !strings.Contains(r.out, "nothing new") {
		t.Errorf("clean dry run = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(clean2, DirName, NotifiedFile)); err == nil {
		t.Error("a dry run must not record")
	}

	// A first run with findings, dry: the listing is printed, nothing kept.
	dir, dv := notifyFixture(t)
	r = run(t, dir, dv, "notify", "--dry-run")
	if r.code != 0 || !strings.Contains(r.out, "no previous notifier state") {
		t.Errorf("dry reset = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName, NotifiedFile)); err == nil {
		t.Error("a dry run must not record")
	}
	// And a store that cannot be written surfaces from the reset path.
	if err := os.Mkdir(filepath.Join(dir, DirName, NotifiedFile+writeTempExt), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, dv, "notify"); r.code != ExitError {
		t.Errorf("unwritable state from the reset path = %+v", r)
	}
}

// TestNotifyPerOwnerWithState covers the ordinary path, which now runs only
// once there is memory: the first run lists everything, and it is the
// second and later runs that route per owner.
func TestNotifyPerOwnerWithState(t *testing.T) {
	// Not parallel: the webhook comes from the environment.
	dir, v := notifyFixture(t)
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		posts = append(posts, string(b))
		if strings.Contains(string(b), "boom") {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	t.Setenv("DS_TEST_HOOK", srv.URL)
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[owners]\n\"@auth\" = [\"khanakia\"]\n[notify]\nslack = \"$DS_TEST_HOOK\"\nescalate_after = \"2d\"\n")

	notify := func(args ...string) result {
		t.Helper()
		return run(t, dir, v, append([]string{"notify"}, args...)...)
	}
	// First run establishes the memory.
	if r := notify(); r.code != 0 {
		t.Fatalf("first = %+v", r)
	}
	posts = nil

	// A new finding with state present routes to its owner alone.
	write(t, dir, "docs/extra.md", "See [it](ds:block?id=nope-h3v8n2wd).\n")
	r := notify()
	if r.code != 0 || len(posts) != 1 {
		t.Fatalf("per-owner send = %+v posts=%v", r, posts)
	}
	if !strings.Contains(r.out, "via slack") || strings.Contains(posts[0], "no previous notifier state") {
		t.Errorf("a later run is not a reset: %s / %s", r.out, posts[0])
	}
	// Dry, with state: prints and records nothing new.
	write(t, dir, "docs/extra2.md", "See [it](ds:block?id=nope-t4k2b9rf).\n")
	posts = nil
	if r := notify("--dry-run"); r.code != 0 || len(posts) != 0 {
		t.Errorf("dry with state = %+v posts=%v", r, posts)
	}
	// A webhook that fails surfaces from the per-owner path.
	write(t, dir, "docs/boom.md", "See [it](ds:block?id=boom-a2b6f8jk).\n")
	if r := notify(); r.code != ExitError {
		t.Errorf("webhook failure = %+v", r)
	}
}
