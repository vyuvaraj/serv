package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"servcloud/pkg/orchestrator"
)

func TestNewServer(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "server-test-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	orch, _ := orchestrator.NewOrchestrator(tempDir)
	s := NewServer(orch, "", "")
	if s.orch != orch {
		t.Error("orch not set")
	}
}

func TestServerHealth(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "server-test-*")
	defer os.RemoveAll(tempDir)
	orch, _ := orchestrator.NewOrchestrator(tempDir)
	s := NewServer(orch, "", "")

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestServerDeployMissingParams(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "server-test-*")
	defer os.RemoveAll(tempDir)
	orch, _ := orchestrator.NewOrchestrator(tempDir)
	s := NewServer(orch, "", "")

	body, _ := json.Marshal(map[string]string{"name": ""})
	req := httptest.NewRequest("POST", "/api/deploy", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}

func TestServerDeployInvalidJSON(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "server-test-*")
	defer os.RemoveAll(tempDir)
	orch, _ := orchestrator.NewOrchestrator(tempDir)
	s := NewServer(orch, "", "")

	req := httptest.NewRequest("POST", "/api/deploy", bytes.NewReader([]byte("{invalid-json}")))
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}

func TestServerUndeployAbsent(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "server-test-*")
	defer os.RemoveAll(tempDir)
	orch, _ := orchestrator.NewOrchestrator(tempDir)
	s := NewServer(orch, "", "")

	req := httptest.NewRequest("DELETE", "/api/services/absent-service", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", rr.Code)
	}
}

func TestServerGetLogsAbsent(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "server-test-*")
	defer os.RemoveAll(tempDir)
	orch, _ := orchestrator.NewOrchestrator(tempDir)
	s := NewServer(orch, "", "")

	req := httptest.NewRequest("GET", "/api/services/absent-service/logs", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", rr.Code)
	}
}

func TestServerRollbackAbsent(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "server-test-*")
	defer os.RemoveAll(tempDir)
	orch, _ := orchestrator.NewOrchestrator(tempDir)
	s := NewServer(orch, "", "")

	req := httptest.NewRequest("POST", "/api/services/absent-service/rollback", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request, got %d", rr.Code)
	}
}

func TestServerInvokeAbsent(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "server-test-*")
	defer os.RemoveAll(tempDir)
	orch, _ := orchestrator.NewOrchestrator(tempDir)
	s := NewServer(orch, "", "")

	req := httptest.NewRequest("GET", "/api/services/absent-service/invoke", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", rr.Code)
	}
}

func TestServerStatsAbsent(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "server-test-*")
	defer os.RemoveAll(tempDir)
	orch, _ := orchestrator.NewOrchestrator(tempDir)
	s := NewServer(orch, "", "")

	req := httptest.NewRequest("GET", "/api/services/absent-service/stats", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", rr.Code)
	}
}

func TestServerHistory(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "server-test-*")
	defer os.RemoveAll(tempDir)
	orch, _ := orchestrator.NewOrchestrator(tempDir)
	s := NewServer(orch, "", "")

	req := httptest.NewRequest("GET", "/api/history", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestServerListServices(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "server-test-*")
	defer os.RemoveAll(tempDir)
	orch, _ := orchestrator.NewOrchestrator(tempDir)
	s := NewServer(orch, "", "")

	req := httptest.NewRequest("GET", "/api/services", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
}

func TestServerUpdateEnvAbsent(t *testing.T) {
	tempDir, _ := os.MkdirTemp("", "server-test-*")
	defer os.RemoveAll(tempDir)
	orch, _ := orchestrator.NewOrchestrator(tempDir)
	s := NewServer(orch, "", "")

	body, _ := json.Marshal(map[string]string{"ENV_VAR": "val"})
	req := httptest.NewRequest("POST", "/api/services/absent-service/env", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 Not Found, got %d", rr.Code)
	}
}
