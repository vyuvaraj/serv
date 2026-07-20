package s3

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"servstore/pkg/auth"
	"servstore/pkg/storage"
)

func TestS3CloudEventsNotifications(t *testing.T) {
	dir, err := os.MkdirTemp("", "servstore-cloudevents-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	store, err := storage.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("failed to create local store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	bucket := "events-bucket"
	if err := store.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	var mu sync.Mutex
	receivedEvents := make([]map[string]interface{}, 0)

	// Mock webhook endpoint acting as CloudEvents receiver
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("failed to decode CloudEvent: %v", err)
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
	server := httptest.NewServer(gateway)
	defer server.Close()

	client := &http.Client{}

	// 1. Configure CloudEvents Notifications via PUT /<bucket>?notification
	rules := []storage.EventNotificationRule{
		{
			ID:         "rule-1",
			Events:     []string{"ObjectCreated:Put", "ObjectRemoved:Delete"},
			FilterKey:  "images/",
			WebhookURL: webhookServer.URL,
		},
	}
	confBytes, _ := json.Marshal(rules)
	putReq, _ := http.NewRequest("PUT", server.URL+"/"+bucket+"?notification", bytes.NewReader(confBytes))
	putReq.Header.Set("Content-Type", "application/json")
	putReq.SetBasicAuth("admin", "admin")

	putResp, err := client.Do(putReq)
	if err != nil {
		t.Fatalf("PUT notification failed: %v", err)
	}
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("expected PUT notifications response 200, got %d", putResp.StatusCode)
	}

	// 2. Fetch config via GET /<bucket>?notification
	getReq, _ := http.NewRequest("GET", server.URL+"/"+bucket+"?notification", nil)
	getReq.SetBasicAuth("admin", "admin")

	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("GET notification failed: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("expected GET notifications response 200, got %d", getResp.StatusCode)
	}

	var fetchedRules []storage.EventNotificationRule
	if err := json.NewDecoder(getResp.Body).Decode(&fetchedRules); err != nil {
		t.Fatalf("failed to decode fetched rules: %v", err)
	}
	if len(fetchedRules) != 1 || fetchedRules[0].ID != "rule-1" {
		t.Fatalf("rules config mismatch: %+v", fetchedRules)
	}

	// 3. PUT matched object (prefix matches "images/")
	putObjReq, _ := http.NewRequest("PUT", server.URL+"/"+bucket+"/images/cat.jpg", strings.NewReader("fake jpeg data"))
	putObjReq.Header.Set("Content-Type", "image/jpeg")
	putObjReq.SetBasicAuth("admin", "admin")

	resp, err := client.Do(putObjReq)
	if err != nil {
		t.Fatalf("PUT matched object failed: %v", err)
	}
	resp.Body.Close()

	// 4. PUT unmatched object (prefix does not match "images/")
	putUnmatchedReq, _ := http.NewRequest("PUT", server.URL+"/"+bucket+"/docs/cv.pdf", strings.NewReader("pdf data"))
	putUnmatchedReq.Header.Set("Content-Type", "application/pdf")
	putUnmatchedReq.SetBasicAuth("admin", "admin")

	resp2, err := client.Do(putUnmatchedReq)
	if err != nil {
		t.Fatalf("PUT unmatched object failed: %v", err)
	}
	resp2.Body.Close()

	// 5. DELETE matched object
	delReq, _ := http.NewRequest("DELETE", server.URL+"/"+bucket+"/images/cat.jpg", nil)
	delReq.SetBasicAuth("admin", "admin")

	resp3, err := client.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE matched object failed: %v", err)
	}
	resp3.Body.Close()

	// Wait for async events dispatch
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Expecting 2 events: PUT images/cat.jpg and DELETE images/cat.jpg
	if len(receivedEvents) != 2 {
		t.Fatalf("expected exactly 2 events to webhook, got %d: %v", len(receivedEvents), receivedEvents)
	}

	// Validate CloudEvent properties of PUT Event
	putCE := receivedEvents[0]
	if putCE["specversion"] != "1.0" {
		t.Errorf("expected specversion 1.0, got %v", putCE["specversion"])
	}
	if putCE["type"] != "com.servstore.s3.object.created" {
		t.Errorf("expected type created, got %v", putCE["type"])
	}
	if putCE["source"] != "/buckets/events-bucket/objects/images/cat.jpg" {
		t.Errorf("expected source path, got %v", putCE["source"])
	}
	data, ok := putCE["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected data field object, got %T", putCE["data"])
	}
	if data["event"] != "ObjectCreated:Put" {
		t.Errorf("expected ObjectCreated:Put data, got %v", data["event"])
	}

	// Validate CloudEvent properties of DELETE Event
	delCE := receivedEvents[1]
	if delCE["type"] != "com.servstore.s3.object.deleted" {
		t.Errorf("expected type deleted, got %v", delCE["type"])
	}
	delData := delCE["data"].(map[string]interface{})
	if delData["event"] != "ObjectRemoved:Delete" {
		t.Errorf("expected ObjectRemoved:Delete data, got %v", delData["event"])
	}
}
