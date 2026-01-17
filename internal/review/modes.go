package review

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/CREVIOS/revo/internal/claude"
	contextaware "github.com/CREVIOS/revo/internal/context"
	"github.com/CREVIOS/revo/internal/database"
	gh "github.com/CREVIOS/revo/internal/github"
	"github.com/CREVIOS/revo/pkg/models"
	"github.com/google/go-github/v60/github"
	"github.com/rs/zerolog/log"
)

// Reviewer handles code review requests
type Reviewer struct {
	githubClient    *gh.Client
	claudeClient    *claude.Client
	maxDiffSize     int
	contextAnalyzer ContextAnalyzer
	rateLimiter     RateLimiter
	store           ReviewStore
}

// ContextAnalyzer interface for gathering PR context
type ContextAnalyzer interface {
	GatherContext(ctx context.Context, owner, repo string, prNumber int) (contextaware.PRContextBuilder, error)
}

// RateLimiter interface for rate limiting
type RateLimiter interface {
	Wait(ctx context.Context) error
	Release()
}

// ReviewStore provides persistence hooks for review lifecycle events.
type ReviewStore interface {
	UpdateReview(id uint, updates map[string]interface{}) error
	CreateReviewComment(comment *database.ReviewComment) error
}

// NewReviewer creates a new code reviewer
func NewReviewer(githubClient *gh.Client, claudeClient *claude.Client, maxDiffSize int) *Reviewer {
	return &Reviewer{
		githubClient: githubClient,
		claudeClient: claudeClient,
		maxDiffSize:  maxDiffSize,
	}
}

// SetContextAnalyzer sets the context analyzer for smarter reviews
func (r *Reviewer) SetContextAnalyzer(analyzer ContextAnalyzer) {
	r.contextAnalyzer = analyzer
}

// SetRateLimiter sets the rate limiter
func (r *Reviewer) SetRateLimiter(limiter RateLimiter) {
	r.rateLimiter = limiter
}

// SetStore sets the persistence store for review metrics.
func (r *Reviewer) SetStore(store ReviewStore) {
	r.store = store
}

