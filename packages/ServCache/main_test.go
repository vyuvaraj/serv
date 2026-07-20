package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServCache/pkg/cache"
	"github.com/vyuvaraj/serv/packages/ServCache/pkg/server"

	"github.com/vyuvaraj/serv/packages/ServShared"
)

func TestInMemoryCacheOperations(t *testing.T) {
	c := cache.NewInMemoryCache(100 * time.Millisecond)

	// Test GET non-existent key
	_, found, err := c.Get("non-existent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected key to not be found")
	}

	// Test SET & GET
	err = c.Set("my_key", "my_val", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, found, err := c.Get("my_key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || val != "my_val" {
		t.Errorf("expected 'my_val', got '%v'", val)
	}

	// Test DELETE
	err = c.Delete("my_key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, found, _ = c.Get("my_key")
	if found {
		t.Error("expected key to be deleted")
	}

	// Test CLEAR
	_ = c.Set("k1", "v1", 0)
	_ = c.Set("k2", "v2", 0)
	_ = c.Clear()

	_, found1, _ := c.Get("k1")
	_, found2, _ := c.Get("k2")
	if found1 || found2 {
		t.Error("expected cache to be fully cleared")
	}
}

func TestCacheTTLEviction(t *testing.T) {
	c := cache.NewInMemoryCache(50 * time.Millisecond)

	// Set key with 100ms TTL
	err := c.Set("ttl_key", "ttl_val", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify key immediately exists
	val, found, _ := c.Get("ttl_key")
	if !found || val != "ttl_val" {
		t.Errorf("expected key to exist, got %v", val)
	}

	// Wait 150ms
	time.Sleep(150 * time.Millisecond)
	c.EvictExpired() // manually trigger eviction

	// Verify key is gone
	_, found, _ = c.Get("ttl_key")
	if found {
		t.Error("expected key to be evicted after TTL expiration")
	}
}

func TestCacheServerRESTAPI(t *testing.T) {
	c := cache.NewInMemoryCache(10 * time.Second)
	srv := server.NewServer(c)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Post SET request
	payload := map[string]interface{}{
		"key":   "api_key",
		"value": "api_value",
		"ttl":   "10s",
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(ts.URL+"/api/cache", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to post set request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 OK, got %d", resp.StatusCode)
	}

	// 2. Get request
	getResp, err := http.Get(ts.URL + "/api/cache/api_key")
	if err != nil {
		t.Fatalf("failed to query get: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 OK, got %d", getResp.StatusCode)
	}

	var resData map[string]interface{}
	_ = json.NewDecoder(getResp.Body).Decode(&resData)
	if resData["value"] != "api_value" {
		t.Errorf("expected value 'api_value', got '%v'", resData["value"])
	}

	// 3. Delete key
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/cache/api_key", nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to delete: %v", err)
	}
	delResp.Body.Close()

	// 4. Verify 404
	getResp2, _ := http.Get(ts.URL + "/api/cache/api_key")
	getResp2.Body.Close()
	if getResp2.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", getResp2.StatusCode)
	}
}

