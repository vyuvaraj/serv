package proxy

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// HTTPCacheEntry holds a cached HTTP response with its expiration time.
type HTTPCacheEntry struct {
	Body       []byte
	StatusCode int
	Headers    http.Header
	ExpiresAt  time.Time
}

// ResponseCache is a TTL-based in-memory HTTP response cache.
// It is safe for concurrent use by multiple goroutines.
type ResponseCache struct {
	mu      sync.RWMutex
	entries map[string]*HTTPCacheEntry
	ttl     time.Duration
	stopCh  chan struct{}
}

// NewResponseCache creates a new cache with the given TTL duration.
// A background goroutine evicts expired entries every 30 seconds.
func NewResponseCache(ttl time.Duration) *ResponseCache {
	rc := &ResponseCache{
		entries: make(map[string]*HTTPCacheEntry),
		ttl:     ttl,
		stopCh:  make(chan struct{}),
	}

	// Background eviction goroutine
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rc.evictExpired()
			case <-rc.stopCh:
				return
			}
		}
	}()

	return rc
}

// Get retrieves a cached response by key. Returns nil, false if not found or expired.
func (rc *ResponseCache) Get(key string) (*HTTPCacheEntry, bool) {
	rc.mu.RLock()
	entry, ok := rc.entries[key]
	rc.mu.RUnlock()

	if !ok {
		return nil, false
	}

	if time.Now().After(entry.ExpiresAt) {
		// Lazily remove expired entry
		rc.mu.Lock()
		delete(rc.entries, key)
		rc.mu.Unlock()
		return nil, false
	}

	return entry, true
}

// Set stores a response in the cache with the configured TTL.
func (rc *ResponseCache) Set(key string, body []byte, statusCode int, headers http.Header) {
	entry := &HTTPCacheEntry{
		Body:       make([]byte, len(body)),
		StatusCode: statusCode,
		Headers:    cloneHeaders(headers),
		ExpiresAt:  time.Now().Add(rc.ttl),
	}
	copy(entry.Body, body)

	rc.mu.Lock()
	rc.entries[key] = entry
	rc.mu.Unlock()
}

// Invalidate removes all cache entries whose key starts with the given prefix.
// If prefix is empty, the entire cache is cleared.
func (rc *ResponseCache) Invalidate(prefix string) int {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	count := 0
	if prefix == "" {
		count = len(rc.entries)
		rc.entries = make(map[string]*HTTPCacheEntry)
		return count
	}

	for key := range rc.entries {
		if strings.HasPrefix(key, prefix) {
			delete(rc.entries, key)
			count++
		}
	}
	return count
}

// Size returns the current number of entries in the cache.
func (rc *ResponseCache) Size() int {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return len(rc.entries)
}

// Stop terminates the background eviction goroutine.
func (rc *ResponseCache) Stop() {
	close(rc.stopCh)
}

func (rc *ResponseCache) evictExpired() {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	now := time.Now()
	for key, entry := range rc.entries {
		if now.After(entry.ExpiresAt) {
			delete(rc.entries, key)
		}
	}
}

// CacheKey builds a deterministic cache key from the request method, path, and query parameters.
// Format: SHA256(METHOD:path?sorted_query_params)
func CacheKey(method, path, rawQuery string) string {
	var sb strings.Builder
	sb.WriteString(method)
	sb.WriteByte(':')
	sb.WriteString(path)

	if rawQuery != "" {
		// Sort query parameters for deterministic keys
		params := strings.Split(rawQuery, "&")
		sort.Strings(params)
		sb.WriteByte('?')
		sb.WriteString(strings.Join(params, "&"))
	}

	h := sha256.Sum256([]byte(sb.String()))
	return fmt.Sprintf("%x", h)
}

// IsCacheableMethod returns true if the HTTP method should be cached.
func IsCacheableMethod(method string, allowedMethods []string) bool {
	if len(allowedMethods) == 0 {
		// Default: only cache GET
		return method == http.MethodGet
	}
	for _, m := range allowedMethods {
		if strings.EqualFold(method, m) {
			return true
		}
	}
	return false
}

func cloneHeaders(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for k, vs := range src {
		dst[k] = append([]string{}, vs...)
	}
	return dst
}
