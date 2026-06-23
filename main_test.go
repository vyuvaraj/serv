package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"servcache/pkg/cache"
	"servcache/pkg/server"
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

