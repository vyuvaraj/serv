package proxy

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// UpgradeToWebSocket upgrades the HTTP connection to a WebSocket connection (RFC 6455).
func UpgradeToWebSocket(w http.ResponseWriter, r *http.Request) (net.Conn, error) {
	if r.Header.Get("Upgrade") != "websocket" {
		return nil, errors.New("missing Upgrade: websocket header")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("missing Sec-WebSocket-Key header")
	}

	// Calculate Sec-WebSocket-Accept
	hash := sha1.New()
	hash.Write([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	accept := base64.StdEncoding.EncodeToString(hash.Sum(nil))

	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("webserver does not support hijacking")
	}

	conn, bufrw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	// Send 101 Switching Protocols response
	bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	bufrw.WriteString("Upgrade: websocket\r\n")
	bufrw.WriteString("Connection: Upgrade\r\n")
	bufrw.WriteString("Sec-WebSocket-Accept: ")
	bufrw.WriteString(accept)
	bufrw.WriteString("\r\n\r\n")
	bufrw.Flush()

	return conn, nil
}

// WriteWebSocketTextFrame formats and writes a text frame (opcode 1) to the client.
func WriteWebSocketTextFrame(conn net.Conn, text []byte) error {
	length := len(text)
	var header []byte

	// FIN=1, Opcode=1 (Text) -> 0x81
	header = append(header, 0x81)

	if length < 126 {
		header = append(header, byte(length))
	} else if length < 65536 {
		header = append(header, 126)
		header = append(header, byte(length>>8), byte(length))
	} else {
		header = append(header, 127)
		for i := 7; i >= 0; i-- {
			header = append(header, byte(length>>(i*8)))
		}
	}

	_, err := conn.Write(append(header, text...))
	return err
}

type wsSession struct {
	conn net.Conn
	mu   sync.Mutex
}

func (s *wsSession) send(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return WriteWebSocketTextFrame(s.conn, payload)
}

// HandleWebSocketMetrics handles the WebSocket connection for real-time metrics.
func HandleWebSocketMetrics(w http.ResponseWriter, r *http.Request, handler *GatewayHandler) {
	conn, err := UpgradeToWebSocket(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer conn.Close()

	session := &wsSession{conn: conn}

	closeChan := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		for {
			conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			_, err := conn.Read(buf)
			if err != nil {
				close(closeChan)
				return
			}
		}
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// Initial message
	initMsg, _ := json.Marshal(map[string]interface{}{
		"status": "connected",
		"type":   "connection_established",
	})
	if err := session.send(initMsg); err != nil {
		return
	}

	for {
		select {
		case <-closeChan:
			return
		case <-ticker.C:
			snap := handler.GetMetricsSnapshot()
			data, err := json.Marshal(snap)
			if err != nil {
				continue
			}
			if err := session.send(data); err != nil {
				return
			}
		}
	}
}
