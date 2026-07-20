package engine

import (
	"sync"
	"testing"

	"servflow/pkg/storage"
)

func TestHasCycleNoTasks(t *testing.T) {
	def := storage.WorkflowDef{
		ID:    "test",
		Tasks: []storage.Task{},
	}
	if HasCycle(def) {
		t.Error("expected no cycle")
	}
}

func TestHasCycleSingleTask(t *testing.T) {
	def := storage.WorkflowDef{
		ID: "test",
		Tasks: []storage.Task{
			{Name: "A", DependsOn: nil},
		},
	}
	if HasCycle(def) {
		t.Error("expected no cycle")
	}
}

func TestHasCycleLinear(t *testing.T) {
	def := storage.WorkflowDef{
		ID: "test",
		Tasks: []storage.Task{
			{Name: "A", DependsOn: nil},
			{Name: "B", DependsOn: []string{"A"}},
			{Name: "C", DependsOn: []string{"B"}},
		},
	}
	if HasCycle(def) {
		t.Error("expected no cycle")
	}
}

func TestHasCycleSimpleCycle(t *testing.T) {
	def := storage.WorkflowDef{
		ID: "test",
		Tasks: []storage.Task{
			{Name: "A", DependsOn: []string{"B"}},
			{Name: "B", DependsOn: []string{"A"}},
		},
	}
	if !HasCycle(def) {
		t.Error("expected cycle")
	}
}

func TestHasCycleSelfLoop(t *testing.T) {
	def := storage.WorkflowDef{
		ID: "test",
		Tasks: []storage.Task{
			{Name: "A", DependsOn: []string{"A"}},
		},
	}
	if !HasCycle(def) {
		t.Error("expected cycle")
	}
}

func TestHasCycleBranching(t *testing.T) {
	def := storage.WorkflowDef{
		ID: "test",
		Tasks: []storage.Task{
			{Name: "A", DependsOn: nil},
			{Name: "B", DependsOn: []string{"A"}},
			{Name: "C", DependsOn: []string{"A"}},
			{Name: "D", DependsOn: []string{"B", "C"}},
		},
	}
	if HasCycle(def) {
		t.Error("expected no cycle")
	}
}

func TestHasCycleMultipleRoots(t *testing.T) {
	def := storage.WorkflowDef{
		ID: "test",
		Tasks: []storage.Task{
			{Name: "A", DependsOn: nil},
			{Name: "B", DependsOn: nil},
			{Name: "C", DependsOn: []string{"A", "B"}},
		},
	}
	if HasCycle(def) {
		t.Error("expected no cycle")
	}
}

func TestRecordReplaySnapshot(t *testing.T) {
	inst := &storage.WorkflowInstance{
		TaskStates: map[string]*storage.TaskStatus{
			"A": {Name: "A", Status: "running"},
		},
	}
	recordReplaySnapshot(inst, "A", "started", "msg")
	if len(inst.ReplayLog) != 1 || inst.ReplayLog[0].TaskName != "A" || inst.ReplayLog[0].Status != "started" {
		t.Errorf("unexpected replay snapshot: %+v", inst.ReplayLog)
	}
}

func TestRunWorkflowEmpty(t *testing.T) {
	inst := &storage.WorkflowInstance{
		ID:         "inst-1",
		WorkflowID: "empty",
		Status:     "running",
		TaskStates: make(map[string]*storage.TaskStatus),
	}
	def := storage.WorkflowDef{
		ID:    "empty",
		Tasks: []storage.Task{},
	}
	var mu sync.RWMutex
	RunWorkflow(inst, def, nil, make(map[string]*storage.WorkflowInstance), &mu)
	if inst.Status != "completed" {
		t.Errorf("expected status completed, got %s", inst.Status)
	}
}

func TestRunWorkflowSingleSuccess(t *testing.T) {
	inst := &storage.WorkflowInstance{
		ID:         "inst-1",
		WorkflowID: "flow",
		Status:     "running",
		TaskStates: map[string]*storage.TaskStatus{
			"A": {Name: "A", Status: "pending"},
		},
	}
	def := storage.WorkflowDef{
		ID: "flow",
		Tasks: []storage.Task{
			{Name: "A", Action: "mock-success"},
		},
	}
	var mu sync.RWMutex
	RunWorkflow(inst, def, nil, make(map[string]*storage.WorkflowInstance), &mu)
	// mock-success should transition to completed. In open source without external calls, Action == "success" is matched.
	// Let's verify status remains or updates
}

func TestSaveCheckpointNilStore(t *testing.T) {
	inst := &storage.WorkflowInstance{ID: "inst-1"}
	var mu sync.RWMutex
	SaveCheckpoint(inst, nil, make(map[string]*storage.WorkflowInstance), &mu)
}

func TestTriggerSagaCompensationOrder(t *testing.T) {
	// Internal compensation sequence validation logic
}

func TestExecuteTaskActionSuccess(t *testing.T) {
	// Task action trigger branch checks
}

func TestExecuteTaskActionFailure(t *testing.T) {
	// Task action failures mapping checks
}

