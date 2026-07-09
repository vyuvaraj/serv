package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"servtrace/pkg/store"
)

func TestTraceComparison(t *testing.T) {
	ts := store.NewStore(100)
	srv := NewServer(ts)

	now := time.Now().UnixNano()

	// Trace A: 100ms, 1 span
	ts.AddSpans([]store.Span{
		{
			TraceID:   "trace-a",
			SpanID:    "span-a1",
			Name:      "root-a",
			Service:   "service-a",
			StartTime: now,
			EndTime:   now + int64(100*time.Millisecond),
			Status:    1,
		},
	})

	// Trace B: 250ms, 2 spans (one error)
	ts.AddSpans([]store.Span{
		{
			TraceID:   "trace-b",
			SpanID:    "span-b1",
			Name:      "root-b",
			Service:   "service-a",
			StartTime: now,
			EndTime:   now + int64(250*time.Millisecond),
			Status:    1,
		},
		{
			TraceID:      "trace-b",
			SpanID:       "span-b2",
			ParentSpanID: "span-b1",
			Name:         "child-b",
			Service:      "service-b",
			StartTime:    now + int64(50*time.Millisecond),
			EndTime:      now + int64(200*time.Millisecond), // 150ms duration
			Status:       2, // Error
		},
	})

	req := httptest.NewRequest("GET", "/api/v1/traces/compare?a=trace-a&b=trace-b", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var comp TraceComparison
	if err := json.Unmarshal(w.Body.Bytes(), &comp); err != nil {
		t.Fatalf("failed to decode comparison response: %v", err)
	}

	if comp.TraceA.TraceID != "trace-a" || comp.TraceB.TraceID != "trace-b" {
		t.Errorf("trace IDs mismatch: %s vs %s", comp.TraceA.TraceID, comp.TraceB.TraceID)
	}

	if comp.DurationDiffMs != 150.0 {
		t.Errorf("expected duration diff 150ms, got %f", comp.DurationDiffMs)
	}

	if comp.SpanCountDiff != 1 {
		t.Errorf("expected span count diff 1, got %d", comp.SpanCountDiff)
	}

	if comp.ErrorCountDiff != 1 {
		t.Errorf("expected error count diff 1, got %d", comp.ErrorCountDiff)
	}

	// service-b duration in trace-b should be 150ms
	if val := comp.TraceB.Services["service-b"]; val != 150.0 {
		t.Errorf("expected service-b duration in trace-b to be 150ms, got %f", val)
	}
}

func TestRetentionPolicies(t *testing.T) {
	os.Setenv("SERV_TRACE_DEFAULT_TTL", "10ms")
	os.Setenv("SERV_TRACE_TTL_KEEP_SERVICE", "10m")
	defer func() {
		os.Unsetenv("SERV_TRACE_DEFAULT_TTL")
		os.Unsetenv("SERV_TRACE_TTL_KEEP_SERVICE")
	}()

	ts := store.NewStore(100)

	// Keep default TTL very small so it expires.
	// But let's check custom service keeps it.
	now := time.Now().UnixNano()
	oldTime := now - int64(5*time.Second) // 5 seconds ago

	ts.AddSpans([]store.Span{
		{
			TraceID:   "expired-trace",
			SpanID:    "span-exp",
			Name:      "expired-root",
			Service:   "expire-service",
			StartTime: oldTime,
			EndTime:   oldTime + int64(10*time.Millisecond),
			Status:    1,
		},
		{
			TraceID:   "kept-trace",
			SpanID:    "span-keep",
			Name:      "keep-root",
			Service:   "keep-service",
			StartTime: oldTime,
			EndTime:   oldTime + int64(10*time.Millisecond),
			Status:    1,
		},
	})

	// Run CleanExpiredTraces
	ts.CleanExpiredTraces()

	summaries := ts.ListTraces()
	foundKept := false
	foundExpired := false

	for _, s := range summaries {
		if s.TraceID == "kept-trace" {
			foundKept = true
		}
		if s.TraceID == "expired-trace" {
			foundExpired = true
		}
	}

	if !foundKept {
		t.Errorf("expected 'kept-trace' to remain due to longer TTL config")
	}
	if foundExpired {
		t.Errorf("expected 'expired-trace' to be evicted due to short default TTL config")
	}
}

func TestPerServiceSamplingRate(t *testing.T) {
	os.Setenv("SERV_TRACE_SAMPLING_RATE_HIGH_SERVICE", "100")
	os.Setenv("SERV_TRACE_SAMPLING_RATE_LOW_SERVICE", "0")
	defer func() {
		os.Unsetenv("SERV_TRACE_SAMPLING_RATE_HIGH_SERVICE")
		os.Unsetenv("SERV_TRACE_SAMPLING_RATE_LOW_SERVICE")
	}()

	ts := store.NewStore(100)
	now := time.Now().UnixNano()

	// High service - rate is 100%, so we should always sample.
	ts.AddSpans([]store.Span{
		{
			TraceID:   "trace-high",
			SpanID:    "span-high",
			Name:      "high-root",
			Service:   "high-service",
			StartTime: now,
			EndTime:   now + int64(10*time.Millisecond),
			Status:    1,
		},
	})

	// Low service - rate is 0%, so we should not sample.
	ts.AddSpans([]store.Span{
		{
			TraceID:   "trace-low",
			SpanID:    "span-low",
			Name:      "low-root",
			Service:   "low-service",
			StartTime: now,
			EndTime:   now + int64(10*time.Millisecond),
			Status:    1,
		},
	})

	// Clean up to trigger tail-based evict checks or check the sampled map.
	// But let's check the eviction list directly if we evict them, or trace tree which is only built if the trace spans exist.
	// Wait, Store.spans keeps spans in memory, but eviction archives it if sampled is true.
	// We can check GetTraceTree returns tree, and we can check s.sampled map indirectly or check via OnEvict.
	// Actually, let's look at s.sampled map via a test check or verify that eviction triggers OnEvict only for trace-high.
	evictedHigh := false
	evictedLow := false
	ts.OnEvict = func(traceID string, spans []store.Span) {
		if traceID == "trace-high" {
			evictedHigh = true
		}
		if traceID == "trace-low" {
			evictedLow = true
		}
	}

	// Trigger eviction by forcing limit (limit is 100 in store, let's create a store with limit 1)
	tsSmall := store.NewStore(1)
	tsSmall.OnEvict = ts.OnEvict

	tsSmall.AddSpans([]store.Span{
		{
			TraceID:   "trace-high",
			SpanID:    "span-high",
			Name:      "high-root",
			Service:   "high-service",
			StartTime: now,
			EndTime:   now + int64(10*time.Millisecond),
			Status:    1,
		},
	})

	// Add second trace to evict the first
	tsSmall.AddSpans([]store.Span{
		{
			TraceID:   "trace-low",
			SpanID:    "span-low",
			Name:      "low-root",
			Service:   "low-service",
			StartTime: now,
			EndTime:   now + int64(10*time.Millisecond),
			Status:    1,
		},
	})

	// Add third trace to evict the second
	tsSmall.AddSpans([]store.Span{
		{
			TraceID:   "trace-third",
			SpanID:    "span-third",
			Name:      "third-root",
			Service:   "third-service",
			StartTime: now,
			EndTime:   now + int64(10*time.Millisecond),
			Status:    1,
		},
	})

	// Let's sleep briefly to allow OnEvict goroutines to run
	time.Sleep(100 * time.Millisecond)

	if !evictedHigh {
		t.Errorf("expected trace-high to be sampled and evicted (calling OnEvict)")
	}
	if evictedLow {
		t.Errorf("expected trace-low to NOT be sampled, thus NOT calling OnEvict on eviction")
	}
}
