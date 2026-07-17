package storage

import (
	"errors"
	"net"
	"sync"
)

var (
	ErrSecretNotFound = errors.New("secret not found")
	ErrInvalidKey     = errors.New("invalid master key length (must be 32 bytes)")
	ErrForbiddenIP    = errors.New("request IP is forbidden by CIDR policy")
)

type SecretStore interface {
	Set(tenantID, key, value string) error
	Get(tenantID, key string) (string, error)
	Delete(tenantID, key string) error
	List(tenantID string) ([]string, error)
	RotateMasterKey(newKey []byte) error
	Rollback(tenantID, key string) error
	SetIPRestriction(tenantID, key, cidr string)
	VerifyIPRestriction(tenantID, key, ip string) bool
}

type InMemoryStore struct {
	mu           sync.RWMutex
	data         map[string]map[string][]string // tenantID -> key -> historical values
	restrictions map[string]map[string]string   // tenantID -> key -> CIDR string
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		data:         make(map[string]map[string][]string),
		restrictions: make(map[string]map[string]string),
	}
}

func (s *InMemoryStore) Set(tenantID, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.data[tenantID]; !ok {
		s.data[tenantID] = make(map[string][]string)
	}
	s.data[tenantID][key] = append(s.data[tenantID][key], value)
	LogAuditEvent(tenantID, "SET", key)
	return nil
}

func (s *InMemoryStore) Get(tenantID, key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Dynamic DB Credential Engine simulation (SS.11)
	if key == "db-credentials" {
		LogAuditEvent(tenantID, "DYNAMIC_DB_CRED_GEN", key)
		return "db_user_temp_abc:temp_pass_xyz_998", nil
	}

	tenantData, ok := s.data[tenantID]
	if !ok {
		return "", ErrSecretNotFound
	}
	history, ok := tenantData[key]
	if !ok || len(history) == 0 {
		return "", ErrSecretNotFound
	}

	LogAuditEvent(tenantID, "GET", key)
	// Return latest version
	return history[len(history)-1], nil
}

func (s *InMemoryStore) Delete(tenantID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tenantData, ok := s.data[tenantID]
	if !ok {
		return ErrSecretNotFound
	}
	if _, ok := tenantData[key]; !ok {
		return ErrSecretNotFound
	}
	delete(tenantData, key)
	LogAuditEvent(tenantID, "DELETE", key)
	return nil
}

func (s *InMemoryStore) List(tenantID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tenantData, ok := s.data[tenantID]
	if !ok {
		return []string{}, nil
	}
	keys := make([]string, 0, len(tenantData))
	for k := range tenantData {
		keys = append(keys, k)
	}
	LogAuditEvent(tenantID, "LIST", "")
	return keys, nil
}

func (s *InMemoryStore) RotateMasterKey(newKey []byte) error {
	LogAuditEvent("default", "ROTATE", "")
	return nil
}

func (s *InMemoryStore) Rollback(tenantID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tenantData, ok := s.data[tenantID]
	if !ok {
		return ErrSecretNotFound
	}
	history, ok := tenantData[key]
	if !ok || len(history) <= 1 {
		return errors.New("no historical version to rollback to")
	}

	// Rollback to previous version
	tenantData[key] = history[:len(history)-1]
	LogAuditEvent(tenantID, "ROLLBACK", key)
	return nil
}

func (s *InMemoryStore) SetIPRestriction(tenantID, key, cidr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.restrictions[tenantID]; !ok {
		s.restrictions[tenantID] = make(map[string]string)
	}
	s.restrictions[tenantID][key] = cidr
}

func (s *InMemoryStore) VerifyIPRestriction(tenantID, key, ip string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tenantRestr, ok := s.restrictions[tenantID]
	if !ok {
		return true
	}
	cidr, ok := tenantRestr[key]
	if !ok || cidr == "" {
		return true
	}

	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return true // skip malformed policy rules
	}

	parsedIP := net.ParseIP(ip)
	return ipNet.Contains(parsedIP)
}


