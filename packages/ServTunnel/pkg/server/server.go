// Package server implements the ServTunnel relay server.
//
// The relay accepts WebSocket connections from tunnel clients and HTTP
// requests from the public internet. It routes incoming HTTP requests to
// the correct tunnel client based on the subdomain extracted from the
// Host header (e.g., "abc123.servverse.net" → tunnel "abc123").
package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"servtunnel/pkg/inspector"
	"servtunnel/pkg/otel"
	"servtunnel/pkg/tunnel"

	"github.com/gorilla/websocket"
	"github.com/vyuvaraj/ServShared"
	"golang.org/x/crypto/acme/autocert"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

var (
	EnterpriseCheckIPAllowlist = func(r *http.Request, subdomain string) error { return nil }
	EnterpriseVerifySSO        = func(r *http.Request, subdomain string) error { return nil }
	EnterpriseAuditLog         = func(action string, subdomain string, details map[string]interface{}) {}
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  256 * 1024,
	WriteBufferSize: 256 * 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// pendingRequest tracks an in-flight request waiting for a tunnel response.
type pendingRequest struct {
	ch    chan tunnel.Envelope
	start time.Time
}

// RateLimiter is a thread-safe token-bucket rate limiter.
type RateLimiter struct {
	mu           sync.Mutex
	rate         float64 // tokens per second
	capacity     float64 // max tokens
	tokens       float64
	lastRefilled time.Time
}

// NewRateLimiter creates a new RateLimiter.
func NewRateLimiter(rate, capacity float64) *RateLimiter {
	return &RateLimiter{
		rate:         rate,
		capacity:     capacity,
		tokens:       capacity,
		lastRefilled: time.Now(),
	}
}

// Allow returns true if a token can be consumed.
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefilled).Seconds()
	rl.lastRefilled = now

	rl.tokens += elapsed * rl.rate
	if rl.tokens > rl.capacity {
		rl.tokens = rl.capacity
	}

	if rl.tokens >= 1.0 {
		rl.tokens -= 1.0
		return true
	}
	return false
}

// tunnelConn represents a connected tunnel client.
type tunnelConn struct {
	subdomain    string
	customDomain string
	conn         *websocket.Conn
	mu           sync.Mutex // protects writes to conn
	pending      sync.Map   // requestID → *pendingRequest
	limiter      *RateLimiter
	bytesRead    int64
	bytesWritten int64
	quotaLimit   int64
	sharingAuth  string
	connections  int64
	startTime    time.Time
}

// Server is the ServTunnel relay server.
type Server struct {
	addr          string
	baseDomain    string // e.g., "servverse.net" or "localhost"
	inspector     *inspector.Inspector
	httpSrv       *http.Server
	jwtSecret     string
	staticToken   string
	idleTimeout   time.Duration
	reservedSubdomains map[string]string // subdomain -> token

	mu            sync.RWMutex
	tunnels       map[string]*tunnelConn // subdomain → tunnelConn
	customTunnels map[string]*tunnelConn // customDomain → tunnelConn
	tcpListeners  map[int]net.Listener   // port → net.Listener (for active TCP tunnels)
	resumptionTokens map[string]string   // subdomain → resumptionToken
	federationPeers  []string
}

