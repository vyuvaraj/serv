package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"servgate/pkg/wasm"
)

// Benchmark10KConcurrentConnections executes 10K simulated concurrent requests
// through the gateway to measure RPS, latency, and resource footprint.
func Benchmark10KConcurrentConnections(b *testing.B) {
	backendCalled := int64(0)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&backendCalled, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend-response"))
	}))
	defer backend.Close()

	routes := []Route{
		{
			Prefix: "/bench",
			Target: backend.URL,
		},
	}
	handler := NewGatewayHandler(routes, nil, "")
	gwServer := httptest.NewServer(handler)
	defer gwServer.Close()

	client := gwServer.Client()
	client.Transport.(*http.Transport).MaxIdleConnsPerHost = 10000
	client.Transport.(*http.Transport).MaxIdleConns = 10000

	b.ResetTimer()

	// Launch 10,000 requests in parallel batches to simulate high-concurrency connections
	const totalRequests = 10000
	var wg sync.WaitGroup
	var errorCount int64

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			wg.Add(totalRequests)
			for i := 0; i < totalRequests; i++ {
				go func() {
					defer wg.Done()
					resp, err := client.Get(gwServer.URL + "/bench/test")
					if err != nil {
						atomic.AddInt64(&errorCount, 1)
						return
					}
					resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						atomic.AddInt64(&errorCount, 1)
					}
				}()
			}
			wg.Wait()
		}
	})

	b.StopTimer()
	if errorCount > 0 {
		b.Logf("Warning: %d requests failed during concurrency benchmark", errorCount)
	}
}

// BenchmarkWasmColdStart measures the compilation and instantiation times
// of a WASM module to demonstrate that cache hits compile/instantiate in < 5ms.
func BenchmarkWasmColdStart(b *testing.B) {
	ctx := context.Background()
	wasmManager, err := wasm.GetMiddlewareManager(ctx)
	if err != nil {
		b.Fatalf("Failed to initialize WASM manager: %v", err)
	}

	wasmBytes := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // Minimal magic WASM header
	}

	b.Run("ColdStartCompilation", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			name := fmt.Sprintf("module-cold-%d", i)
			start := time.Now()
			err := wasmManager.Register(ctx, name, wasmBytes)
			if err != nil {
				b.Fatalf("Registration failed: %v", err)
			}
			duration := time.Since(start)
			b.Logf("Cold start compilation duration: %v", duration)
		}
	})

	b.Run("WarmStartCachedCompilation", func(b *testing.B) {
		// Run a quick pre-compile/register
		name := "module-warm"
		err := wasmManager.Register(ctx, name, wasmBytes)
		if err != nil {
			b.Fatalf("Registration failed: %v", err)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			start := time.Now()
			// Compile/instantiate the cached module
			_, err := wasmManager.Run(ctx, name, []byte("test"))
			if err != nil {
				b.Fatalf("Warm run failed: %v", err)
			}
			duration := time.Since(start)
			if duration > 5*time.Millisecond {
				b.Logf("Warning: Warm start took %v (exceeded target 5ms)", duration)
			}
		}
	})
}
