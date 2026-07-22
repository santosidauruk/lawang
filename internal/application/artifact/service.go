package artifact

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/personaldetails"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang-go/internal/domain/verificationsession"
)

type UploadIntent struct {
	ID                    uuid.UUID
	VerificationSessionID uuid.UUID
	Kind                  string
	StorageKey            string
	Status                string
	CreatedAt             time.Time
	LatestStatusChangeAt  time.Time
	ExpiresAt             time.Time
	ConfirmedAt           *time.Time
	ObjectDeletedAt       *time.Time
	FailureCode           *string
}

type VerificationArtifact struct {
	ID                    uuid.UUID
	VerificationSessionID uuid.UUID
	UploadIntentID        uuid.UUID
	Kind                  string
	StorageKey            string
	ContentType           string
	SizeBytes             int64
	ETag                  string
	CreatedAt             time.Time
}

type ObjectMetadata struct {
	ContentType string
	SizeBytes   int64
	ETag        string
}

type DocumentExtraction struct {
	IdentityNumber string
}

type ErrorCode string

const CodeLocalValidationFailed ErrorCode = "LOCAL_VALIDATION_FAILED"

type FailureReason string

const ReasonIdentityNumberMismatch FailureReason = "identity_number_mismatch"

type Error struct {
	Code   ErrorCode
	Reason FailureReason
}

func (e *Error) Error() string { return string(e.Code) }

type ObjectStorage interface {
	HeadObject(context.Context, string) (ObjectMetadata, error)
}

type DocumentExtractor interface {
	Extract(context.Context, string) (DocumentExtraction, error)
}

type Transaction interface {
	LockSession(context.Context, uuid.UUID) (session.VerificationSession, error)
	LockUploadIntent(context.Context, uuid.UUID, uuid.UUID) (UploadIntent, error)
	LoadPersonalDetails(context.Context, uuid.UUID) (personaldetails.PersonalDetails, error)
	ConfirmUploadIntent(context.Context, uuid.UUID, time.Time) error
	InsertVerificationArtifact(context.Context, VerificationArtifact) error
	UpdateSessionState(context.Context, uuid.UUID, verificationsession.State, verificationsession.State, time.Time) error
	AppendEvent(context.Context, session.AppendEventParams) error
	MarkUploadIntentValidationFailed(ctx context.Context, uploadIntentID uuid.UUID, failureCode string, updatedAt time.Time) error
}

type Transactor interface {
	WithinTransaction(context.Context, func(Transaction) error) error
}

type Reader interface {
	LoadSession(context.Context, uuid.UUID) (session.VerificationSession, error)
	LoadUploadIntent(context.Context, uuid.UUID, uuid.UUID) (UploadIntent, error)
	LoadPersonalDetails(context.Context, uuid.UUID) (personaldetails.PersonalDetails, error)
}

type Service struct {
	reader        Reader
	transactions  Transactor
	objectStorage ObjectStorage
	extractor     DocumentExtractor
	tokens        session.TokenIssuer
	clock         session.Clock
}

func NewService(
	reader Reader,
	transactions Transactor,
	objectStorage ObjectStorage,
	extractor DocumentExtractor,
	tokens session.TokenIssuer,
	clock session.Clock,
) *Service {
	return &Service{
		reader:        reader,
		transactions:  transactions,
		objectStorage: objectStorage,
		extractor:     extractor,
		tokens:        tokens,
		clock:         clock,
	}
}

