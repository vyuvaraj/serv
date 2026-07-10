package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"servflow/pkg/storage"
)

func setupTest() {
	mu.Lock()
	definitions = make(map[string]storage.WorkflowDef)
	instances = make(map[string]*storage.WorkflowInstance)
	mu.Unlock()
}

func TestServFlowDAGExecution(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Define DAG Workflow: storage.Task A -> storage.Task B (depends on A)
	defPayload := storage.WorkflowDef{
		ID: "onboarding-flow",
		Tasks: []storage.Task{
			{Name: "CreateUser", DependsOn: nil, Action: "success"},
			{Name: "SendWelcomeEmail", DependsOn: []string{"CreateUser"}, Action: "success"},
		},
	}
	body, _ := json.Marshal(defPayload)
	resp, err := http.Post(testServer.URL+"/api/workflows/define", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to define workflow: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected StatusCreated, got %d", resp.StatusCode)
	}

	// 2. Execute Workflow Instance
	execPayload := map[string]string{"workflow_id": "onboarding-flow"}
	execBody, _ := json.Marshal(execPayload)
	execResp, err := http.Post(testServer.URL+"/api/workflows/execute", "application/json", bytes.NewReader(execBody))
	if err != nil {
		t.Fatalf("failed to execute workflow: %v", err)
	}

	var inst storage.WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	if inst.Status != "running" {
		t.Fatalf("expected running workflow status, got %q", inst.Status)
	}

	// Poll for workflow completion (up to 1.5 seconds)
	var finalInst storage.WorkflowInstance
	for i := 0; i < 30; i++ {
		getResp, err := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
		if err == nil {
			json.NewDecoder(getResp.Body).Decode(&finalInst)
			getResp.Body.Close()
			if finalInst.Status == "completed" {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	if finalInst.Status != "completed" {
		t.Errorf("expected workflow completion, got %q. Logs: %v", finalInst.Status, finalInst.Logs)
	}

	if finalInst.TaskStates["SendWelcomeEmail"].Status != "completed" {
		t.Errorf("expected SendWelcomeEmail to be completed, got %q", finalInst.TaskStates["SendWelcomeEmail"].Status)
	}
}

func TestServFlowDurableExecution(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/resume", handleResume)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Define Workflow: storage.Task 1 -> storage.Task 2 (designed to fail first!)
	defPayload := storage.WorkflowDef{
		ID: "durable-flow",
		Tasks: []storage.Task{
			{Name: "Task1", DependsOn: nil, Action: "success"},
			{Name: "Task2", DependsOn: []string{"Task1"}, Action: "fail"}, // will fail!
		},
	}
	body, _ := json.Marshal(defPayload)
	resp, _ := http.Post(testServer.URL+"/api/workflows/define", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	// 2. Execute
	execPayload := map[string]string{"workflow_id": "durable-flow"}
	execBody, _ := json.Marshal(execPayload)
	execResp, _ := http.Post(testServer.URL+"/api/workflows/execute", "application/json", bytes.NewReader(execBody))
	var inst storage.WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	// Wait for failure
	time.Sleep(100 * time.Millisecond)

	// Verify failed status
	getResp, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst storage.WorkflowInstance
	json.NewDecoder(getResp.Body).Decode(&finalInst)
	getResp.Body.Close()

	if finalInst.Status != "failed" {
		t.Fatalf("expected workflow to fail, got %q", finalInst.Status)
	}

	// 3. Fix storage.Task 2 Action in the definition so it succeeds on retry
	defPayload.Tasks[1].Action = "success"
	body2, _ := json.Marshal(defPayload)
	resp2, _ := http.Post(testServer.URL+"/api/workflows/define", "application/json", bytes.NewReader(body2))
	resp2.Body.Close()

	// 4. Resume Workflow from checkpoint
	resumePayload := map[string]string{"instance_id": inst.ID}
	resumeBody, _ := json.Marshal(resumePayload)
	resumeResp, err := http.Post(testServer.URL+"/api/workflows/resume", "application/json", bytes.NewReader(resumeBody))
	if err != nil {
		t.Fatalf("failed to resume workflow: %v", err)
	}

	var resumedInst storage.WorkflowInstance
	json.NewDecoder(resumeResp.Body).Decode(&resumedInst)
	resumeResp.Body.Close()

	if resumedInst.Status != "running" {
		t.Fatalf("expected resumed workflow to be running, got %q", resumedInst.Status)
	}

	// Wait for execution to complete
	time.Sleep(100 * time.Millisecond)

	// Verify completed successfully!
	getResp2, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst2 storage.WorkflowInstance
	json.NewDecoder(getResp2.Body).Decode(&finalInst2)
	getResp2.Body.Close()

	if finalInst2.Status != "completed" {
		t.Errorf("expected resumed workflow to complete successfully, got %q. Logs: %v", finalInst2.Status, finalInst2.Logs)
	}

	// Clean up checkpoint file
	time.Sleep(50 * time.Millisecond)
	_ = os.Remove(inst.ID + ".state")
}

func TestServFlowSagaCompensation(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Define DAG with compensation actions: storage.Task A (success, with rollback) -> storage.Task B (fail)
	defPayload := storage.WorkflowDef{
		ID: "saga-flow",
		Tasks: []storage.Task{
			{Name: "ChargeCard", DependsOn: nil, Action: "success", CompensateAction: "RefundCard"},
			{Name: "ReserveSeat", DependsOn: []string{"ChargeCard"}, Action: "fail"}, // triggers failure and rollback
		},
	}
	body, _ := json.Marshal(defPayload)
	resp, _ := http.Post(testServer.URL+"/api/workflows/define", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	// Execute
	execPayload := map[string]string{"workflow_id": "saga-flow"}
	execBody, _ := json.Marshal(execPayload)
	execResp, _ := http.Post(testServer.URL+"/api/workflows/execute", "application/json", bytes.NewReader(execBody))
	var inst storage.WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	// Wait for execution and rollback
	time.Sleep(100 * time.Millisecond)

	// Query Instance status
	getResp, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst storage.WorkflowInstance
	json.NewDecoder(getResp.Body).Decode(&finalInst)
	getResp.Body.Close()

	if finalInst.Status != "failed" {
		t.Fatalf("expected saga workflow to fail overall, got %q", finalInst.Status)
	}

	// Verify that ChargeCard was compensated
	if finalInst.TaskStates["ChargeCard"].Status != "compensated" {
		t.Errorf("expected ChargeCard status to be compensated, got %q", finalInst.TaskStates["ChargeCard"].Status)
	}

	// Check that logs mention the compensation rollback
	foundSagaLog := false
	for _, l := range finalInst.Logs {
		if idx := strings.Index(l, "[SAGA] Executing compensation rollback for task ChargeCard: RefundCard"); idx >= 0 {
			foundSagaLog = true
			break
		}
	}

	if !foundSagaLog {
		t.Errorf("expected saga rollback execution print in logs, got %v", finalInst.Logs)
	}

	// Clean up state file
	_ = os.Remove(inst.ID + ".state")
}

func TestServFlowRetriesAndTimeouts(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Define DAG Workflow with Retry: storage.Task A fails, but retries up to 2 times
	defPayloadRetry := storage.WorkflowDef{
		ID: "retry-flow",
		Tasks: []storage.Task{
			{Name: "RetryTask", DependsOn: nil, Action: "fail", RetryCount: 2},
		},
	}
	body, _ := json.Marshal(defPayloadRetry)
	resp, _ := http.Post(testServer.URL+"/api/workflows/define", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	// Execute
	execPayload := map[string]string{"workflow_id": "retry-flow"}
	execBody, _ := json.Marshal(execPayload)
	execResp, _ := http.Post(testServer.URL+"/api/workflows/execute", "application/json", bytes.NewReader(execBody))
	var inst storage.WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	// Wait for execution and retries (3 attempts total: original + 2 retries)
	time.Sleep(150 * time.Millisecond)

	// Query Instance status
	getResp, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst storage.WorkflowInstance
	json.NewDecoder(getResp.Body).Decode(&finalInst)
	getResp.Body.Close()

	if finalInst.Status != "failed" {
		t.Fatalf("expected workflow to eventually fail, got %q", finalInst.Status)
	}

	// Verify that retries were logged
	foundRetryLog := false
	for _, l := range finalInst.Logs {
		if strings.Contains(l, "failed attempt 1") || strings.Contains(l, "failed attempt 2") {
			foundRetryLog = true
			break
		}
	}
	if !foundRetryLog {
		t.Errorf("expected retry logs in workflow instance, got: %v", finalInst.Logs)
	}
	_ = os.Remove(inst.ID + ".state")

	// 2. Define DAG Workflow with Timeout: storage.Task A sleeps 100ms, but has a 30ms timeout
	defPayloadTimeout := storage.WorkflowDef{
		ID: "timeout-flow",
		Tasks: []storage.Task{
			{Name: "SlowTask", DependsOn: nil, Action: "sleep-100", TimeoutMs: 30},
		},
	}
	body2, _ := json.Marshal(defPayloadTimeout)
	resp2, _ := http.Post(testServer.URL+"/api/workflows/define", "application/json", bytes.NewReader(body2))
	resp2.Body.Close()

	// Execute
	execPayload2 := map[string]string{"workflow_id": "timeout-flow"}
	execBody2, _ := json.Marshal(execPayload2)
	execResp2, _ := http.Post(testServer.URL+"/api/workflows/execute", "application/json", bytes.NewReader(execBody2))
	var inst2 storage.WorkflowInstance
	json.NewDecoder(execResp2.Body).Decode(&inst2)
	execResp2.Body.Close()

	// Wait for execution and timeout
	time.Sleep(150 * time.Millisecond)

	// Query Instance status
	getResp2, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst2.ID)
	var finalInst2 storage.WorkflowInstance
	json.NewDecoder(getResp2.Body).Decode(&finalInst2)
	getResp2.Body.Close()

	if finalInst2.Status != "failed" {
		t.Fatalf("expected workflow to fail due to timeout, got %q", finalInst2.Status)
	}

	// Verify that timeout failure was logged
	foundTimeoutLog := false
	for _, l := range finalInst2.Logs {
		if strings.Contains(l, "task timed out after 30ms") {
			foundTimeoutLog = true
			break
		}
	}
	if !foundTimeoutLog {
		t.Errorf("expected timeout log in workflow instance, got: %v", finalInst2.Logs)
	}
	_ = os.Remove(inst2.ID + ".state")
}

func TestServFlowHumanApprovalGates(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/approve", handleApprove)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Define DAG Workflow with Approval: storage.Task A (success) -> storage.Task B (approval) -> storage.Task C (success)
	defPayload := storage.WorkflowDef{
		ID: "approval-flow",
		Tasks: []storage.Task{
			{Name: "TaskA", DependsOn: nil, Action: "success"},
			{Name: "TaskB", DependsOn: []string{"TaskA"}, Action: "approval"},
			{Name: "TaskC", DependsOn: []string{"TaskB"}, Action: "success"},
		},
	}
	body, _ := json.Marshal(defPayload)
	resp, _ := http.Post(testServer.URL+"/api/workflows/define", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	// Execute
	execPayload := map[string]string{"workflow_id": "approval-flow"}
	execBody, _ := json.Marshal(execPayload)
	execResp, _ := http.Post(testServer.URL+"/api/workflows/execute", "application/json", bytes.NewReader(execBody))
	var inst storage.WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	// Wait briefly for storage.Task A to complete and storage.Task B to pause
	time.Sleep(100 * time.Millisecond)

	// Query Instance status - should be "paused"
	getResp, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst storage.WorkflowInstance
	json.NewDecoder(getResp.Body).Decode(&finalInst)
	getResp.Body.Close()

	if finalInst.Status != "paused" {
		t.Fatalf("expected workflow to be paused on approval step, got %q. Logs: %v", finalInst.Status, finalInst.Logs)
	}
	if finalInst.TaskStates["TaskB"].Status != "pending_approval" {
		t.Errorf("expected TaskB to be pending_approval, got %q", finalInst.TaskStates["TaskB"].Status)
	}

	// Approve storage.Task manually
	approvePayload := map[string]string{
		"instance_id": inst.ID,
		"task_name":   "TaskB",
		"decision":    "approve",
	}
	approveBody, _ := json.Marshal(approvePayload)
	approveResp, _ := http.Post(testServer.URL+"/api/workflows/approve", "application/json", bytes.NewReader(approveBody))
	approveResp.Body.Close()

	// Wait for continuation
	time.Sleep(100 * time.Millisecond)

	// Verify completed successfully!
	getResp2, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst2 storage.WorkflowInstance
	json.NewDecoder(getResp2.Body).Decode(&finalInst2)
	getResp2.Body.Close()

	if finalInst2.Status != "completed" {
		t.Errorf("expected workflow to complete after approval, got %q. Logs: %v", finalInst2.Status, finalInst2.Logs)
	}
	if finalInst2.TaskStates["TaskC"].Status != "completed" {
		t.Errorf("expected TaskC to be completed, got %q", finalInst2.TaskStates["TaskC"].Status)
	}

	_ = os.Remove(inst.ID + ".state")
}

func TestServFlowHistoryAndReplay(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/history", handleHistory)
	mux.HandleFunc("/api/workflows/replay", handleReplay)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Define DAG Workflow
	defPayload := storage.WorkflowDef{
		ID: "replay-flow",
		Tasks: []storage.Task{
			{Name: "Task1", DependsOn: nil, Action: "success"},
		},
	}
	body, _ := json.Marshal(defPayload)
	resp, _ := http.Post(testServer.URL+"/api/workflows/define", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	// 2. Execute
	execPayload := map[string]string{"workflow_id": "replay-flow"}
	execBody, _ := json.Marshal(execPayload)
	execResp, _ := http.Post(testServer.URL+"/api/workflows/execute", "application/json", bytes.NewReader(execBody))
	var inst storage.WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	// Wait briefly to complete
	time.Sleep(100 * time.Millisecond)

	// 3. Query history
	histResp, err := http.Get(testServer.URL + "/api/workflows/history")
	if err != nil {
		t.Fatalf("failed to query history: %v", err)
	}
	defer histResp.Body.Close()

	var history []storage.WorkflowInstance
	json.NewDecoder(histResp.Body).Decode(&history)

	found := false
	for i := range history {
		if history[i].ID == inst.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected to find completed instance %s in history", inst.ID)
	}

	// 4. Trigger replay
	replayPayload := map[string]string{"instance_id": inst.ID}
	replayBody, _ := json.Marshal(replayPayload)
	replayResp, err := http.Post(testServer.URL+"/api/workflows/replay", "application/json", bytes.NewReader(replayBody))
	if err != nil {
		t.Fatalf("replay post failed: %v", err)
	}
	defer replayResp.Body.Close()

	if replayResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected StatusCreated, got %d", replayResp.StatusCode)
	}

	var replayInst storage.WorkflowInstance
	json.NewDecoder(replayResp.Body).Decode(&replayInst)

	if replayInst.ID == "" || replayInst.ID == inst.ID {
		t.Errorf("expected new instance ID for replay, got %q", replayInst.ID)
	}

	// Wait briefly for replay completion
	time.Sleep(100 * time.Millisecond)

	// Verify replay completed successfully!
	getResp, _ := http.Get(testServer.URL + "/api/workflows/instances/" + replayInst.ID)
	var finalReplay storage.WorkflowInstance
	json.NewDecoder(getResp.Body).Decode(&finalReplay)
	getResp.Body.Close()

	if finalReplay.Status != "completed" {
		t.Errorf("expected replay workflow to complete, got %q. Logs: %v", finalReplay.Status, finalReplay.Logs)
	}

	_ = os.Remove(inst.ID + ".state")
	_ = os.Remove(replayInst.ID + ".state")
}

func TestServFlowDAGValidationAndVisualization(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/validate", handleValidate)
	mux.HandleFunc("/api/workflows/visualize", handleVisualize)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Send cyclic workflow def -> should fail validation (400 Bad Request)
	cyclicPayload := storage.WorkflowDef{
		ID: "cyclic-flow",
		Tasks: []storage.Task{
			{Name: "TaskA", DependsOn: []string{"TaskB"}},
			{Name: "TaskB", DependsOn: []string{"TaskA"}},
		},
	}
	bodyC, _ := json.Marshal(cyclicPayload)
	respC, err := http.Post(testServer.URL+"/api/workflows/validate", "application/json", bytes.NewReader(bodyC))
	if err != nil {
		t.Fatalf("validate failed: %v", err)
	}
	defer respC.Body.Close()

	if respC.StatusCode != http.StatusBadRequest {
		t.Errorf("expected StatusBadRequest (400) for cyclic flow, got %d", respC.StatusCode)
	}

	var validRes struct {
		Valid bool   `json:"valid"`
		Error string `json:"error"`
	}
	json.NewDecoder(respC.Body).Decode(&validRes)
	if validRes.Valid || !strings.Contains(validRes.Error, "Cyclic dependency") {
		t.Errorf("invalid cycle validation response: %+v", validRes)
	}

	// 2. Send valid DAG -> should succeed validation (200 OK)
	validPayload := storage.WorkflowDef{
		ID: "valid-flow",
		Tasks: []storage.Task{
			{Name: "TaskA", DependsOn: nil},
			{Name: "TaskB", DependsOn: []string{"TaskA"}},
		},
	}
	bodyV, _ := json.Marshal(validPayload)
	respV, err := http.Post(testServer.URL+"/api/workflows/validate", "application/json", bytes.NewReader(bodyV))
	if err != nil || respV.StatusCode != http.StatusOK {
		t.Fatalf("validation failed for valid flow: %v", err)
	}
	respV.Body.Close()

	// 3. Request visualization -> should return correct Mermaid representation
	respVis, err := http.Post(testServer.URL+"/api/workflows/visualize", "application/json", bytes.NewReader(bodyV))
	if err != nil || respVis.StatusCode != http.StatusOK {
		t.Fatalf("visualize failed: %v", err)
	}
	defer respVis.Body.Close()

	var visRes struct {
		Mermaid string `json:"mermaid"`
	}
	json.NewDecoder(respVis.Body).Decode(&visRes)

	if !strings.Contains(visRes.Mermaid, "TaskA --> TaskB") || !strings.Contains(visRes.Mermaid, "graph TD") {
		t.Errorf("unexpected mermaid output: %q", visRes.Mermaid)
	}
}

func TestTableDrivenWorkflowValidation(t *testing.T) {
	setupTest()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	tests := []struct {
		name       string
		id         string
		tasks      []storage.Task
		wantStatus int
	}{
		{
			name:       "Missing ID",
			id:         "",
			tasks:      []storage.Task{{Name: "TaskA"}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "Empty Tasks",
			id:         "empty-flow",
			tasks:      nil,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := storage.WorkflowDef{
				ID:    tt.id,
				Tasks: tt.tasks,
			}
			body, _ := json.Marshal(payload)
			resp, err := http.Post(testServer.URL+"/api/workflows/define", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("failed to make request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}
		})
	}
}

func BenchmarkWorkflowDefinitionLookup(b *testing.B) {
	setupTest()
	// Pre-populate definitions
	for i := 0; i < 1000; i++ {
		wfID := fmt.Sprintf("workflow-%d", i)
		mu.Lock()
		definitions[wfID] = storage.WorkflowDef{
			ID: wfID,
			Tasks: []storage.Task{
				{Name: "step-a", Action: "mock-success"},
				{Name: "step-b", DependsOn: []string{"step-a"}, Action: "mock-success"},
			},
		}
		mu.Unlock()
	}

	i := 0
	for b.Loop() {
		key := fmt.Sprintf("workflow-%d", i%1000)
		mu.RLock()
		_, _ = definitions[key]
		mu.RUnlock()
		i++
	}
}

func startMockStompServer(t *testing.T) (string, func()) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	
	stopChan := make(chan struct{})
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				select {
				case <-stopChan:
					return
				default:
					continue
				}
			}
			
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				n, _ := c.Read(buf)
				if n > 0 && strings.HasPrefix(string(buf), "CONNECT") {
					c.Write([]byte("CONNECTED\nversion:1.2\n\n\x00"))
				}
				_, _ = c.Read(buf)
			}(conn)
		}
	}()
	
	return l.Addr().String(), func() {
		close(stopChan)
		l.Close()
	}
}

func TestEventDrivenSagaCompensation(t *testing.T) {
	setupTest()
	
	brokerAddr, cleanup := startMockStompServer(t)
	defer cleanup()
	t.Setenv("SERVQUEUE_ADDR", brokerAddr)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)
	mux.HandleFunc("/api/workflows/compensate/complete", handleCompensateComplete)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	defPayload := storage.WorkflowDef{
		ID: "event-saga-flow",
		Tasks: []storage.Task{
			{Name: "ChargeCard", DependsOn: nil, Action: "success", CompensateAction: "event://RefundCard"},
			{Name: "ReserveSeat", DependsOn: []string{"ChargeCard"}, Action: "fail"},
		},
	}
	body, _ := json.Marshal(defPayload)
	resp, _ := http.Post(testServer.URL+"/api/workflows/define", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	execPayload := map[string]string{"workflow_id": "event-saga-flow"}
	execBody, _ := json.Marshal(execPayload)
	execResp, _ := http.Post(testServer.URL+"/api/workflows/execute", "application/json", bytes.NewReader(execBody))
	var inst storage.WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	time.Sleep(150 * time.Millisecond)

	getResp, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var intermediateInst storage.WorkflowInstance
	json.NewDecoder(getResp.Body).Decode(&intermediateInst)
	getResp.Body.Close()

	if intermediateInst.TaskStates["ChargeCard"].Status != "compensating" {
		t.Errorf("expected ChargeCard status to be compensating, got %q", intermediateInst.TaskStates["ChargeCard"].Status)
	}

	completePayload := map[string]string{
		"instance_id": inst.ID,
		"task_name":   "ChargeCard",
		"status":      "success",
	}
	cBody, _ := json.Marshal(completePayload)
	cResp, _ := http.Post(testServer.URL+"/api/workflows/compensate/complete", "application/json", bytes.NewReader(cBody))
	cResp.Body.Close()

	time.Sleep(100 * time.Millisecond)

	getResp2, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst storage.WorkflowInstance
	json.NewDecoder(getResp2.Body).Decode(&finalInst)
	getResp2.Body.Close()

	if finalInst.TaskStates["ChargeCard"].Status != "compensated" {
		t.Errorf("expected final status to be compensated, got %q", finalInst.TaskStates["ChargeCard"].Status)
	}

	_ = os.Remove(inst.ID + ".state")
}

func TestTimeTravelWorkflowReplay(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)
	mux.HandleFunc("/api/instances/", handleTimeTravelReplay)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Define a 3-task sequential workflow
	defPayload := storage.WorkflowDef{
		ID: "replay-test-flow",
		Tasks: []storage.Task{
			{Name: "StepA", DependsOn: nil, Action: "success"},
			{Name: "StepB", DependsOn: []string{"StepA"}, Action: "success"},
			{Name: "StepC", DependsOn: []string{"StepB"}, Action: "success"},
		},
	}
	body, _ := json.Marshal(defPayload)
	resp, err := http.Post(testServer.URL+"/api/workflows/define", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to define workflow: %v", err)
	}
	resp.Body.Close()

	// 2. Execute workflow
	execPayload := map[string]string{"workflow_id": "replay-test-flow"}
	execBody, _ := json.Marshal(execPayload)
	execResp, err := http.Post(testServer.URL+"/api/workflows/execute", "application/json", bytes.NewReader(execBody))
	if err != nil {
		t.Fatalf("failed to execute workflow: %v", err)
	}
	var inst storage.WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	// 3. Wait for completion
	var finalInst storage.WorkflowInstance
	for i := 0; i < 30; i++ {
		getResp, err := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
		if err == nil {
			json.NewDecoder(getResp.Body).Decode(&finalInst)
			getResp.Body.Close()
			if finalInst.Status == "completed" {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if finalInst.Status != "completed" {
		t.Fatalf("expected workflow to complete, got %q. Logs: %v", finalInst.Status, finalInst.Logs)
	}

	// 4. Fetch the full time-travel replay log
	replayResp, err := http.Get(testServer.URL + "/api/instances/" + inst.ID + "/replay")
	if err != nil {
		t.Fatalf("failed to fetch replay log: %v", err)
	}
	defer replayResp.Body.Close()
	if replayResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from replay endpoint, got %d", replayResp.StatusCode)
	}

	var replayResult map[string]interface{}
	json.NewDecoder(replayResp.Body).Decode(&replayResult)

	totalSteps := int(replayResult["total_steps"].(float64))
	// 3 tasks × 2 events (started + completed) = 6 snapshots minimum
	if totalSteps < 6 {
		t.Errorf("expected at least 6 replay steps for 3 tasks, got %d", totalSteps)
	}

	// 5. Fetch step 0 individually
	step0Resp, err := http.Get(testServer.URL + "/api/instances/" + inst.ID + "/replay?step=0")
	if err != nil {
		t.Fatalf("failed to fetch step 0: %v", err)
	}
	defer step0Resp.Body.Close()
	if step0Resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 fetching step 0, got %d", step0Resp.StatusCode)
	}

	var snap storage.ReplaySnapshot
	json.NewDecoder(step0Resp.Body).Decode(&snap)
	if snap.StepIndex != 0 {
		t.Errorf("expected step_index=0, got %d", snap.StepIndex)
	}
	if snap.Status == "" {
		t.Error("expected replay snapshot to have a status")
	}

	// 6. Out-of-range step returns 400
	badResp, _ := http.Get(fmt.Sprintf("%s/api/instances/%s/replay?step=%d", testServer.URL, inst.ID, totalSteps+10))
	if badResp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for out-of-range step, got %d", badResp.StatusCode)
	}
	badResp.Body.Close()

	_ = os.Remove(inst.ID + ".state")
}

func TestSagaComplexRollbackFailures(t *testing.T) {
	setupTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Define DAG: TaskA (succeeds, but compensation fails) -> TaskB (times out, triggers rollback)
	defPayload := storage.WorkflowDef{
		ID: "complex-saga-flow",
		Tasks: []storage.Task{
			{Name: "TaskA", DependsOn: nil, Action: "success", CompensateAction: "fail"},
			{Name: "TaskB", DependsOn: []string{"TaskA"}, Action: "sleep-100", TimeoutMs: 20}, // will time out
		},
	}
	body, _ := json.Marshal(defPayload)
	resp, _ := http.Post(testServer.URL+"/api/workflows/define", "application/json", bytes.NewReader(body))
	resp.Body.Close()

	// Execute
	execPayload := map[string]string{"workflow_id": "complex-saga-flow"}
	execBody, _ := json.Marshal(execPayload)
	execResp, _ := http.Post(testServer.URL+"/api/workflows/execute", "application/json", bytes.NewReader(execBody))
	var inst storage.WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	// Wait for execution, timeout, and rollback attempt
	time.Sleep(150 * time.Millisecond)

	// Fetch final instance details
	getResp, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst storage.WorkflowInstance
	json.NewDecoder(getResp.Body).Decode(&finalInst)
	getResp.Body.Close()

	// Overall status should be failed
	if finalInst.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", finalInst.Status)
	}

	// TaskB must have failed due to timeout
	stateB, existsB := finalInst.TaskStates["TaskB"]
	if !existsB {
		t.Fatal("TaskB state should exist")
	}
	if stateB.Status != "failed" || !strings.Contains(stateB.Error, "timed out") {
		t.Errorf("expected TaskB to fail with timeout, got status %q, error %q", stateB.Status, stateB.Error)
	}

	// TaskA compensation should have been attempted and failed
	stateA, existsA := finalInst.TaskStates["TaskA"]
	if !existsA {
		t.Fatal("TaskA state should exist")
	}
	if stateA.Status != "failed" {
		t.Errorf("expected TaskA status to be 'failed', got %q", stateA.Status)
	}

	// Check logs for timeout and compensation failure messages
	foundTimeoutLog := false
	foundCompFailLog := false
	for _, l := range finalInst.Logs {
		if strings.Contains(l, "timed out") {
			foundTimeoutLog = true
		}
		if strings.Contains(l, "Compensation failed for task TaskA") {
			foundCompFailLog = true
		}
	}

	if !foundTimeoutLog {
		t.Error("expected logs to contain task timeout message")
	}
	if !foundCompFailLog {
		t.Error("expected logs to contain TaskA compensation failure message")
	}

	_ = os.Remove(inst.ID + ".state")
}


