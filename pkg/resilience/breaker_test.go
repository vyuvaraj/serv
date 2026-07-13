package resilience

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreakerTransitions(t *testing.T) {
	// Threshold: 3 failures, Cooldown: 100ms
	cb := NewCircuitBreaker(3, 100*time.Millisecond)

	// 1. Initial state must be Closed
	if cb.State() != Closed {
		t.Errorf("expected Closed state, got %v", cb.State().String())
	}
	if err := cb.Allow(); err != nil {
		t.Errorf("expected requests allowed, got: %v", err)
	}

	// 2. Failure 1
	cb.Failure()
	if cb.State() != Closed {
		t.Errorf("expected Closed state after 1 failure, got %v", cb.State().String())
	}

	// 3. Failure 2
	cb.Failure()
	if cb.State() != Closed {
		t.Errorf("expected Closed state after 2 failures, got %v", cb.State().String())
	}

	// 4. Failure 3 (Tripping Threshold)
	cb.Failure()
	if cb.State() != Open {
		t.Errorf("expected Open state after 3 failures, got %v", cb.State().String())
	}
	if err := cb.Allow(); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("expected ErrCircuitOpen error, got: %v", err)
	}

	// 5. Cooldown period elapsed -> HalfOpen
	time.Sleep(120 * time.Millisecond)
	if err := cb.Allow(); err != nil {
		t.Errorf("expected request allowed in Half-Open state, got: %v", err)
	}
	if cb.State() != HalfOpen {
		t.Errorf("expected Half-Open state, got %v", cb.State().String())
	}

	// 6. Success in HalfOpen -> transitions back to Closed
	cb.Success()
	if cb.State() != Closed {
		t.Errorf("expected Closed state after success in Half-Open, got %v", cb.State().String())
	}

	// 7. Test HalfOpen -> Failure -> Open transition
	cb.Failure() // Fail 1
	cb.Failure() // Fail 2
	cb.Failure() // Fail 3 -> Open
	if cb.State() != Open {
		t.Fatalf("expected Open state, got %v", cb.State().String())
	}

	time.Sleep(120 * time.Millisecond)
	_ = cb.Allow() // transition to Half-Open
	cb.Failure()   // immediate failure in Half-Open -> Open
	if cb.State() != Open {
		t.Errorf("expected Open state after failure in Half-Open, got %v", cb.State().String())
	}
}
