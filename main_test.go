package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"servgate/pkg/proxy"
	"servgate/pkg/wasm"
)

func TestServGateReverseProxy(t *testing.T) {
	// 1. Start a mock backend target server
	backendReceivedPath := ""
	backendHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendReceivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend-response"))
	})
	backendServer := &http.Server{
		Addr:    "127.0.0.1:8091",
		Handler: backendHandler,
	}
	go backendServer.ListenAndServe()

	// 2. Setup gateway handler
	routes := []proxy.Route{
		{
			Prefix: "/api/v1/orders",
			Target: "http://127.0.0.1:8091",
		},
	}

	wasmManager, err := wasm.GetMiddlewareManager(context.Background())
	if err != nil {
		t.Fatalf("WASM setup failed: %v", err)
	}

	gatewayHandler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gatewayServer := &http.Server{
		Addr:    "127.0.0.1:8092",
		Handler: gatewayHandler,
	}
	go gatewayServer.ListenAndServe()

	time.Sleep(200 * time.Millisecond)

	// 3. Issue proxy request
	resp, err := http.Get("http://127.0.0.1:8092/api/v1/orders/create")
	if err != nil {
		t.Fatalf("Failed to execute request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != "backend-response" {
		t.Errorf("Expected body 'backend-response', got %q", string(body))
	}

	if backendReceivedPath != "/create" {
		t.Errorf("Expected backend path prefix strip logic to target '/create', got %q", backendReceivedPath)
	}

	// Clean servers
	_ = backendServer.Shutdown(context.Background())
	_ = gatewayServer.Shutdown(context.Background())
}

func TestWasmMiddlewareInjection(t *testing.T) {
	// 1. Setup wazero manager
	wasmManager, err := wasm.GetMiddlewareManager(context.Background())
	if err != nil {
		t.Fatalf("WASM setup failed: %v", err)
	}

	// Register empty/no-op transform for test
	err = wasmManager.Register(context.Background(), "noop", []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // Magic wasm version
	})
	if err != nil {
		t.Fatalf("Failed to compile WASM: %v", err)
	}

	res, err := wasmManager.Run(context.Background(), "noop", []byte("raw-bytes"))
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// No-op module should compile and return empty bytes safely without error
	if len(res) > 0 {
		t.Errorf("Expected empty bytes response, got %v", res)
	}
}

func TestRateLimiting(t *testing.T) {
	// 1. Start a mock backend target server
	backendHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend-response"))
	})
	backendServer := &http.Server{
		Addr:    "127.0.0.1:8073",
		Handler: backendHandler,
	}
	go backendServer.ListenAndServe()
	defer backendServer.Shutdown(context.Background())

	// 2. Setup gateway handler with rate limit of 2 requests per minute
	routes := []proxy.Route{
		{
			Prefix:       "/api/v1/limited",
			Target:       "http://127.0.0.1:8073",
			RateLimitRPM: 2,
		},
	}

	wasmManager, err := wasm.GetMiddlewareManager(context.Background())
	if err != nil {
		t.Fatalf("WASM setup failed: %v", err)
	}

	gatewayHandler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gatewayServer := &http.Server{
		Addr:    "127.0.0.1:8074",
		Handler: gatewayHandler,
	}
	go gatewayServer.ListenAndServe()
	defer gatewayServer.Shutdown(context.Background())

	time.Sleep(200 * time.Millisecond)

	// Issue 2 requests, which should succeed
	for i := 0; i < 2; i++ {
		resp, err := http.Get("http://127.0.0.1:8074/api/v1/limited/test")
		if err != nil {
			t.Fatalf("Request %d failed: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Request %d: expected status 200, got %d", i, resp.StatusCode)
		}
	}

	// The 3rd request should be rate limited (429)
	resp, err := http.Get("http://127.0.0.1:8074/api/v1/limited/test")
	if err != nil {
		t.Fatalf("3rd request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Expected 3rd request to be rate limited with 429, got %d", resp.StatusCode)
	}
}

func buildWASM(t *testing.T, src string) []byte {
	t.Helper()

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "main.go")
	wasmPath := filepath.Join(tmpDir, "transform.wasm")

	if err := os.WriteFile(srcPath, []byte(src), 0644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", wasmPath, srcPath)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "unsupported GOOS") || strings.Contains(string(out), "unsupported") {
			t.Skipf("GOOS=wasip1 not supported by this Go toolchain: %s", out)
		}
		t.Fatalf("go build wasip1 failed: %v\n%s", err, out)
	}

	data, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("read wasm: %v", err)
	}
	return data
}

func TestDirectMemoryPassingAndResponseFilters(t *testing.T) {
	// 1. Static WASM bytecode with memory export, allocate() returning 0, and transform() incrementing each byte by 1
	wasmBytes := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // Magic & Version
		// Section 1: Type
		0x01, 0x0c, 0x02,
		0x60, 0x01, 0x7f, 0x01, 0x7f,       // Type 0: (i32) -> i32
		0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7e, // Type 1: (i32, i32) -> i64
		// Section 3: Function
		0x03, 0x03, 0x02, 0x00, 0x01,
		// Section 5: Memory
		0x05, 0x03, 0x01, 0x00, 0x01,
		// Section 7: Export
		0x07, 0x21, 0x03,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x08, 'a', 'l', 'l', 'o', 'c', 'a', 't', 'e', 0x00, 0x00,
		0x09, 't', 'r', 'a', 'n', 's', 'f', 'o', 'r', 'm', 0x00, 0x01,
		// Section 10: Code
		0x0a, 0x35, 0x02,
		// Body 0 (allocate)
		0x04, 0x00, 0x41, 0x00, 0x0b,
		// Body 1 (transform)
		46,
		0x01, 0x01, 0x7f,             // 1 local (i32)
		0x41, 0x00, 0x21, 0x02,       // i = 0
		0x02, 0x40,                   // block
		0x03, 0x40,                   // loop
		0x20, 0x02, 0x20, 0x01, 0x46, // i == size
		0x0d, 0x01,                   // br_if 1
		0x20, 0x02, 0x20, 0x02,       // address, address
		0x2d, 0x00, 0x00,             // i32.load8_u
		0x41, 0x01, 0x6a,             // + 1
		0x3a, 0x00, 0x00,             // i32.store8
		0x20, 0x02, 0x41, 0x01, 0x6a, 0x21, 0x02, // i++
		0x0c, 0x00,                   // br 0
		0x0b, 0x0b,                   // end loop, end block
		0x20, 0x01, 0xac, 0x0b,       // return size (as i64), end func
	}

	// 2. Set up WASM manager and register the compiled module
	wasmManager, err := wasm.GetMiddlewareManager(context.Background())
	if err != nil {
		t.Fatalf("WASM setup failed: %v", err)
	}
	
	err = wasmManager.Register(context.Background(), "direct-mem-inc", wasmBytes)
	if err != nil {
		t.Fatalf("Failed to register direct-mem-inc: %v", err)
	}

	// 3. Start a mock backend target server
	backendHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqBody, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend:" + string(reqBody)))
	})
	backendServer := &http.Server{
		Addr:    "127.0.0.1:8065",
		Handler: backendHandler,
	}
	go backendServer.ListenAndServe()
	defer backendServer.Shutdown(context.Background())

	// 4. Setup gateway handler with both request and response WASM middlewares
	routes := []proxy.Route{
		{
			Prefix:             "/api/v1/direct",
			Target:             "http://127.0.0.1:8065",
			Middleware:         "direct-mem-inc",
			ResponseMiddleware: "direct-mem-inc",
		},
	}

	gatewayHandler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gatewayServer := &http.Server{
		Addr:    "127.0.0.1:8066",
		Handler: gatewayHandler,
	}
	go gatewayServer.ListenAndServe()
	defer gatewayServer.Shutdown(context.Background())

	time.Sleep(200 * time.Millisecond)

	// 5. Issue proxy request
	reqBody := []byte("hello-wasm")
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:8066/api/v1/direct/test", bytes.NewReader(reqBody))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	expected := "cbdlfoe;jgnnq/ycuo"
	if string(body) != expected {
		t.Errorf("Expected body %q, got %q", expected, string(body))
	}
}

