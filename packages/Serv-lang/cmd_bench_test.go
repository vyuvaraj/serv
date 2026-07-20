package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestExtractBenchRoutes(t *testing.T) {
	content := `
		route "GET" "/user"
		route "POST" "/user"
		route "GET" "/health"
	`
	tmpFile, err := os.CreateTemp("", "bench-*.srv")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write([]byte(content)); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	tmpFile.Close()

	routes, err := extractBenchRoutes(tmpFile.Name())
	if err != nil {
		t.Fatalf("extractBenchRoutes failed: %v", err)
	}

	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
	if routes[0].Path != "/user" || routes[1].Path != "/health" {
		t.Errorf("unexpected routes: %+v", routes)
	}
}

func TestRunBenchLoad(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer mockServer.Close()

	routes := []benchRoute{
		{Method: "GET", Path: "/user"},
		{Method: "GET", Path: "/health"},
	}

	results := runBenchLoad(mockServer.URL, routes, 10, 1)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Requests != 5 || results[0].Successes != 5 {
		t.Errorf("unexpected result for route 0: %+v", results[0])
	}
}
