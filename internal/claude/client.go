package claude

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/CREVIOS/revo/pkg/models"
	"github.com/rs/zerolog/log"
)

// Client wraps the Claude Code CLI for code reviews
type Client struct {
	claudePath string
	model      string
}

// NewClient creates a new Claude Code CLI client
func NewClient(claudePath string, model string) *Client {
	if claudePath == "" {
		claudePath = "claude" // Use PATH
	}
	return &Client{
		claudePath: claudePath,
		model:      model,
	}
}

// ReviewCode performs a code review using Claude Code CLI
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
		Msg("Sending review request to Claude Code CLI")

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
	args = append(args, fullPrompt)

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
		return "", fmt.Errorf("Claude Code CLI error: %w, stderr: %s", err, stderr.String())
	}

	response := strings.TrimSpace(stdout.String())

	log.Debug().
		Int("response_length", len(response)).
		Msg("Received review response from Claude Code CLI")

	return response, nil
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
