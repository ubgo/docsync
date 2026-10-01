package docsync

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ubgo/docsync/block"
	"github.com/ubgo/docsync/directive"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/id"
	"github.com/ubgo/docsync/scan"
)

// adoptLinkRE matches `[text](path#L10-L20)`, `[text](path#L10)`, and
// `[text](path#Symbol)` where path is repo-relative and not a URL. Group 1
// is the text, 2 the path, 3 the fragment.
var adoptLinkRE = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s:#]+)#([^)\s]+)\)`)

// resolveLink returns the repository path a link in doc points at, and whether
// a file is there.
//
// A relative link is resolved against the directory of the file that holds
// it, which is what it means when the page is rendered: `../README.md` in
// docs/a.md is README.md. adopt used to pass the link through unresolved, so
// every link written from a subdirectory -- to code as well as to other pages --
// reported "file not found" against a file that existed.
//
// When that finds nothing, the link is tried from the repository root, which
// is how adopt read every link before. Such a link is broken when the page is
// rendered, but adopting it replaces the path with a cite and so repairs the
// page; refusing it would only make an upgrade look like a regression. A link
// starting with `/` is root-relative by its own spelling. The returned path on
// failure is the one the page means, so the report names the right file.
func (s *System) resolveLink(doc, link string) (string, bool) {
	if strings.HasPrefix(link, "/") {
		p := path.Clean(strings.TrimPrefix(link, "/"))
		return p, s.isFile(p)
	}
	rel := path.Clean(path.Join(path.Dir(doc), link))
	if !escapesRoot(rel) && s.isFile(rel) {
		return rel, true
	}
	if root := path.Clean(link); !escapesRoot(root) && s.isFile(root) {
		return root, true
	}
	return rel, false
}

// escapesRoot reports a cleaned path that climbs out of the repository.
func escapesRoot(p string) bool { return p == ".." || strings.HasPrefix(p, "../") }

// isFile reports a regular file at p in the tree.
func (s *System) isFile(p string) bool {
	info, err := fs.Stat(s.fsys, p)
	return err == nil && !info.IsDir()
}

// isDocument reports a file the scan reads as prose: a link into one is
// navigation between pages, not a reference to code.
func (s *System) isDocument(p string) bool {
	ex, err := s.registry.For(p)
	if err != nil {
		return false
	}
	pt, ok := ex.(extract.ProseTier)
	return ok && pt.Prose()
}

// lineFragRE reads `L10` or `L10-L20`.
var lineFragRE = regexp.MustCompile(`^L(\d+)(?:-L(\d+))?$`)

// AdoptResult is what Adopt proposes.
type AdoptResult struct {
	Envelope
	// Edits insert defs into source and rewrite doc links to cites. Source
	// edits come first so the ids exist before the cites do.
	Edits []Edit `json:"edits"`
	// Adopted counts links converted.
	Adopted int `json:"adopted"`
	// Unresolved lists links whose target could not be located, with why.
	Unresolved []Unresolved `json:"unresolved"`
}

// Unresolved is a link Adopt left alone.
type Unresolved struct {
	Doc    string `json:"doc"`
	Line   int    `json:"line"`
	Target string `json:"target"`
	Reason string `json:"reason"`
}

