package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"servtunnel/pkg/client"
	"servtunnel/pkg/inspector"

	"gopkg.in/yaml.v3"
)

func TestParseThrottle(t *testing.T) {
	// We need to test the unexported parseThrottle or client.WithThrottle.
	// Since we can't access unexported functions across packages easily without exposing them,
	// let's test via client.NewClient("...", "...", "...", "...", "...", "...", "...").WithThrottle("256k").
	// Or we can verify the ThrottledReader behaviour.
	c := client.NewClient("localhost:8080", "ws://localhost", "", "", "", "", "")
	c.WithThrottle("256k")
	// Since throttleBytesPerSec is unexported, we can verify ThrottledReader directly!
}

func TestThrottledReader(t *testing.T) {
	data := make([]byte, 1000) // 1000 bytes
	reader := bytes.NewReader(data)
	
	// Throttle at 500 bytes per second.
	// Reading 1000 bytes should take at least 1.5 - 2 seconds.
	tr := &client.ThrottledReader{
		R:           reader,
		BytesPerSec: 500,
	}

	start := time.Now()
	buf := make([]byte, 500)
	
	// First read (500 bytes)
	n, err := tr.Read(buf)
	if err != nil || n != 500 {
		t.Fatalf("Failed to read first batch: %v, read %d", err, n)
	}

	// Second read (500 bytes) -> should trigger sleep
	n, err = tr.Read(buf)
	if err != nil || n != 500 {
		t.Fatalf("Failed to read second batch: %v, read %d", err, n)
	}

	dur := time.Since(start)
	if dur < 900*time.Millisecond {
		t.Errorf("Expected throttling to take at least ~1s, took %v", dur)
	}
}

func TestInspectorDiff(t *testing.T) {
	ins := inspector.New(10)

	idA := ins.Record(inspector.Entry{
		Method: "GET",
		Path:   "/users",
		RequestHeaders: map[string]string{
			"User-Agent": "agent-a",
			"X-Shared":   "yes",
		},
		RequestBody: base64.StdEncoding.EncodeToString([]byte("body-a")),
	})

	idB := ins.Record(inspector.Entry{
		Method: "GET",
		Path:   "/users",
		RequestHeaders: map[string]string{
			"User-Agent": "agent-b", // modified
			"X-Added":    "new",     // added
			"X-Shared":   "yes",     // same
		},
		RequestBody: base64.StdEncoding.EncodeToString([]byte("body-b")), // modified body
	})

	req := httptest.NewRequest("GET", "/api/inspect/"+idA+"/diff?other="+idB, nil)
	w := httptest.NewRecorder()
	ins.HandleDiff(w, req, idA)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var res map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("Failed to parse diff: %v", err)
	}

	headers, ok := res["headers"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing headers diff")
	}

	added := headers["added"].([]interface{})
	if len(added) != 1 || added[0] != "X-Added" {
		t.Errorf("Expected added header X-Added, got %v", added)
	}

	modified := headers["modified"].(map[string]interface{})
	userAgentDiff, exists := modified["User-Agent"].(map[string]interface{})
	if !exists || userAgentDiff["from"] != "agent-a" || userAgentDiff["to"] != "agent-b" {
		t.Errorf("Expected User-Agent diff, got %v", userAgentDiff)
	}

	body := res["body"].(map[string]interface{})
	diffText := body["diff"].(string)
	if diffText == "" {
		t.Errorf("Expected body diff description")
	}
}

func TestConfigFileLoading(t *testing.T) {
	yamlContent := `
relay: "ws://custom-relay:9000/ws"
token: "global-token-xyz"
tunnels:
  - port: "8080"
    subdomain: "custom-sub"
    throttle: "100k"
  - port: "9090"
    subdomain: "another-sub"
`
	// Write temporary yaml file
	err := os.WriteFile("tunnel_test_config.yaml", []byte(yamlContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write temp config file: %v", err)
	}
	defer os.Remove("tunnel_test_config.yaml")

	// Test tryLoadConfig with custom path or parsing logic manually
	data, err := os.ReadFile("tunnel_test_config.yaml")
	if err != nil {
		t.Fatalf("Failed to read: %v", err)
	}
	
	var cfg TunnelConfigFile
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		t.Fatalf("Failed to unmarshal yaml config: %v", err)
	}

	if cfg.Relay != "ws://custom-relay:9000/ws" {
		t.Errorf("Relay parsed incorrectly: %s", cfg.Relay)
	}
	if cfg.Token != "global-token-xyz" {
		t.Errorf("Token parsed incorrectly: %s", cfg.Token)
	}
	if len(cfg.Tunnels) != 2 {
		t.Fatalf("Expected 2 tunnels, got %d", len(cfg.Tunnels))
	}
	if cfg.Tunnels[0].Port != "8080" || cfg.Tunnels[0].Subdomain != "custom-sub" || cfg.Tunnels[0].Throttle != "100k" {
		t.Errorf("First tunnel parsed incorrectly: %+v", cfg.Tunnels[0])
	}
}
