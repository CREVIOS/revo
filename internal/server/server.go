package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CREVIOS/revo/internal/cache"
	"github.com/CREVIOS/revo/internal/claude"
	contextaware "github.com/CREVIOS/revo/internal/context"
	"github.com/CREVIOS/revo/internal/database"
	"github.com/CREVIOS/revo/internal/dedup"
	gh "github.com/CREVIOS/revo/internal/github"
	"github.com/CREVIOS/revo/internal/ratelimit"
	"github.com/CREVIOS/revo/internal/retry"
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
	deduplicator    *dedup.Deduplicator
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

	// Initialize Claude Code CLI client with retry and caching
	claudeOpts := []claude.ClientOption{
		claude.WithRetryConfig(retry.Config{
			MaxRetries:     cfg.RetryMaxAttempts,
			InitialDelay:   time.Duration(cfg.RetryInitialDelay) * time.Millisecond,
			MaxDelay:       time.Duration(cfg.RetryMaxDelay) * time.Millisecond,
			Multiplier:     2.0,
			JitterFraction: 0.3,
		}),
		claude.WithCacheEnabled(cfg.CacheEnabled),
	}
	if cfg.CacheEnabled {
		claudeOpts = append(claudeOpts, claude.WithPromptCache(cache.Config{
			MaxSize: cfg.CacheMaxSize,
			TTL:     time.Duration(cfg.CacheTTLMin) * time.Minute,
		}))
	}
	s.claudeClient = claude.NewClient(cfg.ClaudePath, cfg.ClaudeModel, claudeOpts...)

	// Initialize context analyzer for smarter reviews
	s.contextAnalyzer = contextaware.NewContextAwareAnalyzer(s.githubClient, cfg.BotUsername)

	// Initialize rate limiter with configurable settings
	s.rateLimiter = ratelimit.NewLimiter(cfg.RateLimitMaxTokens, time.Duration(cfg.RateLimitRefillSec)*time.Second)

	// Initialize deduplicator for preventing duplicate requests
	if cfg.DedupEnabled {
		s.deduplicator = dedup.New(dedup.Config{
			TTL:             time.Duration(cfg.DedupTTLMin) * time.Minute,
			CleanupInterval: 1 * time.Minute,
		})
	}

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

	// Add eyes reaction immediately to acknowledge we've seen the request
	// This provides instant feedback like Cursor BugBot does
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := s.githubClient.AddReaction(ctx, owner, repo, event.Comment.ID, "eyes"); err != nil {
		log.Warn().Err(err).Msg("Failed to add eyes reaction")
	}
	cancel()

	// Check for duplicate requests (same PR + commit + mode within TTL)
	if s.deduplicator != nil {
		dedupKey := dedup.RequestKeyWithMode(owner, repo, prNumber, "", string(event.Command.Mode))
		if event.PullRequest != nil && event.PullRequest.Head != nil {
			dedupKey = dedup.RequestKeyWithMode(owner, repo, prNumber, event.PullRequest.Head.SHA, string(event.Command.Mode))
		}

		isDuplicate, waitChan := s.deduplicator.CheckAndMark(dedupKey)
		if isDuplicate {
			log.Info().
				Str("key", dedupKey).
				Msg("Duplicate request detected, skipping")
			// Optionally wait for the original request to complete
			if waitChan != nil {
				select {
				case <-waitChan:
					log.Debug().Str("key", dedupKey).Msg("Original request completed")
				case <-time.After(100 * time.Millisecond):
					// Don't block too long
				}
			}
			return nil
		}
		// Mark for completion when this function returns
		defer func() {
			s.deduplicator.Complete(dedupKey, nil)
		}()
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
		Int("concurrency", s.config.AsynqConcurrency).
		Int("rate_limit_tokens", s.config.RateLimitMaxTokens).
		Int("rate_limit_refill_sec", s.config.RateLimitRefillSec).
		Bool("cache_enabled", s.config.CacheEnabled).
		Bool("dedup_enabled", s.config.DedupEnabled).
		Int("retry_max_attempts", s.config.RetryMaxAttempts).
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
