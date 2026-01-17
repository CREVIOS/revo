package circuitbreaker

import (
	"errors"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// State represents the circuit breaker state
type State int

const (
	StateClosed   State = iota // Normal operation, requests pass through
	StateOpen                  // Circuit is open, requests fail fast
	StateHalfOpen              // Testing if service recovered
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Common errors
var (
	ErrCircuitOpen    = errors.New("circuit breaker is open")
	ErrTooManyFailures = errors.New("too many failures")
)

// Config holds circuit breaker configuration
type Config struct {
	Name             string        // Name for logging
	FailureThreshold int           // Number of failures before opening
	SuccessThreshold int           // Number of successes in half-open to close
	Timeout          time.Duration // How long to stay open before half-open
	MaxHalfOpen      int           // Max concurrent requests in half-open state
}

// DefaultConfig returns sensible defaults
func DefaultConfig(name string) Config {
	return Config{
		Name:             name,
		FailureThreshold: 5,           // Open after 5 consecutive failures
		SuccessThreshold: 2,           // Close after 2 successes in half-open
		Timeout:          30 * time.Second, // Wait 30s before testing recovery
		MaxHalfOpen:      1,           // Allow 1 request in half-open
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	mu sync.RWMutex

	config Config
	state  State

	failureCount  int
	successCount  int
	lastFailure   time.Time
	lastStateChange time.Time

	halfOpenCount int // Current requests in half-open state

	// Metrics
	totalRequests   int64
	totalFailures   int64
	totalSuccesses  int64
	totalRejected   int64
}

// New creates a new circuit breaker
func New(config Config) *CircuitBreaker {
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = 5
	}
	if config.SuccessThreshold <= 0 {
		config.SuccessThreshold = 2
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if config.MaxHalfOpen <= 0 {
		config.MaxHalfOpen = 1
	}

	return &CircuitBreaker{
		config:          config,
		state:           StateClosed,
		lastStateChange: time.Now(),
	}
}

// Execute runs the given function with circuit breaker protection
func (cb *CircuitBreaker) Execute(fn func() error) error {
	if !cb.allowRequest() {
		cb.mu.Lock()
		cb.totalRejected++
		cb.mu.Unlock()

		log.Warn().
			Str("circuit", cb.config.Name).
			Str("state", cb.state.String()).
			Msg("Circuit breaker rejected request")

		return ErrCircuitOpen
	}

	cb.mu.Lock()
	cb.totalRequests++
	cb.mu.Unlock()

	// Execute the function
	err := fn()

	// Record result
	cb.recordResult(err)

	return err
}

// allowRequest checks if a request should be allowed
func (cb *CircuitBreaker) allowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if timeout has passed
		if time.Since(cb.lastFailure) > cb.config.Timeout {
			cb.toHalfOpen()
			return cb.halfOpenCount < cb.config.MaxHalfOpen
		}
		return false

	case StateHalfOpen:
		// Allow limited requests in half-open state
		if cb.halfOpenCount < cb.config.MaxHalfOpen {
			cb.halfOpenCount++
			return true
		}
		return false
	}

	return false
}

// recordResult records the result of a request
func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.onFailure()
	} else {
		cb.onSuccess()
	}
}

// onFailure handles a failed request
func (cb *CircuitBreaker) onFailure() {
	cb.totalFailures++
	cb.failureCount++
	cb.successCount = 0
	cb.lastFailure = time.Now()

	switch cb.state {
	case StateClosed:
		if cb.failureCount >= cb.config.FailureThreshold {
			cb.toOpen()
		}

	case StateHalfOpen:
		cb.halfOpenCount--
		cb.toOpen()
	}

	log.Debug().
		Str("circuit", cb.config.Name).
		Str("state", cb.state.String()).
		Int("failure_count", cb.failureCount).
		Msg("Circuit breaker recorded failure")
}

// onSuccess handles a successful request
func (cb *CircuitBreaker) onSuccess() {
	cb.totalSuccesses++

	switch cb.state {
	case StateClosed:
		cb.failureCount = 0

	case StateHalfOpen:
		cb.halfOpenCount--
		cb.successCount++
		if cb.successCount >= cb.config.SuccessThreshold {
			cb.toClosed()
		}
	}

	log.Debug().
		Str("circuit", cb.config.Name).
		Str("state", cb.state.String()).
		Int("success_count", cb.successCount).
		Msg("Circuit breaker recorded success")
}

// State transitions
func (cb *CircuitBreaker) toOpen() {
	cb.state = StateOpen
	cb.lastStateChange = time.Now()
	cb.halfOpenCount = 0

	log.Warn().
		Str("circuit", cb.config.Name).
		Int("failure_count", cb.failureCount).
		Msg("Circuit breaker opened")
}

func (cb *CircuitBreaker) toHalfOpen() {
	cb.state = StateHalfOpen
	cb.lastStateChange = time.Now()
	cb.halfOpenCount = 0
	cb.successCount = 0

	log.Info().
		Str("circuit", cb.config.Name).
		Msg("Circuit breaker half-opened")
}

func (cb *CircuitBreaker) toClosed() {
	cb.state = StateClosed
	cb.lastStateChange = time.Now()
	cb.failureCount = 0
	cb.successCount = 0

	log.Info().
		Str("circuit", cb.config.Name).
		Msg("Circuit breaker closed")
}

// State returns the current state
func (cb *CircuitBreaker) State() State {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Stats returns circuit breaker statistics
type Stats struct {
	Name            string        `json:"name"`
	State           string        `json:"state"`
	FailureCount    int           `json:"failure_count"`
	SuccessCount    int           `json:"success_count"`
	TotalRequests   int64         `json:"total_requests"`
	TotalFailures   int64         `json:"total_failures"`
	TotalSuccesses  int64         `json:"total_successes"`
	TotalRejected   int64         `json:"total_rejected"`
	LastFailure     *time.Time    `json:"last_failure,omitempty"`
	LastStateChange time.Time     `json:"last_state_change"`
	Timeout         time.Duration `json:"timeout"`
}

// Stats returns current statistics
func (cb *CircuitBreaker) Stats() Stats {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	stats := Stats{
		Name:            cb.config.Name,
		State:           cb.state.String(),
		FailureCount:    cb.failureCount,
		SuccessCount:    cb.successCount,
		TotalRequests:   cb.totalRequests,
		TotalFailures:   cb.totalFailures,
		TotalSuccesses:  cb.totalSuccesses,
		TotalRejected:   cb.totalRejected,
		LastStateChange: cb.lastStateChange,
		Timeout:         cb.config.Timeout,
	}

	if !cb.lastFailure.IsZero() {
		stats.LastFailure = &cb.lastFailure
	}

	return stats
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = StateClosed
	cb.failureCount = 0
	cb.successCount = 0
	cb.halfOpenCount = 0
	cb.lastStateChange = time.Now()

	log.Info().
		Str("circuit", cb.config.Name).
		Msg("Circuit breaker reset")
}
