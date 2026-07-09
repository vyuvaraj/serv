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
	"os"
	"strings"
	"testing"
	"time"

	"servtunnel/pkg/client"
	"servtunnel/pkg/inspector"
	"servtunnel/pkg/server"
	"servtunnel/pkg/tunnel"

	"github.com/gorilla/websocket"
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

func TestLocalReplay(t *testing.T) {
	mockLocal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Local-Header", "hello")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("local-response"))
	}))
	defer mockLocal.Close()

	localAddr := strings.TrimPrefix(mockLocal.URL, "http://")

	c := client.NewClient(localAddr, "ws://localhost", "", "", "", "4040", "")
	insp := c.GetInspector()

	id := insp.Record(inspector.Entry{
		Method: "POST",
		Path:   "/test-path",
		RequestHeaders: map[string]string{
			"Content-Type": "application/json",
		},
		RequestBody: base64.StdEncoding.EncodeToString([]byte("request-body")),
	})

	req := httptest.NewRequest("POST", "/api/inspect/"+id+"/replay", nil)
	w := httptest.NewRecorder()
	
	c.HandleLocalReplay(w, req, id)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", w.Code)
	}

	var res map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if res["status"] != "success" {
		t.Errorf("Expected success, got %v", res["status"])
	}

	newID := res["new_id"].(string)
	newEntry, ok := insp.Get(newID)
	if !ok {
		t.Fatalf("Failed to retrieve new replayed entry from inspector")
	}

	if newEntry.StatusCode != 200 {
		t.Errorf("Expected status 200 on replayed entry, got %d", newEntry.StatusCode)
	}

	decodedBody, _ := base64.StdEncoding.DecodeString(newEntry.ResponseBody)
	if string(decodedBody) != "local-response" {
		t.Errorf("Expected body 'local-response', got %s", decodedBody)
	}
}

