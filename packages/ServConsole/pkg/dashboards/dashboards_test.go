package dashboards

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"servconsole/pkg/config"
)

func TestDashboardsAPIEndpoints(t *testing.T) {
	buf := []LogEntry{}
	var mu sync.Mutex
	LogBuffer = &buf
	LogBufferMu = &mu

	checkStatus := func(name, url string) config.ComponentStatus { return config.ComponentStatus{Online: true} }
	writeError := func(w http.ResponseWriter, r *http.Request, msg, code string, status int) { http.Error(w, msg, status) }
	addAlert := func(c, t, m, s string) {}
	clearAlert := func(c, t string) {}
	getUserRole := func(r *http.Request) string { return "admin" }
	scaleTrigger := func(s, m string) {}
	auditLog := func(u, a, m, p string, s int) {}

	Init(checkStatus, writeError, addAlert, clearAlert, getUserRole, scaleTrigger, auditLog)

	// 1. Ingest Log
	logPayload := `{"service":"auth-service","level":"info","message":"user logged in successfully"}`
	req1 := httptest.NewRequest("POST", "/api/logs/ingest", bytes.NewReader([]byte(logPayload)))
	w1 := httptest.NewRecorder()
	HandleIngestLog(w1, req1)

	if w1.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w1.Code)
	}

	// 2. Get Logs
	req2 := httptest.NewRequest("GET", "/api/logs?service=auth-service", nil)
	w2 := httptest.NewRecorder()
	HandleGetLogs(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w2.Code)
	}

	var respLogs []LogEntry
	if err := json.NewDecoder(w2.Body).Decode(&respLogs); err != nil {
		t.Fatalf("failed decoding: %v", err)
	}
	if len(respLogs) != 1 || respLogs[0].Service != "auth-service" {
		t.Errorf("logged service mismatch")
	}

	// 3. SLO endpoint
	req3 := httptest.NewRequest("GET", "/api/slo", nil)
	w3 := httptest.NewRecorder()
	HandleSLO(w3, req3)

	if w3.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w3.Code)
	}
}
