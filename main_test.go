package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestServDBConnectionPoolingAndRouting(t *testing.T) {
	primaryPool = NewConnectionPool(3)
	replicaPool = NewConnectionPool(3)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/db/query", handleQuery)
	mux.HandleFunc("/api/db/stats", handleStats)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Run concurrent queries to verify pool limit acquisition and routing
	// Run 2 SELECT queries (should route to Replica pool)
	// Run 1 INSERT query (should route to Primary pool)
	var wg sync.WaitGroup

	// SELECT queries
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reqPayload := QueryRequest{Query: "SELECT * FROM users;"}
			body, _ := json.Marshal(reqPayload)
			resp, err := http.Post(testServer.URL+"/api/db/query", "application/json", bytes.NewReader(body))
			if err == nil {
				var queryRes QueryResponse
				_ = json.NewDecoder(resp.Body).Decode(&queryRes)
				if len(queryRes.Rows) > 0 && queryRes.Rows[0]["pool"] != "replica" {
					t.Errorf("expected SELECT query to route to replica pool, got %v", queryRes.Rows[0]["pool"])
				}
				resp.Body.Close()
			}
		}()
	}

	// INSERT query
	wg.Add(1)
	go func() {
		defer wg.Done()
		reqPayload := QueryRequest{Query: "INSERT INTO users (name) VALUES ('John');"}
		body, _ := json.Marshal(reqPayload)
		resp, err := http.Post(testServer.URL+"/api/db/query", "application/json", bytes.NewReader(body))
		if err == nil {
			var queryRes QueryResponse
			_ = json.NewDecoder(resp.Body).Decode(&queryRes)
			if len(queryRes.Rows) > 0 && queryRes.Rows[0]["pool"] != "primary" {
				t.Errorf("expected INSERT query to route to primary pool, got %v", queryRes.Rows[0]["pool"])
			}
			resp.Body.Close()
		}
	}()

	wg.Wait()

	// 2. Fetch stats
	statsResp, err := http.Get(testServer.URL + "/api/db/stats")
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}
	defer statsResp.Body.Close()

	var stats StatsResponse
	if err := json.NewDecoder(statsResp.Body).Decode(&stats); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}

	if stats.Primary.MaxConnections != 3 || stats.Replica.MaxConnections != 3 {
		t.Errorf("expected max connections to be 3 in pools, got %+v", stats)
	}

	if stats.Primary.TotalQueries != 1 {
		t.Errorf("expected 1 primary total queries, got %d", stats.Primary.TotalQueries)
	}

	if stats.Replica.TotalQueries != 2 {
		t.Errorf("expected 2 replica total queries, got %d", stats.Replica.TotalQueries)
	}
}
