package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"servmesh/pkg/client"
	"servmesh/pkg/registry"
	"github.com/vyuvaraj/ServShared"
	"github.com/golang-jwt/jwt/v5"
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

func TestRegistryJWTAuthentication(t *testing.T) {
	jwtSecret := "my-secret"
	os.Setenv("SERV_JWT_SECRET", jwtSecret)
	defer os.Unsetenv("SERV_JWT_SECRET")

	reg := registry.NewRegistry(10 * time.Second)
	ts := httptest.NewServer(reg.Handler())
	defer ts.Close()

	// Generate a valid JWT
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, &ServShared.Claims{
		Username: "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	})
	tokenStr, _ := token.SignedString([]byte(jwtSecret))

	// 1. Try to register without authorization header (should fail)
	inst := registry.Instance{
		Service: "test-auth",
		Address: "http://localhost:8080",
	}
	body, _ := json.Marshal(inst)
	resp, err := http.Post(ts.URL+"/api/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected StatusUnauthorized, got %d", resp.StatusCode)
	}

	// 2. Try to register with invalid token (should fail)
	req, _ := http.NewRequest("POST", ts.URL+"/api/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer invalid-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected StatusUnauthorized, got %d", resp.StatusCode)
	}

	// 3. Register with valid token (should succeed)
	req, _ = http.NewRequest("POST", ts.URL+"/api/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tokenStr)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected StatusOK, got %d", resp.StatusCode)
	}
}

func TestDynamicMTLS(t *testing.T) {
	// Start control plane registry
	reg := registry.NewRegistry(10 * time.Second)
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	// Initialize MeshTransport
	transport := client.NewMeshTransport(regServer.URL, 50*time.Millisecond)

	// Obtain client/server TLS configurations via CSR flow
	clientTLS, serverTLS, err := transport.SetupmTLS(context.Background(), "my-secure-service", "")
	if err != nil {
		t.Fatalf("SetupmTLS failed: %v", err)
	}

	if clientTLS == nil || serverTLS == nil {
		t.Fatal("expected non-nil TLS configs")
	}

	// Setup a mock HTTPS server with mutual TLS configuration
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("secure response"))
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()

	// Register instance with control plane
	registerInstance(t, regServer.URL+"/api/register", "my-secure-service", server.URL)

	// Make request via client
	httpClient := &http.Client{
		Transport: transport,
	}

	resp, err := httpClient.Get("serv://my-secure-service/secure")
	if err != nil {
		t.Fatalf("failed to make secure mesh request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "secure response" {
		t.Errorf("expected 'secure response', got '%s'", string(body))
	}
}

func TestCanaryTrafficSplitting(t *testing.T) {
	reg := registry.NewRegistry(10 * time.Second)
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	// Setup two mock service backends
	backendV1Count := 0
	backendV1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendV1Count++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("v1"))
	}))
	defer backendV1.Close()

	backendV2Count := 0
	backendV2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendV2Count++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("v2"))
	}))
	defer backendV2.Close()

	// Register v1 with 80% weight, v2 with 20% weight
	regURL := regServer.URL + "/api/register"
	
	// register with weights
	registerInstanceWithWeight(t, regURL, "canary-service", backendV1.URL, "v1.0.0", 80)
	registerInstanceWithWeight(t, regURL, "canary-service", backendV2.URL, "v2.0.0", 20)

	transport := client.NewMeshTransport(regServer.URL, 50*time.Millisecond)
	httpClient := &http.Client{Transport: transport}

	// Make a number of requests and verify traffic is split dynamically
	for i := 0; i < 50; i++ {
		resp, err := httpClient.Get("serv://canary-service/endpoint")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
	}

	t.Logf("Canary counts - V1: %d, V2: %d", backendV1Count, backendV2Count)
	if backendV1Count == 0 || backendV2Count == 0 {
		t.Errorf("Expected traffic to go to both backend versions, but got V1=%d, V2=%d", backendV1Count, backendV2Count)
	}
}

