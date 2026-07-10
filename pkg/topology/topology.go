package topology

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"servconsole/pkg/config"
)

type TopologyNode struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	Color     string  `json:"color"`
	Online    bool    `json:"online"`
	LatencyMs int64   `json:"latency_ms"`
	ErrorRate float64 `json:"error_rate"`
}

type TopologyEdge struct {
	From      string  `json:"from"`
	To        string  `json:"to"`
	Label     string  `json:"label"`
	LatencyMs int64   `json:"latency_ms"`
	ErrorRate float64 `json:"error_rate"`
}

type TopologyResponse struct {
	Nodes []TopologyNode `json:"nodes"`
	Edges []TopologyEdge `json:"edges"`
}

type LiveTopologyNode struct {
	ID           string  `json:"id"`
	Label        string  `json:"label"`
	Color        string  `json:"color"`
	Online       bool    `json:"online"`
	LatencyMs    int64   `json:"latency_ms"`
	ErrorRate    float64 `json:"error_rate"`
	ReqsSec      float64 `json:"reqs_sec"`
	RequestCount int64   `json:"request_count"`
	HealthScore  float64 `json:"health_score"`
}

type LiveTopologyEdge struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	Label      string  `json:"label"`
	LatencyMs  int64   `json:"latency_ms"`
	ErrorRate  float64 `json:"error_rate"`
	ReqsSec    float64 `json:"reqs_sec"`
	Throughput int64   `json:"throughput"`
}

type LiveTopologyResponse struct {
	Nodes        []LiveTopologyNode `json:"nodes"`
	Edges        []LiveTopologyEdge `json:"edges"`
	DiscoveredAt string             `json:"discovered_at"`
	SpanCount    int                `json:"span_count"`
}

type ReplayRequest struct {
	TraceID string `json:"traceId"`
}

type ReplayResponse struct {
	Success    bool   `json:"success"`
	StatusCode int    `json:"statusCode"`
	Body       string `json:"body"`
	Error      string `json:"error,omitempty"`
}

type WaterfallSpan struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Duration int64           `json:"duration_ms"`
	Offset   int64           `json:"offset_ms"`
	Children []WaterfallSpan `json:"children,omitempty"`
}

type WaterfallResponse struct {
	TraceID string        `json:"traceId"`
	Root    WaterfallSpan `json:"root"`
	ASCII   string        `json:"ascii"`
}

var (
	WriteJSONError func(http.ResponseWriter, *http.Request, string, string, int)
)

func Init(writeError func(http.ResponseWriter, *http.Request, string, string, int)) {
	WriteJSONError = writeError
}

