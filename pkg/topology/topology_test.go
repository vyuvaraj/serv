package topology

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTopologyAPIEndpoints(t *testing.T) {
	writeError := func(w http.ResponseWriter, r *http.Request, msg, code string, status int) {
		http.Error(w, msg, status)
	}

	Init(writeError)

	// 1. Test HandleTopology
	req1 := httptest.NewRequest("GET", "/api/topology", nil)
	w1 := httptest.NewRecorder()
	HandleTopology(w1, req1)

	if w1.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w1.Code)
	}

	var resp1 TopologyResponse
	if err := json.NewDecoder(w1.Body).Decode(&resp1); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	// 2. Test HandleTopologyLive
	req2 := httptest.NewRequest("GET", "/api/topology/live", nil)
	w2 := httptest.NewRecorder()
	HandleTopologyLive(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w2.Code)
	}

	// 3. Test HandleTraceReplay
	body := `{"trace_id":"trace-123","root_span_name":"GET /api/test"}`
	req3 := httptest.NewRequest("POST", "/api/traces/replay", strings.NewReader(body))
	w3 := httptest.NewRecorder()
	HandleTraceReplay(w3, req3)

	if w3.Code != http.StatusOK && w3.Code != http.StatusBadRequest {
		t.Errorf("expected 200 or 400, got %d: %s", w3.Code, w3.Body.String())
	}
}
