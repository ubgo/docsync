package duration

import (
	"testing"
	"time"
)

// TestParse pins the grammar two callers share: directive durations
// (`expires=`, `review_every=`) and `[check] snapshot_max_age`. Months and
// years are rejected on purpose, because their length is ambiguous.
func TestParse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"6h", 6 * time.Hour},
		{"90m", 90 * time.Minute},
		{"0d", 0},
	} {
		got, err := Parse(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("Parse(%q) = %v %v, want %v", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "d", "30", "30y", "30M", "-5d", "thirtyd", "3 0d"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) must fail", bad)
		}
	}
}
