package queue

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"
)

// QueuedEmail represents an outgoing email entry persisted to disk.
type QueuedEmail struct {
	ID        string                 `json:"id"`
	Channel   string                 `json:"channel"`
	Target    string                 `json:"target"`
	Body      string                 `json:"body"`
	Context   map[string]interface{} `json:"context,omitempty"`
	QueuedAt  time.Time              `json:"queued_at"`
	Attempts  int                    `json:"attempts"`
	LastError string                 `json:"last_error,omitempty"`
	Status    string                 `json:"status"` // pending, sent, failed
}

// DiskQueue persists mail entries to a JSONL (newline-delimited JSON) file,
// preventing message loss during server restarts or transient delivery failures.
type DiskQueue struct {
	mu       sync.Mutex
	filePath string
	entries  []*QueuedEmail
}

// NewDiskQueue loads the existing queue from disk (creating the file if absent)
// and returns a ready-to-use DiskQueue.
func NewDiskQueue(filePath string) *DiskQueue {
	q := &DiskQueue{filePath: filePath}
	q.load()
	return q
}

// Enqueue appends a new mail entry to the in-memory list and flushes to disk.
func (q *DiskQueue) Enqueue(email *QueuedEmail) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.entries = append(q.entries, email)
	q.flush()
}

// MarkSent marks an entry as successfully sent and flushes.
func (q *DiskQueue) MarkSent(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range q.entries {
		if e.ID == id {
			e.Status = "sent"
			break
		}
	}
	q.flush()
}

// MarkFailed increments attempt count, records the error, and persists.
func (q *DiskQueue) MarkFailed(id string, errStr string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range q.entries {
		if e.ID == id {
			e.Attempts++
			e.LastError = errStr
			e.Status = "failed"
			break
		}
	}
	q.flush()
}

// PendingEntries returns a copy of all entries with status "pending".
func (q *DiskQueue) PendingEntries() []*QueuedEmail {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []*QueuedEmail
	for _, e := range q.entries {
		if e.Status == "pending" {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out
}

// EnforceRetention cleans up non-pending entries that are older than ageLimit.
func (q *DiskQueue) EnforceRetention(ageLimit time.Duration) {
	q.mu.Lock()
	defer  q.mu.Unlock()

	now := time.Now()
	var active []*QueuedEmail
	removed := 0
	for _, e := range q.entries {
		if e.Status == "pending" || now.Sub(e.QueuedAt) <= ageLimit {
			active = append(active, e)
		} else {
			removed++
		}
	}
	if removed > 0 {
		q.entries = active
		q.flush()
		log.Printf("[MAIL DISK QUEUE] Purged %d expired mail entries from queue due to retention settings", removed)
	}
}

// Size returns the total number of entries in the queue.
func (q *DiskQueue) Size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries)
}

// load reads existing JSONL entries from disk into memory.
func (q *DiskQueue) load() {
	data, err := os.ReadFile(q.filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[MAIL DISK QUEUE] Failed to read queue file %s: %v", q.filePath, err)
		}
		return
	}

	lines := splitLines(data)
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var e QueuedEmail
		if err := json.Unmarshal(line, &e); err != nil {
			log.Printf("[MAIL DISK QUEUE] Skipping corrupt entry: %v", err)
			continue
		}
		q.entries = append(q.entries, &e)
	}
	log.Printf("[MAIL DISK QUEUE] Loaded %d queued mail entries from %s", len(q.entries), q.filePath)
}

// flush rewrites the entire queue to disk in JSONL format.
// Must be called with q.mu held.
func (q *DiskQueue) flush() {
	f, err := os.OpenFile(q.filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		log.Printf("[MAIL DISK QUEUE] Failed to open queue file for flush: %v", err)
		return
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, e := range q.entries {
		if err := enc.Encode(e); err != nil {
			log.Printf("[MAIL DISK QUEUE] Failed to encode entry %s: %v", e.ID, err)
		}
	}
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
