package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServMesh/pkg/client"
	"github.com/vyuvaraj/serv/packages/ServMesh/pkg/registry"
	"github.com/vyuvaraj/serv/packages/ServMesh/pkg/resilience"
	"github.com/vyuvaraj/serv/packages/ServShared"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/resolver"
)

func TestServMeshLifecycle(t *testing.T) {
	// Start Control Plane Registry
	reg := registry.NewRegistry(5 * time.Second)
	defer reg.Close()
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
	defer reg.Close()
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
	defer reg.Close()
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
	defer reg.Close()
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
	defer reg.Close()
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
	defer reg.Close()
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
	defer reg.Close()
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
	defer reg.Close()
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
	defer reg.Close()
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

func BenchmarkCircuitBreakerAllow(b *testing.B) {
	cb := resilience.NewCircuitBreaker(10, 1*time.Second)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = cb.Allow()
		}
	})
}

func TestMulticastServiceDiscovery(t *testing.T) {
	reg := registry.NewRegistry(5 * time.Second)
	defer reg.Close()
	time.Sleep(50 * time.Millisecond)

	addr, err := net.ResolveUDPAddr("udp4", "127.0.0.1:9999")
	if err != nil {
		t.Fatalf("failed to resolve UDP address: %v", err)
	}

	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		t.Fatalf("failed to dial UDP: %v", err)
	}
	defer conn.Close()

	announce := struct {
		Type      string `json:"type"`
		Service   string `json:"service"`
		Address   string `json:"address"`
		HealthURL string `json:"health_url"`
	}{
		Type:      "announce",
		Service:   "multicast-auth",
		Address:   "http://127.0.0.1:9098",
		HealthURL: "http://127.0.0.1:9098/health",
	}

	data, _ := json.Marshal(announce)
	_, err = conn.Write(data)
	if err != nil {
		t.Fatalf("failed to write announce packet: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	resolved := reg.Resolve("multicast-auth")
	if len(resolved) == 0 {
		t.Fatalf("expected resolved instance, got 0")
	}

	if resolved[0].Address != "http://127.0.0.1:9098" {
		t.Errorf("expected address http://127.0.0.1:9098, got %q", resolved[0].Address)
	}
}

func TestGRPCMeshTransport(t *testing.T) {
	os.Setenv("SERV_MESH_GRPC", "true")
	defer os.Unsetenv("SERV_MESH_GRPC")

	reg := registry.NewRegistry(5 * time.Second)
	defer reg.Close()
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Echo", r.Header.Get("X-Request-Header"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello from grpc mesh!"))
	})
	httpServer, err := startSafeHTTPServer(mux)
	if err != nil {
		t.Fatalf("failed to start safe http server: %v", err)
	}
	defer httpServer.Close()

	u, _ := url.Parse(httpServer.URL)
	var portInt int
	fmt.Sscanf(u.Port(), "%d", &portInt)
	grpcAddr := fmt.Sprintf("127.0.0.1:%d", portInt+1000)

	grpcServer, err := client.StartGRPCProxy(grpcAddr, mux)
	if err != nil {
		t.Fatalf("failed to start gRPC proxy: %v", err)
	}
	defer grpcServer.Stop()

	registerInstance(t, regServer.URL+"/api/register", "grpc-service", httpServer.URL)

	transport := client.NewMeshTransport(regServer.URL, 50*time.Millisecond)
	httpClient := &http.Client{Transport: transport}

	req, _ := http.NewRequest("GET", "serv://grpc-service/hello", nil)
	req.Header.Set("X-Request-Header", "mesh-test")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("gRPC mesh request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "hello from grpc mesh!" {
		t.Errorf("expected 'hello from grpc mesh!', got %q", string(body))
	}

	if resp.Header.Get("X-Custom-Echo") != "mesh-test" {
		t.Errorf("expected header echo 'mesh-test', got %q", resp.Header.Get("X-Custom-Echo"))
	}
}

