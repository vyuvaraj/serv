package otel

import (
	"context"

	"github.com/vyuvaraj/ServShared"
)

// InitTrace initializes the telemetry tracing provider.
func InitTrace(ctx context.Context, serviceName string) {
	ServShared.InitTrace(serviceName)
}

// Shutdown closes the telemetry tracing provider.
func Shutdown(ctx context.Context) {
	ServShared.Shutdown()
}
