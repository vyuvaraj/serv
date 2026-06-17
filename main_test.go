package main

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"servgate/pkg/proxy"
	"servgate/pkg/wasm"
)

func TestServGateReverseProxy(t *testing.T) {
	// 1. Start a mock backend target server
	backendReceivedPath := ""
	backendHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendReceivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend-response"))
	})
	backendServer := &http.Server{
		Addr:    "127.0.0.1:8091",
		Handler: backendHandler,
	}
	go backendServer.ListenAndServe()

	// 2. Setup gateway handler
	routes := []proxy.Route{
		{
			Prefix: "/api/v1/orders",
			Target: "http://127.0.0.1:8091",
		},
	}

	wasmManager, err := wasm.GetMiddlewareManager(context.Background())
	if err != nil {
		t.Fatalf("WASM setup failed: %v", err)
	}

	gatewayHandler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gatewayServer := &http.Server{
		Addr:    "127.0.0.1:8092",
		Handler: gatewayHandler,
	}
	go gatewayServer.ListenAndServe()

	time.Sleep(200 * time.Millisecond)

	// 3. Issue proxy request
	resp, err := http.Get("http://127.0.0.1:8092/api/v1/orders/create")
	if err != nil {
		t.Fatalf("Failed to execute request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "backend-response" {
		t.Errorf("Expected body 'backend-response', got %q", string(body))
	}

	if backendReceivedPath != "/create" {
		t.Errorf("Expected backend path prefix strip logic to target '/create', got %q", backendReceivedPath)
	}

	// Clean servers
	_ = backendServer.Shutdown(context.Background())
	_ = gatewayServer.Shutdown(context.Background())
}

func TestWasmMiddlewareInjection(t *testing.T) {
	// 1. Setup wazero manager
	wasmManager, err := wasm.GetMiddlewareManager(context.Background())
	if err != nil {
		t.Fatalf("WASM setup failed: %v", err)
	}

	// Register empty/no-op transform for test
	err = wasmManager.Register(context.Background(), "noop", []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // Magic wasm version
	})
	if err != nil {
		t.Fatalf("Failed to compile WASM: %v", err)
	}

	res, err := wasmManager.Run(context.Background(), "noop", []byte("raw-bytes"))
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// No-op module should compile and return empty bytes safely without error
	if len(res) > 0 {
		t.Errorf("Expected empty bytes response, got %v", res)
	}
}