// NewServer creates a new relay server.
func NewServer(addr, baseDomain string, insp *inspector.Inspector) *Server {
	idleTimeoutStr := os.Getenv("SERVTUNNEL_IDLE_TIMEOUT")
	idleTimeout := 60 * time.Second
	if d, err := time.ParseDuration(idleTimeoutStr); err == nil {
		idleTimeout = d
	} else if secs, err := strconv.Atoi(idleTimeoutStr); err == nil {
		idleTimeout = time.Duration(secs) * time.Second
	}

	reserved := make(map[string]string)
	if envReserved := os.Getenv("SERVTUNNEL_RESERVED_SUBDOMAINS"); envReserved != "" {
		parts := strings.Split(envReserved, ",")
		for _, part := range parts {
			subParts := strings.SplitN(part, ":", 2)
			if len(subParts) == 2 {
				sub := sanitizeSubdomain(subParts[0])
				token := strings.TrimSpace(subParts[1])
				if sub != "" && token != "" {
					reserved[sub] = token
				}
			}
		}
	}

	peersStr := os.Getenv("SERVTUNNEL_FEDERATION_PEERS")
	var peers []string
	if peersStr != "" {
		for _, p := range strings.Split(peersStr, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				peers = append(peers, strings.TrimSuffix(p, "/"))
			}
		}
	}

	return &Server{
		addr:               addr,
		baseDomain:         baseDomain,
		inspector:          insp,
		jwtSecret:          os.Getenv("SERVTUNNEL_JWT_SECRET"),
		staticToken:        os.Getenv("SERVTUNNEL_TOKEN"),
		idleTimeout:        idleTimeout,
		reservedSubdomains: reserved,
		tunnels:            make(map[string]*tunnelConn),
		customTunnels:      make(map[string]*tunnelConn),
		tcpListeners:       make(map[int]net.Listener),
		resumptionTokens:   make(map[string]string),
		federationPeers:    peers,
	}
}

// Start begins listening. It blocks until the server is shut down.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Management endpoints (accessed directly, not via subdomain)
	mux.HandleFunc("/healthz", ServShared.HealthzHandler)
	mux.HandleFunc("/readyz", ServShared.ReadyzHandler)
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servtunnel", "1.0.0"))
	mux.HandleFunc("/api/v1/version", ServShared.VersionHandler("servtunnel", "1.0.0"))
	mux.HandleFunc("/ws/connect", s.handleWebSocket)
	mux.HandleFunc("/api/tunnels", s.handleListTunnels)
	mux.HandleFunc("/api/tunnels/", s.handleTunnelsSubroutes)
	mux.HandleFunc("/api/inspect", s.inspector.HandleList)
	mux.HandleFunc("/api/inspect/", s.handleInspectEntry)

	// Catch-all: route by subdomain in Host header
	mux.HandleFunc("/", s.handleTunnelRequest)

	// Wrapper handler for /api/v1/ prefix rewriting (V1.1 support)
	v1Wrapper := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			r.URL.Path = "/api/" + strings.TrimPrefix(r.URL.Path, "/api/v1/")
		}
		mux.ServeHTTP(w, r)
	})

	// Wrap in ServShared middleware: Trace -> RateLimit -> CORS -> MaxBytes -> Auth -> Tenant -> v1Wrapper
	rateLimiter := ServShared.RateLimitMiddleware
	if flag.Lookup("test.v") != nil {
		rateLimiter = func(next http.Handler) http.Handler {
			return next
		}
	}

	handlerChain := ServShared.TraceMiddleware("servtunnel",
		rateLimiter(
			ServShared.CORSMiddleware(
				ServShared.MaxBytesMiddleware(10*1024*1024)(
					ServShared.AuthMiddleware(
						ServShared.TenantMiddleware(v1Wrapper),
					),
				),
			),
		),
	)

	// Custom dispatcher: management APIs (/api/...) go through handlerChain,
	// WebSocket (/ws/connect) and tunnel proxy requests (/) go directly to original handler.
	dispatcher := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			handlerChain.ServeHTTP(w, r)
			return
		}
		ServShared.AuthMiddleware(mux).ServeHTTP(w, r)
	})

	s.httpSrv = &http.Server{
		Addr:    s.addr,
		Handler: dispatcher,
	}

	tlsCert := os.Getenv("SERVTUNNEL_TLS_CERT")
	tlsKey := os.Getenv("SERVTUNNEL_TLS_KEY")
	autocertEnabled := os.Getenv("SERVTUNNEL_AUTOCERT") == "true"
	autocertDomain := os.Getenv("SERVTUNNEL_AUTOCERT_DOMAIN")

	if autocertEnabled && autocertDomain != "" {
		certManager := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(autocertDomain, "*."+autocertDomain),
			Cache:      autocert.DirCache("certs"),
		}
		s.httpSrv.TLSConfig = certManager.TLSConfig()
		log.Printf("ServTunnel relay listening with Auto-TLS (Let's Encrypt) on %s (base domain: %s)", s.addr, s.baseDomain)
		return s.httpSrv.ListenAndServeTLS("", "")
	}

	if tlsCert != "" && tlsKey != "" {
		log.Printf("ServTunnel relay listening with TLS on %s (base domain: %s)", s.addr, s.baseDomain)
		return s.httpSrv.ListenAndServeTLS(tlsCert, tlsKey)
	}

	log.Printf("ServTunnel relay listening on %s (base domain: %s)", s.addr, s.baseDomain)
	return s.httpSrv.ListenAndServe()
}

