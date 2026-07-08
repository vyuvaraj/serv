//go:build !wasm

package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// distLockMeshURL returns the ServMesh base URL for distributed lock operations.
func distLockMeshURL() string {
	if u := os.Getenv("SERV_MESH_URL"); u != "" {
		return u
	}
	return "http://localhost:8083"
}

// distLockOwner returns a stable owner identifier for this process.
func distLockOwner() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s/%d", host, os.Getpid())
}

type lockAcquireRequest struct {
	Key     string `json:"key"`
	Owner   string `json:"owner"`
	TTLSecs int    `json:"ttl_secs"`
}

type lockAcquireResponse struct {
	Acquired bool   `json:"acquired"`
	HeldBy   string `json:"held_by,omitempty"`
}

// AcquireLock attempts to acquire a distributed lock on `key` with the given
// timeout. It retries until the lock is acquired or the timeout expires.
// Returns true if the lock was acquired, false if the timeout elapsed.
//
// This is the runtime backing for the Serv-lang `lock("key", timeout)` primitive.
func AcquireLock(key string, timeoutSecs int) bool {
	owner := distLockOwner()
	url := distLockMeshURL() + "/api/lock/acquire"
	deadline := time.Now().Add(time.Duration(timeoutSecs) * time.Second)

	payload := lockAcquireRequest{
		Key:     key,
		Owner:   owner,
		TTLSecs: timeoutSecs + 5, // TTL slightly longer than timeout to avoid early expiry
	}

	for time.Now().Before(deadline) {
		body, _ := json.Marshal(payload)
		resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:gosec
		if err == nil {
			var result lockAcquireResponse
			json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()
			if result.Acquired {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond) // poll interval
	}
	return false
}

// ReleaseLock releases a previously acquired distributed lock on `key`.
// This is the runtime backing for the Serv-lang `unlock("key")` primitive.
func ReleaseLock(key string) bool {
	owner := distLockOwner()
	url := distLockMeshURL() + "/api/lock/release"

	payload := map[string]string{"key": key, "owner": owner}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body)) //nolint:gosec
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// WithLock acquires a distributed lock, runs fn, then releases the lock.
// This is the runtime backing for the Serv-lang `lock("key") { ... }` block.
// Returns false if the lock could not be acquired within timeoutSecs.
func WithLock(key string, timeoutSecs int, fn func()) bool {
	if !AcquireLock(key, timeoutSecs) {
		return false
	}
	defer ReleaseLock(key)
	fn()
	return true
}

// LockStatus returns information about who currently holds a lock.
func LockStatus(key string) map[string]interface{} {
	url := distLockMeshURL() + "/api/lock/status?key=" + key
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	return result
}
