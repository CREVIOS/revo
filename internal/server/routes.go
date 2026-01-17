package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() {
	// Health check endpoints (Kubernetes-compatible)
	s.router.HandleFunc("/health", s.healthHandler).Methods(http.MethodGet)      // Liveness probe
	s.router.HandleFunc("/healthz", s.healthHandler).Methods(http.MethodGet)     // Kubernetes liveness
	s.router.HandleFunc("/ready", s.readinessHandler).Methods(http.MethodGet)    // Readiness probe
	s.router.HandleFunc("/readyz", s.readinessHandler).Methods(http.MethodGet)   // Kubernetes readiness

	// GitHub webhook endpoint
	s.router.HandleFunc("/webhook", s.webhookHandler.HandleWebhook).Methods(http.MethodPost)

	// Info endpoint
	s.router.HandleFunc("/", s.infoHandler).Methods(http.MethodGet)

	// Stats endpoint - queue and rate limiter stats
	s.router.HandleFunc("/stats", s.statsHandler).Methods(http.MethodGet)

	// Admin API (protected)
	api := s.router.PathPrefix("/api").Subrouter()
	api.Use(s.adminAuthMiddleware)

	api.HandleFunc("/metrics", s.metricsHandler).Methods(http.MethodGet)

	api.HandleFunc("/reviews", s.listReviewsHandler).Methods(http.MethodGet)
	api.HandleFunc("/reviews", s.createReviewHandler).Methods(http.MethodPost)
	api.HandleFunc("/reviews/{id:[0-9]+}", s.getReviewHandler).Methods(http.MethodGet)
	api.HandleFunc("/reviews/{id:[0-9]+}", s.updateReviewHandler).Methods(http.MethodPut)
	api.HandleFunc("/reviews/{id:[0-9]+}", s.deleteReviewHandler).Methods(http.MethodDelete)

	api.HandleFunc("/review-comments", s.listReviewCommentsHandler).Methods(http.MethodGet)
	api.HandleFunc("/review-comments", s.createReviewCommentHandler).Methods(http.MethodPost)
	api.HandleFunc("/review-comments/{id:[0-9]+}", s.getReviewCommentHandler).Methods(http.MethodGet)
	api.HandleFunc("/review-comments/{id:[0-9]+}", s.updateReviewCommentHandler).Methods(http.MethodPut)
	api.HandleFunc("/review-comments/{id:[0-9]+}", s.deleteReviewCommentHandler).Methods(http.MethodDelete)

	api.HandleFunc("/repositories", s.listRepositoriesHandler).Methods(http.MethodGet)
	api.HandleFunc("/repositories", s.createRepositoryHandler).Methods(http.MethodPost)
	api.HandleFunc("/repositories/{id:[0-9]+}", s.getRepositoryHandler).Methods(http.MethodGet)
	api.HandleFunc("/repositories/{id:[0-9]+}", s.updateRepositoryHandler).Methods(http.MethodPut)
	api.HandleFunc("/repositories/{id:[0-9]+}", s.deleteRepositoryHandler).Methods(http.MethodDelete)

	api.HandleFunc("/webhook-events", s.listWebhookEventsHandler).Methods(http.MethodGet)
	api.HandleFunc("/webhook-events", s.createWebhookEventHandler).Methods(http.MethodPost)
	api.HandleFunc("/webhook-events/{id:[0-9]+}", s.getWebhookEventHandler).Methods(http.MethodGet)
	api.HandleFunc("/webhook-events/{id:[0-9]+}", s.updateWebhookEventHandler).Methods(http.MethodPut)
	api.HandleFunc("/webhook-events/{id:[0-9]+}", s.deleteWebhookEventHandler).Methods(http.MethodDelete)

	api.HandleFunc("/worker-metrics", s.listWorkerMetricsHandler).Methods(http.MethodGet)
	api.HandleFunc("/worker-metrics", s.createWorkerMetricsHandler).Methods(http.MethodPost)
	api.HandleFunc("/worker-metrics/{id:[0-9]+}", s.getWorkerMetricsHandler).Methods(http.MethodGet)
	api.HandleFunc("/worker-metrics/{id:[0-9]+}", s.updateWorkerMetricsHandler).Methods(http.MethodPut)
	api.HandleFunc("/worker-metrics/{id:[0-9]+}", s.deleteWorkerMetricsHandler).Methods(http.MethodDelete)

	api.HandleFunc("/api-keys", s.listAPIKeysHandler).Methods(http.MethodGet)
	api.HandleFunc("/api-keys", s.createAPIKeyHandler).Methods(http.MethodPost)
	api.HandleFunc("/api-keys/{id:[0-9]+}", s.getAPIKeyHandler).Methods(http.MethodGet)
	api.HandleFunc("/api-keys/{id:[0-9]+}", s.updateAPIKeyHandler).Methods(http.MethodPut)
	api.HandleFunc("/api-keys/{id:[0-9]+}", s.deleteAPIKeyHandler).Methods(http.MethodDelete)

	// Cache management endpoints
	api.HandleFunc("/cache/stats", s.cacheStatsHandler).Methods(http.MethodGet)
	api.HandleFunc("/cache/clear", s.cacheClearHandler).Methods(http.MethodPost)

	// Middleware for logging and timeout
	s.router.Use(loggingMiddleware)
	s.router.Use(timeoutMiddleware(30 * time.Second)) // 30 second timeout for API requests
}

