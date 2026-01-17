package database

import (
	"database/sql"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const (
	defaultMaxOpenConns    = 10
	defaultMaxIdleConns    = 5
	defaultConnMaxLifetime = time.Hour
	defaultPingAttempts    = 10
	defaultPingDelay       = 500 * time.Millisecond
)

// Connect opens a Postgres connection, verifies it, and runs migrations.
func Connect(dsn string) (*gorm.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql db: %w", err)
	}

	sqlDB.SetMaxOpenConns(defaultMaxOpenConns)
	sqlDB.SetMaxIdleConns(defaultMaxIdleConns)
	sqlDB.SetConnMaxLifetime(defaultConnMaxLifetime)

	if err := pingWithRetry(sqlDB, defaultPingAttempts, defaultPingDelay); err != nil {
		return nil, err
	}

	if err := db.AutoMigrate(
		&Review{},
		&ReviewComment{},
		&Repository{},
		&WebhookEvent{},
		&WorkerMetrics{},
		&APIKey{},
	); err != nil {
		return nil, fmt.Errorf("failed to auto-migrate: %w", err)
	}

	return db, nil
}

func pingWithRetry(db *sql.DB, attempts int, delay time.Duration) error {
	if attempts <= 0 {
		attempts = 1
	}
	if delay <= 0 {
		delay = 200 * time.Millisecond
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := db.Ping(); err == nil {
			return nil
		} else {
			lastErr = err
		}

		time.Sleep(delay)
		if delay < 5*time.Second {
			delay *= 2
		}
	}

	return fmt.Errorf("database ping failed after %d attempts: %w", attempts, lastErr)
}
