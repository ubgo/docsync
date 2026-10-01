package block

import (
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ValueType is a def's declared `type=` (§9.1, §11): what shape its value
// must have. A scan checks the picked value against it, so a fact that stops
// being the thing its sentences say it is -- a port that became a word, a
// version that lost its patch number -- is a finding, not a silent pass.
// Before, type= was accepted and checked nothing (bug 54).
type ValueType string

// The value types, as §9.1 lists them. `url` also decides rendering: an
// inline def of that type becomes a link.
const (
	TypeURL      ValueType = "url"
	TypeEmail    ValueType = "email"
	TypeInt      ValueType = "int"
	TypeFloat    ValueType = "float"
	TypePercent  ValueType = "percent"
	TypeSemver   ValueType = "semver"
	TypeDate     ValueType = "date"
	TypeDuration ValueType = "duration"
	TypeHost     ValueType = "host"
	TypeOpRef    ValueType = "opref"
)

// ValueTypeValues is the canonical order, for messages and docs.
var ValueTypeValues = []ValueType{TypeURL, TypeEmail, TypeInt, TypeFloat, TypePercent, TypeSemver, TypeDate, TypeDuration, TypeHost, TypeOpRef}

// dateLayout is the one date form directives use (`reviewed=2026-09-06`).
const dateLayout = "2006-01-02"

const number = `[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)`

var (
	intRE     = regexp.MustCompile(`^[+-]?[0-9]+$`)
	floatRE   = regexp.MustCompile(`^` + number + `(?:[eE][+-]?[0-9]+)?$`)
	percentRE = regexp.MustCompile(`^` + number + `\s?%$`)
	semverRE  = regexp.MustCompile(`^v?(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	// A duration is written for a reader: `30 days`, `90d`, `1.5 hours`.
	durationRE = regexp.MustCompile(`(?i)^[0-9]+(?:\.[0-9]+)?\s*(?:ms|milliseconds?|s|secs?|seconds?|m|mins?|minutes?|h|hrs?|hours?|d|days?|w|weeks?|months?|y|years?)$`)
	emailRE    = regexp.MustCompile(`^[^@\s]+@[^@\s.]+(?:\.[^@\s.]+)+$`)
	hostRE     = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*(?::[0-9]+)?$`)
	// A 1Password secret reference: op://vault/item[/section]/field.
	opRefRE = regexp.MustCompile(`^op://[^/\s]+/[^/\s]+(?:/[^/\s]+)+$`)
)

// Known reports whether t is one of ValueTypeValues.
func (t ValueType) Known() bool {
	for _, v := range ValueTypeValues {
		if t == v {
			return true
		}
	}
	return false
}

// Accepts reports whether value has the shape t declares. An unknown type
// accepts nothing; the scan reports it as unknown rather than as a mismatch.
func (t ValueType) Accepts(value string) bool {
	v := strings.TrimSpace(value)
	switch t {
	case TypeURL:
		u, err := url.Parse(v)
		return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
	case TypeEmail:
		return emailRE.MatchString(v)
	case TypeInt:
		return intRE.MatchString(v)
	case TypeFloat:
		return floatRE.MatchString(v)
	case TypePercent:
		return percentRE.MatchString(v)
	case TypeSemver:
		return semverRE.MatchString(v)
	case TypeDate:
		_, err := time.Parse(dateLayout, v)
		return err == nil
	case TypeDuration:
		return durationRE.MatchString(v)
	case TypeHost:
		return hostRE.MatchString(v)
	case TypeOpRef:
		return opRefRE.MatchString(v)
	}
	return false
}