// Adopt converts hand-written `path#L10-L20` and `path#symbol` links in
// scanned docs into defs and cites (§22 `ds adopt`). A link whose target
// already has a def gets a cite to the existing id; otherwise an id is
// minted and a directive edit is proposed above the block. Every edit is
// returned, none applied; the caller applies source edits first, then doc
// edits, then rescans.
func (s *System) Adopt(_ context.Context, res scan.Result) (AdoptResult, error) {
	out := AdoptResult{Envelope: s.envelope()}
	docs := map[string]bool{}
	for _, r := range res.Refs {
		docs[r.Pos.File] = true
	}
	for d := range res.Pages {
		docs[d] = true
	}
	// Docs with no directives at all are still candidates: walk the tier
	// map for markdown files.
	for f, tier := range res.Tier {
		if tier == (extract.Markdown{}).Name() {
			docs[f] = true
		}
	}
	names := make([]string, 0, len(docs))
	for d := range docs {
		names = append(names, d)
	}
	sort.Strings(names)
	// Existing defs by file and start line, so a second link to the same
	// block reuses the id, including ids minted earlier in this run.
	existing := map[string]string{}
	for _, b := range res.Defs {
		existing[b.Pos.File+":"+strconv.Itoa(b.Pos.Start)] = b.ID
	}
	var sourceEdits, docEdits []Edit
	// Insertions shift later lines in the same file; track offsets so two
	// defs in one file land where they were meant to.
	inserted := map[string][]int{}
	for _, doc := range names {
		raw, err := s.readSource(doc)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		// Links are found in the page's carrier view, so an example in a
		// code span or code block is left as the example it is. Offsets
		// match the raw line, which is what gets rewritten.
		prose := extract.CarrierLines(doc, lines)
		for i, l := range lines {
			var splices []splice
			for _, ix := range adoptLinkRE.FindAllStringSubmatchIndex(prose[i], -1) {
				text, link, frag := l[ix[2]:ix[3]], l[ix[4]:ix[5]], l[ix[6]:ix[7]]
				target := link + "#" + frag
				file, found := s.resolveLink(doc, link)
				if !found {
					out.Unresolved = append(out.Unresolved, Unresolved{Doc: doc, Line: i + 1, Target: target, Reason: "file not found: " + file})
					continue
				}
				// A named anchor into a document is a link to a heading: one page
				// pointing at another, which is what documentation is made of.
				// adopt binds docs to CODE, so it leaves these alone and says
				// nothing -- reporting each one buried the real findings under
				// nine lines on a single repository.
				if lineFragRE.FindStringSubmatch(frag) == nil && s.isDocument(file) {
					continue
				}
				tgt, err := parseFragment(frag)
				if err != nil {
					out.Unresolved = append(out.Unresolved, Unresolved{Doc: doc, Line: i + 1, Target: target, Reason: err.Error()})
					continue
				}
				src, err := s.readSource(file)
				if err != nil {
					out.Unresolved = append(out.Unresolved, Unresolved{Doc: doc, Line: i + 1, Target: target, Reason: "file not found: " + file})
					continue
				}
				var located block.Block
				if tgt.Line > 0 {
					located, err = extract.Enclosing(file, src, tgt.Line)
				} else {
					// A symbol is resolved by the tier that scans the file, as
					// `ds def path#Name` resolves it, so a link adopt accepts
					// names what the ledger will record.
					tgt.Prefix = s.cfg.Prefix
					located, err = s.locateSymbol(file, src, tgt)
				}
				if err != nil {
					out.Unresolved = append(out.Unresolved, Unresolved{Doc: doc, Line: i + 1, Target: target, Reason: err.Error()})
					continue
				}
				key := file + ":" + strconv.Itoa(located.Pos.Start)
				blockID, ok := existing[key]
				if !ok {
					if err := s.refuseGenerated(file); err != nil {
						out.Unresolved = append(out.Unresolved, Unresolved{Doc: doc, Line: i + 1, Target: target, Reason: err.Error()})
						continue
					}
					label := id.Slug(located.Symbol)
					if label == "" {
						label = fileLabel(file)
					}
					blockID, err = id.New(s.idcfg, label)
					if err != nil {
						return AdoptResult{}, err
					}
					existing[key] = blockID
					d := directive.Directive{Verb: extract.VerbDef, Args: map[string]string{block.KeyID: blockID}, Keys: []string{block.KeyID}}
					text, _ := directive.Format(s.cfg.Prefix, d)
					srcLines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
					edit, err := directiveEdit(file, srcLines, located, text)
					if err != nil {
						// A link into a file docsync cannot write a directive
						// into is left alone and reported with the reason,
						// never rewritten into invalid syntax.
						out.Unresolved = append(out.Unresolved, Unresolved{Doc: doc, Line: i + 1, Target: target, Reason: err.Error()})
						continue
					}
					// Shift by the number of earlier insertions above this line.
					shift := 0
					for _, at := range inserted[file] {
						if at <= edit.Line {
							shift++
						}
					}
					if edit.Old == "" {
						inserted[file] = append(inserted[file], edit.Line)
					}
					edit.Line += shift
					sourceEdits = append(sourceEdits, edit)
				}
				cite := directive.Directive{Verb: extract.VerbBlock, Args: map[string]string{block.KeyID: blockID}, Keys: []string{block.KeyID}}
				if lf := lineFragRE.FindStringSubmatch(frag); lf != nil && lf[2] != "" && located.Pos.End > located.Pos.Start {
					// A line range inside a larger block becomes lines=a-b
					// relative to the block, when it fits.
					a, _ := strconv.Atoi(lf[1])
					b, _ := strconv.Atoi(lf[2])
					// GitHub highlights #L20-L10 as #L10-L20, so a reversed
					// range is a real link; lines= must run forwards.
					a, b = min(a, b), max(a, b)
					if a >= located.Pos.Start && b <= located.Pos.End && (a != located.Pos.Start || b != located.Pos.End) {
						cite.Args["lines"] = fmt.Sprintf("%d-%d", a-located.Pos.Start+1, b-located.Pos.Start+1)
						cite.Keys = append(cite.Keys, "lines")
					}
				}
				splices = append(splices, splice{ix[0], ix[1], "[" + text + "](" + directive.FormatLink(s.cfg.Prefix, cite) + ")"})
				out.Adopted++
			}
			// By position, last first: replacing the first match of the
			// link's text rewrote an identical example earlier on the line.
			nw := l
			for k := len(splices) - 1; k >= 0; k-- {
				sp := splices[k]
				nw = nw[:sp.start] + sp.text + nw[sp.end:]
			}
			if nw != l {
				docEdits = append(docEdits, Edit{File: doc, Line: i + 1, Old: l, New: nw})
			}
		}
	}
	out.Edits = append(sourceEdits, docEdits...)
	return out, nil
}

// splice replaces line[start:end] with text.
type splice struct {
	start, end int
	text       string
}

// parseFragment turns `L10-L20` into a line target and anything else into a
// symbol target.
func parseFragment(frag string) (extract.Target, error) {
	if m := lineFragRE.FindStringSubmatch(frag); m != nil {
		n, _ := strconv.Atoi(m[1])
		if m[2] != "" {
			// A range is located by its first line in reading order, so
			// #L99-L4 finds the block #L4-L99 does.
			end, _ := strconv.Atoi(m[2])
			n = min(n, end)
		}
		if n < 1 {
			return extract.Target{}, fmt.Errorf("line %d is not a line", n)
		}
		return extract.Target{Line: n}, nil
	}
	if strings.ContainsAny(frag, " /?&=") {
		return extract.Target{}, fmt.Errorf("fragment %q is not a symbol", frag)
	}
	return extract.Target{Symbol: frag}, nil
}
