package otel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/vyuvaraj/serv/packages/ServShared"
)

type contextKey string

const (
	spanContextKey contextKey = "otel-span-ctx"
)

var (
	otelEnabled    bool
	otelService    string
	recentSpans    []otelSpan
	recentSpansMu  sync.RWMutex
	maxRecentSpans = 100
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
	ServShared.InitTrace(serviceName)
	otelEnabled = os.Getenv("OTEL_ENDPOINT") != "" || os.Getenv("SERV_OTLP_ENDPOINT") != "" || os.Getenv("SERVVERSE_DISCOVERY") != ""
	otelService = serviceName
	log.Printf("[OTEL] OpenTelemetry Tracing initialized (via ServShared) for service=%s", otelService)
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

	// Capture in local ring buffer for web UI compatibility
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

	recentSpansMu.Lock()
	recentSpans = append(recentSpans, span)
	if len(recentSpans) > maxRecentSpans {
		recentSpans = recentSpans[len(recentSpans)-maxRecentSpans:]
	}
	recentSpansMu.Unlock()

	// Delegate export to ServShared
	sharedSpan := &ServShared.Span{
		TraceID:   s.TraceID,
		SpanID:    s.SpanID,
		ParentID:  s.ParentID,
		Name:      s.Name,
		Kind:      s.Kind,
		StartTime: s.StartTime.UnixNano(),
	}
	var err error
	if status == 2 {
		err = fmt.Errorf("span status error")
	}
	ServShared.EndSpan(sharedSpan, err, attrs)
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