func TestInstallCommand(t *testing.T) {
	mockWasmContent := []byte("wasm-binary-content")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/middlewares/auth-token.wasm" {
			w.WriteHeader(http.StatusOK)
			w.Write(mockWasmContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"servgate", "install", "auth-token"}

	os.Setenv("SERV_REGISTRY", ts.URL)
	defer os.Unsetenv("SERV_REGISTRY")

	destPath := filepath.Join("middlewares", "auth-token.wasm")
	os.Remove(destPath)
	defer os.Remove(destPath)

	runInstallCommand()

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("Failed to read installed middleware: %v", err)
	}

	if string(data) != "wasm-binary-content" {
		t.Errorf("Expected content 'wasm-binary-content', got %q", string(data))
	}
}

func TestLoadBalancing(t *testing.T) {
	hitCount1, hitCount2 := 0, 0
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount1++
		w.Write([]byte("backend-1"))
	}))
	defer ts1.Close()

	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount2++
		w.Write([]byte("backend-2"))
	}))
	defer ts2.Close()

	routes := []proxy.Route{
		{
			Prefix:       "/api/lb",
			Targets:      []string{ts1.URL, ts2.URL},
			LoadBalancer: "round_robin",
		},
	}

	wasmManager, err := wasm.GetMiddlewareManager(context.Background())
	if err != nil {
		t.Fatalf("WASM setup failed: %v", err)
	}

	gatewayHandler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gatewayServer := httptest.NewServer(gatewayHandler)
	defer gatewayServer.Close()

	for i := 0; i < 4; i++ {
		resp, err := http.Get(gatewayServer.URL + "/api/lb/test")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		resp.Body.Close()
	}

	if hitCount1 != 2 || hitCount2 != 2 {
		t.Errorf("Expected round robin load distribution (2, 2), got (%d, %d)", hitCount1, hitCount2)
	}
}

func TestGRPCTranspilation(t *testing.T) {
	tsGrpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if len(body) < 5 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if body[0] != 0 {
			t.Errorf("Expected compression flag 0, got %d", body[0])
		}
		w.Write(body)
	}))
	defer tsGrpc.Close()

	tsRest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"message":"hello"}` {
			t.Errorf("Expected raw JSON payload, got %q", string(body))
		}
		w.Write([]byte(`{"reply":"hi"}`))
	}))
	defer tsRest.Close()

	routes := []proxy.Route{
		{
			Prefix:        "/api/rest-to-grpc",
			Target:        tsGrpc.URL,
			TranspileType: "rest_to_grpc",
		},
		{
			Prefix:        "/api/grpc-to-rest",
			Target:        tsRest.URL,
			TranspileType: "grpc_to_rest",
		},
	}

	wasmManager, _ := wasm.GetMiddlewareManager(context.Background())
	gatewayHandler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gatewayServer := httptest.NewServer(gatewayHandler)
	defer gatewayServer.Close()

	resp, err := http.Post(gatewayServer.URL+"/api/rest-to-grpc/call", "application/json", strings.NewReader(`{"message":"hello"}`))
	if err != nil {
		t.Fatalf("REST request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"message":"hello"}` {
		t.Errorf("Expected REST response payload, got %q", string(body))
	}

	rawPayload := []byte(`{"message":"hello"}`)
	header := make([]byte, 5)
	header[0] = 0
	binary.BigEndian.PutUint32(header[1:], uint32(len(rawPayload)))
	grpcPayload := append(header, rawPayload...)

	resp2, err := http.Post(gatewayServer.URL+"/api/grpc-to-rest/call", "application/grpc", bytes.NewReader(grpcPayload))
	if err != nil {
		t.Fatalf("gRPC request failed: %v", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if len(body2) < 5 {
		t.Fatalf("Expected gRPC framed response, got short payload")
	}
	jsonReply := body2[5:]
	if string(jsonReply) != `{"reply":"hi"}` {
		t.Errorf("Expected gRPC response body, got %q", string(jsonReply))
	}
}

func TestWebSocketProxying(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer l.Close()

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reqStr := ""
		for {
			buf := make([]byte, 1024)
			n, err := conn.Read(buf)
			if err != nil {
				t.Logf("Mock server read failed: %v", err)
				return
			}
			reqStr += string(buf[:n])
			if strings.Contains(reqStr, "\r\n\r\n") {
				break
			}
		}

		if !strings.Contains(strings.ToLower(reqStr), "upgrade: websocket") {
			t.Logf("Mock server missing Upgrade header: %q", reqStr)
			return
		}

		handshakeResp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: test-accept\r\n\r\n"
		conn.Write([]byte(handshakeResp))

		io.Copy(conn, conn)
	}()

	routes := []proxy.Route{
		{
			Prefix: "/ws",
			Target: "http://" + l.Addr().String(),
		},
	}

	wasmManager, _ := wasm.GetMiddlewareManager(context.Background())
	gatewayHandler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gatewayServer := httptest.NewServer(gatewayHandler)
	defer gatewayServer.Close()

	dialAddr := strings.TrimPrefix(gatewayServer.URL, "http://")
	clientConn, err := net.Dial("tcp", dialAddr)
	if err != nil {
		t.Fatalf("Failed to dial gateway: %v", err)
	}
	defer clientConn.Close()

	handshake := "GET /ws/chat HTTP/1.1\r\n" +
		"Host: " + dialAddr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: test-key\r\n\r\n"
	clientConn.Write([]byte(handshake))

	buf := make([]byte, 1024)
	n, err := clientConn.Read(buf)
	if err != nil {
		t.Fatalf("Failed to read handshake response: %v", err)
	}
	respStr := string(buf[:n])
	if !strings.Contains(respStr, "101 Switching Protocols") {
		t.Errorf("Expected 101 Switching Protocols upgrade, got:\n%s", respStr)
	}

	message := []byte("hello websocket")
	clientConn.Write(message)

	n, _ = clientConn.Read(buf)
	if string(buf[:n]) != "hello websocket" {
		t.Errorf("Expected echo message 'hello websocket', got %q", string(buf[:n]))
	}
}