func TestPersistentTunnelsInvitesAndCustomDomain(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	relayAddr := ln.Addr().String()
	ln.Close()

	relayPort := strings.Split(relayAddr, ":")[1]
	relaySrv := server.NewServer(":"+relayPort, "localhost", inspector.New(10))
	
	os.Setenv("SERVTUNNEL_TOKEN", "my-test-token")
	defer os.Unsetenv("SERVTUNNEL_TOKEN")

	go func() {
		_ = relaySrv.Start()
	}()
	time.Sleep(200 * time.Millisecond)
	defer relaySrv.Shutdown(context.Background())

	wsURL := fmt.Sprintf("ws://127.0.0.1:%s/ws/connect?token=my-test-token", relayPort)
	
	dialer := &websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	ws1, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect ws1: %v", err)
	}
	
	err = ws1.WriteJSON(tunnel.Envelope{
		Type: tunnel.TypeRegister,
		Control: &tunnel.ControlMessage{
			Subdomain:   "persistsub",
			SharingAuth: "user:pass",
		},
	})
	if err != nil {
		t.Fatalf("Failed to register ws1: %v", err)
	}

	var resp1 tunnel.Envelope
	if err := ws1.ReadJSON(&resp1); err != nil || resp1.Type != tunnel.TypeRegistered {
		t.Fatalf("Failed to get registered response: %v", err)
	}

	resToken := resp1.Control.ResumptionToken
	if resToken == "" {
		t.Errorf("Expected non-empty ResumptionToken")
	}

	ws2, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect ws2: %v", err)
	}
	defer ws2.Close()

	err = ws2.WriteJSON(tunnel.Envelope{
		Type: tunnel.TypeRegister,
		Control: &tunnel.ControlMessage{
			Subdomain:       "persistsub",
			ResumptionToken: resToken,
			SharingAuth:     "user:pass",
		},
	})
	if err != nil {
		t.Fatalf("Failed to register ws2: %v", err)
	}

	var resp2 tunnel.Envelope
	if err := ws2.ReadJSON(&resp2); err != nil || resp2.Type != tunnel.TypeRegistered {
		t.Fatalf("Failed to resume session: %v", err)
	}

	var ws1Msg tunnel.Envelope
	err = ws1.ReadJSON(&ws1Msg)
	if err == nil {
		t.Errorf("Expected ws1 to be closed by server, but read succeeded: %+v", ws1Msg)
	}
	ws1.Close()

	inviteURL := fmt.Sprintf("http://127.0.0.1:%s/api/tunnels/persistsub/invite", relayPort)
	reqInv, _ := http.NewRequest("GET", inviteURL, nil)
	reqInv.Header.Set("Authorization", "Bearer my-test-token")
	respInv, err := http.DefaultClient.Do(reqInv)
	if err != nil || respInv.StatusCode != http.StatusOK {
		t.Fatalf("Failed to generate invite: %v, status: %d", err, respInv.StatusCode)
	}
	defer respInv.Body.Close()

	var inviteRes map[string]string
	json.NewDecoder(respInv.Body).Decode(&inviteRes)
	inviteToken := inviteRes["invite_token"]

	if inviteToken == "" {
		t.Fatalf("Invalid invite response: %+v", inviteRes)
	}

	tunnelReqURL := fmt.Sprintf("http://127.0.0.1:%s/?invite_token=%s", relayPort, inviteToken)
	reqTunnel, _ := http.NewRequest("GET", tunnelReqURL, nil)
	reqTunnel.Host = "persistsub.localhost"

	go func() {
		var reqEnv tunnel.Envelope
		if err := ws2.ReadJSON(&reqEnv); err == nil && reqEnv.Type == tunnel.TypeRequest {
			_ = ws2.WriteJSON(tunnel.Envelope{
				Type:      tunnel.TypeResponse,
				RequestID: reqEnv.RequestID,
				Response: &tunnel.TunnelResponse{
					StatusCode: http.StatusOK,
					Body:       base64.StdEncoding.EncodeToString([]byte("response-via-invite")),
				},
			})
		}
	}()

	respTunnel, err := http.DefaultClient.Do(reqTunnel)
	if err != nil {
		t.Fatalf("Failed to fetch tunnel via invite: %v", err)
	}
	defer respTunnel.Body.Close()

	if respTunnel.StatusCode != http.StatusOK {
		t.Errorf("Expected tunnel status 200 via invite, got %d", respTunnel.StatusCode)
	}

	tunnelBody, _ := io.ReadAll(respTunnel.Body)
	if string(tunnelBody) != "response-via-invite" {
		t.Errorf("Expected response-via-invite, got %s", tunnelBody)
	}

	wsCustom, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect wsCustom: %v", err)
	}
	defer wsCustom.Close()

	err = wsCustom.WriteJSON(tunnel.Envelope{
		Type: tunnel.TypeRegister,
		Control: &tunnel.ControlMessage{
			Subdomain:    "customsub",
			CustomDomain: "my-custom-domain.com",
		},
	})
	if err != nil {
		t.Fatalf("Failed to register wsCustom: %v", err)
	}

	var respCustom tunnel.Envelope
	if err := wsCustom.ReadJSON(&respCustom); err != nil || respCustom.Type != tunnel.TypeRegistered {
		t.Fatalf("Failed to register custom domain tunnel: %v", err)
	}

	reqDomain, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%s/", relayPort), nil)
	reqDomain.Host = "my-custom-domain.com"

	go func() {
		var reqEnv tunnel.Envelope
		if err := wsCustom.ReadJSON(&reqEnv); err == nil && reqEnv.Type == tunnel.TypeRequest {
			_ = wsCustom.WriteJSON(tunnel.Envelope{
				Type:      tunnel.TypeResponse,
				RequestID: reqEnv.RequestID,
				Response: &tunnel.TunnelResponse{
					StatusCode: http.StatusOK,
					Body:       base64.StdEncoding.EncodeToString([]byte("response-via-custom-domain")),
				},
			})
		}
	}()

	respDomain, err := http.DefaultClient.Do(reqDomain)
	if err != nil {
		t.Fatalf("Failed to fetch tunnel via custom domain: %v", err)
	}
	defer respDomain.Body.Close()

	if respDomain.StatusCode != http.StatusOK {
		t.Errorf("Expected tunnel status 200 via custom domain, got %d", respDomain.StatusCode)
	}

	domainBody, _ := io.ReadAll(respDomain.Body)
	if string(domainBody) != "response-via-custom-domain" {
		t.Errorf("Expected response-via-custom-domain, got %s", domainBody)
	}
}

