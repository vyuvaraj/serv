package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/vyuvaraj/serv/packages/ServTunnel/pkg/client"
	"github.com/vyuvaraj/serv/packages/ServTunnel/pkg/inspector"
	"github.com/vyuvaraj/serv/packages/ServTunnel/pkg/server"
)

func TestTCPTunneling(t *testing.T) {
	// 1. Start a local mock TCP echo server
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen echo server: %v", err)
	}
	defer echoListener.Close()

	go func() {
		for {
			conn, err := echoListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c) // Echo back
			}(conn)
		}
	}()

	localAddr := echoListener.Addr().String()

	// 2. Start a relay server
	insp := inspector.New(10)
	relay := server.NewServer("127.0.0.1:0", "localhost", insp)

	// Pick a free TCP port to tunnel
	tcpTunnelListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind temp listener: %v", err)
	}
	tcpTunnelPort := tcpTunnelListener.Addr().(*net.TCPAddr).Port
	tcpTunnelListener.Close() // Free up for client

	// Start relay HTTP/WS listener
	relayHTTPListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind relay HTTP: %v", err)
	}
	relayHTTPAddr := relayHTTPListener.Addr().String()
	relayHTTPListener.Close()

	relay = server.NewServer(relayHTTPAddr, "localhost", insp)

	go func() {
		_ = relay.Start()
	}()
	defer relay.Shutdown(context.Background())
	time.Sleep(100 * time.Millisecond)

	// 3. Start client connecting to TCP echo port
	relayWSURL := "ws://" + relayHTTPAddr + "/ws/connect"
	c := client.NewClient(localAddr, relayWSURL, "my-tcp-tunnel", "", "", "", "")
	c.WithTCPPort(tcpTunnelPort)
	defer c.Close()

	go func() {
		_ = c.Run()
	}()
	time.Sleep(200 * time.Millisecond)

	// 4. Connect to tunnel TCP port and verify echo exchange
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", tcpTunnelPort))
	if err != nil {
		t.Fatalf("failed to dial TCP tunnel port %d: %v", tcpTunnelPort, err)
	}
	defer conn.Close()

	msg := "Hello ServTunnel TCP!"
	_, err = conn.Write([]byte(msg))
	if err != nil {
		t.Fatalf("failed to write TCP message: %v", err)
	}

	buf := make([]byte, 1024)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	resp := string(buf[:n])
	if !strings.Contains(resp, msg) {
		t.Errorf("expected echoed message %q, got %q", msg, resp)
	}
}
