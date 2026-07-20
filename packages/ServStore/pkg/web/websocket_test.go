package web

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vyuvaraj/serv/packages/ServStore/pkg/cluster"
)

func TestWebSocketUpgradeAndEventPush(t *testing.T) {
	// Setup test handler that calls HandleWebSocketEvents
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HandleWebSocketEvents(w, r)
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	// Parse server URL for dialing
	addr := server.Listener.Addr().String()

	// Dial TCP directly to perform WebSocket upgrade manually
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("failed to dial test server: %v", err)
	}
	defer conn.Close()

	br := bufio.NewReader(conn)

	// Send manual handshake
	secKey := "dGhlIHNhbXBsZSBub25jZQ=="
	req := fmt.Sprintf(
		"GET / HTTP/1.1\r\n"+
			"Host: %s\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\n"+
			"Sec-WebSocket-Version: 13\r\n\r\n",
		addr, secKey,
	)

	_, err = conn.Write([]byte(req))
	if err != nil {
		t.Fatalf("failed to write handshake request: %v", err)
	}

	// Read handshake response line by line until empty line to avoid consuming websocket frames
	var respHeader strings.Builder
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read handshake line: %v", err)
		}
		respHeader.WriteString(line)
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	resp := respHeader.String()

	if !strings.Contains(resp, "101 Switching Protocols") {
		t.Fatalf("expected 101 Switching Protocols, got: %s", resp)
	}

	// Verify Sec-WebSocket-Accept header
	hash := sha1.New()
	hash.Write([]byte(secKey + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	expectedAccept := base64.StdEncoding.EncodeToString(hash.Sum(nil))
	if !strings.Contains(resp, "Sec-WebSocket-Accept: "+expectedAccept) {
		t.Fatalf("expected Sec-WebSocket-Accept: %s, got: %s", expectedAccept, resp)
	}

	// Helper function to read a text frame
	readFrame := func() ([]byte, error) {
		header := make([]byte, 2)
		_, err := io.ReadFull(br, header)
		if err != nil {
			return nil, err
		}
		// Opcode must be 1 (text)
		if header[0]&0x0F != 1 {
			return nil, fmt.Errorf("unexpected opcode: %d", header[0]&0x0F)
		}
		length := int(header[1] & 0x7F)
		switch length {
		case 126:
			lenBytes := make([]byte, 2)
			_, err = io.ReadFull(br, lenBytes)
			if err != nil {
				return nil, err
			}
			length = int(lenBytes[0])<<8 | int(lenBytes[1])
		case 127:
			return nil, fmt.Errorf("large payload not supported in test")
		}

		payload := make([]byte, length)
		_, err = io.ReadFull(br, payload)
		return payload, err
	}

	// 1. Read first frame (connection_established)
	payload, err := readFrame()
	if err != nil {
		t.Fatalf("failed to read first frame: %v", err)
	}

	var ev cluster.ClusterEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatalf("failed to unmarshal first frame: %v", err)
	}
	if ev.Type != "connection_established" {
		t.Fatalf("expected connection_established event, got: %s", ev.Type)
	}

	// 2. Publish a test event to GlobalHub and read it
	testEvent := cluster.ClusterEvent{
		Type:   "node_join",
		NodeID: "node-test-1",
		Status: "online",
	}
	cluster.GlobalHub.Publish(testEvent)

	payload, err = readFrame()
	if err != nil {
		t.Fatalf("failed to read second frame: %v", err)
	}

	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatalf("failed to unmarshal second frame: %v", err)
	}
	if ev.Type != "node_join" || ev.NodeID != "node-test-1" || ev.Status != "online" {
		t.Fatalf("expected node_join event, got: %+v", ev)
	}
}
