//go:build enterprise

package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"servgate/pkg/wasm"
)

func TestAIBudgetEnforcement(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"reply": "test response content", "usage": {"total_tokens": 10}}`))
	}))
	defer backend.Close()

	routes := []Route{
		{
			Prefix:        "/ai",
			Target:        backend.URL,
			RequireAPIKey: true,
		},
	}

	wasmManager, _ := wasm.GetMiddlewareManager(context.Background())
	handler := NewGatewayHandler(routes, wasmManager, "")
	
	// Configure API Key with MaxTokensPerDay = 5 (very low budget)
	handler.SetAPIKeys([]APIKey{
		{
			Key:             "key-test-budget",
			Tenant:          "tenant-1",
			MaxTokensPerDay: 5,
		},
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	client := &http.Client{}

	// Request 1: Should succeed, consuming 10 tokens
	payload := `{"prompt": "hello world"}`
	req1, _ := http.NewRequest("POST", server.URL+"/ai/ask", strings.NewReader(payload))
	req1.Header.Set("X-API-Key", "key-test-budget")
	req1.Header.Set("Content-Type", "application/json")
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("Request 1 failed: %v", err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Expected first request status 200, got %d", resp1.StatusCode)
	}

	// Request 2: Should be rejected because daily budget (5 tokens) is exceeded (since 10 tokens were consumed)
	req2, _ := http.NewRequest("POST", server.URL+"/ai/ask", strings.NewReader(payload))
	req2.Header.Set("X-API-Key", "key-test-budget")
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("Request 2 failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Expected second request status 429 Too Many Requests, got %d", resp2.StatusCode)
	}
}

func TestAIPromptABTestingAndQualityScoring(t *testing.T) {
	lastPromptReceived := ""
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var data map[string]interface{}
		json.Unmarshal(body, &data)
		
		// Extract prompt
		if p, ok := data["prompt"].(string); ok {
			lastPromptReceived = p
		} else if msgs, ok := data["messages"].([]interface{}); ok && len(msgs) > 0 {
			if first, ok := msgs[0].(map[string]interface{}); ok {
				lastPromptReceived = first["content"].(string)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"reply": "artificial intelligence system and machine learning models"}`))
	}))
	defer backend.Close()

	routes := []Route{
		{
			Prefix:               "/ai",
			Target:               backend.URL,
			ResponseQualityScore: true,
			PromptABTest: &PromptABTest{
				PromptName: "test-ab",
				Versions: map[string]string{
					"v1": "System prompt: {{text}}",
				},
				Weights: map[string]int{
					"v1": 100,
				},
			},
		},
	}

	wasmManager, _ := wasm.GetMiddlewareManager(context.Background())
	handler := NewGatewayHandler(routes, wasmManager, "")
	server := httptest.NewServer(handler)
	defer server.Close()

	client := &http.Client{}

	payload := `{"prompt": "artificial intelligence"}`
	req, _ := http.NewRequest("POST", server.URL+"/ai/ask", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify A/B testing injected system prompt template
	if !strings.Contains(lastPromptReceived, "System prompt:") {
		t.Errorf("Expected injected system prompt template, got %q", lastPromptReceived)
	}

	// Verify X-AB-Version header
	abVer := resp.Header.Get("X-AB-Version")
	if abVer != "v1" {
		t.Errorf("Expected X-AB-Version 'v1', got %q", abVer)
	}

	// Verify X-Grounding-Score header (response contains "artificial intelligence", prompt was "artificial intelligence")
	scoreStr := resp.Header.Get("X-Grounding-Score")
	if scoreStr == "" {
		t.Errorf("Expected X-Grounding-Score header, got empty")
	}
}

func TestAISemanticRateLimiting(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	routes := []Route{
		{
			Prefix:            "/ai",
			Target:            backend.URL,
			SemanticRateLimit: true,
		},
	}

	wasmManager, _ := wasm.GetMiddlewareManager(context.Background())
	handler := NewGatewayHandler(routes, wasmManager, "")
	server := httptest.NewServer(handler)
	defer server.Close()

	client := &http.Client{}

	// Request 1: Should succeed
	payload1 := `{"prompt": "what is capital of France?"}`
	req1, _ := http.NewRequest("POST", server.URL+"/ai/ask", strings.NewReader(payload1))
	req1.Header.Set("Content-Type", "application/json")
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("Request 1 failed: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Expected request 1 status 200, got %d", resp1.StatusCode)
	}

	// Request 2: Semantically similar, should be rate limited (429)
	payload2 := `{"prompt": "what is the capital of France?"}`
	req2, _ := http.NewRequest("POST", server.URL+"/ai/ask", strings.NewReader(payload2))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("Request 2 failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Expected request 2 status 429, got %d", resp2.StatusCode)
	}
}
