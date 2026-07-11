// Package lock implements a distributed lock manager for ServMesh.
//
// Each lock is identified by a string key and is held by a named owner
// (typically the service + instance address). Locks have a configurable TTL
// that is refreshed via Extend(); expired locks are released automatically by
// the background eviction loop.
//
// API surface exposed over HTTP (mounted by registry.Handler):
//
//	POST /api/lock/acquire  {"key":"...", "owner":"...", "ttl_ms":5000}
//	POST /api/lock/release  {"key":"...", "owner":"..."}
//	POST /api/lock/extend   {"key":"...", "owner":"...", "ttl_ms":5000}
//	GET  /api/lock/status?key=...
//	GET  /api/lock/list
package lock

import (
	"sync"
	"time"
)

// LockEntry represents a single held distributed lock.
type LockEntry struct {
	Key        string    `json:"key"`
	Owner      string    `json:"owner"`
	ExpiresAt  time.Time `json:"expires_at"`
	AcquiredAt time.Time `json:"acquired_at"`
	Token      int64     `json:"token"`
}

// IsExpired reports whether the lock has passed its TTL.
func (l *LockEntry) IsExpired() bool {
	return time.Now().After(l.ExpiresAt)
}

// AcquireResult is returned from Store.Acquire.
type AcquireResult struct {
	Acquired bool       `json:"acquired"`
	Lock     *LockEntry `json:"lock,omitempty"`
	// HeldBy is populated when Acquired==false and the lock is taken by another owner.
	HeldBy string `json:"held_by,omitempty"`
}

// Store is a thread-safe, TTL-based distributed lock store.
// All operations are O(1) against the in-memory map. Eviction runs
// on a background goroutine.
type Store struct {
	mu        sync.Mutex
	locks     map[string]*LockEntry
	nextToken int64

	// defaultTTL is used when the caller provides ttl=0.
	defaultTTL time.Duration

	stopCh chan struct{}
}

// NewStore creates a lock store with the given default TTL and starts the
// background eviction goroutine.
func NewStore(defaultTTL time.Duration) *Store {
	s := &Store{
		locks:      make(map[string]*LockEntry),
		defaultTTL: defaultTTL,
		stopCh:     make(chan struct{}),
	}
	go s.evictionLoop(defaultTTL / 2)
	return s
}

// Acquire attempts to take a lock on key for owner.
// If the lock is free (or expired), it is granted and Acquired==true.
// If the lock is held by another owner it is not acquired.
// Re-acquiring with the same owner refreshes the TTL (idempotent).
func (s *Store) Acquire(key, owner string, ttl time.Duration) AcquireResult {
	if ttl <= 0 {
		ttl = s.defaultTTL
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.locks[key]; ok && !existing.IsExpired() {
		if existing.Owner == owner {
			// Re-acquire by the same owner — refresh TTL and increment/update token.
			s.nextToken++
			existing.ExpiresAt = time.Now().Add(ttl)
			existing.Token = s.nextToken
			return AcquireResult{Acquired: true, Lock: existing}
		}
		// Held by a different owner.
		return AcquireResult{Acquired: false, HeldBy: existing.Owner}
	}

	s.nextToken++
	entry := &LockEntry{
		Key:        key,
		Owner:      owner,
		AcquiredAt: time.Now(),
		ExpiresAt:  time.Now().Add(ttl),
		Token:      s.nextToken,
	}
	s.locks[key] = entry
	return AcquireResult{Acquired: true, Lock: entry}
}

// Release relinquishes the lock on key if it is held by owner.
// Returns true when the lock was released, false when it wasn't held or is
// owned by someone else.
func (s *Store) Release(key, owner string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.locks[key]
	if !ok {
		return false // not held
	}
	if existing.Owner != owner {
		return false // held by another
	}
	delete(s.locks, key)
	return true
}

// Extend refreshes the TTL of an existing lock held by owner.
// Returns the updated entry and true on success, or nil and false if the lock
// is not held by owner or doesn't exist.
func (s *Store) Extend(key, owner string, ttl time.Duration) (*LockEntry, bool) {
	if ttl <= 0 {
		ttl = s.defaultTTL
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.locks[key]
	if !ok || existing.IsExpired() || existing.Owner != owner {
		return nil, false
	}
	existing.ExpiresAt = time.Now().Add(ttl)
	return existing, true
}

// Status returns the current state of a lock key.
// Returns (entry, true) if held and not expired, (nil, false) otherwise.
func (s *Store) Status(key string) (*LockEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.locks[key]
	if !ok || entry.IsExpired() {
		return nil, false
	}
	return entry, true
}

// List returns a snapshot of all currently held (non-expired) locks.
func (s *Store) List() []LockEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []LockEntry
	for _, e := range s.locks {
		if !e.IsExpired() {
			out = append(out, *e)
		}
	}
	return out
}

// evictionLoop removes expired locks at the given interval.
func (s *Store) evictionLoop(interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.evict()
		case <-s.stopCh:
			return
		}
	}
}

func (s *Store) evict() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, e := range s.locks {
		if e.IsExpired() {
			delete(s.locks, key)
		}
	}
}

// Close stops the background eviction goroutine.
func (s *Store) Close() {
	close(s.stopCh)
}
