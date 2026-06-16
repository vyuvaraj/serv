package otel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type contextKey string

const (
	spanContextKey contextKey = "otel-span-ctx"
)

var (
	otelEnabled       bool
	otelEndpoint      string
	otelService       string
	spanBuffer        []otelSpan
	spanBufferMu      sync.Mutex
	spanFlushInterval = 2 * time.Second
	maxBatchSize      = 50
	flushChan         = make(chan struct{}, 1)

	recentSpans     []otelSpan
	recentSpansMu   sync.RWMutex
	maxRecentSpans  = 100
)

type otelSpan struct {
	TraceID    string                 `json:"traceId"`
	SpanID     string                 `json:"spanId"`
	ParentID   string                 `json:"parentSpanId,omitempty"`
	Name       string                 `json:"name"`
	Kind       int                    `json:"kind"` // 1=internal, 2=server, 3=client
	StartTime  int64                  `json:"startTimeUnixNano"`
	EndTime    int64                  `json:"endTimeUnixNano"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
	Status     int                    `json:"status"` // 0=unset, 1=ok, 2=error
}

type Span struct {
	TraceID    string
	SpanID     string
	ParentID   string
	Name       string
	Kind       int
	StartTime  time.Time
	Attributes map[string]interface{}
	mu         sync.Mutex
}

func InitOtel(serviceName string) {
	endpoint := os.Getenv("OTEL_ENDPOINT")
	if endpoint == "" {
		return
	}

	otelEndpoint = strings.TrimSuffix(endpoint, "/")
	otelEnabled = true

	otelService = os.Getenv("OTEL_SERVICE_NAME")
	if otelService == "" {
		otelService = serviceName
	}
	if otelService == "" {
		otelService = "servstore"
	}

	go otelFlushLoop()
	log.Printf("[OTEL] OpenTelemetry Tracing enabled: endpoint=%s, service=%s", otelEndpoint, otelService)
}

func OtelEnabled() bool {
	return otelEnabled
}

func generateID(size int) string {
	b := make([]byte, size)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func ExtractTraceparent(tp string) (string, string) {
	if tp == "" {
		return "", ""
	}
	parts := strings.Split(tp, "-")
	if len(parts) >= 4 {
		return parts[1], parts[2]
	}
	return "", ""
}

func StartSpan(ctx context.Context, name string, kind int) (context.Context, *Span) {
	if !otelEnabled {
		return ctx, &Span{}
	}

	var traceID, parentID string

	// Try to get trace ID from parent span in context
	if parentSpan, ok := ctx.Value(spanContextKey).(*Span); ok && parentSpan != nil {
		traceID = parentSpan.TraceID
		parentID = parentSpan.SpanID
	} else {
		traceID = generateID(16)
	}

	span := &Span{
		TraceID:    traceID,
		SpanID:     generateID(8),
		ParentID:   parentID,
		Name:       name,
		Kind:       kind,
		StartTime:  time.Now(),
		Attributes: make(map[string]interface{}),
	}

	childCtx := context.WithValue(ctx, spanContextKey, span)
	return childCtx, span
}

func StartSpanWithParent(ctx context.Context, name string, kind int, parentTraceparent string) (context.Context, *Span) {
	if !otelEnabled {
		return ctx, &Span{}
	}

	traceID, parentID := ExtractTraceparent(parentTraceparent)
	if traceID == "" {
		traceID = generateID(16)
	}

	span := &Span{
		TraceID:    traceID,
		SpanID:     generateID(8),
		ParentID:   parentID,
		Name:       name,
		Kind:       kind,
		StartTime:  time.Now(),
		Attributes: make(map[string]interface{}),
	}

	childCtx := context.WithValue(ctx, spanContextKey, span)
	return childCtx, span
}

func (s *Span) SetAttribute(key string, value interface{}) {
	if !otelEnabled || s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Attributes[key] = value
}

func (s *Span) End(status int) {
	if !otelEnabled || s == nil || s.TraceID == "" {
		return
	}

	s.mu.Lock()
	attrs := make(map[string]interface{}, len(s.Attributes))
	for k, v := range s.Attributes {
		attrs[k] = v
	}
	s.mu.Unlock()

	span := otelSpan{
		TraceID:    s.TraceID,
		SpanID:     s.SpanID,
		ParentID:   s.ParentID,
		Name:       s.Name,
		Kind:       s.Kind,
		StartTime:  s.StartTime.UnixNano(),
		EndTime:    time.Now().UnixNano(),
		Attributes: attrs,
		Status:     status,
	}

	// Capture in local ring buffer
	recentSpansMu.Lock()
	recentSpans = append(recentSpans, span)
	if len(recentSpans) > maxRecentSpans {
		recentSpans = recentSpans[len(recentSpans)-maxRecentSpans:]
	}
	recentSpansMu.Unlock()

	spanBufferMu.Lock()
	spanBuffer = append(spanBuffer, span)
	if len(spanBuffer) >= maxBatchSize {
		batch := spanBuffer
		spanBuffer = nil
		spanBufferMu.Unlock()
		go exportSpans(batch)
	} else {
		spanBufferMu.Unlock()
	}
}

func GetRecentSpans() []otelSpan {
	recentSpansMu.RLock()
	defer recentSpansMu.RUnlock()
	res := make([]otelSpan, len(recentSpans))
	copy(res, recentSpans)
	return res
}

func Traceparent(ctx context.Context) string {
	if parentSpan, ok := ctx.Value(spanContextKey).(*Span); ok && parentSpan != nil {
		return fmt.Sprintf("00-%s-%s-01", parentSpan.TraceID, parentSpan.SpanID)
	}
	return ""
}

func otelFlushLoop() {
	ticker := time.NewTicker(spanFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			spanBufferMu.Lock()
			if len(spanBuffer) == 0 {
				spanBufferMu.Unlock()
				continue
			}
			batch := spanBuffer
			spanBuffer = nil
			spanBufferMu.Unlock()
			exportSpans(batch)
		case <-flushChan:
			spanBufferMu.Lock()
			if len(spanBuffer) == 0 {
				spanBufferMu.Unlock()
				continue
			}
			batch := spanBuffer
			spanBuffer = nil
			spanBufferMu.Unlock()
			exportSpans(batch)
		}
	}
}

func exportSpans(spans []otelSpan) {
	if len(spans) == 0 {
		return
	}

	payload := map[string]interface{}{
		"resourceSpans": []map[string]interface{}{
			{
				"resource": map[string]interface{}{
					"attributes": []map[string]interface{}{
						{"key": "service.name", "value": map[string]interface{}{"stringValue": otelService}},
					},
				},
				"scopeSpans": []map[string]interface{}{
					{
						"scope": map[string]interface{}{"name": "servstore-runtime"},
						"spans": buildSpanPayload(spans),
					},
				},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	url := otelEndpoint + "/v1/traces"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
}

func buildSpanPayload(spans []otelSpan) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(spans))
	for _, s := range spans {
		span := map[string]interface{}{
			"traceId":            s.TraceID,
			"spanId":             s.SpanID,
			"name":              s.Name,
			"kind":              s.Kind,
			"startTimeUnixNano": fmt.Sprintf("%d", s.StartTime),
			"endTimeUnixNano":   fmt.Sprintf("%d", s.EndTime),
			"status":            map[string]interface{}{"code": s.Status},
		}
		if s.ParentID != "" {
			span["parentSpanId"] = s.ParentID
		}
		if len(s.Attributes) > 0 {
			attrs := make([]map[string]interface{}, 0)
			for k, v := range s.Attributes {
				attrs = append(attrs, map[string]interface{}{
					"key":   k,
					"value": map[string]interface{}{"stringValue": fmt.Sprint(v)},
				})
			}
			span["attributes"] = attrs
		}
		result = append(result, span)
	}
	return result
}
