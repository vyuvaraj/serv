package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vyuvaraj/ServShared"
	"servflow/pkg/engine"
	"servflow/pkg/storage"
)

var (
	definitions = make(map[string]storage.WorkflowDef)
	instances   = make(map[string]*storage.WorkflowInstance)
	mu          sync.RWMutex
)

var workflowStore storage.WorkflowStore

func initStore() {
	client := ServShared.NewStoreClient()
	workflowStore = storage.NewServStoreWorkflowStore(client)
	loadStateFromStore()
}

func loadStateFromStore() {
	if defs, err := workflowStore.LoadDefinitions(); err == nil {
		mu.Lock()
		definitions = defs
		mu.Unlock()
	}
	if insts, err := workflowStore.LoadInstances(); err == nil {
		mu.Lock()
		instances = insts
		mu.Unlock()
	}
}

func saveDefinitionsToStore() {
	if workflowStore == nil {
		return
	}
	mu.RLock()
	copied := make(map[string]storage.WorkflowDef)
	for k, v := range definitions {
		copied[k] = v
	}
	mu.RUnlock()
	_ = workflowStore.SaveDefinitions(copied)
}

func saveInstancesToStore() {
	if workflowStore == nil {
		return
	}
	mu.RLock()
	copied := make(map[string]*storage.WorkflowInstance)
	for k, v := range instances {
		copied[k] = v
	}
	mu.RUnlock()
	_ = workflowStore.SaveInstances(copied)
}

func main() {
	portStr := flag.String("port", "8096", "ServFlow server port")
	flag.Parse()

	port := os.Getenv("PORT")
	if port == "" {
		port = *portStr
	}

	initStore()

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/api/version", ServShared.VersionHandler("servflow", "1.0.0"))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/api/workflows/define", handleDefine)
	mux.HandleFunc("/api/workflows/execute", handleExecute)
	mux.HandleFunc("/api/workflows/resume", handleResume)
	mux.HandleFunc("/api/workflows/approve", handleApprove)
	mux.HandleFunc("/api/workflows/instances/", handleGetInstance)
	mux.HandleFunc("/api/workflows/history", handleHistory)
	mux.HandleFunc("/api/workflows/replay", handleReplay)
	mux.HandleFunc("/api/workflows/validate", handleValidate)
	mux.HandleFunc("/api/workflows/visualize", handleVisualize)
	mux.HandleFunc("/api/workflows/compensate/complete", handleCompensateComplete)

	serverHandler := ServShared.TraceMiddleware("servflow", ServShared.AuthMiddleware(mux))

	server := &http.Server{
		Addr:    ":" + port,
		Handler: serverHandler,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[INFO] ServFlow engine starting on port %s", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("failed to start ServFlow: %v", err)
		}
	}()

	<-stop

	log.Println("[INFO] Shutting down ServFlow server...")
	ServShared.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}
	log.Println("[INFO] ServFlow server exited cleanly")
}

