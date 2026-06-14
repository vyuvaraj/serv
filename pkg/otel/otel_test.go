package otel

import (
	"context"
	"testing"
)

func TestOtelTracing(t *testing.T) {
	// 1. Test parent extraction
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	traceID, parentID := ExtractTraceparent(tp)
	if traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("Expected traceID 4bf92f3577b34da6a3ce929d0e0e4736, got %s", traceID)
	}
	if parentID != "00f067aa0ba902b7" {
		t.Errorf("Expected parentID 00f067aa0ba902b7, got %s", parentID)
	}

	// 2. Enable Otel for test duration
	otelEnabled = true
	defer func() {
		otelEnabled = false
	}()

	// 3. Start span from traceparent
	ctx, span := StartSpanWithParent(context.Background(), "RootSpan", 2, tp)
	if span.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("Expected TraceID 4bf92f3577b34da6a3ce929d0e0e4736, got %s", span.TraceID)
	}
	if span.ParentID != "00f067aa0ba902b7" {
		t.Errorf("Expected ParentID 00f067aa0ba902b7, got %s", span.ParentID)
	}

	// 4. Start child span (should propagate context)
	_, childSpan := StartSpan(ctx, "ChildSpan", 1)
	if childSpan.TraceID != span.TraceID {
		t.Errorf("Expected child TraceID %s, got %s", span.TraceID, childSpan.TraceID)
	}
	if childSpan.ParentID != span.SpanID {
		t.Errorf("Expected child ParentID %s, got %s", span.SpanID, childSpan.ParentID)
	}

	// 5. Test traceparent generation
	tpOut := Traceparent(ctx)
	if tpOut == "" {
		t.Error("Expected traceparent output, got empty")
	}
}
