package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/CREVIOS/revo/pkg/models"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog/log"
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

	// Load Claude Code CLI path
	cfg.ClaudePath = getEnvOrDefault("CLAUDE_PATH", "claude")
	cfg.ClaudeAccessToken = os.Getenv("CLAUDE_ACCESS_TOKEN")
	cfg.ClaudeRefreshToken = os.Getenv("CLAUDE_REFRESH_TOKEN")
	cfg.ClaudeCredentialsFile = os.Getenv("CLAUDE_CREDENTIALS_FILE")
	cfg.ClaudeExpiresAt = getEnvInt64OrDefault("CLAUDE_EXPIRES_AT", 0)

	// Load database configuration
	cfg.DatabaseURL = os.Getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	// Redis / Asynq configuration
	cfg.RedisAddr = getEnvOrDefault("REDIS_ADDR", "localhost:6379")
	cfg.RedisPassword = os.Getenv("REDIS_PASSWORD")
	cfg.RedisDB = getEnvIntOrDefault("REDIS_DB", 0)
	cfg.AsynqQueue = getEnvOrDefault("ASYNQ_QUEUE", "reviews")
	cfg.AsynqConcurrency = getEnvIntOrDefault("ASYNQ_CONCURRENCY", 3)
	cfg.AsynqMaxRetry = getEnvIntOrDefault("ASYNQ_MAX_RETRY", 10)

	// Load admin API key
	cfg.AdminAPIKey = os.Getenv("ADMIN_API_KEY")
	if cfg.AdminAPIKey == "" {
		return nil, fmt.Errorf("ADMIN_API_KEY is required")
	}

	return cfg, nil
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

// getEnvInt64OrDefault returns the environment variable as int64 or a default
func getEnvInt64OrDefault(key string, defaultValue int64) int64 {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.ParseInt(value, 10, 64); err == nil {
			return intVal
		}
	}
	return defaultValue
}
