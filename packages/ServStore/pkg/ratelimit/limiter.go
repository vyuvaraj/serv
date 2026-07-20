// Package ratelimit provides a per-tenant token bucket rate limiter.
// It is entirely self-contained — no external dependencies — using only
// sync.Map and the standard time package.
//
// Design:
//   - Each unique tenantID gets its own token bucket.
//   - Tokens refill continuously at the configured rate (RPS).
//   - Burst absorbs short spikes up to BurstSize requests.
//   - Allow is O(1) amortised and goroutine-safe.
package ratelimit

import (
	"sync"
	"time"
)

// bucket is the per-tenant state.
type bucket struct {
	mu         sync.Mutex
	tokens     float64
	lastRefill time.Time
	rps        float64 // tokens added per second
	burst      float64 // maximum token accumulation
}

// refill adds tokens proportional to elapsed time since last call.
// Must be called with b.mu held.
func (b *bucket) refill(now time.Time) {
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens += elapsed * b.rps
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.lastRefill = now
}

// allow consumes one token and returns true, or returns false if the bucket
// is empty (rate limit exceeded).
func (b *bucket) allow() bool {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill(now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// retryAfterMs returns the milliseconds until the next token is available.
func (b *bucket) retryAfterMs() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rps <= 0 {
		return 1000
	}
	ms := int64((1.0 / b.rps) * 1000)
	if ms < 1 {
		ms = 1
	}
	return ms
}

// Limiter manages per-tenant token buckets.
type Limiter struct {
	defaultRPS   float64
	defaultBurst float64
	buckets      sync.Map // tenantID string → *bucket
}

// NewLimiter creates a Limiter with the given default rate and burst for all tenants.
// rps is the steady-state requests per second. burst is the maximum spike size.
func NewLimiter(rps, burst int) *Limiter {
	return &Limiter{
		defaultRPS:   float64(rps),
		defaultBurst: float64(burst),
	}
}

// getOrCreate returns the existing bucket for tenantID or creates one.
func (l *Limiter) getOrCreate(tenantID string) *bucket {
	if v, ok := l.buckets.Load(tenantID); ok {
		return v.(*bucket)
	}
	b := &bucket{
		tokens:     l.defaultBurst, // start full
		lastRefill: time.Now(),
		rps:        l.defaultRPS,
		burst:      l.defaultBurst,
	}
	actual, _ := l.buckets.LoadOrStore(tenantID, b)
	return actual.(*bucket)
}

// Allow reports whether a request from tenantID is within rate limits.
// Returns true if the request should proceed, false if it should be rejected.
func (l *Limiter) Allow(tenantID string) bool {
	return l.getOrCreate(tenantID).allow()
}

// RetryAfterSec returns the number of whole seconds until the next token
// is available for tenantID. Useful for the Retry-After HTTP header.
func (l *Limiter) RetryAfterSec(tenantID string) int64 {
	ms := l.getOrCreate(tenantID).retryAfterMs()
	secs := ms / 1000
	if secs < 1 {
		secs = 1
	}
	return secs
}

// SetLimit dynamically overrides the rate and burst for a specific tenant.
// Useful for per-namespace QoS in the Kubernetes Operator.
func (l *Limiter) SetLimit(tenantID string, rps, burst int) {
	b := l.getOrCreate(tenantID)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rps = float64(rps)
	b.burst = float64(burst)
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
}

// Delete removes the bucket for tenantID (e.g. when a namespace is deleted).
func (l *Limiter) Delete(tenantID string) {
	l.buckets.Delete(tenantID)
}
