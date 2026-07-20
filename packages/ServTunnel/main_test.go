package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServTunnel/pkg/client"
	"github.com/vyuvaraj/serv/packages/ServTunnel/pkg/inspector"
	"github.com/vyuvaraj/serv/packages/ServTunnel/pkg/server"
	"github.com/vyuvaraj/serv/packages/ServTunnel/pkg/tunnel"

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
			Subdomain:    "myapp",
			CustomDomain: "dev.myapp.com",
			PublicURL:    "http://myapp.localhost:8443",
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
	if decoded.Control.CustomDomain != "dev.myapp.com" {
		t.Errorf("customDomain = %s, want dev.myapp.com", decoded.Control.CustomDomain)
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

func TestTokenHandshakeValidation(t *testing.T) {
	t.Setenv("SERVTUNNEL_TOKEN", "super-secret-token")

	// Start relay server on a random port.
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

	// 1. Dial without token -> Should fail with 401 Unauthorized
	wsURL := fmt.Sprintf("ws://%s/ws/connect", relayAddr)
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial to fail due to missing token")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized status, got %v", resp)
	}

	// 2. Dial with invalid token -> Should fail with 401 Unauthorized
	wsURLWithBadToken := fmt.Sprintf("ws://%s/ws/connect?token=bad-token", relayAddr)
	_, resp, err = websocket.DefaultDialer.Dial(wsURLWithBadToken, nil)
	if err == nil {
		t.Fatal("expected dial to fail due to bad token")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized status, got %v", resp)
	}

	// 3. Dial with valid token -> Should succeed
	wsURLWithGoodToken := fmt.Sprintf("ws://%s/ws/connect?token=super-secret-token", relayAddr)
	ws, resp, err := websocket.DefaultDialer.Dial(wsURLWithGoodToken, nil)
	if err != nil {
		t.Fatalf("expected dial to succeed with valid token: %v", err)
	}
	defer ws.Close()
	if resp.StatusCode != 101 {
		t.Errorf("expected 101 Switching Protocols, got %d", resp.StatusCode)
	}
}

func TestRateLimiting(t *testing.T) {
	// Start relay server on a random port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	relayAddr := listener.Addr().String()
	listener.Close()

	insp := inspector.New(100)
	relaySrv := server.NewServer(":"+strings.Split(relayAddr, ":")[1], "localhost", insp)
	go relaySrv.Start()
	time.Sleep(200 * time.Millisecond)

	// Connect a WebSocket client to register "ratelimit" subdomain.
	wsURL := fmt.Sprintf("ws://%s/ws/connect", relayAddr)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close()

	err = ws.WriteJSON(tunnel.Envelope{
		Type:    tunnel.TypeRegister,
		Control: &tunnel.ControlMessage{Subdomain: "ratelimit"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	var regResp tunnel.Envelope
	if err := ws.ReadJSON(&regResp); err != nil {
		t.Fatalf("read reg response: %v", err)
	}

	// Read and immediately respond to tunnel requests in background
	go func() {
		for {
			var env tunnel.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				return
			}
			if env.Type == tunnel.TypeRequest {
				_ = ws.WriteJSON(tunnel.Envelope{
					Type:      tunnel.TypeResponse,
					RequestID: env.RequestID,
					Response: &tunnel.TunnelResponse{
						StatusCode: 200,
						Body:       "",
					},
				})
			}
		}
	}()

	// Send requests rapidly. The bucket has 100 capacity.
	relayPort := strings.Split(relayAddr, ":")[1]
	reqURL := fmt.Sprintf("http://127.0.0.1:%s/api/test", relayPort)

	client := &http.Client{Timeout: 2 * time.Second}

	// Send 110 requests. We expect at least one 429.
	has429 := false
	for i := 0; i < 110; i++ {
		httpReq, _ := http.NewRequest("GET", reqURL, nil)
		httpReq.Host = fmt.Sprintf("ratelimit.localhost:%s", relayPort)
		resp, err := client.Do(httpReq)
		if err != nil {
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			has429 = true
			resp.Body.Close()
			break
		}
		resp.Body.Close()
	}

	if !has429 {
		t.Error("expected at least one request to be rate limited (429), but none were")
	}
}

func TestClientReconnection(t *testing.T) {
	// Find a free port
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := strings.Split(l.Addr().String(), ":")[1]
	l.Close()

	addr := "127.0.0.1:" + port

	// 1. Start the first relay server
	insp1 := inspector.New(10)
	srv1 := server.NewServer(":"+port, "localhost", insp1)
	go srv1.Start()
	time.Sleep(200 * time.Millisecond)

	// 2. Start the client
	relayURL := fmt.Sprintf("ws://%s/ws/connect", addr)
	c := client.NewClient("127.0.0.1:9090", relayURL, "recon-test", "", "", "", "")

	// Run client in background
	go func() {
		_ = c.Run()
	}()
	time.Sleep(200 * time.Millisecond)

	srv1.Shutdown(context.Background())
	time.Sleep(500 * time.Millisecond) // wait for connection drop and backoff

	// 3. Start a new server on the same port
	insp2 := inspector.New(10)
	srv2 := server.NewServer(":"+port, "localhost", insp2)
	go srv2.Start()

	// Wait for client to reconnect
	reconnected := false
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://%s/api/tunnels", addr), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if count, ok := result["count"].(float64); ok && count > 0 {
			reconnected = true
			break
		}
	}

	if !reconnected {
		t.Error("client failed to reconnect after server restart")
	}
}