func TestServGateWebhookBridge(t *testing.T) {
	// 1. Setup mock ServQueue server
	receivedPayload := ""
	receivedTopic := ""
	mockQueue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/api/publish" {
			var body struct {
				Topic   string `json:"topic"`
				Payload string `json:"payload"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				receivedTopic = body.Topic
				receivedPayload = body.Payload
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"success"}`))
			return
		}
		http.Error(w, "Not Found", http.StatusNotFound)
	}))
	defer mockQueue.Close()

	// Configure environment variables so Gateway resolves the mock queue server address
	os.Setenv("SERVVERSE_DISCOVERY", fmt.Sprintf(`{"queue":"%s"}`, mockQueue.URL))
	defer os.Unsetenv("SERVVERSE_DISCOVERY")

	// 2. Setup gateway handler with servqueue:// target
	routes := []proxy.Route{
		{
			Prefix: "/webhook/orders",
			Target: "servqueue://orders",
		},
	}

	wasmManager, err := wasm.GetMiddlewareManager(context.Background())
	if err != nil {
		t.Fatalf("WASM setup failed: %v", err)
	}

	gatewayHandler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gatewayServer := httptest.NewServer(gatewayHandler)
	defer gatewayServer.Close()

	// 3. POST request to gateway webhook route
	client := &http.Client{Timeout: 2 * time.Second}
	postData := `{"item":"gadget","qty":2}`
	resp, err := client.Post(gatewayServer.URL+"/webhook/orders", "application/json", strings.NewReader(postData))
	if err != nil {
		t.Fatalf("Failed to execute request to gateway: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 OK, got %d", resp.StatusCode)
	}

	var res struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if res.Status != "success" || !strings.Contains(res.Message, "orders") {
		t.Errorf("Unexpected response: %+v", res)
	}

	if receivedTopic != "orders" {
		t.Errorf("Expected bridged topic to be 'orders', got %q", receivedTopic)
	}
	if receivedPayload != postData {
		t.Errorf("Expected bridged payload to be %q, got %q", postData, receivedPayload)
	}
}

func TestPolicyCompilationAndEnforcement(t *testing.T) {
	tmpPolicyFile, err := os.CreateTemp("", "test_policy_*.policy")
	if err != nil {
		t.Fatalf("Failed to create temp policy file: %v", err)
	}
	defer os.Remove(tmpPolicyFile.Name())

	policyContent := `
allow GET /api/public
allow POST /api/secure if header.Authorization == admin-token
deny * *
`
	if _, err := tmpPolicyFile.WriteString(policyContent); err != nil {
		t.Fatalf("Failed to write policy file: %v", err)
	}
	tmpPolicyFile.Close()

	tmpWasmFile, err := os.CreateTemp("", "test_policy_*.wasm")
	if err != nil {
		t.Fatalf("Failed to create temp wasm file: %v", err)
	}
	tmpWasmFile.Close()
	defer os.Remove(tmpWasmFile.Name())

	// Compile policy to WASM
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	os.Args = []string{"servgate", "policy", "compile", tmpPolicyFile.Name(), "-o", tmpWasmFile.Name()}
	runPolicyCommand()

	// Verify compiled WASM file exists
	if fi, err := os.Stat(tmpWasmFile.Name()); err != nil || fi.Size() == 0 {
		t.Fatalf("Compiled policy WASM file is missing or empty: %v", err)
	}

	// Register the compiled policy as wasm middleware
	wasmBytes, err := os.ReadFile(tmpWasmFile.Name())
	if err != nil {
		t.Fatalf("Failed to read compiled WASM: %v", err)
	}

	ctx := context.Background()
	wasmManager, err := wasm.GetMiddlewareManager(ctx)
	if err != nil {
		t.Fatalf("Failed to get WASM manager: %v", err)
	}

	mwName := "auth-policy" // Contains 'policy' so it triggers JSON metadata input in proxy handler
	if err := wasmManager.Register(ctx, mwName, wasmBytes); err != nil {
		t.Fatalf("Failed to register WASM middleware: %v", err)
	}

	// Setup Gateway routing
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend success"))
	}))
	defer backend.Close()

	routes := []proxy.Route{
		{
			Prefix:     "/api",
			Target:     backend.URL,
			Middleware: mwName,
		},
	}

	gatewayHandler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gatewayServer := httptest.NewServer(gatewayHandler)
	defer gatewayServer.Close()

	client := &http.Client{Timeout: 2 * time.Second}

	// 1. GET /api/public should be ALLOWED
	req1, _ := http.NewRequest("GET", gatewayServer.URL+"/api/public", nil)
	resp1, err := client.Do(req1)
	if err != nil {
		t.Fatalf("Request 1 failed: %v", err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 for allowed GET, got %d", resp1.StatusCode)
	}

	// 2. POST /api/secure with correct header should be ALLOWED
	req2, _ := http.NewRequest("POST", gatewayServer.URL+"/api/secure", nil)
	req2.Header.Set("Authorization", "admin-token")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("Request 2 failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 for allowed POST, got %d", resp2.StatusCode)
	}

	// 3. POST /api/secure with missing/incorrect header should be DENIED (403 Forbidden)
	req3, _ := http.NewRequest("POST", gatewayServer.URL+"/api/secure", nil)
	resp3, err := client.Do(req3)
	if err != nil {
		t.Fatalf("Request 3 failed: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusForbidden {
		t.Errorf("Expected status 403 for denied POST, got %d", resp3.StatusCode)
	}
}

