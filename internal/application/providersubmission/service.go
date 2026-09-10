package providersubmission

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

type Transaction interface {
	LockSession(ctx context.Context, sessionID uuid.UUID) (session.VerificationSession, error)
	HasRequiredAcceptedArtifacts(ctx context.Context, sessionID uuid.UUID) (bool, error)
	SetPendingVerification(ctx context.Context, submittedAt time.Time, sessionID uuid.UUID) error
	InsertUnpublishedOutbox(ctx context.Context, outboxID uuid.UUID, sessionID uuid.UUID) error
	AppendEvent(ctx context.Context, event session.AppendEventParams) error
}

type Transactor interface {
	WithinTransaction(ctx context.Context, operation func(t Transaction) error) error
}

type Service struct {
	transactor Transactor
	tokens     session.TokenIssuer
	clock      session.Clock
}

type Result struct {
	ID       uuid.UUID
	Status   verificationsession.State
	Replayed bool
}

type ErrorCode string

const (
	CodeSubmissionNotReady ErrorCode = "SUBMISSION_NOT_READY"
	CodeIllegalTransition  ErrorCode = "ILLEGAL_TRANSITION"
)

type Error struct {
	Code   ErrorCode
	ID     uuid.UUID
	From   verificationsession.State
	Action sessionevent.Type
}

func (e *Error) Error() string { return string(e.Code) }

func NewService(transactor Transactor, tokens session.TokenIssuer, clock session.Clock) *Service {
	return &Service{
		transactor: transactor,
		tokens:     tokens,
		clock:      clock,
	}
}

func (s *Service) Submit(ctx context.Context, sessionID uuid.UUID, rawToken string) (Result, error) {
	var result Result
	err := s.transactor.WithinTransaction(ctx, func(t Transaction) error {
		lockedSession, err := t.LockSession(ctx, sessionID)
		if errors.Is(err, session.ErrSessionNotFound) {
			return &session.Error{Code: session.CodeSessionNotFound}
		}
		if err != nil {
			return err
		}

		if !s.tokens.Equal(s.tokens.Hash(rawToken), lockedSession.ResumeTokenHash) {
			return &session.Error{
				Code: session.CodeInvalidResumeToken,
			}
		}

		switch lockedSession.Status {
		case verificationsession.BiometricCaptureUploaded:
			now := s.clock.Now()
			if !now.Before(lockedSession.ExpiresAt) {
				return &session.Error{
					Code: session.CodeSessionExpired,
				}
			}
			hasRequiredArtifacts, err := t.HasRequiredAcceptedArtifacts(ctx, sessionID)
			if err != nil {
				return err
			}
			if !hasRequiredArtifacts {
				return &Error{Code: CodeSubmissionNotReady, ID: sessionID}
			}

			err = t.SetPendingVerification(ctx, now, sessionID)
			if err != nil {
				return err
			}

			err = t.AppendEvent(ctx, session.AppendEventParams{
				SessionID:  sessionID,
				Type:       sessionevent.SubmitSession,
				Metadata:   sessionevent.Metadata{},
				OccurredAt: now,
			})
			if err != nil {
				return err
			}

			outboxID := uuid.New()
			err = t.InsertUnpublishedOutbox(ctx, outboxID, sessionID)
			if err != nil {
				return err
			}

			result = Result{
				ID:       lockedSession.ID,
				Status:   verificationsession.VerificationPending,
				Replayed: false,
			}
			return nil
		case verificationsession.VerificationPending:
			result = Result{
				ID:       lockedSession.ID,
				Status:   lockedSession.Status,
				Replayed: true,
			}
			return nil
		default:
			return &Error{
				Code: CodeIllegalTransition, ID: sessionID,
				From: lockedSession.Status, Action: sessionevent.SubmitSession,
			}
		}
	})
	if err != nil {
		return Result{}, err
	}

	return result, nil
}
