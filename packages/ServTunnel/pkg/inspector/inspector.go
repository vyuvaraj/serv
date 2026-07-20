// Package inspector provides an in-memory ring buffer that captures HTTP
// requests and responses flowing through the tunnel for debugging and replay.
package inspector

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Entry represents a single captured request/response pair.
type Entry struct {
	ID             string            `json:"id"`
	Timestamp      time.Time         `json:"timestamp"`
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	RequestHeaders map[string]string `json:"request_headers"`
	RequestBody    string            `json:"request_body,omitempty"`
	StatusCode     int               `json:"status_code"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	ResponseBody   string            `json:"response_body,omitempty"`
	LatencyMs      int64             `json:"latency_ms"`
	Subdomain      string            `json:"subdomain"`
}

// Inspector stores a rolling window of captured tunnel entries.
type Inspector struct {
	mu      sync.RWMutex
	entries []Entry
	maxSize int
	counter int64
}

// New creates an Inspector with the given ring buffer capacity.
func New(maxSize int) *Inspector {
	if maxSize <= 0 {
		maxSize = 100
	}
	return &Inspector{
		entries: make([]Entry, 0, maxSize),
		maxSize: maxSize,
	}
}

// Record adds a new entry to the ring buffer and returns its ID.
func (ins *Inspector) Record(e Entry) string {
	ins.mu.Lock()
	defer ins.mu.Unlock()

	ins.counter++
	id := fmt.Sprintf("req-%d", ins.counter)
	e.ID = id
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}

	if len(ins.entries) >= ins.maxSize {
		// Shift left, drop oldest.
		copy(ins.entries, ins.entries[1:])
		ins.entries[len(ins.entries)-1] = e
	} else {
		ins.entries = append(ins.entries, e)
	}
	return id
}

// Update updates an existing entry with response details.
func (ins *Inspector) Update(id string, statusCode int, respHeaders map[string]string, respBody string, latencyMs int64) {
	ins.mu.Lock()
	defer ins.mu.Unlock()

	for i, e := range ins.entries {
		if e.ID == id {
			ins.entries[i].StatusCode = statusCode
			ins.entries[i].ResponseHeaders = respHeaders
			ins.entries[i].ResponseBody = respBody
			ins.entries[i].LatencyMs = latencyMs
			return
		}
	}
}

// List returns all captured entries, newest last.
func (ins *Inspector) List() []Entry {
	ins.mu.RLock()
	defer ins.mu.RUnlock()
	out := make([]Entry, len(ins.entries))
	copy(out, ins.entries)
	return out
}

// Get returns a single entry by ID.
func (ins *Inspector) Get(id string) (Entry, bool) {
	ins.mu.RLock()
	defer ins.mu.RUnlock()
	for _, e := range ins.entries {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Count returns the total number of requests captured (lifetime, not just buffer).
func (ins *Inspector) Count() int64 {
	ins.mu.RLock()
	defer ins.mu.RUnlock()
	return ins.counter
}

// HandleList serves GET /api/inspect — returns all captured entries as JSON.
func (ins *Inspector) HandleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	limitStr := r.URL.Query().Get("limit")
	methodFilter := r.URL.Query().Get("method")
	statusFilter := r.URL.Query().Get("status")
	pathFilter := r.URL.Query().Get("path")

	entries := ins.List()

	var filtered []Entry
	for _, e := range entries {
		if methodFilter != "" && !strings.EqualFold(e.Method, methodFilter) {
			continue
		}
		if statusFilter != "" {
			if code, err := strconv.Atoi(statusFilter); err == nil && e.StatusCode != code {
				continue
			}
		}
		if pathFilter != "" && !strings.HasPrefix(e.Path, pathFilter) {
			continue
		}
		filtered = append(filtered, e)
	}
	entries = filtered

	if limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 && limit < len(entries) {
			entries = entries[len(entries)-limit:]
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"count":   len(entries),
		"total":   ins.Count(),
	})
}

// HandleGet serves GET /api/inspect/{id} — returns a single entry.
func (ins *Inspector) HandleGet(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	entry, ok := ins.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "entry not found"})
		return
	}

	writeJSON(w, http.StatusOK, entry)
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// HandleDiff serves GET /api/inspect/{id}/diff?other={otherID}
func (ins *Inspector) HandleDiff(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	entryA, okA := ins.Get(id)
	if !okA {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "first entry not found"})
		return
	}

	otherID := r.URL.Query().Get("other")
	if otherID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "other parameter is required"})
		return
	}

	entryB, okB := ins.Get(otherID)
	if !okB {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "second entry not found"})
		return
	}

	// Diff headers
	addedHeaders := []string{}
	removedHeaders := []string{}
	modifiedHeaders := make(map[string]map[string]string)

	for k, vA := range entryA.RequestHeaders {
		if vB, ok := entryB.RequestHeaders[k]; ok {
			if vA != vB {
				modifiedHeaders[k] = map[string]string{"from": vA, "to": vB}
			}
		} else {
			removedHeaders = append(removedHeaders, k)
		}
	}
	for k := range entryB.RequestHeaders {
		if _, ok := entryA.RequestHeaders[k]; !ok {
			addedHeaders = append(addedHeaders, k)
		}
	}

	// Diff bodies
	bodyABytes, _ := base64.StdEncoding.DecodeString(entryA.RequestBody)
	bodyBBytes, _ := base64.StdEncoding.DecodeString(entryB.RequestBody)
	bodyA := string(bodyABytes)
	bodyB := string(bodyBBytes)

	bodyDiff := ""
	if bodyA != bodyB {
		bodyDiff = fmt.Sprintf("Body sizes differ: %d vs %d bytes", len(bodyA), len(bodyB))
	}

	diffResult := map[string]interface{}{
		"headers": map[string]interface{}{
			"added":    addedHeaders,
			"removed":  removedHeaders,
			"modified": modifiedHeaders,
		},
		"body": map[string]interface{}{
			"diff":     bodyDiff,
			"len_diff": len(bodyB) - len(bodyA),
		},
	}

	writeJSON(w, http.StatusOK, diffResult)
}