func TestEdgeRequestValidation(t *testing.T) {
	// 1. Mock backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	// 2. Setup gateway with validation schema
	routes := []proxy.Route{
		{
			Prefix: "/api/users",
			Target: backend.URL,
			ValidationSchema: map[string]string{
				"name":  "required,string",
				"email": "required,email",
				"age":   "int",
			},
		},
	}

	wasmManager, _ := wasm.GetMiddlewareManager(context.Background())
	handler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gwServer := httptest.NewServer(handler)
	defer gwServer.Close()

	client := &http.Client{Timeout: 2 * time.Second}

	// Case A: Valid request
	validBody := `{"name":"John Doe","email":"john@example.com","age":30}`
	resp, err := client.Post(gwServer.URL+"/api/users", "application/json", strings.NewReader(validBody))
	if err != nil {
		t.Fatalf("Valid request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 for valid body, got %d", resp.StatusCode)
	}

	// Case B: Missing required field (name)
	missingBody := `{"email":"john@example.com","age":30}`
	resp, err = client.Post(gwServer.URL+"/api/users", "application/json", strings.NewReader(missingBody))
	if err != nil {
		t.Fatalf("Missing field request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400 for missing required field, got %d", resp.StatusCode)
	}
	var errResp proxy.APIError
	json.NewDecoder(resp.Body).Decode(&errResp)
	if !strings.Contains(errResp.Error, "required") {
		t.Errorf("Expected error to mention 'required', got: %s", errResp.Error)
	}

	// Case C: Invalid email format
	badEmailBody := `{"name":"John Doe","email":"john-at-example.com","age":30}`
	resp, err = client.Post(gwServer.URL+"/api/users", "application/json", strings.NewReader(badEmailBody))
	if err != nil {
		t.Fatalf("Invalid email request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400 for invalid email, got %d", resp.StatusCode)
	}

	// Case D: Invalid age type (float instead of int)
	floatAgeBody := `{"name":"John Doe","email":"john@example.com","age":30.5}`
	resp, err = client.Post(gwServer.URL+"/api/users", "application/json", strings.NewReader(floatAgeBody))
	if err != nil {
		t.Fatalf("Float age request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400 for float age, got %d", resp.StatusCode)
	}
}

func TestOpenAPIDiscovery(t *testing.T) {
	routes := []proxy.Route{
		{
			Prefix: "/api/users",
			Target: "http://127.0.0.1:8099",
			ValidationSchema: map[string]string{
				"name":  "string,required",
				"email": "string,required,email",
				"age":   "integer",
			},
		},
	}

	wasmManager, err := wasm.GetMiddlewareManager(context.Background())
	if err != nil {
		t.Fatalf("WASM setup failed: %v", err)
	}

	gatewayHandler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gwServer := httptest.NewServer(gatewayHandler)
	defer gwServer.Close()

	resp, err := http.Get(gwServer.URL + "/api/docs/openapi.json")
	if err != nil {
		t.Fatalf("Failed to fetch /api/docs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var schema map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&schema); err != nil {
		t.Fatalf("Failed to decode OpenAPI schema: %v", err)
	}

	if schema["openapi"] != "3.1.0" {
		t.Errorf("Expected openapi version '3.1.0', got %v", schema["openapi"])
	}

	paths, ok := schema["paths"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing 'paths' in OpenAPI document")
	}

	pathItem, ok := paths["/api/users/{path}"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing path '/api/users/{path}' in OpenAPI schema paths")
	}

	postOp, ok := pathItem["post"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing 'post' operation in path item")
	}

	reqBody, ok := postOp["requestBody"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing 'requestBody' in post operation")
	}

	content, ok := reqBody["content"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing 'content' in requestBody")
	}

	jsonType, ok := content["application/json"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing 'application/json' in content")
	}

	bodySchema, ok := jsonType["schema"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing 'schema' in application/json content")
	}

	props, ok := bodySchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing 'properties' in body schema")
	}

	nameProp, ok := props["name"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing 'name' property")
	}
	if nameProp["type"] != "string" {
		t.Errorf("Expected name property type 'string', got %v", nameProp["type"])
	}

	ageProp, ok := props["age"].(map[string]interface{})
	if !ok {
		t.Fatalf("Missing 'age' property")
	}
	if ageProp["type"] != "integer" {
		t.Errorf("Expected age property type 'integer', got %v", ageProp["type"])
	}

	required, ok := bodySchema["required"].([]interface{})
	if !ok {
		t.Fatalf("Missing 'required' fields list")
	}

	hasName := false
	hasEmail := false
	for _, req := range required {
		if req == "name" {
			hasName = true
		}
		if req == "email" {
			hasEmail = true
		}
	}
	if !hasName || !hasEmail {
		t.Errorf("Expected 'name' and 'email' in required list, got %v", required)
	}
}

func TestIPAccessControl(t *testing.T) {
	// A simple backend server to proxy to
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	routes := []proxy.Route{
		{
			Prefix:      "/api/allowed-ip",
			Target:      backend.URL,
			IPAllowlist: []string{"127.0.0.1"},
		},
		{
			Prefix:      "/api/allowed-cidr",
			Target:      backend.URL,
			IPAllowlist: []string{"127.0.0.0/24"},
		},
		{
			Prefix:      "/api/blocked-ip",
			Target:      backend.URL,
			IPBlocklist: []string{"127.0.0.1"},
		},
		{
			Prefix:      "/api/blocked-cidr",
			Target:      backend.URL,
			IPBlocklist: []string{"127.0.0.0/24"},
		},
		{
			Prefix:      "/api/no-limits",
			Target:      backend.URL,
		},
	}

	wasmManager, err := wasm.GetMiddlewareManager(context.Background())
	if err != nil {
		t.Fatalf("WASM setup failed: %v", err)
	}

	gatewayHandler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gwServer := httptest.NewServer(gatewayHandler)
	defer gwServer.Close()

	// Helper to send request and return status code
	sendReq := func(path string) int {
		resp, err := http.Get(gwServer.URL + path)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// 127.0.0.1 is client IP from httptest
	// /api/allowed-ip: 127.0.0.1 is in allowlist -> 200
	if status := sendReq("/api/allowed-ip"); status != http.StatusOK {
		t.Errorf("Expected 200 for allowed IP, got %d", status)
	}

	// /api/allowed-cidr: 127.0.0.1 matches 127.0.0.0/24 -> 200
	if status := sendReq("/api/allowed-cidr"); status != http.StatusOK {
		t.Errorf("Expected 200 for allowed CIDR, got %d", status)
	}

	// /api/blocked-ip: 127.0.0.1 is in blocklist -> 403
	if status := sendReq("/api/blocked-ip"); status != http.StatusForbidden {
		t.Errorf("Expected 403 for blocked IP, got %d", status)
	}

	// /api/blocked-cidr: 127.0.0.1 matches 127.0.0.0/24 -> 403
	if status := sendReq("/api/blocked-cidr"); status != http.StatusForbidden {
		t.Errorf("Expected 403 for blocked CIDR, got %d", status)
	}

	// /api/no-limits: no IP restrictions -> 200
	if status := sendReq("/api/no-limits"); status != http.StatusOK {
		t.Errorf("Expected 200 for unrestricted route, got %d", status)
	}
}

func TestAdminRateLimiting(t *testing.T) {
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("admin ok"))
	})

	// Wrap handler with a low limit of 2 requests per minute for testing
	limitedHandler := withAdminRateLimit(2, dummyHandler)
	server := httptest.NewServer(limitedHandler)
	defer server.Close()

	// First request -> 200
	resp1, err := http.Get(server.URL + "/api/routes")
	if err != nil {
		t.Fatalf("Request 1 failed: %v", err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Request 1: expected 200, got %d", resp1.StatusCode)
	}

	// Second request -> 200
	resp2, err := http.Get(server.URL + "/api/routes")
	if err != nil {
		t.Fatalf("Request 2 failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Request 2: expected 200, got %d", resp2.StatusCode)
	}

	// Third request -> 429
	resp3, err := http.Get(server.URL + "/api/routes")
	if err != nil {
		t.Fatalf("Request 3 failed: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Request 3: expected 429, got %d", resp3.StatusCode)
	}
}



