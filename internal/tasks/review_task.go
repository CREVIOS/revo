package tasks

import (
	"encoding/json"

	"github.com/hibiken/asynq"
)

const TypeReview = "review:process"

// ReviewPayload is the task payload for processing a review.
type ReviewPayload struct {
	EventType   string `json:"event_type"`
	Action      string `json:"action"`
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	PRNumber    int    `json:"pr_number"`
	CommentID   int64  `json:"comment_id"`
	CommentBody string `json:"comment_body"`
	SenderLogin string `json:"sender_login"`
	Mode        string `json:"mode"`
	Verbose     bool   `json:"verbose"`
	CommitSHA   string `json:"commit_sha"`
	ReviewID    uint   `json:"review_id"`
}

func NewReviewTask(payload ReviewPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeReview, data), nil
}

func ParseReviewTask(task *asynq.Task) (ReviewPayload, error) {
	var payload ReviewPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return ReviewPayload{}, err
	}
	return payload, nil
}
