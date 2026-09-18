// Package expiry owns the application operation that expires an overdue
// Verification Session. The operation itself is intentionally left for the
// Issue 010 Checkpoint 1 user-owned implementation.
package expiry

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

var (
	ErrMultipleExpireSession = errors.New("multiple session expired")
)

type Transactor interface {
	WithinTransaction(context.Context, func(Transaction) error) error
}

type Transaction interface {
	LockSession(ctx context.Context, sessionID uuid.UUID) (session.VerificationSession, error)
	GuardExpiry(ctx context.Context, sessionID uuid.UUID, currentState verificationsession.State, expiredAt time.Time) (bool, error)
	AppendEvent(ctx context.Context, event session.AppendEventParams) error
}

type Service struct {
	transactor Transactor
	clock      session.Clock
}

type Result struct {
	ID      uuid.UUID
	Status  verificationsession.State
	Changed bool
}

func NewService(transactor Transactor, clock session.Clock) *Service {
	return &Service{transactor: transactor, clock: clock}
}

type Input struct {
	SessionID uuid.UUID
}

func (s *Service) Expire(ctx context.Context, input Input) (Result, error) {
	var result Result
	err := s.transactor.WithinTransaction(ctx, func(t Transaction) error {
		lockedSession, err := t.LockSession(ctx, input.SessionID)
		if errors.Is(err, session.ErrSessionNotFound) {
			return &session.Error{Code: session.CodeSessionNotFound}
		}
		if err != nil {
			return err
		}

		if lockedSession.Status == verificationsession.Expired {
			result = Result{
				ID:      lockedSession.ID,
				Status:  verificationsession.Expired,
				Changed: false,
			}
			return nil
		}

		deadline := lockedSession.ExpiresAt
		if lockedSession.Status == verificationsession.VerificationPending {
			if lockedSession.VerificationDeadlineAt == nil {
				return errors.New("invalid deadline")
			}
			deadline = *lockedSession.VerificationDeadlineAt
		}

		now := s.clock.Now()
		nextState, err := verificationsession.Expire(lockedSession.Status, now, deadline)
		if err != nil {
			return err
		}

		if nextState != verificationsession.Expired {
			return errors.New("verification status must be expired")
		}

		hasExpired, err := t.GuardExpiry(ctx, lockedSession.ID, lockedSession.Status, now)
		if err != nil {
			return err
		}

		if !hasExpired {
			return session.ErrSessionTransitionStale
		}

		err = t.AppendEvent(ctx, session.AppendEventParams{
			SessionID:  lockedSession.ID,
			Type:       sessionevent.Expire,
			Metadata:   sessionevent.EmptyMetadata(),
			OccurredAt: now,
		})
		if err != nil {
			return err
		}

		result = Result{
			ID:      lockedSession.ID,
			Status:  verificationsession.Expired,
			Changed: true,
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}

	return result, nil
}
