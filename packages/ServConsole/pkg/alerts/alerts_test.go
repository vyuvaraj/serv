package alerts

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"servconsole/pkg/config"
)

func TestAlertsAddingUpdatingClearing(t *testing.T) {
	alertsList := []Alert{}
	var mu sync.Mutex

	checkStatus := func(name, url string) config.ComponentStatus {
		return config.ComponentStatus{Online: true, LatencyMs: 10}
	}
	writeError := func(w http.ResponseWriter, r *http.Request, msg, code string, status int) {
		http.Error(w, msg, status)
	}

	Init(&alertsList, &mu, checkStatus, writeError)

	// 1. Add alert
	AddOrUpdateAlert("ServGate", "offline", "ServGate offline msg", "critical")
	if len(alertsList) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alertsList))
	}
	if alertsList[0].Component != "ServGate" || alertsList[0].Severity != "critical" {
		t.Errorf("alert field mismatch")
	}

	// 2. Update alert
	AddOrUpdateAlert("ServGate", "offline", "ServGate is definitely offline", "critical")
	if len(alertsList) != 1 {
		t.Fatalf("expected 1 alert after update, got %d", len(alertsList))
	}
	if alertsList[0].Message != "ServGate is definitely offline" {
		t.Errorf("expected updated message, got %q", alertsList[0].Message)
	}

	// 3. Clear alert
	ClearAlert("ServGate", "offline")
	if len(alertsList) != 0 {
		t.Errorf("expected 0 alerts, got %d", len(alertsList))
	}
}

func TestHandleAlertsEndpoint(t *testing.T) {
	alertsList := []Alert{
		{ID: "alert-1", Component: "ServStore", Type: "disk_full", Message: "Disk 95%", Severity: "warning"},
	}
	var mu sync.Mutex

	Init(&alertsList, &mu, nil, nil)

	req := httptest.NewRequest("GET", "/api/alerts", nil)
	w := httptest.NewRecorder()
	HandleAlerts(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp []Alert
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed decoding: %v", err)
	}
	if len(resp) != 1 || resp[0].ID != "alert-1" {
		t.Errorf("resp mismatch")
	}
}

func TestHandleAlertAckEndpoint(t *testing.T) {
	alertsList := []Alert{
		{ID: "alert-100", Component: "ServQueue", Type: "queue_oom", Message: "OOM", Severity: "critical", Acknowledged: false},
	}
	var mu sync.Mutex
	writeError := func(w http.ResponseWriter, r *http.Request, msg, code string, status int) {
		http.Error(w, msg, status)
	}

	Init(&alertsList, &mu, nil, writeError)

	// Ack alert-100
	body := []byte(`{"id":"alert-100"}`)
	req := httptest.NewRequest("POST", "/api/alerts/ack", bytes.NewReader(body))
	w := httptest.NewRecorder()
	HandleAlertsAck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if !alertsList[0].Acknowledged {
		t.Error("expected alert to be acknowledged")
	}
}
