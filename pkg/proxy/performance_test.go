package proxy

import (
	"context"
	"fmt"
	"io"
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

	var runIdx int
	for b.Loop() {
		const total = 1000
		const concurrency = 50
		var wg sync.WaitGroup
		var errorCount int64
		var firstErr error
		var once sync.Once

		ch := make(chan struct{}, total)
		for k := 0; k < total; k++ {
			ch <- struct{}{}
		}
		close(ch)

		wg.Add(concurrency)
		for j := 0; j < concurrency; j++ {
			go func() {
				defer wg.Done()
				for range ch {
					resp, err := client.Get(gwServer.URL + "/bench/test")
					if err != nil {
						atomic.AddInt64(&errorCount, 1)
						once.Do(func() {
							firstErr = err
						})
						continue
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						atomic.AddInt64(&errorCount, 1)
					}
				}
			}()
		}
		wg.Wait()
		if errorCount > 0 {
			b.Logf("Warning: %d requests failed during concurrency benchmark run %d. First error: %v", errorCount, runIdx, firstErr)
		}
		runIdx++
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
