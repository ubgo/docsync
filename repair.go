package docsync

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/scan"
)

// RepairResult is what Repair proposes: edits that mend each damaged
// directive, and the damage it could not reach.
type RepairResult struct {
	Envelope
	// Edits either comment a bare directive in the file's own syntax, which
	// keeps its id so every citation still resolves, or -- in a format with no
	// comment syntax -- delete it. A delete removes a def that was never valid
	// there; its citations then report the id missing, which is the truth.
	Edits []Edit `json:"edits"`
	// Manual is damage no edit could be computed for: a file that could not be
	// read back when the repair was built.
	Manual []ManualFix `json:"manual"`
}

// ManualFix is one line Repair found and left for a person.
type ManualFix struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// Repair proposes the fix for every directive written as a bare line into a
// file whose format does not accept one -- the damage `ds def` left behind
// before it learned to refuse. It returns edits and never writes (§22).
//
// A format with comments gets the line commented: a commented directive is
// still a def, so its id and every sentence citing it keep working, and the
// file parses again. A format with no comment syntax -- JSON, CSV, go.sum --
// has nothing to comment into, so the line is deleted. That delete is
// reversible like every other source write: it carries the line that followed
// it, so `ds undo` can find the place and put it back, and refuses if the file
// has moved around it since.
//
// Edits in a file are ordered bottom-up, so applying them in order never
// shifts a line a later edit points at. A directive continued onto following
// lines is mended whole; a deleted one records as its Next the first line
// after it that survives, since the line straight after may be going too.
func (s *System) Repair(res scan.Result) RepairResult {
	out := RepairResult{Envelope: s.envelope()}
	byFile := map[string][]block.Block{}
	var files []string
	for _, d := range res.Defs {
		if d.Carrier != block.CarrierBareLine {
			continue
		}
		p := d.DirectivePos.File
		if _, seen := byFile[p]; !seen {
			files = append(files, p)
		}
		byFile[p] = append(byFile[p], d)
	}
	sort.Strings(files)
	for _, p := range files {
		st, known := extract.StyleFor(p)
		remove := extract.HasNoComments(p)
		if !remove && (!known || st.Bare) {
			// Plain text carries a directive as a bare line correctly, and an
			// unknown format cannot be judged either way: neither is damage.
			continue
		}
		src, err := s.readSource(p)
		if err != nil {
			// The scan read this file a moment ago. If it cannot be read now
			// it is named for a person rather than failing the whole repair
			// or being skipped: damage nobody is told about is the failure
			// this command exists to end.
			for _, d := range byFile[p] {
				out.Manual = append(out.Manual, ManualFix{File: p, Line: d.DirectivePos.Start, Reason: "could not be read to repair: " + err.Error()})
			}
			continue
		}
		lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
		// Every line to mend, including a directive's continuation lines.
		mend := map[int]bool{}
		for _, d := range byFile[p] {
			for n := d.DirectivePos.Start; n <= d.DirectivePos.End && n <= len(lines); n++ {
				mend[n] = true
			}
		}
		order := make([]int, 0, len(mend))
		for n := range mend {
			order = append(order, n)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(order)))
		for _, n := range order {
			old := lines[n-1]
			if remove {
				next := ""
				for m := n + 1; m <= len(lines); m++ {
					if !mend[m] {
						next = lines[m-1]
						break
					}
				}
				out.Edits = append(out.Edits, Edit{File: p, Line: n, Old: old, Delete: true, Next: next})
				continue
			}
			body := strings.TrimLeft(old, " \t")
			indent := old[:len(old)-len(body)]
			nw := indent + st.BlockOpen + " " + body + " " + st.BlockClose
			if len(st.Line) > 0 {
				nw = indent + st.Line[0] + " " + body
			}
			out.Edits = append(out.Edits, Edit{File: p, Line: n, Old: old, New: nw})
		}
	}
	return out
}

// String is the one-line form `ds repair` prints for a manual fix.
func (m ManualFix) String() string { return fmt.Sprintf("%s:%d  %s", m.File, m.Line, m.Reason) }
