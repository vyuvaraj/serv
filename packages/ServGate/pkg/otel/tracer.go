package otel

import (
	"github.com/vyuvaraj/serv/packages/ServShared"
)

type Span = ServShared.Span

func Init() {
	ServShared.InitTrace("github.com/vyuvaraj/serv/packages/ServGate")
}

func GenerateTraceID() string {
	return ServShared.GenerateTraceID()
}

func GenerateSpanID() string {
	return ServShared.GenerateSpanID()
}

func StartSpan(name string, parentTrace string) *Span {
	span := ServShared.StartSpan(name, parentTrace)
	if span != nil {
		span.Kind = 2 // Server span
	}
	return span
}

func EndSpan(span *Span, err error, attributes map[string]interface{}) {
	ServShared.EndSpan(span, err, attributes)
}
