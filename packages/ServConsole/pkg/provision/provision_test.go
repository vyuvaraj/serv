package provision

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProvisionEndpoints(t *testing.T) {
	writeError := func(w http.ResponseWriter, r *http.Request, msg, code string, status int) {
		http.Error(w, msg, status)
	}
	auditLog := func(user, action, method, path string, status int) {}

	Init(writeError, auditLog)

	// 1. Provision Store (POST)
	body1 := `{"bucketName":"test-bucket-123"}`
	req1 := httptest.NewRequest("POST", "/api/provision/store", bytes.NewReader([]byte(body1)))
	w1 := httptest.NewRecorder()
	HandleProvisionStore(w1, req1)

	if w1.Code != http.StatusOK && w1.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 200 or 503, got %d", w1.Code)
	}

	// 2. Provision Store (GET)
	req2 := httptest.NewRequest("GET", "/api/provision/store", nil)
	w2 := httptest.NewRecorder()
	HandleProvisionStore(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", w2.Code)
	}

	var buckets []string
	_ = json.NewDecoder(w2.Body).Decode(&buckets)
}
