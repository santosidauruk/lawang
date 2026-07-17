package personaldetails

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang-go/internal/domain/verificationsession"
)

type Input struct {
	FullName       string
	DateOfBirth    time.Time
	IdentityNumber string
	Address        string
}

type PersonalDetails struct {
	SessionID uuid.UUID
	Input
	CreatedAt time.Time
}

type Transaction interface {
	LockSession(context.Context, uuid.UUID) (session.VerificationSession, error)
	LoadDetails(context.Context, uuid.UUID) (*PersonalDetails, error)
	InsertDetails(context.Context, PersonalDetails) error
	UpdateState(context.Context, uuid.UUID, verificationsession.State, verificationsession.State, time.Time) error
	AppendEvent(context.Context, session.AppendEventParams) error
}

type Transactor interface {
	WithinTransaction(context.Context, func(Transaction) error) error
}

type Service struct {
	transactions Transactor
	tokens       session.TokenIssuer
	clock        session.Clock
}

type ErrorCode string

const (
	CodeConflict          ErrorCode = "PERSONAL_DETAILS_CONFLICT"
	CodeIllegalTransition ErrorCode = "ILLEGAL_TRANSITION"
)

type Error struct {
	Code   ErrorCode
	ID     uuid.UUID
	From   verificationsession.State
	Action sessionevent.Type
}

func (e *Error) Error() string { return string(e.Code) }

func NewService(transactions Transactor, tokens session.TokenIssuer, clock session.Clock) *Service {
	return &Service{transactions: transactions, tokens: tokens, clock: clock}
}

func (s *Service) Submit(
	ctx context.Context,
	id uuid.UUID,
	rawToken string,
	input Input,
) (session.Summary, error) {
	var summary session.Summary
	err := s.transactions.WithinTransaction(ctx, func(tx Transaction) error {
		stored, err := tx.LockSession(ctx, id)
		if errors.Is(err, session.ErrSessionNotFound) {
			return &session.Error{Code: session.CodeSessionNotFound}
		}
		if err != nil {
			return err
		}
		if !s.tokens.Equal(s.tokens.Hash(rawToken), stored.ResumeTokenHash) {
			return &session.Error{Code: session.CodeInvalidResumeToken}
		}
		now := s.clock.Now()
		if !now.Before(stored.ExpiresAt) {
			return &session.Error{Code: session.CodeSessionExpired}
		}
		existing, err := tx.LoadDetails(ctx, id)
		if err != nil {
			return err
		}
		if existing != nil && existing.Input == input {
			summary = session.Summary{ID: stored.ID, Status: stored.Status, ExpiresAt: stored.ExpiresAt}
			return nil
		}
		if existing != nil {
			return &Error{Code: CodeConflict, ID: id}
		}
		next, err := verificationsession.Transition(stored.Status, sessionevent.SubmitPersonalDetails)
		if err != nil {
			return &Error{
				Code: CodeIllegalTransition, ID: id, From: stored.Status,
				Action: sessionevent.SubmitPersonalDetails,
			}
		}
		if err := tx.InsertDetails(ctx, PersonalDetails{
			SessionID: id, Input: input, CreatedAt: now,
		}); err != nil {
			return err
		}
		if err := tx.UpdateState(ctx, id, stored.Status, next, now); err != nil {
			return err
		}
		if err := tx.AppendEvent(ctx, session.AppendEventParams{
			SessionID: id, Type: sessionevent.SubmitPersonalDetails,
			Metadata: sessionevent.EmptyMetadata(), OccurredAt: now,
		}); err != nil {
			return err
		}
		summary = session.Summary{ID: id, Status: next, ExpiresAt: stored.ExpiresAt}
		return nil
	})
	return summary, err
}
