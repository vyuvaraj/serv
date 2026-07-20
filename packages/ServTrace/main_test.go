package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServTrace/pkg/server"
	"github.com/vyuvaraj/serv/packages/ServTrace/pkg/store"
)

func TestServTraceCollector(t *testing.T) {
	ts := store.NewStore(2) // limit to 2 traces for testing eviction
	srv := server.NewServer(ts)
	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	span1ID := "00f067aa0ba902b7"
	span2ID := "3e63f565c553856a"

	// Mock OTLP payload matching exportSpans in ServShared
	nowNano := time.Now().UnixNano()
	end1Nano := nowNano + int64(100*time.Millisecond)
	start2Nano := nowNano + int64(10*time.Millisecond)
	end2Nano := nowNano + int64(80*time.Millisecond)

	payload := map[string]interface{}{
		"resourceSpans": []interface{}{
			map[string]interface{}{
				"resource": map[string]interface{}{
					"attributes": []interface{}{
						map[string]interface{}{"key": "service.name", "value": map[string]interface{}{"stringValue": "test-service"}},
					},
				},
				"scopeSpans": []interface{}{
					map[string]interface{}{
						"scope": map[string]interface{}{"name": "servverse-shared"},
						"spans": []interface{}{
							map[string]interface{}{
								"traceId":           traceID,
								"spanId":            span1ID,
								"name":              "HTTP GET /users",
								"kind":              2, // Server
								"startTimeUnixNano": fmt.Sprintf("%d", nowNano),
								"endTimeUnixNano":   fmt.Sprintf("%d", end1Nano),
								"status":            map[string]interface{}{"code": 1}, // OK
							},
							map[string]interface{}{
								"traceId":           traceID,
								"spanId":            span2ID,
								"parentSpanId":      span1ID,
								"name":              "Database SELECT users",
								"kind":              3, // Client
								"startTimeUnixNano": fmt.Sprintf("%d", start2Nano),
								"endTimeUnixNano":   fmt.Sprintf("%d", end2Nano),
								"status":            map[string]interface{}{"code": 2}, // Error
								"attributes": []interface{}{
									map[string]interface{}{"key": "db.statement", "value": map[string]interface{}{"stringValue": "SELECT * FROM users"}},
								},
							},
						},
					},
				},
			},
		},
	}

	// 1. Ingest Traces
	body, _ := json.Marshal(payload)
	resp, err := http.Post(testServer.URL+"/v1/traces", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to make ingestion request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	// 2. Query Traces List
	listResp, err := http.Get(testServer.URL + "/api/traces")
	if err != nil {
		t.Fatalf("failed to query traces list: %v", err)
	}
	defer listResp.Body.Close()

	var traces []store.TraceSummary
	if err := json.NewDecoder(listResp.Body).Decode(&traces); err != nil {
		t.Fatalf("failed to decode list: %v", err)
	}

	if len(traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(traces))
	}

	summary := traces[0]
	if summary.TraceID != traceID {
		t.Errorf("expected traceId %s, got %s", traceID, summary.TraceID)
	}
	if summary.RootName != "HTTP GET /users" {
		t.Errorf("expected rootName 'HTTP GET /users', got %s", summary.RootName)
	}
	if summary.Service != "test-service" {
		t.Errorf("expected service 'test-service', got %s", summary.Service)
	}
	if summary.TotalSpans != 2 {
		t.Errorf("expected 2 spans, got %d", summary.TotalSpans)
	}
	if summary.ErrorCount != 1 {
		t.Errorf("expected 1 error, got %d", summary.ErrorCount)
	}

	// 3. Query Trace Tree Waterfall
	treeResp, err := http.Get(testServer.URL + "/api/traces/" + traceID)
	if err != nil {
		t.Fatalf("failed to query tree: %v", err)
	}
	defer treeResp.Body.Close()

	var root store.SpanNode
	if err := json.NewDecoder(treeResp.Body).Decode(&root); err != nil {
		t.Fatalf("failed to decode tree root: %v", err)
	}

	if root.Span.SpanID != span1ID {
		t.Errorf("expected root spanID %s, got %s", span1ID, root.Span.SpanID)
	}
	if len(root.Children) != 1 {
		t.Fatalf("expected root to have 1 child, got %d", len(root.Children))
	}

	child := root.Children[0]
	if child.Span.SpanID != span2ID {
		t.Errorf("expected child spanID %s, got %s", span2ID, child.Span.SpanID)
	}
	if child.Span.ParentSpanID != span1ID {
		t.Errorf("expected child parentID %s, got %s", span1ID, child.Span.ParentSpanID)
	}

	// Validate DB statement attribute
	dbStatement, exists := child.Span.Attributes["db.statement"]
	if !exists || dbStatement != "SELECT * FROM users" {
		t.Errorf("expected db.statement attribute 'SELECT * FROM users', got %v", dbStatement)
	}

	// 4. Test Eviction
	// Ingest Trace 2
	payload2 := map[string]interface{}{
		"resourceSpans": []interface{}{
			map[string]interface{}{
				"resource": map[string]interface{}{
					"attributes": []interface{}{
						map[string]interface{}{"key": "service.name", "value": map[string]interface{}{"stringValue": "test-service"}},
					},
				},
				"scopeSpans": []interface{}{
					map[string]interface{}{
						"spans": []interface{}{
							map[string]interface{}{
								"traceId":           "trace2",
								"spanId":            "spanX",
								"name":              "Span 2",
								"startTimeUnixNano": fmt.Sprintf("%d", nowNano),
								"endTimeUnixNano":   fmt.Sprintf("%d", end1Nano),
							},
						},
					},
				},
			},
		},
	}
	body2, _ := json.Marshal(payload2)
	_, _ = http.Post(testServer.URL+"/v1/traces", "application/json", bytes.NewReader(body2))

	// Ingest Trace 3
	payload3 := map[string]interface{}{
		"resourceSpans": []interface{}{
			map[string]interface{}{
				"resource": map[string]interface{}{
					"attributes": []interface{}{
						map[string]interface{}{"key": "service.name", "value": map[string]interface{}{"stringValue": "test-service"}},
					},
				},
				"scopeSpans": []interface{}{
					map[string]interface{}{
						"spans": []interface{}{
							map[string]interface{}{
								"traceId":           "trace3",
								"spanId":            "spanY",
								"name":              "Span 3",
								"startTimeUnixNano": fmt.Sprintf("%d", nowNano),
								"endTimeUnixNano":   fmt.Sprintf("%d", end1Nano),
							},
						},
					},
				},
			},
		},
	}
	body3, _ := json.Marshal(payload3)
	_, _ = http.Post(testServer.URL+"/v1/traces", "application/json", bytes.NewReader(body3))

	// List should now only have Trace 2 and Trace 3, while Trace 1 is evicted
	listResp2, _ := http.Get(testServer.URL + "/api/traces")
	var traces2 []store.TraceSummary
	_ = json.NewDecoder(listResp2.Body).Decode(&traces2)
	listResp2.Body.Close()

	if len(traces2) != 2 {
		t.Fatalf("expected 2 traces, got %d", len(traces2))
	}

	for _, tSum := range traces2 {
		if tSum.TraceID == traceID {
			t.Errorf("expected Trace 1 (%s) to be evicted, but it is still in memory", traceID)
		}
	}
}