func TestCachePatternInvalidation(t *testing.T) {
	c := cache.NewInMemoryCache(10 * time.Second)
	_ = c.Set("user:101", "alice", 0)
	_ = c.Set("user:102", "bob", 0)
	_ = c.Set("session:99", "active", 0)

	// Invalidate "user:*"
	err := c.DeletePattern("user:*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, found1, _ := c.Get("user:101")
	_, found2, _ := c.Get("user:102")
	_, found3, _ := c.Get("session:99")

	if found1 || found2 {
		t.Error("expected matching keys to be invalidated")
	}
	if !found3 {
		t.Error("expected non-matching keys to remain")
	}
}

func TestReadThroughWriteBehind(t *testing.T) {
	dbCalledGet := 0
	dbCalledPost := 0

	// Mock DB Backend server
	dbMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/db_key" {
			dbCalledGet++
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`"db_value"`))
			return
		}
		if r.Method == "POST" && r.URL.Path == "/db_key" {
			dbCalledPost++
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer dbMock.Close()

	t.Setenv("SERV_CACHE_BACKEND_DB", dbMock.URL)

	c := cache.NewInMemoryCache(10 * time.Second)

	// 1. Read-Through: Get "db_key" (cache miss -> loads from dbMock)
	val, found, err := c.Get("db_key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || val != "db_value" {
		t.Errorf("expected to load 'db_value', got %v", val)
	}
	if dbCalledGet != 1 {
		t.Errorf("expected DB to be called once, got %d", dbCalledGet)
	}

	// 2. Write-Behind: Set "db_key" -> should save to dbMock asynchronously
	err = c.Set("db_key", "new_value", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(100 * time.Millisecond) // wait for async write-behind
	if dbCalledPost != 1 {
		t.Errorf("expected DB write to be called once, got %d", dbCalledPost)
	}
}

func TestMultiRegionReplication(t *testing.T) {
	var replicatedSet, replicatedDelete bool

	// Mock peer cache node
	peerMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("replicated") != "true" {
			t.Errorf("expected replication calls to have replicated=true")
		}
		if r.Method == "POST" && r.URL.Path == "/api/cache" {
			replicatedSet = true
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == "DELETE" && r.URL.Path == "/api/cache/repl_key" {
			replicatedDelete = true
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer peerMock.Close()

	t.Setenv("SERV_CACHE_PEERS", peerMock.URL)

	c := cache.NewInMemoryCache(10 * time.Second)
	srv := server.NewServer(c)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Trigger local SET
	payload := map[string]interface{}{
		"key":   "repl_key",
		"value": "repl_val",
	}
	body, _ := json.Marshal(payload)
	_, _ = http.Post(ts.URL+"/api/cache", "application/json", bytes.NewReader(body))

	time.Sleep(100 * time.Millisecond) // wait for async replication
	if !replicatedSet {
		t.Error("expected set to be replicated to peer")
	}

	// 2. Trigger local DELETE
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/cache/repl_key", nil)
	_, _ = http.DefaultClient.Do(req)

	time.Sleep(100 * time.Millisecond) // wait for async replication
	if !replicatedDelete {
		t.Error("expected delete to be replicated to peer")
	}
}

func BenchmarkInMemoryCacheGetSet(b *testing.B) {
	c := cache.NewInMemoryCache(30 * time.Minute)
	for i := 0; i < 100; i++ {
		_ = c.Set(fmt.Sprintf("key-%d", i), "value", 0)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i%100)
			if i%2 == 0 {
				_, _, _ = c.Get(key)
			} else {
				_ = c.Set(key, "new-value", 0)
			}
			i++
		}
	})
}

func TestGossipInvalidation(t *testing.T) {
	c1 := cache.NewInMemoryCache(10 * time.Second)
	s1 := server.NewServer(c1)
	ts1 := httptest.NewServer(s1.Handler())
	defer ts1.Close()

	c2 := cache.NewInMemoryCache(10 * time.Second)
	s2 := server.NewServer(c2)
	ts2 := httptest.NewServer(s2.Handler())
	defer ts2.Close()

	// Configure peers directly on servers to avoid timing/env issues
	// Note: We need to set peers and also the mock addresses.
	// Since we export/access peers directly (unexported, but in package main? No, package main imports pkg/server, and Server has peers field, but wait, is the field exported?)
	// Yes! In server.go:
	// type Server struct {
	// 	cache     cache.Cache
	// 	peers     []string  -> Ah! lower case 'peers'! It is unexported!
	// }
	// Wait, is 'peers' unexported? Yes, type Server struct { cache cache.Cache; peers []string }.
	// Since Server is in package server, and main_test.go is in package main, main_test.go cannot access 'peers' directly!
	// Oh! We can set the env vars BEFORE calling NewServer, or add a test helper!
	// Let's set the env vars BEFORE creating the servers, which is so easy!
	
	os.Setenv("SERV_CACHE_ADDR", ts1.URL)
	os.Setenv("SERV_CACHE_PEERS", ts2.URL)
	defer os.Unsetenv("SERV_CACHE_ADDR")
	defer os.Unsetenv("SERV_CACHE_PEERS")

	s1WithPeers := server.NewServer(c1)
	ts1WithPeers := httptest.NewServer(s1WithPeers.Handler())
	defer ts1WithPeers.Close()

	_ = c2.Set("mykey", "myval", 0)
	_ = c1.Set("mykey", "myval", 0)

	url := fmt.Sprintf("%s/api/cache/gossip-invalidate?key=mykey&replicated=true", ts1WithPeers.URL)
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to request gossip invalidate: %v", err)
	}
	resp.Body.Close()

	time.Sleep(500 * time.Millisecond)

	_, found, _ := c2.Get("mykey")
	if found {
		t.Errorf("expected key to be invalidated on node 2 via gossip, but it was found")
	}
}

