package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/CREVIOS/revo/internal/database"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

func (s *Server) adminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.config.AdminAPIKey == "" {
			writeError(w, http.StatusServiceUnavailable, "admin API key not configured")
			return
		}

		key := r.Header.Get("X-Admin-API-Key")
		if key == "" {
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
				key = strings.TrimSpace(authHeader[7:])
			}
		}

		if key != s.config.AdminAPIKey {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "database not initialized")
		return
	}

	db := s.store.DB()

	var totalReviews int64
	if err := db.Model(&database.Review{}).Count(&totalReviews).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count reviews")
		return
	}

	statusCounts := map[string]int64{}
	for _, status := range []string{"queued", "processing", "completed", "failed", "cancelled"} {
		var count int64
		_ = db.Model(&database.Review{}).Where("status = ?", status).Count(&count).Error
		statusCounts[status] = count
	}

	modeCounts := map[string]int64{}
	for _, mode := range []string{"review", "hunt", "security", "performance", "analyze"} {
		var count int64
		_ = db.Model(&database.Review{}).Where("mode = ?", mode).Count(&count).Error
		modeCounts[mode] = count
	}

	var bugsSum sql.NullInt64
	_ = db.Model(&database.Review{}).Select("COALESCE(SUM(bugs_found),0)").Scan(&bugsSum)

	var commentsSum sql.NullInt64
	_ = db.Model(&database.Review{}).Select("COALESCE(SUM(comments_posted),0)").Scan(&commentsSum)

	var avgDuration sql.NullFloat64
	_ = db.Model(&database.Review{}).Select("AVG(duration_ms)").Scan(&avgDuration)

	var avgQueueMs sql.NullFloat64
	_ = db.Raw(`SELECT AVG(EXTRACT(EPOCH FROM (started_at - queued_at)) * 1000) FROM reviews WHERE started_at IS NOT NULL`).Scan(&avgQueueMs)

	var avgProcessingMs sql.NullFloat64
	_ = db.Raw(`SELECT AVG(EXTRACT(EPOCH FROM (completed_at - started_at)) * 1000) FROM reviews WHERE completed_at IS NOT NULL AND started_at IS NOT NULL`).Scan(&avgProcessingMs)

	var lastReviewAt sql.NullTime
	_ = db.Raw(`SELECT MAX(completed_at) FROM reviews`).Scan(&lastReviewAt)

	response := map[string]interface{}{
		"reviews_total":     totalReviews,
		"reviews_by_status": statusCounts,
		"reviews_by_mode":   modeCounts,
		"bugs_reported":     bugsSum.Int64,
		"comments_posted":   commentsSum.Int64,
		"avg_duration_ms":   avgDuration.Float64,
		"avg_queue_ms":      avgQueueMs.Float64,
		"avg_processing_ms": avgProcessingMs.Float64,
		"last_review_at":    nil,
		"queue":             s.queueInfo(),
		"rate_limiter":      s.rateLimiter.Stats(),
		"uptime":            time.Since(startTime).String(),
	}

	if lastReviewAt.Valid {
		response["last_review_at"] = lastReviewAt.Time
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) listReviewsHandler(w http.ResponseWriter, r *http.Request) {
	query := s.store.DB().Model(&database.Review{})

	if v := r.URL.Query().Get("status"); v != "" {
		query = query.Where("status = ?", v)
	}
	if v := r.URL.Query().Get("mode"); v != "" {
		query = query.Where("mode = ?", v)
	}
	if v := r.URL.Query().Get("owner"); v != "" {
		query = query.Where("owner = ?", v)
	}
	if v := r.URL.Query().Get("repo"); v != "" {
		query = query.Where("repo = ?", v)
	}
	if v := r.URL.Query().Get("pr_number"); v != "" {
		if prNumber, err := strconv.Atoi(v); err == nil {
			query = query.Where("pr_number = ?", prNumber)
		}
	}
	if v := r.URL.Query().Get("requested_by"); v != "" {
		query = query.Where("requested_by = ?", v)
	}

	if include := r.URL.Query().Get("include_comments"); include == "true" {
		query = query.Preload("Comments")
	}

	listWithPagination(w, r, query, &[]database.Review{})
}

func (s *Server) getReviewHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var review database.Review
	query := s.store.DB().Preload("Comments").First(&review, id)
	if err := query.Error; err != nil {
		handleDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, review)
}

func (s *Server) createReviewHandler(w http.ResponseWriter, r *http.Request) {
	var review database.Review
	if err := decodeJSON(r, &review); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	review.ID = 0
	if err := s.store.DB().Create(&review).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create review")
		return
	}

	writeJSON(w, http.StatusCreated, review)
}

