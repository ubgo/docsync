package cli

import (
	"encoding/json"
	"fmt"
)

// MetricsFile accumulates what the agent surface served (§26.11): bytes
// handed out by `context` and `read` against the bytes of the files those
// items came from. `report --metrics` prints the ratio.
const MetricsFile = "metrics.json"

type metrics struct {
	Served int64 `json:"served_bytes"`
	Source int64 `json:"source_bytes"`
}

// LoadMetrics reads the totals; missing means zero.
func (s *Store) LoadMetrics() (served, source int64, err error) {
	raw, ok, err := s.read(MetricsFile)
	if err != nil || !ok {
		return 0, 0, err
	}
	var m metrics
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, 0, fmt.Errorf("%s: %w", MetricsFile, err)
	}
	return m.Served, m.Source, nil
}

// AddMetrics adds one served item to the totals.
func (s *Store) AddMetrics(served, source int64) error {
	cur, src, err := s.LoadMetrics()
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(metrics{Served: cur + served, Source: src + source})
	return s.Write(MetricsFile, raw)
}