// Shutdown gracefully stops the relay server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	for _, tc := range s.tunnels {
		tc.mu.Lock()
		if tc.conn != nil {
			_ = tc.conn.Close()
		}
		tc.mu.Unlock()
	}
	for port, l := range s.tcpListeners {
		_ = l.Close()
		delete(s.tcpListeners, port)
	}
	s.mu.Unlock()

	if s.httpSrv != nil {
		return s.httpSrv.Shutdown(ctx)
	}
	return nil
}

// authenticate checks the incoming request against SERVTUNNEL_JWT_SECRET or SERVTUNNEL_TOKEN.
func (s *Server) authenticate(r *http.Request) error {
	secret := s.jwtSecret
	token := s.staticToken
	if secret == "" && token == "" {
		return nil
	}

	var tokenStr string
	if authHeader := r.Header.Get("Authorization"); authHeader != "" {
		var err error
		tokenStr, err = ServShared.ExtractTokenFromHeader(authHeader)
		if err != nil {
			tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
			tokenStr = strings.TrimSpace(tokenStr)
		}
	}
	if tokenStr == "" {
		tokenStr = r.URL.Query().Get("token")
	}

	if tokenStr == "" {
		return fmt.Errorf("missing token")
	}

	if token != "" && tokenStr == token {
		return nil
	}

	if secret != "" {
		validator := ServShared.NewAuthValidator(secret, "", "")
		_, err := validator.ValidateToken(tokenStr)
		if err == nil {
			return nil
		}
		return fmt.Errorf("invalid token: %w", err)
	}

	return fmt.Errorf("unauthorized")
}

