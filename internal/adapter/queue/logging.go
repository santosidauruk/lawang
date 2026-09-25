package queue

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"
)

type safeTaskHandler struct {
	next   asynq.Handler
	logger *slog.Logger
}

func (h safeTaskHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	startedAt := time.Now()
	err := h.next.ProcessTask(ctx, task)
	taskID, _ := asynq.GetTaskID(ctx)
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, havingMaxRetry := asynq.GetMaxRetry(ctx)
	outcome := "success"

	switch {
	case errors.Is(err, asynq.SkipRetry):
		outcome = "permanent_failure"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		outcome = "cancelled"
	case err != nil && havingMaxRetry && retryCount >= maxRetry:
		outcome = "failed"
	case err != nil:
		outcome = "retry"
	}

	h.logger.Info("queue task completed",
		"task_id", taskID,
		"task_type", task.Type(),
		"attempt", retryCount+1,
		"max_attempts", maxRetry+1,
		"duration", time.Since(startedAt),
		"outcome", outcome)
	return err
}
