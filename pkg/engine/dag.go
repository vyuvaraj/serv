package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-stomp/stomp/v3"
	"github.com/vyuvaraj/ServShared"
	"servflow/pkg/storage"
)

func HasCycle(def storage.WorkflowDef) bool {
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

// recordReplaySnapshot captures the current task state as a time-travel replay step.
func recordReplaySnapshot(inst *storage.WorkflowInstance, taskName, status, msg string) {
	// Deep copy task states for snapshot
	snap := make(map[string]*storage.TaskStatus, len(inst.TaskStates))
	for k, v := range inst.TaskStates {
		copy := *v
		snap[k] = &copy
	}
	inst.ReplayLog = append(inst.ReplayLog, storage.ReplaySnapshot{
		StepIndex:  len(inst.ReplayLog),
		TaskName:   taskName,
		Status:     status,
		Timestamp:  time.Now(),
		TaskStates: snap,
		Message:    msg,
	})
}

func RunWorkflow(
	inst *storage.WorkflowInstance,
	def storage.WorkflowDef,
	store storage.WorkflowStore,
	instances map[string]*storage.WorkflowInstance,
	instancesMu *sync.RWMutex,
) {
	workflowSpan := ServShared.StartSpan(fmt.Sprintf("servflow:WORKFLOW %s", inst.WorkflowID), inst.Traceparent)
	var workflowErr error
	defer func() {
		if workflowSpan != nil {
			ServShared.EndSpan(workflowSpan, workflowErr, map[string]interface{}{
				"workflow.id":       inst.WorkflowID,
				"workflow.instance": inst.ID,
				"workflow.status":   inst.Status,
			})
		}
	}()

	inst.Mu.Lock()
	for _, state := range inst.TaskStates {
		if state.Status == "running" {
			state.Status = "pending"
			inst.Logs = append(inst.Logs, "Resetting running task to pending on startup/resume.")
		}
	}
	inst.Mu.Unlock()

	completedCount := 0
	inst.Mu.RLock()
	for _, state := range inst.TaskStates {
		if state.Status == "completed" || state.Status == "skipped" {
			completedCount++
		}
	}
	inst.Mu.RUnlock()

	totalTasks := len(def.Tasks)

	for completedCount < totalTasks {
		inst.Mu.Lock()
		if inst.Status == "failed" {
			inst.Mu.Unlock()
			workflowErr = fmt.Errorf("workflow execution failed")
			return
		}
		inst.Mu.Unlock()

		progressMade := false

		for _, task := range def.Tasks {
			inst.Mu.Lock()
			state := inst.TaskStates[task.Name]
			if state.Status != "pending" {
				inst.Mu.Unlock()
				continue
			}

			// Check if dependencies are satisfied
			depsSatisfied := true
			shouldSkip := false
			for _, dep := range task.DependsOn {
				depName := dep
				branchName := ""
				if idx := strings.Index(dep, ":"); idx > -1 {
					depName = dep[:idx]
					branchName = dep[idx+1:]
				}

				depState, exists := inst.TaskStates[depName]
				if !exists {
					depsSatisfied = false
					break
				}
				if depState.Status == "skipped" {
					shouldSkip = true
					break
				}
				if depState.Status != "completed" {
					depsSatisfied = false
					break
				}
				if branchName != "" && depState.Result != branchName {
					shouldSkip = true
					break
				}
			}

			if shouldSkip {
				state.Status = "skipped"
				inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s skipped due to branch condition.", task.Name))
				recordReplaySnapshot(inst, task.Name, "skipped", fmt.Sprintf("Task %s skipped due to branch condition", task.Name))
				completedCount++
				progressMade = true
				inst.Mu.Unlock()
				SaveCheckpoint(inst, store, instances, instancesMu)
				continue
			}

			if !depsSatisfied {
				inst.Mu.Unlock()
				continue
			}

			// Start executing task
			if task.Action == "approval" {
				state.Status = "pending_approval"
				inst.Status = "paused"
				inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s paused pending manual approval.", task.Name))
				inst.Mu.Unlock()
				SaveCheckpoint(inst, store, instances, instancesMu)

				if task.TimeoutMs > 0 {
					go func(tName string, timeoutMs int) {
						time.Sleep(time.Duration(timeoutMs) * time.Millisecond)
						inst.Mu.Lock()
						tState := inst.TaskStates[tName]
						if tState.Status == "pending_approval" && inst.Status == "paused" {
							tState.Status = "failed"
							tState.Error = fmt.Sprintf("approval timed out after %dms", timeoutMs)
							inst.Status = "failed"
							inst.FinishedAt = time.Now()
							inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s approval timed out. Initiating Saga compensation...", tName))
							inst.Mu.Unlock()
							SaveCheckpoint(inst, store, instances, instancesMu)
							TriggerRollbackSaga(inst, def, store, instances, instancesMu)
							return
						}
						inst.Mu.Unlock()
					}(task.Name, task.TimeoutMs)
				}
				return
			}

			state.Status = "running"
			state.StartedAt = time.Now()
			inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s started.", task.Name))
			recordReplaySnapshot(inst, task.Name, "started", fmt.Sprintf("Task %s started execution", task.Name))
			inst.Mu.Unlock()
			SaveCheckpoint(inst, store, instances, instancesMu)

			if !acquireTaskLock(inst.ID, task.Name) {
				inst.Mu.Lock()
				inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s is being executed by another cluster node. Gated duplicate run.", task.Name))
				inst.Mu.Unlock()
				continue
			}

			// Start task tracing span
			var taskTraceparent string
			if workflowSpan != nil {
				taskTraceparent = fmt.Sprintf("00-%s-%s-01", workflowSpan.TraceID, workflowSpan.SpanID)
			} else {
				taskTraceparent = inst.Traceparent
			}
			taskSpan := ServShared.StartSpan(fmt.Sprintf("servflow:TASK %s", task.Name), taskTraceparent)

			// Run action logic with retry and timeout simulation
			var err error
			var classifyResult string
			attempts := 1
			maxAttempts := 1
			if task.RetryCount > 0 {
				maxAttempts = task.RetryCount + 1
			}

			for {
				if task.TimeoutMs > 0 {
					// Execute with timeout constraint
					errChan := make(chan error, 1)
					resChan := make(chan string, 1)
					go func() {
						if strings.HasPrefix(task.Action, "ai.classify(") {
							res, cErr := RunAIClassify(task.Action)
							resChan <- res
							errChan <- cErr
						} else {
							errChan <- ExecuteTaskAction(task)
						}
					}()
					select {
					case err = <-errChan:
						if err == nil && strings.HasPrefix(task.Action, "ai.classify(") {
							classifyResult = <-resChan
						}
					case <-time.After(time.Duration(task.TimeoutMs) * time.Millisecond):
						err = fmt.Errorf("task timed out after %dms", task.TimeoutMs)
					}
				} else {
					if strings.HasPrefix(task.Action, "ai.classify(") {
						classifyResult, err = RunAIClassify(task.Action)
					} else {
						err = ExecuteTaskAction(task)
					}
				}

				if err == nil {
					break
				}

				if attempts >= maxAttempts {
					break
				}

				inst.Mu.Lock()
				inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s failed attempt %d: %v. Retrying...", task.Name, attempts, err))
				inst.Mu.Unlock()
				attempts++
				time.Sleep(10 * time.Millisecond) // initial backoff sleep
			}

			if taskSpan != nil {
				ServShared.EndSpan(taskSpan, err, map[string]interface{}{
					"task.name":     task.Name,
					"task.attempts": attempts,
				})
			}

			inst.Mu.Lock()
			state.FinishedAt = time.Now()
			if err != nil {
				state.Status = "failed"
				state.Error = err.Error()
				inst.Status = "failed"
				inst.FinishedAt = time.Now()
				inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s failed: %v. Initiating Saga compensation...", task.Name, err))
				recordReplaySnapshot(inst, task.Name, "failed", fmt.Sprintf("Task %s failed: %v", task.Name, err))
				inst.Mu.Unlock()
				SaveCheckpoint(inst, store, instances, instancesMu)

				// Trigger durable saga rollback/compensations
				workflowErr = err
				releaseTaskLock(inst.ID, task.Name)
				go TriggerRollbackSaga(inst, def, store, instances, instancesMu)
				return
			} else {
				state.Status = "completed"
				state.Result = classifyResult
				inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s completed.", task.Name))
				if classifyResult != "" {
					inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s AI classification result: %s", task.Name, classifyResult))
				}
				recordReplaySnapshot(inst, task.Name, "completed", fmt.Sprintf("Task %s completed successfully", task.Name))
				completedCount++
				progressMade = true
			}
			inst.Mu.Unlock()
			releaseTaskLock(inst.ID, task.Name)
			SaveCheckpoint(inst, store, instances, instancesMu)
		}

		if !progressMade && completedCount < totalTasks {
			// Cyclic dependency detected!
			inst.Mu.Lock()
			inst.Status = "failed"
			inst.FinishedAt = time.Now()
			inst.Logs = append(inst.Logs, "Cycle detected in DAG definition. Workflow aborted.")
			inst.Mu.Unlock()
			SaveCheckpoint(inst, store, instances, instancesMu)
			workflowErr = fmt.Errorf("cycle detected in DAG definition")
			return
		}
	}

	inst.Mu.Lock()
	inst.Status = "completed"
	inst.FinishedAt = time.Now()
	inst.Logs = append(inst.Logs, "Workflow completed successfully.")
	inst.Mu.Unlock()
	SaveCheckpoint(inst, store, instances, instancesMu)
}

// Enterprise hooks for saga checkpoints (overridden in EE build)
var (
	EnterpriseSaveCheckpoint = func(inst *storage.WorkflowInstance, data []byte, store storage.WorkflowStore) bool {
		return false
	}
)

func SaveCheckpoint(
	inst *storage.WorkflowInstance,
	store storage.WorkflowStore,
	instances map[string]*storage.WorkflowInstance,
	instancesMu *sync.RWMutex,
) {
	inst.Mu.RLock()
	data, err := json.Marshal(inst)
	inst.Mu.RUnlock()
	if err != nil {
		return
	}

	if handled := EnterpriseSaveCheckpoint(inst, data, store); !handled {
		if store != nil && store.GetClient() != nil {
			_ = store.GetClient().Put("serv-flow-state", fmt.Sprintf("%s.state", inst.ID), data)
		}
		_ = os.WriteFile(fmt.Sprintf("%s.state", inst.ID), data, 0644)
	}

	instancesMu.Lock()
	instances[inst.ID] = inst
	instancesMu.Unlock()
	if store != nil {
		instancesMu.RLock()
		copied := make(map[string]*storage.WorkflowInstance)
		for k, v := range instances {
			copied[k] = v
		}
		instancesMu.RUnlock()
		_ = store.SaveInstances(copied)
	}
}

func ExecuteTaskAction(t storage.Task) error {
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

func PublishStompMessage(addr string, topic string, body []byte) error {
	conn, err := stomp.Dial("tcp", addr)
	if err != nil {
		return err
	}
	defer conn.Disconnect()

	return conn.Send(topic, "text/plain", body, nil)
}

// SagaCoordinator defines pluggable coordinator hooks for executing workflow rollback compensations.
type SagaCoordinator interface {
	Rollback(
		inst *storage.WorkflowInstance,
		def storage.WorkflowDef,
		store storage.WorkflowStore,
		instances map[string]*storage.WorkflowInstance,
		instancesMu *sync.RWMutex,
	)
}

// ActiveSagaCoordinator is the globally registered saga rollback coordinator.
var ActiveSagaCoordinator SagaCoordinator

// TriggerRollbackSaga dispatches Saga rollback logic to the ActiveSagaCoordinator if registered.
func TriggerRollbackSaga(
	inst *storage.WorkflowInstance,
	def storage.WorkflowDef,
	store storage.WorkflowStore,
	instances map[string]*storage.WorkflowInstance,
	instancesMu *sync.RWMutex,
) {
	if ActiveSagaCoordinator != nil {
		ActiveSagaCoordinator.Rollback(inst, def, store, instances, instancesMu)
	} else {
		RollbackSaga(inst, def, store, instances, instancesMu)
	}
}

func RollbackSaga(
	inst *storage.WorkflowInstance,
	def storage.WorkflowDef,
	store storage.WorkflowStore,
	instances map[string]*storage.WorkflowInstance,
	instancesMu *sync.RWMutex,
) {
	// 1. Gather all tasks that completed
	type completedTask struct {
		Task       storage.Task
		FinishedAt time.Time
	}
	var completed []completedTask

	inst.Mu.RLock()
	for _, t := range def.Tasks {
		tState, exists := inst.TaskStates[t.Name]
		if exists && tState.Status == "completed" && t.CompensateAction != "" {
			completed = append(completed, completedTask{
				Task:       t,
				FinishedAt: tState.FinishedAt,
			})
		}
	}
	inst.Mu.RUnlock()

	// 2. Sort completed tasks by FinishedAt in descending order (reverse completion order)
	for i := 0; i < len(completed); i++ {
		for j := i + 1; j < len(completed); j++ {
			if completed[i].FinishedAt.Before(completed[j].FinishedAt) {
				completed[i], completed[j] = completed[j], completed[i]
			}
		}
	}

	// 3. Compensate them in that exact reverse completion order
	for _, ct := range completed {
		t := ct.Task
		inst.Mu.Lock()
		tState := inst.TaskStates[t.Name]

		if tState.Status == "compensating" {
			inst.Mu.Unlock()
			return
		}

		tState.Status = "compensating"
		inst.Logs = append(inst.Logs, fmt.Sprintf("[SAGA] Executing compensation rollback for task %s: %s", t.Name, t.CompensateAction))
		inst.Mu.Unlock()
		SaveCheckpoint(inst, store, instances, instancesMu)

		if strings.HasPrefix(t.CompensateAction, "event://") {
			topic := "/topic/" + strings.TrimPrefix(t.CompensateAction, "event://")
			brokerAddr := os.Getenv("SERVQUEUE_ADDR")
			if brokerAddr == "" {
				brokerAddr = "localhost:8082"
			}
			payload := fmt.Sprintf(`{"instance_id": "%s", "task_name": "%s"}`, inst.ID, t.Name)

			err := PublishStompMessage(brokerAddr, topic, []byte(payload))
			if err != nil {
				inst.Mu.Lock()
				tState.Status = "failed"
				tState.Error = "STOMP publish failed: " + err.Error()
				inst.Logs = append(inst.Logs, fmt.Sprintf("[SAGA] Compensation failed to publish for task %s: %v", t.Name, err))
				inst.Mu.Unlock()
				SaveCheckpoint(inst, store, instances, instancesMu)
			}
			return
		} else {
			err := ExecuteTaskAction(storage.Task{Name: t.Name, Action: t.CompensateAction})

			inst.Mu.Lock()
			if err != nil {
				tState.Status = "failed"
				tState.Error = "compensation failed: " + err.Error()
				inst.Logs = append(inst.Logs, fmt.Sprintf("[SAGA] Compensation failed for task %s: %v", t.Name, err))
			} else {
				tState.Status = "compensated"
				inst.Logs = append(inst.Logs, fmt.Sprintf("[SAGA] Compensation succeeded for task %s", t.Name))
			}
			inst.Mu.Unlock()
			SaveCheckpoint(inst, store, instances, instancesMu)
		}
	}
}

func RunAIClassify(action string) (string, error) {
	if !strings.HasPrefix(action, "ai.classify(") {
		return "", fmt.Errorf("invalid AI action format")
	}
	content := strings.TrimPrefix(action, "ai.classify(")
	content = strings.TrimSuffix(content, ")")

	firstQuoteIdx := strings.Index(content, "\"")
	if firstQuoteIdx == -1 {
		return "", fmt.Errorf("invalid AI action: missing input quote")
	}
	secondQuoteIdx := strings.Index(content[firstQuoteIdx+1:], "\"")
	if secondQuoteIdx == -1 {
		return "", fmt.Errorf("invalid AI action: mismatched input quote")
	}
	secondQuoteIdx += firstQuoteIdx + 1

	inputText := content[firstQuoteIdx+1 : secondQuoteIdx]

	optionsStart := strings.Index(content, "[")
	optionsEnd := strings.Index(content, "]")
	if optionsStart == -1 || optionsEnd == -1 {
		return "", fmt.Errorf("invalid AI action: missing options array")
	}
	optionsStr := content[optionsStart+1 : optionsEnd]

	var options []string
	for _, opt := range strings.Split(optionsStr, ",") {
		opt = strings.TrimSpace(opt)
		opt = strings.Trim(opt, "\"'")
		if opt != "" {
			options = append(options, opt)
		}
	}
	if len(options) == 0 {
		return "", fmt.Errorf("invalid AI action: no options provided")
	}

	inputLower := strings.ToLower(inputText)
	if strings.Contains(inputLower, "scam") || strings.Contains(inputLower, "suspicious") || strings.Contains(inputLower, "fraud") {
		for _, opt := range options {
			if opt == "reject" || opt == "review" {
				return opt, nil
			}
		}
	}
	if strings.Contains(inputLower, "good") || strings.Contains(inputLower, "vip") || strings.Contains(inputLower, "valid") {
		for _, opt := range options {
			if opt == "approve" {
				return opt, nil
			}
		}
	}

	return options[0], nil
}

func acquireTaskLock(instanceID, taskName string) bool {
	url := os.Getenv("SERV_LOCK_URL")
	if url == "" {
		return true // Standalone / disabled
	}
	host, _ := os.Hostname()
	owner := fmt.Sprintf("%s/%d", host, os.Getpid())
	lockKey := fmt.Sprintf("servflow:instance:%s:task:%s", instanceID, taskName)

	payload := map[string]interface{}{
		"key":         lockKey,
		"owner":       owner,
		"client_id":   owner,
		"duration_ms": 30000, // 30 seconds
		"wait_ms":     0,     // Do not block/wait
		"mode":        "exclusive",
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(url+"/api/locks/acquire", "application/json", bytes.NewReader(body))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func releaseTaskLock(instanceID, taskName string) {
	url := os.Getenv("SERV_LOCK_URL")
	if url == "" {
		return
	}
	host, _ := os.Hostname()
	owner := fmt.Sprintf("%s/%d", host, os.Getpid())
	lockKey := fmt.Sprintf("servflow:instance:%s:task:%s", instanceID, taskName)

	payload := map[string]string{
		"key":   lockKey,
		"owner": owner,
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(url+"/api/locks/release", "application/json", bytes.NewReader(body))
	if err == nil {
		resp.Body.Close()
	}
}
