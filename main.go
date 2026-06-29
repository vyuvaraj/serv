package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/vyuvaraj/ServShared"
)

type Task struct {
	Name      string   `json:"name"`
	DependsOn []string `json:"depends_on"`
	Action    string   `json:"action"` // e.g. "http://..." or "mock-success"
}

type WorkflowDef struct {
	ID    string `json:"id"`
	Tasks []Task `json:"tasks"`
}

type TaskStatus struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"` // pending, running, completed, failed
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type WorkflowInstance struct {
	ID          string                `json:"id"`
	WorkflowID  string                `json:"workflow_id"`
	Status      string                `json:"status"` // running, completed, failed
	TaskStates  map[string]*TaskStatus `json:"task_states"`
	StartedAt   time.Time             `json:"started_at"`
	FinishedAt  time.Time             `json:"finished_at,omitempty"`
	Logs        []string              `json:"logs"`
	mu          sync.RWMutex
}

var (
	definitions = make(map[string]WorkflowDef)
	instances   = make(map[string]*WorkflowInstance)
	mu          sync.RWMutex
)

func main() {
	portStr := flag.String("port", "8096", "ServFlow server port")
	flag.Parse()

	port := os.Getenv("PORT")
	if port == "" {
		port = *portStr
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/resume", handleResume)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)

	serverHandler := ServShared.AuthMiddleware(mux)

	log.Printf("ServFlow engine starting on port %s", port)
	if err := http.ListenAndServe(":"+port, serverHandler); err != nil {
		log.Fatalf("failed to start ServFlow: %v", err)
	}
}

func handleDefine(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var def WorkflowDef
	if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if def.ID == "" || len(def.Tasks) == 0 {
		http.Error(w, "Workflow ID and tasks are required", http.StatusBadRequest)
		return
	}

	mu.Lock()
	definitions[def.ID] = def
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(def)
}

func handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		WorkflowID string `json:"workflow_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	mu.RLock()
	def, exists := definitions[req.WorkflowID]
	mu.RUnlock()

	if !exists {
		http.Error(w, "Workflow definition not found", http.StatusNotFound)
		return
	}

	instanceID := fmt.Sprintf("inst-%d", time.Now().UnixNano())
	taskStates := make(map[string]*TaskStatus)
	for _, task := range def.Tasks {
		taskStates[task.Name] = &TaskStatus{
			Name:   task.Name,
			Status: "pending",
		}
	}

	inst := &WorkflowInstance{
		ID:         instanceID,
		WorkflowID: req.WorkflowID,
		Status:     "running",
		TaskStates: taskStates,
		StartedAt:  time.Now(),
		Logs:       []string{fmt.Sprintf("Workflow %s started.", req.WorkflowID)},
	}

	mu.Lock()
	instances[instanceID] = inst
	mu.Unlock()

	// Execute workflow synchronously/asynchronously.
	// For testing, run it synchronously or in background, here let's run in background
	go runWorkflow(inst, def)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(inst)
}

func handleResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		InstanceID string `json:"instance_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	stateFile := fmt.Sprintf("%s.state", req.InstanceID)
	data, err := os.ReadFile(stateFile)
	if err != nil {
		http.Error(w, "State checkpoint not found: "+err.Error(), http.StatusNotFound)
		return
	}

	var inst WorkflowInstance
	if err := json.Unmarshal(data, &inst); err != nil {
		http.Error(w, "Failed to unmarshal state checkpoint: "+err.Error(), http.StatusInternalServerError)
		return
	}

	mu.RLock()
	def, defExists := definitions[inst.WorkflowID]
	mu.RUnlock()

	if !defExists {
		http.Error(w, "Workflow definition not found", http.StatusNotFound)
		return
	}

	// Reload instance and override state to running
	inst.Status = "running"
	inst.Logs = append(inst.Logs, "Workflow execution resumed from checkpoint.")
	
	// Reset any failed steps back to pending so they rerun
	for _, state := range inst.TaskStates {
		if state.Status == "failed" {
			state.Status = "pending"
			state.Error = ""
		}
	}

	instPointer := &inst

	mu.Lock()
	instances[inst.ID] = instPointer
	mu.Unlock()

	go runWorkflow(instPointer, def)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(instPointer)
}

