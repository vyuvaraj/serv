package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/auth"
	"github.com/vyuvaraj/serv/packages/ServStore/pkg/storage"
)

func TestGatewayNotifications(t *testing.T) {
	dir, _ := os.MkdirTemp("", "servstore-notifications-*")
	defer os.RemoveAll(dir)

	store, _ := storage.NewLocalStore(dir)
	ctx := context.Background()
	_ = store.CreateBucket(ctx, "test-bucket")

	// Set up a mock webhook receiver
	var mu sync.Mutex
	receivedEvents := make([]map[string]interface{}, 0)

	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read webhook body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("failed to unmarshal webhook payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		receivedEvents = append(receivedEvents, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer webhookServer.Close()

	authProv := auth.NewAuthProvider("admin", "admin", false)
	gateway := NewGateway(store, authProv, nil, nil, 1, false, 0, 0)
	gateway.WithNotificationWebhook(webhookServer.URL)

	gatewayServer := httptest.NewServer(gateway)
	defer gatewayServer.Close()

	// 1. Put Object
	client := &http.Client{}
	putPayload := []byte("hello event notifications")
	putReq, _ := http.NewRequest("PUT", gatewayServer.URL+"/test-bucket/hello.txt", bytes.NewReader(putPayload))
	putReq.Header.Set("Content-Type", "text/plain")
	putReq.SetBasicAuth("admin", "admin")

	putResp, err := client.Do(putReq)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}
	defer putResp.Body.Close()

	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d", putResp.StatusCode)
	}

	// 2. Delete Object
	delReq, _ := http.NewRequest("DELETE", gatewayServer.URL+"/test-bucket/hello.txt", nil)
	delReq.SetBasicAuth("admin", "admin")

	delResp, err := client.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE request failed: %v", err)
	}
	defer delResp.Body.Close()

	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE expected 204, got %d", delResp.StatusCode)
	}

	// Give a moment for async goroutines to fire
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(receivedEvents) != 2 {
		t.Fatalf("expected 2 received events, got %d", len(receivedEvents))
	}

	// Validate PUT Event
	putEvent := receivedEvents[0]
	if putEvent["event"] != "ObjectCreated:Put" {
		t.Errorf("expected event ObjectCreated:Put, got %v", putEvent["event"])
	}
	if putEvent["bucket"] != "test-bucket" {
		t.Errorf("expected bucket test-bucket, got %v", putEvent["bucket"])
	}
	if putEvent["key"] != "hello.txt" {
		t.Errorf("expected key hello.txt, got %v", putEvent["key"])
	}

	// Validate DELETE Event
	delEvent := receivedEvents[1]
	if delEvent["event"] != "ObjectRemoved:Delete" {
		t.Errorf("expected event ObjectRemoved:Delete, got %v", delEvent["event"])
	}
	if delEvent["bucket"] != "test-bucket" {
		t.Errorf("expected bucket test-bucket, got %v", delEvent["bucket"])
	}
	if delEvent["key"] != "hello.txt" {
		t.Errorf("expected key hello.txt, got %v", delEvent["key"])
	}
}
