package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() {
	// Health check endpoint
	s.router.HandleFunc("/health", s.healthHandler).Methods(http.MethodGet)

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

	// Middleware for logging
	s.router.Use(loggingMiddleware)
}

// healthHandler returns the health status of the server
func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	status := struct {
		Status string    `json:"status"`
		Bot    string    `json:"bot"`
		Model  string    `json:"model"`
		Time   time.Time `json:"time"`
	}{
		Status: "healthy",
		Bot:    s.config.BotUsername,
		Model:  s.config.ClaudeModel,
		Time:   time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
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
	rateLimiterStats := s.rateLimiter.Stats()

	stats := struct {
		Queue       interface{} `json:"queue"`
		RateLimiter interface{} `json:"rate_limiter"`
		Uptime      string      `json:"uptime"`
	}{
		Queue:       s.queueInfo(),
		RateLimiter: rateLimiterStats,
		Uptime:      time.Since(startTime).String(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

var startTime = time.Now()

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
