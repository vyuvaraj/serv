package storage

import (
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

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
	Status     string    `json:"status"` // pending, running, completed, failed, skipped
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Error      string    `json:"error,omitempty"`
	Result     string    `json:"result,omitempty"`
}

// ReplaySnapshot captures the workflow state at a single execution step for time-travel replay.
type ReplaySnapshot struct {
	StepIndex  int                    `json:"step_index"`
	TaskName   string                 `json:"task_name"`
	Status     string                 `json:"status"` // started, completed, failed
	Timestamp  time.Time              `json:"timestamp"`
	TaskStates map[string]*TaskStatus `json:"task_states"` // snapshot of all states at this moment
	Message    string                 `json:"message,omitempty"`
}

type WorkflowInstance struct {
	ID          string                `json:"id"`
	WorkflowID  string                `json:"workflow_id"`
	Status      string                `json:"status"` // running, completed, failed
	TaskStates  map[string]*TaskStatus `json:"task_states"`
	StartedAt   time.Time             `json:"started_at"`
	FinishedAt  time.Time             `json:"finished_at,omitempty"`
	Logs        []string              `json:"logs"`
	ReplayLog   []ReplaySnapshot      `json:"replay_log,omitempty"` // DX.13: time-travel snapshots
	Traceparent string                `json:"traceparent,omitempty"`
	Mu          sync.RWMutex          `json:"-"`
}

type WorkflowStore interface {
	LoadDefinitions() (map[string]WorkflowDef, error)
	SaveDefinitions(defs map[string]WorkflowDef) error
	LoadInstances() (map[string]*WorkflowInstance, error)
	SaveInstances(insts map[string]*WorkflowInstance) error
	GetClient() *ServShared.StoreClient
}

type ServStoreWorkflowStore struct {
	Client *ServShared.StoreClient
}

func NewServStoreWorkflowStore(client *ServShared.StoreClient) *ServStoreWorkflowStore {
	return &ServStoreWorkflowStore{Client: client}
}

func (s *ServStoreWorkflowStore) GetClient() *ServShared.StoreClient {
	return s.Client
}

func (s *ServStoreWorkflowStore) LoadDefinitions() (map[string]WorkflowDef, error) {
	if s.Client == nil {
		return nil, errors.New("store client not initialized")
	}
	data, err := s.Client.Get("serv-flow-state", "definitions.json")
	if err != nil {
		return nil, err
	}
	var defs map[string]WorkflowDef
	if err := json.Unmarshal(data, &defs); err != nil {
		return nil, err
	}
	log.Printf("[PERSISTENCE] Loaded %d workflow definitions from ServStore", len(defs))
	return defs, nil
}

func (s *ServStoreWorkflowStore) SaveDefinitions(defs map[string]WorkflowDef) error {
	if s.Client == nil {
		return errors.New("store client not initialized")
	}
	data, err := json.Marshal(defs)
	if err != nil {
		return err
	}
	return s.Client.Put("serv-flow-state", "definitions.json", data)
}

func (s *ServStoreWorkflowStore) LoadInstances() (map[string]*WorkflowInstance, error) {
	if s.Client == nil {
		return nil, errors.New("store client not initialized")
	}
	data, err := s.Client.Get("serv-flow-state", "instances.json")
	if err != nil {
		return nil, err
	}
	var insts map[string]*WorkflowInstance
	if err := json.Unmarshal(data, &insts); err != nil {
		return nil, err
	}
	log.Printf("[PERSISTENCE] Loaded %d workflow instances from ServStore", len(insts))
	return insts, nil
}

func (s *ServStoreWorkflowStore) SaveInstances(insts map[string]*WorkflowInstance) error {
	if s.Client == nil {
		return errors.New("store client not initialized")
	}
	data, err := json.Marshal(insts)
	if err != nil {
		return err
	}
	return s.Client.Put("serv-flow-state", "instances.json", data)
}