// ProcessReview handles a complete review request from webhook to GitHub comment
func (r *Reviewer) ProcessReview(ctx context.Context, event *gh.WebhookEvent) error {
	owner := event.Repository.Owner.Login
	repo := event.Repository.Name
	prNumber := event.PullRequest.Number
	reviewID := event.ReviewID
	processStart := time.Now()

	log.Info().
		Str("repo", fmt.Sprintf("%s/%s", owner, repo)).
		Int("pr", prNumber).
		Str("mode", string(event.Command.Mode)).
		Msg("Processing review request")

	if r.store != nil && reviewID > 0 {
		if err := r.store.UpdateReview(reviewID, map[string]interface{}{
			"status":     "processing",
			"started_at": processStart,
		}); err != nil {
			log.Warn().Err(err).Msg("Failed to update review status to processing")
		}
	}

	fail := func(message string, err error) error {
		if r.store != nil && reviewID > 0 {
			completedAt := time.Now()
			status := "failed"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				status = "cancelled"
			}
			updateErr := r.store.UpdateReview(reviewID, map[string]interface{}{
				"status":        status,
				"error_message": fmt.Sprintf("%s: %v", message, err),
				"completed_at":  completedAt,
				"duration_ms":   completedAt.Sub(processStart).Milliseconds(),
			})
			if updateErr != nil {
				log.Warn().Err(updateErr).Msg("Failed to update review status to failed")
			}
		}
		return r.postError(ctx, owner, repo, prNumber, event.Comment.ID, message, err)
	}

	// Add eyes reaction to indicate we're processing
	if err := r.githubClient.AddReaction(ctx, owner, repo, event.Comment.ID, "eyes"); err != nil {
		log.Warn().Err(err).Msg("Failed to add eyes reaction")
	}

	// Fetch PR details
	pr, err := r.githubClient.GetPullRequest(ctx, owner, repo, prNumber)
	if err != nil {
		return fail("Failed to fetch PR details", err)
	}
	if event.PullRequest != nil && event.PullRequest.Head != nil {
		expectedSHA := event.PullRequest.Head.SHA
		if expectedSHA != "" && pr.GetHead().GetSHA() != expectedSHA {
			if r.store != nil && reviewID > 0 {
				completedAt := time.Now()
				_ = r.store.UpdateReview(reviewID, map[string]interface{}{
					"status":        "cancelled",
					"error_message": fmt.Sprintf("stale commit: expected %s, got %s", expectedSHA, pr.GetHead().GetSHA()),
					"completed_at":  completedAt,
					"duration_ms":   completedAt.Sub(processStart).Milliseconds(),
				})
			}
			log.Info().
				Str("expected_sha", expectedSHA).
				Str("current_sha", pr.GetHead().GetSHA()).
				Msg("Skipping stale review task")
			return nil
		}
	}

	// Fetch PR diff
	diff, err := r.githubClient.GetPullRequestDiff(ctx, owner, repo, prNumber)
	if err != nil {
		return fail("Failed to fetch PR diff", err)
	}

	// Truncate diff if too large
	if len(diff) > r.maxDiffSize {
		log.Warn().
			Int("original_size", len(diff)).
			Int("max_size", r.maxDiffSize).
			Msg("Diff exceeds max size, truncating")
		diff = gh.TruncateDiff(diff, r.maxDiffSize)
	}

	// Fetch PR files
	ghFiles, err := r.githubClient.GetPullRequestFiles(ctx, owner, repo, prNumber)
	if err != nil {
		return fail("Failed to fetch PR files", err)
	}
	files := gh.ConvertGitHubFiles(ghFiles)

	// Gather context (existing comments, reviews) for smarter analysis
	var prContext contextaware.PRContextBuilder
	if r.contextAnalyzer != nil {
		prContext, err = r.contextAnalyzer.GatherContext(ctx, owner, repo, prNumber)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to gather PR context, continuing without it")
		}
	}

	// Build review request
	request := &models.ReviewRequest{
		Owner:       owner,
		Repo:        repo,
		PRNumber:    prNumber,
		Command:     *event.Command,
		CommentID:   event.Comment.ID,
		CommentBody: event.Comment.Body,
		Diff:        diff,
		PRTitle:     pr.GetTitle(),
		PRBody:      pr.GetBody(),
		Files:       files,
		PRContext:   prContext,
	}

	// Apply rate limiting before calling Claude Code CLI
	if r.rateLimiter != nil {
		log.Debug().Msg("Waiting for rate limiter")
		if err := r.rateLimiter.Wait(ctx); err != nil {
			return fail("Rate limit wait cancelled", err)
		}
		defer r.rateLimiter.Release()
	}

	// Get review from Claude
	review, err := r.claudeClient.ReviewCode(ctx, request)
	if err != nil {
		return fail("Failed to get review from Claude", err)
	}

	// Parse review for inline comments
	summary, inlineComments := ParseStructuredReview(review)
	commentsPosted := 0
	bugsFound := len(inlineComments)

	// Post inline comments if any were found
	if len(inlineComments) > 0 {
		// Get HEAD commit SHA for the PR
		headSHA := pr.GetHead().GetSHA()

		// Create GitHub draft review comments
		draftComments := make([]*github.DraftReviewComment, 0, len(inlineComments))
		for _, comment := range inlineComments {
			draftComments = append(draftComments, &github.DraftReviewComment{
				Path: github.String(comment.Path),
				Line: github.Int(comment.Line),
				Body: github.String(comment.Body),
			})
		}

		// Create a review with all inline comments
		reviewBody := fmt.Sprintf("## %s TechyBot %s\n\n%s\n\n---\n<sub>🤖 Powered by Claude Code CLI | Triggered by `@%s %s`</sub>",
			GetModeEmoji(event.Command.Mode),
			GetModeDescription(event.Command.Mode),
			summary,
			"techy",
			string(event.Command.Mode))

		if err := r.githubClient.CreateReview(ctx, owner, repo, prNumber, headSHA, reviewBody, draftComments); err != nil {
			log.Warn().Err(err).Msg("Failed to post review with inline comments, falling back to regular comment")
			// Fallback to regular comment if review posting fails
			formattedReview := FormatReview(review, event.Command.Mode)
			if err := r.githubClient.CreateComment(ctx, owner, repo, prNumber, formattedReview); err != nil {
				return fail("Failed to post review", err)
			}
			commentsPosted = 1
		} else {
			commentsPosted = len(inlineComments)
		}

		if r.store != nil && reviewID > 0 && commentsPosted == len(inlineComments) {
			for _, comment := range inlineComments {
				_ = r.store.CreateReviewComment(&database.ReviewComment{
					ReviewID: reviewID,
					FilePath: comment.Path,
					Line:     comment.Line,
					Severity: comment.Severity,
					Category: string(event.Command.Mode),
					Body:     comment.Body,
				})
			}
		}
	} else {
		// No inline comments found, post as regular comment
		formattedReview := FormatReview(review, event.Command.Mode)
		if err := r.githubClient.CreateComment(ctx, owner, repo, prNumber, formattedReview); err != nil {
			return fail("Failed to post review", err)
		}
		commentsPosted = 1
	}

	// Add checkmark reaction to indicate success
	if err := r.githubClient.AddReaction(ctx, owner, repo, event.Comment.ID, "rocket"); err != nil {
		log.Warn().Err(err).Msg("Failed to add rocket reaction")
	}

	log.Info().
		Str("repo", fmt.Sprintf("%s/%s", owner, repo)).
		Int("pr", prNumber).
		Msg("Review posted successfully")

	if r.store != nil && reviewID > 0 {
		completedAt := time.Now()
		updateErr := r.store.UpdateReview(reviewID, map[string]interface{}{
			"status":          "completed",
			"completed_at":    completedAt,
			"duration_ms":     completedAt.Sub(processStart).Milliseconds(),
			"diff_size":       len(diff),
			"files_changed":   len(files),
			"bugs_found":      bugsFound,
			"comments_posted": commentsPosted,
			"review_body":     review,
			"pr_title":        pr.GetTitle(),
			"commit_sha":      pr.GetHead().GetSHA(),
		})
		if updateErr != nil {
			log.Warn().Err(updateErr).Msg("Failed to update review metrics")
		}
	}

	return nil
}