func TestZeroTrustNetworkPolicies(t *testing.T) {
	os.Setenv("SERV_MESH_GRPC", "true")
	defer os.Unsetenv("SERV_MESH_GRPC")

	reg := registry.NewRegistry(5 * time.Second)
	defer reg.Close()
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	policy := registry.NetworkPolicy{
		SourceService: "trusted-app",
		TargetService: "secure-db",
		AllowedPaths:  []string{"/api/db/read"},
	}
	policyBody, _ := json.Marshal(policy)
	respPolicy, err := http.Post(regServer.URL+"/api/policies", "application/json", bytes.NewReader(policyBody))
	if err != nil {
		t.Fatalf("failed to register policy: %v", err)
	}
	respPolicy.Body.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/db/read", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("db read success"))
	})
	mux.HandleFunc("/api/db/write", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("db write success"))
	})
	httpServer, err := startSafeHTTPServer(mux)
	if err != nil {
		t.Fatalf("failed to start safe http server: %v", err)
	}
	defer httpServer.Close()

	u, _ := url.Parse(httpServer.URL)
	var portInt int
	fmt.Sscanf(u.Port(), "%d", &portInt)
	grpcAddr := fmt.Sprintf("127.0.0.1:%d", portInt+1000)

	grpcServer, err := client.StartGRPCProxyWithRegistry(grpcAddr, mux, reg, "secure-db")
	if err != nil {
		t.Fatalf("failed to start gRPC proxy: %v", err)
	}
	defer grpcServer.Stop()

	registerInstance(t, regServer.URL+"/api/register", "secure-db", httpServer.URL)

	transport := client.NewMeshTransport(regServer.URL, 50*time.Millisecond)
	httpClient := &http.Client{Transport: transport}

	req1, _ := http.NewRequest("GET", "serv://secure-db/api/db/read", nil)
	req1.Header.Set("X-Mesh-Source", "trusted-app")
	resp1, err := httpClient.Do(req1)
	if err != nil {
		t.Fatalf("request 1 failed: %v", err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp1.StatusCode)
	}

	req2, _ := http.NewRequest("POST", "serv://secure-db/api/db/write", nil)
	req2.Header.Set("X-Mesh-Source", "trusted-app")
	resp2, err := httpClient.Do(req2)
	if err != nil {
		t.Fatalf("request 2 failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", resp2.StatusCode)
	}

	req3, _ := http.NewRequest("GET", "serv://secure-db/api/db/read", nil)
	req3.Header.Set("X-Mesh-Source", "malicious-app")
	resp3, err := httpClient.Do(req3)
	if err != nil {
		t.Fatalf("request 3 failed: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", resp3.StatusCode)
	}
}

func TestHealthAwareLoadBalancing(t *testing.T) {
	transport := client.NewMeshTransport("http://localhost:8080", 5*time.Second)

	targets := []registry.Instance{
		{Address: "http://10.0.0.1:8080", Weight: 100},
		{Address: "http://10.0.0.2:8080", Weight: 100},
	}

	selected := make(map[string]int)
	for i := 0; i < 100; i++ {
		tgt := transport.SelectTargetForTest("test-svc", targets)
		selected[tgt]++
	}
	if selected["http://10.0.0.1:8080"] == 0 || selected["http://10.0.0.2:8080"] == 0 {
		t.Errorf("Expected traffic on both targets when healthy, got: %+v", selected)
	}

	transport.UpdateTargetMetricsForTest("http://10.0.0.1:8080", 500*time.Millisecond, true)
	transport.UpdateTargetMetricsForTest("http://10.0.0.2:8080", 10*time.Millisecond, false)

	selectedDegraded := make(map[string]int)
	for i := 0; i < 100; i++ {
		tgt := transport.SelectTargetForTest("test-svc", targets)
		selectedDegraded[tgt]++
	}
	t.Logf("Degraded traffic distribution: %+v", selectedDegraded)
	if selectedDegraded["http://10.0.0.2:8080"] < 80 {
		t.Errorf("Expected healthier target to get majority of traffic, got: %+v", selectedDegraded)
	}
}

func TestServMeshRateLimitingPerPair(t *testing.T) {
	// Start a control plane registry
	reg := registry.NewRegistry(5 * time.Second)
	defer reg.Close()
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	// Setup a backend that counts requests
	reqCount := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	// Register the backend
	registerInstance(t, regServer.URL+"/api/register", "rate-svc", backend.URL)

	// Create transport and set rate limit: 2 RPS, burst of 2 for caller-a -> rate-svc
	transport := client.NewMeshTransport(regServer.URL, 50*time.Millisecond)
	transport.SetRateLimit("caller-a", "rate-svc", 2.0, 2)

	httpClient := &http.Client{Transport: transport}

	// Fire 5 rapid requests (no time to refill)
	successCount := 0
	rateLimitedCount := 0
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest("GET", "serv://rate-svc/ping", nil)
		req.Header.Set("X-Caller-Id", "caller-a")
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			successCount++
		} else if resp.StatusCode == http.StatusTooManyRequests {
			rateLimitedCount++
		}
	}

	t.Logf("Rate limit test: %d successes, %d rate-limited", successCount, rateLimitedCount)
	// With burst=2 and no time to refill, first 2 should succeed, remaining 3 should be 429
	if successCount != 2 {
		t.Errorf("expected 2 successes (burst), got %d", successCount)
	}
	if rateLimitedCount != 3 {
		t.Errorf("expected 3 rate-limited responses, got %d", rateLimitedCount)
	}

	// Requests from a DIFFERENT caller should not be rate-limited
	req, _ := http.NewRequest("GET", "serv://rate-svc/ping", nil)
	req.Header.Set("X-Caller-Id", "caller-b")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error from caller-b: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("caller-b should not be rate-limited, got %d", resp.StatusCode)
	}
}

