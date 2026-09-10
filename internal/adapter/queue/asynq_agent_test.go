package queue_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/adapter/queue"
	"github.com/santosidauruk/lawang/internal/application/outbox"
)

func TestPublisherRejectsMalformedOrUnknownTasksBeforeRedis(t *testing.T) {
	tests := []struct {
		name string
		task outbox.Task
	}{
		{
			name: "missing task ID",
			task: outbox.Task{Type: outbox.TaskTypeProviderSubmit, SessionID: uuid.New()},
		},
		{
			name: "unknown task type",
			task: outbox.Task{ID: uuid.New(), Type: "provider:unknown", SessionID: uuid.New()},
		},
		{
			name: "missing session ID",
			task: outbox.Task{ID: uuid.New(), Type: outbox.TaskTypeProviderSubmit},
		},
	}

	publisher := queue.NewPublisher(nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := publisher.Enqueue(context.Background(), tt.task)
			if !errors.Is(err, outbox.ErrInvalidTask) {
				t.Fatalf("Enqueue() error = %v, want ErrInvalidTask", err)
			}
		})
	}
}
