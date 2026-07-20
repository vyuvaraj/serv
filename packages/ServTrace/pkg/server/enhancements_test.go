package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServTrace/pkg/store"
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

func TestAdaptiveSamplingThreshold(t *testing.T) {
	os.Setenv("SERV_TRACE_ADAPTIVE_ERROR_THRESHOLD", "0.25")
	defer os.Unsetenv("SERV_TRACE_ADAPTIVE_ERROR_THRESHOLD")

	ts := store.NewStore(100)
	now := time.Now().UnixNano()

	// Ingest 11 spans (need spanCount > 10 for adaptive sampling)
	// Let's ingest 9 OK spans and 2 Error spans (error rate: 2/11 = 18.1%)
	// Since 18.1% is below threshold 25%, s.samplingRate should NOT jump to 100% (stays baseSamplingRate = 50)
	var spans []store.Span
	for i := 0; i < 9; i++ {
		spans = append(spans, store.Span{
			TraceID:   "trace-adaptive",
			SpanID:    time.Now().Format("20060102150405.000000") + string(rune(i)),
			Name:      "span-ok",
			Service:   "test-service",
			StartTime: now,
			EndTime:   now + int64(10*time.Millisecond),
			Status:    1,
		})
	}
	for i := 0; i < 2; i++ {
		spans = append(spans, store.Span{
			TraceID:   "trace-adaptive",
			SpanID:    time.Now().Format("20060102150405.000000") + "err" + string(rune(i)),
			Name:      "span-err",
			Service:   "test-service",
			StartTime: now,
			EndTime:   now + int64(10*time.Millisecond),
			Status:    2,
		})
	}

	ts.AddSpans(spans)

	rate := ts.GetSamplingRateForTest()
	if rate == 100 {
		t.Errorf("expected sampling rate to stay base rate (50) since error rate 18.1%% is below threshold 25%%, got %d", rate)
	}

	// Now ingest 2 more error spans. Total errors: 4/13 = 30.7%.
	// This is above threshold 25%, so s.samplingRate should jump to 100%!
	var moreSpans []store.Span
	for i := 0; i < 2; i++ {
		moreSpans = append(moreSpans, store.Span{
			TraceID:   "trace-adaptive",
			SpanID:    time.Now().Format("20060102150405.000000") + "err2" + string(rune(i)),
			Name:      "span-err-2",
			Service:   "test-service",
			StartTime: now,
			EndTime:   now + int64(10*time.Millisecond),
			Status:    2,
		})
	}
	ts.AddSpans(moreSpans)

	rate = ts.GetSamplingRateForTest()
	if rate != 100 {
		t.Errorf("expected sampling rate to jump to 100 since error rate 30.7%% exceeds threshold 25%%, got %d", rate)
	}
}

func TestBaggagePropagation(t *testing.T) {
	ts := store.NewStore(100)
	now := time.Now().UnixNano()

	// Ingest parent span with baggage attributes
	ts.AddSpans([]store.Span{
		{
			TraceID:   "trace-baggage",
			SpanID:    "parent-span",
			Name:      "parent",
			Service:   "service-parent",
			StartTime: now,
			EndTime:   now + int64(10*time.Millisecond),
			Status:    1,
			Attributes: map[string]interface{}{
				"baggage.user_id": "user-123",
				"baggage.region":  "us-west",
				"other_attribute": "non-baggage",
			},
		},
	})

	// Ingest child span (should inherit baggage attributes)
	ts.AddSpans([]store.Span{
		{
			TraceID:      "trace-baggage",
			SpanID:       "child-span",
			ParentSpanID: "parent-span",
			Name:         "child",
			Service:      "service-child",
			StartTime:    now + int64(2*time.Millisecond),
			EndTime:      now + int64(8*time.Millisecond),
			Status:       1,
		},
	})

	tree, ok := ts.GetTraceTree("trace-baggage")
	if !ok || len(tree.Children) == 0 {
		t.Fatalf("failed to retrieve child span")
	}

	child := tree.Children[0]
	if child.Span.Attributes == nil {
		t.Fatalf("child span has no attributes")
	}

	if uid, ok := child.Span.Attributes["baggage.user_id"]; !ok || uid != "user-123" {
		t.Errorf("expected propagated baggage.user_id='user-123', got %v", uid)
	}

	if reg, ok := child.Span.Attributes["baggage.region"]; !ok || reg != "us-west" {
		t.Errorf("expected propagated baggage.region='us-west', got %v", reg)
	}

	if _, ok := child.Span.Attributes["other_attribute"]; ok {
		t.Errorf("expected non-baggage attribute to NOT propagate")
	}
}

