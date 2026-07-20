package analytics

import (
	"testing"
	"time"
)

func TestQueryMetricFields(t *testing.T) {
	m := QueryMetric{
		Count:        5,
		TotalLatency: 150,
	}
	if m.Count != 5 || m.TotalLatency != 150 {
		t.Errorf("incorrect QueryMetric fields values: %+v", m)
	}
}

func TestCachedResultFields(t *testing.T) {
	now := time.Now()
	res := CachedResult{
		Rows:      []map[string]interface{}{{"id": 1}},
		CachedAt:  now,
		ExpiresAt: now.Add(5 * time.Minute),
	}
	if len(res.Rows) != 1 || res.CachedAt != now || res.ExpiresAt.Before(now) {
		t.Errorf("incorrect CachedResult fields: %+v", res)
	}
}

func TestCachedResultExpiration(t *testing.T) {
	now := time.Now()
	res := CachedResult{
		ExpiresAt: now.Add(-1 * time.Second),
	}
	if !res.ExpiresAt.Before(now) {
		t.Error("expected expired CachedResult")
	}
}
