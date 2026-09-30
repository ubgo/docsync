// Package duration parses the duration shape docsync writes in directives
// and in configuration: an integer followed by a unit.
//
// It lives here, below both, because two callers need the same grammar and
// neither should import the other for it: the directive verbs read
// `expires=` and `review_every=`, and `[check] snapshot_max_age` reads the
// same shape. A config package that had to import the checker to validate
// one key would pull the whole evaluation tree behind a leaf.
package duration

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

// Parse reads an integer followed by d (days), w (weeks), h (hours), or m
// (minutes). Months and years are not offered because their length is
// ambiguous; write 90d, not 3m.
func Parse(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("bad duration %q", s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 0 {
		return 0, fmt.Errorf("bad duration %q", s)
	}
	unit, ok := units[s[len(s)-1]]
	if !ok {
		return 0, fmt.Errorf("bad duration %q: unit must be d, w, h, or m", s)
	}
	// time.Duration holds about 292 years. Past that the multiplication
	// wraps, and 106752d read as a negative duration: a page that is always
	// due, or never, with no error to say why.
	if time.Duration(n) > math.MaxInt64/unit {
		return 0, fmt.Errorf("bad duration %q: longer than %v", s, time.Duration(math.MaxInt64).Truncate(24*time.Hour))
	}
	return time.Duration(n) * unit, nil
}

// units are the suffixes Parse accepts and what each one is.
var units = map[byte]time.Duration{
	'd': 24 * time.Hour,
	'w': 7 * 24 * time.Hour,
	'h': time.Hour,
	'm': time.Minute,
}
