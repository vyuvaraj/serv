// Package client implements the ServTunnel CLI client.
//
// The client connects to the relay server via WebSocket, registers a
// subdomain, and then proxies incoming tunnel requests to a local HTTP
// service. It provides colorful terminal output showing each proxied
// request in real-time.
package client

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"encoding/json"
	"servtunnel/pkg/inspector"
	"servtunnel/pkg/tunnel"

	"github.com/gorilla/websocket"
)

// Client is the ServTunnel tunnel client.
type Client struct {
	localAddr    string // e.g., "localhost:8080"
	relayURL     string // WebSocket URL of the relay
	subdomain    string // requested subdomain (empty for auto-assign)
	customDomain string // requested custom domain
	token        string // registration token
	conn         *websocket.Conn
	mu           sync.Mutex
	httpClient   *http.Client

	inspectPort  string               // local HTTP port for the inspector UI
	inspector    *inspector.Inspector // captures requests
	shareAuth    string               // credentials for basic auth sharing (user:pass)
	tcpPort      int                  // requested TCP relay port
	tcpConns     map[string]net.Conn  // active downstream TCP connections (session -> net.Conn)
	throttleBytesPerSec int64         // bandwidth limit in bytes/sec
	resumptionToken     string        // persistent tunnel session resumption token
}

// NewClient creates a new tunnel client.
func NewClient(localAddr, relayURL, subdomain, customDomain, token, inspectPort, shareAuth string) *Client {
	var insp *inspector.Inspector
	if inspectPort != "" && inspectPort != "0" {
		insp = inspector.New(100)
	}
	return &Client{
		localAddr:    localAddr,
		relayURL:     relayURL,
		subdomain:    subdomain,
		customDomain: customDomain,
		token:        token,
		inspectPort:  inspectPort,
		inspector:    insp,
		shareAuth:    shareAuth,
		tcpConns:     make(map[string]net.Conn),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // don't follow redirects
			},
		},
	}
}

// WithTCPPort configures the client to request a TCP tunnel.
func (c *Client) WithTCPPort(port int) *Client {
	c.tcpPort = port
	return c
}

// WithThrottle configures the bandwidth limit for the client.
func (c *Client) WithThrottle(rate string) *Client {
	c.throttleBytesPerSec = parseThrottle(rate)
	return c
}

func parseThrottle(rate string) int64 {
	rate = strings.TrimSpace(strings.ToLower(rate))
	if rate == "" {
		return 0
	}
	multiplier := int64(1)
	if strings.HasSuffix(rate, "k") {
		multiplier = 1024
		rate = strings.TrimSuffix(rate, "k")
	} else if strings.HasSuffix(rate, "m") {
		multiplier = 1024 * 1024
		rate = strings.TrimSuffix(rate, "m")
	}
	var val float64
	if _, err := fmt.Sscan(rate, &val); err == nil {
		return int64(val * float64(multiplier))
	}
	return 0
}

type ThrottledReader struct {
	R           io.Reader
	BytesPerSec int64
}

func (tr *ThrottledReader) Read(p []byte) (int, error) {
	start := time.Now()
	n, err := tr.R.Read(p)
	if n > 0 && tr.BytesPerSec > 0 {
		expectedDur := time.Duration(int64(n)) * time.Second / time.Duration(tr.BytesPerSec)
		actualDur := time.Since(start)
		if expectedDur > actualDur {
			time.Sleep(expectedDur - actualDur)
		}
	}
	return n, err
}

