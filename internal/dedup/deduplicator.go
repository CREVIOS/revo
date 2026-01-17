package dedup

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// Deduplicator prevents duplicate requests within a configurable TTL window
type Deduplicator struct {
	mu       sync.RWMutex
	requests map[string]*requestEntry
	ttl      time.Duration
}

type requestEntry struct {
	createdAt time.Time
	status    RequestStatus
	result    interface{}
	err       error
	done      chan struct{}
}

// RequestStatus represents the status of a deduplicated request
type RequestStatus string

const (
	StatusPending   RequestStatus = "pending"
	StatusCompleted RequestStatus = "completed"
	StatusFailed    RequestStatus = "failed"
)

// Config holds deduplicator configuration
type Config struct {
	TTL             time.Duration // How long to remember requests
	CleanupInterval time.Duration // How often to clean up expired entries
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		TTL:             5 * time.Minute,  // Remember requests for 5 minutes
		CleanupInterval: 1 * time.Minute,  // Clean up every minute
	}
}

// New creates a new Deduplicator
func New(cfg Config) *Deduplicator {
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultConfig().TTL
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = DefaultConfig().CleanupInterval
	}

	d := &Deduplicator{
		requests: make(map[string]*requestEntry),
		ttl:      cfg.TTL,
	}

	// Start cleanup goroutine
	go d.cleanupLoop(cfg.CleanupInterval)

	return d
}

// RequestKey generates a unique key for a review request
func RequestKey(owner, repo string, prNumber int, commitSHA string) string {
	return fmt.Sprintf("review:%s/%s/%d:%s", owner, repo, prNumber, commitSHA)
}

// RequestKeyWithMode generates a key that includes the review mode
func RequestKeyWithMode(owner, repo string, prNumber int, commitSHA, mode string) string {
	return fmt.Sprintf("review:%s/%s/%d:%s:%s", owner, repo, prNumber, commitSHA, mode)
}

// CheckAndMark attempts to mark a request as in-progress
// Returns (isDuplicate, waitChan) where:
// - isDuplicate: true if this is a duplicate request
// - waitChan: channel that closes when the original request completes (nil if not duplicate)
func (d *Deduplicator) CheckAndMark(key string) (bool, <-chan struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()

	entry, exists := d.requests[key]
	if exists {
		// Check if expired
		if time.Since(entry.createdAt) > d.ttl {
			// Expired, remove and allow new request
			delete(d.requests, key)
		} else {
			// Still valid, this is a duplicate
			log.Info().
				Str("key", key).
				Str("status", string(entry.status)).
				Dur("age", time.Since(entry.createdAt)).
				Msg("Duplicate request detected")
			return true, entry.done
		}
	}

	// New request, mark it
	d.requests[key] = &requestEntry{
		createdAt: time.Now(),
		status:    StatusPending,
		done:      make(chan struct{}),
	}

	log.Debug().
		Str("key", key).
		Msg("Request marked for deduplication")

	return false, nil
}

// Complete marks a request as completed with its result
func (d *Deduplicator) Complete(key string, result interface{}) {
	d.mu.Lock()
	defer d.mu.Unlock()

	entry, exists := d.requests[key]
	if !exists {
		return
	}

	entry.status = StatusCompleted
	entry.result = result
	close(entry.done)

	log.Debug().
		Str("key", key).
		Msg("Request marked as completed")
}

// Fail marks a request as failed with its error
func (d *Deduplicator) Fail(key string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	entry, exists := d.requests[key]
	if !exists {
		return
	}

	entry.status = StatusFailed
	entry.err = err
	close(entry.done)

	log.Debug().
		Str("key", key).
		Err(err).
		Msg("Request marked as failed")
}

// GetResult retrieves the result of a completed request
func (d *Deduplicator) GetResult(key string) (interface{}, error, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	entry, exists := d.requests[key]
	if !exists {
		return nil, nil, false
	}

	if entry.status == StatusPending {
		return nil, nil, false
	}

	return entry.result, entry.err, true
}

// WaitForResult waits for a request to complete and returns its result
func (d *Deduplicator) WaitForResult(ctx context.Context, key string) (interface{}, error, bool) {
	d.mu.RLock()
	entry, exists := d.requests[key]
	d.mu.RUnlock()

	if !exists {
		return nil, nil, false
	}

	// Wait for completion or context cancellation
	select {
	case <-entry.done:
		return entry.result, entry.err, true
	case <-ctx.Done():
		return nil, ctx.Err(), false
	}
}

// Remove removes a request from deduplication tracking
func (d *Deduplicator) Remove(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.requests, key)
}

// cleanupLoop periodically removes expired entries
func (d *Deduplicator) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		d.cleanup()
	}
}

// cleanup removes expired entries
func (d *Deduplicator) cleanup() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	expired := 0

	for key, entry := range d.requests {
		if now.Sub(entry.createdAt) > d.ttl {
			delete(d.requests, key)
			expired++
		}
	}

	if expired > 0 {
		log.Debug().
			Int("expired", expired).
			Int("remaining", len(d.requests)).
			Msg("Cleaned up expired dedup entries")
	}
}

// Stats returns deduplicator statistics
func (d *Deduplicator) Stats() DedupStats {
	d.mu.RLock()
	defer d.mu.RUnlock()

	pending := 0
	completed := 0
	failed := 0

	for _, entry := range d.requests {
		switch entry.status {
		case StatusPending:
			pending++
		case StatusCompleted:
			completed++
		case StatusFailed:
			failed++
		}
	}

	return DedupStats{
		Total:     len(d.requests),
		Pending:   pending,
		Completed: completed,
		Failed:    failed,
		TTL:       d.ttl,
	}
}

// DedupStats holds deduplicator statistics
type DedupStats struct {
	Total     int           `json:"total"`
	Pending   int           `json:"pending"`
	Completed int           `json:"completed"`
	Failed    int           `json:"failed"`
	TTL       time.Duration `json:"ttl"`
}
