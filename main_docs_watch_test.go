package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDocsServeAndWatch(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "serv_docs_serve_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	fileArg := filepath.Join(tmpDir, "app.srv")
	initialContent := `
route "GET" "/hello" (req) {
	return "hello"
}
`
	if err := os.WriteFile(fileArg, []byte(initialContent), 0644); err != nil {
		t.Fatalf("failed to write app.srv: %v", err)
	}

	// Start serveDocs asynchronously on port 8991
	port := 8991
	server := serveDocs(fileArg, port, true)
	defer server.Close()

	// Wait for server to start
	time.Sleep(200 * time.Millisecond)

	// Fetch GET /
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("Failed to fetch docs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyStr := string(bodyBytes)
	if !strings.Contains(bodyStr, "/hello") {
		t.Errorf("Expected docs HTML to contain '/hello', got:\n%s", bodyStr)
	}
	if !strings.Contains(bodyStr, "evtSource") {
		t.Errorf("Expected live-reload script to be injected, got:\n%s", bodyStr)
	}

	// Connect to SSE /events in a goroutine
	sseConnected := make(chan struct{})
	reloadReceived := make(chan struct{})
	go func() {
		sseResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/events", port))
		if err != nil {
			return
		}
		defer sseResp.Body.Close()

		close(sseConnected)

		reader := bufio.NewReader(sseResp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.Contains(line, "data: reload") {
				close(reloadReceived)
				return
			}
		}
	}()

	// Wait for SSE connection
	time.Sleep(100 * time.Millisecond)

	// Modify the file to trigger watcher
	updatedContent := `
route "GET" "/hello" (req) {
	return "hello"
}

route "POST" "/order" (req) {
	return "order placed"
}
`
	if err := os.WriteFile(fileArg, []byte(updatedContent), 0644); err != nil {
		t.Fatalf("failed to update app.srv: %v", err)
	}

	// Wait for reload signal
	select {
	case <-reloadReceived:
		// Reload received successfully!
	case <-time.After(5 * time.Second):
		t.Error("Timed out waiting for file watcher reload event")
	}

	// Fetch GET / again to check if it's updated
	resp2, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("Failed to fetch updated docs: %v", err)
	}
	defer resp2.Body.Close()

	bodyBytes2, _ := io.ReadAll(resp2.Body)
	bodyStr2 := string(bodyBytes2)
	if !strings.Contains(bodyStr2, "/order") {
		t.Errorf("Expected updated docs HTML to contain '/order', got:\n%s", bodyStr2)
	}
}
