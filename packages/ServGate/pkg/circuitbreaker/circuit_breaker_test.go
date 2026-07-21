package circuitbreaker

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCircuitBreakerClosedAllow(t *testing.T) {
	cb := New(Config{FailureThreshold: 3, ResetTimeout: 100 * time.Millisecond})
	if cb.State() != StateClosed {
		t.Fatalf("expected initial state Closed, got %s", cb.State())
	}
	if !cb.Allow() {
		t.Fatalf("expected Allow() to be true in Closed state")
	}
}

func TestCircuitBreakerTripsToOpen(t *testing.T) {
	cb := New(Config{FailureThreshold: 3, ResetTimeout: 100 * time.Millisecond})

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != StateClosed {
		t.Fatalf("expected state Closed after 2 failures, got %s", cb.State())
	}

	cb.RecordFailure() // 3rd failure trips breaker
	if cb.State() != StateOpen {
		t.Fatalf("expected state Open after 3 failures, got %s", cb.State())
	}
	if cb.Allow() {
		t.Fatalf("expected Allow() to be false when Open")
	}
}

func TestCircuitBreakerCooldownToHalfOpen(t *testing.T) {
	cooldown := 50 * time.Millisecond
	cb := New(Config{FailureThreshold: 2, ResetTimeout: cooldown})

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Fatalf("expected state Open, got %s", cb.State())
	}

	// Wait for cooldown
	time.Sleep(cooldown + 10*time.Millisecond)

	if cb.State() != StateHalfOpen {
		t.Fatalf("expected state HalfOpen after cooldown, got %s", cb.State())
	}
	if !cb.Allow() {
		t.Fatalf("expected Allow() to be true in HalfOpen state")
	}
}

func TestCircuitBreakerHalfOpenSuccessRecovery(t *testing.T) {
	cooldown := 50 * time.Millisecond
	cb := New(Config{FailureThreshold: 2, ResetTimeout: cooldown, MaxHalfOpenRequests: 2})

	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(cooldown + 10*time.Millisecond)

	if cb.State() != StateHalfOpen {
		t.Fatalf("expected state HalfOpen, got %s", cb.State())
	}

	cb.RecordSuccess()
	if cb.State() != StateHalfOpen {
		t.Fatalf("expected state HalfOpen after 1 success, got %s", cb.State())
	}

	cb.RecordSuccess() // 2nd success closes breaker
	if cb.State() != StateClosed {
		t.Fatalf("expected state Closed after 2 successes, got %s", cb.State())
	}
}

func TestCircuitBreakerHalfOpenFailureReopens(t *testing.T) {
	cooldown := 50 * time.Millisecond
	cb := New(Config{FailureThreshold: 2, ResetTimeout: cooldown})

	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(cooldown + 10*time.Millisecond)

	if cb.State() != StateHalfOpen {
		t.Fatalf("expected state HalfOpen, got %s", cb.State())
	}

	cb.RecordFailure() // failure in HalfOpen immediately re-opens breaker
	if cb.State() != StateOpen {
		t.Fatalf("expected state Open after failure in HalfOpen, got %s", cb.State())
	}
}

func TestCircuitBreakerMiddleware(t *testing.T) {
	cb := New(Config{FailureThreshold: 2, ResetTimeout: 100 * time.Millisecond})

	failHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	handler := Middleware(cb)(failHandler)

	// Attempt 1: 500 error recorded
	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}

	// Attempt 2: 500 error recorded -> trips breaker
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}

	// Attempt 3: Breaker is Open -> fast fail 503
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 Service Unavailable, got %d", rec.Code)
	}
}
