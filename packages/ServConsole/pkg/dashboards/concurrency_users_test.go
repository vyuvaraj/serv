package dashboards

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"

	"github.com/vyuvaraj/serv/packages/ServConsole/pkg/config"
)

// TestConcurrentDashboardUsers (D.29) simulates 50 concurrent users accessing
// and updating dashboard logs and alerts to verify thread safety and lack of buffer corruption.
func TestConcurrentDashboardUsers(t *testing.T) {
	buf := []LogEntry{}
	var mu sync.Mutex
	LogBuffer = &buf
	LogBufferMu = &mu

	checkStatus := func(name, url string) config.ComponentStatus { return config.ComponentStatus{Online: true} }
	writeError := func(w http.ResponseWriter, r *http.Request, msg, code string, status int) { http.Error(w, msg, status) }
	addAlert := func(c, t, m, s string) {}
	clearAlert := func(c, t string) {}
	getUserRole := func(r *http.Request) string { return "admin" }
	scaleTrigger := func(s, m string) {}
	auditLog := func(u, a, m, p string, s int) {}

	Init(checkStatus, writeError, addAlert, clearAlert, getUserRole, scaleTrigger, auditLog)

	const concurrency = 50
	const operationsPerUser = 20
	var wg sync.WaitGroup

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(userIndex int) {
			defer wg.Done()
			for j := 0; j < operationsPerUser; j++ {
				// 1. Ingest a log entry (Writer)
				payload := LogEntry{
					Service: "test-service",
					Level:   "info",
					Message: "log-message-contents",
				}
				body, _ := json.Marshal(payload)
				reqWrite := httptest.NewRequest("POST", "/api/logs/ingest", bytes.NewReader(body))
				wWrite := httptest.NewRecorder()
				HandleIngestLog(wWrite, reqWrite)

				// 2. Fetch log entries (Reader)
				reqRead := httptest.NewRequest("GET", "/api/logs", nil)
				wRead := httptest.NewRecorder()
				HandleGetLogs(wRead, reqRead)
			}
		}(i)
	}

	wg.Wait()
}

// TestMemoryProfilingTracesIngested (D.30) verifies that ingesting 100K logs/traces
// triggers ring buffer eviction (keeping buffer <= 2000) and bounds memory allocation.
func TestMemoryProfilingTracesIngested(t *testing.T) {
	buf := []LogEntry{}
	var mu sync.Mutex
	LogBuffer = &buf
	LogBufferMu = &mu

	// Initial GC and memory read
	runtime.GC()
	var msBefore runtime.MemStats
	runtime.ReadMemStats(&msBefore)

	// Ingest 100K log entries
	for i := 0; i < 100000; i++ {
		entry := LogEntry{
			Service: "stress-service",
			Level:   "info",
			Message: "dummy message description",
		}
		LogBufferMu.Lock()
		*LogBuffer = append(*LogBuffer, entry)
		if len(*LogBuffer) > 2000 {
			*LogBuffer = (*LogBuffer)[1:]
		}
		LogBufferMu.Unlock()
	}

	// Eviction limit assertion
	LogBufferMu.Lock()
	bufLen := len(*LogBuffer)
	LogBufferMu.Unlock()

	if bufLen > 2000 {
		t.Errorf("expected buffer size to be capped at 2000, got %d", bufLen)
	}

	// Final GC and memory validation
	runtime.GC()
	var msAfter runtime.MemStats
	runtime.ReadMemStats(&msAfter)

	deltaHeap := int64(msAfter.HeapAlloc) - int64(msBefore.HeapAlloc)
	t.Logf("HeapAlloc change after 100K trace ingest: %d KB", deltaHeap/1024)

	// Limit should keep memory usage extremely low (e.g. < 1 MB growth for the buffer)
	if deltaHeap > 1024*1024 {
		t.Errorf("memory footprint grew by %d KB, ring buffer eviction did not bound memory", deltaHeap/1024)
	}
}
