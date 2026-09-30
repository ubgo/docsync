package structured

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/extract"
)

const doc = `# top comment
auth:
  port: 8081   # ds:def id=port-a2b6f8jk
  hosts:       # ds:def id=hosts-b3c7g9kl
    - a.example
    - b.example   # ds:def id=hostb-c4d8h2lm
  # ds:def id=motd-d5e9j3mn
  #   owner=@ops
  motd: |
    Welcome.
    Two lines.

  quoted: "has # hash"   # ds:def id=quoted-e6f2k4np
# ds:def id=db-f7g3l5pq
db:
  url: postgres://x
  pool: 5

# ds:block id=port-a2b6f8jk
# ds:def
# ds:def id=nothing-g8h4m6qr
`

func find(defs []extract.Def, id string) (block.Block, bool) {
	for _, d := range defs {
		if d.Block.ID == id {
			return d.Block, true
		}
	}
	return block.Block{}, false
}

func TestYAMLBinding(t *testing.T) {
	t.Parallel()
	tier := YAML()
	if tier.Name() != "yaml" || !tier.Match("c/x.YAML") || !tier.Match("a.yml") || tier.Match("a.toml") {
		t.Error("name/match")
	}
	f := tier.Extract("c.yaml", []byte(doc), "ds")
	port, ok := find(f.Defs, "port-a2b6f8jk")
	if !ok || port.Content != "8081" || port.Symbol != "auth.port" || port.Pos != (block.Position{Start: 3, End: 3}) || port.Kind != block.KindKey {
		t.Errorf("port = %+v", port)
	}
	hosts, _ := find(f.Defs, "hosts-b3c7g9kl")
	if hosts.Symbol != "auth.hosts" || hosts.Pos != (block.Position{Start: 4, End: 6}) || !strings.Contains(hosts.Content, "- b.example") {
		t.Errorf("hosts = %+v", hosts)
	}
	hostb, _ := find(f.Defs, "hostb-c4d8h2lm")
	if hostb.Symbol != "auth.hosts[1]" || hostb.Content != "b.example" || hostb.Pos.Start != 6 {
		t.Errorf("hostb = %+v", hostb)
	}
	motd, _ := find(f.Defs, "motd-d5e9j3mn")
	if motd.Content != "Welcome.\nTwo lines." || motd.Pos != (block.Position{Start: 9, End: 11}) || motd.Owner() != "@ops" || motd.DirectivePos != (block.Position{Start: 7, End: 8}) {
		t.Errorf("motd = %+v", motd)
	}
	quoted, _ := find(f.Defs, "quoted-e6f2k4np")
	if quoted.Content != "has # hash" {
		t.Errorf("quoted = %+v", quoted)
	}
	db, _ := find(f.Defs, "db-f7g3l5pq")
	if db.Symbol != "db" || db.Pos != (block.Position{Start: 15, End: 17}) || !strings.Contains(db.Content, "pool: 5") {
		t.Errorf("db = %+v", db)
	}
	if len(f.Refs) != 1 || f.Refs[0].Reference.Verb != "block" || f.Refs[0].Reference.ID != "port-a2b6f8jk" || f.Refs[0].Reference.Pos.Start != 19 {
		t.Errorf("refs = %+v", f.Refs)
	}
	var noID, nothing bool
	for _, p := range f.Problems {
		if errors.Is(p.Err, extract.ErrNoID) && p.Pos.Start == 20 {
			noID = true
		}
		if errors.Is(p.Err, extract.ErrNothingToBind) && p.Pos.Start == 21 {
			nothing = true
		}
	}
	if !noID || !nothing || len(f.Problems) != 2 {
		t.Errorf("problems = %+v", f.Problems)
	}
	if len(f.Defs) != 6 {
		t.Errorf("defs = %d", len(f.Defs))
	}
}