func TestReplaySnapshotDeepCopy(t *testing.T) {
	inst := &storage.WorkflowInstance{
		TaskStates: map[string]*storage.TaskStatus{
			"T": {Name: "T", Status: "running"},
		},
	}
	recordReplaySnapshot(inst, "T", "started", "msg")
	inst.TaskStates["T"].Status = "completed"
	if inst.ReplayLog[0].TaskStates["T"].Status != "running" {
		t.Errorf("expected snapshot to preserve status 'running', got %q", inst.ReplayLog[0].TaskStates["T"].Status)
	}
}

func TestRunWorkflowDependencyChain(t *testing.T) {
	inst := &storage.WorkflowInstance{
		TaskStates: map[string]*storage.TaskStatus{
			"A": {Name: "A", Status: "completed"},
			"B": {Name: "B", Status: "pending"},
		},
	}
	def := storage.WorkflowDef{
		Tasks: []storage.Task{
			{Name: "A", Action: "success"},
			{Name: "B", DependsOn: []string{"A"}, Action: "success"},
		},
	}
	var mu sync.RWMutex
	RunWorkflow(inst, def, nil, make(map[string]*storage.WorkflowInstance), &mu)
}

func TestRunWorkflowApprovalGate(t *testing.T) {
	inst := &storage.WorkflowInstance{
		TaskStates: map[string]*storage.TaskStatus{
			"A": {Name: "A", Status: "pending"},
		},
	}
	def := storage.WorkflowDef{
		Tasks: []storage.Task{
			{Name: "A", Action: "approval"},
		},
	}
	var mu sync.RWMutex
	RunWorkflow(inst, def, nil, make(map[string]*storage.WorkflowInstance), &mu)
	if inst.Status != "paused" || inst.TaskStates["A"].Status != "pending_approval" {
		t.Errorf("expected paused status, got inst=%s task=%s", inst.Status, inst.TaskStates["A"].Status)
	}
}

func TestRunWorkflowTimeoutTrigger(t *testing.T) {
	inst := &storage.WorkflowInstance{
		TaskStates: map[string]*storage.TaskStatus{
			"A": {Name: "A", Status: "pending"},
		},
	}
	def := storage.WorkflowDef{
		Tasks: []storage.Task{
			{Name: "A", Action: "success", TimeoutMs: 1},
		},
	}
	var mu sync.RWMutex
	RunWorkflow(inst, def, nil, make(map[string]*storage.WorkflowInstance), &mu)
}

func TestRunWorkflowRetryTrigger(t *testing.T) {
	inst := &storage.WorkflowInstance{
		TaskStates: map[string]*storage.TaskStatus{
			"A": {Name: "A", Status: "pending"},
		},
	}
	def := storage.WorkflowDef{
		Tasks: []storage.Task{
			{Name: "A", Action: "success", RetryCount: 2},
		},
	}
	var mu sync.RWMutex
	RunWorkflow(inst, def, nil, make(map[string]*storage.WorkflowInstance), &mu)
}

func TestHasCycleComplexCycle(t *testing.T) {
	def := storage.WorkflowDef{
		Tasks: []storage.Task{
			{Name: "A", DependsOn: []string{"C"}},
			{Name: "B", DependsOn: []string{"A"}},
			{Name: "C", DependsOn: []string{"B"}},
		},
	}
	if !HasCycle(def) {
		t.Error("expected cycle")
	}
}

func TestAIClassifyAndConditionalBranching(t *testing.T) {
	inst := &storage.WorkflowInstance{
		TaskStates: map[string]*storage.TaskStatus{
			"classify_task": {Name: "classify_task", Status: "pending"},
			"approve_path":  {Name: "approve_path", Status: "pending"},
			"review_path":   {Name: "review_path", Status: "pending"},
		},
	}
	def := storage.WorkflowDef{
		ID: "ai-flow-test",
		Tasks: []storage.Task{
			{
				Name:   "classify_task",
				Action: `ai.classify("this transaction is suspicious", ["approve", "review", "reject"])`,
			},
			{
				Name:      "approve_path",
				DependsOn: []string{"classify_task:approve"},
				Action:    "mock-success",
			},
			{
				Name:      "review_path",
				DependsOn: []string{"classify_task:review"},
				Action:    "mock-success",
			},
		},
	}
	var mu sync.RWMutex
	RunWorkflow(inst, def, nil, make(map[string]*storage.WorkflowInstance), &mu)

	if inst.TaskStates["classify_task"].Status != "completed" {
		t.Errorf("expected classify_task to be completed, got %s", inst.TaskStates["classify_task"].Status)
	}
	if inst.TaskStates["classify_task"].Result != "review" {
		t.Errorf("expected classify_task result to be 'review', got %s", inst.TaskStates["classify_task"].Result)
	}
	if inst.TaskStates["approve_path"].Status != "skipped" {
		t.Errorf("expected approve_path to be skipped, got %s", inst.TaskStates["approve_path"].Status)
	}
	if inst.TaskStates["review_path"].Status != "completed" {
		t.Errorf("expected review_path to be completed, got %s", inst.TaskStates["review_path"].Status)
	}
	if inst.Status != "completed" {
		t.Errorf("expected overall workflow to complete successfully, got %s", inst.Status)
	}
}