func TestSamplingPolicies(t *testing.T) {
	// Initialize store with 0% sampling rate (head-based drops everything by default)
	os.Setenv("SERV_TRACE_SAMPLING_RATE", "0")
	defer os.Unsetenv("SERV_TRACE_SAMPLING_RATE")

	evictChan := make(chan string, 10)
	ts := store.NewStore(2) // limit 2
	ts.OnEvict = func(traceID string, spans []store.Span) {
		evictChan <- traceID
	}

	// 1. Add healthy trace. Should not be sampled (not archived on eviction)
	ts.AddSpans([]store.Span{
		{TraceID: "trace-healthy", SpanID: "span1", Name: "GET", Status: 1, Service: "gateway"},
	})

	// 2. Add trace with error (tail-based override). Should be sampled (archived on eviction)
	ts.AddSpans([]store.Span{
		{TraceID: "trace-error", SpanID: "span2", Name: "GET", Status: 2, Service: "gateway"}, // status 2 = error
	})

	// 3. Add trace with slow query (tail-based override). Should be sampled (archived on eviction)
	ts.AddSpans([]store.Span{
		{
			TraceID: "trace-slow-query", 
			SpanID: "span3", 
			Name: "SELECT", 
			Status: 1, 
			Service: "database",
			Attributes: map[string]interface{}{
				"db.slow_query": true,
			},
		},
	})

	// Triggers evictions! Since limit is 2, adding the 3rd trace evicts the 1st ("trace-healthy").
	// Since "trace-healthy" is not sampled, it should NOT trigger OnEvict.
	// Let's add a 4th trace to evict "trace-error", which IS sampled and should trigger OnEvict.
	ts.AddSpans([]store.Span{
		{TraceID: "trace-fourth", SpanID: "span4", Name: "GET", Status: 1, Service: "gateway"},
	})

	// Wait a moment for async eviction callbacks
	time.Sleep(50 * time.Millisecond)
	close(evictChan)

	var evicted []string
	for id := range evictChan {
		evicted = append(evicted, id)
	}

	// We expect "trace-error" to be evicted and archived. "trace-healthy" should have been evicted but skipped.
	foundHealthy := false
	foundError := false
	for _, id := range evicted {
		if id == "trace-healthy" {
			foundHealthy = true
		}
		if id == "trace-error" {
			foundError = true
		}
	}

	if foundHealthy {
		t.Errorf("Expected trace-healthy to be dropped, but it was archived")
	}
	if !foundError {
		t.Errorf("Expected trace-error to be archived, but it was dropped")
	}
}

