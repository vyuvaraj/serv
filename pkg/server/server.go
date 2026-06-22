// Package server implements the ServTunnel relay server.
//
// The relay accepts WebSocket connections from tunnel clients and HTTP
// requests from the public internet. It routes incoming HTTP requests to
// the correct tunnel client based on the subdomain extracted from the
// Host header (e.g., "abc123.servverse.net" → tunnel "abc123").
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"servtunnel/pkg/inspector"
	"servtunnel/pkg/otel"
	"servtunnel/pkg/tunnel"

	"github.com/gorilla/websocket"
	"github.com/vyuvaraj/ServShared"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// pendingRequest tracks an in-flight request waiting for a tunnel response.
type pendingRequest struct {
	ch    chan tunnel.Envelope
	start time.Time
}

// tunnelConn represents a connected tunnel client.
type tunnelConn struct {
	subdomain string
	conn      *websocket.Conn
	mu        sync.Mutex // protects writes to conn
	pending   sync.Map   // requestID → *pendingRequest
}

// Server is the ServTunnel relay server.
type Server struct {
	addr      string
	baseDomain string // e.g., "servverse.net" or "localhost"
	inspector *inspector.Inspector
	httpSrv   *http.Server

	mu      sync.RWMutex
	tunnels map[string]*tunnelConn // subdomain → tunnelConn
}

// NewServer creates a new relay server.
func NewServer(addr, baseDomain string, insp *inspector.Inspector) *Server {
	return &Server{
		addr:       addr,
		baseDomain: baseDomain,
		inspector:  insp,
		tunnels:    make(map[string]*tunnelConn),
	}
}

// Start begins listening. It blocks until the server is shut down.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Management endpoints (accessed directly, not via subdomain)
	mux.HandleFunc("/healthz", ServShared.HealthzHandler)
	mux.HandleFunc("/readyz", ServShared.ReadyzHandler)
	mux.HandleFunc("/ws/connect", s.handleWebSocket)
	mux.HandleFunc("/api/tunnels", s.handleListTunnels)
	mux.HandleFunc("/api/inspect", s.inspector.HandleList)
	mux.HandleFunc("/api/inspect/", s.handleInspectEntry)

	// Catch-all: route by subdomain in Host header
	mux.HandleFunc("/", s.handleTunnelRequest)

	s.httpSrv = &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	log.Printf("ServTunnel relay listening on %s (base domain: %s)", s.addr, s.baseDomain)
	return s.httpSrv.ListenAndServe()
}

// Shutdown gracefully stops the relay server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv != nil {
		return s.httpSrv.Shutdown(ctx)
	}
	return nil
}

// handleWebSocket upgrades a client connection and manages its lifecycle.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	// Wait for registration message.
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var env tunnel.Envelope
	if err := conn.ReadJSON(&env); err != nil {
		log.Printf("Failed to read registration: %v", err)
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{}) // clear deadline

	if env.Type != tunnel.TypeRegister || env.Control == nil {
		writeWSError(conn, "expected register message")
		conn.Close()
		return
	}

	subdomain := sanitizeSubdomain(env.Control.Subdomain)
	if subdomain == "" {
		subdomain = generateSubdomain()
	}

	// Check for conflicts.
	s.mu.Lock()
	if _, exists := s.tunnels[subdomain]; exists {
		s.mu.Unlock()
		writeWSError(conn, fmt.Sprintf("subdomain %q already in use", subdomain))
		conn.Close()
		return
	}

	tc := &tunnelConn{
		subdomain: subdomain,
		conn:      conn,
	}
	s.tunnels[subdomain] = tc
	s.mu.Unlock()

	publicURL := fmt.Sprintf("http://%s.%s%s", subdomain, s.baseDomain, s.addr)
	log.Printf("Tunnel registered: %s → %s", subdomain, publicURL)

	// Send confirmation.
	tc.mu.Lock()
	_ = conn.WriteJSON(tunnel.Envelope{
		Type: tunnel.TypeRegistered,
		Control: &tunnel.ControlMessage{
			Subdomain: subdomain,
			PublicURL: publicURL,
		},
	})
	tc.mu.Unlock()

	// Read loop: process responses and pings from the client.
	s.readLoop(tc)

	// Cleanup on disconnect.
	s.mu.Lock()
	delete(s.tunnels, subdomain)
	s.mu.Unlock()
	conn.Close()
	log.Printf("Tunnel disconnected: %s", subdomain)
}

// readLoop reads messages from a tunnel client.
func (s *Server) readLoop(tc *tunnelConn) {
	for {
		var env tunnel.Envelope
		if err := tc.conn.ReadJSON(&env); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("Tunnel %s read error: %v", tc.subdomain, err)
			}
			return
		}

		switch env.Type {
		case tunnel.TypeResponse:
			if val, ok := tc.pending.LoadAndDelete(env.RequestID); ok {
				pr := val.(*pendingRequest)
				pr.ch <- env
			}
		case tunnel.TypePong:
			// keepalive acknowledged
		default:
			log.Printf("Tunnel %s: unexpected message type %s", tc.subdomain, env.Type)
		}
	}
}

