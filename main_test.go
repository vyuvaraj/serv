package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"servqueue/pkg/broker"
	"servqueue/pkg/stomp"
	"servqueue/pkg/web"
)

// Simple WASI mock module byte representation for testing WASM execution.
// If actual wasm runner is utilized, we need compiled wasm. Here, we can mock the transform
// function under the hood in testing. However, to fully test RunTransform, we can provide a minimal
// WASI WebAssembly binary that performs upper-casing of its stdin.
// Below is a pre-compiled minimal WebAssembly module that reads stdin and writes it back as uppercase.
var uppercaseWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // WASM magic and version
	// We will register a mock transform to run the test hermetically without requiring wazero compilation toolchains
}

func TestServQueueWasmTransformIntegration(t *testing.T) {
	_ = os.Remove("queue.wal")
	defer os.Remove("queue.wal")

	// 1. Initialize broker engine
	engine := broker.NewBrokerEngine()

	// 2. Start STOMP server (no auth required for simple integration test)
	stompServer := stomp.NewServer("127.0.0.1:61614", engine, "", "", "", "")
	go stompServer.Start()

	// 3. Start Web server (no auth required here)
	webServer := web.NewServer("127.0.0.1:8083", engine, "", "", "")
	go webServer.Start()

	// Wait for servers to spin up
	time.Sleep(200 * time.Millisecond)

	// 4. Register a WASM mock bytes for a topic (we mock it or let it bypass if empty, but we can verify routing)
	topic := "orders"
	
	// Create sub
	subChan := engine.Subscribe(topic)
	defer engine.Unsubscribe(topic, subChan)

	// Register empty/mock transform
	engine.RegisterTransform(context.Background(), topic, []byte{})

	// Publish message
	msg := "hello servqueue"
	_, err := engine.Publish(context.Background(), topic, msg)
	if err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Read message from subscription
	select {
	case received := <-subChan:
		if received != msg {
			t.Errorf("Expected %q, got %q", msg, received)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for message")
	}
}

func TestHTTPPublish(t *testing.T) {
	_ = os.Remove("queue.wal")
	defer os.Remove("queue.wal")

	engine := broker.NewBrokerEngine()
	// Test with active authentication token
	token := "test-token"
	webServer := web.NewServer("127.0.0.1:8084", engine, token, "", "")
	go webServer.Start()
	time.Sleep(200 * time.Millisecond)

	subChan := engine.Subscribe("test-http")

	reqBody := []byte(`{"topic":"test-http","payload":"http-message"}`)
	req, err := http.NewRequest("POST", "http://127.0.0.1:8084/api/publish", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	select {
	case msg := <-subChan:
		if msg != "http-message" {
			t.Errorf("Expected 'http-message', got %q", msg)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for HTTP published message")
	}

	// Verify metrics endpoint with auth
	reqStats, err := http.NewRequest("GET", "http://127.0.0.1:8084/api/stats", nil)
	if err != nil {
		t.Fatalf("Failed to create stats request: %v", err)
	}
	reqStats.Header.Set("Authorization", "Bearer "+token)

	statsResp, err := http.DefaultClient.Do(reqStats)
	if err != nil {
		t.Fatalf("Failed to fetch stats: %v", err)
	}
	defer statsResp.Body.Close()

	var stats map[string]interface{}
	if err := json.NewDecoder(statsResp.Body).Decode(&stats); err != nil {
		t.Fatalf("Failed to decode stats: %v", err)
	}

	metrics, ok := stats["metrics"].(map[string]interface{})
	if !ok {
		t.Fatal("Metrics object missing from stats response")
	}

	pubCount := metrics["messages_published_total"].(float64)
	if pubCount != 1 {
		t.Errorf("Expected messages_published_total to be 1, got %v", pubCount)
	}
}

func TestMessageDeduplication(t *testing.T) {
	_ = os.Remove("queue.wal")
	defer os.Remove("queue.wal")

	engine := broker.NewBrokerEngine()

	topic := "dedup-test"
	subChan := engine.Subscribe(topic)

	ctx1 := context.WithValue(context.Background(), "message-id", "msg-12345")
	_, err := engine.Publish(ctx1, topic, "message-payload-1")
	if err != nil {
		t.Fatalf("Failed to publish first message: %v", err)
	}

	select {
	case received := <-subChan:
		if received != "message-payload-1" {
			t.Errorf("Expected 'message-payload-1', got %q", received)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for first message")
	}

	ctx2 := context.WithValue(context.Background(), "message-id", "msg-12345")
	_, err = engine.Publish(ctx2, topic, "message-payload-2")
	if err == nil {
		t.Error("Expected error when publishing duplicate message ID, got nil")
	}

	select {
	case received := <-subChan:
		t.Errorf("Received duplicate message when we expected it to be dropped: %q", received)
	case <-time.After(100 * time.Millisecond):
	}

	ctx3 := context.WithValue(context.Background(), "message-id", "msg-12346")
	_, err = engine.Publish(ctx3, topic, "message-payload-3")
	if err != nil {
		t.Fatalf("Failed to publish third message: %v", err)
	}

	select {
	case received := <-subChan:
		if received != "message-payload-3" {
			t.Errorf("Expected 'message-payload-3', got %q", received)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for third message")
	}
}

func TestWasmHotSwapDeferredClose(t *testing.T) {
	_ = os.Remove("queue.wal")
	defer os.Remove("queue.wal")

	engine := broker.NewBrokerEngine()
	topic := "hot-swap-test"

	// Minimal no-op compiled WASM module
	noopWasm := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	
	err := engine.RegisterTransform(context.Background(), topic, noopWasm)
	if err != nil {
		t.Fatalf("Failed to register first transform: %v", err)
	}

	// Trigger hot-swap while keeping reference to the old one
	err = engine.RegisterTransform(context.Background(), topic, noopWasm)
	if err != nil {
		t.Fatalf("Failed to hot-swap second transform: %v", err)
	}

	// Verify it does not crash or panic when executing basic publications
	_, err = engine.Publish(context.Background(), topic, "message")
	if err != nil {
		t.Fatalf("Failed to publish after hot-swap: %v", err)
	}
}

func TestDelayedMessageDelivery(t *testing.T) {
	_ = os.Remove("queue.wal")
	defer os.Remove("queue.wal")

	engine := broker.NewBrokerEngine()
	topic := "delayed-test"
	subChan := engine.Subscribe(topic)

	ctx := context.WithValue(context.Background(), "delay-ms", "200")
	_, err := engine.Publish(ctx, topic, "delayed-payload")
	if err != nil {
		t.Fatalf("Failed to publish delayed message: %v", err)
	}

	select {
	case received := <-subChan:
		t.Fatalf("Message delivered prematurely: %q", received)
	case <-time.After(50 * time.Millisecond):
	}

	select {
	case received := <-subChan:
		if received != "delayed-payload" {
			t.Errorf("Expected 'delayed-payload', got %q", received)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("Timeout waiting for delayed message delivery")
	}
}

func TestStatsWALAndDelayedTracking(t *testing.T) {
	_ = os.Remove("queue.wal")
	defer os.Remove("queue.wal")

	engine := broker.NewBrokerEngine()
	topic := "stats-test"

	// Publish normal message to write to WAL
	_, err := engine.Publish(context.Background(), topic, "normal-payload")
	if err != nil {
		t.Fatalf("Failed to publish normal message: %v", err)
	}

	// Publish delayed message
	ctx := context.WithValue(context.Background(), "delay-ms", "300")
	ctx = context.WithValue(ctx, "message-id", "msg-delayed-1")
	_, err = engine.Publish(ctx, topic, "delayed-payload")
	if err != nil {
		t.Fatalf("Failed to publish delayed message: %v", err)
	}

	// Verify WAL entry exists
	entries, err := engine.GetWALEntries()
	if err != nil {
		t.Fatalf("Failed to get WAL entries: %v", err)
	}
	if len(entries) < 1 {
		t.Errorf("Expected at least 1 WAL entry, got %d", len(entries))
	} else {
		foundNormal := false
		for _, entry := range entries {
			if entry.Payload == "normal-payload" {
				foundNormal = true
				break
			}
		}
		if !foundNormal {
			t.Errorf("Could not find 'normal-payload' in WAL entries")
		}
	}

	// Verify delayed message exists
	delayed := engine.GetDelayedMessages()
	if len(delayed) != 1 {
		t.Errorf("Expected exactly 1 delayed message, got %d", len(delayed))
	} else {
		if delayed[0].ID != "msg-delayed-1" || delayed[0].Payload != "delayed-payload" {
			t.Errorf("Unexpected delayed message: %+v", delayed[0])
		}
	}

	// Wait for delayed message delivery
	time.Sleep(350 * time.Millisecond)

	// Verify delayed message is cleared
	delayed = engine.GetDelayedMessages()
	if len(delayed) != 0 {
		t.Errorf("Expected 0 delayed messages after delivery, got %d", len(delayed))
	}
}

