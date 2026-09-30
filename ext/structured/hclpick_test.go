package structured

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/pick"
)

func TestHCLPicker(t *testing.T) {
	t.Parallel()
	p := HCLPicker()
	if p.Scheme() != "hcl" {
		t.Error("scheme")
	}
	src := "resource \"aws_instance\" \"web\" {\n  ami  = \"ami-123\"\n  tags = {\n    Name = \"web\"\n  }\n}\nregion = \"eu-west-1\"\n"
	if r, err := p.Pick("resource.aws_instance.web.ami", src); err != nil || r.Kind != pick.KindValue || r.Value != "ami-123" || r.Start != 2 {
		t.Errorf("ami = %+v %v", r, err)
	}
	if r, err := p.Pick("resource.aws_instance.web.tags", src); err != nil || r.Kind != pick.KindRange || r.Start != 3 || r.End != 5 || !strings.Contains(r.Text, "Name") {
		t.Errorf("tags = %+v %v", r, err)
	}
	if r, err := p.Pick("resource.aws_instance.web", src); err != nil || r.Kind != pick.KindRange || r.Start != 1 || r.End != 6 {
		t.Errorf("block = %+v %v", r, err)
	}
	if r, err := p.Pick("region", src); err != nil || r.Value != "eu-west-1" {
		t.Errorf("top-level = %+v %v", r, err)
	}
	if _, err := p.Pick("resource.nope", src); !errors.Is(err, ErrHCLPick) {
		t.Errorf("missing = %v", err)
	}
	if _, err := p.Pick("x", "resource {"); !errors.Is(err, ErrHCL) {
		t.Errorf("invalid = %v", err)
	}
	// Through the system: a remote def picking an HCL attribute.
	fsys := fstest.MapFS{
		"infra/main.tf": {Data: []byte(src)},
		"docs/a.md":     {Data: []byte("<!-- ds:def id=ami-a2b6f8jk file=infra/main.tf pick=hcl:resource.aws_instance.web.ami -->\n\nAMI [ami-123](ds:cfg?id=ami-a2b6f8jk).\n")},
	}
	s, err := docsync.New(docsync.WithFS(fsys), docsync.WithExtractor(HCL()), docsync.WithPicker(HCLPicker()))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.Check(context.Background(), docsync.CheckOptions{})
	if err != nil || rep.ExitCode != 0 {
		t.Fatalf("check = %+v %v", rep.States, err)
	}
	out, _, err := s.Render(context.Background(), "docs/a.md", docsync.RenderOptions{})
	if err != nil || !strings.Contains(string(out), "AMI ami-123.") {
		t.Errorf("render = %s %v", out, err)
	}
}
