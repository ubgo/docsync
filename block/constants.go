package block

// Rule: NO BARE STRINGS for any value with a closed set of choices. Every
// switch, map key, TSV column, and JSON field that names a kind, state, class,
// severity, carrier, or stability picks from the constants below. A typo is a
// compile error; a rename is one place. Each group has a *Values slice that is
// the canonical iteration order for reports, JSON enums, and tests.

// Kind says what a defined block is. It is decided by the extractor tier that
// bound the directive (docs/SPEC.md §7.1, §10) and stored in the ledger.
type Kind string

const (
	KindFunc      Kind = "func"      // a function or method from a grammar tier
	KindType      Kind = "type"      // a type, class, struct, interface, enum
	KindConst     Kind = "const"     // a const or var declaration
	KindStatement Kind = "stmt"      // a statement, e.g. a SQL statement
	KindKey       Kind = "key"       // a key in a structured file (yaml, toml, json, ini, env)
	KindLine      Kind = "line"      // a single line in a grammarless file
	KindSpan      Kind = "span"      // a line range in a grammarless file
	KindSection   Kind = "section"   // a markdown heading and its section
	KindParagraph Kind = "paragraph" // a markdown paragraph
	KindLinkText  Kind = "linktext"  // the text of an inline markdown def link, i.e. a fact
	KindElement   Kind = "element"   // an html/xml element
	KindFile      Kind = "file"      // a whole file, from pick=file on an asset
)

// KindValues is the canonical order for enumeration.
var KindValues = []Kind{KindFunc, KindType, KindConst, KindStatement, KindKey, KindLine, KindSpan, KindSection, KindParagraph, KindLinkText, KindElement, KindFile}

// Carrier says where a directive sat: which comment syntax or link form.
// Verbs restrict which carriers they accept (§9.9), and findings report it so
// a remedy can say "the link on line 13" versus "the comment on line 13".
type Carrier string

const (
	CarrierComment  Carrier = "comment"  // a host-language comment: // # -- /* */ <!-- -->
	CarrierBareLine Carrier = "bareline" // a bare `ds:...` line in a file with no comment syntax
	CarrierLink     Carrier = "link"     // a markdown link target `ds:verb?k=v`
	CarrierBlock    Carrier = "block"    // an html comment on its own line in markdown, i.e. block position
)

// CarrierValues is the canonical order.
var CarrierValues = []Carrier{CarrierComment, CarrierBareLine, CarrierLink, CarrierBlock}

// Stability is the `stability=` key on a def: which change classes flag prose
// (§20). The zero value is not valid; DefaultStability is the spec default.
type Stability string

const (
	StabilityFrozen   Stability = "frozen"   // any class but whitespace flags, and is an error on its own
	StabilityStable   Stability = "stable"   // default: any class but whitespace and comment flags
	StabilityAPI      Stability = "api"      // only signature, type, renamed, value flag
	StabilityVolatile Stability = "volatile" // nothing flags
)

// DefaultStability applies when a def has no `stability=`.
const DefaultStability = StabilityStable

// StabilityValues is the canonical order.
var StabilityValues = []Stability{StabilityFrozen, StabilityStable, StabilityAPI, StabilityVolatile}

// ParseStability validates a `stability=` value.
func ParseStability(s string) (Stability, bool) {
	for _, v := range StabilityValues {
		if string(v) == s {
			return v, true
		}
	}
	return "", false
}

// Class is one classified difference between two versions of a block (§20).
// A change carries one or more classes.
type Class string

const (
	ClassRenamed    Class = "renamed"    // symbol name changed
	ClassMoved      Class = "moved"      // file or line range changed, body identical
	ClassSignature  Class = "signature"  // parameters or return types changed
	ClassType       Class = "type"       // a type's shape changed: fields, variants
	ClassBody       Class = "body"       // statements changed, signature unchanged
	ClassComment    Class = "comment"    // comment lines only
	ClassWhitespace Class = "whitespace" // formatting only; never reported
	ClassValue      Class = "value"      // for facts: the picked value changed
	// ClassUnknown says the two versions differ but the older body was not
	// available to classify (§20). It is not a guess at what changed: it is
	// the honest statement that the difference is real and undescribed. It
	// arises when a citation drifted from a hash whose body is not in the
	// body store — pruned, never stored, or withheld because the block is
	// secret. It must never be substituted for ClassBody, because `api`
	// blocks do not flag on ClassBody and a silently-dropped signature
	// change is a wrong `ok`.
	ClassUnknown Class = "unknown"
)

// ClassValues is the canonical order.
var ClassValues = []Class{ClassRenamed, ClassMoved, ClassSignature, ClassType, ClassBody, ClassComment, ClassWhitespace, ClassValue, ClassUnknown}

// Flags reports whether a change with the given classes should flag prose
// under a stability policy. This is the single place the §20 table lives.
//
// Invariant: ClassWhitespace never flags under any policy, and ClassMoved
// alone never flags (a move is absorbed by the ledger), so a change whose
// only classes are those two is silent everywhere except `frozen`, where a
// move is still silent but any other touch is an error.
// dsself:def id=flags-zfrzu4vz owner=@docsync stability=stable
func Flags(st Stability, classes []Class) bool {
	for _, c := range classes {
		switch c {
		case ClassWhitespace, ClassMoved:
			continue
		case ClassComment:
			if st == StabilityFrozen {
				return true
			}
		case ClassSignature, ClassType, ClassRenamed, ClassValue:
			if st != StabilityVolatile {
				return true
			}
		case ClassBody:
			if st == StabilityFrozen || st == StabilityStable {
				return true
			}
		case ClassUnknown:
			// Deliberately wider than ClassBody: an unclassified difference
			// could be a signature change, and `api` exists to catch those.
			// Reporting a change the reader may judge irrelevant costs an
			// ack; staying silent costs a missed breaking change. Only
			// `volatile`, which opts out of all prose flagging, stays quiet.
			if st != StabilityVolatile {
				return true
			}
		}
	}
	return false
}
