package proxy

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSocketMetricsFeed(t *testing.T) {
	// Initialize GatewayHandler with dummy routes
	routes := []Route{
		{Prefix: "/api", Target: "http://localhost:9090"},
	}
	handler := NewGatewayHandler(routes, nil, "secret-token")

	// Set up httptest server with a handler wrapping HandleWebSocketMetrics
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth token from query params or header
		token := r.Header.Get("Authorization")
		token = strings.TrimPrefix(token, "Bearer ")
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token != "secret-token" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		HandleWebSocketMetrics(w, r, handler)
	}))
	defer srv.Close()

	// Parse server address
	addr := srv.Listener.Addr().String()

	// 1. Dial TCP
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial test server: %v", err)
	}
	defer conn.Close()

	// 2. Send handshake with correct token
	secKey := "dGhlIHNhbXBsZSBub25jZQ=="
	req := fmt.Sprintf(
		"GET /?token=secret-token HTTP/1.1\r\n"+
			"Host: %s\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\n"+
			"Sec-WebSocket-Version: 13\r\n\r\n",
		addr, secKey,
	)

	_, err = conn.Write([]byte(req))
	if err != nil {
		t.Fatalf("failed to write handshake request: %v", err)
	}

	// 3. Read response
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("failed to read handshake response: %v", err)
	}
	resp := string(buf[:n])

	if !strings.Contains(resp, "101 Switching Protocols") {
		t.Fatalf("expected 101 Switching Protocols, got: %s", resp)
	}

	// Verify Sec-WebSocket-Accept header
	hash := sha1.New()
	hash.Write([]byte(secKey + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	expectedAccept := base64.StdEncoding.EncodeToString(hash.Sum(nil))
	if !strings.Contains(resp, "Sec-WebSocket-Accept: "+expectedAccept) {
		t.Fatalf("expected Sec-WebSocket-Accept: %s, got: %s", expectedAccept, resp)
	}

	// Helper to read a WebSocket text frame
	readFrame := func() ([]byte, error) {
		header := make([]byte, 2)
		_, err := conn.Read(header)
		if err != nil {
			return nil, err
		}
		if header[0]&0x0F != 1 {
			return nil, fmt.Errorf("unexpected opcode: %d", header[0]&0x0F)
		}
		length := int(header[1] & 0x7F)
		if length == 126 {
			lenBytes := make([]byte, 2)
			_, err = conn.Read(lenBytes)
			if err != nil {
				return nil, err
			}
			length = int(lenBytes[0])<<8 | int(lenBytes[1])
		} else if length == 127 {
			return nil, fmt.Errorf("large payloads not supported in test")
		}

		payload := make([]byte, length)
		_, err = conn.Read(payload)
		return payload, err
	}

	// Read first frame (connection_established)
	payload, err := readFrame()
	if err != nil {
		t.Fatalf("failed to read connection message: %v", err)
	}
	var initMsg map[string]interface{}
	if err := json.Unmarshal(payload, &initMsg); err != nil {
		t.Fatalf("failed to unmarshal init message: %v", err)
	}
	if initMsg["type"] != "connection_established" {
		t.Fatalf("expected connection_established, got: %v", initMsg)
	}

	// Simulate requests
	handler.metricsTracker.IncRequest()
	handler.metricsTracker.IncRequest()
	handler.metricsTracker.IncError()

	// Wait for ticker (or force tick)
	handler.metricsTracker.Tick()

	// Read next frame (metrics snapshot)
	payload, err = readFrame()
	if err != nil {
		t.Fatalf("failed to read metrics frame: %v", err)
	}

	var snap GatewayMetricsSnapshot
	if err := json.Unmarshal(payload, &snap); err != nil {
		t.Fatalf("failed to unmarshal snapshot: %v", err)
	}

	if snap.TotalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", snap.TotalRequests)
	}
	if snap.TotalErrors != 1 {
		t.Errorf("expected 1 error, got %d", snap.TotalErrors)
	}
	if snap.RequestRate < 0 {
		t.Errorf("expected non-negative request rate, got %f", snap.RequestRate)
	}
	if snap.ErrorRate < 0 {
		t.Errorf("expected non-negative error rate, got %f", snap.ErrorRate)
	}
}
