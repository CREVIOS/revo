package database

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store wraps database access for the application.
type Store struct {
	db *gorm.DB
}

// NewStore creates a new Store.
func NewStore(db *gorm.DB) *Store {
	return &Store{db: db}
}

// DB exposes the underlying gorm DB for handlers that need it.
func (s *Store) DB() *gorm.DB {
	return s.db
}

// CreateReview inserts a new review record.
func (s *Store) CreateReview(review *Review) error {
	return s.db.Create(review).Error
}

// UpdateReview updates a review by ID.
func (s *Store) UpdateReview(id uint, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}
	return s.db.Model(&Review{}).Where("id = ?", id).Updates(updates).Error
}

// CreateReviewComment inserts a new review comment record.
func (s *Store) CreateReviewComment(comment *ReviewComment) error {
	return s.db.Create(comment).Error
}

// UpsertRepository creates or updates a repository record.
func (s *Store) UpsertRepository(repo *Repository) error {
	return s.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "owner"}, {Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"full_name",
			"is_private",
			"is_active",
			"updated_at",
		}),
	}).Create(repo).Error
}

// CreateWebhookEvent inserts a webhook event record.
func (s *Store) CreateWebhookEvent(event *WebhookEvent) error {
	return s.db.Create(event).Error
}
