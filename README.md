# ServTrace — Distributed Tracing Backend

```bash
docker run -p 4317:4317 -p 4318:4318 ghcr.io/vyuvaraj/servtrace:latest
```

ServTrace is the centralized distributed tracing collector and visualizer backend for the Serv ecosystem. It implements OTLP/HTTP trace ingestion, allowing it to collect traces from all services and reconstruct trace waterfalls.

## Features

- **OTLP/HTTP Ingestion**: Standard `/v1/traces` ingestion endpoint.
- **Trace Reassembly**: Groups spans by trace ID, links parent-child relationships, and calculates absolute/relative duration offsets.
- **REST APIs**: Query trace summaries and waterfall hierarchy trees.
- **Eviction Policy**: Thread-safe in-memory store with oldest-first trace eviction limits.

## Getting Started

### Starting the Collector

To run the collector on port `8090`:

```bash
go run main.go --port 8090 --limit 1000
```

### APIs

- `POST /v1/traces` - Ingest OTLP/HTTP spans
- `GET /api/traces` - List trace summaries
- `GET /api/traces/{traceId}` - Fetch trace waterfall tree
- `DELETE /api/traces` - Clear all traces in memory

---

## Standalone OTLP Collector Usage

ServTrace can be run as a lightweight, zero-dependency alternative to Jaeger or Zipkin for local development. Since it implements the open standard **OTLP/HTTP** protocol, you can route traces from any application using official OpenTelemetry SDKs directly to ServTrace.

### 1. Configure OpenTelemetry SDKs

Configure your application's OpenTelemetry tracer provider to export spans to the ServTrace receiver endpoint:

#### Go
```go
import (
	"context"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/trace"
)

exporter, err := otlptracehttp.New(ctx,
	otlptracehttp.WithEndpoint("localhost:8090"), // Port ServTrace is running on
	otlptracehttp.WithInsecure(),
)
```

#### Python
```python
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter

exporter = OTLPSpanExporter(
    endpoint="http://localhost:8090/v1/traces"
)
```

#### Node.js / TypeScript
```typescript
import { OTLPTraceExporter } from '@opentelemetry/exporter-trace-otlp-http';

const exporter = new OTLPTraceExporter({
  url: 'http://localhost:8090/v1/traces',
});
```

### 2. Querying Traces
Once your application exports traces, you can inspect them via the HTTP API:
- Reassembled trace summaries: `curl http://localhost:8090/api/traces`
- Full trace waterfall details: `curl http://localhost:8090/api/traces/<trace-id>`