func HandleTopology(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimSuffix(config.ActiveDiscovery.Store, "/") + "/console/traces")
	if err != nil {
		json.NewEncoder(w).Encode(TopologyResponse{Nodes: []TopologyNode{}, Edges: []TopologyEdge{}})
		return
	}
	defer resp.Body.Close()

	type rawSpan struct {
		Name         string    `json:"Name"`
		TraceID      string    `json:"TraceID"`
		SpanID       string    `json:"SpanID"`
		ParentSpanID string    `json:"ParentSpanID"`
		ServiceName  string    `json:"ServiceName"`
		DurationNs   int64     `json:"DurationNs"`
		StatusCode   string    `json:"StatusCode"`
		StartTime    time.Time `json:"StartTime"`
	}

	var spans []rawSpan
	if err := json.NewDecoder(resp.Body).Decode(&spans); err != nil {
		json.NewEncoder(w).Encode(TopologyResponse{Nodes: []TopologyNode{}, Edges: []TopologyEdge{}})
		return
	}

	nodesMap := make(map[string]*TopologyNode)
	edgesMap := make(map[string]*TopologyEdge)

	nodesMap["ServGate"] = &TopologyNode{ID: "ServGate", Label: "ServGate (Gateway)", Color: "#06b6d4", Online: true}
	nodesMap["ServStore"] = &TopologyNode{ID: "ServStore", Label: "ServStore (Storage)", Color: "#10b981", Online: true}
	nodesMap["ServQueue"] = &TopologyNode{ID: "ServQueue", Label: "ServQueue (Broker)", Color: "#f59e0b", Online: true}
	nodesMap["ServTunnel"] = &TopologyNode{ID: "ServTunnel", Label: "ServTunnel (Relay)", Color: "#6366f1", Online: true}

	spanToService := make(map[string]string)
	for _, span := range spans {
		svc := span.ServiceName
		if svc == "" {
			svc = "unknown-service"
		}
		spanToService[span.SpanID] = svc

		if _, exists := nodesMap[svc]; !exists {
			nodesMap[svc] = &TopologyNode{
				ID:     svc,
				Label:  svc,
				Color:  "#a855f7",
				Online: true,
			}
		}
	}

	for _, span := range spans {
		svc := span.ServiceName
		if svc == "" {
			svc = "unknown-service"
		}

		isErr := span.StatusCode == "error"
		latMs := span.DurationNs / 1e6

		nodesMap[svc].LatencyMs = (nodesMap[svc].LatencyMs + latMs) / 2
		if isErr {
			nodesMap[svc].ErrorRate = 0.1
		}

		if span.ParentSpanID != "" {
			if parentSvc, parentExists := spanToService[span.ParentSpanID]; parentExists && parentSvc != svc {
				edgeKey := fmt.Sprintf("%s->%s", parentSvc, svc)
				if _, exists := edgesMap[edgeKey]; !exists {
					edgesMap[edgeKey] = &TopologyEdge{
						From:      parentSvc,
						To:        svc,
						Label:     "Call",
						LatencyMs: latMs,
					}
				} else {
					edgesMap[edgeKey].LatencyMs = (edgesMap[edgeKey].LatencyMs + latMs) / 2
				}
				if isErr {
					edgesMap[edgeKey].ErrorRate = 0.2
				}
			}
		}

		if strings.Contains(span.Name, "PUT") || strings.Contains(span.Name, "GET") {
			edgeKey := fmt.Sprintf("%s->ServStore", svc)
			if _, exists := edgesMap[edgeKey]; !exists {
				edgesMap[edgeKey] = &TopologyEdge{
					From:      svc,
					To:        "ServStore",
					Label:     "S3",
					LatencyMs: latMs,
				}
			}
		}
		if strings.Contains(span.Name, "publish") || strings.Contains(span.Name, "subscribe") {
			edgeKey := fmt.Sprintf("%s->ServQueue", svc)
			if _, exists := edgesMap[edgeKey]; !exists {
				edgesMap[edgeKey] = &TopologyEdge{
					From:      svc,
					To:        "ServQueue",
					Label:     "STOMP",
					LatencyMs: latMs,
				}
			}
		}
	}

	var nodes []TopologyNode
	for _, n := range nodesMap {
		nodes = append(nodes, *n)
	}

	var edges []TopologyEdge
	for _, e := range edgesMap {
		edges = append(edges, *e)
	}

	for _, n := range nodes {
		if n.ID != "ServGate" && n.ID != "ServStore" && n.ID != "ServQueue" && n.ID != "ServTunnel" {
			edgeKey := fmt.Sprintf("ServGate->%s", n.ID)
			if _, exists := edgesMap[edgeKey]; !exists {
				edges = append(edges, TopologyEdge{
					From:      "ServGate",
					To:        n.ID,
					Label:     "HTTP",
					LatencyMs: 10,
				})
			}
		}
	}

	json.NewEncoder(w).Encode(TopologyResponse{Nodes: nodes, Edges: edges})
}

