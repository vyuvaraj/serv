package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"servtrace/pkg/store"

	"github.com/vyuvaraj/ServShared"
)

type Server struct {
	traceStore *store.Store
}

func NewServer(ts *store.Store) *Server {
	s := &Server{traceStore: ts}
	s.startSelfHealingLoop()
	s.startRetentionCleanerLoop()
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", ServShared.HealthzHandler)
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servtrace", "1.0.0"))
	mux.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	mux.HandleFunc("/v1/traces", s.handleIngest)
	mux.HandleFunc("/api/traces", s.handleListTraces)
	mux.HandleFunc("/api/v1/traces/search", s.handleNaturalLanguageSearch)
	mux.HandleFunc("/api/dependency-graph", s.handleDependencyGraph)
	mux.HandleFunc("/api/metrics", s.handleGetMetrics)
	mux.HandleFunc("/api/v1/metrics", s.handleGetMetrics)
	mux.HandleFunc("/api/v1/anomalies", s.handleGetAnomalies)
	mux.HandleFunc("/api/v1/anomalies/explain", s.handleExplainAnomaly)
	mux.HandleFunc("/api/logs", s.handleIngestLog)
	mux.HandleFunc("/api/trace/anomaly/slow-spans", s.handleSlowSpans)
	mux.HandleFunc("/api/trace/anomaly/slo-breach-predict", s.handleSloBreachPredict)
	mux.HandleFunc("/api/v1/tuning/recommendations", s.handleTuningRecommendations)
	mux.HandleFunc("/api/v1/traces/compare", s.handleCompareTraces)
	
	mux.HandleFunc("/api/traces/", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodDelete {
			s.traceStore.Clear()
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success","message":"Traces cleared"}`))
			return
		}

		path := req.URL.Path
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) < 3 {
			http.Error(w, "Trace ID required", http.StatusBadRequest)
			return
		}
		traceID := parts[2]
		if len(parts) == 4 && parts[3] == "logs" {
			s.handleGetTraceLogs(w, req, traceID)
			return
		}
		if len(parts) == 4 && parts[3] == "profiling" {
			s.handleGetTraceProfiling(w, req, traceID)
			return
		}
		s.handleGetTraceTree(w, req, traceID)
	})

	mux.HandleFunc("/api/v1/traces/", func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) < 4 {
			http.Error(w, "Trace ID required", http.StatusBadRequest)
			return
		}
		traceID := parts[3]
		if len(parts) == 5 && parts[4] == "profiling" {
			s.handleGetTraceProfiling(w, req, traceID)
			return
		}
		http.Error(w, "Not Found", http.StatusNotFound)
	})

	return ServShared.AuthMiddleware(mux)
}

