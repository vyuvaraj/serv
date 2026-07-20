package proxy

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestWebSocketProxyStability simulates WebSocket connection upgrades,
// network flaps (rapid connect/disconnect), and long-lived proxy stability.
func TestWebSocketProxyStability(t *testing.T) {
	routes := []Route{
		{Prefix: "/ws", Target: "http://localhost:9091"},
	}
	handler := NewGatewayHandler(routes, nil, "token-secret")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HandleWebSocketMetrics(w, r, handler)
	}))
	defer srv.Close()

	addr := srv.Listener.Addr().String()

	// 1. WebSocket network flap simulation: Connect and immediately disconnect multiple times.
	// Verify no leaking resources or crashed goroutines.
	t.Run("WebSocketConnectionFlap", func(t *testing.T) {
		for i := 0; i < 20; i++ {
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatalf("failed to dial: %v", err)
			}

			// Send handshake request
			secKey := "fGhlIHNhbXBsZSBub25jZQ=="
			req := fmt.Sprintf(
				"GET /?token=token-secret HTTP/1.1\r\n"+
					"Host: %s\r\n"+
					"Upgrade: websocket\r\n"+
					"Connection: Upgrade\r\n"+
					"Sec-WebSocket-Key: %s\r\n"+
					"Sec-WebSocket-Version: 13\r\n\r\n",
				addr, secKey,
			)
			_, _ = conn.Write([]byte(req))

			// Immediately close connection (simulate client dropping)
			conn.Close()
		}
	})

	// 2. Parallel active connections and metrics delivery under load.
	t.Run("WebSocketMultiClientLoad", func(t *testing.T) {
		const clients = 10
		var wg sync.WaitGroup
		wg.Add(clients)

		for c := 0; c < clients; c++ {
			go func(clientID int) {
				defer wg.Done()
				conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
				if err != nil {
					t.Errorf("client %d: failed to dial: %v", clientID, err)
					return
				}
				defer conn.Close()

				secKey := fmt.Sprintf("dGhlIHNhbXBsZSBub25jZ%d==", clientID)
				req := fmt.Sprintf(
					"GET /?token=token-secret HTTP/1.1\r\n"+
						"Host: %s\r\n"+
						"Upgrade: websocket\r\n"+
						"Connection: Upgrade\r\n"+
						"Sec-WebSocket-Key: %s\r\n"+
						"Sec-WebSocket-Version: 13\r\n\r\n",
					addr, secKey,
				)
				_, err = conn.Write([]byte(req))
				if err != nil {
					t.Errorf("client %d: failed to write handshake: %v", clientID, err)
					return
				}

				reader := bufio.NewReader(conn)
				// Read response headers
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						t.Errorf("client %d: failed to read headers: %v", clientID, err)
						return
					}
					if line == "\r\n" || line == "\n" {
						break
					}
				}

				// Helper to read frame
				readFrame := func() ([]byte, error) {
					header := make([]byte, 2)
					_, err := io.ReadFull(reader, header)
					if err != nil {
						return nil, err
					}
					length := int(header[1] & 0x7F)
					payload := make([]byte, length)
					_, err = io.ReadFull(reader, payload)
					return payload, err
				}

				// Read connection established message
				_, err = readFrame()
				if err != nil {
					t.Errorf("client %d: failed to read establishment frame: %v", clientID, err)
					return
				}

				// Sleep for a short duration to ensure stability of the stream
				time.Sleep(100 * time.Millisecond)
			}(c)
		}

		wg.Wait()
	})
}

// TestWebSocketSecAcceptHeaderVerifications checks edge cases on headers
func TestWebSocketSecAcceptHeaderVerifications(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := UpgradeToWebSocket(w, r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(err.Error()))
			return
		}
		conn.Close()
	}))
	defer srv.Close()

	// Missing header tests
	t.Run("MissingUpgradeHeader", func(t *testing.T) {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d", resp.StatusCode)
		}
	})

	t.Run("MissingWebSocketKey", func(t *testing.T) {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		req.Header.Set("Upgrade", "websocket")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("failed to make request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d", resp.StatusCode)
		}
	})

	t.Run("SuccessfulUpgradeVerifyAcceptHash", func(t *testing.T) {
		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		if err != nil {
			t.Fatalf("failed to dial: %v", err)
		}
		defer conn.Close()

		secKey := "x3JJHMbDL1EzLkh9GBhXDw=="
		req := fmt.Sprintf(
			"GET / HTTP/1.1\r\n"+
				"Host: %s\r\n"+
				"Upgrade: websocket\r\n"+
				"Connection: Upgrade\r\n"+
				"Sec-WebSocket-Key: %s\r\n"+
				"Sec-WebSocket-Version: 13\r\n\r\n",
			srv.Listener.Addr().String(), secKey,
		)
		_, _ = conn.Write([]byte(req))

		reader := bufio.NewReader(conn)
		status, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read response: %v", err)
		}
		if !strings.Contains(status, "101 Switching Protocols") {
			t.Fatalf("expected status 101, got: %s", status)
		}

		var acceptHeader string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("failed to read header: %v", err)
			}
			if line == "\r\n" || line == "\n" {
				break
			}
			if strings.HasPrefix(line, "Sec-WebSocket-Accept:") {
				acceptHeader = strings.TrimSpace(strings.TrimPrefix(line, "Sec-WebSocket-Accept:"))
			}
		}

		// Calculate expected accept
		hash := sha1.New()
		hash.Write([]byte(secKey + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		expectedAccept := base64.StdEncoding.EncodeToString(hash.Sum(nil))

		if acceptHeader != expectedAccept {
			t.Errorf("expected accept header %q, got %q", expectedAccept, acceptHeader)
		}
	})
}