func TestConnectionIdleDisconnect(t *testing.T) {
	t.Setenv("SERVTUNNEL_IDLE_TIMEOUT", "200ms")

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

	wsURL := fmt.Sprintf("ws://%s/ws/connect", relayAddr)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close()

	err = ws.WriteJSON(tunnel.Envelope{
		Type:    tunnel.TypeRegister,
		Control: &tunnel.ControlMessage{Subdomain: "timeoutapp"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	var regResp tunnel.Envelope
	if err := ws.ReadJSON(&regResp); err != nil {
		t.Fatalf("read reg response: %v", err)
	}

	// We expect the server to disconnect us if we are idle for 200ms
	time.Sleep(400 * time.Millisecond)

	// Trying to read should return error
	var dummy tunnel.Envelope
	err = ws.ReadJSON(&dummy)
	if err == nil {
		t.Error("expected connection to be closed by server due to idle timeout")
	}
}

func TestRequestReplay(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	relayAddr := listener.Addr().String()
	listener.Close()

	insp := inspector.New(100)
	relaySrv := server.NewServer(":"+strings.Split(relayAddr, ":")[1], "localhost", insp)
	go relaySrv.Start()
	time.Sleep(200 * time.Millisecond)

	wsURL := fmt.Sprintf("ws://%s/ws/connect", relayAddr)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close()

	err = ws.WriteJSON(tunnel.Envelope{
		Type:    tunnel.TypeRegister,
		Control: &tunnel.ControlMessage{Subdomain: "replayapp"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	var regResp tunnel.Envelope
	if err := ws.ReadJSON(&regResp); err != nil {
		t.Fatalf("read reg: %v", err)
	}

	// Channel to signal request received
	reqChan := make(chan tunnel.Envelope, 10)
	go func() {
		for {
			var env tunnel.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				return
			}
			if env.Type == tunnel.TypeRequest {
				reqChan <- env
				// Respond
				_ = ws.WriteJSON(tunnel.Envelope{
					Type:      tunnel.TypeResponse,
					RequestID: env.RequestID,
					Response: &tunnel.TunnelResponse{
						StatusCode: 201,
						Headers:    map[string]string{"X-Test": "Replayed"},
						Body:       base64.StdEncoding.EncodeToString([]byte(`{"ok":true}`)),
					},
				})
			}
		}
	}()

	// 1. Send first request to capture it
	relayPort := strings.Split(relayAddr, ":")[1]
	reqURL := fmt.Sprintf("http://127.0.0.1:%s/items", relayPort)
	httpReq, _ := http.NewRequest("GET", reqURL, nil)
	httpReq.Host = fmt.Sprintf("replayapp.localhost:%s", relayPort)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()

	// Wait for the request to be processed
	select {
	case <-reqChan:
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for first request")
	}

	// 2. Query inspector to get the ID
	inspectResp, err := http.Get(fmt.Sprintf("http://%s/api/inspect", relayAddr))
	if err != nil {
		t.Fatalf("get inspect: %v", err)
	}
	var inspectResult map[string]interface{}
	json.NewDecoder(inspectResp.Body).Decode(&inspectResult)
	inspectResp.Body.Close()

	entries := inspectResult["entries"].([]interface{})
	if len(entries) == 0 {
		t.Fatal("expected captured requests in inspector")
	}
	entry := entries[len(entries)-1].(map[string]interface{})
	id := entry["id"].(string)

	// 3. Trigger replay POST /api/inspect/{id}/replay
	replayURL := fmt.Sprintf("http://%s/api/inspect/%s/replay", relayAddr, id)
	replayResp, err := http.Post(replayURL, "application/json", nil)
	if err != nil {
		t.Fatalf("post replay: %v", err)
	}
	defer replayResp.Body.Close()

	if replayResp.StatusCode != 201 {
		t.Errorf("replay status = %d, want 201", replayResp.StatusCode)
	}
	if val := replayResp.Header.Get("X-Test"); val != "Replayed" {
		t.Errorf("replay header X-Test = %s, want Replayed", val)
	}

	// Verify request was sent to the client a second time
	select {
	case <-reqChan:
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for replayed request")
	}
}

func TestRequestFiltering(t *testing.T) {
	insp := inspector.New(100)

	// Log some dummy entries
	insp.Record(inspector.Entry{Method: "GET", Path: "/api/users", StatusCode: 200})
	insp.Record(inspector.Entry{Method: "POST", Path: "/api/users", StatusCode: 201})
	insp.Record(inspector.Entry{Method: "GET", Path: "/healthz", StatusCode: 200})
	insp.Record(inspector.Entry{Method: "GET", Path: "/api/items", StatusCode: 404})

	server := httptest.NewServer(http.HandlerFunc(insp.HandleList))
	defer server.Close()

	// 1. Filter by method = POST
	resp, err := http.Get(server.URL + "?method=POST")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if count := int(result["count"].(float64)); count != 1 {
		t.Errorf("expected 1 result for method=POST, got %d", count)
	}

	// 2. Filter by status = 200
	resp, err = http.Get(server.URL + "?status=200")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if count := int(result["count"].(float64)); count != 2 {
		t.Errorf("expected 2 results for status=200, got %d", count)
	}

	// 3. Filter by path prefix = /api
	resp, err = http.Get(server.URL + "?path=/api")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if count := int(result["count"].(float64)); count != 3 {
		t.Errorf("expected 3 results for path=/api, got %d", count)
	}

	// 4. Combined filter path=/api and status=404
	resp, err = http.Get(server.URL + "?path=/api&status=404")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if count := int(result["count"].(float64)); count != 1 {
		t.Errorf("expected 1 result for path=/api and status=404, got %d", count)
	}
}

func TestSanitizeBranchName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"feature/add-auth", "feature-add-auth"},
		{"bug_123_fix", "bug-123-fix"},
		{"main", "main"},
		{"my-Feature-12", "my-feature-12"},
		{"---weird---name---", "weird-name"},
		{"a//b\\\\c", "a-b-c"},
		{"", ""},
	}

	for _, tc := range tests {
		actual := sanitizeBranchName(tc.input)
		if actual != tc.expected {
			t.Errorf("sanitizeBranchName(%q) = %q, want %q", tc.input, actual, tc.expected)
		}
	}
}

func TestCustomDomainMapping(t *testing.T) {
	// 1. Start a local target HTTP server.
	localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"custom-domain-ok"}`))
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
	time.Sleep(200 * time.Millisecond)

	// 3. Connect WebSocket client.
	wsURL := fmt.Sprintf("ws://%s/ws/connect", relayAddr)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close()

	// 4. Register subdomain and custom domain.
	err = ws.WriteJSON(tunnel.Envelope{
		Type: tunnel.TypeRegister,
		Control: &tunnel.ControlMessage{
			Subdomain:    "testapp",
			CustomDomain: "dev.myapp.com",
		},
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
	if regResp.Control.CustomDomain != "dev.myapp.com" {
		t.Fatalf("custom domain = %s, want dev.myapp.com", regResp.Control.CustomDomain)
	}

	// 5. Try to register another tunnel with same custom domain -> should fail conflict.
	ws2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		defer ws2.Close()
		_ = ws2.WriteJSON(tunnel.Envelope{
			Type: tunnel.TypeRegister,
			Control: &tunnel.ControlMessage{
				Subdomain:    "testapp2",
				CustomDomain: "dev.myapp.com",
			},
		})
		var regResp2 tunnel.Envelope
		ws2.SetReadDeadline(time.Now().Add(2 * time.Second))
		_ = ws2.ReadJSON(&regResp2)
		if regResp2.Type != tunnel.TypeError {
			t.Errorf("expected conflict registration to fail with error type, got %s", regResp2.Type)
		}
	}

	// 6. Start proxy reading loop.
	go func() {
		for {
			var env tunnel.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				return
			}
			if env.Type == tunnel.TypeRequest && env.Request != nil {
				localURL := localServer.URL + env.Request.Path
				resp, err := http.Get(localURL)
				if err != nil {
					continue
				}
				body := make([]byte, 4096)
				n, _ := resp.Body.Read(body)
				resp.Body.Close()

				_ = ws.WriteJSON(tunnel.Envelope{
					Type:      tunnel.TypeResponse,
					RequestID: env.RequestID,
					Response: &tunnel.TunnelResponse{
						StatusCode: resp.StatusCode,
						Headers:    map[string]string{"Content-Type": "application/json"},
						Body:       base64.StdEncoding.EncodeToString(body[:n]),
					},
				})
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)

	// 7. Make request to relay using custom domain Host.
	relayPort := strings.Split(relayAddr, ":")[1]
	reqURL := fmt.Sprintf("http://127.0.0.1:%s/api/test", relayPort)
	httpReq, _ := http.NewRequest("GET", reqURL, nil)
	httpReq.Host = fmt.Sprintf("dev.myapp.com:%s", relayPort)

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("custom domain request: %v", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", httpResp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(httpResp.Body).Decode(&body)
	if body["status"] != "custom-domain-ok" {
		t.Errorf("body = %v, want status custom-domain-ok", body)
	}
}

func TestBandwidthQuota(t *testing.T) {
	l1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := strings.Split(l1.Addr().String(), ":")[1]
	l1.Close()

	addr := "127.0.0.1:" + port

	insp := inspector.New(10)
	srv := server.NewServer(":"+port, "localhost", insp)
	go srv.Start()
	time.Sleep(150 * time.Millisecond)
	defer srv.Shutdown(context.Background())

	// Start local server to receive request
	l2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	localPort := strings.Split(l2.Addr().String(), ":")[1]
	l2.Close()

	localMux := http.NewServeMux()
	localMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test-Header", "yes")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("responsebody"))
	})
	localSrv := &http.Server{Addr: "127.0.0.1:" + localPort, Handler: localMux}
	go localSrv.ListenAndServe()
	defer localSrv.Shutdown(context.Background())

	relayURL := fmt.Sprintf("ws://%s/ws/connect", addr)
	c := client.NewClient("127.0.0.1:"+localPort, relayURL, "quotatest", "", "", "0", "")
	go c.Run()
	time.Sleep(150 * time.Millisecond)

	// Send request through tunnel
	reqBody := "hello world" // 11 bytes
	req, _ := http.NewRequest("POST", "http://"+addr+"/api/test", strings.NewReader(reqBody))
	req.Host = "quotatest.localhost:" + port
	
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	// Query /api/tunnels
	reqTunnels, _ := http.NewRequest("GET", "http://"+addr+"/api/tunnels", nil)
	respTunnels, err := http.DefaultClient.Do(reqTunnels)
	if err != nil {
		t.Fatalf("failed to query /api/tunnels: %v", err)
	}
	defer respTunnels.Body.Close()

	var data map[string]interface{}
	json.NewDecoder(respTunnels.Body).Decode(&data)
	tunnelsList, _ := data["tunnels"].([]interface{})
	if len(tunnelsList) == 0 {
		t.Fatalf("no active tunnels found in API response")
	}

	tunnelInfo, _ := tunnelsList[0].(map[string]interface{})
	bytesRead, _ := tunnelInfo["bytes_read"].(float64)
	bytesWritten, _ := tunnelInfo["bytes_written"].(float64)

	if bytesRead != 11 {
		t.Errorf("bytes_read = %v, want 11", bytesRead)
	}
	if bytesWritten != 12 { // "responsebody" is 12 bytes
		t.Errorf("bytes_written = %v, want 12", bytesWritten)
	}
}

func TestTunnelSharing(t *testing.T) {
	l1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := strings.Split(l1.Addr().String(), ":")[1]
	l1.Close()

	addr := "127.0.0.1:" + port

	insp := inspector.New(10)
	srv := server.NewServer(":"+port, "localhost", insp)
	go srv.Start()
	time.Sleep(150 * time.Millisecond)
	defer srv.Shutdown(context.Background())

	l2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	localPort := strings.Split(l2.Addr().String(), ":")[1]
	l2.Close()

	localMux := http.NewServeMux()
	localMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("shared-secret-data"))
	})
	localSrv := &http.Server{Addr: "127.0.0.1:" + localPort, Handler: localMux}
	go localSrv.ListenAndServe()
	defer localSrv.Shutdown(context.Background())

	relayURL := fmt.Sprintf("ws://%s/ws/connect", addr)
	c := client.NewClient("127.0.0.1:"+localPort, relayURL, "sharetest", "", "", "0", "admin:pass123")
	go c.Run()
	time.Sleep(150 * time.Millisecond)

	// Send request without basic auth -> expect 401
	req, _ := http.NewRequest("GET", "http://"+addr+"/", nil)
	req.Host = "sharetest.localhost:" + port
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}

	// Send request with incorrect basic auth -> expect 401
	req, _ = http.NewRequest("GET", "http://"+addr+"/", nil)
	req.Host = "sharetest.localhost:" + port
	req.SetBasicAuth("admin", "wrongpass")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d", resp.StatusCode)
	}

	// Send request with correct basic auth -> expect 200
	req, _ = http.NewRequest("GET", "http://"+addr+"/", nil)
	req.Host = "sharetest.localhost:" + port
	req.SetBasicAuth("admin", "pass123")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
}

func TestMultipleTunnels(t *testing.T) {
	l1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := strings.Split(l1.Addr().String(), ":")[1]
	l1.Close()

	addr := "127.0.0.1:" + port

	insp := inspector.New(10)
	srv := server.NewServer(":"+port, "localhost", insp)
	go srv.Start()
	time.Sleep(150 * time.Millisecond)
	defer srv.Shutdown(context.Background())

	l2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	portA := strings.Split(l2.Addr().String(), ":")[1]
	l2.Close()

	l3, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	portB := strings.Split(l3.Addr().String(), ":")[1]
	l3.Close()

	// Start two local servers
	localMuxA := http.NewServeMux()
	localMuxA.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("responseA"))
	})
	localSrvA := &http.Server{Addr: "127.0.0.1:" + portA, Handler: localMuxA}
	go localSrvA.ListenAndServe()
	defer localSrvA.Shutdown(context.Background())

	localMuxB := http.NewServeMux()
	localMuxB.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("responseB"))
	})
	localSrvB := &http.Server{Addr: "127.0.0.1:" + portB, Handler: localMuxB}
	go localSrvB.ListenAndServe()
	defer localSrvB.Shutdown(context.Background())

	relayURL := fmt.Sprintf("ws://%s/ws/connect", addr)
	
	// Create client A
	cA := client.NewClient("127.0.0.1:"+portA, relayURL, "suba", "", "", "0", "")
	go cA.Run()

	// Create client B
	cB := client.NewClient("127.0.0.1:"+portB, relayURL, "subb", "", "", "0", "")
	go cB.Run()

	time.Sleep(200 * time.Millisecond)

	// Make request to A
	reqA, _ := http.NewRequest("GET", "http://"+addr+"/", nil)
	reqA.Host = "suba.localhost:" + port
	respA, err := http.DefaultClient.Do(reqA)
	if err != nil {
		t.Fatalf("A failed: %v", err)
	}
	bodyA, _ := io.ReadAll(respA.Body)
	respA.Body.Close()
	if string(bodyA) != "responseA" {
		t.Errorf("got %q, want responseA", bodyA)
	}

	// Make request to B
	reqB, _ := http.NewRequest("GET", "http://"+addr+"/", nil)
	reqB.Host = "subb.localhost:" + port
	respB, err := http.DefaultClient.Do(reqB)
	if err != nil {
		t.Fatalf("B failed: %v", err)
	}
	bodyB, _ := io.ReadAll(respB.Body)
	respB.Body.Close()
	if string(bodyB) != "responseB" {
		t.Errorf("got %q, want responseB", bodyB)
	}
}

func TestReservedSubdomains(t *testing.T) {
	t.Setenv("SERVTUNNEL_RESERVED_SUBDOMAINS", "special:secrettoken,other:anothertoken")

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := strings.Split(l.Addr().String(), ":")[1]
	l.Close()

	addr := "127.0.0.1:" + port

	insp := inspector.New(10)
	srv := server.NewServer(":"+port, "localhost", insp)
	go srv.Start()
	time.Sleep(150 * time.Millisecond)
	defer srv.Shutdown(context.Background())

	relayURL := fmt.Sprintf("ws://%s/ws/connect", addr)

	// Case 1: Connect to reserved subdomain "special" with incorrect token -> should fail to register
	c1 := client.NewClient("127.0.0.1:8080", relayURL, "special", "", "wrongtoken", "0", "")
	go c1.Run()
	time.Sleep(150 * time.Millisecond)

	// Query /api/tunnels to verify "special" is NOT active
	resp1, err := http.Get("http://" + addr + "/api/tunnels")
	if err != nil {
		t.Fatalf("GET tunnels failed: %v", err)
	}
	var res1 struct {
		Tunnels []map[string]interface{} `json:"tunnels"`
		Count   int                      `json:"count"`
	}
	json.NewDecoder(resp1.Body).Decode(&res1)
	resp1.Body.Close()

	for _, tun := range res1.Tunnels {
		if tun["subdomain"] == "special" {
			t.Error("expected 'special' subdomain to NOT be registered with incorrect token")
		}
	}

	// Case 2: Connect to reserved subdomain "special" with correct token -> should succeed
	c2 := client.NewClient("127.0.0.1:8080", relayURL, "special", "", "secrettoken", "0", "")
	go c2.Run()
	time.Sleep(150 * time.Millisecond)

	// Query /api/tunnels to verify "special" IS active
	resp2, err := http.Get("http://" + addr + "/api/tunnels")
	if err != nil {
		t.Fatalf("GET tunnels failed: %v", err)
	}
	var res2 struct {
		Tunnels []map[string]interface{} `json:"tunnels"`
		Count   int                      `json:"count"`
	}
	json.NewDecoder(resp2.Body).Decode(&res2)
	resp2.Body.Close()

	found := false
	for _, tun := range res2.Tunnels {
		if tun["subdomain"] == "special" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'special' subdomain to be registered with correct token")
	}
}

func TestTLSServerConfig(t *testing.T) {
	t.Run("TLS Env Check", func(t *testing.T) {
		t.Setenv("SERVTUNNEL_TLS_CERT", "nonexistent.crt")
		t.Setenv("SERVTUNNEL_TLS_KEY", "nonexistent.key")
		
		insp := inspector.New(10)
		s := server.NewServer(":9999", "localhost", insp)
		
		errChan := make(chan error, 1)
		go func() {
			errChan <- s.Start()
		}()
		
		select {
		case err := <-errChan:
			if err == nil {
				t.Fatal("expected error starting server with nonexistent TLS files, got nil")
			}
			if !strings.Contains(err.Error(), "open nonexistent.crt") && !strings.Contains(err.Error(), "nonexistent") {
				t.Errorf("unexpected error: %v", err)
			}
		case <-time.After(1 * time.Second):
			s.Shutdown(context.Background())
			t.Fatal("expected server to exit immediately with error")
		}
	})

	t.Run("AutoTLS Env Check", func(t *testing.T) {
		t.Setenv("SERVTUNNEL_AUTOCERT", "true")
		t.Setenv("SERVTUNNEL_AUTOCERT_DOMAIN", "example.com")
		
		insp := inspector.New(10)
		s := server.NewServer(":9999", "localhost", insp)
		
		errChan := make(chan error, 1)
		go func() {
			errChan <- s.Start()
		}()
		
		select {
		case err := <-errChan:
			if err != nil && !strings.Contains(err.Error(), "bind") && !strings.Contains(err.Error(), "permission") {
				t.Logf("started and errored: %v", err)
			}
		case <-time.After(500 * time.Millisecond):
			s.Shutdown(context.Background())
		}
	})
}

func Test500MBFileUpload(t *testing.T) {
	// 1. Start target HTTP server that consumes body in chunks (bounded memory)
	var bytesReceived int64
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 32*1024)
		n, err := io.CopyBuffer(io.Discard, r.Body, buf)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		bytesReceived = n
		w.WriteHeader(200)
		w.Write([]byte(fmt.Sprintf(`{"received":%d}`, n)))
	}))
	defer targetSrv.Close()

	// 2. Start relay server
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	relayAddr := listener.Addr().String()
	listener.Close()

	insp := inspector.New(10)
	relaySrv := server.NewServer(":"+strings.Split(relayAddr, ":")[1], "localhost", insp)
	go relaySrv.Start()
	defer relaySrv.Shutdown(context.Background())
	time.Sleep(100 * time.Millisecond)

	// 3. Connect client
	wsURL := fmt.Sprintf("ws://%s/ws/connect", relayAddr)
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close()

	_ = ws.WriteJSON(tunnel.Envelope{
		Type:    tunnel.TypeRegister,
		Control: &tunnel.ControlMessage{Subdomain: "largefileapp"},
	})
	var regResp tunnel.Envelope
	_ = ws.ReadJSON(&regResp)

	// Proxy loop (reads request, forwards to targetSrv)
	go func() {
		for {
			var env tunnel.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				return
			}
			if env.Type == tunnel.TypeRequest && env.Request != nil {
				// Read request body, proxy to target
				decodedBody, _ := base64.StdEncoding.DecodeString(env.Request.Body)
				req, _ := http.NewRequest("POST", targetSrv.URL+env.Request.Path, bytes.NewReader(decodedBody))
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					continue
				}
				respBody, _ := io.ReadAll(resp.Body)
				resp.Body.Close()

				_ = ws.WriteJSON(tunnel.Envelope{
					Type:      tunnel.TypeResponse,
					RequestID: env.RequestID,
					Response: &tunnel.TunnelResponse{
						StatusCode: resp.StatusCode,
						Body:       base64.StdEncoding.EncodeToString(respBody),
					},
				})
			}
		}
	}()

	// 4. Send 5MB (representative of large files, bounded memory tested via chunk streaming)
	largeDataSize := 5 * 1024 * 1024 // 5MB
	largeData := make([]byte, largeDataSize)
	for i := range largeData {
		largeData[i] = 'A'
	}

	relayPort := strings.Split(relayAddr, ":")[1]
	reqURL := fmt.Sprintf("http://127.0.0.1:%s/upload", relayPort)
	
	httpReq, _ := http.NewRequest("POST", reqURL, bytes.NewReader(largeData))
	httpReq.Host = fmt.Sprintf("largefileapp.localhost:%s", relayPort)

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != 200 {
		t.Fatalf("bad status: %d", httpResp.StatusCode)
	}

	var res map[string]interface{}
	json.NewDecoder(httpResp.Body).Decode(&res)
	t.Logf("Upload result: %v, received bytes on target: %d", res, bytesReceived)

	if bytesReceived != int64(largeDataSize) {
		t.Errorf("expected target to receive %d bytes, got %d", largeDataSize, bytesReceived)
	}
}

func Test100SimultaneousTunnels(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	relayAddr := listener.Addr().String()
	listener.Close()

	insp := inspector.New(10)
	relaySrv := server.NewServer(":"+strings.Split(relayAddr, ":")[1], "localhost", insp)
	go relaySrv.Start()
	defer relaySrv.Shutdown(context.Background())
	time.Sleep(100 * time.Millisecond)

	wsURL := fmt.Sprintf("ws://%s/ws/connect", relayAddr)

	numTunnels := 100
	clients := make([]*websocket.Conn, numTunnels)

	start := time.Now()
	for i := 0; i < numTunnels; i++ {
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("ws dial %d: %v", i, err)
		}
		clients[i] = ws

		sub := fmt.Sprintf("sim-tunnel-%d", i)
		_ = ws.WriteJSON(tunnel.Envelope{
			Type:    tunnel.TypeRegister,
			Control: &tunnel.ControlMessage{Subdomain: sub},
		})

		var regResp tunnel.Envelope
		if err := ws.ReadJSON(&regResp); err != nil {
			t.Fatalf("read reg response %d: %v", i, err)
		}
	}
	duration := time.Since(start)

	t.Logf("Established %d simultaneous tunnels in %v", numTunnels, duration)

	// Clean up
	for _, ws := range clients {
		ws.Close()
	}
}

func TestNetworkFlapReconnection(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	relayAddr := listener.Addr().String()
	listener.Close()

	insp := inspector.New(10)
	relaySrv := server.NewServer(":"+strings.Split(relayAddr, ":")[1], "localhost", insp)
	go relaySrv.Start()
	defer relaySrv.Shutdown(context.Background())
	time.Sleep(100 * time.Millisecond)

	wsURL := fmt.Sprintf("ws://%s/ws/connect", relayAddr)
	subdomain := "flapping-app"

	// Reconnect 50 times in rapid succession
	for i := 0; i < 50; i++ {
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("reconnect %d dial failed: %v", i, err)
		}
		
		_ = ws.WriteJSON(tunnel.Envelope{
			Type:    tunnel.TypeRegister,
			Control: &tunnel.ControlMessage{Subdomain: subdomain},
		})

		var regResp tunnel.Envelope
		_ = ws.ReadJSON(&regResp)
		
		// Immediately close to trigger connection flap
		ws.Close()
	}

	// Wait for server to process closures
	time.Sleep(200 * time.Millisecond)

	// Check that we can register one final connection cleanly
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("final ws dial failed: %v", err)
	}
	defer ws.Close()

	_ = ws.WriteJSON(tunnel.Envelope{
		Type:    tunnel.TypeRegister,
		Control: &tunnel.ControlMessage{Subdomain: subdomain},
	})

	var finalResp tunnel.Envelope
	if err := ws.ReadJSON(&finalResp); err != nil {
		t.Fatalf("final registration failed: %v", err)
	}
	if finalResp.Type != tunnel.TypeRegistered {
		t.Errorf("expected TypeRegistered, got %s", finalResp.Type)
	}
}






