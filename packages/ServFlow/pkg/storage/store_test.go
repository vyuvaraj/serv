package storage

import (
	"testing"
)

func TestNewServStoreWorkflowStore(t *testing.T) {
	s := NewServStoreWorkflowStore(nil)
	if s == nil {
		t.Fatal("expected NewServStoreWorkflowStore to return non-nil")
	}
	if s.GetClient() != nil {
		t.Error("expected Client to be nil")
	}
}

func TestLoadDefinitionsMissingClient(t *testing.T) {
	s := NewServStoreWorkflowStore(nil)
	_, err := s.LoadDefinitions()
	if err == nil {
		t.Error("expected error loading with nil client")
	}
}

func TestSaveDefinitionsMissingClient(t *testing.T) {
	s := NewServStoreWorkflowStore(nil)
	err := s.SaveDefinitions(nil)
	if err == nil {
		t.Error("expected error saving with nil client")
	}
}

func TestLoadInstancesMissingClient(t *testing.T) {
	s := NewServStoreWorkflowStore(nil)
	_, err := s.LoadInstances()
	if err == nil {
		t.Error("expected error loading with nil client")
	}
}

func TestSaveInstancesMissingClient(t *testing.T) {
	s := NewServStoreWorkflowStore(nil)
	err := s.SaveInstances(nil)
	if err == nil {
		t.Error("expected error saving with nil client")
	}
}

func TestSQLWorkflowStore_SQLite(t *testing.T) {
	s, err := NewSQLWorkflowStore("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to create SQL store: %v", err)
	}
	defer s.Close()

	// Test definitions save/load
	defs := map[string]WorkflowDef{
		"flow1": {
			ID: "flow1",
			Tasks: []Task{
				{Name: "task1", Action: "action1"},
			},
		},
	}
	if err := s.SaveDefinitions(defs); err != nil {
		t.Fatalf("failed to save definitions: %v", err)
	}

	loadedDefs, err := s.LoadDefinitions()
	if err != nil {
		t.Fatalf("failed to load definitions: %v", err)
	}
	if len(loadedDefs) != 1 || loadedDefs["flow1"].ID != "flow1" {
		t.Errorf("incorrect loaded definitions: %+v", loadedDefs)
	}

	// Test instances save/load
	insts := map[string]*WorkflowInstance{
		"inst1": {
			ID:         "inst1",
			WorkflowID: "flow1",
			Status:     "running",
			TaskStates: make(map[string]*TaskStatus),
		},
	}
	if err := s.SaveInstances(insts); err != nil {
		t.Fatalf("failed to save instances: %v", err)
	}

	loadedInsts, err := s.LoadInstances()
	if err != nil {
		t.Fatalf("failed to load instances: %v", err)
	}
	if len(loadedInsts) != 1 || loadedInsts["inst1"].WorkflowID != "flow1" {
		t.Errorf("incorrect loaded instances: %+v", loadedInsts)
	}
}
