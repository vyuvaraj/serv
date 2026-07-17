package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Lock represents a successfully acquired lease.
type Lock struct {
	Key             string    `json:"key"`
	Owner           string    `json:"owner"`
	ClientID        string    `json:"client_id"`
	ReentrancyCount int       `json:"reentrancy_count"`
	FencingToken    int64     `json:"fencing_token"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type waiter struct {
	owner    string
	clientID string
	ttl      time.Duration
	granted  chan *Lock
}

// LockBackend defines the interface for distributed lock backends.
type LockBackend interface {
	Acquire(key string, owner string, clientID string, ttl time.Duration) (*Lock, error)
	AcquireWithWait(key string, owner string, clientID string, ttl time.Duration, waitTimeout time.Duration) (*Lock, error)
	Release(key string, owner string, fencingToken int64) (bool, error)
	Renew(key string, owner string, fencingToken int64, ttl time.Duration) (bool, error)
	Get(key string) (*Lock, error)
}

// InMemoryStore is a thread-safe, local implementation of LockBackend.
type InMemoryStore struct {
	mu            sync.Mutex
	locks         map[string]*Lock
	waiters       map[string][]*waiter
	waitingFor    map[string]string // owner -> key
	tokenCounter  int64
	deadlockCount int64
}

// NewInMemoryStore initializes and returns a local LockBackend.
func NewInMemoryStore() *InMemoryStore {
	store := &InMemoryStore{
		locks:      make(map[string]*Lock),
		waiters:    make(map[string][]*waiter),
		waitingFor: make(map[string]string),
	}
	go store.startExpiryCleaner(1 * time.Second)
	return store
}

// Acquire requests a lock for a key. Returns error if already acquired and not expired.
func (s *InMemoryStore) Acquire(key string, owner string, clientID string, ttl time.Duration) (*Lock, error) {
	return s.AcquireWithWait(key, owner, clientID, ttl, 0)
}

// AcquireWithWait requests a lock for a key. Blocks up to waitTimeout if the lock is held.
func (s *InMemoryStore) AcquireWithWait(key string, owner string, clientID string, ttl time.Duration, waitTimeout time.Duration) (*Lock, error) {
	s.mu.Lock()
	now := time.Now()
	existing, exists := s.locks[key]
	if exists && existing.ExpiresAt.After(now) {
		if existing.Owner == owner && existing.ClientID == clientID {
			// Reentrant behavior: increment count and extend lock duration
			existing.ReentrancyCount++
			existing.ExpiresAt = now.Add(ttl)
			s.mu.Unlock()
			return existing, nil
		}

		if waitTimeout <= 0 {
			s.mu.Unlock()
			return nil, fmt.Errorf("lock for key %q is held by owner %q", key, existing.Owner)
		}

		// Deadlock Cycle Detection
		if s.hasCycleLocked(existing.Owner, owner) {
			s.deadlockCount++
			s.mu.Unlock()
			return nil, fmt.Errorf("deadlock detected: cycle in lock wait queue")
		}

		// Queue the waiter
		w := &waiter{
			owner:    owner,
			clientID: clientID,
			ttl:      ttl,
			granted:  make(chan *Lock, 1),
		}
		s.waiters[key] = append(s.waiters[key], w)
		s.waitingFor[owner] = key
		s.mu.Unlock()

		select {
		case lock := <-w.granted:
			s.mu.Lock()
			delete(s.waitingFor, owner)
			s.mu.Unlock()
			return lock, nil
		case <-time.After(waitTimeout):
			// Timeout expired: remove from waiter queue
			s.mu.Lock()
			delete(s.waitingFor, owner)
			q := s.waiters[key]
			for i, val := range q {
				if val == w {
					s.waiters[key] = append(q[:i], q[i+1:]...)
					break
				}
			}
			s.mu.Unlock()
			return nil, fmt.Errorf("timeout waiting for lock %q", key)
		}
	}

	s.tokenCounter++
	lock := &Lock{
		Key:             key,
		Owner:           owner,
		ClientID:        clientID,
		ReentrancyCount: 1,
		FencingToken:    s.tokenCounter,
		ExpiresAt:       now.Add(ttl),
	}
	s.locks[key] = lock
	s.mu.Unlock()
	return lock, nil
}

func (s *InMemoryStore) hasCycleLocked(startOwner, targetOwner string) bool {
	visited := make(map[string]bool)
	visited[targetOwner] = true

	current := startOwner
	for current != "" {
		if visited[current] {
			if current == targetOwner {
				return true
			}
			break
		}
		visited[current] = true

		// Find what key current is waiting for
		waitKey, waiting := s.waitingFor[current]
		if !waiting {
			break
		}

		// Find who owns waitKey
		lock, exists := s.locks[waitKey]
		if !exists || lock.ExpiresAt.Before(time.Now()) {
			break
		}

		current = lock.Owner
	}
	return false
}

// Release frees the lock if the requester matches the owner.
func (s *InMemoryStore) Release(key string, owner string, fencingToken int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.locks[key]
	if !exists || existing.ExpiresAt.Before(time.Now()) {
		return false, nil
	}

	if existing.Owner != owner {
		return false, fmt.Errorf("cannot release lock owned by %q", existing.Owner)
	}

	if fencingToken > 0 && existing.FencingToken != fencingToken {
		return false, fmt.Errorf("fencing token mismatch (expected %d, got %d)", existing.FencingToken, fencingToken)
	}

	existing.ReentrancyCount--
	if existing.ReentrancyCount > 0 {
		return true, nil
	}

	delete(s.locks, key)
	s.grantNextWaiterLocked(key)
	return true, nil
}

// Renew extends the lock lease if active and owned by the requestor.
func (s *InMemoryStore) Renew(key string, owner string, fencingToken int64, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.locks[key]
	if !exists || existing.ExpiresAt.Before(time.Now()) {
		return false, fmt.Errorf("lock has expired or does not exist")
	}

	if existing.Owner != owner {
		return false, fmt.Errorf("cannot renew lock owned by %q", existing.Owner)
	}

	if fencingToken > 0 && existing.FencingToken != fencingToken {
		return false, fmt.Errorf("fencing token mismatch (expected %d, got %d)", existing.FencingToken, fencingToken)
	}

	existing.ExpiresAt = time.Now().Add(ttl)
	return true, nil
}

// Get retrieves current active lock details.
func (s *InMemoryStore) Get(key string) (*Lock, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.locks[key]
	if !exists || existing.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("lock not found or expired")
	}
	return existing, nil
}

func (s *InMemoryStore) grantNextWaiterLocked(key string) {
	q, exists := s.waiters[key]
	if !exists || len(q) == 0 {
		return
	}

	// Pop the first waiter
	w := q[0]
	if len(q) > 1 {
		s.waiters[key] = q[1:]
	} else {
		delete(s.waiters, key)
	}

	delete(s.waitingFor, w.owner)

	s.tokenCounter++
	lock := &Lock{
		Key:             key,
		Owner:           w.owner,
		ClientID:        w.clientID,
		ReentrancyCount: 1,
		FencingToken:    s.tokenCounter,
		ExpiresAt:       time.Now().Add(w.ttl),
	}
	s.locks[key] = lock
	w.granted <- lock
}

func (s *InMemoryStore) startExpiryCleaner(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for k, v := range s.locks {
			if v.ExpiresAt.Before(now) {
				delete(s.locks, k)
				s.grantNextWaiterLocked(k)
			}
		}
		s.mu.Unlock()
	}
}

type LockInfo struct {
	Key          string    `json:"key"`
	Owner        string    `json:"owner"`
	FencingToken int64     `json:"fencing_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Waiters      []string  `json:"waiters"`
}