func TestServMeshVersionRouting(t *testing.T) {
	// Start a control plane registry
	reg := registry.NewRegistry(5 * time.Second)
	defer reg.Close()
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	// Two backends: v1 and v2
	v1Count := 0
	v1Backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v1Count++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("v1"))
	}))
	defer v1Backend.Close()

	v2Count := 0
	v2Backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v2Count++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("v2"))
	}))
	defer v2Backend.Close()

	// Register both backends with version tags
	regURL := regServer.URL + "/api/register"
	body1, _ := json.Marshal(registry.Instance{
		Service:   "versioned-svc",
		Address:   v1Backend.URL,
		HealthURL: v1Backend.URL + "/health",
		Version:   "v1",
	})
	resp, _ := http.Post(regURL, "application/json", bytes.NewReader(body1))
	resp.Body.Close()

	body2, _ := json.Marshal(registry.Instance{
		Service:   "versioned-svc",
		Address:   v2Backend.URL,
		HealthURL: v2Backend.URL + "/health",
		Version:   "v2",
	})
	resp, _ = http.Post(regURL, "application/json", bytes.NewReader(body2))
	resp.Body.Close()

	transport := client.NewMeshTransport(regServer.URL, 50*time.Millisecond)
	httpClient := &http.Client{Transport: transport}

	// Send 4 requests pinned to v2
	for i := 0; i < 4; i++ {
		req, _ := http.NewRequest("GET", "serv://versioned-svc/data", nil)
		req.Header.Set("X-Service-Version", "v2")
		r, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		t.Logf("v2 pinned request %d got: %s", i, body)
	}

	if v2Count != 4 {
		t.Errorf("expected all 4 requests to hit v2, got v1=%d v2=%d", v1Count, v2Count)
	}
	if v1Count != 0 {
		t.Errorf("expected v1 to receive 0 requests, got %d", v1Count)
	}

	// Requests without version header should hit both
	v1Count = 0
	v2Count = 0
	for i := 0; i < 4; i++ {
		r, err := httpClient.Get("serv://versioned-svc/data")
		if err != nil {
			t.Fatalf("unversioned request %d failed: %v", i, err)
		}
		r.Body.Close()
	}
	t.Logf("Unversioned: v1=%d v2=%d", v1Count, v2Count)
	if v1Count+v2Count != 4 {
		t.Errorf("expected 4 total unversioned requests, got %d", v1Count+v2Count)
	}
}