func TestYAMLEdges(t *testing.T) {
	t.Parallel()
	tier := YAML()
	// Remote def, parse error in a directive, top-level sequence, CRLF.
	src := "# ds:def id=remote-a2b6f8jk file=package.json pick=json:version\r\n- one   # ds:def id=one-b3c7g9kl\r\n- two   # ds:def id=bad key\r\n"
	f := tier.Extract("l.yml", []byte(src), "ds")
	if len(f.Defs) != 2 || !f.Defs[0].Remote || f.Defs[0].Block.ID != "remote-a2b6f8jk" || f.Defs[1].Block.Symbol != "[0]" || f.Defs[1].Block.Content != "one" {
		t.Errorf("edges defs = %+v", f.Defs)
	}
	if len(f.Problems) != 1 || f.Problems[0].Pos.Start != 3 {
		t.Errorf("edges problems = %+v", f.Problems)
	}
	// A comment that is not a directive, and directives in the middle of a
	// line inside quotes, are ignored.
	f = tier.Extract("q.yaml", []byte("a: 'x # ds:def id=no' # plain comment\nb: 1 #ds:def id=tight-c4d8h2lm\n"), "ds")
	if len(f.Defs) != 1 || f.Defs[0].Block.ID != "tight-c4d8h2lm" || f.Defs[0].Block.Content != "1" {
		t.Errorf("quoted/tight = %+v", f.Defs)
	}
	// Invalid YAML falls back to line mode and reports the parse failure.
	f = tier.Extract("bad.yaml", []byte("key: [unclosed\nport: 8081   # ds:def id=port-a2b6f8jk\n"), "ds")
	if len(f.Defs) != 1 || strings.TrimSpace(f.Defs[0].Block.Content) != "port: 8081" {
		t.Errorf("fallback defs = %+v", f.Defs)
	}
	found := false
	for _, p := range f.Problems {
		if errors.Is(p.Err, ErrYAML) {
			found = true
		}
	}
	if !found {
		t.Errorf("fallback must report the parse failure: %+v", f.Problems)
	}
	// A standalone directive with nothing after it, and a directive whose
	// next entry is nested deeper than the comment.
	f = tier.Extract("n.yaml", []byte("a:\n  # ds:def id=deep-d5e9j3mn\n  b: 2\n# ds:def id=tail-e6f2k4np\n"), "ds")
	if len(f.Defs) != 1 || f.Defs[0].Block.Symbol != "a.b" || len(f.Problems) != 1 {
		t.Errorf("nested/tail = %+v %+v", f.Defs, f.Problems)
	}
	// Flow collections share one line; the first entry on the line wins
	// and inner entries never end before they start.
	f = tier.Extract("f.yaml", []byte("x: {a: 1, b: 2}   # ds:def id=flow-f7g3l5pq\nlist: [1, 2]   # ds:def id=list-g8h4m6qr\n"), "ds")
	if len(f.Defs) != 2 || f.Defs[0].Block.Symbol != "x" || f.Defs[0].Block.Content != "x: {a: 1, b: 2}   # ds:def id=flow-f7g3l5pq" || f.Defs[1].Block.Pos != (block.Position{Start: 2, End: 2}) {
		t.Errorf("flow = %+v", f.Defs)
	}
	// Empty document.
	if f := tier.Extract("e.yaml", nil, "ds"); len(f.Defs)+len(f.Refs)+len(f.Problems) != 0 {
		t.Errorf("empty = %+v", f)
	}
	if got, _, ok := commentAt(`x: "a#b" # ds:block id=q`, "ds:", yamlMarkers); !ok || got != "ds:block id=q" {
		t.Errorf("commentAt = %q %v", got, ok)
	}
	if trimBack([]string{"a", "", "# c"}, 3) != 1 || trimBack([]string{"a"}, 1) != 1 {
		t.Error("trimBack")
	}
	if nextEntryLine(nil, 1) != 0 {
		t.Error("nextEntryLine empty")
	}
}

func TestYAMLThroughSystem(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"config/auth.yaml": {Data: []byte("auth:\n  # ds:def id=motd-a2b6f8jk\n  motd: |\n    Hello\n    World\n  port: 8081   # ds:def id=port-b3c7g9kl\n")},
		"docs/a.md":        {Data: []byte("<!-- ds:block id=motd-a2b6f8jk -->\n\nPort [8081](ds:cfg?id=port-b3c7g9kl).\n")},
	}
	s, err := docsync.New(docsync.WithFS(fsys), docsync.WithExtractor(YAML()))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.Check(context.Background(), docsync.CheckOptions{})
	if err != nil || rep.ExitCode != 0 || rep.Scan.Tier["config/auth.yaml"] != "yaml" {
		t.Fatalf("check = %+v %v", rep.States, err)
	}
	out, _, err := s.Render(context.Background(), "docs/a.md", docsync.RenderOptions{})
	if err != nil || !strings.Contains(string(out), "```yaml\nHello\nWorld\n```") || !strings.Contains(string(out), "Port 8081.") {
		t.Errorf("render = %s %v", out, err)
	}
}
