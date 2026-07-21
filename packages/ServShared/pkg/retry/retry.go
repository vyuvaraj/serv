package retry

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"time"
)

// Config defines the retry policy configuration.
type Config struct {
	// MaxAttempts is the maximum number of execution attempts (including the initial run).
	// Default is 3.
	MaxAttempts int

	// InitialInterval is the duration of the first backoff delay.
	// Default is 100ms.
	InitialInterval time.Duration

	// MaxInterval is the upper bound on backoff delay between attempts.
	// Default is 2 seconds.
	MaxInterval time.Duration

	// Multiplier is the backoff growth factor.
	// Default is 2.0.
	Multiplier float64

	// Jitter enables randomized backoff noise (0-25% variation).
	// Default is true.
	Jitter bool

	// IsRetryable is an optional filter function to determine if an error can be retried.
	// If nil, all non-nil errors are considered retryable.
	IsRetryable func(err error) bool
}

// DefaultConfig returns reasonable default settings for exponential retries.
func DefaultConfig() Config {
	return Config{
		MaxAttempts:     3,
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     2 * time.Second,
		Multiplier:      2.0,
		Jitter:          true,
	}
}

// Do executes fn with exponential backoff until it succeeds, exceeds max attempts, or context is cancelled.
func Do(ctx context.Context, cfg Config, fn func(ctx context.Context) error) error {
	_, err := DoWithData(ctx, cfg, func(c context.Context) (struct{}, error) {
		return struct{}{}, fn(c)
	})
	return err
}

// DoWithData executes fn returning a data result with exponential backoff until success or limits reached.
func DoWithData[T any](ctx context.Context, cfg Config, fn func(ctx context.Context) (T, error)) (T, error) {
	var zero T

	// Apply defaults for unconfigured parameters
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.InitialInterval <= 0 {
		cfg.InitialInterval = 100 * time.Millisecond
	}
	if cfg.MaxInterval <= 0 {
		cfg.MaxInterval = 2 * time.Second
	}
	if cfg.Multiplier <= 0 {
		cfg.Multiplier = 2.0
	}

	interval := cfg.InitialInterval

	for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}

		res, err := fn(ctx)
		if err == nil {
			return res, nil
		}

		// If this is the last attempt or the error is unretryable, return immediately.
		if attempt == cfg.MaxAttempts {
			return zero, err
		}
		if cfg.IsRetryable != nil && !cfg.IsRetryable(err) {
			return zero, err
		}

		// Calculate sleep duration with optional jitter
		sleepDur := interval
		if cfg.Jitter {
			jitterDelta := time.Duration(rand.Float64() * 0.25 * float64(interval))
			sleepDur += jitterDelta
		}

		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(sleepDur):
		}

		// Increase interval for next attempt
		nextInterval := float64(interval) * cfg.Multiplier
		if nextInterval > float64(cfg.MaxInterval) {
			interval = cfg.MaxInterval
		} else {
			interval = time.Duration(math.Min(nextInterval, float64(cfg.MaxInterval)))
		}
	}

	return zero, errors.New("retry limit reached")
}
