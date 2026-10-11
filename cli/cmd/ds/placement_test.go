package main

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	docsync "github.com/ubgo/docsync"
	"github.com/ubgo/docsync/config"
	"github.com/ubgo/docsync/ext/structured"
	"github.com/ubgo/docsync/extract"
	"github.com/ubgo/docsync/scan"
)

// placementID is the id every generated citation names. No file defines it,
// so a reference that binds shows up as a reference whatever check would
// later say about it.
const placementID = "port-k7m2p4xq"

// placementLink is the link-form directive placed in every position.
const placementLink = "see [8081](ds:cfg?id=" + placementID + ")"

// placementBareRef and placementBareDef are whole-line directives with no
// comment around them, the shape an older `ds def` wrote and the shape a
// person pastes out of a page.
const (
	placementBareRef = "ds:block id=" + placementID
	placementBareDef = "ds:def id=" + placementID
)

// placementRule is what SPEC "Reading directives" says about one position.
type placementRule int

const (
	// mustBind: the position is a carrier, so exactly one reference.
	mustBind placementRule = iota
	// neverBinds: the position is data or broken syntax, so no reference.
	neverBinds
)

// placement is one position a directive can be put in, for one format.
type placement struct {
	pos  string
	src  string
	rule placementRule
	// damage: the scan must report the line as a directive that is not
	// inside a comment.
	damage bool
	// reported: the scan must report some problem on line 1, damage or
	// the def's own (a grammar tier that finds nothing to bind says so
	// first), so the line is never silently ignored.
	reported bool
}

