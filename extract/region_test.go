package extract

import "testing"

// TestRegionIsTheScannersRule pins that the exported Region, which render
// uses to replace a repo-mode copy (bug 105), finds exactly what the
// scanner's region finds.
func TestRegionIsTheScannersRule(t *testing.T) {
	t.Parallel()
	lines := []string{"<!-- ds:block id=a-a2b6f8jk -->", "copy", "<!-- /ds:block hash=abc123 -->", "after"}
	r := Region(lines, 2, "ds")
	if r == nil || r.Start != 2 || r.End != 3 || r.Hash != "abc123" || r.Text != "copy" {
		t.Errorf("region = %+v", r)
	}
	if Region(lines, 4, "ds") != nil {
		t.Error("no closer, no region")
	}
}
