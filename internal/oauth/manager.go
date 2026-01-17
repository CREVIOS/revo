package oauth

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yourusername/techy-bot/pkg/models"
)

const (
	// Claude Code OAuth client ID
	ClaudeClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	// Token refresh endpoint
	TokenEndpoint = "https://console.anthropic.com/v1/oauth/token"
)

// Manager handles OAuth token lifecycle for Claude API
type Manager struct {
	mu              sync.RWMutex
	accessToken     string
	refreshToken    string
	expiresAt       time.Time
	credentialsFile string
	refreshing      bool
}

// NewManager creates a new OAuth token manager
func NewManager(cfg *models.Config) (*Manager, error) {
	m := &Manager{
		accessToken:     cfg.ClaudeAccessToken,
		refreshToken:    cfg.ClaudeRefreshToken,
		expiresAt:       time.UnixMilli(cfg.ClaudeExpiresAt),
		credentialsFile: cfg.ClaudeCredentialsFile,
	}

	// Check if token needs immediate refresh
	if m.isExpired() {
		log.Info().Msg("Token is expired, refreshing...")
		if err := m.Refresh(); err != nil {
			return nil, fmt.Errorf("failed to refresh expired token: %w", err)
		}
	}

	// Start background token refresh goroutine
	go m.backgroundRefresh()

	return m, nil
}

// GetAccessToken returns the current valid access token
// It automatically refreshes if the token is expired
func (m *Manager) GetAccessToken() (string, error) {
	m.mu.RLock()
	if !m.isExpiredUnlocked() {
		token := m.accessToken
		m.mu.RUnlock()
		return token, nil
	}
	m.mu.RUnlock()

	// Token is expired, need to refresh
	if err := m.Refresh(); err != nil {
		return "", fmt.Errorf("failed to refresh token: %w", err)
	}

	m.mu.RLock()
	token := m.accessToken
	m.mu.RUnlock()
	return token, nil
}

// isExpired checks if the token is expired (with 5 minute buffer)
func (m *Manager) isExpired() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isExpiredUnlocked()
}

// isExpiredUnlocked checks expiration without acquiring lock (caller must hold lock)
func (m *Manager) isExpiredUnlocked() bool {
	return time.Now().Add(5 * time.Minute).After(m.expiresAt)
}

// Refresh exchanges the refresh token for a new access token
func (m *Manager) Refresh() error {
	m.mu.Lock()
	// Check if another goroutine is already refreshing
	if m.refreshing {
		m.mu.Unlock()
		// Wait a bit and retry
		time.Sleep(100 * time.Millisecond)
		return nil
	}
	m.refreshing = true
	refreshToken := m.refreshToken
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.refreshing = false
		m.mu.Unlock()
	}()

	log.Info().Msg("Refreshing OAuth token...")

	newTokens, err := refreshOAuthToken(refreshToken)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.accessToken = newTokens.AccessToken
	if newTokens.RefreshToken != "" {
		m.refreshToken = newTokens.RefreshToken
	}
	m.expiresAt = time.Now().Add(time.Duration(newTokens.ExpiresIn) * time.Second)
	m.mu.Unlock()

	log.Info().
		Time("expires_at", m.expiresAt).
		Msg("OAuth token refreshed successfully")

	// Persist new tokens to file if configured
	if m.credentialsFile != "" {
		if err := m.persistCredentials(); err != nil {
			log.Warn().Err(err).Msg("Failed to persist credentials to file")
		}
	}

	return nil
}

// persistCredentials saves the current tokens to the credentials file
func (m *Manager) persistCredentials() error {
	m.mu.RLock()
	creds := models.OAuthCredentials{
		AccessToken:  m.accessToken,
		RefreshToken: m.refreshToken,
		ExpiresAt:    m.expiresAt.UnixMilli(),
		TokenType:    "Bearer",
	}
	m.mu.RUnlock()

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.credentialsFile, data, 0600)
}

// backgroundRefresh periodically checks and refreshes tokens before expiry
func (m *Manager) backgroundRefresh() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.RLock()
		// Refresh when 10 minutes or less until expiry
		shouldRefresh := time.Now().Add(10 * time.Minute).After(m.expiresAt)
		m.mu.RUnlock()

		if shouldRefresh {
			if err := m.Refresh(); err != nil {
				log.Error().Err(err).Msg("Background token refresh failed")
			}
		}
	}
}

// GetExpiresAt returns the token expiration time
func (m *Manager) GetExpiresAt() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.expiresAt
}
