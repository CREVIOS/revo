package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// Limiter implements token bucket rate limiting for Claude Code CLI calls
type Limiter struct {
	tokens         int
	maxTokens      int
	refillRate     time.Duration
	mu             sync.Mutex
	lastRefill     time.Time
	totalRequests  int64
	totalWaitTime  time.Duration
}

// NewLimiter creates a new rate limiter
// maxTokens: maximum concurrent requests
// refillRate: how often to add a new token
func NewLimiter(maxTokens int, refillRate time.Duration) *Limiter {
	if maxTokens <= 0 {
		maxTokens = 2 // Default: 2 concurrent Claude Code CLI calls
	}
	if refillRate <= 0 {
		refillRate = 30 * time.Second // Default: 1 token every 30 seconds
	}

	return &Limiter{
		tokens:     maxTokens,
		maxTokens:  maxTokens,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

// Wait blocks until a token is available
func (l *Limiter) Wait(ctx context.Context) error {
	start := time.Now()

	for {
		// Try to acquire token
		if l.tryAcquire() {
			waitDuration := time.Since(start)
			if waitDuration > 0 {
				l.mu.Lock()
				l.totalWaitTime += waitDuration
				l.mu.Unlock()

				log.Debug().
					Dur("wait_time", waitDuration).
					Int("tokens_available", l.tokens).
					Msg("Rate limit: acquired token after waiting")
			}
			return nil
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			// Continue waiting
		}
	}
}

// tryAcquire attempts to acquire a token
func (l *Limiter) tryAcquire() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Refill tokens based on time elapsed
	now := time.Now()
	elapsed := now.Sub(l.lastRefill)
	tokensToAdd := int(elapsed / l.refillRate)

	if tokensToAdd > 0 {
		l.tokens += tokensToAdd
		if l.tokens > l.maxTokens {
			l.tokens = l.maxTokens
		}
		l.lastRefill = now
	}

	// Try to consume a token
	if l.tokens > 0 {
		l.tokens--
		l.totalRequests++
		return true
	}

	return false
}

// Release returns a token to the bucket (call this when done with review)
func (l *Limiter) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.tokens < l.maxTokens {
		l.tokens++
	}
}

// Stats returns current rate limiter statistics
func (l *Limiter) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()

	avgWait := time.Duration(0)
	if l.totalRequests > 0 {
		avgWait = l.totalWaitTime / time.Duration(l.totalRequests)
	}

	return Stats{
		AvailableTokens: l.tokens,
		MaxTokens:       l.maxTokens,
		TotalRequests:   l.totalRequests,
		AverageWaitTime: avgWait,
		RefillRate:      l.refillRate,
	}
}

// Stats holds rate limiter statistics
type Stats struct {
	AvailableTokens int           `json:"available_tokens"`
	MaxTokens       int           `json:"max_tokens"`
	TotalRequests   int64         `json:"total_requests"`
	AverageWaitTime time.Duration `json:"average_wait_time"`
	RefillRate      time.Duration `json:"refill_rate"`
}

// String returns a human-readable representation
func (s Stats) String() string {
	return fmt.Sprintf(
		"Tokens: %d/%d | Requests: %d | Avg Wait: %v | Refill: %v",
		s.AvailableTokens, s.MaxTokens, s.TotalRequests, s.AverageWaitTime, s.RefillRate,
	)
}
