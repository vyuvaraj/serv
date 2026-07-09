package store

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Span struct {
	TraceID      string                 `json:"traceId"`
	SpanID       string                 `json:"spanId"`
	ParentSpanID string                 `json:"parentSpanId,omitempty"`
	Name         string                 `json:"name"`
	Kind         int                    `json:"kind"`
	StartTime    int64                  `json:"startTimeUnixNano"` // Store as int64 internally
	EndTime      int64                  `json:"endTimeUnixNano"`
	Attributes   map[string]interface{} `json:"attributes,omitempty"`
	Status       int                    `json:"status"`
	Service      string                 `json:"service"`
}

type SpanNode struct {
	Span             Span        `json:"span"`
	Children         []*SpanNode `json:"children,omitempty"`
	DurationMs       float64     `json:"durationMs"`
	OffsetMs         float64     `json:"offsetMs"`
}

type TraceSummary struct {
	TraceID      string  `json:"traceId"`
	RootName     string  `json:"rootName"`
	Service      string  `json:"service"`
	DurationMs   float64 `json:"durationMs"`
	TotalSpans   int     `json:"totalSpans"`
	ErrorCount   int     `json:"errorCount"`
	TimestampNano int64  `json:"timestampUnixNano"`
}

type MetricSummary struct {
	Service    string  `json:"service"`
	SpanName   string  `json:"span_name"`
	Throughput float64 `json:"throughput_rpm"`
	P50        float64 `json:"p50_ms"`
	P90        float64 `json:"p90_ms"`
	P99        float64 `json:"p99_ms"`
}

