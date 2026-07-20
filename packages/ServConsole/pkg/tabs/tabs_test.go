package tabs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/config"
)

func TestTabsDeploymentAPI(t *testing.T) {
	deploymentsList := []Deployment{
		{ID: "dep-1", Version: "v1.0.0", Timestamp: time.Now(), Author: "vyuvaraj", Status: "active", Changelog: "initial release"},
	}
	var mu sync.Mutex

	getUserRole := func(r *http.Request) string {
		return "admin"
	}
	writeError := func(w http.ResponseWriter, r *http.Request, msg, code string, status int) {
		http.Error(w, msg, status)
	}

	Init(&deploymentsList, &mu, writeError, func(u, a, m, p string, s int) {}, getUserRole, nil)

	// Test HandleDeployments GET
	req := httptest.NewRequest("GET", "/api/deployments", nil)
	w := httptest.NewRecorder()
	HandleDeployments(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp []Deployment
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(resp) != 1 || resp[0].ID != "dep-1" {
		t.Errorf("deployments mismatch")
	}
}

func TestTabsClusterEndpoint(t *testing.T) {
	checkStatus := func(name, url string) config.ComponentStatus {
		return config.ComponentStatus{Name: name, Online: true, LatencyMs: 5}
	}
	Init(nil, nil, nil, nil, nil, checkStatus)

	req := httptest.NewRequest("GET", "/api/cluster", nil)
	w := httptest.NewRecorder()
	HandleCluster(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestHandleConsoleLocks(t *testing.T) {
	// Start mock ServLock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/locks/observability" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"key":"test-lock","owner":"test-owner","fencing_token":123,"expires_at":"2026-07-16T12:00:00Z","waiters":["waiter-1"]}]`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config.ActiveDiscovery.Lock = server.URL

	req := httptest.NewRequest("GET", "/api/locks", nil)
	w := httptest.NewRecorder()
	HandleConsoleLocks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var locks []map[string]any
	if err := json.NewDecoder(w.Body).Decode(&locks); err != nil {
		t.Fatalf("failed to decode locks: %v", err)
	}

	if len(locks) != 1 || locks[0]["key"] != "test-lock" {
		t.Errorf("unexpected locks response: %+v", locks)
	}
}

func TestHandleConsoleSecrets(t *testing.T) {
	// Start mock ServSecret server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/secrets" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"secrets":{"db.pass":"super-secret"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	config.ActiveDiscovery.Secret = server.URL

	req := httptest.NewRequest("GET", "/api/secrets", nil)
	w := httptest.NewRecorder()
	HandleConsoleSecrets(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var res map[string]any
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode secrets: %v", err)
	}

	secrets := res["secrets"].(map[string]any)
	if secrets["db.pass"] != "super-secret" {
		t.Errorf("unexpected secrets response: %+v", res)
	}
}
