package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

func TestEventSnapshotsWebAPI(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "store_snap_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	storeEngine, err := storage.NewLocalStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}

	authProvider := auth.NewAuthProvider("key", "secret", false)
	wc := NewWebConsole(nil, authProvider, storeEngine, nil, nil)

	payload := `{"orderId":123,"amount":99.99,"status":"placed"}`
	reqPut := httptest.NewRequest("PUT", "/api/v1/events/snapshots/orders/aggregate-123", bytes.NewReader([]byte(payload)))
	wPut := httptest.NewRecorder()
	wc.ServeHTTP(wPut, reqPut)

	respPut := wPut.Result()
	if respPut.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", respPut.StatusCode)
	}

	reqGet := httptest.NewRequest("GET", "/api/v1/events/snapshots/orders/aggregate-123", nil)
	wGet := httptest.NewRecorder()
	wc.ServeHTTP(wGet, reqGet)

	respGet := wGet.Result()
	if respGet.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", respGet.StatusCode)
	}

	body, _ := io.ReadAll(respGet.Body)
	if string(body) != payload {
		t.Errorf("Expected snapshot payload %q, got %q", payload, string(body))
	}
}