// Run connects to the relay and starts proxying. Blocks until interrupted.
func (c *Client) Run() error {
	fmt.Println()
	fmt.Println("  ╔═══════════════════════════════════════╗")
	fmt.Println("  ║         ServTunnel Client              ║")
	fmt.Println("  ╚═══════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  Local service:  http://%s\n", c.localAddr)
	fmt.Printf("  Relay server:   %s\n", c.relayURL)
	fmt.Println()

	if c.inspectPort != "" && c.inspector != nil {
		go c.startInspectorServer()
	}

	// Handle shutdown signals.
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stopChan
		fmt.Println("\n  Shutting down tunnel...")
		c.mu.Lock()
		if c.conn != nil {
			_ = c.conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			c.conn.Close()
		}
		c.mu.Unlock()
		os.Exit(0)
	}()

	backoff := 100 * time.Millisecond
	maxBackoff := 10 * time.Second

	for {
		fmt.Println("  Connecting...")
		var header http.Header
		if c.token != "" {
			header = make(http.Header)
			header.Set("Authorization", "Bearer "+c.token)
		}
		u := c.relayURL
		if c.token != "" {
			if strings.Contains(u, "?") {
				u += "&token=" + c.token
			} else {
				u += "?token=" + c.token
			}
		}

		dialer := &websocket.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: 45 * time.Second,
			ReadBufferSize:   256 * 1024,
			WriteBufferSize:  256 * 1024,
		}
		conn, _, err := dialer.Dial(u, header)
		if err != nil {
			fmt.Printf("  failed to connect to relay: %v\n", err)
			c.sleepWithJitter(backoff)
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		conn.SetReadLimit(50 * 1024 * 1024) // 50MB read limit

		c.mu.Lock()
		c.conn = conn
		c.mu.Unlock()

		// Send registration message.
		regMsg := tunnel.Envelope{
			Type: tunnel.TypeRegister,
			Control: &tunnel.ControlMessage{
				Subdomain:       c.subdomain,
				CustomDomain:    c.customDomain,
				SharingAuth:     c.shareAuth,
				TCPPort:         c.tcpPort,
				ResumptionToken: c.resumptionToken,
			},
		}
		if err := conn.WriteJSON(regMsg); err != nil {
			fmt.Printf("  failed to send registration: %v\n", err)
			conn.Close()
			c.sleepWithJitter(backoff)
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// Wait for confirmation.
		var regResp tunnel.Envelope
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if err := conn.ReadJSON(&regResp); err != nil {
			fmt.Printf("  failed to read registration response: %v\n", err)
			conn.Close()
			c.sleepWithJitter(backoff)
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		conn.SetReadDeadline(time.Time{}) // clear deadline

		if regResp.Type == tunnel.TypeError {
			conn.Close()
			errMsg := "unknown error"
			if regResp.Control != nil {
				errMsg = regResp.Control.Error
			}
			fmt.Printf("  registration failed: %s\n", errMsg)
			c.sleepWithJitter(backoff)
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		if regResp.Type != tunnel.TypeRegistered || regResp.Control == nil {
			conn.Close()
			fmt.Printf("  unexpected response type: %s\n", regResp.Type)
			c.sleepWithJitter(backoff)
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		c.mu.Lock()
		c.resumptionToken = regResp.Control.ResumptionToken
		c.subdomain = regResp.Control.Subdomain
		c.mu.Unlock()

		// Reset backoff on successful connection.
		backoff = 100 * time.Millisecond

		fmt.Printf("  ✓ Tunnel established!\n")
		fmt.Println()
		fmt.Printf("  Public URL:     %s\n", regResp.Control.PublicURL)
		fmt.Printf("  Subdomain:      %s\n", regResp.Control.Subdomain)
		if regResp.Control.CustomDomain != "" {
			fmt.Printf("  Custom Domain:  http://%s\n", regResp.Control.CustomDomain)
		}
		fmt.Println()
		fmt.Println("  ─────────────────────────────────────────")
		fmt.Println("  Forwarding requests... (Ctrl+C to stop)")
		fmt.Println("  ─────────────────────────────────────────")
		fmt.Println()

		keepaliveDone := make(chan struct{})
		go c.keepalive(conn, keepaliveDone)

		// Read and process requests.
		c.readLoop(conn)

		// Cleanup on connection drop.
		close(keepaliveDone)
		conn.Close()

		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()

		fmt.Println("  Connection lost, reconnecting...")
	}
}

func (c *Client) sleepWithJitter(d time.Duration) {
	// Add up to 20% jitter
	jitter := time.Duration(rand.Int63n(int64(d) / 5))
	time.Sleep(d + jitter)
}

// readLoop reads incoming tunnel requests and dispatches them.
func (c *Client) readLoop(conn *websocket.Conn) {
	for {
		var env tunnel.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			log.Printf("  Read error: %v", err)
			return
		}

		switch env.Type {
		case tunnel.TypeRequest:
			go c.handleRequest(conn, env)
		case tunnel.TypePing:
			c.mu.Lock()
			_ = conn.WriteJSON(tunnel.Envelope{Type: tunnel.TypePong})
			c.mu.Unlock()
		case tunnel.TypeError:
			errMsg := "unknown"
			if env.Control != nil {
				errMsg = env.Control.Error
			}
			log.Printf("  Server error: %s", errMsg)
		}
	}
}

// handleRequest proxies a single request to the local service.
func (c *Client) handleRequest(conn *websocket.Conn, env tunnel.Envelope) {
	if env.Request == nil {
		return
	}

	if env.Request.TCPData != "" {
		c.handleTCPTraffic(conn, env)
		return
	}

	start := time.Now()
	req := env.Request

	var inspectReqID string
	if c.inspector != nil {
		var reqBody string
		if req.Body != "" {
			reqBody = req.Body
		}
		inspectReqID = c.inspector.Record(inspector.Entry{
			Timestamp:      start,
			Method:         req.Method,
			Path:           req.Path,
			RequestHeaders: req.Headers,
			RequestBody:    reqBody,
		})
	}

	// Build local URL.
	localURL := fmt.Sprintf("http://%s%s", c.localAddr, req.Path)

	// Decode body.
	var bodyReader io.Reader
	if req.Body != "" {
		bodyBytes, err := base64.StdEncoding.DecodeString(req.Body)
		if err == nil {
			bodyReader = bytes.NewReader(bodyBytes)
			if c.throttleBytesPerSec > 0 {
				bodyReader = &ThrottledReader{R: bodyReader, BytesPerSec: c.throttleBytesPerSec}
			}
		}
	}

	// Create HTTP request.
	httpReq, err := http.NewRequest(req.Method, localURL, bodyReader)
	if err != nil {
		if c.inspector != nil && inspectReqID != "" {
			c.inspector.Update(inspectReqID, 502, nil, base64.StdEncoding.EncodeToString([]byte("failed to create request")), 0)
		}
		c.sendErrorResponse(conn, env.RequestID, 502, "failed to create request")
		return
	}

	// Copy headers, propagating traceparent for OTel.
	for k, v := range req.Headers {
		lk := strings.ToLower(k)
		// Skip hop-by-hop headers.
		if lk == "host" || lk == "connection" || lk == "upgrade" {
			continue
		}
		httpReq.Header.Set(k, v)
	}

	// Execute local request.
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		latency := time.Since(start)
		fmt.Printf("  %s %-6s %-30s → ERR  (%dms) %v\n",
			time.Now().Format("15:04:05"), req.Method, req.Path, latency.Milliseconds(), err)
		if c.inspector != nil && inspectReqID != "" {
			c.inspector.Update(inspectReqID, 502, nil, base64.StdEncoding.EncodeToString([]byte(err.Error())), latency.Milliseconds())
		}
		c.sendErrorResponse(conn, env.RequestID, 502, "local service error: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// Read response body.
	var respReader io.Reader = resp.Body
	if c.throttleBytesPerSec > 0 {
		respReader = &ThrottledReader{R: resp.Body, BytesPerSec: c.throttleBytesPerSec}
	}
	respBody, _ := io.ReadAll(respReader)
	var respBodyB64 string
	if len(respBody) > 0 {
		respBodyB64 = base64.StdEncoding.EncodeToString(respBody)
	}

	// Flatten response headers.
	respHeaders := make(map[string]string)
	for k, vals := range resp.Header {
		respHeaders[k] = vals[0]
	}

	// Flatten response trailers.
	respTrailers := make(map[string]string)
	for k, vals := range resp.Trailer {
		if len(vals) > 0 {
			respTrailers[k] = vals[0]
		}
	}

	latency := time.Since(start)

	if c.inspector != nil && inspectReqID != "" {
		c.inspector.Update(inspectReqID, resp.StatusCode, respHeaders, respBodyB64, latency.Milliseconds())
	}

	// Terminal output with color.
	statusColor := "\033[32m" // green
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		statusColor = "\033[33m" // yellow
	} else if resp.StatusCode >= 500 {
		statusColor = "\033[31m" // red
	}
	reset := "\033[0m"

	fmt.Printf("  %s %-6s %-30s → %s%d%s (%dms)\n",
		time.Now().Format("15:04:05"), req.Method, req.Path,
		statusColor, resp.StatusCode, reset, latency.Milliseconds())

	// Send response back through tunnel.
	respEnv := tunnel.Envelope{
		Type:      tunnel.TypeResponse,
		RequestID: env.RequestID,
		Response: &tunnel.TunnelResponse{
			StatusCode: resp.StatusCode,
			Headers:    respHeaders,
			Body:       respBodyB64,
			Trailers:   respTrailers,
		},
	}

	c.mu.Lock()
	_ = conn.WriteJSON(respEnv)
	c.mu.Unlock()
}

// sendErrorResponse sends an error response back through the tunnel.
func (c *Client) sendErrorResponse(conn *websocket.Conn, requestID string, status int, msg string) {
	body := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(`{"error":"%s"}`, msg)))
	respEnv := tunnel.Envelope{
		Type:      tunnel.TypeResponse,
		RequestID: requestID,
		Response: &tunnel.TunnelResponse{
			StatusCode: status,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       body,
		},
	}
	c.mu.Lock()
	_ = conn.WriteJSON(respEnv)
	c.mu.Unlock()
}