// handleWebSocket upgrades a client connection and manages its lifecycle.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if err := s.authenticate(r); err != nil {
		log.Printf("Authentication failed: %v", err)
		s.writeJSONError(w, r, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	conn.SetReadLimit(50 * 1024 * 1024) // 50MB read limit

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
	
	// Check for resumption
	resuming := false
	if env.Control.ResumptionToken != "" && subdomain != "" {
		s.mu.Lock()
		token, exists := s.resumptionTokens[subdomain]
		if exists && token == env.Control.ResumptionToken {
			resuming = true
			if oldTc, active := s.tunnels[subdomain]; active {
				oldTc.mu.Lock()
				if oldTc.conn != nil {
					_ = oldTc.conn.Close()
				}
				oldTc.mu.Unlock()
				delete(s.tunnels, subdomain)
				if oldTc.customDomain != "" {
					delete(s.customTunnels, oldTc.customDomain)
				}
			}
		}
		s.mu.Unlock()
	}

	if !resuming {
		if subdomain == "" {
			for {
				subdomain = generateSubdomain()
				s.mu.RLock()
				_, exists := s.tunnels[subdomain]
				_, isReserved := s.reservedSubdomains[subdomain]
				s.mu.RUnlock()
				if !exists && !isReserved {
					break
				}
			}
		} else {
			// Check if the subdomain is reserved
			s.mu.RLock()
			expectedToken, isReserved := s.reservedSubdomains[subdomain]
			s.mu.RUnlock()
			if isReserved {
				var tokenUsed string
				if authHeader := r.Header.Get("Authorization"); authHeader != "" {
					var err error
					tokenUsed, err = ServShared.ExtractTokenFromHeader(authHeader)
					if err != nil {
						tokenUsed = strings.TrimPrefix(authHeader, "Bearer ")
						tokenUsed = strings.TrimSpace(tokenUsed)
					}
				}
				if tokenUsed == "" {
					tokenUsed = r.URL.Query().Get("token")
				}
				if tokenUsed != expectedToken {
					writeWSError(conn, fmt.Sprintf("subdomain %q is reserved", subdomain))
					conn.Close()
					return
				}
			}
		}
	}

	customDomain := env.Control.CustomDomain
	if customDomain != "" {
		customDomain = strings.ToLower(strings.TrimSpace(customDomain))
		if idx := strings.LastIndex(customDomain, ":"); idx != -1 {
			customDomain = customDomain[:idx]
		}
		if customDomain == "localhost" || strings.HasSuffix(customDomain, s.baseDomain) {
			writeWSError(conn, fmt.Sprintf("invalid custom domain %q", customDomain))
			conn.Close()
			return
		}
	}

	// Check for conflicts.
	s.mu.Lock()
	if _, exists := s.tunnels[subdomain]; exists {
		s.mu.Unlock()
		writeWSError(conn, fmt.Sprintf("subdomain %q already in use", subdomain))
		conn.Close()
		return
	}
	if customDomain != "" {
		if _, exists := s.customTunnels[customDomain]; exists {
			s.mu.Unlock()
			writeWSError(conn, fmt.Sprintf("custom domain %q already in use", customDomain))
			conn.Close()
			return
		}
	}

	tc := &tunnelConn{
		subdomain:    subdomain,
		customDomain: customDomain,
		conn:         conn,
		limiter:      NewRateLimiter(50, 100),
		quotaLimit:   100 * 1024 * 1024, // 100 MB default quota
		sharingAuth:  env.Control.SharingAuth,
		startTime:    time.Now(),
	}

	tcpPort := env.Control.TCPPort
	if tcpPort > 0 {
		if _, exists := s.tcpListeners[tcpPort]; exists {
			s.mu.Unlock()
			writeWSError(conn, fmt.Sprintf("TCP port %d already in use", tcpPort))
			conn.Close()
			return
		}
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", tcpPort))
		if err != nil {
			s.mu.Unlock()
			writeWSError(conn, fmt.Sprintf("failed to listen on TCP port %d: %v", tcpPort, err))
			conn.Close()
			return
		}
		s.tcpListeners[tcpPort] = l

		go func(tc *tunnelConn, listener net.Listener, port int) {
			defer listener.Close()
			for {
				clientConn, err := listener.Accept()
				if err != nil {
					return
				}
				go s.handleTCPConnection(tc, clientConn)
			}
		}(tc, l, tcpPort)
	}

	s.tunnels[subdomain] = tc
	if customDomain != "" {
		s.customTunnels[customDomain] = tc
	}
	s.mu.Unlock()

	publicURL := fmt.Sprintf("http://%s.%s%s", subdomain, s.baseDomain, s.addr)
	if tcpPort > 0 {
		publicURL = fmt.Sprintf("tcp://%s:%d", s.baseDomain, tcpPort)
	}
	log.Printf("Tunnel registered: %s (custom: %s, tcp: %d) → %s", subdomain, customDomain, tcpPort, publicURL)

	s.mu.Lock()
	token, exists := s.resumptionTokens[subdomain]
	if !exists {
		token = fmt.Sprintf("resume-%s-%d", subdomain, time.Now().UnixNano())
		s.resumptionTokens[subdomain] = token
	}
	s.mu.Unlock()

	// Send confirmation.
	tc.mu.Lock()
	_ = conn.WriteJSON(tunnel.Envelope{
		Type: tunnel.TypeRegistered,
		Control: &tunnel.ControlMessage{
			Subdomain:       subdomain,
			CustomDomain:    customDomain,
			PublicURL:       publicURL,
			TCPPort:         tcpPort,
			ResumptionToken: token,
		},
	})
	tc.mu.Unlock()

	// Periodic analytics reporting
	analyticsURL := os.Getenv("SERVTUNNEL_ANALYTICS_URL")
	var analyticsStop chan struct{}
	if analyticsURL != "" {
		analyticsStop = make(chan struct{})
		go func(tc *tunnelConn, stopChan chan struct{}) {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					s.sendAnalyticsWebhook(analyticsURL, tc)
				case <-stopChan:
					s.sendAnalyticsWebhook(analyticsURL, tc)
					return
				}
			}
		}(tc, analyticsStop)
	}

	// Read loop: process responses and pings from the client.
	s.readLoop(tc)

	// Cleanup on disconnect.
	s.mu.Lock()
	if currTc, exists := s.tunnels[subdomain]; exists && currTc == tc {
		delete(s.tunnels, subdomain)
		if customDomain != "" {
			delete(s.customTunnels, customDomain)
		}
		if tcpPort > 0 {
			if l, exists := s.tcpListeners[tcpPort]; exists {
				l.Close()
				delete(s.tcpListeners, tcpPort)
			}
		}
		log.Printf("Tunnel disconnected: %s (custom: %s)", subdomain, customDomain)
	} else {
		log.Printf("Tunnel cleanup bypassed for session resumed subdomain: %s", subdomain)
	}
	if analyticsStop != nil {
		close(analyticsStop)
	}
	s.mu.Unlock()
	conn.Close()
}

