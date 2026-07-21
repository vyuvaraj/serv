package ratelimit

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// SlidingWindowLimiter implements a thread-safe sliding window rate limiter.
type SlidingWindowLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	requests map[string][]time.Time
}

// NewSlidingWindowLimiter creates a new sliding window rate limiter with the given limit and duration.
func NewSlidingWindowLimiter(limit int, window time.Duration) *SlidingWindowLimiter {
	if limit <= 0 {
		limit = 100
	}
	if window <= 0 {
		window = 1 * time.Minute
	}

	l := &SlidingWindowLimiter{
		limit:    limit,
		window:   window,
		requests: make(map[string][]time.Time),
	}

	// Periodically clean up stale client entries in background
	go l.startCleanupLoop()

	return l
}

// Allow checks if a request for the given key is allowed under the rate limit.
// Returns allowed (bool), remaining requests (int), and time until reset (time.Duration).
func (l *SlidingWindowLimiter) Allow(key string) (bool, int, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.window)

	timestamps := l.requests[key]
	validIndex := 0

	// Prune timestamps older than cutoff
	for i, t := range timestamps {
		if t.After(cutoff) {
			validIndex = i
			break
		}
		if i == len(timestamps)-1 {
			validIndex = len(timestamps)
		}
	}

	if validIndex > 0 && validIndex <= len(timestamps) {
		timestamps = timestamps[validIndex:]
	}

	if len(timestamps) >= l.limit {
		// Calculate time until earliest timestamp expires
		resetAfter := timestamps[0].Add(l.window).Sub(now)
		if resetAfter < 0 {
			resetAfter = 0
		}
		l.requests[key] = timestamps
		return false, 0, resetAfter
	}

	// Append current timestamp
	timestamps = append(timestamps, now)
	l.requests[key] = timestamps

	remaining := l.limit - len(timestamps)
	resetAfter := l.window
	return true, remaining, resetAfter
}

// startCleanupLoop runs periodically to purge empty or stale key entries.
func (l *SlidingWindowLimiter) startCleanupLoop() {
	ticker := time.NewTicker(l.window)
	defer ticker.Stop()

	for now := range ticker.C {
		l.mu.Lock()
		cutoff := now.Add(-l.window)
		for k, timestamps := range l.requests {
			validCount := 0
			for _, t := range timestamps {
				if t.After(cutoff) {
					validCount++
				}
			}
			if validCount == 0 {
				delete(l.requests, k)
			}
		}
		l.mu.Unlock()
	}
}

// DefaultKeyFunc extracts the client IP address from the request.
func DefaultKeyFunc(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// Middleware creates an HTTP handler middleware enforcing sliding-window rate limits.
func Middleware(limiter *SlidingWindowLimiter, keyFunc func(r *http.Request) string) func(http.Handler) http.Handler {
	if keyFunc == nil {
		keyFunc = DefaultKeyFunc
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			allowed, remaining, resetAfter := limiter.Allow(key)

			resetSec := int(resetAfter.Seconds())
			if resetSec <= 0 {
				resetSec = 1
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limiter.limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("X-RateLimit-Reset", strconv.Itoa(resetSec))

			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(resetSec))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(fmt.Sprintf(`{"error":"rate limit exceeded: too many requests","retry_after_seconds":%d}`, resetSec)))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
