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
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"servtunnel/pkg/tunnel"

	"github.com/gorilla/websocket"
)

// Client is the ServTunnel tunnel client.
type Client struct {
	localAddr string // e.g., "localhost:8080"
	relayURL  string // WebSocket URL of the relay
	subdomain string // requested subdomain (empty for auto-assign)
	conn      *websocket.Conn
	mu        sync.Mutex
	httpClient *http.Client
}

// NewClient creates a new tunnel client.
func NewClient(localAddr, relayURL, subdomain string) *Client {
	return &Client{
		localAddr: localAddr,
		relayURL:  relayURL,
		subdomain: subdomain,
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
	fmt.Println("  Connecting...")

	// Connect to relay.
	conn, _, err := websocket.DefaultDialer.Dial(c.relayURL, nil)
	if err != nil {
		return fmt.Errorf("failed to connect to relay: %w", err)
	}
	c.conn = conn

	// Send registration.
	regMsg := tunnel.Envelope{
		Type: tunnel.TypeRegister,
		Control: &tunnel.ControlMessage{
			Subdomain: c.subdomain,
		},
	}
	if err := conn.WriteJSON(regMsg); err != nil {
		conn.Close()
		return fmt.Errorf("failed to send registration: %w", err)
	}

	// Wait for confirmation.
	var regResp tunnel.Envelope
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err := conn.ReadJSON(&regResp); err != nil {
		conn.Close()
		return fmt.Errorf("failed to read registration response: %w", err)
	}
	conn.SetReadDeadline(time.Time{})

	if regResp.Type == tunnel.TypeError {
		conn.Close()
		errMsg := "unknown error"
		if regResp.Control != nil {
			errMsg = regResp.Control.Error
		}
		return fmt.Errorf("registration failed: %s", errMsg)
	}

	if regResp.Type != tunnel.TypeRegistered || regResp.Control == nil {
		conn.Close()
		return fmt.Errorf("unexpected response type: %s", regResp.Type)
	}

	fmt.Printf("  ✓ Tunnel established!\n")
	fmt.Println()
	fmt.Printf("  Public URL:     %s\n", regResp.Control.PublicURL)
	fmt.Printf("  Subdomain:      %s\n", regResp.Control.Subdomain)
	fmt.Println()
	fmt.Println("  ─────────────────────────────────────────")
	fmt.Println("  Forwarding requests... (Ctrl+C to stop)")
	fmt.Println("  ─────────────────────────────────────────")
	fmt.Println()

	// Handle shutdown signals.
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stopChan
		fmt.Println("\n  Shutting down tunnel...")
		c.mu.Lock()
		conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		c.mu.Unlock()
		conn.Close()
		os.Exit(0)
	}()

	// Start keepalive.
	go c.keepalive()

	// Read and process requests.
	c.readLoop()
	return nil
}

// readLoop reads incoming tunnel requests and dispatches them.
func (c *Client) readLoop() {
	for {
		var env tunnel.Envelope
		if err := c.conn.ReadJSON(&env); err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			log.Printf("  Read error: %v", err)
			return
		}

		switch env.Type {
		case tunnel.TypeRequest:
			go c.handleRequest(env)
		case tunnel.TypePing:
			c.mu.Lock()
			_ = c.conn.WriteJSON(tunnel.Envelope{Type: tunnel.TypePong})
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
func (c *Client) handleRequest(env tunnel.Envelope) {
	if env.Request == nil {
		return
	}

	start := time.Now()
	req := env.Request

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
		c.sendErrorResponse(env.RequestID, 502, "failed to create request")
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
		c.sendErrorResponse(env.RequestID, 502, "local service error: "+err.Error())
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
	_ = c.conn.WriteJSON(respEnv)
	c.mu.Unlock()
}

// sendErrorResponse sends an error response back through the tunnel.
func (c *Client) sendErrorResponse(requestID string, status int, msg string) {
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
	_ = c.conn.WriteJSON(respEnv)
	c.mu.Unlock()
}

// keepalive sends periodic pings to keep the WebSocket connection alive.
func (c *Client) keepalive() {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		err := c.conn.WriteJSON(tunnel.Envelope{Type: tunnel.TypePing})
		c.mu.Unlock()
		if err != nil {
			return
		}
	}
}
