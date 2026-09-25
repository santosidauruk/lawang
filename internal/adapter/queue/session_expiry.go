package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/santosidauruk/lawang/internal/application/expiry"
)

const TaskTypeSessionExpiry = "session:expire"

type SessionExpiryTaskService interface {
	Sweep(ctx context.Context) (expiry.SweepResult, error)
}

type SessionExpiryHandler struct {
	service SessionExpiryTaskService
}

func NewSessionExpiryHandler(service SessionExpiryTaskService) *SessionExpiryHandler {
	return &SessionExpiryHandler{service: service}
}

func NewSessionExpiryTask() *asynq.Task {
	return asynq.NewTask(TaskTypeSessionExpiry, []byte(`{}`), asynq.MaxRetry(0))
}

func (h *SessionExpiryHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if task.Type() != TaskTypeSessionExpiry {
		return fmt.Errorf("%w: unsupported task type", asynq.SkipRetry)
	}

	decoder := json.NewDecoder(bytes.NewReader(task.Payload()))
	decoder.DisallowUnknownFields()
	var payload *struct{}
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("%w: malformed expiry task payload", asynq.SkipRetry)
	}

	if payload == nil {
		return fmt.Errorf("%w: invalid null payload", asynq.SkipRetry)
	}

	if err := ensureTaskJSONEOF(decoder); err != nil {
		return fmt.Errorf("%w: invalid expiry task payload", asynq.SkipRetry)
	}

	_, err := h.service.Sweep(ctx)
	if err != nil {
		return err
	}

	return nil
}
