package models

import "time"

// ReviewMode represents the type of code review to perform
type ReviewMode string

const (
	ModeReview      ReviewMode = "review"
	ModeHunt        ReviewMode = "hunt"
	ModeSecurity    ReviewMode = "security"
	ModePerformance ReviewMode = "performance"
	ModeAnalyze     ReviewMode = "analyze"
)

// Command represents a parsed @techy command from a GitHub comment
type Command struct {
	Mode    ReviewMode
	Verbose bool
	Raw     string
}

// ReviewRequest contains all information needed to perform a code review
type ReviewRequest struct {
	Owner       string
	Repo        string
	PRNumber    int
	Command     Command
	CommentID   int64
	CommentBody string
	Diff        string
	PRTitle     string
	PRBody      string
	Files       []PRFile
}

// PRFile represents a file changed in a pull request
type PRFile struct {
	Filename    string
	Status      string // added, removed, modified, renamed
	Additions   int
	Deletions   int
	Changes     int
	Patch       string
	PreviousName string // for renamed files
}

// ReviewResponse contains the formatted review result
type ReviewResponse struct {
	Summary  string
	Comments []ReviewComment
	Reaction string
}

// ReviewComment represents a single review comment on a specific line
type ReviewComment struct {
	Path     string
	Line     int
	Side     string // LEFT or RIGHT
	Body     string
	Severity string // error, warning, info
}

// OAuthCredentials holds the Claude OAuth tokens
type OAuthCredentials struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    int64     `json:"expiresAt"`
	TokenType    string    `json:"tokenType,omitempty"`
}

// TokenResponse is the response from the OAuth token refresh endpoint
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// IsExpired checks if the token is expired or about to expire (within 5 minutes)
func (c *OAuthCredentials) IsExpired() bool {
	// ExpiresAt is in milliseconds
	expiresAt := time.UnixMilli(c.ExpiresAt)
	// Consider expired if within 5 minutes of expiration
	return time.Now().Add(5 * time.Minute).After(expiresAt)
}

// WebhookEvent represents a GitHub webhook event
type WebhookEvent struct {
	Action      string
	EventType   string
	Repository  Repository
	PullRequest *PullRequest
	Comment     *Comment
	Sender      User
}

// Repository represents a GitHub repository
type Repository struct {
	ID       int64
	Name     string
	FullName string
	Owner    User
	Private  bool
}

// PullRequest represents a GitHub pull request
type PullRequest struct {
	Number    int
	Title     string
	Body      string
	State     string
	Head      Branch
	Base      Branch
	User      User
	HTMLURL   string
	DiffURL   string
}

// Branch represents a git branch
type Branch struct {
	Ref  string
	SHA  string
	Repo Repository
}

// Comment represents a GitHub comment
type Comment struct {
	ID        int64
	Body      string
	User      User
	HTMLURL   string
	CreatedAt time.Time
}

// User represents a GitHub user
type User struct {
	ID    int64
	Login string
	Type  string
}

// Config holds all application configuration
type Config struct {
	// GitHub App settings
	GitHubAppID         int64
	GitHubPrivateKey    []byte
	GitHubWebhookSecret string

	// Claude OAuth settings
	ClaudeAccessToken  string
	ClaudeRefreshToken string
	ClaudeExpiresAt    int64
	ClaudeCredentialsFile string

	// Bot settings
	BotUsername string
	ClaudeModel string
	MaxDiffSize int

	// Server settings
	Port string
}
