package structured

import (
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/ubgo/docsync"
	"github.com/ubgo/docsync/pick"
)

// The `hcl:` pick scheme (§10): `pick=hcl:resource.aws_instance.web.ami`
// narrows a remote HCL file to one attribute (a value when it is a
// literal, the attribute's lines otherwise) or, when the path names a
// block, to that block's lines. Paths are the ones the HCL tier gives
// blocks as symbols, so `ds facts` and `pick=` agree.

// ErrHCLPick is returned when the path names nothing in the file.
var ErrHCLPick = errors.New("structured: hcl pick path not found")

// pickHCLFile is the name the parser reports in diagnostics.
const pickHCLFile = "pick.hcl"

type hclPicker struct{}

// HCLPicker returns the `hcl:` scheme for docsync.WithPicker.
func HCLPicker() docsync.Picker { return hclPicker{} }

func (hclPicker) Scheme() string { return "hcl" }

// Pick resolves arg against content.
func (hclPicker) Pick(arg, content string) (pick.Result, error) {
	src := []byte(content)
	file, diags := hclsyntax.ParseConfig(src, pickHCLFile, hcl.InitialPos)
	if diags.HasErrors() {
		return pick.Result{}, fmt.Errorf("%w: %s", ErrHCL, diags.Error())
	}
	body, _ := file.Body.(*hclsyntax.Body)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for _, e := range hclEntries(body, "", src) {
		if e.path != arg {
			continue
		}
		text := strings.Join(lines[e.start-1:e.end], "\n")
		if e.isScalar {
			return pick.Result{Kind: pick.KindValue, Value: e.scalarVal, Text: text, Start: e.start, End: e.end}, nil
		}
		return pick.Result{Kind: pick.KindRange, Text: text, Start: e.start, End: e.end}, nil
	}
	return pick.Result{}, fmt.Errorf("%w: %q", ErrHCLPick, arg)
}