// cacheStatsHandler returns prompt cache statistics
func (s *Server) cacheStatsHandler(w http.ResponseWriter, r *http.Request) {
	if s.claudeClient == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"enabled": false,
			"message": "cache not configured",
		})
		return
	}

	stats := s.claudeClient.CacheStats()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled": s.config.CacheEnabled,
		"stats":   stats,
	})
}

// cacheClearHandler clears the prompt cache
func (s *Server) cacheClearHandler(w http.ResponseWriter, r *http.Request) {
	if s.claudeClient == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": "cache not configured",
		})
		return
	}

	s.claudeClient.ClearCache()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "cache cleared",
	})
}

// healthHandler returns the health status of the server (liveness probe)
// This should return 200 if the server is running, regardless of dependencies
func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	status := struct {
		Status string    `json:"status"`
		Bot    string    `json:"bot"`
		Model  string    `json:"model"`
		Time   time.Time `json:"time"`
		Uptime string    `json:"uptime"`
	}{
		Status: "healthy",
		Bot:    s.config.BotUsername,
		Model:  s.config.ClaudeModel,
		Time:   time.Now(),
		Uptime: time.Since(startTime).String(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// readinessHandler checks if the server is ready to accept traffic (readiness probe)
// This verifies all dependencies (database, Redis) are accessible
func (s *Server) readinessHandler(w http.ResponseWriter, r *http.Request) {
	checks := make(map[string]interface{})
	allHealthy := true

	// Check database connection
	if s.store != nil {
		db := s.store.DB()
		sqlDB, err := db.DB()
		if err != nil {
			checks["database"] = map[string]interface{}{
				"status": "unhealthy",
				"error":  err.Error(),
			}
			allHealthy = false
		} else if err := sqlDB.Ping(); err != nil {
			checks["database"] = map[string]interface{}{
				"status": "unhealthy",
				"error":  err.Error(),
			}
			allHealthy = false
		} else {
			stats := sqlDB.Stats()
			checks["database"] = map[string]interface{}{
				"status":       "healthy",
				"open_conns":   stats.OpenConnections,
				"in_use":       stats.InUse,
				"idle":         stats.Idle,
				"max_open":     stats.MaxOpenConnections,
			}
		}
	} else {
		checks["database"] = map[string]interface{}{
			"status": "not_configured",
		}
	}

	// Check Redis/Asynq connection
	if s.asynqInspector != nil {
		_, err := s.asynqInspector.GetQueueInfo(s.asynqQueue)
		if err != nil {
			checks["redis"] = map[string]interface{}{
				"status": "unhealthy",
				"error":  err.Error(),
			}
			allHealthy = false
		} else {
			checks["redis"] = map[string]interface{}{
				"status": "healthy",
				"queue":  s.asynqQueue,
			}
		}
	} else {
		checks["redis"] = map[string]interface{}{
			"status": "not_configured",
		}
	}

	// Add cache stats if available
	if s.claudeClient != nil {
		cacheStats := s.claudeClient.CacheStats()
		checks["cache"] = map[string]interface{}{
			"status":   "healthy",
			"size":     cacheStats.Size,
			"max_size": cacheStats.MaxSize,
			"hit_rate": cacheStats.HitRate,
		}
	}

	// Add deduplicator stats if available
	if s.deduplicator != nil {
		dedupStats := s.deduplicator.Stats()
		checks["deduplicator"] = map[string]interface{}{
			"status":    "healthy",
			"total":     dedupStats.Total,
			"pending":   dedupStats.Pending,
			"completed": dedupStats.Completed,
		}
	}

	response := struct {
		Status string                 `json:"status"`
		Checks map[string]interface{} `json:"checks"`
		Time   time.Time              `json:"time"`
	}{
		Status: "ready",
		Checks: checks,
		Time:   time.Now(),
	}

	if !allHealthy {
		response.Status = "not_ready"
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(response)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// infoHandler returns basic information about the bot
func (s *Server) infoHandler(w http.ResponseWriter, r *http.Request) {
	info := struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Commands    []string `json:"commands"`
		Repository  string   `json:"repository"`
		Features    []string `json:"features"`
	}{
		Name:        "TechyBot",
		Description: "AI-powered code review bot using Claude Code CLI (like Cursor's BugBot)",
		Commands: []string{
			"@techy hunt - Quick bug detection (BugBot mode)",
			"@techy review - Standard code review",
			"@techy security - Security-focused analysis",
			"@techy performance - Performance optimization",
			"@techy analyze - Deep technical analysis",
		},
		Features: []string{
			"Inline comments with line numbers",
			"Queue system for concurrent reviews",
			"Context-aware (reads existing PR comments)",
			"Rate limiting to prevent overload",
			"Low false positive rate",
			"Cancels stale reviews on new commits",
		},
		Repository: "https://github.com/CREVIOS/revo",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// statsHandler returns current queue and rate limiter statistics
func (s *Server) statsHandler(w http.ResponseWriter, r *http.Request) {
	stats := struct {
		Queue        interface{} `json:"queue"`
		RateLimiter  interface{} `json:"rate_limiter"`
		Cache        interface{} `json:"cache,omitempty"`
		Deduplicator interface{} `json:"deduplicator,omitempty"`
		Uptime       string      `json:"uptime"`
		Config       interface{} `json:"config"`
	}{
		Queue:       s.queueInfo(),
		RateLimiter: s.rateLimiter.Stats(),
		Uptime:      time.Since(startTime).String(),
		Config: map[string]interface{}{
			"concurrency":           s.config.AsynqConcurrency,
			"rate_limit_max_tokens": s.config.RateLimitMaxTokens,
			"rate_limit_refill_sec": s.config.RateLimitRefillSec,
			"retry_max_attempts":    s.config.RetryMaxAttempts,
			"cache_enabled":         s.config.CacheEnabled,
			"dedup_enabled":         s.config.DedupEnabled,
		},
	}

	// Add cache stats if available
	if s.claudeClient != nil {
		stats.Cache = s.claudeClient.CacheStats()
	}

	// Add deduplicator stats if available
	if s.deduplicator != nil {
		stats.Deduplicator = s.deduplicator.Stats()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

var startTime = time.Now()

// timeoutMiddleware adds a timeout to requests to prevent hanging
func timeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip timeout for webhook endpoint (handled by async processing)
			if r.URL.Path == "/webhook" {
				next.ServeHTTP(w, r)
				return
			}

			// Create a done channel
			done := make(chan struct{})

			// Create a response wrapper
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			go func() {
				next.ServeHTTP(wrapped, r)
				close(done)
			}()

			select {
			case <-done:
				// Request completed normally
			case <-time.After(timeout):
				// Request timed out
				if wrapped.statusCode == http.StatusOK {
					w.WriteHeader(http.StatusGatewayTimeout)
					w.Write([]byte(`{"error":"request timeout"}`))
				}
				log.Warn().
					Str("path", r.URL.Path).
					Dur("timeout", timeout).
					Msg("Request timed out")
			}
		})
	}
}

// loggingMiddleware logs all incoming requests
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a response wrapper to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		log.Debug().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", wrapped.statusCode).
			Dur("duration", time.Since(start)).
			Str("remote_addr", r.RemoteAddr).
			Msg("HTTP request")
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