func handleDefine(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var def storage.WorkflowDef
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
	saveDefinitionsToStore()

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
	taskStates := make(map[string]*storage.TaskStatus)
	for _, task := range def.Tasks {
		taskStates[task.Name] = &storage.TaskStatus{
			Name:   task.Name,
			Status: "pending",
		}
	}

	inst := &storage.WorkflowInstance{
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
	go engine.RunWorkflow(inst, def, workflowStore, instances, &mu)

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
	var data []byte
	var err error
	if workflowStore != nil && workflowStore.GetClient() != nil {
		data, err = workflowStore.GetClient().Get("serv-flow-state", stateFile)
	}
	if err != nil || len(data) == 0 {
		data, err = os.ReadFile(stateFile)
	}
	if err != nil {
		http.Error(w, "State checkpoint not found: "+err.Error(), http.StatusNotFound)
		return
	}

	var inst storage.WorkflowInstance
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

	instPointer := &inst

	isCompensating := false
	for _, state := range inst.TaskStates {
		if state.Status == "compensating" {
			isCompensating = true
			break
		}
	}

	mu.Lock()
	instances[inst.ID] = instPointer
	mu.Unlock()

	if isCompensating {
		instPointer.Logs = append(instPointer.Logs, "Resuming saga rollback execution from checkpoint.")
		instPointer.Status = "failed"
		go engine.RollbackSaga(instPointer, def, workflowStore, instances, &mu)
	} else {
		// Reload instance and override state to running
		instPointer.Status = "running"
		instPointer.Logs = append(instPointer.Logs, "Workflow execution resumed from checkpoint.")

		// Reset any failed steps back to pending so they rerun
		for _, state := range instPointer.TaskStates {
			if state.Status == "failed" {
				state.Status = "pending"
				state.Error = ""
			}
		}
		go engine.RunWorkflow(instPointer, def, workflowStore, instances, &mu)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(instPointer)
}

func handleApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		InstanceID string `json:"instance_id"`
		TaskName   string `json:"task_name"`
		Decision   string `json:"decision"` // approve or reject
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	mu.Lock()
	inst, exists := instances[req.InstanceID]
	mu.Unlock()

	if !exists {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	inst.Mu.Lock()
	state, taskExists := inst.TaskStates[req.TaskName]
	if !taskExists || state.Status != "pending_approval" {
		inst.Mu.Unlock()
		http.Error(w, "Task is not pending approval", http.StatusBadRequest)
		return
	}

	if req.Decision == "approve" {
		state.Status = "completed"
		inst.Status = "running"
		inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s approved manually. Resuming workflow...", req.TaskName))
	} else {
		state.Status = "failed"
		state.Error = "Manual approval rejected"
		inst.Status = "failed"
		inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s rejected manually. Initiating Saga compensations...", req.TaskName))
	}
	inst.Mu.Unlock()
	engine.SaveCheckpoint(inst, workflowStore, instances, &mu)

	mu.RLock()
	def := definitions[inst.WorkflowID]
	mu.RUnlock()

	if req.Decision == "approve" {
		go engine.RunWorkflow(inst, def, workflowStore, instances, &mu)
	} else {
		inst.Mu.Lock()
		inst.FinishedAt = time.Now()
		for i := len(def.Tasks) - 1; i >= 0; i-- {
			t := def.Tasks[i]
			tState := inst.TaskStates[t.Name]
			if tState.Status == "completed" && t.CompensateAction != "" {
				inst.Logs = append(inst.Logs, fmt.Sprintf("[SAGA] Executing compensation rollback for task %s: %s", t.Name, t.CompensateAction))
				tState.Status = "compensated"
			}
		}
		inst.Mu.Unlock()
		engine.SaveCheckpoint(inst, workflowStore, instances, &mu)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(inst)
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

	inst.Mu.RLock()
	defer inst.Mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(inst)
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	mu.RLock()
	defer mu.RUnlock()

	var history []*storage.WorkflowInstance
	for _, inst := range instances {
		inst.Mu.RLock()
		if inst.Status == "completed" || inst.Status == "failed" {
			history = append(history, inst)
		}
		inst.Mu.RUnlock()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(history)
}

func handleReplay(w http.ResponseWriter, r *http.Request) {
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

	mu.Lock()
	oldInst, exists := instances[req.InstanceID]
	mu.Unlock()

	if !exists {
		http.Error(w, "Original instance not found", http.StatusNotFound)
		return
	}

	mu.RLock()
	def, defExists := definitions[oldInst.WorkflowID]
	mu.RUnlock()

	if !defExists {
		http.Error(w, "Workflow definition not found", http.StatusNotFound)
		return
	}

	newID := fmt.Sprintf("inst-%d-replay", time.Now().UnixNano())
	newInst := &storage.WorkflowInstance{
		ID:         newID,
		WorkflowID: oldInst.WorkflowID,
		Status:     "running",
		TaskStates: make(map[string]*storage.TaskStatus),
		Logs:       []string{fmt.Sprintf("Workflow replay of %s initialized.", req.InstanceID)},
		StartedAt:  time.Now(),
	}

	for _, task := range def.Tasks {
		newInst.TaskStates[task.Name] = &storage.TaskStatus{
			Status: "pending",
		}
	}

	mu.Lock()
	instances[newID] = newInst
	mu.Unlock()

	go engine.RunWorkflow(newInst, def, workflowStore, instances, &mu)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newInst)
}

func handleValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var def storage.WorkflowDef
	if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if engine.HasCycle(def) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"valid":false,"error":"Cyclic dependency detected"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"valid":true}`))
}

func handleVisualize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var def storage.WorkflowDef
	if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	var sb strings.Builder
	sb.WriteString("graph TD\n")
	for _, task := range def.Tasks {
		if len(task.DependsOn) == 0 {
			sb.WriteString(fmt.Sprintf("    %s\n", task.Name))
		} else {
			for _, dep := range task.DependsOn {
				sb.WriteString(fmt.Sprintf("    %s --> %s\n", dep, task.Name))
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"mermaid": sb.String(),
	})
}

func handleCompensateComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		InstanceID string `json:"instance_id"`
		TaskName   string `json:"task_name"`
		Status     string `json:"status"` // success or failed
		Error      string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	mu.Lock()
	inst, exists := instances[req.InstanceID]
	mu.Unlock()

	if !exists {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	inst.Mu.Lock()
	tState, taskExists := inst.TaskStates[req.TaskName]
	if !taskExists || tState.Status != "compensating" {
		inst.Mu.Unlock()
		http.Error(w, "Task is not in compensating state", http.StatusBadRequest)
		return
	}

	if req.Status == "success" {
		tState.Status = "compensated"
		inst.Logs = append(inst.Logs, fmt.Sprintf("[SAGA] Compensation succeeded asynchronously for task %s", req.TaskName))
	} else {
		tState.Status = "failed"
		tState.Error = req.Error
		inst.Logs = append(inst.Logs, fmt.Sprintf("[SAGA] Compensation failed asynchronously for task %s: %s", req.TaskName, req.Error))
	}
	inst.Mu.Unlock()
	engine.SaveCheckpoint(inst, workflowStore, instances, &mu)

	mu.RLock()
	def := definitions[inst.WorkflowID]
	mu.RUnlock()

	go engine.RollbackSaga(inst, def, workflowStore, instances, &mu)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
