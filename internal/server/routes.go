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
	}{
		Name:        "TechyBot",
		Description: "AI-powered code review bot using Claude",
		Commands: []string{
			"@techy review - Standard code review",
			"@techy hunt - Quick bug detection",
			"@techy security - Security-focused analysis",
			"@techy performance - Performance optimization",
			"@techy analyze - Deep technical analysis",
		},
		Repository: "https://github.com/yourusername/techy-bot",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
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
