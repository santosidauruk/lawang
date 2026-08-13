package artifact

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

const uploadIntentTTL = 5 * time.Minute

type UploadPresigner interface {
	PresignUpload(context.Context, string, time.Duration) (string, error)
}

type UploadIntentTransaction interface {
	LockSession(context.Context, uuid.UUID) (session.VerificationSession, error)
	SupersedePendingUploadIntent(context.Context, uuid.UUID, string, time.Time) error
	InsertUploadIntent(context.Context, UploadIntent) error
}

type UploadIntentTransactor interface {
	WithinUploadIntentTransaction(
		context.Context,
		func(UploadIntentTransaction) error,
	) error
}

type UploadIntentSessionReader interface {
	LoadSession(context.Context, uuid.UUID) (session.VerificationSession, error)
}

type CreatedUploadIntent struct {
	ID        uuid.UUID
	UploadURL string
}

type UploadIntentService struct {
	sessions     UploadIntentSessionReader
	transactions UploadIntentTransactor
	presigner    UploadPresigner
	tokens       session.TokenIssuer
	clock        session.Clock
}

func NewUploadIntentService(
	sessions UploadIntentSessionReader,
	transactions UploadIntentTransactor,
	presigner UploadPresigner,
	tokens session.TokenIssuer,
	clock session.Clock,
) *UploadIntentService {
	return &UploadIntentService{
		sessions:     sessions,
		transactions: transactions,
		presigner:    presigner,
		tokens:       tokens,
		clock:        clock,
	}
}

func (s *UploadIntentService) Create(
	ctx context.Context,
	sessionID uuid.UUID,
	rawToken string,
	kind string,
) (CreatedUploadIntent, error) {
	if kind != "identity_document" && kind != "biometric_capture" {
		return CreatedUploadIntent{}, &Error{Code: CodeInvalidUploadIntentKind}
	}

	storedSession, err := s.sessions.LoadSession(ctx, sessionID)
	if errors.Is(err, session.ErrSessionNotFound) {
		return CreatedUploadIntent{}, &session.Error{Code: session.CodeSessionNotFound}
	}
	if err != nil {
		return CreatedUploadIntent{}, err
	}
	if !s.tokens.Equal(s.tokens.Hash(rawToken), storedSession.ResumeTokenHash) {
		return CreatedUploadIntent{}, &session.Error{Code: session.CodeInvalidResumeToken}
	}

	createdAt := s.clock.Now()
	if !createdAt.Before(storedSession.ExpiresAt) {
		return CreatedUploadIntent{}, &session.Error{Code: session.CodeSessionExpired}
	}

	switch kind {
	case "identity_document":
		if storedSession.Status != session.StatusPersonalDetailsSubmitted {
			return CreatedUploadIntent{}, &Error{Code: CodeUploadIntentStale}
		}
	case "biometric_capture":
		if storedSession.Status != session.StatusIdentityDocumentUploaded {
			return CreatedUploadIntent{}, &Error{Code: CodeUploadIntentStale}
		}
	}

	intentID := uuid.New()
	storageKey := fmt.Sprintf(
		"verification-sessions/%s/%s/%s",
		sessionID,
		kind,
		intentID,
	)
	uploadURL, err := s.presigner.PresignUpload(ctx, storageKey, uploadIntentTTL)
	if err != nil {
		return CreatedUploadIntent{}, &Error{Code: CodeObjectStorageFailed}
	}

	err = s.transactions.WithinUploadIntentTransaction(
		ctx,
		func(tx UploadIntentTransaction) error {
			lockedSession, err := tx.LockSession(ctx, sessionID)
			if errors.Is(err, session.ErrSessionNotFound) {
				return &session.Error{Code: session.CodeSessionNotFound}
			}
			if err != nil {
				return err
			}
			if !s.tokens.Equal(s.tokens.Hash(rawToken), lockedSession.ResumeTokenHash) {
				return &session.Error{Code: session.CodeInvalidResumeToken}
			}
			if !createdAt.Before(lockedSession.ExpiresAt) {
				return &session.Error{Code: session.CodeSessionExpired}
			}
			if lockedSession.Status != storedSession.Status ||
				!lockedSession.ExpiresAt.Equal(storedSession.ExpiresAt) {
				return &Error{Code: CodeUploadIntentStale}
			}

			if err := tx.SupersedePendingUploadIntent(
				ctx,
				sessionID,
				kind,
				createdAt,
			); err != nil {
				return err
			}
			return tx.InsertUploadIntent(ctx, UploadIntent{
				ID:                    intentID,
				VerificationSessionID: sessionID,
				Kind:                  kind,
				StorageKey:            storageKey,
				Status:                "pending",
				CreatedAt:             createdAt,
				LatestStatusChangeAt:  createdAt,
				ExpiresAt:             createdAt.Add(uploadIntentTTL),
			})
		},
	)
	if err != nil {
		return CreatedUploadIntent{}, err
	}

	return CreatedUploadIntent{ID: intentID, UploadURL: uploadURL}, nil
}
