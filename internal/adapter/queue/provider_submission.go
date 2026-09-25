package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/santosidauruk/lawang/internal/application/outbox"
	"github.com/santosidauruk/lawang/internal/application/providersubmission"
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
