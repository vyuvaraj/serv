package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetrySuccessFirstAttempt(t *testing.T) {
	attempts := 0
	err := Do(context.Background(), DefaultConfig(), func(ctx context.Context) error {
		attempts++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}
}

func TestRetrySuccessAfterRetries(t *testing.T) {
	attempts := 0
	cfg := Config{
		MaxAttempts:     4,
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     20 * time.Millisecond,
		Multiplier:      2.0,
		Jitter:          false,
	}

	err := Do(context.Background(), cfg, func(ctx context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("transient error")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error after retries, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryMaxAttemptsExceeded(t *testing.T) {
	attempts := 0
	targetErr := errors.New("persistent failure")
	cfg := Config{
		MaxAttempts:     3,
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     20 * time.Millisecond,
		Multiplier:      2.0,
		Jitter:          false,
	}

	err := Do(context.Background(), cfg, func(ctx context.Context) error {
		attempts++
		return targetErr
	})
	if !errors.Is(err, targetErr) {
		t.Fatalf("expected targetErr %v, got %v", targetErr, err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryNonRetryableError(t *testing.T) {
	attempts := 0
	fatalErr := errors.New("fatal error")
	cfg := Config{
		MaxAttempts:     5,
		InitialInterval: 5 * time.Millisecond,
		IsRetryable: func(err error) bool {
			return !errors.Is(err, fatalErr)
		},
	}

	err := Do(context.Background(), cfg, func(ctx context.Context) error {
		attempts++
		return fatalErr
	})
	if !errors.Is(err, fatalErr) {
		t.Fatalf("expected fatalErr, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt for non-retryable error, got %d", attempts)
	}
}

func TestRetryContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	attempts := 0
	err := Do(ctx, DefaultConfig(), func(c context.Context) error {
		attempts++
		return errors.New("should not run")
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if attempts != 0 {
		t.Errorf("expected 0 attempts, got %d", attempts)
	}
}

func TestRetryDoWithData(t *testing.T) {
	attempts := 0
	cfg := Config{
		MaxAttempts:     3,
		InitialInterval: 5 * time.Millisecond,
	}

	val, err := DoWithData(context.Background(), cfg, func(ctx context.Context) (string, error) {
		attempts++
		if attempts < 2 {
			return "", errors.New("try again")
		}
		return "hello world", nil
	})

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if val != "hello world" {
		t.Errorf("expected 'hello world', got %q", val)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}
