package resilience

import (
	"errors"
	"sync"
	"time"
)

type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "Closed"
	case Open:
		return "Open"
	case HalfOpen:
		return "Half-Open"
	default:
		return "Unknown"
	}
}

var ErrCircuitOpen = errors.New("circuit breaker is open")

type CircuitBreaker struct {
	mu           sync.Mutex
	state        State
	failures     int
	threshold    int
	cooldown     time.Duration
	lastStateChg time.Time
}

func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:        Closed,
		threshold:    threshold,
		cooldown:     cooldown,
		lastStateChg: time.Now(),
	}
}

func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == Open {
		if time.Since(cb.lastStateChg) > cb.cooldown {
			cb.state = HalfOpen
			cb.lastStateChg = time.Now()
			return nil
		}
		return ErrCircuitOpen
	}

	return nil
}

func (cb *CircuitBreaker) Success() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == HalfOpen || cb.failures > 0 {
		cb.state = Closed
		cb.failures = 0
		cb.lastStateChg = time.Now()
	}
}

func (cb *CircuitBreaker) Failure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	if cb.state == Closed && cb.failures >= cb.threshold {
		cb.state = Open
		cb.lastStateChg = time.Now()
	} else if cb.state == HalfOpen {
		cb.state = Open
		cb.lastStateChg = time.Now()
	}
}

func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}
