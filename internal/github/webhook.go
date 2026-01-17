package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/yourusername/techy-bot/pkg/models"
)

// WebhookHandler processes GitHub webhook events
type WebhookHandler struct {
	secret      string
	botUsername string
	onCommand   func(event *WebhookEvent) error
}

// WebhookEvent contains parsed webhook event data
type WebhookEvent struct {
	EventType   string
	Action      string
	Repository  *Repository
	PullRequest *PullRequest
	Comment     *Comment
	Sender      *User
	Command     *models.Command
}

// Repository represents GitHub repository data
type Repository struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Owner    *User  `json:"owner"`
	Private  bool   `json:"private"`
}

// PullRequest represents GitHub PR data
type PullRequest struct {
	Number  int     `json:"number"`
	Title   string  `json:"title"`
	Body    string  `json:"body"`
	State   string  `json:"state"`
	HTMLURL string  `json:"html_url"`
	DiffURL string  `json:"diff_url"`
	Head    *Branch `json:"head"`
	Base    *Branch `json:"base"`
	User    *User   `json:"user"`
}

// Branch represents a git branch reference
type Branch struct {
	Ref  string      `json:"ref"`
	SHA  string      `json:"sha"`
	Repo *Repository `json:"repo"`
}

// Comment represents a GitHub comment
type Comment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	User    *User  `json:"user"`
	HTMLURL string `json:"html_url"`
}

// User represents a GitHub user
type User struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}

// Issue represents a GitHub issue (used for PR comments on issues endpoint)
type Issue struct {
	Number      int          `json:"number"`
	PullRequest *PullRequest `json:"pull_request,omitempty"`
}

// webhookPayload is the raw webhook payload structure
type webhookPayload struct {
	Action      string       `json:"action"`
	Repository  *Repository  `json:"repository"`
	PullRequest *PullRequest `json:"pull_request"`
	Issue       *Issue       `json:"issue"`
	Comment     *Comment     `json:"comment"`
	Sender      *User        `json:"sender"`
}

// NewWebhookHandler creates a new webhook handler
func NewWebhookHandler(secret, botUsername string, onCommand func(event *WebhookEvent) error) *WebhookHandler {
	return &WebhookHandler{
		secret:      secret,
		botUsername: botUsername,
		onCommand:   onCommand,
	}
}

// HandleWebhook processes incoming webhook requests
func (h *WebhookHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to read webhook body")
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}

	// Verify signature
	signature := r.Header.Get("X-Hub-Signature-256")
	if !h.verifySignature(body, signature) {
		log.Warn().Msg("Invalid webhook signature")
		http.Error(w, "Invalid signature", http.StatusUnauthorized)
		return
	}

	// Get event type
	eventType := r.Header.Get("X-GitHub-Event")
	log.Debug().
		Str("event", eventType).
		Msg("Received webhook event")

	// Parse and handle event
	event, err := h.parseEvent(eventType, body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to parse webhook event")
		http.Error(w, "Failed to parse event", http.StatusBadRequest)
		return
	}

	// Check if this is a command we should handle
	if event == nil || event.Command == nil {
		// Not a command for us, acknowledge and return
		w.WriteHeader(http.StatusOK)
		return
	}

	// Process command asynchronously
	go func() {
		if err := h.onCommand(event); err != nil {
			log.Error().Err(err).Msg("Failed to process command")
		}
	}()

	w.WriteHeader(http.StatusAccepted)
}

// verifySignature verifies the HMAC-SHA256 signature
func (h *WebhookHandler) verifySignature(body []byte, signature string) bool {
	if signature == "" {
		return false
	}

	// Remove "sha256=" prefix
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	signature = strings.TrimPrefix(signature, "sha256=")

	// Compute expected signature
	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expected))
}

// parseEvent parses the webhook payload and extracts command if present
func (h *WebhookHandler) parseEvent(eventType string, body []byte) (*WebhookEvent, error) {
	var payload webhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	// Only handle comment events
	if eventType != "issue_comment" && eventType != "pull_request_review_comment" {
		return nil, nil
	}

	// Only handle created comments
	if payload.Action != "created" {
		return nil, nil
	}

	// Must have a comment
	if payload.Comment == nil {
		return nil, nil
	}

	// Parse command from comment body
	command := h.parseCommand(payload.Comment.Body)
	if command == nil {
		return nil, nil
	}

	event := &WebhookEvent{
		EventType:  eventType,
		Action:     payload.Action,
		Repository: payload.Repository,
		Comment:    payload.Comment,
		Sender:     payload.Sender,
		Command:    command,
	}

	// For issue comments, check if this is a PR and get PR number
	if eventType == "issue_comment" {
		if payload.Issue != nil && payload.Issue.PullRequest != nil {
			// This is a comment on a PR
			event.PullRequest = &PullRequest{
				Number: payload.Issue.Number,
			}
		} else {
			// Not a PR comment, ignore
			return nil, nil
		}
	} else {
		event.PullRequest = payload.PullRequest
	}

	log.Info().
		Str("repo", payload.Repository.FullName).
		Int("pr", event.PullRequest.Number).
		Str("mode", string(command.Mode)).
		Bool("verbose", command.Verbose).
		Msg("Parsed command from comment")

	return event, nil
}

// parseCommand extracts the @techy command from comment body
func (h *WebhookHandler) parseCommand(body string) *models.Command {
	// Match @botname <mode> [verbose]
	// Case-insensitive matching for the username
	pattern := fmt.Sprintf(`(?i)@%s\s+(\w+)(?:\s+(verbose))?`, regexp.QuoteMeta(h.botUsername))
	re := regexp.MustCompile(pattern)

	matches := re.FindStringSubmatch(body)
	if matches == nil {
		return nil
	}

	modeStr := strings.ToLower(matches[1])
	verbose := len(matches) > 2 && strings.ToLower(matches[2]) == "verbose"

	// Map mode string to ReviewMode
	var mode models.ReviewMode
	switch modeStr {
	case "review":
		mode = models.ModeReview
	case "hunt":
		mode = models.ModeHunt
	case "security":
		mode = models.ModeSecurity
	case "performance":
		mode = models.ModePerformance
	case "analyze":
		mode = models.ModeAnalyze
	default:
		// Unknown mode, default to review
		log.Warn().Str("mode", modeStr).Msg("Unknown review mode, defaulting to review")
		mode = models.ModeReview
	}

	return &models.Command{
		Mode:    mode,
		Verbose: verbose,
		Raw:     matches[0],
	}
}
