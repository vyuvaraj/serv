package main

import (
	"encoding/json"
	"errors"
	"log"

	"github.com/vyuvaraj/ServShared"
)

type TemplateStore interface {
	LoadTemplates() (map[string]map[string]string, error)
	SaveTemplates(templates map[string]map[string]string) error
}

type ServStoreTemplateStore struct {
	client *ServShared.StoreClient
}

func NewServStoreTemplateStore(client *ServShared.StoreClient) *ServStoreTemplateStore {
	return &ServStoreTemplateStore{client: client}
}

func (s *ServStoreTemplateStore) LoadTemplates() (map[string]map[string]string, error) {
	if s.client == nil {
		return nil, errors.New("store client not initialized")
	}
	data, err := s.client.Get("serv-mail-templates", "templates.json")
	if err != nil {
		return nil, err
	}
	var templates map[string]map[string]string
	if err := json.Unmarshal(data, &templates); err != nil {
		return nil, err
	}
	log.Printf("[PERSISTENCE] Loaded %d templates from ServStore", len(templates))
	return templates, nil
}

func (s *ServStoreTemplateStore) SaveTemplates(templates map[string]map[string]string) error {
	if s.client == nil {
		return errors.New("store client not initialized")
	}
	data, err := json.Marshal(templates)
	if err != nil {
		return err
	}
	return s.client.Put("serv-mail-templates", "templates.json", data)
}
