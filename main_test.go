package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"servcloud/pkg/orchestrator"
	"servcloud/pkg/server"
)

func TestServCloudLifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "servcloud-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	orch, err := orchestrator.NewOrchestrator(tempDir)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	// Create test server.
	srv := server.NewServer(orch, "", "")
	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	serviceName := "testservice"
	serviceCode := `
server "8080" {
	route "/hello" -> "Hello World!"
}
`

	// 1. Deploy service
	payload := map[string]string{
		"name": serviceName,
		"code": serviceCode,
	}
	bodyBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", testServer.URL+"/api/deploy", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("failed to create deploy request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to perform deploy request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected status 202 Accepted, got %d", resp.StatusCode)
	}

	var deployRes orchestrator.ServiceProcess
	if err := json.NewDecoder(resp.Body).Decode(&deployRes); err != nil {
		t.Fatalf("failed to decode deploy response: %v", err)
	}

	if deployRes.Name != serviceName {
		t.Errorf("expected name %q, got %q", serviceName, deployRes.Name)
	}

	// 2. Poll status until running (mock build uses go build so it takes a split second)
	var activePort int
	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	running := false
	for !running {
		select {
		case <-timeout:
			t.Fatal("timeout waiting for service to compile and run")
		case <-ticker.C:
			req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/services/%s", testServer.URL, serviceName), nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				continue
			}
			var statusRes orchestrator.ServiceProcess
			if err := json.NewDecoder(resp.Body).Decode(&statusRes); err == nil {
				if statusRes.Status == "running" {
					running = true
					activePort = statusRes.Port
				} else if statusRes.Status == "failed" {
					t.Fatalf("service deployment failed: %s", statusRes.Error)
				}
			}
			resp.Body.Close()
		}
	}

	if activePort == 0 {
		t.Fatal("service running but no port allocated")
	}

	// 3. Make HTTP request directly to the deployed service port
	serviceURL := fmt.Sprintf("http://localhost:%d", activePort)
	
	// Test health check
	healthResp, err := http.Get(serviceURL + "/health")
	if err != nil {
		t.Fatalf("failed to ping service health: %v", err)
	}
	defer healthResp.Body.Close()
	healthBytes, _ := io.ReadAll(healthResp.Body)
	if string(healthBytes) != "OK" {
		t.Errorf("expected health check response 'OK', got %q", string(healthBytes))
	}

	// Test main endpoint
	serviceResp, err := http.Get(serviceURL + "/")
	if err != nil {
		t.Fatalf("failed to query service endpoint: %v", err)
	}
	defer serviceResp.Body.Close()
	srvBytes, _ := io.ReadAll(serviceResp.Body)
	if !strings.Contains(string(srvBytes), "Hello from mock service") {
		t.Errorf("expected service to return hello response, got %q", string(srvBytes))
	}

	// 4. Retrieve service logs
	logsReq, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/services/%s/logs", testServer.URL, serviceName), nil)
	logsResp, err := http.DefaultClient.Do(logsReq)
	if err != nil {
		t.Fatalf("failed to retrieve logs: %v", err)
	}
	defer logsResp.Body.Close()
	var logs []string
	if err := json.NewDecoder(logsResp.Body).Decode(&logs); err != nil {
		t.Fatalf("failed to decode logs: %v", err)
	}

	if len(logs) == 0 {
		t.Errorf("expected logs to be captured, got empty list")
	}

	// 5. Undeploy service
	delReq, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/api/services/%s", testServer.URL, serviceName), nil)
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("failed to perform delete: %v", err)
	}
	defer delResp.Body.Close()

	if delResp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 OK, got %d", delResp.StatusCode)
	}

	// Verify it's removed
	checkReq, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/services/%s", testServer.URL, serviceName), nil)
	checkResp, err := http.DefaultClient.Do(checkReq)
	if err != nil {
		t.Fatalf("failed to check: %v", err)
	}
	defer checkResp.Body.Close()
	if checkResp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", checkResp.StatusCode)
	}
}
