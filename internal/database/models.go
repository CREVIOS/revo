package database

import (
	"time"

	"gorm.io/gorm"
)

// Review represents a completed code review
type Review struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// PR Information
	Owner     string `gorm:"index;not null" json:"owner"`
	Repo      string `gorm:"index;not null" json:"repo"`
	PRNumber  int    `gorm:"index;not null" json:"pr_number"`
	PRTitle   string `json:"pr_title"`
	CommitSHA string `gorm:"index" json:"commit_sha"`

	// Review Details
	Mode           string `gorm:"index;not null" json:"mode"`   // hunt, security, performance, etc.
	Status         string `gorm:"index;not null" json:"status"` // queued, processing, completed, failed, cancelled
	BugsFound      int    `json:"bugs_found"`
	CommentsPosted int    `json:"comments_posted"`
	ReviewBody     string `gorm:"type:text" json:"review_body,omitempty"`

	// Performance Metrics
	QueuedAt     time.Time  `json:"queued_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	DurationMs   int64      `json:"duration_ms"` // milliseconds
	DiffSize     int        `json:"diff_size"`
	FilesChanged int        `json:"files_changed"`

	// Error Tracking
	ErrorMessage string `gorm:"type:text" json:"error_message,omitempty"`
	RetryCount   int    `gorm:"default:0" json:"retry_count"`

	// User Information
	RequestedBy string `json:"requested_by"` // GitHub username who triggered review
	WorkerID    string `json:"worker_id"`    // Which worker processed this

	// Relationships
	Comments []ReviewComment `gorm:"foreignKey:ReviewID" json:"comments,omitempty"`
}

// ReviewComment represents an inline comment posted on a PR
type ReviewComment struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	ReviewID uint   `gorm:"index;not null" json:"review_id"`
	FilePath string `gorm:"not null" json:"file_path"`
	Line     int    `gorm:"not null" json:"line"`
	Severity string `json:"severity"` // error, warning, info
	Category string `json:"category"` // bug, security, performance, etc.
	Body     string `gorm:"type:text;not null" json:"body"`

	// GitHub metadata
	GitHubCommentID int64 `gorm:"index" json:"github_comment_id,omitempty"`
}

// Repository tracks repositories using TechyBot
type Repository struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	Owner     string `gorm:"uniqueIndex:idx_owner_repo;not null" json:"owner"`
	Name      string `gorm:"uniqueIndex:idx_owner_repo;not null" json:"name"`
	FullName  string `gorm:"index;not null" json:"full_name"` // owner/name
	IsPrivate bool   `gorm:"default:false" json:"is_private"`
	IsActive  bool   `gorm:"default:true;index" json:"is_active"`

	// Stats
	TotalReviews      int        `gorm:"default:0" json:"total_reviews"`
	TotalBugsFound    int        `gorm:"default:0" json:"total_bugs_found"`
	LastReviewAt      *time.Time `json:"last_review_at,omitempty"`
	AvgResponseTimeMs int64      `json:"avg_response_time_ms"`

	// Configuration
	AutoReviewEnabled bool   `gorm:"default:false" json:"auto_review_enabled"`
	DefaultMode       string `gorm:"default:'hunt'" json:"default_mode"`
	CustomRules       string `gorm:"type:text" json:"custom_rules,omitempty"`
}

// WebhookEvent tracks all webhook events received
type WebhookEvent struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`

	EventType string `gorm:"index;not null" json:"event_type"` // issue_comment, pull_request, etc.
	Owner     string `gorm:"index;not null" json:"owner"`
	Repo      string `gorm:"index;not null" json:"repo"`
	PRNumber  int    `gorm:"index" json:"pr_number"`
	Action    string `json:"action"` // opened, synchronize, created, etc.

	Payload     string     `gorm:"type:jsonb" json:"payload,omitempty"`
	ProcessedAt *time.Time `json:"processed_at,omitempty"`
	ReviewID    *uint      `gorm:"index" json:"review_id,omitempty"`
	Signature   string     `json:"signature,omitempty"`
}

// WorkerMetrics tracks worker performance
type WorkerMetrics struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`

	WorkerID       string    `gorm:"index;not null" json:"worker_id"`
	Hostname       string    `json:"hostname"`
	Status         string    `gorm:"index" json:"status"` // active, idle, stopped
	TasksProcessed int       `gorm:"default:0" json:"tasks_processed"`
	TasksFailed    int       `gorm:"default:0" json:"tasks_failed"`
	LastHeartbeat  time.Time `gorm:"index" json:"last_heartbeat"`
	AvgTaskTimeMs  int64     `json:"avg_task_time_ms"`
	CPUUsage       float64   `json:"cpu_usage"`
	MemoryUsageMB  int64     `json:"memory_usage_mb"`
}

// APIKey for authenticating external requests (for analytics API, etc.)
type APIKey struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	Key          string     `gorm:"uniqueIndex;not null" json:"key"`
	Name         string     `gorm:"not null" json:"name"`
	Description  string     `json:"description"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	IsActive     bool       `gorm:"default:true;index" json:"is_active"`
	RateLimit    int        `gorm:"default:100" json:"rate_limit"` // requests per hour
	RequestCount int64      `gorm:"default:0" json:"request_count"`
}
