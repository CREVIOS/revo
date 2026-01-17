package queue

import (
	"context"
	"fmt"
	"sync"
	"time"

	gh "github.com/CREVIOS/revo/internal/github"
	"github.com/rs/zerolog/log"
)

// ReviewJob represents a single review request
type ReviewJob struct {
	ID        string
	Owner     string
	Repo      string
	PRNumber  int
	CommitSHA string
	Event     *gh.WebhookEvent
	CreatedAt time.Time
	ctx       context.Context
	cancel    context.CancelFunc
}

// ReviewQueue manages concurrent PR reviews with worker pool
type ReviewQueue struct {
	jobs        chan *ReviewJob
	activeJobs  map[string]*ReviewJob // key: "owner/repo/pr"
	mu          sync.RWMutex
	workers     int
	maxQueueLen int
	processor   ReviewProcessor
}

// ReviewProcessor is the interface for processing reviews
type ReviewProcessor interface {
	ProcessReview(ctx context.Context, event *gh.WebhookEvent) error
}

// NewReviewQueue creates a new review queue with worker pool
func NewReviewQueue(workers int, maxQueueLen int, processor ReviewProcessor) *ReviewQueue {
	if workers <= 0 {
		workers = 3 // Default: 3 concurrent reviews
	}
	if maxQueueLen <= 0 {
		maxQueueLen = 50 // Default: max 50 queued reviews
	}

	return &ReviewQueue{
		jobs:        make(chan *ReviewJob, maxQueueLen),
		activeJobs:  make(map[string]*ReviewJob),
		workers:     workers,
		maxQueueLen: maxQueueLen,
		processor:   processor,
	}
}

// Start begins processing jobs with worker pool
func (q *ReviewQueue) Start(ctx context.Context) {
	log.Info().
		Int("workers", q.workers).
		Int("max_queue", q.maxQueueLen).
		Msg("Starting review queue with worker pool")

	for i := 0; i < q.workers; i++ {
		go q.worker(ctx, i)
	}
}

// worker processes jobs from the queue
func (q *ReviewQueue) worker(ctx context.Context, workerID int) {
	log.Debug().Int("worker_id", workerID).Msg("Worker started")

	for {
		select {
		case <-ctx.Done():
			log.Debug().Int("worker_id", workerID).Msg("Worker stopping")
			return
		case job := <-q.jobs:
			q.processJob(workerID, job)
		}
	}
}

// processJob executes a single review job
func (q *ReviewQueue) processJob(workerID int, job *ReviewJob) {
	log.Info().
		Int("worker_id", workerID).
		Str("job_id", job.ID).
		Str("repo", fmt.Sprintf("%s/%s", job.Owner, job.Repo)).
		Int("pr", job.PRNumber).
		Msg("Processing review job")

	// Track job as active
	q.mu.Lock()
	q.activeJobs[job.ID] = job
	q.mu.Unlock()

	// Clean up when done
	defer func() {
		q.mu.Lock()
		delete(q.activeJobs, job.ID)
		q.mu.Unlock()

		log.Info().
			Int("worker_id", workerID).
			Str("job_id", job.ID).
			Dur("duration", time.Since(job.CreatedAt)).
			Msg("Review job completed")
	}()

	// Process the review with context (can be cancelled)
	if err := q.processor.ProcessReview(job.ctx, job.Event); err != nil {
		log.Error().
			Err(err).
			Str("job_id", job.ID).
			Str("repo", fmt.Sprintf("%s/%s", job.Owner, job.Repo)).
			Int("pr", job.PRNumber).
			Msg("Review job failed")
	}
}

// Enqueue adds a new review job to the queue
func (q *ReviewQueue) Enqueue(event *gh.WebhookEvent) error {
	owner := event.Repository.Owner.Login
	repo := event.Repository.Name
	prNumber := event.PullRequest.Number
	commitSHA := event.PullRequest.Head.SHA

	jobID := fmt.Sprintf("%s/%s/%d", owner, repo, prNumber)

	// Cancel any existing review for this PR
	q.CancelStaleReview(owner, repo, prNumber)

	// Create new job with cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	job := &ReviewJob{
		ID:        jobID,
		Owner:     owner,
		Repo:      repo,
		PRNumber:  prNumber,
		CommitSHA: commitSHA,
		Event:     event,
		CreatedAt: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
	}

	// Try to enqueue (non-blocking)
	select {
	case q.jobs <- job:
		log.Info().
			Str("job_id", jobID).
			Int("queue_len", len(q.jobs)).
			Int("max_queue", q.maxQueueLen).
			Msg("Review job enqueued")
		return nil
	default:
		cancel() // Clean up context
		return fmt.Errorf("review queue is full (%d jobs), rejecting new review for PR #%d", q.maxQueueLen, prNumber)
	}
}

// CancelStaleReview cancels any ongoing review for the same PR
// This prevents reviewing outdated commits when new pushes arrive
func (q *ReviewQueue) CancelStaleReview(owner, repo string, prNumber int) {
	jobID := fmt.Sprintf("%s/%s/%d", owner, repo, prNumber)

	q.mu.Lock()
	defer q.mu.Unlock()

	if existingJob, exists := q.activeJobs[jobID]; exists {
		log.Info().
			Str("job_id", jobID).
			Str("commit", existingJob.CommitSHA).
			Msg("Cancelling stale review for PR")
		existingJob.cancel()
	}
}

// Stats returns current queue statistics
func (q *ReviewQueue) Stats() QueueStats {
	q.mu.RLock()
	defer q.mu.RUnlock()

	return QueueStats{
		QueueLength: len(q.jobs),
		ActiveJobs:  len(q.activeJobs),
		Workers:     q.workers,
		MaxQueueLen: q.maxQueueLen,
		Utilization: float64(len(q.activeJobs)) / float64(q.workers) * 100,
	}
}

// QueueStats holds queue statistics
type QueueStats struct {
	QueueLength int     `json:"queue_length"`
	ActiveJobs  int     `json:"active_jobs"`
	Workers     int     `json:"workers"`
	MaxQueueLen int     `json:"max_queue_len"`
	Utilization float64 `json:"utilization_percent"`
}