func (s *InMemoryStore) GetActiveLocks() []LockInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	var res []LockInfo
	now := time.Now()
	for k, lock := range s.locks {
		if lock.ExpiresAt.After(now) {
			var waitList []string
			for _, w := range s.waiters[k] {
				waitList = append(waitList, w.owner)
			}
			res = append(res, LockInfo{
				Key:          lock.Key,
				Owner:        lock.Owner,
				FencingToken: lock.FencingToken,
				ExpiresAt:    lock.ExpiresAt,
				Waiters:      waitList,
			})
		}
	}
	return res
}

func (s *InMemoryStore) GetDeadlockCount() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deadlockCount
}

// FileLockStore implements local file-persisted locking.
type FileLockStore struct {
	*InMemoryStore
	filePath string
}

func NewFileLockStore(filePath string) (*FileLockStore, error) {
	ims := NewInMemoryStore()
	store := &FileLockStore{
		InMemoryStore: ims,
		filePath:      filePath,
	}
	if err := store.loadFromFile(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileLockStore) loadFromFile() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.filePath); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return nil
	}

	var savedLocks map[string]*Lock
	if err := json.Unmarshal(data, &savedLocks); err != nil {
		return err
	}

	now := time.Now()
	for k, lock := range savedLocks {
		if lock.ExpiresAt.After(now) {
			s.locks[k] = lock
			if lock.FencingToken > s.tokenCounter {
				s.tokenCounter = lock.FencingToken
			}
		}
	}
	return nil
}

func (s *FileLockStore) saveToFile() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(s.locks)
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0600)
}

func (s *FileLockStore) Acquire(key string, owner string, clientID string, ttl time.Duration) (*Lock, error) {
	lock, err := s.InMemoryStore.Acquire(key, owner, clientID, ttl)
	if err == nil {
		s.saveToFile()
	}
	return lock, err
}

func (s *FileLockStore) AcquireWithWait(key string, owner string, clientID string, ttl time.Duration, waitTimeout time.Duration) (*Lock, error) {
	lock, err := s.InMemoryStore.AcquireWithWait(key, owner, clientID, ttl, waitTimeout)
	if err == nil {
		s.saveToFile()
	}
	return lock, err
}

func (s *FileLockStore) Release(key string, owner string, fencingToken int64) (bool, error) {
	ok, err := s.InMemoryStore.Release(key, owner, fencingToken)
	if ok && err == nil {
		s.saveToFile()
	}
	return ok, err
}

func (s *FileLockStore) Renew(key string, owner string, fencingToken int64, ttl time.Duration) (bool, error) {
	ok, err := s.InMemoryStore.Renew(key, owner, fencingToken, ttl)
	if ok && err == nil {
		s.saveToFile()
	}
	return ok, err
}
