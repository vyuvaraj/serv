package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"servflow/pkg/engine"
	"servflow/pkg/storage"
)

type HandlerContext struct {
	Definitions   map[string]storage.WorkflowDef
	Instances     map[string]*storage.WorkflowInstance
	Mu            *sync.RWMutex
	WorkflowStore storage.WorkflowStore
}

func NewHandlerContext(store storage.WorkflowStore) *HandlerContext {
	return &HandlerContext{
		Definitions:   make(map[string]storage.WorkflowDef),
		Instances:     make(map[string]*storage.WorkflowInstance),
		Mu:            &sync.RWMutex{},
		WorkflowStore: store,
	}
}

func (ctx *HandlerContext) LoadState() {
	if defs, err := ctx.WorkflowStore.LoadDefinitions(); err == nil {
		ctx.Mu.Lock()
		ctx.Definitions = defs
		ctx.Mu.Unlock()
	}
	if insts, err := ctx.WorkflowStore.LoadInstances(); err == nil {
		ctx.Mu.Lock()
		ctx.Instances = insts
		ctx.Mu.Unlock()
	}
}

func (ctx *HandlerContext) SaveDefinitionsToStore() {
	if ctx.WorkflowStore == nil {
		return
	}
	ctx.Mu.RLock()
	copied := make(map[string]storage.WorkflowDef)
	for k, v := range ctx.Definitions {
		copied[k] = v
	}
	ctx.Mu.RUnlock()
	_ = ctx.WorkflowStore.SaveDefinitions(copied)
}

func (ctx *HandlerContext) SaveInstancesToStore() {
	if ctx.WorkflowStore == nil {
		return
	}
	ctx.Mu.RLock()
	copied := make(map[string]*storage.WorkflowInstance)
	for k, v := range ctx.Instances {
		copied[k] = v
	}
	ctx.Mu.RUnlock()
	_ = ctx.WorkflowStore.SaveInstances(copied)
}

func (ctx *HandlerContext) HandleDefine(w http.ResponseWriter, r *http.Request) {
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

	ctx.Mu.Lock()
	ctx.Definitions[def.ID] = def
	ctx.Mu.Unlock()
	ctx.SaveDefinitionsToStore()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(def)
}

func (ctx *HandlerContext) HandleExecute(w http.ResponseWriter, r *http.Request) {
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

	ctx.Mu.RLock()
	def, exists := ctx.Definitions[req.WorkflowID]
	ctx.Mu.RUnlock()

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

	ctx.Mu.Lock()
	ctx.Instances[instanceID] = inst
	ctx.Mu.Unlock()

	go engine.RunWorkflow(inst, def, ctx.WorkflowStore, ctx.Instances, ctx.Mu)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(inst)
}

func (ctx *HandlerContext) HandleResume(w http.ResponseWriter, r *http.Request) {
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
	if ctx.WorkflowStore != nil && ctx.WorkflowStore.GetClient() != nil {
		data, err = ctx.WorkflowStore.GetClient().Get("serv-flow-state", stateFile)
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

	ctx.Mu.RLock()
	def, defExists := ctx.Definitions[inst.WorkflowID]
	ctx.Mu.RUnlock()

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

	ctx.Mu.Lock()
	ctx.Instances[inst.ID] = instPointer
	ctx.Mu.Unlock()

	if isCompensating {
		instPointer.Logs = append(instPointer.Logs, "Resuming saga rollback execution from checkpoint.")
		instPointer.Status = "failed"
		go engine.RollbackSaga(instPointer, def, ctx.WorkflowStore, ctx.Instances, ctx.Mu)
	} else {
		instPointer.Status = "running"
		instPointer.Logs = append(instPointer.Logs, "Workflow execution resumed from checkpoint.")

		for _, state := range instPointer.TaskStates {
			if state.Status == "failed" {
				state.Status = "pending"
				state.Error = ""
			}
		}
		go engine.RunWorkflow(instPointer, def, ctx.WorkflowStore, ctx.Instances, ctx.Mu)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(instPointer)
}

func (ctx *HandlerContext) HandleApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		InstanceID string `json:"instance_id"`
		TaskName   string `json:"task_name"`
		Decision   string `json:"decision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	ctx.Mu.Lock()
	inst, exists := ctx.Instances[req.InstanceID]
	ctx.Mu.Unlock()

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
	engine.SaveCheckpoint(inst, ctx.WorkflowStore, ctx.Instances, ctx.Mu)

	ctx.Mu.RLock()
	def := ctx.Definitions[inst.WorkflowID]
	ctx.Mu.RUnlock()

	if req.Decision == "approve" {
		go engine.RunWorkflow(inst, def, ctx.WorkflowStore, ctx.Instances, ctx.Mu)
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
		engine.SaveCheckpoint(inst, ctx.WorkflowStore, ctx.Instances, ctx.Mu)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(inst)
}

func (ctx *HandlerContext) HandleGetInstance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Path
	var id string
	fmt.Sscanf(path, "/api/workflows/instances/%s", &id)
	if id == "" {
		http.Error(w, "Instance ID required", http.StatusBadRequest)
		return
	}

	ctx.Mu.RLock()
	inst, exists := ctx.Instances[id]
	ctx.Mu.RUnlock()

	if !exists {
		http.Error(w, "Instance not found", http.StatusNotFound)
		return
	}

	inst.Mu.RLock()
	defer inst.Mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(inst)
}

func (ctx *HandlerContext) HandleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx.Mu.RLock()
	defer ctx.Mu.RUnlock()

	var history []*storage.WorkflowInstance
	for _, inst := range ctx.Instances {
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

func (ctx *HandlerContext) HandleReplay(w http.ResponseWriter, r *http.Request) {
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

	ctx.Mu.Lock()
	oldInst, exists := ctx.Instances[req.InstanceID]
	ctx.Mu.Unlock()

	if !exists {
		http.Error(w, "Original instance not found", http.StatusNotFound)
		return
	}

	ctx.Mu.RLock()
	def, defExists := ctx.Definitions[oldInst.WorkflowID]
	ctx.Mu.RUnlock()

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

	ctx.Mu.Lock()
	ctx.Instances[newID] = newInst
	ctx.Mu.Unlock()

	go engine.RunWorkflow(newInst, def, ctx.WorkflowStore, ctx.Instances, ctx.Mu)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newInst)
}

func (ctx *HandlerContext) HandleValidate(w http.ResponseWriter, r *http.Request) {
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

func (ctx *HandlerContext) HandleVisualize(w http.ResponseWriter, r *http.Request) {
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

func (ctx *HandlerContext) HandleCompensateComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		InstanceID string `json:"instance_id"`
		TaskName   string `json:"task_name"`
		Status     string `json:"status"`
		Error      string `json:"error,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	ctx.Mu.Lock()
	inst, exists := ctx.Instances[req.InstanceID]
	ctx.Mu.Unlock()

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
	engine.SaveCheckpoint(inst, ctx.WorkflowStore, ctx.Instances, ctx.Mu)

	ctx.Mu.RLock()
	def := ctx.Definitions[inst.WorkflowID]
	ctx.Mu.RUnlock()

	go engine.RollbackSaga(inst, def, ctx.WorkflowStore, ctx.Instances, ctx.Mu)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
