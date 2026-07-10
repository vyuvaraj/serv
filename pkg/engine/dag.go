package engine

import (
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

	completedCount := 0
	inst.Mu.RLock()
	for _, state := range inst.TaskStates {
		if state.Status == "completed" {
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
			for _, dep := range task.DependsOn {
				depState, exists := inst.TaskStates[dep]
				if !exists || depState.Status != "completed" {
					depsSatisfied = false
					break
				}
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
				return
			}

			state.Status = "running"
			state.StartedAt = time.Now()
			inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s started.", task.Name))
			recordReplaySnapshot(inst, task.Name, "started", fmt.Sprintf("Task %s started execution", task.Name))
			inst.Mu.Unlock()
			SaveCheckpoint(inst, store, instances, instancesMu)

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
						errChan <- ExecuteTaskAction(task)
					}()
					select {
					case err = <-errChan:
						// completed before timeout
					case <-time.After(time.Duration(task.TimeoutMs) * time.Millisecond):
						err = fmt.Errorf("task timed out after %dms", task.TimeoutMs)
					}
				} else {
					err = ExecuteTaskAction(task)
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
				go TriggerRollbackSaga(inst, def, store, instances, instancesMu)
				return
			} else {
				state.Status = "completed"
				inst.Logs = append(inst.Logs, fmt.Sprintf("Task %s completed.", task.Name))
				recordReplaySnapshot(inst, task.Name, "completed", fmt.Sprintf("Task %s completed successfully", task.Name))
				completedCount++
				progressMade = true
			}
			inst.Mu.Unlock()
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
	for i := len(def.Tasks) - 1; i >= 0; i-- {
		t := def.Tasks[i]

		inst.Mu.Lock()
		tState := inst.TaskStates[t.Name]

		if tState.Status == "compensating" {
			inst.Mu.Unlock()
			return
		}

		if tState.Status == "completed" && t.CompensateAction != "" {
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
		} else {
			inst.Mu.Unlock()
		}
	}
}
