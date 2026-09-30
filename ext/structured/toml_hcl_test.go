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

const tomlDoc = `# top
title = "x"   # ds:def id=title-a2b6f8jk
n = 5
# ds:def id=arr-b3c7g9kl
arr = [
  1,
  2,
]
ml = """
line1
line2"""   # ds:def id=ml-c4d8h2lm

# ds:def id=server-d5e9j3mn
[server]
# ds:def id=port-e6f2k4np
#   owner=@ops
port = 8081
hosts = ["a", "b"]   # ds:def id=hosts-f7g3l5pq

# trailing comment
[server.tls]
enabled = true   # ds:def id=tls-g8h4m6qr
# ds:def id=item0-h2j5k7rs
[[items]]
name = "one"
[[items]]
name = "two"   # ds:def id=two-j3k6m8st
inline = { a = 1, b = 2 }   # ds:def id=inline-k4m7n9tu
# ds:block id=port-e6f2k4np
# ds:def
# ds:def id=remote-m5n8p2uv file=package.json pick=json:version
# ds:def id=tail-n6p9q3vw
`

func TestTOML(t *testing.T) {
	t.Parallel()
	tier := TOML()
	if tier.Name() != "toml" || !tier.Match("a/B.TOML") || tier.Match("a.yaml") {
		t.Error("name/match")
	}
	f := tier.Extract("c.toml", []byte(tomlDoc), "ds")
	want := map[string]struct {
		symbol, content string
		pos             block.Position
	}{
		"title-a2b6f8jk":  {"title", "x", block.Position{Start: 2, End: 2}},
		"arr-b3c7g9kl":    {"arr", "arr = [\n  1,\n  2,\n]", block.Position{Start: 5, End: 8}},
		"ml-c4d8h2lm":     {"ml", "line1\nline2", block.Position{Start: 9, End: 11}},
		"server-d5e9j3mn": {"server", "[server]\n# ds:def id=port-e6f2k4np\n#   owner=@ops\nport = 8081\nhosts = [\"a\", \"b\"]   # ds:def id=hosts-f7g3l5pq", block.Position{Start: 14, End: 18}},
		"port-e6f2k4np":   {"server.port", "8081", block.Position{Start: 17, End: 17}},
		"hosts-f7g3l5pq":  {"server.hosts", "hosts = [\"a\", \"b\"]   # ds:def id=hosts-f7g3l5pq", block.Position{Start: 18, End: 18}},
		"tls-g8h4m6qr":    {"server.tls.enabled", "true", block.Position{Start: 22, End: 22}},
		"item0-h2j5k7rs":  {"items[0]", "[[items]]\nname = \"one\"", block.Position{Start: 24, End: 25}},
		"two-j3k6m8st":    {"items[1].name", "two", block.Position{Start: 27, End: 27}},
		"inline-k4m7n9tu": {"items[1].inline", "inline = { a = 1, b = 2 }   # ds:def id=inline-k4m7n9tu", block.Position{Start: 28, End: 28}},
	}
	for id, w := range want {
		b, ok := find(f.Defs, id)
		if !ok || b.Symbol != w.symbol || b.Content != w.content || b.Pos != w.pos || b.Kind != block.KindKey {
			t.Errorf("%s = %+v (found %v)", id, b, ok)
		}
	}
	port, _ := find(f.Defs, "port-e6f2k4np")
	if port.Owner() != "@ops" || port.DirectivePos != (block.Position{Start: 15, End: 16}) {
		t.Errorf("port directive = %+v", port)
	}
	if len(f.Defs) != 11 || !f.Defs[10].Remote {
		t.Errorf("defs = %d", len(f.Defs))
	}
	if len(f.Refs) != 1 || f.Refs[0].Reference.ID != "port-e6f2k4np" {
		t.Errorf("refs = %+v", f.Refs)
	}
	var noID, nothing bool
	for _, p := range f.Problems {
		noID = noID || errors.Is(p.Err, extract.ErrNoID)
		nothing = nothing || errors.Is(p.Err, extract.ErrNothingToBind)
	}
	if len(f.Problems) != 2 || !noID || !nothing {
		t.Errorf("problems = %+v", f.Problems)
	}
	// Invalid TOML falls back to line mode and says so.
	f = tier.Extract("bad.toml", []byte("a = [1,\nport = 8081   # ds:def id=port-a2b6f8jk\n"), "ds")
	if len(f.Defs) != 1 || len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, ErrTOML) {
		t.Errorf("fallback = %+v %+v", f.Defs, f.Problems)
	}
	// An empty table at the end of the file, a CRLF file, and two headers
	// back to back.
	f = tier.Extract("e.toml", []byte("# ds:def id=t-a2b6f8jk\r\n[empty]\r\n# ds:def id=u-b3c7g9kl\r\n[a]\r\n[b]\r\n"), "ds")
	if b, ok := find(f.Defs, "t-a2b6f8jk"); !ok || b.Pos != (block.Position{Start: 2, End: 2}) || b.Content != "[empty]" {
		t.Errorf("empty table = %+v", b)
	}
	if b, ok := find(f.Defs, "u-b3c7g9kl"); !ok || b.Pos != (block.Position{Start: 4, End: 4}) {
		t.Errorf("adjacent headers = %+v", b)
	}
	// A trailing directive on a line no entry starts on and nothing spans
	// is nothing to bind.
	f = tier.Extract("x.toml", []byte("a = 1\n\n# ds:def id=v-c4d8h2lm\n"), "ds")
	if len(f.Problems) != 1 {
		t.Errorf("no entry = %+v", f)
	}
	if f := tier.Extract("n.toml", nil, "ds"); len(f.Defs)+len(f.Problems) != 0 {
		t.Errorf("empty file = %+v", f)
	}
}

