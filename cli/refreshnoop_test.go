package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A refresh with nothing to record writes nothing: it rewrote the ledger
// and refs with a new scanned_at every time, so a second refresh was never
// a no-op and every run dirtied two committed files (bug 77). A change of
// layout ([ledger] shard) or a moved block is still written.
func TestRefreshWithNothingToRecordWritesNothing(t *testing.T) {
	t.Parallel()
	dir, v := initialised(t)
	if r := run(t, dir, v, "scan"); r.code != 0 {
		t.Fatal(r)
	}
	ledgerPath := filepath.Join(dir, ".ds", "ledger.tsv")
	before, _ := os.ReadFile(ledgerPath)
	later := clock.Add(time.Hour)
	var out strings.Builder
	Run([]string{"refresh"}, WithDir(dir), WithIO(nil, &out, &out), WithVCS(v), WithClock(func() time.Time { return later }))
	if !strings.Contains(out.String(), "0 moved; ledger unchanged") {
		t.Errorf("refresh = %q", out.String())
	}
	if after, _ := os.ReadFile(ledgerPath); string(after) != string(before) {
		t.Errorf("an idle refresh rewrote the ledger:\n%s\n%s", before, after)
	}
	// A layout change is written even with the same rows.
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n[ledger]\nshard = true\n")
	if r := run(t, dir, v, "refresh"); r.code != 0 || !strings.Contains(r.out, "ledger updated") {
		t.Errorf("shard on = %+v", r)
	}
	if !NewStore(dir).shardedOnDisk() {
		t.Error("the sharded layout was not written")
	}
	if r := run(t, dir, v, "refresh"); !strings.Contains(r.out, "ledger unchanged") {
		t.Errorf("sharded, idle = %+v", r)
	}
	// A moved block is written, and a write that fails says so.
	write(t, dir, "internal/store/write.go", "// moved\n"+goV1)
	write(t, dir, ".ds/config.toml", "[scan]\ncode = [\"**\"]\ndocs = [\"docs/**\"]\n")
	blocked := filepath.Join(dir, DirName, LedgerFile+writeTempExt)
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, v, "refresh"); r.code != ExitError {
		t.Errorf("refresh with a blocked ledger and a move to record = %+v", r)
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, v, "refresh"); !strings.Contains(r.out, "1 moved; ledger updated") {
		t.Errorf("moved = %+v", r)
	}
	// The cache is written after the ledger; a cache that cannot be written
	// fails the run.
	write(t, dir, "internal/store/write.go", "// moved\n// again\n"+goV1)
	cacheTmp := filepath.Join(dir, DirName, CacheDir, CacheFile+writeTempExt)
	if err := os.MkdirAll(cacheTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, v, "refresh"); r.code != ExitError {
		t.Errorf("refresh with a blocked cache = %+v", r)
	}
	if err := os.Remove(cacheTmp); err != nil {
		t.Fatal(err)
	}
	if r := run(t, dir, v, "refresh"); !strings.Contains(r.out, "ledger u") {
		t.Errorf("after the cache is unblocked = %+v", r)
	}
}
