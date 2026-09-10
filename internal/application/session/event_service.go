package session

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

var ErrSessionTransitionStale = errors.New("verification session transition lost an expected-state race")

type AppendEventParams struct {
	SessionID  uuid.UUID
	Type       sessionevent.Type
	Metadata   sessionevent.Metadata
	OccurredAt time.Time
}

type EventTransaction interface {
	UpdateState(context.Context, uuid.UUID, verificationsession.State, verificationsession.State, time.Time) error
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
	return s.Transition(ctx, TransitionParams{
		SessionID: id, ExpectedState: verificationsession.Created,
		Action: sessionevent.SubmitPersonalDetails,
	})
}

type TransitionParams struct {
	SessionID     uuid.UUID
	ExpectedState verificationsession.State
	Action        sessionevent.Type
}

type ExpireParams struct {
	SessionID     uuid.UUID
	ExpectedState verificationsession.State
	Deadline      time.Time
}

func (s *EventService) Expire(ctx context.Context, params ExpireParams) error {
	now := s.clock.Now()
	next, err := verificationsession.Expire(params.ExpectedState, now, params.Deadline)
	if err != nil {
		return err
	}
	return s.commitTransition(ctx, params.SessionID, params.ExpectedState, next, sessionevent.Expire, now)
}

func (s *EventService) Transition(ctx context.Context, params TransitionParams) error {
	next, err := verificationsession.Transition(params.ExpectedState, params.Action)
	if err != nil {
		return err
	}
	occurredAt := s.clock.Now()
	return s.commitTransition(ctx, params.SessionID, params.ExpectedState, next, params.Action, occurredAt)
}

func (s *EventService) commitTransition(
	ctx context.Context,
	sessionID uuid.UUID,
	expected verificationsession.State,
	next verificationsession.State,
	action sessionevent.Type,
	occurredAt time.Time,
) error {
	return s.transactions.WithinTransaction(ctx, func(tx EventTransaction) error {
		if err := tx.UpdateState(ctx, sessionID, expected, next, occurredAt); err != nil {
			return err
		}
		return tx.AppendEvent(ctx, AppendEventParams{
			SessionID:  sessionID,
			Type:       action,
			Metadata:   sessionevent.EmptyMetadata(),
			OccurredAt: occurredAt,
		})
	})
}
