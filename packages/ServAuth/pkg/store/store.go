package store

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/vyuvaraj/serv/packages/ServShared"
)

type User struct {
	Username       string    `json:"username"`
	Email          string    `json:"email"`
	Password       string    `json:"-"`
	Salt           string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	FailedAttempts int       `json:"-"`
	LockedUntil    time.Time `json:"-"`
	ResetToken     string    `json:"-"`
	TenantID       string    `json:"tenant_id,omitempty"`
	MFASecret        string    `json:"-"`
	MFAEnabled       bool      `json:"mfa_enabled"`
	PasskeyID        string    `json:"passkey_id,omitempty"`
	PasskeyPublicKey string    `json:"passkey_public_key,omitempty"`
	LastDevice       string    `json:"last_device,omitempty"`
	LastCountry      string    `json:"last_country,omitempty"`
}

type ResetRequest struct {
	Email string `json:"email"`
}

type ResetConfirm struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type RegisterRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (r *ResetRequest) Validate() error {
	if r.Email == "" || !strings.Contains(r.Email, "@") {
		return fmt.Errorf("invalid email address")
	}
	return nil
}

func (r *ResetConfirm) Validate() error {
	if r.Token == "" {
		return fmt.Errorf("token cannot be empty")
	}
	if len(r.Password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	return nil
}

func (r *RegisterRequest) Validate() error {
	if r.Username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if len(r.Password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	if r.Email == "" || !strings.Contains(r.Email, "@") {
		return fmt.Errorf("invalid email address")
	}
	return nil
}

func (r *LoginRequest) Validate() error {
	if r.Username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if r.Password == "" {
		return fmt.Errorf("password cannot be empty")
	}
	return nil
}

type LoginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
}

type APIKey struct {
	Key       string    `json:"key"`
	Username  string    `json:"username"`
	Scopes    []string  `json:"scopes"`
	CreatedAt time.Time `json:"created_at"`
	Revoked   bool      `json:"revoked"`
}

type Session struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	CreatedAt time.Time `json:"created_at"`
	Revoked   bool      `json:"revoked"`
}

type JWKKeyPair struct {
	KeyID      string
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey
	CreatedAt  time.Time
}

type UserStore interface {
	LoadUsers() (map[string]User, error)
	SaveUsers(users map[string]User) error
	LoadKeys() (map[string]*APIKey, error)
	SaveKeys(keys map[string]*APIKey) error
	LoadSessions() (map[string]*Session, error)
	SaveSessions(sessions map[string]*Session) error
}

type ServStoreUserStore struct {
	Client *ServShared.StoreClient
}

func NewServStoreUserStore(client *ServShared.StoreClient) *ServStoreUserStore {
	return &ServStoreUserStore{Client: client}
}

func (s *ServStoreUserStore) LoadUsers() (map[string]User, error) {
	if s.Client == nil {
		return nil, errors.New("store client not initialized")
	}
	data, err := s.Client.Get("serv-auth-users", "users.json")
	if err != nil {
		return nil, err
	}
	var users map[string]User
	if err := json.Unmarshal(data, &users); err != nil {
		return nil, err
	}
	log.Printf("[PERSISTENCE] Loaded %d users from ServStore", len(users))
	return users, nil
}

func (s *ServStoreUserStore) SaveUsers(users map[string]User) error {
	if s.Client == nil {
		return errors.New("store client not initialized")
	}
	data, err := json.Marshal(users)
	if err != nil {
		return err
	}
	return s.Client.Put("serv-auth-users", "users.json", data)
}

func (s *ServStoreUserStore) LoadKeys() (map[string]*APIKey, error) {
	if s.Client == nil {
		return nil, errors.New("store client not initialized")
	}
	data, err := s.Client.Get("serv-auth-users", "apikeys.json")
	if err != nil {
		return nil, err
	}
	var keys map[string]*APIKey
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, err
	}
	log.Printf("[PERSISTENCE] Loaded %d API keys from ServStore", len(keys))
	return keys, nil
}

func (s *ServStoreUserStore) SaveKeys(keys map[string]*APIKey) error {
	if s.Client == nil {
		return errors.New("store client not initialized")
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return s.Client.Put("serv-auth-users", "apikeys.json", data)
}

func (s *ServStoreUserStore) LoadSessions() (map[string]*Session, error) {
	if s.Client == nil {
		return nil, errors.New("store client not initialized")
	}
	data, err := s.Client.Get("serv-auth-users", "sessions.json")
	if err != nil {
		return nil, err
	}
	var sessions map[string]*Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, err
	}
	log.Printf("[PERSISTENCE] Loaded %d sessions from ServStore", len(sessions))
	return sessions, nil
}

func (s *ServStoreUserStore) SaveSessions(sessions map[string]*Session) error {
	if s.Client == nil {
		return errors.New("store client not initialized")
	}
	data, err := json.Marshal(sessions)
	if err != nil {
		return err
	}
	return s.Client.Put("serv-auth-users", "sessions.json", data)
}