// placementFormats is every file name the binary has a carrier rule for:
// each key of extract.Styles and extract.NoComments, made into a file name.
// Read from the tables, so a language added to either one joins the test
// with no edit here.
func placementFormats() []string {
	keys := map[string]bool{}
	for k := range extract.Styles {
		keys[k] = true
	}
	for k := range extract.NoComments {
		keys[k] = true
	}
	var out []string
	for k := range keys {
		if strings.HasPrefix(k, ".") {
			out = append(out, "f"+k)
		} else {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// structuredValue maps each structured tier the binary ships to the way a
// key is given a value in its format. Built from structured.All(), so a
// structured tier added there must be given one here or the test fails.
var structuredValue = map[string]string{"yaml": ": ", "toml": " = ", "hcl": " = "}

// placements generates every position for one file name from its row in
// the carrier tables. tier is the tier the binary routes it to.
func placements(name, tier string) []placement {
	if extract.HasNoComments(name) {
		// No comments and no prose: every word of a valid file is a value,
		// so nothing is a citation (bug 139), and a whole-line directive
		// breaks the file and is damage (bug 140).
		return []placement{
			{pos: "string value", src: "{\"note\": \"" + placementLink + "\"}\n", rule: neverBinds},
			{pos: "link line", src: placementLink + "\n", rule: neverBinds},
			{pos: "bare ref line", src: placementBareRef + "\n", rule: neverBinds, damage: true},
			{pos: "bare def line", src: placementBareDef + "\n{\"a\": 1}\n", rule: neverBinds, damage: true},
		}
	}
	st, _ := extract.StyleFor(name)
	// Prose hosts: plain text, and markup where a quote is punctuation.
	prose := st.NoStrings || st.Bare
	// Every comment is a carrier (SPEC §7.1), in every tier: the
	// structured tiers (YAML, TOML, HCL) used to read only the comment
	// form of a directive and dropped a link in a comment (bug 142).
	commentRule := mustBind
	var out []placement
	for _, p := range st.Line {
		out = append(out, placement{pos: "line comment " + p, src: p + " " + placementLink + "\n", rule: commentRule})
	}
	for _, pair := range [][2]string{{st.BlockOpen, st.BlockClose}, {st.AltOpen, st.AltClose}} {
		if pair[0] != "" {
			out = append(out, placement{pos: "block comment " + pair[0], src: pair[0] + " " + placementLink + " " + pair[1] + "\n", rule: commentRule})
		}
	}
	if sep, ok := structuredValue[tier]; ok {
		// A parsed YAML, TOML or HCL file is configuration: its values are
		// data and are not read for links, only its comments are (left as
		// it was when bug 142 made comments carriers). A YAML plain value
		// is a valid file too; a bare TOML or HCL one is not, and is read
		// line by line through the fallback tier, so it is not generated.
		out = append(out, placement{pos: "string value", src: "x" + sep + "\"" + placementLink + "\"\n", rule: neverBinds})
		if tier == "yaml" {
			out = append(out, placement{pos: "plain value", src: "x" + sep + placementLink + "\n", rule: neverBinds})
		}
		for _, p := range st.Line {
			out = append(out, placement{pos: "string holding a directive", src: "x" + sep + "\"" + p + " " + placementBareRef + "\"\n", rule: neverBinds})
		}
	} else {
		// A link outside a comment: prose in a prose host, where a quote is
		// punctuation; in code, a string literal is prose too (help text,
		// errors, UI and JSX text), so a link in one is a citation, and a
		// link in bare code is read as well, since `[x](ds:…)` is not valid
		// code in any shipped language and telling a multi-line string from
		// code needs a lexer per language (SPEC §36).
		out = append(out,
			placement{pos: "uncommented", src: "x = " + placementLink + "\n", rule: mustBind},
			placement{pos: "in a string", src: "x = \"" + placementLink + "\"\n", rule: mustBind},
		)
		for _, p := range st.Line {
			// A comment marker inside a string opens no comment, but the
			// link is still in a string, so it is still a citation.
			out = append(out, placement{pos: "string holding " + p, src: "x = \"" + p + " " + placementLink + "\"\n", rule: mustBind})
			if !prose {
				// A whole-line directive is one only in a comment: inside
				// a string it is fixture or example text (SPEC "Reading
				// directives").
				out = append(out, placement{pos: "string holding a " + p + " directive", src: "x = \"" + p + " " + placementBareRef + "\"\n", rule: neverBinds})
			}
		}
	}
	if st.Bare {
		// Plain text carries a directive as a line of its own.
		out = append(out, placement{pos: "bare ref line", src: placementBareRef + "\n", rule: mustBind})
	} else {
		// Anywhere else a whole-line directive outside a comment cites
		// nothing: it was a citation in .gitignore and .dockerignore,
		// which take # comments but are read by the text tier (bug 140).
		out = append(out, placement{pos: "bare ref line", src: placementBareRef + "\nx = 1\n", rule: neverBinds})
		// A def outside a comment in a format with comments is reported
		// (SPEC "A directive is only ever written as a comment"); the
		// config, markdown and document tiers ignored it (bug 143).
		out = append(out, placement{pos: "bare def line", src: placementBareDef + "\nx: 1\n", rule: neverBinds, reported: true})
	}
	return out
}

// TestDirectivePlacement is the property bugs 139 and 140 broke: a
// reference binds only in a position SPEC "Reading directives" makes real,
// and a whole-line directive in a format with no comment syntax is reported
// as damage, never read as a citation. Bug 139 was a link inside a JSON
// string, read as a citation because the text tier reads every line; bug
// 140 was the same file with the directive on a line of its own, and the
// same line in a .gitignore. Each was one format and one position, and the
// examples written before them covered the formats someone had in mind, so
// this test generates the cases instead: every file type in the carrier
// tables, crossed with every position, scanned through the binary's own
// tier order.
//
// The axes:
//   - format: every key of extract.Styles and extract.NoComments;
//   - position: each line-comment prefix and block-comment pair the format
//     has; uncommented; inside a string; inside a string that holds a
//     comment marker; a whole-line directive; and, for formats with no
//     comment syntax, a string value and a bare line.
//
// The rules are SPEC "Reading directives" and the decisions in SPEC §36:
// a link in a comment or a string literal is a citation, and one in bare
// code is read too; a whole-line directive is one only in a comment, never
// in a string; formats with no comment syntax hold data, not citations;
// the values of a parsed YAML, TOML or HCL file are data.
//
// promise:string-holds-no-directive
func TestDirectivePlacement(t *testing.T) {
	t.Parallel()
	reg := standardRegistry()
	formats := placementFormats()
	cases := 0
	for _, name := range formats {
		ex, err := reg.For(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, pl := range placements(name, ex.Name()) {
			cases++
			res := placementScan(t, name, pl.src)
			damage, reported := false, false
			for _, p := range res.Problems {
				if errors.Is(p.Err, scan.ErrBareDirective) {
					damage = true
				}
				if p.Pos.Start == 1 {
					reported = true
				}
			}
			refs := len(res.Refs)
			switch {
			case pl.rule == mustBind && refs != 1:
				t.Errorf("%s (%s) %s: %d references, want 1", name, ex.Name(), pl.pos, refs)
			case pl.rule == neverBinds && refs != 0:
				t.Errorf("%s (%s) %s: %d references, want none", name, ex.Name(), pl.pos, refs)
			}
			if pl.damage && !damage {
				t.Errorf("%s (%s) %s: not reported as a directive outside a comment", name, ex.Name(), pl.pos)
			}
			if pl.reported && !reported {
				t.Errorf("%s (%s) %s: silently ignored", name, ex.Name(), pl.pos)
			}
		}
	}
	// A table emptied by mistake must not pass on nothing.
	if cases < 300 {
		t.Errorf("generated %d cases; the carrier tables shrank or the generator broke", cases)
	}
	// Every structured tier the binary ships has a value form above.
	for _, ex := range structured.All() {
		if _, ok := structuredValue[ex.Name()]; !ok {
			t.Errorf("structured tier %s has no entry in structuredValue", ex.Name())
		}
	}
	t.Logf("%d formats, %d cases", len(formats), cases)
}

// placementScan scans one file with the binary's tiers and the default
// config, its code globs covering everything as `ds init` writes them.
func placementScan(t *testing.T, name, src string) scan.Result {
	t.Helper()
	c := config.Default()
	c.Scan.Code = []string{"**"}
	s, err := docsync.New(docsync.WithFS(fstest.MapFS{name: {Data: []byte(src)}}), docsync.WithConfig(c), docsync.WithRegistry(standardRegistry()))
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res
}
