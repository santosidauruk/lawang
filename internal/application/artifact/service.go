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

type Input struct {
	UploadIntentID string
}

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

const (
	CodeLocalValidationFailed    ErrorCode = "LOCAL_VALIDATION_FAILED"
	CodeUploadIntentExpired      ErrorCode = "UPLOAD_INTENT_EXPIRED"
	CodeUploadIntentSuperseded   ErrorCode = "UPLOAD_INTENT_SUPERSEDED"
	CodeUploadIntentNotFound     ErrorCode = "UPLOAD_INTENT_NOT_FOUND"
	CodeInvalidUploadIntentKind  ErrorCode = "INVALID_UPLOAD_INTENT_KIND"
	CodeInvalidObjectMetadata    ErrorCode = "INVALID_OBJECT_METADATA"
	CodeObjectStorageFailed      ErrorCode = "OBJECT_STORAGE_FAILED"
	CodeDocumentExtractionFailed ErrorCode = "DOCUMENT_EXTRACTION_FAILED"
	CodeConfirmationStale        ErrorCode = "CONFIRMATION_STALE"
	CodeUploadIntentStale        ErrorCode = "UPLOAD_INTENT_STALE"
)

type FailureReason string

const (
	ReasonIdentityNumberMismatch FailureReason = "identity_number_mismatch"
	ReasonObjectEmpty            FailureReason = "empty"
	ReasonObjectTooLarge         FailureReason = "too_large"
	ReasonUnsupportedContentType FailureReason = "unsupported_content_type"
)

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
	if errors.Is(err, session.ErrSessionNotFound) {
		return session.Summary{}, &session.Error{Code: session.CodeSessionNotFound}
	}
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
	if errors.Is(err, ErrUploadIntentNotFound) {
		return session.Summary{}, &Error{Code: CodeUploadIntentNotFound}
	}
	if err != nil {
		return session.Summary{}, err
	}

	if err := validatePendingIdentityIntent(storedIntent, startedAt); err != nil {
		return session.Summary{}, err
	}

	if storedSession.Status != session.StatusPersonalDetailsSubmitted {
		return session.Summary{}, &Error{Code: CodeConfirmationStale}
	}

	storedDetails, err := s.reader.LoadPersonalDetails(ctx, sessionID)
	if errors.Is(err, ErrPersonalDetailsNotFound) {
		return session.Summary{}, &Error{Code: CodeConfirmationStale}
	}
	if err != nil {
		return session.Summary{}, err
	}

	objectMetadata, err := s.objectStorage.HeadObject(ctx, storedIntent.StorageKey)
	if err != nil {
		return session.Summary{}, &Error{Code: CodeObjectStorageFailed}
	}

	const maximumIdentityDocumentSize int64 = 10 * 1024 * 1024

	if objectMetadata.SizeBytes <= 0 {
		return session.Summary{}, &Error{
			Code:   CodeInvalidObjectMetadata,
			Reason: ReasonObjectEmpty,
		}
	}

	if objectMetadata.SizeBytes > maximumIdentityDocumentSize {
		return session.Summary{}, &Error{
			Code:   CodeInvalidObjectMetadata,
			Reason: ReasonObjectTooLarge,
		}
	}

	switch objectMetadata.ContentType {
	case "image/jpeg", "image/png", "application/pdf":
		// valid
	default:
		return session.Summary{}, &Error{
			Code:   CodeInvalidObjectMetadata,
			Reason: ReasonUnsupportedContentType,
		}
	}

	extraction, err := s.extractor.Extract(ctx, storedIntent.StorageKey)
	if err != nil {
		return session.Summary{}, &Error{Code: CodeDocumentExtractionFailed}
	}

	eventMetadata, err := sessionevent.NewMetadata(sessionevent.OutcomeAccepted)
	if err != nil {
		return session.Summary{}, err
	}

	var summary session.Summary
	var outcomeError error
	err = s.transactions.WithinTransaction(ctx, func(tx Transaction) error {
		lockedSession, err := tx.LockSession(ctx, sessionID)
		if errors.Is(err, session.ErrSessionNotFound) {
			return &session.Error{Code: session.CodeSessionNotFound}
		}
		if err != nil {
			return err
		}

		lockedIntent, err := tx.LockUploadIntent(ctx, sessionID, uploadIntentID)
		if errors.Is(err, ErrUploadIntentNotFound) {
			return &Error{Code: CodeUploadIntentNotFound}
		}
		if err != nil {
			return err
		}

		lockedDetails, err := tx.LoadPersonalDetails(ctx, sessionID)
		if errors.Is(err, ErrPersonalDetailsNotFound) {
			return &Error{Code: CodeConfirmationStale}
		}
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

		if lockedSession.Status != storedSession.Status ||
			!lockedSession.ExpiresAt.Equal(storedSession.ExpiresAt) {
			return &Error{Code: CodeConfirmationStale}
		}

		if err := validatePendingIdentityIntent(lockedIntent, confirmedAt); err != nil {
			return err
		}

		if lockedIntent.StorageKey != storedIntent.StorageKey {
			return &Error{Code: CodeConfirmationStale}
		}

		if !lockedIntent.ExpiresAt.Equal(storedIntent.ExpiresAt) {
			return &Error{Code: CodeConfirmationStale}
		}

		if lockedDetails.IdentityNumber != storedDetails.IdentityNumber {
			return &Error{Code: CodeConfirmationStale}
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

func validatePendingIdentityIntent(intent UploadIntent, now time.Time) error {
	if intent.Status == "superseded" {
		return &Error{Code: CodeUploadIntentSuperseded}
	}
	if intent.Status != "pending" {
		return &Error{Code: CodeConfirmationStale}
	}
	if !now.Before(intent.ExpiresAt) {
		return &Error{Code: CodeUploadIntentExpired}
	}
	if intent.Kind != "identity_document" {
		return &Error{Code: CodeInvalidUploadIntentKind}
	}
	return nil
}

var ErrUploadIntentNotFound = errors.New("upload intent not found")

var ErrPersonalDetailsNotFound = errors.New("personal details not found")
