package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSlidingWindowAllow(t *testing.T) {
	limiter := NewSlidingWindowLimiter(3, 100*time.Millisecond)

	allowed, remaining, _ := limiter.Allow("client-1")
	if !allowed || remaining != 2 {
		t.Errorf("expected allowed=true, remaining=2; got allowed=%v, remaining=%d", allowed, remaining)
	}

	allowed, remaining, _ = limiter.Allow("client-1")
	if !allowed || remaining != 1 {
		t.Errorf("expected allowed=true, remaining=1; got allowed=%v, remaining=%d", allowed, remaining)
	}

	allowed, remaining, _ = limiter.Allow("client-1")
	if !allowed || remaining != 0 {
		t.Errorf("expected allowed=true, remaining=0; got allowed=%v, remaining=%d", allowed, remaining)
	}
}

func TestSlidingWindowExceedLimit(t *testing.T) {
	limiter := NewSlidingWindowLimiter(2, 100*time.Millisecond)

	limiter.Allow("client-2")
	limiter.Allow("client-2")

	// 3rd attempt should be rejected
	allowed, remaining, resetAfter := limiter.Allow("client-2")
	if allowed {
		t.Errorf("expected allowed=false when limit exceeded")
	}
	if remaining != 0 {
		t.Errorf("expected remaining=0, got %d", remaining)
	}
	if resetAfter <= 0 {
		t.Errorf("expected positive resetAfter, got %v", resetAfter)
	}
}

func TestSlidingWindowWindowExpiry(t *testing.T) {
	window := 50 * time.Millisecond
	limiter := NewSlidingWindowLimiter(1, window)

	allowed, _, _ := limiter.Allow("client-3")
	if !allowed {
		t.Fatal("expected 1st request allowed")
	}

	allowed, _, _ = limiter.Allow("client-3")
	if allowed {
		t.Fatal("expected 2nd request blocked")
	}

	// Wait for window to expire
	time.Sleep(window + 10*time.Millisecond)

	allowed, _, _ = limiter.Allow("client-3")
	if !allowed {
		t.Fatal("expected request allowed after window expiry")
	}
}

func TestSlidingWindowMiddleware(t *testing.T) {
	limiter := NewSlidingWindowLimiter(2, 100*time.Millisecond)
	handler := Middleware(limiter, func(r *http.Request) string { return "test-ip" })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Request 1: OK
	req := httptest.NewRequest("GET", "/api/data", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}
	if rec.Header().Get("X-RateLimit-Limit") != "2" {
		t.Errorf("expected X-RateLimit-Limit=2, got %s", rec.Header().Get("X-RateLimit-Limit"))
	}

	// Request 2: OK
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rec.Code)
	}

	// Request 3: 429 Too Many Requests
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 Too Many Requests, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Errorf("expected Retry-After header to be set")
	}
}
