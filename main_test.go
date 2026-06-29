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
				switch statusRes.Status {
				case "running":
					running = true
					activePort = statusRes.Port
				case "failed":
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
	serviceURL := fmt.Sprintf("http://127.0.0.1:%d", activePort)
	
	// Test health check with retry loop to allow process to bind port
	var healthResp *http.Response
	for i := 0; i < 30; i++ {
		healthResp, err = http.Get(serviceURL + "/health")
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
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

func TestServCloudPhase3Features(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "servcloud-phase3-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	orch, err := orchestrator.NewOrchestrator(tempDir)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	srv := server.NewServer(orch, "", "")
	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	serviceName := "canary-service"
	serviceCode1 := `server "8080" { route "/" -> "version 1" }`
	serviceCode2 := `server "8080" { route "/" -> "version 2" }`

	// 1. Deploy Version 1
	payload := map[string]string{"name": serviceName, "code": serviceCode1}
	bodyBytes, _ := json.Marshal(payload)
	resp, err := http.Post(testServer.URL+"/api/deploy", "application/json", bytes.NewReader(bodyBytes))
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("Failed to deploy version 1: %v, status: %v", err, resp.StatusCode)
	}
	resp.Body.Close()

	// Wait for running status
	var activePort int
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for activePort == 0 {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for deployment 1 to run")
		case <-ticker.C:
			proc, ok := orch.GetService(serviceName)
			if ok && proc.Status == "running" {
				activePort = proc.Port
			}
		}
	}

	// 2. Query stats endpoint
	statsResp, err := http.Get(fmt.Sprintf("%s/api/services/%s/stats", testServer.URL, serviceName))
	if err != nil {
		t.Fatalf("failed to query stats: %v", err)
	}
	defer statsResp.Body.Close()
	var stats orchestrator.ServiceStats
	if err := json.NewDecoder(statsResp.Body).Decode(&stats); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}
	if stats.PID <= 0 {
		t.Errorf("expected positive PID, got %d", stats.PID)
	}

	// 3. Deploy Version 2 (which overrides version 1 and adds to history)
	payload2 := map[string]string{"name": serviceName, "code": serviceCode2}
	bodyBytes2, _ := json.Marshal(payload2)
	resp2, err := http.Post(testServer.URL+"/api/deploy", "application/json", bytes.NewReader(bodyBytes2))
	if err != nil || resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("Failed to deploy version 2: %v", err)
	}
	resp2.Body.Close()

	// Wait for running status
	activePort2 := 0
	timeout = time.After(5 * time.Second)
	for activePort2 == 0 {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for deployment 2 to run")
		case <-ticker.C:
			proc, ok := orch.GetService(serviceName)
			if ok && proc.Status == "running" && proc.Port != activePort {
				activePort2 = proc.Port
			}
		}
	}

	// 4. Retrieve deployment history
	histResp, err := http.Get(testServer.URL + "/api/history")
	if err != nil {
		t.Fatalf("failed to query history: %v", err)
	}
	defer histResp.Body.Close()
	var history []orchestrator.DeploymentHistoryItem
	if err := json.NewDecoder(histResp.Body).Decode(&history); err != nil {
		t.Fatalf("failed to decode history: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(history))
	}

	// 5. Test rollback to Version 1
	rollResp, err := http.Post(fmt.Sprintf("%s/api/services/%s/rollback", testServer.URL, serviceName), "application/json", nil)
	if err != nil || rollResp.StatusCode != http.StatusOK {
		t.Fatalf("rollback failed: %v", err)
	}
	rollResp.Body.Close()

	// Wait for running status again
	activePort3 := 0
	timeout = time.After(5 * time.Second)
	for activePort3 == 0 {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for rollback deployment to run")
		case <-ticker.C:
			proc, ok := orch.GetService(serviceName)
			if ok && proc.Status == "running" && proc.Port != activePort2 {
				activePort3 = proc.Port
			}
		}
	}

	if activePort3 == 0 {
		t.Error("expected active port to be updated after rollback")
	}

	// 6. Test Health Check Eviction / Unhealthy transitions
	proc, _ := orch.GetService(serviceName)
	// Post to /toggle-health to make it unhealthy, with retries to let process bind to port
	toggleURL := fmt.Sprintf("http://127.0.0.1:%d/toggle-health", proc.Port)
	var respToggle *http.Response
	for i := 0; i < 20; i++ {
		respToggle, err = http.Get(toggleURL)
		if err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("failed to toggle health: %v", err)
	}
	respToggle.Body.Close()

	// Wait for health check loop (takes at least 3 failed checks, checked every 2s)
	// So it should take about 6 seconds to change to unhealthy
	timeout = time.After(10 * time.Second)
	unhealthy := false
	for !unhealthy {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for service to transition to unhealthy")
		case <-ticker.C:
			proc, ok := orch.GetService(serviceName)
			if ok && proc.Status == "unhealthy" {
				unhealthy = true
			}
		}
	}
}

func TestOrchestratorIsolationModes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "servcloud-test-modes-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	orch, err := orchestrator.NewOrchestrator(tempDir)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	// 1. Test WASM isolation deployment
	wasmCode := `// runtime: wasm
	print("Hello WASM");`
	procWasm, err := orch.Deploy("wasm-service", wasmCode)
	if err != nil {
		t.Fatalf("WASM deployment failed: %v", err)
	}
	defer orch.Undeploy("wasm-service")

	if procWasm.IsolationMode != "wasm" {
		t.Errorf("Expected isolation mode wasm, got %q", procWasm.IsolationMode)
	}

	// 2. Test Docker isolation deployment
	dockerCode := `// runtime: docker
	print("Hello Docker");`
	procDocker, err := orch.Deploy("docker-service", dockerCode)
	if err != nil {
		t.Fatalf("Docker deployment failed: %v", err)
	}
	defer orch.Undeploy("docker-service")

	if procDocker.IsolationMode != "docker" {
		t.Errorf("Expected isolation mode docker, got %q", procDocker.IsolationMode)
	}

	// Wait and poll for status to become running
	timeoutRun := time.After(15 * time.Second)
	tickerRun := time.NewTicker(200 * time.Millisecond)
	defer tickerRun.Stop()

	wasmRunning := false
	dockerRunning := false

	for !wasmRunning || !dockerRunning {
		select {
		case <-timeoutRun:
			pWasm, _ := orch.GetService("wasm-service")
			pDocker, _ := orch.GetService("docker-service")
			t.Fatalf("Timeout waiting for services to start. WASM mode: %s, Docker mode: %s", pWasm.Status, pDocker.Status)
		case <-tickerRun.C:
			pWasm, _ := orch.GetService("wasm-service")
			if pWasm.Status == "running" {
				wasmRunning = true
			}
			pDocker, _ := orch.GetService("docker-service")
			if pDocker.Status == "running" {
				dockerRunning = true
			}
		}
	}
}