func TestCacheLRUEviction(t *testing.T) {
	os.Setenv("SERV_CACHE_MAX_KEYS", "3")
	defer os.Unsetenv("SERV_CACHE_MAX_KEYS")

	c := cache.NewInMemoryCache(10 * time.Second)

	_ = c.Set("k1", "v1", 0)
	_ = c.Set("k2", "v2", 0)
	_ = c.Set("k3", "v3", 0)

	// Access k1, making it most recently used, k2 remains oldest (least recently used)
	_, _, _ = c.Get("k1")

	// Set k4, which should trigger eviction of k2
	_ = c.Set("k4", "v4", 0)

	// Verify k2 is evicted
	_, found2, _ := c.Get("k2")
	if found2 {
		t.Error("expected k2 to be evicted via LRU eviction policy")
	}

	// Verify k1 and k4 are still present
	_, found1, _ := c.Get("k1")
	if !found1 {
		t.Error("expected k1 to be retained")
	}
	_, found4, _ := c.Get("k4")
	if !found4 {
		t.Error("expected k4 to be retained")
	}
}

func TestNamespaceIsolation(t *testing.T) {
	secret := "my-jwt-test-secret-123"
	os.Setenv("SERV_JWT_SECRET", secret)
	defer os.Unsetenv("SERV_JWT_SECRET")

	c := cache.NewInMemoryCache(10 * time.Second)
	s := server.NewServer(c)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// Generate tokens for Tenant A and Tenant B
	tokenA, err := ServShared.GenerateUserToken(secret, "alice", []string{"user"}, "tenant-a", time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token A: %v", err)
	}
	tokenB, err := ServShared.GenerateUserToken(secret, "bob", []string{"user"}, "tenant-b", time.Hour)
	if err != nil {
		t.Fatalf("failed to generate token B: %v", err)
	}

	// 1. Tenant A sets key "shared-key" -> should succeed
	body := `{"key":"shared-key","value":"tenant-a-private-data"}`
	reqSet, _ := http.NewRequest("POST", ts.URL+"/api/cache", bytes.NewReader([]byte(body)))
	reqSet.Header.Set("Authorization", "Bearer "+tokenA)
	reqSet.Header.Set("X-Tenant-ID", "tenant-a")
	reqSet.Header.Set("Content-Type", "application/json")

	respSet, err := http.DefaultClient.Do(reqSet)
	if err != nil {
		t.Fatalf("Tenant A SET request failed: %v", err)
	}
	respSet.Body.Close()
	if respSet.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", respSet.StatusCode)
	}

	// 2. Tenant B attempts to read "shared-key" -> should return 404 (isolated!)
	reqGetB, _ := http.NewRequest("GET", ts.URL+"/api/cache/shared-key", nil)
	reqGetB.Header.Set("Authorization", "Bearer "+tokenB)
	reqGetB.Header.Set("X-Tenant-ID", "tenant-b")

	respGetB, err := http.DefaultClient.Do(reqGetB)
	if err != nil {
		t.Fatalf("Tenant B GET request failed: %v", err)
	}
	respGetB.Body.Close()
	if respGetB.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 Not Found for Tenant B, got %d", respGetB.StatusCode)
	}

	// 3. Tenant A reads "shared-key" -> should succeed and get "tenant-a-private-data"
	reqGetA, _ := http.NewRequest("GET", ts.URL+"/api/cache/shared-key", nil)
	reqGetA.Header.Set("Authorization", "Bearer "+tokenA)
	reqGetA.Header.Set("X-Tenant-ID", "tenant-a")

	respGetA, err := http.DefaultClient.Do(reqGetA)
	if err != nil {
		t.Fatalf("Tenant A GET request failed: %v", err)
	}
	defer respGetA.Body.Close()
	if respGetA.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", respGetA.StatusCode)
	}

	var getResult map[string]interface{}
	if err := json.NewDecoder(respGetA.Body).Decode(&getResult); err != nil {
		t.Fatalf("failed decoding GET response: %v", err)
	}
	if getResult["value"] != "tenant-a-private-data" {
		t.Errorf("expected 'tenant-a-private-data', got %v", getResult["value"])
	}
}


