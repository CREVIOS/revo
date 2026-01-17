package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog/log"
	"github.com/yourusername/techy-bot/pkg/models"
)

// Load reads configuration from environment variables and files
func Load() (*models.Config, error) {
	// Load .env file if it exists (ignore error if not found)
	if err := godotenv.Load(); err != nil {
		log.Debug().Msg("No .env file found, using environment variables")
	}

	cfg := &models.Config{
		BotUsername: getEnvOrDefault("BOT_USERNAME", "techy"),
		ClaudeModel: getEnvOrDefault("CLAUDE_MODEL", "claude-sonnet-4-20250514"),
		MaxDiffSize: getEnvIntOrDefault("MAX_DIFF_SIZE", 100000),
		Port:        getEnvOrDefault("PORT", "8080"),
	}

	// Load GitHub App settings
	appID, err := strconv.ParseInt(os.Getenv("GITHUB_APP_ID"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid GITHUB_APP_ID: %w", err)
	}
	cfg.GitHubAppID = appID

	// Load GitHub private key
	privateKeyPath := getEnvOrDefault("GITHUB_PRIVATE_KEY_PATH", "/app/private-key.pem")
	privateKey, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read GitHub private key from %s: %w", privateKeyPath, err)
	}
	cfg.GitHubPrivateKey = privateKey

	// Load webhook secret
	cfg.GitHubWebhookSecret = os.Getenv("GITHUB_WEBHOOK_SECRET")
	if cfg.GitHubWebhookSecret == "" {
		return nil, fmt.Errorf("GITHUB_WEBHOOK_SECRET is required")
	}

	// Load Claude OAuth credentials
	if err := loadClaudeCredentials(cfg); err != nil {
		return nil, fmt.Errorf("failed to load Claude credentials: %w", err)
	}

	return cfg, nil
}

// loadClaudeCredentials loads Claude OAuth tokens from env vars or credentials file
func loadClaudeCredentials(cfg *models.Config) error {
	// First, try loading from environment variables
	cfg.ClaudeAccessToken = os.Getenv("CLAUDE_ACCESS_TOKEN")
	cfg.ClaudeRefreshToken = os.Getenv("CLAUDE_REFRESH_TOKEN")

	if expiresAt := os.Getenv("CLAUDE_EXPIRES_AT"); expiresAt != "" {
		exp, err := strconv.ParseInt(expiresAt, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid CLAUDE_EXPIRES_AT: %w", err)
		}
		cfg.ClaudeExpiresAt = exp
	}

	// If env vars are set, we're done
	if cfg.ClaudeAccessToken != "" && cfg.ClaudeRefreshToken != "" {
		log.Info().Msg("Loaded Claude credentials from environment variables")
		return nil
	}

	// Try loading from credentials file
	credFile := getEnvOrDefault("CLAUDE_CREDENTIALS_FILE", "")
	if credFile == "" {
		// Try default Claude credentials location
		homeDir, err := os.UserHomeDir()
		if err == nil {
			credFile = homeDir + "/.claude/.credentials.json"
		}
	}

	if credFile != "" {
		if creds, err := loadCredentialsFile(credFile); err == nil {
			cfg.ClaudeAccessToken = creds.AccessToken
			cfg.ClaudeRefreshToken = creds.RefreshToken
			cfg.ClaudeExpiresAt = creds.ExpiresAt
			cfg.ClaudeCredentialsFile = credFile
			log.Info().Str("file", credFile).Msg("Loaded Claude credentials from file")
			return nil
		} else {
			log.Debug().Err(err).Str("file", credFile).Msg("Could not load credentials file")
		}
	}

	if cfg.ClaudeAccessToken == "" {
		return fmt.Errorf("Claude credentials not found. Set CLAUDE_ACCESS_TOKEN and CLAUDE_REFRESH_TOKEN environment variables, or provide CLAUDE_CREDENTIALS_FILE")
	}

	return nil
}

// loadCredentialsFile reads and parses a Claude credentials JSON file
func loadCredentialsFile(path string) (*models.OAuthCredentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var creds models.OAuthCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}

	return &creds, nil
}

// getEnvOrDefault returns the environment variable value or a default
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvIntOrDefault returns the environment variable as int or a default
func getEnvIntOrDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