// keepalive sends periodic pings to keep the WebSocket connection alive.
func (c *Client) keepalive(conn *websocket.Conn, done chan struct{}) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			err := conn.WriteJSON(tunnel.Envelope{Type: tunnel.TypePing})
			c.mu.Unlock()
			if err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

// startInspectorServer runs the local HTTP server serving the Web UI and JSON endpoints.
func (c *Client) startInspectorServer() {
	port := c.inspectPort
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}

	fmt.Printf("  Inspector UI:   http://localhost%s\n", port)
	fmt.Println()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(inspectorHTML))
			return
		}
		if r.URL.Path == "/api/inspect" || r.URL.Path == "/api/inspect/" {
			c.inspector.HandleList(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/inspect/") {
			id := strings.TrimPrefix(r.URL.Path, "/api/inspect/")
			if strings.HasSuffix(id, "/diff") {
				id = strings.TrimSuffix(id, "/diff")
				c.inspector.HandleDiff(w, r, id)
				return
			}
			if strings.HasSuffix(id, "/replay") {
				id = strings.TrimSuffix(id, "/replay")
				c.handleLocalReplay(w, r, id)
				return
			}
			c.inspector.HandleGet(w, r, id)
			return
		}
		http.NotFound(w, r)
	})

	_ = http.ListenAndServe(port, mux)
}

