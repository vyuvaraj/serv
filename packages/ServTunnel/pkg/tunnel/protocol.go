// Package tunnel defines the wire protocol for communication between the
// ServTunnel relay server and tunnel clients over WebSocket.
//
// All messages are JSON-encoded. The relay sends TunnelRequest frames to the
// client; the client responds with TunnelResponse frames. ControlMessage is
// used for registration, heartbeat, and error signaling.
package tunnel

// MessageType identifies the kind of frame sent over the WebSocket.
type MessageType string

const (
	// Client → Server: register a tunnel with a requested subdomain.
	TypeRegister MessageType = "register"
	// Server → Client: confirm registration with the assigned subdomain.
	TypeRegistered MessageType = "registered"
	// Server → Client: forward an incoming HTTP request to the local service.
	TypeRequest MessageType = "request"
	// Client → Server: return the local service's HTTP response.
	TypeResponse MessageType = "response"
	// Bidirectional: keepalive ping.
	TypePing MessageType = "ping"
	// Bidirectional: keepalive pong.
	TypePong MessageType = "pong"
	// Server → Client: report an error.
	TypeError MessageType = "error"
)

// Envelope is the top-level JSON frame exchanged over the WebSocket.
type Envelope struct {
	Type      MessageType `json:"type"`
	RequestID string      `json:"request_id,omitempty"`

	// Populated for TypeRegister / TypeRegistered / TypeError
	Control *ControlMessage `json:"control,omitempty"`

	// Populated for TypeRequest
	Request *TunnelRequest `json:"request,omitempty"`

	// Populated for TypeResponse
	Response *TunnelResponse `json:"response,omitempty"`
}

// ControlMessage carries registration and error information.
type ControlMessage struct {
	Subdomain       string `json:"subdomain,omitempty"`
	CustomDomain    string `json:"custom_domain,omitempty"`
	PublicURL       string `json:"public_url,omitempty"`
	Error           string `json:"error,omitempty"`
	SharingAuth     string `json:"sharing_auth,omitempty"`
	TCPPort         int    `json:"tcp_port,omitempty"` // Requested TCP port for TCP Tunneling
	ResumptionToken string `json:"resumption_token,omitempty"`
}

// TunnelRequest represents an incoming HTTP or raw TCP connection forwarded through the tunnel.
type TunnelRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body,omitempty"` // base64-encoded for binary safety
	TCPData string            `json:"tcp_data,omitempty"` // Base64 raw TCP payload chunk
}

// TunnelResponse represents the local service's response sent back through the tunnel.
type TunnelResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body,omitempty"` // base64-encoded for binary safety
	Trailers   map[string]string `json:"trailers,omitempty"`
	TCPData    string            `json:"tcp_data,omitempty"` // Base64 raw TCP response chunk
}
