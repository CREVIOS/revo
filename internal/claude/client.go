package claude

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/CREVIOS/revo/internal/cache"
	"github.com/CREVIOS/revo/internal/retry"
	"github.com/CREVIOS/revo/pkg/models"
	"github.com/rs/zerolog/log"
)

// Client wraps the Claude Code CLI for code reviews with retry and caching
type Client struct {
	claudePath   string
	model        string
	retrier      *retry.Retrier
	promptCache  *cache.PromptCache
	enableCache  bool
}

// ClientOption configures the Client
type ClientOption func(*Client)

// WithRetryConfig sets custom retry configuration
func WithRetryConfig(cfg retry.Config) ClientOption {
	return func(c *Client) {
		c.retrier = retry.New(cfg)
	}
}

// WithPromptCache enables prompt caching with the given config
func WithPromptCache(cfg cache.Config) ClientOption {
	return func(c *Client) {
		c.promptCache = cache.NewPromptCache(cfg)
		c.enableCache = true
	}
}

// WithCacheEnabled enables or disables caching
func WithCacheEnabled(enabled bool) ClientOption {
	return func(c *Client) {
		c.enableCache = enabled
	}
}

// NewClient creates a new Claude Code CLI client
func NewClient(claudePath string, model string, opts ...ClientOption) *Client {
	if claudePath == "" {
		claudePath = "claude" // Use PATH
	}

	c := &Client{
		claudePath:  claudePath,
		model:       model,
		retrier:     retry.NewWithDefaults(),
		promptCache: cache.NewPromptCache(cache.DefaultConfig()),
		enableCache: true, // Enable by default
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// ReviewCode performs a code review using Claude Code CLI with retry and caching
func (c *Client) ReviewCode(ctx context.Context, request *models.ReviewRequest) (string, error) {
	// Get the appropriate system prompt for the review mode
	systemPrompt := GetSystemPrompt(request.Command.Mode)

	// Build the user message with PR context
	userMessage := buildUserMessage(request)

	// Add context if provided (to avoid duplicates)
	contextPrompt := ""
	if request.PRContext != nil {
		contextPrompt = request.PRContext.BuildContextPrompt()
	}

	// Combine system prompt, context, and user message
	fullPrompt := fmt.Sprintf("%s%s\n\n%s", systemPrompt, contextPrompt, userMessage)

	log.Debug().
		Str("mode", string(request.Command.Mode)).
		Int("diff_size", len(request.Diff)).
		Bool("cache_enabled", c.enableCache).
		Msg("Sending review request to Claude Code CLI")

	// Check cache first (for identical prompts)
	if c.enableCache && c.promptCache != nil {
		if cached, found := c.promptCache.Get(fullPrompt); found {
			log.Info().
				Str("repo", fmt.Sprintf("%s/%s", request.Owner, request.Repo)).
				Int("pr", request.PRNumber).
				Msg("Returning cached review response")
			return cached, nil
		}
	}

	// Execute with retry logic
	var response string
	err := c.retrier.Do(ctx, func(ctx context.Context) error {
		var err error
		response, err = c.executeClaudeCLI(ctx, fullPrompt)
		return err
	})

	if err != nil {
		return "", err
	}

	// Cache the successful response
	if c.enableCache && c.promptCache != nil {
		c.promptCache.Set(fullPrompt, response)
	}

	return response, nil
}

// executeClaudeCLI runs the Claude Code CLI command
func (c *Client) executeClaudeCLI(ctx context.Context, prompt string) (string, error) {
	// Prepare Claude Code CLI command
	args := []string{
		"-p",                             // Print mode (non-interactive)
		"--dangerously-skip-permissions", // Skip permission prompts
		"--no-session-persistence",       // Don't save session
		"--output-format", "text",        // Plain text output
	}

	// Add model if specified
	if c.model != "" {
		args = append(args, "--model", c.model)
	}

	// Add the prompt as the last argument
	args = append(args, prompt)

	// Execute Claude Code CLI
	cmd := exec.CommandContext(ctx, c.claudePath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	log.Debug().
		Str("command", c.claudePath).
		Strs("args", args[:len(args)-1]). // Don't log the full prompt
		Msg("Executing Claude Code CLI")

	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()

		// Check for rate limit indicators in stderr
		if strings.Contains(stderrStr, "429") ||
			strings.Contains(stderrStr, "rate limit") ||
			strings.Contains(stderrStr, "too many requests") ||
			strings.Contains(stderrStr, "overloaded") {
			return "", fmt.Errorf("%w: %s", retry.ErrRateLimited, stderrStr)
		}

		// Check for server errors
		if strings.Contains(stderrStr, "500") ||
			strings.Contains(stderrStr, "502") ||
			strings.Contains(stderrStr, "503") ||
			strings.Contains(stderrStr, "504") {
			return "", fmt.Errorf("%w: %s", retry.ErrServerError, stderrStr)
		}

		return "", fmt.Errorf("Claude Code CLI error: %w, stderr: %s", err, stderrStr)
	}

	response := strings.TrimSpace(stdout.String())

	log.Debug().
		Int("response_length", len(response)).
		Msg("Received review response from Claude Code CLI")

	return response, nil
}

// CacheStats returns the prompt cache statistics
func (c *Client) CacheStats() cache.CacheStats {
	if c.promptCache == nil {
		return cache.CacheStats{}
	}
	return c.promptCache.Stats()
}

// ClearCache clears the prompt cache
func (c *Client) ClearCache() {
	if c.promptCache != nil {
		c.promptCache.Clear()
	}
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