func TestProfilingSummary(t *testing.T) {
	ts := store.NewStore(100)
	srv := NewServer(ts)
	now := time.Now().UnixNano()

	ts.AddSpans([]store.Span{
		{
			TraceID:   "trace-prof",
			SpanID:    "root-span",
			Name:      "root",
			Service:   "web",
			StartTime: now,
			EndTime:   now + int64(200*time.Millisecond),
			Status:    1,
			Attributes: map[string]interface{}{
				"pprof.cpu_ms":   50.0,
				"pprof.mem_mb":   10.5,
				"pprof.hot_path": "auth.VerifyToken",
			},
		},
		{
			TraceID:      "trace-prof",
			SpanID:       "child-span",
			ParentSpanID: "root-span",
			Name:         "child",
			Service:      "db",
			StartTime:    now + int64(10*time.Millisecond),
			EndTime:      now + int64(190*time.Millisecond),
			Status:       1,
			Attributes: map[string]interface{}{
				"pprof.cpu_ms":   120.0, // hot span!
				"pprof.mem_mb":   2.1,
				"pprof.hot_path": "db.ExecuteQuery",
			},
		},
	})

	req := httptest.NewRequest("GET", "/api/traces/trace-prof/profiling", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var summary TraceProfilingSummary
	if err := json.Unmarshal(w.Body.Bytes(), &summary); err != nil {
		t.Fatalf("failed to decode profiling response: %v", err)
	}

	if summary.TotalCPUMs != 170.0 {
		t.Errorf("expected total CPU 170ms, got %f", summary.TotalCPUMs)
	}

	if summary.TotalMemoryMB != 12.6 {
		t.Errorf("expected total memory 12.6MB, got %f", summary.TotalMemoryMB)
	}

	if summary.HotPathSpan != "child" {
		t.Errorf("expected hot path span 'child', got %s", summary.HotPathSpan)
	}
}

type mockAnomalyExplainer struct{}

func (m *mockAnomalyExplainer) Explain(traceID string) (map[string]interface{}, error) {
	return map[string]interface{}{"explanation": "mock-explanation-for-" + traceID}, nil
}

type mockSloBreachPredictor struct{}

func (m *mockSloBreachPredictor) Predict(traces []store.TraceSummary) (map[string]interface{}, error) {
	return map[string]interface{}{"days_to_breach": 12.3}, nil
}

func TestPluggableTraceEnterpriseFeatures(t *testing.T) {
	ts := store.NewStore(100)
	srv := NewServer(ts)

	// 1. Check fallback to 403 when ActiveAnomalyExplainer and ActiveSloBreachPredictor are nil.
	// In an EE build, init() registers these hooks, so we must temporarily nil them to
	// simulate the OSS "no hook" state, then restore them afterward.
	savedExplainer := ActiveAnomalyExplainer
	savedSlo := ActiveSloBreachPredictor
	ActiveAnomalyExplainer = nil
	ActiveSloBreachPredictor = nil
	defer func() {
		ActiveAnomalyExplainer = savedExplainer
		ActiveSloBreachPredictor = savedSlo
	}()

	reqExplainOSS := httptest.NewRequest("GET", "/api/v1/anomalies/explain?traceId=test-trace", nil)
	wExplainOSS := httptest.NewRecorder()

	srv.Handler().ServeHTTP(wExplainOSS, reqExplainOSS)
	if wExplainOSS.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for explain endpoint in OSS, got %d", wExplainOSS.Code)
	}

	reqSloOSS := httptest.NewRequest("GET", "/api/trace/anomaly/slo-breach-predict", nil)
	wSloOSS := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wSloOSS, reqSloOSS)
	if wSloOSS.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for SLO predict endpoint in OSS, got %d", wSloOSS.Code)
	}

	// 2. Check returns 200 and calls providers when hooks are registered
	mockExp := &mockAnomalyExplainer{}
	ActiveAnomalyExplainer = mockExp
	defer func() { ActiveAnomalyExplainer = nil }()

	mockSlo := &mockSloBreachPredictor{}
	ActiveSloBreachPredictor = mockSlo
	defer func() { ActiveSloBreachPredictor = nil }()

	wExplainEE := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wExplainEE, reqExplainOSS)
	if wExplainEE.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for explain, got %d", wExplainEE.Code)
	}
	var explainResp map[string]interface{}
	json.Unmarshal(wExplainEE.Body.Bytes(), &explainResp)
	if explainResp["explanation"] != "mock-explanation-for-test-trace" {
		t.Errorf("expected mock explanation, got %v", explainResp)
	}

	wSloEE := httptest.NewRecorder()
	srv.Handler().ServeHTTP(wSloEE, reqSloOSS)
	if wSloEE.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for SLO predict, got %d", wSloEE.Code)
	}
	var sloResp map[string]interface{}
	json.Unmarshal(wSloEE.Body.Bytes(), &sloResp)
	if sloResp["days_to_breach"] != 12.3 {
		t.Errorf("expected mock days_to_breach 12.3, got %v", sloResp)
	}
}

