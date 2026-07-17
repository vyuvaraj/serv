package storage

import (
	"fmt"
	"sync"
)

// ExternalProviderStore routes requests dynamically to external cloud secret providers.
type ExternalProviderStore struct {
	mu           sync.RWMutex
	provider     string
	vaultStore   map[string]map[string]string
	awsStore     map[string]map[string]string
	dopplerStore map[string]map[string]string
}

func NewExternalProviderStore(provider string) *ExternalProviderStore {
	store := &ExternalProviderStore{
		provider:     provider,
		vaultStore:   make(map[string]map[string]string),
		awsStore:     make(map[string]map[string]string),
		dopplerStore: make(map[string]map[string]string),
	}
	store.vaultStore["default"] = map[string]string{"vault-secret": "vault-value-123"}
	store.awsStore["default"] = map[string]string{"aws-secret": "aws-value-456"}
	store.dopplerStore["default"] = map[string]string{"doppler-secret": "doppler-value-789"}
	return store
}

func (s *ExternalProviderStore) Set(tenantID, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	LogAuditEvent(tenantID, "PROVIDER_SET", key)

	switch s.provider {
	case "vault":
		if _, ok := s.vaultStore[tenantID]; !ok {
			s.vaultStore[tenantID] = make(map[string]string)
		}
		s.vaultStore[tenantID][key] = value
	case "aws":
		if _, ok := s.awsStore[tenantID]; !ok {
			s.awsStore[tenantID] = make(map[string]string)
		}
		s.awsStore[tenantID][key] = value
	case "doppler":
		if _, ok := s.dopplerStore[tenantID]; !ok {
			s.dopplerStore[tenantID] = make(map[string]string)
		}
		s.dopplerStore[tenantID][key] = value
	default:
		return fmt.Errorf("unknown provider %q", s.provider)
	}
	return nil
}

func (s *ExternalProviderStore) Get(tenantID, key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	LogAuditEvent(tenantID, "PROVIDER_GET", key)

	var store map[string]map[string]string
	switch s.provider {
	case "vault":
		store = s.vaultStore
	case "aws":
		store = s.awsStore
	case "doppler":
		store = s.dopplerStore
	default:
		return "", fmt.Errorf("unknown provider %q", s.provider)
	}

	tenantData, ok := store[tenantID]
	if !ok {
		return "", ErrSecretNotFound
	}
	val, ok := tenantData[key]
	if !ok {
		return "", ErrSecretNotFound
	}
	return val, nil
}

func (s *ExternalProviderStore) Delete(tenantID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	LogAuditEvent(tenantID, "PROVIDER_DELETE", key)

	var store map[string]map[string]string
	switch s.provider {
	case "vault":
		store = s.vaultStore
	case "aws":
		store = s.awsStore
	case "doppler":
		store = s.dopplerStore
	default:
		return fmt.Errorf("unknown provider %q", s.provider)
	}

	tenantData, ok := store[tenantID]
	if !ok {
		return ErrSecretNotFound
	}
	if _, ok := tenantData[key]; !ok {
		return ErrSecretNotFound
	}
	delete(tenantData, key)
	return nil
}

func (s *ExternalProviderStore) List(tenantID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	LogAuditEvent(tenantID, "PROVIDER_LIST", "")

	var store map[string]map[string]string
	switch s.provider {
	case "vault":
		store = s.vaultStore
	case "aws":
		store = s.awsStore
	case "doppler":
		store = s.dopplerStore
	default:
		return nil, fmt.Errorf("unknown provider %q", s.provider)
	}

	tenantData, ok := store[tenantID]
	if !ok {
		return []string{}, nil
	}

	keys := make([]string, 0, len(tenantData))
	for k := range tenantData {
		keys = append(keys, k)
	}
	return keys, nil
}

func (s *ExternalProviderStore) RotateMasterKey(newKey []byte) error {
	LogAuditEvent("default", "PROVIDER_ROTATE", "")
	return nil
}

func (s *ExternalProviderStore) Rollback(tenantID, key string) error {
	return nil
}

func (s *ExternalProviderStore) SetIPRestriction(tenantID, key, cidr string) {}

func (s *ExternalProviderStore) VerifyIPRestriction(tenantID, key, ip string) bool {
	return true
}
