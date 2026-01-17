package server

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"
	"github.com/yourusername/techy-bot/internal/claude"
	gh "github.com/yourusername/techy-bot/internal/github"
	"github.com/yourusername/techy-bot/internal/review"
	"github.com/yourusername/techy-bot/pkg/models"
)

// Server represents the TechyBot HTTP server
type Server struct {
	config         *models.Config
	router         *mux.Router
	httpServer     *http.Server
	githubClient   *gh.Client
	claudeClient   *claude.Client
	reviewer       *review.Reviewer
	webhookHandler *gh.WebhookHandler
}

// New creates a new Server instance
func New(cfg *models.Config) (*Server, error) {
	s := &Server{
		config: cfg,
		router: mux.NewRouter(),
	}

	// Initialize GitHub client
	s.githubClient = gh.NewClient(cfg.GitHubAppID, cfg.GitHubPrivateKey)

	// Initialize Claude Code CLI client
	s.claudeClient = claude.NewClient(cfg.ClaudePath, cfg.ClaudeModel)

	// Initialize reviewer
	s.reviewer = review.NewReviewer(s.githubClient, s.claudeClient, cfg.MaxDiffSize)

	// Initialize webhook handler
	s.webhookHandler = gh.NewWebhookHandler(
		cfg.GitHubWebhookSecret,
		cfg.BotUsername,
		s.handleCommand,
	)

	// Setup routes
	s.setupRoutes()

	// Create HTTP server
	s.httpServer = &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s, nil
}

// handleCommand processes a parsed command from a webhook event
func (s *Server) handleCommand(event *gh.WebhookEvent) error {
	ctx := context.Background()
	return s.reviewer.ProcessReview(ctx, event)
}

// Start begins listening for HTTP requests
func (s *Server) Start() error {
	// Setup graceful shutdown
	done := make(chan bool, 1)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Info().Msg("Server is shutting down...")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		s.httpServer.SetKeepAlivesEnabled(false)
		if err := s.httpServer.Shutdown(ctx); err != nil {
			log.Error().Err(err).Msg("Could not gracefully shutdown server")
		}
		close(done)
	}()

	log.Info().
		Str("port", s.config.Port).
		Str("bot_username", s.config.BotUsername).
		Str("model", s.config.ClaudeModel).
		Msg("TechyBot server starting")

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}

	<-done
	log.Info().Msg("Server stopped")
	return nil
}

// GetRouter returns the router for testing
func (s *Server) GetRouter() *mux.Router {
	return s.router
}