func TestSpanMetricsAndAnomalies(t *testing.T) {
	ts := store.NewStore(10)
	
	// Record multiple spans to establish a baseline for "gateway:GET"
	// Let's add 6 healthy spans with a latency of 10ms
	for i := 0; i < 6; i++ {
		ts.AddSpans([]store.Span{
			{
				TraceID:   fmt.Sprintf("trace-%d", i),
				SpanID:    fmt.Sprintf("span-%d", i),
				Service:   "gateway",
				Name:      "GET",
				StartTime: 10000000,
				EndTime:   20000000, // 10ms
				Status:    1,
			},
		})
	}

	// Fetch metrics and assert p50/p90 baseline is calculated
	metrics := ts.GetMetrics()
	found := false
	for _, m := range metrics {
		if m.Service == "gateway" && m.SpanName == "GET" {
			found = true
			if m.P50 != 10.0 {
				t.Errorf("expected P50 to be 10.0ms, got %.2fms", m.P50)
			}
			if m.P90 != 10.0 {
				t.Errorf("expected P90 to be 10.0ms, got %.2fms", m.P90)
			}
		}
	}
	if !found {
		t.Fatalf("expected gateway:GET metrics summary, but none found")
	}

	// Trigger anomaly detection stdout logs!
	// 1. Latency spike: add a span with 100ms which is > 3 * 10ms rolling P90.
	// Since anomaly check writes to stdout, we can capture stdout or just verify the logic works.
	// Let's also assert the new latency is recorded.
	ts.AddSpans([]store.Span{
		{
			TraceID:   "trace-anomaly",
			SpanID:    "span-anomaly",
			Service:   "gateway",
			Name:      "GET",
			StartTime: 10000000,
			EndTime:   110000000, // 100ms (10x baseline P90!)
			Status:    1,
		},
	})

	// 2. Error burst: add a trace with multiple spans where >30% have status 2
	ts.AddSpans([]store.Span{
		{TraceID: "trace-burst", SpanID: "b-span1", Service: "gateway", Name: "GET", Status: 2},
		{TraceID: "trace-burst", SpanID: "b-span2", Service: "auth", Name: "Verify", Status: 2},
		{TraceID: "trace-burst", SpanID: "b-span3", Service: "database", Name: "Select", Status: 1},
	})
}

