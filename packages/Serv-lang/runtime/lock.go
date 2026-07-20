//go:build !wasm

package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// distLockMeshURL returns the ServLock base URL.
func distLockMeshURL() string {
	if u := os.Getenv("SERV_LOCK_URL"); u != "" {
		return u
	}
	return "http://localhost:8089"
}

// distLockOwner returns a stable owner identifier for this process.
func distLockOwner() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s/%d", host, os.Getpid())
}

// AcquireLock attempts to acquire a distributed lock on `key` with the given
// timeout. It delegates waiting to the ServLock backend.
func AcquireLock(key string, timeoutSecs int) bool {
	owner := distLockOwner()
	url := distLockMeshURL() + "/api/locks/acquire"

	payload := map[string]interface{}{
		"key":         key,
		"owner":       owner,
		"client_id":   owner,
		"duration_ms": (timeoutSecs + 5) * 1000,
		"wait_ms":     timeoutSecs * 1000,
		"mode":        "exclusive",
	}

	body, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err == nil {
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}
	return false
}

// ReleaseLock releases a previously acquired distributed lock on `key`.
func ReleaseLock(key string) bool {
	owner := distLockOwner()
	url := distLockMeshURL() + "/api/locks/release"

	payload := map[string]string{
		"key":   key,
		"owner": owner,
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// WithLock acquires a distributed lock, runs fn, then releases the lock.
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
	url := distLockMeshURL() + "/api/locks/observability"
	resp, err := http.Get(url)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	defer resp.Body.Close()

	var locks []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&locks); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}

	for _, l := range locks {
		if l["key"] == key {
			return l
		}
	}
	return map[string]interface{}{"status": "free"}
}
