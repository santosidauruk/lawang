package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hibiken/asynq"
	"github.com/santosidauruk/lawang/internal/application/cleanup"
)

const TaskTypeStorageCleanup = "storage:cleanup"

type StorageCleanupTaskService interface {
	Run(ctx context.Context, input cleanup.Input) (cleanup.CleanupResult, error)
}

func NewStorageCleanupTask() *asynq.Task {
	return asynq.NewTask(TaskTypeStorageCleanup, []byte(`{}`))
}

var errInvalidStorageCleanupTask = errors.New("invalid storage cleanup task")

func decodeStorageCleanupTask(task *asynq.Task) error {
	if task == nil || task.Type() != TaskTypeStorageCleanup {
		return errInvalidStorageCleanupTask
	}

	decoder := json.NewDecoder(bytes.NewReader(task.Payload()))
	decoder.DisallowUnknownFields()

	var payload *struct{}
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return errInvalidStorageCleanupTask
	}
	if err := ensureTaskJSONEOF(decoder); err != nil {
		return errInvalidStorageCleanupTask
	}
	return nil

}

type StorageCleanupHandler struct {
	service   StorageCleanupTaskService
	batchSize int32
}

func NewStorageCleanupHandler(service StorageCleanupTaskService, batchSize int32) *StorageCleanupHandler {
	return &StorageCleanupHandler{
		service:   service,
		batchSize: batchSize,
	}
}

func (h *StorageCleanupHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if err := decodeStorageCleanupTask(task); err != nil {
		return fmt.Errorf("%w: invalid storage cleanup task", asynq.SkipRetry)
	}

	_, err := h.service.Run(ctx, cleanup.Input{Limit: h.batchSize})
	return classifyStorageCleanupError(err)
}

func classifyStorageCleanupError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, cleanup.ErrTransientCleanup):
		return errors.New("storage cleanup transient failure")
	case errors.Is(err, cleanup.ErrPermanentCleanup):
		return fmt.Errorf("%w: permanent storage cleanup failure", asynq.SkipRetry)
	default:
		return errors.New("storage cleanup failure")
	}
}
