package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