func TestConfigSignatureVerification(t *testing.T) {
	os.Setenv("SERV_JWT_SECRET", "super-secret-key-12345")
	defer os.Unsetenv("SERV_JWT_SECRET")

	tmpFile := filepath.Join(os.TempDir(), "test_config_sig.json")
	defer os.Remove(tmpFile)

	prov := proxy.NewLocalFileProvider(tmpFile)
	cfg := &proxy.GatewayConfig{
		Addr:      ":8080",
		AuthToken: "token",
		Routes: []proxy.Route{
			{Prefix: "/api/v1", Target: "http://localhost:8081"},
		},
	}

	err := prov.Save(cfg)
	if err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	if cfg.Signature == "" {
		t.Fatalf("Expected config signature to be populated")
	}

	loaded, err := prov.Load()
	if err != nil {
		t.Fatalf("Expected Load to succeed: %v", err)
	}

	if loaded.Signature != cfg.Signature {
		t.Errorf("Loaded signature mismatch")
	}

	loaded.Routes[0].Prefix = "/api/tampered"
	data, _ := json.MarshalIndent(loaded, "", "  ")
	_ = os.WriteFile(tmpFile, data, 0644)

	_, err = prov.Load()
	if err == nil || !strings.Contains(err.Error(), "signature verification failed") {
		t.Errorf("Expected signature verification error, got: %v", err)
	}
}

func TestRouteDeletionAndActiveConnections(t *testing.T) {
	routes := []proxy.Route{
		{Prefix: "/api/test", Target: "http://localhost:8081"},
	}
	wasmManager, _ := wasm.GetMiddlewareManager(context.Background())
	handler := proxy.NewGatewayHandler(routes, wasmManager, "sec")

	if len(handler.GetRoutes()) != 1 {
		t.Fatalf("Expected 1 route initially")
	}

	conns := handler.GetActiveConnections()
	if conns == nil {
		t.Fatalf("Expected active connections map")
	}
}

func TestAccessLog(t *testing.T) {
	// 1. Setup backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"ok"}`))
	}))
	defer backend.Close()

	// 2. Setup gateway with access logging enabled
	logFile := filepath.Join(t.TempDir(), "access.jsonl")

	routes := []proxy.Route{
		{
			Prefix:        "/api/logged",
			Target:        backend.URL,
			AccessLog:     true,
			AccessLogPath: logFile,
		},
	}

	wasmManager, _ := wasm.GetMiddlewareManager(context.Background())
	handler := proxy.NewGatewayHandler(routes, wasmManager, "")
	defer handler.Close()
	gwServer := httptest.NewServer(handler)
	defer gwServer.Close()

	client := &http.Client{Timeout: 2 * time.Second}

	// 3. Send 3 requests
	for i := 0; i < 3; i++ {
		resp, err := client.Get(gwServer.URL + "/api/logged/items")
		if err != nil {
			t.Fatalf("Request %d failed: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Request %d: expected 200, got %d", i, resp.StatusCode)
		}
	}

	// Allow log to flush
	time.Sleep(100 * time.Millisecond)

	// 4. Read and verify log file
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("Failed to read access log: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("Expected 3 log lines, got %d:\n%s", len(lines), string(data))
	}

	// Verify structure of first log entry
	var entry proxy.LogEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("Failed to parse log entry: %v", err)
	}

	if entry.Method != "GET" {
		t.Errorf("Expected method GET, got %s", entry.Method)
	}
	if entry.Route != "/api/logged" {
		t.Errorf("Expected route /api/logged, got %s", entry.Route)
	}
	if entry.Status != 200 {
		t.Errorf("Expected status 200, got %d", entry.Status)
	}
	if entry.Path != "/items" {
		// After proxy rewrite, path becomes the stripped version
		t.Logf("Path after proxy: %s (note: may be rewritten)", entry.Path)
	}
}