func (s *Server) updateReviewHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	updates, err := decodeUpdates(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.DB().Model(&database.Review{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		handleDBError(w, err)
		return
	}

	var review database.Review
	if err := s.store.DB().First(&review, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, review)
}

func (s *Server) deleteReviewHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	if err := s.store.DB().Delete(&database.Review{}, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listReviewCommentsHandler(w http.ResponseWriter, r *http.Request) {
	query := s.store.DB().Model(&database.ReviewComment{})

	if v := r.URL.Query().Get("review_id"); v != "" {
		if reviewID, err := strconv.Atoi(v); err == nil {
			query = query.Where("review_id = ?", reviewID)
		}
	}
	if v := r.URL.Query().Get("category"); v != "" {
		query = query.Where("category = ?", v)
	}
	if v := r.URL.Query().Get("severity"); v != "" {
		query = query.Where("severity = ?", v)
	}

	listWithPagination(w, r, query, &[]database.ReviewComment{})
}

func (s *Server) getReviewCommentHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var comment database.ReviewComment
	if err := s.store.DB().First(&comment, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, comment)
}

func (s *Server) createReviewCommentHandler(w http.ResponseWriter, r *http.Request) {
	var comment database.ReviewComment
	if err := decodeJSON(r, &comment); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	comment.ID = 0
	if err := s.store.DB().Create(&comment).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create review comment")
		return
	}

	writeJSON(w, http.StatusCreated, comment)
}

func (s *Server) updateReviewCommentHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	updates, err := decodeUpdates(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.DB().Model(&database.ReviewComment{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		handleDBError(w, err)
		return
	}

	var comment database.ReviewComment
	if err := s.store.DB().First(&comment, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, comment)
}

func (s *Server) deleteReviewCommentHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	if err := s.store.DB().Delete(&database.ReviewComment{}, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRepositoriesHandler(w http.ResponseWriter, r *http.Request) {
	query := s.store.DB().Model(&database.Repository{})

	if v := r.URL.Query().Get("owner"); v != "" {
		query = query.Where("owner = ?", v)
	}
	if v := r.URL.Query().Get("name"); v != "" {
		query = query.Where("name = ?", v)
	}
	if v := r.URL.Query().Get("is_active"); v != "" {
		if isActive, err := strconv.ParseBool(v); err == nil {
			query = query.Where("is_active = ?", isActive)
		}
	}

	listWithPagination(w, r, query, &[]database.Repository{})
}

func (s *Server) getRepositoryHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var repo database.Repository
	if err := s.store.DB().First(&repo, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, repo)
}

func (s *Server) createRepositoryHandler(w http.ResponseWriter, r *http.Request) {
	var repo database.Repository
	if err := decodeJSON(r, &repo); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	repo.ID = 0
	if err := s.store.DB().Create(&repo).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create repository")
		return
	}

	writeJSON(w, http.StatusCreated, repo)
}

func (s *Server) updateRepositoryHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	updates, err := decodeUpdates(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.DB().Model(&database.Repository{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		handleDBError(w, err)
		return
	}

	var repo database.Repository
	if err := s.store.DB().First(&repo, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, repo)
}

func (s *Server) deleteRepositoryHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	if err := s.store.DB().Delete(&database.Repository{}, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listWebhookEventsHandler(w http.ResponseWriter, r *http.Request) {
	query := s.store.DB().Model(&database.WebhookEvent{})

	if v := r.URL.Query().Get("event_type"); v != "" {
		query = query.Where("event_type = ?", v)
	}
	if v := r.URL.Query().Get("action"); v != "" {
		query = query.Where("action = ?", v)
	}
	if v := r.URL.Query().Get("owner"); v != "" {
		query = query.Where("owner = ?", v)
	}
	if v := r.URL.Query().Get("repo"); v != "" {
		query = query.Where("repo = ?", v)
	}
	if v := r.URL.Query().Get("pr_number"); v != "" {
		if prNumber, err := strconv.Atoi(v); err == nil {
			query = query.Where("pr_number = ?", prNumber)
		}
	}
	if v := r.URL.Query().Get("review_id"); v != "" {
		if reviewID, err := strconv.Atoi(v); err == nil {
			query = query.Where("review_id = ?", reviewID)
		}
	}

	listWithPagination(w, r, query, &[]database.WebhookEvent{})
}

func (s *Server) getWebhookEventHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var evt database.WebhookEvent
	if err := s.store.DB().First(&evt, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, evt)
}

func (s *Server) createWebhookEventHandler(w http.ResponseWriter, r *http.Request) {
	var evt database.WebhookEvent
	if err := decodeJSON(r, &evt); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	evt.ID = 0
	if err := s.store.DB().Create(&evt).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create webhook event")
		return
	}

	writeJSON(w, http.StatusCreated, evt)
}

