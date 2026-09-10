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

type Service struct {
	store  Store
	tokens TokenIssuer
	clock  Clock
}

func NewService(store Store, tokens TokenIssuer, clock Clock) *Service {
	return &Service{store: store, tokens: tokens, clock: clock}
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
	if !s.clock.Now().Before(stored.ExpiresAt) {
		return Summary{}, &Error{Code: CodeSessionExpired}
	}

	return Summary{ID: stored.ID, Status: stored.Status, ExpiresAt: stored.ExpiresAt}, nil
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