func HandleTopologyLive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimSuffix(*config.StoreUrl, "/") + "/console/traces")
	if err != nil {
		json.NewEncoder(w).Encode(LiveTopologyResponse{
			Nodes:        []LiveTopologyNode{},
			Edges:        []LiveTopologyEdge{},
			DiscoveredAt: time.Now().Format(time.RFC3339),
			SpanCount:    0,
		})
		return
	}
	defer resp.Body.Close()

	type rawSpan struct {
		Name         string    `json:"Name"`
		TraceID      string    `json:"TraceID"`
		SpanID       string    `json:"SpanID"`
		ParentSpanID string    `json:"ParentSpanID"`
		ServiceName  string    `json:"ServiceName"`
		DurationNs   int64     `json:"DurationNs"`
		StatusCode   string    `json:"StatusCode"`
		StartTime    time.Time `json:"StartTime"`
	}

	var spans []rawSpan
	if err := json.NewDecoder(resp.Body).Decode(&spans); err != nil {
		json.NewEncoder(w).Encode(LiveTopologyResponse{
			Nodes:        []LiveTopologyNode{},
			Edges:        []LiveTopologyEdge{},
			DiscoveredAt: time.Now().Format(time.RFC3339),
			SpanCount:    0,
		})
		return
	}

	type nodeStats struct {
		totalLatency int64
		requestCount int64
		errorCount   int64
	}

	type edgeStats struct {
		totalLatency int64
		requestCount int64
		errorCount   int64
	}

	nodeStatsMap := make(map[string]*nodeStats)
	edgeStatsMap := make(map[string]*edgeStats)
	nodesMap := make(map[string]*LiveTopologyNode)

	serviceColors := map[string]string{
		"ServGate":   "#06b6d4",
		"ServStore":  "#10b981",
		"ServQueue":  "#f59e0b",
		"ServTunnel": "#6366f1",
		"ServTrace":  "#a855f7",
	}

	for svc, color := range serviceColors {
		nodesMap[svc] = &LiveTopologyNode{ID: svc, Label: svc, Color: color, Online: true}
		nodeStatsMap[svc] = &nodeStats{}
	}

	spanToService := make(map[string]string)
	for _, span := range spans {
		svc := span.ServiceName
		if svc == "" {
			svc = "unknown-service"
		}
		spanToService[span.SpanID] = svc

		if _, exists := nodesMap[svc]; !exists {
			color := "#94a3b8"
			if c, ok := serviceColors[svc]; ok {
				color = c
			}
			nodesMap[svc] = &LiveTopologyNode{ID: svc, Label: svc, Color: color, Online: true}
			nodeStatsMap[svc] = &nodeStats{}
		}

		latMs := span.DurationNs / 1e6
		nodeStatsMap[svc].totalLatency += latMs
		nodeStatsMap[svc].requestCount++
		if span.StatusCode == "error" {
			nodeStatsMap[svc].errorCount++
		}
	}

	for _, span := range spans {
		if span.ParentSpanID == "" {
			continue
		}
		svc := span.ServiceName
		if svc == "" {
			svc = "unknown-service"
		}

		parentSvc, parentExists := spanToService[span.ParentSpanID]
		if !parentExists || parentSvc == svc {
			continue
		}

		edgeKey := parentSvc + "->" + svc
		latMs := span.DurationNs / 1e6

		if _, exists := edgeStatsMap[edgeKey]; !exists {
			edgeStatsMap[edgeKey] = &edgeStats{}
		}
		edgeStatsMap[edgeKey].totalLatency += latMs
		edgeStatsMap[edgeKey].requestCount++
		if span.StatusCode == "error" {
			edgeStatsMap[edgeKey].errorCount++
		}
	}

	for _, span := range spans {
		svc := span.ServiceName
		if svc == "" {
			svc = "unknown-service"
		}
		latMs := span.DurationNs / 1e6

		if strings.Contains(span.Name, "PUT") || strings.Contains(span.Name, "GET") || strings.Contains(span.Name, "DELETE") {
			if svc != "ServStore" {
				edgeKey := svc + "->ServStore"
				if _, exists := edgeStatsMap[edgeKey]; !exists {
					edgeStatsMap[edgeKey] = &edgeStats{totalLatency: latMs, requestCount: 1}
				}
			}
		}
		if strings.Contains(span.Name, "publish") || strings.Contains(span.Name, "subscribe") {
			if svc != "ServQueue" {
				edgeKey := svc + "->ServQueue"
				if _, exists := edgeStatsMap[edgeKey]; !exists {
					edgeStatsMap[edgeKey] = &edgeStats{totalLatency: latMs, requestCount: 1}
				}
			}
		}
	}

	var nodes []LiveTopologyNode
	for svcID, node := range nodesMap {
		ns := nodeStatsMap[svcID]
		if ns.requestCount > 0 {
			node.LatencyMs = ns.totalLatency / ns.requestCount
			node.ErrorRate = float64(ns.errorCount) / float64(ns.requestCount)
			node.ReqsSec = float64(ns.requestCount)
			node.RequestCount = ns.requestCount
		}
		health := 1.0 - node.ErrorRate
		if node.LatencyMs > 500 {
			health -= 0.15
		} else if node.LatencyMs > 200 {
			health -= 0.05
		}
		if health < 0 {
			health = 0
		}
		node.HealthScore = health
		nodes = append(nodes, *node)
	}

	var edges []LiveTopologyEdge
	for edgeKey, es := range edgeStatsMap {
		parts := strings.SplitN(edgeKey, "->", 2)
		if len(parts) != 2 {
			continue
		}
		avgLat := int64(0)
		errRate := 0.0
		if es.requestCount > 0 {
			avgLat = es.totalLatency / es.requestCount
			errRate = float64(es.errorCount) / float64(es.requestCount)
		}

		label := "Call"
		if parts[1] == "ServStore" {
			label = "S3"
		} else if parts[1] == "ServQueue" {
			label = "STOMP"
		} else if parts[0] == "ServGate" {
			label = "HTTP"
		}

		edges = append(edges, LiveTopologyEdge{
			From:       parts[0],
			To:         parts[1],
			Label:      label,
			LatencyMs:  avgLat,
			ErrorRate:  errRate,
			ReqsSec:    float64(es.requestCount),
			Throughput: es.requestCount,
		})
	}

	json.NewEncoder(w).Encode(LiveTopologyResponse{
		Nodes:        nodes,
		Edges:        edges,
		DiscoveredAt: time.Now().Format(time.RFC3339),
		SpanCount:    len(spans),
	})
}


