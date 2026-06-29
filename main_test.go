package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestServFlowDAGExecution(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Define DAG Workflow: Task A -> Task B (depends on A)
	defPayload := WorkflowDef{
		ID: "onboarding-flow",
		Tasks: []Task{
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

	var inst WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	if inst.Status != "running" {
		t.Fatalf("expected running workflow status, got %q", inst.Status)
	}

	// Wait briefly for background execution to complete
	time.Sleep(100 * time.Millisecond)

	// 3. Query Instance Status
	getResp, err := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	if err != nil {
		t.Fatalf("failed to query instance: %v", err)
	}
	defer getResp.Body.Close()

	var finalInst WorkflowInstance
	json.NewDecoder(getResp.Body).Decode(&finalInst)

	if finalInst.Status != "completed" {
		t.Errorf("expected workflow completion, got %q. Logs: %v", finalInst.Status, finalInst.Logs)
	}

	if finalInst.TaskStates["SendWelcomeEmail"].Status != "completed" {
		t.Errorf("expected SendWelcomeEmail to be completed, got %q", finalInst.TaskStates["SendWelcomeEmail"].Status)
	}
}

func TestServFlowDurableExecution(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/resume", handleResume)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Define Workflow: Task 1 -> Task 2 (designed to fail first!)
	defPayload := WorkflowDef{
		ID: "durable-flow",
		Tasks: []Task{
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
	var inst WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	// Wait for failure
	time.Sleep(100 * time.Millisecond)

	// Verify failed status
	getResp, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst WorkflowInstance
	json.NewDecoder(getResp.Body).Decode(&finalInst)
	getResp.Body.Close()

	if finalInst.Status != "failed" {
		t.Fatalf("expected workflow to fail, got %q", finalInst.Status)
	}

	// 3. Fix Task 2 Action in the definition so it succeeds on retry
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

	var resumedInst WorkflowInstance
	json.NewDecoder(resumeResp.Body).Decode(&resumedInst)
	resumeResp.Body.Close()

	if resumedInst.Status != "running" {
		t.Fatalf("expected resumed workflow to be running, got %q", resumedInst.Status)
	}

	// Wait for execution to complete
	time.Sleep(100 * time.Millisecond)

	// Verify completed successfully!
	getResp2, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst2 WorkflowInstance
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
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Define DAG with compensation actions: Task A (success, with rollback) -> Task B (fail)
	defPayload := WorkflowDef{
		ID: "saga-flow",
		Tasks: []Task{
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
	var inst WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	// Wait for execution and rollback
	time.Sleep(100 * time.Millisecond)

	// Query Instance status
	getResp, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst WorkflowInstance
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
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Define DAG Workflow with Retry: Task A fails, but retries up to 2 times
	defPayloadRetry := WorkflowDef{
		ID: "retry-flow",
		Tasks: []Task{
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
	var inst WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	// Wait for execution and retries (3 attempts total: original + 2 retries)
	time.Sleep(150 * time.Millisecond)

	// Query Instance status
	getResp, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst WorkflowInstance
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

	// 2. Define DAG Workflow with Timeout: Task A sleeps 100ms, but has a 30ms timeout
	defPayloadTimeout := WorkflowDef{
		ID: "timeout-flow",
		Tasks: []Task{
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
	var inst2 WorkflowInstance
	json.NewDecoder(execResp2.Body).Decode(&inst2)
	execResp2.Body.Close()

	// Wait for execution and timeout
	time.Sleep(150 * time.Millisecond)

	// Query Instance status
	getResp2, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst2.ID)
	var finalInst2 WorkflowInstance
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
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/approve", handleApprove)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Define DAG Workflow with Approval: Task A (success) -> Task B (approval) -> Task C (success)
	defPayload := WorkflowDef{
		ID: "approval-flow",
		Tasks: []Task{
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
	var inst WorkflowInstance
	json.NewDecoder(execResp.Body).Decode(&inst)
	execResp.Body.Close()

	// Wait briefly for Task A to complete and Task B to pause
	time.Sleep(100 * time.Millisecond)

	// Query Instance status - should be "paused"
	getResp, _ := http.Get(testServer.URL + "/api/workflows/instances/" + inst.ID)
	var finalInst WorkflowInstance
	json.NewDecoder(getResp.Body).Decode(&finalInst)
	getResp.Body.Close()

	if finalInst.Status != "paused" {
		t.Fatalf("expected workflow to be paused on approval step, got %q. Logs: %v", finalInst.Status, finalInst.Logs)
	}
	if finalInst.TaskStates["TaskB"].Status != "pending_approval" {
		t.Errorf("expected TaskB to be pending_approval, got %q", finalInst.TaskStates["TaskB"].Status)
	}

	// Approve task manually
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
	var finalInst2 WorkflowInstance
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
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/history", handleHistory)
	mux.HandleFunc("/api/workflows/replay", handleReplay)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Define DAG Workflow
	defPayload := WorkflowDef{
		ID: "replay-flow",
		Tasks: []Task{
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
	var inst WorkflowInstance
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

	var history []WorkflowInstance
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

	var replayInst WorkflowInstance
	json.NewDecoder(replayResp.Body).Decode(&replayInst)

	if replayInst.ID == "" || replayInst.ID == inst.ID {
		t.Errorf("expected new instance ID for replay, got %q", replayInst.ID)
	}

	// Wait briefly for replay completion
	time.Sleep(100 * time.Millisecond)

	// Verify replay completed successfully!
	getResp, _ := http.Get(testServer.URL + "/api/workflows/instances/" + replayInst.ID)
	var finalReplay WorkflowInstance
	json.NewDecoder(getResp.Body).Decode(&finalReplay)
	getResp.Body.Close()

	if finalReplay.Status != "completed" {
		t.Errorf("expected replay workflow to complete, got %q. Logs: %v", finalReplay.Status, finalReplay.Logs)
	}

	_ = os.Remove(inst.ID + ".state")
	_ = os.Remove(replayInst.ID + ".state")
}

func TestServFlowDAGValidationAndVisualization(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/workflows/validate", handleValidate)
	mux.HandleFunc("/api/workflows/visualize", handleVisualize)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// 1. Send cyclic workflow def -> should fail validation (400 Bad Request)
	cyclicPayload := WorkflowDef{
		ID: "cyclic-flow",
		Tasks: []Task{
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
	validPayload := WorkflowDef{
		ID: "valid-flow",
		Tasks: []Task{
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
