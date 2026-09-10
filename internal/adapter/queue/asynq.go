package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/santosidauruk/lawang/internal/application/outbox"
)

type Publisher struct {
	client *asynq.Client
}

type providerSubmitPayload struct {
	SessionID uuid.UUID `json:"sessionId"`
}

func NewPublisher(client *asynq.Client) *Publisher {
	return &Publisher{client: client}
}

func (p *Publisher) Enqueue(
	ctx context.Context,
	task outbox.Task,
) error {
	if err := task.Validate(); err != nil {
		return err
	}

	payload, err := json.Marshal(providerSubmitPayload{
		SessionID: task.SessionID,
	})
	if err != nil {
		return err
	}

	asynqTask := asynq.NewTask(task.Type, payload)

	_, err = p.client.EnqueueContext(ctx, asynqTask, asynq.TaskID(task.ID.String()))
	switch {
	case errors.Is(err, asynq.ErrTaskIDConflict):
		return nil
	case err != nil:
		return fmt.Errorf("enqueue outbox task: %w", err)
	default:
		return nil
	}
}