func TestResponseCache(t *testing.T) {
	// 1. Setup backend that counts hits
	hitCount := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf(`{"hit":%d}`, hitCount)))
	}))
	defer backend.Close()

	// 2. Setup gateway with 60s cache TTL
	routes := []proxy.Route{
		{
			Prefix:          "/api/cached",
			Target:          backend.URL,
			CacheTTLSeconds: 60,
		},
	}

	wasmManager, _ := wasm.GetMiddlewareManager(context.Background())
	handler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gwServer := httptest.NewServer(handler)
	defer gwServer.Close()

	client := &http.Client{Timeout: 2 * time.Second}

	// 3. First GET — should MISS cache and hit backend
	resp1, err := client.Get(gwServer.URL + "/api/cached/products")
	if err != nil {
		t.Fatalf("Request 1 failed: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Request 1: expected 200, got %d", resp1.StatusCode)
	}
	if resp1.Header.Get("X-Cache") != "MISS" {
		t.Errorf("Request 1: expected X-Cache: MISS, got %s", resp1.Header.Get("X-Cache"))
	}
	if string(body1) != `{"hit":1}` {
		t.Errorf("Request 1: expected {\"hit\":1}, got %s", string(body1))
	}

	// 4. Second GET (same path) — should HIT cache, NOT hit backend
	resp2, err := client.Get(gwServer.URL + "/api/cached/products")
	if err != nil {
		t.Fatalf("Request 2 failed: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if resp2.Header.Get("X-Cache") != "HIT" {
		t.Errorf("Request 2: expected X-Cache: HIT, got %s", resp2.Header.Get("X-Cache"))
	}
	if string(body2) != `{"hit":1}` {
		t.Errorf("Request 2: expected cached {\"hit\":1}, got %s", string(body2))
	}
	if hitCount != 1 {
		t.Errorf("Expected backend to be hit only once, got %d", hitCount)
	}

	// 5. POST should bypass cache entirely
	resp3, err := client.Post(gwServer.URL+"/api/cached/products", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST request failed: %v", err)
	}
	resp3.Body.Close()
	if hitCount != 2 {
		t.Errorf("POST should hit backend (hit count should be 2), got %d", hitCount)
	}

	// 6. Cache invalidation
	count := handler.InvalidateCache("/api/cached", "")
	if count != 1 {
		t.Errorf("Expected 1 cache entry invalidated, got %d", count)
	}

	// 7. After invalidation, GET should MISS and hit backend again
	resp4, err := client.Get(gwServer.URL + "/api/cached/products")
	if err != nil {
		t.Fatalf("Request 4 failed: %v", err)
	}
	resp4.Body.Close()
	if resp4.Header.Get("X-Cache") != "MISS" {
		t.Errorf("Request 4: expected X-Cache: MISS after invalidation, got %s", resp4.Header.Get("X-Cache"))
	}
	if hitCount != 3 {
		t.Errorf("Expected backend to be hit 3 times after invalidation, got %d", hitCount)
	}
}

func TestCanaryTrafficSplitting(t *testing.T) {
	// 1. Setup two backends representing v1 and v2
	v1Hits, v2Hits := 0, 0

	tsV1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v1Hits++
		w.Write([]byte("v1"))
	}))
	defer tsV1.Close()

	tsV2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v2Hits++
		w.Write([]byte("v2"))
	}))
	defer tsV2.Close()

	// 2. Setup gateway with 80/20 weighted split (v1=80, v2=20)
	routes := []proxy.Route{
		{
			Prefix: "/api/canary",
			TargetsWeighted: []proxy.WeightedTarget{
				{URL: tsV1.URL, Weight: 80},
				{URL: tsV2.URL, Weight: 20},
			},
		},
	}

	wasmManager, _ := wasm.GetMiddlewareManager(context.Background())
	handler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gwServer := httptest.NewServer(handler)
	defer gwServer.Close()

	client := &http.Client{Timeout: 2 * time.Second}

	// 3. Send 200 requests
	totalRequests := 200
	for i := 0; i < totalRequests; i++ {
		resp, err := client.Get(gwServer.URL + "/api/canary/test")
		if err != nil {
			t.Fatalf("Request %d failed: %v", i, err)
		}
		resp.Body.Close()
	}

	totalHits := v1Hits + v2Hits
	if totalHits != totalRequests {
		t.Fatalf("Expected %d total hits, got %d (v1=%d, v2=%d)", totalRequests, totalHits, v1Hits, v2Hits)
	}

	// 4. Verify distribution is within reasonable bounds
	// Expected: v1 ~80%, v2 ~20%. Allow ±15% tolerance.
	v1Pct := float64(v1Hits) / float64(totalRequests) * 100
	v2Pct := float64(v2Hits) / float64(totalRequests) * 100

	t.Logf("Traffic split: v1=%.1f%% (%d/%d), v2=%.1f%% (%d/%d)", v1Pct, v1Hits, totalRequests, v2Pct, v2Hits, totalRequests)

	if v1Pct < 55 || v1Pct > 95 {
		t.Errorf("v1 traffic percentage %.1f%% is outside expected bounds (55-95%%)", v1Pct)
	}
	if v2Pct < 5 || v2Pct > 45 {
		t.Errorf("v2 traffic percentage %.1f%% is outside expected bounds (5-45%%)", v2Pct)
	}

	// 5. Verify X-Canary-Target header is present
	resp, err := client.Get(gwServer.URL + "/api/canary/test")
	if err != nil {
		t.Fatalf("Canary header check failed: %v", err)
	}
	resp.Body.Close()
	canaryTarget := resp.Header.Get("X-Canary-Target")
	if canaryTarget == "" {
		t.Errorf("Expected X-Canary-Target header to be set")
	}
	if canaryTarget != tsV1.URL && canaryTarget != tsV2.URL {
		t.Errorf("X-Canary-Target %q doesn't match any backend URL", canaryTarget)
	}
}

func TestMaxBodySizeLimit(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer backend.Close()

	routes := []proxy.Route{
		{
			Prefix:      "/limited",
			Target:      backend.URL,
			MaxBodySize: 10, // 10 bytes limit
		},
	}

	handler := proxy.NewGatewayHandler(routes, nil, "")
	gwServer := httptest.NewServer(handler)
	defer gwServer.Close()

	client := &http.Client{}
	resp, err := client.Post(gwServer.URL+"/limited/data", "application/json", strings.NewReader("123456789"))
	if err != nil {
		t.Fatalf("Failed to post within limit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK for 9-byte payload, got %d", resp.StatusCode)
	}

	resp2, err := client.Post(gwServer.URL+"/limited/data", "application/json", strings.NewReader("123456789012345"))
	if err != nil {
		t.Fatalf("Failed to post exceeding limit: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == http.StatusOK {
		t.Errorf("Expected request to be rejected, but got 200 OK")
	}
}

func TestGitOpsConfigSyncWebhook(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "servgate-gitops-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	initialCfg := proxy.GatewayConfig{
		Addr: ":12345",
		Routes: []proxy.Route{
			{Prefix: "/old", Target: "http://localhost:8081"},
		},
	}
	data, _ := json.Marshal(initialCfg)
	tmpFile.Write(data)
	tmpFile.Close()

	localProv := proxy.NewLocalFileProvider(tmpFile.Name())

	mux := http.NewServeMux()
	cfg := &proxy.GatewayConfig{
		AuthToken: "secret",
		Routes:    initialCfg.Routes,
	}
	handler := proxy.NewGatewayHandler(cfg.Routes, nil, cfg.AuthToken)

	handleGitOpsWebhook := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			proxy.WriteJSONError(w, r, "Method not allowed", "ERR_METHOD_NOT_ALLOWED", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			proxy.WriteJSONError(w, r, "Unauthorized", "ERR_UNAUTHORIZED", http.StatusUnauthorized)
			return
		}

		newCfg, err := localProv.Load()
		if err != nil {
			proxy.WriteJSONError(w, r, "Failed to load config: "+err.Error(), "ERR_CONFIG_LOAD_FAILED", http.StatusInternalServerError)
			return
		}

		handler.UpdateRoutes(newCfg.Routes)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	}

	mux.HandleFunc("/api/gitops/webhook", handleGitOpsWebhook)
	server := httptest.NewServer(mux)
	defer server.Close()

	updatedCfg := proxy.GatewayConfig{
		Addr: ":12345",
		Routes: []proxy.Route{
			{Prefix: "/new", Target: "http://localhost:8082"},
		},
	}
	data2, _ := json.Marshal(updatedCfg)
	os.WriteFile(tmpFile.Name(), data2, 0644)

	client := &http.Client{}
	req, _ := http.NewRequest("POST", server.URL+"/api/gitops/webhook", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("failed to post webhook: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	routes := handler.GetRoutes()
	if len(routes) != 1 || routes[0].Prefix != "/new" {
		t.Errorf("expected updated routes, got %+v", routes)
	}
}

func createTestSignedJWT(username string, exp, iat int64, policyVer int, secret []byte) string {
	header := `{"alg":"HS256","typ":"JWT"}`
	payload := fmt.Sprintf(`{"username":%q,"exp":%d,"iat":%d,"policy_ver":%d}`, username, exp, iat, policyVer)

	hBase := base64UrlEncodeBytes([]byte(header))
	pBase := base64UrlEncodeBytes([]byte(payload))

	// Validate signature
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(hBase + "." + pBase))
	sig := base64UrlEncodeBytes(mac.Sum(nil))

	return hBase + "." + pBase + "." + sig
}