type OtlpPayload struct {
	ResourceSpans []struct {
		Resource struct {
			Attributes []struct {
				Key   string `json:"key"`
				Value struct {
					StringValue string `json:"stringValue"`
				} `json:"value"`
			} `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []struct {
			Spans []struct {
				TraceID           string `json:"traceId"`
				SpanID            string `json:"spanId"`
				ParentSpanID      string `json:"parentSpanId"`
				Name              string `json:"name"`
				Kind              int    `json:"kind"`
				StartTimeUnixNano string `json:"startTimeUnixNano"`
				EndTimeUnixNano   string `json:"endTimeUnixNano"`
				Status            struct {
					Code int `json:"code"`
				} `json:"status"`
				Attributes []struct {
					Key   string `json:"key"`
					Value struct {
						StringValue string `json:"stringValue"`
					} `json:"value"`
				} `json:"attributes"`
			} `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

func (s *Server) handleIngest(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read and parse raw OTLP payload
	var raw interface{}
	if err := json.NewDecoder(req.Body).Decode(&raw); err != nil {
		http.Error(w, "Malformed JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// We decode using map[string]interface{} to be extremely flexible with varying OTLP types
	payloadMap, ok := raw.(map[string]interface{})
	if !ok {
		http.Error(w, "Invalid payload type", http.StatusBadRequest)
		return
	}

	var parsedSpans []store.Span

	resourceSpans, ok := payloadMap["resourceSpans"].([]interface{})
	if !ok {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success","message":"No resourceSpans"}`))
		return
	}

	for _, resSpan := range resourceSpans {
		rs, ok := resSpan.(map[string]interface{})
		if !ok {
			continue
		}

		serviceName := "unknown-service"
		if resource, ok := rs["resource"].(map[string]interface{}); ok {
			if attributes, ok := resource["attributes"].([]interface{}); ok {
				for _, attr := range attributes {
					if attrMap, ok := attr.(map[string]interface{}); ok {
						if attrMap["key"] == "service.name" {
							if valMap, ok := attrMap["value"].(map[string]interface{}); ok {
								serviceName = valMap["stringValue"].(string)
							}
						}
					}
				}
			}
		}

		scopeSpans, ok := rs["scopeSpans"].([]interface{})
		if !ok {
			continue
		}

		for _, scopeSpan := range scopeSpans {
			ss, ok := scopeSpan.(map[string]interface{})
			if !ok {
				continue
			}

			spans, ok := ss["spans"].([]interface{})
			if !ok {
				continue
			}

			for _, sp := range spans {
				sMap, ok := sp.(map[string]interface{})
				if !ok {
					continue
				}

				traceID, _ := sMap["traceId"].(string)
				spanID, _ := sMap["spanId"].(string)
				parentSpanID, _ := sMap["parentSpanId"].(string)
				name, _ := sMap["name"].(string)
				
				// Handle kind as float64 (JSON numbers decode to float64)
				kind := 1
				if kVal, ok := sMap["kind"].(float64); ok {
					kind = int(kVal)
				}

				startTime := store.ParseInt64Safe(sMap["startTimeUnixNano"])
				endTime := store.ParseInt64Safe(sMap["endTimeUnixNano"])

				statusCode := 1
				if statusMap, ok := sMap["status"].(map[string]interface{}); ok {
					if codeVal, ok := statusMap["code"].(float64); ok {
						statusCode = int(codeVal)
					}
				}

				attrs := make(map[string]interface{})
				if attributes, ok := sMap["attributes"].([]interface{}); ok {
					for _, attr := range attributes {
						if attrMap, ok := attr.(map[string]interface{}); ok {
							k, _ := attrMap["key"].(string)
							if valMap, ok := attrMap["value"].(map[string]interface{}); ok {
								attrs[k] = valMap["stringValue"]
							}
						}
					}
				}

				// Detect database slow queries
				durationMs := float64(endTime-startTime) / 1e6
				_, hasDb := attrs["db.system"].(string)
				dbStatement, hasStmt := attrs["db.statement"].(string)
				
				// Standard heuristic: name contains SQL keywords, or has db.system/db.statement
				isDb := hasDb || hasStmt || strings.HasPrefix(strings.ToLower(name), "select") || strings.HasPrefix(strings.ToLower(name), "db:") || strings.HasPrefix(strings.ToLower(name), "query")
				
				if isDb && durationMs > 100 { // threshold: 100ms
					attrs["db.slow_query"] = true
					attrs["db.duration_ms"] = durationMs
					fmt.Printf("[DATABASE_ALERT] Slow query detected in %s: '%s' took %.2fms (query: %s)\n", serviceName, name, durationMs, dbStatement)
				}

				parsedSpans = append(parsedSpans, store.Span{
					TraceID:      traceID,
					SpanID:       spanID,
					ParentSpanID: parentSpanID,
					Name:         name,
					Kind:         kind,
					StartTime:    startTime,
					EndTime:      endTime,
					Status:       statusCode,
					Attributes:   attrs,
					Service:      serviceName,
				})
			}
		}
	}

	s.traceStore.AddSpans(parsedSpans)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success"}`))
}

func (s *Server) handleListTraces(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	serviceFilter := req.URL.Query().Get("service")
	operationFilter := req.URL.Query().Get("operation")
	errorFilter := req.URL.Query().Get("error")
	durationFilter := req.URL.Query().Get("min_duration_ms")

	traces := s.traceStore.ListTraces()
	filtered := make([]store.TraceSummary, 0)

	for _, t := range traces {
		if serviceFilter != "" && !strings.Contains(strings.ToLower(t.Service), strings.ToLower(serviceFilter)) {
			continue
		}
		if operationFilter != "" && !strings.Contains(strings.ToLower(t.RootName), strings.ToLower(operationFilter)) {
			continue
		}
		if errorFilter == "true" && t.ErrorCount == 0 {
			continue
		}
		if durationFilter != "" {
			if limit, err := strconv.ParseFloat(durationFilter, 64); err == nil && t.DurationMs < limit {
				continue
			}
		}
		filtered = append(filtered, t)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(filtered)
}

func (s *Server) handleGetTraceTree(w http.ResponseWriter, req *http.Request, traceID string) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	tree, ok := s.traceStore.GetTraceTree(traceID)
	if !ok {
		// Fallback to S3 Cold Tier!
		endpoint := os.Getenv("SERV_CONFIG_S3_ENDPOINT")
		if endpoint == "" {
			endpoint = "http://localhost:8081"
		}
		bucket := "serv-traces"
		authToken := os.Getenv("SERV_CONFIG_S3_AUTH_TOKEN")
		if authToken == "" {
			authToken = "gateway-secret-token"
		}

		fileURL := fmt.Sprintf("%s/%s/%s.json", endpoint, bucket, traceID)
		fallbackReq, _ := http.NewRequestWithContext(req.Context(), "GET", fileURL, nil)
		if authToken != "" {
			fallbackReq.Header.Set("Authorization", "Bearer "+authToken)
		}

		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Do(fallbackReq)
		if err == nil && resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			var spans []store.Span
			if err := json.NewDecoder(resp.Body).Decode(&spans); err == nil && len(spans) > 0 {
				// Re-insert temporarily into store so we can build the tree
				s.traceStore.AddSpans(spans)
				if reloadedTree, ok := s.traceStore.GetTraceTree(traceID); ok {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(reloadedTree)
					return
				}
			}
		}
		if err == nil {
			resp.Body.Close()
		}

		http.Error(w, "Trace not found in memory or cold tier", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tree)
}

func (s *Server) handleDependencyGraph(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	graph := s.traceStore.GenerateDependencyGraph()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(graph)
}

func (s *Server) handleGetMetrics(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	metrics := s.traceStore.GetMetrics()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(metrics)
}

type IngestLogReq struct {
	TraceID   string    `json:"traceId"`
	Timestamp time.Time `json:"timestamp"`
	Service   string    `json:"service"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

func (s *Server) handleIngestLog(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var r IngestLogReq
	if err := json.NewDecoder(req.Body).Decode(&r); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if r.TraceID == "" {
		http.Error(w, "TraceID required", http.StatusBadRequest)
		return
	}

	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now()
	}

	s.traceStore.AddLog(r.TraceID, store.LogLine{
		Timestamp: r.Timestamp,
		Service:   r.Service,
		Level:     r.Level,
		Message:   r.Message,
	})

	w.WriteHeader(http.StatusAccepted)
	w.Write([]byte(`{"status":"accepted"}`))
}

func (s *Server) handleGetTraceLogs(w http.ResponseWriter, req *http.Request, traceID string) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	logs := s.traceStore.GetLogs(traceID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func (s *Server) handleGetAnomalies(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	anomalies := s.traceStore.GetAnomalies()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(anomalies)
}

func (s *Server) handleExplainAnomaly(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	traceID := req.URL.Query().Get("traceId")
	explanation, err := s.ExplainAnomaly(traceID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(explanation)
}

func (s *Server) handleSlowSpans(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	traces := s.traceStore.ListTraces()
	var bottlenecks []map[string]interface{}

	for _, t := range traces {
		if t.DurationMs > 500 { // focus on slow traces
			tree, exists := s.traceStore.GetTraceTree(t.TraceID)
			if exists && tree != nil {
				// Find slowest child node recursively
				slowest := tree
				var findSlowest func(*store.SpanNode)
				findSlowest = func(n *store.SpanNode) {
					if n.DurationMs > slowest.DurationMs {
						slowest = n
					}
					for _, child := range n.Children {
						findSlowest(child)
					}
				}
				findSlowest(tree)

				percentage := 0.0
				if tree.DurationMs > 0 {
					percentage = (slowest.DurationMs / tree.DurationMs) * 100
				}

				bottlenecks = append(bottlenecks, map[string]interface{}{
					"trace_id":       t.TraceID,
					"service":        slowest.Span.Service,
					"span_name":      slowest.Span.Name,
					"duration_ms":    slowest.DurationMs,
					"percentage":     percentage,
					"explanation":    fmt.Sprintf("Auto-correlate slow spans: %s had a %s span taking %.1f%% of overall latency", slowest.Span.Service, slowest.Span.Name, percentage),
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(bottlenecks)
}

func (s *Server) handleSloBreachPredict(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	traces := s.traceStore.ListTraces()
	total := len(traces)
	errors := 0

	for _, t := range traces {
		if t.ErrorCount > 0 {
			errors++
		}
	}

	failureRate := 0.0
	if total > 0 {
		failureRate = float64(errors) / float64(total)
	}

	// Simple linear extrapolation of SLO budget remaining
	sloTarget := 0.99 // 99% availability target
	allowedFailRate := 1.0 - sloTarget
	budgetBurnRatio := 0.0
	if allowedFailRate > 0 {
		budgetBurnRatio = failureRate / allowedFailRate
	}

	daysRemaining := 30.0
	if budgetBurnRatio > 1.0 {
		daysRemaining = 30.0 / budgetBurnRatio
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_traces":    total,
		"error_count":     errors,
		"failure_rate":    failureRate,
		"slo_target":      sloTarget,
		"budget_burn":     budgetBurnRatio,
		"days_to_breach":  daysRemaining,
		"status":          fmt.Sprintf("Current error rate trajectory: SLO budget exhausted in %.1f days", daysRemaining),
	})
}

func (s *Server) startSelfHealingLoop() {
	ticker := time.NewTicker(1 * time.Second)
	go func() {
		for range ticker.C {
			s.runSelfHealingDiagnostics()
		}
	}()
}

func (s *Server) runSelfHealingDiagnostics() {
	traces := s.traceStore.ListTraces()
	if len(traces) == 0 {
		return
	}

	serviceErrors := make(map[string]int)
	serviceTotal := make(map[string]int)

	for _, t := range traces {
		if t.Service != "" {
			serviceTotal[t.Service]++
			if t.ErrorCount > 0 {
				serviceErrors[t.Service]++
			}
		}
	}

	for svc, total := range serviceTotal {
		if total >= 5 {
			errRate := float64(serviceErrors[svc]) / float64(total)
			if errRate > 0.20 {
				fmt.Printf("[Self-Healing] High error rate (%.2f%%) detected on service '%s'. Triggering rollback...\n", errRate*100, svc)
				s.triggerRollback(svc)
			}
		}
	}
}

func (s *Server) handleTuningRecommendations(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	traces := s.traceStore.ListTraces()
	slowEndpointCounts := make(map[string]int)
	slowQueryCounts := make(map[string]int)

	for _, t := range traces {
		if t.DurationMs > 200 { // focus on spans taking > 200ms
			tree, exists := s.traceStore.GetTraceTree(t.TraceID)
			if exists && tree != nil {
				var traverse func(*store.SpanNode)
				traverse = func(n *store.SpanNode) {
					if n.DurationMs > 200 {
						// Check if it's a database query
						isDB := false
						if n.Span.Attributes != nil {
							if dbType, ok := n.Span.Attributes["db.system"]; ok && dbType != "" {
								isDB = true
							}
						}
						// If span name contains DB operations or isDB
						nameLower := strings.ToLower(n.Span.Name)
						if isDB || strings.Contains(nameLower, "select") || strings.Contains(nameLower, "query") {
							slowQueryCounts[n.Span.Name]++
						} else {
							// Exclude general health checks or short ones
							if !strings.Contains(nameLower, "health") {
								slowEndpointCounts[n.Span.Name]++
							}
						}
					}
					for _, child := range n.Children {
						traverse(child)
					}
				}
				traverse(tree)
			}
		}
	}

	type Recommendation struct {
		Target string `json:"target"`
		Type   string `json:"type"` // "cache" or "index"
		Reason string `json:"reason"`
	}
	var recommendations []Recommendation

	for endpoint, count := range slowEndpointCounts {
		if count >= 1 {
			recommendations = append(recommendations, Recommendation{
				Target: endpoint,
				Type:   "cache",
				Reason: fmt.Sprintf("Endpoint %s detected as latency bottleneck (%d slow executions). Cache recommended.", endpoint, count),
			})
		}
	}

	for query, count := range slowQueryCounts {
		if count >= 1 {
			recommendations = append(recommendations, Recommendation{
				Target: query,
				Type:   "index",
				Reason: fmt.Sprintf("Slow database operation %s executed %d times. Creating database index recommended.", query, count),
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(recommendations)
}

func (s *Server) triggerRollback(service string) {
	cloudURL := os.Getenv("SERV_CLOUD_URL")
	if cloudURL == "" {
		cloudURL = "http://localhost:8086"
	}

	url := fmt.Sprintf("%s/api/services/%s/rollback", strings.TrimSuffix(cloudURL, "/"), service)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		fmt.Printf("[Self-Healing] Failed to trigger rollback: %v\n", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		fmt.Printf("[Self-Healing] Rollback triggered successfully for service '%s'!\n", service)
		// Clear traces after successful rollback to reset error count
		s.traceStore.Clear()
	} else {
		fmt.Printf("[Self-Healing] Rollback failed with status: %d\n", resp.StatusCode)
	}
}

type TraceComparisonInfo struct {
	TraceID    string             `json:"traceId"`
	DurationMs float64            `json:"durationMs"`
	SpanCount  int                `json:"spanCount"`
	ErrorCount int                `json:"errorCount"`
	Services   map[string]float64 `json:"services"`
}

type TraceComparison struct {
	TraceA          TraceComparisonInfo `json:"traceA"`
	TraceB          TraceComparisonInfo `json:"traceB"`
	DurationDiffMs  float64             `json:"durationDiffMs"`
	SpanCountDiff   int                 `json:"spanCountDiff"`
	ErrorCountDiff  int                 `json:"errorCountDiff"`
	CommonServices  []string            `json:"commonServices"`
}

func (s *Server) handleCompareTraces(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	traceIDA := req.URL.Query().Get("a")
	traceIDB := req.URL.Query().Get("b")

	if traceIDA == "" || traceIDB == "" {
		http.Error(w, "Both 'a' and 'b' query parameters (trace IDs) are required", http.StatusBadRequest)
		return
	}

	treeA, okA := s.traceStore.GetTraceTree(traceIDA)
	treeB, okB := s.traceStore.GetTraceTree(traceIDB)

	if !okA || !okB {
		http.Error(w, "One or both traces not found", http.StatusNotFound)
		return
	}

	infoA := s.getComparisonInfo(traceIDA, treeA)
	infoB := s.getComparisonInfo(traceIDB, treeB)

	commonServices := []string{}
	for svc := range infoA.Services {
		if _, exists := infoB.Services[svc]; exists {
			commonServices = append(commonServices, svc)
		}
	}

	comparison := TraceComparison{
		TraceA:         infoA,
		TraceB:         infoB,
		DurationDiffMs: infoB.DurationMs - infoA.DurationMs,
		SpanCountDiff:  infoB.SpanCount - infoA.SpanCount,
		ErrorCountDiff: infoB.ErrorCount - infoA.ErrorCount,
		CommonServices: commonServices,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(comparison)
}

func (s *Server) getComparisonInfo(traceID string, root *store.SpanNode) TraceComparisonInfo {
	services := make(map[string]float64)
	spanCount := 0
	errorCount := 0

	var traverse func(node *store.SpanNode)
	traverse = func(node *store.SpanNode) {
		if node == nil {
			return
		}
		spanCount++
		if node.Span.Status == 2 {
			errorCount++
		}
		services[node.Span.Service] += node.DurationMs

		for _, child := range node.Children {
			traverse(child)
		}
	}

	traverse(root)

	duration := 0.0
	if root != nil {
		duration = root.DurationMs
	}

	return TraceComparisonInfo{
		TraceID:    traceID,
		DurationMs: duration,
		SpanCount:  spanCount,
		ErrorCount: errorCount,
		Services:   services,
	}
}

func (s *Server) startRetentionCleanerLoop() {
	ticker := time.NewTicker(5 * time.Second)
	go func() {
		for range ticker.C {
			s.traceStore.CleanExpiredTraces()
		}
	}()
}

type SpanProfilingInfo struct {
	SpanID   string  `json:"spanId"`
	SpanName string  `json:"spanName"`
	Service  string  `json:"service"`
	CPUMs    float64 `json:"cpuMs"`
	MemoryMB float64 `json:"memoryMb"`
	HotPath  string  `json:"hotPath,omitempty"`
}

type TraceProfilingSummary struct {
	TraceID       string              `json:"traceId"`
	TotalCPUMs    float64             `json:"totalCpuMs"`
	TotalMemoryMB float64             `json:"totalMemoryMb"`
	Spans         []SpanProfilingInfo `json:"spans"`
	HotPathSpan   string              `json:"hotPathSpan,omitempty"`
	HotPathReason string              `json:"hotPathReason,omitempty"`
}

func (s *Server) handleGetTraceProfiling(w http.ResponseWriter, req *http.Request, traceID string) {
	if req.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	tree, ok := s.traceStore.GetTraceTree(traceID)
	if !ok {
		http.Error(w, "Trace not found", http.StatusNotFound)
		return
	}

	var spansInfo []SpanProfilingInfo
	var totalCPU float64
	var totalMem float64

	var maxCPU float64
	var hotSpanName string
	var hotReason string

	var traverse func(node *store.SpanNode)
	traverse = func(node *store.SpanNode) {
		if node == nil {
			return
		}

		cpu := 0.0
		mem := 0.0
		hotPath := ""

		if node.Span.Attributes != nil {
			if cpuVal, ok := node.Span.Attributes["pprof.cpu_ms"]; ok {
				cpu = parseFloatSafe(cpuVal)
			}
			if memVal, ok := node.Span.Attributes["pprof.mem_mb"]; ok {
				mem = parseFloatSafe(memVal)
			}
			if hp, ok := node.Span.Attributes["pprof.hot_path"].(string); ok {
				hotPath = hp
			}
		}

		if cpu == 0 && node.DurationMs > 0 {
			cpu = node.DurationMs * 0.4
		}

		totalCPU += cpu
		totalMem += mem

		info := SpanProfilingInfo{
			SpanID:   node.Span.SpanID,
			SpanName: node.Span.Name,
			Service:  node.Span.Service,
			CPUMs:    cpu,
			MemoryMB: mem,
			HotPath:  hotPath,
		}
		spansInfo = append(spansInfo, info)

		if cpu > maxCPU {
			maxCPU = cpu
			hotSpanName = node.Span.Name
			if hotPath != "" {
				hotReason = fmt.Sprintf("Span '%s' consumed %.1fms CPU on hot path: %s", node.Span.Name, cpu, hotPath)
			} else {
				hotReason = fmt.Sprintf("Span '%s' consumed %.1fms CPU (estimated)", node.Span.Name, cpu)
			}
		}

		for _, child := range node.Children {
			traverse(child)
		}
	}

	traverse(tree)

	summary := TraceProfilingSummary{
		TraceID:       traceID,
		TotalCPUMs:    totalCPU,
		TotalMemoryMB: totalMem,
		Spans:         spansInfo,
		HotPathSpan:   hotSpanName,
		HotPathReason: hotReason,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)
}

func parseFloatSafe(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case string:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	}
	return 0.0
}



