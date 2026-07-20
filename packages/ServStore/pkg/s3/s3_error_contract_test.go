package s3

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"servstore/pkg/auth"
	"servstore/pkg/storage"
)

func TestStandardizedErrors(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}
	defer func() {
		// Allow async access-log goroutines to finish before closing the store,
		// otherwise TempDir cleanup races with background PutObject writes.
		time.Sleep(50 * time.Millisecond)
		store.Close()
	}()

	// Enable Auth to trigger Forbidden (AccessDenied) error
	authProv := auth.NewAuthProvider("admin", "adminsecret", true)
	gateway := NewGateway(store, authProv, nil, nil, 1, false, 0, 0)

	// Test 1: S3 client requesting XML (standard S3 XML response with trace parent/request id check)
	req := httptest.NewRequest("GET", "/test-bucket/non-existent", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d", w.Code)
	}

	var errResp ErrorResponse
	if err := xml.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to parse S3 XML error: %v, body: %s", err, w.Body.String())
	}
	if errResp.Code != "AccessDenied" {
		t.Errorf("expected AccessDenied, got %s", errResp.Code)
	}
	if errResp.RequestID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("expected trace ID %q as RequestId, got %q", "4bf92f3577b34da6a3ce929d0e0e4736", errResp.RequestID)
	}

	// Test 2: Console endpoint or client requesting JSON (returns JSON format + trace_id)
	reqJSON := httptest.NewRequest("GET", "/console/cluster/status", nil)
	reqJSON.Header.Set("Accept", "application/json")
	reqJSON.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	wJSON := httptest.NewRecorder()
	gateway.ServeHTTP(wJSON, reqJSON)

	if wJSON.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %d", wJSON.Code)
	}

	var jsonErr map[string]string
	if err := json.Unmarshal(wJSON.Body.Bytes(), &jsonErr); err != nil {
		t.Fatalf("failed to parse JSON error: %v, body: %s", err, wJSON.Body.String())
	}
	if jsonErr["code"] != "AccessDenied" {
		t.Errorf("expected JSON code AccessDenied, got %s", jsonErr["code"])
	}
	if jsonErr["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("expected trace_id %q in JSON, got %q", "4bf92f3577b34da6a3ce929d0e0e4736", jsonErr["trace_id"])
	}
}
