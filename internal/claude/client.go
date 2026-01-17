package claude

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/rs/zerolog/log"
	"github.com/yourusername/techy-bot/internal/oauth"
	"github.com/yourusername/techy-bot/pkg/models"
)

// Client wraps the Anthropic SDK with OAuth token injection
type Client struct {
	oauthManager *oauth.Manager
	model        string
}

// NewClient creates a new Claude API client
func NewClient(oauthManager *oauth.Manager, model string) *Client {
	return &Client{
		oauthManager: oauthManager,
		model:        model,
	}
}

// ReviewCode performs a code review using Claude
func (c *Client) ReviewCode(ctx context.Context, request *models.ReviewRequest) (string, error) {
	// Get current access token
	token, err := c.oauthManager.GetAccessToken()
	if err != nil {
		return "", fmt.Errorf("failed to get access token: %w", err)
	}

	// Create client with current token
	client := anthropic.NewClient(
		option.WithAPIKey(token),
	)

	// Get the appropriate system prompt for the review mode
	systemPrompt := GetSystemPrompt(request.Command.Mode)

	// Build the user message with PR context
	userMessage := buildUserMessage(request)

	log.Debug().
		Str("mode", string(request.Command.Mode)).
		Int("diff_size", len(request.Diff)).
		Msg("Sending review request to Claude")

	// Make the API call
	message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model(c.model)),
		MaxTokens: anthropic.F(int64(4096)),
		System: anthropic.F([]anthropic.TextBlockParam{
			anthropic.NewTextBlock(systemPrompt),
		}),
		Messages: anthropic.F([]anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userMessage)),
		}),
	})
	if err != nil {
		return "", fmt.Errorf("Claude API error: %w", err)
	}

	// Extract text response
	var response strings.Builder
	for _, block := range message.Content {
		if block.Type == anthropic.ContentBlockTypeText {
			response.WriteString(block.Text)
		}
	}

	log.Debug().
		Int("response_length", response.Len()).
		Msg("Received review response from Claude")

	return response.String(), nil
}

// buildUserMessage constructs the user message with PR context
func buildUserMessage(request *models.ReviewRequest) string {
	var sb strings.Builder

	sb.WriteString("## Pull Request\n\n")
	sb.WriteString(fmt.Sprintf("**Repository:** %s/%s\n", request.Owner, request.Repo))
	sb.WriteString(fmt.Sprintf("**PR #%d:** %s\n\n", request.PRNumber, request.PRTitle))

	if request.PRBody != "" {
		sb.WriteString("### Description\n")
		sb.WriteString(request.PRBody)
		sb.WriteString("\n\n")
	}

	sb.WriteString("### Files Changed\n\n")
	for _, file := range request.Files {
		status := file.Status
		if status == "" {
			status = "modified"
		}
		sb.WriteString(fmt.Sprintf("- `%s` (%s, +%d/-%d)\n",
			file.Filename, status, file.Additions, file.Deletions))
	}
	sb.WriteString("\n")

	sb.WriteString("### Diff\n\n")
	sb.WriteString("```diff\n")
	sb.WriteString(request.Diff)
	sb.WriteString("\n```\n")

	if request.Command.Verbose {
		sb.WriteString("\n**Note:** Verbose mode enabled. Please provide detailed analysis.\n")
	}

	return sb.String()
}
