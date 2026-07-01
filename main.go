package main

import (
	"context"
	"encoding/json"
	"errors"
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

	"github.com/go-stomp/stomp/v3"

	"github.com/vyuvaraj/ServShared"
)

type Task struct {
	Name             string   `json:"name"`
	DependsOn        []string `json:"depends_on"`
	Action           string   `json:"action"` // e.g. "http://..." or "mock-success"
	CompensateAction string   `json:"compensate_action,omitempty"`
	RetryCount       int      `json:"retry_count,omitempty"`
	TimeoutMs        int      `json:"timeout_ms,omitempty"`
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

var workflowStore WorkflowStore

func initStore() {
	client := ServShared.NewStoreClient()
	workflowStore = NewServStoreWorkflowStore(client)
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
	copied := make(map[string]WorkflowDef)
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
	copied := make(map[string]*WorkflowInstance)
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
		go rollbackSaga(instPointer, def)
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
		go runWorkflow(instPointer, def)
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

	inst.mu.Lock()
	state, taskExists := inst.TaskStates[req.TaskName]
	if !taskExists || state.Status != "pending_approval" {
		inst.mu.Unlock()
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
	inst.mu.Unlock()
	saveCheckpoint(inst)

	mu.RLock()
	def := definitions[inst.WorkflowID]
	mu.RUnlock()

	if req.Decision == "approve" {
		go runWorkflow(inst, def)
	} else {
		inst.mu.Lock()
		inst.FinishedAt = time.Now()
		for i := len(def.Tasks) - 1; i >= 0; i-- {
			t := def.Tasks[i]
			tState := inst.TaskStates[t.Name]
			if tState.Status == "completed" && t.CompensateAction != "" {
				inst.Logs = append(inst.Logs, fmt.Sprintf("[SAGA] Executing compensation rollback for task %s: %s", t.Name, t.CompensateAction))
				tState.Status = "compensated"
			}
		}
		inst.mu.Unlock()
		saveCheckpoint(inst)
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

	inst.mu.RLock()
	defer inst.mu.RUnlock()

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

	var history []*WorkflowInstance
	for _, inst := range instances {
		inst.mu.RLock()
		if inst.Status == "completed" || inst.Status == "failed" {
			history = append(history, inst)
		}
		inst.mu.RUnlock()
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
	newInst := &WorkflowInstance{
		ID:         newID,
		WorkflowID: oldInst.WorkflowID,
		Status:     "running",
		TaskStates: make(map[string]*TaskStatus),
		Logs:       []string{fmt.Sprintf("Workflow replay of %s initialized.", req.InstanceID)},
		StartedAt:  time.Now(),
	}

	for _, task := range def.Tasks {
		newInst.TaskStates[task.Name] = &TaskStatus{
			Status: "pending",
		}
	}

	mu.Lock()
	instances[newID] = newInst
	mu.Unlock()

	go runWorkflow(newInst, def)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newInst)
}

func hasCycle(def WorkflowDef) bool {
	adj := make(map[string][]string)
	for _, task := range def.Tasks {
		for _, dep := range task.DependsOn {
			adj[dep] = append(adj[dep], task.Name)
		}
	}

	visited := make(map[string]int)
	var dfs func(node string) bool
	dfs = func(node string) bool {
		visited[node] = 1
		for _, neighbor := range adj[node] {
			if visited[neighbor] == 1 {
				return true
			}
			if visited[neighbor] == 0 {
				if dfs(neighbor) {
					return true
				}
			}
		}
		visited[node] = 2
		return false
	}

	for _, task := range def.Tasks {
		if visited[task.Name] == 0 {
			if dfs(task.Name) {
				return true
			}
		}
	}
	return false
}

func handleValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var def WorkflowDef
	if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	if hasCycle(def) {
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

	var def WorkflowDef
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
			if task.Action == "approval" {
				state.Status = "pending_approval"
				inst.Status = "paused"
				inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s paused pending manual approval.", task.Name))
				inst.mu.Unlock()
				saveCheckpoint(inst)
				return
			}

			state.Status = "running"
			state.StartedAt = time.Now()
			inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s started.", task.Name))
			inst.mu.Unlock()
			saveCheckpoint(inst)

			// Run action logic with retry and timeout simulation
			var err error
			attempts := 1
			maxAttempts := 1
			if task.RetryCount > 0 {
				maxAttempts = task.RetryCount + 1
			}

			for {
				if task.TimeoutMs > 0 {
					// Execute with timeout constraint
					errChan := make(chan error, 1)
					go func() {
						errChan <- executeTaskAction(task)
					}()
					select {
					case err = <-errChan:
						// completed before timeout
					case <-time.After(time.Duration(task.TimeoutMs) * time.Millisecond):
						err = fmt.Errorf("task timed out after %dms", task.TimeoutMs)
					}
				} else {
					err = executeTaskAction(task)
				}

				if err == nil {
					break
				}

				if attempts >= maxAttempts {
					break
				}

				inst.mu.Lock()
				inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s failed attempt %d: %v. Retrying...", task.Name, attempts, err))
				inst.mu.Unlock()
				attempts++
				time.Sleep(10 * time.Millisecond) // initial backoff sleep
			}

			inst.mu.Lock()
			state.FinishedAt = time.Now()
			if err != nil {
				state.Status = "failed"
				state.Error = err.Error()
				inst.Status = "failed"
				inst.FinishedAt = time.Now()
				inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s failed: %v. Initiating Saga compensation...", task.Name, err))
				inst.mu.Unlock()
				saveCheckpoint(inst)

				// Trigger durable saga rollback/compensations
				go rollbackSaga(inst, def)
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
	data, err := json.Marshal(inst)
	inst.mu.RUnlock()
	if err != nil {
		return
	}

	if workflowStore != nil && workflowStore.GetClient() != nil {
		_ = workflowStore.GetClient().Put("serv-flow-state", fmt.Sprintf("%s.state", inst.ID), data)
	}

	_ = os.WriteFile(fmt.Sprintf("%s.state", inst.ID), data, 0644)

	mu.Lock()
	instances[inst.ID] = inst
	mu.Unlock()
	saveInstancesToStore()
}

func executeTaskAction(t Task) error {
	if strings.HasPrefix(t.Action, "http://") || strings.HasPrefix(t.Action, "https://") {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", t.Action, strings.NewReader(`{}`))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("HTTP action failed with status %d", resp.StatusCode)
		}
		return nil
	}

	// Simple simulation
	if t.Action == "sleep-100" {
		time.Sleep(100 * time.Millisecond)
	} else {
		time.Sleep(10 * time.Millisecond)
	}

	if t.Action == "fail" {
		return errors.New("simulated action failure")
	}
	return nil
}

func publishStompMessage(addr string, topic string, body []byte) error {
	conn, err := stomp.Dial("tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	return conn.Send(topic, "text/plain", body, nil)
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

	inst.mu.Lock()
	tState, taskExists := inst.TaskStates[req.TaskName]
	if !taskExists || tState.Status != "compensating" {
		inst.mu.Unlock()
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
	inst.mu.Unlock()
	saveCheckpoint(inst)

	mu.RLock()
	def := definitions[inst.WorkflowID]
	mu.RUnlock()

	go rollbackSaga(inst, def)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func rollbackSaga(inst *WorkflowInstance, def WorkflowDef) {
	// Traverse tasks in reverse order
	for i := len(def.Tasks) - 1; i >= 0; i-- {
		t := def.Tasks[i]
		
		inst.mu.Lock()
		tState := inst.TaskStates[t.Name]
		
		if tState.Status == "compensating" {
			inst.mu.Unlock()
			return
		}
		
		if tState.Status == "completed" && t.CompensateAction != "" {
			tState.Status = "compensating"
			inst.Logs = append(inst.Logs, fmt.Sprintf("[SAGA] Executing compensation rollback for task %s: %s", t.Name, t.CompensateAction))
			inst.mu.Unlock()
			saveCheckpoint(inst)
			
			if strings.HasPrefix(t.CompensateAction, "event://") {
				topic := "/topic/" + strings.TrimPrefix(t.CompensateAction, "event://")
				brokerAddr := os.Getenv("SERVQUEUE_ADDR")
				if brokerAddr == "" {
					brokerAddr = "localhost:8082"
				}
				payload := fmt.Sprintf(`{"instance_id": "%s", "task_name": "%s"}`, inst.ID, t.Name)
				
				err := publishStompMessage(brokerAddr, topic, []byte(payload))
				if err != nil {
					inst.mu.Lock()
					tState.Status = "failed"
					tState.Error = "STOMP publish failed: " + err.Error()
					inst.Logs = append(inst.Logs, fmt.Sprintf("[SAGA] Compensation failed to publish for task %s: %v", t.Name, err))
					inst.mu.Unlock()
					saveCheckpoint(inst)
				}
				return
			} else {
				// Execute compensation action
				err := executeTaskAction(Task{Name: t.Name, Action: t.CompensateAction})
				
				inst.mu.Lock()
				if err != nil {
					tState.Status = "failed"
					tState.Error = "compensation failed: " + err.Error()
					inst.Logs = append(inst.Logs, fmt.Sprintf("[SAGA] Compensation failed for task %s: %v", t.Name, err))
				} else {
					tState.Status = "compensated"
					inst.Logs = append(inst.Logs, fmt.Sprintf("[SAGA] Compensation succeeded for task %s", t.Name))
				}
				inst.mu.Unlock()
				saveCheckpoint(inst)
			}
		} else {
			inst.mu.Unlock()
		}
	}
}