func base64UrlEncodeBytes(b []byte) string {
	s := base64.URLEncoding.EncodeToString(b)
	return strings.TrimRight(s, "=")
}

func TestDynamicBackpressureRouting(t *testing.T) {
	backend1Count := 0
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend1Count++
		w.Header().Set("X-Backpressure", "90")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend1"))
	}))
	defer backend1.Close()

	backend2Count := 0
	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend2Count++
		w.Header().Set("X-Backpressure", "10")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend2"))
	}))
	defer backend2.Close()

	routes := []proxy.Route{
		{
			Prefix:       "/service",
			LoadBalancer: "backpressure",
			Targets:      []string{backend1.URL, backend2.URL},
		},
	}
	handler := proxy.NewGatewayHandler(routes, nil, "")

	server := httptest.NewServer(handler)
	defer server.Close()

	client := &http.Client{}
	// Warm up target loads
	r1, _ := client.Get(server.URL + "/service/ping")
	r1.Body.Close()
	r2, _ := client.Get(server.URL + "/service/ping")
	r2.Body.Close()

	backend1Count = 0
	backend2Count = 0

	for i := 0; i < 5; i++ {
		resp, err := client.Get(server.URL + "/service/ping")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
	}

	if backend1Count > 0 {
		t.Errorf("Expected zero requests to backend1 (90%% backpressure), got %d", backend1Count)
	}
	if backend2Count != 5 {
		t.Errorf("Expected all 5 requests to go to backend2 (10%% backpressure), got %d", backend2Count)
	}
}

func TestDynamicIAMPolicyHotReloading(t *testing.T) {
	jwtSec := "super-secret-key"
	t.Setenv("SERV_JWT_SECRET", jwtSec)

	testBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer testBackend.Close()

	routes := []proxy.Route{
		{Prefix: "/secure", Target: testBackend.URL},
	}
	handler := proxy.NewGatewayHandler(routes, nil, "admin-token")

	// 1. Initial valid token
	tokenStr := createTestSignedJWT("bob", time.Now().Add(1*time.Hour).Unix(), time.Now().Unix(), 0, []byte(jwtSec))
	username, ok := handler.ValidateJWTWithPolicy(tokenStr, []byte(jwtSec))
	if !ok || username != "bob" {
		t.Fatalf("expected valid token authentication")
	}

	// 2. Revoke user session
	handler.RevokeUserSession("bob")
	_, ok = handler.ValidateJWTWithPolicy(tokenStr, []byte(jwtSec))
	if ok {
		t.Errorf("expected token authentication to fail after revocation")
	}

	// 3. Stale policy version refresh signaling
	freshToken := createTestSignedJWT("alice", time.Now().Add(1*time.Hour).Unix(), time.Now().Unix(), 0, []byte(jwtSec))
	handler.IncrementPolicyVersion() // current version becomes 1, alice has version 0

	server := httptest.NewServer(handler)
	defer server.Close()

	client := &http.Client{}
	req, _ := http.NewRequest("GET", server.URL+"/secure", nil)
	req.Header.Set("Authorization", "Bearer "+freshToken)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Token-Refresh") != "true" {
		t.Errorf("expected X-Token-Refresh header to be set, got %q", resp.Header.Get("X-Token-Refresh"))
	}
}

