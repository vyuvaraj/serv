package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"servtrace/pkg/store"
)

func TestTuningRecommendations(t *testing.T) {
	ts := store.NewStore(100)
	srv := NewServer(ts)

	// Inject a slow trace (>200ms)
	now := time.Now().UnixNano()
	ts.AddSpans([]store.Span{
		{
			TraceID:   "slow-trace-id",
			SpanID:    "span-1",
			Name:      "/api/slow-endpoint",
			Service:   "test-service",
			StartTime: now,
			EndTime:   now + int64(300*time.Millisecond), // 300ms
			Status:    0,
		},
		{
			TraceID:   "slow-trace-id",
			SpanID:    "span-2",
			ParentSpanID: "span-1",
			Name:      "SELECT * FROM users",
			Service:   "test-db",
			StartTime: now,
			EndTime:   now + int64(250*time.Millisecond), // 250ms
			Status:    0,
		},
	})

	req := httptest.NewRequest("GET", "/api/v1/tuning/recommendations", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var recs []map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &recs); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(recs) < 2 {
		t.Fatalf("expected at least 2 recommendations, got %d", len(recs))
	}

	foundCache := false
	foundIndex := false
	for _, rec := range recs {
		if rec["type"] == "cache" && rec["target"] == "/api/slow-endpoint" {
			foundCache = true
		}
		if rec["type"] == "index" && rec["target"] == "SELECT * FROM users" {
			foundIndex = true
		}
	}

	if !foundCache {
		t.Errorf("expected recommendation to cache '/api/slow-endpoint'")
	}
	if !foundIndex {
		t.Errorf("expected recommendation to index 'SELECT * FROM users'")
	}
}
