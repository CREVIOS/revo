package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// PromptCache provides caching for Claude API responses to reduce API calls
// and improve throughput. Cached tokens don't count toward rate limits.
type PromptCache struct {
	mu       sync.RWMutex
	entries  map[string]*cacheEntry
	maxSize  int
	ttl      time.Duration
	hits     int64
	misses   int64
	evictions int64
}

type cacheEntry struct {
	response  string
	createdAt time.Time
	accessedAt time.Time
	accessCount int64
}

// Config holds cache configuration
type Config struct {
	MaxSize int           // Maximum number of entries
	TTL     time.Duration // Time-to-live for entries
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		MaxSize: 1000,            // Cache up to 1000 responses
		TTL:     30 * time.Minute, // 30 minute TTL
	}
}

// NewPromptCache creates a new prompt cache
func NewPromptCache(cfg Config) *PromptCache {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = DefaultConfig().MaxSize
	}
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultConfig().TTL
	}

	cache := &PromptCache{
		entries: make(map[string]*cacheEntry),
		maxSize: cfg.MaxSize,
		ttl:     cfg.TTL,
	}

	// Start background cleanup goroutine
	go cache.cleanupLoop()

	return cache
}

// Get retrieves a cached response for the given prompt
func (c *PromptCache) Get(prompt string) (string, bool) {
	key := c.hashKey(prompt)

	c.mu.RLock()
	entry, exists := c.entries[key]
	c.mu.RUnlock()

	if !exists {
		c.mu.Lock()
		c.misses++
		c.mu.Unlock()
		return "", false
	}

	// Check if expired
	if time.Since(entry.createdAt) > c.ttl {
		c.mu.Lock()
		delete(c.entries, key)
		c.misses++
		c.mu.Unlock()
		return "", false
	}

	// Update access time and count
	c.mu.Lock()
	entry.accessedAt = time.Now()
	entry.accessCount++
	c.hits++
	c.mu.Unlock()

	log.Debug().
		Str("key", key[:16]+"...").
		Int64("access_count", entry.accessCount).
		Msg("Prompt cache hit")

	return entry.response, true
}

// Set stores a response in the cache
func (c *PromptCache) Set(prompt, response string) {
	key := c.hashKey(prompt)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict if at capacity
	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	c.entries[key] = &cacheEntry{
		response:    response,
		createdAt:   time.Now(),
		accessedAt:  time.Now(),
		accessCount: 1,
	}

	log.Debug().
		Str("key", key[:16]+"...").
		Int("cache_size", len(c.entries)).
		Msg("Prompt cached")
}

// hashKey generates a cache key from the prompt
func (c *PromptCache) hashKey(prompt string) string {
	hash := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(hash[:])
}

// evictOldest removes the oldest entry (LRU-style)
func (c *PromptCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range c.entries {
		if oldestKey == "" || entry.accessedAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.accessedAt
		}
	}

	if oldestKey != "" {
		delete(c.entries, oldestKey)
		c.evictions++
		log.Debug().
			Str("key", oldestKey[:16]+"...").
			Msg("Evicted cache entry")
	}
}

// cleanupLoop periodically removes expired entries
func (c *PromptCache) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.cleanup()
	}
}

// cleanup removes expired entries
func (c *PromptCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	expired := 0

	for key, entry := range c.entries {
		if now.Sub(entry.createdAt) > c.ttl {
			delete(c.entries, key)
			expired++
		}
	}

	if expired > 0 {
		log.Debug().
			Int("expired", expired).
			Int("remaining", len(c.entries)).
			Msg("Cleaned up expired cache entries")
	}
}

// Stats returns cache statistics
func (c *PromptCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	hitRate := float64(0)
	total := c.hits + c.misses
	if total > 0 {
		hitRate = float64(c.hits) / float64(total) * 100
	}

	return CacheStats{
		Size:      len(c.entries),
		MaxSize:   c.maxSize,
		Hits:      c.hits,
		Misses:    c.misses,
		HitRate:   hitRate,
		Evictions: c.evictions,
		TTL:       c.ttl,
	}
}

// CacheStats holds cache statistics
type CacheStats struct {
	Size      int           `json:"size"`
	MaxSize   int           `json:"max_size"`
	Hits      int64         `json:"hits"`
	Misses    int64         `json:"misses"`
	HitRate   float64       `json:"hit_rate_percent"`
	Evictions int64         `json:"evictions"`
	TTL       time.Duration `json:"ttl"`
}

// Clear removes all entries from the cache
func (c *PromptCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*cacheEntry)
	log.Info().Msg("Prompt cache cleared")
}
