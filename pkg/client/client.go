// Package client implements the ServTunnel CLI client.
//
// The client connects to the relay server via WebSocket, registers a
// subdomain, and then proxies incoming tunnel requests to a local HTTP
// service. It provides colorful terminal output showing each proxied
// request in real-time.
package client

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

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
}

// NewClient creates a new tunnel client.
func NewClient(localAddr, relayURL, subdomain, customDomain, token, inspectPort string) *Client {
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
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // don't follow redirects
			},
		},
	}
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

		conn, _, err := websocket.DefaultDialer.Dial(u, header)
		if err != nil {
			fmt.Printf("  failed to connect to relay: %v\n", err)
			c.sleepWithJitter(backoff)
			backoff = backoff * 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		c.mu.Lock()
		c.conn = conn
		c.mu.Unlock()

		// Send registration message.
		regMsg := tunnel.Envelope{
			Type: tunnel.TypeRegister,
			Control: &tunnel.ControlMessage{
				Subdomain:    c.subdomain,
				CustomDomain: c.customDomain,
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
			bodyReader = strings.NewReader(string(bodyBytes))
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
	respBody, _ := io.ReadAll(resp.Body)
	var respBodyB64 string
	if len(respBody) > 0 {
		respBodyB64 = base64.StdEncoding.EncodeToString(respBody)
	}

	// Flatten response headers.
	respHeaders := make(map[string]string)
	for k, vals := range resp.Header {
		respHeaders[k] = vals[0]
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
			c.inspector.HandleGet(w, r, id)
			return
		}
		http.NotFound(w, r)
	})

	_ = http.ListenAndServe(port, mux)
}

