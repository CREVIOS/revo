package retry

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// Common error types for retry logic
var (
	ErrRateLimited   = errors.New("rate limited (429)")
	ErrServerError   = errors.New("server error (5xx)")
	ErrMaxRetries    = errors.New("maximum retries exceeded")
	ErrNonRetryable  = errors.New("non-retryable error")
)

// Config holds retry configuration
type Config struct {
	MaxRetries      int           // Maximum number of retry attempts
	InitialDelay    time.Duration // Initial delay before first retry
	MaxDelay        time.Duration // Maximum delay between retries
	Multiplier      float64       // Multiplier for exponential backoff
	JitterFraction  float64       // Fraction of delay to use as jitter (0.0-1.0)
}

// DefaultConfig returns sensible defaults for production use
func DefaultConfig() Config {
	return Config{
		MaxRetries:     5,
		InitialDelay:   1 * time.Second,
		MaxDelay:       60 * time.Second,
		Multiplier:     2.0,
		JitterFraction: 0.3, // 30% jitter
	}
}

// Retrier implements exponential backoff with jitter
type Retrier struct {
	config Config
	rng    *rand.Rand
}

// New creates a new Retrier with the given configuration
func New(config Config) *Retrier {
	return &Retrier{
		config: config,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NewWithDefaults creates a Retrier with default configuration
func NewWithDefaults() *Retrier {
	return New(DefaultConfig())
}

// RetryableFunc is a function that can be retried
type RetryableFunc func(ctx context.Context) error

// Do executes the function with retries using exponential backoff with jitter
func (r *Retrier) Do(ctx context.Context, fn RetryableFunc) error {
	var lastErr error

	for attempt := 0; attempt <= r.config.MaxRetries; attempt++ {
		// Execute the function
		err := fn(ctx)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !r.isRetryable(err) {
			log.Debug().
				Err(err).
				Int("attempt", attempt+1).
				Msg("Non-retryable error, stopping")
			return err
		}

		// Don't wait after the last attempt
		if attempt == r.config.MaxRetries {
			break
		}

		// Calculate delay with exponential backoff
		delay := r.calculateDelay(attempt, err)

		log.Warn().
			Err(err).
			Int("attempt", attempt+1).
			Int("max_retries", r.config.MaxRetries).
			Dur("delay", delay).
			Msg("Retrying after error")

		// Wait with context cancellation support
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	return errors.Join(ErrMaxRetries, lastErr)
}

// isRetryable determines if an error should be retried
func (r *Retrier) isRetryable(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Rate limit errors (429) - always retry
	if strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "too many requests") ||
		errors.Is(err, ErrRateLimited) {
		return true
	}

	// Server errors (5xx) - retry
	if strings.Contains(errStr, "500") ||
		strings.Contains(errStr, "502") ||
		strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "504") ||
		strings.Contains(errStr, "server error") ||
		errors.Is(err, ErrServerError) {
		return true
	}

	// Timeout errors - retry
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") ||
		errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Connection errors - retry
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "network is unreachable") {
		return true
	}

	// Claude CLI specific errors that are retryable
	if strings.Contains(errStr, "overloaded") ||
		strings.Contains(errStr, "capacity") {
		return true
	}

	return false
}

// calculateDelay computes the delay for a given attempt with jitter
func (r *Retrier) calculateDelay(attempt int, err error) time.Duration {
	// Check for retry-after hint in error message
	if retryAfter := r.extractRetryAfter(err); retryAfter > 0 {
		// Add small jitter to retry-after to prevent thundering herd
		jitter := time.Duration(r.rng.Float64() * float64(retryAfter) * 0.1)
		return retryAfter + jitter
	}

	// Exponential backoff: initialDelay * multiplier^attempt
	delay := float64(r.config.InitialDelay) * math.Pow(r.config.Multiplier, float64(attempt))

	// Cap at maximum delay
	if delay > float64(r.config.MaxDelay) {
		delay = float64(r.config.MaxDelay)
	}

	// Add jitter: delay ± (jitterFraction * delay)
	jitterRange := delay * r.config.JitterFraction
	jitter := (r.rng.Float64() * 2 * jitterRange) - jitterRange
	delay += jitter

	// Ensure minimum delay of 100ms
	if delay < float64(100*time.Millisecond) {
		delay = float64(100 * time.Millisecond)
	}

	return time.Duration(delay)
}

// extractRetryAfter attempts to extract a retry-after duration from error
func (r *Retrier) extractRetryAfter(err error) time.Duration {
	if err == nil {
		return 0
	}

	errStr := err.Error()

	// Look for patterns like "retry after 30s" or "retry-after: 30"
	patterns := []string{
		"retry after ",
		"retry-after: ",
		"retry-after:",
		"wait ",
	}

	for _, pattern := range patterns {
		if idx := strings.Index(strings.ToLower(errStr), pattern); idx != -1 {
			remaining := errStr[idx+len(pattern):]
			// Try to parse duration
			if d, err := time.ParseDuration(strings.Fields(remaining)[0]); err == nil {
				return d
			}
			// Try to parse as seconds
			var seconds int
			if _, err := parseFirstInt(remaining, &seconds); err == nil && seconds > 0 {
				return time.Duration(seconds) * time.Second
			}
		}
	}

	return 0
}

// parseFirstInt extracts the first integer from a string
func parseFirstInt(s string, result *int) (string, error) {
	s = strings.TrimSpace(s)
	var num int
	var i int
	for i = 0; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		num = num*10 + int(s[i]-'0')
	}
	if i == 0 {
		return s, errors.New("no integer found")
	}
	*result = num
	return s[i:], nil
}

// WithRetryAfter wraps an error with a retry-after duration hint
func WithRetryAfter(err error, duration time.Duration) error {
	return &retryAfterError{err: err, duration: duration}
}

type retryAfterError struct {
	err      error
	duration time.Duration
}

func (e *retryAfterError) Error() string {
	return e.err.Error() + " (retry after " + e.duration.String() + ")"
}

func (e *retryAfterError) Unwrap() error {
	return e.err
}
