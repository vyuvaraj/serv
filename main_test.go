package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"servmesh/pkg/client"
	"servmesh/pkg/registry"
)

func TestServMeshLifecycle(t *testing.T) {
	// Start Control Plane Registry
	reg := registry.NewRegistry(5 * time.Second)
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	// Setup two mock service backends
	backend1Count := 0
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend1Count++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response from backend 1"))
	}))
	defer backend1.Close()

	backend2Count := 0
	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend2Count++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("response from backend 2"))
	}))
	defer backend2.Close()

	// 1. Register instances with the control plane registry
	regURL := regServer.URL + "/api/register"
	
	registerInstance(t, regURL, "user-service", backend1.URL)
	registerInstance(t, regURL, "user-service", backend2.URL)

	// 2. Setup Mesh HTTP Client
	transport := client.NewMeshTransport(regServer.URL, 50*time.Millisecond)
	httpClient := &http.Client{
		Transport: transport,
	}

	// 3. Make requests to serv://user-service and check load balancing
	for i := 0; i < 4; i++ {
		resp, err := httpClient.Get("serv://user-service/users")
		if err != nil {
			t.Fatalf("failed to make mesh request: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Logf("Request %d response: %s", i, string(body))
	}

	// Round-robin should distribute requests evenly (2 each)
	if backend1Count != 2 || backend2Count != 2 {
		t.Errorf("expected even load distribution (2/2), got Backend1=%d, Backend2=%d", backend1Count, backend2Count)
	}

	// 4. Test Circuit Breaker: Backend 1 starts failing
	failingBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failingBackend.Close()

	// Register the failing backend
	registerInstance(t, regURL, "failing-service", failingBackend.URL)
	// Refresh cache by waiting or setting a small cache TTL.
	// Let's create a separate client transport for the failing test with zero cache TTL
	failTransport := client.NewMeshTransport(regServer.URL, 1*time.Millisecond)
	failClient := &http.Client{Transport: failTransport}

	// Make requests to trigger failure and trip the circuit breaker
	// Default breaker threshold is 3
	for i := 0; i < 3; i++ {
		_, _ = failClient.Get("serv://failing-service/")
	}

	// Next request should fail immediately with ErrCircuitOpen/circuit open error
	_, err := failClient.Get("serv://failing-service/")
	if err == nil || (!stringsContains(err.Error(), "circuit breaker is open") && !stringsContains(err.Error(), "blocked by circuit breaker")) {
		t.Errorf("expected circuit open error, got %v", err)
	}
}

func registerInstance(t *testing.T, regURL, serviceName, address string) {
	payload := registry.Instance{
		Service:   serviceName,
		Address:   address,
		HealthURL: address + "/health",
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(regURL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to register instance: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("registration returned status %d", resp.StatusCode)
	}
}

func stringsContains(s, sub string) bool {
	// Simple standard library wrapper to avoid import issues
	return len(s) >= len(sub) && (s == sub || stringsContainsRecursive(s, sub))
}

func stringsContainsRecursive(s, sub string) bool {
	// Simple loop-free fallback containing
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Add a test for heartbeats
func TestRegistryHeartbeats(t *testing.T) {
	reg := registry.NewRegistry(500 * time.Millisecond)
	regURL := "http://localhost:9999" // dummy
	_ = regURL

	inst := registry.Instance{
		Service: "test-heartbeat",
		Address: "http://localhost:8080",
	}
	reg.Register(inst)

	// Verify resolved
	resolved := reg.Resolve("test-heartbeat")
	if len(resolved) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(resolved))
	}

	// Wait for TTL eviction
	time.Sleep(700 * time.Millisecond)
	reg.Evict() // Trigger eviction manually for fast deterministic test
	resolved = reg.Resolve("test-heartbeat")
	if len(resolved) != 0 {
		t.Errorf("expected instance to be evicted, got %d", len(resolved))
	}
}

// Thread safety test
func TestRegistryConcurrency(t *testing.T) {
	reg := registry.NewRegistry(10 * time.Second)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			reg.Register(registry.Instance{
				Service: "concurrent-service",
				Address: fmt.Sprintf("http://localhost:%d", 8000+id),
			})
			reg.Resolve("concurrent-service")
		}(i)
	}
	wg.Wait()

	resolved := reg.Resolve("concurrent-service")
	if len(resolved) != 50 {
		t.Errorf("expected 50 instances, got %d", len(resolved))
	}
}
