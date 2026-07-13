package tabs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"servconsole/pkg/config"
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