func TestHealthMetricsPush(t *testing.T) {
	// Start registry
	reg := registry.NewRegistry(5 * time.Second)
	defer reg.Close()
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	// Backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	}))
	defer backend.Close()

	// Register instance
	registerInstance(t, regServer.URL+"/api/register", "health-svc", backend.URL)

	// Make 4 requests via MeshTransport so metrics are pushed
	transport := client.NewMeshTransport(regServer.URL, 50*time.Millisecond)
	httpClient := &http.Client{Transport: transport}
	for i := 0; i < 4; i++ {
		resp, err := httpClient.Get("serv://health-svc/ping")
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		resp.Body.Close()
	}

	// Give async goroutines a moment to push metrics
	time.Sleep(100 * time.Millisecond)

	// Query /api/topology
	resp, err := http.Get(regServer.URL + "/api/topology")
	if err != nil {
		t.Fatalf("topology request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("topology returned status %d", resp.StatusCode)
	}

	var entries []registry.TopologyEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatalf("failed to decode topology: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("expected at least 1 topology entry")
	}

	var found *registry.TopologyEntry
	for i := range entries {
		if entries[i].Address == backend.URL {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("backend %s not found in topology", backend.URL)
	}
	t.Logf("Topology entry: service=%s latency=%.1fms err=%.2f state=%s",
		found.Service, found.AvgLatencyMs, found.ErrorRate, found.State)

	// After successful requests the metrics should have been reported
	if found.AvgLatencyMs <= 0 {
		t.Errorf("expected AvgLatencyMs > 0, got %.2f", found.AvgLatencyMs)
	}
	if found.State != "healthy" {
		t.Errorf("expected state 'healthy', got '%s'", found.State)
	}
}

func TestTopologyEndpoint(t *testing.T) {
	// Start registry
	reg := registry.NewRegistry(5 * time.Second)
	defer reg.Close()
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	// Two backends for two different services
	svc1Backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer svc1Backend.Close()
	svc2Backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer svc2Backend.Close()

	registerInstance(t, regServer.URL+"/api/register", "alpha-svc", svc1Backend.URL)
	registerInstance(t, regServer.URL+"/api/register", "beta-svc", svc2Backend.URL)

	// Push a manual health metric for alpha-svc
	metric := registry.HealthMetric{
		Service:      "alpha-svc",
		Address:      svc1Backend.URL,
		AvgLatencyMs: 42.5,
		ErrorRate:    0.01,
	}
	body, _ := json.Marshal(metric)
	postResp, err := http.Post(regServer.URL+"/api/health-metrics", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to push health metric: %v", err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusOK {
		t.Fatalf("health-metrics POST returned %d", postResp.StatusCode)
	}

	// Query topology — all services
	topoResp, err := http.Get(regServer.URL + "/api/topology")
	if err != nil {
		t.Fatalf("topology GET failed: %v", err)
	}
	defer topoResp.Body.Close()

	var all []registry.TopologyEntry
	json.NewDecoder(topoResp.Body).Decode(&all)

	services := map[string]bool{}
	for _, e := range all {
		services[e.Service] = true
	}
	if !services["alpha-svc"] || !services["beta-svc"] {
		t.Errorf("expected both alpha-svc and beta-svc in topology, got: %+v", services)
	}

	// Query topology — filter by service
	filterResp, err := http.Get(regServer.URL + "/api/topology?service=alpha-svc")
	if err != nil {
		t.Fatalf("topology filter GET failed: %v", err)
	}
	defer filterResp.Body.Close()

	var filtered []registry.TopologyEntry
	json.NewDecoder(filterResp.Body).Decode(&filtered)

	if len(filtered) != 1 {
		t.Errorf("expected 1 filtered entry, got %d", len(filtered))
	}
	if len(filtered) > 0 {
		e := filtered[0]
		if e.AvgLatencyMs != 42.5 {
			t.Errorf("expected AvgLatencyMs=42.5, got %.2f", e.AvgLatencyMs)
		}
		if e.State != "healthy" {
			t.Errorf("expected state 'healthy', got '%s'", e.State)
		}
		t.Logf("alpha-svc: latency=%.1fms err=%.2f state=%s", e.AvgLatencyMs, e.ErrorRate, e.State)
	}
}

func TestGRPCResolverAndInterceptor(t *testing.T) {
	// 1. Start Control Plane Registry
	reg := registry.NewRegistry(5 * time.Second)
	defer reg.Close()
	regServer := httptest.NewServer(reg.Handler())
	defer regServer.Close()

	// 2. Setup Mesh HTTP Client & Resolver builder
	transport := client.NewMeshTransport(regServer.URL, 50*time.Millisecond)
	builder := client.NewServResolverBuilder(transport)
	resolver.Register(builder)

	// Register a mock address
	regURL := regServer.URL + "/api/register"
	registerInstance(t, regURL, "grpc-service", "localhost:50051")

	// Verify builder scheme
	if builder.Scheme() != "github.com/vyuvaraj/serv/packages/Serv-lang" {
		t.Errorf("expected scheme 'serv', got '%s'", builder.Scheme())
	}

	// Test Unary Interceptor
	var interceptor grpc.UnaryClientInterceptor = transport.GRPCUnaryInterceptor()
	if interceptor == nil {
		t.Errorf("expected non-nil interceptor")
	}
}

func startSafeHTTPServer(handler http.Handler) (*httptest.Server, error) {
	for port := 15000; port < 25000; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		l, err := net.Listen("tcp", addr)
		if err == nil {
			server := httptest.NewUnstartedServer(handler)
			server.Listener = l
			server.Start()
			return server, nil
		}
	}
	return nil, fmt.Errorf("failed to find free port in safe range")
}

func TestSubcommandUp(t *testing.T) {
	reg := registry.NewRegistry(5 * time.Second)
	defer reg.Close()

	reg.Register(registry.Instance{
		Service: "payment-service",
		Address: "http://127.0.0.1:9099",
	})

	proxy := &devProxy{reg: reg}
	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()

	req, _ := http.NewRequest("GET", proxyServer.URL+"/payment-service/pay", nil)
	client := &http.Client{Timeout: 100 * time.Millisecond}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}