func (s *Service) Confirm(ctx context.Context, sessionID uuid.UUID, rawToken string, uploadIntentID uuid.UUID) (session.Summary, error) {
	storedSession, err := s.reader.LoadSession(ctx, sessionID)
	if err != nil {
		return session.Summary{}, err
	}

	if !s.tokens.Equal(s.tokens.Hash(rawToken), storedSession.ResumeTokenHash) {
		return session.Summary{}, &session.Error{
			Code: session.CodeInvalidResumeToken,
		}
	}

	startedAt := s.clock.Now()
	if !startedAt.Before(storedSession.ExpiresAt) {
		return session.Summary{}, &session.Error{
			Code: session.CodeSessionExpired,
		}
	}

	storedIntent, err := s.reader.LoadUploadIntent(ctx, sessionID, uploadIntentID)
	if err != nil {
		return session.Summary{}, err
	}

	storedDetails, err := s.reader.LoadPersonalDetails(ctx, sessionID)
	if err != nil {
		return session.Summary{}, err
	}

	if storedIntent.Status != "pending" {
		return session.Summary{}, errors.New("upload intent is not pending")
	}

	if !startedAt.Before(storedIntent.ExpiresAt) {
		return session.Summary{}, errors.New("upload intent expired")
	}

	if storedIntent.Kind != "identity_document" {
		return session.Summary{}, errors.New("upload intent is not an identity document")
	}

	objectMetadata, err := s.objectStorage.HeadObject(ctx, storedIntent.StorageKey)
	if err != nil {
		return session.Summary{}, err
	}

	const maximumIdentityDocumentSize int64 = 10 * 1024 * 1024

	if objectMetadata.SizeBytes <= 0 {
		return session.Summary{}, errors.New("identity document is empty")
	}

	if objectMetadata.SizeBytes > maximumIdentityDocumentSize {
		return session.Summary{}, errors.New("identity document is too large")
	}

	switch objectMetadata.ContentType {
	case "image/jpeg", "image/png", "application/pdf":
		// valid
	default:
		return session.Summary{},
			errors.New("unsupported identity document content type")
	}

	extraction, err := s.extractor.Extract(ctx, storedIntent.StorageKey)
	if err != nil {
		return session.Summary{}, err
	}

	eventMetadata, err := sessionevent.NewMetadata(sessionevent.OutcomeAccepted)
	if err != nil {
		return session.Summary{}, err
	}

	var summary session.Summary
	var outcomeError error
	err = s.transactions.WithinTransaction(ctx, func(tx Transaction) error {
		lockedSession, err := tx.LockSession(ctx, sessionID)
		if err != nil {
			return err
		}

		lockedIntent, err := tx.LockUploadIntent(ctx, sessionID, uploadIntentID)
		if err != nil {
			return err
		}

		lockedDetails, err := tx.LoadPersonalDetails(ctx, sessionID)
		if err != nil {
			return err
		}

		confirmedAt := s.clock.Now()
		if !s.tokens.Equal(s.tokens.Hash(rawToken), lockedSession.ResumeTokenHash) {
			return &session.Error{
				Code: session.CodeInvalidResumeToken,
			}
		}

		if !confirmedAt.Before(lockedSession.ExpiresAt) {
			return &session.Error{
				Code: session.CodeSessionExpired,
			}
		}

		if lockedIntent.Status != "pending" {
			return errors.New("upload intent is not pending")
		}

		if !confirmedAt.Before(lockedIntent.ExpiresAt) {
			return errors.New("upload intent expired")
		}

		if lockedIntent.Kind != "identity_document" {
			return errors.New("upload intent is not an identity document")
		}

		if lockedIntent.StorageKey != storedIntent.StorageKey {
			return errors.New("upload intent changed during confirmation")
		}

		if lockedDetails.IdentityNumber != storedDetails.IdentityNumber {
			return errors.New("personal details changed during confirmation")
		}

		lockedIdentityNumberMismatch := extraction.IdentityNumber != lockedDetails.IdentityNumber

		if lockedIdentityNumberMismatch {
			failureMetadata, err := sessionevent.NewMetadata(sessionevent.OutcomeLocalValidationFailed)
			if err != nil {
				return err
			}

			if err := tx.MarkUploadIntentValidationFailed(
				ctx,
				uploadIntentID,
				string(ReasonIdentityNumberMismatch),
				confirmedAt,
			); err != nil {
				return err
			}

			if err := tx.AppendEvent(ctx, session.AppendEventParams{
				SessionID:  lockedSession.ID,
				Type:       sessionevent.ConfirmIdentityDocument,
				Metadata:   failureMetadata,
				OccurredAt: confirmedAt,
			}); err != nil {
				return err
			}

			outcomeError = &Error{
				Code:   CodeLocalValidationFailed,
				Reason: ReasonIdentityNumberMismatch,
			}
			return nil
		}

		nextStatus, err := verificationsession.Transition(lockedSession.Status, sessionevent.ConfirmIdentityDocument)
		if err != nil {
			return err
		}

		if err := tx.ConfirmUploadIntent(ctx, uploadIntentID, confirmedAt); err != nil {
			return err
		}

		if err := tx.InsertVerificationArtifact(ctx,
			VerificationArtifact{
				ID:                    uuid.New(),
				VerificationSessionID: lockedSession.ID,
				UploadIntentID:        uploadIntentID,
				Kind:                  lockedIntent.Kind,
				StorageKey:            lockedIntent.StorageKey,
				ContentType:           objectMetadata.ContentType,
				SizeBytes:             objectMetadata.SizeBytes,
				ETag:                  objectMetadata.ETag,
				CreatedAt:             confirmedAt,
			},
		); err != nil {
			return err
		}

		if err := tx.UpdateSessionState(
			ctx,
			lockedSession.ID,
			lockedSession.Status,
			nextStatus,
			confirmedAt,
		); err != nil {
			return err
		}

		if err := tx.AppendEvent(ctx, session.AppendEventParams{
			SessionID:  lockedSession.ID,
			Type:       sessionevent.ConfirmIdentityDocument,
			Metadata:   eventMetadata,
			OccurredAt: confirmedAt,
		}); err != nil {
			return err
		}

		summary = session.Summary{
			ID:        lockedSession.ID,
			Status:    nextStatus,
			ExpiresAt: lockedSession.ExpiresAt,
		}
		return nil
	})
	if err != nil {
		return session.Summary{}, err
	}

	if outcomeError != nil {
		return session.Summary{}, outcomeError
	}

	return summary, nil
}

var ErrUploadIntentNotFound = errors.New("upload intent not found")
