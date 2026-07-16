package session

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
)

var ErrSessionTransitionRejected = errors.New("verification session transition rejected")

type AppendEventParams struct {
	SessionID  uuid.UUID
	Type       sessionevent.Type
	Metadata   sessionevent.Metadata
	OccurredAt time.Time
}

type EventTransaction interface {
	MarkPersonalDetailsSubmitted(context.Context, uuid.UUID, time.Time) error
	AppendEvent(context.Context, AppendEventParams) error
}

type EventTransactor interface {
	WithinTransaction(context.Context, func(EventTransaction) error) error
}

type EventService struct {
	transactions EventTransactor
	clock        Clock
}

func NewEventService(transactions EventTransactor, clock Clock) *EventService {
	return &EventService{transactions: transactions, clock: clock}
}

func (s *EventService) RecordPersonalDetailsSubmission(ctx context.Context, id uuid.UUID) error {
	occurredAt := s.clock.Now()
	return s.transactions.WithinTransaction(ctx, func(tx EventTransaction) error {
		if err := tx.MarkPersonalDetailsSubmitted(ctx, id, occurredAt); err != nil {
			return err
		}
		return tx.AppendEvent(ctx, AppendEventParams{
			SessionID:  id,
			Type:       sessionevent.SubmitPersonalDetails,
			Metadata:   sessionevent.EmptyMetadata(),
			OccurredAt: occurredAt,
		})
	})
}