func (s *Server) updateWebhookEventHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	updates, err := decodeUpdates(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.DB().Model(&database.WebhookEvent{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		handleDBError(w, err)
		return
	}

	var evt database.WebhookEvent
	if err := s.store.DB().First(&evt, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, evt)
}

func (s *Server) deleteWebhookEventHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	if err := s.store.DB().Delete(&database.WebhookEvent{}, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listWorkerMetricsHandler(w http.ResponseWriter, r *http.Request) {
	query := s.store.DB().Model(&database.WorkerMetrics{})

	if v := r.URL.Query().Get("worker_id"); v != "" {
		query = query.Where("worker_id = ?", v)
	}
	if v := r.URL.Query().Get("status"); v != "" {
		query = query.Where("status = ?", v)
	}

	listWithPagination(w, r, query, &[]database.WorkerMetrics{})
}

func (s *Server) getWorkerMetricsHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var metrics database.WorkerMetrics
	if err := s.store.DB().First(&metrics, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, metrics)
}

func (s *Server) createWorkerMetricsHandler(w http.ResponseWriter, r *http.Request) {
	var metrics database.WorkerMetrics
	if err := decodeJSON(r, &metrics); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	metrics.ID = 0
	if err := s.store.DB().Create(&metrics).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create worker metrics")
		return
	}

	writeJSON(w, http.StatusCreated, metrics)
}

func (s *Server) updateWorkerMetricsHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	updates, err := decodeUpdates(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.DB().Model(&database.WorkerMetrics{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		handleDBError(w, err)
		return
	}

	var metrics database.WorkerMetrics
	if err := s.store.DB().First(&metrics, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, metrics)
}

func (s *Server) deleteWorkerMetricsHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	if err := s.store.DB().Delete(&database.WorkerMetrics{}, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAPIKeysHandler(w http.ResponseWriter, r *http.Request) {
	query := s.store.DB().Model(&database.APIKey{})

	if v := r.URL.Query().Get("name"); v != "" {
		query = query.Where("name = ?", v)
	}
	if v := r.URL.Query().Get("is_active"); v != "" {
		if isActive, err := strconv.ParseBool(v); err == nil {
			query = query.Where("is_active = ?", isActive)
		}
	}

	listWithPagination(w, r, query, &[]database.APIKey{})
}

func (s *Server) getAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var key database.APIKey
	if err := s.store.DB().First(&key, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, key)
}

func (s *Server) createAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	var key database.APIKey
	if err := decodeJSON(r, &key); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	key.ID = 0
	if err := s.store.DB().Create(&key).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create api key")
		return
	}

	writeJSON(w, http.StatusCreated, key)
}

func (s *Server) updateAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	updates, err := decodeUpdates(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.store.DB().Model(&database.APIKey{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		handleDBError(w, err)
		return
	}

	var key database.APIKey
	if err := s.store.DB().First(&key, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, key)
}

func (s *Server) deleteAPIKeyHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	if err := s.store.DB().Delete(&database.APIKey{}, id).Error; err != nil {
		handleDBError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func listWithPagination(w http.ResponseWriter, r *http.Request, query *gorm.DB, out interface{}) {
	limit, offset := parsePagination(r)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count records")
		return
	}

	if err := query.Order("id desc").Limit(limit).Offset(offset).Find(out).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list records")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":  out,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func parsePagination(r *http.Request) (int, int) {
	limit := defaultLimit
	offset := 0

	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			if parsed > 0 && parsed <= maxLimit {
				limit = parsed
			}
		}
	}

	if v := r.URL.Query().Get("offset"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	return limit, offset
}

func parseID(r *http.Request) (uint, error) {
	vars := mux.Vars(r)
	idStr := vars["id"]
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(id), nil
}

func decodeJSON(r *http.Request, dst interface{}) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return nil
}

func decodeUpdates(r *http.Request) (map[string]interface{}, error) {
	var updates map[string]interface{}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&updates); err != nil {
		return nil, err
	}

	if updates == nil {
		return nil, errors.New("empty update payload")
	}

	delete(updates, "id")
	delete(updates, "created_at")
	delete(updates, "updated_at")
	delete(updates, "deleted_at")

	return updates, nil
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Error().Err(err).Msg("Failed to write JSON response")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func handleDBError(w http.ResponseWriter, err error) {
	if err == gorm.ErrRecordNotFound {
		writeError(w, http.StatusNotFound, "record not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "database error")
}
