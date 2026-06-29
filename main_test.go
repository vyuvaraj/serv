package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServMailTemplateAndChannels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/send", handleSend)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Test Email Channel Template Rendering
	payload := SendRequest{
		Channel:  "email",
		Target:   "admin@example.com",
		Template: "Hello {{.Name}}! Your code is {{.Code}}.",
		Context: map[string]interface{}{
			"Name": "Alice",
			"Code": 123456,
		},
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to post send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected StatusOK, got %d", resp.StatusCode)
	}

	var res SendResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if res.Status != "delivered" || res.DeliveredTo != "admin@example.com" {
		t.Errorf("unexpected delivery response: %+v", res)
	}

	expectedBody := "Hello Alice! Your code is 123456."
	if res.Body != expectedBody {
		t.Errorf("expected rendered body %q, got %q", expectedBody, res.Body)
	}

	// 2. Test Slack Channel
	payload.Channel = "slack"
	payload.Target = "https://hooks.slack.com/services/123"
	body2, _ := json.Marshal(payload)
	resp2, _ := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body2))
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected StatusOK on Slack channel, got %d", resp2.StatusCode)
	}
}

func TestServMailRetriesAndDLQ(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mail/send", handleSend)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	payload := SendRequest{
		Channel:  "email",
		Target:   "fail-mailbox@example.com", // will trigger failures
		Template: "Hello",
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(testServer.URL+"/api/mail/send", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to post send request: %v", err)
	}
	defer resp.Body.Close()

	// Should be StatusAccepted (queued in DLQ) on persistent failure
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected StatusAccepted on DLQ fallback, got %d", resp.StatusCode)
	}

	var res SendResponse
	_ = json.NewDecoder(resp.Body).Decode(&res)

	if res.Status != "queued_in_dlq" {
		t.Errorf("expected queued_in_dlq status, got %q", res.Status)
	}
}