// readLoop reads messages from a tunnel client.
func (s *Server) readLoop(tc *tunnelConn) {
	for {
		_ = tc.conn.SetReadDeadline(time.Now().Add(s.idleTimeout))
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
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(host)

	var tc *tunnelConn
	var exists bool
	var subdomain string

	s.mu.RLock()
	tc, exists = s.customTunnels[host]
	if exists {
		subdomain = tc.subdomain
	}
	s.mu.RUnlock()

	if !exists {
		subdomain = s.extractSubdomain(r.Host)
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

		s.mu.RLock()
		tc, exists = s.tunnels[subdomain]
		s.mu.RUnlock()
	}

	if !exists {
		if s.routeToFederationPeer(w, r, subdomain) {
			return
		}
	}

	span := otel.StartSpan("tunnel.proxy", r.Header.Get("traceparent"))

	if !exists {
		otel.EndSpan(span, fmt.Errorf("tunnel not found: %s", subdomain), nil)
		s.writeJSONError(w, r, fmt.Sprintf(`{"error":"tunnel %q not found"}`, subdomain), http.StatusBadGateway)
		return
	}

	if err := EnterpriseCheckIPAllowlist(r, subdomain); err != nil {
		otel.EndSpan(span, err, nil)
		s.writeJSONError(w, r, err.Error(), http.StatusForbidden)
		return
	}

	if err := EnterpriseVerifySSO(r, subdomain); err != nil {
		otel.EndSpan(span, err, nil)
		s.writeJSONError(w, r, err.Error(), http.StatusForbidden)
		return
	}

	EnterpriseAuditLog("request", subdomain, map[string]interface{}{"method": r.Method, "path": r.URL.Path, "ip": r.RemoteAddr})

	atomic.AddInt64(&tc.connections, 1)

	// Check bandwidth limit
	if atomic.LoadInt64(&tc.bytesRead)+atomic.LoadInt64(&tc.bytesWritten) > tc.quotaLimit {
		otel.EndSpan(span, fmt.Errorf("bandwidth quota exceeded"), nil)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests) // 429
		w.Write([]byte(`{"error":"bandwidth quota exceeded"}`))
		return
	}

	// Check sharing basic authentication
	if tc.sharingAuth != "" {
		inviteToken := r.URL.Query().Get("invite_token")
		inviteValid := false
		if inviteToken != "" {
			parts := strings.SplitN(inviteToken, ".", 2)
			if len(parts) == 2 {
				expiresStr := parts[0]
				sig := parts[1]
				expires, err := strconv.ParseInt(expiresStr, 10, 64)
				if err == nil && expires > time.Now().Unix() {
					secret := s.jwtSecret
					if secret == "" {
						secret = "default-invite-secret-xyz"
					}
					mac := hmac.New(sha256.New, []byte(secret))
					mac.Write([]byte(fmt.Sprintf("%s:%d", subdomain, expires)))
					expectedSig := hex.EncodeToString(mac.Sum(nil))
					if sig == expectedSig {
						inviteValid = true
					}
				}
			}
		}

		if !inviteValid {
			username, password, ok := r.BasicAuth()
			parts := strings.SplitN(tc.sharingAuth, ":", 2)
			expectedUser := parts[0]
			expectedPass := ""
			if len(parts) > 1 {
				expectedPass = parts[1]
			}
			if !ok || username != expectedUser || password != expectedPass {
				otel.EndSpan(span, fmt.Errorf("unauthorized sharing access"), nil)
				w.Header().Set("WWW-Authenticate", `Basic realm="ServTunnel Share"`)
				s.writeJSONError(w, r, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
	}

	// Rate limiting check
	if !tc.limiter.Allow() {
		otel.EndSpan(span, fmt.Errorf("rate limit exceeded"), nil)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limit exceeded"}`))
		return
	}

	start := time.Now()

	// Read request body.
	var bodyB64 string
	if r.Body != nil {
		bodyBytes, _ := io.ReadAll(r.Body)
		if len(bodyBytes) > 0 {
			bodyB64 = base64.StdEncoding.EncodeToString(bodyBytes)
			atomic.AddInt64(&tc.bytesRead, int64(len(bodyBytes)))
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
		s.writeJSONError(w, r, `{"error":"failed to send request to tunnel client"}`, http.StatusBadGateway)
		return
	}

	// Wait for response with timeout.
	select {
	case resp := <-pr.ch:
		latency := time.Since(start)
		if resp.Response == nil {
			otel.EndSpan(span, fmt.Errorf("empty response"), nil)
			s.writeJSONError(w, r, `{"error":"empty response from tunnel"}`, http.StatusBadGateway)
			return
		}

		// Write response headers.
		for k, v := range resp.Response.Headers {
			w.Header().Set(k, v)
		}
		// Announce trailers
		for k := range resp.Response.Trailers {
			w.Header().Add("Trailer", k)
		}
		w.WriteHeader(resp.Response.StatusCode)

		// Write response body.
		var writtenBytes int64
		if resp.Response.Body != "" {
			bodyBytes, err := base64.StdEncoding.DecodeString(resp.Response.Body)
			if err == nil {
				w.Write(bodyBytes)
				writtenBytes = int64(len(bodyBytes))
				atomic.AddInt64(&tc.bytesWritten, writtenBytes)
			}
		}

		// Set trailers after writing the body
		for k, v := range resp.Response.Trailers {
			w.Header().Set(k, v)
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
		s.writeJSONError(w, r, `{"error":"tunnel request timed out (30s)"}`, http.StatusGatewayTimeout)
	}
}

// handleListTunnels returns all active tunnel connections.
func (s *Server) handleListTunnels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSONError(w, r, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	tunnels := make([]map[string]interface{}, 0, len(s.tunnels))
	for sub, tc := range s.tunnels {
		tunnels = append(tunnels, map[string]interface{}{
			"subdomain":     sub,
			"custom_domain": tc.customDomain,
			"public_url":    fmt.Sprintf("http://%s.%s%s", sub, s.baseDomain, s.addr),
			"bytes_read":    atomic.LoadInt64(&tc.bytesRead),
			"bytes_written": atomic.LoadInt64(&tc.bytesWritten),
			"quota_limit":   tc.quotaLimit,
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
		s.writeJSONError(w, r, `{"error":"missing entry id"}`, http.StatusBadRequest)
		return
	}
	if len(parts) >= 4 && parts[3] == "replay" {
		s.handleReplayRequest(w, r, parts[2])
		return
	}
	s.inspector.HandleGet(w, r, parts[2])
}

// handleReplayRequest retrieves a logged request by ID and forwards it through the tunnel again.
func (s *Server) handleReplayRequest(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		s.writeJSONError(w, r, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	entry, ok := s.inspector.Get(id)
	if !ok {
		s.writeJSONError(w, r, `{"error":"entry not found"}`, http.StatusNotFound)
		return
	}

	s.mu.RLock()
	tc, exists := s.tunnels[entry.Subdomain]
	s.mu.RUnlock()

	if !exists {
		s.writeJSONError(w, r, fmt.Sprintf(`{"error":"tunnel %q not found"}`, entry.Subdomain), http.StatusBadGateway)
		return
	}

	if !tc.limiter.Allow() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limit exceeded"}`))
		return
	}

	start := time.Now()
	requestID := otel.GenerateSpanID()
	env := tunnel.Envelope{
		Type:      tunnel.TypeRequest,
		RequestID: requestID,
		Request: &tunnel.TunnelRequest{
			Method:  entry.Method,
			Path:    entry.Path,
			Headers: entry.RequestHeaders,
			Body:    entry.RequestBody,
		},
	}

	pr := &pendingRequest{
		ch:    make(chan tunnel.Envelope, 1),
		start: start,
	}
	tc.pending.Store(requestID, pr)
	defer tc.pending.Delete(requestID)

	tc.mu.Lock()
	err := tc.conn.WriteJSON(env)
	tc.mu.Unlock()
	if err != nil {
		s.writeJSONError(w, r, `{"error":"failed to send request to tunnel client"}`, http.StatusBadGateway)
		return
	}

	select {
	case resp := <-pr.ch:
		latency := time.Since(start)
		if resp.Response == nil {
			s.writeJSONError(w, r, `{"error":"empty response from tunnel"}`, http.StatusBadGateway)
			return
		}

		for k, v := range resp.Response.Headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(resp.Response.StatusCode)

		if resp.Response.Body != "" {
			bodyBytes, err := base64.StdEncoding.DecodeString(resp.Response.Body)
			if err == nil {
				w.Write(bodyBytes)
			}
		}

		s.inspector.Record(inspector.Entry{
			Method:          entry.Method,
			Path:            entry.Path,
			RequestHeaders:  entry.RequestHeaders,
			RequestBody:     entry.RequestBody,
			StatusCode:      resp.Response.StatusCode,
			ResponseHeaders: resp.Response.Headers,
			ResponseBody:    resp.Response.Body,
			LatencyMs:       latency.Milliseconds(),
			Subdomain:       entry.Subdomain,
		})

	case <-time.After(30 * time.Second):
		s.writeJSONError(w, r, `{"error":"tunnel request timed out (30s)"}`, http.StatusGatewayTimeout)
	}
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

// handleTCPConnection handles a single accepted TCP connection, forwarding
// data bidirectionally between the TCP client and the tunnel client.
func (s *Server) handleTCPConnection(tc *tunnelConn, clientConn net.Conn) {
	defer clientConn.Close()

	// Unique session identifier for this TCP connection
	requestID := otel.GenerateSpanID()

	// Setup pending request channel to receive responses back from client
	pr := &pendingRequest{
		ch:    make(chan tunnel.Envelope, 10),
		start: time.Now(),
	}
	tc.pending.Store(requestID, pr)
	defer tc.pending.Delete(requestID)

	// Goroutine to read TCP responses from client websocket and write to local client TCP socket
	go func() {
		for env := range pr.ch {
			if env.Response != nil && env.Response.TCPData != "" {
				data, err := base64.StdEncoding.DecodeString(env.Response.TCPData)
				if err == nil {
					_, _ = clientConn.Write(data)
					atomic.AddInt64(&tc.bytesWritten, int64(len(data)))
				}
			}
		}
	}()

	buf := make([]byte, 32*1024)
	for {
		_ = clientConn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := clientConn.Read(buf)
		if err != nil {
			break
		}

		if n > 0 {
			atomic.AddInt64(&tc.bytesRead, int64(n))
			payloadB64 := base64.StdEncoding.EncodeToString(buf[:n])

			env := tunnel.Envelope{
				Type:      tunnel.TypeRequest,
				RequestID: requestID,
				Request: &tunnel.TunnelRequest{
					TCPData: payloadB64,
				},
			}

			tc.mu.Lock()
			writeErr := tc.conn.WriteJSON(env)
			tc.mu.Unlock()
			if writeErr != nil {
				break
			}
		}
	}
}

func (s *Server) handleTunnelsSubroutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/tunnels/")
	parts := strings.Split(path, "/")
	if len(parts) >= 2 {
		subdomain := parts[0]
		action := parts[1]
		if action == "invite" {
			s.handleGenerateInvite(w, r, subdomain)
			return
		}
		if action == "exists" {
			s.handleCheckExists(w, r, subdomain)
			return
		}
		if action == "analytics" {
			s.handleGetAnalytics(w, r, subdomain)
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) handleGenerateInvite(w http.ResponseWriter, r *http.Request, subdomain string) {
	if err := s.authenticate(r); err != nil {
		s.writeJSONError(w, r, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	s.mu.RLock()
	_, exists := s.tunnels[subdomain]
	s.mu.RUnlock()
	if !exists {
		s.writeJSONError(w, r, `{"error":"tunnel not found"}`, http.StatusNotFound)
		return
	}

	secret := s.jwtSecret
	if secret == "" {
		secret = "default-invite-secret-xyz"
	}
	
	expires := time.Now().Add(24 * time.Hour).Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%s:%d", subdomain, expires)))
	sig := hex.EncodeToString(mac.Sum(nil))

	inviteToken := fmt.Sprintf("%d.%s", expires, sig)
	inviteURL := fmt.Sprintf("http://%s.%s%s/?invite_token=%s", subdomain, s.baseDomain, s.addr, inviteToken)
	if strings.Contains(s.addr, ":") && !strings.HasPrefix(s.addr, ":") {
		inviteURL = fmt.Sprintf("http://%s.%s/?invite_token=%s", subdomain, s.baseDomain, inviteToken)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"subdomain":    subdomain,
		"invite_token": inviteToken,
		"invite_url":   inviteURL,
	})
}

func (s *Server) handleCheckExists(w http.ResponseWriter, _ *http.Request, subdomain string) {
	s.mu.RLock()
	_, exists := s.tunnels[subdomain]
	s.mu.RUnlock()
	if exists {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) handleGetAnalytics(w http.ResponseWriter, r *http.Request, subdomain string) {
	s.mu.RLock()
	tc, exists := s.tunnels[subdomain]
	s.mu.RUnlock()
	if !exists {
		s.writeJSONError(w, r, `{"error":"tunnel not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"subdomain":     subdomain,
		"bytes_read":    atomic.LoadInt64(&tc.bytesRead),
		"bytes_written": atomic.LoadInt64(&tc.bytesWritten),
		"connections":   atomic.LoadInt64(&tc.connections),
		"uptime_sec":    int64(time.Since(tc.startTime).Seconds()),
	})
}

func (s *Server) sendAnalyticsWebhook(targetURL string, tc *tunnelConn) {
	payload := map[string]interface{}{
		"subdomain":     tc.subdomain,
		"bytes_read":    atomic.LoadInt64(&tc.bytesRead),
		"bytes_written": atomic.LoadInt64(&tc.bytesWritten),
		"connections":   atomic.LoadInt64(&tc.connections),
		"uptime_sec":    int64(time.Since(tc.startTime).Seconds()),
		"timestamp":     time.Now().Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequest("POST", targetURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (s *Server) proxyToPeer(w http.ResponseWriter, r *http.Request, peerURL string) {
	target, err := url.Parse(peerURL)
	if err != nil {
		s.writeJSONError(w, r, "Bad Gateway", http.StatusBadGateway)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ServeHTTP(w, r)
}

func (s *Server) writeJSONError(w http.ResponseWriter, r *http.Request, msg string, status int) {
	var errorCode string
	switch status {
	case http.StatusMethodNotAllowed:
		errorCode = "ERR_METHOD_NOT_ALLOWED"
	case http.StatusBadRequest:
		errorCode = "ERR_BAD_REQUEST"
	case http.StatusUnauthorized:
		errorCode = "ERR_UNAUTHORIZED"
	case http.StatusForbidden:
		errorCode = "ERR_FORBIDDEN"
	case http.StatusNotFound:
		errorCode = "ERR_NOT_FOUND"
	case http.StatusConflict:
		errorCode = "ERR_CONFLICT"
	case http.StatusNotImplemented:
		errorCode = "ERR_NOT_IMPLEMENTED"
	case http.StatusBadGateway:
		errorCode = "ERR_BAD_GATEWAY"
	case http.StatusGatewayTimeout:
		errorCode = "ERR_GATEWAY_TIMEOUT"
	default:
		errorCode = "ERR_INTERNAL_SERVER_ERROR"
	}
	if strings.HasPrefix(msg, `{"error":`) && strings.HasSuffix(msg, `}`) {
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(msg), &raw); err == nil {
			if eMsg, ok := raw["error"].(string); ok {
				msg = eMsg
			}
		}
	}
	ServShared.WriteJSONError(w, r, msg, errorCode, status)
}