func HandleTraceReplay(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
		return
	}

	var req ReplayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteJSONError(w, r, "Invalid request body", "ERR_INVALID_BODY", http.StatusBadRequest)
		return
	}

	traceID := req.TraceID
	if traceID == "" {
		WriteJSONError(w, r, "Trace ID is required", "ERR_TRACE_ID_REQUIRED", http.StatusBadRequest)
		return
	}

	traceDetailUrl := fmt.Sprintf("%s/api/traces/%s", *config.TraceUrl, traceID)
	resp, err := http.Get(traceDetailUrl)
	if err != nil {
		WriteJSONError(w, r, fmt.Sprintf("Failed to fetch trace from ServTrace: %v", err), "ERR_FETCH_TRACE_FAILED", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		WriteJSONError(w, r, "Trace not found in ServTrace", "ERR_TRACE_NOT_FOUND", http.StatusNotFound)
		return
	}

	var rootNode struct {
		Span struct {
			Name       string                 `json:"name"`
			Attributes map[string]interface{} `json:"attributes"`
		} `json:"span"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rootNode); err != nil {
		WriteJSONError(w, r, fmt.Sprintf("Failed to parse trace: %v", err), "ERR_PARSE_TRACE_FAILED", http.StatusInternalServerError)
		return
	}

	parts := strings.SplitN(rootNode.Span.Name, " ", 2)
	if len(parts) < 2 {
		WriteJSONError(w, r, "Invalid root span name format. Expected 'METHOD PATH'", "ERR_INVALID_SPAN_FORMAT", http.StatusBadRequest)
		return
	}
	method := parts[0]
	path := parts[1]

	bodyStr, _ := rootNode.Span.Attributes["http.request.body"].(string)
	contentType, _ := rootNode.Span.Attributes["http.request.header.content-type"].(string)

	gateReplayUrl := fmt.Sprintf("%s%s", *config.GateUrl, path)
	var gateReq *http.Request
	if bodyStr != "" {
		gateReq, err = http.NewRequest(method, gateReplayUrl, strings.NewReader(bodyStr))
	} else {
		gateReq, err = http.NewRequest(method, gateReplayUrl, nil)
	}

	if err != nil {
		WriteJSONError(w, r, fmt.Sprintf("Failed to create replay request: %v", err), "ERR_CREATE_REQUEST_FAILED", http.StatusInternalServerError)
		return
	}

	if contentType != "" {
		gateReq.Header.Set("Content-Type", contentType)
	}
	if *config.AuthToken != "" {
		gateReq.Header.Set("Authorization", "Bearer "+*config.AuthToken)
	}
	gateReq.Header.Set("X-Replayed-From", traceID)

	client := &http.Client{Timeout: 10 * time.Second}
	gateResp, err := client.Do(gateReq)
	if err != nil {
		json.NewEncoder(w).Encode(ReplayResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to replay request through ServGate: %v", err),
		})
		return
	}
	defer gateResp.Body.Close()

	gateBodyBytes, _ := io.ReadAll(gateResp.Body)

	json.NewEncoder(w).Encode(ReplayResponse{
		Success:    gateResp.StatusCode >= 200 && gateResp.StatusCode < 300,
		StatusCode: gateResp.StatusCode,
		Body:       string(gateBodyBytes),
	})
}

func HandleTraceWaterfall(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	traceID := r.URL.Query().Get("traceId")
	if traceID == "" {
		traceID = "tr-mock-123"
	}

	root := WaterfallSpan{
		ID:       "span-root",
		Name:     "GET /api/v1/checkout",
		Duration: 120,
		Offset:   0,
		Children: []WaterfallSpan{
			{
				ID:       "span-auth",
				Name:     "ServAuth /api/auth/keys/validate",
				Duration: 15,
				Offset:   5,
			},
			{
				ID:       "span-db-read",
				Name:     "ServDB SELECT * FROM products",
				Duration: 45,
				Offset:   25,
			},
			{
				ID:       "span-queue-pub",
				Name:     "ServQueue PUBLISH order.created",
				Duration: 30,
				Offset:   75,
				Children: []WaterfallSpan{
					{
						ID:       "span-queue-network",
						Name:     "TCP Handshake & TLS",
						Duration: 10,
						Offset:   77,
					},
				},
			},
		},
	}

	var ascii strings.Builder
	ascii.WriteString(fmt.Sprintf("Trace Waterfall: %s\n", traceID))
	ascii.WriteString("====================================================\n")

	var printSpan func(s WaterfallSpan, depth int)
	printSpan = func(s WaterfallSpan, depth int) {
		indent := strings.Repeat("  ", depth)
		barLength := int(s.Duration / 5)
		if barLength < 1 {
			barLength = 1
		}
		offsetSpaces := int(s.Offset / 5)
		timelineBar := strings.Repeat(" ", offsetSpaces) + "[" + strings.Repeat("█", barLength) + "]"

		ascii.WriteString(fmt.Sprintf("%-35s %3dms %s\n", indent+s.Name, s.Duration, timelineBar))
		for _, child := range s.Children {
			printSpan(child, depth+1)
		}
	}
	printSpan(root, 0)

	resp := WaterfallResponse{
		TraceID: traceID,
		Root:    root,
		ASCII:   ascii.String(),
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}