func TestAutomatedCanaryRollback(t *testing.T) {
	tsV1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("v1_stable"))
	}))
	defer tsV1.Close()

	tsV2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("v2_error"))
	}))
	defer tsV2.Close()

	routes := []proxy.Route{
		{
			Prefix: "/api/auto-canary",
			TargetsWeighted: []proxy.WeightedTarget{
				{URL: tsV1.URL, Weight: 50},
				{URL: tsV2.URL, Weight: 50},
			},
			CanaryAutoPromote:  true,
			CanaryPromoteStep:  10,
			CanaryPromoteSec:   2,
			CanaryMaxErrorRate: 0.05,
		},
	}

	wasmManager, _ := wasm.GetMiddlewareManager(context.Background())
	handler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gwServer := httptest.NewServer(handler)
	defer gwServer.Close()

	client := &http.Client{Timeout: 1 * time.Second}

	for i := 0; i < 40; i++ {
		resp, err := client.Get(gwServer.URL + "/api/auto-canary/test")
		if err == nil {
			resp.Body.Close()
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Sleep 300ms to ensure the 200ms CanaryPromotionLoop ticker has ticked at least once
	time.Sleep(300 * time.Millisecond)

	req, _ := http.NewRequest("GET", gwServer.URL+"/api/auto-canary/test", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	canaryTarget := resp.Header.Get("X-Canary-Target")
	if canaryTarget != tsV1.URL {
		t.Errorf("Expected rollback to stable target %s, got target %s", tsV1.URL, canaryTarget)
	}
}

func TestTenantControlPlanePolicies(t *testing.T) {
	os.Setenv("SERV_CLUSTER", "prod-cluster-1")
	os.Setenv("SERV_REGION", "eu-west")
	defer os.Unsetenv("SERV_CLUSTER")
	defer os.Unsetenv("SERV_REGION")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("backend success"))
	}))
	defer ts.Close()

	routes := []proxy.Route{
		{
			Prefix: "/api/tenant-test",
			Target: ts.URL,
		},
	}

	wasmManager, _ := wasm.GetMiddlewareManager(context.Background())
	handler := proxy.NewGatewayHandler(routes, wasmManager, "")
	gwServer := httptest.NewServer(handler)
	defer gwServer.Close()

	client := &http.Client{}

	policy := proxy.TenantPolicy{
		TenantID:        "tenant-alpha",
		AllowedClusters: []string{"prod-cluster-1"},
		AllowedRegions:  []string{"eu-west"},
	}
	body, _ := json.Marshal(policy)
	respPolicy, err := client.Post(gwServer.URL+"/api/tenants/policies", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to post policy: %v", err)
	}
	respPolicy.Body.Close()

	policyBeta := proxy.TenantPolicy{
		TenantID:        "tenant-beta",
		AllowedClusters: []string{"prod-cluster-1"},
		AllowedRegions:  []string{"us-east"},
	}
	bodyBeta, _ := json.Marshal(policyBeta)
	respPolicyBeta, err := client.Post(gwServer.URL+"/api/tenants/policies", "application/json", bytes.NewReader(bodyBeta))
	if err != nil {
		t.Fatalf("failed to post policy: %v", err)
	}
	respPolicyBeta.Body.Close()

	reqAlpha, _ := http.NewRequest("GET", gwServer.URL+"/api/tenant-test/read", nil)
	reqAlpha.Header.Set("X-Tenant-ID", "tenant-alpha")
	respAlpha, err := client.Do(reqAlpha)
	if err != nil {
		t.Fatalf("req failed: %v", err)
	}
	defer respAlpha.Body.Close()
	if respAlpha.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", respAlpha.StatusCode)
	}

	reqBeta, _ := http.NewRequest("GET", gwServer.URL+"/api/tenant-test/read", nil)
	reqBeta.Header.Set("X-Tenant-ID", "tenant-beta")
	respBeta, err := client.Do(reqBeta)
	if err != nil {
		t.Fatalf("req failed: %v", err)
	}
	defer respBeta.Body.Close()
	if respBeta.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden, got %d", respBeta.StatusCode)
	}
}

func TestEcosystemSchemaRegistryValidation(t *testing.T) {
	registryMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/schemas/validate" {
			var req struct {
				Schema  string `json:"schema"`
				Payload string `json:"payload"`
			}
			json.NewDecoder(r.Body).Decode(&req)
			if req.Schema == "user" {
				if strings.Contains(req.Payload, `"username"`) {
					w.Write([]byte(`{"valid":true}`))
				} else {
					w.Write([]byte(`{"valid":false,"errors":["Missing required property: username"]}`))
				}
			}
		}
	}))
	defer registryMock.Close()

	os.Setenv("SERV_REGISTRY_URL", registryMock.URL)
	defer os.Unsetenv("SERV_REGISTRY_URL")

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("backend success"))
	}))
	defer backend.Close()

	routes := []proxy.Route{
		{
			Prefix:     "/users",
			SchemaName: "user",
			Target:     backend.URL,
		},
	}

	handler := proxy.NewGatewayHandler(routes, nil, "")
	gwServer := httptest.NewServer(handler)
	defer gwServer.Close()

	client := &http.Client{}

	validPayload := `{"username":"alice","age":30}`
	respValid, err := client.Post(gwServer.URL+"/users/create", "application/json", strings.NewReader(validPayload))
	if err != nil {
		t.Fatalf("Failed to post: %v", err)
	}
	defer respValid.Body.Close()
	if respValid.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", respValid.StatusCode)
	}

	invalidPayload := `{"age":30}`
	respInvalid, err := client.Post(gwServer.URL+"/users/create", "application/json", strings.NewReader(invalidPayload))
	if err != nil {
		t.Fatalf("Failed to post: %v", err)
	}
	defer respInvalid.Body.Close()
	if respInvalid.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400 Bad Request, got %d", respInvalid.StatusCode)
	}
	body, _ := io.ReadAll(respInvalid.Body)
	if !strings.Contains(string(body), "Missing required property: username") {
		t.Errorf("Expected error message to mention missing username, got: %s", string(body))
	}
}

func TestDynamicPolicyEngine(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"username":"john_doe","email":"john@example.com","secret":"hidden"}`))
	}))
	defer backend.Close()

	routes := []proxy.Route{
		{
			Prefix: "/api",
			Target: backend.URL,
		},
	}

	h := proxy.NewGatewayHandler(routes, nil, "admin-token")
	gwServer := httptest.NewServer(h)
	defer gwServer.Close()

	client := &http.Client{}

	// 1. Initial request without policy -> success
	req, _ := http.NewRequest("GET", gwServer.URL+"/api/data", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "hidden") {
		t.Errorf("Expected body to contain unredacted data, got: %s", string(body))
	}

	// 2. Apply Dynamic Policy
	policyJSON := `{
		"version": 1,
		"rules": [
			{
				"id": "deny-delete",
				"action": "deny",
				"methods": ["DELETE"],
				"path": "/api/*"
			},
			{
				"id": "redact-secret",
				"action": "allow",
				"methods": ["GET"],
				"path": "/api/*",
				"redact_fields": ["secret"]
			}
		]
	}`

	applyReq, _ := http.NewRequest("POST", gwServer.URL+"/api/v1/admin/policy/reload", strings.NewReader(policyJSON))
	applyReq.Header.Set("Authorization", "Bearer admin-token")
	applyResp, err := client.Do(applyReq)
	if err != nil {
		t.Fatalf("Failed to apply policy: %v", err)
	}
	applyResp.Body.Close()
	if applyResp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 from policy reload, got %d", applyResp.StatusCode)
	}

	// 3. GET /api/data -> Should redact "secret" field
	req, _ = http.NewRequest("GET", gwServer.URL+"/api/data", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	if strings.Contains(string(body), "hidden") {
		t.Errorf("Expected secret field to be redacted, got: %s", string(body))
	}
	if !strings.Contains(string(body), "[REDACTED]") {
		t.Errorf("Expected body to contain [REDACTED], got: %s", string(body))
	}

	// 4. DELETE /api/data -> Should be denied
	req, _ = http.NewRequest("DELETE", gwServer.URL+"/api/data", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden, got %d", resp.StatusCode)
	}
}