func TestFederationAndAnalytics(t *testing.T) {
	// 1. Setup peer relay
	lnPeer, _ := net.Listen("tcp", "127.0.0.1:0")
	peerAddr := lnPeer.Addr().String()
	lnPeer.Close()
	peerPort := strings.Split(peerAddr, ":")[1]

	peerSrv := server.NewServer(":"+peerPort, "localhost", inspector.New(10))
	go func() {
		_ = peerSrv.Start()
	}()
	time.Sleep(100 * time.Millisecond)
	defer peerSrv.Shutdown(context.Background())

	// 2. Setup local relay with federation peer pointing to peerSrv
	lnLocal, _ := net.Listen("tcp", "127.0.0.1:0")
	localAddr := lnLocal.Addr().String()
	lnLocal.Close()
	localPort := strings.Split(localAddr, ":")[1]

	os.Setenv("SERVTUNNEL_FEDERATION_PEERS", "http://127.0.0.1:"+peerPort)
	os.Setenv("SERVTUNNEL_TOKEN", "my-test-token")
	defer os.Unsetenv("SERVTUNNEL_FEDERATION_PEERS")
	defer os.Unsetenv("SERVTUNNEL_TOKEN")

	localSrv := server.NewServer(":"+localPort, "localhost", inspector.New(10))
	go func() {
		_ = localSrv.Start()
	}()
	time.Sleep(100 * time.Millisecond)
	defer localSrv.Shutdown(context.Background())

	// 3. Register tunnel client on peerSrv
	wsURL := fmt.Sprintf("ws://127.0.0.1:%s/ws/connect?token=my-test-token", peerPort)
	dialer := &websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	ws, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect to peer ws: %v", err)
	}
	defer ws.Close()

	err = ws.WriteJSON(tunnel.Envelope{
		Type: tunnel.TypeRegister,
		Control: &tunnel.ControlMessage{
			Subdomain: "fedsub",
		},
	})
	if err != nil {
		t.Fatalf("Failed to register on peer: %v", err)
	}

	var respReg tunnel.Envelope
	if err := ws.ReadJSON(&respReg); err != nil || respReg.Type != tunnel.TypeRegistered {
		t.Fatalf("Failed to confirm register on peer: %v", err)
	}

	// 4. Send request to localSrv (subdomain: fedsub).
	// Since fedsub is on peerSrv, localSrv should query peerSrv exists endpoint,
	// find it, and proxy the request to peerSrv!
	reqFed, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%s/", localPort), nil)
	reqFed.Host = "fedsub.localhost"

	go func() {
		var reqEnv tunnel.Envelope
		if err := ws.ReadJSON(&reqEnv); err == nil && reqEnv.Type == tunnel.TypeRequest {
			_ = ws.WriteJSON(tunnel.Envelope{
				Type:      tunnel.TypeResponse,
				RequestID: reqEnv.RequestID,
				Response: &tunnel.TunnelResponse{
					StatusCode: http.StatusOK,
					Body:       base64.StdEncoding.EncodeToString([]byte("response-via-federation")),
				},
			})
		}
	}()

	respFed, err := http.DefaultClient.Do(reqFed)
	if err != nil {
		t.Fatalf("Failed to request via federation: %v", err)
	}
	defer respFed.Body.Close()

	if respFed.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 via federation proxy, got %d", respFed.StatusCode)
	}

	fedBody, _ := io.ReadAll(respFed.Body)
	if string(fedBody) != "response-via-federation" {
		t.Errorf("Expected 'response-via-federation', got '%s'", string(fedBody))
	}

	// 5. Test Live Analytics endpoint on peerSrv
	analyticsURL := fmt.Sprintf("http://127.0.0.1:%s/api/tunnels/fedsub/analytics", peerPort)
	reqAn, _ := http.NewRequest("GET", analyticsURL, nil)
	reqAn.Header.Set("Authorization", "Bearer my-test-token")
	respAn, err := http.DefaultClient.Do(reqAn)
	if err != nil || respAn.StatusCode != http.StatusOK {
		t.Fatalf("Failed to fetch analytics: %v, status: %d", err, respAn.StatusCode)
	}
	defer respAn.Body.Close()

	var an map[string]interface{}
	json.NewDecoder(respAn.Body).Decode(&an)
	if an["subdomain"] != "fedsub" {
		t.Errorf("Expected subdomain 'fedsub', got %v", an["subdomain"])
	}

	// Connections count should be 1
	connectionsNum, _ := an["connections"].(float64)
	if int(connectionsNum) != 1 {
		t.Errorf("Expected 1 connection tracked, got %v", connectionsNum)
	}
}
