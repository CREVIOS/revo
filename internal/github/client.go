package github

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/go-github/v60/github"
	"github.com/rs/zerolog/log"
)

// Client wraps the GitHub API client with app authentication
type Client struct {
	appID           int64
	privateKey      []byte
	installationIDs sync.Map // repo full name -> installation ID cache
}

// NewClient creates a new GitHub App client
func NewClient(appID int64, privateKey []byte) *Client {
	return &Client{
		appID:      appID,
		privateKey: privateKey,
	}
}

// createJWT creates a JWT for GitHub App authentication
func (c *Client) createJWT() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		Issuer:    fmt.Sprintf("%d", c.appID),
	}

	key, err := jwt.ParseRSAPrivateKeyFromPEM(c.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(key)
}

// GetInstallationClient returns a GitHub client authenticated as an installation
func (c *Client) GetInstallationClient(ctx context.Context, owner, repo string) (*github.Client, error) {
	fullName := fmt.Sprintf("%s/%s", owner, repo)

	// Check cache first
	if cached, ok := c.installationIDs.Load(fullName); ok {
		return c.clientForInstallation(ctx, cached.(int64))
	}

	// Get installation ID for the repository
	jwtToken, err := c.createJWT()
	if err != nil {
		return nil, err
	}

	// Create a client with JWT auth to find the installation
	transport := &jwtTransport{token: jwtToken}
	httpClient := &http.Client{Transport: transport}
	appClient := github.NewClient(httpClient)

	installation, _, err := appClient.Apps.FindRepositoryInstallation(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to find installation for %s: %w", fullName, err)
	}

	installationID := installation.GetID()
	c.installationIDs.Store(fullName, installationID)

	return c.clientForInstallation(ctx, installationID)
}

// clientForInstallation creates a client for a specific installation ID
func (c *Client) clientForInstallation(ctx context.Context, installationID int64) (*github.Client, error) {
	jwtToken, err := c.createJWT()
	if err != nil {
		return nil, err
	}

	// Create app client
	transport := &jwtTransport{token: jwtToken}
	httpClient := &http.Client{Transport: transport}
	appClient := github.NewClient(httpClient)

	// Get installation access token
	token, _, err := appClient.Apps.CreateInstallationToken(ctx, installationID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create installation token: %w", err)
	}

	// Create client with installation token
	installTransport := &tokenTransport{token: token.GetToken()}
	installClient := &http.Client{Transport: installTransport}

	return github.NewClient(installClient), nil
}

// GetPullRequestDiff fetches the diff for a pull request
func (c *Client) GetPullRequestDiff(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	client, err := c.GetInstallationClient(ctx, owner, repo)
	if err != nil {
		return "", err
	}

	// Get PR diff using raw media type
	diff, _, err := client.PullRequests.GetRaw(ctx, owner, repo, prNumber, github.RawOptions{
		Type: github.Diff,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get PR diff: %w", err)
	}

	return diff, nil
}

// GetPullRequest fetches pull request details
func (c *Client) GetPullRequest(ctx context.Context, owner, repo string, prNumber int) (*github.PullRequest, error) {
	client, err := c.GetInstallationClient(ctx, owner, repo)
	if err != nil {
		return nil, err
	}

	pr, _, err := client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to get PR: %w", err)
	}

	return pr, nil
}

// GetPullRequestFiles fetches the list of files changed in a PR
func (c *Client) GetPullRequestFiles(ctx context.Context, owner, repo string, prNumber int) ([]*github.CommitFile, error) {
	client, err := c.GetInstallationClient(ctx, owner, repo)
	if err != nil {
		return nil, err
	}

	files, _, err := client.PullRequests.ListFiles(ctx, owner, repo, prNumber, &github.ListOptions{
		PerPage: 100,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list PR files: %w", err)
	}

	return files, nil
}

// CreateComment posts a comment on an issue or PR
func (c *Client) CreateComment(ctx context.Context, owner, repo string, number int, body string) error {
	client, err := c.GetInstallationClient(ctx, owner, repo)
	if err != nil {
		return err
	}

	comment := &github.IssueComment{Body: github.String(body)}
	_, _, err = client.Issues.CreateComment(ctx, owner, repo, number, comment)
	if err != nil {
		return fmt.Errorf("failed to create comment: %w", err)
	}

	log.Info().
		Str("repo", fmt.Sprintf("%s/%s", owner, repo)).
		Int("pr", number).
		Msg("Posted review comment")

	return nil
}

// AddReaction adds a reaction to a comment
func (c *Client) AddReaction(ctx context.Context, owner, repo string, commentID int64, reaction string) error {
	client, err := c.GetInstallationClient(ctx, owner, repo)
	if err != nil {
		return err
	}

	_, _, err = client.Reactions.CreateIssueCommentReaction(ctx, owner, repo, commentID, reaction)
	if err != nil {
		return fmt.Errorf("failed to add reaction: %w", err)
	}

	return nil
}

// CreateReviewComment creates an inline comment on a specific line in a PR
func (c *Client) CreateReviewComment(ctx context.Context, owner, repo string, prNumber int, commitID, path, body string, line int) error {
	client, err := c.GetInstallationClient(ctx, owner, repo)
	if err != nil {
		return err
	}

	comment := &github.PullRequestComment{
		Body:     github.String(body),
		CommitID: github.String(commitID),
		Path:     github.String(path),
		Line:     github.Int(line),
	}

	_, _, err = client.PullRequests.CreateComment(ctx, owner, repo, prNumber, comment)
	if err != nil {
		return fmt.Errorf("failed to create review comment: %w", err)
	}

	log.Info().
		Str("repo", fmt.Sprintf("%s/%s", owner, repo)).
		Int("pr", prNumber).
		Str("file", path).
		Int("line", line).
		Msg("Posted inline review comment")

	return nil
}

// CreateReview creates a pull request review with multiple inline comments
func (c *Client) CreateReview(ctx context.Context, owner, repo string, prNumber int, commitID, body string, comments []*github.DraftReviewComment) error {
	client, err := c.GetInstallationClient(ctx, owner, repo)
	if err != nil {
		return err
	}

	review := &github.PullRequestReviewRequest{
		CommitID: github.String(commitID),
		Body:     github.String(body),
		Event:    github.String("COMMENT"),
		Comments: comments,
	}

	_, _, err = client.PullRequests.CreateReview(ctx, owner, repo, prNumber, review)
	if err != nil {
		return fmt.Errorf("failed to create review: %w", err)
	}

	log.Info().
		Str("repo", fmt.Sprintf("%s/%s", owner, repo)).
		Int("pr", prNumber).
		Int("comments", len(comments)).
		Msg("Posted review with inline comments")

	return nil
}

// jwtTransport adds JWT auth header to requests
type jwtTransport struct {
	token string
}

func (t *jwtTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	return http.DefaultTransport.RoundTrip(req)
}

// tokenTransport adds Bearer token auth header to requests
type tokenTransport struct {
	token string
}

func (t *tokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	return http.DefaultTransport.RoundTrip(req)
}
