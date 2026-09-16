package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/santosidauruk/lawang-go/internal/application/outbox"
	"github.com/santosidauruk/lawang-go/internal/application/providersubmission"
)

const MaxProviderRetries = 9

type RedisConfig struct {
	Address  string
	Password string
	Database int
}

type providerSubmitPayload struct {
	SessionID uuid.UUID `json:"sessionId"`
}

// [queue adapter] TODO: checkpoint 7 no 6
type ProviderSubmissionTaskService interface {
	SendToProvider(ctx context.Context, sessionID uuid.UUID) error
}

type ProviderSubmissionHandler struct {
	service ProviderSubmissionTaskService
}

func NewProviderSubmissionHandler(service ProviderSubmissionTaskService) *ProviderSubmissionHandler {
	return &ProviderSubmissionHandler{service: service}
}

func (h *ProviderSubmissionHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if task.Type() != outbox.TaskTypeProviderSubmit {
		return fmt.Errorf("%w: unsupported task type", asynq.SkipRetry)
	}

	decoder := json.NewDecoder(bytes.NewReader(task.Payload()))
	decoder.DisallowUnknownFields()
	var payload providerSubmitPayload
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("%w: malformed provider task payload", asynq.SkipRetry)
	}

	if err := ensureTaskJSONEOF(decoder); err != nil || payload.SessionID == uuid.Nil {
		return fmt.Errorf("%w: invalid provider task payload", asynq.SkipRetry)
	}

	err := h.service.SendToProvider(ctx, payload.SessionID)
	if errors.Is(err, providersubmission.ErrImmutableRecordsMissing) ||
		errors.Is(err, providersubmission.ErrPermanentProvider) ||
		errors.Is(err, providersubmission.ErrInvalidTask) {
		return errors.Join(asynq.SkipRetry, err)
	}

	return err
}

func ensureTaskJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return errors.New("task payload has trailing JSON")
}

type safeTaskHandler struct {
	next   asynq.Handler
	logger *slog.Logger
}

func (h safeTaskHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	startedAt := time.Now()
	err := h.next.ProcessTask(ctx, task)
	taskID, _ := asynq.GetTaskID(ctx)
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	outcome := "success"

	switch {
	case errors.Is(err, asynq.SkipRetry):
		outcome = "permanent_failure"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		outcome = "cancelled"
	case err != nil:
		outcome = "retry"
	}

	h.logger.Info("provider task completed",
		"task_id", taskID,
		"task_type", task.Type(),
		"attempt", retryCount+1,
		"max_attempts", maxRetry+1,
		"duration", time.Since(startedAt),
		"outcome", outcome)
	return err
}
