package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"servtunnel/pkg/inspector"
	"servtunnel/pkg/server"
	"servtunnel/pkg/tunnel"

	"github.com/gorilla/websocket"
)

// ---------- Protocol Tests ----------

func TestEnvelopeSerialization(t *testing.T) {
	env := tunnel.Envelope{
		Type:      tunnel.TypeRequest,
		RequestID: "test-123",
		Request: &tunnel.TunnelRequest{
			Method:  "GET",
			Path:    "/users",
			Headers: map[string]string{"Accept": "application/json"},
		},
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded tunnel.Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Type != tunnel.TypeRequest {
		t.Errorf("type = %s, want %s", decoded.Type, tunnel.TypeRequest)
	}
	if decoded.RequestID != "test-123" {
		t.Errorf("requestID = %s, want test-123", decoded.RequestID)
	}
	if decoded.Request == nil {
		t.Fatal("request is nil")
	}
	if decoded.Request.Method != "GET" {
		t.Errorf("method = %s, want GET", decoded.Request.Method)
	}
	if decoded.Request.Path != "/users" {
		t.Errorf("path = %s, want /users", decoded.Request.Path)
	}
}

func TestControlMessageSerialization(t *testing.T) {
	env := tunnel.Envelope{
		Type: tunnel.TypeRegistered,
		Control: &tunnel.ControlMessage{
			Subdomain: "myapp",
			PublicURL: "http://myapp.localhost:8443",
		},
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded tunnel.Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Control == nil {
		t.Fatal("control is nil")
	}
	if decoded.Control.Subdomain != "myapp" {
		t.Errorf("subdomain = %s, want myapp", decoded.Control.Subdomain)
	}
}

func TestResponseSerialization(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte(`{"status":"ok"}`))
	env := tunnel.Envelope{
		Type:      tunnel.TypeResponse,
		RequestID: "req-1",
		Response: &tunnel.TunnelResponse{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       body,
		},
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded tunnel.Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Response == nil {
		t.Fatal("response is nil")
	}
	if decoded.Response.StatusCode != 200 {
		t.Errorf("status = %d, want 200", decoded.Response.StatusCode)
	}

	respBody, _ := base64.StdEncoding.DecodeString(decoded.Response.Body)
	if string(respBody) != `{"status":"ok"}` {
		t.Errorf("body = %s, want {\"status\":\"ok\"}", string(respBody))
	}
}

// ---------- Inspector Tests ----------

func TestInspectorRingBuffer(t *testing.T) {
	insp := inspector.New(3)

	for i := 0; i < 5; i++ {
		insp.Record(inspector.Entry{
			Method: "GET",
			Path:   fmt.Sprintf("/path-%d", i),
		})
	}

	entries := insp.List()
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}

	// Should contain the last 3 entries.
	if entries[0].Path != "/path-2" {
		t.Errorf("entries[0].Path = %s, want /path-2", entries[0].Path)
	}
	if entries[2].Path != "/path-4" {
		t.Errorf("entries[2].Path = %s, want /path-4", entries[2].Path)
	}

	if insp.Count() != 5 {
		t.Errorf("count = %d, want 5", insp.Count())
	}
}

func TestInspectorGetByID(t *testing.T) {
	insp := inspector.New(10)

	insp.Record(inspector.Entry{Method: "POST", Path: "/users"})
	insp.Record(inspector.Entry{Method: "GET", Path: "/health"})

	entry, ok := insp.Get("req-1")
	if !ok {
		t.Fatal("expected entry req-1 to exist")
	}
	if entry.Method != "POST" {
		t.Errorf("method = %s, want POST", entry.Method)
	}

	_, ok = insp.Get("req-999")
	if ok {
		t.Error("expected req-999 to not exist")
	}
}

func TestInspectorHTTPList(t *testing.T) {
	insp := inspector.New(10)
	insp.Record(inspector.Entry{Method: "GET", Path: "/a"})
	insp.Record(inspector.Entry{Method: "POST", Path: "/b"})

	req := httptest.NewRequest("GET", "/api/inspect", nil)
	w := httptest.NewRecorder()
	insp.HandleList(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)

	count := int(result["count"].(float64))
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

// ---------- Integration Tests ----------

func TestEndToEndTunnel(t *testing.T) {
	// 1. Start a local "target" HTTP server.
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom", "test-value")
		w.WriteHeader(200)
		w.Write([]byte(`{"hello":"world"}`))
	}))
	defer localServer.Close()

	// 2. Start the relay server on a random port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	relayAddr := listener.Addr().String()
	listener.Close()

	insp := inspector.New(100)
	relaySrv := server.NewServer(":"+strings.Split(relayAddr, ":")[1], "localhost", insp)
	go relaySrv.Start()
	time.Sleep(200 * time.Millisecond) // wait for relay to start

	// 3. Connect a WebSocket client (simulating the tunnel client).
	wsURL := fmt.Sprintf("ws://%s/ws/connect", relayAddr)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close()

	// 4. Register a subdomain.
	err = ws.WriteJSON(tunnel.Envelope{
		Type:    tunnel.TypeRegister,
		Control: &tunnel.ControlMessage{Subdomain: "testapp"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	var regResp tunnel.Envelope
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := ws.ReadJSON(&regResp); err != nil {
		t.Fatalf("read reg response: %v", err)
	}
	ws.SetReadDeadline(time.Time{})

	if regResp.Type != tunnel.TypeRegistered {
		t.Fatalf("expected registered, got %s", regResp.Type)
	}
	if regResp.Control.Subdomain != "testapp" {
		t.Fatalf("subdomain = %s, want testapp", regResp.Control.Subdomain)
	}

	// 5. Start reading tunnel requests in background and proxy to local server.
	go func() {
		for {
			var env tunnel.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				return
			}
			if env.Type == tunnel.TypeRequest && env.Request != nil {
				// Proxy to local server.
				localURL := localServer.URL + env.Request.Path
				resp, err := http.Get(localURL)
				if err != nil {
					continue
				}
				body := make([]byte, 4096)
				n, _ := resp.Body.Read(body)
				resp.Body.Close()

				respHeaders := make(map[string]string)
				for k, vals := range resp.Header {
					respHeaders[k] = vals[0]
				}

				_ = ws.WriteJSON(tunnel.Envelope{
					Type:      tunnel.TypeResponse,
					RequestID: env.RequestID,
					Response: &tunnel.TunnelResponse{
						StatusCode: resp.StatusCode,
						Headers:    respHeaders,
						Body:       base64.StdEncoding.EncodeToString(body[:n]),
					},
				})
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)

	// 6. Send an HTTP request to the relay with the subdomain Host header.
	relayPort := strings.Split(relayAddr, ":")[1]
	reqURL := fmt.Sprintf("http://127.0.0.1:%s/api/test", relayPort)
	httpReq, _ := http.NewRequest("GET", reqURL, nil)
	httpReq.Host = fmt.Sprintf("testapp.localhost:%s", relayPort)

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("tunnel request: %v", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", httpResp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(httpResp.Body).Decode(&body)
	if body["hello"] != "world" {
		t.Errorf("body = %v, want {\"hello\":\"world\"}", body)
	}

	// 7. Verify request was captured by inspector.
	time.Sleep(100 * time.Millisecond)
	entries := insp.List()
	if len(entries) < 1 {
		t.Error("expected at least 1 inspector entry")
	}
}

func TestTunnelNotFound(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	relayAddr := listener.Addr().String()
	listener.Close()

	insp := inspector.New(10)
	relaySrv := server.NewServer(":"+strings.Split(relayAddr, ":")[1], "localhost", insp)
	go relaySrv.Start()
	time.Sleep(200 * time.Millisecond)

	relayPort := strings.Split(relayAddr, ":")[1]
	reqURL := fmt.Sprintf("http://127.0.0.1:%s/test", relayPort)
	httpReq, _ := http.NewRequest("GET", reqURL, nil)
	httpReq.Host = fmt.Sprintf("nonexistent.localhost:%s", relayPort)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

func TestHealthEndpoints(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	relayAddr := listener.Addr().String()
	listener.Close()

	insp := inspector.New(10)
	relaySrv := server.NewServer(":"+strings.Split(relayAddr, ":")[1], "localhost", insp)
	go relaySrv.Start()
	time.Sleep(200 * time.Millisecond)

	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(fmt.Sprintf("http://%s%s", relayAddr, path))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("%s status = %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestListTunnelsEmpty(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	relayAddr := listener.Addr().String()
	listener.Close()

	insp := inspector.New(10)
	relaySrv := server.NewServer(":"+strings.Split(relayAddr, ":")[1], "localhost", insp)
	go relaySrv.Start()
	time.Sleep(200 * time.Millisecond)

	resp, err := http.Get(fmt.Sprintf("http://%s/api/tunnels", relayAddr))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	count := int(result["count"].(float64))
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}
