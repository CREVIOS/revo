package review

import (
	"context"
	"fmt"

	"github.com/google/go-github/v60/github"
	"github.com/rs/zerolog/log"
	"github.com/yourusername/techy-bot/internal/claude"
	contextaware "github.com/yourusername/techy-bot/internal/context"
	gh "github.com/yourusername/techy-bot/internal/github"
	"github.com/yourusername/techy-bot/pkg/models"
)

// Reviewer handles code review requests
type Reviewer struct {
	githubClient    *gh.Client
	claudeClient    *claude.Client
	maxDiffSize     int
	contextAnalyzer ContextAnalyzer
	rateLimiter     RateLimiter
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

// ProcessReview handles a complete review request from webhook to GitHub comment
func (r *Reviewer) ProcessReview(ctx context.Context, event *gh.WebhookEvent) error {
	owner := event.Repository.Owner.Login
	repo := event.Repository.Name
	prNumber := event.PullRequest.Number

	log.Info().
		Str("repo", fmt.Sprintf("%s/%s", owner, repo)).
		Int("pr", prNumber).
		Str("mode", string(event.Command.Mode)).
		Msg("Processing review request")

	// Add eyes reaction to indicate we're processing
	if err := r.githubClient.AddReaction(ctx, owner, repo, event.Comment.ID, "eyes"); err != nil {
		log.Warn().Err(err).Msg("Failed to add eyes reaction")
	}

	// Fetch PR details
	pr, err := r.githubClient.GetPullRequest(ctx, owner, repo, prNumber)
	if err != nil {
		return r.postError(ctx, owner, repo, prNumber, event.Comment.ID, "Failed to fetch PR details", err)
	}

	// Fetch PR diff
	diff, err := r.githubClient.GetPullRequestDiff(ctx, owner, repo, prNumber)
	if err != nil {
		return r.postError(ctx, owner, repo, prNumber, event.Comment.ID, "Failed to fetch PR diff", err)
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
		return r.postError(ctx, owner, repo, prNumber, event.Comment.ID, "Failed to fetch PR files", err)
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
			return r.postError(ctx, owner, repo, prNumber, event.Comment.ID, "Rate limit wait cancelled", err)
		}
		defer r.rateLimiter.Release()
	}

	// Get review from Claude
	review, err := r.claudeClient.ReviewCode(ctx, request)
	if err != nil {
		return r.postError(ctx, owner, repo, prNumber, event.Comment.ID, "Failed to get review from Claude", err)
	}

	// Parse review for inline comments
	summary, inlineComments := ParseStructuredReview(review)

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
				return r.postError(ctx, owner, repo, prNumber, event.Comment.ID, "Failed to post review", err)
			}
		}
	} else {
		// No inline comments found, post as regular comment
		formattedReview := FormatReview(review, event.Command.Mode)
		if err := r.githubClient.CreateComment(ctx, owner, repo, prNumber, formattedReview); err != nil {
			return r.postError(ctx, owner, repo, prNumber, event.Comment.ID, "Failed to post review", err)
		}
	}

	// Add checkmark reaction to indicate success
	if err := r.githubClient.AddReaction(ctx, owner, repo, event.Comment.ID, "rocket"); err != nil {
		log.Warn().Err(err).Msg("Failed to add rocket reaction")
	}

	log.Info().
		Str("repo", fmt.Sprintf("%s/%s", owner, repo)).
		Int("pr", prNumber).
		Msg("Review posted successfully")

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
