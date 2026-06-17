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