type LogLine struct {
	Timestamp time.Time `json:"timestamp"`
	Service   string    `json:"service"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
}

type Anomaly struct {
	TraceID     string    `json:"traceId"`
	Description string    `json:"description"`
	Timestamp   time.Time `json:"timestamp"`
}

type Store struct {
	mu                     sync.RWMutex
	spans                  map[string][]Span // key: traceId
	limit                  int
	order                  []string // FIFO queue of traceIds for eviction
	OnEvict                func(traceID string, spans []Span)
	samplingRate           int
	sampled                map[string]bool // key: traceId
	
	baseSamplingRate       int
	spanCount              int
	errorCount             int
	adaptiveErrorThreshold float64

	// Metrics fields
	latencies  map[string][]float64   // key: service:spanName -> list of latencies in ms
	timestamps map[string][]time.Time // key: service:spanName -> list of hit timestamps
	traceLogs  map[string][]LogLine   // key: traceId
	anomalies  []Anomaly
}

func NewStore(limit int) *Store {
	samplingRate := 50
	if env := os.Getenv("SERV_TRACE_SAMPLING_RATE"); env != "" {
		if val, err := strconv.Atoi(env); err == nil {
			samplingRate = val
		}
	}
	adaptiveErrorThreshold := 0.05
	if env := os.Getenv("SERV_TRACE_ADAPTIVE_ERROR_THRESHOLD"); env != "" {
		if val, err := strconv.ParseFloat(env, 64); err == nil {
			adaptiveErrorThreshold = val
		}
	}
	return &Store{
		spans:                  make(map[string][]Span),
		limit:                  limit,
		samplingRate:           samplingRate,
		baseSamplingRate:       samplingRate,
		adaptiveErrorThreshold: adaptiveErrorThreshold,
		sampled:                make(map[string]bool),
		latencies:              make(map[string][]float64),
		timestamps:             make(map[string][]time.Time),
		traceLogs:              make(map[string][]LogLine),
		anomalies:              make([]Anomaly, 0),
	}
}

func (s *Store) AddSpans(newSpans []Span) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, span := range newSpans {
		s.spanCount++
		if span.Status == 2 {
			s.errorCount++
		}
		if s.spanCount >= 100 {
			s.spanCount = int(float64(s.spanCount) * 0.5)
			s.errorCount = int(float64(s.errorCount) * 0.5)
		}
	}

	if s.spanCount > 10 {
		errRate := float64(s.errorCount) / float64(s.spanCount)
		if errRate > s.adaptiveErrorThreshold {
			s.samplingRate = 100
		} else {
			s.samplingRate = s.baseSamplingRate
		}
	}

	for _, span := range newSpans {
		traceID := span.TraceID
		if traceID == "" {
			continue
		}

		if _, exists := s.spans[traceID]; !exists {
			// Enforce limit eviction
			if len(s.spans) >= s.limit && len(s.order) > 0 {
				oldest := s.order[0]
				s.order = s.order[1:]
				evicted := s.spans[oldest]
				delete(s.spans, oldest)
				
				// Tail-based sampling evaluation during eviction!
				isSampled := s.sampled[oldest]
				delete(s.sampled, oldest)
				delete(s.traceLogs, oldest)
				
				if isSampled && s.OnEvict != nil && len(evicted) > 0 {
					go s.OnEvict(oldest, evicted)
				}
			}
			s.spans[traceID] = []Span{}
			s.order = append(s.order, traceID)
			
			// Head-based sampling: hash trace ID to determine initial sampling decision
			hash := 0
			for _, char := range traceID {
				hash += int(char)
			}
			rate := s.getServiceSamplingRate(span.Service)
			s.sampled[traceID] = (hash % 100) < rate
		}

		// Prevent duplicate spans
		duplicate := false
		for _, existing := range s.spans[traceID] {
			if existing.SpanID == span.SpanID {
				duplicate = true
				break
			}
		}

		if !duplicate {
			// Propagate baggage attributes from parent if present
			if span.ParentSpanID != "" {
				var parentSpan *Span
				for i := range s.spans[traceID] {
					if s.spans[traceID][i].SpanID == span.ParentSpanID {
						parentSpan = &s.spans[traceID][i]
						break
					}
				}
				if parentSpan == nil {
					for i := range newSpans {
						if newSpans[i].SpanID == span.ParentSpanID && newSpans[i].TraceID == traceID {
							parentSpan = &newSpans[i]
							break
						}
					}
				}

				if parentSpan != nil && parentSpan.Attributes != nil {
					if span.Attributes == nil {
						span.Attributes = make(map[string]interface{})
					}
					for k, v := range parentSpan.Attributes {
						if strings.HasPrefix(k, "baggage.") {
							if _, exists := span.Attributes[k]; !exists {
								span.Attributes[k] = v
							}
						}
					}
				}
			}

			s.spans[traceID] = append(s.spans[traceID], span)
			
			// Tail-based sampling override: always keep traces with errors or slow query alerts!
			isError := span.Status == 2
			isSlowQuery := false
			if span.Attributes != nil {
				if sq, ok := span.Attributes["db.slow_query"]; ok {
					isSlowQuery = (sq == true || sq == "true")
				}
			}
			if isError || isSlowQuery {
				s.sampled[traceID] = true
			}

			// Span metrics calculation!
			s.recordSpanMetrics(span)
		}
	}
}

func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans = make(map[string][]Span)
	s.order = nil
}

func (s *Store) ListTraces() []TraceSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	summaries := make([]TraceSummary, 0, len(s.spans))

	for traceID, spans := range s.spans {
		if len(spans) == 0 {
			continue
		}

		// Find root span or oldest span
		var root Span
		foundRoot := false
		minStart := spans[0].StartTime
		maxEnd := spans[0].EndTime
		errCount := 0

		for _, span := range spans {
			if span.ParentSpanID == "" {
				root = span
				foundRoot = true
			}
			if span.StartTime < minStart {
				minStart = span.StartTime
			}
			if span.EndTime > maxEnd {
				maxEnd = span.EndTime
			}
			if span.Status == 2 { // status 2 = error
				errCount++
			}
		}

		if !foundRoot {
			// Fallback: pick the first/earliest span as root
			for _, span := range spans {
				if span.StartTime == minStart {
					root = span
					break
				}
			}
		}

		durationMs := float64(maxEnd-minStart) / 1e6
		if durationMs < 0 {
			durationMs = 0
		}

		summaries = append(summaries, TraceSummary{
			TraceID:       traceID,
			RootName:      root.Name,
			Service:       root.Service,
			DurationMs:    durationMs,
			TotalSpans:    len(spans),
			ErrorCount:    errCount,
			TimestampNano: minStart,
		})
	}

	return summaries
}

func (s *Store) GetTraceTree(traceID string) (*SpanNode, bool) {
	s.mu.RLock()
	spans, ok := s.spans[traceID]
	s.mu.RUnlock()

	if !ok || len(spans) == 0 {
		return nil, false
	}

	// Group by parent
	parentMap := make(map[string][]*SpanNode)
	var rootNode *SpanNode
	minStart := spans[0].StartTime

	for _, span := range spans {
		if span.StartTime < minStart {
			minStart = span.StartTime
		}
	}

	nodes := make(map[string]*SpanNode)
	for _, span := range spans {
		node := &SpanNode{
			Span:       span,
			DurationMs: float64(span.EndTime-span.StartTime) / 1e6,
			OffsetMs:   float64(span.StartTime-minStart) / 1e6,
		}
		if node.DurationMs < 0 {
			node.DurationMs = 0
		}
		if node.OffsetMs < 0 {
			node.OffsetMs = 0
		}
		nodes[span.SpanID] = node
	}

	// Link parents and children
	for _, node := range nodes {
		pID := node.Span.ParentSpanID
		if pID == "" {
			rootNode = node
		} else {
			parentMap[pID] = append(parentMap[pID], node)
		}
	}

	// Fallback if no root span with empty ParentSpanID exists
	if rootNode == nil {
		var earliest *SpanNode
		for _, node := range nodes {
			if earliest == nil || node.Span.StartTime < earliest.Span.StartTime {
				earliest = node
			}
		}
		rootNode = earliest
	}

	// Build tree recursively
	var buildTree func(node *SpanNode)
	buildTree = func(node *SpanNode) {
		if node == nil {
			return
		}
		children := parentMap[node.Span.SpanID]
		node.Children = children
		for _, child := range children {
			buildTree(child)
		}
	}

	buildTree(rootNode)

	return rootNode, true
}

type DependencyNode struct {
	ID         string  `json:"id"`
	Throughput int     `json:"throughput"`
	LatencyMs  float64 `json:"latency_ms"`
	ErrorRate  float64 `json:"error_rate"`
}

type DependencyEdge struct {
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Throughput int     `json:"throughput"`
	LatencyMs  float64 `json:"latency_ms"`
	ErrorRate  float64 `json:"error_rate"`
}

type DependencyGraph struct {
	Nodes []DependencyNode `json:"nodes"`
	Edges []DependencyEdge `json:"edges"`
}

func (s *Store) GenerateDependencyGraph() DependencyGraph {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type stats struct {
		requestCount int
		totalLatency float64
		errorCount   int
	}

	nodeStats := make(map[string]*stats)
	edgeStats := make(map[string]map[string]*stats) // source -> target -> stats
	
	// First pass: locate span info and map span ID to service
	spanServiceMap := make(map[string]string)
	for _, spans := range s.spans {
		for _, span := range spans {
			spanServiceMap[span.SpanID] = span.Service
		}
	}

	for _, spans := range s.spans {
		for _, span := range spans {
			svc := span.Service
			if svc == "" {
				svc = "unknown"
			}

			// Node stats
			ns, ok := nodeStats[svc]
			if !ok {
				ns = &stats{}
				nodeStats[svc] = ns
			}
			ns.requestCount++
			latency := float64(span.EndTime-span.StartTime) / 1e6
			if latency < 0 {
				latency = 0
			}
			ns.totalLatency += latency
			if span.Status == 2 {
				ns.errorCount++
			}

			// Edge stats
			if span.ParentSpanID != "" {
				parentSvc, ok := spanServiceMap[span.ParentSpanID]
				if ok && parentSvc != svc {
					// We have a service-to-service call: parentSvc -> svc
					if _, ok := edgeStats[parentSvc]; !ok {
						edgeStats[parentSvc] = make(map[string]*stats)
					}
					es, ok := edgeStats[parentSvc][svc]
					if !ok {
						es = &stats{}
						edgeStats[parentSvc][svc] = es
					}
					es.requestCount++
					es.totalLatency += latency
					if span.Status == 2 {
						es.errorCount++
					}
				}
			}
		}
	}

	// Build nodes list
	nodes := make([]DependencyNode, 0, len(nodeStats))
	for id, ns := range nodeStats {
		avgLatency := 0.0
		if ns.requestCount > 0 {
			avgLatency = ns.totalLatency / float64(ns.requestCount)
		}
		errRate := 0.0
		if ns.requestCount > 0 {
			errRate = float64(ns.errorCount) / float64(ns.requestCount)
		}
		nodes = append(nodes, DependencyNode{
			ID:         id,
			Throughput: ns.requestCount,
			LatencyMs:  avgLatency,
			ErrorRate:  errRate,
		})
	}

	// Build edges list
	var edges []DependencyEdge
	for src, targets := range edgeStats {
		for tgt, es := range targets {
			avgLatency := 0.0
			if es.requestCount > 0 {
				avgLatency = es.totalLatency / float64(es.requestCount)
			}
			errRate := 0.0
			if es.requestCount > 0 {
				errRate = float64(es.errorCount) / float64(es.requestCount)
			}
			edges = append(edges, DependencyEdge{
				Source:     src,
				Target:     tgt,
				Throughput: es.requestCount,
				LatencyMs:  avgLatency,
				ErrorRate:  errRate,
			})
		}
	}

	return DependencyGraph{
		Nodes: nodes,
		Edges: edges,
	}
}

// ParseInt64Safe helper for OTLP timestamps
func ParseInt64Safe(v interface{}) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case float64:
		return int64(val)
	case string:
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
	}
	return 0
}

func (s *Store) recordSpanMetrics(span Span) {
	if span.Service == "" || span.Name == "" {
		return
	}

	key := span.Service + ":" + span.Name
	durationNano := span.EndTime - span.StartTime
	durationMs := float64(durationNano) / 1000000.0

	// Get current historical rolling average for anomaly detection BEFORE adding the new span
	var currentP90 float64
	existingLats := s.latencies[key]
	if len(existingLats) > 5 {
		// Calculate rolling p90
		sorted := make([]float64, len(existingLats))
		copy(sorted, existingLats)
		sort.Float64s(sorted)
		idx := int(float64(len(sorted)) * 0.9)
		currentP90 = sorted[idx]
	}

	// Record latency and timestamp
	s.latencies[key] = append(s.latencies[key], durationMs)
	s.timestamps[key] = append(s.timestamps[key], time.Now())

	// Cap history to last 100 entries for rolling metrics
	if len(s.latencies[key]) > 100 {
		s.latencies[key] = s.latencies[key][1:]
	}
	if len(s.timestamps[key]) > 100 {
		s.timestamps[key] = s.timestamps[key][1:]
	}

	// Anomaly Detection:
	// 1. Latency Spike: if duration > 3 * currentP90 (and we have enough samples)
	if currentP90 > 0 && durationMs > 3*currentP90 {
		desc := fmt.Sprintf("Latency spike in service %s (span: %s): %.2fms exceeds 3x rolling P90 (%.2fms)", span.Service, span.Name, durationMs, currentP90)
		fmt.Printf("[ANOMALY_DETECTION] %s\n", desc)
		s.anomalies = append(s.anomalies, Anomaly{TraceID: span.TraceID, Description: desc, Timestamp: time.Now()})
	}

	// 2. Error Burst: check error rate in the last 10 samples
	if len(s.spans[span.TraceID]) > 0 {
		errCount := 0
		totalInTrace := len(s.spans[span.TraceID])
		for _, sp := range s.spans[span.TraceID] {
			if sp.Status == 2 {
				errCount++
			}
		}
		if totalInTrace >= 3 && float64(errCount)/float64(totalInTrace) > 0.3 {
			desc := fmt.Sprintf("Error burst in trace %s: %d errors in %d spans (%.1f%% error rate)", span.TraceID, errCount, totalInTrace, float64(errCount)/float64(totalInTrace)*100.0)
			fmt.Printf("[ANOMALY_DETECTION] %s\n", desc)
			s.anomalies = append(s.anomalies, Anomaly{TraceID: span.TraceID, Description: desc, Timestamp: time.Now()})
		}
	}

	if len(s.anomalies) > 100 {
		s.anomalies = s.anomalies[1:]
	}
}

func (s *Store) GetMetrics() []MetricSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var summaries []MetricSummary

	for key, lats := range s.latencies {
		if len(lats) == 0 {
			continue
		}

		parts := strings.Split(key, ":")
		if len(parts) < 2 {
			continue
		}
		service := parts[0]
		spanName := parts[1]

		// Calculate percentiles
		sorted := make([]float64, len(lats))
		copy(sorted, lats)
		sort.Float64s(sorted)

		p50 := sorted[int(float64(len(sorted))*0.5)]
		p90 := sorted[int(float64(len(sorted))*0.9)]
		p99 := sorted[int(float64(len(sorted))*0.99)]

		// Calculate throughput: hits in the last 60 seconds
		now := time.Now()
		hits := 0
		for _, ts := range s.timestamps[key] {
			if now.Sub(ts) <= 1*time.Minute {
				hits++
			}
		}
		throughput := float64(hits) // requests per minute (rpm)

		summaries = append(summaries, MetricSummary{
			Service:    service,
			SpanName:   spanName,
			Throughput: throughput,
			P50:        p50,
			P90:        p90,
			P99:        p99,
		})
	}

	return summaries
}

func (s *Store) AddLog(traceID string, log LogLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traceLogs[traceID] = append(s.traceLogs[traceID], log)
	// Cap logs to 100 per trace to avoid infinite growth
	if len(s.traceLogs[traceID]) > 100 {
		s.traceLogs[traceID] = s.traceLogs[traceID][1:]
	}
}

func (s *Store) GetLogs(traceID string) []LogLine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	logs, ok := s.traceLogs[traceID]
	if !ok {
		return []LogLine{}
	}
	
	logsCopy := make([]LogLine, len(logs))
	copy(logsCopy, logs)
	return logsCopy
}

func (s *Store) GetAnomalies() []Anomaly {
	s.mu.RLock()
	defer s.mu.RUnlock()

	anomaliesCopy := make([]Anomaly, len(s.anomalies))
	copy(anomaliesCopy, s.anomalies)
	return anomaliesCopy
}

func (s *Store) GetSamplingRateForTest() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.samplingRate
}

func (s *Store) getServiceSamplingRate(service string) int {
	if service == "" {
		return s.samplingRate
	}
	normalized := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(service, "-", "_"), ".", "_"))
	envKey := "SERV_TRACE_SAMPLING_RATE_" + normalized
	if val := os.Getenv(envKey); val != "" {
		if rate, err := strconv.Atoi(val); err == nil {
			return rate
		}
	}
	return s.samplingRate
}

func (s *Store) getServiceTTL(service string) time.Duration {
	defaultTTL := 24 * time.Hour
	if val := os.Getenv("SERV_TRACE_DEFAULT_TTL"); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			defaultTTL = d
		}
	}
	if service == "" {
		return defaultTTL
	}
	normalized := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(service, "-", "_"), ".", "_"))
	envKey := "SERV_TRACE_TTL_" + normalized
	if val := os.Getenv(envKey); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultTTL
}

func (s *Store) CleanExpiredTraces() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var activeOrder []string

	for _, traceID := range s.order {
		spans, exists := s.spans[traceID]
		if !exists || len(spans) == 0 {
			continue
		}

		minStartNano := spans[0].StartTime
		service := spans[0].Service
		for _, span := range spans {
			if span.StartTime < minStartNano {
				minStartNano = span.StartTime
				service = span.Service
			}
		}

		ttl := s.getServiceTTL(service)
		startTime := time.Unix(0, minStartNano)

		if now.Sub(startTime) > ttl {
			evicted := s.spans[traceID]
			delete(s.spans, traceID)
			isSampled := s.sampled[traceID]
			delete(s.sampled, traceID)
			delete(s.traceLogs, traceID)

			if isSampled && s.OnEvict != nil && len(evicted) > 0 {
				go s.OnEvict(traceID, evicted)
			}
		} else {
			activeOrder = append(activeOrder, traceID)
		}
	}
	s.order = activeOrder
}


