package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/CREVIOS/revo/internal/cache"
	"github.com/CREVIOS/revo/internal/claude"
	contextaware "github.com/CREVIOS/revo/internal/context"
	"github.com/CREVIOS/revo/internal/database"
	gh "github.com/CREVIOS/revo/internal/github"
	"github.com/CREVIOS/revo/internal/ratelimit"
	"github.com/CREVIOS/revo/internal/retry"
	"github.com/CREVIOS/revo/internal/review"
	"github.com/CREVIOS/revo/internal/tasks"
	"github.com/CREVIOS/revo/pkg/models"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"
)

// Run starts the background worker for processing review tasks.
func Run(cfg *models.Config) error {
	db, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	store := database.NewStore(db)

	githubClient := gh.NewClient(cfg.GitHubAppID, cfg.GitHubPrivateKey)

	// Initialize Claude client with retry and caching
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
	claudeClient := claude.NewClient(cfg.ClaudePath, cfg.ClaudeModel, claudeOpts...)

	contextAnalyzer := contextaware.NewContextAwareAnalyzer(githubClient, cfg.BotUsername)
	rateLimiter := ratelimit.NewLimiter(cfg.RateLimitMaxTokens, time.Duration(cfg.RateLimitRefillSec)*time.Second)

	reviewer := review.NewReviewer(githubClient, claudeClient, cfg.MaxDiffSize)
	reviewer.SetContextAnalyzer(contextAnalyzer)
	reviewer.SetRateLimiter(rateLimiter)
	reviewer.SetStore(store)

	redisOpt := asynq.RedisClientOpt{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	}

	server := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: cfg.AsynqConcurrency,
		Queues: map[string]int{
			cfg.AsynqQueue: 1,
		},
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(tasks.TypeReview, func(ctx context.Context, task *asynq.Task) error {
		payload, err := tasks.ParseReviewTask(task)
		if err != nil {
			return fmt.Errorf("invalid task payload: %w", err)
		}

		mode := models.ReviewMode(strings.ToLower(payload.Mode))
		if mode == "" {
			mode = models.ModeReview
		}

		event := &gh.WebhookEvent{
			EventType: payload.EventType,
			Action:    payload.Action,
			Repository: &gh.Repository{
				Owner: &gh.User{Login: payload.Owner},
				Name:  payload.Repo,
			},
			PullRequest: &gh.PullRequest{
				Number: payload.PRNumber,
				Head:   &gh.Branch{SHA: payload.CommitSHA},
			},
			Comment: &gh.Comment{
				ID:   payload.CommentID,
				Body: payload.CommentBody,
			},
			Sender: &gh.User{Login: payload.SenderLogin},
			Command: &models.Command{
				Mode:    mode,
				Verbose: payload.Verbose,
				Raw:     "@" + cfg.BotUsername + " " + payload.Mode,
			},
			ReviewID: payload.ReviewID,
		}

		return reviewer.ProcessReview(ctx, event)
	})

	log.Info().
		Int("concurrency", cfg.AsynqConcurrency).
		Str("queue", cfg.AsynqQueue).
		Int("rate_limit_tokens", cfg.RateLimitMaxTokens).
		Int("rate_limit_refill_sec", cfg.RateLimitRefillSec).
		Bool("cache_enabled", cfg.CacheEnabled).
		Int("retry_max_attempts", cfg.RetryMaxAttempts).
		Msg("TechyBot worker starting")

	return server.Run(mux)
}
