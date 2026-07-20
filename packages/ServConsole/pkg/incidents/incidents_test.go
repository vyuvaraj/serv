package incidents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSLOTracker(t *testing.T) {
	tracker := NewSLOTracker()
	req := httptest.NewRequest("GET", "/api/slo", nil)
	w := httptest.NewRecorder()
	tracker.HandleSLO(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var slos map[string]SLO
	if err := json.NewDecoder(w.Body).Decode(&slos); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	if len(slos) == 0 {
		t.Error("expected SLOs to be returned")
	}
}
