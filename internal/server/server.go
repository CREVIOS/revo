package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CREVIOS/revo/internal/claude"
	contextaware "github.com/CREVIOS/revo/internal/context"
	"github.com/CREVIOS/revo/internal/database"
	gh "github.com/CREVIOS/revo/internal/github"
	"github.com/CREVIOS/revo/internal/ratelimit"
	"github.com/CREVIOS/revo/internal/review"
	"github.com/CREVIOS/revo/internal/tasks"
	"github.com/CREVIOS/revo/pkg/models"
	"github.com/gorilla/mux"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"
)

// Server represents the TechyBot HTTP server
type Server struct {
	config          *models.Config
	router          *mux.Router
	httpServer      *http.Server
	githubClient    *gh.Client
	claudeClient    *claude.Client
	reviewer        *review.Reviewer
	webhookHandler  *gh.WebhookHandler
	rateLimiter     *ratelimit.Limiter
	contextAnalyzer *contextaware.ContextAwareAnalyzer
	store           *database.Store
	asynqClient     *asynq.Client
	asynqInspector  *asynq.Inspector
	asynqQueue      string
}

// New creates a new Server instance
func New(cfg *models.Config) (*Server, error) {
	s := &Server{
		config: cfg,
		router: mux.NewRouter(),
	}

	// Initialize database connection and store
	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	s.store = database.NewStore(db)

	redisOpt := asynq.RedisClientOpt{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	}
	s.asynqClient = asynq.NewClient(redisOpt)
	s.asynqInspector = asynq.NewInspector(redisOpt)
	s.asynqQueue = cfg.AsynqQueue

	// Initialize GitHub client
	s.githubClient = gh.NewClient(cfg.GitHubAppID, cfg.GitHubPrivateKey)

	// Initialize Claude Code CLI client
	s.claudeClient = claude.NewClient(cfg.ClaudePath, cfg.ClaudeModel)

	// Initialize context analyzer for smarter reviews
	s.contextAnalyzer = contextaware.NewContextAwareAnalyzer(s.githubClient, cfg.BotUsername)

	// Initialize rate limiter (2 concurrent reviews, 1 token every 30 seconds)
	s.rateLimiter = ratelimit.NewLimiter(2, 30*time.Second)

	// Initialize reviewer with enhanced features
	s.reviewer = review.NewReviewer(s.githubClient, s.claudeClient, cfg.MaxDiffSize)
	s.reviewer.SetContextAnalyzer(s.contextAnalyzer)
	s.reviewer.SetRateLimiter(s.rateLimiter)
	s.reviewer.SetStore(s.store)

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
	owner := event.Repository.Owner.Login
	repo := event.Repository.Name
	prNumber := event.PullRequest.Number
	commitSHA := ""
	senderLogin := ""
	if event.Sender != nil {
		senderLogin = event.Sender.Login
	}

	if s.store != nil {
		if event.PullRequest != nil && event.PullRequest.Head != nil {
			commitSHA = event.PullRequest.Head.SHA
		}
		if commitSHA == "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			pr, err := s.githubClient.GetPullRequest(ctx, owner, repo, prNumber)
			cancel()
			if err == nil && pr.GetHead() != nil {
				commitSHA = pr.GetHead().GetSHA()
			}
		}

		_ = s.store.UpsertRepository(&database.Repository{
			Owner:     owner,
			Name:      repo,
			FullName:  event.Repository.FullName,
			IsPrivate: event.Repository.Private,
			IsActive:  true,
		})

		reviewRecord := &database.Review{
			Owner:       owner,
			Repo:        repo,
			PRNumber:    prNumber,
			Mode:        string(event.Command.Mode),
			Status:      "queued",
			QueuedAt:    time.Now(),
			RequestedBy: event.Sender.Login,
		}
		if event.PullRequest != nil {
			reviewRecord.PRTitle = event.PullRequest.Title
			reviewRecord.CommitSHA = commitSHA
		}

		if err := s.store.CreateReview(reviewRecord); err != nil {
			log.Warn().Err(err).Msg("Failed to create review record")
		} else {
			event.ReviewID = reviewRecord.ID
			_ = s.store.CreateWebhookEvent(&database.WebhookEvent{
				EventType:   event.EventType,
				Owner:       owner,
				Repo:        repo,
				PRNumber:    prNumber,
				Action:      event.Action,
				ProcessedAt: ptrTime(time.Now()),
				ReviewID:    &reviewRecord.ID,
			})
		}
	}

	payload := tasks.ReviewPayload{
		EventType:   event.EventType,
		Action:      event.Action,
		Owner:       owner,
		Repo:        repo,
		PRNumber:    prNumber,
		CommentID:   event.Comment.ID,
		CommentBody: event.Comment.Body,
		SenderLogin: senderLogin,
		Mode:        string(event.Command.Mode),
		Verbose:     event.Command.Verbose,
		CommitSHA:   commitSHA,
		ReviewID:    event.ReviewID,
	}

	task, err := tasks.NewReviewTask(payload)
	if err != nil {
		log.Error().Err(err).Msg("Failed to build review task")
		return err
	}

	taskID := fmt.Sprintf("review:%s/%s/%d", owner, repo, prNumber)
	if commitSHA != "" {
		taskID = fmt.Sprintf("%s:%s", taskID, commitSHA)
	}

	_, err = s.asynqClient.Enqueue(
		task,
		asynq.Queue(s.asynqQueue),
		asynq.MaxRetry(s.config.AsynqMaxRetry),
		asynq.TaskID(taskID),
	)
	if err != nil {
		if err == asynq.ErrDuplicateTask || err == asynq.ErrTaskIDConflict {
			log.Info().Err(err).Msg("Duplicate review task ignored")
			if s.store != nil && event.ReviewID > 0 {
				completedAt := time.Now()
				_ = s.store.UpdateReview(event.ReviewID, map[string]interface{}{
					"status":        "cancelled",
					"error_message": "duplicate task",
					"completed_at":  completedAt,
					"duration_ms":   int64(0),
				})
			}
			return nil
		}

		log.Error().Err(err).Msg("Failed to enqueue review task")
		if s.store != nil && event.ReviewID > 0 {
			completedAt := time.Now()
			_ = s.store.UpdateReview(event.ReviewID, map[string]interface{}{
				"status":        "failed",
				"error_message": err.Error(),
				"completed_at":  completedAt,
				"duration_ms":   int64(0),
			})
		}
		return err
	}

	return nil
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func (s *Server) queueInfo() interface{} {
	if s.asynqInspector == nil {
		return nil
	}

	info, err := s.asynqInspector.GetQueueInfo(s.asynqQueue)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to fetch queue info")
		return nil
	}

	return info
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
		if s.asynqClient != nil {
			if err := s.asynqClient.Close(); err != nil {
				log.Warn().Err(err).Msg("Failed to close Redis client")
			}
		}
		close(done)
	}()

	log.Info().
		Str("port", s.config.Port).
		Str("bot_username", s.config.BotUsername).
		Str("model", s.config.ClaudeModel).
		Str("queue", s.asynqQueue).
		Msg("TechyBot server starting with Redis queue")

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
