package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogEntry represents a structured access log record for a single proxied request.
type LogEntry struct {
	Timestamp    string `json:"timestamp"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	Route        string `json:"route"`
	ClientIP     string `json:"client_ip"`
	Status       int    `json:"status"`
	LatencyMs    int64  `json:"latency_ms"`
	RequestSize  int64  `json:"request_size"`
	ResponseSize int    `json:"response_size"`
	UserAgent    string `json:"user_agent"`
	TraceID      string `json:"trace_id,omitempty"`
	Target       string `json:"target"`
	Error        string `json:"error,omitempty"`
	Tenant       string `json:"tenant,omitempty"`
}

// AccessLogger writes structured JSONL access logs to a file.
type AccessLogger struct {
	mu     sync.Mutex
	file   *os.File
	writer *bufio.Writer
}

// NewAccessLogger creates a new AccessLogger writing to the specified path.
// It creates parent directories and opens the file in append mode.
func NewAccessLogger(path string) (*AccessLogger, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory %s: %w", dir, err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file %s: %w", path, err)
	}

	return &AccessLogger{
		file:   f,
		writer: bufio.NewWriterSize(f, 4096),
	}, nil
}

// Log writes a single LogEntry as a JSON line to the log file.
func (al *AccessLogger) Log(entry LogEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	al.mu.Lock()
	defer al.mu.Unlock()

	al.writer.Write(data)
	al.writer.WriteByte('\n')
	al.writer.Flush()
}

// Close flushes any buffered data and closes the underlying file.
func (al *AccessLogger) Close() error {
	al.mu.Lock()
	defer al.mu.Unlock()

	if err := al.writer.Flush(); err != nil {
		return err
	}
	return al.file.Close()
}

// StatusRecordingResponseWriter wraps http.ResponseWriter to capture the
// response status code and bytes written for access logging and caching.
type StatusRecordingResponseWriter struct {
	http.ResponseWriter
	StatusCode   int
	BytesWritten int
	body         []byte // captured response body (for caching)
	captureBody  bool
	wroteHeader  bool
}

// NewStatusRecordingResponseWriter creates a wrapper that records status and size.
// If captureBody is true, the full response body is also buffered in memory.
func NewStatusRecordingResponseWriter(w http.ResponseWriter, captureBody bool) *StatusRecordingResponseWriter {
	return &StatusRecordingResponseWriter{
		ResponseWriter: w,
		StatusCode:     http.StatusOK,
		captureBody:    captureBody,
	}
}

func (rw *StatusRecordingResponseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.StatusCode = code
		rw.wroteHeader = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *StatusRecordingResponseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.BytesWritten += n
	if rw.captureBody {
		rw.body = append(rw.body, b[:n]...)
	}
	return n, err
}

// Body returns the captured response body. Only valid if captureBody was true.
func (rw *StatusRecordingResponseWriter) Body() []byte {
	return rw.body
}

// CapturedHeaders returns a copy of the response headers at the time of reading.
func (rw *StatusRecordingResponseWriter) CapturedHeaders() http.Header {
	h := make(http.Header)
	for k, v := range rw.ResponseWriter.Header() {
		h[k] = append([]string{}, v...)
	}
	return h
}

// Flush implements http.Flusher if the underlying writer supports it.
func (rw *StatusRecordingResponseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// BuildLogEntry creates a LogEntry from the current request context and recorded response.
func BuildLogEntry(r *http.Request, rec *StatusRecordingResponseWriter, route, target, traceID string, start time.Time, errMsg string) LogEntry {
	tenant, _ := r.Context().Value("tenant").(string)
	return LogEntry{
		Timestamp:    start.UTC().Format(time.RFC3339),
		Method:       r.Method,
		Path:         r.URL.Path,
		Route:        route,
		ClientIP:     extractClientIP(r),
		Status:       rec.StatusCode,
		LatencyMs:    time.Since(start).Milliseconds(),
		RequestSize:  r.ContentLength,
		ResponseSize: rec.BytesWritten,
		UserAgent:    r.Header.Get("User-Agent"),
		TraceID:      traceID,
		Target:       target,
		Error:        errMsg,
		Tenant:       tenant,
	}
}

func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
