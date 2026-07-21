package circuitbreaker

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// State represents the current circuit breaker state.
type State int

const (
	// StateClosed allows requests through and monitors failure counts.
	StateClosed State = iota
	// StateOpen isolates downstream outages by fast-failing requests.
	StateOpen
	// StateHalfOpen allows limited trial requests to probe upstream recovery.
	StateHalfOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "Closed"
	case StateOpen:
		return "Open"
	case StateHalfOpen:
		return "Half-Open"
	default:
		return fmt.Sprintf("Unknown(%d)", s)
	}
}

// ErrCircuitOpen is returned when a request is rejected because the circuit is open.
var ErrCircuitOpen = errors.New("circuit breaker open: upstream outage isolated")

// CircuitBreaker is a thread-safe circuit breaker state machine.
type CircuitBreaker struct {
	mu                   sync.Mutex
	state                State
	failureThreshold     int
	resetTimeout         time.Duration
	maxHalfOpenRequests  int
	consecutiveFailures  int
	halfOpenSuccesses    int
	lastStateChange      time.Time
	onStateChangeHandler func(from, to State)
}

// Config defines the setup parameters for a CircuitBreaker.
type Config struct {
	// FailureThreshold is the consecutive failure count to trip the breaker from Closed to Open.
	// Default is 5.
	FailureThreshold int

	// ResetTimeout is the cooldown duration before transitioning from Open to Half-Open.
	// Default is 5 seconds.
	ResetTimeout time.Duration

	// MaxHalfOpenRequests is the number of successful trial requests required in Half-Open to close the circuit.
	// Default is 2.
	MaxHalfOpenRequests int

	// OnStateChange is an optional callback invoked when the state changes.
	OnStateChange func(from, to State)
}

// New creates a new CircuitBreaker with the given configuration.
func New(cfg Config) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = 5 * time.Second
	}
	if cfg.MaxHalfOpenRequests <= 0 {
		cfg.MaxHalfOpenRequests = 2
	}

	return &CircuitBreaker{
		state:                StateClosed,
		failureThreshold:     cfg.FailureThreshold,
		resetTimeout:         cfg.ResetTimeout,
		maxHalfOpenRequests:  cfg.MaxHalfOpenRequests,
		lastStateChange:      time.Now(),
		onStateChangeHandler: cfg.OnStateChange,
	}
}

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.checkStateTransition()
	return cb.state
}

// Allow checks whether a request is permitted to proceed.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.checkStateTransition()

	switch cb.state {
	case StateClosed:
		return true
	case StateOpen:
		return false
	case StateHalfOpen:
		return true
	default:
		return false
	}
}

// RecordSuccess records a successful execution and updates state accordingly.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFailures = 0

	if cb.state == StateHalfOpen {
		cb.halfOpenSuccesses++
		if cb.halfOpenSuccesses >= cb.maxHalfOpenRequests {
			cb.transitionTo(StateClosed)
		}
	}
}

// RecordFailure records an execution failure and trips the breaker if threshold is met.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFailures++

	switch cb.state {
	case StateClosed:
		if cb.consecutiveFailures >= cb.failureThreshold {
			cb.transitionTo(StateOpen)
		}
	case StateHalfOpen:
		cb.transitionTo(StateOpen)
	case StateOpen:
		// Already open
	}
}

// Reset resets the circuit breaker to the Closed state with zero failures.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFailures = 0
	cb.halfOpenSuccesses = 0
	cb.transitionTo(StateClosed)
}

// checkStateTransition shifts from Open to Half-Open if ResetTimeout has elapsed. Must be called with lock held.
func (cb *CircuitBreaker) checkStateTransition() {
	if cb.state == StateOpen {
		if time.Since(cb.lastStateChange) >= cb.resetTimeout {
			cb.transitionTo(StateHalfOpen)
		}
	}
}

// transitionTo performs a state shift and triggers the optional state change callback. Must be called with lock held.
func (cb *CircuitBreaker) transitionTo(newState State) {
	if cb.state == newState {
		return
	}
	oldState := cb.state
	cb.state = newState
	cb.lastStateChange = time.Now()

	if newState == StateClosed {
		cb.consecutiveFailures = 0
		cb.halfOpenSuccesses = 0
	} else if newState == StateHalfOpen {
		cb.halfOpenSuccesses = 0
	}

	if cb.onStateChangeHandler != nil {
		handler := cb.onStateChangeHandler
		go handler(oldState, newState)
	}
}

// Middleware returns an HTTP handler middleware that enforces circuit breaking.
func Middleware(cb *CircuitBreaker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cb.Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":"circuit breaker open: upstream outage isolated"}`))
				return
			}

			// Wrap response writer to capture status code
			rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
			next.ServeHTTP(rec, r)

			if rec.statusCode >= 500 {
				cb.RecordFailure()
			} else {
				cb.RecordSuccess()
			}
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}