func handleGetInstance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Path
	// Expect /api/workflows/instances/{id}
	var id string
	fmt.Sscanf(path, "/api/workflows/instances/%s", &id)
	if id == "" {
		http.Error(w, "Instance ID required", http.StatusBadRequest)
		return
	}

	mu.RLock()
	inst, exists := instances[id]
	mu.RUnlock()

	if !exists {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	inst.mu.RLock()
	defer inst.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(inst)
}

// runWorkflow executes tasks in DAG dependency order
func runWorkflow(inst *WorkflowInstance, def WorkflowDef) {
	completedCount := 0
	inst.mu.RLock()
	for _, state := range inst.TaskStates {
		if state.Status == "completed" {
			completedCount++
		}
	}
	inst.mu.RUnlock()

	totalTasks := len(def.Tasks)

	for completedCount < totalTasks {
		inst.mu.Lock()
		if inst.Status == "failed" {
			inst.mu.Unlock()
			return
		}
		inst.mu.Unlock()

		progressMade := false

		for _, task := range def.Tasks {
			inst.mu.Lock()
			state := inst.TaskStates[task.Name]
			if state.Status != "pending" {
				inst.mu.Unlock()
				continue
			}

			// Check if dependencies are satisfied
			depsSatisfied := true
			for _, dep := range task.DependsOn {
				depState, exists := inst.TaskStates[dep]
				if !exists || depState.Status != "completed" {
					depsSatisfied = false
					break
				}
			}

			if !depsSatisfied {
				inst.mu.Unlock()
				continue
			}

			// Start executing task
			state.Status = "running"
			state.StartedAt = time.Now()
			inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s started.", task.Name))
			inst.mu.Unlock()
			saveCheckpoint(inst)

			// Run action logic (simulation)
			err := executeTaskAction(task)

			inst.mu.Lock()
			state.FinishedAt = time.Now()
			if err != nil {
				state.Status = "failed"
				state.Error = err.Error()
				inst.Status = "failed"
				inst.FinishedAt = time.Now()
				inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s failed: %v. Workflow failed.", task.Name, err))
				inst.mu.Unlock()
				saveCheckpoint(inst)
				return
			} else {
				state.Status = "completed"
				inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s completed.", task.Name))
				completedCount++
				progressMade = true
			}
			inst.mu.Unlock()
			saveCheckpoint(inst)
		}

		if !progressMade && completedCount < totalTasks {
			// Cyclic dependency detected!
			inst.mu.Lock()
			inst.Status = "failed"
			inst.FinishedAt = time.Now()
			inst.Logs = append(inst.Logs, "Cycle detected in DAG definition. Workflow aborted.")
			inst.mu.Unlock()
			saveCheckpoint(inst)
			return
		}
	}

	inst.mu.Lock()
	inst.Status = "completed"
	inst.FinishedAt = time.Now()
	inst.Logs = append(inst.Logs, "Workflow completed successfully.")
	inst.mu.Unlock()
	saveCheckpoint(inst)
}

func saveCheckpoint(inst *WorkflowInstance) {
	inst.mu.RLock()
	defer inst.mu.RUnlock()

	data, err := json.Marshal(inst)
	if err != nil {
		return
	}

	_ = os.WriteFile(fmt.Sprintf("%s.state", inst.ID), data, 0644)
}

func executeTaskAction(t Task) error {
	// Simple simulation
	time.Sleep(10 * time.Millisecond)
	if t.Action == "fail" {
		return errors.New("simulated action failure")
	}
	return nil
}