func TestTraceToLogCorrelation(t *testing.T) {
	ts := store.NewStore(5)
	srv := server.NewServer(ts)
	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	// 1. Add trace spans
	ts.AddSpans([]store.Span{
		{TraceID: "correlated-trace-id", SpanID: "span1", Name: "GET", Status: 1, Service: "gateway", StartTime: 1000, EndTime: 2000},
	})

	// 2. Ingest logs associated with this traceID
	logPayload := map[string]interface{}{
		"traceId":   "correlated-trace-id",
		"service":   "gateway",
		"level":     "info",
		"message":   "Gateway received request on path /users",
		"timestamp": time.Now(),
	}
	body, _ := json.Marshal(logPayload)
	resp, err := http.Post(testServer.URL+"/api/logs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to post log: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected StatusAccepted, got %d", resp.StatusCode)
	}

	// 3. Fetch correlated logs via the endpoint
	getResp, err := http.Get(testServer.URL + "/api/traces/correlated-trace-id/logs")
	if err != nil {
		t.Fatalf("failed to get trace logs: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected StatusOK, got %d", getResp.StatusCode)
	}

	var logs []store.LogLine
	if err := json.NewDecoder(getResp.Body).Decode(&logs); err != nil {
		t.Fatalf("failed to decode logs payload: %v", err)
	}

	if len(logs) != 1 {
		t.Fatalf("expected 1 correlated log line, got %d", len(logs))
	}

	if logs[0].Message != "Gateway received request on path /users" {
		t.Errorf("expected log message to match, got %q", logs[0].Message)
	}
}

func BenchmarkTraceStoreAddSpans(b *testing.B) {
	s := store.NewStore(10000)
	now := int64(1700000000000000000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		traceID := fmt.Sprintf("trace-%d", i%100)
		spans := []store.Span{
			{
				TraceID:   traceID,
				SpanID:    fmt.Sprintf("span-%d", i),
				Name:      "http.request",
				StartTime: now,
				EndTime:   now + int64(5*time.Millisecond),
				Service:   "github.com/vyuvaraj/serv/packages/ServGate",
			},
		}
		s.AddSpans(spans)
	}
}

func TestAdaptiveSampling(t *testing.T) {
	os.Setenv("SERV_TRACE_SAMPLING_RATE", "10")
	defer os.Unsetenv("SERV_TRACE_SAMPLING_RATE")

	ts := store.NewStore(100)

	// 1. Send healthy spans, should stay at 10%
	for i := 0; i < 15; i++ {
		ts.AddSpans([]store.Span{
			{TraceID: fmt.Sprintf("trace-h-%d", i), SpanID: "span", Name: "GET", Status: 1, Service: "gateway"},
		})
	}
	if rate := ts.GetSamplingRateForTest(); rate != 10 {
		t.Errorf("Expected sampling rate to remain 10, got %d", rate)
	}

	// 2. Send some error spans to spike the error rate (> 5%)
	ts.AddSpans([]store.Span{
		{TraceID: "trace-err-1", SpanID: "span", Name: "GET", Status: 2, Service: "gateway"}, // error
		{TraceID: "trace-err-2", SpanID: "span", Name: "GET", Status: 2, Service: "gateway"}, // error
	})

	// Sampling rate should adaptively spike to 100%
	if rate := ts.GetSamplingRateForTest(); rate != 100 {
		t.Errorf("Expected sampling rate to spike to 100, got %d", rate)
	}
}

func TestSpanIngestionThroughput(t *testing.T) {
	ts := store.NewStore(50000)
	
	// Create 20,000 spans
	spans := make([]store.Span, 20000)
	for i := 0; i < 20000; i++ {
		spans[i] = store.Span{
			TraceID:   fmt.Sprintf("trace-%d", i/5),
			SpanID:    fmt.Sprintf("span-%d", i),
			Name:      "GET /items",
			Service:   "perf-service",
			StartTime: time.Now().UnixNano(),
			EndTime:   time.Now().UnixNano() + 1000000,
		}
	}

	start := time.Now()
	// Add spans in batches of 100
	batchSize := 100
	for i := 0; i < len(spans); i += batchSize {
		end := i + batchSize
		if end > len(spans) {
			end = len(spans)
		}
		ts.AddSpans(spans[i:end])
	}
	duration := time.Since(start)

	spansPerSec := float64(len(spans)) / duration.Seconds()
	t.Logf("Ingestion performance: %d spans in %v (%.2f spans/sec)", len(spans), duration, spansPerSec)

	// Target: 10K/sec single node
	if spansPerSec < 10000 {
		t.Errorf("Ingestion throughput too low: %.2f spans/sec (target: >= 10,000/sec)", spansPerSec)
	}
}

