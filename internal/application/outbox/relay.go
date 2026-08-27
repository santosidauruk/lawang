package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const TaskTypeProviderSubmit = "provider:submit"

var (
	ErrNoOutboxAvailable  = errors.New("no outbox task available")
	ErrClaimOwnershipLost = errors.New("outbox claim ownership lost")
	ErrInvalidTask        = errors.New("invalid outbox task")
)

type Task struct {
	ID        uuid.UUID
	Type      string
	SessionID uuid.UUID
}

func (t Task) Validate() error {
	switch {
	case t.ID == uuid.Nil:
		return fmt.Errorf("%w: missing task ID", ErrInvalidTask)
	case t.Type != TaskTypeProviderSubmit:
		return fmt.Errorf("%w: unsupported task type", ErrInvalidTask)
	case t.SessionID == uuid.Nil:
		return fmt.Errorf("%w: missing session ID", ErrInvalidTask)
	default:
		return nil
	}
}

type ClaimedOutbox struct {
	Task       Task
	ClaimToken uuid.UUID
}

type OutboxStore interface {
	ClaimNext(ctx context.Context, claimedAt time.Time, claimToken uuid.UUID, claimedUntil time.Time) (ClaimedOutbox, error)
	MarkPublished(ctx context.Context, outboxID uuid.UUID, claimToken uuid.UUID) error
	RecordPublishFailure(ctx context.Context, outboxID uuid.UUID, claimToken uuid.UUID) error
}

type QueuePublisher interface {
	Enqueue(ctx context.Context, task Task) error
}

type Clock interface {
	Now() time.Time
}

type Relay struct {
	store         OutboxStore
	publisher     QueuePublisher
	clock         Clock
	leaseDuration time.Duration
}

func NewRelay(store OutboxStore, publisher QueuePublisher, clock Clock, leaseDuration time.Duration) *Relay {
	return &Relay{
		store:         store,
		publisher:     publisher,
		clock:         clock,
		leaseDuration: leaseDuration,
	}
}
func (r *Relay) RunOnce(ctx context.Context) error {
	claimedAt := r.clock.Now()
	claimedUntil := claimedAt.Add(r.leaseDuration)
	claimToken := uuid.New()
	claimedOutbox, err := r.store.ClaimNext(ctx, claimedAt, claimToken, claimedUntil)
	if errors.Is(err, ErrNoOutboxAvailable) {
		return nil
	}
	if err != nil {
		return err
	}

	publishErr := r.publisher.Enqueue(ctx, claimedOutbox.Task)
	if publishErr != nil {
		recordErr := r.store.RecordPublishFailure(ctx, claimedOutbox.Task.ID, claimedOutbox.ClaimToken)
		if recordErr != nil {
			return errors.Join(publishErr, recordErr)
		}
		return publishErr
	}

	return r.store.MarkPublished(ctx, claimedOutbox.Task.ID, claimedOutbox.ClaimToken)
}
