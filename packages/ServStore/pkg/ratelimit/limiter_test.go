package ratelimit

import (
	"testing"
	"time"
)

func TestAllow_WithinLimit(t *testing.T) {
	l := NewLimiter(10, 10) // 10 RPS, burst 10
	// First 10 requests must all pass (burst is full at start)
	for i := 0; i < 10; i++ {
		if !l.Allow("tenant-a") {
			t.Errorf("request %d should have been allowed", i+1)
		}
	}
}

func TestAllow_ExceedsLimit(t *testing.T) {
	l := NewLimiter(1, 1) // 1 RPS, burst 1
	// First request uses the single burst token
	if !l.Allow("tenant-b") {
		t.Fatal("first request should be allowed")
	}
	// Second immediate request must be rejected
	if l.Allow("tenant-b") {
		t.Error("second immediate request should be rate-limited")
	}
}

func TestAllow_TokenRefill(t *testing.T) {
	l := NewLimiter(100, 100) // 100 RPS
	// Drain the bucket
	for i := 0; i < 100; i++ {
		l.Allow("tenant-c")
	}
	// Wait for tokens to refill (10ms = 1 token at 100 RPS)
	time.Sleep(20 * time.Millisecond)
	if !l.Allow("tenant-c") {
		t.Error("expected token to refill after sleep")
	}
}

func TestAllow_Isolation(t *testing.T) {
	l := NewLimiter(1, 1)
	// Drain tenant-x
	l.Allow("tenant-x")
	l.Allow("tenant-x") // second should fail, but we don't check it here

	// tenant-y is independent — its bucket should still be full
	if !l.Allow("tenant-y") {
		t.Error("tenant-y bucket should not be affected by tenant-x")
	}
}

func TestSetLimit(t *testing.T) {
	l := NewLimiter(1, 1)
	// Pre-create bucket for tenant-d
	l.Allow("tenant-d")

	// Increase burst to 5
	l.SetLimit("tenant-d", 5, 5)
	// Bucket tokens are reset to min(existing, new burst); we need to wait for refill
	time.Sleep(1100 * time.Millisecond) // 1.1s × 5 RPS ≈ 5 tokens
	allowed := 0
	for i := 0; i < 5; i++ {
		if l.Allow("tenant-d") {
			allowed++
		}
	}
	if allowed == 0 {
		t.Error("expected at least one request to be allowed after SetLimit and refill")
	}
}

func TestRetryAfterSec(t *testing.T) {
	l := NewLimiter(2, 2)
	l.Allow("tenant-e")
	l.Allow("tenant-e")
	// Bucket is now empty; RetryAfterSec should return ≥1
	sec := l.RetryAfterSec("tenant-e")
	if sec < 1 {
		t.Errorf("RetryAfterSec should return ≥1, got %d", sec)
	}
}

func TestDelete(t *testing.T) {
	l := NewLimiter(1, 1)
	l.Allow("tenant-f") // drain
	l.Delete("tenant-f")
	// After delete, bucket is recreated fresh — first request must succeed
	if !l.Allow("tenant-f") {
		t.Error("bucket after Delete should start fresh and allow first request")
	}
}

func BenchmarkAllow(b *testing.B) {
	l := NewLimiter(1_000_000, 1_000_000)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Allow("bench-tenant")
		}
	})
}