// postError posts an error message as a comment and adds a confused reaction
func (r *Reviewer) postError(ctx context.Context, owner, repo string, prNumber int, commentID int64, message string, err error) error {
	log.Error().Err(err).Str("message", message).Msg("Review processing failed")

	// Add confused reaction
	if reactionErr := r.githubClient.AddReaction(ctx, owner, repo, commentID, "confused"); reactionErr != nil {
		log.Warn().Err(reactionErr).Msg("Failed to add confused reaction")
	}

	// Post error comment
	errorComment := fmt.Sprintf("❌ **TechyBot Error**\n\n%s: %v\n\nPlease try again or check the bot logs.", message, err)
	if postErr := r.githubClient.CreateComment(ctx, owner, repo, prNumber, errorComment); postErr != nil {
		log.Error().Err(postErr).Msg("Failed to post error comment")
	}

	return fmt.Errorf("%s: %w", message, err)
}

// GetModeDescription returns a human-readable description of the review mode
func GetModeDescription(mode models.ReviewMode) string {
	switch mode {
	case models.ModeHunt:
		return "Bug Hunt"
	case models.ModeSecurity:
		return "Security Audit"
	case models.ModePerformance:
		return "Performance Analysis"
	case models.ModeAnalyze:
		return "Deep Analysis"
	case models.ModeReview:
		return "Code Review"
	default:
		return "Code Review"
	}
}

// GetModeEmoji returns an emoji for the review mode
func GetModeEmoji(mode models.ReviewMode) string {
	switch mode {
	case models.ModeHunt:
		return "🐛"
	case models.ModeSecurity:
		return "🔒"
	case models.ModePerformance:
		return "⚡"
	case models.ModeAnalyze:
		return "🔬"
	case models.ModeReview:
		return "📝"
	default:
		return "📝"
	}
}