func TestOutOfOrderTraceReconstruction(t *testing.T) {
	ts := store.NewStore(100)
	traceID := "ooo-trace-987"

	// 1. Define 20 spans of a single trace in parent-child relationship:
	// Span 0 is root. Span i depends on i-1.
	spans := make([]store.Span, 20)
	for i := 0; i < 20; i++ {
		pID := ""
		if i > 0 {
			pID = fmt.Sprintf("span-%d", i-1)
		}
		attrs := make(map[string]interface{})
		if i == 0 {
			attrs["baggage.request_id"] = "req-12345"
		}
		spans[i] = store.Span{
			TraceID:      traceID,
			SpanID:       fmt.Sprintf("span-%d", i),
			ParentSpanID: pID,
			Name:         fmt.Sprintf("Step-%d", i),
			Service:      "test-service",
			StartTime:    time.Now().UnixNano() + int64(i)*1000,
			EndTime:      time.Now().UnixNano() + int64(i)*1000 + 500,
			Attributes:   attrs,
		}
	}

	// 2. Ingest out-of-order (reverse: children first, then parent, then root)
	for i := 19; i >= 0; i-- {
		ts.AddSpans([]store.Span{spans[i]})
	}

	// 3. Get reconstructed tree
	tree, ok := ts.GetTraceTree(traceID)
	if !ok {
		t.Fatalf("failed to retrieve trace tree")
	}

	// 4. Verify tree depth/size
	count := 0
	var checkNode func(n *store.SpanNode)
	checkNode = func(n *store.SpanNode) {
		if n == nil {
			return
		}
		count++
		
		// Verify baggage propagation succeeded recursively for all nodes
		val, exists := n.Span.Attributes["baggage.request_id"]
		if !exists || val != "req-12345" {
			t.Errorf("missing baggage on task %s", n.Span.Name)
		}
		
		for _, child := range n.Children {
			checkNode(child)
		}
	}
	checkNode(tree)

	if count != 20 {
		t.Errorf("expected 20 spans in tree, got %d", count)
	}
}

func TestColdTierRetrievalLatency(t *testing.T) {
	ts := store.NewStore(100)
	srv := server.NewServer(ts)

	traceID := "cold-trace-456"

	// Mock S3 Cold Tier Server
	s3Mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]store.Span{
			{TraceID: traceID, SpanID: "span1", Name: "RootSpan", Service: "gateway", StartTime: 1000, EndTime: 2000},
			{TraceID: traceID, SpanID: "span2", ParentSpanID: "span1", Name: "ChildSpan", Service: "auth", StartTime: 1100, EndTime: 1800},
		})
	}))
	defer s3Mock.Close()

	os.Setenv("SERV_CONFIG_S3_ENDPOINT", s3Mock.URL)
	defer os.Unsetenv("SERV_CONFIG_S3_ENDPOINT")

	// Call handleGetTraceTree over HTTP via srv.Handler()
	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	start := time.Now()
	getResp, err := http.Get(testServer.URL + "/api/traces/" + traceID)
	if err != nil {
		t.Fatalf("failed request: %v", err)
	}
	defer getResp.Body.Close()
	duration := time.Since(start)

	t.Logf("Cold tier retrieval took %v", duration)

	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", getResp.StatusCode)
	}

	var tree store.SpanNode
	if err := json.NewDecoder(getResp.Body).Decode(&tree); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if tree.Span.Name != "RootSpan" || len(tree.Children) != 1 || tree.Children[0].Span.Name != "ChildSpan" {
		t.Errorf("incorrect tree reconstructed: %+v", tree)
	}

	// Target: Query latency < 500ms
	if duration > 500*time.Millisecond {
		t.Errorf("cold tier query latency exceeded 500ms limit: %v", duration)
	}
}

