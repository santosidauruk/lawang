package artifact

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/personaldetails"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang/internal/domain/verificationsession"
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

type ConfirmCoordinator interface {
	WithinConfirm(
		ctx context.Context,
		uploadIntentID uuid.UUID,
		operation func(Reader, Transactor) error,
	) error
}

type Service struct {
	reader             Reader
	objectStorage      ObjectStorage
	extractor          DocumentExtractor
	tokens             session.TokenIssuer
	clock              session.Clock
	confirmCoordinator ConfirmCoordinator
}

func NewService(
	reader Reader,
	confirmCoordinator ConfirmCoordinator,
	objectStorage ObjectStorage,
	extractor DocumentExtractor,
	tokens session.TokenIssuer,
	clock session.Clock,
) *Service {
	return &Service{
		reader:             reader,
		objectStorage:      objectStorage,
		extractor:          extractor,
		tokens:             tokens,
		clock:              clock,
		confirmCoordinator: confirmCoordinator,
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

	var summary session.Summary
	var outcomeError error
	err = s.confirmCoordinator.WithinConfirm(ctx, uploadIntentID, func(r Reader, t Transactor) error {
		rereadSession, err := r.LoadSession(ctx, sessionID)
		if errors.Is(err, session.ErrSessionNotFound) {
			return &session.Error{Code: session.CodeSessionNotFound}
		}
		if err != nil {
			return err
		}

		if !s.tokens.Equal(s.tokens.Hash(rawToken), rereadSession.ResumeTokenHash) {
			return &session.Error{
				Code: session.CodeInvalidResumeToken,
			}
		}

		startedAt := s.clock.Now()
		if !startedAt.Before(rereadSession.ExpiresAt) {
			return &session.Error{
				Code: session.CodeSessionExpired,
			}
		}

		rereadIntent, err := r.LoadUploadIntent(ctx, sessionID, uploadIntentID)
		if errors.Is(err, ErrUploadIntentNotFound) {
			return &Error{Code: CodeUploadIntentNotFound}
		}
		if err != nil {
			return err
		}

		if rereadIntent.Status == "confirmed" {
			summary = session.Summary{
				Status:    rereadSession.Status,
				ID:        rereadSession.ID,
				ExpiresAt: rereadSession.ExpiresAt,
			}
			return nil
		}

		if rereadIntent.Status == "validation_failed" {
			if rereadIntent.FailureCode == nil ||
				*rereadIntent.FailureCode != string(ReasonIdentityNumberMismatch) {
				return &Error{Code: CodeConfirmationStale}
			}
			outcomeError = &Error{
				Code:   CodeLocalValidationFailed,
				Reason: ReasonIdentityNumberMismatch,
			}
			return nil
		}

		if err := validatePendingUploadIntent(rereadIntent, startedAt); err != nil {
			return err
		}

		if rereadIntent.Kind == "identity_document" && rereadSession.Status != session.StatusPersonalDetailsSubmitted {
			return &Error{Code: CodeConfirmationStale}
		} else if rereadIntent.Kind == "biometric_capture" && rereadSession.Status != session.StatusIdentityDocumentUploaded {
			return &Error{Code: CodeConfirmationStale}
		}

		var rereadDetails personaldetails.PersonalDetails
		if rereadIntent.Kind == "identity_document" {
			rereadDetails, err = r.LoadPersonalDetails(ctx, sessionID)
			if errors.Is(err, ErrPersonalDetailsNotFound) {
				return &Error{Code: CodeConfirmationStale}
			}
			if err != nil {
				return err
			}
		}

		objectMetadata, err := s.objectStorage.HeadObject(ctx, rereadIntent.StorageKey)
		if err != nil {
			return &Error{Code: CodeObjectStorageFailed}
		}

		if err := validateObjectMetadata(rereadIntent.Kind, objectMetadata); err != nil {
			return err
		}

		var extraction DocumentExtraction
		if rereadIntent.Kind == "identity_document" {
			extraction, err = s.extractor.Extract(ctx, rereadIntent.StorageKey)
			if err != nil {
				return &Error{Code: CodeDocumentExtractionFailed}
			}
		}

		eventMetadata, err := sessionevent.NewMetadata(sessionevent.OutcomeAccepted)
		if err != nil {
			return err
		}

		err = t.WithinTransaction(ctx, func(tx Transaction) error {
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

			if lockedSession.Status != rereadSession.Status ||
				!lockedSession.ExpiresAt.Equal(rereadSession.ExpiresAt) {
				return &Error{Code: CodeConfirmationStale}
			}

			if lockedIntent.Kind != rereadIntent.Kind {
				return &Error{Code: CodeConfirmationStale}
			}

			if err := validatePendingUploadIntent(lockedIntent, confirmedAt); err != nil {
				return err
			}

			var lockedDetails personaldetails.PersonalDetails
			if lockedIntent.Kind == "identity_document" {
				lockedDetails, err = tx.LoadPersonalDetails(ctx, sessionID)
				if errors.Is(err, ErrPersonalDetailsNotFound) {
					return &Error{Code: CodeConfirmationStale}
				}
				if err != nil {
					return err
				}
			}

			if lockedIntent.StorageKey != rereadIntent.StorageKey {
				return &Error{Code: CodeConfirmationStale}
			}

			if !lockedIntent.ExpiresAt.Equal(rereadIntent.ExpiresAt) {
				return &Error{Code: CodeConfirmationStale}
			}

			if lockedIntent.Kind == "identity_document" {
				if lockedDetails.IdentityNumber != rereadDetails.IdentityNumber {
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
			}

			var event sessionevent.Type
			if lockedIntent.Kind == "identity_document" {
				event = sessionevent.ConfirmIdentityDocument
			} else {
				event = sessionevent.ConfirmBiometricCapture
			}
			nextStatus, err := verificationsession.Transition(lockedSession.Status, event)
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
				Type:       event,
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

		return err
	})

	if err != nil {
		return session.Summary{}, err
	}

	if outcomeError != nil {
		return session.Summary{}, outcomeError
	}

	return summary, nil
}

func validatePendingUploadIntent(intent UploadIntent, now time.Time) error {
	if intent.Status == "superseded" {
		return &Error{Code: CodeUploadIntentSuperseded}
	}
	if intent.Status != "pending" {
		return &Error{Code: CodeConfirmationStale}
	}
	if !now.Before(intent.ExpiresAt) {
		return &Error{Code: CodeUploadIntentExpired}
	}
	if intent.Kind != "identity_document" && intent.Kind != "biometric_capture" {
		return &Error{Code: CodeInvalidUploadIntentKind}
	}
	return nil
}

func validateObjectMetadata(kind string, metadata ObjectMetadata) error {
	maximumSize := int64(10 * 1024 * 1024)
	if kind == "biometric_capture" {
		maximumSize = 5 * 1024 * 1024
	}

	if metadata.SizeBytes <= 0 {
		return &Error{Code: CodeInvalidObjectMetadata, Reason: ReasonObjectEmpty}
	}
	if metadata.SizeBytes > maximumSize {
		return &Error{Code: CodeInvalidObjectMetadata, Reason: ReasonObjectTooLarge}
	}

	switch kind {
	case "identity_document":
		switch metadata.ContentType {
		case "image/jpeg", "image/png", "application/pdf":
			return nil
		}
	case "biometric_capture":
		switch metadata.ContentType {
		case "image/jpeg", "image/png":
			return nil
		}
	}

	return &Error{Code: CodeInvalidObjectMetadata, Reason: ReasonUnsupportedContentType}
}

var ErrUploadIntentNotFound = errors.New("upload intent not found")

var ErrPersonalDetailsNotFound = errors.New("personal details not found")
