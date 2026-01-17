package context

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-github/v60/github"
	"github.com/rs/zerolog/log"
	gh "github.com/yourusername/techy-bot/internal/github"
)

// PRContextBuilder interface for building context prompts
type PRContextBuilder interface {
	BuildContextPrompt() string
}

// PRContext holds contextual information about a PR for smarter reviews
type PRContext struct {
	ExistingComments []CommentInfo
	PreviousReviews  []ReviewInfo
	PRDescription    string
	Labels           []string
}

// CommentInfo represents an existing comment on the PR
type CommentInfo struct {
	Author    string
	Body      string
	FilePath  string
	Line      int
	IsBot     bool
	CreatedAt string
}

// ReviewInfo represents a previous review
type ReviewInfo struct {
	Author  string
	State   string // APPROVED, CHANGES_REQUESTED, COMMENTED
	Body    string
	BugCount int
}

// ContextAwareAnalyzer gathers context about a PR before reviewing
type ContextAwareAnalyzer struct {
	githubClient *gh.Client
	botUsername  string
}

// NewContextAwareAnalyzer creates a new context analyzer
func NewContextAwareAnalyzer(githubClient *gh.Client, botUsername string) *ContextAwareAnalyzer {
	return &ContextAwareAnalyzer{
		githubClient: githubClient,
		botUsername:  botUsername,
	}
}

// GatherContext collects context about the PR to avoid duplicates and be smarter
func (c *ContextAwareAnalyzer) GatherContext(ctx context.Context, owner, repo string, prNumber int) (PRContextBuilder, error) {
	prContext := &PRContext{
		ExistingComments: []CommentInfo{},
		PreviousReviews:  []ReviewInfo{},
		Labels:           []string{},
	}

	// Get GitHub client
	client, err := c.githubClient.GetInstallationClient(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitHub client: %w", err)
	}

	// Get PR details for labels and description
	pr, _, err := client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get PR details for context")
	} else {
		prContext.PRDescription = pr.GetBody()
		for _, label := range pr.Labels {
			prContext.Labels = append(prContext.Labels, label.GetName())
		}
	}

	// Get existing review comments (inline comments)
	reviewComments, _, err := client.PullRequests.ListComments(ctx, owner, repo, prNumber, &github.PullRequestListCommentsOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	})
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get review comments for context")
	} else {
		for _, comment := range reviewComments {
			isBot := comment.GetUser().GetType() == "Bot" ||
				strings.Contains(strings.ToLower(comment.GetUser().GetLogin()), "bot")

			prContext.ExistingComments = append(prContext.ExistingComments, CommentInfo{
				Author:    comment.GetUser().GetLogin(),
				Body:      comment.GetBody(),
				FilePath:  comment.GetPath(),
				Line:      comment.GetLine(),
				IsBot:     isBot,
				CreatedAt: comment.GetCreatedAt().Format("2006-01-02 15:04"),
			})
		}
	}

	// Get previous reviews
	reviews, _, err := client.PullRequests.ListReviews(ctx, owner, repo, prNumber, &github.ListOptions{PerPage: 50})
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get reviews for context")
	} else {
		for _, review := range reviews {
			bugCount := strings.Count(strings.ToLower(review.GetBody()), "bug") +
				strings.Count(strings.ToLower(review.GetBody()), "issue") +
				strings.Count(strings.ToLower(review.GetBody()), "🐛")

			prContext.PreviousReviews = append(prContext.PreviousReviews, ReviewInfo{
				Author:   review.GetUser().GetLogin(),
				State:    review.GetState(),
				Body:     review.GetBody(),
				BugCount: bugCount,
			})
		}
	}

	log.Info().
		Int("existing_comments", len(prContext.ExistingComments)).
		Int("previous_reviews", len(prContext.PreviousReviews)).
		Int("labels", len(prContext.Labels)).
		Msg("Gathered PR context for review")

	return prContext, nil
}

// BuildContextPrompt creates a prompt section with PR context
func (c *PRContext) BuildContextPrompt() string {
	var sb strings.Builder

	sb.WriteString("\n\n## PR CONTEXT (Read this to avoid duplicates)\n\n")

	// Existing comments summary
	if len(c.ExistingComments) > 0 {
		sb.WriteString("### Existing Comments\n")
		sb.WriteString(fmt.Sprintf("There are %d existing comments on this PR. **DO NOT** repeat issues already mentioned:\n\n", len(c.ExistingComments)))

		botComments := 0
		humanComments := 0
		for _, comment := range c.ExistingComments {
			if comment.IsBot {
				botComments++
			} else {
				humanComments++
			}
		}

		sb.WriteString(fmt.Sprintf("- Bot comments: %d\n", botComments))
		sb.WriteString(fmt.Sprintf("- Human comments: %d\n\n", humanComments))

		// Show recent comments
		showCount := 5
		if len(c.ExistingComments) < showCount {
			showCount = len(c.ExistingComments)
		}

		sb.WriteString("Recent comments to be aware of:\n")
		for i := 0; i < showCount; i++ {
			comment := c.ExistingComments[len(c.ExistingComments)-1-i]
			sb.WriteString(fmt.Sprintf("- [%s] %s:%d - %s\n",
				comment.Author,
				comment.FilePath,
				comment.Line,
				truncate(comment.Body, 100)))
		}
		sb.WriteString("\n")
	}

	// Previous reviews summary
	if len(c.PreviousReviews) > 0 {
		sb.WriteString("### Previous Reviews\n")
		approvals := 0
		changesRequested := 0
		totalBugsFound := 0

		for _, review := range c.PreviousReviews {
			if review.State == "APPROVED" {
				approvals++
			} else if review.State == "CHANGES_REQUESTED" {
				changesRequested++
			}
			totalBugsFound += review.BugCount
		}

		sb.WriteString(fmt.Sprintf("- Approvals: %d\n", approvals))
		sb.WriteString(fmt.Sprintf("- Changes requested: %d\n", changesRequested))
		sb.WriteString(fmt.Sprintf("- Estimated bugs mentioned in previous reviews: %d\n\n", totalBugsFound))
	}

	// Labels
	if len(c.Labels) > 0 {
		sb.WriteString("### PR Labels\n")
		sb.WriteString(strings.Join(c.Labels, ", "))
		sb.WriteString("\n\n")
	}

	sb.WriteString("**IMPORTANT**: Focus on NEW issues not already mentioned in existing comments. Be context-aware!\n")

	return sb.String()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
