package scan

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync/internal/glob"
)

// A report ds wrote is not scanned: a `check --json` saved in the
// repository was a phantom citation of every block it quoted (bug 75), and
// an exported audit log had its ids rewritten by rename (bug 74).
func TestOwnOutputIsNotScanned(t *testing.T) {
	t.Parallel()
	cite := "[due](ds:block?id=due-k7m2p4xq)"
	fsys := fstest.MapFS{
		"a.go":        &fstest.MapFile{Data: []byte("package p\n\n// ds:def id=due-k7m2p4xq\nfunc Due() {}\n")},
		"docs.md":     &fstest.MapFile{Data: []byte("It is " + cite + ".\n")},
		"report.json": &fstest.MapFile{Data: []byte("{\n  \"json_format\": 1,\n  \"findings\": [{\"sentence\": \"It is " + cite + ".\"}]\n}\n")},
		"audit.jsonl": &fstest.MapFile{Data: []byte(`{"At":"2026-09-06T00:00:00Z","Actor":"a","ActorKind":"human","Sentence":"It is ` + cite + `."}` + "\n")},
		// A user's own JSON that merely mentions a citation is still read.
		"mine.json": &fstest.MapFile{Data: []byte(`{"note": "It is ` + cite + `."}` + "\n")},
	}
	inc, _ := glob.CompileAll([]string{"**"})
	res, err := Scan(context.Background(), fsys, Options{Prefix: "ds", Include: inc})
	if err != nil {
		t.Fatal(err)
	}
	skipped := map[string]SkipReason{}
	for _, s := range res.Skipped {
		skipped[s.File] = s.Reason
	}
	if skipped["report.json"] != SkipOwnOutput || skipped["audit.jsonl"] != SkipOwnOutput || skipped["mine.json"] != "" {
		t.Errorf("skipped = %v", skipped)
	}
	for _, r := range res.Refs {
		if r.Pos.File == "report.json" || r.Pos.File == "audit.jsonl" {
			t.Errorf("a ds report cited a block: %+v", r)
		}
	}
	if SkipOwnOutput.Unreadable() {
		t.Error("ds output is skipped by choice, not because it cannot be read")
	}
}

func TestIsOwnOutput(t *testing.T) {
	t.Parallel()
	for src, want := range map[string]bool{
		"{\"json_format\":1}":                        true,
		"  {\n  \"json_format\": 1,\n":               true,
		"{\"repo\": \"x\", \"json_format\": 1}":      false,
		`{"At":"x","Actor":"a","ActorKind":"human"}`: true,
		`{"At":"x","Actor":"a"}`:                     false,
		"plain text":                                 false,
		"":                                           false,
	} {
		if got := IsOwnOutput([]byte(src)); got != want {
			t.Errorf("IsOwnOutput(%q) = %v, want %v", src, got, want)
		}
	}
}