// handleTunnelRequest receives an external HTTP request and forwards it
// through the tunnel to the client's local service.
func (s *Server) handleTunnelRequest(w http.ResponseWriter, r *http.Request) {
	subdomain := s.extractSubdomain(r.Host)
	if subdomain == "" {
		// Not a tunnel request — show a landing message.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"service": "ServTunnel",
			"status":  "running",
			"hint":    "Connect via: servtunnel client <port> --relay ws://" + r.Host + "/ws/connect",
		})
		return
	}

	span := otel.StartSpan("tunnel.proxy", r.Header.Get("traceparent"))

	s.mu.RLock()
	tc, exists := s.tunnels[subdomain]
	s.mu.RUnlock()

	if !exists {
		otel.EndSpan(span, fmt.Errorf("tunnel not found: %s", subdomain), nil)
		http.Error(w, fmt.Sprintf(`{"error":"tunnel %q not found"}`, subdomain), http.StatusBadGateway)
		return
	}

	start := time.Now()

	// Read request body.
	var bodyB64 string
	if r.Body != nil {
		bodyBytes, _ := io.ReadAll(r.Body)
		if len(bodyBytes) > 0 {
			bodyB64 = base64.StdEncoding.EncodeToString(bodyBytes)
		}
	}

	// Flatten headers (single value per key for simplicity).
	headers := make(map[string]string)
	for k, vals := range r.Header {
		headers[k] = vals[0]
	}

	requestID := otel.GenerateSpanID()
	env := tunnel.Envelope{
		Type:      tunnel.TypeRequest,
		RequestID: requestID,
		Request: &tunnel.TunnelRequest{
			Method:  r.Method,
			Path:    r.URL.RequestURI(),
			Headers: headers,
			Body:    bodyB64,
		},
	}

	// Create pending request channel.
	pr := &pendingRequest{
		ch:    make(chan tunnel.Envelope, 1),
		start: start,
	}
	tc.pending.Store(requestID, pr)
	defer tc.pending.Delete(requestID)

	// Send request to client.
	tc.mu.Lock()
	err := tc.conn.WriteJSON(env)
	tc.mu.Unlock()
	if err != nil {
		otel.EndSpan(span, err, nil)
		http.Error(w, `{"error":"failed to send request to tunnel client"}`, http.StatusBadGateway)
		return
	}

	// Wait for response with timeout.
	select {
	case resp := <-pr.ch:
		latency := time.Since(start)
		if resp.Response == nil {
			otel.EndSpan(span, fmt.Errorf("empty response"), nil)
			http.Error(w, `{"error":"empty response from tunnel"}`, http.StatusBadGateway)
			return
		}

		// Write response headers.
		for k, v := range resp.Response.Headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(resp.Response.StatusCode)

		// Write response body.
		if resp.Response.Body != "" {
			bodyBytes, err := base64.StdEncoding.DecodeString(resp.Response.Body)
			if err == nil {
				w.Write(bodyBytes)
			}
		}

		// Record in inspector.
		reqHeaders := make(map[string]string)
		for k, v := range headers {
			reqHeaders[k] = v
		}
		s.inspector.Record(inspector.Entry{
			Method:          r.Method,
			Path:            r.URL.RequestURI(),
			RequestHeaders:  reqHeaders,
			RequestBody:     bodyB64,
			StatusCode:      resp.Response.StatusCode,
			ResponseHeaders: resp.Response.Headers,
			ResponseBody:    resp.Response.Body,
			LatencyMs:       latency.Milliseconds(),
			Subdomain:       subdomain,
		})

		otel.EndSpan(span, nil, map[string]interface{}{
			"http.method":      r.Method,
			"http.path":        r.URL.Path,
			"http.status_code": resp.Response.StatusCode,
			"tunnel.subdomain": subdomain,
			"tunnel.latency_ms": latency.Milliseconds(),
		})

	case <-time.After(30 * time.Second):
		otel.EndSpan(span, fmt.Errorf("timeout"), nil)
		http.Error(w, `{"error":"tunnel request timed out (30s)"}`, http.StatusGatewayTimeout)
	}
}

// handleListTunnels returns all active tunnel connections.
func (s *Server) handleListTunnels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	tunnels := make([]map[string]string, 0, len(s.tunnels))
	for sub := range s.tunnels {
		tunnels = append(tunnels, map[string]string{
			"subdomain":  sub,
			"public_url": fmt.Sprintf("http://%s.%s%s", sub, s.baseDomain, s.addr),
		})
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tunnels": tunnels,
		"count":   len(tunnels),
	})
}

// handleInspectEntry routes /api/inspect/{id} to the inspector.
func (s *Server) handleInspectEntry(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, `{"error":"missing entry id"}`, http.StatusBadRequest)
		return
	}
	s.inspector.HandleGet(w, r, parts[2])
}

// extractSubdomain pulls the subdomain from a Host header.
// For "abc123.servverse.net:8443" with baseDomain "servverse.net", returns "abc123".
// For "localhost:8443" with baseDomain "localhost", returns "" (no subdomain).
func (s *Server) extractSubdomain(host string) string {
	// Strip port.
	h := host
	if idx := strings.LastIndex(h, ":"); idx != -1 {
		h = h[:idx]
	}

	// Check if host ends with the base domain.
	if !strings.HasSuffix(h, s.baseDomain) {
		return ""
	}

	// Extract prefix before base domain.
	prefix := strings.TrimSuffix(h, s.baseDomain)
	prefix = strings.TrimSuffix(prefix, ".")
	if prefix == "" {
		return ""
	}

	// Only take the first label (in case of a.b.basedomain).
	parts := strings.Split(prefix, ".")
	return parts[len(parts)-1]
}

// sanitizeSubdomain cleans a requested subdomain name.
func sanitizeSubdomain(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var result []byte
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			result = append(result, c)
		}
	}
	if len(result) > 32 {
		result = result[:32]
	}
	return string(result)
}

// generateSubdomain creates a random 8-character subdomain.
func generateSubdomain() string {
	return otel.GenerateSpanID()[:8]
}

func writeWSError(conn *websocket.Conn, msg string) {
	_ = conn.WriteJSON(tunnel.Envelope{
		Type:    tunnel.TypeError,
		Control: &tunnel.ControlMessage{Error: msg},
	})
}
