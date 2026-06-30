package main

import (
	"encoding/json"
	"errors"
	"log"

	"github.com/vyuvaraj/ServShared"
)

type WorkflowStore interface {
	LoadDefinitions() (map[string]WorkflowDef, error)
	SaveDefinitions(defs map[string]WorkflowDef) error
	LoadInstances() (map[string]*WorkflowInstance, error)
	SaveInstances(insts map[string]*WorkflowInstance) error
	GetClient() *ServShared.StoreClient
}

type ServStoreWorkflowStore struct {
	client *ServShared.StoreClient
}

func NewServStoreWorkflowStore(client *ServShared.StoreClient) *ServStoreWorkflowStore {
	return &ServStoreWorkflowStore{client: client}
}

func (s *ServStoreWorkflowStore) GetClient() *ServShared.StoreClient {
	return s.client
}

func (s *ServStoreWorkflowStore) LoadDefinitions() (map[string]WorkflowDef, error) {
	if s.client == nil {
		return nil, errors.New("store client not initialized")
	}
	data, err := s.client.Get("serv-flow-state", "definitions.json")
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
	if s.client == nil {
		return errors.New("store client not initialized")
	}
	data, err := json.Marshal(defs)
	if err != nil {
		return err
	}
	return s.client.Put("serv-flow-state", "definitions.json", data)
}

func (s *ServStoreWorkflowStore) LoadInstances() (map[string]*WorkflowInstance, error) {
	if s.client == nil {
		return nil, errors.New("store client not initialized")
	}
	data, err := s.client.Get("serv-flow-state", "instances.json")
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
	if s.client == nil {
		return errors.New("store client not initialized")
	}
	data, err := json.Marshal(insts)
	if err != nil {
		return err
	}
	return s.client.Put("serv-flow-state", "instances.json", data)
}