// handleTCPTraffic proxies incoming TCP data chunks to the local TCP target,
// and relays responses back through the WebSocket connection to the server.
func (c *Client) handleTCPTraffic(conn *websocket.Conn, env tunnel.Envelope) {
	c.mu.Lock()
	localConn, ok := c.tcpConns[env.RequestID]
	c.mu.Unlock()

	if !ok {
		// Establish new connection to local target
		var err error
		localConn, err = net.Dial("tcp", c.localAddr)
		if err != nil {
			log.Printf("  Failed to dial local target %s: %v", c.localAddr, err)
			return
		}
		c.mu.Lock()
		c.tcpConns[env.RequestID] = localConn
		c.mu.Unlock()

		// Start background goroutine to read local TCP target responses and send back via websocket
		go func(requestID string, targetConn net.Conn) {
			defer func() {
				targetConn.Close()
				c.mu.Lock()
				delete(c.tcpConns, requestID)
				c.mu.Unlock()
			}()

			buf := make([]byte, 32*1024)
			for {
				n, err := targetConn.Read(buf)
				if err != nil {
					break
				}
				if n > 0 {
					payloadB64 := base64.StdEncoding.EncodeToString(buf[:n])
					respEnv := tunnel.Envelope{
						Type:      tunnel.TypeResponse,
						RequestID: requestID,
						Response: &tunnel.TunnelResponse{
							TCPData: payloadB64,
						},
					}
					c.mu.Lock()
					writeErr := conn.WriteJSON(respEnv)
					c.mu.Unlock()
					if writeErr != nil {
						break
					}
				}
			}
		}(env.RequestID, localConn)
	}

	// Write payload chunk to local target
	data, err := base64.StdEncoding.DecodeString(env.Request.TCPData)
	if err == nil && len(data) > 0 {
		_, _ = localConn.Write(data)
	}
}

func (c *Client) handleLocalReplay(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	entry, ok := c.inspector.Get(id)
	if !ok {
		http.Error(w, "Entry not found", http.StatusNotFound)
		return
	}

	start := time.Now()
	localURL := fmt.Sprintf("http://%s%s", c.localAddr, entry.Path)

	var bodyReader io.Reader
	if entry.RequestBody != "" {
		bodyBytes, err := base64.StdEncoding.DecodeString(entry.RequestBody)
		if err == nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}
	}

	httpReq, err := http.NewRequest(entry.Method, localURL, bodyReader)
	if err != nil {
		http.Error(w, "Failed to create request: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for k, v := range entry.RequestHeaders {
		httpReq.Header.Set(k, v)
	}
	httpReq.Header.Set("X-ServTunnel-Replayed", "true")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		http.Error(w, "Local service error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var respBodyB64 string
	if len(respBody) > 0 {
		respBodyB64 = base64.StdEncoding.EncodeToString(respBody)
	}

	respHeaders := make(map[string]string)
	for k, vals := range resp.Header {
		respHeaders[k] = vals[0]
	}

	latency := time.Since(start)

	newID := c.inspector.Record(inspector.Entry{
		Timestamp:      start,
		Method:         entry.Method,
		Path:           entry.Path,
		RequestHeaders: entry.RequestHeaders,
		RequestBody:    entry.RequestBody,
	})
	c.inspector.Update(newID, resp.StatusCode, respHeaders, respBodyB64, latency.Milliseconds())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "success",
		"message":     "request replayed successfully",
		"original_id": id,
		"new_id":      newID,
		"status_code": resp.StatusCode,
		"latency_ms":  latency.Milliseconds(),
	})
}

// GetInspector returns the unexported inspector instance.
func (c *Client) GetInspector() *inspector.Inspector {
	return c.inspector
}

// HandleLocalReplay allows calling handleLocalReplay from other packages (tests).
func (c *Client) HandleLocalReplay(w http.ResponseWriter, r *http.Request, id string) {
	c.handleLocalReplay(w, r, id)
}

