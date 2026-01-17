package review

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/yourusername/techy-bot/internal/claude"
	gh "github.com/yourusername/techy-bot/internal/github"
	"github.com/yourusername/techy-bot/pkg/models"
)

// Reviewer handles code review requests
type Reviewer struct {
	githubClient *gh.Client
	claudeClient *claude.Client
	maxDiffSize  int
}

// NewReviewer creates a new code reviewer
func NewReviewer(githubClient *gh.Client, claudeClient *claude.Client, maxDiffSize int) *Reviewer {
	return &Reviewer{
		githubClient: githubClient,
		claudeClient: claudeClient,
		maxDiffSize:  maxDiffSize,
	}
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
	}

	// Get review from Claude
	review, err := r.claudeClient.ReviewCode(ctx, request)
	if err != nil {
		return r.postError(ctx, owner, repo, prNumber, event.Comment.ID, "Failed to get review from Claude", err)
	}

	// Format and post the review
	formattedReview := FormatReview(review, event.Command.Mode)
	if err := r.githubClient.CreateComment(ctx, owner, repo, prNumber, formattedReview); err != nil {
		return r.postError(ctx, owner, repo, prNumber, event.Comment.ID, "Failed to post review", err)
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
