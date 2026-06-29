package store

import (
	"strconv"
	"sync"
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

type Store struct {
	mu      sync.RWMutex
	spans   map[string][]Span // key: traceId
	limit   int
	order   []string // FIFO queue of traceIds for eviction
	OnEvict func(traceID string, spans []Span)
}

func NewStore(limit int) *Store {
	return &Store{
		spans: make(map[string][]Span),
		limit: limit,
	}
}

func (s *Store) AddSpans(newSpans []Span) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
				if s.OnEvict != nil && len(evicted) > 0 {
					go s.OnEvict(oldest, evicted)
				}
			}
			s.spans[traceID] = []Span{}
			s.order = append(s.order, traceID)
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
			s.spans[traceID] = append(s.spans[traceID], span)
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
