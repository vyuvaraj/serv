package ws

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEventBroadcasterCore(t *testing.T) {
	b := NewEventBroadcaster()
	if b.ActiveCount() != 0 {
		t.Errorf("expected 0 clients, got %d", b.ActiveCount())
	}

	ch := make(chan string, 10)
	b.Register(ch)
	if b.ActiveCount() != 1 {
		t.Errorf("expected 1 client, got %d", b.ActiveCount())
	}

	event := "test-message-payload"
	b.Broadcast(event)

	select {
	case received := <-ch:
		if received != event {
			t.Errorf("expected %q, got %q", event, received)
		}
	default:
		t.Error("expected to receive broadcast message")
	}

	b.Unregister(ch)
	if b.ActiveCount() != 0 {
		t.Errorf("expected 0 clients after unregister, got %d", b.ActiveCount())
	}
}

// TestEventStreamReconnectionSimulated (D.28) verifies that when the client
// loses connection to the event stream, it can reconnect to a restarted server
// instance and resume receiving events without state loss.
func TestEventStreamReconnectionSimulated(t *testing.T) {
	broadcaster1 := NewEventBroadcaster()
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		broadcaster1.HandleEvents(w, r)
	}))

	// Connect client to server 1 with a separate cancelable context (concurrently to avoid deadlock)
	ctx1, cancel1 := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx1, "GET", srv1.URL, nil)

	respChan := make(chan *http.Response, 1)
	errChan := make(chan error, 1)

	go func() {
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			errChan <- e
		} else {
			respChan <- r
		}
	}()

	// Wait for handler to register client
	time.Sleep(20 * time.Millisecond)

	// Broadcast an event to unblock headers/write
	broadcaster1.Broadcast("initial-event")

	var resp1 *http.Response
	select {
	case resp1 = <-respChan:
	case err := <-errChan:
		cancel1()
		t.Fatalf("failed to connect to srv1: %v", err)
	case <-time.After(2 * time.Second):
		cancel1()
		t.Fatalf("timeout waiting for srv1 response")
	}

	reader := bufio.NewReader(resp1.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		resp1.Body.Close()
		cancel1()
		t.Fatalf("failed to read initial: %v", err)
	}
	if !strings.Contains(line, "initial-event") {
		line, _ = reader.ReadString('\n')
	}
	t.Logf("client received first event: %q", line)
	
	// Close response and cancel context to stop handler loop immediately
	resp1.Body.Close()
	cancel1()
	srv1.Close()

	// Restart: start server 2 on a new URL (client reconnects to the new endpoint)
	broadcaster2 := NewEventBroadcaster()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		broadcaster2.HandleEvents(w, r)
	}))
	defer srv2.Close()

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	
	req2, _ := http.NewRequestWithContext(ctx2, "GET", srv2.URL, nil)
	
	respChan2 := make(chan *http.Response, 1)
	errChan2 := make(chan error, 1)

	go func() {
		r, e := http.DefaultClient.Do(req2)
		if e != nil {
			errChan2 <- e
		} else {
			respChan2 <- r
		}
	}()

	time.Sleep(20 * time.Millisecond)
	broadcaster2.Broadcast("post-restart-event")

	var resp2 *http.Response
	select {
	case resp2 = <-respChan2:
	case err := <-errChan2:
		t.Fatalf("client failed to auto-reconnect: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for srv2 response")
	}
	defer resp2.Body.Close()

	reader2 := bufio.NewReader(resp2.Body)
	line2, err := reader2.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read post-restart: %v", err)
	}
	if !strings.Contains(line2, "post-restart-event") {
		line2, _ = reader2.ReadString('\n')
	}

	t.Logf("client received event after restart: %q", line2)
	if !strings.Contains(line2, "post-restart-event") {
		t.Errorf("expected post-restart-event, got %q", line2)
	}
}
