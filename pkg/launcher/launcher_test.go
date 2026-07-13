package launcher

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLauncherEndpoints(t *testing.T) {
	// 1. Get dev services status
	req1 := httptest.NewRequest("GET", "/api/launcher/services", nil)
	w1 := httptest.NewRecorder()
	HandleDevServices(w1, req1)

	if w1.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w1.Code)
	}

	var services []DevServiceStatus
	if err := json.NewDecoder(w1.Body).Decode(&services); err != nil {
		t.Fatalf("failed decoding: %v", err)
	}

	// 2. Restart dev service non-POST returns Method Not Allowed
	req2 := httptest.NewRequest("GET", "/api/launcher/restart?service=ServGate", nil)
	w2 := httptest.NewRecorder()
	HandleDevRestart(w2, req2)

	if w2.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed, got %d", w2.Code)
	}
}
