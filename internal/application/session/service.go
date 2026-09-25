package session

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
)

const applicantSessionTTL = 30 * time.Minute

type Status = verificationsession.State

const (
	StatusCreated                  = verificationsession.Created
	StatusPersonalDetailsSubmitted = verificationsession.PersonalDetailsSubmitted
	StatusIdentityDocumentUploaded = verificationsession.IdentityDocumentUploaded
	StatusBiometricCaptureUploaded = verificationsession.BiometricCaptureUploaded
	StatusVerificationPending      = verificationsession.VerificationPending
	StatusVerified                 = verificationsession.Verified
	StatusRejected                 = verificationsession.Rejected
	StatusExpired                  = verificationsession.Expired
)

type VerificationSession struct {
	ID                     uuid.UUID
	Status                 Status
	ResumeTokenHash        []byte
	ExpiresAt              time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
	VerificationDeadlineAt *time.Time
}

type CreateParams struct {
	ResumeTokenHash []byte
	ExpiresAt       time.Time
}

type Store interface {
	Create(context.Context, CreateParams) (VerificationSession, error)
	FindByID(context.Context, uuid.UUID) (VerificationSession, error)
}

type TokenIssuer interface {
	Issue() (raw string, hash []byte, err error)
	Hash(raw string) []byte
	Equal(left, right []byte) bool
}

type Clock interface {
	Now() time.Time
}

// ExpiryOperation is the shared lazy-expiry seam. Applicant services authenticate
// first, then invoke this operation for an overdue session instead of duplicating
// the guarded state transition and event append owned by package expiry.
type ExpiryOperation func(context.Context, uuid.UUID) error

type Service struct {
	store         Store
	tokens        TokenIssuer
	clock         Clock
	expireSession ExpiryOperation
}

func NewService(store Store, tokens TokenIssuer, clock Clock, expireSession ExpiryOperation) *Service {
	return &Service{store: store, tokens: tokens, clock: clock, expireSession: expireSession}
}

type CreatedSession struct {
	ID          uuid.UUID
	Status      Status
	ExpiresAt   time.Time
	ResumeToken string
}

type Summary struct {
	ID        uuid.UUID
	Status    Status
	ExpiresAt time.Time
}

func (s *Service) Create(ctx context.Context) (CreatedSession, error) {
	raw, hash, err := s.tokens.Issue()
	if err != nil {
		return CreatedSession{}, err
	}

	expiresAt := s.clock.Now().Add(applicantSessionTTL)
	created, err := s.store.Create(ctx, CreateParams{
		ResumeTokenHash: hash,
		ExpiresAt:       expiresAt,
	})
	if err != nil {
		return CreatedSession{}, err
	}

	return CreatedSession{
		ID:          created.ID,
		Status:      created.Status,
		ExpiresAt:   created.ExpiresAt,
		ResumeToken: raw,
	}, nil
}

func (s *Service) Resume(ctx context.Context, id uuid.UUID, rawToken string) (Summary, error) {
	stored, err := s.store.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return Summary{}, &Error{Code: CodeSessionNotFound}
		}
		return Summary{}, err
	}
	if !s.tokens.Equal(s.tokens.Hash(rawToken), stored.ResumeTokenHash) {
		return Summary{}, &Error{Code: CodeInvalidResumeToken}
	}

	if stored.Status == verificationsession.Expired {
		return Summary{}, &Error{Code: CodeSessionExpired}
	}

	due, err := IsExpiryDue(s.clock.Now(), stored)
	if err != nil {
		return Summary{}, err
	}
	if due {
		if err := s.expireSession(ctx, id); err != nil {
			return Summary{}, err
		}
		return Summary{}, &Error{Code: CodeSessionExpired}
	}

	return Summary{ID: stored.ID, Status: stored.Status, ExpiresAt: stored.ExpiresAt}, nil
}

func IsExpiryDue(now time.Time, stored VerificationSession) (bool, error) {
	var deadline time.Time
	switch stored.Status {
	case verificationsession.Created,
		verificationsession.PersonalDetailsSubmitted,
		verificationsession.IdentityDocumentUploaded,
		verificationsession.BiometricCaptureUploaded:
		deadline = stored.ExpiresAt
	case verificationsession.VerificationPending:
		if stored.VerificationDeadlineAt == nil {
			return false, errors.New("verification_pending session is missing verification deadline")
		}
		deadline = *stored.VerificationDeadlineAt
	case verificationsession.Verified,
		verificationsession.Rejected,
		verificationsession.Expired:
		return false, nil
	default:
		return false, errors.New("verification session has unknown status")
	}

	return !now.Before(deadline), nil
}

type ErrorCode string

const CodeInvalidResumeToken ErrorCode = "INVALID_RESUME_TOKEN"

const CodeSessionNotFound ErrorCode = "SESSION_NOT_FOUND"
const CodeSessionExpired ErrorCode = "SESSION_EXPIRED"

var ErrSessionNotFound = errors.New("session not found")

type Error struct {
	Code ErrorCode
}

func (e *Error) Error() string { return string(e.Code) }