const hclDoc = `# ds:def id=res-a2b6f8jk
resource "aws_instance" "web" {
  ami           = "ami-123"   # ds:def id=ami-b3c7g9kl
  count         = 2           // ds:def id=count-c4d8h2lm
  name          = "web-${var.env}"   # ds:def id=name-d5e9j3mn
  tags = {                    # ds:def id=tags-e6f2k4np
    Name = "web"
  }
  url = "https://example.com" # ds:def id=url-f7g3l5pq

  # ds:def id=ebs-g8h4m6qr
  ebs_block_device {
    size = 10   # ds:def id=size-h2j5k7rs
  }
}

# ds:def id=var-j3k6m8st
variable "env" {
  default = "prod"
}
# ds:block id=ami-b3c7g9kl
# ds:def id=tail-k4m7n9tu
`

func TestHCL(t *testing.T) {
	t.Parallel()
	tier := HCL()
	if tier.Name() != "hcl" || !tier.Match("main.tf") || !tier.Match("x.HCL") || !tier.Match("a.tfvars") || tier.Match("a.toml") {
		t.Error("name/match")
	}
	f := tier.Extract("main.tf", []byte(hclDoc), "ds")
	want := map[string]struct {
		symbol, content string
		pos             block.Position
	}{
		"res-a2b6f8jk":   {"resource.aws_instance.web", "resource \"aws_instance\" \"web\" {", block.Position{Start: 2, End: 15}},
		"ami-b3c7g9kl":   {"resource.aws_instance.web.ami", "ami-123", block.Position{Start: 3, End: 3}},
		"count-c4d8h2lm": {"resource.aws_instance.web.count", "2", block.Position{Start: 4, End: 4}},
		"name-d5e9j3mn":  {"resource.aws_instance.web.name", "  name          = \"web-${var.env}\"   # ds:def id=name-d5e9j3mn", block.Position{Start: 5, End: 5}},
		"tags-e6f2k4np":  {"resource.aws_instance.web.tags", "  tags = {                    # ds:def id=tags-e6f2k4np\n    Name = \"web\"\n  }", block.Position{Start: 6, End: 8}},
		"url-f7g3l5pq":   {"resource.aws_instance.web.url", "https://example.com", block.Position{Start: 9, End: 9}},
		"ebs-g8h4m6qr":   {"resource.aws_instance.web.ebs_block_device", "  ebs_block_device {\n    size = 10   # ds:def id=size-h2j5k7rs\n  }", block.Position{Start: 12, End: 14}},
		"size-h2j5k7rs":  {"resource.aws_instance.web.ebs_block_device.size", "10", block.Position{Start: 13, End: 13}},
		"var-j3k6m8st":   {"variable.env", "variable \"env\" {\n  default = \"prod\"\n}", block.Position{Start: 18, End: 20}},
	}
	for id, w := range want {
		b, ok := find(f.Defs, id)
		if !ok || b.Symbol != w.symbol || b.Pos != w.pos || !strings.HasPrefix(b.Content, w.content) {
			t.Errorf("%s = %+v (found %v)", id, b, ok)
		}
	}
	if len(f.Defs) != 9 || len(f.Refs) != 1 || len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, extract.ErrNothingToBind) {
		t.Errorf("counts = %d defs %d refs %+v", len(f.Defs), len(f.Refs), f.Problems)
	}
	// Invalid HCL falls back to the heuristic code tier.
	f = tier.Extract("bad.tf", []byte("resource \"a\" {\n# ds:def id=x-a2b6f8jk\nvalue = 1\n"), "ds")
	if len(f.Defs) != 1 || len(f.Problems) != 1 || !errors.Is(f.Problems[0].Err, ErrHCL) {
		t.Errorf("fallback = %+v %+v", f.Defs, f.Problems)
	}
	if got, ok := hclLiteral(nil, nil); ok || got != "" {
		t.Error("nil expression is no literal")
	}
}

func TestTOMLAndHCLThroughSystem(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"config/app.toml": {Data: []byte("[server]\nport = 8081   # ds:def id=port-a2b6f8jk\n")},
		"infra/main.tf":   {Data: []byte("resource \"aws_instance\" \"web\" {\n  ami = \"ami-123\"   # ds:def id=ami-b3c7g9kl\n}\n")},
		"docs/a.md":       {Data: []byte("Port [8081](ds:cfg?id=port-a2b6f8jk) and AMI [ami-123](ds:cfg?id=ami-b3c7g9kl).\n")},
	}
	s, err := docsync.New(docsync.WithFS(fsys), docsync.WithExtractor(TOML(), HCL()))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.Check(context.Background(), docsync.CheckOptions{})
	if err != nil || rep.ExitCode != 0 || rep.Scan.Tier["config/app.toml"] != "toml" || rep.Scan.Tier["infra/main.tf"] != "hcl" {
		t.Fatalf("check = %+v %v %v", rep.States, rep.Scan.Tier, err)
	}
	out, _, err := s.Render(context.Background(), "docs/a.md", docsync.RenderOptions{})
	if err != nil || !strings.Contains(string(out), "Port 8081 and AMI ami-123.") {
		t.Errorf("render = %s %v", out, err)
	}
	if len(All()) != 3 {
		t.Error("All")
	}
}