func registerInstanceWithWeight(t *testing.T, regURL, serviceName, address, version string, weight int) {
	payload := registry.Instance{
		Service:   serviceName,
		Address:   address,
		HealthURL: address + "/health",
		Version:   version,
		Weight:    weight,
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

func TestDynamicRoutingRules(t *testing.T) {
	reg := registry.NewRegistry(5 * time.Second)
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	// Configure a custom rule for the service
	rule := registry.RoutingRule{
		Service:    "test-service",
		MaxRetries: 5,
		TimeoutMs:  100,
		BackoffMs:  10,
	}
	ruleBody, _ := json.Marshal(rule)
	resp, err := http.Post(regServer.URL+"/api/rules", "application/json", bytes.NewReader(ruleBody))
	if err != nil {
		t.Fatalf("failed to post rule: %v", err)
	}
	resp.Body.Close()

	transport := client.NewMeshTransport(regServer.URL, 50*time.Millisecond)

	// 1. Resolve and check rule values
	ctx := context.Background()
	resRule, err := transport.ResolveRule(ctx, "test-service")
	if err != nil {
		t.Fatalf("failed to resolve rule: %v", err)
	}

	if resRule.MaxRetries != 5 || resRule.TimeoutMs != 100 || resRule.BackoffMs != 10 {
		t.Errorf("expected custom rule (MaxRetries=5, TimeoutMs=100, BackoffMs=10), got %+v", resRule)
	}

	// 2. Resolve unknown service, should fall back to default
	resDefaultRule, err := transport.ResolveRule(ctx, "unknown-service")
	if err != nil {
		t.Fatalf("failed to resolve default rule: %v", err)
	}
	if resDefaultRule.MaxRetries != 3 || resDefaultRule.TimeoutMs != 2000 || resDefaultRule.BackoffMs != 50 {
		t.Errorf("expected default rule fallback, got %+v", resDefaultRule)
	}
}

func TestRegionalGeoRouting(t *testing.T) {
	reg := registry.NewRegistry(5 * time.Second)
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	// Register instance 1 in us-east
	inst1 := registry.Instance{
		Service:   "geo-service",
		Address:   "http://10.0.0.1:8080",
		HealthURL: "http://10.0.0.1:8080/health",
		Region:    "us-east",
	}
	body1, _ := json.Marshal(inst1)
	resp1, _ := http.Post(regServer.URL+"/api/register", "application/json", bytes.NewReader(body1))
	resp1.Body.Close()

	// Register instance 2 in eu-west
	inst2 := registry.Instance{
		Service:   "geo-service",
		Address:   "http://10.0.0.2:8080",
		HealthURL: "http://10.0.0.2:8080/health",
		Region:    "eu-west",
	}
	body2, _ := json.Marshal(inst2)
	resp2, _ := http.Post(regServer.URL+"/api/register", "application/json", bytes.NewReader(body2))
	resp2.Body.Close()

	// 1. Resolve specifying us-east
	resp, err := http.Get(regServer.URL + "/api/resolve/geo-service?region=us-east")
	if err != nil {
		t.Fatalf("failed resolve: %v", err)
	}
	defer resp.Body.Close()
	var resolved []registry.Instance
	json.NewDecoder(resp.Body).Decode(&resolved)

	if len(resolved) != 1 || resolved[0].Address != "http://10.0.0.1:8080" {
		t.Errorf("expected 1 instance in us-east, got: %+v", resolved)
	}

	// 2. Resolve specifying an unknown region, should fall back to all healthy
	respFallback, err := http.Get(regServer.URL + "/api/resolve/geo-service?region=ap-south")
	if err != nil {
		t.Fatalf("failed resolve fallback: %v", err)
	}
	defer respFallback.Body.Close()
	var resolvedFallback []registry.Instance
	json.NewDecoder(respFallback.Body).Decode(&resolvedFallback)

	if len(resolvedFallback) != 2 {
		t.Errorf("expected fallback to return both instances, got: %d", len(resolvedFallback))
	}
}

func TestChaosFaultInjection(t *testing.T) {
	reg := registry.NewRegistry(5 * time.Second)
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	rule := registry.RoutingRule{
		Service:          "chaos-service",
		MaxRetries:       1,
		FaultDelayMs:     50,
		FaultDelayRatio:  1.0,
		FaultErrorStatus: 502,
		FaultErrorRatio:  1.0,
	}
	ruleBody, _ := json.Marshal(rule)
	resp, _ := http.Post(regServer.URL+"/api/rules", "application/json", bytes.NewReader(ruleBody))
	resp.Body.Close()

	// Register a mock instance so resolver finds an endpoint
	inst := registry.Instance{
		Service:   "chaos-service",
		Address:   "http://127.0.0.1:9099",
		HealthURL: "http://127.0.0.1:9099/health",
	}
	body, _ := json.Marshal(inst)
	respReg, _ := http.Post(regServer.URL+"/api/register", "application/json", bytes.NewReader(body))
	respReg.Body.Close()

	transport := client.NewMeshTransport(regServer.URL, 50*time.Millisecond)
	httpClient := &http.Client{Transport: transport}

	start := time.Now()
	res, err := httpClient.Get("serv://chaos-service/some-endpoint")
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != 502 {
		t.Errorf("expected status code 502, got %d", res.StatusCode)
	}

	if duration < 50*time.Millisecond {
		t.Errorf("expected delay of at least 50ms, got %v", duration)
	}
}
